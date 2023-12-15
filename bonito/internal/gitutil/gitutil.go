package gitutil

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/diamondburned/nix-bonito/bonito/internal/executil"
)

// RefCommit fetches the latest commit of the reference in the given remote.
// If the reference is a commit hash, it will be returned as is, otherwise it
// will try to fetch a latest reference matching the given ref. If the ref ends
// with a *, it will be treated as a glob, and the latest reference matching
// the glob will be returned.
func RefCommit(ctx context.Context, remote, ref string) (string, error) {
	if len(ref) == 40 && isValidCommitHash(ref) {
		// Immediately consider it a commit hash.
		// A branch name of 40 characters of hex is very unlikely.
		// If it happens, the user should use refs/heads/branch instead.
		return ref, nil
	}

	args := []string{
		"git", "-c", "versionsort.suffix=-",
		"ls-remote", "--sort=v:refname",
		remote,
	}

	if strings.HasSuffix(ref, "*") {
		// If ref isn't prefixed with refs/*, we assume it's a branch. This is
		// done to prevent confusion with refs/remotes.
		if !strings.HasPrefix(ref, "refs/") {
			ref = "refs/heads/" + ref
		}
		// Require an exact match.
		args = append(args, ref)
	}

	var out string
	err := executil.Exec(ctx, &out, args[0], args[1:]...)
	if err != nil {
		return "", err
	}

	refs := splitLsRemote(out)

	if strings.HasSuffix(ref, "*") {
		// Filter lines that match our glob, then take the last one, which is
		// the latest one.
		filtered := refs[:0]
		matchRef := ref[:len(ref)-1]
		for _, ref := range refs {
			if strings.HasPrefix(ref.ref, matchRef) {
				filtered = append(filtered, ref)
			}
		}
		refs = filtered
	}

	if len(refs) == 0 {
		// This could still be a commit hash.
		if isValidCommitHash(ref) {
			return ref, nil
		}
		return "", fmt.Errorf("ref %q not found", ref)
	}

	return refs[len(refs)-1].commit, nil
}

type gitReference struct {
	commit string
	ref    string
}

func splitLsRemote(out string) []gitReference {
	lines := strings.Split(out, "\n")
	refs := make([]gitReference, 0, len(lines))

	for _, line := range lines {
		commit, ref, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if strings.HasPrefix(ref, "refs/tags/") && !strings.HasSuffix(ref, "^{}") {
			// Skip the tags that aren't dereferenced.
			// See https://stackoverflow.com/q/15472107.
			continue
		}
		refs = append(refs, gitReference{
			commit: commit,
			ref:    ref,
		})
	}

	return refs
}

func isValidCommitHash(hash string) bool {
	if len(hash) < 4 || len(hash) > 40 {
		// Require at least 4 characters.
		// Require at most 40 characters.
		return false
	}
	// Round down the hex string so that it's a multiple of 2.
	l := len(hash) - len(hash)%2
	_, err := hex.DecodeString(hash[:l])
	return err == nil
}
