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
	"github.com/omry/reploy/internal/buildprogress"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

func TestRunProviderBuildV1HoldsOneLockAcrossPreparationAndExecution(t *testing.T) {
	dir, document := stageProviderBuildRunState(t, false)
	baseOverride := "sha256:" + strings.Repeat("a", 64)
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	overrides := deploy.EmptyPackageOverridesV1(document.Environment.ID)
	overrides.Environment.Base = &deploy.BaseImageOverrideV1{Image: baseOverride}
	if err := operation.CommitPackageOverridesV1(overrides); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	order := []string{}
	want := LockedProviderBuildExecutionResultV1{Reused: true}
	var progress strings.Builder
	var buildEvents []buildprogress.Event

	result, err := runProviderBuildV1(t.Context(), ProviderBuildRunInputV1{
		DeploymentDir: dir, NoCache: true,
		Runtime:  StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1001, GID: 1002},
		Progress: &progress,
		BuildProgress: func(event buildprogress.Event) {
			buildEvents = append(buildEvents, event)
		},
	}, providerBuildRunBackend{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			order = append(order, "prepare")
			if err := input.Operation.RequireHeld(); err != nil {
				t.Fatal(err)
			}
			if input.Environment != document.Environment.ID || input.DeploymentDir != dir || !input.NoCache || input.Store.Root() != filepath.Join(dir, ".reploy", "provider-store") || len(input.Sources) != 0 || input.BaseImage != baseOverride || input.LocalOverrides == nil || len(input.LocalOverrides) != 0 || input.ReployVersion != deploy.ToolVersion {
				t.Fatalf("preparation input = %#v", input)
			}
			if input.DockerPlan.EnvironmentID != "demo" || input.DockerPlan.Phase != blueprint.PhaseStaged || input.DockerPlan.Image != providerBuildPlanImage || input.DockerPlan.Scope != nil || input.DockerPlan.Sandbox.RuntimeUser.UID != 1001 || input.DockerPlan.Sandbox.RuntimeUser.GID != 1002 {
				t.Fatalf("Docker plan = %#v", input.DockerPlan)
			}
			return LockedProviderBuildPreparationV1{Operation: input.Operation, Store: input.Store}, nil
		},
		execute: func(_ context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			order = append(order, "execute")
			if err := input.Preparation.Operation.RequireHeld(); err != nil {
				t.Fatal(err)
			}
			if !input.RunOptions.NoCache || len(input.SourceWheels) != 0 || len(input.LocalOverrides) != 0 || input.Progress != &progress || input.BuildProgress == nil {
				t.Fatalf("execution input = %#v", input)
			}
			return want, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result, want) || !reflect.DeepEqual(order, []string{"prepare", "execute"}) {
		t.Fatalf("result/order = %#v/%#v", result, order)
	}
	if len(buildEvents) != 3 ||
		buildEvents[0].Phase != buildprogress.PhaseInspect ||
		buildEvents[0].Environment != document.Environment.ID ||
		buildEvents[1].Phase != buildprogress.PhasePrepare ||
		buildEvents[2].Phase != buildprogress.PhasePublish ||
		buildEvents[2].Completed != 1 || buildEvents[2].Total != 1 {
		t.Fatalf("structured build progress = %#v", buildEvents)
	}
	for _, want := range []string{
		"preparing environment demo for linux/amd64",
		"preparing component packages and image layers",
	} {
		if !strings.Contains(progress.String(), want) {
			t.Fatalf("progress missing %q:\n%s", want, progress.String())
		}
	}
	operation, err = deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal("build did not release operation lock:", err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestRunLockedProviderBuildV1UsesAndRetainsCallerLock(t *testing.T) {
	dir, document := stageProviderBuildRunState(t, false)
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := LockedProviderBuildExecutionResultV1{Reused: true}
	order := []string{}

	result, err := runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{
		Operation: operation, Store: store, DeploymentDir: dir,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 0, GID: 0},
		NoCache: true,
	}, providerBuildRunBackend{
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			order = append(order, "prepare")
			if input.Operation != operation || input.Store.Root() != store.Root() || input.Environment != document.Environment.ID || !input.NoCache {
				t.Fatalf("preparation input = %#v", input)
			}
			return LockedProviderBuildPreparationV1{Operation: input.Operation, Store: input.Store}, nil
		},
		execute: func(_ context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			order = append(order, "execute")
			if input.Preparation.Operation != operation {
				t.Fatalf("execution input = %#v", input)
			}
			return LockedProviderBuildExecutionResultV1{Reused: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result, want) || !reflect.DeepEqual(order, []string{"prepare", "execute"}) {
		t.Fatalf("result/order = %#v/%#v", result, order)
	}
	if err := operation.RequireHeld(); err != nil {
		t.Fatalf("caller lock was released: %v", err)
	}
}

func TestRunLockedProviderBuildV1DiscardsSupersededCandidateAfterSuccessfulExecution(t *testing.T) {
	for _, cleanupFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("cleanup-failure=%v", cleanupFailure), func(t *testing.T) {
			dir, operation, store, lock, state := currentBuildFixture(t, true)
			defer operation.Unlock()
			document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
			if err != nil {
				t.Fatal(err)
			}
			overrides := deploy.EmptyPackageOverridesV1(document.Environment.ID)
			inputs, err := ValidatedBuildInputs(document, state.Overlay, overrides, dir, state.Platform)
			if err != nil {
				t.Fatal(err)
			}
			candidate := validatedPublicationRecordV1(t, dir, lock, inputs, 21)
			if err := operation.CommitValidatedBuildV1(candidate); err != nil {
				t.Fatal(err)
			}
			images := &validatedPublicationImagesV1{pendingPublicationImagesV1: pendingPublicationImagesV1{
				images: map[string]providers.RealizedImageV1{}, operation: operation, t: t,
			}}
			images.retain(t, candidate, dir)
			currentPairs, err := projectEnvironmentOwnedReferencesV1(*state.Current, lock, "demo", dir, registry.ValidateRequirementProfileV1)
			if err != nil {
				t.Fatal(err)
			}
			for _, pair := range currentPairs {
				images.images[pair.Reference] = pair.Image
			}
			beforeAliases := make(map[string]providers.RealizedImageV1, len(images.images))
			for reference, image := range images.images {
				beforeAliases[reference] = image
			}
			order := []string{}
			discarded := false
			var progress strings.Builder
			result, err := runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{
				Operation: operation, Store: store, DeploymentDir: dir,
				Runtime:  StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1001, GID: 1002},
				Progress: &progress,
			}, providerBuildRunBackend{
				prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
					order = append(order, "prepare")
					if input.ValidatedCandidate == nil {
						t.Fatal("validated candidate was not offered to preparation")
					}
					return LockedProviderBuildPreparationV1{
						Operation: input.Operation, Store: input.Store, Environment: input.Environment,
						DeploymentDir: input.DeploymentDir, DockerPlan: input.DockerPlan,
						ValidatedCandidate: input.ValidatedCandidate,
					}, nil
				},
				discardValidated: func(ctx context.Context, op *deploy.OperationLock, environment, deploymentDir string) error {
					order = append(order, "discard")
					discarded = true
					if len(order) < 3 || order[len(order)-2] != "execute" {
						t.Fatalf("candidate retirement preceded successful execution: %v", order)
					}
					fault := ""
					if cleanupFailure {
						fault = candidate.ImageReference
					}
					_, found, err := discardValidatedBuildWithBackendV1(
						ctx, op, environment, deploymentDir,
						validatedRetirementImagesBackendV1(t, images, fault, false),
					)
					if err != nil || !found {
						return errors.Join(fmt.Errorf("candidate retirement found=%v", found), err)
					}
					if err := cleanupValidatedBuildStorage(op, store, nil); err != nil {
						return nil
					}
					return op.RemoveValidatedBuildV1()
				},
				execute: func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
					order = append(order, "execute")
					if discarded {
						t.Fatal("superseded candidate was discarded before successful provider execution")
					}
					return LockedProviderBuildExecutionResultV1{}, nil
				},
			})
			if err != nil || !reflect.DeepEqual(order, []string{"prepare", "execute", "discard"}) {
				t.Fatalf("result/error/order = %#v/%v/%v", result, err, order)
			}
			if !discarded {
				t.Fatal("successful publication did not retire the superseded candidate")
			}
			if cleanupFailure {
				recorded, found, err := operation.ReadValidatedBuildV1()
				if err != nil || !found || !reflect.DeepEqual(recorded, candidate) {
					t.Fatalf("cleanup failure lost retryable owner: found=%v error=%v record=%#v", found, err, recorded)
				}
				if !reflect.DeepEqual(images.images, beforeAliases) {
					t.Fatalf("failed cleanup changed exact aliases: before=%#v after=%#v", beforeAliases, images.images)
				}
				if !strings.Contains(progress.String(), "cleanup of superseded validated references is pending") {
					t.Fatalf("cleanup warning missing: %s", progress.String())
				}
			} else {
				if _, found, err := operation.ReadValidatedBuildV1(); err != nil || found {
					t.Fatalf("successful retirement left a validated record: found=%v error=%v", found, err)
				}
				if _, found := images.images[candidate.ImageReference]; found {
					t.Fatal("successful retirement retained the superseded alias")
				}
				for _, pair := range currentPairs {
					if images.images[pair.Reference] != pair.Image {
						t.Fatalf("successful retirement removed current alias %s", pair.Reference)
					}
				}
			}
		})
	}
}

