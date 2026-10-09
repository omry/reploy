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

func validatedRetirementFixtureV1(t *testing.T) (string, providerstore.Store, deploy.BuildLockV1, deploy.ValidatedBuildV1, *validatedPublicationImagesV1) {
	t.Helper()
	dir, store, lock, inputs, images := validatedPublicationFixtureV1(t)
	if _, err := images.operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	record := validatedPublicationRecordV1(t, dir, lock, inputs, 21)
	if err := images.operation.CommitValidatedBuildV1(record); err != nil {
		t.Fatal(err)
	}
	images.retain(t, record, dir)
	return dir, store, lock, record, images
}

func TestProviderPreparationRetriesDiscardedPortableInventoryV1(t *testing.T) {
	for _, which := range []string{"primary", "companion"} {
		for _, after := range []bool{false, true} {
			for _, pruned := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/after=%v/pruned=%v", which, after, pruned), func(t *testing.T) {
					dir, store, lock, record, images := validatedRetirementFixtureV1(t)
					fault := record.ImageReference
					if which == "companion" {
						fault = record.Companion.Reference
					}
					independent := fixedPublicationReferences(t, dir, 81).Generation
					images.images[independent] = record.Image
					_, _, err := discardValidatedBuildWithBackendV1(t.Context(), images.operation, "demo", dir, validatedRetirementImagesBackendV1(t, images, fault, after))
					if !errors.Is(err, errPendingPublicationFaultV1) {
						t.Fatalf("partial discard = %v", err)
					}
					if pruned {
						if err := images.operation.RemoveBuildLock(record.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil {
							t.Fatal(err)
						}
					}
					images.restart(t, dir)
					backend := validatedRetirementImagesBackendV1(t, images, fault, false)
					// Another transient failure must preserve the exact inventory,
					// including when the preceding remover applied its effect.
					state := deploy.StateV1{Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, publicationInput(t, dir, lock).Document), Platform: lock.Platform, Overlay: lock.Overlay}
					if err := images.operation.CommitStateV1(nil, state); err != nil {
						t.Fatal(err)
					}
					_, err = runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{
						Operation: images.operation, Store: store, DeploymentDir: dir,
						Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1001, GID: 1002},
					}, providerBuildRunBackend{
						prepare: func(ctx context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
							if input.ValidatedCandidate != nil {
								t.Fatal("discarded portable candidate was offered for reuse")
							}
							return LockedProviderBuildPreparationV1{}, retryOrdinaryPendingValidatedCleanupWithBackendV1(ctx, input.Operation, input.Store, input.Environment, input.DeploymentDir, backend)
						},
						execute: func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
							t.Fatal("partial retirement reached provider execution")
							return LockedProviderBuildExecutionResultV1{}, nil
						},
					})
					if !errors.Is(err, errPendingPublicationFaultV1) {
						t.Fatalf("discarded retry did not reach scoped retirement through runner: %v", err)
					}
					retained, found, err := images.operation.ReadValidatedBuildV1()
					if err != nil || !found || !retained.Discarded || !retained.PendingStorageCleanup || len(retained.PendingCleanup) != 1 || retained.PendingCleanup[0].ImageReference != fault {
						t.Fatalf("failed retry lost authority: found=%v err=%v record=%#v", found, err, retained)
					}
					images.restart(t, dir)
					if err := retryOrdinaryPendingValidatedCleanupWithBackendV1(t.Context(), images.operation, store, "demo", dir, validatedRetirementImagesBackendV1(t, images, "", false)); err != nil {
						t.Fatal(err)
					}
					retained, found, err = images.operation.ReadValidatedBuildV1()
					if err != nil || !found || !retained.Discarded || !retained.PendingStorageCleanup || retained.PendingCleanup != nil {
						t.Fatalf("successful retry did not record exact progress: %v %v %#v", found, err, retained)
					}
					if len(images.images) != 1 || images.images[independent] != record.Image {
						t.Fatalf("retry changed independent ownership or retained old aliases: %#v", images.images)
					}
					if err := requireValidatedPruningBoundaryV1(images.operation); err != nil {
						t.Fatalf("preparation remains blocked after reference retry: %v", err)
					}
					if _, err := deploy.BuildLockStoreClosure(lock, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
						t.Fatalf("reference retry pruned immutable objects: %v", err)
					}
				})
			}
		}
	}
}

func TestFailedProviderExecutionRetainsMissingTrialCacheV1(t *testing.T) {
	for _, noCache := range []bool{false, true} {
		t.Run(fmt.Sprintf("no-cache=%v", noCache), func(t *testing.T) {
			stubNoAbandonedBuildReferences(t)
			dir, operation, store, current, state := currentBuildFixture(t, true)
			defer operation.Unlock()
			document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
			if err != nil {
				t.Fatal(err)
			}
			inputs, err := ValidatedBuildInputs(document, state.Overlay, deploy.EmptyPackageOverridesV1("demo"), dir, state.Platform)
			if err != nil {
				t.Fatal(err)
			}
			trial := validatedBuildStorageVariant(t, store, current, "7", "8")
			trial.FinalImage.RootFSSubject = rendererDigest("c")
			trial.RuntimeLayer.Result.RootFSSubject = trial.FinalImage.RootFSSubject
			policy, err := deploy.RuntimePolicyDigestV1(trial.RuntimePolicy)
			if err != nil {
				t.Fatal(err)
			}
			trial.ValidationRecord, err = deploy.PublishPrefixValidation(t.Context(), store, deploy.PrefixValidationV1{Schema: deploy.PrefixValidationSchemaV1, SubjectRootFS: trial.FinalImage.RootFSSubject, Profiles: []providers.ValidationEvidence{}, RuntimePolicy: policy, ExposedOutputs: []providers.ExecutableEvidence{}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := operation.PublishBuildLock(trial, registry.ValidateRequirementProfileV1); err != nil {
				t.Fatal(err)
			}
			record := validatedPublicationRecordV1(t, dir, trial, inputs, 21)
			record.PendingStorageCleanup = true
			if err := operation.CommitValidatedBuildV1(record); err != nil {
				t.Fatal(err)
			}
			missing, err := store.ValidationRecordPath(trial.ValidationRecord)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(missing); err != nil {
				t.Fatal(err)
			}
			unrelated := validatedBuildStorageVariant(t, store, current, "9", "a")
			unrelatedDigest, err := operation.PublishBuildLock(unrelated, registry.ValidateRequirementProfileV1)
			if err != nil {
				t.Fatal(err)
			}
			workspace, err := store.NewWorkspace("failed-missing-trial-*")
			if err != nil {
				t.Fatal(err)
			}
			immutable, err := store.Publish(t.Context(), "failed.deb", "deb", strings.NewReader("preserve objects while trial closure is missing"))
			if err != nil {
				t.Fatal(err)
			}
			injected := errors.New("provider execution failed with missing trial cache")
			_, runErr := runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{Operation: operation, Store: store, DeploymentDir: dir, NoCache: noCache, Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1001, GID: 1002}}, providerBuildRunBackend{
				prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
					if input.ValidatedCandidate != nil {
						t.Fatal("missing trial cache was offered for reuse")
					}
					return LockedProviderBuildPreparationV1{Operation: operation, Store: store, Environment: "demo", DeploymentDir: dir, NoCache: noCache}, nil
				},
				execute: func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
					return LockedProviderBuildExecutionResultV1{}, injected
				},
			})
			if !errors.Is(runErr, injected) || runErr.Error() != injected.Error() {
				t.Fatalf("failed execution gained a cleanup error: %v", runErr)
			}
			retained, found, err := operation.ReadValidatedBuildV1()
			if err != nil || !found || !reflect.DeepEqual(retained, record) {
				t.Fatalf("missing trial cache revoked owner or cleared cleanup marker: %v %v %#v", found, err, retained)
			}
			for _, digest := range []canonical.Digest{record.BuildLockDigest, state.Current.BuildLockDigest} {
				if _, found, err := operation.ReadBuildLock(digest, registry.ValidateRequirementProfileV1); err != nil || !found {
					t.Fatalf("owner lock lost: %s %v %v", digest, found, err)
				}
			}
			if _, found, err := operation.ReadBuildLock(unrelatedDigest, registry.ValidateRequirementProfileV1); err != nil || found {
				t.Fatalf("unrelated lock cleanup was skipped: %v %v", found, err)
			}
			if _, err := os.Lstat(workspace); !os.IsNotExist(err) {
				t.Fatalf("temporary workspace retained: %v", err)
			}
			path, err := store.BlobPath(immutable.SHA256)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("unsafe immutable pruning: %v", err)
			}
			if _, err := deploy.BuildLockStoreClosure(current, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
				t.Fatalf("independent current closure lost: %v", err)
			}
		})
	}
}

