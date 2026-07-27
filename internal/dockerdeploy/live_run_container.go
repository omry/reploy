package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/omry/reploy/internal/deploy"
)

type admittedTransientContainerBackendV1 struct {
	acquire      func(context.Context, string) (*deploy.OperationLock, error)
	create       commandRunner
	followup     temporaryCommandRunner
	runTemporary func(temporaryCommandRunner, CommandSpec, CommandSpec, RunOptions) error
}

var ErrLiveRunStoppedV1 = errors.New("live run was stopped")

// RunAdmittedTransientContainerV1 takes ownership of operation. It creates and
// records the container while the lock is held, releases the lock for attached
// execution, and reacquires it to remove the live entry on every exit path.
func RunAdmittedTransientContainerV1(
	ctx context.Context,
	deploymentDir string,
	operation *deploy.OperationLock,
	runID string,
	execution TransientContainerExecutionV1,
	options RunOptions,
) error {
	return runAdmittedTransientContainerV1(ctx, deploymentDir, operation, runID, execution, options, admittedTransientContainerBackendV1{
		acquire:      deploy.AcquireOperationLock,
		create:       runCommand,
		followup:     runCommandWithoutDockerPreflight,
		runTemporary: runTemporaryContainerCommand,
	})
}

func runAdmittedTransientContainerV1(
	ctx context.Context,
	deploymentDir string,
	operation *deploy.OperationLock,
	runID string,
	execution TransientContainerExecutionV1,
	options RunOptions,
	backend admittedTransientContainerBackendV1,
) error {
	if operation == nil {
		return fmt.Errorf("run admitted transient container requires an operation lock")
	}
	if deploymentDir == "" {
		return releaseAdmittedTransientOperationV1(operation, fmt.Errorf("run admitted transient container requires a deployment directory"))
	}
	absoluteDir, err := filepath.Abs(deploymentDir)
	if err != nil {
		return releaseAdmittedTransientOperationV1(operation, fmt.Errorf("resolve admitted transient deployment directory: %w", err))
	}
	if filepath.Dir(filepath.Dir(operation.Path())) != absoluteDir {
		return releaseAdmittedTransientOperationV1(operation, fmt.Errorf("admitted transient operation lock does not belong to deployment %q", absoluteDir))
	}
	if ctx == nil {
		return removeAdmittedTransientBeforeCreateV1(operation, runID, fmt.Errorf("run admitted transient container requires a context"))
	}
	if err := ctx.Err(); err != nil {
		return removeAdmittedTransientBeforeCreateV1(operation, runID, err)
	}
	if err := ValidateTransientContainerExecutionV1(execution, runID); err != nil {
		return removeAdmittedTransientBeforeCreateV1(operation, runID, err)
	}
	if backend.acquire == nil || backend.create == nil || backend.followup == nil || backend.runTemporary == nil {
		return removeAdmittedTransientBeforeCreateV1(operation, runID, fmt.Errorf("run admitted transient container requires a complete backend"))
	}

	createOptions := options
	createOptions.Context = ctx
	createOptions.Stdin = nil
	createOptions.Stdout = nil
	createOptions.Stderr = nil
	if err := backend.create(execution.Create, createOptions); err != nil {
		return abortAdmittedTransientBeforeStartV1(context.WithoutCancel(ctx), operation, runID, execution, options, backend,
			fmt.Errorf("create admitted transient container: %w", err))
	}
	if err := operation.RecordLiveRunContainerV1(runID, execution.Container); err != nil {
		return abortAdmittedTransientBeforeStartV1(context.WithoutCancel(ctx), operation, runID, execution, options, backend,
			fmt.Errorf("record admitted transient container: %w", err))
	}
	if err := operation.Unlock(); err != nil {
		return abortAdmittedTransientAfterReleaseV1(context.WithoutCancel(ctx), absoluteDir, runID, execution, options, backend,
			fmt.Errorf("release operation lock before transient execution: %w", err))
	}

	startOptions := options
	startOptions.Context = ctx
	runErr := backend.runTemporary(backend.followup, execution.Start, execution.Cleanup, startOptions)
	removed, completionErr := completeAdmittedTransientRunV1(context.WithoutCancel(ctx), absoluteDir, runID, backend)
	if runErr != nil {
		if completionErr == nil && !removed {
			runErr = ErrLiveRunStoppedV1
		} else {
			runErr = fmt.Errorf("run admitted transient container: %w", runErr)
		}
	}
	return errors.Join(runErr, completionErr)
}

func ValidateTransientContainerExecutionV1(execution TransientContainerExecutionV1, runID string) error {
	if err := deploy.ValidateLiveRunIDV1(runID); err != nil {
		return err
	}
	if execution.Container == "" {
		return fmt.Errorf("transient container execution requires a container name")
	}
	wantSuffix := "-" + runID
	if len(execution.Container) <= len(wantSuffix) || execution.Container[len(execution.Container)-len(wantSuffix):] != wantSuffix {
		return fmt.Errorf("transient container name must end with admitted run ID")
	}
	if !reflectDockerCreateCommandV1(execution.Create, execution.Container) {
		return fmt.Errorf("transient container create command does not name the admitted container")
	}
	if !reflectDockerFinalContainerCommandV1(execution.Start, "start", execution.Container) {
		return fmt.Errorf("transient container start command does not name the admitted container")
	}
	if !reflectDockerCleanupCommandV1(execution.Cleanup, execution.Container) {
		return fmt.Errorf("transient container cleanup command does not name the admitted container")
	}
	return nil
}

