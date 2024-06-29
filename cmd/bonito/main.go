package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/diamondburned/nix-bonito/bonito"
	"github.com/gofrs/flock"
	"github.com/pkg/errors"
	"github.com/urfave/cli/v2"
)

const flockName = "bonito.lock"

func init() {
	log.SetFlags(0)
}

func main() {
	var defaultConfigFile string
	if hostname, err := os.Hostname(); err == nil {
		defaultConfigFile = hostname + ".toml"
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	flockPath := filepath.Join(os.TempDir(), "bonito.lock")
	flock := flock.New(flockPath)

	_, err := flock.TryLockContext(ctx, time.Second)
	if err != nil {
		log.Fatalf("cannot acquire %s: %v", flockPath, err)
	}
	defer func() {
		if err := flock.Unlock(); err != nil {
			log.Fatalf("cannot release %s: %v", flockPath, err)
		}
	}()

	app := cli.App{
		Name:      "bonito",
		Usage:     "Declarative Nix channel manager",
		Action:    run,
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
	}

	if err := app.RunContext(ctx, os.Args); err != nil {
		cli.HandleExitCoder(err)
		log.Fatalln(err)
	}
}

func run(ctx *cli.Context) error {
	if ctx.Bool("verbose") {
		ctx.Context = bonito.WithVerbose(ctx.Context)
	}

	state, err := readState(ctx)
	if err != nil {
		return err
	}

	if ctx.Bool("update") || ctx.Bool("update-locks") {
		newState := bonito.State{
			Config: state.Config,
			Lock:   state.Lock,
		}

		channels := ctx.Args().Slice()
		if len(channels) > 0 {
			newState.Config = state.Config.FilterChannels(channels)
		}

		channelCount := recordChannels(newState)
		if channelCount == 0 {
			log.Println("no channels to update")
			return nil
		}

		switch {
		case ctx.Bool("update"):
			if err := newState.Update(ctx.Context); err != nil {
				return errors.Wrap(err, "cannot update inputs to latest versions")
			}
		case ctx.Bool("update-locks"):
			if err := newState.UpdateLocks(ctx.Context); err != nil {
				return errors.Wrap(err, "cannot update locks")
			}
		}

		// Update the actual lock state.
		state.Lock = newState.Lock
	}

	log.Println("applying channels...")

	if err := state.Apply(ctx.Context); err != nil {
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

	for name := range state.Config.Global.Channels {
		log.Print("will update channel global.", name)
		channelCount++
	}

	for name := range state.Config.Flakes.Channels {
		log.Print("will update channel flakes.", name)
		channelCount++
	}

	for username, usercfg := range state.Config.Users {
		for name := range usercfg.Channels {
			log.Printf("will update channel users.%s.%s", username, name)
			channelCount++
		}
	}

	return channelCount
}

func runIncludeFlags(ctx *cli.Context) error {
	state, err := readState(ctx)
	if err != nil {
		return err
	}

	username, err := currentUsername(ctx)
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

func runStorePath(ctx *cli.Context) error {
	state, err := readState(ctx)
	if err != nil {
		return err
	}

	channel := ctx.Args().First()
	if channel == "" {
		return errors.New("channel argument is required")
	}

	username, err := currentUsername(ctx)
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

func currentUsername(ctx *cli.Context) (string, error) {
	username := ctx.String("user")
	if username == "" {
		u, err := user.Current()
		if err != nil {
			return "", fmt.Errorf("cannot get current user: %w", err)
		}
		username = u.Username
	}
	return username, nil
}
