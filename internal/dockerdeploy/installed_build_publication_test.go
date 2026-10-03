package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
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
	installLock := source.Lock
	installLock.RuntimeLayer.Result.Digest = rendererDigest("e")
	installLock.RuntimeLayer.Result.ConfigDigest = rendererDigest("e")
	installLock.FinalImage = installLock.RuntimeLayer.Result
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
		Source: source, Build: installLock, Installation: installation, References: references,
	}, backend)
	if err != nil {
		t.Fatal(err)
	}
	if result.Current == nil || result.Current.Reference != references.Generation || result.Deployment == nil || !reflect.DeepEqual(result.Deployment.Installation, installation) {
		t.Fatalf("installed result = %#v", result)
	}
	if !reflect.DeepEqual(created, []publicationReferenceCall{
		{image: installLock.FinalImage, kind: EnvironmentReferenceTemporary},
		{image: installLock.FinalImage, kind: EnvironmentReferenceGeneration},
	}) {
		t.Fatalf("created references = %#v", created)
	}
	if !reflect.DeepEqual(removed, []publicationReferenceCall{{image: installLock.FinalImage, kind: EnvironmentReferenceTemporary}}) {
		t.Fatalf("removed references = %#v", removed)
	}
	if _, err := deploy.BuildLockStoreClosure(installLock, destinationStore, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
		t.Fatalf("destination closure = %v", err)
	}
	if lock, found, err := destinationOperation.ReadBuildLock(result.Current.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil || !found || !reflect.DeepEqual(lock, installLock) {
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
		Source: source, Build: source.Lock, Installation: installedBuildPublicationInstallation(destinationDir), References: references,
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
		UnitPath: filepath.Join(destinationDir, ".reploy", "demo.service"), InstanceID: "demo-1", ComposeProject: "demo",
		ContainerName: "demo", NetworkName: "demo", Ports: []deploy.InstallationPortBindingV1{},
	}
}

// Reuse the current-publication reference model and actual installed transfer.
// Only Docker alias effects are injected; state, locks, stores and recovery are real.
func installedOwnedPublicationBackendV1(t *testing.T, source *deploy.OperationLock, images *pendingPublicationImagesV1, store providerstore.Store, dir string) installedBuildPublicationBackend {
	t.Helper()
	refs := images.backend(store, dir)
	return installedBuildPublicationBackend{
		transferClosure: transferInstalledBuildClosure,
		createReference: func(ctx context.Context, image providers.RealizedImageV1, refsValue EnvironmentImageReferences, kind EnvironmentReferenceKind, environment, path string) error {
			if err := source.RequireHeld(); err != nil {
				t.Fatal(err)
			}
			return refs.createReference(ctx, image, refsValue, kind, environment, path)
		},
		removeReference: func(ctx context.Context, image providers.RealizedImageV1, refsValue EnvironmentImageReferences, kind EnvironmentReferenceKind, environment, path string) error {
			if err := source.RequireHeld(); err != nil {
				t.Fatal(err)
			}
			return refs.removeReference(ctx, image, refsValue, kind, environment, path)
		},
		createCompanion: refs.createCompanion, removeCompanion: refs.removeCompanion,
		removeIntent: refs.removeIntent,
	}
}

type installedOwnedFixtureV1 struct {
	input            InstalledBuildPublicationInputV1
	sourceOperation  *deploy.OperationLock
	sourceStore      providerstore.Store
	sourcePairs      []OwnedImageReferenceV1
	sourceSnapshot   map[string]string
	destinationStore providerstore.Store
	images           *pendingPublicationImagesV1
	prior            deploy.StateV1
	oldPairs         []OwnedImageReferenceV1
	validated        deploy.ValidatedBuildV1
	validatedLock    deploy.BuildLockV1
}