func TestFailedProviderCleanupRejectsCorruptTrialCacheV1(t *testing.T) {
	stubNoAbandonedBuildReferences(t)
	dir, store, lock, record, images := validatedRetirementFixtureV1(t)
	record.PendingStorageCleanup = true
	if err := images.operation.CommitValidatedBuildV1(record); err != nil {
		t.Fatal(err)
	}
	path, err := store.ValidationRecordPath(lock.ValidationRecord)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("corrupt retained validation"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
	err = cleanupFailedProviderBuildV1(t.Context(), LockedProviderBuildPreparationV1{Operation: images.operation, Store: store, Environment: "demo", DeploymentDir: dir})
	if err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("corrupt trial closure was treated as recoverable absence: %v", err)
	}
	if !reflect.DeepEqual(before, pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())) {
		t.Fatal("corrupt trial cache permitted metadata or immutable-store pruning")
	}
}

// Only Docker effects are substituted. Metadata, canonical locks, store objects
// and restart use the real implementation, including the operation lock.
func validatedRetirementImagesBackendV1(t *testing.T, images *validatedPublicationImagesV1, faultReference string, after bool) validatedRetirementBackendV1 {
	t.Helper()
	remove := func(image providers.RealizedImageV1, reference string) error {
		if err := images.operation.RequireHeld(); err != nil {
			return err
		}
		if reference == faultReference && !after {
			return errPendingPublicationFaultV1
		}
		if actual, found := images.images[reference]; found && actual != image {
			return fmt.Errorf("retargeted reference %s", reference)
		}
		images.effects++
		delete(images.images, reference)
		if reference == faultReference && after {
			return errPendingPublicationFaultV1
		}
		return nil
	}
	return validatedRetirementBackendV1{
		removeReference: func(_ context.Context, image providers.RealizedImageV1, reference, environment, dir string) error {
			if err := ValidateEnvironmentGenerationReference(reference, environment, dir); err != nil {
				return err
			}
			return remove(image, reference)
		},
		removeCompanion: func(_ context.Context, op *deploy.OperationLock, pair OwnedImageReferenceV1, owner deploy.EnvironmentGenerationState, environment, dir string) error {
			if op != images.operation {
				t.Fatal("companion retirement used another operation lock")
			}
			if err := validatePortableOwnedReferenceV1(pair, owner, environment, dir); err != nil {
				return err
			}
			return remove(pair.Image, pair.Reference)
		},
	}
}

func TestValidatedRetirementRestartsAfterEachReferenceRemovalV1(t *testing.T) {
	for _, which := range []string{"primary", "companion"} {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/after=%v", which, after), func(t *testing.T) {
				dir, store, lock, record, images := validatedRetirementFixtureV1(t)
				fault := record.ImageReference
				if which == "companion" {
					fault = record.Companion.Reference
				}
				_, _, err := discardValidatedBuildWithBackendV1(t.Context(), images.operation, "demo", dir, validatedRetirementImagesBackendV1(t, images, fault, after))
				if !errors.Is(err, errPendingPublicationFaultV1) {
					t.Fatalf("retirement error=%v", err)
				}
				retained, found, err := images.operation.ReadValidatedBuildV1()
				want := record
				want.Discarded, want.PendingStorageCleanup = true, true
				pending := deploy.ValidatedBuildReferenceV1{Image: record.Image, ImageReference: fault}
				if which == "companion" {
					pending.Image, pending.CompanionOwner = record.Companion.Image, record.Owner
				}
				want.PendingCleanup = []deploy.ValidatedBuildReferenceV1{pending}
				if err != nil || !found || !reflect.DeepEqual(want, retained) {
					t.Fatalf("partial retirement erased durable inventory: %v %v %#v", found, err, retained)
				}
				if _, err := deploy.BuildLockStoreClosure(lock, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
					t.Fatal("partial retirement pruned live cache", err)
				}
				images.restart(t, dir)
				retired, found, err := discardValidatedBuildWithBackendV1(t.Context(), images.operation, "demo", dir, validatedRetirementImagesBackendV1(t, images, "", false))
				if err != nil || !found || !retired.Discarded || !retired.PendingStorageCleanup || len(images.images) != 0 {
					t.Fatalf("restart did not retire both exact aliases: %v %v %#v %#v", found, err, retired, images.images)
				}
				if err := cleanupValidatedBuildStorage(images.operation, store, nil); err != nil {
					t.Fatal(err)
				}
				images.restart(t, dir)
				// Storage pruning removed this lock. Exact persisted discard still
				// proves that both references were retired before that pruning.
				if _, found, err := images.operation.ReadBuildLock(record.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil || found {
					t.Fatalf("retired lock survived: %v %v", found, err)
				}
				if _, _, err := discardValidatedBuildWithBackendV1(t.Context(), images.operation, "demo", dir, validatedRetirementImagesBackendV1(t, images, "", false)); err != nil {
					t.Fatal("discard retry required pruned lock", err)
				}
				if _, found, err := RetryValidatedBuildCleanup(t.Context(), images.operation, store, "demo", dir); err != nil || found {
					t.Fatalf("storage retry did not finish discarded record: %v %v", found, err)
				}
			})
		}
	}
}

func TestValidatedDiscardGuardPersistenceFailuresPreserveInventoryV1(t *testing.T) {
	for _, failure := range []string{"unapplied-error", "success-without-write", "applied-error"} {
		t.Run(failure, func(t *testing.T) {
			dir, store, lock, record, images := validatedRetirementFixtureV1(t)
			before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
			aliases := map[string]providers.RealizedImageV1{
				record.ImageReference: record.Image, record.Companion.Reference: record.Companion.Image,
			}
			backend := validatedRetirementImagesBackendV1(t, images, "", false)
			backend.commitRecord = func(guard deploy.ValidatedBuildV1) error {
				switch failure {
				case "success-without-write":
					return nil
				case "applied-error":
					if err := images.operation.CommitValidatedBuildV1(guard); err != nil {
						t.Fatal(err)
					}
				}
				return errPendingPublicationFaultV1
			}
			if _, found, err := discardValidatedBuildWithBackendV1(t.Context(), images.operation, "demo", dir, backend); err == nil || !found {
				t.Fatalf("unconfirmed guard authorized discard: found=%v err=%v", found, err)
			}
			if images.effects != 0 || !reflect.DeepEqual(images.images, aliases) {
				t.Fatalf("guard publication failure mutated aliases: effects=%d images=%#v", images.effects, images.images)
			}
			retained, found, err := images.operation.ReadValidatedBuildV1()
			if err != nil || !found {
				t.Fatalf("guard publication lost authority: found=%v err=%v", found, err)
			}
			if failure == "applied-error" {
				if !retained.Discarded || !retained.PendingStorageCleanup || len(retained.PendingCleanup) != 2 {
					t.Fatalf("applied guard lost exact inventory: %#v", retained)
				}
				for _, pending := range retained.PendingCleanup {
					if image, ok := aliases[pending.ImageReference]; !ok || image != pending.Image {
						t.Fatalf("guard retained another image: %#v", pending)
					}
					if pending.ImageReference == record.Companion.Reference {
						if !reflect.DeepEqual(pending.CompanionOwner, record.Owner) {
							t.Fatal("guard lost companion scope")
						}
					} else if pending.CompanionOwner != nil {
						t.Fatal("guard changed primary ownership")
					}
				}
			} else {
				if !reflect.DeepEqual(retained, record) || !reflect.DeepEqual(before, pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())) {
					t.Fatal("unapplied guard changed durable authority")
				}
			}
			if _, err := deploy.BuildLockStoreClosure(lock, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
				t.Fatal("guard failure pruned storage", err)
			}
			images.restart(t, dir)
			backend = validatedRetirementImagesBackendV1(t, images, "", false)
			retired, found, err := discardValidatedBuildWithBackendV1(t.Context(), images.operation, "demo", dir, backend)
			if err != nil || !found || !retired.Discarded || len(retired.PendingCleanup) != 0 || len(images.images) != 0 {
				t.Fatalf("guard failure restart could not retire exact pair: found=%v err=%v record=%#v images=%#v", found, err, retired, images.images)
			}
		})
	}
}

