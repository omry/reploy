package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

// Only Docker reference effects are substituted; admission, canonical records,
// the terminal guard, native operation lock and directory isolation are real.
func stagedRetirementFixtureV1(t *testing.T) (string, *validatedPublicationImagesV1, []string, stagedDeploymentRemoveBackendV1) {
	t.Helper()
	dir, store, lock, record, images := validatedRetirementFixtureV1(t)
	current := record
	current.ImageReference = fixedPublicationReferences(t, dir, 51).Generation
	commitValidatedTestCurrentV1(t, images, dir, lock, current)
	state, _, err := images.operation.ReadStateV1()
	if err != nil {
		t.Fatal(err)
	}
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	state.BlueprintSource = "retained staged source"
	if err := images.operation.CommitStateV1(state.Current, state); err != nil {
		t.Fatal(err)
	}
	pairs, err := projectEnvironmentOwnedReferencesV1(*state.Current, lock, "demo", dir, acceptProviderProfileOwnerForCutoverV1)
	if err != nil {
		t.Fatal(err)
	}
	references := []string{pairs[0].Reference, pairs[1].Reference, record.ImageReference, record.Companion.Reference}
	for _, pair := range pairs {
		images.images[pair.Reference] = pair.Image
	}
	backend := testStagedRemovalBackendV1(store)
	backend.acquire = func(ctx context.Context, path string) (*deploy.OperationLock, error) {
		operation, err := deploy.AcquireExistingOperationLock(ctx, path)
		if err == nil {
			images.operation = operation
		}
		return operation, err
	}
	retirement := validatedRetirementImagesBackendV1(t, images, "", false)
	bindStagedRetirementBackendV1(&backend, retirement)
	if err := images.operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	return dir, images, references, backend
}

func bindStagedRetirementBackendV1(backend *stagedDeploymentRemoveBackendV1, retirement validatedRetirementBackendV1) {
	backend.discardValidated = func(ctx context.Context, op *deploy.OperationLock, env, dir string) error {
		_, _, err := discardValidatedBuildWithBackendV1(ctx, op, env, dir, retirement)
		return err
	}
	backend.removeReference, backend.removeCompanion = retirement.removeReference, retirement.removeCompanion
}

func assertStagedRetirementAuthorityV1(t *testing.T, dir string, guarded, admitted bool) {
	t.Helper()
	operation, err := deploy.AcquireExistingOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	state, found, err := operation.ReadStateV1()
	if err != nil || !found || state.TerminalRemoval != guarded || state.Current == nil || state.Staging == nil {
		t.Fatalf("retained staging authority: %v %v %#v", found, err, state)
	}
	if _, found, err := operation.ReadBuildLock(state.Current.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil || !found {
		t.Fatalf("current lock authority lost: %v %v", found, err)
	}
	if _, found, err := operation.ReadValidatedBuildV1(); err != nil || !found {
		t.Fatalf("validated inventory lost: %v %v", found, err)
	}
	if guarded && operation.RequireWritable() == nil {
		t.Fatal("ordinary writer admitted into retained removal")
	}
	if admitted {
		queue, found, err := operation.ReadLiveRunQueueV1()
		if err != nil || !found || len(queue.Runs) == 0 {
			t.Fatalf("admission erased before retirement: %v %v %#v", found, err, queue)
		}
		leases, err := filepath.Glob(filepath.Join(dir, ".reploy", "*.lease"))
		if err != nil || len(leases) == 0 {
			t.Fatalf("lease ownership file erased: %v %v", leases, err)
		}
	}
}

func forbidStagedRetirementReadmissionV1(t *testing.T, backend *stagedDeploymentRemoveBackendV1) {
	t.Helper()
	backend.admit = func(context.Context, string, *deploy.OperationLock, ControlAdmissionInputV1) (AdmittedControlV1, error) {
		t.Fatal("guarded retry admitted another control operation")
		return AdmittedControlV1{}, nil
	}
	backend.recoverPending = func(context.Context, *deploy.OperationLock, providerstore.Store, *deploy.EnvironmentGenerationState, string, string) (bool, error) {
		t.Fatal("guarded retry entered ordinary publication recovery")
		return false, nil
	}
}

func TestStagedRetirementRetriesEveryOwnedReferenceV1(t *testing.T) {
	for index := 0; index < 4; index++ {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("reference=%d/after=%v", index, after), func(t *testing.T) {
				dir, images, references, backend := stagedRetirementFixtureV1(t)
				independent := fixedPublicationReferences(t, dir, 83).Generation
				images.images[independent] = images.images[references[0]]
				bindStagedRetirementBackendV1(&backend, validatedRetirementImagesBackendV1(t, images, references[index], after))
				input := StagedDeploymentRemoveInputV1{DeploymentDir: dir}
				for attempt := 0; attempt < 2; attempt++ {
					_, err := removeStagedDeploymentV1(t.Context(), input, backend)
					if !errors.Is(err, errPendingPublicationFaultV1) {
						t.Fatalf("partial removal: %v", err)
					}
					assertStagedRetirementAuthorityV1(t, dir, true, true)
					forbidStagedRetirementReadmissionV1(t, &backend)
				}
				bindStagedRetirementBackendV1(&backend, validatedRetirementImagesBackendV1(t, images, "", false))
				if _, err := removeStagedDeploymentV1(t.Context(), input, backend); err != nil {
					t.Fatal(err)
				}
				if len(images.images) != 1 {
					t.Fatalf("retained owned aliases or changed independent ownership: %#v", images.images)
				}
				if _, found := images.images[independent]; !found {
					t.Fatal("independent same-image alias was removed")
				}
				if _, err := os.Lstat(dir); !os.IsNotExist(err) {
					t.Fatalf("original directory remains: %v", err)
				}
			})
		}
	}
}

