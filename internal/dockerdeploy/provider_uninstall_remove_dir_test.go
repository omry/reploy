package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

// Retained records, admission, kernel locks, guard and isolation are real.
// The substituted effects are Docker reference operations and host integration.
func installedRetirementFixtureV1(t *testing.T) (string, *validatedPublicationImagesV1, []string, providerUninstallRunBackendV1, *providerUninstallRemoveDirBackendV1) {
	t.Helper()
	dir, images, references, staged := stagedRetirementFixtureV1(t)
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	images.restart(t, dir)
	state, _, err := images.operation.ReadStateV1()
	if err != nil {
		t.Fatal(err)
	}
	state.Staging = nil
	state.Deployment = &deploy.DeploymentStateV1{Schema: deploy.DeploymentStateSchemaV1, Installation: installedBuildPublicationInstallation(dir)}
	if err := images.operation.CommitStateV1(state.Current, state); err != nil {
		t.Fatal(err)
	}
	previous, _, err := images.operation.ReadValidatedBuildV1()
	if err != nil {
		t.Fatal(err)
	}
	lock, _, err := images.operation.ReadBuildLock(previous.BuildLockDigest, acceptProviderProfileOwnerForCutoverV1)
	if err != nil {
		t.Fatal(err)
	}
	inputs := ValidatedBuildInputsV1{BlueprintDigest: previous.BlueprintDigest, OverlayDigest: previous.OverlayDigest, PackageOverridesDigest: previous.PackageOverridesDigest}
	next := validatedPublicationRecordV1(t, dir, lock, inputs, 31)
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
	removal := &providerUninstallRemoveDirBackendV1{
		discardValidated: staged.discardValidated, removeCompanion: staged.removeCompanion,
		guard: staged.guard, complete: staged.complete, removeMarker: staged.removeMarker,
		releaseLease: staged.releaseLease, reserve: reserveProviderUninstallTombstoneV1,
		isolate: staged.isolate, unlock: staged.unlock, removeReference: staged.removeReference,
		finalize: finalizePendingProviderUninstallRemovalV1,
	}
	runner := newProviderUninstallRunBackendV1()
	runner.acquire = staged.acquire
	runner.execute = func(ctx context.Context, op *deploy.OperationLock, plan providerUninstallPlanV1, options RunOptions) error {
		return executeProviderUninstallWithV1(ctx, op, plan, options, func(context.Context, providerUninstallPlanV1, RunOptions) error { return nil })
	}
	runner.removeDeployment = func(ctx context.Context, op *deploy.OperationLock, marker string, lease *deploy.ControlLeaseV1, plan providerUninstallPlanV1, options RunOptions) error {
		return removeProviderUninstallDeploymentWithV1(ctx, op, marker, lease, plan, options, *removal)
	}
	return dir, images, references, runner, removal
}

func bindInstalledRetirementBackendV1(removal *providerUninstallRemoveDirBackendV1, retirement validatedRetirementBackendV1) {
	removal.discardValidated = func(ctx context.Context, op *deploy.OperationLock, env, dir string) error {
		_, _, err := discardValidatedBuildWithBackendV1(ctx, op, env, dir, retirement)
		return err
	}
	removal.removeReference, removal.removeCompanion = retirement.removeReference, retirement.removeCompanion
}

func runInstalledRetirementV1(t *testing.T, dir string, runner providerUninstallRunBackendV1) error {
	t.Helper()
	return runProviderUninstallV1(t.Context(), ProviderUninstallInputV1{DeploymentDir: dir, RemoveDir: true, Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux}}, runner)
}

func assertInstalledRetirementAuthorityV1(t *testing.T, dir string, guarded bool) {
	t.Helper()
	op, err := deploy.AcquireExistingOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Unlock()
	state, found, err := op.ReadStateV1()
	if err != nil || !found || state.TerminalRemoval != guarded || state.Deployment == nil || state.Current == nil {
		t.Fatalf("retained installed authority: %v %v %#v", found, err, state)
	}
	if _, found, err := op.ReadBuildLock(state.Current.BuildLockDigest, acceptProviderProfileOwnerForCutoverV1); err != nil || !found {
		t.Fatalf("missing installed lock: %v %v", found, err)
	}
	if guarded && op.RequireWritable() == nil {
		t.Fatal("ordinary writer bypassed installed guard")
	}
}