func TestDiscardedPendingInventoryBlocksPruningAndRetainsRecoveryRootsV1(t *testing.T) {
	dir, store, _, record, images := validatedRetirementFixtureV1(t)
	if _, _, err := discardValidatedBuildWithBackendV1(t.Context(), images.operation, "demo", dir, validatedRetirementImagesBackendV1(t, images, record.ImageReference, true)); !errors.Is(err, errPendingPublicationFaultV1) {
		t.Fatalf("partial discard error=%v", err)
	}
	before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
	if err := requireValidatedPruningBoundaryV1(images.operation); err == nil {
		t.Fatal("discarded pending alias admitted pruning")
	}
	if err := cleanupValidatedBuildStorage(images.operation, store, nil); err == nil {
		t.Fatal("discarded pending alias allowed storage cleanup")
	}
	roots, digests, safe, err := pendingPublicationRootsV1(images.operation, store, nil, "", "demo", dir, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1)
	if err != nil || !safe || len(roots) != 1 || !reflect.DeepEqual(digests, []canonical.Digest{record.BuildLockDigest}) {
		t.Fatalf("recovery lost pending discard roots: root count=%d digests=%#v safe=%v err=%v", len(roots), digests, safe, err)
	}
	if digest, err := deploy.BuildLockDigestV1(roots[0], registry.ValidateRequirementProfileV1); err != nil || digest != record.BuildLockDigest {
		t.Fatalf("recovery selected another canonical root: digest=%s err=%v", digest, err)
	}
	if !reflect.DeepEqual(before, pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())) {
		t.Fatal("blocked pruning or recovery mutated retained state")
	}
	images.restart(t, dir)
	if _, _, err := discardValidatedBuildWithBackendV1(t.Context(), images.operation, "demo", dir, validatedRetirementImagesBackendV1(t, images, "", false)); err != nil {
		t.Fatal(err)
	}
	roots, digests, _, err = pendingPublicationRootsV1(images.operation, store, nil, "", "demo", dir, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1)
	if err != nil || len(roots) != 0 || len(digests) != 0 {
		t.Fatalf("completed retirement retained reusable roots: roots=%#v digests=%#v err=%v", roots, digests, err)
	}
}

func TestValidatedRetirementMissingAndRetargetedReferencesV1(t *testing.T) {
	for _, which := range []string{"primary", "companion"} {
		for _, retargeted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/retargeted=%v", which, retargeted), func(t *testing.T) {
				dir, store, lock, record, images := validatedRetirementFixtureV1(t)
				reference := record.ImageReference
				if which == "companion" {
					reference = record.Companion.Reference
				}
				foreign := providers.RealizedImageV1{Digest: rendererDigest("e"), ConfigDigest: rendererDigest("f"), RootFSSubject: rendererDigest("d")}
				if retargeted {
					images.images[reference] = foreign
				} else {
					delete(images.images, reference)
				}
				_, _, err := discardValidatedBuildWithBackendV1(t.Context(), images.operation, "demo", dir, validatedRetirementImagesBackendV1(t, images, "", false))
				if !retargeted {
					if err != nil || len(images.images) != 0 {
						t.Fatalf("missing alias was not idempotent: %v %#v", err, images.images)
					}
					return
				}
				if err == nil || images.images[reference] != foreign {
					t.Fatalf("retargeted alias was removed: %v %#v", err, images.images)
				}
				retained, found, readErr := images.operation.ReadValidatedBuildV1()
				want := record
				want.Discarded, want.PendingStorageCleanup = true, true
				pending := deploy.ValidatedBuildReferenceV1{Image: record.Image, ImageReference: reference}
				if which == "companion" {
					pending.Image, pending.CompanionOwner = record.Companion.Image, record.Owner
				}
				want.PendingCleanup = []deploy.ValidatedBuildReferenceV1{pending}
				if readErr != nil || !found || !reflect.DeepEqual(retained, want) {
					t.Fatalf("unproved owner inventory changed: %v %v", found, readErr)
				}
				if _, err := deploy.BuildLockStoreClosure(lock, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
					t.Fatal("unproved owner lost cache roots", err)
				}
			})
		}
	}
}

func TestValidatedSupersededRetirementUsesPairsAfterOldLockPruningV1(t *testing.T) {
	dir, store, lock, old, images := validatedRetirementFixtureV1(t)
	newLock := validatedBuildStorageVariant(t, store, lock, "7", "8")
	if _, err := images.operation.PublishBuildLock(newLock, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	inputs := ValidatedBuildInputsV1{BlueprintDigest: old.BlueprintDigest, OverlayDigest: old.OverlayDigest, PackageOverridesDigest: old.PackageOverridesDigest}
	record := validatedPublicationRecordV1(t, dir, newLock, inputs, 31)
	var err error
	record.PendingCleanup, err = pendingValidatedCleanupV1(&old, record, "demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := images.operation.CommitValidatedBuildV1(record); err != nil {
		t.Fatal(err)
	}
	images.retain(t, record, dir)
	if err := images.operation.RemoveBuildLock(old.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	backend := validatedRetirementImagesBackendV1(t, images, old.Companion.Reference, true)
	retained, cleanupErrors := cleanupPendingValidatedReferencesV1(t.Context(), images.operation, record, "demo", dir, backend)
	if len(cleanupErrors) != 1 || len(retained.PendingCleanup) != 1 || retained.PendingCleanup[0].CompanionOwner == nil {
		t.Fatalf("partial pair progress not durable: %#v %#v", retained, cleanupErrors)
	}
	images.restart(t, dir)
	retained, cleanupErrors = cleanupPendingValidatedReferencesV1(t.Context(), images.operation, retained, "demo", dir, validatedRetirementImagesBackendV1(t, images, "", false))
	if len(cleanupErrors) != 0 || len(retained.PendingCleanup) != 0 {
		t.Fatalf("cleanup required the pruned old lock: %#v %#v", retained, cleanupErrors)
	}
	for _, pair := range []OwnedImageReferenceV1{{Reference: record.ImageReference, Image: record.Image}, *record.Companion} {
		if images.images[pair.Reference] != pair.Image {
			t.Fatal("superseded retirement removed new owner", pair.Reference)
		}
	}
}

func TestValidatedPublicationRetainsInterruptedDiscardInventoryV1(t *testing.T) {
	for _, which := range []string{"primary", "companion"} {
		for _, after := range []bool{false, true} {
			for _, interruptedCommit := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/after=%v/interrupted-commit=%v", which, after, interruptedCommit), func(t *testing.T) {
					dir, store, lock, inputs, images := validatedPublicationFixtureV1(t)
					previous := validatedPublicationRecordV1(t, dir, lock, inputs, 21)
					if _, err := images.operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1); err != nil {
						t.Fatal(err)
					}
					if err := images.operation.CommitValidatedBuildV1(previous); err != nil {
						t.Fatal(err)
					}
					images.retain(t, previous, dir)
					fault := previous.ImageReference
					if which == "companion" {
						fault = previous.Companion.Reference
					}
					retained, found, err := discardValidatedBuildWithBackendV1(t.Context(), images.operation, "demo", dir, validatedRetirementImagesBackendV1(t, images, fault, after))
					if !errors.Is(err, errPendingPublicationFaultV1) || !found || !retained.Discarded || len(retained.PendingCleanup) != 1 {
						t.Fatalf("partial discard: found=%v err=%v record=%#v", found, err, retained)
					}
					remaining := retained.PendingCleanup
					images.restart(t, dir)
					if interruptedCommit {
						images.fault, images.after = "commit", true
					}
					_, err = publishValidatedBuild(t.Context(), images.operation, store, "demo", dir, lock, inputs, images.backend(dir))
					if interruptedCommit {
						if !errors.Is(err, errPendingPublicationFaultV1) {
							t.Fatalf("publication was not interrupted at commit: %v", err)
						}
						images.restart(t, dir)
						images.fault = ""
						if _, err := recoverPendingValidatedPublicationV1(t.Context(), images.operation, store, "demo", dir, images.backend(dir)); err != nil {
							t.Fatal(err)
						}
					} else if err != nil {
						t.Fatal(err)
					}
					selected, found, err := images.operation.ReadValidatedBuildV1()
					if err != nil || !found || selected.Discarded || !reflect.DeepEqual(selected.PendingCleanup, remaining) {
						t.Fatalf("replacement lost exact discard inventory: found=%v err=%v record=%#v", found, err, selected)
					}
					images.restart(t, dir)
					selected, cleanupErrors := cleanupPendingValidatedReferencesV1(t.Context(), images.operation, selected, "demo", dir, validatedRetirementImagesBackendV1(t, images, "", false))
					if len(cleanupErrors) != 0 || selected.PendingCleanup != nil || len(images.images) != 2 {
						t.Fatalf("retirement restart lost or resurrected ownership: errors=%v record=%#v images=%#v", cleanupErrors, selected, images.images)
					}
					if images.images[selected.ImageReference] != selected.Image || images.images[selected.Companion.Reference] != selected.Companion.Image {
						t.Fatal("cleanup removed the independently published replacement")
					}
					if _, err := deploy.BuildLockStoreClosure(lock, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
						t.Fatal("replacement closure was pruned", err)
					}
				})
			}
		}
	}
}

