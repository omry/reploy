package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// Metadata and lock/store bytes are real. Only exact Docker reference operations
// are substituted; effects assert that the held lock and durable intent exist.
type validatedPublicationImagesV1 struct {
	pendingPublicationImagesV1
	effects int
}

func (images *validatedPublicationImagesV1) backend(dir string) publishValidatedBuildBackendV1 {
	create := func(name, reference string, image providers.RealizedImageV1) error {
		if err := images.operation.RequireHeld(); err != nil {
			return err
		}
		intent, found, err := images.operation.ReadPendingValidatedBuildV1()
		if err != nil || !found {
			images.t.Fatalf("tag without durable intent: %v %v", found, err)
		}
		if _, found, err := images.operation.ReadBuildLock(intent.Candidate.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil || !found {
			images.t.Fatalf("tag without durable candidate lock: %v %v", found, err)
		}
		return images.effect(name, func() error {
			images.effects++
			if old, found := images.images[reference]; found && old != image {
				return fmt.Errorf("retargeted image %s", reference)
			}
			images.images[reference] = image
			return nil
		})
	}
	remove := func(name, reference string, image providers.RealizedImageV1) error {
		if err := images.operation.RequireHeld(); err != nil {
			return err
		}
		return images.effect(name, func() error {
			images.effects++
			if old, found := images.images[reference]; found && old != image {
				return fmt.Errorf("retargeted image %s", reference)
			}
			delete(images.images, reference)
			return nil
		})
	}
	verify := func(reference string, image providers.RealizedImageV1) error {
		images.effects++
		if actual, found := images.images[reference]; !found || actual != image {
			return fmt.Errorf("missing or retargeted image %s", reference)
		}
		return nil
	}
	return publishValidatedBuildBackendV1{
		newReferences: func(string, string) (EnvironmentImageReferences, error) {
			return fixedPublicationReferences(images.t, dir, 31), nil
		},
		createReference: func(_ context.Context, image providers.RealizedImageV1, refs EnvironmentImageReferences, kind EnvironmentReferenceKind, environment, dir string) error {
			if kind != EnvironmentReferenceGeneration {
				images.t.Fatal("validated publication tagged a temporary owner")
			}
			if err := ValidateEnvironmentImageReferences(refs, environment, dir); err != nil {
				return err
			}
			return create("create-primary", refs.Generation, image)
		},
		removeReference: func(_ context.Context, image providers.RealizedImageV1, reference, environment, dir string) error {
			if err := ValidateEnvironmentGenerationReference(reference, environment, dir); err != nil {
				return err
			}
			return remove("remove-primary", reference, image)
		},
		verifyReference: func(_ context.Context, image providers.RealizedImageV1, reference, _, _ string) error {
			return verify(reference, image)
		},
		createCompanion: func(_ context.Context, op *deploy.OperationLock, pair OwnedImageReferenceV1, owner deploy.EnvironmentGenerationState, environment, dir string) error {
			if op != images.operation {
				images.t.Fatal("companion has wrong lock")
			}
			if err := validatePortableOwnedReferenceV1(pair, owner, environment, dir); err != nil {
				return err
			}
			return create("create-companion", pair.Reference, pair.Image)
		},
		removeCompanion: func(_ context.Context, op *deploy.OperationLock, pair OwnedImageReferenceV1, owner deploy.EnvironmentGenerationState, environment, dir string) error {
			if op != images.operation {
				images.t.Fatal("companion has wrong lock")
			}
			if err := validatePortableOwnedReferenceV1(pair, owner, environment, dir); err != nil {
				return err
			}
			return remove("remove-companion", pair.Reference, pair.Image)
		},
		verifyCompanion: func(_ context.Context, op *deploy.OperationLock, pair OwnedImageReferenceV1, owner deploy.EnvironmentGenerationState, environment, dir string) error {
			if op != images.operation {
				images.t.Fatal("companion has wrong lock")
			}
			if err := validatePortableOwnedReferenceV1(pair, owner, environment, dir); err != nil {
				return err
			}
			return verify(pair.Reference, pair.Image)
		},
		publishLock: func(lock deploy.BuildLockV1) (canonical.Digest, error) {
			var digest canonical.Digest
			err := images.effect("lock", func() error {
				var err error
				digest, err = images.operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1)
				return err
			})
			return digest, err
		},
		writeIntent: func(intent deploy.PendingValidatedBuildV1) error {
			return images.effect("intent", func() error { return images.operation.WritePendingValidatedBuildV1(intent) })
		},
		commitRecord: func(record deploy.ValidatedBuildV1) error {
			return images.effect("commit", func() error { return images.operation.CommitValidatedBuildV1(record) })
		},
		removeIntent: func() error { return images.effect("remove-intent", images.operation.RemovePendingValidatedBuildV1) },
	}
}

