package dockerdeploy

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

type ControlAdmissionModeV1 string

const (
	ControlAdmissionImmediateV1 ControlAdmissionModeV1 = "immediate"
	ControlAdmissionWaitV1      ControlAdmissionModeV1 = "wait"
	ControlAdmissionDrainV1     ControlAdmissionModeV1 = "drain"
	ControlAdmissionForceV1     ControlAdmissionModeV1 = "force"
)

type ControlAdmissionInputV1 struct {
	Operation              deploy.ControlOperationV1
	GenerationReference    string
	Mode                   ControlAdmissionModeV1
	DockerPreflightTimeout time.Duration
	Notice                 io.Writer
}

type AdmittedControlV1 struct {
	Operation    *deploy.OperationLock
	Marker       deploy.ControlMarkerV1
	Lease        *deploy.ControlLeaseV1
	CanceledRuns []deploy.LiveRunV1
	StoppedRuns  []deploy.LiveRunV1
}

type controlOperationAdmissionBackendV1 struct {
	newID           func() (string, error)
	pause           func(context.Context, time.Duration) error
	await           func(context.Context, string, *deploy.OperationLock, deploy.ControlMarkerV1, bool, io.Writer) (*deploy.OperationLock, error)
	removeContainer commandRunner
}

const controlDisruptionDelayV1 = 3 * time.Second

func AdmitControlOperationV1(
	ctx context.Context,
	deploymentDir string,
	operation *deploy.OperationLock,
	input ControlAdmissionInputV1,
) (AdmittedControlV1, error) {
	return admitControlOperationV1(ctx, deploymentDir, operation, input, controlOperationAdmissionBackendV1{
		newID:           deploy.NewControlMarkerIDV1,
		pause:           waitForControlDisruptionV1,
		await:           AwaitControlAdmissionWithNoticeV1,
		removeContainer: runCommand,
	})
}

func admitControlOperationV1(
	ctx context.Context,
	deploymentDir string,
	operation *deploy.OperationLock,
	input ControlAdmissionInputV1,
	backend controlOperationAdmissionBackendV1,
) (AdmittedControlV1, error) {
	result := AdmittedControlV1{}
	if operation == nil {
		return result, fmt.Errorf("admit control operation requires an operation lock")
	}
	if ctx == nil {
		_, err := releaseControlAdmissionLockV1(operation, fmt.Errorf("admit control operation requires a context"))
		return result, err
	}
	if err := ctx.Err(); err != nil {
		_, err = releaseControlAdmissionLockV1(operation, err)
		return result, err
	}
	if deploymentDir == "" {
		_, err := releaseControlAdmissionLockV1(operation, fmt.Errorf("admit control operation requires a deployment directory"))
		return result, err
	}
	if !validControlAdmissionModeV1(input.Mode) {
		_, err := releaseControlAdmissionLockV1(operation, fmt.Errorf("control admission mode must be immediate, wait, drain, or force"))
		return result, err
	}
	if backend.newID == nil || backend.pause == nil || backend.await == nil || backend.removeContainer == nil {
		_, err := releaseControlAdmissionLockV1(operation, fmt.Errorf("admit control operation requires a complete backend"))
		return result, err
	}
	id, err := backend.newID()
	if err != nil {
		_, releaseErr := releaseControlAdmissionLockV1(operation, err)
		return result, releaseErr
	}
	result.Marker = deploy.ControlMarkerV1{
		ID: id, Operation: input.Operation,
		GenerationReference: input.GenerationReference,
	}
	if _, _, err := deploy.AdmitControlMarkerV1(deploy.NewLiveRunQueueV1(), result.Marker, false); err != nil {
		_, releaseErr := releaseControlAdmissionLockV1(operation, err)
		return result, releaseErr
	}
	result.Lease, err = operation.AcquireControlLeaseV1(result.Marker.ID)
	if err != nil {
		_, releaseErr := releaseControlAdmissionLockV1(operation, err)
		return result, releaseErr
	}
	releaseLeaseOnError := func(cause error) error {
		if leaseErr := result.Lease.Release(); leaseErr != nil {
			return fmt.Errorf("%w; release lifecycle queue ownership: %v", cause, leaseErr)
		}
		return cause
	}
	if input.Mode == ControlAdmissionForceV1 {
		if _, err := recoverLiveRunQueueV1(ctx, operation, input.Notice, backend.removeContainer); err != nil {
			_, releaseErr := releaseControlAdmissionLockV1(operation, err)
			return result, releaseLeaseOnError(releaseErr)
		}
		queue, _, err := operation.ReadLiveRunQueueV1()
		if err != nil {
			_, releaseErr := releaseControlAdmissionLockV1(operation, err)
			return result, releaseLeaseOnError(releaseErr)
		}
		if len(deploy.ControlMarkersV1(queue)) != 0 {
			_, releaseErr := releaseControlAdmissionLockV1(operation, deploy.ErrLiveRunConflict)
			return result, releaseLeaseOnError(releaseErr)
		}
		if input.Operation == deploy.ControlOperationStopV1 || input.Operation == deploy.ControlOperationRestartV1 {
			active, waiting := controlDisruptionCountsV1(queue)
			if active+waiting != 0 {
				writeControlDisruptionNoticeV1(input.Notice, input.Operation, active, waiting)
				if err := backend.pause(ctx, controlDisruptionDelayV1); err != nil {
					_, releaseErr := releaseControlAdmissionLockV1(operation, fmt.Errorf(
						"%s canceled before stopping outstanding jobs: %w", input.Operation, err,
					))
					return result, releaseLeaseOnError(releaseErr)
				}
			}
		}
	}

	if input.Mode == ControlAdmissionDrainV1 || input.Mode == ControlAdmissionForceV1 {
		_, result.CanceledRuns, err = operation.CancelWaitingLiveRunsV1()
		if err != nil {
			_, releaseErr := releaseControlAdmissionLockV1(operation, err)
			return result, releaseLeaseOnError(releaseErr)
		}
	}
	if input.Mode == ControlAdmissionForceV1 {
		result.StoppedRuns, err = stopActiveLiveRunsForControlV1(ctx, operation, input.DockerPreflightTimeout, backend.removeContainer)
		if err != nil {
			_, releaseErr := releaseControlAdmissionLockV1(operation, err)
			return result, releaseLeaseOnError(releaseErr)
		}
	}

	wait := input.Mode == ControlAdmissionWaitV1 || input.Mode == ControlAdmissionDrainV1
	result.Operation, err = backend.await(ctx, deploymentDir, operation, result.Marker, wait, input.Notice)
	if err != nil {
		return result, releaseLeaseOnError(err)
	}
	return result, nil
}

