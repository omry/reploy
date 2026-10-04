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
	for _, installed := range []bool{false, true} {
		t.Run(map[bool]string{false: "staged", true: "installed"}[installed], func(t *testing.T) {
			testTerminalRemovalAllowsReadOnlyCurrentBuildDiagnosticsV1(t, installed)
		})
	}
}

func testTerminalRemovalAllowsReadOnlyCurrentBuildDiagnosticsV1(t *testing.T, installed bool) {
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
	if installed {
		scope := blueprint.InstallScopeUser
		expected, err := PlanDockerExecution(document, DockerPlanContext{
			DeploymentDir: dir, InstallTarget: dir, Phase: blueprint.PhaseInstalled, Scope: &scope,
			GeneratedImage: state.Current.Reference, Host: runtime.Host, UID: runtime.UID, GID: runtime.GID,
			SupplementaryGIDs: append([]uint32(nil), runtime.SupplementaryGIDs...),
		})
		if err != nil {
			t.Fatal(err)
		}
		installation := installedBuildPublicationInstallation(dir)
		installation.Scope = string(scope)
		installation.ContainerName, installation.NetworkName, installation.ComposeProject = expected.ContainerName, expected.NetworkName, expected.NetworkName
		installation.Ports = installationPortsForDockerPlanV1(expected)
		state, _, err = operation.SetInstallationStateV1(installation)
		if err != nil {
			t.Fatal(err)
		}
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
	if !installed {
		state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
		state.BlueprintSource = "retained source"
	}
	if err := operation.CommitStateV1(state.Current, state); err != nil {
		t.Fatal(err)
	}
	if _, found, err := LoadRecordedCurrentBuildV1(t.Context(), operation, store, "demo", dir); err != nil || !found {
		t.Fatalf("recorded build before guard: found=%v err=%v", found, err)
	}
	begin := operation.BeginStagedRemovalV1
	if installed {
		begin = operation.BeginInstalledRemovalV1
	}
	if err := begin(state); err != nil {
		t.Fatal(err)
	}
	if _, found, err := LoadRecordedCurrentBuildV1(t.Context(), operation, store, "demo", dir); err != nil || !found {
		t.Fatalf("recorded build after guard: found=%v err=%v", found, err)
	}
	guarded, found, err := operation.ReadStateV1()
	if err != nil || !found || !(guarded.Staging != nil && guarded.Staging.TerminalRemoval || guarded.Deployment != nil && guarded.Deployment.TerminalRemoval) {
		t.Fatalf("terminal guard after diagnostic read: found=%v state=%#v err=%v", found, guarded, err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}

	if _, err := ListLiveRunsV1(t.Context(), dir); err != nil {
		t.Fatalf("list live runs after terminal guard: %v", err)
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

type terminalPendingPreviousProfileFixtureV1 struct {
	dir       string
	operation *deploy.OperationLock
	state     deploy.StateV1
	lock      deploy.BuildLockV1
	pairs     []OwnedImageReferenceV1
	images    map[string]providers.RealizedImageV1
}

func terminalPendingPreviousProfileFixture(t *testing.T, portable bool) terminalPendingPreviousProfileFixtureV1 {
	t.Helper()
	var dir string
	var store providerstore.Store
	var lock deploy.BuildLockV1
	if portable {
		dir, store, lock = pendingPortablePublicationFixtureV1(t)
	} else {
		fixture := newPreparedPythonGraphReuseFixture(t)
		dir = filepath.Dir(filepath.Dir(fixture.store.Root()))
		store, lock = fixture.store, fixture.lock
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	document, _ := testSelectedPlatformDocumentV1(t)
	document.Environment.ID = lock.PackageOverrides.EnvironmentID
	lock.BlueprintDigest = testResolvedBlueprintDigestV1(t, document)
	lock.Nodes[0].RequirementProfile.Facts.Schema = "python-profile-facts-previous"
	profileDigest, err := providers.RequirementProfileDigest(
		lock.Nodes[0].RequirementProfile, acceptProviderProfileOwnerForCutoverV1,
	)
	if err != nil {
		t.Fatal(err)
	}
	lock.Nodes[0].ValidationEvidence.ProfileDigest = profileDigest
	content, err := store.LoadManifest(lock.Nodes[0].BundleManifest)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := providers.DecodeResolvedBundleManifest(
		content, lock.Nodes[0].BundleManifest, registry.ValidateResolvedBundlePayloadV1,
	)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Payload.RequirementProfileDigest = profileDigest
	bundle, err = providers.NewResolvedBundle(bundle.Payload, acceptProviderBundleOwnerForCutoverV1)
	if err != nil {
		t.Fatal(err)
	}
	lock.Nodes[0].BundleManifest, err = providers.PublishResolvedBundleManifest(
		t.Context(), store, bundle, acceptProviderBundleOwnerForCutoverV1,
	)
	if err != nil {
		t.Fatal(err)
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	lock.ValidationRecord, err = deploy.PublishPrefixValidation(t.Context(), store, deploy.PrefixValidationV1{
		Schema: deploy.PrefixValidationSchemaV1, SubjectRootFS: lock.FinalImage.RootFSSubject,
		RuntimePolicy: policyDigest, Profiles: []providers.ValidationEvidence{}, ExposedOutputs: []providers.ExecutableEvidence{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deploy.BuildLockStoreClosure(lock, store, acceptProviderProfileOwnerForCutoverV1, acceptProviderBundleOwnerForCutoverV1); err != nil {
		t.Fatalf("previous-profile owner is not canonical: %v", err)
	}
	if _, err := deploy.BuildLockDigestV1(lock, registry.ValidateRequirementProfileV1); err == nil {
		t.Fatal("previous-profile owner passed strict current-schema validation")
	}
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	lockDigest, err := operation.PublishBuildLock(lock, acceptProviderProfileOwnerForCutoverV1)
	if err != nil {
		t.Fatal(err)
	}
	reference := fixedPublicationReferences(t, dir, 0x64).Generation
	owner := deploy.EnvironmentGenerationState{
		Reference: reference, ImageDigest: lock.FinalImage.Digest,
		RootFSSubject: lock.FinalImage.RootFSSubject, BuildLockDigest: lockDigest,
		Platform: lock.Platform, RuntimePolicyDigest: policyDigest,
	}
	state := deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, document),
		Platform: lock.Platform, Overlay: lock.Overlay, Current: &owner,
		Deployment: &deploy.DeploymentStateV1{
			Schema: deploy.DeploymentStateSchemaV1, Installation: installedBuildPublicationInstallation(dir),
		},
	}
	if err := operation.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	pairs, err := projectEnvironmentOwnedReferencesV1(owner, lock, document.Environment.ID, dir, acceptProviderProfileOwnerForCutoverV1)
	if err != nil {
		t.Fatal(err)
	}
	images := make(map[string]providers.RealizedImageV1, len(pairs))
	for _, pair := range pairs {
		images[pair.Reference] = pair.Image
	}
	return terminalPendingPreviousProfileFixtureV1{
		dir: dir, operation: operation, state: state, lock: lock, pairs: pairs, images: images,
	}
}

func TestInstalledRetirementRetryAcceptsPreviousProfileFromDeterministicRootsV1(t *testing.T) {
	for _, portable := range []bool{false, true} {
		for _, pendingRoot := range []string{"tombstone", "control"} {
			t.Run(fmt.Sprintf("portable=%v/root=%s", portable, pendingRoot), func(t *testing.T) {
				fixture := terminalPendingPreviousProfileFixture(t, portable)
				var removeCalls, absentCalls, finalizeCalls int
				expected := make(map[string]providers.RealizedImageV1, len(fixture.pairs))
				for _, pair := range fixture.pairs {
					expected[pair.Reference] = pair.Image
				}
				checkGuard := func() {
					if err := fixture.operation.RequireHeld(); err != nil {
						t.Fatalf("retirement effect without held operation lock: %v", err)
					}
					state, found, err := fixture.operation.ReadStateV1()
					if err != nil || !found || state.Current == nil || state.Deployment == nil ||
						!state.Deployment.TerminalRemoval || state.Deployment.Installation.TargetDir != fixture.dir {
						t.Fatalf("retirement effect without exact installed guard: found=%v err=%v state=%#v", found, err, state)
					}
				}
				remove := func(image providers.RealizedImageV1, reference, environment, deploymentDir string) error {
					checkGuard()
					if environment != "demo" || deploymentDir != fixture.dir {
						return fmt.Errorf("retirement changed owner scope")
					}
					want, owned := expected[reference]
					if !owned || want != image {
						return fmt.Errorf("retirement changed exact image owner for %s", reference)
					}
					if current, present := fixture.images[reference]; present {
						if current != image {
							return fmt.Errorf("retirement retargeted %s", reference)
						}
						delete(fixture.images, reference)
					} else {
						absentCalls++
					}
					removeCalls++
					return nil
				}
				primary := func(_ context.Context, image providers.RealizedImageV1, reference, environment, deploymentDir string) error {
					return remove(image, reference, environment, deploymentDir)
				}
				companion := func(_ context.Context, operation *deploy.OperationLock, pair OwnedImageReferenceV1, owner deploy.EnvironmentGenerationState, environment, deploymentDir string) error {
					if operation != fixture.operation {
						return fmt.Errorf("retirement changed operation lock")
					}
					if err := validatePortableOwnedReferenceV1(pair, owner, environment, deploymentDir); err != nil {
						return err
					}
					return remove(pair.Image, pair.Reference, environment, deploymentDir)
				}
				tombstone, err := providerUninstallTombstoneV1(fixture.dir)
				if err != nil {
					t.Fatal(err)
				}
				control, err := providerUninstallControlV1(fixture.dir)
				if err != nil {
					t.Fatal(err)
				}
				interrupt := errors.New("injected after-rename finalization interruption")
				finalize := func(deploymentDir, gotTombstone string) error {
					if deploymentDir != fixture.dir || gotTombstone != tombstone {
						return fmt.Errorf("finalization changed target or tombstone")
					}
					finalizeCalls++
					if finalizeCalls == 1 {
						if pendingRoot == "control" {
							if err := os.Mkdir(control, 0o700); err != nil {
								return err
							}
							if err := os.Rename(filepath.Join(tombstone, ".reploy"), filepath.Join(control, ".reploy")); err != nil {
								return err
							}
						}
						return interrupt
					}
					if finalizeCalls == 2 {
						return interrupt
					}
					return finalizePendingProviderUninstallRemovalV1(deploymentDir, gotTombstone)
				}
				backend := providerUninstallRemoveDirBackendV1{
					newStore: providerstore.NewStore, load: loadTerminalCurrentBuildV1,
					complete: func(operation *deploy.OperationLock, _ string, _ *deploy.ControlLeaseV1) error {
						return operation.Unlock()
					},
					removeMarker: func(*deploy.OperationLock, string) error { return nil },
					releaseLease: func(*deploy.ControlLeaseV1) error { return nil },
					reserve:      reserveProviderUninstallTombstoneV1, rename: os.Rename,
					unlock:          func(operation *deploy.OperationLock) error { return operation.Unlock() },
					removeReference: primary,
					removeCompanion: companion, finalize: finalize,
				}
				plan := providerUninstallPlanV1{
					Installation: fixture.state.Deployment.Installation, Environment: "demo",
					GenerationReference: fixture.state.Current.Reference, RemoveDir: true,
				}
				if err := removeProviderUninstallDeploymentWithV1(t.Context(), fixture.operation, "control-0000000000000001", new(deploy.ControlLeaseV1), plan, RunOptions{}, backend); !errors.Is(err, interrupt) {
					t.Fatalf("initial installed removal error=%v", err)
				}
				if len(fixture.images) != 0 || removeCalls != len(fixture.pairs) {
					t.Fatalf("post-rename interruption left owner aliases: images=%v removeCalls=%d", fixture.images, removeCalls)
				}
				stateRoot := tombstone
				if pendingRoot == "control" {
					stateRoot = control
				}
				type retryHandle struct{ operation *deploy.OperationLock }
				handle := &retryHandle{}
				retryBackend := providerUninstallPendingRemovalBackendV1{
					acquire: func(ctx context.Context, root string) (*deploy.OperationLock, error) {
						if root != stateRoot {
							return nil, fmt.Errorf("retry selected state root %q, want %q", root, stateRoot)
						}
						op, err := deploy.AcquireInstalledRemovalOperationLock(ctx, root)
						if err == nil {
							fixture.operation = op
							handle.operation = op
						}
						return op, err
					},
					removeReference: func(_ context.Context, image providers.RealizedImageV1, reference, environment, deploymentDir string) error {
						return remove(image, reference, environment, deploymentDir)
					},
					removeCompanion: func(ctx context.Context, operation *deploy.OperationLock, pair OwnedImageReferenceV1, owner deploy.EnvironmentGenerationState, environment, deploymentDir string) error {
						if operation != handle.operation {
							return fmt.Errorf("retry changed operation lock")
						}
						if err := validatePortableOwnedReferenceV1(pair, owner, environment, deploymentDir); err != nil {
							return err
						}
						return remove(pair.Image, pair.Reference, environment, deploymentDir)
					},
					finalize: finalize,
				}
				for attempt := 1; attempt <= 2; attempt++ {
					_, found, err := retryPendingProviderUninstallRemovalWithV1(t.Context(), fixture.dir, "demo", retryBackend)
					if !found {
						t.Fatalf("retry %d lost pending removal", attempt)
					}
					if attempt == 1 && !errors.Is(err, interrupt) {
						t.Fatalf("first retry should retain deterministic root: %v", err)
					}
					if attempt == 2 && err != nil {
						t.Fatalf("repeat retry error=%v", err)
					}
				}
				if removeCalls != len(fixture.pairs)*3 || absentCalls != len(fixture.pairs)*2 {
					t.Fatalf("retry did not tolerate absent retired aliases: removeCalls=%d absentCalls=%d pairs=%d", removeCalls, absentCalls, len(fixture.pairs))
				}
				for _, path := range []string{tombstone, control} {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatalf("completed repeated retry retained %s: %v", path, err)
					}
				}
			})
		}
	}
}

func TestInstalledPendingPreviousProfileRejectsMismatchedAuthorityBeforeEffectsV1(t *testing.T) {
	for _, mismatch := range []string{"current", "hash", "scope"} {
		t.Run(mismatch, func(t *testing.T) {
			fixture := terminalPendingPreviousProfileFixture(t, false)
			state := fixture.state
			current := *fixture.state.Current
			state.Current = &current
			deployment := *fixture.state.Deployment
			state.Deployment = &deployment
			switch mismatch {
			case "current":
				current.ImageDigest = rendererDigest("f")
			case "hash":
				current.BuildLockDigest = rendererDigest("f")
			case "scope":
				state.Deployment.Installation.TargetDir = filepath.Join(filepath.Dir(fixture.dir), "other-target")
			}
			if err := fixture.operation.CommitStateV1(fixture.state.Current, state); err != nil {
				t.Fatal(err)
			}
			if err := fixture.operation.Unlock(); err != nil {
				t.Fatal(err)
			}
			tombstone, err := providerUninstallTombstoneV1(fixture.dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(fixture.dir, tombstone); err != nil {
				t.Fatal(err)
			}
			effects := 0
			_, found, err := retryPendingProviderUninstallRemovalWithV1(t.Context(), fixture.dir, "demo", providerUninstallPendingRemovalBackendV1{
				acquire: deploy.AcquireOperationLock,
				removeReference: func(context.Context, providers.RealizedImageV1, string, string, string) error {
					effects++
					return nil
				},
				finalize: func(string, string) error {
					effects++
					return nil
				},
			})
			if !found || err == nil || effects != 0 {
				t.Fatalf("mismatched %s authority found=%v error=%v effects=%d", mismatch, found, err, effects)
			}
		})
	}
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
						if err != nil || !found || state.Deployment == nil || !state.Deployment.TerminalRemoval || state.Deployment.Installation.TargetDir != dir {
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
									op, err := deploy.AcquireInstalledRemovalOperationLock(ctx, path)
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
							images.operation, err = deploy.AcquireInstalledRemovalOperationLock(t.Context(), dir)
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

func TestTerminalInstalledRetirementFailuresKeepWritersExcludedV1(t *testing.T) {
	for _, phase := range []string{"reference-before", "reference-after", "reserve", "remove-marker", "release-lease", "rename"} {
		t.Run(phase, func(t *testing.T) {
			dir, _, current, _, images := installedTerminalRetirementFixtureV1(t)
			failed := true
			remove := func(_ context.Context, image providers.RealizedImageV1, ref, _, _ string) error {
				if failed && ref == current.Generation.Reference && phase == "reference-before" {
					return errPendingPublicationFaultV1
				}
				delete(images.images, ref)
				if failed && ref == current.Generation.Reference && phase == "reference-after" {
					return errPendingPublicationFaultV1
				}
				return nil
			}
			backend := providerUninstallRemoveDirBackendV1{
				newStore: providerstore.NewStore, load: loadTerminalCurrentBuildV1,
				complete: func(op *deploy.OperationLock, _ string, _ *deploy.ControlLeaseV1) error { return op.Unlock() },
				removeMarker: func(*deploy.OperationLock, string) error {
					if failed && phase == "remove-marker" {
						return errPendingPublicationFaultV1
					}
					return nil
				},
				releaseLease: func(*deploy.ControlLeaseV1) error {
					if failed && phase == "release-lease" {
						return errPendingPublicationFaultV1
					}
					return nil
				},
				reserve: func(root string) (string, error) {
					if failed && phase == "reserve" {
						return "", errPendingPublicationFaultV1
					}
					return reserveProviderUninstallTombstoneV1(root)
				},
				rename: func(from, to string) error {
					if failed && phase == "rename" {
						return errPendingPublicationFaultV1
					}
					return os.Rename(from, to)
				},
				unlock:          func(op *deploy.OperationLock) error { return op.Unlock() },
				removeReference: remove,
				removeCompanion: func(ctx context.Context, _ *deploy.OperationLock, pair OwnedImageReferenceV1, _ deploy.EnvironmentGenerationState, env, root string) error {
					return remove(ctx, pair.Image, pair.Reference, env, root)
				},
				finalize: finalizePendingProviderUninstallRemovalV1,
			}
			plan := providerUninstallPlanV1{Installation: current.State.Deployment.Installation, Environment: "demo", GenerationReference: current.Generation.Reference, RemoveDir: true}
			err := removeProviderUninstallDeploymentWithV1(t.Context(), images.operation, "control-0000000000000001", new(deploy.ControlLeaseV1), plan, RunOptions{}, backend)
			if !errors.Is(err, errPendingPublicationFaultV1) {
				t.Fatalf("failed removal=%v", err)
			}
			writer, err := deploy.AcquireOperationLock(t.Context(), dir)
			if writer != nil {
				_ = writer.Unlock()
			}
			if err == nil {
				t.Fatal("installed retirement failure reopened ordinary writer admission")
			}
			diagnostic, err := deploy.AcquireExistingOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			defer diagnostic.Unlock()
			state, found, err := diagnostic.ReadStateV1()
			if err != nil || !found || state.Current == nil || *state.Current != current.Generation || state.Deployment == nil || !state.Deployment.TerminalRemoval {
				t.Fatalf("retirement lost current retry authority: %v", err)
			}
			if err := diagnostic.RequireOwnerWritable(); err == nil {
				t.Fatal("diagnostic lock admitted a new owner")
			}
			if err := diagnostic.CommitStateV1(state.Current, current.State); err == nil {
				t.Fatal("diagnostic writer cleared terminal state")
			}
			if err := diagnostic.Unlock(); err != nil {
				t.Fatal(err)
			}
			failed = false
			retry, err := deploy.AcquireInstalledRemovalOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			images.operation = retry
			if err := removeProviderUninstallDeploymentWithV1(t.Context(), retry, "", nil, plan, RunOptions{}, backend); err != nil {
				t.Fatalf("explicit terminal retry=%v", err)
			}
			if len(images.images) != 0 {
				t.Fatal("successful retry retained image owners")
			}
			if _, err := os.Lstat(dir); !os.IsNotExist(err) {
				t.Fatalf("successful retry retained deployment: %v", err)
			}
		})
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