func validatedPublicationRecordV1(t *testing.T, dir string, lock deploy.BuildLockV1, inputs ValidatedBuildInputsV1, sequence byte) deploy.ValidatedBuildV1 {
	t.Helper()
	digest, err := deploy.BuildLockDigestV1(lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	record := deploy.ValidatedBuildV1{Schema: deploy.ValidatedBuildSchemaV1, BlueprintDigest: inputs.BlueprintDigest, OverlayDigest: inputs.OverlayDigest, PackageOverridesDigest: inputs.PackageOverridesDigest, Platform: lock.Platform, BuildLockDigest: digest, Image: lock.FinalImage, ImageReference: fixedPublicationReferences(t, dir, sequence).Generation, PendingStorageCleanup: true}
	owner, err := validatedPublicationOwnerV1(record, lock)
	if err != nil {
		t.Fatal(err)
	}
	pairs, err := ProjectEnvironmentOwnedReferencesV1(owner, lock, "demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) == 2 {
		record.Owner = &owner
		record.Companion = &pairs[1]
	}
	return record
}

func validatedPublicationFixtureV1(t *testing.T) (string, providerstore.Store, deploy.BuildLockV1, ValidatedBuildInputsV1, *validatedPublicationImagesV1) {
	t.Helper()
	dir, store, lock := pendingPortablePublicationFixtureV1(t)
	document := publicationInput(t, dir, lock).Document
	overrides := deploy.EmptyPackageOverridesV1("demo")
	overrides.Environment.Base = &deploy.BaseImageOverrideV1{Image: lock.Base.AuthorReference}
	overrides.Environment.PackageOverrides["python"] = map[string]deploy.PackageOverrideChoiceV1{"demo-server": {Path: "."}}
	inputs, err := ValidatedBuildInputs(document, lock.Overlay, overrides, dir, lock.Platform)
	if err != nil {
		t.Fatal(err)
	}
	// The supplied build uses the resolved sidecar intent, including its Go
	// representation of empty exclusions, just as the provider build does.
	lock.PackageOverrides = inputs.PackageOverrides
	op, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	images := &validatedPublicationImagesV1{pendingPublicationImagesV1: pendingPublicationImagesV1{images: map[string]providers.RealizedImageV1{}, operation: op, t: t}}
	t.Cleanup(func() { _ = images.operation.Unlock() })
	return dir, store, lock, inputs, images
}

func (images *validatedPublicationImagesV1) retain(t *testing.T, record deploy.ValidatedBuildV1, dir string) {
	t.Helper()
	pairs, err := validatedRecordReferencesV1(record, "demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range pairs {
		images.images[pair.ImageReference] = pair.Image
	}
}

func (images *validatedPublicationImagesV1) restart(t *testing.T, dir string) {
	t.Helper()
	if err := images.operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	op, err := deploy.AcquireExistingOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	images.operation = op
}

func commitValidatedTestCurrentV1(t *testing.T, images *validatedPublicationImagesV1, dir string, lock deploy.BuildLockV1, owner deploy.ValidatedBuildV1) {
	t.Helper()
	if _, err := images.operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	payload, err := blueprint.EncodeResolvedDocumentV1(publicationInput(t, dir, lock).Document)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := validatedPublicationOwnerV1(owner, lock)
	if err != nil {
		t.Fatal(err)
	}
	state := deploy.StateV1{Schema: deploy.StateSchemaV1, Blueprint: payload, Platform: lock.Platform, Overlay: lock.Overlay, Current: &generation}
	if err := images.operation.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
}

func TestPendingValidatedPublicationCrashRecoveryV1(t *testing.T) {
	for _, previousKind := range []string{"absent", "portable", "ordinary"} {
		for _, fault := range []string{"lock", "intent", "create-companion", "create-primary", "commit", "remove-intent"} {
			for _, after := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/after=%v", previousKind, fault, after), func(t *testing.T) {
					dir, store, lock, inputs, images := validatedPublicationFixtureV1(t)
					var previous *deploy.ValidatedBuildV1
					if previousKind != "absent" {
						oldLock := lock
						if previousKind == "ordinary" {
							_, oldLock = publicationLockFixture(t, dir, "1", "2", "3")
							oldLock.BlueprintDigest = lock.BlueprintDigest
						}
						if _, err := images.operation.PublishBuildLock(oldLock, registry.ValidateRequirementProfileV1); err != nil {
							t.Fatal(err)
						}
						old := validatedPublicationRecordV1(t, dir, oldLock, inputs, 21)
						if err := images.operation.CommitValidatedBuildV1(old); err != nil {
							t.Fatal(err)
						}
						images.retain(t, old, dir)
						previous = &old
					}
					// An independent owner with byte-identical image remains untouched.
					independent := validatedPublicationRecordV1(t, dir, lock, inputs, 22)
					images.retain(t, independent, dir)
					if previousKind == "portable" {
						commitValidatedTestCurrentV1(t, images, dir, lock, independent)
					}
					retained := map[string]providers.RealizedImageV1{}
					for reference, image := range images.images {
						retained[reference] = image
					}
					images.fault, images.after = fault, after
					record, err := publishValidatedBuild(t.Context(), images.operation, store, "demo", dir, lock, inputs, images.backend(dir))
					// The after-commit fault leaves a visible candidate, but must still
					// report the unresolved commit error and retain its intent.
					selectedCandidate := fault == "remove-intent" || fault == "commit" && after
					publicationSucceeded := fault == "remove-intent"
					if !images.fired || publicationSucceeded && err != nil || !publicationSucceeded && !errors.Is(err, errPendingPublicationFaultV1) {
						t.Fatalf("fired=%v publicationSucceeded=%v error=%v", images.fired, publicationSucceeded, err)
					}
					if publicationSucceeded && (record.Companion == nil || !record.PendingStorageCleanup) {
						t.Fatalf("committed owner lost: %#v", record)
					}
					intent, hadIntent, err := images.operation.ReadPendingValidatedBuildV1()
					if err != nil {
						t.Fatal(err)
					}
					wantIntent := fault != "lock" && !(fault == "intent" && !after) && !(fault == "remove-intent" && after)
					if hadIntent != wantIntent {
						t.Fatalf("intent=%v want=%v", hadIntent, wantIntent)
					}
					images.restart(t, dir)
					images.fault = ""
					before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
					recovered, err := recoverPendingValidatedPublicationV1(t.Context(), images.operation, store, "demo", dir, images.backend(dir))
					if err != nil || recovered != hadIntent {
						t.Fatalf("recovered=%v error=%v", recovered, err)
					}
					selected, found, err := images.operation.ReadValidatedBuildV1()
					if err != nil {
						t.Fatal(err)
					}
					if selectedCandidate {
						candidate := record
						if hadIntent {
							candidate = intent.Candidate
						}
						if !found || !reflect.DeepEqual(selected, candidate) {
							t.Fatal("recovery changed visible selected candidate")
						}
					} else if !sameValidatedRecordV1(selected, found, previous) {
						t.Fatal("rollback changed previous owner")
					}
					for reference, image := range retained {
						if images.images[reference] != image {
							t.Fatalf("independent owner removed: %s", reference)
						}
					}
					candidate := validatedPublicationRecordV1(t, dir, lock, inputs, 31)
					pairs, err := validatedRecordReferencesV1(candidate, "demo", dir)
					if err != nil {
						t.Fatal(err)
					}
					for _, pair := range pairs {
						image, found := images.images[pair.ImageReference]
						if found != selectedCandidate || found && image != pair.Image {
							t.Fatalf("candidate pair=%s retained=%v selectedCandidate=%v", pair.ImageReference, found, selectedCandidate)
						}
					}
					if _, found, err := images.operation.ReadPendingValidatedBuildV1(); err != nil || found {
						t.Fatalf("intent remained: %v %v", found, err)
					}
					// Recovery removes only its intent; it never prunes locks/store bytes.
					afterSnapshot := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
					for path, value := range before {
						if strings.HasSuffix(path, "pending-validated-build.json") {
							continue
						}
						if afterSnapshot[path] != value {
							t.Fatalf("recovery changed storage %s", path)
						}
					}
					if hadIntent && !reflect.DeepEqual(intent.Candidate.PendingCleanup, selected.PendingCleanup) && selectedCandidate {
						t.Fatal("recovery erased superseded inventory")
					}
					if recovered, err := recoverPendingValidatedPublicationV1(t.Context(), images.operation, store, "demo", dir, images.backend(dir)); err != nil || recovered {
						t.Fatalf("repeat recovery=%v error=%v", recovered, err)
					}
				})
			}
		}
	}
}

