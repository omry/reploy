package dockerdeploy

import (
	"context"
	"fmt"
	"reflect"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type RuntimeReadinessInput struct {
	Current    CurrentBuild
	DockerPlan DockerExecutionPlan
	PlanID     string
	Sources    []RuntimeHostSourceV1
}

type PublishedRuntimeReadinessInput struct {
	Operation     *deploy.OperationLock
	Store         providerstore.Store
	Environment   string
	DeploymentDir string
	DockerPlan    DockerExecutionPlan
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
	DockerPlan    DockerExecutionPlan
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
		DeploymentDir: input.DeploymentDir, DockerPlan: input.DockerPlan,
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
		return CurrentBuild{}, fmt.Errorf("%s", runtimeBuildRecoveryForPhaseV1(input.DockerPlan.Phase, "runtime build is missing"))
	}
	if err := RequireRuntimeReady(RuntimeReadinessInput{
		Current: current, DockerPlan: input.DockerPlan, PlanID: input.PlanID, Sources: input.Sources,
	}); err != nil {
		return CurrentBuild{}, err
	}
	return current, nil
}

func runtimeBuildRecoveryForPhaseV1(phase blueprint.Phase, problem string) string {
	if phase == blueprint.PhaseInstalled {
		return problem + "; rerun the original `reploy install` command"
	}
	return problem + "; run `reploy build`"
}

// RequireRuntimeReady performs the complete pre-container check that needs no
// Docker probe: the current build must match exact inputs, then the selected
// locked plan's host sources must still match.
func RequireRuntimeReady(input RuntimeReadinessInput) error {
	matched, err := CurrentBuildMatchesRuntimeV1(input.Current, input.DockerPlan)
	if err != nil {
		return fmt.Errorf("runtime current-build check: %w", err)
	}
	if !matched {
		return fmt.Errorf("%s", currentBuildRecoveryMessageV1(input.Current.State, "runtime build is missing or stale"))
	}
	if err := ValidateRuntimeHostSourcesV1(input.Current.Lock.RuntimePolicy, input.PlanID, input.Sources); err != nil {
		return fmt.Errorf("runtime host-source check: %w", err)
	}
	return nil
}

// CurrentBuildMatchesRuntimeV1 checks the complete runtime-affecting state
// against the published lock without resolving a mutable base or contacting a
// provider. A valid state that no longer composes with the published lock is a
// stale build, not runtime work to repair or reproduce.
func CurrentBuildMatchesRuntimeV1(current CurrentBuild, dockerPlan DockerExecutionPlan) (bool, error) {
	if err := deploy.ValidateStateV1(current.State); err != nil {
		return false, fmt.Errorf("runtime current build state: %w", err)
	}
	if current.State.Current == nil || !reflect.DeepEqual(*current.State.Current, current.Generation) {
		return false, fmt.Errorf("runtime current build state does not name the supplied generation")
	}
	if err := validateGenerationBuildLock(current.Generation, current.Lock, registry.ValidateRequirementProfileV1); err != nil {
		return false, fmt.Errorf("runtime current build: %w", err)
	}
	document, err := blueprint.DecodeResolvedDocumentV1(current.State.Blueprint)
	if err != nil {
		return false, fmt.Errorf("runtime blueprint: %w", err)
	}
	blueprintDigest, err := blueprint.ResolvedDocumentDigestV1(current.State.Blueprint)
	if err != nil {
		return false, err
	}
	if blueprintDigest != current.Lock.BlueprintDigest || current.State.Platform != current.Lock.Platform {
		return false, nil
	}
	stateOverlayDigest, err := deploy.RequestOverlayDigestV1(current.State.Overlay)
	if err != nil {
		return false, err
	}
	lockedOverlayDigest, err := deploy.RequestOverlayDigestV1(current.Lock.Overlay)
	if err != nil {
		return false, err
	}
	if stateOverlayDigest != lockedOverlayDigest {
		return false, nil
	}

	if document.Environment.Base.Image == "" {
		return false, nil
	}
	if document.Environment.Base.Image != current.Lock.Base.AuthorReference {
		return false, nil
	}

	plans, err := RuntimePlansV1(document, dockerPlan)
	if err != nil {
		return false, fmt.Errorf("runtime plan: %w", err)
	}
	policy, err := CompileRuntimePolicyFromLockV1(document, current.Lock, plans)
	if err != nil {
		return false, nil
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(policy)
	if err != nil {
		return false, err
	}
	lockedPolicyDigest, err := deploy.RuntimePolicyDigestV1(current.Lock.RuntimePolicy)
	if err != nil {
		return false, err
	}
	account, err := applicationLocalAccountV1(dockerPlan.Sandbox)
	if err != nil {
		return false, fmt.Errorf("runtime local account: %w", err)
	}
	return policyDigest == lockedPolicyDigest && reflect.DeepEqual(account, current.Lock.RuntimeLayer.Account), nil
}