func TestValidatedDiscardCompletesAfterSupersededCleanupV1(t *testing.T) {
	dir, _, lock, previous, images := validatedRetirementFixtureV1(t)
	inputs := ValidatedBuildInputsV1{
		BlueprintDigest: previous.BlueprintDigest, OverlayDigest: previous.OverlayDigest,
		PackageOverridesDigest: previous.PackageOverridesDigest,
	}
	record := validatedPublicationRecordV1(t, dir, lock, inputs, 31)
	var err error
	record.PendingCleanup, err = pendingValidatedCleanupV1(&previous, record, "demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.PendingCleanup) != 2 {
		t.Fatalf("superseded ownership inventory = %#v", record.PendingCleanup)
	}
	if err := images.operation.CommitValidatedBuildV1(record); err != nil {
		t.Fatal(err)
	}
	images.retain(t, record, dir)
	retired, found, err := discardValidatedBuildWithBackendV1(
		t.Context(), images.operation, "demo", dir,
		validatedRetirementImagesBackendV1(t, images, "", false),
	)
	if err != nil || !found || !retired.Discarded || !retired.PendingStorageCleanup {
		t.Fatalf("successful superseded cleanup blocked active discard: found=%v err=%v record=%#v", found, err, retired)
	}
	persisted, found, err := images.operation.ReadValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(persisted, retired) || retired.PendingCleanup != nil {
		t.Fatalf("returned retirement differs from canonical persisted record: found=%v err=%v persisted=%#v returned=%#v", found, err, persisted, retired)
	}
	if len(images.images) != 0 {
		t.Fatalf("successful discard retained superseded or active aliases: %#v", images.images)
	}
}

func TestFailedProviderBuildPreservesCurrentValidatedUnionV1(t *testing.T) {
	for _, withCurrent := range []bool{false, true} {
		for _, sameImage := range []bool{false, true} {
			for _, noCache := range []bool{false, true} {
				t.Run(fmt.Sprintf("current=%v/same=%v/no-cache=%v", withCurrent, sameImage, noCache), func(t *testing.T) {
					stubNoAbandonedBuildReferences(t)
					dir, store, lock, record, images := validatedRetirementFixtureV1(t)
					if withCurrent {
						currentLock := lock
						if !sameImage {
							currentLock = validatedBuildStorageVariant(t, store, lock, "7", "8")
							if _, err := images.operation.PublishBuildLock(currentLock, registry.ValidateRequirementProfileV1); err != nil {
								t.Fatal(err)
							}
						}
						inputs := ValidatedBuildInputsV1{BlueprintDigest: record.BlueprintDigest, OverlayDigest: record.OverlayDigest, PackageOverridesDigest: record.PackageOverridesDigest}
						current := validatedPublicationRecordV1(t, dir, currentLock, inputs, 41)
						state := deploy.StateV1{Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, publicationInput(t, dir, lock).Document), Platform: lock.Platform, Overlay: lock.Overlay, Current: current.Owner}
						if err := images.operation.CommitStateV1(nil, state); err != nil {
							t.Fatal(err)
						}
						images.retain(t, current, dir)
					}
					refs := map[string]providers.RealizedImageV1{}
					for ref, image := range images.images {
						refs[ref] = image
					}
					dropped, err := store.Publish(t.Context(), "failed.deb", "deb", strings.NewReader("unreachable failed candidate"))
					if err != nil {
						t.Fatal(err)
					}
					if err := cleanupFailedProviderBuildV1(t.Context(), LockedProviderBuildPreparationV1{Operation: images.operation, Store: store, Environment: "demo", DeploymentDir: dir, NoCache: noCache}); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(refs, images.images) {
						t.Fatal("provider failure changed independently owned aliases")
					}
					for _, digest := range []canonical.Digest{record.BuildLockDigest, operationCurrentDigestIfFoundV1(t, images.operation)} {
						if digest == "" {
							continue
						}
						retained, found, err := images.operation.ReadBuildLock(digest, registry.ValidateRequirementProfileV1)
						if err != nil || !found {
							t.Fatalf("owned lock was pruned: %v %v", found, err)
						}
						if _, err := deploy.BuildLockStoreClosure(retained, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
							t.Fatal("owned store root was pruned", err)
						}
					}
					path, err := store.BlobPath(dropped.SHA256)
					if err != nil {
						t.Fatal(err)
					}
					_, err = os.Lstat(path)
					if noCache && err != nil || !noCache && !os.IsNotExist(err) {
						t.Fatalf("failed candidate pruning ignored no-cache=%v: %v", noCache, err)
					}
				})
			}
		}
	}
}