func TestValidatedPublicationCommitErrorPreservesIntentAndSelectionV1(t *testing.T) {
	dir, store, lock, inputs, images := validatedPublicationFixtureV1(t)
	previous := validatedPublicationRecordV1(t, dir, lock, inputs, 21)
	if _, err := images.operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	if err := images.operation.CommitValidatedBuildV1(previous); err != nil {
		t.Fatal(err)
	}
	images.retain(t, previous, dir)

	independent := validatedPublicationRecordV1(t, dir, lock, inputs, 22)
	images.retain(t, independent, dir)
	commitValidatedTestCurrentV1(t, images, dir, lock, independent)
	retained := map[string]providers.RealizedImageV1{}
	for reference, image := range images.images {
		retained[reference] = image
	}

	failure := errors.New("injected validated record directory sync failure")
	backend := images.backend(dir)
	backend.commitRecord = func(record deploy.ValidatedBuildV1) error {
		if err := images.operation.CommitValidatedBuildV1(record); err != nil {
			return err
		}
		return failure
	}
	if _, err := publishValidatedBuild(t.Context(), images.operation, store, "demo", dir, lock, inputs, backend); !errors.Is(err, failure) {
		t.Fatalf("publication error=%v", err)
	}

	intent, found, err := images.operation.ReadPendingValidatedBuildV1()
	expectedCandidate := validatedPublicationRecordV1(t, dir, lock, inputs, 31)
	expectedCleanup, cleanupErr := pendingValidatedCleanupV1(&previous, expectedCandidate, "demo", dir)
	if cleanupErr != nil {
		t.Fatal(cleanupErr)
	}
	expectedCandidate.PendingCleanup = expectedCleanup
	expectedIntent := deploy.PendingValidatedBuildV1{Schema: deploy.PendingValidatedBuildSchemaV1, Previous: &previous, Candidate: expectedCandidate}
	if err != nil || !found || !reflect.DeepEqual(intent, expectedIntent) {
		t.Fatalf("intent=%#v found=%v error=%v", intent, found, err)
	}
	selected, found, err := images.operation.ReadValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(selected, intent.Candidate) {
		t.Fatalf("visible selected owner=%#v found=%v error=%v", selected, found, err)
	}
	candidateReferences, err := validatedRecordReferencesV1(intent.Candidate, "demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, reference := range candidateReferences {
		if image, found := images.images[reference.ImageReference]; !found || image != reference.Image {
			t.Fatalf("candidate reference was not retained: %s", reference.ImageReference)
		}
	}
	for reference, image := range retained {
		if images.images[reference] != image {
			t.Fatalf("previous or independent current reference changed before restart: %s", reference)
		}
	}

	images.restart(t, dir)
	before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
	recovered, err := recoverPendingValidatedPublicationV1(t.Context(), images.operation, store, "demo", dir, images.backend(dir))
	if err != nil || !recovered {
		t.Fatalf("recovered=%v error=%v", recovered, err)
	}
	selected, found, err = images.operation.ReadValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(selected, intent.Candidate) {
		t.Fatalf("restart changed visible selected owner: %#v found=%v error=%v", selected, found, err)
	}
	if _, found, err := images.operation.ReadPendingValidatedBuildV1(); err != nil || found {
		t.Fatalf("intent remained after recovery: %v %v", found, err)
	}
	for reference, image := range retained {
		if images.images[reference] != image {
			t.Fatalf("recovery changed previous or independent current reference: %s", reference)
		}
	}
	for _, reference := range candidateReferences {
		if image, found := images.images[reference.ImageReference]; !found || image != reference.Image {
			t.Fatalf("recovery removed selected candidate reference: %s", reference.ImageReference)
		}
	}
	after := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
	validatedRecordKey := filepath.Join("deployment", "validated-build.json")
	pendingIntentKey := filepath.Join("deployment", "pending-validated-build.json")
	if before[validatedRecordKey] != after[validatedRecordKey] {
		t.Fatal("recovery changed the selected validated record")
	}
	if _, found := before[pendingIntentKey]; !found {
		t.Fatal("pending intent missing from pre-recovery snapshot")
	}
	if _, found := after[pendingIntentKey]; found {
		t.Fatal("recovery left the pending intent behind")
	}
	for path, value := range before {
		if path == pendingIntentKey {
			continue
		}
		if after[path] != value {
			t.Fatalf("recovery changed unrelated durable file %s", path)
		}
	}
	for path, value := range after {
		if path != pendingIntentKey && before[path] != value {
			t.Fatalf("recovery added durable file %s", path)
		}
	}
}