func newInstalledOwnedFixtureV1(t *testing.T, portable, old bool) *installedOwnedFixtureV1 {
	t.Helper()
	f := &installedOwnedFixtureV1{}
	var sourceDir string
	var source CurrentBuild
	if portable {
		var lock deploy.BuildLockV1
		sourceDir, f.sourceStore, lock = pendingPortablePublicationFixtureV1(t)
		var err error
		f.sourceOperation, err = deploy.AcquireOperationLock(t.Context(), sourceDir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.sourceOperation.Unlock() })
		images := &pendingPublicationImagesV1{images: map[string]providers.RealizedImageV1{}, operation: f.sourceOperation, t: t, sequence: 60}
		state, err := publishBuild(t.Context(), f.sourceOperation, f.sourceStore, publicationInput(t, sourceDir, lock), images.backend(f.sourceStore, sourceDir))
		if err != nil {
			t.Fatal(err)
		}
		source = CurrentBuild{State: state, Generation: *state.Current, Lock: lock}
	} else {
		sourceDir, f.sourceOperation, f.sourceStore, source = installedBuildPublicationSourceFixture(t)
	}
	destDir, store, validatedLock, record, validatedImages := validatedRetirementFixtureV1(t)
	f.destinationStore, f.validated, f.validatedLock = store, record, validatedLock
	f.images = &pendingPublicationImagesV1{images: validatedImages.images, operation: validatedImages.operation, t: t}
	f.input = InstalledBuildPublicationInputV1{Environment: "demo", SourceDeploymentDir: sourceDir, DestinationDeploymentDir: destDir, Source: source, Build: source.Lock, Installation: installedBuildPublicationInstallation(destDir), References: fixedPublicationReferences(t, destDir, 71)}
	var err error
	f.sourcePairs, err = ProjectEnvironmentOwnedReferencesV1(source.Generation, source.Lock, "demo", sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	// Model global Docker aliases so destination cleanup can be checked against
	// the independently owned source, even when their image bytes coincide.
	for _, pair := range f.sourcePairs {
		f.images.images[pair.Reference] = pair.Image
	}
	if old {
		oldLock := source.Lock
		oldLock.RuntimeLayer.Result = providers.RealizedImageV1{Digest: rendererDigest("e"), ConfigDigest: rendererDigest("e"), RootFSSubject: rendererDigest("f")}
		oldLock.FinalImage = oldLock.RuntimeLayer.Result
		policy, err := deploy.RuntimePolicyDigestV1(oldLock.RuntimePolicy)
		if err != nil {
			t.Fatal(err)
		}
		oldLock.ValidationRecord, err = deploy.PublishPrefixValidation(t.Context(), f.sourceStore, deploy.PrefixValidationV1{Schema: deploy.PrefixValidationSchemaV1, SubjectRootFS: oldLock.FinalImage.RootFSSubject, RuntimePolicy: policy, Profiles: []providers.ValidationEvidence{}, ExposedOutputs: []providers.ExecutableEvidence{}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := transferInstalledBuildClosure(t.Context(), f.sourceOperation, f.images.operation, f.sourceStore, store, oldLock); err != nil {
			t.Fatal(err)
		}
		digest, err := f.images.operation.PublishBuildLock(oldLock, registry.ValidateRequirementProfileV1)
		if err != nil {
			t.Fatal(err)
		}
		generation := source.Generation
		generation.Reference = fixedPublicationReferences(t, destDir, 41).Generation
		generation.ImageDigest, generation.RootFSSubject, generation.BuildLockDigest = oldLock.FinalImage.Digest, oldLock.FinalImage.RootFSSubject, digest
		f.prior = source.State
		f.prior.Current = &generation
		f.prior.Deployment = &deploy.DeploymentStateV1{Schema: deploy.DeploymentStateSchemaV1, Installation: f.input.Installation}
		if err := f.images.operation.CommitStateV1(nil, f.prior); err != nil {
			t.Fatal(err)
		}
		f.oldPairs, err = ProjectEnvironmentOwnedReferencesV1(generation, oldLock, "demo", destDir)
		if err != nil {
			t.Fatal(err)
		}
		for _, pair := range f.oldPairs {
			f.images.images[pair.Reference] = pair.Image
		}
	}
	f.sourceSnapshot = pendingOwnedFilesystemSnapshotV1(t, sourceDir, f.sourceStore.Root())
	return f
}

func (f *installedOwnedFixtureV1) publish(t *testing.T) (deploy.StateV1, error) {
	t.Helper()
	return publishInstalledBuildV1(t.Context(), f.sourceOperation, f.images.operation, f.sourceStore, f.destinationStore, f.input, installedOwnedPublicationBackendV1(t, f.sourceOperation, f.images, f.destinationStore, f.input.DestinationDeploymentDir))
}

func (f *installedOwnedFixtureV1) restartDestination(t *testing.T) {
	t.Helper()
	if err := f.images.operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.images.operation, err = deploy.AcquireOperationLock(t.Context(), f.input.DestinationDeploymentDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.images.operation.Unlock() })
}

func (f *installedOwnedFixtureV1) assertSurvivingOwners(t *testing.T) {
	t.Helper()
	for _, pair := range f.sourcePairs {
		if f.images.images[pair.Reference] != pair.Image {
			t.Fatal("installed publication changed an independent source alias")
		}
	}
	if after := pendingOwnedFilesystemSnapshotV1(t, f.input.SourceDeploymentDir, f.sourceStore.Root()); !reflect.DeepEqual(after, f.sourceSnapshot) {
		t.Fatal("installed publication changed source state, locks or store")
	}
	record, found, err := f.images.operation.ReadValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(record, f.validated) {
		t.Fatalf("independent validated record changed: %v", err)
	}
	if f.images.images[record.ImageReference] != record.Image || f.images.images[record.Companion.Reference] != record.Companion.Image {
		t.Fatal("independent validated aliases changed")
	}
	if _, found, err := f.images.operation.ReadBuildLock(record.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil || !found {
		t.Fatalf("independent validated lock lost: %v", err)
	}
	if _, err := deploy.BuildLockStoreClosure(f.validatedLock, f.destinationStore, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
		t.Fatal("independent validated closure lost", err)
	}
}

func TestInstalledOwnedPublicationV1(t *testing.T) {
	for _, portable := range []bool{false, true} {
		for _, old := range []bool{false, true} {
			t.Run(fmt.Sprintf("portable=%t/old=%t", portable, old), func(t *testing.T) {
				f := newInstalledOwnedFixtureV1(t, portable, old)
				state, err := f.publish(t)
				if err != nil {
					t.Fatal(err)
				}
				pairs, err := ProjectEnvironmentOwnedReferencesV1(*state.Current, f.input.Build, "demo", f.input.DestinationDeploymentDir)
				if err != nil {
					t.Fatal(err)
				}
				for _, pair := range pairs {
					if f.images.images[pair.Reference] != pair.Image {
						t.Fatal("destination does not independently own candidate")
					}
				}
				for _, pair := range f.oldPairs {
					if _, found := f.images.images[pair.Reference]; found {
						t.Fatal("old destination alias survived")
					}
				}
				if _, found := f.images.images[f.input.References.Temporary]; found {
					t.Fatal("temporary alias survived")
				}
				if _, found, err := f.images.operation.ReadPendingBuild(); err != nil || found {
					t.Fatal("completed installed intent survived", err)
				}
				if err := f.sourceOperation.RequireHeld(); err != nil {
					t.Fatal(err)
				}
				if err := f.images.operation.RequireHeld(); err != nil {
					t.Fatal(err)
				}
				f.assertSurvivingOwners(t)
			})
		}
	}
}

func TestInstalledOwnedPartialTagRecoveryV1(t *testing.T) {
	for _, portable := range []bool{false, true} {
		faults := []string{"create-temporary", "create-primary"}
		if portable {
			faults = append(faults, "create-companion")
		}
		for _, fault := range faults {
			for _, after := range []bool{false, true} {
				t.Run(fmt.Sprintf("portable=%t/%s/after=%t", portable, fault, after), func(t *testing.T) {
					f := newInstalledOwnedFixtureV1(t, portable, true)
					f.images.fault, f.images.after = fault, after
					if _, err := f.publish(t); !errors.Is(err, errPendingPublicationFaultV1) {
						t.Fatal("tag fault not observed", err)
					}
					state, _, err := f.images.operation.ReadStateV1()
					if err != nil || !reflect.DeepEqual(state, f.prior) {
						t.Fatal("partial tags changed installed state", err)
					}
					pending, found, err := f.images.operation.ReadPendingBuild()
					if err != nil || !found || !reflect.DeepEqual(pending.OldReferences, f.oldPairs) || (pending.Candidate.Companion != nil) != portable {
						t.Fatal("incomplete exact intent", err)
					}
					f.restartDestination(t)
					f.images.fault = ""
					if err := f.images.recover(f.destinationStore, f.input.DestinationDeploymentDir); err != nil {
						t.Fatal(err)
					}
					for _, pair := range f.oldPairs {
						if f.images.images[pair.Reference] != pair.Image {
							t.Fatal("rollback lost old destination owner")
						}
					}
					if _, err := f.publish(t); err != nil {
						t.Fatal("installed publication retry", err)
					}
					f.assertSurvivingOwners(t)
				})
			}
		}
	}
}

func TestInstalledOwnedCleanupAfterPrunedLockV1(t *testing.T) {
	for _, fault := range []string{"remove-primary", "remove-companion"} {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/after=%t", fault, after), func(t *testing.T) {
				f := newInstalledOwnedFixtureV1(t, true, true)
				f.images.fault, f.images.after = fault, after
				if _, err := f.publish(t); !errors.Is(err, errPendingPublicationFaultV1) {
					t.Fatal(err)
				}
				pending, found, err := f.images.operation.ReadPendingBuild()
				if err != nil || !found || pending.Phase != deploy.PendingBuildPhaseCleanup || len(pending.OldReferences) != 2 {
					t.Fatal("cleanup intent not retained", err)
				}
				if err := f.images.operation.RemoveBuildLock(f.prior.Current.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil {
					t.Fatal(err)
				}
				f.restartDestination(t)
				f.images.fault = ""
				if err := f.images.recover(f.destinationStore, f.input.DestinationDeploymentDir); err != nil {
					t.Fatal("exact cleanup required a pruned lock", err)
				}
				state, _, err := f.images.operation.ReadStateV1()
				if err != nil || state.Current.Reference != f.input.References.Generation {
					t.Fatal("recovery replaced committed installed owner", err)
				}
				for _, pair := range f.oldPairs {
					if _, found := f.images.images[pair.Reference]; found {
						t.Fatal("old exact alias survived")
					}
				}
				if _, found, err := f.images.operation.ReadPendingBuild(); err != nil || found {
					t.Fatal("recovery did not complete", err)
				}
				f.assertSurvivingOwners(t)
			})
		}
	}
}

