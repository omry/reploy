package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

var bindInstalledServiceCommandRunner = bindPinnedDockerCommandRunnerV1

// RunInstalledServiceContainerV1 is the system-service container boundary. It
// deliberately bypasses the public control surface because systemd itself is
// already the admitted host-service operation.
func RunInstalledServiceContainerV1(ctx context.Context, deploymentDir string, action string, dockerPath string, options RunOptions) error {
	return runInstalledServiceContainerV1(ctx, deploymentDir, action, dockerPath, options, installedServiceEngineTimeoutV1)
}

func runInstalledServiceContainerV1(ctx context.Context, deploymentDir string, action string, dockerPath string, options RunOptions, preparationTimeout time.Duration) (err error) {
	if ctx == nil {
		return fmt.Errorf("run installed service container requires a context")
	}
	if action != "run" {
		return fmt.Errorf("installed service container action must be run")
	}
	preparationStarted := time.Now()
	preparationCtx, cancelPreparation := context.WithTimeout(ctx, preparationTimeout)
	defer cancelPreparation()
	var engineStarted time.Time
	preparationDone := false
	defer func() {
		if preparationDone || err == nil {
			return
		}
		setupElapsed, engineElapsed := time.Since(preparationStarted), time.Duration(0)
		if !engineStarted.IsZero() {
			setupElapsed, engineElapsed = engineStarted.Sub(preparationStarted), time.Since(engineStarted)
		}
		err = fmt.Errorf("installed service preparation failed; lock/setup=%s; engine wait=%s; preparation=%s (limit %s); application startup=not started: %w", setupElapsed.Round(time.Millisecond), engineElapsed.Round(time.Millisecond), (setupElapsed + engineElapsed).Round(time.Millisecond), preparationTimeout, err)
	}()
	operation, err := deploy.AcquireOperationLock(preparationCtx, deploymentDir)
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
	start := composeCommandWithProject(deploymentDir, installation.ComposeProject, "up", "--pull", "never", "-d")
	start.Name = dockerPath
	engineStarted = time.Now()
	start, runDocker, err := waitInstalledServiceEngineV1(preparationCtx, start, options, installedServiceEngineWaitV1{
		bind: bindInstalledServiceCommandRunner, timeout: preparationTimeout, interval: time.Second,
	})
	if err != nil {
		return fmt.Errorf("bind installed service Docker endpoint: %w", err)
	}
	if err := preparationCtx.Err(); err != nil {
		return err
	}
	preparationDone = true
	cancelPreparation()
	serviceCtx := ctx
	startupCtx, timing, finishBudget := newInstalledServiceStartupBudgetV1(ctx, engineStarted.Sub(preparationStarted), time.Since(engineStarted), installedServiceStartupTimeoutV1, options.Stderr)
	var lifecycle *workloadLifecycleV1
	var deferredCleanup func(context.Context) error
	var failureCleanupContext context.Context
	var failureCleanupCancel context.CancelFunc
	defer func() {
		err = timing.finish(startupCtx, err, finishBudget, func(cause error) error {
			return cleanupInstalledServiceStartupFailureV1(serviceCtx, lifecycle, deferredCleanup, failureCleanupContext, failureCleanupCancel, cause)
		})
	}()
	ctx = startupCtx
	timing.step("build validation")
	current, currentFound, err := ValidateCurrentBuild(ctx, operation, store, document.Environment.ID, deploymentDir)
	if err != nil {
		return fmt.Errorf("installed service current build: %w", err)
	}
	if !currentFound {
		return fmt.Errorf("installed service build is missing; rerun the original `reploy install` command")
	}
	timing.step("runtime preparation")
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
	timing.step("private environment preparation")
	environment, err := preparePrivateWorkloadEnvironmentV1(deploymentDir)
	if err != nil {
		return fmt.Errorf("prepare private workload environment: %w", err)
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
	timing.step("publish runtime inputs")
	if _, err := PublishCurrentRuntimeInputsV1(operation, deploymentDir, plan); err != nil {
		return err
	}
	lifecycle = &workloadLifecycleV1{command: start, containerName: plan.Docker.ContainerName, run: runDocker, options: options,
		operation: func(_ context.Context, action func(*deploy.OperationLock) error) error { return action(operation) }}
	binding, err := workloadRuntimeBindingV1(plan.Docker)
	if err != nil {
		return err
	}
	timing.step("inspect existing workload")
	reused, _, err := lifecycle.initialize(ctx, binding, environment.Present)
	if err != nil {
		return err
	}
	if !reused && environment.Present {
		timing.step("prepare private workload runtime")
		plan.Docker.PrivateEnvironment = true
		if err := validatePrivateWorkloadEnvironmentIsolationV1(deploymentDir, plan.Docker); err != nil {
			return err
		}
		invocation, err = WorkloadRuntimeInvocationV1(plan.Docker)
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
	}
	if reused {
		if err := preserveConfirmedWorkloadRuntimeInputsV1(operation, deploymentDir, CurrentRuntimePlanV1{Document: document, Docker: plan.Docker}, lifecycle.record, PublishCurrentRuntimeInputsV1); err != nil {
			return err
		}
	}
	started := privateWorkloadStartResultV1{ContainerID: lifecycle.initial.ID, StartedAt: lifecycle.initial.StartedAt}
	if !reused {
		if lifecycle.initial.Running {
			return fmt.Errorf("installed workload startup is unconfirmed; use `reploy restart`")
		}
		timing.step("plan startup events")
		startup, err := planLockedStartLifecycleV1(plan.Document, plan.Docker, current.Lock.Catalog)
		if err != nil {
			return err
		}
		cleanupHook := installedServiceCustomEventCleanupHookV1(startupCtx, timing, finishBudget, &deferredCleanup)
		executor := environmentLifecycleExecutorWithCleanupHookV1(
			RuntimeOptions{Dir: deploymentDir, DockerPreflightTimeout: options.DockerPreflightTimeout},
			plan.Document, plan.Docker, current.Lock.RuntimePolicy, options.Stdout, options.Stderr,
			cleanupHook,
		)
		executor.Readiness = lifecycle.waitInstalledServiceReadiness
		executor.Start = func(startCtx context.Context) error {
			environment.Operation, environment.RuntimeBinding = operation, binding
			var err error
			started, err = startAndInjectPrivateWorkloadEnvironmentWithFailureCleanupHookV1(
				startCtx, start, plan.Docker.ContainerName, plan.Docker.Sandbox, environment, options, runDocker,
				injectPrivateWorkloadEnvironmentV1,
				installedServiceFailureCleanupHookV1(serviceCtx, startupCtx, timing, finishBudget, &failureCleanupContext, &failureCleanupCancel),
			)
			lifecycle.started = started
			return err
		}
		executor.beforeOperation = timing.lifecycleStep
		if err := ExecuteLifecycle(ctx, startup, executor); err != nil {
			return err
		}
		timing.step("confirm application startup")
		if err := lifecycle.complete(ctx); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	timing.step("notify service readiness")
	if err := notifyInstalledServiceReadyV1(); err != nil {
		return err
	}
	timing.mu.Lock()
	timing.ready = true
	timing.mu.Unlock()
	finishBudget()
	ctx = serviceCtx
	if err := operation.Unlock(); err != nil {
		return err
	}
	operation = nil

	waitContainer := plan.Docker.ContainerName
	if started.ContainerID != "" {
		waitContainer = started.ContainerID
	}
	return waitInstalledServiceWorkloadV1(ctx, dockerPath, waitContainer, installation.Service, options, runDocker)
}

func cleanupInstalledServiceStartupFailureV1(serviceCtx context.Context, lifecycle *workloadLifecycleV1, deferredCleanup func(context.Context) error, failureCleanupContext context.Context, failureCleanupCancel context.CancelFunc, cause error) error {
	if failureCleanupCancel != nil {
		defer failureCleanupCancel()
	}
	if lifecycle == nil {
		return cause
	}
	if deferredCleanup != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(serviceCtx), privateWorkloadEnvironmentTimeoutV1)
		defer cancel()
		rollbackErr := lifecycle.rollbackWithCleanupContextV1(cleanupCtx, cause)
		return errors.Join(rollbackErr, deferredCleanup(cleanupCtx))
	}
	if failureCleanupContext != nil {
		return lifecycle.rollbackWithCleanupContextV1(failureCleanupContext, cause)
	}
	return lifecycle.rollback(serviceCtx, cause)
}

