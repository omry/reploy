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
	"time"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func retainedIdentityRecordV1(t *testing.T, dir string, state deploy.StateV1, variant string) deploy.StateV1 {
	t.Helper()
	owner, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := &deploy.IdentityMappingStateV1{
		Schema: deploy.IdentityMappingStateSchemaV1, InstallationID: "mapping-retained", OwnerDir: owner,
		Revision: "7", OperatorUID: "1000", OperatorGID: "1000", Profile: "exact",
	}
	if state.Deployment != nil {
		m.InstallationBinding = &state.Deployment.Installation.InstanceID
	}
	values := []deploy.IdentityMappingIntervalV1{{ContainerStart: "0", HostStart: "100000", Count: "16"}}
	switch variant {
	case "pending":
		m.PendingOperation = &deploy.IdentityMappingOperationV1{OperationID: "operation-fixture", Kind: "configure", ExpectedRevision: "7", ExpectedGeneration: state.Current,
			Candidate: deploy.IdentityMappingGeometryV1{Profile: "exact", UIDs: values, GIDs: values}, Phase: "effects-pending", Inhibited: true}
	case "revoked":
		m.Revoked = true
	case "dormant":
		m.UIDAssignments, m.GIDAssignments = values, values
		m.DormantUIDs, m.DormantGIDs = values, values
		m.AdmittedGeneration = state.Current
		supervisor, engine := "retained-supervisor", "retained-engine"
		m.Supervisor, m.Engine = &supervisor, &engine
		m.RetainedStorage = []string{"retained-data"}
	}
	state.IdentityMapping = m
	content, err := deploy.EncodeStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateFileName), content, 0o600); err != nil {
		t.Fatal(err)
	}
	return state
}

func retainedIdentityStageV1(t *testing.T, variant string, installed bool) (string, deploy.StateV1, providerstore.Store) {
	t.Helper()
	dir, operation, store, _, state := currentBuildFixture(t, true)
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	state.BlueprintSource = "retained source"
	state = identityGuardDocumentV1(t, state, false, false) // declaration removed
	if installed {
		installation := installedBuildPublicationInstallation(dir)
		state.Staging = nil
		state.Deployment = &deploy.DeploymentStateV1{Schema: deploy.DeploymentStateSchemaV1, Installation: installation}
	}
	state = retainedIdentityRecordV1(t, dir, state, variant)
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "managed-data"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, state, store
}

func requireRetainedIdentityRejectedV1(t *testing.T, dir string, before map[string]string, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("identity protection: %v", err)
	}
	if after := identityGuardTreeV1(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatalf("rejected operation changed retained files: %s", retainedIdentityTreeChangesV1(before, after))
	}
}

func retainedIdentityTreeChangesV1(before, after map[string]string) string {
	keys := map[string]bool{}
	for key := range before {
		keys[key] = true
	}
	for key := range after {
		keys[key] = true
	}
	var changes []string
	for key := range keys {
		if before[key] != after[key] {
			changes = append(changes, fmt.Sprintf("%q: %q -> %q", key, before[key], after[key]))
		}
	}
	sort.Strings(changes)
	if len(changes) > 8 {
		changes = append(changes[:8], "additional changes omitted")
	}
	return strings.Join(changes, "; ")
}

func TestRetainedIdentityBlocksRemovalBeforeEffectsV1(t *testing.T) {
	for _, variant := range []string{"empty", "pending", "revoked", "dormant"} {
		for _, mode := range []ControlAdmissionModeV1{ControlAdmissionImmediateV1, ControlAdmissionWaitV1, ControlAdmissionDrainV1, ControlAdmissionForceV1} {
			for _, terminal := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/terminal=%t", variant, mode, terminal), func(t *testing.T) {
					dir, state, store := retainedIdentityStageV1(t, variant, false)
					if terminal {
						state.TerminalRemoval = true
						retainedIdentityRecordV1(t, dir, state, variant)
					}
					before := identityGuardTreeV1(t, dir)
					backend := testStagedRemovalBackendV1(store)
					backend.newStore = func(string) (providerstore.Store, error) {
						t.Fatal("store creation before identity barrier")
						return store, nil
					}
					_, err := removeStagedDeploymentV1(t.Context(), StagedDeploymentRemoveInputV1{DeploymentDir: dir, ControlMode: mode}, backend)
					requireRetainedIdentityRejectedV1(t, dir, before, err)
				})
			}
		}
	}
}

