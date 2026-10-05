package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

// State, owner inventories, control admission and locks are real. Only Docker
// alias operations and workload stopping are substituted.
func forceRetirementFixtureV1(t *testing.T) (string, *validatedPublicationImagesV1, []string, forceReplaceStagedDesiredStateBackendV1, ForceReplaceStagedDesiredStateInputV1) {
	t.Helper()
	dir, images, references, staged := stagedRetirementFixtureV1(t)
	images.restart(t, dir)
	previous, _, err := images.operation.ReadValidatedBuildV1()
	if err != nil {
		t.Fatal(err)
	}
	lock, _, err := images.operation.ReadBuildLock(previous.BuildLockDigest, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	next := validatedPublicationRecordV1(t, dir, lock, ValidatedBuildInputsV1{
		BlueprintDigest: previous.BlueprintDigest, OverlayDigest: previous.OverlayDigest,
		PackageOverridesDigest: previous.PackageOverridesDigest,
	}, 31)
	next.PendingCleanup, err = pendingValidatedCleanupV1(&previous, next, "demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := images.operation.CommitValidatedBuildV1(next); err != nil {
		t.Fatal(err)
	}
	images.retain(t, next, dir)
	references = append(references, next.ImageReference, next.Companion.Reference)
	if err := images.operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	document, _ := testSelectedPlatformDocumentV1(t)
	document.Environment.ID = "replacement"
	input := ForceReplaceStagedDesiredStateInputV1{DesiredState: DesiredStateStageInputV1{
		DeploymentDir: dir, Document: document, BlueprintSource: "replacement source",
	}}
	backend := forceReplaceStagedDesiredStateBackendV1{
		acquire: staged.acquire, newStore: staged.newStore, recoverPending: staged.recoverPending,
		admit: func(ctx context.Context, dir string, op *deploy.OperationLock, in ControlAdmissionInputV1) (AdmittedControlV1, error) {
			admitted, err := staged.admit(ctx, dir, op, in)
			images.operation = admitted.Operation
			return admitted, err
		},
		complete: staged.complete, stopOwned: staged.stopOwned,
		removeReference: staged.removeReference, removeCompanion: staged.removeCompanion,
		commit: func(op *deploy.OperationLock, expected *deploy.EnvironmentGenerationState, state deploy.StateV1) error {
			for _, reference := range references {
				if _, found := images.images[reference]; found {
					t.Fatalf("replacement committed before former alias retired: %s", reference)
				}
			}
			if _, found, err := op.ReadValidatedBuildV1(); err != nil || found {
				t.Fatalf("replacement retained old-scope validated record: %v %v", found, err)
			}
			return op.CommitStateV1(expected, state)
		},
		stageSame: StageDesiredStateV1,
	}
	return dir, images, references, backend, input
}

func bindForceRetirementBackendV1(t *testing.T, backend *forceReplaceStagedDesiredStateBackendV1, images *validatedPublicationImagesV1, fault string, after bool) {
	t.Helper()
	retirement := validatedRetirementImagesBackendV1(t, images, fault, after)
	backend.removeReference = func(ctx context.Context, image providers.RealizedImageV1, reference, environment, dir string) error {
		assertForceRetirementOldScopeV1(t, images.operation, environment)
		return retirement.removeReference(ctx, image, reference, environment, dir)
	}
	backend.removeCompanion = func(ctx context.Context, op *deploy.OperationLock, pair OwnedImageReferenceV1, owner deploy.EnvironmentGenerationState, environment, dir string) error {
		assertForceRetirementOldScopeV1(t, op, environment)
		return retirement.removeCompanion(ctx, op, pair, owner, environment, dir)
	}
}

func assertForceRetirementOldScopeV1(t *testing.T, op *deploy.OperationLock, environment string) {
	t.Helper()
	state, found, retainedEnvironment, err := readForceReplacementStateV1(op)
	if err != nil || !found || environment != "demo" || retainedEnvironment != "demo" || state.Staging == nil || state.TerminalRemoval {
		t.Fatalf("retirement lost old staged scope: %v %v %s %#v", found, err, environment, state)
	}
}

func assertForceRetirementRetainedV1(t *testing.T, dir string) {
	t.Helper()
	op, err := deploy.AcquireExistingOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Unlock()
	assertForceRetirementOldScopeV1(t, op, "demo")
	state, _, err := op.ReadStateV1()
	if err != nil || state.Current == nil {
		t.Fatalf("current retry authority lost: %v %#v", err, state)
	}
	if _, found, err := op.ReadBuildLock(state.Current.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil || !found {
		t.Fatalf("current lock lost before replacement: %v %v", found, err)
	}
}

func snapshotForceRetirementRecoveryResourcesV1(t *testing.T, dir string, store providerstore.Store) map[string][]byte {
	t.Helper()
	snapshot := map[string][]byte{}
	readFile := func(path string) {
		content, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			snapshot[path] = []byte("<missing>")
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		snapshot[path] = content
	}
	for _, name := range []string{"state.json", "validated-build.json", "pending-build.json", "pending-validated-build.json"} {
		readFile(filepath.Join(dir, ".reploy", name))
	}
	for _, root := range []string{filepath.Join(dir, ".reploy", "locks"), store.Root()} {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snapshot[path] = content
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	return snapshot
}

func cloneForceRetirementImagesV1(images map[string]providers.RealizedImageV1) map[string]providers.RealizedImageV1 {
	cloned := make(map[string]providers.RealizedImageV1, len(images))
	for reference, image := range images {
		cloned[reference] = image
	}
	return cloned
}

func retainForceRetirementValidatedCleanupOverlapV1(t *testing.T, op *deploy.OperationLock, dir string, images *validatedPublicationImagesV1, overlap string, committed bool, candidateSequence byte) {
	t.Helper()
	state, found, err := op.ReadStateV1()
	if err != nil || !found || state.Current == nil {
		t.Fatalf("read current owner for validated cleanup overlap: found=%v err=%v state=%#v", found, err, state)
	}
	currentLock, found, err := op.ReadBuildLock(state.Current.BuildLockDigest, registry.ValidateRequirementProfileV1)
	if err != nil || !found {
		t.Fatalf("read current owner lock: found=%v err=%v", found, err)
	}
	currentPairs, err := ProjectEnvironmentOwnedReferencesV1(*state.Current, currentLock, "demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	cleanupPair := deploy.ValidatedBuildReferenceV1{Image: currentPairs[0].Image, ImageReference: currentPairs[0].Reference}
	if overlap == "companion" {
		if len(currentPairs) != 2 {
			t.Fatalf("portable companion overlap requires a portable current owner: %#v", currentPairs)
		}
		owner := *state.Current
		cleanupPair = deploy.ValidatedBuildReferenceV1{Image: currentPairs[1].Image, ImageReference: currentPairs[1].Reference, CompanionOwner: &owner}
	} else if overlap != "primary" {
		t.Fatalf("unknown overlap kind %q", overlap)
	}
	previous, found, err := op.ReadValidatedBuildV1()
	if err != nil || !found || previous.Discarded {
		t.Fatalf("read active previous validated owner: found=%v err=%v record=%#v", found, err, previous)
	}
	previous.PendingCleanup = append(previous.PendingCleanup, cleanupPair)
	sort.Slice(previous.PendingCleanup, func(i, j int) bool {
		return previous.PendingCleanup[i].ImageReference < previous.PendingCleanup[j].ImageReference
	})
	if err := op.CommitValidatedBuildV1(previous); err != nil {
		t.Fatal(err)
	}
	candidate := previous
	candidate.ImageReference = fixedPublicationReferences(t, dir, candidateSequence).Generation
	candidate.Owner, candidate.Companion, candidate.PendingCleanup = nil, nil, nil
	candidate.PendingStorageCleanup = true
	candidateLock, found, err := op.ReadBuildLock(candidate.BuildLockDigest, registry.ValidateRequirementProfileV1)
	if err != nil || !found {
		t.Fatalf("read candidate validated lock: found=%v err=%v", found, err)
	}
	owner, err := validatedPublicationOwnerV1(candidate, candidateLock)
	if err != nil {
		t.Fatal(err)
	}
	candidatePairs, err := ProjectEnvironmentOwnedReferencesV1(owner, candidateLock, "demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidatePairs) == 2 {
		candidate.Owner = &owner
		candidate.Companion = &candidatePairs[1]
	}
	candidate.PendingCleanup, err = pendingValidatedCleanupV1(&previous, candidate, "demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	intent := deploy.PendingValidatedBuildV1{Schema: deploy.PendingValidatedBuildSchemaV1, Previous: &previous, Candidate: candidate}
	if err := deploy.ValidatePendingValidatedBuildV1(intent); err != nil {
		t.Fatalf("constructed canonical pending validated intent: %v", err)
	}
	if err := op.WritePendingValidatedBuildV1(intent); err != nil {
		t.Fatal(err)
	}
	if committed {
		if err := op.CommitValidatedBuildV1(candidate); err != nil {
			t.Fatal(err)
		}
	}
	images.retain(t, candidate, dir)
}

func TestForceRetirementRetriesEveryFormerPairV1(t *testing.T) {
	for index := 0; index < 6; index++ {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("reference=%d/after=%v", index, after), func(t *testing.T) {
				dir, images, references, backend, input := forceRetirementFixtureV1(t)
				independent := fixedPublicationReferences(t, dir, 83).Generation
				images.images[independent] = images.images[references[0]]
				bindForceRetirementBackendV1(t, &backend, images, references[index], after)
				attempts := 1
				if index == 1 {
					attempts = 2 // Repeated current-pair retry after validated metadata is gone.
				}
				for attempt := 0; attempt < attempts; attempt++ {
					result, err := forceReplaceStagedDesiredStateV1(t.Context(), input, backend)
					if !errors.Is(err, errPendingPublicationFaultV1) || result.Changed {
						t.Fatalf("partial retirement changed desired identity: %#v %v", result, err)
					}
					assertForceRetirementRetainedV1(t, dir)
				}
				bindForceRetirementBackendV1(t, &backend, images, "", false)
				result, err := forceReplaceStagedDesiredStateV1(t.Context(), input, backend)
				if err != nil || !result.Changed || result.State.Current != nil {
					t.Fatalf("replacement retry failed: %#v %v", result, err)
				}
				if len(images.images) != 1 || images.images[independent].Digest == "" {
					t.Fatalf("former aliases survived or independent owner changed: %#v", images.images)
				}
			})
		}
	}
}

func TestForceRetirementValidatedOnlyAndFailedCommitV1(t *testing.T) {
	for _, scenario := range []string{"validated-only", "commit-before-effect", "commit-after-effect"} {
		t.Run(scenario, func(t *testing.T) {
			dir, images, references, backend, input := forceRetirementFixtureV1(t)
			bindForceRetirementBackendV1(t, &backend, images, "", false)
			if scenario == "validated-only" {
				images.restart(t, dir)
				state, _, err := images.operation.ReadStateV1()
				if err != nil {
					t.Fatal(err)
				}
				old := state.Current
				state.Current = nil
				if err := images.operation.CommitStateV1(old, state); err != nil {
					t.Fatal(err)
				}
				delete(images.images, references[0])
				delete(images.images, references[1])
				if err := images.operation.Unlock(); err != nil {
					t.Fatal(err)
				}
				backend.stopOwned = func(context.Context, *deploy.OperationLock, deploy.StateV1, string, RunOptions) error {
					t.Fatal("validated-only staging stopped an unrecorded current workload")
					return nil
				}
			} else {
				commit := backend.commit
				backend.commit = func(op *deploy.OperationLock, old *deploy.EnvironmentGenerationState, state deploy.StateV1) error {
					if scenario == "commit-after-effect" {
						if err := commit(op, old, state); err != nil {
							return err
						}
					}
					return errPendingPublicationFaultV1
				}
			}
			result, err := forceReplaceStagedDesiredStateV1(t.Context(), input, backend)
			if scenario == "validated-only" {
				if err != nil || !result.Changed {
					t.Fatalf("validated-only replacement: %#v %v", result, err)
				}
			} else if !errors.Is(err, errPendingPublicationFaultV1) || result.Changed != (scenario == "commit-after-effect") {
				t.Fatalf("commit outcome not recovered: %#v %v", result, err)
			}
			if len(images.images) != 0 {
				t.Fatal("former aliases survived replacement commit boundary", images.images)
			}
			if scenario == "commit-before-effect" {
				assertForceRetirementRetainedV1(t, dir)
				backend.commit = func(op *deploy.OperationLock, old *deploy.EnvironmentGenerationState, state deploy.StateV1) error {
					return op.CommitStateV1(old, state)
				}
				if _, err := forceReplaceStagedDesiredStateV1(t.Context(), input, backend); err != nil {
					t.Fatal("old-scope retry after failed commit", err)
				}
			}
		})
	}
}

func TestForceRetirementRejectsAuthorityBeforeEffectsV1(t *testing.T) {
	for _, scenario := range []string{"missing-current-lock", "missing-validated-lock", "foreign-current-scope", "malformed-inventory", "changed-current-during-admission", "changed-validated-during-admission"} {
		t.Run(scenario, func(t *testing.T) {
			dir, images, _, backend, input := forceRetirementFixtureV1(t)
			images.restart(t, dir)
			state, _, err := images.operation.ReadStateV1()
			if err != nil {
				t.Fatal(err)
			}
			record, _, err := images.operation.ReadValidatedBuildV1()
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "missing-current-lock" {
				if err := images.operation.RemoveBuildLock(state.Current.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil {
					t.Fatal(err)
				}
			} else if scenario == "missing-validated-lock" {
				record.BuildLockDigest = rendererDigest("f")
				record.Owner.BuildLockDigest = record.BuildLockDigest
				if err := images.operation.CommitValidatedBuildV1(record); err != nil {
					t.Fatal(err)
				}
			} else if scenario == "foreign-current-scope" {
				refs, err := NewEnvironmentImageReferences("foreign", dir)
				if err != nil {
					t.Fatal(err)
				}
				previous := *state.Current
				state.Current.Reference = refs.Generation
				if err := images.operation.CommitStateV1(&previous, state); err != nil {
					t.Fatal(err)
				}
			} else if scenario == "malformed-inventory" {
				if err := os.WriteFile(filepath.Join(dir, ".reploy", "validated-build.json"), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				admit := backend.admit
				backend.admit = func(ctx context.Context, dir string, op *deploy.OperationLock, in ControlAdmissionInputV1) (AdmittedControlV1, error) {
					if scenario == "changed-current-during-admission" {
						changed := state
						changed.BlueprintSource += " changed"
						if err := op.CommitStateV1(state.Current, changed); err != nil {
							return AdmittedControlV1{}, err
						}
					} else {
						record.PendingStorageCleanup = !record.PendingStorageCleanup
						if err := op.CommitValidatedBuildV1(record); err != nil {
							return AdmittedControlV1{}, err
						}
					}
					return admit(ctx, dir, op, in)
				}
			}
			if err := images.operation.Unlock(); err != nil {
				t.Fatal(err)
			}
			backend.stopOwned = func(context.Context, *deploy.OperationLock, deploy.StateV1, string, RunOptions) error {
				t.Fatal("invalid former authority reached workload stop")
				return nil
			}
			_, err = forceReplaceStagedDesiredStateV1(t.Context(), input, backend)
			if err == nil || images.effects != 0 || len(images.images) != 6 {
				t.Fatalf("invalid authority reached retirement: %v %d %#v", err, images.effects, images.images)
			}
		})
	}
}

func TestForceRetirementRejectsIncompleteRetainedOwnerBeforeCurrentRecoveryV1(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprintf("committed=%v", committed), func(t *testing.T) {
			dir, images, _, backend, input := forceRetirementFixtureV1(t)
			recoverCurrent := suspendForcePublicationV1(t, dir, images, backend, "current", committed)
			recoveryCalls := 0
			backend.recoverPending = func(ctx context.Context, op *deploy.OperationLock, store providerstore.Store, current *deploy.EnvironmentGenerationState, environment, deploymentDir string) (bool, error) {
				recoveryCalls++
				return recoverCurrent(ctx, op, store, current, environment, deploymentDir)
			}

			images.restart(t, dir)
			record, found, err := images.operation.ReadValidatedBuildV1()
			if err != nil || !found || record.Companion == nil || record.Owner == nil {
				t.Fatalf("expected retained portable validated owner: found=%v err=%v record=%#v", found, err, record)
			}
			record.Owner, record.Companion = nil, nil
			if err := images.operation.CommitValidatedBuildV1(record); err != nil {
				t.Fatal(err)
			}
			store, err := backend.newStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			beforeState, stateFound, err := images.operation.ReadStateV1()
			if err != nil || !stateFound {
				t.Fatalf("read pre-recovery state: found=%v err=%v", stateFound, err)
			}
			beforeResources := snapshotForceRetirementRecoveryResourcesV1(t, dir, store)
			beforeImages := cloneForceRetirementImagesV1(images.images)
			beforeEffects := images.effects
			if _, pending, err := images.operation.ReadPendingBuild(); err != nil || !pending {
				t.Fatalf("fixture has no current publication intent: pending=%v err=%v", pending, err)
			}
			if err := images.operation.Unlock(); err != nil {
				t.Fatal(err)
			}

			result, replaceErr := forceReplaceStagedDesiredStateV1(t.Context(), input, backend)
			if replaceErr == nil || result.Changed || recoveryCalls != 0 {
				t.Fatalf("incomplete retained owner reached current recovery: result=%#v err=%v recovery calls=%d", result, replaceErr, recoveryCalls)
			}
			images.restart(t, dir)
			afterState, stateFound, err := images.operation.ReadStateV1()
			if err != nil || !stateFound || !reflect.DeepEqual(beforeState, afterState) {
				t.Fatalf("recovery changed current authority: found=%v err=%v after=%#v", stateFound, err, afterState)
			}
			afterResources := snapshotForceRetirementRecoveryResourcesV1(t, dir, store)
			if !reflect.DeepEqual(beforeResources, afterResources) {
				t.Fatal("recovery changed pending intent, validated authority, build locks, or provider-store bytes")
			}
			if !reflect.DeepEqual(beforeImages, images.images) || beforeEffects != images.effects {
				t.Fatal("recovery changed Docker aliases or performed a Docker effect")
			}
			if err := images.operation.Unlock(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestForceRetirementRejectsValidatedCleanupOverlapBeforeRecoveryV1(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		committed bool
		overlap   string
	}{
		{name: "uncommitted-primary", overlap: "primary"},
		{name: "committed-primary", committed: true, overlap: "primary"},
		{name: "uncommitted-portable-companion", overlap: "companion"},
		{name: "committed-portable-companion", committed: true, overlap: "companion"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			dir, images, _, backend, input := forceRetirementFixtureV1(t)
			images.restart(t, dir)
			store, err := backend.newStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			retainForceRetirementValidatedCleanupOverlapV1(t, images.operation, dir, images, scenario.overlap, scenario.committed, 72)
			beforeState, stateFound, err := images.operation.ReadStateV1()
			if err != nil || !stateFound {
				t.Fatalf("read state before recovery: found=%v err=%v", stateFound, err)
			}
			beforeResources := snapshotForceRetirementRecoveryResourcesV1(t, dir, store)
			beforeImages := cloneForceRetirementImagesV1(images.images)
			beforeEffects := images.effects
			if err := images.operation.Unlock(); err != nil {
				t.Fatal(err)
			}

			recoveryCalls := 0
			backend.recoverPending = func(ctx context.Context, op *deploy.OperationLock, store providerstore.Store, _ *deploy.EnvironmentGenerationState, environment, deploymentDir string) (bool, error) {
				recoveryCalls++
				images.operation = op
				return recoverPendingValidatedPublicationV1(ctx, op, store, environment, deploymentDir, images.backend(deploymentDir))
			}
			result, replaceErr := forceReplaceStagedDesiredStateV1(t.Context(), input, backend)
			images.restart(t, dir)
			afterState, stateFound, err := images.operation.ReadStateV1()
			afterResources := snapshotForceRetirementRecoveryResourcesV1(t, dir, store)
			stateUnchanged := err == nil && stateFound && reflect.DeepEqual(beforeState, afterState)
			resourcesUnchanged := reflect.DeepEqual(beforeResources, afterResources)
			aliasesUnchanged := reflect.DeepEqual(beforeImages, images.images)
			effectsUnchanged := beforeEffects == images.effects
			if replaceErr == nil || result.Changed || recoveryCalls != 0 || !stateUnchanged || !resourcesUnchanged || !aliasesUnchanged || !effectsUnchanged {
				t.Fatalf("validated cleanup overlap crossed recovery boundary: result=%#v err=%v recovery calls=%d state unchanged=%v resources unchanged=%v aliases unchanged=%v effects unchanged=%v", result, replaceErr, recoveryCalls, stateUnchanged, resourcesUnchanged, aliasesUnchanged, effectsUnchanged)
			}
			if err := images.operation.Unlock(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestForceRetirementRejectsValidatedCleanupOverlapAfterAdmissionV1(t *testing.T) {
	dir, images, _, backend, input := forceRetirementFixtureV1(t)
	store, err := backend.newStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	var expectedResources map[string][]byte
	var expectedImages map[string]providers.RealizedImageV1
	expectedEffects := images.effects
	admissionReached := false
	postAdmissionRecoveryCalls := 0
	backend.recoverPending = func(ctx context.Context, op *deploy.OperationLock, store providerstore.Store, _ *deploy.EnvironmentGenerationState, environment, deploymentDir string) (bool, error) {
		if admissionReached {
			postAdmissionRecoveryCalls++
		}
		_, currentPending, err := op.ReadPendingBuild()
		if err != nil {
			return false, err
		}
		_, validatedPending, err := op.ReadPendingValidatedBuildV1()
		if err != nil || (!currentPending && !validatedPending) {
			return false, err
		}
		images.operation = op
		return recoverPendingValidatedPublicationV1(ctx, op, store, environment, deploymentDir, images.backend(deploymentDir))
	}
	baseAdmit := backend.admit
	backend.admit = func(ctx context.Context, deploymentDir string, op *deploy.OperationLock, admission ControlAdmissionInputV1) (AdmittedControlV1, error) {
		admitted, err := baseAdmit(ctx, deploymentDir, op, admission)
		if err != nil {
			return admitted, err
		}
		admissionReached = true
		images.operation = admitted.Operation
		retainForceRetirementValidatedCleanupOverlapV1(t, admitted.Operation, dir, images, "companion", false, 73)
		expectedResources = snapshotForceRetirementRecoveryResourcesV1(t, dir, store)
		expectedImages = cloneForceRetirementImagesV1(images.images)
		expectedEffects = images.effects
		return admitted, nil
	}

	result, replaceErr := forceReplaceStagedDesiredStateV1(t.Context(), input, backend)
	images.restart(t, dir)
	afterResources := snapshotForceRetirementRecoveryResourcesV1(t, dir, store)
	resourcesUnchanged := reflect.DeepEqual(expectedResources, afterResources)
	aliasesUnchanged := reflect.DeepEqual(expectedImages, images.images)
	effectsUnchanged := expectedEffects == images.effects
	if !admissionReached || replaceErr == nil || result.Changed || postAdmissionRecoveryCalls != 0 || !resourcesUnchanged || !aliasesUnchanged || !effectsUnchanged {
		changed := []string{}
		for path, before := range expectedResources {
			if after, found := afterResources[path]; !found || string(before) != string(after) {
				changed = append(changed, path)
			}
		}
		for path := range afterResources {
			if _, found := expectedResources[path]; !found {
				changed = append(changed, path)
			}
		}
		t.Fatalf("admission did not reject before validated recovery: result=%#v err=%v admission=%v post-admission recoveries=%d resources unchanged=%v changed=%v aliases unchanged=%v effects unchanged=%v", result, replaceErr, admissionReached, postAdmissionRecoveryCalls, resourcesUnchanged, changed, aliasesUnchanged, effectsUnchanged)
	}
	if err := images.operation.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestForceRetirementPreflightsCurrentRecoveryAfterAdmissionV1(t *testing.T) {
	dir, images, _, backend, input := forceRetirementFixtureV1(t)
	store, err := backend.newStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	var pendingImages *pendingPublicationImagesV1
	var expectedResources map[string][]byte
	var expectedImages map[string]providers.RealizedImageV1
	recoveryCalls := 0
	admissionReached := false
	backend.recoverPending = func(ctx context.Context, op *deploy.OperationLock, store providerstore.Store, _ *deploy.EnvironmentGenerationState, environment, deploymentDir string) (bool, error) {
		recoveryCalls++
		_, found, err := op.ReadPendingBuild()
		if err != nil || !found {
			return false, err
		}
		if pendingImages == nil {
			t.Fatal("post-admission intent has no substituted Docker recovery backend")
		}
		pendingImages.operation = op
		return true, pendingImages.recover(store, deploymentDir)
	}
	baseAdmit := backend.admit
	backend.admit = func(ctx context.Context, deploymentDir string, op *deploy.OperationLock, admission ControlAdmissionInputV1) (AdmittedControlV1, error) {
		admitted, err := baseAdmit(ctx, deploymentDir, op, admission)
		if err != nil {
			return admitted, err
		}
		admissionReached = true
		images.operation = admitted.Operation
		state, found, err := admitted.Operation.ReadStateV1()
		if err != nil || !found || state.Current == nil {
			t.Fatalf("read admitted current state: found=%v err=%v state=%#v", found, err, state)
		}
		lock, found, err := admitted.Operation.ReadBuildLock(state.Current.BuildLockDigest, registry.ValidateRequirementProfileV1)
		if err != nil || !found {
			t.Fatalf("read admitted current lock: found=%v err=%v", found, err)
		}
		validated, found, err := admitted.Operation.ReadValidatedBuildV1()
		if err != nil || !found {
			t.Fatalf("read admitted validated inventory: found=%v err=%v", found, err)
		}
		validated.PendingCleanup = nil
		if err := admitted.Operation.CommitValidatedBuildV1(validated); err != nil {
			t.Fatal(err)
		}
		pendingImages = &pendingPublicationImagesV1{images: images.images, operation: admitted.Operation, t: t, sequence: 101, fault: "intent", after: true}
		_, publishErr := publishBuild(ctx, admitted.Operation, store, publicationInput(t, dir, lock), pendingImages.backend(store, dir))
		if !errors.Is(publishErr, errPendingPublicationFaultV1) {
			t.Fatalf("failed to retain post-admission current intent: %v", publishErr)
		}
		pendingImages.fault, pendingImages.after = "", false
		record, found, err := admitted.Operation.ReadValidatedBuildV1()
		if err != nil || !found || record.Owner == nil || record.Companion == nil {
			t.Fatalf("read admitted retained portable owner: found=%v err=%v record=%#v", found, err, record)
		}
		record.Owner, record.Companion = nil, nil
		if err := admitted.Operation.CommitValidatedBuildV1(record); err != nil {
			t.Fatal(err)
		}
		expectedResources = snapshotForceRetirementRecoveryResourcesV1(t, dir, store)
		expectedImages = cloneForceRetirementImagesV1(images.images)
		return admitted, nil
	}

	result, replaceErr := forceReplaceStagedDesiredStateV1(t.Context(), input, backend)
	if !admissionReached || replaceErr == nil || result.Changed || recoveryCalls != 1 {
		t.Fatalf("post-admission incomplete owner reached recovery: result=%#v err=%v recovery calls=%d", result, replaceErr, recoveryCalls)
	}
	images.restart(t, dir)
	if afterResources := snapshotForceRetirementRecoveryResourcesV1(t, dir, store); !reflect.DeepEqual(expectedResources, afterResources) {
		changed := []string{}
		for path, before := range expectedResources {
			if after, found := afterResources[path]; !found || string(before) != string(after) {
				changed = append(changed, path)
			}
		}
		for path := range afterResources {
			if _, found := expectedResources[path]; !found {
				changed = append(changed, path)
			}
		}
		t.Fatalf("post-admission recovery changed state, pending intent, validated authority, build locks, or provider-store bytes: %v", changed)
	}
	if !reflect.DeepEqual(expectedImages, images.images) {
		t.Fatal("post-admission recovery changed Docker aliases")
	}
	if err := images.operation.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestForceSameEnvironmentPreservesPendingValidatedCleanupV1(t *testing.T) {
	for _, current := range []bool{true, false} {
		t.Run(fmt.Sprintf("current=%v", current), func(t *testing.T) {
			dir, images, _, backend, input := forceRetirementFixtureV1(t)
			images.restart(t, dir)
			state, _, err := images.operation.ReadStateV1()
			if err != nil {
				t.Fatal(err)
			}
			if !current {
				previous := state.Current
				state.Current = nil
				if err := images.operation.CommitStateV1(previous, state); err != nil {
					t.Fatal(err)
				}
			}
			record, found, err := images.operation.ReadValidatedBuildV1()
			if err != nil || !found || len(record.PendingCleanup) == 0 {
				t.Fatalf("fixture requires retained validated cleanup: %v %v %#v", found, err, record)
			}
			document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
			if err != nil {
				t.Fatal(err)
			}
			input.DesiredState.Document = document
			input.DesiredState.BlueprintSource = state.BlueprintSource
			input.DesiredState.ExplicitPlatform = state.Platform.Canonical
			store, err := backend.newStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			before := snapshotForceRetirementRecoveryResourcesV1(t, dir, store)
			beforeImages := cloneForceRetirementImagesV1(images.images)
			if err := images.operation.Unlock(); err != nil {
				t.Fatal(err)
			}
			result, err := forceReplaceStagedDesiredStateV1(t.Context(), input, backend)
			if err != nil || result.Changed || !reflect.DeepEqual(result.State, state) {
				t.Fatalf("ordinary same-environment staging with retained cleanup: %#v %v", result, err)
			}
			images.restart(t, dir)
			defer images.operation.Unlock()
			if !reflect.DeepEqual(before, snapshotForceRetirementRecoveryResourcesV1(t, dir, store)) ||
				!reflect.DeepEqual(beforeImages, images.images) || images.effects != 0 {
				t.Fatal("same-environment staging changed retained owner resources")
			}
		})
	}
}

func TestForceRetirementOwnerFreeStorageRetryRemainsRequiredV1(t *testing.T) {
	dir, images, _, backend, input := forceRetirementFixtureV1(t)
	bindForceRetirementBackendV1(t, &backend, images, "", false)
	images.restart(t, dir)
	state, _, err := images.operation.ReadStateV1()
	if err != nil {
		t.Fatal(err)
	}
	oldDigest := state.Current.BuildLockDigest
	if err := images.operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	backend.cleanupStorage = func(*deploy.OperationLock, providerstore.Store, *deploy.BuildLockV1) error {
		return errPendingPublicationFaultV1
	}
	if result, err := forceReplaceStagedDesiredStateV1(t.Context(), input, backend); !errors.Is(err, errPendingPublicationFaultV1) || !result.Changed {
		t.Fatalf("replacement must report committed state and failed storage cleanup: %#v %v", result, err)
	}
	if result, err := forceReplaceStagedDesiredStateV1(t.Context(), input, backend); !errors.Is(err, errPendingPublicationFaultV1) || result.Changed {
		t.Fatalf("owner-free retry must not pass with failed storage cleanup: %#v %v", result, err)
	}
	images.restart(t, dir)
	if _, found, err := images.operation.ReadBuildLock(oldDigest, registry.ValidateRequirementProfileV1); err != nil || !found {
		t.Fatalf("failed storage cleanup lost retained retry work: %v %v", found, err)
	}
	if err := images.operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	backend.cleanupStorage = cleanupValidatedBuildStorage
	if result, err := forceReplaceStagedDesiredStateV1(t.Context(), input, backend); err != nil || result.Changed {
		t.Fatalf("owner-free storage retry did not complete: %#v %v", result, err)
	}
	images.restart(t, dir)
	defer images.operation.Unlock()
	if _, found, err := images.operation.ReadBuildLock(oldDigest, registry.ValidateRequirementProfileV1); err != nil || found {
		t.Fatalf("successful storage retry left the former lock: %v %v", found, err)
	}
	if len(images.images) != 0 {
		t.Fatal("storage retry restored former image aliases")
	}
}

func TestForceRetirementStorageRetryPreservesNewOwnersV1(t *testing.T) {
	dir, images, _, backend, input := forceRetirementFixtureV1(t)
	bindForceRetirementBackendV1(t, &backend, images, "", false)
	store, err := backend.newStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	backend.cleanupStorage = func(*deploy.OperationLock, providerstore.Store, *deploy.BuildLockV1) error {
		return errPendingPublicationFaultV1
	}
	result, err := forceReplaceStagedDesiredStateV1(t.Context(), input, backend)
	if !errors.Is(err, errPendingPublicationFaultV1) || !result.Changed || len(images.images) != 0 {
		t.Fatalf("replacement did not precede storage fault: %#v %v", result, err)
	}
	images.restart(t, dir)
	_, lock := publicationLockFixture(t, dir, "a", "b", "c")
	lock.BlueprintDigest = testResolvedBlueprintDigestV1(t, input.DesiredState.Document)
	lock.PackageOverrides.EnvironmentID = "replacement"
	digest, err := images.operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := NewEnvironmentImageReferences("replacement", dir)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	owner := deploy.EnvironmentGenerationState{Reference: refs.Generation, ImageDigest: lock.FinalImage.Digest, RootFSSubject: lock.FinalImage.RootFSSubject, BuildLockDigest: digest, Platform: lock.Platform, RuntimePolicyDigest: policy}
	state, _, err := images.operation.ReadStateV1()
	if err != nil {
		t.Fatal(err)
	}
	state.Current = &owner
	if err := images.operation.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	validatedRefs, err := NewEnvironmentImageReferences("replacement", dir)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := ValidatedBuildInputs(input.DesiredState.Document, lock.Overlay, deploy.EmptyPackageOverridesV1("replacement"), dir, lock.Platform)
	if err != nil {
		t.Fatal(err)
	}
	_, validatedLock := publicationLockFixture(t, dir, "d", "e", "f")
	validatedLock.BlueprintDigest = lock.BlueprintDigest
	validatedLock.PackageOverrides.EnvironmentID = "replacement"
	validatedDigest, err := images.operation.PublishBuildLock(validatedLock, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	validated := deploy.ValidatedBuildV1{Schema: deploy.ValidatedBuildSchemaV1, BlueprintDigest: inputs.BlueprintDigest, OverlayDigest: inputs.OverlayDigest, PackageOverridesDigest: inputs.PackageOverridesDigest, Platform: lock.Platform, BuildLockDigest: validatedDigest, Image: validatedLock.FinalImage, ImageReference: validatedRefs.Generation}
	if err := images.operation.CommitValidatedBuildV1(validated); err != nil {
		t.Fatal(err)
	}
	images.images[owner.Reference], images.images[validated.ImageReference] = lock.FinalImage, validatedLock.FinalImage
	before := map[string]providers.RealizedImageV1{}
	for key, value := range images.images {
		before[key] = value
	}
	if err := images.operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	backend.cleanupStorage = cleanupValidatedBuildStorage
	backend.removeReference = func(context.Context, providers.RealizedImageV1, string, string, string) error {
		t.Fatal("metadata retry attempted stale image retirement")
		return nil
	}
	if _, err := forceReplaceStagedDesiredStateV1(t.Context(), input, backend); err != nil {
		t.Fatal("metadata retry failed with new owners", err)
	}
	images.restart(t, dir)
	defer images.operation.Unlock()
	if !reflect.DeepEqual(before, images.images) {
		t.Fatal("metadata retry changed new owner aliases")
	}
	retained, found, err := images.operation.ReadValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(retained, validated) {
		t.Fatalf("metadata retry changed new validated owner: %v %v %#v", found, err, retained)
	}
	retainedState, _, err := images.operation.ReadStateV1()
	if err != nil || !reflect.DeepEqual(retainedState.Current, &owner) {
		t.Fatal("metadata retry changed new current owner", err)
	}
	if _, err := deploy.BuildLockStoreClosure(lock, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
		t.Fatal("metadata retry pruned new owner roots", err)
	}
	if _, found, err := images.operation.ReadBuildLock(validatedDigest, registry.ValidateRequirementProfileV1); err != nil || !found {
		t.Fatal("metadata retry pruned distinct validated lock", found, err)
	}
	if _, err := deploy.BuildLockStoreClosure(validatedLock, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
		t.Fatal("metadata retry pruned distinct validated roots", err)
	}
}

func suspendForcePublicationV1(t *testing.T, dir string, images *validatedPublicationImagesV1, backend forceReplaceStagedDesiredStateBackendV1, kind string, committed bool) func(context.Context, *deploy.OperationLock, providerstore.Store, *deploy.EnvironmentGenerationState, string, string) (bool, error) {
	t.Helper()
	images.restart(t, dir)
	store, err := backend.newStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	state, _, err := images.operation.ReadStateV1()
	if err != nil {
		t.Fatal(err)
	}
	lock, _, err := images.operation.ReadBuildLock(state.Current.BuildLockDigest, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	fault := "intent"
	if committed {
		fault = "remove-intent"
	}
	var recover func(context.Context, *deploy.OperationLock, providerstore.Store, *deploy.EnvironmentGenerationState, string, string) (bool, error)
	if kind == "current" {
		// Ordinary publication starts only after previous superseded cleanup.
		previous, _, readErr := images.operation.ReadValidatedBuildV1()
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, pair := range previous.PendingCleanup {
			delete(images.images, pair.ImageReference)
		}
		previous.PendingCleanup = nil
		if err := images.operation.CommitValidatedBuildV1(previous); err != nil {
			t.Fatal(err)
		}
		pendingImages := &pendingPublicationImagesV1{images: images.images, operation: images.operation, t: t, sequence: 90, fault: fault, after: !committed}
		_, err = publishBuild(t.Context(), images.operation, store, publicationInput(t, dir, lock), pendingImages.backend(store, dir))
		pendingImages.fault, pendingImages.after = "", false
		recover = func(_ context.Context, op *deploy.OperationLock, store providerstore.Store, _ *deploy.EnvironmentGenerationState, environment, dir string) (bool, error) {
			if environment != "demo" {
				t.Fatal("current recovery changed environment", environment)
			}
			pendingImages.operation = op
			return true, pendingImages.recover(store, dir)
		}
	} else {
		previous, _, readErr := images.operation.ReadValidatedBuildV1()
		if readErr != nil {
			t.Fatal(readErr)
		}
		publication := images.backend(dir)
		publication.newReferences = func(string, string) (EnvironmentImageReferences, error) {
			return fixedPublicationReferences(t, dir, 61), nil
		}
		images.fault, images.after = fault, !committed
		_, err = publishValidatedBuild(t.Context(), images.operation, store, "demo", dir, lock, ValidatedBuildInputsV1{
			BlueprintDigest: previous.BlueprintDigest, OverlayDigest: previous.OverlayDigest, PackageOverridesDigest: previous.PackageOverridesDigest,
			PackageOverrides: lock.PackageOverrides, BaseImage: lock.Base.AuthorReference, Platform: lock.Platform,
		}, publication)
		images.fault, images.after = "", false
		recover = func(ctx context.Context, op *deploy.OperationLock, store providerstore.Store, _ *deploy.EnvironmentGenerationState, environment, dir string) (bool, error) {
			return recoverPendingValidatedPublicationV1(ctx, op, store, environment, dir, publication)
		}
	}
	if !errors.Is(err, errPendingPublicationFaultV1) && !(kind == "validated" && committed && err == nil) {
		t.Fatalf("publication fault did not retain intent: %v", err)
	}
	if kind == "validated" {
		if _, found, err := images.operation.ReadPendingValidatedBuildV1(); err != nil || !found {
			t.Fatalf("validated fault did not retain pending authority: %v %v", found, err)
		}
	} else if _, found, err := images.operation.ReadPendingBuild(); err != nil || !found {
		t.Fatalf("current fault did not retain pending authority: %v %v", found, err)
	}
	if err := images.operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	return recover
}

func TestForceRetirementRecoversBothPublicationKindsV1(t *testing.T) {
	for _, kind := range []string{"current", "validated"} {
		for _, committed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/committed=%v", kind, committed), func(t *testing.T) {
				stubNoAbandonedBuildReferences(t)
				dir, images, _, backend, input := forceRetirementFixtureV1(t)
				backend.recoverPending = suspendForcePublicationV1(t, dir, images, backend, kind, committed)
				bindForceRetirementBackendV1(t, &backend, images, "", false)
				if result, err := forceReplaceStagedDesiredStateV1(t.Context(), input, backend); err != nil || !result.Changed || len(images.images) != 0 {
					t.Fatalf("publication recovery left former ownership: %#v %v %#v", result, err, images.images)
				}
				images.restart(t, dir)
				defer images.operation.Unlock()
				if _, found, err := images.operation.ReadPendingBuild(); err != nil || found {
					t.Fatalf("current intent survived replacement: %v %v", found, err)
				}
				if _, found, err := images.operation.ReadPendingValidatedBuildV1(); err != nil || found {
					t.Fatalf("validated intent survived replacement: %v %v", found, err)
				}
			})
		}
	}
}

func TestForceReplacementAndOrdinaryStagingRejectUnresolvedIntentsV1(t *testing.T) {
	for _, scenario := range []string{"malformed-current", "malformed-validated", "conflicting-intents"} {
		t.Run(scenario, func(t *testing.T) {
			dir, images, _, backend, input := forceRetirementFixtureV1(t)
			if scenario == "conflicting-intents" {
				_ = suspendForcePublicationV1(t, dir, images, backend, "validated", false)
				path := filepath.Join(dir, ".reploy", "pending-validated-build.json")
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				images.restart(t, dir)
				if err := images.operation.RemovePendingValidatedBuildV1(); err != nil {
					t.Fatal(err)
				}
				if err := images.operation.Unlock(); err != nil {
					t.Fatal(err)
				}
				_ = suspendForcePublicationV1(t, dir, images, backend, "current", false)
				if err := os.WriteFile(path, content, 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				filename := "pending-build.json"
				if scenario == "malformed-validated" {
					filename = "pending-validated-build.json"
				}
				if err := os.WriteFile(filepath.Join(dir, ".reploy", filename), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			images.restart(t, dir)
			before, _, err := images.operation.ReadStateV1()
			if err != nil {
				t.Fatal(err)
			}
			document, err := blueprint.DecodeResolvedDocumentV1(before.Blueprint)
			if err != nil {
				t.Fatal(err)
			}
			if err := images.operation.Unlock(); err != nil {
				t.Fatal(err)
			}
			_, err = ForceReplaceStagedDesiredStateV1(t.Context(), input)
			if err == nil || (scenario == "conflicting-intents" && !strings.Contains(err.Error(), "conflict")) {
				t.Fatalf("public force replacement accepted unresolved intents: %v", err)
			}
			_, err = StageDesiredStateV1(t.Context(), DesiredStateStageInputV1{DeploymentDir: dir, Document: document, BlueprintSource: "changed source"})
			if err == nil {
				t.Fatal("ordinary staging bypassed unresolved publication")
			}
			if _, err := RestageCurrentDesiredPlatformV1(t.Context(), dir, "linux/amd64"); err == nil {
				t.Fatal("ordinary platform restaging bypassed unresolved publication")
			}
			images.restart(t, dir)
			defer images.operation.Unlock()
			after, _, err := images.operation.ReadStateV1()
			wantAliases := 6
			if scenario == "conflicting-intents" {
				wantAliases = 4 // Previous superseded cleanup completed before current intent.
			}
			if err != nil || !reflect.DeepEqual(before, after) || len(images.images) != wantAliases {
				t.Fatalf("unresolved intents changed ownership: %v %#v", err, after)
			}
		})
	}
}
