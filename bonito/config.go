package bonito

import (
	"io"

	"github.com/pelletier/go-toml/v2"
	"github.com/pkg/errors"
)

// Config is the root structure of the host configuration file. It maps the
// usernames to their corresponding config.
type Config struct {
	// Global is the global channels.
	Global struct {
		// PreferredUser is the preferred user to use for nix-channel invocations.
		// If this is empty, then it will be picked automatically.
		PreferredUser string `toml:"preferred_user,omitempty"`
		ChannelRegistry
	} `toml:"global"`

	// Flakes is the flakes channels.
	Flakes struct {
		Enable bool   `toml:"enable"`
		Output string `toml:"output"` // ("nix") or "flakes"
		ChannelRegistry
	} `toml:"flakes"`

	// Users maps the usernames to their respective UserConfig.
	Users map[Username]UserConfig `toml:"users"`
}

// NewConfigFromReader creates a new Config by decoding the given reader as a
// TOML file.
func NewConfigFromReader(r io.Reader) (Config, error) {
	var cfg Config
	cfg.Flakes.Output = "nix"
	err := toml.NewDecoder(r).Decode(&cfg)
	return cfg, err
}

// ChannelInputs returns all channel inputs within the current config.
func (cfg Config) ChannelInputs() map[ChannelInput]struct{} {
	inputsLen := len(cfg.Global.Channels) + len(cfg.Flakes.Channels)
	for _, usercfg := range cfg.Users {
		inputsLen += len(usercfg.Channels)
	}

	inputsSet := make(map[ChannelInput]struct{}, inputsLen)
	for _, input := range cfg.Global.Channels {
		inputsSet[input] = struct{}{}
	}

	for _, input := range cfg.Flakes.Channels {
		inputsSet[input] = struct{}{}
	}

	for _, usercfg := range cfg.Users {
		for _, input := range usercfg.Channels {
			inputsSet[input] = struct{}{}
		}
	}

	return inputsSet
}

// FilterChannels returns a new Config with only the channels that are
// present in the given names.
func (cfg Config) FilterChannels(names []string) Config {
	cfg.Global.ChannelRegistry = cfg.Global.ChannelRegistry.FilterChannels(names)
	cfg.Flakes.ChannelRegistry = cfg.Flakes.ChannelRegistry.FilterChannels(names)

	users := cfg.Users
	cfg.Users = make(map[Username]UserConfig, len(users))
	for username, usercfg := range users {
		usercfg.ChannelRegistry = usercfg.ChannelRegistry.FilterChannels(names)
		cfg.Users[username] = usercfg
	}

	return cfg
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
	ChannelRegistry
}

// ChannelRegistry is a common structure holding configured channels and its
// aliases.
type ChannelRegistry struct {
	// Channels maps the channel names to its respective input strings.
	Channels map[string]ChannelInput `toml:"channels"`
	// Aliases maps a channel name to another channel name as aliases. The
	// aliasing channel will have the same channel input as the aliased.
	Aliases map[string]string `toml:"aliases"`
}

// CombineChannelRegistries combines the given ChannelRegistries into a single
// channel input map. It also resolves the aliases. Channels defined later in
// the list will override the ones defined earlier.
func CombineChannelRegistries(registries []ChannelRegistry) (map[string]ChannelInput, error) {
	channelInputs := make(map[string]ChannelInput)
	for _, registry := range registries {
		for name, input := range registry.Channels {
			channelInputs[name] = input
		}

		for name, alias := range registry.Aliases {
			input, ok := channelInputs[alias]
			if !ok {
				return nil, errors.Errorf("unknown channel alias %q", alias)
			}
			channelInputs[name] = input
		}
	}

	return channelInputs, nil
}

// FilterChannels returns a new ChannelRegistry with only the channels that are
// present in the given names.
func (r ChannelRegistry) FilterChannels(names []string) ChannelRegistry {
	filteredChannels := make(map[string]ChannelInput, len(r.Channels))
	for _, name := range names {
		if ch, ok := r.Channels[name]; ok {
			filteredChannels[name] = ch
			continue
		}
	}

	filteredAliases := make(map[string]string, len(r.Aliases))
	for _, name := range names {
		if alias, ok := r.Aliases[name]; ok {
			filteredAliases[name] = alias
			// Also include the channel that the alias points to.
			if _, ok := filteredChannels[alias]; !ok {
				filteredChannels[alias] = r.Channels[alias]
			}
		}
	}

	newer := r
	newer.Channels = filteredChannels
	newer.Aliases = filteredAliases
	return newer
}