func TestRetainedIdentityBlocksInstalledRemovalBeforeRecoveryV1(t *testing.T) {
	for _, variant := range []string{"empty", "pending", "revoked", "dormant"} {
		for _, mode := range []ControlAdmissionModeV1{ControlAdmissionImmediateV1, ControlAdmissionWaitV1, ControlAdmissionDrainV1, ControlAdmissionForceV1} {
			for _, terminal := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/terminal=%t", variant, mode, terminal), func(t *testing.T) {
					dir, state, _ := retainedIdentityStageV1(t, variant, true)
					if terminal {
						state.TerminalRemoval = true
						retainedIdentityRecordV1(t, dir, state, variant)
					}
					before := identityGuardTreeV1(t, dir)
					backend := newProviderUninstallRunBackendV1()
					backend.recoverPending = func(context.Context, *deploy.OperationLock, string) error {
						t.Fatal("publication recovery reached")
						return nil
					}
					backend.plan = func(providerUninstallPlanningInputV1) (providerUninstallPlanV1, error) {
						t.Fatal("planning reached")
						return providerUninstallPlanV1{}, nil
					}
					err := runProviderUninstallV1(t.Context(), ProviderUninstallInputV1{DeploymentDir: dir, Service: state.Deployment.Installation.Service, RemoveDir: true, ControlMode: mode}, backend)
					requireRetainedIdentityRejectedV1(t, dir, before, err)
				})
			}
		}
	}
}

func TestRetainedIdentityForceReplacementBeforeEffectsV1(t *testing.T) {
	for _, variant := range []string{"empty", "pending", "revoked", "dormant"} {
		t.Run(variant, func(t *testing.T) {
			dir, state, store := retainedIdentityStageV1(t, variant, false)
			before := identityGuardTreeV1(t, dir)
			document, _ := testSelectedPlatformDocumentV1(t)
			document.Environment.ID = "replacement"
			_, err := forceReplaceStagedDesiredStateV1(t.Context(), ForceReplaceStagedDesiredStateInputV1{DesiredState: DesiredStateStageInputV1{DeploymentDir: dir, Document: document, BlueprintSource: "replacement", ExplicitPlatform: state.Platform.Canonical}}, forceReplaceStagedDesiredStateBackendV1{
				acquire:  deploy.AcquireExistingOperationLock,
				newStore: func(string) (providerstore.Store, error) { t.Fatal("replacement store reached"); return store, nil },
				recoverPending: func(context.Context, *deploy.OperationLock, providerstore.Store, *deploy.EnvironmentGenerationState, string, string) (bool, error) {
					t.Fatal("replacement recovery reached")
					return false, nil
				},
				admit: AdmitControlOperationV1, complete: CompleteControlAdmissionV1,
				stopOwned: stopOwnedCurrentWorkloadV1, removeReference: RemoveEnvironmentGenerationReference,
				commit: func(*deploy.OperationLock, *deploy.EnvironmentGenerationState, deploy.StateV1) error {
					t.Fatal("replacement commit reached")
					return nil
				}, stageSame: StageDesiredStateV1,
			})
			requireRetainedIdentityRejectedV1(t, dir, before, err)
		})
	}
}

