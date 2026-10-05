package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

type providerUninstallRemoveDirBackendV1 struct {
	discardValidated func(context.Context, *deploy.OperationLock, string, string) error
	removeCompanion  func(context.Context, *deploy.OperationLock, OwnedImageReferenceV1, deploy.EnvironmentGenerationState, string, string) error
	guard            func(*deploy.OperationLock, deploy.StateV1) error
	complete         func(*deploy.OperationLock, string, *deploy.ControlLeaseV1) error
	removeMarker     func(*deploy.OperationLock, string) error
	releaseLease     func(*deploy.ControlLeaseV1) error
	reserve          func(string) (string, error)
	isolate          func(*deploy.OperationLock, string) (bool, error)
	unlock           func(*deploy.OperationLock) error
	removeReference  func(context.Context, providers.RealizedImageV1, string, string, string) error
	finalize         func(string, string) error
}

func removeProviderUninstallDeploymentV1(
	ctx context.Context,
	operation *deploy.OperationLock,
	markerID string,
	lease *deploy.ControlLeaseV1,
	plan providerUninstallPlanV1,
	options RunOptions,
) error {
	return removeProviderUninstallDeploymentWithV1(ctx, operation, markerID, lease, plan, options, providerUninstallRemoveDirBackendV1{
		discardValidated: func(ctx context.Context, operation *deploy.OperationLock, environment, dir string) error {
			_, _, err := discardValidatedBuildWithBackendV1(ctx, operation, environment, dir, validatedRetirementBackend(RemoveEnvironmentGenerationReference))
			return err
		},
		removeCompanion: RemovePortableEnvironmentReferenceV1,
		guard:           guardStagedRemovalV1,
		complete:        CompleteControlAdmissionV1,
		removeMarker: func(operation *deploy.OperationLock, markerID string) error {
			_, removed, err := operation.RemoveControlMarkerV1(markerID)
			if err == nil && !removed {
				return fmt.Errorf("control marker %q is not outstanding", markerID)
			}
			return err
		},
		releaseLease: func(lease *deploy.ControlLeaseV1) error { return lease.Release() },
		reserve:      reserveProviderUninstallTombstoneV1,
		isolate: func(operation *deploy.OperationLock, destination string) (bool, error) {
			return operation.IsolateOriginalDirectory(destination)
		},
		unlock:          func(operation *deploy.OperationLock) error { return operation.Unlock() },
		removeReference: RemoveEnvironmentGenerationReference,
		finalize:        finalizePendingProviderUninstallRemovalV1,
	})
}