func TestOrdinaryNoCacheRunnerPreservesValidatedOwnerOnFailureV1(t *testing.T) {
	for _, portable := range []bool{false, true} {
		for _, withCurrent := range []bool{false, true} {
			for _, failure := range []string{"prepare", "execute"} {
				t.Run(fmt.Sprintf("portable=%v/current=%v/failure=%s", portable, withCurrent, failure), func(t *testing.T) {
					var (
						dir       string
						store     providerstore.Store
						lock      deploy.BuildLockV1
						record    deploy.ValidatedBuildV1
						images    *validatedPublicationImagesV1
						operation *deploy.OperationLock
					)
					if portable {
						dir, store, lock, record, images = validatedRetirementFixtureV1(t)
						operation = images.operation
					} else {
						var state deploy.StateV1
						dir, operation, store, lock, state = currentBuildFixture(t, true)
						document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
						if err != nil {
							t.Fatal(err)
						}
						overrides := deploy.EmptyPackageOverridesV1(document.Environment.ID)
						inputs, err := ValidatedBuildInputs(document, state.Overlay, overrides, dir, state.Platform)
						if err != nil {
							t.Fatal(err)
						}
						record = validatedPublicationRecordV1(t, dir, lock, inputs, 21)
						if _, err := operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1); err != nil {
							t.Fatal(err)
						}
						if err := operation.CommitValidatedBuildV1(record); err != nil {
							t.Fatal(err)
						}
						images = &validatedPublicationImagesV1{pendingPublicationImagesV1: pendingPublicationImagesV1{
							images: map[string]providers.RealizedImageV1{}, operation: operation, t: t,
						}}
						images.retain(t, record, dir)
						t.Cleanup(func() { _ = operation.Unlock() })
					}

					document := publicationInput(t, dir, lock).Document
					payload, err := blueprint.EncodeResolvedDocumentV1(document)
					if err != nil {
						t.Fatal(err)
					}
					previousState, previousFound, err := operation.ReadStateV1()
					if err != nil {
						t.Fatalf("read initial state: found=%v error=%v", previousFound, err)
					}
					state := deploy.StateV1{
						Schema: deploy.StateSchemaV1, Blueprint: payload,
						Platform: lock.Platform, Overlay: lock.Overlay,
					}
					var roots []deploy.BuildLockV1
					roots = append(roots, lock)
					if withCurrent {
						currentLock := validatedBuildStorageVariant(t, store, lock, "7", "8")
						if _, err := operation.PublishBuildLock(currentLock, registry.ValidateRequirementProfileV1); err != nil {
							t.Fatal(err)
						}
						currentRecord := validatedPublicationRecordV1(t, dir, currentLock, ValidatedBuildInputsV1{
							BlueprintDigest: record.BlueprintDigest, OverlayDigest: record.OverlayDigest,
							PackageOverridesDigest: record.PackageOverridesDigest,
						}, 41)
						current, err := validatedPublicationOwnerV1(currentRecord, currentLock)
						if err != nil {
							t.Fatal(err)
						}
						state.Current = &current
						pairs, err := projectEnvironmentOwnedReferencesV1(current, currentLock, "demo", dir, registry.ValidateRequirementProfileV1)
						if err != nil {
							t.Fatal(err)
						}
						for _, pair := range pairs {
							images.images[pair.Reference] = pair.Image
						}
						roots = append(roots, currentLock)
					}
					var previousCurrent *deploy.EnvironmentGenerationState
					if previousFound {
						previousCurrent = previousState.Current
					}
					if err := operation.CommitStateV1(previousCurrent, state); err != nil {
						t.Fatal(err)
					}
					beforeAliases := make(map[string]providers.RealizedImageV1, len(images.images))
					for reference, image := range images.images {
						beforeAliases[reference] = image
					}
					beforeRecord, found, err := operation.ReadValidatedBuildV1()
					if err != nil || !found || !reflect.DeepEqual(beforeRecord, record) {
						t.Fatalf("initial validated owner = found %v, error %v, record %#v", found, err, beforeRecord)
					}

					discardCalls := 0
					injected := fmt.Errorf("injected %s failure", failure)
					backend := providerBuildRunBackend{
						discardValidated: func(ctx context.Context, op *deploy.OperationLock, environment, deploymentDir string) error {
							discardCalls++
							if _, err := RecoverPendingValidatedPublicationV1(ctx, op, store, environment, deploymentDir); err != nil {
								return err
							}
							_, found, err := discardValidatedBuildWithBackendV1(ctx, op, environment, deploymentDir, validatedRetirementImagesBackendV1(t, images, "", false))
							if err != nil || !found {
								return errors.Join(fmt.Errorf("discard did not retire the recorded owner, found=%v", found), err)
							}
							if err := cleanupValidatedBuildStorage(op, store, nil); err != nil {
								return err
							}
							return op.RemoveValidatedBuildV1()
						},
						prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
							if failure == "prepare" {
								return LockedProviderBuildPreparationV1{}, injected
							}
							return LockedProviderBuildPreparationV1{
								Operation: input.Operation, Store: input.Store, Environment: input.Environment,
								DeploymentDir: input.DeploymentDir, NoCache: input.NoCache,
							}, nil
						},
						execute: func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
							if failure == "execute" {
								return LockedProviderBuildExecutionResultV1{}, injected
							}
							return LockedProviderBuildExecutionResultV1{}, nil
						},
					}
					_, err = runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{
						Operation: operation, Store: store, DeploymentDir: dir, NoCache: true,
						Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1001, GID: 1002},
					}, backend)
					if !errors.Is(err, injected) {
						t.Fatalf("runner error = %v, want %v", err, injected)
					}
					if discardCalls != 0 {
						t.Fatalf("automatic retirement ran before failed publication: %d calls", discardCalls)
					}
					afterRecord, found, err := operation.ReadValidatedBuildV1()
					if err != nil || !found || !reflect.DeepEqual(afterRecord, beforeRecord) {
						t.Fatalf("failed build changed validated owner: found=%v error=%v record=%#v", found, err, afterRecord)
					}
					if !reflect.DeepEqual(images.images, beforeAliases) {
						t.Fatalf("failed build changed exact aliases: before=%#v after=%#v", beforeAliases, images.images)
					}
					for _, root := range roots {
						digest, err := deploy.BuildLockDigestV1(root, registry.ValidateRequirementProfileV1)
						if err != nil {
							t.Fatal(err)
						}
						stored, found, err := operation.ReadBuildLock(digest, registry.ValidateRequirementProfileV1)
						if err != nil || !found {
							t.Fatalf("failed build removed owner lock %s: found=%v error=%v", digest, found, err)
						}
						if _, err := deploy.BuildLockStoreClosure(stored, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
							t.Fatalf("failed build removed owner store closure %s: %v", digest, err)
						}
					}
				})
			}
		}
	}
}

func operationCurrentDigestIfFoundV1(t *testing.T, operation *deploy.OperationLock) canonical.Digest {
	t.Helper()
	state, found, err := operation.ReadStateV1()
	if err != nil {
		t.Fatal(err)
	}
	if !found || state.Current == nil {
		return ""
	}
	return state.Current.BuildLockDigest
}

func TestValidatedRetirementPreservesCurrentOwnerOfSameImageV1(t *testing.T) {
	dir, store, lock, record, images := validatedRetirementFixtureV1(t)
	inputs := ValidatedBuildInputsV1{BlueprintDigest: record.BlueprintDigest, OverlayDigest: record.OverlayDigest, PackageOverridesDigest: record.PackageOverridesDigest}
	current := validatedPublicationRecordV1(t, dir, lock, inputs, 41)
	state := deploy.StateV1{Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, publicationInput(t, dir, lock).Document), Platform: lock.Platform, Overlay: lock.Overlay, Current: current.Owner}
	if err := images.operation.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	images.retain(t, current, dir)
	if _, _, err := discardValidatedBuildWithBackendV1(t.Context(), images.operation, "demo", dir, validatedRetirementImagesBackendV1(t, images, "", false)); err != nil {
		t.Fatal(err)
	}
	if _, found, err := RetryValidatedBuildCleanup(t.Context(), images.operation, store, "demo", dir); err != nil || found {
		t.Fatalf("retirement storage cleanup failed: %v %v", found, err)
	}
	if len(images.images) != 2 || images.images[current.ImageReference] != current.Image || images.images[current.Companion.Reference] != current.Companion.Image {
		t.Fatalf("same-image current aliases were not preserved: %#v", images.images)
	}
	if _, found, err := images.operation.ReadBuildLock(current.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil || !found {
		t.Fatalf("shared current lock was removed: %v %v", found, err)
	}
	if _, err := deploy.BuildLockStoreClosure(lock, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
		t.Fatal("current cache closure was removed", err)
	}
}

