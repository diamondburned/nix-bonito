package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/diamondburned/nix-bonito/bonito"
	"github.com/gofrs/flock"
	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
	"github.com/pkg/errors"
	"github.com/urfave/cli/v3"
)

const flockName = "bonito.lock"

var (
	flockPath = filepath.Join(os.TempDir(), "bonito.lock")
	flockLock = flock.New(flockPath)
)

func main() {
	var defaultConfigFile string
	if hostname, err := os.Hostname(); err == nil {
		defaultConfigFile = hostname + ".toml"
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	cmd := cli.Command{
		Name:      "bonito",
		Usage:     "Declarative Nix channel manager",
		Before:    cmdInit,
		After:     cmdFinish,
		Action:    cmdRun,
		ArgsUsage: "[channels...]",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "update",
				Aliases: []string{"u"},
				Usage:   "update inputs and locks",
			},
			&cli.BoolFlag{
				Name:  "update-locks",
				Usage: "update locks only",
			},
			&cli.BoolFlag{
				Name:    "verbose",
				Aliases: []string{"v"},
				Usage:   "verbose mode",
			},
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "path to the config file",
				Value:   defaultConfigFile,
			},
			&cli.StringFlag{
				Name:  "lock-file",
				Usage: "manual path to the lock file, or {config}.lock.json if empty",
			},
			&cli.StringFlag{
				Name:  "registry-file",
				Usage: "path to the nix registry JSON file, or {config}.registry.json if empty",
			},
			&cli.BoolFlag{
				Name:  "no-color",
				Usage: "disable colored logging, true if stderr is not a terminal",
				Value: os.Getenv("NO_COLOR") != "" || !isatty.IsTerminal(os.Stderr.Fd()),
			},
		},
		Commands: []*cli.Command{
			{
				Name:   "include-flags",
				Usage:  "generate -I flags for NIX_PATH and Nix CLIs",
				Action: runIncludeFlags,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "user",
						Aliases: []string{"u"},
						Usage:   "generate flags for a specific user, default to current user",
					},
				},
			},
			{
				Name:      "store-path",
				Usage:     "query the store path of a channel input",
				ArgsUsage: "channel",
				Action:    runStorePath,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "user",
						Aliases: []string{"u"},
						Usage:   "generate flags for a specific user, default to current user",
					},
				},
			},
		},
		ExitErrHandler: func(ctx context.Context, cmd *cli.Command, err error) {
			if errors.Is(ctx.Err(), context.Canceled) {
				slog.Info("interrupted", tint.Err(err))
			} else {
				slog.Error("fatal error", tint.Err(err))
			}
			os.Exit(1)
		},
	}

	cmd.Run(ctx, os.Args)
}

func cmdInit(ctx context.Context, cmd *cli.Command) error {
	level := slog.LevelInfo
	if cmd.Bool("verbose") {
		level = slog.LevelDebug
	}

	tintLogger := tint.NewHandler(cmd.ErrWriter, &tint.Options{
		Level:   level,
		NoColor: cmd.Bool("no-color"),
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Do not include timestamps in logs.
			if a.Key == slog.TimeKey && len(groups) == 0 {
				return slog.Attr{}
			}
			return a
		},
	})

	logger := slog.New(tintLogger)
	slog.SetDefault(logger)

	_, err := flockLock.TryLockContext(ctx, time.Second)
	if err != nil {
		slog.Warn(
			"cannot acquire file lock",
			"path", flockPath,
			"err", err)
	}

	return nil
}

func cmdFinish(ctx context.Context, cmd *cli.Command) error {
	if err := flockLock.Unlock(); err != nil {
		slog.Warn(
			"cannot release file lock",
			"path", flockPath,
			"err", err)
	}

	return nil
}

