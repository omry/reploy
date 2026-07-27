package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestForceReplaceStagedDesiredStateStopsBuiltWorkloadAndRemovesGeneration(t *testing.T) {
	dir, operation, store, lock, state := currentBuildFixture(t, true)
	oldDocument, _ := testSelectedPlatformDocumentV1(t)
	oldDocument.Environment.ID = "demo"
	state.Blueprint = testResolvedBlueprintV1(t, oldDocument)
	state.BlueprintSource = "blueprint: old\n"
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	if err := operation.CommitStateV1(state.Current, state); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}

	replacement, _ := testSelectedPlatformDocumentV1(t)
	replacement.Environment.ID = "replacement"
	effects := []string{}
	result, err := forceReplaceStagedDesiredStateV1(t.Context(), ForceReplaceStagedDesiredStateInputV1{
		DesiredState: DesiredStateStageInputV1{
			DeploymentDir: dir, Document: replacement, BlueprintSource: "blueprint: replacement\n",
		},
	}, forceReplaceStagedDesiredStateBackendV1{
		acquire: deploy.AcquireOperationLock,
		newStore: func(got string) (providerstore.Store, error) {
			if got != dir {
				t.Fatalf("store dir = %q, want %q", got, dir)
			}
			return store, nil
		},
		recoverPending: func(context.Context, *deploy.OperationLock, providerstore.Store, *deploy.EnvironmentGenerationState, string, string) (bool, error) {
			return false, nil
		},
		admit: func(_ context.Context, gotDir string, operation *deploy.OperationLock, input ControlAdmissionInputV1) (AdmittedControlV1, error) {
			if gotDir != dir || input.Operation != deploy.ControlOperationStageV1 || input.Mode != ControlAdmissionForceV1 || input.GenerationReference != state.Current.Reference {
				t.Fatalf("force admission = %q/%#v", gotDir, input)
			}
			return AdmittedControlV1{Operation: operation, Marker: deploy.ControlMarkerV1{ID: "control-0000000000000001"}}, nil
		},
		complete: func(operation *deploy.OperationLock, marker string, _ *deploy.ControlLeaseV1) error {
			if marker != "control-0000000000000001" {
				t.Fatalf("marker = %q", marker)
			}
			return operation.Unlock()
		},
		stopOwned: func(_ context.Context, operation *deploy.OperationLock, got deploy.StateV1, gotDir string, _ RunOptions) error {
			if err := operation.RequireHeld(); err != nil {
				t.Fatal(err)
			}
			if gotDir != dir || !reflect.DeepEqual(got.Current, state.Current) {
				t.Fatalf("stop input = %q/%#v", gotDir, got)
			}
			effects = append(effects, "stop")
			return nil
		},
		removeReference: func(_ context.Context, image providers.RealizedImageV1, reference string, environment string, gotDir string) error {
			if image != lock.FinalImage || reference != state.Current.Reference || environment != "demo" || gotDir != dir {
				t.Fatalf("remove reference = %#v/%q/%q/%q", image, reference, environment, gotDir)
			}
			effects = append(effects, "remove-reference")
			return nil
		},
		commit: func(operation *deploy.OperationLock, expected *deploy.EnvironmentGenerationState, candidate deploy.StateV1) error {
			effects = append(effects, "commit-state")
			return operation.CommitStateV1(expected, candidate)
		},
		stageSame: func(context.Context, DesiredStateStageInputV1) (deploy.DesiredStateUpdateResult, error) {
			t.Fatal("same-environment staging was called")
			return deploy.DesiredStateUpdateResult{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(effects, []string{"stop", "commit-state", "remove-reference"}) {
		t.Fatalf("effects = %#v", effects)
	}
	if !result.Changed || result.State.Current != nil || result.State.Staging == nil || !reflect.DeepEqual(result.State.Overlay, deploy.EmptyRequestOverlayV1()) {
		t.Fatalf("result = %#v", result)
	}
	document, err := blueprint.DecodeResolvedDocumentV1(result.State.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	if document.Environment.ID != "replacement" {
		t.Fatalf("environment = %q", document.Environment.ID)
	}
}

func TestForceReplaceStagedDesiredStateKeepsOldReferenceWhenStateCommitFails(t *testing.T) {
	dir, operation, store, _, state := currentBuildFixture(t, true)
	oldDocument, _ := testSelectedPlatformDocumentV1(t)
	oldDocument.Environment.ID = "demo"
	state.Blueprint = testResolvedBlueprintV1(t, oldDocument)
	state.BlueprintSource = "blueprint: old\n"
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	if err := operation.CommitStateV1(state.Current, state); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}

	replacement, _ := testSelectedPlatformDocumentV1(t)
	replacement.Environment.ID = "replacement"
	wantCommit := errors.New("state write failed")
	removed := false
	_, err := forceReplaceStagedDesiredStateV1(t.Context(), ForceReplaceStagedDesiredStateInputV1{
		DesiredState: DesiredStateStageInputV1{DeploymentDir: dir, Document: replacement, BlueprintSource: "blueprint: replacement\n"},
	}, forceReplaceStagedDesiredStateBackendV1{
		acquire:  deploy.AcquireOperationLock,
		newStore: func(string) (providerstore.Store, error) { return store, nil },
		recoverPending: func(context.Context, *deploy.OperationLock, providerstore.Store, *deploy.EnvironmentGenerationState, string, string) (bool, error) {
			return false, nil
		},
		admit: func(_ context.Context, _ string, operation *deploy.OperationLock, _ ControlAdmissionInputV1) (AdmittedControlV1, error) {
			return AdmittedControlV1{Operation: operation, Marker: deploy.ControlMarkerV1{ID: "control-0000000000000001"}}, nil
		},
		complete: func(operation *deploy.OperationLock, _ string, _ *deploy.ControlLeaseV1) error {
			return operation.Unlock()
		},
		stopOwned: func(context.Context, *deploy.OperationLock, deploy.StateV1, string, RunOptions) error { return nil },
		removeReference: func(context.Context, providers.RealizedImageV1, string, string, string) error {
			removed = true
			return nil
		},
		commit: func(*deploy.OperationLock, *deploy.EnvironmentGenerationState, deploy.StateV1) error {
			return wantCommit
		},
		stageSame: func(context.Context, DesiredStateStageInputV1) (deploy.DesiredStateUpdateResult, error) {
			return deploy.DesiredStateUpdateResult{}, nil
		},
	})
	if !errors.Is(err, wantCommit) || removed {
		t.Fatalf("commit error = %v, removed reference = %t", err, removed)
	}
	locked, lockErr := deploy.AcquireOperationLock(t.Context(), dir)
	if lockErr != nil {
		t.Fatal(lockErr)
	}
	defer locked.Unlock()
	retained, found, readErr := locked.ReadStateV1()
	if readErr != nil || !found || !reflect.DeepEqual(retained.Current, state.Current) {
		t.Fatalf("retained state = %#v, found=%t, error=%v", retained, found, readErr)
	}
}