func TestRetainedIdentityPendingRemovalAndDirectFinalizerV1(t *testing.T) {
	for _, location := range []string{"tombstone", "control"} {
		for _, variant := range []string{"empty", "pending", "revoked", "dormant"} {
			t.Run(location+"/"+variant, func(t *testing.T) {
				dir, tombstone, _ := pendingProviderUninstallRemovalFixture(t)
				path := filepath.Join(tombstone, StateFileName)
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				state, err := deploy.DecodeStateV1(content)
				if err != nil {
					t.Fatal(err)
				}
				retainedIdentityRecordV1(t, tombstone, state, variant)
				// Preserve the original owner binding even after isolation.
				content, err = os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				state, err = deploy.DecodeStateV1(content)
				if err != nil {
					t.Fatal(err)
				}
				state.IdentityMapping.OwnerDir = dir
				content, err = deploy.EncodeStateV1(state)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, content, 0o600); err != nil {
					t.Fatal(err)
				}
				root := tombstone
				if location == "control" {
					control, err := providerUninstallControlV1(dir)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(control, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(filepath.Join(tombstone, ".reploy"), filepath.Join(control, ".reploy")); err != nil {
						t.Fatal(err)
					}
					root = control
				}
				before := identityGuardTreeV1(t, root)
				_, found, err := retryPendingProviderUninstallRemovalWithV1(t.Context(), dir, "demo", providerUninstallPendingRemovalBackendV1{
					acquire: deploy.AcquireExistingOperationLock,
					removeReference: func(context.Context, providers.RealizedImageV1, string, string, string) error {
						t.Fatal("pending retirement reached")
						return nil
					},
					finalize: func(string, string) error { t.Fatal("pending finalization reached"); return nil },
				})
				if !found {
					t.Fatal("pending owner was lost")
				}
				requireRetainedIdentityRejectedV1(t, root, before, err)
				err = finalizePendingProviderUninstallRemovalV1(dir, tombstone)
				requireRetainedIdentityRejectedV1(t, root, before, err)
			})
		}
	}
}

func TestRetainedIdentityPayloadAndControlRemainInhibitedV1(t *testing.T) {
	for _, installed := range []bool{false, true} {
		for _, variant := range []string{"empty", "pending", "revoked", "dormant"} {
			t.Run(fmt.Sprintf("%s/installed=%t", variant, installed), func(t *testing.T) {
				dir, state, _ := retainedIdentityStageV1(t, variant, installed)
				if _, err := unmappedRuntimeDocumentV1(state); err == nil {
					t.Fatal("retained mapping admitted after declaration removal")
				}
				op, err := deploy.AcquireExistingOperationLock(t.Context(), dir)
				if err != nil {
					t.Fatal(err)
				}
				defer op.Unlock()
				before := identityGuardTreeV1(t, dir)
				for _, kind := range []deploy.ControlOperationV1{deploy.ControlOperationStageV1, deploy.ControlOperationUninstallV1, deploy.ControlOperationUpV1} {
					requireRetainedIdentityRejectedV1(t, dir, before, requireUnmappedControlDeploymentV1(op, kind))
				}
			})
		}
	}
}

func TestRetainedIdentityRecheckedAfterRemovalWaitV1(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(fmt.Sprint(installed), func(t *testing.T) {
			dir, op, store, _, state := currentBuildFixture(t, true)
			state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
			state.BlueprintSource = "retained source"
			if installed {
				state.Staging = nil
				state.Deployment = &deploy.DeploymentStateV1{Schema: deploy.DeploymentStateSchemaV1, Installation: installedBuildPublicationInstallation(dir)}
			}
			if err := op.CommitStateV1(state.Current, state); err != nil {
				t.Fatal(err)
			}
			if err := op.Unlock(); err != nil {
				t.Fatal(err)
			}
			recoveries := 0
			admit := func(ctx context.Context, _ string, op *deploy.OperationLock, _ ControlAdmissionInputV1) (AdmittedControlV1, error) {
				if recoveries != 1 {
					t.Fatalf("pre-wait recoveries=%d", recoveries)
				}
				if err := op.Unlock(); err != nil {
					t.Fatal(err)
				}
				next, err := deploy.AcquireExistingOperationLock(ctx, dir)
				if err != nil {
					t.Fatal(err)
				}
				retainedIdentityRecordV1(t, dir, state, "pending")
				return AdmittedControlV1{Operation: next, Marker: deploy.ControlMarkerV1{ID: "test-marker"}}, nil
			}
			complete := func(op *deploy.OperationLock, _ string, _ *deploy.ControlLeaseV1) error { return op.Unlock() }
			var err error
			if installed {
				backend := newProviderUninstallRunBackendV1()
				backend.admit = admit
				backend.complete = complete
				backend.recoverPending = func(context.Context, *deploy.OperationLock, string) error {
					recoveries++
					if recoveries > 1 {
						t.Fatal("recovery after retained authority appeared")
					}
					return nil
				}
				backend.execute = func(context.Context, *deploy.OperationLock, providerUninstallPlanV1, RunOptions) error {
					t.Fatal("host cleanup after wait")
					return nil
				}
				err = runProviderUninstallV1(t.Context(), ProviderUninstallInputV1{DeploymentDir: dir, RemoveDir: true, ControlMode: ControlAdmissionWaitV1, Runtime: blueprintRuntimeFixtureV1()}, backend)
			} else {
				backend := testStagedRemovalBackendV1(store)
				backend.admit = admit
				backend.complete = complete
				backend.recoverPending = func(context.Context, *deploy.OperationLock, providerstore.Store, *deploy.EnvironmentGenerationState, string, string) (bool, error) {
					recoveries++
					if recoveries > 1 {
						t.Fatal("recovery after retained authority appeared")
					}
					return false, nil
				}
				backend.stopOwned = func(context.Context, *deploy.OperationLock, deploy.StateV1, string, RunOptions) error {
					t.Fatal("runtime stop after wait")
					return nil
				}
				_, err = removeStagedDeploymentV1(t.Context(), StagedDeploymentRemoveInputV1{DeploymentDir: dir, ControlMode: ControlAdmissionWaitV1}, backend)
			}
			if err == nil || !strings.Contains(err.Error(), "identity") {
				t.Fatalf("reacquired authority: %v", err)
			}
			loaded, readErr := os.ReadFile(filepath.Join(dir, StateFileName))
			if readErr != nil {
				t.Fatal(readErr)
			}
			retained, readErr := deploy.DecodeStateV1(loaded)
			if readErr != nil || retained.IdentityMapping == nil || retained.IdentityMapping.PendingOperation == nil {
				t.Fatalf("authority lost: %v", readErr)
			}
		})
	}
}