func TestValidatedRetirementPreservesInterruptedPromotionRootsV1(t *testing.T) {
	dir, store, lock, record, images := validatedRetirementFixtureV1(t)
	promotion := &pendingPublicationImagesV1{images: images.images, operation: images.operation, t: t, sequence: 41, fault: "commit", after: true}
	if _, err := publishBuild(t.Context(), images.operation, store, publicationInput(t, dir, lock), promotion.backend(store, dir)); !errors.Is(err, errPendingPublicationFaultV1) {
		t.Fatalf("expected interrupted committed promotion: %v", err)
	}
	before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
	if err := cleanupValidatedBuildStorage(images.operation, store, nil); err == nil || !strings.Contains(err.Error(), "current publication requires recovery") {
		t.Fatalf("storage pruned before current publication recovery: %v", err)
	}
	if !reflect.DeepEqual(before, pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())) {
		t.Fatal("pending-promotion rejection changed authoritative files")
	}
	if _, _, err := discardValidatedBuildWithBackendV1(t.Context(), images.operation, "demo", dir, validatedRetirementImagesBackendV1(t, images, "", false)); err != nil {
		t.Fatal("independent validated retirement failed", err)
	}
	images.restart(t, dir)
	promotion.operation = images.operation
	promotion.fault = ""
	if err := promotion.recover(store, dir); err != nil {
		t.Fatal(err)
	}
	if _, found, err := RetryValidatedBuildCleanup(t.Context(), images.operation, store, "demo", dir); err != nil || found {
		t.Fatalf("retirement retry failed after promotion recovery: %v %v", found, err)
	}
	state, found, err := images.operation.ReadStateV1()
	if err != nil || !found || state.Current == nil || state.Current.Reference == record.ImageReference {
		t.Fatalf("promotion did not retain independent destination: %v %v %#v", found, err, state)
	}
	pairs, err := ProjectEnvironmentOwnedReferencesV1(*state.Current, lock, "demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range pairs {
		if images.images[pair.Reference] != pair.Image {
			t.Fatal("validated retirement removed promoted ownership", pair.Reference)
		}
	}
	if _, err := deploy.BuildLockStoreClosure(lock, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
		t.Fatal("promotion roots were pruned", err)
	}
}

func TestProviderBuildAdmissionRecoversPendingValidatedBeforeConsumersV1(t *testing.T) {
	for _, committed := range []bool{false, true} {
		for _, noCache := range []bool{false, true} {
			t.Run(fmt.Sprintf("committed=%v/no-cache=%v", committed, noCache), func(t *testing.T) {
				dir, store, lock, inputs, images := validatedPublicationFixtureV1(t)
				document := publicationInput(t, dir, lock).Document
				state := deploy.StateV1{Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, document), Platform: lock.Platform, Overlay: lock.Overlay}
				if err := images.operation.CommitStateV1(nil, state); err != nil {
					t.Fatal(err)
				}
				images.fault, images.after = "create-primary", true
				if committed {
					images.fault = "commit"
				}
				if _, err := publishValidatedBuild(t.Context(), images.operation, store, "demo", dir, lock, inputs, images.backend(dir)); !errors.Is(err, errPendingPublicationFaultV1) {
					t.Fatalf("publication was not interrupted: %v", err)
				}
				images.restart(t, dir)
				images.fault = ""
				recoveryCalls, preparationCalls := 0, 0
				stop := errors.New("stop before new provider work")
				_, err := runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{Operation: images.operation, Store: store, DeploymentDir: dir, NoCache: noCache, ValidateChoices: true, Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1001, GID: 1002}}, providerBuildRunBackend{
					recoverValidated: func(ctx context.Context, op *deploy.OperationLock, actualStore providerstore.Store, environment, dir string) (bool, error) {
						recoveryCalls++
						return recoverPendingValidatedPublicationV1(ctx, op, actualStore, environment, dir, images.backend(dir))
					},
					prepare: func(context.Context, LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
						preparationCalls++
						if _, pending, err := images.operation.ReadPendingValidatedBuildV1(); err != nil || pending {
							t.Fatalf("provider effects preceded recovery: %v %v", pending, err)
						}
						return LockedProviderBuildPreparationV1{}, stop
					},
					execute: func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
						t.Fatal("unexpected provider execution")
						return LockedProviderBuildExecutionResultV1{}, nil
					},
				})
				if recoveryCalls != 1 {
					t.Fatalf("recovery calls=%d error=%v", recoveryCalls, err)
				}
				// Completed owners can now reach preparation. Any actual reuse
				// must pass the exact paired ownership and optional content audit.
				if preparationCalls != 1 || !errors.Is(err, stop) {
					t.Fatalf("provider admission did not reach recovered state: %d %v", preparationCalls, err)
				}
				if _, pending, err := images.operation.ReadPendingValidatedBuildV1(); err != nil || pending {
					t.Fatalf("validated intent was not recovered: %v %v", pending, err)
				}
				_, found, err := images.operation.ReadValidatedBuildV1()
				if err != nil || found != committed || committed && len(images.images) != 2 || !committed && len(images.images) != 0 {
					t.Fatalf("wrong post-recovery ownership: %v %v %#v", found, err, images.images)
				}
			})
		}
	}
}

