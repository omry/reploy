package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

func TestTerminalStagedRetirementRetriesEveryOwnedPairV1(t *testing.T) {
	for _, which := range []string{"current-primary", "current-companion", "validated-primary", "validated-companion"} {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/after=%v", which, after), func(t *testing.T) {
				dir, store, lock, inputs, images := validatedPublicationFixtureV1(t)
				if _, err := images.operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1); err != nil {
					t.Fatal(err)
				}
				record := validatedPublicationRecordV1(t, dir, lock, inputs, 21)
				if err := images.operation.CommitValidatedBuildV1(record); err != nil {
					t.Fatal(err)
				}
				images.retain(t, record, dir)
				current := ownedReferenceGenerationFixtureV1(t, dir, "demo", lock)
				pairs, err := ProjectEnvironmentOwnedReferencesV1(current, lock, "demo", dir)
				if err != nil {
					t.Fatal(err)
				}
				for _, pair := range pairs {
					images.images[pair.Reference] = pair.Image
				}
				state := deploy.StateV1{Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, publicationInput(t, dir, lock).Document), BlueprintSource: "retained source", Platform: lock.Platform, Overlay: lock.Overlay, Current: &current, Staging: &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}}
				if err := images.operation.CommitStateV1(nil, state); err != nil {
					t.Fatal(err)
				}
				externalDir := t.TempDir()
				externalOwner := ownedReferenceGenerationFixtureV1(t, externalDir, "demo", lock)
				externalPairs, err := ProjectEnvironmentOwnedReferencesV1(externalOwner, lock, "demo", externalDir)
				if err != nil {
					t.Fatal(err)
				}
				for _, pair := range externalPairs {
					images.images[pair.Reference] = pair.Image
				}
				faults := map[string]string{"current-primary": pairs[0].Reference, "current-companion": pairs[1].Reference, "validated-primary": record.ImageReference, "validated-companion": record.Companion.Reference}
				fault, fired := faults[which], false
				remove := func(image providers.RealizedImageV1, ref string) error {
					guarded, found, err := images.operation.ReadStateV1()
					if err != nil || !found || !guarded.Staging.TerminalRemoval {
						t.Fatalf("retirement without durable guard: %v", err)
					}
					if err := images.operation.RequireWritable(); err != nil {
						return err
					}
					if ref == fault && !fired && !after {
						fired = true
						return errPendingPublicationFaultV1
					}
					if actual, found := images.images[ref]; found && actual != image {
						return fmt.Errorf("retargeted %s", ref)
					}
					delete(images.images, ref)
					if ref == fault && !fired && after {
						fired = true
						return errPendingPublicationFaultV1
					}
					return nil
				}
				retirement := validatedRetirementBackendV1{removeReference: func(_ context.Context, image providers.RealizedImageV1, ref, env, root string) error {
					if env != "demo" || root != dir {
						t.Fatal("wrong primary scope")
					}
					return remove(image, ref)
				}, removeCompanion: func(_ context.Context, op *deploy.OperationLock, pair OwnedImageReferenceV1, owner deploy.EnvironmentGenerationState, env, root string) error {
					if op != images.operation {
						t.Fatal("wrong held lock")
					}
					if err := validatePortableOwnedReferenceV1(pair, owner, env, root); err != nil {
						return err
					}
					return remove(pair.Image, pair.Reference)
				}}
				backend := testStagedRemovalBackendV1(store)
				backend.acquire = func(ctx context.Context, dir string) (*deploy.OperationLock, error) {
					op, err := deploy.AcquireStagedRemovalOperationLock(ctx, dir)
					images.operation = op
					return op, err
				}
				backend.discardValidated = func(ctx context.Context, op *deploy.OperationLock, env, dir string) error {
					_, _, err := discardValidatedBuildWithBackendV1(ctx, op, env, dir, retirement)
					return err
				}
				backend.removeReference = retirement.removeReference
				backend.removeCompanion = retirement.removeCompanion
				if err := images.operation.Unlock(); err != nil {
					t.Fatal(err)
				}
				if _, err := removeStagedDeploymentV1(t.Context(), StagedDeploymentRemoveInputV1{DeploymentDir: dir}, backend); !errors.Is(err, errPendingPublicationFaultV1) {
					t.Fatalf("partial retirement=%v", err)
				}
				if _, err := os.Lstat(dir); err != nil {
					t.Fatal("partial retirement lost original path", err)
				}
				if _, err := RunProviderBuildV1(t.Context(), ProviderBuildRunInputV1{DeploymentDir: dir, NoCache: true}); !errors.Is(err, deploy.ErrStagedTerminalRemoval) {
					t.Fatalf("partial retirement admitted writer: %v", err)
				}
				if _, err := ListLiveRunsV1(t.Context(), dir); err != nil {
					t.Fatalf("read-only queue diagnostics blocked: %v", err)
				}
				if _, err := removeStagedDeploymentV1(t.Context(), StagedDeploymentRemoveInputV1{DeploymentDir: dir}, backend); err != nil {
					t.Fatalf("restart retirement=%v", err)
				}
				if _, err := os.Lstat(dir); !os.IsNotExist(err) {
					t.Fatal("successful retry retained original directory", err)
				}
				if len(images.images) != len(externalPairs) {
					t.Fatalf("live references after removal=%v", images.images)
				}
				for _, pair := range externalPairs {
					if images.images[pair.Reference] != pair.Image {
						t.Fatal("same-image independent owner removed")
					}
				}
			})
		}
	}
}

