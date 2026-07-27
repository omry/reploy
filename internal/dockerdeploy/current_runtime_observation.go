package dockerdeploy

import (
	"context"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

type CurrentRuntimeObservationInputV1 struct {
	DeploymentDir string
	Action        string
	Runtime       StagedProviderBuildRuntimeV1
	Command       RuntimeCommandOptions
	RunOptions    RunOptions
}

type currentRuntimeObservationBackendV1 struct {
	acquire       func(context.Context, string) (*deploy.OperationLock, error)
	newStore      func(string) (providerstore.Store, error)
	readState     func(*deploy.OperationLock) (deploy.StateV1, bool, error)
	loadCurrent   currentBuildLoader
	plan          func(CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error)
	matches       func(CurrentBuild, DockerExecutionPlan) (bool, error)
	requireInputs func(*deploy.OperationLock, string, CurrentRuntimePlanV1) error
	command       func(string, string, RuntimeCommandOptions) (CommandSpec, error)
	containerID   func(context.Context, DockerExecutionPlan, time.Duration) (string, error)
	logsCommand   func(string, RuntimeCommandOptions) (CommandSpec, error)
	status        func(context.Context, DockerExecutionPlan, time.Duration) (RuntimeStatusV1, error)
	run           func(CommandSpec, RunOptions) error
}

// RunCurrentRuntimeObservationV1 runs one read-only state-v1 Compose
// observation against the exact published runtime inputs. It does not resolve
// providers, build images, check mutable host sources, or repair runtime files.
func RunCurrentRuntimeObservationV1(ctx context.Context, input CurrentRuntimeObservationInputV1) error {
	return runCurrentRuntimeObservationV1(ctx, input, currentRuntimeObservationBackendV1{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
		readState: func(operation *deploy.OperationLock) (deploy.StateV1, bool, error) {
			return operation.ReadStateV1()
		},
		loadCurrent:   ValidateCurrentBuild,
		plan:          PlanCurrentRuntimeV1,
		matches:       CurrentBuildMatchesRuntimeV1,
		requireInputs: RequireCurrentRuntimeInputsV1,
		command:       RuntimeCommandWithOptions,
		containerID:   CurrentRuntimeContainerIDV1,
		logsCommand:   RuntimeContainerLogsCommandV1,
		status:        ObserveRuntimeStatusV1,
		run:           runCommand,
	})
}

func runCurrentRuntimeObservationV1(
	ctx context.Context,
	input CurrentRuntimeObservationInputV1,
	backend currentRuntimeObservationBackendV1,
) (err error) {
	if ctx == nil {
		return fmt.Errorf("run current runtime observation requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if input.Action != "ps" && input.Action != "status" && input.Action != "logs" {
		return fmt.Errorf("current runtime observation action must be ps, status, or logs")
	}
	if input.DeploymentDir == "" {
		return fmt.Errorf("run current runtime observation requires a deployment directory")
	}
	if backend.acquire == nil || backend.newStore == nil || backend.readState == nil || backend.loadCurrent == nil || backend.plan == nil || backend.matches == nil || backend.requireInputs == nil || backend.command == nil || backend.containerID == nil || backend.logsCommand == nil || backend.status == nil || backend.run == nil {
		return fmt.Errorf("run current runtime observation requires a complete backend")
	}
	dir, err := filepath.Abs(input.DeploymentDir)
	if err != nil {
		return fmt.Errorf("resolve current runtime observation deployment directory: %w", err)
	}
	operation, err := backend.acquire(ctx, dir)
	if err != nil {
		return err
	}
	operationHeld := true
	defer func() {
		if !operationHeld {
			return
		}
		if unlockErr := operation.Unlock(); err == nil && unlockErr != nil {
			err = unlockErr
		}
	}()
	store, err := backend.newStore(dir)
	if err != nil {
		return err
	}
	state, found, err := backend.readState(operation)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("runtime state is missing; run `reploy stage` or `reploy install`")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return fmt.Errorf("runtime blueprint: %w", err)
	}
	current, found, err := backend.loadCurrent(ctx, operation, store, document.Environment.ID, dir)
	if err != nil {
		return fmt.Errorf("runtime current build: %w", err)
	}
	if !found {
		return fmt.Errorf("%s", currentBuildRecoveryMessageV1(state, "runtime build is missing"))
	}
	planned, err := backend.plan(CurrentRuntimePlanInputV1{DeploymentDir: dir, Current: current, Runtime: input.Runtime})
	if err != nil {
		return err
	}
	matched, err := backend.matches(current, planned.Docker)
	if err != nil {
		return fmt.Errorf("runtime current-build check: %w", err)
	}
	if !matched {
		return fmt.Errorf("%s", currentBuildRecoveryMessageV1(state, "runtime build is missing or stale"))
	}
	if err := backend.requireInputs(operation, dir, planned); err != nil {
		return err
	}
	if input.Action == "status" {
		status, err := backend.status(ctx, planned.Docker, input.RunOptions.DockerPreflightTimeout)
		if err != nil {
			return err
		}
		phase, color := stagingOutputPhase, stagingOutputColor
		if state.Deployment != nil {
			phase, color = deployedOutputPhase, deployedOutputColor
		}
		label := deploymentOutputLabel(phase, document.Environment.ID)
		return WriteRuntimeStatusV1(newDeploymentOutputWriter(input.RunOptions.Stdout, label, color), status, planned.Docker)
	}
	var spec CommandSpec
	if input.Action == "logs" {
		containerID, err := backend.containerID(ctx, planned.Docker, input.RunOptions.DockerPreflightTimeout)
		if err != nil {
			return err
		}
		spec, err = backend.logsCommand(containerID, input.Command)
	} else {
		spec, err = backend.command(dir, input.Action, input.Command)
	}
	if err != nil {
		return err
	}
	options := input.RunOptions
	if input.Action != "logs" {
		phase, color := stagingOutputPhase, stagingOutputColor
		if state.Deployment != nil {
			phase, color = deployedOutputPhase, deployedOutputColor
		}
		label := deploymentOutputLabel(phase, document.Environment.ID)
		options.Stdout = newDeploymentOutputWriter(options.Stdout, label, color)
		options.Stderr = newDeploymentOutputWriter(options.Stderr, label, color)
	}
	options.Context = ctx
	// A followed log stream may outlive the current deployment generation. Pin
	// it to the exact container ID selected under the lock, then release the
	// lock so lifecycle work is not blocked by the stream.
	if input.Action == "logs" && input.Command.Follow {
		unlockErr := operation.Unlock()
		operationHeld = false
		if unlockErr != nil {
			return unlockErr
		}
	}
	return backend.run(spec, options)
}

func CurrentRuntimeContainerIDV1(ctx context.Context, plan DockerExecutionPlan, dockerPreflightTimeout time.Duration) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("resolve current runtime container ID requires a context")
	}
	if strings.TrimSpace(plan.ContainerName) == "" {
		return "", fmt.Errorf("resolve current runtime container ID requires a container name")
	}
	output, err := commandOutput(
		CommandSpec{Name: "docker", Args: []string{"inspect", "--format", "{{.Id}}", plan.ContainerName}},
		RunOptions{Context: ctx, DockerPreflightTimeout: dockerPreflightTimeout},
	)
	if err != nil {
		return "", fmt.Errorf("inspect runtime container %q: %w", plan.ContainerName, err)
	}
	containerID := strings.TrimSpace(string(output))
	if len(containerID) != 64 {
		return "", fmt.Errorf("inspect runtime container %q returned invalid container ID %q", plan.ContainerName, containerID)
	}
	if _, err := hex.DecodeString(containerID); err != nil {
		return "", fmt.Errorf("inspect runtime container %q returned invalid container ID %q", plan.ContainerName, containerID)
	}
	return containerID, nil
}

func RuntimeContainerLogsCommandV1(containerID string, options RuntimeCommandOptions) (CommandSpec, error) {
	if len(containerID) != 64 {
		return CommandSpec{}, fmt.Errorf("runtime logs require a full container ID")
	}
	if _, err := hex.DecodeString(containerID); err != nil {
		return CommandSpec{}, fmt.Errorf("runtime logs require a full container ID")
	}
	args := []string{"logs"}
	if options.Timestamps {
		args = append(args, "--timestamps")
	}
	if options.Since != "" {
		args = append(args, "--since", options.Since)
	}
	if options.Tail != "" {
		args = append(args, "--tail", options.Tail)
	}
	if options.Follow {
		args = append(args, "--follow")
	}
	args = append(args, containerID)
	return CommandSpec{Name: "docker", Args: args}, nil
}