func forbidInstalledReadmissionV1(t *testing.T, runner *providerUninstallRunBackendV1) {
	t.Helper()
	runner.admit = func(context.Context, string, *deploy.OperationLock, ControlAdmissionInputV1) (AdmittedControlV1, error) {
		t.Fatal("guarded removal readmitted")
		return AdmittedControlV1{}, nil
	}
	runner.recoverPending = func(context.Context, *deploy.OperationLock, string) error {
		t.Fatal("guarded removal recovered publication")
		return nil
	}
	runner.execute = func(context.Context, *deploy.OperationLock, providerUninstallPlanV1, RunOptions) error {
		t.Fatal("guarded removal repeated host mutation")
		return nil
	}
}

func TestInstalledRetirementRetriesEveryPairBeforeDirectoryRemovalV1(t *testing.T) {
	for index := 0; index < 6; index++ {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("reference=%d/after=%v", index, after), func(t *testing.T) {
				dir, images, references, runner, removal := installedRetirementFixtureV1(t)
				independent := fixedPublicationReferences(t, t.TempDir(), 83).Generation
				images.images[independent] = images.images[references[0]]
				bindInstalledRetirementBackendV1(removal, validatedRetirementImagesBackendV1(t, images, references[index], after))
				for attempt := 0; attempt < 2; attempt++ {
					if err := runInstalledRetirementV1(t, dir, runner); !errors.Is(err, errPendingPublicationFaultV1) {
						t.Fatalf("partial retirement: %v", err)
					}
					assertInstalledRetirementAuthorityV1(t, dir, true)
					leases, err := filepath.Glob(filepath.Join(dir, ".reploy", "*.lease"))
					if err != nil || len(leases) == 0 {
						t.Fatalf("lost admission lease file: %v %v", leases, err)
					}
					forbidInstalledReadmissionV1(t, &runner)
				}
				bindInstalledRetirementBackendV1(removal, validatedRetirementImagesBackendV1(t, images, "", false))
				if err := runInstalledRetirementV1(t, dir, runner); err != nil {
					t.Fatal(err)
				}
				if len(images.images) != 1 {
					t.Fatalf("lost independent owner or retained references: %#v", images.images)
				}
				if _, found := images.images[independent]; !found {
					t.Fatal("source/independent same-image owner removed")
				}
				if _, err := os.Lstat(dir); !os.IsNotExist(err) {
					t.Fatalf("original directory remains: %v", err)
				}
			})
		}
	}
}

