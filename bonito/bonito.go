package bonito

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/davecgh/go-spew/spew"
	"github.com/diamondburned/nix-bonito/bonito/internal/executil"
	"github.com/diamondburned/nix-bonito/bonito/internal/nixutil"
	"github.com/pelletier/go-toml/v2"
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

// LockFile describes a file containing hashes (or checksums) of the channels
// fetched.
type LockFile struct {
	// Channels maps channel URLs to its lock.
	Channels map[ChannelInput]ChannelLock `json:"channels"`
}

// NewLockFileFromReader creates a new LockFile containing data from the given
// reader parsed as JSON.
func NewLockFileFromReader(r io.Reader) (LockFile, error) {
	var l LockFile
	if err := json.NewDecoder(r).Decode(&l); err != nil {
		return l, err
	}
	return l, nil
}

// Eq returns true if l == old.
func (l LockFile) Eq(old LockFile) bool {
	if len(l.Channels) != len(old.Channels) {
		return false
	}

	for input, lock := range l.Channels {
		oldLock, ok := old.Channels[input]
		if !ok {
			return false
		}

		if oldLock != lock {
			return false
		}
	}

	return true
}

// WriteTo writes the LockFile as a JSON file to the given writer.
func (l LockFile) WriteTo(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(l)
}

// ChannelLock describes the locking checksums for a single channel.
type ChannelLock struct {
	// URL is the resolved channel URL that's used for Nix. This URL must always
	// point to the same file, and the store hash guarantees that.
	URL string `json:"url"`
	// StoreHash is the hash part of the /nix/store output path of the channel.
	StoreHash nixutil.StoreHash `json:"store_hash"`
}

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

// State encapsulates the current configuration and the locks of it. A State can
// be applied onto the current system.
type State struct {
	Config Config
	Lock   LockFile
}

// Apply applies the state onto the current system.
func (s *State) Apply(ctx context.Context, update bool) error {
	newLock, err := s.Config.CreateLockFile(ctx)
	if err != nil {
		return err
	}

	if !s.Lock.Eq(newLock) {
		if !update {
			return errors.New("locks need to be updated first")
		}
		s.Lock = newLock
	}

	spew.Dump(s)
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

type locksUpdater struct {
	ctx      context.Context
	locks    map[ChannelInput]ChannelLock
	storeDir string
}

func newLocksUpdater(ctx context.Context) (*locksUpdater, error) {
	storeDir, err := nixutil.StoreDir(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "cannot get store directory")
	}

	return &locksUpdater{
		ctx:      ctx,
		locks:    make(map[ChannelInput]ChannelLock, 10),
		storeDir: storeDir,
	}, nil
}

func (u *locksUpdater) add(username string, usercfg UserConfig) (err error) {
	ctx := executil.WithOpts(u.ctx, executil.Opts{
		Username: username,
		UseSudo:  usercfg.UseSudo,
	})

	channels := newChannelExecer(ctx, true)
	defer func() {
		if e := channels.rollbackAll(); e != nil {
			err = e
		}
	}()

	type addedCh struct {
		name string
		url  string
	}

	added := make(map[ChannelInput]addedCh, len(usercfg.Channels))
	names := make([]string, 0, len(usercfg.Channels))

	for name, input := range usercfg.Channels {
		if _, ok := u.locks[input]; ok {
			continue
		}

		url, err := input.Resolve()
		if err != nil {
			return errors.Wrapf(err, "cannot resolve %q", input)
		}

		n, err := channels.add(name, url)
		if err != nil {
			return errors.Wrapf(err, "cannot add channel %q", input)
		}

		names = append(names, n)
		added[input] = addedCh{
			name: n,
			url:  url,
		}
	}

	if err := channels.update(names...); err != nil {
		return errors.Wrapf(err, "cannot update channels %q", names)
	}

	for input, add := range added {
		src, err := nixutil.ChannelSourcePath(ctx, add.name)
		if err != nil {
			return errors.Wrapf(err, "cannot get source path for channel %q", input)
		}

		path, err := nixutil.ParseStorePath(u.storeDir, src)
		if err != nil {
			return errors.Wrapf(err, "invalid store path for channel %q", input)
		}

		u.locks[input] = ChannelLock{
			URL:       add.url,
			StoreHash: path.Hash,
		}
	}

	return nil
}