func TestRetainedIdentityInstallAndUninstallPreflightV1(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(fmt.Sprint(installed), func(t *testing.T) {
			dir, state, _ := retainedIdentityStageV1(t, "empty", installed)
			before := identityGuardTreeV1(t, dir)
			if installed {
				requireRetainedIdentityRejectedV1(t, dir, before, preflightProviderInstallDestinationV1(dir, ""))
				op, err := deploy.AcquireExistingOperationLock(t.Context(), dir)
				if err != nil {
					t.Fatal(err)
				}
				defer op.Unlock()
				requireRetainedIdentityRejectedV1(t, dir, before, validateProviderInstallDestinationServiceV1(op, ""))
				state.BlueprintSource = ""
				retainedIdentityRecordV1(t, dir, state, "empty")
				before = identityGuardTreeV1(t, dir)
				err = executeProviderUninstallWithV1(t.Context(), op, providerUninstallPlanV1{Installation: state.Deployment.Installation}, RunOptions{}, func(context.Context, providerUninstallPlanV1, RunOptions) error {
					t.Fatal("cleanup before retained inputs validate")
					return nil
				})
				if err == nil {
					t.Fatal("invalid retained staging accepted")
				}
				if !reflect.DeepEqual(before, identityGuardTreeV1(t, dir)) {
					t.Fatal("failed preflight changed state")
				}
			} else {
				target := filepath.Join(t.TempDir(), "destination")
				_, err := RunProviderInstallV1(t.Context(), ProviderInstallInputV1{SourceDeploymentDir: dir, DestinationDeploymentDir: target, Scope: InstallScopeUser, Runtime: blueprintRuntimeFixtureV1()})
				requireRetainedIdentityRejectedV1(t, dir, before, err)
				if _, err := os.Stat(target); !os.IsNotExist(err) {
					t.Fatal("mapped source prepared install destination", err)
				}
			}
		})
	}
}

func TestRetainedIdentityBuildPublicationPreservesRecordV1(t *testing.T) {
	dir := t.TempDir()
	store, lock := publicationLockFixture(t, dir, "4", "5", "6")
	op, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Unlock()
	input := publicationInput(t, dir, lock)
	state := deploy.StateV1{Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, input.Document), BlueprintSource: "retained source", Platform: lock.Platform, Overlay: deploy.EmptyRequestOverlayV1(), Staging: &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}}
	state = retainedIdentityRecordV1(t, dir, state, "pending")
	references := fixedPublicationReferences(t, dir, 0x51)
	result, err := publishBuild(t.Context(), op, store, input, buildPublicationBackend{
		newReferences: func(string, string) (EnvironmentImageReferences, error) { return references, nil },
		createReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
			return nil
		},
		removeReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Current == nil || !reflect.DeepEqual(state.IdentityMapping, result.IdentityMapping) {
		t.Fatal("explicit portable publication lost mapping or pending intent")
	}
}

