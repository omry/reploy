package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

func TestPublishInstalledBuildTransfersAndCommitsSelectedBuild(t *testing.T) {
	sourceDir, sourceOperation, sourceStore, source := installedBuildPublicationSourceFixture(t)
	destinationDir := t.TempDir()
	destinationOperation, err := deploy.AcquireOperationLock(t.Context(), destinationDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = destinationOperation.Unlock() })
	destinationStore, err := providerstore.NewStore(destinationDir)
	if err != nil {
		t.Fatal(err)
	}
	references := fixedPublicationReferences(t, destinationDir, 0x71)
	var created []publicationReferenceCall
	var removed []publicationReferenceCall
	backend := installedBuildPublicationBackend{
		transferClosure: transferInstalledBuildClosure,
		createReference: func(_ context.Context, image providers.RealizedImageV1, _ EnvironmentImageReferences, kind EnvironmentReferenceKind, _, _ string) error {
			created = append(created, publicationReferenceCall{image: image, kind: kind})
			return nil
		},
		removeReference: func(_ context.Context, image providers.RealizedImageV1, _ EnvironmentImageReferences, kind EnvironmentReferenceKind, _, _ string) error {
			removed = append(removed, publicationReferenceCall{image: image, kind: kind})
			return nil
		},
	}
	installation := installedBuildPublicationInstallation(destinationDir)

	result, err := publishInstalledBuildV1(t.Context(), sourceOperation, destinationOperation, sourceStore, destinationStore, InstalledBuildPublicationInputV1{
		Environment: "demo", SourceDeploymentDir: sourceDir, DestinationDeploymentDir: destinationDir,
		Source: source, Installation: installation, References: references,
	}, backend)
	if err != nil {
		t.Fatal(err)
	}
	if result.Current == nil || result.Current.Reference != references.Generation || result.Deployment == nil || !reflect.DeepEqual(result.Deployment.Installation, installation) {
		t.Fatalf("installed result = %#v", result)
	}
	if !reflect.DeepEqual(created, []publicationReferenceCall{
		{image: source.Lock.FinalImage, kind: EnvironmentReferenceTemporary},
		{image: source.Lock.FinalImage, kind: EnvironmentReferenceGeneration},
	}) {
		t.Fatalf("created references = %#v", created)
	}
	if !reflect.DeepEqual(removed, []publicationReferenceCall{{image: source.Lock.FinalImage, kind: EnvironmentReferenceTemporary}}) {
		t.Fatalf("removed references = %#v", removed)
	}
	if _, err := deploy.BuildLockStoreClosure(source.Lock, destinationStore, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
		t.Fatalf("destination closure = %v", err)
	}
	if lock, found, err := destinationOperation.ReadBuildLock(result.Current.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil || !found || !reflect.DeepEqual(lock, source.Lock) {
		t.Fatalf("destination lock=%#v found=%v error=%v", lock, found, err)
	}
	if _, found, err := destinationOperation.ReadPendingBuild(); err != nil || found {
		t.Fatalf("destination pending found=%v error=%v", found, err)
	}
	if err := sourceOperation.RequireHeld(); err != nil {
		t.Fatalf("source lock released: %v", err)
	}
	if err := destinationOperation.RequireHeld(); err != nil {
		t.Fatalf("destination lock released: %v", err)
	}
}

