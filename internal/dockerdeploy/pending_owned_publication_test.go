package dockerdeploy

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

func pendingPortablePublicationFixtureV1(t *testing.T) (string, providerstore.Store, deploy.BuildLockV1) {
	t.Helper()
	f := newPreparedPythonGraphReuseFixture(t)
	lock := f.lock
	tools := buildLockAssemblyPortableToolsV1(t, f.store, f.request.Plan, f.request.NodeID)
	lock.PortableTools = &tools
	for _, node := range f.request.Plan.Nodes {
		if node.ID == "base" {
			lock.Base.AuthorReference, _ = node.Request.Value["image"].(string)
			var err error
			lock.BasePlanDigest, err = providers.ProviderNodePlanDigest(node)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	result := providers.RealizedImageV1{Digest: rendererDigest("c"), ConfigDigest: rendererDigest("c"), RootFSSubject: rendererDigest("d")}
	tx, err := deploy.PortableRuntimeLayerTransactionDigestV1(tools, lock.RuntimeLayer.Upstream, result)
	if err != nil {
		t.Fatal(err)
	}
	lock.PortableRuntimeLayer = &deploy.PortableRuntimeLayerV1{Schema: deploy.PortableRuntimeLayerSchemaV1, Upstream: lock.RuntimeLayer.Upstream, Result: result, TransactionDigest: tx}
	lock.RuntimeLayer.Upstream = result
	lock.RuntimeLayer.TransactionDigest, err = deploy.ApplicationRuntimeLayerTransactionDigestV1(lock.RuntimeLayer.Verifier, lock.RuntimeLayer.Account, result, lock.Platform)
	if err != nil {
		t.Fatal(err)
	}
	document, _ := testSelectedPlatformDocumentV1(t)
	lock.BlueprintDigest = testResolvedBlueprintDigestV1(t, document)
	policy, err := deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	lock.ValidationRecord, err = deploy.PublishPrefixValidation(t.Context(), f.store, deploy.PrefixValidationV1{Schema: deploy.PrefixValidationSchemaV1, SubjectRootFS: lock.FinalImage.RootFSSubject, RuntimePolicy: policy, Profiles: []providers.ValidationEvidence{}, ExposedOutputs: []providers.ExecutableEvidence{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deploy.BuildLockStoreClosure(lock, f.store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Dir(f.store.Root())), f.store, lock
}

type pendingPublicationImagesV1 struct {
	images    map[string]providers.RealizedImageV1
	fault     string
	after     bool
	fired     bool
	sequence  byte
	operation *deploy.OperationLock
	t         *testing.T
}

var errPendingPublicationFaultV1 = errors.New("injected publication failure")

func (images *pendingPublicationImagesV1) effect(name string, action func() error) error {
	if images.fault == name && !images.fired && !images.after {
		images.fired = true
		return errPendingPublicationFaultV1
	}
	if err := action(); err != nil {
		return err
	}
	if images.fault == name && !images.fired && images.after {
		images.fired = true
		return errPendingPublicationFaultV1
	}
	return nil
}

func (images *pendingPublicationImagesV1) create(name, reference string, image providers.RealizedImageV1) error {
	if _, found, err := images.operation.ReadPendingBuild(); err != nil || !found {
		images.t.Fatalf("tag before durable intent: %v %v", found, err)
	}
	return images.effect(name, func() error {
		if previous, found := images.images[reference]; found && previous != image {
			return fmt.Errorf("retargeted image %s", reference)
		}
		images.images[reference] = image
		return nil
	})
}

func (images *pendingPublicationImagesV1) remove(name, reference string, image providers.RealizedImageV1) error {
	return images.effect(name, func() error {
		if previous, found := images.images[reference]; found && previous != image {
			return fmt.Errorf("retargeted image %s", reference)
		}
		delete(images.images, reference)
		return nil
	})
}

func (images *pendingPublicationImagesV1) primaryRemove(_ context.Context, image providers.RealizedImageV1, refs EnvironmentImageReferences, kind EnvironmentReferenceKind, _, _ string) error {
	reference := refs.Generation
	name := "remove-primary"
	if kind == EnvironmentReferenceTemporary {
		reference = refs.Temporary
		name = "remove-temporary"
	}
	return images.remove(name, reference, image)
}

func (images *pendingPublicationImagesV1) companionRemove(owner deploy.EnvironmentGenerationState, pair OwnedImageReferenceV1, environment, dir string) error {
	if err := validatePortableOwnedReferenceV1(pair, owner, environment, dir); err != nil {
		return err
	}
	return images.remove("remove-companion", pair.Reference, pair.Image)
}

func (images *pendingPublicationImagesV1) backend(store providerstore.Store, dir string) buildPublicationBackend {
	return buildPublicationBackend{
		newReferences: func(string, string) (EnvironmentImageReferences, error) {
			images.sequence++
			return fixedPublicationReferences(images.t, dir, images.sequence), nil
		},
		createReference: func(_ context.Context, image providers.RealizedImageV1, refs EnvironmentImageReferences, kind EnvironmentReferenceKind, _, _ string) error {
			reference := refs.Generation
			name := "create-primary"
			if kind == EnvironmentReferenceTemporary {
				reference = refs.Temporary
				name = "create-temporary"
			}
			return images.create(name, reference, image)
		},
		removeReference: images.primaryRemove,
		createCompanion: func(_ context.Context, operation *deploy.OperationLock, pair OwnedImageReferenceV1, owner deploy.EnvironmentGenerationState, environment, dir string) error {
			if operation != images.operation {
				images.t.Fatal("wrong companion operation lock")
			}
			if err := operation.RequireHeld(); err != nil {
				return err
			}
			if err := validatePortableOwnedReferenceV1(pair, owner, environment, dir); err != nil {
				return err
			}
			return images.create("create-companion", pair.Reference, pair.Image)
		},
		removeCompanion: func(_ context.Context, operation *deploy.OperationLock, pair OwnedImageReferenceV1, owner deploy.EnvironmentGenerationState, environment, dir string) error {
			if operation != images.operation {
				images.t.Fatal("wrong companion operation lock")
			}
			return images.companionRemove(owner, pair, environment, dir)
		},
		writeIntent: func(p deploy.PendingBuildV1) error {
			return images.effect("intent", func() error { return images.operation.WritePendingBuild(p) })
		},
		advancePhase: func(phase string) error {
			return images.effect("phase-"+phase, func() error { return images.operation.AdvancePendingBuildPhase(phase) })
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
		commitState: func(old *deploy.EnvironmentGenerationState, state deploy.StateV1) error {
			return images.effect("commit", func() error { return images.operation.CommitStateV1(old, state) })
		},
		pruneLocks: func(digests []canonical.Digest) error {
			return images.effect("prune-locks", func() error {
				return images.operation.RemoveBuildLocksExcept(digests, registry.ValidateRequirementProfileV1)
			})
		},
		pruneStore: func(roots []deploy.BuildLockV1) error {
			return images.effect("prune-store", func() error {
				return images.operation.RemoveUnreachableBuildObjectsForBuilds(store, roots, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1)
			})
		},
		removeIntent: func() error { return images.effect("remove-intent", images.operation.RemovePendingBuild) },
	}
}

func (images *pendingPublicationImagesV1) recover(store providerstore.Store, dir string) error {
	t := images.t
	state, _, err := images.operation.ReadStateV1()
	if err != nil {
		return err
	}
	pending, found, err := images.operation.ReadPendingBuild()
	if err != nil || !found {
		return err
	}
	plan, err := PreparePendingPublicationRecovery(state.Current, pending, store, "demo", dir, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1, func(d canonical.Digest) (deploy.BuildLockV1, error) {
		lock, found, err := images.operation.ReadBuildLock(d, registry.ValidateRequirementProfileV1)
		if err == nil && !found {
			err = fmt.Errorf("missing lock %s", d)
		}
		return lock, err
	})
	if err != nil {
		return err
	}
	return executePendingPublicationRecovery(t.Context(), images.operation, store, plan, "demo", dir, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1, func(ctx context.Context, p deploy.PendingBuildV1, d deploy.PendingRecoveryDecision, old *providers.RealizedImageV1, environment, dir string) error {
		return recoverPendingImageReferences(ctx, p, d, old, environment, dir, images.primaryRemove, func(owner deploy.EnvironmentGenerationState, pair OwnedImageReferenceV1) error {
			return images.companionRemove(owner, pair, environment, dir)
		})
	})
}

func TestPendingOwnedPublicationCrashBoundariesV1(t *testing.T) {
	for _, boundary := range []string{"intent", "create-temporary", "create-companion", "create-primary", "phase-generation-created", "lock", "phase-lock-published", "commit", "phase-state-committed", "phase-cleanup", "remove-primary", "remove-companion", "remove-temporary", "prune-locks", "prune-store", "remove-intent"} {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/after=%v", boundary, after), func(t *testing.T) {
				stubNoAbandonedBuildReferences(t)
				dir, store, lock := pendingPortablePublicationFixtureV1(t)
				operation, err := deploy.AcquireOperationLock(t.Context(), dir)
				if err != nil {
					t.Fatal(err)
				}
				defer operation.Unlock()
				images := &pendingPublicationImagesV1{images: map[string]providers.RealizedImageV1{}, operation: operation, t: t, sequence: 10}
				previous, err := publishBuild(t.Context(), operation, store, publicationInput(t, dir, lock), images.backend(store, dir))
				if err != nil {
					t.Fatal(err)
				}
				images.fault = boundary
				images.after = after
				_, err = publishBuild(t.Context(), operation, store, publicationInput(t, dir, lock), images.backend(store, dir))
				if !errors.Is(err, errPendingPublicationFaultV1) || !images.fired {
					t.Fatalf("fault not observed: %v", err)
				}
				state, found, err := operation.ReadStateV1()
				if err != nil || !found {
					t.Fatalf("state lost: %v", err)
				}
				want := *state.Current
				images.fault = ""
				images.fired = false
				if err := images.recover(store, dir); err != nil {
					t.Fatal(err)
				}
				state, _, err = operation.ReadStateV1()
				if err != nil || *state.Current != want {
					t.Fatalf("recovery changed selected state: %v", err)
				}
				pairs, err := ProjectEnvironmentOwnedReferencesV1(want, lock, "demo", dir)
				if err != nil {
					t.Fatal(err)
				}
				if len(images.images) != 2 {
					t.Fatalf("wrong surviving image count: %#v", images.images)
				}
				for _, pair := range pairs {
					if images.images[pair.Reference] != pair.Image {
						t.Fatalf("selected pair lost: %s", pair.Reference)
					}
				}
				if _, found, err := operation.ReadPendingBuild(); err != nil || found {
					t.Fatalf("intent not removed: %v", err)
				}
				if boundary == "commit" && after && want.Reference == previous.Current.Reference {
					t.Fatal("commit effect was rolled back after returned error")
				}
				if err := images.recover(store, dir); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestPendingOwnedRecoveryAfterRetiredLockPruningV1(t *testing.T) {
	stubNoAbandonedBuildReferences(t)
	dir, store, oldLock := pendingPortablePublicationFixtureV1(t)
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	images := &pendingPublicationImagesV1{images: map[string]providers.RealizedImageV1{}, operation: operation, t: t, sequence: 30}
	old, err := publishBuild(t.Context(), operation, store, publicationInput(t, dir, oldLock), images.backend(store, dir))
	if err != nil {
		t.Fatal(err)
	}
	_, next := publicationLockFixture(t, dir, "a", "e", "f")
	images.fault = "remove-intent"
	if _, err := publishBuild(t.Context(), operation, store, publicationInput(t, dir, next), images.backend(store, dir)); !errors.Is(err, errPendingPublicationFaultV1) {
		t.Fatal(err)
	}
	if _, found, err := operation.ReadBuildLock(old.Current.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil || found {
		t.Fatalf("old lock not pruned: %v", err)
	}
	pending, found, err := operation.ReadPendingBuild()
	if err != nil || !found || len(pending.OldReferences) != 2 {
		t.Fatalf("cleanup authority lost: %v", err)
	}
	images.fault = ""
	if err := images.recover(store, dir); err != nil {
		t.Fatal(err)
	}
	if len(images.images) != 1 {
		t.Fatalf("wrong survivor: %#v", images.images)
	}
}

func TestPendingOwnedRecoveryConflictAndMalformedScopeChangeNothingV1(t *testing.T) {
	dir, store, lock := pendingPortablePublicationFixtureV1(t)
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	images := &pendingPublicationImagesV1{images: map[string]providers.RealizedImageV1{}, operation: operation, t: t, sequence: 40, fault: "commit"}
	if _, err := publishBuild(t.Context(), operation, store, publicationInput(t, dir, lock), images.backend(store, dir)); !errors.Is(err, errPendingPublicationFaultV1) {
		t.Fatal(err)
	}
	pending, _, err := operation.ReadPendingBuild()
	if err != nil {
		t.Fatal(err)
	}
	before := map[string]providers.RealizedImageV1{}
	for k, v := range images.images {
		before[k] = v
	}
	bad := pending
	companion := *bad.Candidate.Companion
	companion.Reference = strings.Replace(companion.Reference, ":g-", ":g-f", 1)
	bad.Candidate.Companion = &companion
	calls := 0
	if err := recoverPendingImageReferences(t.Context(), bad, deploy.PendingRecoveryDiscardCandidate, nil, "demo", dir, func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
		calls++
		return nil
	}, func(deploy.EnvironmentGenerationState, OwnedImageReferenceV1) error { calls++; return nil }); err == nil || calls != 0 {
		t.Fatalf("malformed scope mutated resources: %v %d", err, calls)
	}
	conflict := *pending.Candidate.Owner
	conflict.Reference = fixedPublicationReferences(t, dir, 60).Generation
	if _, err := PreparePendingPublicationRecovery(&conflict, pending, store, "demo", dir, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1, func(d canonical.Digest) (deploy.BuildLockV1, error) { return lock, nil }); err == nil {
		t.Fatal("conflict accepted")
	}
	if !reflect.DeepEqual(before, images.images) {
		t.Fatal("preflight changed references")
	}
	images.fault = ""
	images.images[pending.Candidate.Companion.Reference] = providers.RealizedImageV1{Digest: rendererDigest("f"), ConfigDigest: rendererDigest("f"), RootFSSubject: rendererDigest("a")}
	if err := images.recover(store, dir); err == nil {
		t.Fatal("retargeted companion erased")
	}
	if _, found, err := operation.ReadPendingBuild(); err != nil || !found {
		t.Fatal("retry authority erased")
	}
	if images.images[pending.Candidate.Companion.Reference] == pending.Candidate.Companion.Image {
		t.Fatal("retargeted companion was repaired")
	}
}

func TestPendingOwnedPublicationRetainsValidatedRootsV1(t *testing.T) {
	for _, scenario := range []string{"publish-distinct", "publish-equal-digest", "discard-validated-only"} {
		t.Run(scenario, func(t *testing.T) {
			stubNoAbandonedBuildReferences(t)
			dir, store, candidate := pendingPortablePublicationFixtureV1(t)
			operation, err := deploy.AcquireOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			defer operation.Unlock()
			_, validated := publicationLockFixture(t, dir, "a", "b", "f")
			if scenario == "publish-equal-digest" {
				candidate = validated
			}
			digest, err := operation.PublishBuildLock(validated, registry.ValidateRequirementProfileV1)
			if err != nil {
				t.Fatal(err)
			}
			overlay, err := deploy.RequestOverlayDigestV1(validated.Overlay)
			if err != nil {
				t.Fatal(err)
			}
			record := deploy.ValidatedBuildV1{Schema: deploy.ValidatedBuildSchemaV1, BlueprintDigest: validated.BlueprintDigest, OverlayDigest: overlay, PackageOverridesDigest: rendererDigest("e"), Platform: validated.Platform, BuildLockDigest: digest, Image: validated.FinalImage, ImageReference: fixedPublicationReferences(t, dir, 70).Generation}
			if err := operation.CommitValidatedBuildV1(record); err != nil {
				t.Fatal(err)
			}
			images := &pendingPublicationImagesV1{images: map[string]providers.RealizedImageV1{record.ImageReference: record.Image}, operation: operation, t: t, sequence: 80}
			if scenario == "discard-validated-only" {
				images.fault = "commit"
			}
			_, err = publishBuild(t.Context(), operation, store, publicationInput(t, dir, candidate), images.backend(store, dir))
			if scenario == "discard-validated-only" {
				if !errors.Is(err, errPendingPublicationFaultV1) {
					t.Fatal(err)
				}
				images.fault = ""
				if err := images.recover(store, dir); err != nil {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if _, found, err := operation.ReadBuildLock(digest, registry.ValidateRequirementProfileV1); err != nil || !found {
				t.Fatalf("validated lock lost: %v", err)
			}
			if _, err := deploy.BuildLockStoreClosure(validated, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
				t.Fatalf("validated store roots lost: %v", err)
			}
			if images.images[record.ImageReference] != record.Image {
				t.Fatal("independent validated alias was removed")
			}
			stored, found, err := operation.ReadValidatedBuildV1()
			if err != nil || !found || !reflect.DeepEqual(record, stored) {
				t.Fatal("validated owner changed")
			}
			if scenario == "discard-validated-only" {
				if len(images.images) != 1 {
					t.Fatal("uncommitted candidate aliases survived")
				}
				state, _, err := operation.ReadStateV1()
				if err != nil || state.Current != nil {
					t.Fatal("discarded candidate became current")
				}
			}
		})
	}
}

func TestPendingOwnedPublicationRejectsMissingValidatedRootBeforeEffectsV1(t *testing.T) {
	dir := t.TempDir()
	store, lock := publicationLockFixture(t, dir, "a", "b", "c")
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	record := deploy.ValidatedBuildV1{Schema: deploy.ValidatedBuildSchemaV1, BlueprintDigest: lock.BlueprintDigest, OverlayDigest: rendererDigest("a"), PackageOverridesDigest: rendererDigest("b"), Platform: lock.Platform, BuildLockDigest: rendererDigest("f"), Image: lock.FinalImage, ImageReference: fixedPublicationReferences(t, dir, 90).Generation}
	if err := operation.CommitValidatedBuildV1(record); err != nil {
		t.Fatal(err)
	}
	images := &pendingPublicationImagesV1{images: map[string]providers.RealizedImageV1{}, operation: operation, t: t}
	if _, err := publishBuild(t.Context(), operation, store, publicationInput(t, dir, lock), images.backend(store, dir)); err == nil {
		t.Fatal("missing validated root accepted")
	}
	if len(images.images) != 0 {
		t.Fatal("missing root was checked after tagging")
	}
	if _, found, err := operation.ReadPendingBuild(); err != nil || found {
		t.Fatal("intent written with missing retained root")
	}
}

func TestPendingOwnedPublicationConsumerGuardsV1(t *testing.T) {
	for _, boundary := range []string{"forced replacement", "provider failure", "validated publication", "installed transfer"} {
		t.Run(boundary, func(t *testing.T) {
			dir, store, lock := pendingPortablePublicationFixtureV1(t)
			operation, err := deploy.AcquireOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			images := &pendingPublicationImagesV1{images: map[string]providers.RealizedImageV1{}, operation: operation, t: t, sequence: 100}
			state, err := publishBuild(t.Context(), operation, store, publicationInput(t, dir, lock), images.backend(store, dir))
			if err != nil {
				t.Fatal(err)
			}
			state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
			state.BlueprintSource = filepath.Join(dir, "blueprint.yaml")
			if err := operation.CommitStateV1(state.Current, state); err != nil {
				t.Fatal(err)
			}
			mutations := 0
			switch boundary {
			case "forced replacement":
				if err := operation.Unlock(); err != nil {
					t.Fatal(err)
				}
				document, _ := testSelectedPlatformDocumentV1(t)
				document.Environment.ID = "replacement"
				_, err = ForceReplaceStagedDesiredStateV1(t.Context(), ForceReplaceStagedDesiredStateInputV1{DesiredState: DesiredStateStageInputV1{DeploymentDir: dir, Document: document, ExplicitPlatform: "linux/amd64"}})
			case "provider failure":
				defer operation.Unlock()
				stubNoAbandonedBuildReferences(t)
				err = cleanupFailedProviderBuildV1(t.Context(), LockedProviderBuildPreparationV1{Operation: operation, Store: store, Environment: "demo", DeploymentDir: dir})
			case "validated publication":
				defer operation.Unlock()
				_, err = publishValidatedBuild(t.Context(), operation, store, "demo", dir, lock, ValidatedBuildInputsV1{}, publishValidatedBuildBackendV1{newReferences: func(string, string) (EnvironmentImageReferences, error) {
					mutations++
					return fixedPublicationReferences(t, dir, 120), nil
				}, createReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
					mutations++
					return nil
				}, removeReference: func(context.Context, providers.RealizedImageV1, string, string, string) error {
					mutations++
					return nil
				}})
			case "installed transfer":
				defer operation.Unlock()
				destination := t.TempDir()
				destinationStore, storeErr := providerstore.NewStore(destination)
				if storeErr != nil {
					t.Fatal(storeErr)
				}
				dest, lockErr := deploy.AcquireOperationLock(t.Context(), destination)
				if lockErr != nil {
					t.Fatal(lockErr)
				}
				defer dest.Unlock()
				destinationImages := &pendingPublicationImagesV1{images: map[string]providers.RealizedImageV1{}, operation: dest, t: t}
				input := InstalledBuildPublicationInputV1{Environment: "demo", SourceDeploymentDir: dir, DestinationDeploymentDir: destination, Source: CurrentBuild{State: state, Generation: *state.Current, Lock: lock}, Build: lock, Installation: installedBuildPublicationInstallation(destination), References: fixedPublicationReferences(t, destination, 120)}
				_, err = publishInstalledBuildV1(t.Context(), operation, dest, store, destinationStore, input, installedOwnedPublicationBackendV1(t, operation, destinationImages, destinationStore, destination))
				if len(destinationImages.images) != 2 {
					t.Fatal("installed destination did not acquire separate ownership")
				}

			}
			if boundary == "provider failure" || boundary == "installed transfer" {
				if err != nil {
					t.Fatalf("delivered consumer rejected current portable ownership: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "portable") || mutations != 0 {
				t.Fatalf("unsafe consumer not rejected before effects: %v %d", err, mutations)
			}
			if _, err := deploy.BuildLockStoreClosure(lock, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
				t.Fatal("guard pruned owned storage")
			}
			if len(images.images) != 2 {
				t.Fatal("guard changed current references")
			}
		})
	}
}

func TestPendingOwnedPublicationStagedRemovalRecoversAfterAdmissionV1(t *testing.T) {
	for _, scenario := range []string{"portable current owner", "portable pending intent"} {
		t.Run(scenario, func(t *testing.T) {
			stubNoAbandonedBuildReferences(t)
			dir, store, lock := pendingPortablePublicationFixtureV1(t)
			document, platform := testSelectedPlatformDocumentV1(t)
			operation, err := deploy.AcquireOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			initial := deploy.StateV1{
				Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, document),
				BlueprintSource: "staged publication boundary fixture", Platform: platform,
				Overlay: deploy.EmptyRequestOverlayV1(),
				Staging: &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1},
			}
			if err := operation.CommitStateV1(nil, initial); err != nil {
				t.Fatal(err)
			}
			if err := operation.Unlock(); err != nil {
				t.Fatal(err)
			}

			images := &pendingPublicationImagesV1{
				images: map[string]providers.RealizedImageV1{}, t: t, sequence: 140,
			}
			backend := testStagedRemovalBackendV1(store)
			recoveryCalls := 0
			removedReferences := 0
			backend.newStore = func(string) (providerstore.Store, error) { return store, nil }
			backend.recoverPending = func(
				_ context.Context,
				op *deploy.OperationLock,
				_ providerstore.Store,
				_ *deploy.EnvironmentGenerationState,
				_ string,
				_ string,
			) (bool, error) {
				recoveryCalls++
				images.operation = op
				return true, images.recover(store, dir)
			}
			backend.admit = func(
				ctx context.Context,
				deploymentDir string,
				waitingOperation *deploy.OperationLock,
				input ControlAdmissionInputV1,
			) (AdmittedControlV1, error) {
				if input.Mode != ControlAdmissionWaitV1 {
					return AdmittedControlV1{}, fmt.Errorf("admission mode = %q, want wait", input.Mode)
				}
				if err := waitingOperation.Unlock(); err != nil {
					return AdmittedControlV1{}, err
				}
				writer, err := deploy.AcquireOperationLock(ctx, deploymentDir)
				if err != nil {
					return AdmittedControlV1{}, err
				}
				images.operation = writer
				publication := publicationInput(t, dir, lock)
				if scenario == "portable current owner" {
					if _, err := publishBuild(ctx, writer, store, publication, images.backend(store, dir)); err != nil {
						_ = writer.Unlock()
						return AdmittedControlV1{}, err
					}
				} else {
					images.fault = "intent"
					images.after = true
					_, publishErr := publishBuild(ctx, writer, store, publication, images.backend(store, dir))
					images.fault = ""
					images.after = false
					if !errors.Is(publishErr, errPendingPublicationFaultV1) {
						_ = writer.Unlock()
						return AdmittedControlV1{}, fmt.Errorf("portable pending intent was not retained: %w", publishErr)
					}
				}
				if err := writer.Unlock(); err != nil {
					return AdmittedControlV1{}, err
				}
				admittedOperation, err := deploy.AcquireOperationLock(ctx, deploymentDir)
				if err != nil {
					return AdmittedControlV1{}, err
				}
				return AdmittedControlV1{Operation: admittedOperation}, nil
			}
			backend.removeReference = func(_ context.Context, image providers.RealizedImageV1, reference, _, _ string) error {
				removedReferences++
				return images.remove("staged-remove-primary", reference, image)
			}
			backend.removeCompanion = func(_ context.Context, _ *deploy.OperationLock, pair OwnedImageReferenceV1, owner deploy.EnvironmentGenerationState, environment, dir string) error {
				removedReferences++
				return images.companionRemove(owner, pair, environment, dir)
			}
			backend.isolate = func(op *deploy.OperationLock, destination string) (bool, error) {
				if len(images.images) != 0 {
					t.Fatal("isolation reached with surviving publication references")
				}
				return op.IsolateOriginalDirectory(destination)
			}

			_, err = removeStagedDeploymentV1(t.Context(), StagedDeploymentRemoveInputV1{
				DeploymentDir: dir, ControlMode: ControlAdmissionWaitV1,
			}, backend)
			if err != nil {
				t.Fatal(err)
			}
			wantRemoved := 0
			if scenario == "portable current owner" {
				wantRemoved = 2
			}
			if recoveryCalls != 2 || removedReferences != wantRemoved || len(images.images) != 0 {
				t.Fatalf("recovery calls=%d, retired pairs=%d, survivors=%v", recoveryCalls, removedReferences, images.images)
			}
			if _, err := os.Lstat(dir); !os.IsNotExist(err) {
				t.Fatalf("staging directory remains: %v", err)
			}
		})
	}
}

func TestPendingOwnedPublicationDocumentContextV1(t *testing.T) {
	stubNoAbandonedBuildReferences(t)
	dir, store, lock := pendingPortablePublicationFixtureV1(t)
	validLock := lock
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	filesBefore := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
	document, _ := testSelectedPlatformDocumentV1(t)
	document.Environment.ID = "other-owner"
	lock.BlueprintDigest = testResolvedBlueprintDigestV1(t, document)
	input := BuildPublicationInput{
		Environment: "demo", DeploymentDir: dir, Document: document, Lock: lock,
	}
	mutations := 0
	countMutation := func() { mutations++ }
	backend := buildPublicationBackend{
		newReferences: func(string, string) (EnvironmentImageReferences, error) {
			countMutation()
			return fixedPublicationReferences(t, dir, 150), nil
		},
		createReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
			countMutation()
			return nil
		},
		removeReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
			countMutation()
			return nil
		},
		createCompanion: func(context.Context, *deploy.OperationLock, OwnedImageReferenceV1, deploy.EnvironmentGenerationState, string, string) error {
			countMutation()
			return nil
		},
		removeCompanion: func(context.Context, *deploy.OperationLock, OwnedImageReferenceV1, deploy.EnvironmentGenerationState, string, string) error {
			countMutation()
			return nil
		},
		commitState: func(*deploy.EnvironmentGenerationState, deploy.StateV1) error {
			countMutation()
			return nil
		},
		writeIntent: func(deploy.PendingBuildV1) error {
			countMutation()
			return nil
		},
		advancePhase: func(string) error {
			countMutation()
			return nil
		},
		publishLock: func(deploy.BuildLockV1) (canonical.Digest, error) {
			countMutation()
			return "", nil
		},
		pruneLocks: func([]canonical.Digest) error {
			countMutation()
			return nil
		},
		pruneStore: func([]deploy.BuildLockV1) error {
			countMutation()
			return nil
		},
		removeIntent: func() error {
			countMutation()
			return nil
		},
	}
	if _, err := publishBuild(t.Context(), operation, store, input, backend); err == nil || !strings.Contains(err.Error(), "ownership context") || mutations != 0 {
		t.Fatalf("mismatched document context error=%v mutations=%d", err, mutations)
	}
	if got := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root()); !reflect.DeepEqual(got, filesBefore) {
		t.Fatal("mismatched context changed deployment, build-lock, or provider-store files")
	}
	if _, found, err := operation.ReadStateV1(); err != nil || found {
		t.Fatalf("mismatched context committed state: found=%t err=%v", found, err)
	}
	if _, found, err := operation.ReadPendingBuild(); err != nil || found {
		t.Fatalf("mismatched context wrote pending intent: found=%t err=%v", found, err)
	}

	images := &pendingPublicationImagesV1{images: map[string]providers.RealizedImageV1{}, operation: operation, t: t, sequence: 160}
	state, err := publishBuild(t.Context(), operation, store, publicationInput(t, dir, validLock), images.backend(store, dir))
	if err != nil || state.Current == nil {
		t.Fatalf("same-owner publication failed: state=%#v err=%v", state.Current, err)
	}
}

func pendingOwnedFilesystemSnapshotV1(t *testing.T, deploymentDir, storeRoot string) map[string]string {
	t.Helper()
	result := map[string]string{}
	roots := []struct {
		name string
		path string
	}{{name: "deployment", path: filepath.Join(deploymentDir, ".reploy")}, {name: "store", path: storeRoot}}
	for _, root := range roots {
		err := filepath.WalkDir(root.path, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(root.path, path)
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			key := filepath.Join(root.name, relative)
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}
				result[key] = fmt.Sprintf("symlink:%o:%s", info.Mode().Perm(), target)
				return nil
			}
			if info.IsDir() {
				result[key] = fmt.Sprintf("directory:%o", info.Mode().Perm())
				return nil
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("unexpected filesystem entry %s: %s", path, info.Mode())
			}
			// Windows prohibits reading the byte range of a held operation lock.
			// This file carries no state; retain its metadata while snapshotting
			// every state, pending, build-lock, and provider-store file below.
			if root.name == "deployment" && relative == "operation.lock" {
				result[key] = fmt.Sprintf("operation-lock:%o:%d", info.Mode().Perm(), info.Size())
				return nil
			}
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			// Windows directory enumeration may retain stale hard-link
			// attributes. Bind current mode and bytes to the same open file.
			info, err = file.Stat()
			if err != nil {
				return errors.Join(err, file.Close())
			}
			if !info.Mode().IsRegular() {
				return errors.Join(fmt.Errorf("unexpected open filesystem entry %s: %s", path, info.Mode()), file.Close())
			}
			content, readErr := io.ReadAll(file)
			if err := errors.Join(readErr, file.Close()); err != nil {
				return err
			}
			result[key] = fmt.Sprintf("file:%o:%s", info.Mode().Perm(), content)
			return nil
		})
		if err != nil {
			t.Fatalf("snapshot %s: %v", root.name, err)
		}
	}
	return result
}

func TestRunProviderInstallV1RejectsPortableSourceBeforeInstallEffects(t *testing.T) {
	for _, existingDestination := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing-destination-%t", existingDestination), func(t *testing.T) {
			stubNoAbandonedBuildReferences(t)
			dir, store, lock := pendingPortablePublicationFixtureV1(t)
			operation, err := deploy.AcquireOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			images := &pendingPublicationImagesV1{images: map[string]providers.RealizedImageV1{}, operation: operation, t: t, sequence: 180}
			state, err := publishBuild(t.Context(), operation, store, publicationInput(t, dir, lock), images.backend(store, dir))
			if err != nil {
				t.Fatal(err)
			}
			before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
			if err := operation.Unlock(); err != nil {
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
					t.Fatal("acquired destination for unsupported portable install")
				}
				return deploy.AcquireOperationLock(ctx, path)
			}
			var releasedSource bool
			backend.release = func(held *deploy.OperationLock) error {
				releasedSource = true
				return held.Unlock()
			}
			backend.buildSource = func(_ context.Context, input LockedProviderBuildRunInputV1) (LockedProviderBuildExecutionResultV1, error) {
				if err := input.Operation.RequireHeld(); err != nil {
					t.Fatal(err)
				}
				return LockedProviderBuildExecutionResultV1{State: state, Lock: lock}, nil
			}
			backend.recoverDestination = func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (bool, error) {
				t.Fatal("recovered destination for unsupported portable install")
				return false, nil
			}
			backend.prepareAccount = func(context.Context, blueprint.SystemAccount, providerstore.Store, CurrentBuild, providerInstallRunInputV1) (providerInstallRunInputV1, error) {
				t.Fatal("prepared host account for unsupported portable install")
				return providerInstallRunInputV1{}, nil
			}
			_, err = runProviderInstallV1(t.Context(), providerInstallRunInputV1{SourceDeploymentDir: dir, DestinationDeploymentDir: destination}, backend)
			if err == nil || !strings.Contains(err.Error(), "portable installed publication is not yet supported") || !releasedSource {
				t.Fatalf("portable install error=%v released-source=%t", err, releasedSource)
			}
			if existingDestination {
				entries, err := os.ReadDir(destination)
				if err != nil || len(entries) != 0 {
					t.Fatalf("existing destination changed: entries=%v err=%v", entries, err)
				}
			} else if _, err := os.Lstat(destination); !os.IsNotExist(err) {
				t.Fatalf("unsupported portable install created destination: %v", err)
			}
			if after := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root()); !reflect.DeepEqual(after, before) {
				t.Fatalf("unsupported portable install changed source ownership: %s", pendingOwnedSnapshotDifferenceV1(before, after))
			}
		})
	}
}

