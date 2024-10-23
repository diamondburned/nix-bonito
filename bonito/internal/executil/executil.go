package executil

import (
	"context"
	"fmt"
	"io"
	"log/slog"
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

	currentUser := CurrentUser()
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
		slog.Debug(
			"running command",
			"user", o.Username,
			"args", args(arg0, argv))
	} else {
		cmd.Stderr = &stderr
	}

	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			slog.Warn(
				"command failed with non-zero exit status",
				"user", o.Username,
				"args", args(arg0, argv),
				"status", cmd.ProcessState.ExitCode(),
				"stderr", stderr.String())
			return fmt.Errorf("%s failed", arg0)
		}

		slog.Warn(
			"command failed with error",
			"user", o.Username,
			"args", args(arg0, argv),
			"err", err)
		return err
	}

	return nil
}

func args(arg0 string, argv []string) []string {
	return append([]string{arg0}, argv...)
}

// CurrentUserIs returns true if the given thisUser's name matches the current
// username.
func CurrentUserIs(thisUser string) bool {
	return CurrentUser() == thisUser
}

// CurrentUser gets the current user's name. If it fails, then a panic is
// thrown.
func CurrentUser() string {
	// Trust $USER more. See https://golang.org/issue/38599.
	currentUser := os.Getenv("USER")
	if currentUser != "" {
		return currentUser
	}

	u, err := user.Current()
	if err != nil {
		panic(errors.Wrap(err, "cannot get current user"))
	}

	return u.Username
}
