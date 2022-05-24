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
		Name:   "bonito",
		Usage:  "Declarative Nix channel manager",
		Action: run,
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
				Usage: "manual path to the lock file, or {config}.lock if empty",
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

	if ctx.Bool("update-locks") {
		if err := state.UpdateLocks(ctx.Context); err != nil {
			return errors.Wrap(err, "cannot update locks")
		}
	} else {
		if err := state.Apply(ctx.Context, ctx.Bool("update")); err != nil {
			return errors.Wrap(err, "cannot apply")
		}
	}

	if err := state.saveLockFile(); err != nil {
		return errors.Wrap(err, "cannot save lock file")
	}

	return nil
}
