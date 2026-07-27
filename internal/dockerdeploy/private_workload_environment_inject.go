package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
)

const privateWorkloadEnvironmentRelayV1 = `while IFS= read -r reploy_private_environment_line; do
  printf '%s\n' "$reploy_private_environment_line"
  [ -n "$reploy_private_environment_line" ] || break
done > /proc/1/fd/0
`

func injectPrivateWorkloadEnvironmentV1(
	ctx context.Context,
	dockerPath string,
	containerName string,
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
			"/bin/sh",
			"-c",
			privateWorkloadEnvironmentRelayV1,
		},
	}
	if err := run(spec, runOptions); err != nil {
		return fmt.Errorf("inject private workload environment through the one-shot stdin relay: %w", err)
	}
	return nil
}

func startAndInjectPrivateWorkloadEnvironmentV1(
	ctx context.Context,
	start CommandSpec,
	cleanup CommandSpec,
	containerName string,
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
	if err := injectPrivateWorkloadEnvironmentV1(ctx, start.Name, containerName, environment, options, run); err != nil {
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