func cmdRun(ctx context.Context, cmd *cli.Command) error {
	if cmd.Bool("verbose") {
		ctx = bonito.WithVerbose(ctx)
	}

	state, err := readState(cmd)
	if err != nil {
		return err
	}

	if cmd.Bool("update") || cmd.Bool("update-locks") {
		newState := bonito.State{
			Config: state.Config,
			Lock:   state.Lock,
		}

		channels := cmd.Args().Slice()
		if len(channels) > 0 {
			newState.Config = state.Config.FilterChannels(channels)
		}

		channelCount := recordChannels(newState)
		if channelCount == 0 {
			slog.Warn("no channels to update")
			return nil
		}

		switch {
		case cmd.Bool("update"):
			if err := newState.Update(ctx); err != nil {
				return errors.Wrap(err, "cannot update inputs to latest versions")
			}
		case cmd.Bool("update-locks"):
			if err := newState.UpdateLocks(ctx); err != nil {
				return errors.Wrap(err, "cannot update locks")
			}
		}

		// Update the actual lock state.
		state.Lock = newState.Lock
	}

	slog.Info("applying channels")

	if err := state.Apply(ctx); err != nil {
		return errors.Wrap(err, "cannot apply")
	}

	if state.Config.Flakes.Enable {
		if err := state.saveNixRegistryFile(); err != nil {
			return errors.Wrap(err, "cannot save nix registry file")
		}
	}

	if err := state.saveLockFile(); err != nil {
		return errors.Wrap(err, "cannot save lock file")
	}

	return nil
}

func recordChannels(state bonito.State) int {
	var channelCount int

	for name, input := range state.Config.Global.Channels {
		slog.Info(
			"updating global channel",
			"channel", name,
			"input", input,
			"scope", "global")
		channelCount++
	}

	for name, input := range state.Config.Flakes.Channels {
		slog.Info(
			"updating flakes channel",
			"channel", name,
			"input", input,
			"scope", "flakes")
		channelCount++
	}

	for username, usercfg := range state.Config.Users {
		for name, input := range usercfg.Channels {
			slog.Info(
				"updating user channel",
				"channel", name,
				"input", input,
				"scope", "user",
				"user", username)
			channelCount++
		}
	}

	return channelCount
}

func runIncludeFlags(ctx context.Context, cmd *cli.Command) error {
	state, err := readState(cmd)
	if err != nil {
		return err
	}

	username, err := currentUsername(cmd)
	if err != nil {
		return fmt.Errorf("cannot get current user: %w", err)
	}

	channelInputs, err := state.Config.UserChannels(username)
	if err != nil {
		return fmt.Errorf("cannot get channels for user %q: %w", username, err)
	}

	values := make([]string, 0, len(channelInputs))
	for name, input := range channelInputs {
		lock, ok := state.Lock.Channels[input]
		if !ok || lock.StorePath == "" {
			return fmt.Errorf("channel %q has no lock, try running `bonito` again?", name)
		}
		value := fmt.Sprintf("-I %s=%s", name, lock.StorePath)
		values = append(values, value)
	}

	fmt.Println(strings.Join(values, " "))
	return nil
}

func runStorePath(ctx context.Context, cmd *cli.Command) error {
	state, err := readState(cmd)
	if err != nil {
		return err
	}

	channel := cmd.Args().First()
	if channel == "" {
		return errors.New("channel argument is required")
	}

	username, err := currentUsername(cmd)
	if err != nil {
		return fmt.Errorf("cannot get current user: %w", err)
	}

	channelInputs, err := state.Config.UserChannels(username)
	if err != nil {
		return fmt.Errorf("cannot get channels for user %q: %w", username, err)
	}

	channelInput, ok := channelInputs[channel]
	if !ok {
		return fmt.Errorf("channel %q not found", channel)
	}

	lock, ok := state.Lock.Channels[channelInput]
	if !ok {
		return fmt.Errorf("channel %q has no lock, try running `bonito` again?", channel)
	}

	fmt.Println(lock.StorePath)
	return nil
}

func currentUsername(cmd *cli.Command) (string, error) {
	username := cmd.String("user")
	if username == "" {
		u, err := user.Current()
		if err != nil {
			return "", fmt.Errorf("cannot get current user: %w", err)
		}
		username = u.Username
	}
	return username, nil
}
