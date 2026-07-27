package deploy

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func acceptBuildLockBundle(providers.ResolvedBundleIdentityV1) error { return nil }

func buildReachabilityFixture(t *testing.T) (string, providerstore.Store, BuildLockV1, providerstore.StoreObjectRef, providerstore.StoreObjectRef) {
	t.Helper()
	dir := t.TempDir()
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	keepArtifact, err := store.Publish(context.Background(), "packages/keep.deb", "deb", strings.NewReader("keep"))
	if err != nil {
		t.Fatal(err)
	}
	dropArtifact, err := store.Publish(context.Background(), "packages/drop.deb", "deb", strings.NewReader("drop"))
	if err != nil {
		t.Fatal(err)
	}
	keepReference, _ := keepArtifact.StoreObjectRef()
	dropReference, _ := dropArtifact.StoreObjectRef()

	lock := validBuildLock(t)
	addValidAPTNode(t, &lock)
	profileDigest, err := providers.RequirementProfileDigest(lock.Nodes[0].RequirementProfile, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := providers.NewResolvedBundle(providers.ResolvedBundleIdentityV1{
		Schema: providers.ResolvedBundleSchemaV1, NodeID: "apt", Provider: lock.Nodes[0].Provider,
		Request:                  providers.CanonicalProviderRequest{Schema: "apt-request-v1", Provider: lock.Nodes[0].Provider, Value: canonical.Object{}},
		RequirementProfileDigest: profileDigest, RecipeVersion: "apt-resolver-v1", Platform: lock.Platform, Upstream: lock.Nodes[0].Upstream,
		SelectedSources: []providers.ResolvedSourceInput{}, Artifacts: []providerstore.ArtifactDescriptor{keepArtifact}, Outputs: []providers.ResolvedOutput{},
		ProviderPayload: canonical.Envelope{Schema: "apt-bundle-v1", Value: canonical.Object{}},
	}, acceptBuildLockBundle)
	if err != nil {
		t.Fatal(err)
	}
	manifestReference, err := providers.PublishResolvedBundleManifest(context.Background(), store, bundle, acceptBuildLockBundle)
	if err != nil {
		t.Fatal(err)
	}
	lock.Nodes[0].BundleManifest = manifestReference
	policyDigest, err := RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	validationReference, err := PublishPrefixValidation(context.Background(), store, PrefixValidationV1{
		Schema: PrefixValidationSchemaV1, SubjectRootFS: lock.FinalImage.RootFSSubject,
		Profiles: []providers.ValidationEvidence{}, RuntimePolicy: policyDigest, ExposedOutputs: []providers.ExecutableEvidence{},
	})
	if err != nil {
		t.Fatal(err)
	}
	lock.ValidationRecord = validationReference
	return dir, store, lock, keepReference, dropReference
}

func TestBuildLockStoreClosureLoadsExactTransitiveObjects(t *testing.T) {
	_, store, lock, keepReference, _ := buildReachabilityFixture(t)
	closure, err := BuildLockStoreClosure(lock, store, acceptBuildLockProfile, acceptBuildLockBundle)
	if err != nil {
		t.Fatal(err)
	}
	want := []providerstore.StoreObjectRef{keepReference, lock.Nodes[0].BundleManifest, lock.ValidationRecord}
	if len(closure) != len(want) {
		t.Fatalf("closure = %#v", closure)
	}
	for index := range want {
		if closure[index] != want[index] {
			t.Fatalf("closure[%d] = %#v, want %#v", index, closure[index], want[index])
		}
	}
}

func TestBuildLockStoreClosureBytesUsesExactObjectSizesWithoutRehashingBlobs(t *testing.T) {
	_, store, lock, keepReference, _ := buildReachabilityFixture(t)
	blobPath, err := store.BlobPath(keepReference.Digest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath, err := store.ManifestPath(lock.Nodes[0].BundleManifest)
	if err != nil {
		t.Fatal(err)
	}
	validationPath, err := store.ValidationRecordPath(lock.ValidationRecord)
	if err != nil {
		t.Fatal(err)
	}
	want := uint64(0)
	for _, path := range []string{blobPath, manifestPath, validationPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		want += uint64(info.Size())
	}
	if err := os.Chmod(blobPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blobPath, []byte("xxxx"), 0o600); err != nil {
		t.Fatal(err)
	}

	references, got, err := InspectBuildLockStoreClosure(lock, store, acceptBuildLockProfile, acceptBuildLockBundle)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("closure bytes = %d, want %d", got, want)
	}
	wantReferences := []providerstore.StoreObjectRef{keepReference, lock.Nodes[0].BundleManifest, lock.ValidationRecord}
	if len(references) != len(wantReferences) {
		t.Fatalf("closure references = %#v", references)
	}
	for index := range wantReferences {
		if references[index] != wantReferences[index] {
			t.Fatalf("closure reference %d = %#v, want %#v", index, references[index], wantReferences[index])
		}
	}
}

func TestBuildLockStoreClosureBytesRejectsWrongBlobSize(t *testing.T) {
	_, store, lock, keepReference, _ := buildReachabilityFixture(t)
	blobPath, err := store.BlobPath(keepReference.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blobPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blobPath, []byte("wrong-size"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = BuildLockStoreClosureBytes(lock, store, acceptBuildLockProfile, acceptBuildLockBundle)
	if err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("wrong blob size error = %v", err)
	}
}

func TestOperationLockCleansOnlyObjectsOutsideCurrentBuildClosure(t *testing.T) {
	dir, store, lock, keepReference, dropReference := buildReachabilityFixture(t)
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	if err := operation.RemoveUnreachableBuildObjects(store, lock, acceptBuildLockProfile, acceptBuildLockBundle); err != nil {
		t.Fatal(err)
	}
	keepPath, _ := store.BlobPath(keepReference.Digest)
	dropPath, _ := store.BlobPath(dropReference.Digest)
	if _, err := os.Lstat(keepPath); err != nil {
		t.Fatalf("reachable blob removed: %v", err)
	}
	if _, err := os.Lstat(dropPath); !os.IsNotExist(err) {
		t.Fatalf("unreachable blob remains: %v", err)
	}
}

func TestOperationLockRejectsCorruptReachableBlobBeforeCleanup(t *testing.T) {
	dir, store, lock, keepReference, dropReference := buildReachabilityFixture(t)
	keepPath, _ := store.BlobPath(keepReference.Digest)
	if err := os.Chmod(keepPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keepPath, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	if err := operation.RemoveUnreachableBuildObjects(store, lock, acceptBuildLockProfile, acceptBuildLockBundle); err == nil || !strings.Contains(err.Error(), "verify") {
		t.Fatalf("corrupt reachable error = %v", err)
	}
	dropPath, _ := store.BlobPath(dropReference.Digest)
	if _, err := os.Lstat(dropPath); err != nil {
		t.Fatalf("cleanup changed store before validating closure: %v", err)
	}
}

func TestOperationLockRejectsStoreFromAnotherDeployment(t *testing.T) {
	dir, _, lock, _, _ := buildReachabilityFixture(t)
	otherStore, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	if err := operation.RemoveUnreachableBuildObjects(otherStore, lock, acceptBuildLockProfile, acceptBuildLockBundle); err == nil || !strings.Contains(err.Error(), "locked deployment") {
		t.Fatalf("foreign store error = %v", err)
	}
}

func TestOperationLockRemovesOnlyOwnedProviderStoreAndRetainsLock(t *testing.T) {
	dir, store, _, _, _ := buildReachabilityFixture(t)
	foreignStore, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	if removed, err := operation.RemoveProviderStore(foreignStore); err == nil || removed {
		t.Fatalf("foreign store removal = %v, %v", removed, err)
	}
	if _, err := os.Stat(store.Root()); err != nil {
		t.Fatalf("foreign removal changed owned store: %v", err)
	}
	removed, err := operation.RemoveProviderStore(store)
	if err != nil || !removed {
		t.Fatalf("owned store removal = %v, %v", removed, err)
	}
	if err := operation.RequireHeld(); err != nil {
		t.Fatalf("provider store removal released operation lock: %v", err)
	}
}