func TestProviderBuildPreparationRetriesOrdinaryValidatedCleanupBeforePruningV1(t *testing.T) {
	for _, noCache := range []bool{false, true} {
		t.Run(fmt.Sprintf("no-cache=%v", noCache), func(t *testing.T) {
			dir := t.TempDir()
			store, lock := publicationLockFixture(t, dir, "4", "5", "6")
			operation, err := deploy.AcquireOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = operation.Unlock() })
			if _, err := operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1); err != nil {
				t.Fatal(err)
			}
			document, _ := testSelectedPlatformDocumentV1(t)
			state := deploy.StateV1{
				Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, document),
				Platform: lock.Platform, Overlay: lock.Overlay,
			}
			if err := operation.CommitStateV1(nil, state); err != nil {
				t.Fatal(err)
			}
			overrides := deploy.EmptyPackageOverridesV1(document.Environment.ID)
			inputs, err := ValidatedBuildInputs(document, state.Overlay, overrides, dir, state.Platform)
			if err != nil {
				t.Fatal(err)
			}
			previous := validatedPublicationRecordV1(t, dir, lock, inputs, 31)
			current := validatedPublicationRecordV1(t, dir, lock, inputs, 41)
			current.PendingCleanup = []deploy.ValidatedBuildReferenceV1{{
				Image: previous.Image, ImageReference: previous.ImageReference,
			}}
			if err := operation.CommitValidatedBuildV1(current); err != nil {
				t.Fatal(err)
			}
			images := &validatedPublicationImagesV1{pendingPublicationImagesV1: pendingPublicationImagesV1{
				images: map[string]providers.RealizedImageV1{}, operation: operation, t: t,
			}}
			images.retain(t, current, dir)
			images.images[previous.ImageReference] = previous.Image

			stopAfterRecovery := errors.New("stop after pruning admission")
			failReference := previous.ImageReference
			var order []string
			backend := providerBuildPreparationBackend{
				retryValidatedCleanup: func(ctx context.Context, op *deploy.OperationLock, actualStore providerstore.Store, environment, deploymentDir string) error {
					order = append(order, "retry-cleanup")
					if op != operation || actualStore.Root() != store.Root() || environment != "demo" || deploymentDir != dir {
						t.Fatalf("cleanup retry identity changed: op=%p store=%s environment=%q dir=%q", op, actualStore.Root(), environment, deploymentDir)
					}
					if err := op.RequireHeld(); err != nil {
						return err
					}
					persisted, found, err := op.ReadValidatedBuildV1()
					if err != nil || !found || !reflect.DeepEqual(persisted, current) {
						return fmt.Errorf("retry did not receive canonical committed cleanup inventory: found=%v err=%v record=%#v", found, err, persisted)
					}
					return retryOrdinaryPendingValidatedCleanupWithBackendV1(
						ctx, op, actualStore, environment, deploymentDir,
						validatedRetirementImagesBackendV1(t, images, failReference, false),
					)
				},
				recover: func(_ context.Context, op *deploy.OperationLock, _ providerstore.Store, _ *deploy.EnvironmentGenerationState, _, _ string, _ providers.RequirementProfileOwnerValidator, _ providers.ResolvedBundleOwnerValidator) (bool, error) {
					order = append(order, "recover")
					if err := requireValidatedPruningBoundaryV1(op); err != nil {
						return false, err
					}
					return false, nil
				},
				load: func(*deploy.OperationLock, deploy.PackageOverrideIntentV1, string, []providers.ResolvedSourceInput) (LoadedBuildRequestV1, error) {
					order = append(order, "load")
					persisted, found, err := operation.ReadValidatedBuildV1()
					if err != nil || !found || len(persisted.PendingCleanup) != 0 {
						t.Fatalf("provider request load preceded exact cleanup: found=%v err=%v record=%#v", found, err, persisted)
					}
					if _, found := images.images[previous.ImageReference]; found {
						t.Fatalf("superseded reference remains after cleanup: %s", previous.ImageReference)
					}
					if images.images[current.ImageReference] != current.Image {
						t.Fatal("current validated reference was removed during cleanup")
					}
					return LoadedBuildRequestV1{}, stopAfterRecovery
				},
				loadVerifier: func(blueprint.Platform) (deploy.ApplicationStartupVerifierV1, error) {
					return deploy.ApplicationStartupVerifierV1{}, nil
				},
				selectCachedBase: func(context.Context, providers.ResolvedRequestV1, deploy.ImageDescriptor) (SelectedProviderBase, bool, error) {
					return SelectedProviderBase{}, false, nil
				},
				selectBase: func(context.Context, providers.ResolvedRequestV1) (SelectedProviderBase, error) {
					return SelectedProviderBase{}, nil
				},
				validateCurrent: func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
					return CurrentBuild{}, false, nil
				},
				lockedSources: func(deploy.BuildLockV1) ([]providers.ResolvedSourceInput, error) {
					return nil, nil
				},
				matches: func(CurrentBuild, CurrentBuildReuseInput) (bool, error) {
					return false, nil
				},
				cacheAvailable: func(deploy.BuildLockV1, providerstore.Store) (bool, error) {
					return false, nil
				},
				realizeBase: func(context.Context, providerstore.Store, SelectedProviderBase) (PreparedProviderBase, error) {
					return PreparedProviderBase{}, nil
				},
			}
			prepare := func() error {
				_, err := prepareLockedProviderBuildV1(t.Context(), LockedProviderBuildPreparationInputV1{
					Operation: operation, Store: store, Environment: "demo", DeploymentDir: dir,
					Sources: []providers.ResolvedSourceInput{}, LocalOverrides: []PythonLocalOverrideV1{}, NoCache: noCache,
				}, backend)
				return err
			}
			err = prepare()
			if !errors.Is(err, errPendingPublicationFaultV1) {
				t.Fatalf("transient cleanup failure did not stop preparation safely: order=%v err=%v", order, err)
			}
			if !reflect.DeepEqual(order, []string{"retry-cleanup"}) {
				t.Fatalf("first preparation recovery order = %v", order)
			}
			retained, found, readErr := operation.ReadValidatedBuildV1()
			if readErr != nil || !found || !reflect.DeepEqual(retained, current) {
				t.Fatalf("transient cleanup failure changed canonical inventory: found=%v err=%v record=%#v", found, readErr, retained)
			}
			if images.images[previous.ImageReference] != previous.Image || images.images[current.ImageReference] != current.Image {
				t.Fatalf("transient cleanup failure changed exact owners: %#v", images.images)
			}
			if _, err := deploy.BuildLockStoreClosure(lock, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
				t.Fatalf("transient cleanup failure pruned validated candidate roots: %v", err)
			}

			failReference = ""
			order = nil
			err = prepare()
			if !errors.Is(err, stopAfterRecovery) {
				t.Fatalf("next preparation did not pass pruning admission after retry: order=%v err=%v", order, err)
			}
			if !reflect.DeepEqual(order, []string{"retry-cleanup", "recover", "load"}) {
				t.Fatalf("next preparation recovery order = %v", order)
			}
			if err := requireValidatedPruningBoundaryV1(operation); err != nil {
				t.Fatalf("cleanup inventory remains after successful retry: %v", err)
			}
			if _, err := deploy.BuildLockStoreClosure(lock, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
				t.Fatalf("validated candidate store roots were pruned: %v", err)
			}
		})
	}
}

func TestProviderBuildPreparationCleanupFailurePreservesOwnersV1(t *testing.T) {
	for _, failure := range []string{"retargeted-reference", "missing-authority", "current-overlap", "pending-overlap"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			store, lock := publicationLockFixture(t, dir, "4", "5", "6")
			operation, err := deploy.AcquireOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = operation.Unlock() })
			if _, err := operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1); err != nil {
				t.Fatal(err)
			}
			document, _ := testSelectedPlatformDocumentV1(t)
			state := deploy.StateV1{
				Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, document),
				Platform: lock.Platform, Overlay: lock.Overlay,
			}
			if err := operation.CommitStateV1(nil, state); err != nil {
				t.Fatal(err)
			}
			inputs, err := ValidatedBuildInputs(document, state.Overlay, deploy.EmptyPackageOverridesV1(document.Environment.ID), dir, state.Platform)
			if err != nil {
				t.Fatal(err)
			}
			previous := validatedPublicationRecordV1(t, dir, lock, inputs, 31)
			current := validatedPublicationRecordV1(t, dir, lock, inputs, 41)
			current.PendingCleanup = []deploy.ValidatedBuildReferenceV1{{Image: previous.Image, ImageReference: previous.ImageReference}}
			if err := operation.CommitValidatedBuildV1(current); err != nil {
				t.Fatal(err)
			}
			images := &validatedPublicationImagesV1{pendingPublicationImagesV1: pendingPublicationImagesV1{
				images: map[string]providers.RealizedImageV1{}, operation: operation, t: t,
			}}
			images.retain(t, current, dir)
			images.images[previous.ImageReference] = previous.Image
			switch failure {
			case "retargeted-reference":
				images.images[previous.ImageReference] = providers.RealizedImageV1{
					Digest: rendererDigest("a"), ConfigDigest: rendererDigest("b"), RootFSSubject: rendererDigest("c"),
				}
			case "missing-authority":
				if err := operation.RemoveBuildLock(current.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil {
					t.Fatal(err)
				}
			case "current-overlap":
				policy, err := deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
				if err != nil {
					t.Fatal(err)
				}
				state.Current = &deploy.EnvironmentGenerationState{
					Reference: previous.ImageReference, ImageDigest: previous.Image.Digest,
					RootFSSubject: previous.Image.RootFSSubject, BuildLockDigest: current.BuildLockDigest,
					Platform: current.Platform, RuntimePolicyDigest: policy,
				}
				if err := operation.CommitStateV1(nil, state); err != nil {
					t.Fatal(err)
				}
			case "pending-overlap":
				references := fixedPublicationReferences(t, dir, 77)
				pending := deploy.PendingBuildV1{
					Schema: deploy.PendingBuildSchemaV1, Phase: deploy.PendingBuildPhaseValidated,
					Candidate: deploy.PendingCandidateV1{
						TemporaryReference: references.Temporary, GenerationReference: previous.ImageReference,
						Image: current.Image, BuildLockDigest: current.BuildLockDigest,
						StoreObjects: []providerstore.StoreObjectRef{},
					},
					Cleanup: []deploy.CleanupItemV1{},
				}
				if err := operation.WritePendingBuild(pending); err != nil {
					t.Fatal(err)
				}
			}
			before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
			beforeImages := make(map[string]providers.RealizedImageV1, len(images.images))
			for reference, image := range images.images {
				beforeImages[reference] = image
			}
			recoverCalls := 0
			backend := providerBuildPreparationBackend{
				retryValidatedCleanup: func(ctx context.Context, op *deploy.OperationLock, actualStore providerstore.Store, environment, deploymentDir string) error {
					return retryOrdinaryPendingValidatedCleanupWithBackendV1(
						ctx, op, actualStore, environment, deploymentDir,
						validatedRetirementImagesBackendV1(t, images, "", false),
					)
				},
				recover: func(_ context.Context, op *deploy.OperationLock, _ providerstore.Store, _ *deploy.EnvironmentGenerationState, _, _ string, _ providers.RequirementProfileOwnerValidator, _ providers.ResolvedBundleOwnerValidator) (bool, error) {
					recoverCalls++
					return false, requireValidatedPruningBoundaryV1(op)
				},
				load: func(*deploy.OperationLock, deploy.PackageOverrideIntentV1, string, []providers.ResolvedSourceInput) (LoadedBuildRequestV1, error) {
					return LoadedBuildRequestV1{}, errors.New("unexpected provider request load")
				},
				loadVerifier: func(blueprint.Platform) (deploy.ApplicationStartupVerifierV1, error) {
					return deploy.ApplicationStartupVerifierV1{}, nil
				},
				selectCachedBase: func(context.Context, providers.ResolvedRequestV1, deploy.ImageDescriptor) (SelectedProviderBase, bool, error) {
					return SelectedProviderBase{}, false, nil
				},
				selectBase: func(context.Context, providers.ResolvedRequestV1) (SelectedProviderBase, error) {
					return SelectedProviderBase{}, nil
				},
				validateCurrent: func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
					return CurrentBuild{}, false, nil
				},
				lockedSources: func(deploy.BuildLockV1) ([]providers.ResolvedSourceInput, error) {
					return nil, nil
				},
				matches: func(CurrentBuild, CurrentBuildReuseInput) (bool, error) {
					return false, nil
				},
				cacheAvailable: func(deploy.BuildLockV1, providerstore.Store) (bool, error) {
					return false, nil
				},
				realizeBase: func(context.Context, providerstore.Store, SelectedProviderBase) (PreparedProviderBase, error) {
					return PreparedProviderBase{}, nil
				},
			}
			_, err = prepareLockedProviderBuildV1(t.Context(), LockedProviderBuildPreparationInputV1{
				Operation: operation, Store: store, Environment: "demo", DeploymentDir: dir,
				Sources: []providers.ResolvedSourceInput{}, LocalOverrides: []PythonLocalOverrideV1{}, NoCache: true,
			}, backend)
			if err == nil || recoverCalls != 0 {
				t.Fatalf("uncertain cleanup reached pruning recovery: recover=%d err=%v", recoverCalls, err)
			}
			if !reflect.DeepEqual(before, pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())) {
				t.Fatal("uncertain cleanup changed canonical files, locks, or provider-store roots")
			}
			if !reflect.DeepEqual(beforeImages, images.images) {
				t.Fatalf("uncertain cleanup changed reference owners: before=%#v after=%#v", beforeImages, images.images)
			}
			retained, found, err := operation.ReadValidatedBuildV1()
			if err != nil || !found || !reflect.DeepEqual(retained, current) {
				t.Fatalf("uncertain cleanup changed canonical inventory: found=%v err=%v record=%#v", found, err, retained)
			}
		})
	}
}