func TestRunLockedProviderBuildV1PublishesReplacementWithMissingTrialCacheV1(t *testing.T) {
	stubNoAbandonedBuildReferences(t)
	dir, operation, store, replacement, state := currentBuildFixture(t, true)
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
	trial := validatedBuildStorageVariant(t, store, replacement, "7", "8")
	trial.FinalImage.RootFSSubject = rendererDigest("c")
	trial.RuntimeLayer.Result.RootFSSubject = trial.FinalImage.RootFSSubject
	policy, err := deploy.RuntimePolicyDigestV1(trial.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	trial.ValidationRecord, err = deploy.PublishPrefixValidation(t.Context(), store, deploy.PrefixValidationV1{
		Schema: deploy.PrefixValidationSchemaV1, SubjectRootFS: trial.FinalImage.RootFSSubject,
		Profiles: []providers.ValidationEvidence{}, RuntimePolicy: policy,
		ExposedOutputs: []providers.ExecutableEvidence{},
	})
	if err != nil {
		t.Fatal(err)
	}
	trialDigest, err := operation.PublishBuildLock(trial, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	trialRecord := validatedPublicationRecordV1(t, dir, trial, inputs, 21)
	if err := operation.CommitValidatedBuildV1(trialRecord); err != nil {
		t.Fatal(err)
	}
	images := &validatedPublicationImagesV1{pendingPublicationImagesV1: pendingPublicationImagesV1{
		images: map[string]providers.RealizedImageV1{}, operation: operation, t: t, sequence: 30,
	}}
	images.retain(t, trialRecord, dir)
	currentPairs, err := projectEnvironmentOwnedReferencesV1(*state.Current, replacement, "demo", dir, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range currentPairs {
		images.images[pair.Reference] = pair.Image
	}
	validationPath, err := store.ValidationRecordPath(trial.ValidationRecord)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(validationPath); err != nil {
		t.Fatal(err)
	}
	if _, err := deploy.BuildLockStoreClosure(replacement, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
		t.Fatalf("replacement closure is incomplete: %v", err)
	}
	beforePrepare := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
	aliasesBeforePrepare := make(map[string]providers.RealizedImageV1, len(images.images))
	for reference, image := range images.images {
		aliasesBeforePrepare[reference] = image
	}
	prepareFailure := errors.New("replacement preparation failed")
	_, prepareErr := runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{
		Operation: operation, Store: store, DeploymentDir: dir,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1001, GID: 1002},
	}, providerBuildRunBackend{
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			if input.ValidatedCandidate != nil {
				t.Fatal("incomplete trial cache was offered for reuse")
			}
			return LockedProviderBuildPreparationV1{}, prepareFailure
		},
		execute: func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			t.Fatal("failed preparation reached publication")
			return LockedProviderBuildExecutionResultV1{}, nil
		},
	})
	if !errors.Is(prepareErr, prepareFailure) {
		t.Fatalf("preparation error = %v, want %v", prepareErr, prepareFailure)
	}
	if got := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root()); !reflect.DeepEqual(got, beforePrepare) {
		t.Fatalf("preparation failure changed current/trial storage: before=%#v after=%#v", beforePrepare, got)
	}
	if !reflect.DeepEqual(images.images, aliasesBeforePrepare) {
		t.Fatalf("preparation failure changed exact references: before=%#v after=%#v", aliasesBeforePrepare, images.images)
	}

	_, runErr := runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{
		Operation: operation, Store: store, DeploymentDir: dir,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1001, GID: 1002},
	}, providerBuildRunBackend{
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			if input.ValidatedCandidate != nil {
				t.Fatal("incomplete trial cache was offered for reuse")
			}
			return LockedProviderBuildPreparationV1{
				Operation: operation, Store: store, Environment: input.Environment,
				DeploymentDir: dir, DockerPlan: input.DockerPlan,
			}, nil
		},
		execute: func(ctx context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			published, err := publishBuild(ctx, operation, store, BuildPublicationInput{
				Environment: "demo", DeploymentDir: dir, Document: document, Lock: replacement,
			}, images.pendingPublicationImagesV1.backend(store, dir))
			return LockedProviderBuildExecutionResultV1{State: published, Lock: replacement}, err
		},
		discardValidated: func(ctx context.Context, op *deploy.OperationLock, environment, deploymentDir string) error {
			_, found, err := discardValidatedBuildWithBackendV1(
				ctx, op, environment, deploymentDir, validatedRetirementImagesBackendV1(t, images, trialRecord.ImageReference, false),
			)
			if err != nil || !found {
				return errors.Join(fmt.Errorf("trial retirement found=%v", found), err)
			}
			if err := cleanupValidatedBuildStorage(op, store, nil); err != nil {
				return err
			}
			return op.RemoveValidatedBuildV1()
		},
	})
	if runErr != nil {
		t.Fatalf("replacement publication failed with complete replacement closure: %v", runErr)
	}
	currentDigest := operationCurrentDigest(t, operation)
	if currentDigest == trialDigest {
		t.Fatal("replacement publication retained the trial lock as current")
	}
	if _, found, err := operation.ReadBuildLock(currentDigest, registry.ValidateRequirementProfileV1); err != nil || !found {
		t.Fatalf("published replacement lock missing: found=%v error=%v", found, err)
	}
	retained, found, err := operation.ReadValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(retained, trialRecord) {
		t.Fatalf("partial trial retirement lost retryable ownership: found=%v error=%v record=%#v", found, err, retained)
	}
	if images.images[trialRecord.ImageReference] != trialRecord.Image {
		t.Fatal("partial trial retirement removed the exact trial alias")
	}
	if _, found, err := operation.ReadBuildLock(trialDigest, registry.ValidateRequirementProfileV1); err != nil || !found {
		t.Fatalf("partial trial retirement lost the trial lock: found=%v error=%v", found, err)
	}
	if err := discardValidatedBuildWithRetryV1(t.Context(), operation, store, images, trialRecord); err != nil {
		t.Fatalf("trial retirement retry: %v", err)
	}
	if _, found, err := operation.ReadValidatedBuildV1(); err != nil || found {
		t.Fatalf("successful retirement retry retained the trial record: found=%v error=%v", found, err)
	}
	if _, found := images.images[trialRecord.ImageReference]; found {
		t.Fatal("successful retirement retry retained the trial alias")
	}
	if _, found, err := operation.ReadBuildLock(trialDigest, registry.ValidateRequirementProfileV1); err != nil || found {
		t.Fatalf("successful retirement retry retained the trial lock: found=%v error=%v", found, err)
	}
}

