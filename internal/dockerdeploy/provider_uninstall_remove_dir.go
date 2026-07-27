package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

type providerUninstallRemoveDirBackendV1 struct {
	newStore        func(string) (providerstore.Store, error)
	load            func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error)
	complete        func(*deploy.OperationLock, string, *deploy.ControlLeaseV1) error
	removeMarker    func(*deploy.OperationLock, string) error
	releaseLease    func(*deploy.ControlLeaseV1) error
	reserve         func(string) (string, error)
	rename          func(string, string) error
	unlock          func(*deploy.OperationLock) error
	removeReference func(context.Context, providers.RealizedImageV1, string, string, string) error
	removeAll       func(string) error
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
		newStore: providerstore.NewStore,
		load:     ValidateCurrentBuild,
		complete: CompleteControlAdmissionV1,
		removeMarker: func(operation *deploy.OperationLock, markerID string) error {
			_, removed, err := operation.RemoveControlMarkerV1(markerID)
			if err == nil && !removed {
				return fmt.Errorf("control marker %q is not outstanding", markerID)
			}
			return err
		},
		releaseLease:    func(lease *deploy.ControlLeaseV1) error { return lease.Release() },
		reserve:         reserveProviderUninstallTombstoneV1,
		rename:          os.Rename,
		unlock:          func(operation *deploy.OperationLock) error { return operation.Unlock() },
		removeReference: RemoveEnvironmentGenerationReference,
		removeAll:       os.RemoveAll,
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
	if operation == nil || markerID == "" || lease == nil {
		return fmt.Errorf("remove provider uninstall deployment requires admitted lock ownership")
	}
	if !plan.RemoveDir {
		return fmt.Errorf("remove provider uninstall deployment requires remove-dir")
	}
	if backend.newStore == nil || backend.load == nil || backend.complete == nil || backend.removeMarker == nil || backend.releaseLease == nil || backend.reserve == nil || backend.rename == nil || backend.unlock == nil || backend.removeReference == nil || backend.removeAll == nil {
		return fmt.Errorf("remove provider uninstall deployment requires a complete backend")
	}
	markerOutstanding := true
	leaseOutstanding := true
	operationHeld := true
	defer func() {
		if !operationHeld {
			return
		}
		var releaseErr error
		if markerOutstanding {
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

	store, err := backend.newStore(plan.Installation.TargetDir)
	if err != nil {
		return err
	}
	current, found, err := backend.load(ctx, operation, store, plan.Environment, plan.Installation.TargetDir)
	if err != nil {
		return fmt.Errorf("validate deployment before removal: %w", err)
	}
	if !found || current.Generation.Reference != plan.GenerationReference {
		return fmt.Errorf("installed generation changed before deployment removal")
	}
	tombstone, err := backend.reserve(plan.Installation.TargetDir)
	if err != nil {
		return fmt.Errorf("reserve deployment removal path: %w", err)
	}
	if err := backend.removeMarker(operation, markerID); err != nil {
		return fmt.Errorf("complete uninstall admission before deployment removal: %w", err)
	}
	markerOutstanding = false
	if err := backend.releaseLease(lease); err != nil {
		return fmt.Errorf("release uninstall queue ownership before deployment removal: %w", err)
	}
	leaseOutstanding = false
	if err := backend.rename(plan.Installation.TargetDir, tombstone); err != nil {
		return fmt.Errorf("move deployment for removal: %w", err)
	}
	if err := backend.unlock(operation); err != nil {
		operationHeld = false
		return fmt.Errorf("release operation lock after moving deployment: %w", err)
	}
	operationHeld = false

	if err := backend.removeReference(ctx, current.Lock.FinalImage, plan.GenerationReference, plan.Environment, plan.Installation.TargetDir); err != nil {
		restoreErr := backend.rename(tombstone, plan.Installation.TargetDir)
		return errors.Join(fmt.Errorf("remove deployment image reference: %w", err), providerUninstallRestoreErrorV1(restoreErr))
	}
	if err := backend.removeAll(tombstone); err != nil {
		restoreErr := backend.rename(tombstone, plan.Installation.TargetDir)
		return errors.Join(fmt.Errorf("remove deployment directory: %w", err), providerUninstallRestoreErrorV1(restoreErr))
	}
	if options.Stdout != nil {
		fmt.Fprintf(options.Stdout, "uninstalled service: %s\n", plan.Installation.Service)
	}
	return nil
}

func reserveProviderUninstallTombstoneV1(deploymentDir string) (string, error) {
	parent := filepath.Dir(deploymentDir)
	pattern := "." + filepath.Base(deploymentDir) + ".reploy-uninstall-*"
	reserved, err := os.MkdirTemp(parent, pattern)
	if err != nil {
		return "", err
	}
	if err := os.Remove(reserved); err != nil {
		_ = os.RemoveAll(reserved)
		return "", err
	}
	return reserved, nil
}

func providerUninstallRestoreErrorV1(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("restore deployment after failed removal: %w", err)
}
