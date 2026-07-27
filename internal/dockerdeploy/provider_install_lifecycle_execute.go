package dockerdeploy

import (
	"context"
	"fmt"
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
	return executeProviderInstallStartWithV1(ctx, locked, providerInstallLifecycleExecutionBackendV1{
		executor: providerInstallLifecycleExecutorV1,
		start: func(ctx context.Context, locked lockedProviderInstallV1) error {
			return startProviderInstallHostV1(ctx, locked.Plan, locked.HostTools, locked.Input.RunOptions)
		},
	})
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
		locked.Plan.Docker,
		locked.DestinationStore,
		locked.SourceBuild.Lock.Platform,
		locked.Input.RunOptions.Stdout,
		locked.Input.RunOptions.Stderr,
	)
}