func discardValidatedBuildWithRetryV1(ctx context.Context, operation *deploy.OperationLock, store providerstore.Store, images *validatedPublicationImagesV1, expected deploy.ValidatedBuildV1) error {
	pending, err := discardValidatedBuild(ctx, operation, store, "demo", filepath.Dir(filepath.Dir(operation.Path())), func(_ context.Context, image providers.RealizedImageV1, reference, _, _ string) error {
		if image != expected.Image || reference != expected.ImageReference {
			return fmt.Errorf("retry tried to retire a different validated reference")
		}
		if actual, found := images.images[reference]; !found || actual != image {
			return fmt.Errorf("retry validated reference is absent or retargeted")
		}
		delete(images.images, reference)
		return nil
	})
	if err != nil {
		return err
	}
	if pending {
		return fmt.Errorf("retry left validated storage cleanup pending")
	}
	return nil
}

func TestRunLockedProviderBuildV1PreservesTrialAcrossPrecommitRecoveryV1(t *testing.T) {
	stubNoAbandonedBuildReferences(t)
	dir, operation, store, replacement, state := currentBuildFixture(t, true)
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
	trial := validatedBuildStorageVariant(t, store, replacement, "7", "8")
	trial.FinalImage.RootFSSubject = rendererDigest("c")
	trial.RuntimeLayer.Result.RootFSSubject = trial.FinalImage.RootFSSubject
	policy, err := deploy.RuntimePolicyDigestV1(trial.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	trial.ValidationRecord, err = deploy.PublishPrefixValidation(t.Context(), store, deploy.PrefixValidationV1{
		Schema: deploy.PrefixValidationSchemaV1, SubjectRootFS: trial.FinalImage.RootFSSubject,
		Profiles: []providers.ValidationEvidence{}, RuntimePolicy: policy,
		ExposedOutputs: []providers.ExecutableEvidence{},
	})
	if err != nil {
		t.Fatal(err)
	}
	trialDigest, err := operation.PublishBuildLock(trial, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	trialRecord := validatedPublicationRecordV1(t, dir, trial, inputs, 21)
	if err := operation.CommitValidatedBuildV1(trialRecord); err != nil {
		t.Fatal(err)
	}
	images := &validatedPublicationImagesV1{pendingPublicationImagesV1: pendingPublicationImagesV1{
		images: map[string]providers.RealizedImageV1{}, operation: operation, t: t, sequence: 30,
	}}
	images.retain(t, trialRecord, dir)
	currentPairs, err := projectEnvironmentOwnedReferencesV1(*state.Current, replacement, "demo", dir, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range currentPairs {
		images.images[pair.Reference] = pair.Image
	}
	validationPath, err := store.ValidationRecordPath(trial.ValidationRecord)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(validationPath); err != nil {
		t.Fatal(err)
	}
	before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
	aliasesBefore := make(map[string]providers.RealizedImageV1, len(images.images))
	for reference, image := range images.images {
		aliasesBefore[reference] = image
	}
	want := errPendingPublicationFaultV1
	_, runErr := runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{
		Operation: operation, Store: store, DeploymentDir: dir,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1001, GID: 1002},
	}, providerBuildRunBackend{
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			if input.ValidatedCandidate != nil {
				t.Fatal("incomplete trial cache was offered for reuse")
			}
			return LockedProviderBuildPreparationV1{
				Operation: operation, Store: store, Environment: input.Environment,
				DeploymentDir: dir, DockerPlan: input.DockerPlan,
			}, nil
		},
		execute: func(ctx context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			images.fault = "commit"
			_, err := publishBuild(ctx, operation, store, BuildPublicationInput{
				Environment: "demo", DeploymentDir: dir, Document: document, Lock: replacement,
			}, images.pendingPublicationImagesV1.backend(store, dir))
			return LockedProviderBuildExecutionResultV1{}, err
		},
		cleanupFailure: func(_ context.Context, _ LockedProviderBuildPreparationV1) error {
			return images.recover(store, dir)
		},
	})
	if !errors.Is(runErr, want) {
		t.Fatalf("runner error = %v, want injected precommit failure", runErr)
	}
	if got := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root()); !reflect.DeepEqual(got, before) {
		t.Fatalf("precommit recovery changed the trial/current filesystem: before=%#v after=%#v", before, got)
	}
	if !reflect.DeepEqual(images.images, aliasesBefore) {
		t.Fatalf("precommit recovery changed exact trial/current aliases: before=%#v after=%#v", aliasesBefore, images.images)
	}
	retained, found, err := operation.ReadValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(retained, trialRecord) {
		t.Fatalf("precommit recovery lost the independent trial owner: found=%v error=%v record=%#v", found, err, retained)
	}
	if _, found, err := operation.ReadBuildLock(trialDigest, registry.ValidateRequirementProfileV1); err != nil || !found {
		t.Fatalf("precommit recovery lost the trial canonical lock: found=%v error=%v", found, err)
	}
	if _, found, err := operation.ReadPendingBuild(); err != nil || found {
		t.Fatalf("precommit recovery left a pending publication: found=%v error=%v", found, err)
	}
}