func TestPublishInstalledBuildFailurePreservesPriorDestinationState(t *testing.T) {
	sourceDir, sourceOperation, sourceStore, source := installedBuildPublicationSourceFixture(t)
	destinationDir := t.TempDir()
	destinationStore, priorLock := publicationLockFixture(t, destinationDir, "7", "8", "9")
	destinationOperation, err := deploy.AcquireOperationLock(t.Context(), destinationDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = destinationOperation.Unlock() })
	priorLockDigest, err := destinationOperation.PublishBuildLock(priorLock, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	priorPolicyDigest, err := deploy.RuntimePolicyDigestV1(priorLock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	priorReferences := fixedPublicationReferences(t, destinationDir, 0x73)
	priorGeneration := deploy.EnvironmentGenerationState{
		Reference: priorReferences.Generation, ImageDigest: priorLock.FinalImage.Digest,
		RootFSSubject: priorLock.FinalImage.RootFSSubject, BuildLockDigest: priorLockDigest,
		Platform: priorLock.Platform, RuntimePolicyDigest: priorPolicyDigest,
	}
	document, platform := testSelectedPlatformDocumentV1(t)
	priorState := deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, document),
		Platform: platform, Overlay: priorLock.Overlay, Current: &priorGeneration,
		Deployment: &deploy.DeploymentStateV1{
			Schema: deploy.DeploymentStateSchemaV1, Installation: installedBuildPublicationInstallation(destinationDir),
		},
	}
	if err := destinationOperation.CommitStateV1(nil, priorState); err != nil {
		t.Fatal(err)
	}
	references := fixedPublicationReferences(t, destinationDir, 0x72)
	want := errors.New("generation reference failed")
	backend := installedBuildPublicationBackend{
		transferClosure: transferInstalledBuildClosure,
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

	_, err = publishInstalledBuildV1(t.Context(), sourceOperation, destinationOperation, sourceStore, destinationStore, InstalledBuildPublicationInputV1{
		Environment: "demo", SourceDeploymentDir: sourceDir, DestinationDeploymentDir: destinationDir,
		Source: source, Installation: installedBuildPublicationInstallation(destinationDir), References: references,
	}, backend)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	state, found, err := destinationOperation.ReadStateV1()
	if err != nil || !found || !reflect.DeepEqual(state, priorState) {
		t.Fatalf("destination state=%#v found=%v error=%v, want %#v", state, found, err, priorState)
	}
	pending, found, err := destinationOperation.ReadPendingBuild()
	if err != nil || !found || pending.Phase != deploy.PendingBuildPhaseValidated || !reflect.DeepEqual(pending.Old, &priorGeneration) {
		t.Fatalf("destination pending=%#v found=%v error=%v", pending, found, err)
	}
}

func installedBuildPublicationSourceFixture(t *testing.T) (string, *deploy.OperationLock, providerstore.Store, CurrentBuild) {
	t.Helper()
	dir := t.TempDir()
	operation, store, current := installedBuildPublicationSourceFixtureAtDir(t, dir)
	return dir, operation, store, current
}

func installedBuildPublicationSourceFixtureAtDir(t *testing.T, dir string) (*deploy.OperationLock, providerstore.Store, CurrentBuild) {
	t.Helper()
	store, lock := publicationLockFixture(t, dir, "4", "5", "6")
	document, platform := testSelectedPlatformDocumentV1(t)
	document.Environment.ID = "demo"
	lock.BlueprintDigest = testResolvedBlueprintDigestV1(t, document)
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	lockDigest, err := operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	references := fixedPublicationReferences(t, dir, 0x70)
	generation := deploy.EnvironmentGenerationState{
		Reference: references.Generation, ImageDigest: lock.FinalImage.Digest,
		RootFSSubject: lock.FinalImage.RootFSSubject, BuildLockDigest: lockDigest,
		Platform: lock.Platform, RuntimePolicyDigest: policyDigest,
	}
	state := deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, document),
		Platform: platform, Overlay: lock.Overlay, Current: &generation,
	}
	if err := operation.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	return operation, store, CurrentBuild{State: state, Generation: generation, Lock: lock}
}

func installedBuildPublicationInstallation(destinationDir string) deploy.InstallationStateV1 {
	return deploy.InstallationStateV1{
		Schema: deploy.InstallationSchemaV1, Status: deploy.InstallationStatusReady,
		TargetDir: destinationDir, Scope: "system", Service: "demo",
		UnitPath: "/etc/systemd/system/demo.service", InstanceID: "demo-1", ComposeProject: "demo",
		ContainerName: "demo", NetworkName: "demo", Ports: []deploy.InstallationPortBindingV1{},
	}
}
