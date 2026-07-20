package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type LockedProviderBuildPreparationInputV1 struct {
	Operation     *deploy.OperationLock
	Store         providerstore.Store
	Environment   string
	DeploymentDir string
	Sources       []providers.ResolvedSourceInput
	DockerPlan    DockerExecutionPlan
	NoCache       bool
}

type LockedProviderBuildPreparationV1 struct {
	Operation        *deploy.OperationLock
	Store            providerstore.Store
	Environment      string
	DeploymentDir    string
	DockerPlan       DockerExecutionPlan
	Loaded           LoadedBuildRequestV1
	SelectedBase     SelectedProviderBase
	PreparedBase     *PreparedProviderBase
	FinalImageConfig providers.ImageConfigPolicy
	// Current is the verified previously published generation, including when
	// it is stale. ReusableLock is the only cache input for later provider work
	// and is nil under NoCache.
	Current      *CurrentBuild
	ReusableLock *deploy.BuildLockV1
	Recovered    bool
	Reused       bool
}

type providerBuildPreparationBackend struct {
	recover func(
		context.Context,
		*deploy.OperationLock,
		providerstore.Store,
		*deploy.EnvironmentGenerationState,
		string,
		string,
		providers.RequirementProfileOwnerValidator,
		providers.ResolvedBundleOwnerValidator,
	) (bool, error)
	load            func(*deploy.OperationLock, []providers.ResolvedSourceInput) (LoadedBuildRequestV1, error)
	selectBase      func(context.Context, providers.ResolvedRequestV1) (SelectedProviderBase, error)
	validateCurrent currentBuildLoader
	lockedSources   func(deploy.BuildLockV1) ([]providers.ResolvedSourceInput, error)
	matches         func(CurrentBuild, CurrentBuildReuseInput) (bool, error)
	realizeBase     func(context.Context, providerstore.Store, SelectedProviderBase) (PreparedProviderBase, error)
}

// PrepareLockedProviderBuildV1 performs the read/recovery and reuse boundary
// under a caller-held deployment lock. Exact reuse stops after immutable base
// selection. A stale or absent build realizes base outputs but does not start a
// provider resolver or construct a provider layer.
func PrepareLockedProviderBuildV1(
	ctx context.Context,
	input LockedProviderBuildPreparationInputV1,
) (LockedProviderBuildPreparationV1, error) {
	return prepareLockedProviderBuildV1(ctx, input, providerBuildPreparationBackend{
		recover:         RecoverPendingPublication,
		load:            LoadBuildRequestV1,
		selectBase:      SelectProviderBase,
		validateCurrent: ValidateCurrentBuild,
		lockedSources:   buildLockSelectedSourcesV1,
		matches:         CurrentBuildMatches,
		realizeBase:     RealizeSelectedProviderBase,
	})
}

func prepareLockedProviderBuildV1(
	ctx context.Context,
	input LockedProviderBuildPreparationInputV1,
	backend providerBuildPreparationBackend,
) (LockedProviderBuildPreparationV1, error) {
	if ctx == nil {
		return LockedProviderBuildPreparationV1{}, fmt.Errorf("prepare locked provider build requires a context")
	}
	if err := ctx.Err(); err != nil {
		return LockedProviderBuildPreparationV1{}, err
	}
	if input.Operation == nil {
		return LockedProviderBuildPreparationV1{}, fmt.Errorf("prepare locked provider build requires an operation lock")
	}
	if input.Sources == nil {
		return LockedProviderBuildPreparationV1{}, fmt.Errorf("prepare locked provider build sources must use an array")
	}
	if backend.recover == nil || backend.load == nil || backend.selectBase == nil || backend.validateCurrent == nil || backend.lockedSources == nil || backend.matches == nil || backend.realizeBase == nil {
		return LockedProviderBuildPreparationV1{}, fmt.Errorf("prepare locked provider build requires a complete backend")
	}

	state, found, err := input.Operation.ReadStateV1()
	if err != nil {
		return LockedProviderBuildPreparationV1{}, fmt.Errorf("prepare locked provider build state: %w", err)
	}
	if found && state.Deployment != nil {
		return LockedProviderBuildPreparationV1{}, fmt.Errorf("provider build requires a staged deployment; an installed deployment cannot be used as a build source")
	}
	var generation *deploy.EnvironmentGenerationState
	if found {
		generation = state.Current
	}
	recovered, err := backend.recover(
		ctx, input.Operation, input.Store, generation, input.Environment, input.DeploymentDir,
		registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1,
	)
	if err != nil {
		return LockedProviderBuildPreparationV1{}, fmt.Errorf("prepare locked provider build recovery: %w", err)
	}
	loaded, err := backend.load(input.Operation, append([]providers.ResolvedSourceInput{}, input.Sources...))
	if err != nil {
		return LockedProviderBuildPreparationV1{}, err
	}
	if _, err := RuntimePlansV1(loaded.Document, input.DockerPlan); err != nil {
		return LockedProviderBuildPreparationV1{}, fmt.Errorf("prepare locked provider build runtime plan: %w", err)
	}
	selected, err := backend.selectBase(ctx, loaded.Request)
	if err != nil {
		return LockedProviderBuildPreparationV1{}, err
	}
	result := LockedProviderBuildPreparationV1{
		Operation: input.Operation, Store: input.Store, Environment: input.Environment,
		DeploymentDir: input.DeploymentDir, DockerPlan: input.DockerPlan,
		Loaded: loaded, SelectedBase: selected, Recovered: recovered,
	}

	current, currentFound, err := backend.validateCurrent(
		ctx, input.Operation, input.Store, input.Environment, input.DeploymentDir,
	)
	if err != nil {
		return LockedProviderBuildPreparationV1{}, fmt.Errorf("prepare locked provider build current generation: %w", err)
	}
	if currentFound {
		result.Current = &current
		if !input.NoCache {
			lock := current.Lock
			result.ReusableLock = &lock
			lockedSources, err := backend.lockedSources(current.Lock)
			if err != nil {
				return LockedProviderBuildPreparationV1{}, err
			}
			lockedRequest, exactSources, err := resolvedRequestForLockedSourcesV1(
				loaded.Document, loaded.State.Overlay, loaded.Request, lockedSources,
			)
			if err != nil {
				return LockedProviderBuildPreparationV1{}, err
			}
			if exactSources {
				matches, err := backend.matches(current, CurrentBuildReuseInput{
					ResolvedRequest: lockedRequest, Overlay: loaded.State.Overlay, Base: selected.Descriptor,
					Document: loaded.Document, DockerPlan: input.DockerPlan,
				})
				if err != nil {
					return LockedProviderBuildPreparationV1{}, err
				}
				if matches {
					result.Reused = true
					return result, nil
				}
			}
		}
	}

	prepared, err := backend.realizeBase(ctx, input.Store, selected)
	if err != nil {
		return LockedProviderBuildPreparationV1{}, err
	}
	config, err := ProviderFinalImageConfigV1(prepared.Config)
	if err != nil {
		return LockedProviderBuildPreparationV1{}, err
	}
	result.PreparedBase = &prepared
	result.FinalImageConfig = config
	return result, nil
}