func installedServiceCustomEventCleanupHookV1(startupCtx context.Context, timing *installedServiceStartupTimingsV1, stop func(), deferredCleanup *func(context.Context) error) func(error, func(context.Context) error) {
	return func(failure error, cleanup func(context.Context) error) {
		timing.failBeforeCleanup(startupCtx, failure, stop)
		*deferredCleanup = cleanup
	}
}

func installedServiceFailureCleanupHookV1(serviceCtx, startupCtx context.Context, timing *installedServiceStartupTimingsV1, stop func(), cleanupContext *context.Context, cleanupCancel *context.CancelFunc) func(error) context.Context {
	return func(failure error) context.Context {
		startupDeadline, hasStartupDeadline := startupCtx.Deadline()
		timing.failBeforeCleanup(startupCtx, failure, stop)
		if *cleanupContext == nil {
			cleanupBase := context.WithoutCancel(serviceCtx)
			cleanupDeadline := time.Now().Add(privateWorkloadEnvironmentTimeoutV1)
			if hasStartupDeadline && startupDeadline.Add(privateWorkloadEnvironmentTimeoutV1).Before(cleanupDeadline) {
				cleanupDeadline = startupDeadline.Add(privateWorkloadEnvironmentTimeoutV1)
			}
			*cleanupContext, *cleanupCancel = context.WithDeadline(cleanupBase, cleanupDeadline)
		}
		return *cleanupContext
	}
}
