package gitutil

import (
	"context"
	"encoding/hex"
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
		// ref might be a commit hash. Check if it's a valid hex string.
		if len(ref) <= 40 && isValidCommitHash(ref) {
			return ref, nil
		}
		return "", fmt.Errorf("ref %q not found", ref)
	}

	return parts[0], nil
}

func isValidCommitHash(hash string) bool {
	// Round down the hex string so that it's a multiple of 2.
	l := len(hash) - len(hash)%2
	_, err := hex.DecodeString(hash[:l])
	return err == nil
}