func TestTerminalRemovalRejectsCallerHeldOwnerWritersV1(t *testing.T) {
	dir, store, current, record, images := installedTerminalRetirementFixtureV1(t)
	state := current.State
	state.Deployment = nil
	state.BlueprintSource = "retained source"
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	if err := images.operation.CommitStateV1(state.Current, state); err != nil {
		t.Fatal(err)
	}
	if err := images.operation.BeginStagedRemovalV1(state); err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name string
		run  func() error
	}{
		{"no-cache build", func() error {
			_, err := RunLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{Operation: images.operation, DeploymentDir: dir, NoCache: true})
			return err
		}},
		{"current publication", func() error {
			_, err := PublishBuild(t.Context(), images.operation, store, publicationInput(t, dir, current.Lock))
			return err
		}},
		{"validated publication", func() error {
			_, err := PublishValidatedBuild(t.Context(), images.operation, store, "demo", dir, current.Lock, ValidatedBuildInputsV1{})
			return err
		}},
		{"companion creation", func() error {
			return CreatePortableEnvironmentReferenceV1(t.Context(), images.operation, *record.Companion, *record.Owner, "demo", dir)
		}},
		{"image finalization", func() error {
			_, err := CompleteProviderBuild(t.Context(), images.operation, store, ProviderBuildCompletionInput{})
			return err
		}},
		{"runtime input publication", func() error {
			_, err := PublishCurrentRuntimeInputsV1(images.operation, dir, CurrentRuntimePlanV1{})
			return err
		}},
		{"runtime start", func() error {
			return RunCurrentWorkloadLifecycleV1(t.Context(), CurrentWorkloadLifecycleInputV1{Operation: images.operation, DeploymentDir: dir, Action: "up"})
		}},
		{"published runtime start", func() error {
			return RunPublishedRuntimeContainerV1(t.Context(), PublishedRuntimeContainerInput{Operation: images.operation, Invocation: RuntimeInvocationV1{PlanID: "shell"}}, func(context.Context, CurrentBuild) error { t.Fatal("guarded runtime started"); return nil })
		}},
		{"publication recovery", func() error {
			_, err := recoverTerminalPublicationWithStoreV1(t.Context(), images.operation, store, state.Current, "demo", dir)
			return err
		}},
	}
	for _, diagnostic := range []bool{false, true} {
		if diagnostic {
			if err := images.operation.Unlock(); err != nil {
				t.Fatal(err)
			}
			var err error
			images.operation, err = deploy.AcquireExistingOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
		}
		for _, check := range checks {
			t.Run(fmt.Sprintf("%s/diagnostic=%v", check.name, diagnostic), func(t *testing.T) {
				if err := check.run(); err == nil || !strings.Contains(err.Error(), "terminal removal") {
					t.Fatalf("caller-held owner writer error=%v", err)
				}
			})
		}
	}
	if err := images.operation.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalRemovalAllowsReadOnlyCurrentBuildDiagnosticsV1(t *testing.T) {
	dir, operation, store, lock, state := currentBuildFixture(t, true)
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	document.Environment.ID = "demo"
	document.Environment.ControlScript = "demo-control"
	state.Blueprint, err = blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.CommitStateV1(state.Current, state); err != nil {
		t.Fatal(err)
	}
	runtime, err := CurrentStagedProviderBuildRuntimeV1()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanCurrentRuntimeV1(CurrentRuntimePlanInputV1{
		DeploymentDir: dir,
		Current:       CurrentBuild{State: state, Generation: *state.Current, Lock: lock},
		Runtime:       runtime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PublishCurrentRuntimeInputsV1(operation, dir, plan); err != nil {
		t.Fatal(err)
	}
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	state.BlueprintSource = "retained source"
	if err := operation.CommitStateV1(state.Current, state); err != nil {
		t.Fatal(err)
	}
	if _, found, err := LoadRecordedCurrentBuildV1(t.Context(), operation, store, "demo", dir); err != nil || !found {
		t.Fatalf("recorded build before guard: found=%v err=%v", found, err)
	}
	if err := operation.BeginStagedRemovalV1(state); err != nil {
		t.Fatal(err)
	}
	if _, found, err := LoadRecordedCurrentBuildV1(t.Context(), operation, store, "demo", dir); err != nil || !found {
		t.Fatalf("recorded build after guard: found=%v err=%v", found, err)
	}
	guarded, found, err := operation.ReadStateV1()
	if err != nil || !found || guarded.Staging == nil || !guarded.Staging.TerminalRemoval {
		t.Fatalf("terminal guard after diagnostic read: found=%v state=%#v err=%v", found, guarded, err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}

	findings := doctorFindings(dir, false, "", 0)
	for _, finding := range findings {
		if finding.Status == "ok" && finding.Message == "current runtime files match the recorded build" {
			return
		}
	}
	t.Fatalf("doctor findings after terminal guard: %#v", findings)
}

func TestTerminalStagedRemovalGuardFailurePrecedesRetirementV1(t *testing.T) {
	dir, op, store, _, state := currentBuildFixture(t, true)
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	state.BlueprintSource = "retained source"
	if err := op.CommitStateV1(state.Current, state); err != nil {
		t.Fatal(err)
	}
	if err := op.Unlock(); err != nil {
		t.Fatal(err)
	}
	backend := testStagedRemovalBackendV1(store)
	backend.beginRemoval = func(*deploy.OperationLock, deploy.StateV1) error { return errPendingPublicationFaultV1 }
	backend.stopOwned = func(context.Context, *deploy.OperationLock, deploy.StateV1, string, RunOptions) error {
		t.Fatal("workload retirement after guard failure")
		return nil
	}
	backend.discardValidated = func(context.Context, *deploy.OperationLock, string, string) error {
		t.Fatal("validated retirement after guard failure")
		return nil
	}
	backend.removeReference = func(context.Context, providers.RealizedImageV1, string, string, string) error {
		t.Fatal("image retirement after guard failure")
		return nil
	}
	if _, err := removeStagedDeploymentV1(t.Context(), StagedDeploymentRemoveInputV1{DeploymentDir: dir}, backend); !errors.Is(err, errPendingPublicationFaultV1) {
		t.Fatalf("guard failure=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".reploy", "state.json")); err != nil {
		t.Fatal("guard failure erased authority", err)
	}
}

func installedTerminalRetirementFixtureV1(t *testing.T) (string, providerstore.Store, CurrentBuild, deploy.ValidatedBuildV1, *validatedPublicationImagesV1) {
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
	owner := ownedReferenceGenerationFixtureV1(t, dir, "demo", lock)
	pairs, err := ProjectEnvironmentOwnedReferencesV1(owner, lock, "demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range pairs {
		images.images[pair.Reference] = pair.Image
	}
	state := deploy.StateV1{Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, publicationInput(t, dir, lock).Document), Platform: lock.Platform, Overlay: lock.Overlay, Current: &owner, Deployment: &deploy.DeploymentStateV1{Schema: deploy.DeploymentStateSchemaV1, Installation: installedBuildPublicationInstallation(dir)}}
	if err := images.operation.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	return dir, store, CurrentBuild{State: state, Generation: owner, Lock: lock}, record, images
}

func TestTerminalInstalledRetirementRetriesEveryOwnedPairV1(t *testing.T) {
	for _, pendingRoot := range []string{"original", "tombstone", "control"} {
		for _, which := range []string{"current-primary", "current-companion", "validated-primary", "validated-companion"} {
			for _, after := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/after=%v", pendingRoot, which, after), func(t *testing.T) {
					dir, _, current, record, images := installedTerminalRetirementFixtureV1(t)
					pairs, err := ProjectEnvironmentOwnedReferencesV1(current.Generation, current.Lock, "demo", dir)
					if err != nil {
						t.Fatal(err)
					}
					fault := map[string]string{"current-primary": pairs[0].Reference, "current-companion": pairs[1].Reference, "validated-primary": record.ImageReference, "validated-companion": record.Companion.Reference}[which]
					fired := false
					remove := func(image providers.RealizedImageV1, ref string) error {
						state, found, err := images.operation.ReadStateV1()
						if err != nil || !found || state.Deployment == nil || state.Deployment.Installation.TargetDir != dir {
							t.Fatalf("retirement lost original installed authority: %v", err)
						}
						if ref == fault && !fired && !after {
							fired = true
							return errPendingPublicationFaultV1
						}
						if actual, found := images.images[ref]; found && actual != image {
							return fmt.Errorf("retargeted %s", ref)
						}
						delete(images.images, ref)
						if ref == fault && !fired && after {
							fired = true
							return errPendingPublicationFaultV1
						}
						return nil
					}
					primary := func(_ context.Context, image providers.RealizedImageV1, ref, env, root string) error {
						if env != "demo" || root != dir {
							t.Fatal("retirement changed owner scope")
						}
						return remove(image, ref)
					}
					companion := func(_ context.Context, op *deploy.OperationLock, pair OwnedImageReferenceV1, owner deploy.EnvironmentGenerationState, env, root string) error {
						if op != images.operation {
							t.Fatal("retirement changed lock")
						}
						if err := validatePortableOwnedReferenceV1(pair, owner, env, root); err != nil {
							return err
						}
						return remove(pair.Image, pair.Reference)
					}
					root := dir
					if pendingRoot != "original" {
						if err := images.operation.Unlock(); err != nil {
							t.Fatal(err)
						}
						root, err = providerUninstallTombstoneV1(dir)
						if pendingRoot == "control" {
							root, err = providerUninstallControlV1(dir)
						}
						if err != nil {
							t.Fatal(err)
						}
						if err := os.Rename(dir, root); err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { _ = os.RemoveAll(root) })
					}
					attempt := func() error {
						if pendingRoot != "original" {
							_, found, err := retryPendingProviderUninstallRemovalWithV1(t.Context(), dir, "demo", providerUninstallPendingRemovalBackendV1{
								acquire: func(ctx context.Context, path string) (*deploy.OperationLock, error) {
									op, err := deploy.AcquireOperationLock(ctx, path)
									images.operation = op
									return op, err
								}, removeReference: primary, removeCompanion: companion, finalize: finalizePendingProviderUninstallRemovalV1,
							})
							if !found {
								t.Fatal("lost pending removal")
							}
							return err
						}
						if err := images.operation.RequireHeld(); err != nil {
							images.operation, err = deploy.AcquireOperationLock(t.Context(), dir)
							if err != nil {
								return err
							}
						}
						return removeProviderUninstallDeploymentWithV1(t.Context(), images.operation, "control-0000000000000001", new(deploy.ControlLeaseV1), providerUninstallPlanV1{Installation: current.State.Deployment.Installation, Environment: "demo", GenerationReference: current.Generation.Reference, RemoveDir: true}, RunOptions{}, providerUninstallRemoveDirBackendV1{
							newStore: providerstore.NewStore, load: loadTerminalCurrentBuildV1,
							complete:     func(op *deploy.OperationLock, _ string, _ *deploy.ControlLeaseV1) error { return op.Unlock() },
							removeMarker: func(*deploy.OperationLock, string) error { return nil }, releaseLease: func(*deploy.ControlLeaseV1) error { return nil },
							reserve: reserveProviderUninstallTombstoneV1, rename: os.Rename, unlock: func(op *deploy.OperationLock) error { return op.Unlock() },
							removeReference: primary, removeCompanion: companion, finalize: finalizePendingProviderUninstallRemovalV1,
						})
					}
					if err := attempt(); !errors.Is(err, errPendingPublicationFaultV1) {
						t.Fatalf("partial retirement=%v", err)
					}
					if _, err := os.Stat(filepath.Join(root, ".reploy", "state.json")); err != nil {
						t.Fatal("partial retirement erased authority", err)
					}
					if err := attempt(); err != nil {
						t.Fatalf("restart retirement=%v", err)
					}
					if len(images.images) != 0 {
						t.Fatalf("terminal retirement retained owners: %v", images.images)
					}
					if _, err := os.Lstat(root); !os.IsNotExist(err) {
						t.Fatal("completed retirement retained authority", err)
					}
				})
			}
		}
	}
}

