package dockerdeploy

import (
	"context"
	"io"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func environmentLifecycleExecutor(options RuntimeOptions, document blueprint.Document, plan DockerExecutionPlan, policy deploy.RuntimePolicyV1, stdout io.Writer, stderr io.Writer) LifecycleExecutor {
	return environmentLifecycleExecutorWithCleanupHookV1(options, document, plan, policy, stdout, stderr, nil)
}

func environmentLifecycleExecutorWithCleanupHookV1(
	options RuntimeOptions,
	document blueprint.Document,
	plan DockerExecutionPlan,
	policy deploy.RuntimePolicyV1,
	stdout io.Writer,
	stderr io.Writer,
	beforeCleanup func(error, func(context.Context) error),
) LifecycleExecutor {
	return LifecycleExecutor{
		RunCommand: func(ctx context.Context, command ResolvedEnvironmentCommand) error {
			commandPlan, err := effectiveCommandDockerPlanV1(document, plan, command.Name)
			if err != nil {
				return err
			}
			if err := validateLifecycleRuntimeHostSourcesV1(policy, commandPlan, command.Name); err != nil {
				return err
			}
			if _, err := preparePrivateWorkloadEnvironmentV1(options.Dir); err != nil {
				return err
			}
			containerName := transientCommandContainerName(commandPlan)
			spec, err := transientContainerCommandSpecV1("run", containerName, commandPlan, command, nil, false, false)
			if err != nil {
				return err
			}
			return runTemporaryContainerCommandWithCleanupHookV1(
				runRuntimeCommand,
				spec,
				TemporaryContainerCleanupCommand(containerName),
				RunOptions{Context: ctx, Stdout: stdout, Stderr: stderr, DockerPreflightTimeout: options.DockerPreflightTimeout},
				beforeCleanup,
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
