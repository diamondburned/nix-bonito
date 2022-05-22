package main

import (
	"os"
	"path/filepath"

	"github.com/diamondburned/nix-bonito/bonito"
	"github.com/pkg/errors"
	"github.com/urfave/cli/v2"
)

type stateFiles struct {
	bonito.State
	lockPath   string
	configPath string
}

func readState(ctx *cli.Context) (*stateFiles, error) {
	configPath := ctx.String("config")

	config, err := readConfigFile(configPath)
	if err != nil {
		return nil, errors.Wrap(err, "cannot read config file")
	}

	lockPath := ctx.String("lock-file")
	if lockPath == "" {
		lockPath = configPath + ".lock"
	}

	lockFile, err := tryReadLockFile(lockPath)
	if err != nil {
		return nil, errors.Wrap(err, "cannot read lock file")
	}

	return &stateFiles{
		State: bonito.State{
			Config: config,
			Lock:   lockFile,
		},
		lockPath:   lockPath,
		configPath: configPath,
	}, nil
}

func tryReadLockFile(lockPath string) (bonito.LockFile, error) {
	f, err := os.Open(lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			err = nil // not an error
		}
		return bonito.LockFile{}, err
	}
	defer f.Close()

	return bonito.NewLockFileFromReader(f)
}

func readConfigFile(configPath string) (bonito.Config, error) {
	f, err := os.Open(configPath)
	if err != nil {
		return nil, errors.Wrap(err, "cannot open config file")
	}
	defer f.Close()

	return bonito.NewConfigFromReader(f)
}

func (s stateFiles) saveLockFile() error {
	dir := filepath.Dir(s.lockPath)

	f, err := os.CreateTemp(dir, ".tmp.*.lock")
	if err != nil {
		return errors.Wrap(err, "cannot make temporary lock file")
	}
	defer f.Close()
	defer os.Remove(f.Name())

	if err := s.Lock.WriteTo(f); err != nil {
		return errors.Wrap(err, "cannot write to temporary lock file")
	}

	if err := os.Rename(f.Name(), s.lockPath); err != nil {
		return errors.Wrap(err, "cannot commit lock file")
	}

	return nil
}
