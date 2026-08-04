package dockerdeploy

import (
	"context"
	"io"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providerstore"
)

func environmentLifecycleExecutor(options RuntimeOptions, plan DockerExecutionPlan, _ providerstore.Store, _ blueprint.Platform, stdout io.Writer, stderr io.Writer) LifecycleExecutor {
	return LifecycleExecutor{
		RunCommand: func(ctx context.Context, command ResolvedEnvironmentCommand) error {
			if _, err := preparePrivateWorkloadEnvironmentV1(options.Dir); err != nil {
				return err
			}
			spec, err := TransientCommandSpec(plan, command, nil, false, false)
			if err != nil {
				return err
			}
			return runTemporaryContainerCommand(
				runRuntimeCommand,
				spec,
				TemporaryContainerCleanupCommand(transientCommandContainerName(plan)),
				RunOptions{Context: ctx, Stdout: stdout, Stderr: stderr, DockerPreflightTimeout: options.DockerPreflightTimeout},
			)
		},
		Readiness: func(ctx context.Context, endpoint EndpointExecutionPlan) error {
			return WaitForHTTPReadinessWithServiceCheck(ctx, endpoint, func(context.Context) error {
				return requireComposeServiceRunning(options.Dir, "", options.DockerPreflightTimeout)
			})
		},
	}
}