func reflectDockerCreateCommandV1(spec CommandSpec, container string) bool {
	if spec.Name != "docker" || len(spec.Args) < 3 || spec.Args[0] != "create" {
		return false
	}
	for index := 1; index+1 < len(spec.Args); index++ {
		if spec.Args[index] == "--name" && spec.Args[index+1] == container {
			return true
		}
	}
	return false
}

func reflectDockerFinalContainerCommandV1(spec CommandSpec, operation string, container string) bool {
	return spec.Name == "docker" && len(spec.Args) >= 2 && spec.Args[0] == operation && spec.Args[len(spec.Args)-1] == container
}

func reflectDockerCleanupCommandV1(spec CommandSpec, container string) bool {
	return spec.Name == "docker" && len(spec.Args) >= 3 && spec.Args[0] == "container" && spec.Args[1] == "rm" && spec.Args[len(spec.Args)-1] == container
}

func releaseAdmittedTransientOperationV1(operation *deploy.OperationLock, cause error) error {
	if operation == nil {
		return cause
	}
	if err := operation.Unlock(); err != nil {
		return errors.Join(cause, fmt.Errorf("release operation lock: %w", err))
	}
	return cause
}

func removeAdmittedTransientBeforeCreateV1(operation *deploy.OperationLock, runID string, cause error) error {
	if operation == nil {
		return cause
	}
	var removeErr error
	if deploy.ValidateLiveRunIDV1(runID) == nil {
		_, _, removeErr = operation.RemoveLiveRunV1(runID)
		if removeErr != nil {
			removeErr = fmt.Errorf("remove admitted live run: %w", removeErr)
		}
	}
	unlockErr := operation.Unlock()
	if unlockErr != nil {
		unlockErr = fmt.Errorf("release operation lock: %w", unlockErr)
	}
	return errors.Join(cause, removeErr, unlockErr)
}

func abortAdmittedTransientBeforeStartV1(
	cleanupContext context.Context,
	operation *deploy.OperationLock,
	runID string,
	execution TransientContainerExecutionV1,
	options RunOptions,
	backend admittedTransientContainerBackendV1,
	cause error,
) error {
	var cleanupErr error
	if execution.Cleanup.Name != "" && backend.followup != nil {
		cleanupOptions := options
		cleanupOptions.Context = cleanupContext
		cleanupOptions.Stdin = nil
		cleanupOptions.Stdout = nil
		cleanupOptions.Stderr = nil
		cleanupErr = backend.followup(execution.Cleanup, cleanupOptions)
		if cleanupErr != nil && isMissingContainerCleanupError(cleanupErr) {
			cleanupErr = nil
		}
		if cleanupErr != nil {
			cleanupErr = fmt.Errorf("clean admitted transient container: %w", cleanupErr)
		}
	}
	var queueErr error
	var unlockErr error
	if operation != nil {
		if runID != "" {
			_, _, queueErr = operation.RemoveLiveRunV1(runID)
			if queueErr != nil {
				queueErr = fmt.Errorf("remove admitted live run: %w", queueErr)
			}
		}
		if err := operation.Unlock(); err != nil {
			unlockErr = fmt.Errorf("release operation lock: %w", err)
		}
	}
	return errors.Join(cause, cleanupErr, queueErr, unlockErr)
}

func abortAdmittedTransientAfterReleaseV1(
	cleanupContext context.Context,
	deploymentDir string,
	runID string,
	execution TransientContainerExecutionV1,
	options RunOptions,
	backend admittedTransientContainerBackendV1,
	cause error,
) error {
	cleanupOptions := options
	cleanupOptions.Context = cleanupContext
	cleanupOptions.Stdin = nil
	cleanupOptions.Stdout = nil
	cleanupOptions.Stderr = nil
	var containerErr error
	if backend.followup != nil {
		containerErr = backend.followup(execution.Cleanup, cleanupOptions)
		if containerErr != nil && isMissingContainerCleanupError(containerErr) {
			containerErr = nil
		}
		if containerErr != nil {
			containerErr = fmt.Errorf("clean admitted transient container: %w", containerErr)
		}
	}
	_, completionErr := completeAdmittedTransientRunV1(cleanupContext, deploymentDir, runID, backend)
	return errors.Join(cause, containerErr, completionErr)
}

func completeAdmittedTransientRunV1(
	cleanupContext context.Context,
	deploymentDir string,
	runID string,
	backend admittedTransientContainerBackendV1,
) (bool, error) {
	operation, err := backend.acquire(cleanupContext, deploymentDir)
	if err != nil {
		return false, fmt.Errorf("reacquire operation lock after transient execution: %w", err)
	}
	_, removed, removeErr := operation.RemoveLiveRunV1(runID)
	unlockErr := operation.Unlock()
	if removeErr != nil {
		removeErr = fmt.Errorf("remove completed live run: %w", removeErr)
	}
	if unlockErr != nil {
		unlockErr = fmt.Errorf("release operation lock after transient execution: %w", unlockErr)
	}
	return removed, errors.Join(removeErr, unlockErr)
}
