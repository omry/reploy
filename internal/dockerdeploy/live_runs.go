package dockerdeploy

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

type LiveRunStopResultV1 struct {
	Found    bool
	Run      deploy.LiveRunV1
	Recovery deploy.LiveRunRecoveryV1
}

type liveRunsBackendV1 struct {
	acquire         func(context.Context, string) (*deploy.OperationLock, error)
	removeContainer commandRunner
}

func ListLiveRunsV1(ctx context.Context, deploymentDir string) ([]deploy.LiveRunV1, error) {
	return listLiveRunsV1(ctx, deploymentDir, nil, liveRunsBackendV1{acquire: deploy.AcquireOperationLock})
}

func ListLiveRunsWithNoticeV1(ctx context.Context, deploymentDir string, notice io.Writer) ([]deploy.LiveRunV1, error) {
	return listLiveRunsV1(ctx, deploymentDir, notice, liveRunsBackendV1{acquire: deploy.AcquireOperationLock})
}

func StopLiveRunV1(ctx context.Context, deploymentDir string, id string, dockerPreflightTimeout time.Duration) (LiveRunStopResultV1, error) {
	return stopLiveRunV1(ctx, deploymentDir, id, dockerPreflightTimeout, liveRunsBackendV1{
		acquire:         deploy.AcquireOperationLock,
		removeContainer: runCommand,
	})
}

func listLiveRunsV1(ctx context.Context, deploymentDir string, notice io.Writer, backend liveRunsBackendV1) (runs []deploy.LiveRunV1, err error) {
	if ctx == nil {
		return nil, fmt.Errorf("list live runs requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if deploymentDir == "" {
		return nil, fmt.Errorf("list live runs requires a deployment directory")
	}
	if backend.acquire == nil {
		return nil, fmt.Errorf("list live runs requires a complete backend")
	}
	dir, err := filepath.Abs(deploymentDir)
	if err != nil {
		return nil, fmt.Errorf("resolve live run deployment directory: %w", err)
	}
	operation, err := backend.acquire(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if unlockErr := operation.Unlock(); err == nil && unlockErr != nil {
			err = unlockErr
		}
	}()
	if _, err := recoverLiveRunQueueV1(ctx, operation, notice, nil); err != nil {
		return nil, err
	}
	queue, _, err := operation.ReadLiveRunQueueV1()
	if err != nil {
		return nil, err
	}
	runs = make([]deploy.LiveRunV1, 0, len(queue.Runs))
	for _, entry := range queue.Runs {
		if entry.Kind != deploy.LiveRunKindControlV1 {
			if entry.Status == deploy.LiveRunStatusReadyV1 {
				entry.Status = deploy.LiveRunStatusWaitingV1
			}
			runs = append(runs, entry)
		}
	}
	return runs, nil
}

func stopLiveRunV1(
	ctx context.Context,
	deploymentDir string,
	id string,
	dockerPreflightTimeout time.Duration,
	backend liveRunsBackendV1,
) (result LiveRunStopResultV1, err error) {
	if ctx == nil {
		return result, fmt.Errorf("stop live run requires a context")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if deploymentDir == "" {
		return result, fmt.Errorf("stop live run requires a deployment directory")
	}
	if err := deploy.ValidateLiveRunIDV1(id); err != nil {
		return result, err
	}
	if backend.acquire == nil || backend.removeContainer == nil {
		return result, fmt.Errorf("stop live run requires a complete backend")
	}
	dir, err := filepath.Abs(deploymentDir)
	if err != nil {
		return result, fmt.Errorf("resolve live run deployment directory: %w", err)
	}
	operation, err := backend.acquire(ctx, dir)
	if err != nil {
		return result, err
	}
	defer func() {
		if unlockErr := operation.Unlock(); err == nil && unlockErr != nil {
			err = unlockErr
		}
	}()
	result.Recovery, err = recoverLiveRunQueueV1(ctx, operation, nil, backend.removeContainer)
	if err != nil {
		return result, err
	}
	queue, _, err := operation.ReadLiveRunQueueV1()
	if err != nil {
		return result, err
	}
	run, found := findLiveRunV1(queue, id)
	if !found {
		return result, nil
	}
	result.Found = true
	result.Run = run
	if run.Status == deploy.LiveRunStatusActiveV1 {
		for _, container := range liveRunContainerTargetsV1(queue, run) {
			removeErr := backend.removeContainer(
				TemporaryContainerCleanupCommand(container),
				RunOptions{Context: ctx, DockerPreflightTimeout: dockerPreflightTimeout},
			)
			if removeErr != nil && !isMissingContainerCleanupError(removeErr) {
				return result, fmt.Errorf("stop live run container %q: %w", container, removeErr)
			}
		}
	}
	_, removed, err := operation.RemoveLiveRunV1(id)
	if err != nil {
		return result, err
	}
	if !removed {
		return result, fmt.Errorf("live run %q disappeared while the operation lock was held", id)
	}
	if result.Run.Status == deploy.LiveRunStatusReadyV1 {
		result.Run.Status = deploy.LiveRunStatusWaitingV1
	}
	return result, nil
}

// liveRunContainerTargetsV1 returns every exact container owned by a live run.
// Workload-first ordering leaves the controller available to observe workload
// termination for as long as possible. Controlled-session ownership remains
// durable until its supervisor or recovery verifies complete cleanup.
func liveRunContainerTargetsV1(queue deploy.LiveRunQueueV1, run deploy.LiveRunV1) []string {
	targets := make([]string, 0, 2)
	if run.Container != "" {
		targets = append(targets, run.Container)
	}
	for _, ownership := range queue.ControlledSessions {
		if ownership.LiveRunID == run.ID {
			if ownership.Workload.ID != "" {
				targets = append(targets, ownership.Workload.ID)
			}
			if ownership.Controller.ID != "" {
				targets = append(targets, ownership.Controller.ID)
			}
			break
		}
	}
	return targets
}
