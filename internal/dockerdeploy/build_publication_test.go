package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type publicationReferenceCall struct {
	image providers.RealizedImageV1
	kind  EnvironmentReferenceKind
}

func TestPublishBuildCommitsStateAndRemovesPendingLast(t *testing.T) {
	dir := t.TempDir()
	store, lock := publicationLockFixture(t, dir, "4", "5", "6")
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	installation := &deploy.DeploymentStateV1{
		Schema: deploy.DeploymentStateSchemaV1,
		Installation: deploy.InstallationStateV1{
			Schema: deploy.InstallationSchemaV1, Status: deploy.InstallationStatusReady,
			TargetDir: "/opt/demo", Scope: "system", Service: "demo",
			UnitPath: "/etc/systemd/system/demo.service", InstanceID: "demo-1", ComposeProject: "demo-1",
			ContainerName: "demo", NetworkName: "demo", Ports: []deploy.InstallationPortBindingV1{},
		},
	}
	document, platform := testSelectedPlatformDocumentV1(t)
	if err := operation.CommitStateV1(nil, deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, document),
		Platform: platform, Overlay: deploy.EmptyRequestOverlayV1(), Deployment: installation,
	}); err != nil {
		t.Fatal(err)
	}

	references := fixedPublicationReferences(t, dir, 0x41)
	var created []publicationReferenceCall
	var removed []publicationReferenceCall
	backend := buildPublicationBackend{
		newReferences: func(string, string) (EnvironmentImageReferences, error) { return references, nil },
		createReference: func(_ context.Context, image providers.RealizedImageV1, _ EnvironmentImageReferences, kind EnvironmentReferenceKind, _, _ string) error {
			created = append(created, publicationReferenceCall{image: image, kind: kind})
			return nil
		},
		removeReference: func(_ context.Context, image providers.RealizedImageV1, _ EnvironmentImageReferences, kind EnvironmentReferenceKind, _, _ string) error {
			removed = append(removed, publicationReferenceCall{image: image, kind: kind})
			if _, found, err := operation.ReadPendingBuild(); err != nil || !found {
				t.Fatalf("pending build missing during cleanup: found=%v err=%v", found, err)
			}
			return nil
		},
	}
	result, err := publishBuild(t.Context(), operation, store, publicationInput(t, dir, lock), backend)
	if err != nil {
		t.Fatal(err)
	}
	wantCreated := []publicationReferenceCall{
		{image: lock.FinalImage, kind: EnvironmentReferenceTemporary},
		{image: lock.FinalImage, kind: EnvironmentReferenceGeneration},
	}
	if !reflect.DeepEqual(created, wantCreated) {
		t.Fatalf("created references = %#v, want %#v", created, wantCreated)
	}
	wantRemoved := []publicationReferenceCall{{image: lock.FinalImage, kind: EnvironmentReferenceTemporary}}
	if !reflect.DeepEqual(removed, wantRemoved) {
		t.Fatalf("removed references = %#v, want %#v", removed, wantRemoved)
	}
	stored, found, err := operation.ReadStateV1()
	if err != nil || !found || !reflect.DeepEqual(stored, result) {
		t.Fatalf("stored state = %#v, found=%v err=%v, want %#v", stored, found, err, result)
	}
	if result.Current == nil || result.Current.Reference != references.Generation || result.Current.ImageDigest != lock.FinalImage.Digest {
		t.Fatalf("published current generation = %#v", result.Current)
	}
	if result.Blueprint != testResolvedBlueprintV1(t, document) || result.Platform != platform {
		t.Fatalf("published resolved blueprint = %q", result.Blueprint)
	}
	if !reflect.DeepEqual(result.Deployment, installation) {
		t.Fatalf("deployment-local state changed during build publication: got=%#v want=%#v", result.Deployment, installation)
	}
	if _, found, err := operation.ReadPendingBuild(); err != nil || found {
		t.Fatalf("pending after publication: found=%v err=%v", found, err)
	}
	locked, found, err := operation.ReadBuildLock(result.Current.BuildLockDigest, registry.ValidateRequirementProfileV1)
	if err != nil || !found || !reflect.DeepEqual(locked, lock) {
		t.Fatalf("published lock = %#v, found=%v err=%v", locked, found, err)
	}
}