func TestRetainedIdentityRealPayloadEntriesRejectRemovedDeclarationV1(t *testing.T) {
	for _, entry := range []string{"shell", "app", "test", "up", "restart", "down"} {
		t.Run(entry, func(t *testing.T) {
			dir, _, _ := retainedIdentityStageV1(t, "pending", false)
			before := identityGuardTreeV1(t, dir)
			var err error
			switch entry {
			case "shell":
				err = RunCurrentShellV1(t.Context(), CurrentShellRunInputV1{DeploymentDir: dir, Wait: true})
			case "app":
				err = RunCurrentAppCommandV1(t.Context(), CurrentAppCommandRunInputV1{DeploymentDir: dir, Arguments: []string{"config", "init"}})
			case "test":
				err = RunCurrentRuntimeTestV1(t.Context(), CurrentRuntimeTestInputV1{DeploymentDir: dir})
			default:
				err = RunCurrentWorkloadV1(t.Context(), CurrentWorkloadRunInputV1{DeploymentDir: dir, Action: entry, ControlMode: ControlAdmissionForceV1})
			}
			requireRetainedIdentityRejectedV1(t, dir, before, err)
		})
	}
}

func TestRetainedIdentityForceReplacementRechecksReacquiredLockV1(t *testing.T) {
	dir, op, store, _, state := currentBuildFixture(t, true)
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	state.BlueprintSource = "retained source"
	if err := op.CommitStateV1(state.Current, state); err != nil {
		t.Fatal(err)
	}
	if err := op.Unlock(); err != nil {
		t.Fatal(err)
	}
	document, _ := testSelectedPlatformDocumentV1(t)
	document.Environment.ID = "replacement"
	recoveries := 0
	_, err := forceReplaceStagedDesiredStateV1(t.Context(), ForceReplaceStagedDesiredStateInputV1{DesiredState: DesiredStateStageInputV1{DeploymentDir: dir, Document: document, BlueprintSource: "replacement", ExplicitPlatform: state.Platform.Canonical}}, forceReplaceStagedDesiredStateBackendV1{
		acquire: deploy.AcquireExistingOperationLock, newStore: func(string) (providerstore.Store, error) { return store, nil },
		recoverPending: func(context.Context, *deploy.OperationLock, providerstore.Store, *deploy.EnvironmentGenerationState, string, string) (bool, error) {
			recoveries++
			if recoveries > 1 {
				t.Fatal("replacement recovery after identity appeared")
			}
			return false, nil
		},
		admit: func(ctx context.Context, _ string, op *deploy.OperationLock, _ ControlAdmissionInputV1) (AdmittedControlV1, error) {
			if err := op.Unlock(); err != nil {
				t.Fatal(err)
			}
			next, err := deploy.AcquireExistingOperationLock(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			retainedIdentityRecordV1(t, dir, state, "pending")
			return AdmittedControlV1{Operation: next, Marker: deploy.ControlMarkerV1{ID: "test-marker"}}, nil
		},
		complete: func(op *deploy.OperationLock, _ string, _ *deploy.ControlLeaseV1) error { return op.Unlock() },
		stopOwned: func(context.Context, *deploy.OperationLock, deploy.StateV1, string, RunOptions) error {
			t.Fatal("replacement stop after identity appeared")
			return nil
		},
		removeReference: func(context.Context, providers.RealizedImageV1, string, string, string) error {
			t.Fatal("replacement retirement after identity appeared")
			return nil
		},
		commit: func(*deploy.OperationLock, *deploy.EnvironmentGenerationState, deploy.StateV1) error {
			t.Fatal("replacement commit after identity appeared")
			return nil
		}, stageSame: StageDesiredStateV1,
	})
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("reacquired replacement authority: %v", err)
	}
	if recoveries != 1 {
		t.Fatalf("recovery count=%d", recoveries)
	}
}

