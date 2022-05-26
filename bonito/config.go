package bonito

import (
	"context"
	"io"
	"sort"

	"github.com/pelletier/go-toml/v2"
	"github.com/pkg/errors"
)

// Config is the root structure of the host configuration file. It maps the
// usernames to their corresponding config.
type Config map[Username]UserConfig

// NewConfigFromReader creates a new Config by decoding the given reader as a
// TOML file.
func NewConfigFromReader(r io.Reader) (Config, error) {
	var cfg Config

	if err := toml.NewDecoder(r).Decode(&cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// CreateLockFile creates a new LockFile using the channels inside the current
// config.
func (cfg Config) CreateLockFile(ctx context.Context) (LockFile, error) {
	updater, err := newLocksUpdater(ctx)
	if err != nil {
		return LockFile{}, err
	}

	for username, usercfg := range cfg {
		if err := updater.add(username, usercfg); err != nil {
			return LockFile{}, errors.Wrapf(err, "cannot update locks for user %q", username)
		}
	}

	return LockFile{
		Channels: updater.locks,
	}, nil
}

// UserConfig is the structure of the user configuration.
type UserConfig struct {
	// UseSudo, if true, will use sudo if the current user is not the user that
	// this config belongs to. If it's false and the current user is not this
	// user, then an error will be thrown.
	UseSudo bool `toml:"use-sudo"`
	// OverrideChannels, if true, will cause all channels not defined in the
	// configuration file to be deleted.
	OverrideChannels bool `toml:"override-channels"`
	// Channels maps the channel names to its respective input strings.
	Channels map[string]ChannelInput `toml:"channels"`
	// Aliases maps a channel name to another channel name as aliases. The
	// aliasing channel will have the same channel input as the aliased.
	Aliases map[string]string `toml:"aliases"`
}

/// ChannelNames returns the sorted list of channel names inside the user
//config.
func (cfg UserConfig) ChannelNames() []string {
	names := make([]string, 0, len(cfg.Channels)+len(cfg.Aliases))
	for name := range cfg.Channels {
		names = append(names, name)
	}
	for name := range cfg.Aliases {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
