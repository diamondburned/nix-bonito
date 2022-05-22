package nixutil

import (
	"context"
	"os/user"
	"path/filepath"

	"github.com/diamondburned/nix-bonito/bonito/internal/executil"
	"github.com/pkg/errors"
)

// ChannelSourcePath resolves the /nix/store path of the channel with the given
// name.
func ChannelSourcePath(ctx context.Context, channelName string) (string, error) {
	o := executil.OptsFromContext(ctx)

	var u *user.User
	var err error

	if o.Username != "" {
		u, err = user.Lookup(o.Username)
		if err != nil {
			return "", errors.Wrapf(err, "cannot lookup user %q", o.Username)
		}
	} else {
		u, err = user.Current()
		if err != nil {
			return "", errors.Wrap(err, "cannot get current user")
		}
	}

	defexpr := filepath.Join(u.HomeDir, ".nix-defexpr", "channels", channelName)

	var out string
	// Use Exec so sudo works.
	err = executil.Exec(ctx, &out, "readlink", defexpr)
	return out, err
}

// StoreDir retrieves the Nix store directory. It is usually /nix/store but can
// technically be different.
func StoreDir(ctx context.Context) (string, error) {
	var out string
	err := Eval(ctx, &out, "builtins.storeDir")
	return out, err
}