func TestPendingOwnedPublicationNoCacheRetainsProviderSchemaRootsV1(t *testing.T) {
	for _, previousSchema := range []string{"profile", "bundle"} {
		t.Run(previousSchema, func(t *testing.T) {
			stubNoAbandonedBuildReferences(t)
			fixture := newPreparedPythonGraphReuseFixture(t)
			dir := filepath.Dir(filepath.Dir(fixture.store.Root()))
			store, candidate := publicationLockFixture(t, dir, "d", "e", "f")
			old := fixture.lock
			if previousSchema == "profile" {
				old.Nodes[0].RequirementProfile.Facts.Schema = "python-profile-facts-previous"
			}
			profileDigest, err := providers.RequirementProfileDigest(old.Nodes[0].RequirementProfile, acceptProviderProfileOwnerForCutoverV1)
			if err != nil {
				t.Fatal(err)
			}
			old.Nodes[0].ValidationEvidence.ProfileDigest = profileDigest
			content, err := store.LoadManifest(old.Nodes[0].BundleManifest)
			if err != nil {
				t.Fatal(err)
			}
			bundle, err := providers.DecodeResolvedBundleManifest(content, old.Nodes[0].BundleManifest, registry.ValidateResolvedBundlePayloadV1)
			if err != nil {
				t.Fatal(err)
			}
			bundle.Payload.RequirementProfileDigest = profileDigest
			if previousSchema == "bundle" {
				bundle.Payload.ProviderPayload.Schema = "python-bundle-previous"
			}
			bundle, err = providers.NewResolvedBundle(bundle.Payload, acceptProviderBundleOwnerForCutoverV1)
			if err != nil {
				t.Fatal(err)
			}
			old.Nodes[0].BundleManifest, err = providers.PublishResolvedBundleManifest(t.Context(), store, bundle, acceptProviderBundleOwnerForCutoverV1)
			if err != nil {
				t.Fatal(err)
			}
			policy, err := deploy.RuntimePolicyDigestV1(old.RuntimePolicy)
			if err != nil {
				t.Fatal(err)
			}
			old.ValidationRecord, err = deploy.PublishPrefixValidation(t.Context(), store, deploy.PrefixValidationV1{
				Schema: deploy.PrefixValidationSchemaV1, SubjectRootFS: old.FinalImage.RootFSSubject,
				RuntimePolicy: policy, Profiles: []providers.ValidationEvidence{}, ExposedOutputs: []providers.ExecutableEvidence{},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := deploy.BuildLockStoreClosure(old, store, acceptProviderProfileOwnerForCutoverV1, acceptProviderBundleOwnerForCutoverV1); err != nil {
				t.Fatalf("previous-schema fixture is not a complete canonical closure: %v", err)
			}
			if _, err := deploy.BuildLockStoreClosure(old, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err == nil {
				t.Fatal("previous-schema fixture unexpectedly passes current provider validation")
			}
			operation, err := deploy.AcquireOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			defer operation.Unlock()
			oldDigest, err := operation.PublishBuildLock(old, acceptProviderProfileOwnerForCutoverV1)
			if err != nil {
				t.Fatal(err)
			}
			overlay, err := deploy.RequestOverlayDigestV1(old.Overlay)
			if err != nil {
				t.Fatal(err)
			}
			record := deploy.ValidatedBuildV1{
				Schema: deploy.ValidatedBuildSchemaV1, BlueprintDigest: old.BlueprintDigest,
				OverlayDigest: overlay, PackageOverridesDigest: rendererDigest("e"), Platform: old.Platform,
				BuildLockDigest: oldDigest, Image: old.FinalImage, ImageReference: fixedPublicationReferences(t, dir, 170).Generation,
			}
			if err := operation.CommitValidatedBuildV1(record); err != nil {
				t.Fatal(err)
			}
			images := &pendingPublicationImagesV1{images: map[string]providers.RealizedImageV1{record.ImageReference: record.Image}, operation: operation, t: t, sequence: 180}
			backend := images.backend(store, dir)
			// Exercise the production default lock and store pruning validators.
			backend.pruneLocks = nil
			backend.pruneStore = nil
			before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
			input := publicationInput(t, dir, candidate)
			if _, err := publishBuild(t.Context(), operation, store, input, backend); err == nil {
				t.Fatal("ordinary publication accepted an incompatible retained owner")
			}
			if got := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root()); !reflect.DeepEqual(got, before) || len(images.images) != 1 {
				t.Fatalf("ordinary rejection changed retained ownership: images=%d; %s", len(images.images), pendingOwnedSnapshotDifferenceV1(before, got))
			}
			// No-cache relaxes retained owners only, never the supplied candidate.
			bad := old
			document, _ := testSelectedPlatformDocumentV1(t)
			bad.BlueprintDigest = testResolvedBlueprintDigestV1(t, document)
			badInput := publicationInput(t, dir, bad)
			badInput.NoCache = true
			if _, err := publishBuild(t.Context(), operation, store, badInput, backend); err == nil {
				t.Fatal("no-cache accepted a new candidate with incompatible provider data")
			}
			if got := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root()); !reflect.DeepEqual(got, before) || len(images.images) != 1 {
				t.Fatalf("incompatible candidate rejection changed retained ownership: images=%d; %s", len(images.images), pendingOwnedSnapshotDifferenceV1(before, got))
			}
			input.NoCache = true
			state, err := publishBuild(t.Context(), operation, store, input, backend)
			if err != nil || state.Current == nil {
				t.Fatalf("no-cache publication with independent retained owner failed: %v", err)
			}
			if images.images[record.ImageReference] != record.Image || len(images.images) != 2 {
				t.Fatal("no-cache publication lost the independently validated alias")
			}
			stored, found, err := operation.ReadValidatedBuildV1()
			if err != nil || !found || !reflect.DeepEqual(stored, record) {
				t.Fatalf("no-cache publication changed validated ownership: found=%t err=%v", found, err)
			}
			if _, found, err := operation.ReadBuildLock(oldDigest, acceptProviderProfileOwnerForCutoverV1); err != nil || !found {
				t.Fatalf("no-cache publication pruned the retained lock: found=%t err=%v", found, err)
			}
			if _, err := deploy.BuildLockStoreClosure(old, store, acceptProviderProfileOwnerForCutoverV1, acceptProviderBundleOwnerForCutoverV1); err != nil {
				t.Fatalf("no-cache publication pruned retained store roots: %v", err)
			}
			if _, found, err := operation.ReadPendingBuild(); err != nil || found {
				t.Fatalf("publication did not complete its intent: found=%t err=%v", found, err)
			}
		})
	}
}

func TestPendingOwnedPublicationNoCacheRetiresPortablePreviousSchemaCurrentV1(t *testing.T) {
	for _, failure := range []string{"", "create-primary"} {
		t.Run(failure, func(t *testing.T) {
			stubNoAbandonedBuildReferences(t)
			dir, store, old := pendingPortablePublicationFixtureV1(t)
			_, candidate := publicationLockFixture(t, dir, "d", "e", "f")
			old.Nodes[0].RequirementProfile.Facts.Schema = "python-profile-facts-previous"
			profileDigest, err := providers.RequirementProfileDigest(old.Nodes[0].RequirementProfile, acceptProviderProfileOwnerForCutoverV1)
			if err != nil {
				t.Fatal(err)
			}
			old.Nodes[0].ValidationEvidence.ProfileDigest = profileDigest
			content, err := store.LoadManifest(old.Nodes[0].BundleManifest)
			if err != nil {
				t.Fatal(err)
			}
			bundle, err := providers.DecodeResolvedBundleManifest(content, old.Nodes[0].BundleManifest, registry.ValidateResolvedBundlePayloadV1)
			if err != nil {
				t.Fatal(err)
			}
			bundle.Payload.RequirementProfileDigest = profileDigest
			bundle, err = providers.NewResolvedBundle(bundle.Payload, acceptProviderBundleOwnerForCutoverV1)
			if err != nil {
				t.Fatal(err)
			}
			old.Nodes[0].BundleManifest, err = providers.PublishResolvedBundleManifest(t.Context(), store, bundle, acceptProviderBundleOwnerForCutoverV1)
			if err != nil {
				t.Fatal(err)
			}
			policy, err := deploy.RuntimePolicyDigestV1(old.RuntimePolicy)
			if err != nil {
				t.Fatal(err)
			}
			old.ValidationRecord, err = deploy.PublishPrefixValidation(t.Context(), store, deploy.PrefixValidationV1{
				Schema: deploy.PrefixValidationSchemaV1, SubjectRootFS: old.FinalImage.RootFSSubject,
				RuntimePolicy: policy, Profiles: []providers.ValidationEvidence{}, ExposedOutputs: []providers.ExecutableEvidence{},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := deploy.BuildLockStoreClosure(old, store, acceptProviderProfileOwnerForCutoverV1, acceptProviderBundleOwnerForCutoverV1); err != nil {
				t.Fatalf("previous-schema fixture is not a complete canonical closure: %v", err)
			}
			if _, err := deploy.BuildLockStoreClosure(old, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err == nil {
				t.Fatal("previous-schema fixture unexpectedly passes current provider validation")
			}
			operation, err := deploy.AcquireOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			defer operation.Unlock()
			oldDigest, err := operation.PublishBuildLock(old, acceptProviderProfileOwnerForCutoverV1)
			if err != nil {
				t.Fatal(err)
			}
			document, platform := testSelectedPlatformDocumentV1(t)
			reference := fixedPublicationReferences(t, dir, 170).Generation
			owner := deploy.EnvironmentGenerationState{Reference: reference, ImageDigest: old.FinalImage.Digest, RootFSSubject: old.FinalImage.RootFSSubject, BuildLockDigest: oldDigest, Platform: old.Platform, RuntimePolicyDigest: policy}
			if err := operation.CommitStateV1(nil, deploy.StateV1{Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, document), Platform: platform, Overlay: deploy.EmptyRequestOverlayV1(), Current: &owner}); err != nil {
				t.Fatal(err)
			}
			companion, err := portableGenerationReferenceV1(reference, "demo", dir)
			if err != nil {
				t.Fatal(err)
			}
			images := &pendingPublicationImagesV1{images: map[string]providers.RealizedImageV1{reference: old.FinalImage, companion: old.PortableRuntimeLayer.Result}, operation: operation, t: t, sequence: 180}
			backend := images.backend(store, dir)
			// Exercise the production default lock and store pruning validators.
			backend.pruneLocks = nil
			backend.pruneStore = nil
			before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
			input := publicationInput(t, dir, candidate)
			if _, err := publishBuild(t.Context(), operation, store, input, backend); err == nil {
				t.Fatal("ordinary publication accepted an incompatible retained owner")
			}
			if got := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root()); !reflect.DeepEqual(got, before) || len(images.images) != 2 {
				t.Fatalf("ordinary rejection changed retained ownership: images=%d; %s", len(images.images), pendingOwnedSnapshotDifferenceV1(before, got))
			}
			// No-cache relaxes retained owners only, never the supplied candidate.
			bad := old
			document, _ = testSelectedPlatformDocumentV1(t)
			bad.BlueprintDigest = testResolvedBlueprintDigestV1(t, document)
			badInput := publicationInput(t, dir, bad)
			badInput.NoCache = true
			if _, err := publishBuild(t.Context(), operation, store, badInput, backend); err == nil {
				t.Fatal("no-cache accepted a new candidate with incompatible provider data")
			}
			if got := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root()); !reflect.DeepEqual(got, before) || len(images.images) != 2 {
				t.Fatalf("incompatible candidate rejection changed retained ownership: images=%d; %s", len(images.images), pendingOwnedSnapshotDifferenceV1(before, got))
			}
			input.NoCache = true
			if failure != "" {
				images.fault = failure
				if _, err := publishBuild(t.Context(), operation, store, input, backend); !errors.Is(err, errPendingPublicationFaultV1) {
					t.Fatalf("no-cache did not reach injected failure: %v", err)
				}
				pending, found, err := operation.ReadPendingBuild()
				if err != nil || !found {
					t.Fatalf("missing pending intent: %v", err)
				}
				profile, bundle := providerBuildRecoveryValidatorsV1(true)
				plan, err := PreparePendingPublicationRecovery(&owner, pending, store, "demo", dir, profile, bundle, func(d canonical.Digest) (deploy.BuildLockV1, error) {
					l, found, err := operation.ReadBuildLock(d, profile)
					if err == nil && !found {
						err = fmt.Errorf("missing lock %s", d)
					}
					return l, err
				})
				if err != nil {
					t.Fatalf("no-cache rollback preflight: %v", err)
				}
				if err := executePendingPublicationRecovery(t.Context(), operation, store, plan, "demo", dir, profile, bundle, func(ctx context.Context, p deploy.PendingBuildV1, d deploy.PendingRecoveryDecision, old *providers.RealizedImageV1, environment, dir string) error {
					return recoverPendingImageReferences(ctx, p, d, old, environment, dir, images.primaryRemove, func(owner deploy.EnvironmentGenerationState, pair OwnedImageReferenceV1) error {
						return images.companionRemove(owner, pair, environment, dir)
					})
				}); err != nil {
					t.Fatalf("no-cache rollback: %v", err)
				}
				if len(images.images) != 2 || images.images[reference] != old.FinalImage || images.images[companion] != old.PortableRuntimeLayer.Result {
					t.Fatal("rollback changed surviving old portable ownership")
				}
				if _, found, err := operation.ReadPendingBuild(); err != nil || found {
					t.Fatalf("rollback retained completed intent: %v", err)
				}
				// Rollback prunes the failed candidate's unowned store objects.
				// A fresh attempt rebuilds them before publication.
				_, candidate = publicationLockFixture(t, dir, "d", "e", "f")
				input = publicationInput(t, dir, candidate)
				input.NoCache = true
			}
			state, err := publishBuild(t.Context(), operation, store, input, backend)
			if err != nil || state.Current == nil {
				t.Fatalf("no-cache replacement of old portable current failed: %v", err)
			}
			if len(images.images) != 1 || images.images[state.Current.Reference] != candidate.FinalImage {
				t.Fatal("old portable owner did not retire exactly")
			}
			if _, found, err := operation.ReadBuildLock(oldDigest, acceptProviderProfileOwnerForCutoverV1); err != nil || found {
				t.Fatalf("retired old lock remains: %v", err)
			}
			if _, found, err := operation.ReadPendingBuild(); err != nil || found {
				t.Fatalf("completed publication retained intent: %v", err)
			}
		})
	}
}

// Keep byte/type/mode comparisons complete and report only bounded identities.
func pendingOwnedSnapshotDifferenceV1(before, after map[string]string) string {
	changed := map[string]bool{}
	for k, v := range before {
		if got, found := after[k]; !found || got != v {
			changed[k] = true
		}
	}
	for k, v := range after {
		if old, found := before[k]; !found || old != v {
			changed[k] = true
		}
	}
	keys := make([]string, 0, len(changed))
	for k := range changed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	count := len(keys)
	if len(keys) > 20 {
		keys = keys[:20]
	}
	parts := []string{}
	for _, k := range keys {
		old, oldFound := before[k]
		got, found := after[k]
		parts = append(parts, fmt.Sprintf("%s before=%t:%x after=%t:%x", k, oldFound, sha256.Sum256([]byte(old)), found, sha256.Sum256([]byte(got))))
	}
	return fmt.Sprintf("%d changed entries: %s", count, strings.Join(parts, "; "))
}
