package dockerdeploy

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/omry/reploy/internal/providerstore"
)

// ensureProviderInstallDestinationV1 bootstraps the self-contained deployment
// directory needed for its advisory lock. It publishes no installation state
// or live runtime files; later preflight and publication remain responsible for
// those changes.
func ensureProviderInstallDestinationV1(destination string) (bool, error) {
	if destination == "" || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return false, fmt.Errorf("install destination must be an absolute clean path")
	}
	info, err := os.Lstat(destination)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false, fmt.Errorf("install destination must be a real directory: %s", destination)
		}
		return false, nil
	}
	if !os.IsNotExist(err) {
		return false, fmt.Errorf("inspect install destination: %w", err)
	}
	if err := validateProviderInstallDirectoryAncestorsV1(filepath.Dir(destination)); err != nil {
		return false, err
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return false, fmt.Errorf("create install destination: %w", err)
	}
	if err := validateProviderInstallDirectoryAncestorsV1(destination); err != nil {
		return false, err
	}
	return true, nil
}

// preflightProviderInstallBootstrapV1 checks the large, already-known store
// transfer against the destination filesystem before creating the deployment
// directory or its operation lock. The exact candidate-file check still runs
// under the destination lock immediately before candidate preparation.
func preflightProviderInstallBootstrapV1(sourceStore providerstore.Store, sourceBuild CurrentBuild, destination string) error {
	requirements, err := providerInstallAccountBulkDiskRequirementsV1(sourceStore, sourceBuild, destination)
	if err != nil {
		return err
	}
	return preflightProviderInstallDiskSpaceV1(requirements)
}

// cleanupFailedProviderInstallDestinationV1 removes a destination created by a
// failed first install only when it still contains bootstrap lock state and no
// installation data. Renaming first gives the cleanup a stable private path;
// a concurrently claimed or otherwise changed destination is restored and
// retained.
func cleanupFailedProviderInstallDestinationV1(destination string) error {
	bootstrap, err := isEmptyProviderInstallBootstrapV1(destination)
	if err != nil || !bootstrap {
		return err
	}
	parent := filepath.Dir(destination)
	quarantine, err := os.MkdirTemp(parent, ".reploy-failed-install-*")
	if err != nil {
		return fmt.Errorf("reserve failed-install cleanup path: %w", err)
	}
	if err := os.Remove(quarantine); err != nil {
		return fmt.Errorf("release failed-install cleanup path: %w", err)
	}
	if err := os.Rename(destination, quarantine); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("isolate failed install destination: %w", err)
	}
	bootstrap, inspectErr := isEmptyProviderInstallBootstrapV1(quarantine)
	if inspectErr != nil || !bootstrap {
		if _, destinationErr := os.Lstat(destination); destinationErr == nil || !os.IsNotExist(destinationErr) {
			if inspectErr != nil {
				return fmt.Errorf("inspect isolated failed install destination: %w; retained at %s", inspectErr, quarantine)
			}
			return fmt.Errorf("failed install destination changed during cleanup; retained at %s", quarantine)
		}
		if restoreErr := os.Rename(quarantine, destination); restoreErr != nil {
			return fmt.Errorf("restore changed failed install destination: %w; retained at %s", restoreErr, quarantine)
		}
		return inspectErr
	}
	if err := os.RemoveAll(quarantine); err != nil {
		return fmt.Errorf("remove failed install destination: %w", err)
	}
	return nil
}

func isEmptyProviderInstallBootstrapV1(destination string) (bool, error) {
	entries, err := os.ReadDir(destination)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect failed install destination: %w", err)
	}
	if len(entries) == 0 {
		return true, nil
	}
	if len(entries) != 1 || entries[0].Name() != ".reploy" || !entries[0].IsDir() || entries[0].Type()&os.ModeSymlink != 0 {
		return false, nil
	}
	stateDir := filepath.Join(destination, ".reploy")
	stateEntries, err := os.ReadDir(stateDir)
	if err != nil {
		return false, fmt.Errorf("inspect failed install state directory: %w", err)
	}
	if len(stateEntries) == 0 {
		return true, nil
	}
	if len(stateEntries) != 1 || stateEntries[0].Name() != "operation.lock" || !stateEntries[0].Type().IsRegular() || stateEntries[0].Type()&os.ModeSymlink != 0 {
		return false, nil
	}
	return true, nil
}