func TestTerminalUninstallWithoutRemoveDirPreservesPortableOwnersV1(t *testing.T) {
	dir, _, current, record, images := installedTerminalRetirementFixtureV1(t)
	before := len(images.images)
	if err := executeProviderUninstallWithV1(t.Context(), images.operation, providerUninstallPlanV1{Installation: current.State.Deployment.Installation, Environment: "demo"}, RunOptions{}, func(context.Context, providerUninstallPlanV1, RunOptions) error { return nil }); err != nil {
		t.Fatal(err)
	}
	state, found, err := images.operation.ReadStateV1()
	if err != nil || !found || state.Deployment != nil || state.Current == nil || *state.Current != current.Generation {
		t.Fatalf("uninstall lost staged owner: %v", err)
	}
	retained, found, err := images.operation.ReadValidatedBuildV1()
	if err != nil || !found || retained.BuildLockDigest != record.BuildLockDigest || len(images.images) != before {
		t.Fatalf("uninstall lost validated owner: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".reploy", "state.json")); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalStagedRetirementRetainsGuardAfterFailedRenameV1(t *testing.T) {
	dir, operation, store, _, state := currentBuildFixture(t, true)
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	state.BlueprintSource = "retained source"
	if err := operation.CommitStateV1(state.Current, state); err != nil {
		t.Fatal(err)
	}
	_ = operation.Unlock()
	backend := testStagedRemovalBackendV1(store)
	tombstone := dir + ".isolated"
	t.Cleanup(func() { _ = os.RemoveAll(tombstone) })
	backend.reserve = func(string) (string, error) { return tombstone, nil }
	backend.rename = func(string, string) error { return os.ErrPermission }
	if _, err := removeStagedDeploymentV1(t.Context(), StagedDeploymentRemoveInputV1{DeploymentDir: dir}, backend); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("rename failure=%v", err)
	}
	if _, err := deploy.AcquireOperationLock(t.Context(), dir); !errors.Is(err, deploy.ErrStagedTerminalRemoval) {
		t.Fatalf("failed rename reopened writers: %v", err)
	}
	backend.rename = os.Rename
	if _, err := removeStagedDeploymentV1(t.Context(), StagedDeploymentRemoveInputV1{DeploymentDir: dir}, backend); err != nil {
		t.Fatalf("explicit failed-rename retry=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".reploy")); !os.IsNotExist(err) {
		t.Fatal("retirement state survived successful isolation", err)
	}
}
