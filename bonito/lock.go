package bonito

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"path"
	"sync"

	"github.com/diamondburned/nix-bonito/bonito/internal/nixutil"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

// LockFile describes a file containing hashes (or checksums) of the channels
// fetched.
type LockFile struct {
	// Channels maps channel URLs to its lock.
	Channels map[ChannelInput]ChannelLock `json:"channels"`
}

// Update updates the lock file to have hashes from the given LockFile.
func (l *LockFile) Update(newer LockFile) {
	for channel, lock := range newer.Channels {
		l.Channels[channel] = lock
	}
}

// ChannelLock describes the locking checksums for a single channel.
type ChannelLock struct {
	// URL is the resolved channel URL that's used for Nix. This URL must always
	// point to the same file, and the store hash guarantees that.
	URL string `json:"url"`
	// StoreHash is the hash part of the /nix/store output path of the channel.
	StoreHash nixutil.StoreHash `json:"store_hash"`
}

// HashChanged returns true if the channel URL is the same, but the store hash
// is different.
func (l ChannelLock) HashChanged(newer ChannelLock) bool {
	return l.URL == newer.URL && l.StoreHash != newer.StoreHash
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

// String formats the LockFile as a pretty JSON string.
func (l LockFile) String() string {
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(b)
}

type locksUpdater struct {
	ctx   context.Context
	locks map[ChannelInput]ChannelLock
}

func newLocksUpdater(ctx context.Context) (*locksUpdater, error) {
	return &locksUpdater{
		ctx:   ctx,
		locks: make(map[ChannelInput]ChannelLock, 10),
	}, nil
}

func (u *locksUpdater) add(channelInputs map[string]ChannelInput) (err error) {
	channels := newChannelExecer(u.ctx, true)

	type addedCh struct {
		name string
		url  string
	}

	added := make(map[ChannelInput]addedCh, len(channelInputs))
	names := make([]string, 0, len(channelInputs))

	for name, input := range channelInputs {
		if _, ok := u.locks[input]; ok {
			continue
		}

		url, err := input.Resolve(u.ctx)
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
		src, err := nixutil.ChannelSourcePath(u.ctx, add.name)
		if err != nil {
			return errors.Wrapf(err, "cannot get source path for channel %q", input)
		}

		path, err := nixutil.ParseStorePath(src)
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

func resolveInputs(ctx context.Context, inputs map[ChannelInput]struct{}) (map[ChannelInput]string, error) {
	urls := make(map[ChannelInput]string, len(inputs))

	var mu sync.Mutex
	errg, ctx := errgroup.WithContext(ctx)

	for input := range inputs {
		input := input

		errg.Go(func() error {
			url, err := input.Resolve(ctx)
			if err != nil {
				return errors.Wrapf(err, "cannot resolve %q", input)
			}

			mu.Lock()
			urls[input] = url
			mu.Unlock()

			return nil
		})
	}

	if err := errg.Wait(); err != nil {
		return nil, err
	}

	return urls, nil
}

func resolveChannelLocks(ctx context.Context, inputURLs map[ChannelInput]string) (map[ChannelInput]ChannelLock, error) {
	if len(inputURLs) == 0 {
		return nil, nil
	}

	locks := make(map[ChannelInput]ChannelLock, len(inputURLs))

	channels := newChannelExecer(ctx, true)
	channelNames := make([]string, 0, len(inputURLs))
	channelInputs := make(map[string]ChannelInput, len(inputURLs))

	for input, url := range inputURLs {
		tempName := shortHash(url) + "-" + path.Base(string(input.URL))

		chName, err := channels.add(tempName, url)
		if err != nil {
			return nil, errors.Wrap(err, "cannot add channel")
		}

		channelInputs[chName] = input
		channelNames = append(channelNames, chName)
	}

	if err := channels.update(channelNames...); err != nil {
		return nil, errors.Wrap(err, "cannot update channels")
	}

	for name, input := range channelInputs {
		src, err := nixutil.ChannelSourcePath(ctx, name)
		if err != nil {
			return nil, errors.Wrap(err, "cannot get source path for channel")
		}

		path, err := nixutil.ParseStorePath(src)
		if err != nil {
			return nil, errors.Wrap(err, "invalid store path for channel")
		}

		locks[input] = ChannelLock{
			URL:       inputURLs[input],
			StoreHash: path.Hash,
		}
	}

	return locks, nil
}

func removeTmpChannels(ctx context.Context) error {
	channels := newChannelExecer(ctx, true)
	return channels.removeAll()
}

func shortHash(str string) string {
	h := sha256.New()
	h.Write([]byte(str))
	return base64.URLEncoding.EncodeToString(h.Sum(nil))[:16]
}
