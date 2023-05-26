package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
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
				Usage:   "update channels and lock files",
			},
			&cli.BoolFlag{
				Name:  "update-locks",
				Usage: "update lock files only",
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
				Name:  "nix-registry-file",
				Usage: "path to the nix registry JSON file, or {config}.registry.json if empty",
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

	if ctx.Bool("update") {
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

		if err := newState.UpdateLocks(ctx.Context); err != nil {
			return errors.Wrap(err, "cannot update locks")
		}

		if ctx.Bool("update-locks") {
			return nil
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
	}

	for name := range state.Config.Flakes.Channels {
		log.Print("will update channel flakes.", name)
	}

	for username, usercfg := range state.Config.Users {
		for name := range usercfg.Channels {
			log.Printf("will update channel users.%s.%s", username, name)
		}
	}

	return channelCount
}
