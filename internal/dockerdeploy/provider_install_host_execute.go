package dockerdeploy

import (
	"context"
	"fmt"
)

func configureProviderInstallHostV1(ctx context.Context, plan providerInstallationPlanV1, tools providerInstallHostToolsV1, options RunOptions) error {
	commands, err := planProviderInstallHostCommandsV1(plan, tools.DockerPath, tools.SystemctlPath)
	if err != nil {
		return err
	}
	return configureProviderInstallHostWithV1(ctx, commands, options, runCommandWithoutDockerPreflight)
}

func startProviderInstallHostV1(ctx context.Context, plan providerInstallationPlanV1, tools providerInstallHostToolsV1, options RunOptions) error {
	commands, err := planProviderInstallHostCommandsV1(plan, tools.DockerPath, tools.SystemctlPath)
	if err != nil {
		return err
	}
	environment, err := preparePrivateWorkloadEnvironmentV1(plan.Installation.TargetDir)
	if err != nil {
		return fmt.Errorf("prepare installed private workload environment: %w", err)
	}
	if plan.Backend != installBackendLinuxSystemd {
		currentDockerPlan := plan.Docker
		currentDockerPlan.DeploymentDir = plan.Installation.TargetDir
		if err := validatePrivateRuntimeMaskSnapshotV1(currentDockerPlan, plan.Rendered.privateRuntimeMasks); err != nil {
			return fmt.Errorf("validate installed private runtime isolation: %w", err)
		}
	}
	run := commandRunner(runCommandWithoutDockerPreflight)
	if plan.Backend == installBackendDockerDesktop || plan.Backend == installBackendDockerManaged {
		run = runDockerCommand
	}
	if plan.Docker.PrivateEnvironment && plan.Backend != installBackendLinuxSystemd {
		if !environment.Present {
			return fmt.Errorf("installed private workload environment disappeared before startup")
		}
		if err := validatePrivateWorkloadEnvironmentIsolationV1(plan.Installation.TargetDir, plan.Docker); err != nil {
			return fmt.Errorf("validate installed private workload environment isolation: %w", err)
		}
		cleanup := composeCommandWithProject(plan.Installation.TargetDir, plan.Installation.ComposeProject, "down", "--remove-orphans")
		cleanup.Name = tools.DockerPath
		if err := startAndInjectPrivateWorkloadEnvironmentV1(
			ctx,
			commands.Start,
			cleanup,
			plan.Docker.ContainerName,
			plan.Docker.Sandbox,
			environment,
			options,
			run,
		); err != nil {
			return fmt.Errorf("install host startup: %w", err)
		}
		return nil
	}
	return startProviderInstallHostWithV1(ctx, commands, options, run)
}

func configureProviderInstallHostWithV1(ctx context.Context, commands providerInstallHostCommandsV1, options RunOptions, run commandRunner) error {
	if ctx == nil {
		return fmt.Errorf("configure install host requires a context")
	}
	if run == nil {
		return fmt.Errorf("configure install host requires a command runner")
	}
	for index, command := range commands.Configure {
		if err := ctx.Err(); err != nil {
			return err
		}
		runOptions := options
		runOptions.Context = ctx
		if err := run(command, runOptions); err != nil {
			return fmt.Errorf("install host configuration command %d: %w", index+1, err)
		}
	}
	return nil
}

func startProviderInstallHostWithV1(ctx context.Context, commands providerInstallHostCommandsV1, options RunOptions, run commandRunner) error {
	if ctx == nil {
		return fmt.Errorf("start install host requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if run == nil {
		return fmt.Errorf("start install host requires a command runner")
	}
	if commands.Start.Name == "" {
		return fmt.Errorf("start install host requires a planned command")
	}
	runOptions := options
	runOptions.Context = ctx
	if err := run(commands.Start, runOptions); err != nil {
		return fmt.Errorf("install host startup: %w", err)
	}
	return nil
}