func TestRunLockedProviderBuildV1PreservesIncompleteValidationCandidateWhenPreparationFails(t *testing.T) {
	dir, operation, store, lock, state := currentBuildFixture(t, true)
	defer operation.Unlock()
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	overrides := deploy.EmptyPackageOverridesV1(document.Environment.ID)
	inputs, err := ValidatedBuildInputs(document, state.Overlay, overrides, dir, state.Platform)
	if err != nil {
		t.Fatal(err)
	}
	record := deploy.ValidatedBuildV1{
		Schema:          deploy.ValidatedBuildSchemaV1,
		BlueprintDigest: inputs.BlueprintDigest, OverlayDigest: inputs.OverlayDigest,
		PackageOverridesDigest: inputs.PackageOverridesDigest, Platform: inputs.Platform,
		BuildLockDigest: state.Current.BuildLockDigest, Image: lock.FinalImage, ImageReference: state.Current.Reference,
	}
	if err := operation.CommitValidatedBuildV1(record); err != nil {
		t.Fatal(err)
	}
	if _, err := operation.RemoveProviderStore(store); err != nil {
		t.Fatal(err)
	}

	discarded := false
	want := errors.New("incomplete candidate rebuild preparation failed")
	_, err = runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{
		Operation: operation, Store: store, DeploymentDir: dir,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1001, GID: 1002},
	}, providerBuildRunBackend{
		discardValidated: func(context.Context, *deploy.OperationLock, string, string) error {
			discarded = true
			return nil
		},
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			if discarded || input.ValidatedCandidate != nil {
				t.Fatalf("incomplete candidate reached preparation: retired=%v candidate=%#v", discarded, input.ValidatedCandidate)
			}
			return LockedProviderBuildPreparationV1{}, want
		},
		execute: func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			t.Fatal("incomplete candidate failure unexpectedly reached execution")
			return LockedProviderBuildExecutionResultV1{}, nil
		},
	})
	if !errors.Is(err, want) {
		t.Fatalf("runner error = %v, want %v", err, want)
	}
	retained, found, err := operation.ReadValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(retained, record) || discarded {
		t.Fatalf("incomplete candidate owner changed before publication: found=%v discarded=%v error=%v record=%#v", found, discarded, err, retained)
	}
}