func controlDisruptionCountsV1(queue deploy.LiveRunQueueV1) (active int, waiting int) {
	for _, entry := range queue.Runs {
		if entry.Kind == deploy.LiveRunKindControlV1 {
			continue
		}
		switch entry.Status {
		case deploy.LiveRunStatusActiveV1:
			active++
		case deploy.LiveRunStatusWaitingV1:
			waiting++
		}
	}
	return active, waiting
}

func writeControlDisruptionNoticeV1(output io.Writer, operation deploy.ControlOperationV1, active int, waiting int) {
	if output == nil {
		return
	}
	fmt.Fprintf(output,
		"warning: %s will stop %d active %s and cancel %d waiting %s in 3 seconds. Press Ctrl-C to abort; use `--wait` to let active jobs finish.\n",
		operation, active, pluralizedJobV1(active), waiting, pluralizedJobV1(waiting),
	)
}

func pluralizedJobV1(count int) string {
	if count == 1 {
		return "job"
	}
	return "jobs"
}

func controlWaitNoticeWriterV1(options RunOptions) io.Writer {
	if options.Progress != nil {
		return options.Progress
	}
	return options.Stderr
}

func waitForControlDisruptionV1(ctx context.Context, delay time.Duration) error {
	if ctx == nil {
		return fmt.Errorf("disruption pause requires a context")
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case received := <-signals:
		return fmt.Errorf("interrupted by %s", received)
	case <-timer.C:
		return nil
	}
}

func validControlAdmissionModeV1(mode ControlAdmissionModeV1) bool {
	switch mode {
	case ControlAdmissionImmediateV1, ControlAdmissionWaitV1, ControlAdmissionDrainV1, ControlAdmissionForceV1:
		return true
	default:
		return false
	}
}

func stopActiveLiveRunsForControlV1(
	ctx context.Context,
	operation *deploy.OperationLock,
	dockerPreflightTimeout time.Duration,
	removeContainer commandRunner,
) ([]deploy.LiveRunV1, error) {
	queue, _, err := operation.ReadLiveRunQueueV1()
	if err != nil {
		return nil, err
	}
	active := []deploy.LiveRunV1{}
	for _, entry := range queue.Runs {
		if entry.Kind != deploy.LiveRunKindControlV1 && entry.Status == deploy.LiveRunStatusActiveV1 {
			active = append(active, entry)
		}
	}
	stopped := []deploy.LiveRunV1{}
	for _, run := range active {
		if run.Container != "" {
			err := removeContainer(
				TemporaryContainerStopCommand(run.Container),
				RunOptions{Context: ctx, DockerPreflightTimeout: dockerPreflightTimeout},
			)
			if err != nil && !isMissingContainerCleanupError(err) {
				return stopped, fmt.Errorf("stop live run container %q: %w", run.Container, err)
			}
		}
		_, removed, err := operation.RemoveLiveRunV1(run.ID)
		if err != nil {
			return stopped, err
		}
		if !removed {
			return stopped, fmt.Errorf("active live run %q disappeared while the operation lock was held", run.ID)
		}
		stopped = append(stopped, run)
	}
	return stopped, nil
}
