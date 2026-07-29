package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/buildprogress"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

func TestRunProviderBuildV1HoldsOneLockAcrossPreparationAndExecution(t *testing.T) {
	dir, document := stageProviderBuildRunState(t, false)
	baseOverride := "sha256:" + strings.Repeat("a", 64)
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	overrides := deploy.EmptyPackageOverridesV1(document.Environment.ID)
	overrides.Environment.Base = &deploy.BaseImageOverrideV1{Image: baseOverride}
	if err := operation.CommitPackageOverridesV1(overrides); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	order := []string{}
	want := LockedProviderBuildExecutionResultV1{Reused: true}
	var progress strings.Builder
	var buildEvents []buildprogress.Event

	result, err := runProviderBuildV1(t.Context(), ProviderBuildRunInputV1{
		DeploymentDir: dir, NoCache: true,
		Runtime:  StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1001, GID: 1002},
		Progress: &progress,
		BuildProgress: func(event buildprogress.Event) {
			buildEvents = append(buildEvents, event)
		},
	}, providerBuildRunBackend{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			order = append(order, "prepare")
			if err := input.Operation.RequireHeld(); err != nil {
				t.Fatal(err)
			}
			if input.Environment != document.Environment.ID || input.DeploymentDir != dir || !input.NoCache || input.Store.Root() != filepath.Join(dir, ".reploy", "provider-store") || len(input.Sources) != 0 || input.BaseImage != baseOverride {
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
			if !input.RunOptions.NoCache || len(input.SourceWheels) != 0 || len(input.LocalOverrides) != 0 || input.Progress != &progress || input.BuildProgress == nil {
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
	if len(buildEvents) != 3 ||
		buildEvents[0].Phase != buildprogress.PhaseInspect ||
		buildEvents[0].Environment != document.Environment.ID ||
		buildEvents[1].Phase != buildprogress.PhasePrepare ||
		buildEvents[2].Phase != buildprogress.PhasePublish ||
		buildEvents[2].Completed != 1 || buildEvents[2].Total != 1 {
		t.Fatalf("structured build progress = %#v", buildEvents)
	}
	for _, want := range []string{
		"preparing environment demo for linux/amd64",
		"preparing component packages and image layers",
	} {
		if !strings.Contains(progress.String(), want) {
			t.Fatalf("progress missing %q:\n%s", want, progress.String())
		}
	}
	operation, err = deploy.AcquireOperationLock(t.Context(), dir)
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
		NoCache: true,
	}, providerBuildRunBackend{
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			order = append(order, "prepare")
			if input.Operation != operation || input.Store.Root() != store.Root() || input.Environment != document.Environment.ID || !input.NoCache {
				t.Fatalf("preparation input = %#v", input)
			}
			return LockedProviderBuildPreparationV1{Operation: input.Operation, Store: input.Store}, nil
		},
		execute: func(_ context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			order = append(order, "execute")
			if input.Preparation.Operation != operation {
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

func TestRunLockedProviderBuildV1DiscardsSupersededCandidateBeforeExecution(t *testing.T) {
	dir, operation, store, lock, state := currentBuildFixture(t, true)
	defer operation.Unlock()
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	overrides := deploy.EmptyPackageOverridesV1(document.Environment.ID)
	inputs, err := ValidatedBuildInputs(document, state.Overlay, overrides, dir, state.Platform)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.CommitValidatedBuildV1(deploy.ValidatedBuildV1{
		Schema:          deploy.ValidatedBuildSchemaV1,
		BlueprintDigest: inputs.BlueprintDigest, OverlayDigest: inputs.OverlayDigest,
		PackageOverridesDigest: inputs.PackageOverridesDigest, Platform: inputs.Platform,
		BuildLockDigest: state.Current.BuildLockDigest, Image: lock.FinalImage, ImageReference: state.Current.Reference,
	}); err != nil {
		t.Fatal(err)
	}

	discarded := false
	_, err = runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{
		Operation: operation, Store: store, DeploymentDir: dir,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1001, GID: 1002},
	}, providerBuildRunBackend{
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			if input.ValidatedCandidate == nil {
				t.Fatal("validated candidate was not offered to preparation")
			}
			return LockedProviderBuildPreparationV1{
				Operation: input.Operation, Store: input.Store, Environment: input.Environment,
				DeploymentDir: input.DeploymentDir, DockerPlan: input.DockerPlan,
				ValidatedCandidate: input.ValidatedCandidate,
			}, nil
		},
		discardValidated: func(context.Context, *deploy.OperationLock, string, string) error {
			discarded = true
			return nil
		},
		execute: func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			if !discarded {
				t.Fatal("provider execution started before the superseded candidate was discarded")
			}
			return LockedProviderBuildExecutionResultV1{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunLockedProviderBuildV1RebuildsAnIncompleteValidationCandidate(t *testing.T) {
	dir, operation, store, lock, state := currentBuildFixture(t, true)
	defer operation.Unlock()
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	overrides := deploy.EmptyPackageOverridesV1(document.Environment.ID)
	inputs, err := ValidatedBuildInputs(document, state.Overlay, overrides, dir, state.Platform)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.CommitValidatedBuildV1(deploy.ValidatedBuildV1{
		Schema:          deploy.ValidatedBuildSchemaV1,
		BlueprintDigest: inputs.BlueprintDigest, OverlayDigest: inputs.OverlayDigest,
		PackageOverridesDigest: inputs.PackageOverridesDigest, Platform: inputs.Platform,
		BuildLockDigest: state.Current.BuildLockDigest, Image: lock.FinalImage, ImageReference: state.Current.Reference,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := operation.RemoveProviderStore(store); err != nil {
		t.Fatal(err)
	}

	discarded := false
	_, err = runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{
		Operation: operation, Store: store, DeploymentDir: dir, ValidateChoices: true,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1001, GID: 1002},
	}, providerBuildRunBackend{
		discardValidated: func(context.Context, *deploy.OperationLock, string, string) error {
			discarded = true
			return nil
		},
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			if !discarded || input.ValidatedCandidate != nil {
				t.Fatalf("incomplete candidate reached preparation: discarded=%v candidate=%#v", discarded, input.ValidatedCandidate)
			}
			return LockedProviderBuildPreparationV1{
				Operation: input.Operation, Store: input.Store, Environment: input.Environment,
				DeploymentDir: input.DeploymentDir, DockerPlan: input.DockerPlan,
			}, nil
		},
		execute: func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			return LockedProviderBuildExecutionResultV1{Validated: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
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

func TestCleanupFailedProviderBuildV1NoCachePreservesImmutableObjects(t *testing.T) {
	dir, operation, store, _, _ := currentBuildFixture(t, true)
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
		Operation: operation, Store: store, Environment: "demo", DeploymentDir: dir, NoCache: true,
	}); err != nil {
		t.Fatal(err)
	}
	droppedPath, err := store.BlobPath(dropped.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(droppedPath); err != nil {
		t.Fatalf("no-cache cleanup removed an immutable candidate object: %v", err)
	}
	if _, err := os.Lstat(workspace); !os.IsNotExist(err) {
		t.Fatalf("no-cache cleanup retained a temporary workspace: %v", err)
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

func TestRunProviderBuildV1PassesLocalOverrideLocatorsWithoutObservation(t *testing.T) {
	dir, _ := stageProviderBuildRunState(t, true)
	prepared := false
	executed := false
	_, err := runProviderBuildV1(t.Context(), ProviderBuildRunInputV1{DeploymentDir: dir}, providerBuildRunBackend{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			prepared = true
			return LockedProviderBuildPreparationV1{Operation: input.Operation, Store: input.Store}, nil
		},
		execute: func(_ context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			executed = true
			if len(input.LocalOverrides) != 1 || input.LocalOverrides[0].Distribution != "demo" ||
				input.LocalOverrides[0].HostDir != filepath.Join(dir, "demo") {
				t.Fatalf("local overrides = %#v", input.LocalOverrides)
			}
			return LockedProviderBuildExecutionResultV1{}, nil
		},
	})
	if err != nil || !prepared || !executed {
		t.Fatalf("error/prepared/executed = %v/%v/%v", err, prepared, executed)
	}
	operation, lockErr := deploy.AcquireOperationLock(t.Context(), dir)
	if lockErr != nil {
		t.Fatal("rejected build retained operation lock:", lockErr)
	}
	if unlockErr := operation.Unlock(); unlockErr != nil {
		t.Fatal(unlockErr)
	}
}

func TestRunProviderBuildV1DoesNotObserveLocalOverridePathOnExactReuse(t *testing.T) {
	dir, _ := stageProviderBuildRunState(t, true)
	if err := os.RemoveAll(filepath.Join(dir, "demo")); err != nil {
		t.Fatal(err)
	}
	executed := false
	_, err := runProviderBuildV1(t.Context(), ProviderBuildRunInputV1{
		DeploymentDir: dir,
		Automatic:     true,
	}, providerBuildRunBackend{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			return LockedProviderBuildPreparationV1{Operation: input.Operation, Store: input.Store, Reused: true}, nil
		},
		execute: func(_ context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			executed = true
			if len(input.LocalOverrides) != 1 || input.LocalOverrides[0].HostDir != filepath.Join(dir, "demo") {
				t.Fatalf("local overrides = %#v", input.LocalOverrides)
			}
			return LockedProviderBuildExecutionResultV1{Reused: true}, nil
		},
	})
	if err != nil || !executed {
		t.Fatalf("error/executed = %v/%v", err, executed)
	}
}

func TestRunLockedProviderBuildV1RoutesUnchangedLocalSourceThroughFreshWheelBuild(t *testing.T) {
	workspaceRoot := t.TempDir()
	sourceDir := filepath.Join(workspaceRoot, "demo")
	if err := os.Mkdir(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "pyproject.toml"), []byte("[project]\nname='demo-server'\nversion='1.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, manifest, err := ObservePythonSourceManifest(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	fixture := newPreparedPythonGraphReuseFixtureWithManifest(t, manifest)
	deploymentDir := filepath.Dir(filepath.Dir(fixture.store.Root()))
	operation, err := deploy.AcquireOperationLock(t.Context(), deploymentDir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	lockDigest, err := operation.PublishBuildLock(fixture.lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(fixture.lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	generation := deploy.EnvironmentGenerationState{
		Reference: "reploy/test:g-current", ImageDigest: fixture.lock.FinalImage.Digest,
		RootFSSubject: fixture.lock.FinalImage.RootFSSubject, BuildLockDigest: lockDigest,
		Platform: fixture.lock.Platform, RuntimePolicyDigest: policyDigest,
	}
	document := providerBuildRunWorkspaceDocument(fixture.request.Platform)
	resolved, err := blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	state := deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: resolved, BlueprintSource: "blueprint",
		Platform: fixture.request.Platform, Overlay: deploy.EmptyRequestOverlayV1(), Current: &generation,
		Staging: &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1},
	}
	if err := operation.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	if err := operation.CommitPackageOverridesV1(localPythonPackageOverrides(
		"demo", "demo-server", sourceDir,
	)); err != nil {
		t.Fatal(err)
	}
	prepared := false
	executed := false
	_, err = runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{
		Operation: operation, Store: fixture.store, DeploymentDir: deploymentDir,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1001, GID: 1002},
	}, providerBuildRunBackend{
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			prepared = true
			if len(input.Sources) != 0 {
				t.Fatalf("explicit build reused source identities = %#v", input.Sources)
			}
			return LockedProviderBuildPreparationV1{Operation: operation, Store: fixture.store}, nil
		},
		execute: func(_ context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			executed = true
			if len(input.SourceWheels) != 0 || len(input.LocalOverrides) != 1 ||
				input.LocalOverrides[0].Distribution != "demo-server" {
				t.Fatalf("execution source inputs = %#v/%#v", input.SourceWheels, input.LocalOverrides)
			}
			return LockedProviderBuildExecutionResultV1{}, nil
		},
	})
	if err != nil || !prepared || !executed {
		t.Fatalf("error/prepared/executed = %v/%v/%v", err, prepared, executed)
	}

	prepared = false
	executed = false
	_, err = runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{
		Operation: operation, Store: fixture.store, DeploymentDir: deploymentDir,
		Automatic: true,
		Runtime:   StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1001, GID: 1002},
	}, providerBuildRunBackend{
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			prepared = true
			if !reflect.DeepEqual(input.Sources, fixture.request.SourceCandidates) {
				t.Fatalf("automatic reuse sources = %#v", input.Sources)
			}
			return LockedProviderBuildPreparationV1{Operation: operation, Store: fixture.store, Reused: true}, nil
		},
		execute: func(_ context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			executed = true
			if len(input.SourceWheels) != 0 || len(input.LocalOverrides) != 1 {
				t.Fatalf("automatic execution source inputs = %#v/%#v", input.SourceWheels, input.LocalOverrides)
			}
			return LockedProviderBuildExecutionResultV1{Reused: true}, nil
		},
	})
	if err != nil || !prepared || !executed {
		t.Fatalf("automatic error/prepared/executed = %v/%v/%v", err, prepared, executed)
	}
}

func TestRunProviderBuildV1RejectsInvalidRuntimeContextBeforeProviderWork(t *testing.T) {
	dir, _ := stageProviderBuildRunState(t, false)
	called := false
	_, err := runProviderBuildV1(t.Context(), ProviderBuildRunInputV1{
		DeploymentDir: dir,
		Runtime:       StagedProviderBuildRuntimeV1{Host: blueprint.HostOS("plan9")},
	}, providerBuildRunBackend{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
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
	got, err := stagedProviderBuildRuntimeV1("windows", -1, -1)
	if err != nil || got.UID != 0 || got.GID != 0 {
		t.Fatalf("Windows runtime identity = %#v, %v", got, err)
	}
	if _, err := stagedProviderBuildRuntimeV1("plan9", 1, 2); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error = %v", err)
	}
}

func stageProviderBuildRunState(t *testing.T, workspace bool) (string, blueprint.Document) {
	t.Helper()
	dir := t.TempDir()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	document := blueprint.Document{
		Blueprint: blueprint.Metadata{Compatibility: blueprint.Compatibility{Platforms: []blueprint.Platform{platform}}},
		Environment: blueprint.Environment{
			ID:   "demo",
			Base: blueprint.BaseComponent{Image: "debian:13", Exports: map[string]blueprint.BaseExecutableExport{}},
		},
	}
	if workspace {
		if err := os.Mkdir(filepath.Join(dir, "demo"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "demo", "pyproject.toml"), []byte("[project]\nname='demo'\nversion='1.0'\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var stageErr error
	if workspace {
		_, stageErr = deploy.SetStagedDesiredStateV1(t.Context(), dir, document, platform, nil, "blueprint", false)
	} else {
		_, stageErr = deploy.SetDesiredStateV1(t.Context(), dir, document, platform, nil)
	}
	if stageErr != nil {
		t.Fatal(stageErr)
	}
	if workspace {
		operation, err := deploy.AcquireOperationLock(t.Context(), dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := operation.CommitPackageOverridesV1(localPythonPackageOverrides(
			"demo", "demo", filepath.Join(dir, "demo"),
		)); err != nil {
			_ = operation.Unlock()
			t.Fatal(err)
		}
		if err := operation.Unlock(); err != nil {
			t.Fatal(err)
		}
	}
	return dir, document
}

func providerBuildRunWorkspaceDocument(platform blueprint.Platform) blueprint.Document {
	return blueprint.Document{
		Blueprint: blueprint.Metadata{Compatibility: blueprint.Compatibility{Platforms: []blueprint.Platform{platform}}},
		Environment: blueprint.Environment{
			ID: "demo",
			Base: blueprint.BaseComponent{
				Image: "debian:13", Exports: map[string]blueprint.BaseExecutableExport{
					"python": {Executable: "/usr/bin/python3"},
				},
			},
			Applications: map[string]blueprint.Application{
				"application": {
					Packages: blueprint.ApplicationPackages{Python: &blueprint.PythonComponent{
						Interpreter:  blueprint.CommandRequirement{Command: "python", Version: ">=3.11", Supplier: "base"},
						Requirements: []string{"demo-server==1.0"},
					}},
					Options: map[string]blueprint.ApplicationOption{},
				},
			},
		},
	}
}

func localPythonPackageOverrides(environmentID string, distribution string, sourceDir string) deploy.PackageOverridesV1 {
	return deploy.PackageOverridesV1{Environment: deploy.PackageOverridesEnvironmentV1{
		ID:   environmentID,
		Vars: map[string]any{},
		PackageOverrides: map[string]map[string]deploy.PackageOverrideChoiceV1{
			"python": {distribution: {Path: sourceDir}},
		},
	}}
}
