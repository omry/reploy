package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
)

const privateWorkloadEnvironmentFIFOPathV1 = environmentTemporaryHome + "/.reploy-private-environment"

const privateWorkloadEnvironmentRelayV1 = `reploy_private_environment_pipe=$1
while [ ! -p "$reploy_private_environment_pipe" ]; do
  sleep 0.05
done
cat > "$reploy_private_environment_pipe"
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
	runOptions := options
	runOptions.Context = ctx
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
		return fmt.Errorf("inject private workload environment through one-shot FIFO relay: %w", err)
	}
	return nil
}

func startAndInjectPrivateWorkloadEnvironmentV1(
	ctx context.Context,
	start CommandSpec,
	cleanup CommandSpec,
	containerName string,
	sandbox ApplicationSandboxPlanV1,
	environment privateWorkloadEnvironmentV1,
	options RunOptions,
	run commandRunner,
) error {
	startOptions := options
	startOptions.Context = ctx
	startOptions.Stdin = nil
	if err := run(start, startOptions); err != nil {
		return err
	}
	if !environment.Present {
		return nil
	}
	if err := injectPrivateWorkloadEnvironmentV1(ctx, start.Name, containerName, sandbox, environment, options, run); err != nil {
		cleanupOptions := options
		cleanupOptions.Context = context.WithoutCancel(ctx)
		cleanupOptions.Stdin = nil
		cleanupOptions.Stdout = nil
		cleanupOptions.Stderr = nil
		return errors.Join(err, cleanupPrivateWorkloadContainerV1(cleanup, cleanupOptions, run))
	}
	return nil
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
