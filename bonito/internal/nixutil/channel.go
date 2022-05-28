package nixutil

import (
	"context"
	"os"
	"os/user"
	"path/filepath"

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

// StoreDir retrieves the Nix store directory. It is usually /nix/store but can
// technically be different.
func StoreDir(ctx context.Context) (string, error) {
	var out string
	err := Eval(ctx, &out, "builtins.storeDir")
	return out, err
}
