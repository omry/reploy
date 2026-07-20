package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

type RuntimeReadinessInput struct {
	Current    CurrentBuild
	BuildInput CurrentBuildReuseInput
	PlanID     string
	Sources    []RuntimeHostSourceV1
}

type PublishedRuntimeReadinessInput struct {
	Operation     *deploy.OperationLock
	Store         providerstore.Store
	Environment   string
	DeploymentDir string
	BuildInput    CurrentBuildReuseInput
	PlanID        string
	Sources       []RuntimeHostSourceV1
}

type RuntimeInvocationV1 struct {
	PlanID  string
	Sources []RuntimeHostSourceV1
}

type PublishedRuntimeContainerInput struct {
	Operation     *deploy.OperationLock
	Store         providerstore.Store
	Environment   string
	DeploymentDir string
	BuildInput    CurrentBuildReuseInput
	Invocation    RuntimeInvocationV1
}

func WorkloadRuntimeInvocationV1(plan DockerExecutionPlan) (RuntimeInvocationV1, error) {
	sources, err := RuntimeHostSourcesV1(plan, nil)
	return RuntimeInvocationV1{PlanID: runtimeWorkloadPlanID, Sources: sources}, err
}

func ShellRuntimeInvocationV1(plan DockerExecutionPlan) (RuntimeInvocationV1, error) {
	sources, err := RuntimeHostSourcesV1(plan, nil)
	return RuntimeInvocationV1{PlanID: runtimeShellPlanID, Sources: sources}, err
}

func CommandRuntimeInvocationV1(plan DockerExecutionPlan, commandName string, output *transientOutputMount) (RuntimeInvocationV1, error) {
	if commandName == "" {
		return RuntimeInvocationV1{}, fmt.Errorf("runtime command name is required")
	}
	sources, err := RuntimeHostSourcesV1(plan, output)
	return RuntimeInvocationV1{PlanID: runtimeCommandPlanID(commandName, output != nil), Sources: sources}, err
}

type currentBuildLoader func(
	context.Context,
	*deploy.OperationLock,
	providerstore.Store,
	string,
	string,
) (CurrentBuild, bool, error)

type PublishedRuntimeContainerRunnerV1 func(context.Context, CurrentBuild) error

// RunPublishedRuntimeContainerV1 places the complete read-only runtime gate
// immediately before container creation. The runner receives the exact current
// build that passed the gate so it can use that generation's immutable image
// reference.
func RunPublishedRuntimeContainerV1(
	ctx context.Context,
	input PublishedRuntimeContainerInput,
	run PublishedRuntimeContainerRunnerV1,
) error {
	return runPublishedRuntimeContainerV1(ctx, input, ValidateCurrentBuild, run)
}

func runPublishedRuntimeContainerV1(
	ctx context.Context,
	input PublishedRuntimeContainerInput,
	load currentBuildLoader,
	run PublishedRuntimeContainerRunnerV1,
) error {
	if run == nil {
		return fmt.Errorf("runtime container runner is required")
	}
	if input.Invocation.PlanID == "" {
		return fmt.Errorf("runtime invocation plan is required")
	}
	current, err := requirePublishedRuntimeReady(ctx, PublishedRuntimeReadinessInput{
		Operation: input.Operation, Store: input.Store, Environment: input.Environment,
		DeploymentDir: input.DeploymentDir, BuildInput: input.BuildInput,
		PlanID: input.Invocation.PlanID, Sources: input.Invocation.Sources,
	}, load)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := run(ctx, current); err != nil {
		return fmt.Errorf("runtime container: %w", err)
	}
	return nil
}

func RequirePublishedRuntimeReady(ctx context.Context, input PublishedRuntimeReadinessInput) (CurrentBuild, error) {
	return requirePublishedRuntimeReady(ctx, input, ValidateCurrentBuild)
}

func requirePublishedRuntimeReady(ctx context.Context, input PublishedRuntimeReadinessInput, load currentBuildLoader) (CurrentBuild, error) {
	if ctx == nil {
		return CurrentBuild{}, fmt.Errorf("runtime readiness requires a context")
	}
	if err := ctx.Err(); err != nil {
		return CurrentBuild{}, err
	}
	if load == nil {
		return CurrentBuild{}, fmt.Errorf("runtime readiness requires a current-build loader")
	}
	current, found, err := load(ctx, input.Operation, input.Store, input.Environment, input.DeploymentDir)
	if err != nil {
		return CurrentBuild{}, fmt.Errorf("runtime current build: %w", err)
	}
	if !found {
		return CurrentBuild{}, fmt.Errorf("runtime build is missing; run `reploy build`")
	}
	if err := RequireRuntimeReady(RuntimeReadinessInput{
		Current: current, BuildInput: input.BuildInput, PlanID: input.PlanID, Sources: input.Sources,
	}); err != nil {
		return CurrentBuild{}, err
	}
	return current, nil
}

// RequireRuntimeReady performs the complete pre-container check that needs no
// Docker probe: the current build must match exact inputs, then the selected
// locked plan's host sources must still match.
func RequireRuntimeReady(input RuntimeReadinessInput) error {
	matched, err := CurrentBuildMatches(input.Current, input.BuildInput)
	if err != nil {
		return fmt.Errorf("runtime current-build check: %w", err)
	}
	if !matched {
		return fmt.Errorf("runtime build is missing or stale; run `reploy build`")
	}
	if err := ValidateRuntimeHostSourcesV1(input.Current.Lock.RuntimePolicy, input.PlanID, input.Sources); err != nil {
		return fmt.Errorf("runtime host-source check: %w", err)
	}
	return nil
}
