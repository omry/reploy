package dockerdeploy

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type ProviderStoreCleanResultV1 struct {
	Path      string
	Removed   bool
	Recovered bool
}

type providerStoreCleanBackend struct {
	acquire  func(context.Context, string) (*deploy.OperationLock, error)
	newStore func(string) (providerstore.Store, error)
	recover  func(
		context.Context,
		*deploy.OperationLock,
		providerstore.Store,
		*deploy.EnvironmentGenerationState,
		string,
		string,
		providers.RequirementProfileOwnerValidator,
		providers.ResolvedBundleOwnerValidator,
	) (bool, error)
	remove func(*deploy.OperationLock, providerstore.Store) (bool, error)
}

// CleanProviderStoreV1 removes only the selected deployment's provider cache.
// The committed generation, its build lock, and its Docker reference remain
// current and runtime-usable. Recovery runs first under the same operation lock
// so clean cannot race an interrupted publication or install transfer.
func CleanProviderStoreV1(ctx context.Context, deploymentDir string) (ProviderStoreCleanResultV1, error) {
	return cleanProviderStoreV1(ctx, deploymentDir, providerStoreCleanBackend{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
		recover:  RecoverPendingPublication,
		remove: func(operation *deploy.OperationLock, store providerstore.Store) (bool, error) {
			return operation.RemoveProviderStore(store)
		},
	})
}

func cleanProviderStoreV1(
	ctx context.Context,
	deploymentDir string,
	backend providerStoreCleanBackend,
) (result ProviderStoreCleanResultV1, err error) {
	if ctx == nil {
		return ProviderStoreCleanResultV1{}, fmt.Errorf("clean provider store requires a context")
	}
	if err := ctx.Err(); err != nil {
		return ProviderStoreCleanResultV1{}, err
	}
	if deploymentDir == "" {
		return ProviderStoreCleanResultV1{}, fmt.Errorf("clean provider store requires a deployment directory")
	}
	if backend.acquire == nil || backend.newStore == nil || backend.recover == nil || backend.remove == nil {
		return ProviderStoreCleanResultV1{}, fmt.Errorf("clean provider store requires a complete backend")
	}
	absoluteDir, err := filepath.Abs(deploymentDir)
	if err != nil {
		return ProviderStoreCleanResultV1{}, fmt.Errorf("resolve provider store deployment directory: %w", err)
	}
	operation, err := backend.acquire(ctx, absoluteDir)
	if err != nil {
		return ProviderStoreCleanResultV1{}, err
	}
	defer func() {
		if unlockErr := operation.Unlock(); err == nil && unlockErr != nil {
			err = unlockErr
		}
	}()

	state, found, err := operation.ReadStateV1()
	if err != nil {
		return ProviderStoreCleanResultV1{}, fmt.Errorf("clean provider store state: %w", err)
	}
	if !found {
		return ProviderStoreCleanResultV1{}, fmt.Errorf("deployment state is missing; stage or install the deployment first")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return ProviderStoreCleanResultV1{}, fmt.Errorf("clean provider store blueprint: %w", err)
	}
	store, err := backend.newStore(absoluteDir)
	if err != nil {
		return ProviderStoreCleanResultV1{}, err
	}
	existedBefore, err := store.Exists()
	if err != nil {
		return ProviderStoreCleanResultV1{}, err
	}
	recovered, err := backend.recover(
		ctx, operation, store, state.Current, document.Environment.ID, absoluteDir,
		registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1,
	)
	if err != nil {
		return ProviderStoreCleanResultV1{}, fmt.Errorf("clean provider store recovery: %w", err)
	}
	removed, err := backend.remove(operation, store)
	if err != nil {
		return ProviderStoreCleanResultV1{}, err
	}
	return ProviderStoreCleanResultV1{Path: store.Root(), Removed: existedBefore || removed, Recovered: recovered}, nil
}