func TestStagedRetirementGuardFailuresRetainAdmissionV1(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprintf("after=%v", after), func(t *testing.T) {
			dir, images, _, backend := stagedRetirementFixtureV1(t)
			backend.guard = func(op *deploy.OperationLock, state deploy.StateV1) error {
				if after {
					if err := guardStagedRemovalV1(op, state); err != nil {
						return err
					}
				}
				return errPendingPublicationFaultV1
			}
			if _, err := removeStagedDeploymentV1(t.Context(), StagedDeploymentRemoveInputV1{DeploymentDir: dir}, backend); !errors.Is(err, errPendingPublicationFaultV1) {
				t.Fatalf("guard failure: %v", err)
			}
			if images.effects != 0 || len(images.images) != 4 {
				t.Fatal("uncertain guard granted image retirement")
			}
			assertStagedRetirementAuthorityV1(t, dir, after, true)
			backend.guard = guardStagedRemovalV1
			if after {
				forbidStagedRetirementReadmissionV1(t, &backend)
			}
			if _, err := removeStagedDeploymentV1(t.Context(), StagedDeploymentRemoveInputV1{DeploymentDir: dir}, backend); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStagedRetirementValidatesWholeClosureBeforeEffectsV1(t *testing.T) {
	for _, corrupt := range []string{"current-lock", "validated-owner"} {
		t.Run(corrupt, func(t *testing.T) {
			dir, images, _, backend := stagedRetirementFixtureV1(t)
			op, err := deploy.AcquireExistingOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			state, _, err := op.ReadStateV1()
			if err != nil {
				t.Fatal(err)
			}
			if corrupt == "current-lock" {
				if err := op.RemoveBuildLock(state.Current.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil {
					t.Fatal(err)
				}
			} else {
				record, _, err := op.ReadValidatedBuildV1()
				if err != nil {
					t.Fatal(err)
				}
				record.Companion.Reference = "foreign:owner"
				if err := op.CommitValidatedBuildV1(record); err != nil {
					t.Fatal(err)
				}
			}
			if err := op.Unlock(); err != nil {
				t.Fatal(err)
			}
			backend.guard = func(*deploy.OperationLock, deploy.StateV1) error {
				t.Fatal("invalid closure reached terminal guard")
				return nil
			}
			backend.stopOwned = func(context.Context, *deploy.OperationLock, deploy.StateV1, string, RunOptions) error {
				t.Fatal("invalid closure stopped workload")
				return nil
			}
			if _, err := removeStagedDeploymentV1(t.Context(), StagedDeploymentRemoveInputV1{DeploymentDir: dir}, backend); err == nil {
				t.Fatal("invalid retained closure authorized removal")
			}
			if images.effects != 0 || len(images.images) != 4 {
				t.Fatal("invalid closure granted partial retirement")
			}
		})
	}
}

func TestStagedRetirementIsolationFailuresPreserveRetiredAuthorityV1(t *testing.T) {
	for _, failure := range []string{"occupied", "moved-error", "erase-error"} {
		t.Run(failure, func(t *testing.T) {
			dir, images, _, backend := stagedRetirementFixtureV1(t)
			tombstone := dir + ".terminal"
			t.Cleanup(func() { _ = os.RemoveAll(tombstone) })
			backend.reserve = func(string) (string, error) { return tombstone, nil }
			backend.isolate = func(op *deploy.OperationLock, destination string) (bool, error) {
				if len(images.images) != 0 {
					t.Fatal("directory isolation preceded paired retirement")
				}
				if failure == "occupied" {
					if err := os.Mkdir(destination, 0700); err != nil {
						t.Fatal(err)
					}
				}
				moved, err := op.IsolateOriginalDirectory(destination)
				if failure == "moved-error" && err == nil {
					return moved, errPendingPublicationFaultV1
				}
				return moved, err
			}
			backend.removeAll = func(path string) error {
				if failure != "erase-error" {
					t.Fatal("uncertain isolation reached directory deletion")
				}
				if len(images.images) != 0 {
					t.Fatal("deletion preceded image retirement")
				}
				return errPendingPublicationFaultV1
			}
			if _, err := removeStagedDeploymentV1(t.Context(), StagedDeploymentRemoveInputV1{DeploymentDir: dir}, backend); err == nil {
				t.Fatal("isolation failure was ignored")
			}
			retained := tombstone
			if failure == "occupied" {
				retained = dir
			}
			assertStagedRetirementAuthorityV1(t, retained, true, false)
			if failure == "occupied" {
				if err := os.Remove(tombstone); err != nil {
					t.Fatal(err)
				}
				forbidStagedRetirementReadmissionV1(t, &backend)
				backend.isolate = func(op *deploy.OperationLock, destination string) (bool, error) {
					return op.IsolateOriginalDirectory(destination)
				}
				backend.removeAll = os.RemoveAll
				if _, err := removeStagedDeploymentV1(t.Context(), StagedDeploymentRemoveInputV1{DeploymentDir: dir}, backend); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := RemoveStagedDeploymentV1(t.Context(), StagedDeploymentRemoveInputV1{DeploymentDir: dir}); err == nil {
					t.Fatal("missing original directory admitted removal")
				}
				if _, err := os.Lstat(dir); !os.IsNotExist(err) {
					t.Fatalf("missing original directory recreated: %v", err)
				}
			}
		})
	}
}

func TestStagedRetirementWindowsReleasedLockRejectsNoCacheWriterV1(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows kernel release window")
	}
	dir, images, _, backend := stagedRetirementFixtureV1(t)
	tombstone := dir + ".occupied"
	t.Cleanup(func() { _ = os.RemoveAll(tombstone) })
	backend.reserve = func(string) (string, error) { return tombstone, nil }
	backend.isolate = func(op *deploy.OperationLock, destination string) (bool, error) {
		if err := os.Mkdir(destination, 0700); err != nil {
			t.Fatal(err)
		}
		moved, err := op.IsolateOriginalDirectory(destination)
		if moved || err == nil {
			t.Fatal("occupied isolation unexpectedly succeeded")
		}
		// The real Windows isolation released its operation lock before attempting
		// the non-replacing move. The original guard must still exclude this writer.
		_, buildErr := RunProviderBuildV1(t.Context(), ProviderBuildRunInputV1{DeploymentDir: dir, NoCache: true, Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostWindows, UID: 1001, GID: 1002}})
		if buildErr == nil {
			t.Fatal("no-cache build admitted after native lock release")
		}
		assertStagedRetirementAuthorityV1(t, dir, true, false)
		if len(images.images) != 0 {
			t.Fatal("lock release preceded paired retirement")
		}
		return moved, err
	}
	if _, err := removeStagedDeploymentV1(t.Context(), StagedDeploymentRemoveInputV1{DeploymentDir: dir}, backend); err == nil {
		t.Fatal("occupied removal succeeded")
	}
	if err := os.Remove(tombstone); err != nil {
		t.Fatal(err)
	}
	forbidStagedRetirementReadmissionV1(t, &backend)
	backend.isolate = func(op *deploy.OperationLock, destination string) (bool, error) {
		return op.IsolateOriginalDirectory(destination)
	}
	if _, err := removeStagedDeploymentV1(t.Context(), StagedDeploymentRemoveInputV1{DeploymentDir: dir}, backend); err != nil {
		t.Fatal(err)
	}
}

func TestStagedRetirementPublicDefaultsRecoverValidatedIntentV1(t *testing.T) {
	dir, images, references, _ := stagedRetirementFixtureV1(t)
	images.restart(t, dir)
	record, _, err := images.operation.ReadValidatedBuildV1()
	if err != nil {
		t.Fatal(err)
	}
	lock, _, err := images.operation.ReadBuildLock(record.BuildLockDigest, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	document := publicationInput(t, dir, lock).Document
	overrides := deploy.EmptyPackageOverridesV1("demo")
	overrides.Environment.Base = &deploy.BaseImageOverrideV1{Image: lock.Base.AuthorReference}
	overrides.Environment.PackageOverrides["python"] = map[string]deploy.PackageOverrideChoiceV1{"demo-server": {Path: "."}}
	inputs, err := ValidatedBuildInputs(document, lock.Overlay, overrides, dir, lock.Platform)
	if err != nil {
		t.Fatal(err)
	}
	lock.PackageOverrides = inputs.PackageOverrides
	images.fault, images.after = "intent", true
	if _, err := publishValidatedBuild(t.Context(), images.operation, store, "demo", dir, lock, inputs, images.backend(dir)); !errors.Is(err, errPendingPublicationFaultV1) {
		t.Fatalf("pending validated intent: %v", err)
	}
	intent, found, err := images.operation.ReadPendingValidatedBuildV1()
	if err != nil || !found {
		t.Fatalf("durable pending intent: %v %v", found, err)
	}
	candidatePairs, err := validatedRecordReferencesV1(intent.Candidate, "demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := images.operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	originalRunner := runDockerOutput
	t.Cleanup(func() { runDockerOutput = originalRunner })
	inspected := map[string]bool{}
	runDockerOutput = func(_ context.Context, args ...string) (string, error) {
		if len(args) < 3 || args[0] != "image" || (args[1] != "inspect" && args[1] != "ls") {
			t.Fatalf("unexpected Docker effect: %v", args)
		}
		reference := args[len(args)-1]
		inspected[reference] = true
		if args[1] == "ls" {
			return "", nil
		}
		// All aliases are already absent: production retirement must remain
		// idempotent, recover the pending candidate, and preserve the older owners
		// until its whole retained cleanup inventory has been validated.
		return "No such image", errors.New("No such image")
	}
	if _, err := RemoveStagedDeploymentV1(t.Context(), StagedDeploymentRemoveInputV1{DeploymentDir: dir}); err != nil {
		t.Fatal(err)
	}
	for _, pair := range candidatePairs {
		if !inspected[pair.ImageReference] {
			t.Fatalf("pending candidate not recovered: %s", pair.ImageReference)
		}
	}
	for _, reference := range references {
		if !inspected[reference] {
			t.Fatalf("retained owner not retired: %s", reference)
		}
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("original directory remains: %v", err)
	}
}
