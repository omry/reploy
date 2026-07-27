package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestRemoveStagedDeploymentRemovesOwnedResourcesAndDirectory(t *testing.T) {
	dir, operation, store, lock, state := currentBuildFixture(t, true)
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	state.BlueprintSource = "retained source"
	if err := operation.CommitStateV1(state.Current, state); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}

	stopped := false
	discarded := false
	var removedImage providers.RealizedImageV1
	var removedReference string
	backend := testStagedRemovalBackendV1(store)
	backend.newStore = func(gotDir string) (providerstore.Store, error) {
		if gotDir != dir {
			t.Fatalf("store directory = %q, want %q", gotDir, dir)
		}
		return store, nil
	}
	backend.stopOwned = func(
		_ context.Context,
		_ *deploy.OperationLock,
		gotState deploy.StateV1,
		gotDir string,
		_ RunOptions,
	) error {
		stopped = true
		if gotDir != dir || !reflect.DeepEqual(gotState.Current, state.Current) {
			t.Fatalf("stop input = %q/%#v", gotDir, gotState.Current)
		}
		return nil
	}
	backend.discardValidated = func(
		context.Context,
		*deploy.OperationLock,
		string,
		string,
	) error {
		discarded = true
		return nil
	}
	backend.removeReference = func(
		_ context.Context,
		image providers.RealizedImageV1,
		reference string,
		environment string,
		gotDir string,
	) error {
		if environment != "demo" || gotDir != dir {
			t.Fatalf("remove reference scope = %q/%q", environment, gotDir)
		}
		removedImage = image
		removedReference = reference
		return nil
	}
	result, err := removeStagedDeploymentV1(
		t.Context(),
		StagedDeploymentRemoveInputV1{DeploymentDir: dir},
		backend,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.DeploymentDir != dir || result.Environment != "demo" {
		t.Fatalf("result = %#v", result)
	}
	if !stopped || !discarded {
		t.Fatalf("stopped=%t discarded=%t", stopped, discarded)
	}
	if !reflect.DeepEqual(removedImage, lock.FinalImage) ||
		removedReference != state.Current.Reference {
		t.Fatalf("removed image = %#v, reference = %q", removedImage, removedReference)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("staging directory remains: %v", err)
	}
}

func TestRemoveStagedDeploymentStopsMaterializedRuntimeWithoutCurrentBuild(t *testing.T) {
	dir, operation, store, _, state := currentBuildFixture(t, true)
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	state.BlueprintSource = "retained source"
	previous := state.Current
	state.Current = nil
	if err := operation.CommitStateV1(previous, state); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}

	stopped := false
	backend := testStagedRemovalBackendV1(store)
	backend.stopOwned = func(
		_ context.Context,
		_ *deploy.OperationLock,
		gotState deploy.StateV1,
		gotDir string,
		_ RunOptions,
	) error {
		stopped = true
		if gotDir != dir || gotState.Current != nil {
			t.Fatalf("stop input = %q/%#v", gotDir, gotState.Current)
		}
		return nil
	}
	if _, err := removeStagedDeploymentV1(
		t.Context(),
		StagedDeploymentRemoveInputV1{DeploymentDir: dir},
		backend,
	); err != nil {
		t.Fatal(err)
	}
	if !stopped {
		t.Fatal("materialized staged workload was not stopped")
	}
}

func TestRemoveStagedDeploymentRetainsTombstoneAfterDirectoryCleanupFailure(t *testing.T) {
	dir, operation, store, _, state := currentBuildFixture(t, true)
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	state.BlueprintSource = "retained source"
	if err := operation.CommitStateV1(state.Current, state); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}

	backend := testStagedRemovalBackendV1(store)
	tombstone := dir + ".retained"
	t.Cleanup(func() { _ = os.RemoveAll(tombstone) })
	backend.reserve = func(string) (string, error) { return tombstone, nil }
	backend.removeAll = func(got string) error {
		if got != tombstone {
			t.Fatalf("remove path = %q, want %q", got, tombstone)
		}
		return os.ErrPermission
	}
	_, err := removeStagedDeploymentV1(
		t.Context(),
		StagedDeploymentRemoveInputV1{DeploymentDir: dir},
		backend,
	)
	if err == nil || !strings.Contains(err.Error(), "partial removal retained at "+tombstone) {
		t.Fatalf("cleanup failure = %v", err)
	}
	if _, statErr := os.Lstat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("original staging directory was restored: %v", statErr)
	}
	if info, statErr := os.Stat(tombstone); statErr != nil || !info.IsDir() {
		t.Fatalf("retained tombstone = %v, %v", info, statErr)
	}
}

