package nixutil

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nix-community/go-nix/pkg/nixbase32"
	"github.com/pkg/errors"
)

// StoreHash is the string hash part of the /nix/store string.
type StoreHash string

// Decode decodes the store hash to its raw digest.
func (h StoreHash) Decode() ([]byte, error) {
	return nixbase32.DecodeString(string(h))
}

// StorePath is the checksum string extracted from the /nix/store path.
type StorePath struct {
	Root string
	Name string
	Hash StoreHash
}

// ParseStorePath parses the given path within the root directory as a Nix
// StorePath. If root is empty, then /nix/store is implied.
func ParseStorePath(root, path string) (StorePath, error) {
	if root == "" {
		root = "/nix/store"
	}

	var storePath StorePath

	name, err := filepath.Rel(root, path)
	if err != nil {
		return storePath, errors.Wrap(err, "invalid path")
	}

	// If we're thrown a /nix/store/X/Y, then extract only X.
	if names := strings.Split(name, string(filepath.Separator)); len(names) > 1 {
		name = names[0]
	}

	parts := strings.SplitN(name, "-", 2)
	if len(parts) != 2 {
		return storePath, fmt.Errorf("invalid nix-store name %q", name)
	}

	storePath.Root = root
	storePath.Name = parts[1]
	storePath.Hash = StoreHash(parts[0])

	if _, err := storePath.Hash.Decode(); err != nil {
		return storePath, errors.Wrap(err, "invalid nixbase32 hash")
	}

	return storePath, nil
}

// String formats the path into /nix/store/X.
func (p StorePath) String() string {
	return filepath.Join(p.Root, string(p.Hash)+"-"+p.Name)
}