func TestPublishBuildLeavesRecoverablePendingBeforeCommit(t *testing.T) {
	dir := t.TempDir()
	store, lock := publicationLockFixture(t, dir, "7", "8", "9")
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	want := errors.New("generation tag failed")
	references := fixedPublicationReferences(t, dir, 0x42)
	backend := buildPublicationBackend{
		newReferences: func(string, string) (EnvironmentImageReferences, error) { return references, nil },
		createReference: func(_ context.Context, _ providers.RealizedImageV1, _ EnvironmentImageReferences, kind EnvironmentReferenceKind, _, _ string) error {
			if kind == EnvironmentReferenceGeneration {
				return want
			}
			return nil
		},
		removeReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
			return nil
		},
	}
	_, err = publishBuild(t.Context(), operation, store, publicationInput(t, dir, lock), backend)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	pending, found, err := operation.ReadPendingBuild()
	if err != nil || !found || pending.Phase != deploy.PendingBuildPhaseValidated {
		t.Fatalf("pending = %#v, found=%v err=%v", pending, found, err)
	}
	wantCleanup := []deploy.CleanupItemV1{{Kind: deploy.CleanupKindTemporaryImageReference, Identity: references.Temporary}}
	if !reflect.DeepEqual(pending.Cleanup, wantCleanup) {
		t.Fatalf("pending cleanup inventory = %#v, want %#v", pending.Cleanup, wantCleanup)
	}
	if _, found, err := operation.ReadStateV1(); err != nil || found {
		t.Fatalf("state changed before commit: found=%v err=%v", found, err)
	}
	if _, found, err := operation.ReadBuildLock(pending.Candidate.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil || found {
		t.Fatalf("lock published before generation: found=%v err=%v", found, err)
	}
}

func TestPublishBuildLeavesCommittedStateRecoverableOnCleanupFailure(t *testing.T) {
	dir := t.TempDir()
	store, lock := publicationLockFixture(t, dir, "7", "8", "9")
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	want := errors.New("temporary tag cleanup failed")
	references := fixedPublicationReferences(t, dir, 0x43)
	backend := buildPublicationBackend{
		newReferences: func(string, string) (EnvironmentImageReferences, error) { return references, nil },
		createReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
			return nil
		},
		removeReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
			return want
		},
	}
	_, err = publishBuild(t.Context(), operation, store, publicationInput(t, dir, lock), backend)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	state, found, err := operation.ReadStateV1()
	if err != nil || !found || state.Current == nil {
		t.Fatalf("committed state = %#v, found=%v err=%v", state, found, err)
	}
	pending, found, err := operation.ReadPendingBuild()
	if err != nil || !found || pending.Phase != deploy.PendingBuildPhaseCleanup {
		t.Fatalf("pending = %#v, found=%v err=%v", pending, found, err)
	}
	plan, err := PreparePendingPublicationRecovery(
		state.Current, pending, store, "demo", dir,
		registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1,
		func(digest canonical.Digest) (deploy.BuildLockV1, error) {
			loaded, found, err := operation.ReadBuildLock(digest, registry.ValidateRequirementProfileV1)
			if err != nil {
				return deploy.BuildLockV1{}, err
			}
			if !found {
				return deploy.BuildLockV1{}, errors.New("lock missing")
			}
			return loaded, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Decision != deploy.PendingRecoveryKeepCandidate || plan.SelectedLock == nil || !reflect.DeepEqual(*plan.SelectedLock, lock) {
		t.Fatalf("recovery plan = %#v", plan)
	}
}

func TestPublishBuildReplacementUsesOldLockedConfigForCleanup(t *testing.T) {
	dir := t.TempDir()
	store, first := publicationLockFixture(t, dir, "a", "b", "c")
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	sequence := byte(0x50)
	var removed []publicationReferenceCall
	backend := buildPublicationBackend{
		newReferences: func(string, string) (EnvironmentImageReferences, error) {
			result := fixedPublicationReferences(t, dir, sequence)
			sequence++
			return result, nil
		},
		createReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
			return nil
		},
		removeReference: func(_ context.Context, image providers.RealizedImageV1, _ EnvironmentImageReferences, kind EnvironmentReferenceKind, _, _ string) error {
			removed = append(removed, publicationReferenceCall{image: image, kind: kind})
			return nil
		},
	}
	firstState, err := publishBuild(t.Context(), operation, store, publicationInput(t, dir, first), backend)
	if err != nil {
		t.Fatal(err)
	}
	secondStore, second := publicationLockFixture(t, dir, "d", "e", "f")
	if secondStore.Root() != store.Root() {
		t.Fatal("fixture changed provider store")
	}
	removed = nil
	secondState, err := publishBuild(t.Context(), operation, store, publicationInput(t, dir, second), backend)
	if err != nil {
		t.Fatal(err)
	}
	if firstState.Current == nil || secondState.Current == nil || firstState.Current.Reference == secondState.Current.Reference {
		t.Fatalf("generation references were not replaced: first=%#v second=%#v", firstState.Current, secondState.Current)
	}
	want := []publicationReferenceCall{
		{image: first.FinalImage, kind: EnvironmentReferenceGeneration},
		{image: second.FinalImage, kind: EnvironmentReferenceTemporary},
	}
	if !reflect.DeepEqual(removed, want) {
		t.Fatalf("replacement cleanup = %#v, want %#v", removed, want)
	}
	if first.FinalImage.ConfigDigest == first.FinalImage.Digest {
		t.Fatal("fixture must distinguish image digest from config digest")
	}
	if _, found, err := operation.ReadBuildLock(firstState.Current.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil || found {
		t.Fatalf("old lock remains: found=%v err=%v", found, err)
	}
}

func TestPublishBuildRejectsResolvedBlueprintMismatchBeforeDockerChanges(t *testing.T) {
	dir := t.TempDir()
	store, lock := publicationLockFixture(t, dir, "4", "5", "6")
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	calls := 0
	backend := buildPublicationBackend{
		newReferences: func(string, string) (EnvironmentImageReferences, error) {
			calls++
			return EnvironmentImageReferences{}, nil
		},
		createReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
			calls++
			return nil
		},
		removeReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
			calls++
			return nil
		},
	}
	document := blueprint.Document{Environment: blueprint.Environment{ID: "changed"}}
	_, err = publishBuild(t.Context(), operation, store, BuildPublicationInput{
		Environment: "demo", DeploymentDir: dir, Document: document, Lock: lock,
	}, backend)
	if err == nil || !strings.Contains(err.Error(), "blueprint") || calls != 0 {
		t.Fatalf("error = %v, backend calls = %d", err, calls)
	}
}