func TestRemoveStagedDeploymentRestoresDirectoryAfterImageReferenceFailure(t *testing.T) {
	dir, operation, store, _, state := currentBuildFixture(t, true)
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	state.BlueprintSource = "retained source"
	if err := operation.CommitStateV1(state.Current, state); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}

	backend := testStagedRemovalBackendV1(store)
	tombstone := dir + ".rollback"
	backend.reserve = func(string) (string, error) { return tombstone, nil }
	backend.removeReference = func(
		context.Context,
		providers.RealizedImageV1,
		string,
		string,
		string,
	) error {
		return errors.New("injected image-reference failure")
	}
	_, err := removeStagedDeploymentV1(
		t.Context(),
		StagedDeploymentRemoveInputV1{DeploymentDir: dir},
		backend,
	)
	if err == nil || !strings.Contains(err.Error(), "remove staged image reference") {
		t.Fatalf("image-reference failure = %v", err)
	}
	if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
		t.Fatalf("restored staging directory = %v, %v", info, statErr)
	}
	if _, statErr := os.Lstat(tombstone); !os.IsNotExist(statErr) {
		t.Fatalf("rollback tombstone remains: %v", statErr)
	}
}

func TestReadStagedDeploymentForRemovalRejectsNonStagingState(t *testing.T) {
	_, operation, _, _, _ := currentBuildFixture(t, true)
	defer operation.Unlock()
	if _, _, err := readStagedDeploymentForRemovalV1(operation); err == nil {
		t.Fatal("non-staging deployment was accepted as staging")
	}
}

func TestStopStagedWorkloadForRemovalAllowsUnmaterializedRuntime(t *testing.T) {
	dir, operation, _, _, state := currentBuildFixture(t, true)
	defer operation.Unlock()
	if err := stopStagedWorkloadForRemovalV1(
		t.Context(), operation, state, dir, RunOptions{},
	); err != nil {
		t.Fatal(err)
	}
}

func testStagedRemovalBackendV1(store providerstore.Store) stagedDeploymentRemoveBackendV1 {
	return stagedDeploymentRemoveBackendV1{
		acquire:  deploy.AcquireOperationLock,
		newStore: func(string) (providerstore.Store, error) { return store, nil },
		recoverPending: func(
			context.Context,
			*deploy.OperationLock,
			providerstore.Store,
			*deploy.EnvironmentGenerationState,
			string,
			string,
		) (bool, error) {
			return false, nil
		},
		admit:     AdmitControlOperationV1,
		stopOwned: func(context.Context, *deploy.OperationLock, deploy.StateV1, string, RunOptions) error { return nil },
		discardValidated: func(
			context.Context,
			*deploy.OperationLock,
			string,
			string,
		) error {
			return nil
		},
		removeMarker: func(operation *deploy.OperationLock, markerID string) error {
			_, removed, err := operation.RemoveControlMarkerV1(markerID)
			if err == nil && !removed {
				return errors.New("control marker was absent")
			}
			return err
		},
		releaseLease:    func(lease *deploy.ControlLeaseV1) error { return lease.Release() },
		reserve:         reserveStagedDeploymentTombstoneV1,
		rename:          os.Rename,
		unlock:          func(operation *deploy.OperationLock) error { return operation.Unlock() },
		removeReference: func(context.Context, providers.RealizedImageV1, string, string, string) error { return nil },
		removeAll:       os.RemoveAll,
		complete:        CompleteControlAdmissionV1,
	}
}
