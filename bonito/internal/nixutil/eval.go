package nixutil

import (
	"context"
	"encoding/json"

	"github.com/diamondburned/nix-bonito/bonito/internal/executil"
	"github.com/pkg/errors"
)

// Eval uses nix-instantiate to evaluate a Nix expression.
func Eval(ctx context.Context, out interface{}, expr string) error {
	var stdout string

	err := executil.Exec(ctx, &stdout, "nix-instantiate", "--json", "--eval", "-E", expr)
	if err != nil {
		return err
	}

	if err := json.Unmarshal([]byte(stdout), out); err != nil {
		return errors.Wrap(err, "cannot decode JSON output")
	}

	return nil
}
