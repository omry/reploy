package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

func TestRunProviderBuildV1HoldsOneLockAcrossPreparationAndExecution(t *testing.T) {
	dir, document := stageProviderBuildRunState(t, false)
	order := []string{}
	want := LockedProviderBuildExecutionResultV1{Reused: true}

	result, err := runProviderBuildV1(t.Context(), ProviderBuildRunInputV1{
		DeploymentDir: dir, NoCache: true, ValidateLayers: true,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1001, GID: 1002},
	}, providerBuildRunBackend{
		cleanupFailure: providerBuildRunCleanupFixture,
		acquire:        deploy.AcquireOperationLock,
		newStore:       providerstore.NewStore,
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			order = append(order, "prepare")
			if err := input.Operation.RequireHeld(); err != nil {
				t.Fatal(err)
			}
			if input.Environment != document.Environment.ID || input.DeploymentDir != dir || !input.NoCache || input.Store.Root() != dir+"/.reploy/provider-store" || len(input.Sources) != 0 {
				t.Fatalf("preparation input = %#v", input)
			}
			if input.DockerPlan.EnvironmentID != "demo" || input.DockerPlan.Phase != blueprint.PhaseStaged || input.DockerPlan.Image != providerBuildPlanImage || input.DockerPlan.Scope != nil || input.DockerPlan.RuntimeUser.UID != 1001 || input.DockerPlan.RuntimeUser.GID != 1002 {
				t.Fatalf("Docker plan = %#v", input.DockerPlan)
			}
			return LockedProviderBuildPreparationV1{Operation: input.Operation, Store: input.Store}, nil
		},
		execute: func(_ context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			order = append(order, "execute")
			if err := input.Preparation.Operation.RequireHeld(); err != nil {
				t.Fatal(err)
			}
			if !input.ValidateLayers || !input.RunOptions.NoCache || len(input.SourceWheels) != 0 {
				t.Fatalf("execution input = %#v", input)
			}
			return want, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result, want) || !reflect.DeepEqual(order, []string{"prepare", "execute"}) {
		t.Fatalf("result/order = %#v/%#v", result, order)
	}
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal("build did not release operation lock:", err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestRunLockedProviderBuildV1UsesAndRetainsCallerLock(t *testing.T) {
	dir, document := stageProviderBuildRunState(t, false)
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := LockedProviderBuildExecutionResultV1{Reused: true}
	order := []string{}

	result, err := runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{
		Operation: operation, Store: store, DeploymentDir: dir,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1001, GID: 1002},
		NoCache: true, ValidateLayers: true,
	}, providerBuildRunBackend{
		cleanupFailure: providerBuildRunCleanupFixture,
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			order = append(order, "prepare")
			if input.Operation != operation || input.Store.Root() != store.Root() || input.Environment != document.Environment.ID || !input.NoCache {
				t.Fatalf("preparation input = %#v", input)
			}
			return LockedProviderBuildPreparationV1{Operation: input.Operation, Store: input.Store}, nil
		},
		execute: func(_ context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			order = append(order, "execute")
			if input.Preparation.Operation != operation || !input.ValidateLayers {
				t.Fatalf("execution input = %#v", input)
			}
			return want, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result, want) || !reflect.DeepEqual(order, []string{"prepare", "execute"}) {
		t.Fatalf("result/order = %#v/%#v", result, order)
	}
	if err := operation.RequireHeld(); err != nil {
		t.Fatalf("caller lock was released: %v", err)
	}
}

func TestRunLockedProviderBuildV1CleansFailedExecutionAndPreservesItsError(t *testing.T) {
	dir, _ := stageProviderBuildRunState(t, false)
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("graph failed")
	cleaned := false

	_, err = runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{
		Operation: operation, Store: store, DeploymentDir: dir,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux},
	}, providerBuildRunBackend{
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			return LockedProviderBuildPreparationV1{
				Operation: input.Operation, Store: input.Store, Environment: input.Environment,
				DeploymentDir: input.DeploymentDir,
			}, nil
		},
		execute: func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			return LockedProviderBuildExecutionResultV1{}, want
		},
		cleanupFailure: func(_ context.Context, preparation LockedProviderBuildPreparationV1) error {
			cleaned = true
			if preparation.Operation != operation || preparation.Store.Root() != store.Root() || preparation.Environment != "demo" || preparation.DeploymentDir != dir {
				t.Fatalf("cleanup preparation = %#v", preparation)
			}
			return nil
		},
	})
	if !errors.Is(err, want) || !cleaned {
		t.Fatalf("error/cleaned = %v/%v", err, cleaned)
	}
}

