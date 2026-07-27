package dockerdeploy

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

type CurrentRuntimeTestInputV1 struct {
	DeploymentDir          string
	Runtime                StagedProviderBuildRuntimeV1
	Timeout                time.Duration
	Stdout                 io.Writer
	RestartingDiagnostics  string
	DockerPreflightTimeout time.Duration
}

type currentRuntimeTestBackendV1 struct {
	acquire       func(context.Context, string) (*deploy.OperationLock, error)
	newStore      func(string) (providerstore.Store, error)
	readState     func(*deploy.OperationLock) (deploy.StateV1, bool, error)
	loadCurrent   currentBuildLoader
	plan          func(CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error)
	matches       func(CurrentBuild, DockerExecutionPlan) (bool, error)
	requireInputs func(*deploy.OperationLock, string, CurrentRuntimePlanV1) error
	serviceCheck  func(string, string, time.Duration) error
	wait          func(context.Context, EndpointExecutionPlan, func(context.Context) error) error
}

// RunCurrentRuntimeTestV1 tests the already-running workload represented by
// the exact published state-v1 runtime plan.
func RunCurrentRuntimeTestV1(ctx context.Context, input CurrentRuntimeTestInputV1) error {
	return runCurrentRuntimeTestV1(ctx, input, currentRuntimeTestBackendV1{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
		readState: func(operation *deploy.OperationLock) (deploy.StateV1, bool, error) {
			return operation.ReadStateV1()
		},
		loadCurrent:   ValidateCurrentBuild,
		plan:          PlanCurrentRuntimeV1,
		matches:       CurrentBuildMatchesRuntimeV1,
		requireInputs: RequireCurrentRuntimeInputsV1,
		serviceCheck:  requireComposeServiceRunning,
		wait:          WaitForHTTPReadinessWithServiceCheck,
	})
}

func runCurrentRuntimeTestV1(ctx context.Context, input CurrentRuntimeTestInputV1, backend currentRuntimeTestBackendV1) (err error) {
	if ctx == nil {
		return fmt.Errorf("run current runtime test requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if input.DeploymentDir == "" {
		return fmt.Errorf("run current runtime test requires a deployment directory")
	}
	if backend.acquire == nil || backend.newStore == nil || backend.readState == nil || backend.loadCurrent == nil || backend.plan == nil || backend.matches == nil || backend.requireInputs == nil || backend.serviceCheck == nil || backend.wait == nil {
		return fmt.Errorf("run current runtime test requires a complete backend")
	}
	dir, err := filepath.Abs(input.DeploymentDir)
	if err != nil {
		return fmt.Errorf("resolve current runtime test deployment directory: %w", err)
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
	if err := backend.requireInputs(operation, dir, planned); err != nil {
		return err
	}
	if planned.Docker.Workload == nil {
		return fmt.Errorf("environment has no workload to test")
	}
	names := make([]string, 0, len(planned.Docker.Workload.Endpoints))
	for name, endpoint := range planned.Docker.Workload.Endpoints {
		if endpoint.Readiness != nil {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return fmt.Errorf("environment workload has no readiness endpoint")
	}
	sort.Strings(names)
	phase, color := stagingOutputPhase, stagingOutputColor
	if state.Deployment != nil {
		phase, color = deployedOutputPhase, deployedOutputColor
	}
	stdout := newDeploymentOutputWriter(input.Stdout, deploymentOutputLabel(phase, document.Environment.ID), color)
	if err := backend.serviceCheck(dir, input.RestartingDiagnostics, input.DockerPreflightTimeout); err != nil {
		return err
	}
	for _, name := range names {
		endpoint := planned.Docker.Workload.Endpoints[name]
		readiness := *endpoint.Readiness
		readiness.Timeout = input.Timeout
		endpoint.Readiness = &readiness
		if err := backend.wait(ctx, endpoint, func(context.Context) error {
			return backend.serviceCheck(dir, input.RestartingDiagnostics, input.DockerPreflightTimeout)
		}); err != nil {
			return err
		}
		if stdout != nil {
			fmt.Fprintf(stdout, "ok: %s\n", readinessTarget(endpoint))
		}
	}
	return nil
}