func TestRetainedIdentityFinalizerRejectsMalformedAuthorityBeforeMoveV1(t *testing.T) {
	dir, tombstone, _ := pendingProviderUninstallRemovalFixture(t)
	path := filepath.Join(tombstone, StateFileName)
	if err := os.WriteFile(path, []byte(`{"schema":"unknown"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before := identityGuardTreeV1(t, tombstone)
	if err := finalizePendingProviderUninstallRemovalV1(dir, tombstone); err == nil {
		t.Fatal("unknown retained state authorized finalization")
	}
	if after := identityGuardTreeV1(t, tombstone); !reflect.DeepEqual(before, after) {
		t.Fatalf("finalizer moved or deleted unproved state: %s", retainedIdentityTreeChangesV1(before, after))
	}
	control, err := providerUninstallControlV1(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(control); !os.IsNotExist(err) {
		t.Fatal("finalizer created control directory before authority check", err)
	}
}

func TestRetainedIdentityDirectInstallAdaptersRejectBeforeEffectsV1(t *testing.T) {
	for _, mappedSource := range []bool{false, true} {
		t.Run(fmt.Sprint(mappedSource), func(t *testing.T) {
			dir, _, _ := retainedIdentityStageV1(t, "pending", false)
			mapped, err := deploy.AcquireExistingOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			defer mapped.Unlock()
			ordinary, err := deploy.AcquireOperationLock(t.Context(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer ordinary.Unlock()
			source, destination := ordinary, mapped
			if mappedSource {
				source, destination = mapped, ordinary
			}
			before := identityGuardTreeV1(t, dir)
			_, err = publishInstalledBuildV1(t.Context(), source, destination, providerstore.Store{}, providerstore.Store{}, InstalledBuildPublicationInputV1{}, installedBuildPublicationBackend{})
			requireRetainedIdentityRejectedV1(t, dir, before, err)
			_, err = prepareProviderInstallDestinationWithV1(t.Context(), lockedProviderInstallV1{SourceOperation: source, DestinationOperation: destination}, "", false, providerInstallPrepareDestinationBackendV1{})
			requireRetainedIdentityRejectedV1(t, dir, before, err)
		})
	}
}

func TestRetainedIdentityDirectUninstallBackendsV1(t *testing.T) {
	for _, entry := range []string{"directory removal", "remove-dir execute", "retained staging execute"} {
		t.Run(entry, func(t *testing.T) {
			dir, state, _ := retainedIdentityStageV1(t, "empty", true)
			op, err := deploy.AcquireExistingOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Unlock()
			before := identityGuardTreeV1(t, dir)
			plan := providerUninstallPlanV1{State: state, Installation: state.Deployment.Installation, RemoveDir: entry != "retained staging execute"}
			if entry == "directory removal" {
				err = removeProviderUninstallDeploymentV1(t.Context(), op, "", nil, plan, RunOptions{})
				requireRetainedIdentityRejectedV1(t, dir, before, err)
				return
			}
			cleaned := false
			err = executeProviderUninstallWithV1(t.Context(), op, plan, RunOptions{}, func(context.Context, providerUninstallPlanV1, RunOptions) error { cleaned = true; return nil })
			if plan.RemoveDir {
				requireRetainedIdentityRejectedV1(t, dir, before, err)
				if cleaned {
					t.Fatal("mapped remove-dir performed host cleanup")
				}
				return
			}
			if err != nil || !cleaned {
				t.Fatalf("retained staging transition: cleaned=%v err=%v", cleaned, err)
			}
			stored, found, err := op.ReadStateV1()
			if err != nil || !found {
				t.Fatal(err)
			}
			if stored.Deployment != nil || stored.Staging == nil || stored.IdentityMapping.InstallationBinding != nil || stored.IdentityMapping.InstallationID != state.IdentityMapping.InstallationID || stored.IdentityMapping.Revision != "8" {
				t.Fatal("uninstall failed to retain valid staged authority")
			}
		})
	}
}

func pendingIdentityRecoveryFixtureV1(t *testing.T, location, variant string) (string, string, deploy.StateV1) {
	t.Helper()
	dir, tombstone, _ := pendingProviderUninstallRemovalFixture(t)
	path := filepath.Join(tombstone, StateFileName)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := deploy.DecodeStateV1(content)
	if err != nil {
		t.Fatal(err)
	}
	if variant != "ordinary" {
		state = retainedIdentityRecordV1(t, tombstone, state, variant)
		state.IdentityMapping.OwnerDir = dir
		content, err = deploy.EncodeStateV1(state)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root := tombstone
	if location == "control" {
		control, err := providerUninstallControlV1(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(control, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(tombstone, ".reploy"), filepath.Join(control, ".reploy")); err != nil {
			t.Fatal(err)
		}
		root = control
	}
	if err := os.WriteFile(filepath.Join(tombstone, "managed-data"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, root, state
}

func TestRetainedIdentityServiceAliasAndDirectRecoveryRejectBeforeEffectsV1(t *testing.T) {
	for _, location := range []string{"tombstone", "control"} {
		for _, variant := range []string{"empty", "pending", "revoked", "dormant"} {
			t.Run(location+"/"+variant, func(t *testing.T) {
				dir, root, state := pendingIdentityRecoveryFixtureV1(t, location, variant)
				unitDir := t.TempDir()
				installation := state.Deployment.Installation
				unit := fmt.Sprintf("# Managed-By: reploy\n# Reploy-Service: %s\n# Reploy-Target: %s\n# Reploy-Compose-Project: %s\n", installation.Service, dir, installation.ComposeProject)
				unitPath := filepath.Join(unitDir, installation.Service+".service")
				if err := os.WriteFile(unitPath, []byte(unit), 0o640); err != nil {
					t.Fatal(err)
				}
				before := identityGuardTreeV1(t, filepath.Dir(dir))
				_, err := uninstallProviderV1(UninstallOptions{ServiceName: installation.Service, RemoveDir: true}, providerUninstallPublicBackendV1{
					runtime: func() (StagedProviderBuildRuntimeV1, error) { return blueprintRuntimeFixtureV1(), nil },
					uninstall: func(context.Context, ProviderUninstallInputV1) error {
						t.Fatal("service alias used ordinary uninstall")
						return nil
					},
					readState: readProviderUninstallStateV1, retryPending: retryPendingProviderUninstallRemovalV1,
					recover: func(ctx context.Context, input ProviderUninstallRecoveryInputV1) error {
						if input.RequestedDir != "" {
							t.Fatal("test must resolve target from service unit")
						}
						return recoverMissingProviderUninstallV1(ctx, input, providerUninstallRecoveryBackendV1{unitDir: unitDir, apply: func(context.Context, providerUninstallRecoveryPlanV1, RunOptions) error {
							t.Fatal("service recovery reached host effects")
							return nil
						}})
					},
				})
				requireRetainedIdentityRejectedV1(t, filepath.Dir(dir), before, err)
				plan := providerUninstallRecoveryPlanV1{TargetDir: dir, Service: installation.Service, ComposeProject: installation.ComposeProject, UnitPath: unitPath}
				err = applyProviderUninstallRecoveryV1(t.Context(), plan, RunOptions{}, providerUninstallRecoveryApplyBackendV1{})
				requireRetainedIdentityRejectedV1(t, filepath.Dir(dir), before, err)
				after, err := os.ReadFile(unitPath)
				if err != nil || string(after) != unit {
					t.Fatal("rejected recovery changed service unit", err)
				}
				op, err := deploy.AcquireExistingOperationLock(t.Context(), root)
				if err != nil {
					t.Fatal("rejected recovery leaked lock", err)
				}
				if err := op.Unlock(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestRetainedIdentityServiceRecoveryRejectsUnknownOrConflictingStateV1(t *testing.T) {
	for _, variant := range []string{"malformed", "foreign target", "foreign service", "foreign project", "conflicting roots"} {
		t.Run(variant, func(t *testing.T) {
			dir, root, state := pendingIdentityRecoveryFixtureV1(t, "tombstone", "ordinary")
			installation := state.Deployment.Installation
			switch variant {
			case "malformed":
				if err := os.WriteFile(filepath.Join(root, StateFileName), []byte(`{"schema":"unknown"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "conflicting roots":
				control, err := providerUninstallControlV1(dir)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(control, ".reploy"), 0o700); err != nil {
					t.Fatal(err)
				}
			default:
				switch variant {
				case "foreign target":
					state.Deployment.Installation.TargetDir += "-foreign"
				case "foreign service":
					state.Deployment.Installation.Service += "-foreign"
				case "foreign project":
					state.Deployment.Installation.ComposeProject += "-foreign"
				}
				content, err := deploy.EncodeStateV1(state)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, StateFileName), content, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before := identityGuardTreeV1(t, filepath.Dir(dir))
			plan := providerUninstallRecoveryPlanV1{TargetDir: dir, Service: installation.Service, ComposeProject: installation.ComposeProject}
			err := withUnmappedPendingRemovalV1(t.Context(), plan, func() error { t.Fatal("unproved state reached recovery effects"); return nil })
			if err == nil {
				t.Fatal("unknown/foreign/conflicting authority accepted")
			}
			if after := identityGuardTreeV1(t, filepath.Dir(dir)); !reflect.DeepEqual(before, after) {
				t.Fatalf("recovery changed rejected retained state: %s", retainedIdentityTreeChangesV1(before, after))
			}
		})
	}
}

