package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

type providerUninstallPendingRemovalBackendV1 struct {
	acquire         func(context.Context, string) (*deploy.OperationLock, error)
	removeReference func(context.Context, providers.RealizedImageV1, string, string, string) error
	confirmAbsent   func(context.Context, string) error
	finalize        func(string, string) error
}

func retryPendingProviderUninstallRemovalV1(
	ctx context.Context,
	deploymentDir string,
	service string,
) (ProviderUninstallResultV1, bool, error) {
	return retryPendingProviderUninstallRemovalWithV1(
		ctx,
		deploymentDir,
		service,
		providerUninstallPendingRemovalBackendV1{
			acquire:         deploy.AcquireExistingOperationLock,
			removeReference: RemoveEnvironmentGenerationReference,
			confirmAbsent: func(ctx context.Context, reference string) error {
				output, err := runDockerOutput(ctx, "image", "ls", "--quiet", "--no-trunc", reference)
				if err != nil || strings.TrimSpace(output) != "" {
					return errors.Join(fmt.Errorf("owned reference %q is not confirmed absent after installed isolation", reference), err)
				}
				return nil
			},
			finalize: finalizePendingProviderUninstallRemovalV1,
		},
	)
}

func retryPendingProviderUninstallRemovalWithV1(
	ctx context.Context,
	deploymentDir string,
	service string,
	backend providerUninstallPendingRemovalBackendV1,
) (result ProviderUninstallResultV1, found bool, err error) {
	if ctx == nil {
		return ProviderUninstallResultV1{}, false, fmt.Errorf("retry pending provider uninstall removal requires a context")
	}
	if err := ctx.Err(); err != nil {
		return ProviderUninstallResultV1{}, false, err
	}
	if backend.acquire == nil || backend.removeReference == nil || backend.finalize == nil {
		return ProviderUninstallResultV1{}, false, fmt.Errorf("retry pending provider uninstall removal requires a complete backend")
	}
	deploymentDir, err = filepath.Abs(deploymentDir)
	if err != nil {
		return ProviderUninstallResultV1{}, false, fmt.Errorf("resolve provider uninstall deployment directory: %w", err)
	}
	if _, err := os.Lstat(deploymentDir); err == nil {
		return ProviderUninstallResultV1{}, false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return ProviderUninstallResultV1{}, false, fmt.Errorf("inspect provider uninstall deployment directory: %w", err)
	}
	pending, found, err := locatePendingProviderUninstallRemovalV1(deploymentDir)
	if err != nil {
		return ProviderUninstallResultV1{}, found, err
	}
	if !found {
		return ProviderUninstallResultV1{}, false, nil
	}
	operation, err := backend.acquire(ctx, pending.StateRoot)
	if err != nil {
		return ProviderUninstallResultV1{}, true, fmt.Errorf("lock pending deployment removal: %w", err)
	}
	operationHeld := true
	defer func() {
		if operationHeld {
			err = errors.Join(err, operation.Unlock())
		}
	}()
	state, stateFound, err := operation.ReadStateV1()
	if err != nil {
		return ProviderUninstallResultV1{}, true, fmt.Errorf("read pending deployment removal state: %w", err)
	}
	if !stateFound || state.Deployment == nil || state.Current == nil {
		return ProviderUninstallResultV1{}, true, fmt.Errorf("pending deployment removal has no installed state: %s", pending.StateRoot)
	}
	installation := state.Deployment.Installation
	if installation.TargetDir != deploymentDir {
		return ProviderUninstallResultV1{}, true, fmt.Errorf(
			"pending deployment removal target %q does not match requested deployment %q",
			installation.TargetDir, deploymentDir,
		)
	}
	if service = strings.TrimSpace(service); service != "" && service != installation.Service {
		return ProviderUninstallResultV1{}, true, fmt.Errorf(
			"--service-name %q does not match pending removal service %q",
			service, installation.Service,
		)
	}
	if state.TerminalRemoval {
		if err := operation.ResumeTerminalRemovalV1(state); err != nil {
			return ProviderUninstallResultV1{}, true, err
		}
	}
	if _, pending, err := operation.ReadPendingBuild(); err != nil {
		return ProviderUninstallResultV1{}, true, fmt.Errorf("read pending deployment build: %w", err)
	} else if pending {
		return ProviderUninstallResultV1{}, true, fmt.Errorf("pending deployment removal has an incomplete build publication")
	}
	if err := requireNoPendingValidatedBuildV1(operation); err != nil {
		return ProviderUninstallResultV1{}, true, err
	}
	lock, lockFound, err := operation.ReadBuildLock(
		state.Current.BuildLockDigest,
		acceptProviderProfileOwnerForCutoverV1,
	)
	if err != nil {
		return ProviderUninstallResultV1{}, true, fmt.Errorf("read pending deployment build lock: %w", err)
	}
	if !lockFound {
		return ProviderUninstallResultV1{}, true, fmt.Errorf(
			"pending deployment build lock %s is missing",
			state.Current.BuildLockDigest,
		)
	}
	if err := validateGenerationBuildLock(
		*state.Current,
		lock,
		acceptProviderProfileOwnerForCutoverV1,
	); err != nil {
		return ProviderUninstallResultV1{}, true, fmt.Errorf("pending deployment current build: %w", err)
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return ProviderUninstallResultV1{}, true, fmt.Errorf("decode pending deployment blueprint: %w", err)
	}
	pairs, err := stagedRemovalOwnedReferencesV1(operation, state, document.Environment.ID, deploymentDir)
	if err != nil {
		return ProviderUninstallResultV1{}, true, err
	}
	validated, validatedFound, err := operation.ReadValidatedBuildV1()
	if err != nil {
		return ProviderUninstallResultV1{}, true, err
	}
	if state.TerminalRemoval {
		// Every owner retired before isolation. Retained canonical authority
		// proves exact absence here; missing original paths grant no deletion.
		if backend.confirmAbsent == nil || validatedFound && (!validated.Discarded || len(validated.PendingCleanup) != 0) {
			return ProviderUninstallResultV1{}, true, fmt.Errorf("pending installed removal has unconfirmed retirement")
		}
		if validatedFound {
			validatedPairs, err := validatedRecordReferencesV1(validated, document.Environment.ID, deploymentDir)
			if err != nil {
				return ProviderUninstallResultV1{}, true, err
			}
			for _, pair := range validatedPairs {
				pairs = append(pairs, OwnedImageReferenceV1{Reference: pair.ImageReference, Image: pair.Image})
			}
		}
		for _, pair := range pairs {
			if err := backend.confirmAbsent(context.WithoutCancel(ctx), pair.Reference); err != nil {
				return ProviderUninstallResultV1{}, true, err
			}
		}
	} else {
		// Preserve the existing previous-profile, primary-only pending cleanup.
		// Unreleased incomplete portable tombstones are never a new retry format.
		if len(pairs) != 1 || validatedFound {
			return ProviderUninstallResultV1{}, true, fmt.Errorf("unguarded pending removal lacks completed paired retirement authority")
		}
		if err := backend.removeReference(context.WithoutCancel(ctx), lock.FinalImage, state.Current.Reference, document.Environment.ID, deploymentDir); err != nil {
			return ProviderUninstallResultV1{}, true, fmt.Errorf("remove pending deployment image reference: %w", err)
		}
	}
	if err := operation.Unlock(); err != nil {
		return ProviderUninstallResultV1{}, true, fmt.Errorf("release pending deployment lock: %w", err)
	}
	operationHeld = false
	if err := backend.finalize(deploymentDir, pending.Tombstone); err != nil {
		return ProviderUninstallResultV1{}, true, pendingProviderUninstallRemovalErrorV1(
			"remove pending deployment directory",
			err,
			deploymentDir,
			pending.Tombstone,
		)
	}
	return ProviderUninstallResultV1{
		DeploymentDir:    deploymentDir,
		Environment:      document.Environment.ID,
		Service:          installation.Service,
		RemovedDirectory: true,
	}, true, nil
}

