package dockerdeploy

import (
	"context"
	"errors"
	"io"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providerstore"
)

func environmentLifecycleExecutor(options RuntimeOptions, plan DockerExecutionPlan, store providerstore.Store, platform blueprint.Platform, stdout io.Writer, stderr io.Writer) LifecycleExecutor {
	return LifecycleExecutor{
		RunCommand: func(ctx context.Context, command ResolvedEnvironmentCommand) error {
			if _, err := preparePrivateWorkloadEnvironmentV1(options.Dir); err != nil {
				return err
			}
			workspace, cleanup, err := PrepareProbeWorkspace(ctx, store, platform)
			if err != nil {
				return err
			}
			spec, err := TransientCommandSpec(plan, command, workspace, nil, false, false)
			if err != nil {
				return errors.Join(err, cleanup())
			}
			return errors.Join(
				runTemporaryContainerCommand(
					runRuntimeCommand,
					spec,
					TemporaryContainerCleanupCommand(transientCommandContainerName(plan)),
					RunOptions{Context: ctx, Stdout: stdout, Stderr: stderr, DockerPreflightTimeout: options.DockerPreflightTimeout},
				),
				cleanup(),
			)
		},
		Readiness: func(ctx context.Context, endpoint EndpointExecutionPlan) error {
			return WaitForHTTPReadinessWithServiceCheck(ctx, endpoint, func(context.Context) error {
				return requireComposeServiceRunning(options.Dir, "", options.DockerPreflightTimeout)
			})
		},
	}
}
