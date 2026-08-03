package dockerdeploy

import (
	"context"
	"io"

	"github.com/omry/reploy/internal/deploy"
)

func environmentLifecycleExecutor(options RuntimeOptions, plan DockerExecutionPlan, policy deploy.RuntimePolicyV1, stdout io.Writer, stderr io.Writer) LifecycleExecutor {
	return LifecycleExecutor{
		RunCommand: func(ctx context.Context, command ResolvedEnvironmentCommand) error {
			if err := validateLifecycleRuntimeHostSourcesV1(policy, plan, command.Name); err != nil {
				return err
			}
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

func validateLifecycleRuntimeHostSourcesV1(policy deploy.RuntimePolicyV1, plan DockerExecutionPlan, commandName string) error {
	invocation, err := CommandRuntimeInvocationV1(plan, commandName, nil)
	if err != nil {
		return err
	}
	return ValidateRuntimeHostSourcesV1(policy, invocation.PlanID, plan.Sandbox.RuntimeUser.UID, invocation.Sources)
}
