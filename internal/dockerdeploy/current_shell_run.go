package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

type CurrentShellRunInputV1 struct {
	DeploymentDir string
	Wait          bool
	ReadOnly      bool
	Runtime       StagedProviderBuildRuntimeV1
	TTY           bool
	RunOptions    RunOptions
}

type currentShellRunBackendV1 struct {
	acquire      func(context.Context, string) (*deploy.OperationLock, error)
	newStore     func(string) (providerstore.Store, error)
	readState    func(*deploy.OperationLock) (deploy.StateV1, bool, error)
	loadCurrent  currentBuildLoader
	plan         func(CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error)
	matches      func(CurrentBuild, DockerExecutionPlan) (bool, error)
	invocation   func(DockerExecutionPlan) (RuntimeInvocationV1, error)
	concurrency  func(blueprint.Document, DockerExecutionPlan, *transientOutputMount) (LiveRunConcurrencyDecisionV1, error)
	newRunID     func() (string, error)
	await        func(context.Context, string, *deploy.OperationLock, deploy.LiveRunV1, bool, io.Writer) (*deploy.OperationLock, error)
	runPublished func(context.Context, PublishedRuntimeContainerInput, PublishedRuntimeContainerRunnerV1) error
	prepareProbe func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error)
	execution    func(DockerExecutionPlan, PreparedProbeWorkspace, string, bool, bool) (TransientContainerExecutionV1, error)
	runAdmitted  func(context.Context, string, *deploy.OperationLock, string, TransientContainerExecutionV1, RunOptions) error
}

// RunCurrentShellV1 runs a shell in the exact published state-v1 generation.
func RunCurrentShellV1(ctx context.Context, input CurrentShellRunInputV1) error {
	return runCurrentShellV1(ctx, input, currentShellRunBackendV1{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
		readState: func(operation *deploy.OperationLock) (deploy.StateV1, bool, error) {
			return operation.ReadStateV1()
		},
		loadCurrent:  ValidateCurrentBuild,
		plan:         PlanCurrentRuntimeV1,
		matches:      CurrentBuildMatchesRuntimeV1,
		invocation:   ShellRuntimeInvocationV1,
		concurrency:  PlanLiveRunConcurrencyV1,
		newRunID:     deploy.NewLiveRunIDV1,
		await:        AwaitLiveRunAdmissionWithNoticeV1,
		runPublished: RunPublishedRuntimeContainerV1,
		prepareProbe: PrepareProbeWorkspace,
		execution: func(plan DockerExecutionPlan, workspace PreparedProbeWorkspace, runID string, interactive bool, tty bool) (TransientContainerExecutionV1, error) {
			return PlanTransientContainerExecutionV1(
				plan, ResolvedEnvironmentCommand{Argv: []string{"/bin/sh"}}, workspace, nil, runID, interactive, tty,
			)
		},
		runAdmitted: RunAdmittedTransientContainerV1,
	})
}

func runCurrentShellV1(ctx context.Context, input CurrentShellRunInputV1, backend currentShellRunBackendV1) (err error) {
	if ctx == nil {
		return fmt.Errorf("run current shell requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if input.DeploymentDir == "" {
		return fmt.Errorf("run current shell requires a deployment directory")
	}
	if backend.acquire == nil || backend.newStore == nil || backend.readState == nil || backend.loadCurrent == nil || backend.plan == nil || backend.matches == nil || backend.invocation == nil || backend.concurrency == nil || backend.newRunID == nil || backend.await == nil || backend.runPublished == nil || backend.prepareProbe == nil || backend.execution == nil || backend.runAdmitted == nil {
		return fmt.Errorf("run current shell requires a complete backend")
	}
	dir, err := filepath.Abs(input.DeploymentDir)
	if err != nil {
		return fmt.Errorf("resolve current shell deployment directory: %w", err)
	}
	operation, err := backend.acquire(ctx, dir)
	if err != nil {
		return err
	}
	defer func() {
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
	if _, err := preparePrivateWorkloadEnvironmentV1(dir); err != nil {
		return fmt.Errorf("prepare private workload environment: %w", err)
	}
	invocation, err := backend.invocation(planned.Docker)
	if err != nil {
		return err
	}
	effectivePlan := planned.Docker
	if input.ReadOnly {
		effectivePlan.Mounts = append([]MountExecutionPlan(nil), planned.Docker.Mounts...)
		for index := range effectivePlan.Mounts {
			effectivePlan.Mounts[index].ReadOnly = true
		}
	}
	concurrency, err := backend.concurrency(document, effectivePlan, nil)
	if err != nil {
		return err
	}
	runID, err := backend.newRunID()
	if err != nil {
		return err
	}
	candidate := deploy.LiveRunV1{
		ID: runID, Kind: deploy.LiveRunKindShellV1, Name: "shell",
		GenerationReference: current.Generation.Reference,
		Exclusive:           !concurrency.AllowsOverlap,
		WritableMount:       concurrency.WritableMount,
		WritablePaths:       concurrency.WritablePaths,
	}
	operation, err = backend.await(ctx, dir, operation, candidate, input.Wait, input.RunOptions.Stderr)
	if err != nil {
		if errors.Is(err, deploy.ErrLiveRunConflict) {
			err = liveRunConflictErrorV1(document.Environment.AllowConcurrent, concurrency.WritableMount)
		}
		return err
	}
	runOptions := input.RunOptions
	runOptions.Context = ctx
	published := PublishedRuntimeContainerInput{
		Operation: operation, Store: store, Environment: document.Environment.ID,
		DeploymentDir: dir, DockerPlan: planned.Docker, Invocation: invocation,
	}
	callbackEntered := false
	runErr := backend.runPublished(ctx, published, func(runCtx context.Context, gated CurrentBuild) error {
		callbackEntered = true
		if gated.Generation.Reference != candidate.GenerationReference {
			return removeAdmittedTransientBeforeCreateV1(operation, runID, fmt.Errorf(
				"deployment generation changed while live run %q was waiting; retry the shell", runID,
			))
		}
		workspace, cleanup, err := backend.prepareProbe(runCtx, store, gated.Lock.Platform)
		if err != nil {
			return removeAdmittedTransientBeforeCreateV1(operation, runID, err)
		}
		interactive := runOptions.Stdin != nil
		execution, err := backend.execution(effectivePlan, workspace, runID, interactive, interactive && input.TTY)
		if err != nil {
			return removeAdmittedTransientBeforeCreateV1(operation, runID, errors.Join(err, cleanup()))
		}
		options := runOptions
		options.Context = runCtx
		return errors.Join(backend.runAdmitted(runCtx, dir, operation, runID, execution, options), cleanup())
	})
	if runErr != nil && !callbackEntered {
		return removeAdmittedTransientBeforeCreateV1(operation, runID, runErr)
	}
	return runErr
}