func TestInstalledOwnedRetargetedDestinationV1(t *testing.T) {
	for _, which := range []int{0, 1} {
		t.Run(fmt.Sprintf("pair-%d", which), func(t *testing.T) {
			f := newInstalledOwnedFixtureV1(t, true, true)
			pair := f.oldPairs[which]
			foreign := pair.Image
			foreign.Digest, foreign.ConfigDigest = rendererDigest("9"), rendererDigest("9")
			f.images.images[pair.Reference] = foreign
			if _, err := f.publish(t); err == nil {
				t.Fatal("retargeted destination silently removed")
			}
			f.restartDestination(t)
			if err := f.images.recover(f.destinationStore, f.input.DestinationDeploymentDir); err == nil {
				t.Fatal("retargeted recovery silently removed")
			}
			if f.images.images[pair.Reference] != foreign {
				t.Fatal("foreign alias removed")
			}
			f.assertSurvivingOwners(t)
			f.images.images[pair.Reference] = pair.Image
			if err := f.images.recover(f.destinationStore, f.input.DestinationDeploymentDir); err != nil {
				t.Fatal(err)
			}
			f.assertSurvivingOwners(t)
		})
	}
}

func TestInstalledOwnedMismatchBeforeEffectsV1(t *testing.T) {
	for _, mismatch := range []string{"source-state", "source-generation", "missing-source-lock", "source-environment", "candidate-platform", "destination-reference", "validated-overlap", "missing-companion-backend"} {
		t.Run(mismatch, func(t *testing.T) {
			f := newInstalledOwnedFixtureV1(t, true, true)
			backend := installedOwnedPublicationBackendV1(t, f.sourceOperation, f.images, f.destinationStore, f.input.DestinationDeploymentDir)
			switch mismatch {
			case "source-state":
				f.input.Source.State.BlueprintSource = "changed-selection.yaml"
			case "source-generation":
				f.input.Source.Generation.Reference = fixedPublicationReferences(t, f.input.SourceDeploymentDir, 90).Generation
			case "missing-source-lock":
				if err := f.sourceOperation.RemoveBuildLock(f.input.Source.Generation.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil {
					t.Fatal(err)
				}
			case "source-environment":
				f.input.Environment = "foreign"
			case "candidate-platform":
				f.input.Build.Platform.Architecture = "arm64"
			case "destination-reference":
				f.input.References = fixedPublicationReferences(t, f.input.SourceDeploymentDir, 91)
			case "validated-overlap":
				f.input.References.Generation = f.validated.ImageReference
			case "missing-companion-backend":
				backend.createCompanion = nil
			}
			beforeSource := pendingOwnedFilesystemSnapshotV1(t, f.input.SourceDeploymentDir, f.sourceStore.Root())
			beforeDest := pendingOwnedFilesystemSnapshotV1(t, f.input.DestinationDeploymentDir, f.destinationStore.Root())
			beforeImages := make(map[string]providers.RealizedImageV1)
			for ref, image := range f.images.images {
				beforeImages[ref] = image
			}
			backend.transferClosure = func(context.Context, *deploy.OperationLock, *deploy.OperationLock, providerstore.Store, providerstore.Store, deploy.BuildLockV1) ([]providerstore.StoreObjectRef, error) {
				t.Fatal("mismatch reached transfer")
				return nil, nil
			}
			if _, err := publishInstalledBuildV1(t.Context(), f.sourceOperation, f.images.operation, f.sourceStore, f.destinationStore, f.input, backend); err == nil {
				t.Fatal("mismatched authority accepted")
			}
			if !reflect.DeepEqual(beforeSource, pendingOwnedFilesystemSnapshotV1(t, f.input.SourceDeploymentDir, f.sourceStore.Root())) || !reflect.DeepEqual(beforeDest, pendingOwnedFilesystemSnapshotV1(t, f.input.DestinationDeploymentDir, f.destinationStore.Root())) || !reflect.DeepEqual(beforeImages, f.images.images) {
				t.Fatal("mismatch changed authority or aliases")
			}
		})
	}
}

func TestInstalledOwnedIntentRemovalCrashV1(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprintf("after=%t", after), func(t *testing.T) {
			f := newInstalledOwnedFixtureV1(t, true, true)
			f.images.fault, f.images.after = "remove-intent", after
			if _, err := f.publish(t); !errors.Is(err, errPendingPublicationFaultV1) {
				t.Fatal(err)
			}
			if _, found, err := f.images.operation.ReadBuildLock(f.prior.Current.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil || found {
				t.Fatal("old lock not actually pruned before intent removal", err)
			}
			if _, found, err := f.images.operation.ReadPendingBuild(); err != nil || found == after {
				t.Fatal("unexpected interrupted intent", err)
			}
			f.restartDestination(t)
			f.images.fault = ""
			if !after {
				if err := f.images.recover(f.destinationStore, f.input.DestinationDeploymentDir); err != nil {
					t.Fatal("postprune recovery", err)
				}
			}
			state, _, err := f.images.operation.ReadStateV1()
			if err != nil || state.Current.Reference != f.input.References.Generation {
				t.Fatal("intent failure lost committed destination", err)
			}
			pairs, err := ProjectEnvironmentOwnedReferencesV1(*state.Current, f.input.Build, "demo", f.input.DestinationDeploymentDir)
			if err != nil {
				t.Fatal(err)
			}
			for _, pair := range pairs {
				if f.images.images[pair.Reference] != pair.Image {
					t.Fatal("postprune recovery lost destination alias")
				}
			}
			f.assertSurvivingOwners(t)
		})
	}
}

