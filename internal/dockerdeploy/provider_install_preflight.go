package dockerdeploy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/omry/reploy/internal/deploy"
)

// This is an early read-only check, not the install's serialization boundary.
// The destination is checked again under its operation lock before mutation.
func preflightProviderInstallDestinationV1(destination, service string) error {
	state, found, err := readProviderInstallDestinationStateV1(destination)
	statePath := filepath.Join(destination, StateFileName)
	if err != nil {
		return fmt.Errorf("install destination state %s cannot be used: %w; inspect or update it with a compatible Reploy release, preserving configuration and data", statePath, err)
	}
	if !found {
		return nil
	}
	if state.Deployment == nil {
		return fmt.Errorf("destination contains a staging deployment at %s; choose a different install target", statePath)
	}
	if state.TerminalRemoval {
		return fmt.Errorf("install destination %s has pending terminal removal; complete the original removal before installation", statePath)
	}
	if service != "" && state.Deployment.Installation.Service != service {
		return fmt.Errorf("install destination %s is already installed as service %q; uninstall it before installing as service %q", statePath, state.Deployment.Installation.Service, service)
	}
	return nil
}

func readProviderInstallDestinationStateV1(destination string) (deploy.StateV1, bool, error) {
	var state deploy.StateV1
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		return state, false, nil
	}
	if err != nil {
		return state, false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return state, false, fmt.Errorf("install target must be a real directory")
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		return state, false, err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return state, false, fmt.Errorf("install target changed during preflight")
	}
	info, err = root.Lstat(".reploy")
	if os.IsNotExist(err) {
		return state, false, nil
	}
	if err != nil {
		return state, false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return state, false, fmt.Errorf("metadata path must be a real directory")
	}
	metadata, err := root.OpenRoot(".reploy")
	if err != nil {
		return state, false, err
	}
	defer metadata.Close()
	opened, err = metadata.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return state, false, fmt.Errorf("metadata directory changed during preflight")
	}
	info, err = metadata.Lstat("state.json")
	if os.IsNotExist(err) {
		return state, false, nil
	}
	if err != nil {
		return state, false, err
	}
	if !info.Mode().IsRegular() {
		return state, false, fmt.Errorf("state must be a regular file")
	}
	file, err := openProviderPreflightStateV1(metadata)
	if err != nil {
		return state, false, err
	}
	defer file.Close()
	opened, err = file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return state, false, fmt.Errorf("state changed during preflight")
	}
	content, err := io.ReadAll(file)
	if err != nil {
		return state, false, err
	}
	after, err := metadata.Lstat("state.json")
	if err != nil || !os.SameFile(opened, after) {
		return state, false, fmt.Errorf("state changed during preflight")
	}
	state, err = deploy.DecodeStateV1(content)
	return state, err == nil, err
}
