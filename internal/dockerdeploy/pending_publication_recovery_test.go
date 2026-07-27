package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func acceptRecoveryProfileOwner(providers.RequirementProfile) error { return nil }

func acceptRecoveryBundleOwner(providers.ResolvedBundleIdentityV1) error { return nil }

func TestPreparePendingPublicationRecoveryDiscardsInterruptedFirstBuildWithoutCandidateLock(t *testing.T) {
	dir, pending := pendingReferenceFixture(t)
	pending.Old = nil
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	loads := 0
	plan, err := PreparePendingPublicationRecovery(nil, pending, store, "demo", dir, acceptRecoveryProfileOwner, acceptRecoveryBundleOwner, func(canonical.Digest) (deploy.BuildLockV1, error) {
		loads++
		return deploy.BuildLockV1{}, errors.New("candidate lock must not be loaded")
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Decision != deploy.PendingRecoveryDiscardCandidate || plan.SelectedLock != nil || loads != 0 {
		t.Fatalf("decision = %q, selected lock = %#v, loads = %d", plan.Decision, plan.SelectedLock, loads)
	}
}

func TestRecoverPendingPublicationCleansTemporaryEntriesWhenNothingIsPending(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := store.NewWorkspace("abandoned-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "partial"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := RecoverPendingPublication(t.Context(), operation, store, nil, "demo", dir, acceptRecoveryProfileOwner, acceptRecoveryBundleOwner)
	if err != nil {
		t.Fatal(err)
	}
	if recovered {
		t.Fatal("recovery reported a pending publication")
	}
	requireNoTemporaryEntries(t, store)
}

func TestExecutePendingPublicationRecoveryRemovesPendingLast(t *testing.T) {
	dir, pending := pendingReferenceFixture(t)
	pending.Old = nil
	pending.Phase = deploy.PendingBuildPhaseValidated
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	if err := operation.WritePendingBuild(pending); err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := store.NewWorkspace("abandoned-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "partial"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := PendingPublicationRecovery{Pending: pending, Decision: deploy.PendingRecoveryDiscardCandidate}
	recovered := false
	recoverReferences := func(context.Context, deploy.PendingBuildV1, deploy.PendingRecoveryDecision, *providers.RealizedImageV1, string, string) error {
		recovered = true
		_, found, err := operation.ReadPendingBuild()
		if err != nil || !found {
			t.Fatalf("pending was removed before reference recovery: found = %v, error = %v", found, err)
		}
		return nil
	}
	if err := executePendingPublicationRecovery(t.Context(), operation, store, plan, "demo", dir, acceptRecoveryProfileOwner, acceptRecoveryBundleOwner, recoverReferences); err != nil {
		t.Fatal(err)
	}
	if !recovered {
		t.Fatal("image references were not recovered")
	}
	if _, found, err := operation.ReadPendingBuild(); err != nil || found {
		t.Fatalf("pending found after successful recovery = %v, error = %v", found, err)
	}
	requireNoTemporaryEntries(t, store)
}

func requireNoTemporaryEntries(t *testing.T, store providerstore.Store) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(store.Root(), "tmp"))
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary entries remain: %#v", entries)
	}
}

func TestExecutePendingPublicationRecoveryKeepsPendingOnReferenceFailure(t *testing.T) {
	dir, pending := pendingReferenceFixture(t)
	pending.Old = nil
	pending.Phase = deploy.PendingBuildPhaseValidated
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	if err := operation.WritePendingBuild(pending); err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("reference backend failed")
	plan := PendingPublicationRecovery{Pending: pending, Decision: deploy.PendingRecoveryDiscardCandidate}
	err = executePendingPublicationRecovery(t.Context(), operation, store, plan, "demo", dir, acceptRecoveryProfileOwner, acceptRecoveryBundleOwner, func(context.Context, deploy.PendingBuildV1, deploy.PendingRecoveryDecision, *providers.RealizedImageV1, string, string) error {
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if _, found, err := operation.ReadPendingBuild(); err != nil || !found {
		t.Fatalf("pending preserved after failure = %v, error = %v", found, err)
	}
}