func readPendingProviderUninstallStateV1(deploymentDir string) (deploy.StateV1, bool, error) {
	pending, found, err := locatePendingProviderUninstallRemovalV1(deploymentDir)
	if err != nil {
		return deploy.StateV1{}, false, err
	}
	if !found {
		return deploy.StateV1{}, false, nil
	}
	state, found, err := readProviderUninstallStateV1(pending.StateRoot)
	if err != nil || !found {
		return state, found, err
	}
	absolute, err := filepath.Abs(deploymentDir)
	if err != nil {
		return deploy.StateV1{}, false, fmt.Errorf("resolve provider uninstall deployment directory: %w", err)
	}
	if state.Deployment == nil || state.Current == nil {
		return deploy.StateV1{}, false, fmt.Errorf("pending deployment removal has no installed state: %s", pending.StateRoot)
	}
	if state.Deployment.Installation.TargetDir != absolute {
		return deploy.StateV1{}, false, fmt.Errorf(
			"pending deployment removal target %q does not match requested deployment %q",
			state.Deployment.Installation.TargetDir, absolute,
		)
	}
	return state, true, nil
}

type pendingProviderUninstallRemovalV1 struct {
	Tombstone string
	Control   string
	StateRoot string
}

func locatePendingProviderUninstallRemovalV1(
	deploymentDir string,
) (pendingProviderUninstallRemovalV1, bool, error) {
	tombstone, err := providerUninstallTombstoneV1(deploymentDir)
	if err != nil {
		return pendingProviderUninstallRemovalV1{}, false, err
	}
	control, err := providerUninstallControlV1(deploymentDir)
	if err != nil {
		return pendingProviderUninstallRemovalV1{}, false, err
	}
	tombstoneFound, err := requirePendingRemovalDirectoryV1(tombstone)
	if err != nil {
		return pendingProviderUninstallRemovalV1{}, true, err
	}
	controlFound, err := requirePendingRemovalDirectoryV1(control)
	if err != nil {
		return pendingProviderUninstallRemovalV1{}, true, err
	}
	if !tombstoneFound && !controlFound {
		return pendingProviderUninstallRemovalV1{}, false, nil
	}
	tombstoneState, err := requirePendingRemovalDirectoryV1(filepath.Join(tombstone, ".reploy"))
	if err != nil {
		return pendingProviderUninstallRemovalV1{}, true, err
	}
	controlState, err := requirePendingRemovalDirectoryV1(filepath.Join(control, ".reploy"))
	if err != nil {
		return pendingProviderUninstallRemovalV1{}, true, err
	}
	if tombstoneState == controlState {
		if tombstoneState {
			return pendingProviderUninstallRemovalV1{}, true, fmt.Errorf(
				"pending deployment removal has ambiguous state in %s and %s",
				tombstone, control,
			)
		}
		return pendingProviderUninstallRemovalV1{}, true, fmt.Errorf(
			"pending deployment removal has no recoverable state in %s or %s",
			tombstone, control,
		)
	}
	stateRoot := tombstone
	if controlState {
		stateRoot = control
	}
	return pendingProviderUninstallRemovalV1{
		Tombstone: tombstone,
		Control:   control,
		StateRoot: stateRoot,
	}, true, nil
}

