package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

var bindInstalledServiceCommandRunner = bindDockerCommandRunnerV1

// RunInstalledServiceContainerV1 is the system-service container boundary. It
// deliberately bypasses the public control surface because systemd itself is
// already the admitted host-service operation.
func RunInstalledServiceContainerV1(ctx context.Context, deploymentDir string, action string, dockerPath string, options RunOptions) (err error) {
	if ctx == nil {
		return fmt.Errorf("run installed service container requires a context")
	}
	if action != "run" {
		return fmt.Errorf("installed service container action must be run")
	}
	operation, err := deploy.AcquireOperationLock(ctx, deploymentDir)
	if err != nil {
		return err
	}
	defer func() {
		if operation != nil {
			err = errors.Join(err, operation.Unlock())
		}
	}()
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return err
	}
	if !found || state.Deployment == nil || state.Deployment.Installation.Scope != string(InstallScopeSystem) {
		return fmt.Errorf("installed service container requires a system installation")
	}
	installation := state.Deployment.Installation
	if dockerPath == "" || !filepath.IsAbs(dockerPath) || filepath.Clean(dockerPath) != dockerPath {
		return fmt.Errorf("installed service container requires an absolute Docker path")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return fmt.Errorf("installed service blueprint: %w", err)
	}
	store, err := providerstore.NewStore(deploymentDir)
	if err != nil {
		return err
	}
	current, currentFound, err := ValidateCurrentBuild(ctx, operation, store, document.Environment.ID, deploymentDir)
	if err != nil {
		return fmt.Errorf("installed service current build: %w", err)
	}
	if !currentFound {
		return fmt.Errorf("installed service build is missing; rerun the original `reploy install` command")
	}
	runtime, err := CurrentStagedProviderBuildRuntimeV1()
	if err != nil {
		return err
	}
	plan, err := PlanCurrentRuntimeV1(CurrentRuntimePlanInputV1{
		DeploymentDir: deploymentDir,
		Current:       current,
		Runtime:       runtime,
	})
	if err != nil {
		return err
	}
	environment, err := preparePrivateWorkloadEnvironmentV1(deploymentDir)
	if err != nil {
		return fmt.Errorf("prepare private workload environment: %w", err)
	}
	if environment.Present {
		if err := validatePrivateWorkloadEnvironmentIsolationV1(deploymentDir, plan.Docker); err != nil {
			return err
		}
		plan.Docker.PrivateEnvironment = true
	}
	invocation, err := WorkloadRuntimeInvocationV1(plan.Docker)
	if err != nil {
		return err
	}
	if err := RequireRuntimeReady(RuntimeReadinessInput{
		Current: current, DockerPlan: plan.Docker,
		PlanID: invocation.PlanID, Sources: invocation.Sources,
	}); err != nil {
		return err
	}
	if _, err := PublishCurrentRuntimeInputsV1(operation, deploymentDir, plan); err != nil {
		return err
	}
	start := composeCommandWithProject(deploymentDir, installation.ComposeProject, "up", "--pull", "never", "-d")
	start.Name = dockerPath
	cleanup := composeCommandWithProject(deploymentDir, installation.ComposeProject, "down", "--remove-orphans")
	cleanup.Name = dockerPath
	runDocker, err := bindInstalledServiceCommandRunner(ctx, start, options.DockerPreflightTimeout)
	if err != nil {
		return fmt.Errorf("bind installed service Docker endpoint: %w", err)
	}
	if err := startAndInjectPrivateWorkloadEnvironmentV1(
		ctx,
		start,
		cleanup,
		plan.Docker.ContainerName,
		plan.Docker.Sandbox,
		environment,
		options,
		runDocker,
	); err != nil {
		return err
	}
	if err := notifyInstalledServiceReadyV1(); err != nil {
		return errors.Join(err, cleanupPrivateWorkloadContainerV1(cleanup, RunOptions{Context: context.WithoutCancel(ctx)}, runDocker))
	}
	if err := operation.Unlock(); err != nil {
		return err
	}
	operation = nil

	var status bytes.Buffer
	waitOptions := options
	waitOptions.Context = ctx
	waitOptions.Stdin = nil
	waitOptions.Stdout = &status
	waitOptions.Stderr = options.Stderr
	if err := runDocker(CommandSpec{
		Name: dockerPath,
		Args: []string{"wait", plan.Docker.ContainerName},
	}, waitOptions); err != nil {
		return fmt.Errorf("wait for installed workload container: %w", err)
	}
	exitCode, err := strconv.Atoi(strings.TrimSpace(status.String()))
	if err != nil {
		return fmt.Errorf("Docker returned an invalid workload exit status")
	}
	if exitCode != 0 {
		return fmt.Errorf("installed workload container exited with status %d", exitCode)
	}
	return nil
}
