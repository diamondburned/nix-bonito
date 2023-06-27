package gitutil

import (
	"context"
	"fmt"
	"strings"

	"github.com/diamondburned/nix-bonito/bonito/internal/executil"
)

// RefCommit fetches the latest commit of the reference in the given remote.
func RefCommit(ctx context.Context, remote, ref string) (string, error) {
	var out string

	err := executil.Exec(ctx, &out, "git", "ls-remote", remote, ref)
	if err != nil {
		return "", err
	}

	parts := strings.Fields(out)
	if len(parts) < 1 {
		return "", fmt.Errorf("ref %q not found", ref)
	}

	return parts[0], nil
}