func publicationLockFixture(t *testing.T, dir string, imageChar string, configChar string, rootChar string) (providerstore.Store, deploy.BuildLockV1) {
	t.Helper()
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	baseConfig := rendererDigest("1")
	rootFSSubject, err := deploy.RootFSSubject([]canonical.Digest{rendererDigest(rootChar)})
	if err != nil {
		t.Fatal(err)
	}
	image := providers.RealizedImageV1{Digest: rendererDigest(imageChar), ConfigDigest: rendererDigest(configChar), RootFSSubject: rootFSSubject}
	policy := deploy.RuntimePolicyV1{
		Schema: deploy.RuntimePolicySchemaV1, AllowedRoots: []string{"/mnt"},
		ProtectedPaths: []deploy.ProtectedPathV1{}, Plans: []deploy.RuntimePlanV1{},
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(policy)
	if err != nil {
		t.Fatal(err)
	}
	validation, err := deploy.PublishPrefixValidation(t.Context(), store, deploy.PrefixValidationV1{
		Schema: deploy.PrefixValidationSchemaV1, SubjectRootFS: image.RootFSSubject,
		Profiles: []providers.ValidationEvidence{}, RuntimePolicy: policyDigest,
		ExposedOutputs: []providers.ExecutableEvidence{},
	})
	if err != nil {
		t.Fatal(err)
	}
	document, _ := testSelectedPlatformDocumentV1(t)
	lock := deploy.BuildLockV1{
		Schema: deploy.BuildLockSchemaV1, BlueprintDigest: testResolvedBlueprintDigestV1(t, document), Overlay: deploy.EmptyRequestOverlayV1(),
		ResolvedRequestDigest: rendererDigest("3"), Platform: platform,
		Base: deploy.ImageDescriptor{
			Schema: deploy.ImageDescriptorSchemaV1, Platform: platform, AuthorReference: "local-base",
			ImmutableReference: string(baseConfig), ConfigDigest: baseConfig,
			RootFSDiffIDs: []canonical.Digest{rendererDigest(rootChar)},
		},
		Graph: deploy.ProviderGraphLockV1{Nodes: []providers.NodeID{"base"}, Edges: []providers.ProviderEdgeV1{}},
		Nodes: []deploy.NodeLockV1{}, Catalog: []providers.RealizedOutput{},
		RuntimePolicy: policy, ValidationRecord: validation, FinalImage: image,
	}
	if err := deploy.ValidateBuildLockV1(lock, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	return store, lock
}

func publicationInput(t *testing.T, dir string, lock deploy.BuildLockV1) BuildPublicationInput {
	t.Helper()
	document, _ := testSelectedPlatformDocumentV1(t)
	return BuildPublicationInput{Environment: "demo", DeploymentDir: dir, Document: document, Lock: lock}
}

func fixedPublicationReferences(t *testing.T, dir string, value byte) EnvironmentImageReferences {
	t.Helper()
	references, err := newEnvironmentImageReferences("demo", dir, bytes.NewReader(bytes.Repeat([]byte{value}, environmentReferenceRandomBytes*2)))
	if err != nil {
		t.Fatal(err)
	}
	return references
}
