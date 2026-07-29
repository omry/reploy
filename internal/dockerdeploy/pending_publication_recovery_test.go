package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
	stubNoAbandonedBuildReferences(t)
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

func TestRecoverPendingPublicationRemovesContainersThenBuildReferencesBeforeScratch(t *testing.T) {
	stubNoAbandonedBuildReferences(t)
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
	probeWorkspace, err := store.NewWorkspace("probe-*")
	if err != nil {
		t.Fatal(err)
	}
	aptWorkspace, err := store.NewWorkspace("apt-resolve-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewWorkspace("python-resolve-*"); err != nil {
		t.Fatal(err)
	}
	buildWorkspace, err := store.NewWorkspace("build-*")
	if err != nil {
		t.Fatal(err)
	}

	previousRun := runAbandonedProviderContainerCleanupCommand
	t.Cleanup(func() { runAbandonedProviderContainerCleanupCommand = previousRun })
	got := []string{}
	order := []string{}
	runAbandonedProviderContainerCleanupCommand = func(spec CommandSpec, _ RunOptions) error {
		if spec.Name != "docker" || len(spec.Args) != 3 || spec.Args[0] != "rm" || spec.Args[1] != "--force" {
			t.Fatalf("command = %#v", spec)
		}
		if _, err := os.Stat(probeWorkspace); err != nil {
			t.Fatalf("probe workspace was removed before helper containers: %v", err)
		}
		got = append(got, spec.Args[2])
		order = append(order, "container")
		return nil
	}
	runAbandonedBuildReferenceCleanupDocker = func(
		_ context.Context,
		args ...string,
	) (string, error) {
		if args[1] != "ls" {
			t.Fatalf("unexpected build-reference command: %v", args)
		}
		if _, err := os.Stat(probeWorkspace); err != nil {
			t.Fatalf("probe workspace was removed before build references: %v", err)
		}
		if _, err := os.Stat(buildWorkspace); err != nil {
			t.Fatalf("build workspace was removed before build references: %v", err)
		}
		order = append(order, "reference")
		return "", nil
	}

	if _, err := RecoverPendingPublication(t.Context(), operation, store, nil, "demo", dir, acceptRecoveryProfileOwner, acceptRecoveryBundleOwner); err != nil {
		t.Fatal(err)
	}
	want := []string{
		aptResolverContainerName(aptWorkspace),
		imageProbeContainerName(probeWorkspace),
		pythonResolverContainerName(probeWorkspace),
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("helper containers = %#v, want %#v", got, want)
	}
	wantOrder := []string{
		"container", "container", "container",
		"reference", "reference",
	}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("cleanup order = %v, want %v", order, wantOrder)
	}
	requireNoTemporaryEntries(t, store)
}

func TestRecoverPendingPublicationKeepsBuildWorkspaceWhenReferenceCleanupFails(t *testing.T) {
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
	workspace, err := store.NewWorkspace("build-*")
	if err != nil {
		t.Fatal(err)
	}
	previous := runAbandonedBuildReferenceCleanupDocker
	t.Cleanup(func() { runAbandonedBuildReferenceCleanupDocker = previous })
	cause := errors.New("injected reference removal failure")
	runAbandonedBuildReferenceCleanupDocker = func(
		_ context.Context,
		args ...string,
	) (string, error) {
		if args[1] == "ls" {
			return string(rendererDigest("a")), nil
		}
		return "", cause
	}
	_, err = RecoverPendingPublication(
		t.Context(), operation, store, nil, "demo", dir,
		acceptRecoveryProfileOwner, acceptRecoveryBundleOwner,
	)
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Lstat(workspace); err != nil {
		t.Fatalf("recovery workspace was removed after reference failure: %v", err)
	}
}

func TestRemoveAbandonedProviderContainerAcceptsAlreadyAbsentContainer(t *testing.T) {
	previousRun := runAbandonedProviderContainerCleanupCommand
	t.Cleanup(func() { runAbandonedProviderContainerCleanupCommand = previousRun })
	runAbandonedProviderContainerCleanupCommand = func(_ CommandSpec, options RunOptions) error {
		_, _ = options.Stderr.Write([]byte("Error response from daemon: No such container: reploy-probe-missing\n"))
		return errors.New("docker failed")
	}
	if err := removeAbandonedProviderContainer(t.Context(), "reploy-probe-missing"); err != nil {
		t.Fatal(err)
	}
}

func TestExecutePendingPublicationRecoveryRemovesPendingLast(t *testing.T) {
	stubNoAbandonedBuildReferences(t)
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