func TestProviderBuildCleanupRetryRetiresSupersededPortablePairV1(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure=%v", fail), func(t *testing.T) {
			dir, store, lock, inputs, images := validatedPublicationFixtureV1(t)
			if _, err := images.operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1); err != nil {
				t.Fatal(err)
			}
			previous := validatedPublicationRecordV1(t, dir, lock, inputs, 31)
			current := validatedPublicationRecordV1(t, dir, lock, inputs, 41)
			pending, err := pendingValidatedCleanupV1(&previous, current, "demo", dir)
			if err != nil {
				t.Fatal(err)
			}
			current.PendingCleanup = pending
			if err := images.operation.CommitValidatedBuildV1(current); err != nil {
				t.Fatal(err)
			}
			images.retain(t, previous, dir)
			images.retain(t, current, dir)
			fault := ""
			if fail {
				fault = previous.Companion.Reference
			}
			err = retryOrdinaryPendingValidatedCleanupWithBackendV1(t.Context(), images.operation, store, "demo", dir, validatedRetirementImagesBackendV1(t, images, fault, false))
			if fail && !errors.Is(err, errPendingPublicationFaultV1) || !fail && err != nil {
				t.Fatalf("cleanup retry = %v", err)
			}
			retained, found, err := images.operation.ReadValidatedBuildV1()
			if err != nil || !found {
				t.Fatalf("retained candidate found=%v err=%v", found, err)
			}
			expected := current
			expected.PendingCleanup = nil
			if fail {
				for _, pair := range pending {
					if pair.ImageReference == fault {
						expected.PendingCleanup = append(expected.PendingCleanup, pair)
					}
				}
			}
			if !reflect.DeepEqual(retained, expected) {
				t.Fatalf("retained progress = %#v, want %#v", retained, expected)
			}
			if images.images[current.ImageReference] != current.Image || images.images[current.Companion.Reference] != current.Companion.Image {
				t.Fatal("retry changed live candidate pair")
			}
			if _, found := images.images[previous.ImageReference]; found {
				t.Fatal("retry did not remove superseded primary")
			}
			if fail {
				if images.images[fault] != previous.Companion.Image {
					t.Fatal("failed companion owner was lost")
				}
				if err := retryOrdinaryPendingValidatedCleanupWithBackendV1(t.Context(), images.operation, store, "demo", dir, validatedRetirementImagesBackendV1(t, images, "", false)); err != nil {
					t.Fatal(err)
				}
			}
			retained, found, err = images.operation.ReadValidatedBuildV1()
			if err != nil || !found || len(retained.PendingCleanup) != 0 || len(images.images) != 2 {
				t.Fatalf("completed retry found=%v err=%v record=%#v images=%#v", found, err, retained, images.images)
			}
			if _, err := deploy.BuildLockStoreClosure(lock, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
				t.Fatalf("retry pruned live closure: %v", err)
			}
		})
	}
}

func TestValidatedRetirementRejectsForeignStoreBeforeEffectsV1(t *testing.T) {
	dir, _, _, record, images := validatedRetirementFixtureV1(t)
	foreign, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	before := images.effects
	_, err = discardValidatedBuild(t.Context(), images.operation, foreign, "demo", dir, validatedRetirementImagesBackendV1(t, images, "", false).removeReference)
	if err == nil || images.effects != before {
		t.Fatalf("foreign store reached retirement: %v %d", err, images.effects)
	}
	retained, found, err := images.operation.ReadValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(retained, record) {
		t.Fatalf("foreign-store rejection changed owner: %v %v", found, err)
	}
}

func TestValidatedRetirementRetriesStorageAfterPortableLockPruningV1(t *testing.T) {
	dir, store, _, record, images := validatedRetirementFixtureV1(t)
	if _, _, err := discardValidatedBuildWithBackendV1(t.Context(), images.operation, "demo", dir, validatedRetirementImagesBackendV1(t, images, "", false)); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(dir, ".reploy", "locks", "unknown")
	if err := os.WriteFile(blocker, []byte("interrupt lock pruning"), 0o600); err != nil {
		t.Fatal(err)
	}
	retained, found, err := RetryValidatedBuildCleanup(t.Context(), images.operation, store, "demo", dir)
	if err != nil || !found || !retained.Discarded || !retained.PendingStorageCleanup || retained.Companion == nil {
		t.Fatalf("partial storage cleanup lost portable inventory: %v %v %#v", found, err, retained)
	}
	if err := images.operation.RemoveBuildLock(record.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	images.restart(t, dir)
	if _, found, err := RetryValidatedBuildCleanup(t.Context(), images.operation, store, "demo", dir); err != nil || found {
		t.Fatalf("portable storage retry required retired lock: %v %v", found, err)
	}
}

func TestFailedProviderBuildPreservesMissingValidatedAuthorityV1(t *testing.T) {
	stubNoAbandonedBuildReferences(t)
	dir, store, _, record, images := validatedRetirementFixtureV1(t)
	if err := images.operation.RemoveBuildLock(record.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewWorkspace("unproved-*"); err != nil {
		t.Fatal(err)
	}
	before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
	refs := map[string]providers.RealizedImageV1{}
	for ref, image := range images.images {
		refs[ref] = image
	}
	if err := cleanupFailedProviderBuildV1(t.Context(), LockedProviderBuildPreparationV1{Operation: images.operation, Store: store, Environment: "demo", DeploymentDir: dir}); err == nil {
		t.Fatal("missing live authority reached failed-build cleanup")
	}
	if !reflect.DeepEqual(before, pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())) || !reflect.DeepEqual(refs, images.images) {
		t.Fatal("missing authority did not preserve all unproved resources")
	}
}
