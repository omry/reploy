package dockerdeploy

import (
	"bytes"
	"context"
	"fmt"
	"time"
)

const privateWorkloadEnvironmentFIFOPathV1 = environmentTemporaryHome + "/.reploy-private-environment"

const privateWorkloadEnvironmentTimeoutV1 = 30 * time.Second

type privateWorkloadEnvironmentInjectorV1 func(context.Context, string, string, ApplicationSandboxPlanV1, privateWorkloadEnvironmentV1, RunOptions, commandRunner) error

const privateWorkloadEnvironmentRelayV1 = `reploy_private_environment_pipe=$1
reploy_private_environment_attempts=600
while [ ! -p "$reploy_private_environment_pipe" ]; do
  if [ "$reploy_private_environment_attempts" -eq 0 ]; then exit 124; fi
  reploy_private_environment_attempts=$((reploy_private_environment_attempts - 1))
  sleep 0.05
done
exec cat > "$reploy_private_environment_pipe"
`

func injectPrivateWorkloadEnvironmentV1(
	ctx context.Context,
	dockerPath string,
	containerName string,
	sandbox ApplicationSandboxPlanV1,
	environment privateWorkloadEnvironmentV1,
	options RunOptions,
	run commandRunner,
) error {
	if ctx == nil {
		return fmt.Errorf("inject private workload environment requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !environment.Present {
		return nil
	}
	if dockerPath == "" || containerName == "" || run == nil {
		return fmt.Errorf("inject private workload environment requires Docker, a container, and a command runner")
	}
	relayCtx, cancel := context.WithTimeout(ctx, privateWorkloadEnvironmentTimeoutV1)
	defer cancel()
	runOptions := options
	runOptions.Context = relayCtx
	runOptions.Stdin = bytes.NewReader(environment.Payload)
	runOptions.Stdout = nil
	runOptions.Stderr = nil
	spec := CommandSpec{
		Name: dockerPath,
		Args: []string{
			"exec",
			"-i",
			containerName,
		},
	}
	relay := []string{"/bin/sh", "-c", privateWorkloadEnvironmentRelayV1, "reploy-private-environment", privateWorkloadEnvironmentFIFOPathV1}
	wrapperPlan := DockerExecutionPlan{Sandbox: sandbox}
	spec.Args = append(spec.Args, sandbox.StartupVerifier.Path)
	spec.Args = append(spec.Args, sandboxApplicationArgvV1(wrapperPlan, relay, false, []int{})...)
	if err := run(spec, runOptions); err != nil {
		if relayCtx.Err() != nil {
			return fmt.Errorf("private environment injection did not complete before cancellation or its 30-second deadline: %w", relayCtx.Err())
		}
		return fmt.Errorf("inject private workload environment through one-shot FIFO relay: %w", err)
	}
	return nil
}

func startAndInjectPrivateWorkloadEnvironmentV1(
	ctx context.Context,
	start CommandSpec,
	containerName string,
	sandbox ApplicationSandboxPlanV1,
	environment privateWorkloadEnvironmentV1,
	options RunOptions,
	run commandRunner,
) error {
	_, err := startAndInjectPrivateWorkloadEnvironmentWithV1(ctx, start, containerName, sandbox, environment, options, run, injectPrivateWorkloadEnvironmentV1)
	return err
}

func cleanupPrivateWorkloadContainerV1(spec CommandSpec, options RunOptions, run commandRunner) error {
	if spec.Name == "" {
		return fmt.Errorf("private workload environment injection failed and no cleanup command was available")
	}
	if err := run(spec, options); err != nil {
		return fmt.Errorf("remove workload container after private environment injection failure: %w", err)
	}
	return nil
}