func TestRetainedPendingRemovalCannotEnrollNewMappingOwnerV1(t *testing.T) {
	for _, location := range []string{"tombstone", "control"} {
		t.Run(location, func(t *testing.T) {
			_, root, state := pendingIdentityRecoveryFixtureV1(t, location, "ordinary")
			state.Deployment.Installation.Scope = "user"
			content, err := deploy.EncodeStateV1(state)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, StateFileName), content, 0o600); err != nil {
				t.Fatal(err)
			}
			op, err := deploy.AcquireExistingOperationLock(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Unlock()
			before := identityGuardTreeV1(t, root)
			input := deploy.IdentityMappingStateV1{Schema: deploy.IdentityMappingStateSchemaV1, OperatorUID: "1000", OperatorGID: "1000", Profile: "exact"}
			if _, err := op.CommitIdentityMappingV1(state.Current, "", 0, input); err == nil {
				t.Fatal("isolated installed state enrolled a mapping owner")
			}
			if _, err := op.CheckIdentityMappingHostV1(t.Context(), nil, nil); err == nil || !strings.Contains(err.Error(), "selected mapping owner") {
				t.Fatalf("isolated target passed mapping host preflight: %v", err)
			}
			if after := identityGuardTreeV1(t, root); !reflect.DeepEqual(before, after) {
				t.Fatalf("rejected isolated mapping changed state: %s", retainedIdentityTreeChangesV1(before, after))
			}
		})
	}
}

