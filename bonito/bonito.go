package bonito

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/diamondburned/nix-bonito/bonito/internal/executil"
	"github.com/diamondburned/nix-bonito/bonito/internal/nixutil"
	"github.com/pkg/errors"
)

// WithVerbose enables verbose mode for all invokations that use the returned
// context.
func WithVerbose(ctx context.Context) context.Context {
	ctx = executil.WithVerbose(ctx)
	return ctx
}

// ChannelURL is the URL to the source of a channel.
type ChannelURL string

// Parse parses the channel URL string. An invalid URL will cause an error.
func (u ChannelURL) Parse() (*url.URL, error) {
	if strings.Contains(string(u), " ") {
		return nil, fmt.Errorf("url %q contains invalid space", u)
	}

	return url.Parse(string(u))
}

// Validate validates the ChannelURL string.
func (u ChannelURL) Validate() error {
	if strings.Contains(string(u), " ") {
		return fmt.Errorf("url %q contains invalid space", u)
	}

	// TODO: VCS scheme validation
	url, err := u.Parse()
	if err != nil {
		return err
	}

	if _, ok := ChannelResolvers[url.Scheme]; !ok {
		return fmt.Errorf("unknown scheme %q", url.Scheme)
	}

	return nil
}

// Username is the name of the user in a local machine.
// It is a type alias for documentation purposes.
type Username = string

// State encapsulates the current configuration and the locks of it. A State can
// be applied onto the current system.
type State struct {
	Config Config
	Lock   LockFile
}

// Apply applies the state onto the current system.
func (s *State) Apply(ctx context.Context) error {
	if err := s.applyGlobal(ctx, noUpdate); err != nil {
		return errors.Wrap(err, "cannot apply global channels")
	}

	for username, usercfg := range s.Config.Users {
		if err := s.applyUser(ctx, username, usercfg); err != nil {
			return errors.Wrapf(err, "cannot apply for user %q", username)
		}
	}

	return nil
}

type updateFlag int

const (
	noUpdate updateFlag = iota
	updateLocks
	updateInputs
)

func (f updateFlag) is(other updateFlag) bool { return f >= other }

func (s *State) applyGlobal(ctx context.Context, update updateFlag) error {
	channelInputs := s.Config.ChannelInputs()

	// If this map is nil, then we're expecting the next loop to populate
	// everything.
	if s.Lock.Channels == nil {
		s.Lock.Channels = make(map[ChannelInput]ChannelLock, len(channelInputs))
	}

	user, err := s.preferredUser()
	if err != nil {
		return errors.Wrap(err, "cannot get preferred user")
	}

	ctx = executil.WithOpts(ctx, executil.Opts{
		Username: user.Username,
		UseSudo:  user.UseSudo,
	})

	// Remove all existing temporary channels. These aren't used anywhere else,
	// so we can just remove them before we add the new ones.
	if err := removeTmpChannels(ctx); err != nil {
		return errors.Wrap(err, "cannot remove existing temporary channels")
	}

	var inputURLs map[ChannelInput]string
	// Fully resolve the inputs if we're updating. Otherwise, we'll just use
	// the locked ones.
	if update.is(updateInputs) {
		inputURLs, err = resolveInputs(ctx, channelInputs)
		if err != nil {
			return errors.Wrap(err, "cannot resolve input URLs")
		}
	} else {
		inputURLs = make(map[ChannelInput]string, len(channelInputs))

		// Ensure that channelInputs doesn't have any missing locks.
		// If it does, we'll need to update them.
		missingInputs := make(map[ChannelInput]struct{}, len(channelInputs))
		for input := range channelInputs {
			lock, ok := inputURLs[input]
			if ok {
				inputURLs[input] = lock
			} else {
				missingInputs[input] = struct{}{}
			}
		}

		newInputURLs, err := resolveInputs(ctx, missingInputs)
		if err != nil {
			return errors.Wrap(err, "cannot resolve missing input URLs")
		}

		for input, url := range newInputURLs {
			inputURLs[input] = url
		}
	}

	locks, err := resolveChannelLocks(ctx, inputURLs)
	if err != nil {
		return errors.Wrap(err, "cannot resolve channel locks")
	}

	for input, lock := range locks {
		// Assert that the hashes are the same after resolving the channel
		// locks.
		if oldLock, ok := s.Lock.Channels[input]; ok && oldLock.HashChanged(lock) {
			if !update.is(updateLocks) {
				return fmt.Errorf("channel %q has a different store hash (try --update-locks)", input)
			}
			log.Println("channel", input, "has a different store hash, updating...")
		}
		s.Lock.Channels[input] = lock
	}

	return nil
}

