package nixutil

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/diamondburned/nix-bonito/bonito/internal/executil"
	"github.com/pkg/errors"
)

// ChannelSourcePath resolves the /nix/store path of the channel with the given
// name.
func ChannelSourcePath(ctx context.Context, channelName string) (string, error) {
	o := executil.OptsFromContext(ctx)

	var homeDir string
	var err error

	if o.Username == "" || executil.CurrentUserIs(o.Username) {
		homeDir, err = os.UserHomeDir()
		if err != nil {
			u, err := user.Current()
			if err != nil {
				return "", errors.Wrap(err, "cannot get current user")
			}
			homeDir = u.HomeDir
		}
	} else {
		u, err := user.Lookup(o.Username)
		if err != nil {
			return "", errors.Wrapf(err, "cannot lookup user %q", o.Username)
		}
		homeDir = u.HomeDir
	}

	defexpr := filepath.Join(homeDir, ".nix-defexpr", "channels", channelName)

	var out string
	// Use Exec so sudo works.
	err = executil.Exec(ctx, &out, "readlink", defexpr)
	return out, err
}

var storeDir atomic.Pointer[string]

// StoreDir retrieves the Nix store directory. It is usually /nix/store but can
// technically be different.
func StoreDir() (string, error) {
	if v := storeDir.Load(); v != nil {
		return *v, nil
	}

	// This should NEVER take more than 2 seconds.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	d, err := StoreDirUncached(ctx)
	if err != nil {
		return "", err
	}

	storeDir.CompareAndSwap(nil, &d)
	return d, nil
}

// StoreDirUncached retrieves the Nix store directory without using the
// cache.
func StoreDirUncached(ctx context.Context) (string, error) {
	var out string
	err := Eval(ctx, &out, "builtins.storeDir")
	return out, err
}