func finalizePendingProviderUninstallRemovalV1(
	deploymentDir string,
	tombstone string,
) error {
	expectedTombstone, err := providerUninstallTombstoneV1(deploymentDir)
	if err != nil {
		return err
	}
	if tombstone != expectedTombstone {
		return fmt.Errorf("pending deployment removal path %q does not match expected path %q", tombstone, expectedTombstone)
	}
	control, err := providerUninstallControlV1(deploymentDir)
	if err != nil {
		return err
	}
	tombstoneState := filepath.Join(tombstone, ".reploy")
	controlState := filepath.Join(control, ".reploy")
	tombstoneStateFound, err := requirePendingRemovalDirectoryV1(tombstoneState)
	if err != nil {
		return err
	}
	controlStateFound, err := requirePendingRemovalDirectoryV1(controlState)
	if err != nil {
		return err
	}
	if tombstoneStateFound && controlStateFound {
		return fmt.Errorf("pending deployment removal has state in both %s and %s", tombstone, control)
	}
	if tombstoneStateFound {
		controlFound, err := requirePendingRemovalDirectoryV1(control)
		if err != nil {
			return err
		}
		if !controlFound {
			if err := os.Mkdir(control, 0o700); err != nil {
				return fmt.Errorf("create pending deployment removal control directory: %w", err)
			}
		} else if err := validatePendingRemovalControlContentsV1(control, false); err != nil {
			return err
		}
		if err := os.Rename(tombstoneState, controlState); err != nil {
			return fmt.Errorf("preserve pending deployment removal state: %w", err)
		}
	} else if !controlStateFound {
		return fmt.Errorf("pending deployment removal has no recoverable state")
	}
	if err := validatePendingRemovalControlContentsV1(control, true); err != nil {
		return err
	}
	if err := os.RemoveAll(tombstone); err != nil {
		return fmt.Errorf("remove pending deployment workload files: %w", err)
	}
	if err := os.RemoveAll(control); err != nil {
		return fmt.Errorf("remove pending deployment control state: %w", err)
	}
	return nil
}

func providerUninstallControlV1(deploymentDir string) (string, error) {
	absolute, err := filepath.Abs(deploymentDir)
	if err != nil {
		return "", fmt.Errorf("resolve provider uninstall deployment directory: %w", err)
	}
	return filepath.Join(
		filepath.Dir(absolute),
		"."+filepath.Base(absolute)+".reploy-uninstall-control",
	), nil
}

func requirePendingRemovalDirectoryV1(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect pending deployment removal path %s: %w", path, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("pending deployment removal path must be a real directory: %s", path)
	}
	return true, nil
}

func validatePendingRemovalControlContentsV1(control string, stateExpected bool) error {
	entries, err := os.ReadDir(control)
	if err != nil {
		return fmt.Errorf("read pending deployment removal control directory: %w", err)
	}
	if !stateExpected && len(entries) == 0 {
		return nil
	}
	if stateExpected && len(entries) == 1 && entries[0].Name() == ".reploy" && entries[0].IsDir() {
		return nil
	}
	return fmt.Errorf("pending deployment removal control directory contains unexpected entries: %s", control)
}

func pendingProviderUninstallRemovalErrorV1(
	action string,
	err error,
	deploymentDir string,
	tombstone string,
) error {
	paths := make([]string, 0, 2)
	if found, inspectErr := requirePendingRemovalDirectoryV1(tombstone); inspectErr == nil && found {
		paths = append(paths, tombstone)
	}
	if control, controlErr := providerUninstallControlV1(deploymentDir); controlErr == nil {
		if found, inspectErr := requirePendingRemovalDirectoryV1(control); inspectErr == nil && found {
			paths = append(paths, control)
		}
	}
	if len(paths) == 0 {
		paths = append(paths, tombstone)
	}
	return fmt.Errorf(
		"%s: %w\npartial removal retained at %s\nnext: retry uninstall against %s",
		action,
		err,
		strings.Join(paths, ", "),
		deploymentDir,
	)
}
