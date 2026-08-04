package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

type CurrentAppCommandRunInputV1 struct {
	DeploymentDir string
	Arguments     []string
	DeployedOnly  bool
	OutputDir     string
	OutputFile    string
	Wait          bool
	Runtime       StagedProviderBuildRuntimeV1
	TTY           bool
	RunOptions    RunOptions
}

type currentAppCommandRunBackendV1 struct {
	acquire       func(context.Context, string) (*deploy.OperationLock, error)
	newStore      func(string) (providerstore.Store, error)
	readState     func(*deploy.OperationLock) (deploy.StateV1, bool, error)
	loadCurrent   currentBuildLoader
	planRuntime   func(CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error)
	matches       func(CurrentBuild, DockerExecutionPlan) (bool, error)
	planCommand   func(CurrentRuntimePlanV1, []providers.RealizedOutput, []string, bool) (ResolvedEnvironmentCommand, error)
	prepareOutput func(string, string, RuntimeUserPlan) (*oneShotOutputSession, error)
	abortOutput   func(*oneShotOutputSession) error
	publishOutput func(*oneShotOutputSession) error
	invocation    func(DockerExecutionPlan, string, *transientOutputMount) (RuntimeInvocationV1, error)
	concurrency   func(blueprint.Document, DockerExecutionPlan, *transientOutputMount) (LiveRunConcurrencyDecisionV1, error)
	newRunID      func() (string, error)
	acquireLease  func(*deploy.OperationLock, string) (*deploy.QueueEntryLeaseV1, error)
	await         func(context.Context, string, *deploy.OperationLock, deploy.LiveRunV1, bool, io.Writer) (*deploy.OperationLock, error)
	runPublished  func(context.Context, PublishedRuntimeContainerInput, PublishedRuntimeContainerRunnerV1) error
	prepareProbe  func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error)
	execution     func(DockerExecutionPlan, ResolvedEnvironmentCommand, PreparedProbeWorkspace, *transientOutputMount, string, bool, bool) (TransientContainerExecutionV1, error)
	runAdmitted   func(context.Context, string, *deploy.OperationLock, string, TransientContainerExecutionV1, RunOptions) error
}

// RunCurrentAppCommandV1 runs one state-v1 app command against the exact
// published generation and locked output catalog.
func RunCurrentAppCommandV1(ctx context.Context, input CurrentAppCommandRunInputV1) error {
	return runCurrentAppCommandV1(ctx, input, currentAppCommandRunBackendV1{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
		readState: func(operation *deploy.OperationLock) (deploy.StateV1, bool, error) {
			return operation.ReadStateV1()
		},
		loadCurrent:   ValidateCurrentBuild,
		planRuntime:   PlanCurrentRuntimeV1,
		matches:       CurrentBuildMatchesRuntimeV1,
		planCommand:   PlanCurrentAppCommandV1,
		prepareOutput: prepareOneShotOutput,
		abortOutput:   func(output *oneShotOutputSession) error { return output.abort() },
		publishOutput: func(output *oneShotOutputSession) error { return output.publish() },
		invocation:    CommandRuntimeInvocationV1,
		concurrency:   PlanLiveRunConcurrencyV1,
		newRunID:      deploy.NewLiveRunIDV1,
		acquireLease: func(operation *deploy.OperationLock, id string) (*deploy.QueueEntryLeaseV1, error) {
			return operation.AcquireLiveRunLeaseV1(id)
		},
		await:        AwaitLiveRunAdmissionWithNoticeV1,
		runPublished: RunPublishedRuntimeContainerV1,
		prepareProbe: PrepareProbeWorkspace,
		execution:    PlanTransientContainerExecutionV1,
		runAdmitted:  RunAdmittedTransientContainerV1,
	})
}

