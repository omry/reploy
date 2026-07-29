package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

func TestCleanProviderStoreV1RemovesOnlyProviderStoreAndIsRepeatable(t *testing.T) {
	stubNoAbandonedBuildReferences(t)
	dir, operation, store, lock, state := currentBuildFixture(t, true)
	statePath := filepath.Join(dir, ".reploy", "state.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	lockDigest := state.Current.BuildLockDigest
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}

	result, err := CleanProviderStoreV1(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Removed || result.Recovered || result.Path != store.Root() {
		t.Fatalf("clean result = %#v", result)
	}
	if _, err := os.Lstat(store.Root()); !os.IsNotExist(err) {
		t.Fatalf("provider store remains: %v", err)
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("clean changed committed deployment state")
	}
	operation, err = deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, found, err := operation.ReadBuildLock(lockDigest, registry.ValidateRequirementProfileV1)
	if err != nil || !found || !reflect.DeepEqual(loaded, lock) {
		t.Fatalf("retained build lock = %#v, %v, %v", loaded, found, err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}

	result, err = CleanProviderStoreV1(t.Context(), dir)
	if err != nil || result.Removed || result.Path != store.Root() {
		t.Fatalf("repeat clean result = %#v, %v", result, err)
	}
}

func TestCleanProviderStoreV1RecoversBeforeRemovingUnderOneLock(t *testing.T) {
	dir, document := stageProviderBuildRunState(t, false)
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(t.Context(), "packages/recovery.deb", "deb", strings.NewReader("recovery")); err != nil {
		t.Fatal(err)
	}
	order := []string{}
	result, err := cleanProviderStoreV1(t.Context(), dir, providerStoreCleanBackend{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
		recover: func(_ context.Context, operation *deploy.OperationLock, store providerstore.Store, current *deploy.EnvironmentGenerationState, environment string, deploymentDir string, validateProfile providers.RequirementProfileOwnerValidator, validateBundle providers.ResolvedBundleOwnerValidator) (bool, error) {
			order = append(order, "recover")
			if err := operation.RequireHeld(); err != nil {
				t.Fatal(err)
			}
			if current != nil || environment != document.Environment.ID || deploymentDir != dir || store.Root() != filepath.Join(dir, ".reploy", providerstore.StoreDirName) || validateProfile == nil || validateBundle == nil {
				t.Fatal("recovery inputs changed")
			}
			removed, err := operation.RemoveProviderStore(store)
			if err != nil || !removed {
				t.Fatalf("recovery cleanup = %v, %v", removed, err)
			}
			return true, nil
		},
		remove: func(operation *deploy.OperationLock, store providerstore.Store) (bool, error) {
			order = append(order, "remove")
			if err := operation.RequireHeld(); err != nil {
				t.Fatal(err)
			}
			return operation.RemoveProviderStore(store)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Recovered || !result.Removed || !reflect.DeepEqual(order, []string{"recover", "remove"}) {
		t.Fatalf("result/order = %#v/%#v", result, order)
	}
}

func TestCleanProviderStoreV1PreservesStoreWhenRecoveryFails(t *testing.T) {
	dir, _ := stageProviderBuildRunState(t, false)
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(t.Context(), "packages/demo.deb", "deb", strings.NewReader("demo")); err != nil {
		t.Fatal(err)
	}
	want := errors.New("recovery failed")
	_, err = cleanProviderStoreV1(t.Context(), dir, providerStoreCleanBackend{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
		recover: func(context.Context, *deploy.OperationLock, providerstore.Store, *deploy.EnvironmentGenerationState, string, string, providers.RequirementProfileOwnerValidator, providers.ResolvedBundleOwnerValidator) (bool, error) {
			return false, want
		},
		remove: func(*deploy.OperationLock, providerstore.Store) (bool, error) {
			t.Fatal("clean removed provider store after failed recovery")
			return false, nil
		},
	})
	if !errors.Is(err, want) {
		t.Fatalf("clean error = %v", err)
	}
	if _, err := os.Stat(store.Root()); err != nil {
		t.Fatalf("failed clean changed provider store: %v", err)
	}
}

func TestCleanProviderStoreV1CancellationWhileWaitingForLockLeavesStore(t *testing.T) {
	dir, _ := stageProviderBuildRunState(t, false)
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(t.Context(), "packages/demo.deb", "deb", strings.NewReader("demo")); err != nil {
		t.Fatal(err)
	}
	held, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Unlock()

	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, cleanErr := cleanProviderStoreV1(ctx, dir, providerStoreCleanBackend{
			acquire: func(ctx context.Context, deploymentDir string) (*deploy.OperationLock, error) {
				close(started)
				return deploy.AcquireOperationLock(ctx, deploymentDir)
			},
			newStore: providerstore.NewStore,
			recover:  RecoverPendingPublication,
			remove: func(operation *deploy.OperationLock, store providerstore.Store) (bool, error) {
				return operation.RemoveProviderStore(store)
			},
		})
		finished <- cleanErr
	}()
	<-started
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled clean error = %v", err)
	}
	if _, err := os.Stat(store.Root()); err != nil {
		t.Fatalf("cancelled clean changed provider store: %v", err)
	}
}

func TestCleanProviderStoreV1RejectsLegacyStateWithoutRemovingStore(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".reploy")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"schema_version":1,"bundle":{"prepared_fingerprint":"secret"}}`)
	statePath := filepath.Join(stateDir, "state.json")
	if err := os.WriteFile(statePath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(t.Context(), "packages/demo.deb", "deb", strings.NewReader("demo")); err != nil {
		t.Fatal(err)
	}

	_, err = CleanProviderStoreV1(t.Context(), dir)
	if !errors.Is(err, deploy.ErrLegacyStateUnsupported) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("legacy clean error = %v", err)
	}
	after, readErr := os.ReadFile(statePath)
	if readErr != nil || !bytes.Equal(after, legacy) {
		t.Fatalf("legacy state changed: %q, %v", after, readErr)
	}
	if _, err := os.Stat(store.Root()); err != nil {
		t.Fatalf("legacy clean removed provider store: %v", err)
	}
}