func TestInstalledOwnedForeignDestinationStateBeforeEffectsV1(t *testing.T) {
	for _, current := range []bool{false, true} {
		t.Run(fmt.Sprintf("current=%t", current), func(t *testing.T) {
			f := newInstalledOwnedFixtureV1(t, true, current)
			foreign := f.input.Source.State
			foreign.Current = f.prior.Current
			document, err := blueprint.DecodeResolvedDocumentV1(foreign.Blueprint)
			if err != nil {
				t.Fatal(err)
			}
			document.Environment.ID = "foreign"
			foreign.Blueprint = testResolvedBlueprintV1(t, document)
			if err := f.images.operation.CommitStateV1(f.prior.Current, foreign); err != nil {
				t.Fatal(err)
			}
			before := pendingOwnedFilesystemSnapshotV1(t, f.input.DestinationDeploymentDir, f.destinationStore.Root())
			beforeImages := make(map[string]providers.RealizedImageV1)
			for reference, image := range f.images.images {
				beforeImages[reference] = image
			}
			backend := installedOwnedPublicationBackendV1(t, f.sourceOperation, f.images, f.destinationStore, f.input.DestinationDeploymentDir)
			backend.transferClosure = func(context.Context, *deploy.OperationLock, *deploy.OperationLock, providerstore.Store, providerstore.Store, deploy.BuildLockV1) ([]providerstore.StoreObjectRef, error) {
				t.Fatal("foreign destination reached transfer")
				return nil, nil
			}
			if _, err := publishInstalledBuildV1(t.Context(), f.sourceOperation, f.images.operation, f.sourceStore, f.destinationStore, f.input, backend); err == nil {
				t.Fatal("foreign destination state replaced")
			}
			if !reflect.DeepEqual(before, pendingOwnedFilesystemSnapshotV1(t, f.input.DestinationDeploymentDir, f.destinationStore.Root())) || !reflect.DeepEqual(beforeImages, f.images.images) {
				t.Fatal("foreign destination changed before rejection")
			}
			f.assertSurvivingOwners(t)
		})
	}
}
