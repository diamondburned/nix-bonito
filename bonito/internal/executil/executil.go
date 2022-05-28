package executil

import (
	"context"
	"fmt"
	"io"
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

	// Trust $USER more. See https://golang.org/issue/38599.
	currentUser := os.Getenv("USER")
	if currentUser == "" {
		u, err := user.Current()
		if err != nil {
			return errors.Wrap(err, "cannot get current user")
		}
		currentUser = u.Username
	}

	if o.Username == "" {
		o.Username = currentUser
	}

	var cmd *exec.Cmd
	if o.Username == currentUser {
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

	var stderr strings.Builder
	if isVerbose(ctx) {
		cmd.Stderr = io.MultiWriter(&stderr, os.Stderr)
		log.Printf("user %q: running command %q", o.Username, append([]string{arg0}, argv...))
	} else {
		cmd.Stderr = &stderr
	}

	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("%s exited status %d: %s", arg0, cmd.ProcessState.ExitCode(), &stderr)
		}
		return err
	}

	return nil
}