func TestValidatedPublicationRetainsExactSupersededPairV1(t *testing.T) {
	dir, store, lock, inputs, images := validatedPublicationFixtureV1(t)
	previous := validatedPublicationRecordV1(t, dir, lock, inputs, 21)
	if _, err := images.operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	if err := images.operation.CommitValidatedBuildV1(previous); err != nil {
		t.Fatal(err)
	}
	images.retain(t, previous, dir)
	record, err := publishValidatedBuild(t.Context(), images.operation, store, "demo", dir, lock, inputs, images.backend(dir))
	if err != nil {
		t.Fatal(err)
	}
	if record.Owner == nil || record.Companion == nil || len(record.PendingCleanup) != 2 || !record.PendingStorageCleanup {
		t.Fatalf("inventory=%#v", record)
	}
	want, err := validatedRecordReferencesV1(previous, "demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range want {
		matched := false
		for _, pending := range record.PendingCleanup {
			if reflect.DeepEqual(pair, pending) {
				matched = true
			}
		}
		if !matched || images.images[pair.ImageReference] != pair.Image {
			t.Fatalf("superseded pair lost: %#v", pair)
		}
	}
	persisted, found, err := images.operation.ReadValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(persisted, record) {
		t.Fatalf("durable inventory differs: %v %v", found, err)
	}
}