func TestRunLockedProviderBuildV1CleansFailedExecutionAndPreservesItsError(t *testing.T) {
	dir, _ := stageProviderBuildRunState(t, false)
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("graph failed")
	cleaned := false

	_, err = runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{
		Operation: operation, Store: store, DeploymentDir: dir,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux},
	}, providerBuildRunBackend{
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			return LockedProviderBuildPreparationV1{
				Operation: input.Operation, Store: input.Store, Environment: input.Environment,
				DeploymentDir: input.DeploymentDir,
			}, nil
		},
		execute: func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			return LockedProviderBuildExecutionResultV1{}, want
		},
		cleanupFailure: func(_ context.Context, preparation LockedProviderBuildPreparationV1) error {
			cleaned = true
			if preparation.Operation != operation || preparation.Store.Root() != store.Root() || preparation.Environment != "demo" || preparation.DeploymentDir != dir {
				t.Fatalf("cleanup preparation = %#v", preparation)
			}
			return nil
		},
	})
	if !errors.Is(err, want) || !cleaned {
		t.Fatalf("error/cleaned = %v/%v", err, cleaned)
	}
}

func TestCleanupFailedProviderBuildV1RemovesOnlyUnreachableObjects(t *testing.T) {
	stubNoAbandonedBuildReferences(t)
	dir, operation, store, lock, _ := currentBuildFixture(t, true)
	defer operation.Unlock()
	dropped, err := store.Publish(t.Context(), "failed.deb", "deb", strings.NewReader("failed candidate"))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := store.NewWorkspace("failed-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanupFailedProviderBuildV1(t.Context(), LockedProviderBuildPreparationV1{
		Operation: operation, Store: store, Environment: "demo", DeploymentDir: dir,
	}); err != nil {
		t.Fatal(err)
	}
	droppedPath, err := store.BlobPath(dropped.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(droppedPath); !os.IsNotExist(err) {
		t.Fatalf("unreachable failed-build blob remains: %v", err)
	}
	if _, err := os.Lstat(workspace); !os.IsNotExist(err) {
		t.Fatalf("failed-build workspace remains: %v", err)
	}
	if _, found, err := operation.ReadBuildLock(operationCurrentDigest(t, operation), registry.ValidateRequirementProfileV1); err != nil || !found {
		t.Fatalf("current build lock was not preserved: found=%v error=%v lock=%#v", found, err, lock)
	}
}

func TestCleanupFailedProviderBuildV1NoCachePreservesImmutableObjects(t *testing.T) {
	stubNoAbandonedBuildReferences(t)
	dir, operation, store, _, _ := currentBuildFixture(t, true)
	defer operation.Unlock()
	dropped, err := store.Publish(t.Context(), "failed.deb", "deb", strings.NewReader("failed candidate"))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := store.NewWorkspace("failed-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanupFailedProviderBuildV1(t.Context(), LockedProviderBuildPreparationV1{
		Operation: operation, Store: store, Environment: "demo", DeploymentDir: dir, NoCache: true,
	}); err != nil {
		t.Fatal(err)
	}
	droppedPath, err := store.BlobPath(dropped.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(droppedPath); err != nil {
		t.Fatalf("no-cache cleanup removed an immutable candidate object: %v", err)
	}
	if _, err := os.Lstat(workspace); !os.IsNotExist(err) {
		t.Fatalf("no-cache cleanup retained a temporary workspace: %v", err)
	}
}