func removeProviderUninstallDeploymentWithV1(
	ctx context.Context,
	operation *deploy.OperationLock,
	markerID string,
	lease *deploy.ControlLeaseV1,
	plan providerUninstallPlanV1,
	options RunOptions,
	backend providerUninstallRemoveDirBackendV1,
) (err error) {
	if operation == nil {
		return fmt.Errorf("remove provider uninstall deployment requires admitted lock ownership")
	}
	if !plan.RemoveDir {
		return fmt.Errorf("remove provider uninstall deployment requires remove-dir")
	}
	if backend.discardValidated == nil || backend.removeCompanion == nil || backend.guard == nil || backend.complete == nil || backend.removeMarker == nil || backend.releaseLease == nil || backend.reserve == nil || backend.isolate == nil || backend.unlock == nil || backend.removeReference == nil || backend.finalize == nil {
		return fmt.Errorf("remove provider uninstall deployment requires a complete backend")
	}
	markerOutstanding := markerID != ""
	leaseOutstanding := lease != nil
	operationHeld := true
	terminalAttempt := plan.State.TerminalRemoval
	defer func() {
		if !operationHeld {
			return
		}
		var releaseErr error
		if terminalAttempt {
			releaseErr = errors.Join(lease.Close(), backend.unlock(operation))
		} else if markerOutstanding {
			releaseErr = backend.complete(operation, markerID, lease)
		} else {
			if leaseOutstanding {
				releaseErr = backend.releaseLease(lease)
			}
			releaseErr = errors.Join(releaseErr, backend.unlock(operation))
		}
		err = errors.Join(err, releaseErr)
	}()
	if ctx == nil {
		return fmt.Errorf("remove provider uninstall deployment requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	state, found, err := operation.ReadStateV1()
	if err != nil || !found || state.Deployment == nil || state.Current == nil {
		return errors.Join(fmt.Errorf("installed removal requires retained deployment and current authority"), err)
	}
	if !reflect.DeepEqual(state.Deployment.Installation, plan.Installation) || state.Current.Reference != plan.GenerationReference || plan.Installation.TargetDir == "" {
		return fmt.Errorf("installed authority changed before deployment removal")
	}
	terminalAttempt = state.TerminalRemoval
	if !terminalAttempt && (markerID == "" || lease == nil) {
		return fmt.Errorf("installed removal requires admitted lock ownership before its guard")
	}
	if terminalAttempt {
		if err := backend.guard(operation, state); err != nil {
			return fmt.Errorf("resume installed removal: %w", err)
		}
	}
	pairs, err := stagedRemovalOwnedReferencesV1(operation, state, plan.Environment, plan.Installation.TargetDir)
	if err != nil {
		return err
	}
	if !terminalAttempt {
		terminalAttempt = true
		if err := backend.guard(operation, state); err != nil {
			return fmt.Errorf("guard installed removal: %w", err)
		}
	}
	cleanupContext := context.WithoutCancel(ctx)
	if err := backend.discardValidated(cleanupContext, operation, plan.Environment, plan.Installation.TargetDir); err != nil {
		return fmt.Errorf("retire installed validated owners: %w", err)
	}
	validated, found, err := operation.ReadValidatedBuildV1()
	if err != nil || found && (!validated.Discarded || len(validated.PendingCleanup) != 0) {
		return errors.Join(fmt.Errorf("validated references remain before installed removal"), err)
	}
	for index, pair := range pairs {
		if err := operation.RequireRetirement(); err != nil {
			return err
		}
		var removeErr error
		if index == 0 {
			removeErr = backend.removeReference(cleanupContext, pair.Image, pair.Reference, plan.Environment, plan.Installation.TargetDir)
		} else {
			removeErr = backend.removeCompanion(cleanupContext, operation, pair, *state.Current, plan.Environment, plan.Installation.TargetDir)
		}
		if removeErr != nil {
			return fmt.Errorf("retire installed image reference %q: %w", pair.Reference, removeErr)
		}
	}
	tombstone, err := backend.reserve(plan.Installation.TargetDir)
	if err != nil {
		return fmt.Errorf("reserve deployment removal path: %w", err)
	}
	if markerID != "" {
		if err := backend.removeMarker(operation, markerID); err != nil {
			return fmt.Errorf("complete uninstall admission before deployment removal: %w", err)
		}
	}
	markerOutstanding = false
	if err := backend.releaseLease(lease); err != nil {
		return fmt.Errorf("release uninstall queue ownership before deployment removal: %w", err)
	}
	leaseOutstanding = false
	lease = nil
	moved, isolateErr := backend.isolate(operation, tombstone)
	if isolateErr != nil {
		if moved {
			return pendingProviderUninstallRemovalErrorV1("isolate deployment directory", isolateErr, plan.Installation.TargetDir, tombstone)
		}
		return fmt.Errorf("move deployment for removal: %w", isolateErr)
	}
	if !moved {
		return fmt.Errorf("deployment directory was not isolated")
	}
	if err := backend.unlock(operation); err != nil {
		operationHeld = false
		return fmt.Errorf("release operation lock after moving deployment: %w", err)
	}
	operationHeld = false

	if err := backend.finalize(plan.Installation.TargetDir, tombstone); err != nil {
		return pendingProviderUninstallRemovalErrorV1(
			"remove deployment directory",
			err,
			plan.Installation.TargetDir,
			tombstone,
		)
	}
	if options.Stdout != nil {
		fmt.Fprintf(options.Stdout, "uninstalled service: %s\n", plan.Installation.Service)
	}
	return nil
}

func reserveProviderUninstallTombstoneV1(deploymentDir string) (string, error) {
	reserved, err := providerUninstallTombstoneV1(deploymentDir)
	if err != nil {
		return "", err
	}
	control, err := providerUninstallControlV1(deploymentDir)
	if err != nil {
		return "", err
	}
	for _, path := range []string{reserved, control} {
		if _, err := os.Lstat(path); err == nil {
			return "", fmt.Errorf("pending deployment removal already exists: %s", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect pending deployment removal path: %w", err)
		}
	}
	return reserved, nil
}

func providerUninstallTombstoneV1(deploymentDir string) (string, error) {
	absolute, err := filepath.Abs(deploymentDir)
	if err != nil {
		return "", fmt.Errorf("resolve provider uninstall deployment directory: %w", err)
	}
	return filepath.Join(
		filepath.Dir(absolute),
		"."+filepath.Base(absolute)+".reploy-uninstall-pending",
	), nil
}
