package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/diamondburned/nix-bonito/bonito"
	"github.com/pkg/errors"
	"github.com/urfave/cli/v3"
)

type stateFiles struct {
	bonito.State
	lockPath     string
	configPath   string
	registryPath string
}

func readState(cmd *cli.Command) (*stateFiles, error) {
	configPath := cmd.String("config")

	// Resolve configPath to an absolute path so that symlinks are resolved.
	var err error
	if configPath, err = filepath.EvalSymlinks(configPath); err != nil {
		return nil, errors.Wrap(err, "cannot resolve config path")
	}

	config, err := readConfigFile(configPath)
	if err != nil {
		return nil, errors.Wrap(err, "cannot read config file")
	}

	lockPath := cmd.String("lock-file")
	if lockPath == "" {
		lockPath = trimExt(configPath) + ".lock.json"
	}

	lockFile, err := tryReadLockFile(lockPath)
	if err != nil {
		return nil, errors.Wrap(err, "cannot read lock file")
	}

	registryPath := cmd.String("registry-file")
	if registryPath == "" {
		registryPath = trimExt(configPath) + ".registry.json"
	}

	return &stateFiles{
		State: bonito.State{
			Config: config,
			Lock:   lockFile,
		},
		lockPath:     lockPath,
		configPath:   configPath,
		registryPath: registryPath,
	}, nil
}

func trimExt(name string) string {
	if i := strings.LastIndexByte(name, '.'); i != -1 {
		name = name[:i]
	}
	return name
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
		return bonito.Config{}, errors.Wrap(err, "cannot open config file")
	}
	defer f.Close()

	return bonito.NewConfigFromReader(f)
}

func (s stateFiles) saveLockFile() error {
	return writeToFile([]byte(s.Lock.String()), s.lockPath)
}

func (s stateFiles) saveNixRegistryFile() error {
	registryJSON, err := s.GenerateNixRegistry()
	if err != nil {
		return errors.Wrap(err, "cannot generate flakes registry")
	}

	return writeToFile(registryJSON, s.registryPath)
}

func writeToFile(b []byte, dst string) error {
	dir := filepath.Dir(dst)

	f, err := os.CreateTemp(dir, ".tmp.bonito.*.lock")
	if err != nil {
		return errors.Wrap(err, "cannot make temporary lock file")
	}
	defer f.Close()
	defer os.Remove(f.Name())

	if _, err := f.Write(b); err != nil {
		return errors.Wrap(err, "cannot write to temporary lock file")
	}

	if err := os.Rename(f.Name(), dst); err != nil {
		return errors.Wrap(err, "cannot commit lock file")
	}

	return nil
}