func TestCleanupFailedProviderBuildV1WithoutCurrentRemovesAllBuildObjects(t *testing.T) {
	stubNoAbandonedBuildReferences(t)
	dir, _ := stageProviderBuildRunState(t, false)
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	dropped, err := store.Publish(t.Context(), "failed.deb", "deb", strings.NewReader("failed candidate"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanupFailedProviderBuildV1(t.Context(), LockedProviderBuildPreparationV1{
		Operation: operation, Store: store, Environment: "demo", DeploymentDir: dir,
	}); err != nil {
		t.Fatal(err)
	}
	droppedPath, err := store.BlobPath(dropped.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(droppedPath); !os.IsNotExist(err) {
		t.Fatalf("unpublished failed-build blob remains: %v", err)
	}
}

func operationCurrentDigest(t *testing.T, operation *deploy.OperationLock) canonical.Digest {
	t.Helper()
	state, found, err := operation.ReadStateV1()
	if err != nil || !found || state.Current == nil {
		t.Fatalf("read current state: found=%v error=%v state=%#v", found, err, state)
	}
	return state.Current.BuildLockDigest
}

func TestRunLockedProviderBuildV1RejectsForeignStoreBeforeProviderWork(t *testing.T) {
	dir, _ := stageProviderBuildRunState(t, false)
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	foreignStore, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	called := false
	_, err = runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{
		Operation: operation, Store: foreignStore, DeploymentDir: dir,
	}, providerBuildRunBackend{
		prepare: func(context.Context, LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			called = true
			return LockedProviderBuildPreparationV1{}, nil
		},
		execute: func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			called = true
			return LockedProviderBuildExecutionResultV1{}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "does not belong to the locked deployment") || called {
		t.Fatalf("error/called = %v/%v", err, called)
	}
}

func TestRunProviderBuildV1PassesLocalOverrideLocatorsWithoutObservation(t *testing.T) {
	dir, _ := stageProviderBuildRunState(t, true)
	prepared := false
	executed := false
	_, err := runProviderBuildV1(t.Context(), ProviderBuildRunInputV1{DeploymentDir: dir}, providerBuildRunBackend{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			prepared = true
			return LockedProviderBuildPreparationV1{Operation: input.Operation, Store: input.Store}, nil
		},
		execute: func(_ context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			executed = true
			if len(input.LocalOverrides) != 1 || input.LocalOverrides[0].Distribution != "demo" ||
				input.LocalOverrides[0].HostDir != filepath.Join(dir, "demo") {
				t.Fatalf("local overrides = %#v", input.LocalOverrides)
			}
			return LockedProviderBuildExecutionResultV1{}, nil
		},
	})
	if err != nil || !prepared || !executed {
		t.Fatalf("error/prepared/executed = %v/%v/%v", err, prepared, executed)
	}
	operation, lockErr := deploy.AcquireOperationLock(t.Context(), dir)
	if lockErr != nil {
		t.Fatal("rejected build retained operation lock:", lockErr)
	}
	if unlockErr := operation.Unlock(); unlockErr != nil {
		t.Fatal(unlockErr)
	}
}

func TestRunProviderBuildV1DoesNotObserveLocalOverridePathOnExactReuse(t *testing.T) {
	dir, _ := stageProviderBuildRunState(t, true)
	if err := os.RemoveAll(filepath.Join(dir, "demo")); err != nil {
		t.Fatal(err)
	}
	executed := false
	_, err := runProviderBuildV1(t.Context(), ProviderBuildRunInputV1{
		DeploymentDir: dir,
		Automatic:     true,
	}, providerBuildRunBackend{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			return LockedProviderBuildPreparationV1{Operation: input.Operation, Store: input.Store, Reused: true}, nil
		},
		execute: func(_ context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			executed = true
			if len(input.LocalOverrides) != 1 || input.LocalOverrides[0].HostDir != filepath.Join(dir, "demo") {
				t.Fatalf("local overrides = %#v", input.LocalOverrides)
			}
			return LockedProviderBuildExecutionResultV1{Reused: true}, nil
		},
	})
	if err != nil || !executed {
		t.Fatalf("error/executed = %v/%v", err, executed)
	}
}

func TestRunProviderBuildV1VerifiesExactReuseBeforeExecution(t *testing.T) {
	dir, _ := stageProviderBuildRunState(t, false)
	current := CurrentBuild{Generation: deploy.EnvironmentGenerationState{
		Reference: "reploy/test:g-current",
	}}
	order := []string{}
	result, err := runProviderBuildV1(t.Context(), ProviderBuildRunInputV1{
		DeploymentDir: dir,
		Verify:        true,
	}, providerBuildRunBackend{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			order = append(order, "prepare")
			return LockedProviderBuildPreparationV1{
				Operation: input.Operation, Store: input.Store,
				DeploymentDir: input.DeploymentDir, Current: &current, Reused: true,
			}, nil
		},
		planCurrent: func(input CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error) {
			order = append(order, "plan")
			if !reflect.DeepEqual(input.Current, current) || input.DeploymentDir != dir {
				t.Fatalf("current runtime input = %#v", input)
			}
			return CurrentRuntimePlanV1{}, nil
		},
		verifyCurrent: func(_ context.Context, input CurrentBuildVerificationInputV1) (CurrentBuildVerificationResultV1, error) {
			order = append(order, "verify")
			if !reflect.DeepEqual(input.Current, current) {
				t.Fatalf("verification input = %#v", input)
			}
			return CurrentBuildVerificationResultV1{}, nil
		},
		execute: func(_ context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			order = append(order, "execute")
			if !input.Preparation.Reused || input.RunOptions.NoCache {
				t.Fatalf("verified execution input = %#v", input)
			}
			return LockedProviderBuildExecutionResultV1{Reused: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{"prepare", "plan", "verify", "execute"}
	if !result.Reused ||
		result.VerificationFailure != "" ||
		!reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("result=%#v order=%v", result, order)
	}
}

func TestRunProviderBuildV1VerifiesValidatedCandidateBeforePromotion(t *testing.T) {
	dir, _ := stageProviderBuildRunState(t, false)
	candidateCurrent := CurrentBuild{Generation: deploy.EnvironmentGenerationState{
		Reference: "reploy/test:v-validated",
	}}
	candidate := ValidatedBuildCandidateV1{Current: candidateCurrent}
	order := []string{}
	result, err := runProviderBuildV1(t.Context(), ProviderBuildRunInputV1{
		DeploymentDir: dir,
		Verify:        true,
	}, providerBuildRunBackend{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			order = append(order, "prepare")
			return LockedProviderBuildPreparationV1{
				Operation: input.Operation, Store: input.Store,
				DeploymentDir: input.DeploymentDir, Reused: true, ReusedCandidate: true,
				ValidatedCandidate: &candidate,
			}, nil
		},
		planCurrent: func(input CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error) {
			order = append(order, "plan")
			if !reflect.DeepEqual(input.Current, candidateCurrent) {
				t.Fatalf("validated candidate runtime input = %#v", input)
			}
			return CurrentRuntimePlanV1{}, nil
		},
		verifyCurrent: func(_ context.Context, input CurrentBuildVerificationInputV1) (CurrentBuildVerificationResultV1, error) {
			order = append(order, "verify")
			if !reflect.DeepEqual(input.Current, candidateCurrent) {
				t.Fatalf("validated candidate verification input = %#v", input)
			}
			return CurrentBuildVerificationResultV1{}, nil
		},
		execute: func(_ context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			order = append(order, "execute")
			if !input.Preparation.ReusedCandidate ||
				input.Preparation.ValidatedCandidate != &candidate ||
				input.RunOptions.NoCache {
				t.Fatalf("validated candidate execution input = %#v", input)
			}
			return LockedProviderBuildExecutionResultV1{Reused: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{"prepare", "plan", "verify", "execute"}
	if !result.Reused || !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("result=%#v order=%v", result, order)
	}
}

func TestRunProviderBuildV1RebuildsAfterReuseVerificationFailure(t *testing.T) {
	dir, _ := stageProviderBuildRunState(t, false)
	current := CurrentBuild{}
	wantVerification := errors.New("provider artifact digest changed")
	prepareCalls := 0
	var progress strings.Builder
	result, err := runProviderBuildV1(t.Context(), ProviderBuildRunInputV1{
		DeploymentDir: dir,
		Verify:        true,
		Progress:      &progress,
	}, providerBuildRunBackend{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			prepareCalls++
			if prepareCalls == 1 {
				if input.NoCache {
					t.Fatal("initial reuse preparation bypassed cache")
				}
				return LockedProviderBuildPreparationV1{
					Operation: input.Operation, Store: input.Store,
					DeploymentDir: input.DeploymentDir, Current: &current, Reused: true,
				}, nil
			}
			if !input.NoCache || input.ValidatedCandidate != nil {
				t.Fatalf("rebuild preparation input = %#v", input)
			}
			return LockedProviderBuildPreparationV1{
				Operation: input.Operation, Store: input.Store,
				DeploymentDir: input.DeploymentDir,
			}, nil
		},
		planCurrent: func(CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error) {
			return CurrentRuntimePlanV1{}, nil
		},
		verifyCurrent: func(context.Context, CurrentBuildVerificationInputV1) (CurrentBuildVerificationResultV1, error) {
			return CurrentBuildVerificationResultV1{}, wantVerification
		},
		execute: func(_ context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			if input.Preparation.Reused || !input.RunOptions.NoCache {
				t.Fatalf("verification failure reused cache: %#v", input)
			}
			return LockedProviderBuildExecutionResultV1{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepareCalls != 2 ||
		result.Reused ||
		result.VerificationFailure != wantVerification.Error() ||
		!strings.Contains(progress.String(), "verification failed; rebuilding instead") {
		t.Fatalf(
			"prepare=%d result=%#v progress=%q",
			prepareCalls,
			result,
			progress.String(),
		)
	}
}

func TestRunLockedProviderBuildV1RoutesUnchangedLocalSourceThroughFreshWheelBuild(t *testing.T) {
	workspaceRoot := t.TempDir()
	sourceDir := filepath.Join(workspaceRoot, "demo")
	if err := os.Mkdir(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "pyproject.toml"), []byte("[project]\nname='demo-server'\nversion='1.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, manifest, err := ObservePythonSourceManifest(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	fixture := newPreparedPythonGraphReuseFixtureWithManifest(t, manifest)
	deploymentDir := filepath.Dir(filepath.Dir(fixture.store.Root()))
	operation, err := deploy.AcquireOperationLock(t.Context(), deploymentDir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	lockDigest, err := operation.PublishBuildLock(fixture.lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(fixture.lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	generation := deploy.EnvironmentGenerationState{
		Reference: "reploy/test:g-current", ImageDigest: fixture.lock.FinalImage.Digest,
		RootFSSubject: fixture.lock.FinalImage.RootFSSubject, BuildLockDigest: lockDigest,
		Platform: fixture.lock.Platform, RuntimePolicyDigest: policyDigest,
	}
	document := providerBuildRunWorkspaceDocument(fixture.request.Platform)
	resolved, err := blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	state := deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: resolved, BlueprintSource: "blueprint",
		Platform: fixture.request.Platform, Overlay: deploy.EmptyRequestOverlayV1(), Current: &generation,
		Staging: &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1},
	}
	if err := operation.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	if err := operation.CommitPackageOverridesV1(localPythonPackageOverrides(
		"demo", "demo-server", sourceDir,
	)); err != nil {
		t.Fatal(err)
	}
	prepared := false
	executed := false
	_, err = runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{
		Operation: operation, Store: fixture.store, DeploymentDir: deploymentDir,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1001, GID: 1002},
	}, providerBuildRunBackend{
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			prepared = true
			if len(input.Sources) != 0 {
				t.Fatalf("explicit build reused source identities = %#v", input.Sources)
			}
			return LockedProviderBuildPreparationV1{Operation: operation, Store: fixture.store}, nil
		},
		execute: func(_ context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			executed = true
			if len(input.SourceWheels) != 0 || len(input.LocalOverrides) != 1 ||
				input.LocalOverrides[0].Distribution != "demo-server" {
				t.Fatalf("execution source inputs = %#v/%#v", input.SourceWheels, input.LocalOverrides)
			}
			return LockedProviderBuildExecutionResultV1{}, nil
		},
	})
	if err != nil || !prepared || !executed {
		t.Fatalf("error/prepared/executed = %v/%v/%v", err, prepared, executed)
	}

	prepared = false
	executed = false
	_, err = runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{
		Operation: operation, Store: fixture.store, DeploymentDir: deploymentDir,
		Automatic: true,
		Runtime:   StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1001, GID: 1002},
	}, providerBuildRunBackend{
		prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			prepared = true
			if !reflect.DeepEqual(input.Sources, fixture.request.SourceCandidates) {
				t.Fatalf("automatic reuse sources = %#v", input.Sources)
			}
			return LockedProviderBuildPreparationV1{Operation: operation, Store: fixture.store, Reused: true}, nil
		},
		execute: func(_ context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			executed = true
			if len(input.SourceWheels) != 0 || len(input.LocalOverrides) != 1 {
				t.Fatalf("automatic execution source inputs = %#v/%#v", input.SourceWheels, input.LocalOverrides)
			}
			return LockedProviderBuildExecutionResultV1{Reused: true}, nil
		},
	})
	if err != nil || !prepared || !executed {
		t.Fatalf("automatic error/prepared/executed = %v/%v/%v", err, prepared, executed)
	}
}

func TestRunProviderBuildV1RejectsInvalidRuntimeContextBeforeProviderWork(t *testing.T) {
	dir, _ := stageProviderBuildRunState(t, false)
	called := false
	_, err := runProviderBuildV1(t.Context(), ProviderBuildRunInputV1{
		DeploymentDir: dir,
		Runtime:       StagedProviderBuildRuntimeV1{Host: blueprint.HostOS("plan9")},
	}, providerBuildRunBackend{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
		prepare: func(context.Context, LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
			called = true
			return LockedProviderBuildPreparationV1{}, nil
		},
		execute: func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			called = true
			return LockedProviderBuildExecutionResultV1{}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported Docker host") || called {
		t.Fatalf("error/called = %v/%v", err, called)
	}
	operation, lockErr := deploy.AcquireOperationLock(t.Context(), dir)
	if lockErr != nil {
		t.Fatal("runtime-plan failure retained operation lock:", lockErr)
	}
	if unlockErr := operation.Unlock(); unlockErr != nil {
		t.Fatal(unlockErr)
	}
}

func TestStagedProviderBuildRuntimeV1MapsSupportedHosts(t *testing.T) {
	tests := []struct {
		goos string
		host blueprint.HostOS
	}{
		{goos: "linux", host: blueprint.HostLinux},
		{goos: "darwin", host: blueprint.HostMacOS},
		{goos: "windows", host: blueprint.HostWindows},
	}
	for _, test := range tests {
		t.Run(test.goos, func(t *testing.T) {
			got, err := stagedProviderBuildRuntimeV1(test.goos, 501, 20, []int{44, 20, 33, 44})
			if err != nil {
				t.Fatal(err)
			}
			wantGroups := []uint32{33, 44}
			if test.goos == "windows" {
				wantGroups = []uint32{}
			}
			want := StagedProviderBuildRuntimeV1{Host: test.host, UID: 501, GID: 20, SupplementaryGIDs: wantGroups}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("runtime = %#v, want %#v", got, want)
			}
		})
	}
	if _, err := stagedProviderBuildRuntimeV1("windows", -1, -1, nil); err == nil || !strings.Contains(err.Error(), "mapped non-root") {
		t.Fatalf("Windows runtime identity error = %v", err)
	}
	if _, err := stagedProviderBuildRuntimeV1("plan9", 1, 2, nil); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error = %v", err)
	}
}

func TestWindowsSIDRuntimeIdentityV1IsStableNonRootAndSIDSpecific(t *testing.T) {
	firstUID, firstGID, err := windowsSIDRuntimeIdentityV1("S-1-5-21-100-200-300-1001")
	if err != nil {
		t.Fatal(err)
	}
	repeatedUID, repeatedGID, err := windowsSIDRuntimeIdentityV1("s-1-5-21-100-200-300-1001")
	if err != nil {
		t.Fatal(err)
	}
	otherUID, otherGID, err := windowsSIDRuntimeIdentityV1("S-1-5-21-100-200-300-1002")
	if err != nil {
		t.Fatal(err)
	}
	if firstUID < windowsRuntimeIDMinimumV1 || firstUID != firstGID || firstUID != repeatedUID || firstGID != repeatedGID {
		t.Fatalf("stable SID mapping = %d:%d then %d:%d", firstUID, firstGID, repeatedUID, repeatedGID)
	}
	canonicalUID, canonicalGID, err := windowsSIDRuntimeIdentityV1("S-01-005-021-0100-0200-0300-01001")
	if err != nil || canonicalUID != firstUID || canonicalGID != firstGID {
		t.Fatalf("canonical SID mapping = %d:%d, %v", canonicalUID, canonicalGID, err)
	}
	if otherUID != otherGID || otherUID == firstUID {
		t.Fatalf("distinct SID mapping = %d:%d, first %d:%d", otherUID, otherGID, firstUID, firstGID)
	}
	for _, malformed := range []string{"", "not-a-sid", "S-1-5", "S-1-X-21"} {
		if _, _, err := windowsSIDRuntimeIdentityV1(malformed); err == nil {
			t.Fatalf("malformed SID %q unexpectedly accepted", malformed)
		}
	}
}

func stageProviderBuildRunState(t *testing.T, workspace bool) (string, blueprint.Document) {
	t.Helper()
	dir := t.TempDir()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	document := blueprint.Document{
		Blueprint: blueprint.Metadata{Compatibility: blueprint.Compatibility{Platforms: []blueprint.Platform{platform}}},
		Environment: blueprint.Environment{
			ID:   "demo",
			Base: blueprint.BaseComponent{Image: "debian:13", Exports: map[string]blueprint.BaseExecutableExport{}},
		},
	}
	if workspace {
		if err := os.Mkdir(filepath.Join(dir, "demo"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "demo", "pyproject.toml"), []byte("[project]\nname='demo'\nversion='1.0'\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var stageErr error
	if workspace {
		_, stageErr = deploy.SetStagedDesiredStateV1(t.Context(), dir, document, platform, nil, "blueprint", false)
	} else {
		_, stageErr = deploy.SetDesiredStateV1(t.Context(), dir, document, platform, nil)
	}
	if stageErr != nil {
		t.Fatal(stageErr)
	}
	if workspace {
		operation, err := deploy.AcquireOperationLock(t.Context(), dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := operation.CommitPackageOverridesV1(localPythonPackageOverrides(
			"demo", "demo", filepath.Join(dir, "demo"),
		)); err != nil {
			_ = operation.Unlock()
			t.Fatal(err)
		}
		if err := operation.Unlock(); err != nil {
			t.Fatal(err)
		}
	}
	return dir, document
}

func providerBuildRunWorkspaceDocument(platform blueprint.Platform) blueprint.Document {
	return blueprint.Document{
		Blueprint: blueprint.Metadata{Compatibility: blueprint.Compatibility{Platforms: []blueprint.Platform{platform}}},
		Environment: blueprint.Environment{
			ID: "demo",
			Base: blueprint.BaseComponent{
				Image: "debian:13", Exports: map[string]blueprint.BaseExecutableExport{
					"python": {Executable: "/usr/bin/python3"},
				},
			},
			Applications: map[string]blueprint.Application{
				"application": {
					Packages: blueprint.ApplicationPackages{Python: &blueprint.PythonComponent{
						Interpreter:  blueprint.CommandRequirement{Command: "python", Version: ">=3.11", Supplier: "base"},
						Requirements: []string{"demo-server==1.0"},
					}},
					Options: map[string]blueprint.ApplicationOption{},
				},
			},
		},
	}
}

func localPythonPackageOverrides(environmentID string, distribution string, sourceDir string) deploy.PackageOverridesV1 {
	return deploy.PackageOverridesV1{Environment: deploy.PackageOverridesEnvironmentV1{
		ID:   environmentID,
		Vars: map[string]any{},
		PackageOverrides: map[string]map[string]deploy.PackageOverrideChoiceV1{
			"python": {distribution: {Path: sourceDir}},
		},
	}}
}
