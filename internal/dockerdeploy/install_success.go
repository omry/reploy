package dockerdeploy

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

type installSuccessLinesBackendV1 struct {
	acquire     func(context.Context, string) (*deploy.OperationLock, error)
	newStore    func(string) (providerstore.Store, error)
	readState   func(*deploy.OperationLock) (deploy.StateV1, bool, error)
	loadCurrent currentBuildLoader
	plan        func(CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error)
}

func PrintInstallSuccess(dir string, stdout io.Writer, dockerPreflightTimeout time.Duration) error {
	if stdout == nil {
		return nil
	}
	lines, err := InstallSuccessLines(dir, dockerPreflightTimeout)
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Fprintln(stdout, line)
	}
	return nil
}

// InstallSuccessLines reconstructs success output from the installed state-v1
// generation. The timeout remains in the public signature until the broader
// Docker CLI surface stops passing it; rendering does not invoke Docker.
func InstallSuccessLines(dir string, _ time.Duration) ([]string, error) {
	runtime, err := CurrentStagedProviderBuildRuntimeV1()
	if err != nil {
		return nil, err
	}
	return installSuccessLinesV1(context.Background(), dir, runtime, installSuccessLinesBackendV1{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
		readState: func(operation *deploy.OperationLock) (deploy.StateV1, bool, error) {
			return operation.ReadStateV1()
		},
		loadCurrent: LoadRecordedCurrentBuildV1,
		plan:        PlanCurrentRuntimeV1,
	})
}

func installSuccessLinesV1(
	ctx context.Context,
	dir string,
	runtime StagedProviderBuildRuntimeV1,
	backend installSuccessLinesBackendV1,
) (lines []string, err error) {
	if ctx == nil {
		return nil, fmt.Errorf("resolve install success output requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if dir == "" {
		return nil, fmt.Errorf("resolve install success output requires a deployment directory")
	}
	if backend.acquire == nil || backend.newStore == nil || backend.readState == nil || backend.loadCurrent == nil || backend.plan == nil {
		return nil, fmt.Errorf("resolve install success output requires a complete backend")
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve install success deployment directory: %w", err)
	}
	operation, err := backend.acquire(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if unlockErr := operation.Unlock(); err == nil && unlockErr != nil {
			err = unlockErr
		}
	}()
	store, err := backend.newStore(dir)
	if err != nil {
		return nil, err
	}
	state, found, err := backend.readState(operation)
	if err != nil {
		return nil, err
	}
	if !found || state.Deployment == nil {
		return nil, fmt.Errorf("installed deployment state is missing; rerun `reploy install`")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return nil, fmt.Errorf("install success blueprint: %w", err)
	}
	current, found, err := backend.loadCurrent(ctx, operation, store, document.Environment.ID, dir)
	if err != nil {
		return nil, fmt.Errorf("install success current build: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("installed build is missing; rerun `reploy install`")
	}
	planned, err := backend.plan(CurrentRuntimePlanInputV1{DeploymentDir: dir, Current: current, Runtime: runtime})
	if err != nil {
		return nil, err
	}
	return resolveInstallSuccessLines(planned.Document, planned.Docker)
}