func runCurrentAppCommandV1(ctx context.Context, input CurrentAppCommandRunInputV1, backend currentAppCommandRunBackendV1) (err error) {
	if ctx == nil {
		return fmt.Errorf("run current app command requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if input.DeploymentDir == "" {
		return fmt.Errorf("run current app command requires a deployment directory")
	}
	if len(input.Arguments) == 0 {
		return fmt.Errorf("run current app command requires command arguments")
	}
	if backend.acquire == nil || backend.newStore == nil || backend.readState == nil || backend.loadCurrent == nil || backend.planRuntime == nil || backend.matches == nil || backend.planCommand == nil || backend.prepareOutput == nil || backend.abortOutput == nil || backend.publishOutput == nil || backend.invocation == nil || backend.concurrency == nil || backend.newRunID == nil || backend.acquireLease == nil || backend.await == nil || backend.runPublished == nil || backend.prepareProbe == nil || backend.execution == nil || backend.runAdmitted == nil {
		return fmt.Errorf("run current app command requires a complete backend")
	}
	dir, err := filepath.Abs(input.DeploymentDir)
	if err != nil {
		return fmt.Errorf("resolve current app command deployment directory: %w", err)
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
	if input.DeployedOnly && state.Deployment == nil {
		return fmt.Errorf("deployed-only app commands require an installed deployment")
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
	planned, err := backend.planRuntime(CurrentRuntimePlanInputV1{DeploymentDir: dir, Current: current, Runtime: input.Runtime})
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
	command, err := backend.planCommand(planned, current.Lock.Catalog, input.Arguments, input.DeployedOnly)
	if err != nil {
		return err
	}
	output, err := backend.prepareOutput(input.OutputDir, input.OutputFile, planned.Docker.Sandbox.RuntimeUser)
	if err != nil {
		return err
	}
	if output == nil {
		return fmt.Errorf("prepare current app command output returned no session")
	}
	abort := func(cause error) error {
		if cleanupErr := backend.abortOutput(output); cleanupErr != nil {
			return fmt.Errorf("%w; output cleanup failed: %v", cause, cleanupErr)
		}
		return cause
	}
	invocation, err := backend.invocation(planned.Docker, command.Name, output.mount)
	if err != nil {
		return abort(err)
	}
	concurrency, err := backend.concurrency(document, planned.Docker, output.mount)
	if err != nil {
		return abort(err)
	}
	runID, err := backend.newRunID()
	if err != nil {
		return abort(err)
	}
	lease, err := backend.acquireLease(operation, runID)
	if err != nil {
		return abort(fmt.Errorf("acquire app-command queue ownership: %w", err))
	}
	defer func() {
		if leaseErr := lease.Release(); leaseErr != nil {
			err = errors.Join(err, fmt.Errorf("release app-command queue ownership: %w", leaseErr))
		}
	}()
	candidate := deploy.LiveRunV1{
		ID: runID, Kind: deploy.LiveRunKindAppV1, Name: command.Name,
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
		return abort(err)
	}
	runOptions := input.RunOptions
	phase, color := stagingOutputPhase, stagingOutputColor
	if state.Deployment != nil {
		phase, color = deployedOutputPhase, deployedOutputColor
	}
	label := deploymentOutputLabel(phase, document.Environment.ID)
	runOptions.Stdout = newDeploymentOutputWriter(runOptions.Stdout, label, color)
	runOptions.Stderr = newDeploymentOutputWriter(runOptions.Stderr, label, color)
	runOptions.Context = ctx
	published := PublishedRuntimeContainerInput{
		Operation: operation, Store: store, Environment: document.Environment.ID,
		DeploymentDir: dir, DockerPlan: planned.Docker, Invocation: invocation,
	}
	var commandRunErr error
	var helperCleanupErr error
	callbackEntered := false
	runErr := backend.runPublished(ctx, published, func(runCtx context.Context, gated CurrentBuild) error {
		callbackEntered = true
		if gated.Generation.Reference != candidate.GenerationReference {
			return removeAdmittedTransientBeforeCreateV1(operation, runID, fmt.Errorf(
				"deployment generation changed while live run %q was waiting; retry the command", runID,
			))
		}
		workspace, cleanup, err := backend.prepareProbe(runCtx, store, gated.Lock.Platform)
		if err != nil {
			return removeAdmittedTransientBeforeCreateV1(operation, runID, err)
		}
		interactive := runOptions.Stdin != nil
		execution, err := backend.execution(planned.Docker, command, workspace, output.mount, runID, interactive, interactive && input.TTY)
		if err != nil {
			return removeAdmittedTransientBeforeCreateV1(operation, runID, errors.Join(err, cleanup()))
		}
		options := runOptions
		options.Context = runCtx
		commandRunErr = backend.runAdmitted(runCtx, dir, operation, runID, execution, options)
		helperCleanupErr = cleanup()
		return errors.Join(commandRunErr, helperCleanupErr)
	})
	if runErr != nil {
		if !callbackEntered {
			runErr = removeAdmittedTransientBeforeCreateV1(operation, runID, runErr)
		}
		if commandRunErr != nil {
			return abort(errors.Join(appCommandError(commandRunErr), helperCleanupErr))
		}
		return abort(runErr)
	}
	if err := backend.publishOutput(output); err != nil {
		return fmt.Errorf("app command output: %w", err)
	}
	return nil
}
