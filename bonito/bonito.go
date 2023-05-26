package bonito

import (
	"context"
	"encoding/json"
	"fmt"
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
	if err := s.applyGlobal(ctx, false); err != nil {
		return errors.Wrap(err, "cannot apply global channels")
	}

	for username, usercfg := range s.Config.Users {
		if err := s.applyUser(ctx, username, usercfg); err != nil {
			return errors.Wrapf(err, "cannot apply for user %q", username)
		}
	}

	return nil
}

func (s *State) applyGlobal(ctx context.Context, update bool) error {
	channelInputs := s.Config.ChannelInputs()
	if !update {
		// If we're not updating, then we should remove channels that are
		// already locked. Otherwise, channels here will override the locked
		// ones.
		for input := range s.Lock.Channels {
			delete(channelInputs, input)
		}
	}

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

	locks, err := resolveChannelLocks(ctx, channelInputs)
	if err != nil {
		return errors.Wrap(err, "cannot resolve channel locks")
	}

	for input, lock := range locks {
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

// UpdateLocks updates the locks for the current configuration.
func (s *State) UpdateLocks(ctx context.Context) error {
	return s.applyGlobal(ctx, true)
}

// GenerateFlakesRegistry generates a flakes registry for the current
// configuration.
func (s *State) GenerateFlakesRegistry() (json.RawMessage, error) {
	registry, err := s.flakesRegistry()
	if err != nil {
		return nil, err
	}

	registryJSON, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return nil, errors.Wrap(err, "cannot marshal flakes registry")
	}

	return registryJSON, nil
}

// GenerateNixRegistry generates the nix.registry attributes as JSON for the
// current configuration.
func (s *State) GenerateNixRegistry() (json.RawMessage, error) {
	registry, err := s.flakesRegistry()
	if err != nil {
		return nil, err
	}

	registryJSON, err := json.MarshalIndent(registry.convertToNixRegistry(), "", "  ")
	if err != nil {
		return nil, errors.Wrap(err, "cannot marshal nix registry")
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
