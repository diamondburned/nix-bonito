package bonito

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/diamondburned/nix-bonito/bonito/internal/executil"
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
func (s *State) Apply(ctx context.Context, update bool) error {
	if update {
		if err := s.UpdateLocks(ctx); err != nil {
			return err
		}
	}

	for username, usercfg := range s.Config {
		if err := s.apply(ctx, username, usercfg); err != nil {
			return errors.Wrapf(err, "cannot apply for user %q", username)
		}
	}

	return nil
}

func (s *State) apply(ctx context.Context, username string, usercfg UserConfig) error {
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

	channelInputs := make(map[string]ChannelInput, len(usercfg.Channels)+len(usercfg.Aliases))
	for name, channel := range usercfg.Channels {
		channelInputs[name] = channel
	}

	for alias, name := range usercfg.Aliases {
		in, ok := channelInputs[name]
		if !ok {
			return fmt.Errorf("alias %q references unknown channel %q", alias, name)
		}
		channelInputs[alias] = in
	}

	// If this map is nil, then we're expecting the next loop to populate
	// everything.
	if s.Lock.Channels == nil {
		s.Lock.Channels = make(map[ChannelInput]ChannelLock, len(channelInputs))
	}

	// Resolve channels with missing locks before moving on to removing the old
	// channels and adding new ones.
	for _, channel := range channelInputs {
		lock, ok := s.Lock.Channels[channel]
		if ok {
			continue
		}

		lock, err = resolveChannelLock(ctx, username, usercfg, channel)
		if err != nil {
			rollback()
			return errors.Wrapf(err, "channel %q cannot resolve lock", channel)
		}

		s.Lock.Channels[channel] = lock
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

	for name, channel := range channelInputs {
		lock, ok := s.Lock.Channels[channel]
		if !ok {
		}

		_, err := channels.add(name, lock.URL)
		if err != nil {
			rollback()
			return errors.Wrapf(err, "cannot add channel %q", name)
		}

		// TODO: validate hash
	}

	if err := channels.update(usercfg.ChannelNames()...); err != nil {
		rollback()
		return errors.Wrap(err, "cannot update")
	}

	return nil
}

// UpdateLocks updates the locks for the current configuration.
func (s *State) UpdateLocks(ctx context.Context) error {
	newLock, err := s.Config.CreateLockFile(ctx)
	if err != nil {
		return err
	}

	s.Lock = newLock
	return nil
}
