package executil

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"strings"

	"github.com/pkg/errors"
)

type ctxKey uint8

const (
	_ ctxKey = iota
	optsCtxKey
	verboseCtxKey
)

func isVerbose(ctx context.Context) bool {
	verbose, _ := ctx.Value(verboseCtxKey).(bool)
	return verbose
}

// WithVerbose puts all Exec calls using the returned context into verbose mode,
// meaning stderr will be used.
func WithVerbose(ctx context.Context) context.Context {
	return context.WithValue(ctx, verboseCtxKey, true)
}

// Opts contains the options to be used during evaluation.
type Opts struct {
	UseSudo  bool
	Username string
}

// WithOpts inserts the given Opts into the context to be used. It overrides the
// parent Opts, if any.
func WithOpts(ctx context.Context, opts Opts) context.Context {
	return context.WithValue(ctx, optsCtxKey, opts)
}

// OptsFromContext returns the Opts from the given context.
func OptsFromContext(ctx context.Context) Opts {
	o, _ := ctx.Value(optsCtxKey).(Opts)
	return o
}

// Exec executes a command.
func Exec(ctx context.Context, out *string, arg0 string, argv ...string) error {
	o := OptsFromContext(ctx)

	u, err := user.Current()
	if err != nil {
		return errors.Wrap(err, "cannot get current user")
	}

	if o.Username == "" {
		o.Username = u.Username
	}

	var cmd *exec.Cmd
	if u.Username == o.Username {
		cmd = exec.CommandContext(ctx, arg0, argv...)
	} else {
		if !o.UseSudo {
			return fmt.Errorf("cannot run as user %q", o.Username)
		}

		sudoArgs := []string{"-u", o.Username, arg0}
		sudoArgs = append(sudoArgs, argv...)

		cmd = exec.CommandContext(ctx, "sudo", sudoArgs...)
		cmd.Stdin = os.Stdin // for the prompt
	}

	if out != nil {
		var outbuf strings.Builder
		cmd.Stdout = &outbuf
		defer func() { *out = outbuf.String() }()
	}

	if isVerbose(ctx) {
		cmd.Stderr = os.Stderr
		log.Printf("user %q: running command %q", o.Username, append([]string{arg0}, argv...))
	}

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("%s: %s", arg0, exitErr.Stderr)
		}
		return err
	}

	return nil
}
