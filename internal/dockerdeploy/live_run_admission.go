package dockerdeploy

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

const liveRunAdmissionPollIntervalV1 = 25 * time.Millisecond

type liveRunAdmissionBackendV1 struct {
	acquire func(context.Context, string) (*deploy.OperationLock, error)
	wait    func(context.Context) error
}

// AwaitLiveRunAdmissionV1 takes ownership of operation. On success it returns
// the operation lock held for the admitted active run. On failure it releases
// the lock and removes a queued entry before returning whenever possible.
func AwaitLiveRunAdmissionV1(
	ctx context.Context,
	deploymentDir string,
	operation *deploy.OperationLock,
	candidate deploy.LiveRunV1,
	wait bool,
) (*deploy.OperationLock, error) {
	return awaitLiveRunAdmissionV1(ctx, deploymentDir, operation, candidate, wait, liveRunAdmissionBackendV1{
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
	})
}

func awaitLiveRunAdmissionV1(
	ctx context.Context,
	deploymentDir string,
	operation *deploy.OperationLock,
	candidate deploy.LiveRunV1,
	wait bool,
	backend liveRunAdmissionBackendV1,
) (*deploy.OperationLock, error) {
	if ctx == nil {
		return releaseLiveRunAdmissionLockV1(operation, fmt.Errorf("live run admission requires a context"))
	}
	if err := ctx.Err(); err != nil {
		return releaseLiveRunAdmissionLockV1(operation, err)
	}
	if deploymentDir == "" {
		return releaseLiveRunAdmissionLockV1(operation, fmt.Errorf("live run admission requires a deployment directory"))
	}
	if operation == nil {
		return nil, fmt.Errorf("live run admission requires an operation lock")
	}
	absoluteDir, err := filepath.Abs(deploymentDir)
	if err != nil {
		return releaseLiveRunAdmissionLockV1(operation, fmt.Errorf("resolve live run deployment directory: %w", err))
	}
	if filepath.Dir(filepath.Dir(operation.Path())) != absoluteDir {
		return releaseLiveRunAdmissionLockV1(operation, fmt.Errorf(
			"live run operation lock does not belong to deployment %q", absoluteDir,
		))
	}
	deploymentDir = absoluteDir
	if backend.acquire == nil || backend.wait == nil {
		return releaseLiveRunAdmissionLockV1(operation, fmt.Errorf("live run admission requires a complete backend"))
	}
	if _, _, err := operation.RecoverAbandonedControlMarkerV1(); err != nil {
		return releaseLiveRunAdmissionLockV1(operation, err)
	}
	status, err := operation.AdmitLiveRunV1(candidate, wait)
	if err != nil {
		return releaseLiveRunAdmissionLockV1(operation, err)
	}
	if status == deploy.LiveRunStatusActiveV1 {
		return operation, nil
	}
	if status != deploy.LiveRunStatusWaitingV1 {
		return cancelLiveRunAdmissionV1(ctx, deploymentDir, operation, candidate.ID, backend,
			fmt.Errorf("live run admission returned unsupported status %q", status))
	}
	if err := operation.Unlock(); err != nil {
		return cancelLiveRunAdmissionV1(ctx, deploymentDir, nil, candidate.ID, backend,
			fmt.Errorf("release operation lock while waiting: %w", err))
	}
	operation = nil

	for {
		if err := backend.wait(ctx); err != nil {
			return cancelLiveRunAdmissionV1(ctx, deploymentDir, nil, candidate.ID, backend, err)
		}
		operation, err = backend.acquire(ctx, deploymentDir)
		if err != nil {
			return cancelLiveRunAdmissionV1(ctx, deploymentDir, nil, candidate.ID, backend, err)
		}
		if _, _, err := operation.RecoverAbandonedControlMarkerV1(); err != nil {
			return cancelLiveRunAdmissionV1(ctx, deploymentDir, operation, candidate.ID, backend, err)
		}
		queue, _, err := operation.ReadLiveRunQueueV1()
		if err != nil {
			return cancelLiveRunAdmissionV1(ctx, deploymentDir, operation, candidate.ID, backend, err)
		}
		run, found := findLiveRunV1(queue, candidate.ID)
		if !found {
			return releaseLiveRunAdmissionLockV1(operation, fmt.Errorf(
				"live run %q is no longer outstanding; it may have been stopped", candidate.ID,
			))
		}
		switch run.Status {
		case deploy.LiveRunStatusActiveV1:
			return operation, nil
		case deploy.LiveRunStatusWaitingV1:
			if err := operation.Unlock(); err != nil {
				return cancelLiveRunAdmissionV1(ctx, deploymentDir, nil, candidate.ID, backend,
					fmt.Errorf("release operation lock while waiting: %w", err))
			}
			operation = nil
		default:
			return cancelLiveRunAdmissionV1(ctx, deploymentDir, operation, candidate.ID, backend,
				fmt.Errorf("live run %q has unsupported status %q", candidate.ID, run.Status))
		}
	}
}

func cancelLiveRunAdmissionV1(
	ctx context.Context,
	deploymentDir string,
	operation *deploy.OperationLock,
	id string,
	backend liveRunAdmissionBackendV1,
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
			return nil, fmt.Errorf("%w; remove queued live run %q: %v", cause, id, err)
		}
	}
	_, _, removeErr := operation.RemoveLiveRunV1(id)
	unlockErr := operation.Unlock()
	if removeErr != nil {
		if unlockErr != nil {
			return nil, fmt.Errorf("%w; remove queued live run %q: %v; release operation lock: %v", cause, id, removeErr, unlockErr)
		}
		return nil, fmt.Errorf("%w; remove queued live run %q: %v", cause, id, removeErr)
	}
	if unlockErr != nil {
		return nil, fmt.Errorf("%w; release operation lock: %v", cause, unlockErr)
	}
	return nil, cause
}

func releaseLiveRunAdmissionLockV1(operation *deploy.OperationLock, cause error) (*deploy.OperationLock, error) {
	if operation == nil {
		return nil, cause
	}
	if err := operation.Unlock(); err != nil {
		return nil, fmt.Errorf("%w; release operation lock: %v", cause, err)
	}
	return nil, cause
}

func findLiveRunV1(queue deploy.LiveRunQueueV1, id string) (deploy.LiveRunV1, bool) {
	for _, run := range queue.Runs {
		if run.ID == id {
			return run, true
		}
	}
	return deploy.LiveRunV1{}, false
}
