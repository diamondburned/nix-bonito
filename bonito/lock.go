package bonito

import (
	"context"
	"encoding/json"
	"io"

	"github.com/diamondburned/nix-bonito/bonito/internal/executil"
	"github.com/diamondburned/nix-bonito/bonito/internal/nixutil"
	"github.com/pkg/errors"
)

// LockFile describes a file containing hashes (or checksums) of the channels
// fetched.
type LockFile struct {
	// Channels maps channel URLs to its lock.
	Channels map[ChannelInput]ChannelLock `json:"channels"`
}

// ChannelLock describes the locking checksums for a single channel.
type ChannelLock struct {
	// URL is the resolved channel URL that's used for Nix. This URL must always
	// point to the same file, and the store hash guarantees that.
	URL string `json:"url"`
	// StoreHash is the hash part of the /nix/store output path of the channel.
	StoreHash nixutil.StoreHash `json:"store_hash"`
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

		url, err := input.Resolve(ctx)
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
