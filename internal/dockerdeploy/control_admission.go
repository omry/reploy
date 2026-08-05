package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

type controlAdmissionBackendV1 struct {
	acquire         func(context.Context, string) (*deploy.OperationLock, error)
	wait            func(context.Context) error
	removeContainer commandRunner
}

// AwaitControlAdmissionV1 takes ownership of operation. On success it returns
// the operation lock held and the marker active. On failure it releases the
// lock and removes the caller's waiting marker whenever possible.
func AwaitControlAdmissionV1(
	ctx context.Context,
	deploymentDir string,
	operation *deploy.OperationLock,
	candidate deploy.ControlMarkerV1,
	wait bool,
) (*deploy.OperationLock, error) {
	return AwaitControlAdmissionWithNoticeV1(ctx, deploymentDir, operation, candidate, wait, nil)
}

func AwaitControlAdmissionWithNoticeV1(
	ctx context.Context,
	deploymentDir string,
	operation *deploy.OperationLock,
	candidate deploy.ControlMarkerV1,
	wait bool,
	notice io.Writer,
) (*deploy.OperationLock, error) {
	return awaitControlAdmissionV1(ctx, deploymentDir, operation, candidate, wait, notice, controlAdmissionBackendV1{
		acquire: deploy.AcquireOperationLock,
		wait: func(ctx context.Context) error {
			timer := time.NewTimer(liveRunAdmissionPollIntervalV1)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
		removeContainer: runCommandWithoutDockerPreflight,
	})
}

func awaitControlAdmissionV1(
	ctx context.Context,
	deploymentDir string,
	operation *deploy.OperationLock,
	candidate deploy.ControlMarkerV1,
	wait bool,
	notice io.Writer,
	backend controlAdmissionBackendV1,
) (*deploy.OperationLock, error) {
	if ctx == nil {
		return releaseControlAdmissionLockV1(operation, fmt.Errorf("control admission requires a context"))
	}
	if err := ctx.Err(); err != nil {
		return releaseControlAdmissionLockV1(operation, err)
	}
	if deploymentDir == "" {
		return releaseControlAdmissionLockV1(operation, fmt.Errorf("control admission requires a deployment directory"))
	}
	if operation == nil {
		return nil, fmt.Errorf("control admission requires an operation lock")
	}
	absoluteDir, err := filepath.Abs(deploymentDir)
	if err != nil {
		return releaseControlAdmissionLockV1(operation, fmt.Errorf("resolve control admission deployment directory: %w", err))
	}
	if filepath.Dir(filepath.Dir(operation.Path())) != absoluteDir {
		return releaseControlAdmissionLockV1(operation, fmt.Errorf(
			"control admission operation lock does not belong to deployment %q", absoluteDir,
		))
	}
	if backend.acquire == nil || backend.wait == nil {
		return releaseControlAdmissionLockV1(operation, fmt.Errorf("control admission requires a complete backend"))
	}
	if err := operation.RequireQueueEntryLeaseHeldV1(candidate.ID); err != nil {
		return releaseControlAdmissionLockV1(operation, fmt.Errorf("control admission ownership: %w", err))
	}
	if _, err := recoverLiveRunQueueV1(ctx, operation, notice, backend.removeContainer); err != nil {
		return releaseControlAdmissionLockV1(operation, err)
	}
	status, err := operation.AdmitControlMarkerV1(candidate, wait)
	if err != nil {
		return releaseControlAdmissionLockV1(operation, err)
	}
	if status == deploy.LiveRunStatusActiveV1 {
		return operation, nil
	}
	if status != deploy.LiveRunStatusWaitingV1 {
		return cancelControlAdmissionV1(ctx, absoluteDir, operation, candidate, backend,
			fmt.Errorf("control admission returned unsupported status %q", status))
	}
	if err := writeAdmissionWaitNoticeV1(operation, candidate.ID, notice); err != nil {
		return cancelControlAdmissionV1(ctx, absoluteDir, operation, candidate, backend,
			fmt.Errorf("describe lifecycle wait: %w", err))
	}
	if err := operation.Unlock(); err != nil {
		return cancelControlAdmissionV1(ctx, absoluteDir, nil, candidate, backend,
			fmt.Errorf("release operation lock while waiting for control admission: %w", err))
	}
	operation = nil

	for {
		if err := backend.wait(ctx); err != nil {
			return cancelControlAdmissionV1(ctx, absoluteDir, nil, candidate, backend, err)
		}
		operation, err = backend.acquire(ctx, absoluteDir)
		if err != nil {
			return cancelControlAdmissionV1(ctx, absoluteDir, nil, candidate, backend, err)
		}
		if _, err := recoverLiveRunQueueV1(ctx, operation, notice, backend.removeContainer); err != nil {
			return cancelControlAdmissionV1(ctx, absoluteDir, operation, candidate, backend, err)
		}
		queue, _, err := operation.ReadLiveRunQueueV1()
		if err != nil {
			return cancelControlAdmissionV1(ctx, absoluteDir, operation, candidate, backend, err)
		}
		marker, found := findControlMarkerV1(queue, candidate.ID)
		if !found {
			return releaseControlAdmissionLockV1(operation, fmt.Errorf(
				"control operation %q is no longer waiting; retry the command", candidate.Operation,
			))
		}
		if marker.Status == deploy.LiveRunStatusActiveV1 {
			return operation, nil
		}
		if err := ctx.Err(); err != nil {
			return cancelControlAdmissionV1(ctx, absoluteDir, operation, candidate, backend, err)
		}
		switch marker.Status {
		case deploy.LiveRunStatusReadyV1:
			// Claiming ready is the admission linearization point. Cancellation
			// observed before this transition removes the unstarted reservation;
			// cancellation after it belongs to admitted lifecycle cleanup.
			if err := operation.ActivateReadyControlMarkerV1(candidate.ID); err != nil {
				return cancelControlAdmissionV1(ctx, absoluteDir, operation, candidate, backend,
					fmt.Errorf("claim ready control admission: %w", err))
			}
			return operation, nil
		case deploy.LiveRunStatusWaitingV1:
			if err := operation.Unlock(); err != nil {
				return cancelControlAdmissionV1(ctx, absoluteDir, nil, candidate, backend,
					fmt.Errorf("release operation lock while waiting for control admission: %w", err))
			}
			operation = nil
		default:
			return cancelControlAdmissionV1(ctx, absoluteDir, operation, candidate, backend,
				fmt.Errorf("control marker %q has unsupported status %q", candidate.ID, marker.Status))
		}
	}
}

func CompleteControlAdmissionV1(operation *deploy.OperationLock, markerID string, lease *deploy.ControlLeaseV1) error {
	if operation == nil {
		return fmt.Errorf("complete control admission requires an operation lock")
	}
	_, removed, removeErr := operation.RemoveControlMarkerV1(markerID)
	if removeErr == nil && !removed {
		removeErr = fmt.Errorf("control marker %q is not outstanding", markerID)
	}
	leaseErr := lease.Release()
	unlockErr := operation.Unlock()
	if removeErr != nil {
		return errors.Join(fmt.Errorf("complete control admission: %w", removeErr), leaseErr, unlockErr)
	}
	if leaseErr != nil || unlockErr != nil {
		return errors.Join(
			wrapControlCompletionErrorV1("release lifecycle queue ownership", leaseErr),
			wrapControlCompletionErrorV1("release operation lock after control admission", unlockErr),
		)
	}
	return nil
}

func wrapControlCompletionErrorV1(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", action, err)
}

func cancelControlAdmissionV1(
	ctx context.Context,
	deploymentDir string,
	operation *deploy.OperationLock,
	candidate deploy.ControlMarkerV1,
	backend controlAdmissionBackendV1,
	cause error,
) (*deploy.OperationLock, error) {
	if operation == nil {
		cleanupContext := context.Background()
		if ctx != nil {
			cleanupContext = context.WithoutCancel(ctx)
		}
		var err error
		operation, err = backend.acquire(cleanupContext, deploymentDir)
		if err != nil {
			return nil, fmt.Errorf("%w; remove queued control marker %q: %v", cause, candidate.ID, err)
		}
	}
	_, removed, removeErr := operation.RemoveControlMarkerV1(candidate.ID)
	unlockErr := operation.Unlock()
	if removeErr != nil {
		if unlockErr != nil {
			return nil, fmt.Errorf("%w; remove queued control marker %q: %v; release operation lock: %v", cause, candidate.ID, removeErr, unlockErr)
		}
		return nil, fmt.Errorf("%w; remove queued control marker %q: %v", cause, candidate.ID, removeErr)
	}
	if unlockErr != nil {
		return nil, fmt.Errorf("%w; release operation lock: %v", cause, unlockErr)
	}
	if removed {
		return nil, fmt.Errorf(
			"%w; canceled queued lifecycle operation %q (%s)",
			cause, candidate.Operation, candidate.ID,
		)
	}
	return nil, cause
}

func releaseControlAdmissionLockV1(operation *deploy.OperationLock, cause error) (*deploy.OperationLock, error) {
	if operation == nil {
		return nil, cause
	}
	if err := operation.Unlock(); err != nil {
		return nil, fmt.Errorf("%w; release operation lock: %v", cause, err)
	}
	return nil, cause
}

func findControlMarkerV1(queue deploy.LiveRunQueueV1, id string) (deploy.ControlMarkerV1, bool) {
	for _, marker := range deploy.ControlMarkersV1(queue) {
		if marker.ID == id {
			return marker, true
		}
	}
	return deploy.ControlMarkerV1{}, false
}