func (s *State) applyUser(ctx context.Context, username string, usercfg UserConfig) error {
	ctx = executil.WithOpts(ctx, executil.Opts{
		Username: username,
		UseSudo:  usercfg.UseSudo,
	})

	channels := newChannelExecer(ctx, false)

	oldList, err := channels.list()
	if err != nil {
		return errors.Wrap(err, "cannot get current channels list")
	}

	rollback := func() {
		// Undo all our channels.
		for name := range usercfg.Channels {
			channels.remove(name)
		}
		// Re-add the old ones.
		for name, url := range oldList {
			channels.add(name, url)
		}
	}

	if usercfg.OverrideChannels {
		// Remove old channels first. Nix might add some extra channels, and we
		// want to keep those.
		for name := range oldList {
			_, ok := usercfg.Channels[name]
			if ok {
				continue
			}

			if err := channels.remove(name); err != nil {
				rollback()
				return errors.Wrap(err, "cannot remove channel %q for overriding")
			}
		}
	}

	channelInputs, err := CombineChannelRegistries([]ChannelRegistry{
		s.Config.Global.ChannelRegistry,
		usercfg.ChannelRegistry,
	})
	if err != nil {
		return errors.Wrapf(err, "cannot get channels for user %q", username)
	}

	names := make([]string, 0, len(channelInputs))

	for name, input := range channelInputs {
		lock, ok := s.Lock.Channels[input]
		if !ok {
			return fmt.Errorf("channel %q has no lock", name)
		}

		_, err := channels.add(name, lock.URL)
		if err != nil {
			rollback()
			return errors.Wrapf(err, "cannot add channel %q", name)
		}

		names = append(names, name)
		// TODO: validate hash
	}

	if usercfg.OverrideChannels {
		err = channels.update()
	} else {
		err = channels.update(names...)
	}
	if err != nil {
		rollback()
		return errors.Wrap(err, "cannot update")
	}

	return nil
}

type preferredUser struct {
	Username string
	UseSudo  bool
}

// preferredUser returns the username of the user that should be used for
// running Nix commands. It returns root whenever possible, otherwise it returns
// the current user.
func (s State) preferredUser() (preferredUser, error) {
	var z preferredUser

	if s.Config.Global.PreferredUser != "" {
		usercfg, ok := s.Config.Users[s.Config.Global.PreferredUser]
		if !ok {
			return z, fmt.Errorf("preferred user %q does not exist", s.Config.Global.PreferredUser)
		}

		return preferredUser{s.Config.Global.PreferredUser, usercfg.UseSudo}, nil
	}

	user, _ := user.Current()

	// Prioritize root.
	if user != nil && user.Username == "root" {
		return preferredUser{"root", false}, nil
	}
	if u, ok := s.Config.Users["root"]; ok && u.UseSudo {
		return preferredUser{"root", true}, nil
	}

	// Otherwise, use the current user if it's in the list.
	if user != nil {
		if _, ok := s.Config.Users[user.Username]; ok {
			return preferredUser{user.Username, false}, nil
		}
	}

	return z, errors.New("no suitable user, perhaps run as root or allow use-sudo for root")
}

// UpdateLocks updates just the locks for the current configuration.
func (s *State) UpdateLocks(ctx context.Context) error {
	return s.applyGlobal(ctx, updateLocks)
}

// Update updates the inputs and locks for the current configuration. It is not
// to be confused with UpdateLocks which only updates the lock hashes,
// UpdateInputs will also update the input URLs to the latest versions.
func (s *State) Update(ctx context.Context) error {
	return s.applyGlobal(ctx, updateInputs)
}

// GenerateNixRegistry generates the nix.registry attributes as JSON for the
// current configuration.
func (s *State) GenerateNixRegistry() (json.RawMessage, error) {
	registry, err := s.flakesRegistry()
	if err != nil {
		return nil, err
	}

	var v any
	switch s.Config.Flakes.Output {
	case "nix":
		v = registry.convertToNixRegistry()
	case "flakes":
		v = registry
	default:
		return nil, fmt.Errorf("unknown output format %q", s.Config.Flakes.Output)
	}

	registryJSON, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, errors.Wrap(err, "cannot marshal registry JSON file")
	}

	return registryJSON, nil
}

func (s *State) flakesRegistry() (*flakesRegistryV2, error) {
	channelInputs, err := CombineChannelRegistries([]ChannelRegistry{
		s.Config.Global.ChannelRegistry,
		s.Config.Flakes.ChannelRegistry,
	})
	if err != nil {
		return nil, errors.Wrap(err, "cannot combine channels")
	}

	var registry flakesRegistryV2

	for name, input := range channelInputs {
		lock, ok := s.Lock.Channels[input]
		if !ok {
			return nil, fmt.Errorf("channel %q has no lock, perhaps run bonito first", name)
		}

		storePath, err := nixutil.LocatePath(lock.StoreHash)
		if err != nil {
			return nil, errors.Wrapf(err, "channel %q cannot find store hash %q", name, lock.StoreHash)
		}

		// flakeRoot hard-codes the structure of the directory that should have
		// a flake.nix file. If this changes, then this code will break.
		flakeRoot := filepath.Join(storePath.String(), storePath.Name)
		flakePath := filepath.Join(flakeRoot, "flake.nix")

		// Double-check that the flake path exists.
		if _, err := os.Stat(flakePath); err != nil {
			return nil, errors.Wrapf(err, "channel %q has no flake.nix file, try removing it", name)
		}

		registry.Flakes = append(registry.Flakes, flakesRegistryV2Flake{
			From: flakesRegistryV2FromIndirect{ID: name},
			To:   flakesRegistryV2ToPath{Path: flakeRoot},
		})
	}

	return &registry, nil
}
