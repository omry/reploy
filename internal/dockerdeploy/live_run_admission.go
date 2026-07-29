package dockerdeploy

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

const liveRunAdmissionPollIntervalV1 = 25 * time.Millisecond

type liveRunAdmissionBackendV1 struct {
	acquire         func(context.Context, string) (*deploy.OperationLock, error)
	wait            func(context.Context) error
	removeContainer commandRunner
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
	return AwaitLiveRunAdmissionWithNoticeV1(ctx, deploymentDir, operation, candidate, wait, nil)
}

func AwaitLiveRunAdmissionWithNoticeV1(
	ctx context.Context,
	deploymentDir string,
	operation *deploy.OperationLock,
	candidate deploy.LiveRunV1,
	wait bool,
	notice io.Writer,
) (*deploy.OperationLock, error) {
	return awaitLiveRunAdmissionV1(ctx, deploymentDir, operation, candidate, wait, notice, liveRunAdmissionBackendV1{
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

func awaitLiveRunAdmissionV1(
	ctx context.Context,
	deploymentDir string,
	operation *deploy.OperationLock,
	candidate deploy.LiveRunV1,
	wait bool,
	notice io.Writer,
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
	if err := operation.RequireQueueEntryLeaseHeldV1(candidate.ID); err != nil {
		return releaseLiveRunAdmissionLockV1(operation, fmt.Errorf("live run admission ownership: %w", err))
	}
	if _, err := recoverLiveRunQueueV1(ctx, operation, notice, backend.removeContainer); err != nil {
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
		return cancelLiveRunAdmissionV1(ctx, deploymentDir, operation, candidate, backend,
			fmt.Errorf("live run admission returned unsupported status %q", status))
	}
	if err := writeAdmissionWaitNoticeV1(operation, candidate.ID, notice); err != nil {
		return cancelLiveRunAdmissionV1(ctx, deploymentDir, operation, candidate, backend,
			fmt.Errorf("describe live run wait: %w", err))
	}
	if err := operation.Unlock(); err != nil {
		return cancelLiveRunAdmissionV1(ctx, deploymentDir, nil, candidate, backend,
			fmt.Errorf("release operation lock while waiting: %w", err))
	}
	operation = nil

	for {
		if err := backend.wait(ctx); err != nil {
			return cancelLiveRunAdmissionV1(ctx, deploymentDir, nil, candidate, backend, err)
		}
		operation, err = backend.acquire(ctx, deploymentDir)
		if err != nil {
			return cancelLiveRunAdmissionV1(ctx, deploymentDir, nil, candidate, backend, err)
		}
		if _, err := recoverLiveRunQueueV1(ctx, operation, notice, backend.removeContainer); err != nil {
			return cancelLiveRunAdmissionV1(ctx, deploymentDir, operation, candidate, backend, err)
		}
		queue, _, err := operation.ReadLiveRunQueueV1()
		if err != nil {
			return cancelLiveRunAdmissionV1(ctx, deploymentDir, operation, candidate, backend, err)
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
				return cancelLiveRunAdmissionV1(ctx, deploymentDir, nil, candidate, backend,
					fmt.Errorf("release operation lock while waiting: %w", err))
			}
			operation = nil
		default:
			return cancelLiveRunAdmissionV1(ctx, deploymentDir, operation, candidate, backend,
				fmt.Errorf("live run %q has unsupported status %q", candidate.ID, run.Status))
		}
	}
}

func writeAdmissionWaitNoticeV1(operation *deploy.OperationLock, candidateID string, output io.Writer) error {
	if output == nil {
		return nil
	}
	queue, _, err := operation.ReadLiveRunQueueV1()
	if err != nil {
		return err
	}
	candidateIndex := -1
	for index, entry := range queue.Runs {
		if entry.ID == candidateID {
			candidateIndex = index
			break
		}
	}
	if candidateIndex < 0 {
		return fmt.Errorf("waiting operation is not in the queue")
	}
	ahead := queue.Runs[:candidateIndex]
	active := deploy.LiveRunV1{}
	for _, entry := range ahead {
		if entry.Status == deploy.LiveRunStatusActiveV1 {
			active = entry
			break
		}
	}
	label := admissionEntryLabelV1(active)
	detail := ""
	if len(active.WritablePaths) != 0 {
		detail = " (shared writable mounts: " + strings.Join(active.WritablePaths, ", ") + ")"
	}
	if len(ahead) <= 1 {
		fmt.Fprintf(output, "Waiting for %s to finish%s.\n", label, detail)
	} else {
		fmt.Fprintf(output, "Waiting behind %d operations; %s is blocking this command%s.\n", len(ahead), label, detail)
	}
	fmt.Fprintln(output, "Ctrl-C cancels this wait without affecting the active command.")
	return nil
}

func admissionEntryLabelV1(entry deploy.LiveRunV1) string {
	switch entry.Kind {
	case deploy.LiveRunKindShellV1:
		return "active shell"
	case deploy.LiveRunKindAppV1:
		return fmt.Sprintf("active app command %q", entry.Name)
	case deploy.LiveRunKindControlV1:
		return fmt.Sprintf("active %s operation", entry.Name)
	default:
		return "active operation"
	}
}

func cancelLiveRunAdmissionV1(
	ctx context.Context,
	deploymentDir string,
	operation *deploy.OperationLock,
	candidate deploy.LiveRunV1,
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
			return nil, fmt.Errorf("%w; remove queued live run %q: %v", cause, candidate.ID, err)
		}
	}
	_, removed, removeErr := operation.RemoveLiveRunV1(candidate.ID)
	unlockErr := operation.Unlock()
	if removeErr != nil {
		if unlockErr != nil {
			return nil, fmt.Errorf("%w; remove queued live run %q: %v; release operation lock: %v", cause, candidate.ID, removeErr, unlockErr)
		}
		return nil, fmt.Errorf("%w; remove queued live run %q: %v", cause, candidate.ID, removeErr)
	}
	if unlockErr != nil {
		return nil, fmt.Errorf("%w; release operation lock: %v", cause, unlockErr)
	}
	if removed {
		return nil, fmt.Errorf(
			"%w; canceled queued %s %q (%s)",
			cause, admissionRecoveryKindV1(candidate), candidate.Name, candidate.ID,
		)
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
