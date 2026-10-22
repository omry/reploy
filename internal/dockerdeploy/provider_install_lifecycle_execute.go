package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"github.com/omry/reploy/internal/deploy"
)

type providerInstallLifecycleExecutionBackendV1 struct {
	executor func(lockedProviderInstallV1) LifecycleExecutor
	start    func(context.Context, lockedProviderInstallV1) error
}

func executeProviderInstallAfterInstallV1(ctx context.Context, locked lockedProviderInstallV1) error {
	return executeProviderInstallAfterInstallWithV1(ctx, locked, providerInstallLifecycleExecutionBackendV1{
		executor: providerInstallLifecycleExecutorV1,
	})
}

func executeProviderInstallAfterInstallWithV1(ctx context.Context, locked lockedProviderInstallV1, backend providerInstallLifecycleExecutionBackendV1) error {
	if backend.executor == nil {
		return fmt.Errorf("run after_install lifecycle requires an executor")
	}
	executor := backend.executor(locked)
	if err := ExecuteLifecycle(ctx, locked.Plan.AfterInstall, executor); err != nil {
		return fmt.Errorf("run after_install lifecycle: %w", err)
	}
	return nil
}

func executeProviderInstallStartV1(ctx context.Context, locked lockedProviderInstallV1) error {
	command := composeCommandWithProject(locked.Plan.Installation.TargetDir, locked.Plan.Installation.ComposeProject, "up", "--pull", "never", "-d")
	command.Name = locked.HostTools.DockerPath
	pinned, run, err := bindPinnedDockerCommandRunnerV1(ctx, command, locked.Input.RunOptions.DockerPreflightTimeout)
	if err != nil {
		return err
	}
	operation := func(ctx context.Context, action func(*deploy.OperationLock) error) (err error) {
		lock := locked.DestinationOperation
		if lock == nil {
			lock, err = deploy.AcquireOperationLock(ctx, locked.Plan.Installation.TargetDir)
			if err != nil {
				return err
			}
			defer func() { err = errors.Join(err, lock.Unlock()) }()
		}
		return action(lock)
	}
	lifecycle := &workloadLifecycleV1{command: pinned, containerName: locked.Plan.Docker.ContainerName, run: run, options: locked.Input.RunOptions, operation: operation}
	binding, err := workloadRuntimeBindingV1(locked.Plan.Docker)
	if err != nil {
		return err
	}
	reused, _, err := lifecycle.initialize(ctx, binding, locked.Plan.Docker.PrivateEnvironment)
	if err != nil {
		return err
	}
	if reused {
		return nil
	}
	locked.Input.RunOptions.workloadLifecycle = lifecycle
	if locked.Plan.Backend == installBackendLinuxSystemd {
		// The service boundary runs startup hooks before notifying readiness,
		// including starts performed by the service manager after boot.
		locked.Plan.Start = LifecyclePlan{Operations: []LifecycleOperation{{Kind: LifecycleStart, Event: "start"}}}
	}
	err = executeProviderInstallStartWithV1(ctx, locked, providerInstallLifecycleExecutionBackendV1{
		executor: providerInstallLifecycleExecutorV1,
		start: func(ctx context.Context, locked lockedProviderInstallV1) error {
			err := startProviderInstallHostV1(ctx, locked.Plan, locked.HostTools, locked.Input.RunOptions)
			if locked.Plan.Backend == installBackendLinuxSystemd {
				current, inspectErr := lifecycle.inspect(context.WithoutCancel(ctx))
				if inspectErr != nil {
					return errors.Join(err, inspectErr)
				}
				lifecycle.started = privateWorkloadStartResultV1{ContainerID: current.ID, StartedAt: current.StartedAt,
					Owned: current.ID != "" && (current.ID != lifecycle.initial.ID || !lifecycle.initial.Running && current.StartedAt != lifecycle.initial.StartedAt)}
			}
			return err
		},
	})
	if err != nil {
		return lifecycle.rollback(ctx, err)
	}
	if err := lifecycle.complete(ctx); err != nil {
		return lifecycle.rollback(ctx, err)
	}
	return nil
}

func executeProviderInstallStartWithV1(ctx context.Context, locked lockedProviderInstallV1, backend providerInstallLifecycleExecutionBackendV1) error {
	if backend.executor == nil || backend.start == nil {
		return fmt.Errorf("run start lifecycle requires an executor and start operation")
	}
	executor := backend.executor(locked)
	executor.Start = func(ctx context.Context) error {
		return backend.start(ctx, locked)
	}
	if err := ExecuteLifecycle(ctx, locked.Plan.Start, executor); err != nil {
		return fmt.Errorf("run start lifecycle: %w", err)
	}
	return nil
}

func providerInstallLifecycleExecutorV1(locked lockedProviderInstallV1) LifecycleExecutor {
	return environmentLifecycleExecutor(
		RuntimeOptions{
			Dir:                    locked.Input.DestinationDeploymentDir,
			DockerPreflightTimeout: locked.Input.RunOptions.DockerPreflightTimeout,
		},
		locked.Plan.Document,
		locked.Plan.Docker,
		locked.InstallBuild.RuntimePolicy,
		locked.Input.RunOptions.Stdout,
		locked.Input.RunOptions.Stderr,
	)
}
