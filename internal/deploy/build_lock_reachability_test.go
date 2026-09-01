package deploy

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
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
	lock.Nodes[0].BundleManifest = publishReachabilityBundle(t, store, lock, []providerstore.ArtifactDescriptor{keepArtifact})
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

func publishReachabilityBundle(
	t *testing.T,
	store providerstore.Store,
	lock BuildLockV1,
	artifacts []providerstore.ArtifactDescriptor,
) providerstore.StoreObjectRef {
	t.Helper()
	profileDigest, err := providers.RequirementProfileDigest(lock.Nodes[0].RequirementProfile, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := providers.NewResolvedBundle(providers.ResolvedBundleIdentityV1{
		Schema: providers.ResolvedBundleSchemaV1, NodeID: "apt", Provider: lock.Nodes[0].Provider,
		Request:                  providers.CanonicalProviderRequest{Schema: "apt-request-v1", Provider: lock.Nodes[0].Provider, Value: canonical.Object{}},
		RequirementProfileDigest: profileDigest, RecipeVersion: "apt-resolver-v1", Platform: lock.Platform, Upstream: lock.Nodes[0].Upstream,
		SelectedSources: []providers.ResolvedSourceInput{}, Artifacts: artifacts, Outputs: []providers.ResolvedOutput{},
		ProviderPayload: canonical.Envelope{Schema: "apt-bundle-v1", Value: canonical.Object{}},
	}, acceptBuildLockBundle)
	if err != nil {
		t.Fatal(err)
	}
	manifestReference, err := providers.PublishResolvedBundleManifest(context.Background(), store, bundle, acceptBuildLockBundle)
	if err != nil {
		t.Fatal(err)
	}
	return manifestReference
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

func TestBuildLockStoreClosureIncludesVerifiedPortableToolArtifacts(t *testing.T) {
	_, store, lock, keepReference, _ := buildReachabilityFixture(t)
	portableArtifact, err := store.Publish(context.Background(), "portable/demo.tar", "jdk-archive", strings.NewReader("portable"))
	if err != nil {
		t.Fatal(err)
	}
	lock.PortableTools = portableToolReachabilityLockV1(t, &lock, portableArtifact)
	closure, err := BuildLockStoreClosure(lock, store, acceptBuildLockProfile, acceptBuildLockBundle)
	if err != nil {
		t.Fatal(err)
	}
	portableReference, _ := portableArtifact.StoreObjectRef()
	want := map[providerstore.StoreObjectRef]bool{
		keepReference: true, portableReference: true,
		lock.Nodes[0].BundleManifest: true, lock.ValidationRecord: true,
	}
	if len(closure) != len(want) {
		t.Fatalf("closure = %#v", closure)
	}
	for _, reference := range closure {
		if !want[reference] {
			t.Fatalf("unexpected closure reference %#v", reference)
		}
	}
}

func portableToolReachabilityLockV1(t *testing.T, build *BuildLockV1, artifact providerstore.ArtifactDescriptor) *providers.PortableToolLockV1 {
	t.Helper()
	recordValue := canonical.Object{
		"schema": "portable-tool-payload-v1", "id": "tool:demo/releases/1.0.0/payloads/demo-linux-amd64",
		"name": "demo", "revision": "1", "upstream_version": "1.0.0", "platform": "linux/amd64",
		"logical_path": artifact.LogicalPath, "kind": artifact.Kind, "size": artifact.Size, "sha256": string(artifact.SHA256),
		"resolver": "https-sha256", "entries": "1", "unpacked_size": artifact.Size,
		"install_directory": "demo", "archive_root": "demo-root", "executables": []any{"demo-root/bin/demo"},
	}
	recordDigest, err := canonical.Sum("portable-tool-record", "portable-tool-record-v1", recordValue)
	if err != nil {
		t.Fatal(err)
	}
	record := providers.PortableToolSelectedRecordV1{
		Reference: providers.PortableToolRecordReferenceV1{ID: recordValue["id"].(string), Digest: recordDigest},
		Record:    providers.CanonicalProviderData{Schema: "portable-tool-payload-v1", Value: recordValue},
	}
	plan := providers.PortableToolPlanV1{
		Schema: providers.PortableToolPlanSchemaV1,
		Tools: []providers.PortableToolPlanEntryV1{{
			Scope: "application", SelectedClosureDigest: buildLockTestDigest("8"),
			Provenance: providers.PortableToolReleaseProvenanceV1{
				Tool: "demo", Version: "1.0.0", Revision: "1", ManifestDigest: buildLockTestDigest("9"),
			},
			Responsibilities: providers.PortableToolResponsibilitiesV1{
				BindingContracts: []providers.PortableToolSelectedRecordV1{}, BindingArtifacts: []providers.PortableToolSelectedRecordV1{},
				Payloads: []providers.PortableToolSelectedRecordV1{record}, NativePackageSets: []providers.PortableToolSelectedRecordV1{},
			},
			Exports: []providers.PortableToolExportV1{}, ValidationProfiles: []providers.PortableToolValidationProfileV1{},
		}},
	}
	baseRequest, err := providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{
		Image: build.Base.AuthorReference, Exports: map[string]blueprint.BaseExecutableExport{},
	})
	if err != nil {
		t.Fatal(err)
	}
	baseNode, err := providers.BaseNodeSpec(baseRequest)
	if err != nil {
		t.Fatal(err)
	}
	build.BasePlanDigest, err = providers.ProviderNodePlanDigest(baseNode)
	if err != nil {
		t.Fatal(err)
	}
	aptNode := providers.NodeSpec{
		ID: "apt", Provider: blueprint.ComponentTypeAPT, Components: []string{"system"},
		Request:            providers.CanonicalProviderRequest{Schema: "apt-provider-request-v1", Provider: blueprint.ComponentTypeAPT, Value: canonical.Object{}},
		OutputDeclarations: []providers.OutputDeclaration{},
		Requirements: providers.RequirementDeclaration{
			Executables: []providers.ExecutableRequirement{}, Files: []providers.FileRequirement{},
			ProviderData: providers.CanonicalProviderData{Schema: "apt-requirements-v1", Value: canonical.Object{}},
		},
	}
	aptPlanDigest, err := providers.ProviderNodePlanDigest(aptNode)
	if err != nil {
		t.Fatal(err)
	}
	build.Nodes[0].PlanDigest = aptPlanDigest
	sourceValue := canonical.Object{
		"schema": providers.PortableToolArtifactSourceRecordSchemaV1,
		"id":     "tool:demo/releases/1.0.0/revisions/1/sources/demo", "sha256": string(artifact.SHA256),
		"mirrors": []any{"https://mirror.example/demo.tar"}, "provenance": []any{"https://upstream.example/demo.tar"}, "diagnostics": []any{},
	}
	sourceDigest, err := canonical.Sum("portable-tool-record", "portable-tool-record-v1", sourceValue)
	if err != nil {
		t.Fatal(err)
	}
	source := providers.PortableToolSelectedRecordV1{
		Reference: providers.PortableToolRecordReferenceV1{ID: sourceValue["id"].(string), Digest: sourceDigest},
		Record:    providers.CanonicalProviderData{Schema: providers.PortableToolArtifactSourceRecordSchemaV1, Value: sourceValue},
	}
	manifestValue := canonical.Object{
		"schema": providers.PortableToolReleaseManifestRecordSchemaV1,
		"id":     "tool:demo/releases/1.0.0/revisions/1/manifest",
		"tool":   "demo", "version": "1.0.0", "revision": "1",
		"aliases": []any{}, "provenance": []any{},
		"targets":             []any{canonical.Object{"id": "tool:demo/releases/1.0.0/targets/debian/12/amd64", "digest": string(buildLockTestDigest("8"))}},
		"validation_profiles": []any{canonical.Object{"id": "tool:demo/releases/1.0.0/validation/profiles/default", "digest": string(buildLockTestDigest("9"))}},
		"contract":            canonical.Object{"id": "tool:demo/releases/1.0.0/contract", "digest": string(buildLockTestDigest("7"))},
		"artifact_sources": []any{canonical.Object{
			"artifact_sha256": string(artifact.SHA256),
			"artifact":        canonical.Object{"id": record.Reference.ID, "digest": string(record.Reference.Digest)},
			"source":          canonical.Object{"id": source.Reference.ID, "digest": string(source.Reference.Digest)},
		}},
	}
	manifestDigest, err := canonical.Sum("portable-tool-record", "portable-tool-record-v1", manifestValue)
	if err != nil {
		t.Fatal(err)
	}
	manifest := providers.PortableToolSelectedRecordV1{
		Reference: providers.PortableToolRecordReferenceV1{ID: manifestValue["id"].(string), Digest: manifestDigest},
		Record:    providers.CanonicalProviderData{Schema: providers.PortableToolReleaseManifestRecordSchemaV1, Value: manifestValue},
	}
	plan.Tools[0].Provenance.ManifestDigest = manifest.Reference.Digest
	domain := providers.PortableToolDomainAuthorityV1{ID: "application", Owner: "base"}
	dag, err := providers.BuildPortableToolProviderDAGV1(
		providers.ProviderPlanV1{Schema: providers.ProviderPlanSchemaV1, Nodes: []providers.NodeSpec{aptNode, baseNode}, Edges: []providers.ProviderEdgeV1{}},
		plan,
		[]providers.PortableToolProviderDomainSetV1{{
			Scope: "application", PackageManager: domain, Binding: domain, Filesystem: domain,
			Environment: domain, Exports: domain, Capabilities: domain,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	portableLock, err := providers.BuildPortableToolLockV1(dag, []providers.PortableToolReleaseManifestInputV1{{
		Scope: "application", Tool: "demo", Manifest: manifest,
	}}, []providers.PortableToolArtifactAcquisitionInputV1{{
		Scope: "application", Tool: "demo", Artifact: record.Reference, Descriptor: artifact, Source: source,
		Provenance: providerstore.AcquisitionProvenance{
			Outcome: providerstore.AcquisitionOutcomeCacheHit, SourceID: source.Reference.ID,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return &portableLock
}

func TestReusableBuildLockStoreClosureTrustsExactDebVerificationStamp(t *testing.T) {
	_, store, lock, keepReference, _ := buildReachabilityFixture(t)
	blobPath, err := store.BlobPath(keepReference.Digest)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.Lstat(blobPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blobPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blobPath, []byte("xxxx"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(blobPath, original.ModTime(), original.ModTime()); err != nil {
		t.Fatal(err)
	}

	if _, err := ReusableBuildLockStoreClosure(lock, store, acceptBuildLockProfile, acceptBuildLockBundle); err != nil {
		t.Fatalf("optimistic reusable closure rejected unchanged metadata: %v", err)
	}
	if _, err := BuildLockStoreClosure(lock, store, acceptBuildLockProfile, acceptBuildLockBundle); err == nil {
		t.Fatal("full closure verification accepted changed bytes")
	}
}

func TestReusableBuildLockStoreClosureHashesDebWithChangedModificationTime(t *testing.T) {
	_, store, lock, keepReference, _ := buildReachabilityFixture(t)
	blobPath, err := store.BlobPath(keepReference.Digest)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.Lstat(blobPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blobPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blobPath, []byte("xxxx"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifactTime := original.ModTime().Add(time.Second)
	if err := os.Chtimes(blobPath, artifactTime, artifactTime); err != nil {
		t.Fatal(err)
	}

	if _, err := ReusableBuildLockStoreClosure(lock, store, acceptBuildLockProfile, acceptBuildLockBundle); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("changed deb error = %v", err)
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

func TestOperationLockRetainsUnionOfSelectedBuildClosures(t *testing.T) {
	dir, store, first, firstReference, droppedReference := buildReachabilityFixture(t)
	secondArtifact, err := store.Publish(context.Background(), "packages/second.deb", "deb", strings.NewReader("second"))
	if err != nil {
		t.Fatal(err)
	}
	secondReference, err := secondArtifact.StoreObjectRef()
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.Nodes = append([]NodeLockV1(nil), first.Nodes...)
	second.Nodes[0].BundleManifest = publishReachabilityBundle(
		t, store, second, []providerstore.ArtifactDescriptor{secondArtifact},
	)
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	if err := operation.RemoveUnreachableBuildObjectsForBuilds(
		store,
		[]BuildLockV1{first, second},
		acceptBuildLockProfile,
		acceptBuildLockBundle,
	); err != nil {
		t.Fatal(err)
	}
	for _, reference := range []providerstore.StoreObjectRef{firstReference, secondReference} {
		path, _ := store.BlobPath(reference.Digest)
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("reachable blob %s removed: %v", reference.Digest, err)
		}
	}
	droppedPath, _ := store.BlobPath(droppedReference.Digest)
	if _, err := os.Lstat(droppedPath); !os.IsNotExist(err) {
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