func TestRetainedIdentityTreeObservationAndModeControlV1(t *testing.T) {
	dir, _, _ := retainedIdentityStageV1(t, "empty", false)
	before := identityGuardTreeV1(t, dir)
	after := identityGuardTreeV1(t, dir)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("observation alone changed retained snapshot: %s", retainedIdentityTreeChangesV1(before, after))
	}
	path := filepath.Join(dir, "managed-data")
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	after = identityGuardTreeV1(t, dir)
	key := strings.TrimPrefix(path, dir)
	if before[key] == "" || after[key] == "" || strings.SplitN(before[key], ":", 2)[0] == strings.SplitN(after[key], ":", 2)[0] {
		t.Fatal("snapshot did not detect an actual file-mode change")
	}
}

func TestRetainedIdentityServiceRecoveryKeepsOrdinaryStateLockedV1(t *testing.T) {
	for _, location := range []string{"tombstone", "control"} {
		t.Run(location, func(t *testing.T) {
			dir, root, state := pendingIdentityRecoveryFixtureV1(t, location, "ordinary")
			installation := state.Deployment.Installation
			unitDir := t.TempDir()
			unit := fmt.Sprintf("# Managed-By: reploy\n# Reploy-Service: %s\n# Reploy-Target: %s\n# Reploy-Compose-Project: %s\n", installation.Service, dir, installation.ComposeProject)
			if err := os.WriteFile(filepath.Join(unitDir, installation.Service+".service"), []byte(unit), 0o640); err != nil {
				t.Fatal(err)
			}
			called := false
			err := recoverMissingProviderUninstallV1(t.Context(), ProviderUninstallRecoveryInputV1{Service: installation.Service, Runtime: blueprintRuntimeFixtureV1()}, providerUninstallRecoveryBackendV1{unitDir: unitDir, apply: func(context.Context, providerUninstallRecoveryPlanV1, RunOptions) error {
				called = true
				ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
				defer cancel()
				other, err := deploy.AcquireExistingOperationLock(ctx, root)
				if err == nil {
					_ = other.Unlock()
					t.Fatal("pending state lock was released before effects")
				}
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("unexpected competing lock error: %v", err)
				}
				return nil
			}})
			if err != nil || !called {
				t.Fatalf("ordinary recovery: called=%v err=%v", called, err)
			}
			op, err := deploy.AcquireExistingOperationLock(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			if err := op.Unlock(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