func TestPendingValidatedPublicationConflictsPreserveOwnershipV1(t *testing.T) {
	for _, conflict := range []string{"changed-previous", "malformed-intent", "missing-lock", "changed-overlay", "changed-companion-record", "changed-cleanup-inventory", "retargeted-primary", "retargeted-companion", "current-alias", "current-intent"} {
		t.Run(conflict, func(t *testing.T) {
			dir, store, lock, inputs, images := validatedPublicationFixtureV1(t)
			images.fault, images.after = "create-primary", true
			if _, err := publishValidatedBuild(t.Context(), images.operation, store, "demo", dir, lock, inputs, images.backend(dir)); !errors.Is(err, errPendingPublicationFaultV1) {
				t.Fatalf("prepare error=%v", err)
			}
			intent, found, err := images.operation.ReadPendingValidatedBuildV1()
			if err != nil || !found {
				t.Fatalf("intent=%v %v", found, err)
			}
			switch conflict {
			case "changed-previous":
				changed := validatedPublicationRecordV1(t, dir, lock, inputs, 23)
				if err := images.operation.CommitValidatedBuildV1(changed); err != nil {
					t.Fatal(err)
				}
				images.retain(t, changed, dir)
			case "malformed-intent":
				if err := os.WriteFile(filepath.Join(dir, ".reploy", "pending-validated-build.json"), []byte(`{"schema":"unknown"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing-lock":
				if err := images.operation.RemoveAllBuildLocks(registry.ValidateRequirementProfileV1); err != nil {
					t.Fatal(err)
				}
			case "changed-overlay":
				intent.Candidate.OverlayDigest = rendererDigest("e")
				if err := images.operation.RemovePendingValidatedBuildV1(); err != nil {
					t.Fatal(err)
				}
				if err := images.operation.WritePendingValidatedBuildV1(intent); err != nil {
					t.Fatal(err)
				}
			case "changed-companion-record", "changed-cleanup-inventory":
				if conflict == "changed-companion-record" {
					intent.Candidate.Companion.Image = providers.RealizedImageV1{Digest: rendererDigest("e"), ConfigDigest: rendererDigest("e"), RootFSSubject: rendererDigest("e")}
				} else {
					intent.Candidate.PendingCleanup = []deploy.ValidatedBuildReferenceV1{{Image: lock.FinalImage, ImageReference: fixedPublicationReferences(t, dir, 25).Generation}}
				}
				if err := images.operation.RemovePendingValidatedBuildV1(); err != nil {
					t.Fatal(err)
				}
				if err := images.operation.WritePendingValidatedBuildV1(intent); err != nil {
					t.Fatal(err)
				}
			case "retargeted-primary":
				images.images[intent.Candidate.ImageReference] = providers.RealizedImageV1{Digest: rendererDigest("e"), ConfigDigest: rendererDigest("e"), RootFSSubject: rendererDigest("e")}
			case "retargeted-companion":
				images.images[intent.Candidate.Companion.Reference] = providers.RealizedImageV1{Digest: rendererDigest("e"), ConfigDigest: rendererDigest("e"), RootFSSubject: rendererDigest("e")}
			case "current-alias":
				commitValidatedTestCurrentV1(t, images, dir, lock, intent.Candidate)
			case "current-intent":
				refs := fixedPublicationReferences(t, dir, 24)
				pending := deploy.PendingBuildV1{Schema: deploy.PendingBuildSchemaV1, Phase: deploy.PendingBuildPhaseValidated, Candidate: deploy.PendingCandidateV1{TemporaryReference: refs.Temporary, GenerationReference: refs.Generation, Image: lock.FinalImage, BuildLockDigest: intent.Candidate.BuildLockDigest, StoreObjects: []providerstore.StoreObjectRef{}}, Cleanup: []deploy.CleanupItemV1{}}
				if err := images.operation.WritePendingBuild(pending); err != nil {
					t.Fatal(err)
				}
			}
			images.restart(t, dir)
			images.fault = ""
			before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
			refs := map[string]providers.RealizedImageV1{}
			for reference, image := range images.images {
				refs[reference] = image
			}
			_, err = recoverPendingValidatedPublicationV1(t.Context(), images.operation, store, "demo", dir, images.backend(dir))
			if err == nil {
				t.Fatal("unproven ownership recovered")
			}
			if !reflect.DeepEqual(before, pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())) {
				t.Fatal("conflict changed durable metadata/store")
			}
			// Partial rollback may have removed its proven primary before a
			// retargeted companion failed. The retargeted alias itself survives.
			if conflict == "retargeted-companion" {
				delete(refs, intent.Candidate.ImageReference)
			}
			if !reflect.DeepEqual(refs, images.images) {
				t.Fatal("conflict removed unproven references")
			}
		})
	}
}

func TestRunProviderInstallRejectsValidatedOwnerBeforeInstallEffectsV1(t *testing.T) {
	for _, existingDestination := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing-destination=%v", existingDestination), func(t *testing.T) {
			dir, store, lock, inputs, images := validatedPublicationFixtureV1(t)
			if _, err := publishValidatedBuild(t.Context(), images.operation, store, "demo", dir, lock, inputs, images.backend(dir)); err != nil {
				t.Fatal(err)
			}
			_, ordinary := publicationLockFixture(t, dir, "1", "2", "3")
			ordinary.BlueprintDigest = lock.BlueprintDigest
			current := validatedPublicationRecordV1(t, dir, ordinary, inputs, 22)
			commitValidatedTestCurrentV1(t, images, dir, ordinary, current)
			state, found, err := images.operation.ReadStateV1()
			if err != nil || !found {
				t.Fatalf("state=%v %v", found, err)
			}
			before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
			if err := images.operation.Unlock(); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "installation")
			if existingDestination {
				if err := os.Mkdir(destination, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			backend := newProviderInstallRunBackendV1()
			backend.acquire = func(ctx context.Context, path string) (*deploy.OperationLock, error) {
				if path != dir {
					t.Fatal("validated guard allowed destination acquisition")
				}
				return deploy.AcquireOperationLock(ctx, path)
			}
			released := false
			backend.release = func(op *deploy.OperationLock) error { released = true; return op.Unlock() }
			backend.buildSource = func(_ context.Context, input LockedProviderBuildRunInputV1) (LockedProviderBuildExecutionResultV1, error) {
				if err := input.Operation.RequireHeld(); err != nil {
					t.Fatal(err)
				}
				return LockedProviderBuildExecutionResultV1{State: state, Lock: ordinary}, nil
			}
			backend.prepareAccount = func(context.Context, blueprint.SystemAccount, providerstore.Store, CurrentBuild, providerInstallRunInputV1) (providerInstallRunInputV1, error) {
				t.Fatal("validated guard allowed host-account changes")
				return providerInstallRunInputV1{}, nil
			}
			_, err = runProviderInstallV1(t.Context(), providerInstallRunInputV1{SourceDeploymentDir: dir, DestinationDeploymentDir: destination}, backend)
			if err == nil || !strings.Contains(err.Error(), "completed installed ownership transfer") || !released {
				t.Fatalf("error=%v released=%v", err, released)
			}
			if existingDestination {
				entries, err := os.ReadDir(destination)
				if err != nil || len(entries) != 0 {
					t.Fatalf("destination changed: %v %v", entries, err)
				}
			} else if _, err := os.Lstat(destination); !os.IsNotExist(err) {
				t.Fatalf("destination created: %v", err)
			}
			if after := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root()); !reflect.DeepEqual(before, after) {
				t.Fatal("validated source ownership changed")
			}
		})
	}
}

func TestValidatedPublicationConsumerGuardsV1(t *testing.T) {
	for _, boundary := range []string{"pending", "committed", "cleanup-inventory"} {
		t.Run(boundary, func(t *testing.T) {
			dir, store, lock, inputs, images := validatedPublicationFixtureV1(t)
			if boundary == "cleanup-inventory" {
				previous := validatedPublicationRecordV1(t, dir, lock, inputs, 21)
				if _, err := images.operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1); err != nil {
					t.Fatal(err)
				}
				if err := images.operation.CommitValidatedBuildV1(previous); err != nil {
					t.Fatal(err)
				}
				images.retain(t, previous, dir)
			}
			if boundary == "pending" {
				images.fault, images.after = "create-primary", true
			}
			record, err := publishValidatedBuild(t.Context(), images.operation, store, "demo", dir, lock, inputs, images.backend(dir))
			if boundary == "pending" {
				if !errors.Is(err, errPendingPublicationFaultV1) {
					t.Fatalf("prepare error=%v", err)
				}
				intent, found, err := images.operation.ReadPendingValidatedBuildV1()
				if err != nil || !found {
					t.Fatalf("intent=%v %v", found, err)
				}
				record = intent.Candidate
			} else if err != nil {
				t.Fatal(err)
			}
			before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
			effects := images.effects
			remove := func(context.Context, providers.RealizedImageV1, string, string, string) error {
				t.Fatal("guard allowed reference removal")
				return nil
			}
			document := publicationInput(t, dir, lock).Document
			for name, action := range map[string]func() error{
				"load": func() error {
					_, _, err := LoadValidatedBuildCandidate(t.Context(), images.operation, store, document, deploy.StateV1{}, deploy.EmptyPackageOverridesV1("demo"), dir, true, true)
					return err
				},
				"discard": func() error {
					_, err := discardValidatedBuild(t.Context(), images.operation, store, "demo", dir, remove)
					return err
				},
				"retry": func() error {
					_, _, err := RetryValidatedBuildCleanup(t.Context(), images.operation, store, "demo", dir)
					return err
				},
				"references": func() error {
					_, errs := cleanupPendingValidatedBuildReferences(t.Context(), images.operation, record, "demo", dir, remove)
					return errors.Join(errs...)
				},
				"storage": func() error { return cleanupValidatedBuildStorage(images.operation, store, &lock) },
				"later-consumers": func() error {
					return requirePublicationConsumerBoundaryV1(images.operation, "completed consumer ownership transition")
				},
				"provider-failure": func() error {
					return cleanupFailedProviderBuildV1(t.Context(), LockedProviderBuildPreparationV1{Operation: images.operation, Store: store, Environment: "demo", DeploymentDir: dir})
				},
			} {
				t.Run(name, func(t *testing.T) {
					if err := action(); err == nil {
						t.Fatal("unmet consumer boundary accepted")
					}
				})
			}
			if boundary != "committed" {
				currentImages := &pendingPublicationImagesV1{images: images.images, operation: images.operation, t: t}
				if _, err := publishBuild(t.Context(), images.operation, store, publicationInput(t, dir, lock), currentImages.backend(store, dir)); err == nil {
					t.Fatal("current publication accepted pending validated ownership")
				}
				if _, err := RecoverPendingPublication(t.Context(), images.operation, store, nil, "demo", dir, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err == nil {
					t.Fatal("current recovery accepted pending validated ownership")
				}
			}
			if effects != images.effects || !reflect.DeepEqual(before, pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())) {
				t.Fatal("guard mutated reference or durable ownership")
			}
		})
	}
}

func TestValidatedConsumerGuardRejectsMissingPortableMetadataV1(t *testing.T) {
	for _, consumer := range []string{"boundary", "discard", "retry", "load"} {
		t.Run(consumer, func(t *testing.T) {
			dir, store, lock, inputs, images := validatedPublicationFixtureV1(t)
			record := validatedPublicationRecordV1(t, dir, lock, inputs, 31)
			if record.Owner == nil || record.Companion == nil {
				t.Fatal("fixture did not produce portable ownership")
			}
			if _, err := images.operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1); err != nil {
				t.Fatal(err)
			}
			images.retain(t, record, dir)
			// The canonical record codec permits both optional ownership fields to
			// be absent. The retained lock must still prevent a consumer from
			// treating that portable owner as ordinary.
			record.Owner = nil
			record.Companion = nil
			if err := images.operation.CommitValidatedBuildV1(record); err != nil {
				t.Fatal(err)
			}

			before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
			retained := map[string]providers.RealizedImageV1{}
			for reference, image := range images.images {
				retained[reference] = image
			}
			removed := 0
			removeReference := func(_ context.Context, _ providers.RealizedImageV1, reference, _, _ string) error {
				removed++
				delete(images.images, reference)
				return nil
			}
			var err error
			switch consumer {
			case "boundary":
				err = requireValidatedConsumerBoundaryV1(images.operation, "test consumer")
			case "discard":
				_, err = discardValidatedBuild(t.Context(), images.operation, store, "demo", dir, removeReference)
			case "retry":
				_, _, err = RetryValidatedBuildCleanup(t.Context(), images.operation, store, "demo", dir)
			case "load":
				_, _, err = LoadValidatedBuildCandidate(
					t.Context(), images.operation, store, blueprint.Document{}, deploy.StateV1{}, deploy.PackageOverridesV1{}, dir, false, false,
				)
			}
			if err == nil || !strings.Contains(err.Error(), "record and retained build lock disagree") {
				t.Fatalf("consumer accepted omitted portable metadata: %v", err)
			}
			if removed != 0 || !reflect.DeepEqual(retained, images.images) {
				t.Fatal("portable owner reference changed before the guard rejected it")
			}
			if !reflect.DeepEqual(before, pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())) {
				t.Fatal("portable owner files changed before the guard rejected it")
			}
		})
	}
}

func TestValidatedConsumerGuardRejectsMissingOrMalformedPortableLockV1(t *testing.T) {
	for _, lockState := range []string{"missing", "malformed"} {
		t.Run(lockState, func(t *testing.T) {
			dir, store, lock, inputs, images := validatedPublicationFixtureV1(t)
			record := validatedPublicationRecordV1(t, dir, lock, inputs, 31)
			record.Owner = nil
			record.Companion = nil
			if _, err := images.operation.PublishBuildLock(lock, acceptProviderProfileOwnerForCutoverV1); err != nil {
				t.Fatal(err)
			}
			if err := images.operation.CommitValidatedBuildV1(record); err != nil {
				t.Fatal(err)
			}
			lockPath := filepath.Join(dir, ".reploy", "locks", "sha256-"+strings.TrimPrefix(string(record.BuildLockDigest), "sha256:")+".json")
			switch lockState {
			case "missing":
				if err := images.operation.RemoveBuildLock(record.BuildLockDigest, acceptProviderProfileOwnerForCutoverV1); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := os.WriteFile(lockPath, []byte("not a canonical build lock"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
			if err := requireValidatedConsumerBoundaryV1(images.operation, "test consumer"); err == nil {
				t.Fatal("live record with an unavailable retained lock passed the guard")
			}
			if !reflect.DeepEqual(before, pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())) {
				t.Fatal("missing or malformed lock changed durable ownership")
			}
		})
	}
}

func TestValidatedConsumerGuardRejectsOneSidedPortableMetadataV1(t *testing.T) {
	for _, missing := range []string{"owner", "companion"} {
		t.Run(missing, func(t *testing.T) {
			dir, store, lock, inputs, images := validatedPublicationFixtureV1(t)
			record := validatedPublicationRecordV1(t, dir, lock, inputs, 31)
			if _, err := images.operation.PublishBuildLock(lock, acceptProviderProfileOwnerForCutoverV1); err != nil {
				t.Fatal(err)
			}
			encoded, err := deploy.EncodeValidatedBuildV1(record)
			if err != nil {
				t.Fatal(err)
			}
			var needle string
			switch missing {
			case "owner":
				value, err := canonical.Marshal(*record.Owner)
				if err != nil {
					t.Fatal(err)
				}
				needle = `"owner":` + string(value) + `,`
			case "companion":
				value, err := canonical.Marshal(*record.Companion)
				if err != nil {
					t.Fatal(err)
				}
				needle = `,"companion":` + string(value)
			}
			content := strings.Replace(string(encoded), needle, "", 1)
			if content == string(encoded) {
				t.Fatal("failed to construct a one-sided canonical ownership record")
			}
			if err := os.WriteFile(filepath.Join(dir, ".reploy", "validated-build.json"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
			if err := requireValidatedConsumerBoundaryV1(images.operation, "test consumer"); err == nil {
				t.Fatal("one-sided portable metadata passed the guard")
			}
			if !reflect.DeepEqual(before, pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())) {
				t.Fatal("one-sided portable metadata changed durable ownership")
			}
		})
	}
}

func TestValidatedConsumerGuardAllowsDiscardedOrdinaryRetryAfterLockPruningV1(t *testing.T) {
	dir, operation, store, current, state := currentBuildFixture(t, true)
	defer operation.Unlock()
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := ValidatedBuildInputs(
		document, state.Overlay, deploy.EmptyPackageOverridesV1(document.Environment.ID), dir, state.Platform,
	)
	if err != nil {
		t.Fatal(err)
	}
	candidate := validatedBuildStorageVariant(t, store, current, "7", "8")
	digest, err := operation.PublishBuildLock(candidate, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.CommitValidatedBuildV1(deploy.ValidatedBuildV1{
		Schema: deploy.ValidatedBuildSchemaV1, BlueprintDigest: inputs.BlueprintDigest,
		OverlayDigest: inputs.OverlayDigest, PackageOverridesDigest: inputs.PackageOverridesDigest,
		Platform: inputs.Platform, BuildLockDigest: digest, Image: candidate.FinalImage,
		ImageReference: "reploy/env/demo:validated-discarded-retry", PendingStorageCleanup: true, Discarded: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := operation.RemoveBuildLock(digest, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	if err := requireValidatedConsumerBoundaryV1(operation, "discarded ordinary retry"); err != nil {
		t.Fatalf("pruned lock blocked ordinary discarded cleanup: %v", err)
	}
	_, found, err := RetryValidatedBuildCleanup(t.Context(), operation, store, document.Environment.ID, dir)
	if err != nil || found {
		t.Fatalf("ordinary discarded cleanup retry found=%v err=%v", found, err)
	}
	if _, found, err := operation.ReadValidatedBuildV1(); err != nil || found {
		t.Fatalf("discarded retry record remained: found=%v err=%v", found, err)
	}
}

func TestValidatedPublicationPreflightPreventsTaggingV1(t *testing.T) {
	for _, conflict := range []string{"missing-lock", "current-alias", "previous-alias", "missing-companion-backend", "mutated-intent"} {
		t.Run(conflict, func(t *testing.T) {
			dir, store, lock, inputs, images := validatedPublicationFixtureV1(t)
			candidate := validatedPublicationRecordV1(t, dir, lock, inputs, 31)
			backend := images.backend(dir)
			switch conflict {
			case "missing-lock":
				backend.publishLock = func(deploy.BuildLockV1) (canonical.Digest, error) { return candidate.BuildLockDigest, nil }
			case "current-alias":
				commitValidatedTestCurrentV1(t, images, dir, lock, candidate)
			case "previous-alias":
				if _, err := images.operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1); err != nil {
					t.Fatal(err)
				}
				if err := images.operation.CommitValidatedBuildV1(candidate); err != nil {
					t.Fatal(err)
				}
			case "missing-companion-backend":
				backend.createCompanion = nil
			case "mutated-intent":
				backend.writeIntent = func(intent deploy.PendingValidatedBuildV1) error {
					intent.Candidate.OverlayDigest = rendererDigest("e")
					return images.operation.WritePendingValidatedBuildV1(intent)
				}
			}
			if _, err := publishValidatedBuild(t.Context(), images.operation, store, "demo", dir, lock, inputs, backend); err == nil {
				t.Fatal("unproven candidate accepted")
			}
			if images.effects != 0 || len(images.images) != 0 {
				t.Fatal("preflight allowed tagging/removal")
			}
		})
	}
}

func TestPendingValidatedRollbackResumesPartialRemovalV1(t *testing.T) {
	for _, fault := range []string{"remove-primary", "remove-companion", "remove-intent"} {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/after=%v", fault, after), func(t *testing.T) {
				dir, store, lock, inputs, images := validatedPublicationFixtureV1(t)
				images.fault, images.after = "create-primary", true
				if _, err := publishValidatedBuild(t.Context(), images.operation, store, "demo", dir, lock, inputs, images.backend(dir)); !errors.Is(err, errPendingPublicationFaultV1) {
					t.Fatal(err)
				}
				images.restart(t, dir)
				images.fault, images.after, images.fired = fault, after, false
				if _, err := recoverPendingValidatedPublicationV1(t.Context(), images.operation, store, "demo", dir, images.backend(dir)); !errors.Is(err, errPendingPublicationFaultV1) || !images.fired {
					t.Fatalf("fired=%v error=%v", images.fired, err)
				}
				images.restart(t, dir)
				images.fault = ""
				if _, err := recoverPendingValidatedPublicationV1(t.Context(), images.operation, store, "demo", dir, images.backend(dir)); err != nil {
					t.Fatal(err)
				}
				if len(images.images) != 0 {
					t.Fatalf("candidate aliases remain: %#v", images.images)
				}
				if _, found, err := images.operation.ReadPendingValidatedBuildV1(); err != nil || found {
					t.Fatalf("intent=%v %v", found, err)
				}
			})
		}
	}
}

func TestCurrentPublicationPreservesIndependentlyValidatedAliasesV1(t *testing.T) {
	for _, effect := range []string{"publish", "rollback", "retire-old"} {
		t.Run(effect, func(t *testing.T) {
			stubNoAbandonedBuildReferences(t)
			dir, store, lock, inputs, images := validatedPublicationFixtureV1(t)
			record, err := publishValidatedBuild(t.Context(), images.operation, store, "demo", dir, lock, inputs, images.backend(dir))
			if err != nil {
				t.Fatal(err)
			}
			currentImages := &pendingPublicationImagesV1{images: images.images, operation: images.operation, t: t}
			if effect != "publish" {
				candidate := record
				if effect == "retire-old" {
					candidate = validatedPublicationRecordV1(t, dir, lock, inputs, 22)
					commitValidatedTestCurrentV1(t, images, dir, lock, candidate)
					images.retain(t, candidate, dir)
				}
				refs := fixedPublicationReferences(t, dir, 31)
				if effect == "retire-old" {
					refs = fixedPublicationReferences(t, dir, 22)
				}
				closure, err := deploy.BuildLockStoreClosure(lock, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1)
				if err != nil {
					t.Fatal(err)
				}
				pending := deploy.PendingBuildV1{Schema: deploy.PendingBuildSchemaV1, Phase: deploy.PendingBuildPhaseValidated, Candidate: deploy.PendingCandidateV1{TemporaryReference: refs.Temporary, GenerationReference: refs.Generation, Image: candidate.Image, BuildLockDigest: candidate.BuildLockDigest, StoreObjects: closure, Owner: candidate.Owner, Companion: candidate.Companion}, Cleanup: []deploy.CleanupItemV1{}}
				if effect == "retire-old" {
					pending.Old = record.Owner
					pending.OldReferences = []OwnedImageReferenceV1{{Reference: record.ImageReference, Image: record.Image}, *record.Companion}
				}
				if err := images.operation.WritePendingBuild(pending); err != nil {
					t.Fatal(err)
				}
				images.restart(t, dir)
				currentImages.operation = images.operation
			}
			before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
			retained := map[string]providers.RealizedImageV1{}
			for reference, image := range images.images {
				retained[reference] = image
			}
			if effect == "publish" {
				backend := currentImages.backend(store, dir)
				backend.newReferences = func(string, string) (EnvironmentImageReferences, error) {
					return fixedPublicationReferences(t, dir, 31), nil
				}
				_, err = publishBuild(t.Context(), images.operation, store, publicationInput(t, dir, lock), backend)
			} else {
				err = currentImages.recover(store, dir)
			}
			if err == nil || !strings.Contains(err.Error(), "validated ownership") {
				t.Fatalf("alias conflict accepted: %v", err)
			}
			if !reflect.DeepEqual(retained, images.images) || !reflect.DeepEqual(before, pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())) {
				t.Fatal("current consumer changed independent validated ownership")
			}
		})
	}
}