func TestInstalledRetirementRetainsGuardAfterBeforeAndAfterWriteFailureV1(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprint(after), func(t *testing.T) {
			dir, images, _, runner, removal := installedRetirementFixtureV1(t)
			removal.guard = func(op *deploy.OperationLock, state deploy.StateV1) error {
				if after {
					if err := guardStagedRemovalV1(op, state); err != nil {
						t.Fatal(err)
					}
				}
				return errPendingPublicationFaultV1
			}
			if err := runInstalledRetirementV1(t, dir, runner); !errors.Is(err, errPendingPublicationFaultV1) {
				t.Fatal(err)
			}
			if images.effects != 0 {
				t.Fatal("uncertain guard permitted retirement")
			}
			assertInstalledRetirementAuthorityV1(t, dir, after)
			removal.guard = guardStagedRemovalV1
			if after {
				forbidInstalledReadmissionV1(t, &runner)
			}
			if err := runInstalledRetirementV1(t, dir, runner); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInstalledRetirementChecksWholeClosureBeforeHostOrAliasEffectsV1(t *testing.T) {
	for _, invalid := range []string{"current-lock", "validated-companion"} {
		t.Run(invalid, func(t *testing.T) {
			dir, images, _, runner, _ := installedRetirementFixtureV1(t)
			images.restart(t, dir)
			state, _, err := images.operation.ReadStateV1()
			if err != nil {
				t.Fatal(err)
			}
			if invalid == "current-lock" {
				if err := images.operation.RemoveBuildLock(state.Current.BuildLockDigest, acceptProviderProfileOwnerForCutoverV1); err != nil {
					t.Fatal(err)
				}
			} else {
				record, _, err := images.operation.ReadValidatedBuildV1()
				if err != nil {
					t.Fatal(err)
				}
				record.Companion.Reference = "foreign:owner"
				if err := images.operation.CommitValidatedBuildV1(record); err != nil {
					t.Fatal(err)
				}
			}
			if err := images.operation.Unlock(); err != nil {
				t.Fatal(err)
			}
			runner.execute = func(context.Context, *deploy.OperationLock, providerUninstallPlanV1, RunOptions) error {
				t.Fatal("invalid closure reached host mutation")
				return nil
			}
			if err := runInstalledRetirementV1(t, dir, runner); err == nil {
				t.Fatal("invalid closure accepted")
			}
			if images.effects != 0 {
				t.Fatal("invalid closure retired aliases")
			}
		})
	}
}

func installedPendingImagesBackendV1(t *testing.T, images *validatedPublicationImagesV1) providerUninstallPendingRemovalBackendV1 {
	t.Helper()
	return providerUninstallPendingRemovalBackendV1{
		acquire: deploy.AcquireExistingOperationLock,
		removeReference: func(context.Context, providers.RealizedImageV1, string, string, string) error {
			t.Fatal("isolated guarded retry deleted an alias")
			return nil
		},
		confirmAbsent: func(_ context.Context, ref string) error {
			if _, found := images.images[ref]; found {
				return fmt.Errorf("owned reference remains: %s", ref)
			}
			return nil
		},
		finalize: finalizePendingProviderUninstallRemovalV1,
	}
}

func TestInstalledRetirementIsolationAndDeterministicRetryV1(t *testing.T) {
	for _, failure := range []string{"occupied", "moved-error", "control-finalize-error"} {
		t.Run(failure, func(t *testing.T) {
			dir, images, references, runner, removal := installedRetirementFixtureV1(t)
			tombstone, err := providerUninstallTombstoneV1(dir)
			if err != nil {
				t.Fatal(err)
			}
			removal.isolate = func(op *deploy.OperationLock, destination string) (bool, error) {
				if len(images.images) != 0 {
					t.Fatal("isolation preceded paired retirement")
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
			removal.finalize = func(root, pending string) error {
				if failure != "control-finalize-error" {
					t.Fatal("uncertain isolation erased state")
				}
				control, err := providerUninstallControlV1(root)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(control, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(filepath.Join(pending, ".reploy"), filepath.Join(control, ".reploy")); err != nil {
					t.Fatal(err)
				}
				return errPendingPublicationFaultV1
			}
			if err := runInstalledRetirementV1(t, dir, runner); err == nil {
				t.Fatal("isolation failure ignored")
			}
			if len(images.images) != 0 {
				t.Fatal("isolation failure retained aliases")
			}
			if failure == "occupied" {
				assertInstalledRetirementAuthorityV1(t, dir, true)
				if err := os.Remove(tombstone); err != nil {
					t.Fatal(err)
				}
				forbidInstalledReadmissionV1(t, &runner)
				removal.isolate = func(op *deploy.OperationLock, destination string) (bool, error) {
					return op.IsolateOriginalDirectory(destination)
				}
				removal.finalize = finalizePendingProviderUninstallRemovalV1
				if err := runInstalledRetirementV1(t, dir, runner); err != nil {
					t.Fatal(err)
				}
			} else {
				pendingBackend := installedPendingImagesBackendV1(t, images)
				images.images[references[0]] = providers.RealizedImageV1{}
				if _, found, err := retryPendingProviderUninstallRemovalWithV1(t.Context(), dir, "demo", pendingBackend); !found || err == nil {
					t.Fatal("unconfirmed alias absence authorized final deletion")
				}
				if _, present := images.images[references[0]]; !present {
					t.Fatal("pending retry deleted an unconfirmed alias")
				}
				delete(images.images, references[0])
				result, found, err := retryPendingProviderUninstallRemovalWithV1(t.Context(), dir, "demo", installedPendingImagesBackendV1(t, images))
				if err != nil || !found || !result.RemovedDirectory {
					t.Fatalf("deterministic retry: %v %v %#v", err, found, result)
				}
			}
		})
	}
}

func TestPendingProviderUninstallRemovalRejectsPendingValidatedIntentWithoutRecordV1(t *testing.T) {
	for _, kind := range []string{"canonical", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			dir, tombstone, current := pendingProviderUninstallRemovalFixture(t)
			operation, err := deploy.AcquireExistingOperationLock(t.Context(), tombstone)
			if err != nil {
				t.Fatal(err)
			}
			if _, found, err := operation.ReadValidatedBuildV1(); err != nil || found {
				t.Fatalf("fixture has a committed validated record: found=%v err=%v", found, err)
			}
			if err := operation.Unlock(); err != nil {
				t.Fatal(err)
			}

			intentPath := filepath.Join(tombstone, ".reploy", "pending-validated-build.json")
			var intent []byte
			if kind == "canonical" {
				candidate := validatedPublicationRecordV1(t, dir, current.Lock, ValidatedBuildInputsV1{
					BlueprintDigest:        current.Lock.BlueprintDigest,
					OverlayDigest:          current.Generation.BuildLockDigest,
					PackageOverridesDigest: current.Generation.BuildLockDigest,
				}, 42)
				intent, err = deploy.EncodePendingValidatedBuildV1(deploy.PendingValidatedBuildV1{
					Schema:    deploy.PendingValidatedBuildSchemaV1,
					Candidate: candidate,
				})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				intent = []byte("{")
			}
			if err := os.WriteFile(intentPath, intent, 0o600); err != nil {
				t.Fatal(err)
			}

			removedReference, finalized := false, false
			_, found, err := retryPendingProviderUninstallRemovalWithV1(t.Context(), dir, "", providerUninstallPendingRemovalBackendV1{
				acquire: deploy.AcquireExistingOperationLock,
				removeReference: func(context.Context, providers.RealizedImageV1, string, string, string) error {
					removedReference = true
					return nil
				},
				finalize: func(string, string) error {
					finalized = true
					return nil
				},
			})
			if !found || err == nil {
				t.Fatalf("retry found=%v err=%v, want pending validated intent rejection", found, err)
			}
			if removedReference || finalized {
				t.Fatalf("pending intent crossed effects: removedReference=%v finalized=%v", removedReference, finalized)
			}
			retainedIntent, err := os.ReadFile(intentPath)
			if err != nil || !bytes.Equal(retainedIntent, intent) {
				t.Fatalf("pending intent changed: bytes=%q err=%v", retainedIntent, err)
			}
			for _, path := range []string{tombstone, filepath.Join(tombstone, ".reploy")} {
				if info, err := os.Lstat(path); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
					t.Fatalf("pending removal root was not retained at %s: %v", path, err)
				}
			}
			control, err := providerUninstallControlV1(dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(control); !os.IsNotExist(err) {
				t.Fatalf("retry moved pending root to control directory: %v", err)
			}
		})
	}
}

func TestInstalledRetirementWithoutRemoveDirKeepsOwnedPairsV1(t *testing.T) {
	dir, images, _, runner, _ := installedRetirementFixtureV1(t)
	err := runProviderUninstallV1(t.Context(), ProviderUninstallInputV1{DeploymentDir: dir, Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux}}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if images.effects != 0 || len(images.images) != 6 {
		t.Fatal("retained-directory uninstall retired aliases")
	}
	images.restart(t, dir)
	defer images.operation.Unlock()
	state, found, err := images.operation.ReadStateV1()
	if err != nil || !found || state.Deployment != nil || state.Current == nil || state.TerminalRemoval {
		t.Fatalf("retained staged authority: %v %v %#v", err, found, state)
	}
}

func TestInstalledRetirementDefaultRecoveryBeforeGuardV1(t *testing.T) {
	dir, images, references, runner, _ := installedRetirementFixtureV1(t)
	images.restart(t, dir)
	record, _, err := images.operation.ReadValidatedBuildV1()
	if err != nil {
		t.Fatal(err)
	}
	lock, _, err := images.operation.ReadBuildLock(record.BuildLockDigest, acceptProviderProfileOwnerForCutoverV1)
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
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	images.fault, images.after = "intent", true
	publication := images.backend(dir)
	publication.newReferences = func(string, string) (EnvironmentImageReferences, error) {
		return fixedPublicationReferences(t, dir, 61), nil
	}
	if _, err := publishValidatedBuild(t.Context(), images.operation, store, "demo", dir, lock, inputs, publication); !errors.Is(err, errPendingPublicationFaultV1) {
		t.Fatalf("pending validated intent: %v", err)
	}
	intent, found, err := images.operation.ReadPendingValidatedBuildV1()
	if err != nil || !found {
		t.Fatalf("pending authority: %v %v", found, err)
	}
	candidate, err := validatedRecordReferencesV1(intent.Candidate, "demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planProviderUninstallV1(providerUninstallPlanningInputV1{Operation: images.operation, DeploymentDir: dir, Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux}, RemoveDir: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := executeProviderUninstallWithV1(t.Context(), images.operation, plan, RunOptions{}, func(context.Context, providerUninstallPlanV1, RunOptions) error {
		t.Fatal("held-lock uninstall crossed pending publication before recovery")
		return nil
	}); err == nil {
		t.Fatal("held-lock uninstall accepted pending validated publication")
	}
	if err := images.operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	originalRunner := runDockerOutput
	t.Cleanup(func() { runDockerOutput = originalRunner })
	inspected := map[string]bool{}
	runDockerOutput = func(_ context.Context, args ...string) (string, error) {
		if len(args) < 3 || args[0] != "image" || (args[1] != "ls" && args[1] != "inspect") {
			t.Fatalf("unexpected Docker effect: %v", args)
		}
		inspected[args[len(args)-1]] = true
		if args[1] == "ls" {
			return "", nil
		}
		return "No such image", errors.New("No such image")
	}
	runner.removeDeployment = removeProviderUninstallDeploymentV1
	if err := runInstalledRetirementV1(t, dir, runner); err != nil {
		t.Fatal(err)
	}
	for _, pair := range candidate {
		references = append(references, pair.ImageReference)
	}
	for _, ref := range references {
		if !inspected[ref] {
			t.Fatalf("owner not recovered/retired before directory removal: %s", ref)
		}
	}
}

func TestInstalledRetirementWindowsReleasedLockRejectsWriterAndOverlappingRemovalV1(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows kernel release window")
	}
	dir, images, _, runner, removal := installedRetirementFixtureV1(t)
	tombstone, err := providerUninstallTombstoneV1(dir)
	if err != nil {
		t.Fatal(err)
	}
	realIsolation := removal.isolate
	removal.isolate = func(op *deploy.OperationLock, destination string) (bool, error) {
		if err := os.Mkdir(destination, 0700); err != nil {
			t.Fatal(err)
		}
		moved, err := op.IsolateOriginalDirectory(destination)
		if moved || err == nil {
			t.Fatal("occupied isolation unexpectedly succeeded")
		}
		_, buildErr := RunProviderBuildV1(t.Context(), ProviderBuildRunInputV1{DeploymentDir: dir, NoCache: true, Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostWindows, UID: 1001, GID: 1002}})
		if buildErr == nil {
			t.Fatal("no-cache writer bypassed durable guard after unlock")
		}
		if err := os.Remove(tombstone); err != nil {
			t.Fatal(err)
		}
		removal.isolate = realIsolation
		if err := runInstalledRetirementV1(t, dir, runner); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "replacement"), []byte("new owner"), 0600); err != nil {
			t.Fatal(err)
		}
		moved, err = op.IsolateOriginalDirectory(destination)
		if moved || err == nil {
			t.Fatal("old descriptor isolated recreated deployment")
		}
		return moved, err
	}
	if err := runInstalledRetirementV1(t, dir, runner); err == nil {
		t.Fatal("overlapping removal ignored original-object change")
	}
	if _, err := os.Stat(filepath.Join(dir, "replacement")); err != nil {
		t.Fatal("overlap deleted recreated deployment", err)
	}
	if len(images.images) != 0 {
		t.Fatal("native removal retained aliases")
	}
}

func TestReserveProviderUninstallTombstoneUsesAbsentDeterministicSiblingPath(t *testing.T) {
	dir := t.TempDir()
	tombstone, err := reserveProviderUninstallTombstoneV1(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(filepath.Dir(dir), "."+filepath.Base(dir)+".reploy-uninstall-pending")
	if tombstone != want {
		t.Fatalf("tombstone = %q for %q", tombstone, dir)
	}
	if _, err := os.Lstat(tombstone); !os.IsNotExist(err) {
		t.Fatalf("reserved tombstone left filesystem entry: %v", err)
	}
}

func TestReserveProviderUninstallTombstoneRejectsExistingPendingRemoval(t *testing.T) {
	dir := t.TempDir()
	tombstone, err := providerUninstallTombstoneV1(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(tombstone, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tombstone) })
	if _, err := reserveProviderUninstallTombstoneV1(dir); err == nil ||
		!strings.Contains(err.Error(), "pending deployment removal already exists") {
		t.Fatalf("reserve existing tombstone error = %v", err)
	}
}
