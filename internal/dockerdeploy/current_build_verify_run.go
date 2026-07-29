package dockerdeploy

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

type VerifyCurrentBuildInputV1 struct {
	DeploymentDir string
	Runtime       StagedProviderBuildRuntimeV1
}

type VerifyCurrentBuildResultV1 struct {
	Environment string
	Reference   string
	Details     CurrentBuildVerificationResultV1
}

type verifyCurrentBuildBackendV1 struct {
	acquire     func(context.Context, string) (*deploy.OperationLock, error)
	newStore    func(string) (providerstore.Store, error)
	readState   func(*deploy.OperationLock) (deploy.StateV1, bool, error)
	loadCurrent currentBuildLoader
	plan        func(CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error)
	verify      func(context.Context, CurrentBuildVerificationInputV1) (CurrentBuildVerificationResultV1, error)
}

// VerifyCurrentBuildV1 holds the existing deployment lock through a complete,
// read-only audit of the current staged generation.
func VerifyCurrentBuildV1(
	ctx context.Context,
	input VerifyCurrentBuildInputV1,
) (VerifyCurrentBuildResultV1, error) {
	return verifyCurrentBuildV1(ctx, input, verifyCurrentBuildBackendV1{
		acquire:     deploy.AcquireExistingOperationLock,
		newStore:    providerstore.NewStore,
		readState:   func(operation *deploy.OperationLock) (deploy.StateV1, bool, error) { return operation.ReadStateV1() },
		loadCurrent: ValidateCurrentBuild,
		plan:        PlanCurrentRuntimeV1,
		verify:      VerifyLoadedCurrentBuildV1,
	})
}

func verifyCurrentBuildV1(
	ctx context.Context,
	input VerifyCurrentBuildInputV1,
	backend verifyCurrentBuildBackendV1,
) (result VerifyCurrentBuildResultV1, err error) {
	if ctx == nil {
		return VerifyCurrentBuildResultV1{}, fmt.Errorf("verify current build requires a context")
	}
	if err := ctx.Err(); err != nil {
		return VerifyCurrentBuildResultV1{}, err
	}
	if input.DeploymentDir == "" {
		return VerifyCurrentBuildResultV1{}, fmt.Errorf("verify current build requires a deployment directory")
	}
	if backend.acquire == nil ||
		backend.newStore == nil ||
		backend.readState == nil ||
		backend.loadCurrent == nil ||
		backend.plan == nil ||
		backend.verify == nil {
		return VerifyCurrentBuildResultV1{}, fmt.Errorf("verify current build requires a complete backend")
	}
	dir, err := filepath.Abs(input.DeploymentDir)
	if err != nil {
		return VerifyCurrentBuildResultV1{}, fmt.Errorf("resolve current build verification directory: %w", err)
	}
	operation, err := backend.acquire(ctx, dir)
	if err != nil {
		return VerifyCurrentBuildResultV1{}, err
	}
	defer func() {
		if unlockErr := operation.Unlock(); err == nil && unlockErr != nil {
			err = unlockErr
		}
	}()
	store, err := backend.newStore(dir)
	if err != nil {
		return VerifyCurrentBuildResultV1{}, err
	}
	state, found, err := backend.readState(operation)
	if err != nil {
		return VerifyCurrentBuildResultV1{}, fmt.Errorf("verify current build state: %w", err)
	}
	if !found {
		return VerifyCurrentBuildResultV1{}, fmt.Errorf("staging state is missing; run `reploy stage`")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return VerifyCurrentBuildResultV1{}, fmt.Errorf("verify current build blueprint: %w", err)
	}
	if state.Deployment != nil {
		return VerifyCurrentBuildResultV1{}, fmt.Errorf("current build verification requires a staged deployment")
	}
	current, found, err := backend.loadCurrent(
		ctx,
		operation,
		store,
		document.Environment.ID,
		dir,
	)
	if err != nil {
		return VerifyCurrentBuildResultV1{}, fmt.Errorf("load current build for verification: %w", err)
	}
	if !found {
		return VerifyCurrentBuildResultV1{}, fmt.Errorf("current build is missing; run `reploy build`")
	}
	runtime, err := backend.plan(CurrentRuntimePlanInputV1{
		DeploymentDir: dir,
		Current:       current,
		Runtime:       input.Runtime,
	})
	if err != nil {
		return VerifyCurrentBuildResultV1{}, fmt.Errorf("plan current build verification: %w", err)
	}
	details, err := backend.verify(ctx, CurrentBuildVerificationInputV1{
		Store: store, Current: current, Runtime: runtime,
	})
	if err != nil {
		return VerifyCurrentBuildResultV1{}, err
	}
	return VerifyCurrentBuildResultV1{
		Environment: document.Environment.ID,
		Reference:   current.Generation.Reference,
		Details:     details,
	}, nil
}