func TestCleanupFailedProviderBuildV1RemovesOnlyUnreachableObjects(t *testing.T) {
	dir, operation, store, lock, _ := currentBuildFixture(t, true)
	defer operation.Unlock()
	dropped, err := store.Publish(t.Context(), "failed.deb", "deb", strings.NewReader("failed candidate"))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := store.NewWorkspace("failed-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanupFailedProviderBuildV1(t.Context(), LockedProviderBuildPreparationV1{
		Operation: operation, Store: store, Environment: "demo", DeploymentDir: dir,
	}); err != nil {
		t.Fatal(err)
	}
	droppedPath, err := store.BlobPath(dropped.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(droppedPath); !os.IsNotExist(err) {
		t.Fatalf("unreachable failed-build blob remains: %v", err)
	}
	if _, err := os.Lstat(workspace); !os.IsNotExist(err) {
		t.Fatalf("failed-build workspace remains: %v", err)
	}
	if _, found, err := operation.ReadBuildLock(operationCurrentDigest(t, operation), registry.ValidateRequirementProfileV1); err != nil || !found {
		t.Fatalf("current build lock was not preserved: found=%v error=%v lock=%#v", found, err, lock)
	}
}

func TestCleanupFailedProviderBuildV1WithoutCurrentRemovesAllBuildObjects(t *testing.T) {
	dir, _ := stageProviderBuildRunState(t, false)
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	dropped, err := store.Publish(t.Context(), "failed.deb", "deb", strings.NewReader("failed candidate"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanupFailedProviderBuildV1(t.Context(), LockedProviderBuildPreparationV1{
		Operation: operation, Store: store, Environment: "demo", DeploymentDir: dir,
	}); err != nil {
		t.Fatal(err)
	}
	droppedPath, err := store.BlobPath(dropped.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(droppedPath); !os.IsNotExist(err) {
		t.Fatalf("unpublished failed-build blob remains: %v", err)
	}
}

func operationCurrentDigest(t *testing.T, operation *deploy.OperationLock) canonical.Digest {
	t.Helper()
	state, found, err := operation.ReadStateV1()
	if err != nil || !found || state.Current == nil {
		t.Fatalf("read current state: found=%v error=%v state=%#v", found, err, state)
	}
	return state.Current.BuildLockDigest
}

func TestRunLockedProviderBuildV1RejectsForeignStoreBeforeProviderWork(t *testing.T) {
	dir, _ := stageProviderBuildRunState(t, false)
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	foreignStore, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	called := false
	_, err = runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{
		Operation: operation, Store: foreignStore, DeploymentDir: dir,
	}, providerBuildRunBackend{
		cleanupFailure: providerBuildRunCleanupFixture,
		prepare: func(context.Context, LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			called = true
			return LockedProviderBuildPreparationV1{}, nil
		},
		execute: func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			called = true
			return LockedProviderBuildExecutionResultV1{}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "does not belong to the locked deployment") || called {
		t.Fatalf("error/called = %v/%v", err, called)
	}
}

func TestRunProviderBuildV1RequiresSourcePreparationForTranslations(t *testing.T) {
	dir, _ := stageProviderBuildRunState(t, true)
	called := false
	_, err := runProviderBuildV1(t.Context(), ProviderBuildRunInputV1{DeploymentDir: dir}, providerBuildRunBackend{
		cleanupFailure: providerBuildRunCleanupFixture,
		acquire:        deploy.AcquireOperationLock,
		newStore:       providerstore.NewStore,
		prepare: func(context.Context, LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			called = true
			return LockedProviderBuildPreparationV1{}, nil
		},
		execute: func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			called = true
			return LockedProviderBuildExecutionResultV1{}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "translations are not implemented") || called {
		t.Fatalf("error/called = %v/%v", err, called)
	}
	operation, lockErr := deploy.AcquireOperationLock(t.Context(), dir)
	if lockErr != nil {
		t.Fatal("rejected build retained operation lock:", lockErr)
	}
	if unlockErr := operation.Unlock(); unlockErr != nil {
		t.Fatal(unlockErr)
	}
}

func TestRunProviderBuildV1RejectsInvalidRuntimeContextBeforeProviderWork(t *testing.T) {
	dir, _ := stageProviderBuildRunState(t, false)
	called := false
	_, err := runProviderBuildV1(t.Context(), ProviderBuildRunInputV1{
		DeploymentDir: dir,
		Runtime:       StagedProviderBuildRuntimeV1{Host: blueprint.HostOS("plan9")},
	}, providerBuildRunBackend{
		cleanupFailure: providerBuildRunCleanupFixture,
		acquire:        deploy.AcquireOperationLock,
		newStore:       providerstore.NewStore,
		prepare: func(context.Context, LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			called = true
			return LockedProviderBuildPreparationV1{}, nil
		},
		execute: func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			called = true
			return LockedProviderBuildExecutionResultV1{}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported Docker host") || called {
		t.Fatalf("error/called = %v/%v", err, called)
	}
	operation, lockErr := deploy.AcquireOperationLock(t.Context(), dir)
	if lockErr != nil {
		t.Fatal("runtime-plan failure retained operation lock:", lockErr)
	}
	if unlockErr := operation.Unlock(); unlockErr != nil {
		t.Fatal(unlockErr)
	}
}

func TestStagedProviderBuildRuntimeV1MapsSupportedHosts(t *testing.T) {
	tests := []struct {
		goos string
		host blueprint.HostOS
	}{
		{goos: "linux", host: blueprint.HostLinux},
		{goos: "darwin", host: blueprint.HostMacOS},
		{goos: "windows", host: blueprint.HostWindows},
	}
	for _, test := range tests {
		t.Run(test.goos, func(t *testing.T) {
			got, err := stagedProviderBuildRuntimeV1(test.goos, 501, 20)
			if err != nil {
				t.Fatal(err)
			}
			want := StagedProviderBuildRuntimeV1{Host: test.host, UID: 501, GID: 20}
			if got != want {
				t.Fatalf("runtime = %#v, want %#v", got, want)
			}
		})
	}
	if _, err := stagedProviderBuildRuntimeV1("plan9", 1, 2); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error = %v", err)
	}
}

func stageProviderBuildRunState(t *testing.T, translation bool) (string, blueprint.Document) {
	t.Helper()
	dir := t.TempDir()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	document := blueprint.Document{
		Blueprint: blueprint.Metadata{Compatibility: blueprint.Compatibility{Platforms: []blueprint.Platform{platform}}},
		Environment: blueprint.Environment{
			ID: "demo", Translations: map[string]blueprint.Translation{},
			Components: map[string]blueprint.Component{
				"base": {Type: blueprint.ComponentTypeBase, Base: &blueprint.BaseComponent{Image: "debian:13", Exports: map[string]blueprint.BaseExecutableExport{}}},
			},
		},
	}
	if translation {
		document.Environment.Translations["local"] = blueprint.Translation{
			Type: blueprint.ComponentTypePython, Scope: blueprint.TranslationScopeDevelopment,
			Root: ".", Mappings: map[string]string{"demo": "demo"},
		}
	}
	if _, err := deploy.SetDesiredStateV1(t.Context(), dir, document, platform, nil); err != nil {
		t.Fatal(err)
	}
	return dir, document
}

func providerBuildRunCleanupFixture(context.Context, LockedProviderBuildPreparationV1) error {
	return nil
}
