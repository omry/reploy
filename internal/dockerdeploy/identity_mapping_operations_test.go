package dockerdeploy

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestMappingOperationsSelectedOverrideSnapshotV1(t *testing.T) {
	dir, state := identityGuardFixtureV1(t, true, false)
	operation, err := deploy.AcquireExistingOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.CommitPackageOverridesV1(mappingInputOverridesV1(t, document.Environment.ID, "300000")); err != nil {
		t.Fatal(err)
	}
	selected := filepath.Join(t.TempDir(), "selected.yaml")
	content, err := deploy.EncodePackageOverridesV1(mappingInputOverridesV1(t, document.Environment.ID, "400000"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(selected, content, 0o600); err != nil {
		t.Fatal(err)
	}
	before := identityGuardTreeV1(t, dir)
	observed, candidate, err := loadSelectedIdentityMappingV1(operation, dir, selected)
	if err != nil || !reflect.DeepEqual(observed, state) || candidate.UIDs[0].HostStart != 400000 {
		t.Fatalf("selected capture: %#v %v", candidate, err)
	}
	if err := os.WriteFile(selected, []byte("invalid: true"), 0o600); err != nil {
		t.Fatal(err)
	}
	if candidate.UIDs[0].HostStart != 400000 {
		t.Fatal("captured input changed after file replacement")
	}
	if _, _, err := loadSelectedIdentityMappingV1(operation, dir, selected); err == nil {
		t.Fatal("new operation reused stale override")
	}
	_, local, err := loadSelectedIdentityMappingV1(operation, dir, "")
	if err != nil || local.UIDs[0].HostStart != 300000 {
		t.Fatalf("local selection: %v", err)
	}
	if err := os.WriteFile(selected, content, 0o600); err != nil {
		t.Fatal(err)
	}
	// An explicitly selected valid override must not also load the local sidecar.
	if err := os.WriteFile(filepath.Join(dir, deploy.PackageOverridesFilename), []byte("invalid: true"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadSelectedIdentityMappingV1(operation, dir, selected); err != nil {
		t.Fatal("explicit selection read unrelated sidecar:", err)
	}
	if err := operation.CommitPackageOverridesV1(mappingInputOverridesV1(t, document.Environment.ID, "300000")); err != nil {
		t.Fatal(err)
	}
	if _, err := configureStagedIdentityMappingV1(nil, operation, dir, selected, "configure", nil); err == nil || !strings.Contains(err.Error(), "context") {
		t.Fatalf("private configure did not consume captured input: %v", err)
	}
	// Restore exact sidecar timestamps before comparing metadata-only reads.
	after := identityGuardTreeV1(t, dir)
	for path, fingerprint := range before {
		if strings.HasSuffix(path, deploy.PackageOverridesFilename) {
			continue
		}
		if after[path] != fingerprint {
			t.Fatalf("mapping input changed %s", path)
		}
	}
	for _, scenario := range []string{"missing", "wrong environment", "wrong owner", "undeclared"} {
		t.Run(scenario, func(t *testing.T) {
			selectedDir, selectedPath := dir, selected
			switch scenario {
			case "missing":
				selectedPath = selected + ".missing"
			case "wrong environment":
				if err := os.WriteFile(selected, []byte(strings.Replace(string(content), document.Environment.ID, "foreign", 1)), 0o600); err != nil {
					t.Fatal(err)
				}
			case "wrong owner":
				selectedDir = t.TempDir()
			case "undeclared":
				ordinary := identityGuardDocumentV1(t, state, false, false)
				if err := operation.CommitStateV1(state.Current, ordinary); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(selected, content, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := loadSelectedIdentityMappingV1(operation, selectedDir, selectedPath); err == nil {
				t.Fatal("invalid selected input accepted")
			}
			if err := os.WriteFile(selected, content, 0o600); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMappingOperationsActualRetainedUninstallThroughStageAndGuardsV1(t *testing.T) {
	for _, input := range []string{"complete", "missing", "invalid"} {
		t.Run(input, func(t *testing.T) {
			dir, installed, store := retainedIdentityStageV1(t, "empty", true)
			op, err := deploy.AcquireExistingOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			installed = identityGuardDocumentV1(t, installed, true, true)
			if err := op.CommitStateV1(installed.Current, installed); err != nil {
				t.Fatal(err)
			}
			cleaned := false
			plan := providerUninstallPlanV1{State: installed, Installation: installed.Deployment.Installation}
			err = executeProviderUninstallWithV1(t.Context(), op, plan, RunOptions{}, func(context.Context, providerUninstallPlanV1, RunOptions) error { cleaned = true; return nil })
			if err != nil || !cleaned {
				t.Fatalf("actual retained uninstall: %v", err)
			}
			retained, found, err := op.ReadStateV1()
			if err != nil || !found || retained.Deployment != nil || retained.Staging == nil || retained.IdentityMapping.InstallationBinding != nil {
				t.Fatalf("retained staging: %v", err)
			}
			owner := *retained.IdentityMapping
			document, err := blueprint.DecodeResolvedDocumentV1(retained.Blueprint)
			if err != nil {
				t.Fatal(err)
			}
			if input != "missing" {
				if err := op.CommitPackageOverridesV1(mappingInputOverridesV1(t, document.Environment.ID, "300000")); err != nil {
					t.Fatal(err)
				}
			}
			if input == "invalid" {
				if err := os.WriteFile(filepath.Join(dir, deploy.PackageOverridesFilename), []byte("environment: null"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, candidate, err := loadSelectedIdentityMappingV1(op, dir, "")
			if input == "complete" {
				if err != nil || candidate.UIDs[0].HostStart != 300000 {
					t.Fatalf("retained selected mapping: %v", err)
				}
				raw, _, _, err := LoadStagedPackageOverridesV1(op, dir, document)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := ValidatedBuildInputs(document, retained.Overlay, raw, dir, retained.Platform); err != nil {
					t.Fatalf("retained real build validation: %v", err)
				}
			} else if err == nil {
				t.Fatal("missing/invalid retained mapping input accepted")
			}
			if _, err := unmappedRuntimeDocumentV1(retained); err == nil {
				t.Fatal("retained uninstall enabled ordinary payload execution")
			}
			if err := op.Unlock(); err != nil {
				t.Fatal(err)
			}
			// Use the existing public stage update, with the actual uninstall result.
			update, updateErr := StageDesiredStateV1(t.Context(), DesiredStateStageInputV1{DeploymentDir: dir, Document: document, ExplicitPlatform: retained.Platform.Canonical, BlueprintSource: retained.BlueprintSource})
			if input == "invalid" {
				if updateErr == nil {
					t.Fatal("invalid sidecar accepted on retained update")
				}
			} else if updateErr != nil || !reflect.DeepEqual(update.State.IdentityMapping, &owner) {
				t.Fatalf("same-environment update lost retained mapping: %v", updateErr)
			}
			before := identityGuardTreeV1(t, dir)
			replacement := document
			replacement.Environment.ID = "foreign-replacement"
			_, err = forceReplaceStagedDesiredStateV1(t.Context(), ForceReplaceStagedDesiredStateInputV1{DesiredState: DesiredStateStageInputV1{DeploymentDir: dir, Document: replacement, ExplicitPlatform: retained.Platform.Canonical, BlueprintSource: "replacement"}}, forceReplaceStagedDesiredStateBackendV1{
				acquire:  deploy.AcquireExistingOperationLock,
				newStore: func(string) (providerstore.Store, error) { t.Fatal("replacement reached effects"); return store, nil },
				recoverPending: func(context.Context, *deploy.OperationLock, providerstore.Store, *deploy.EnvironmentGenerationState, string, string) (bool, error) {
					t.Fatal("replacement recovery reached")
					return false, nil
				},
				admit: AdmitControlOperationV1, complete: CompleteControlAdmissionV1, stopOwned: stopOwnedCurrentWorkloadV1,
				removeReference: func(context.Context, providers.RealizedImageV1, string, string, string) error {
					t.Fatal("replacement image removal reached")
					return nil
				},
				commit: func(*deploy.OperationLock, *deploy.EnvironmentGenerationState, deploy.StateV1) error {
					t.Fatal("replacement commit reached")
					return nil
				},
				stageSame: StageDesiredStateV1,
			})
			requireRetainedIdentityRejectedV1(t, dir, before, err)
			backend := testStagedRemovalBackendV1(store)
			backend.newStore = func(string) (providerstore.Store, error) { t.Fatal("removal reached effects"); return store, nil }
			_, err = removeStagedDeploymentV1(t.Context(), StagedDeploymentRemoveInputV1{DeploymentDir: dir, ControlMode: ControlAdmissionForceV1}, backend)
			requireRetainedIdentityRejectedV1(t, dir, before, err)
			current, err := os.ReadFile(filepath.Join(dir, StateFileName))
			if err != nil {
				t.Fatal(err)
			}
			final, err := deploy.DecodeStateV1(current)
			if err != nil || !reflect.DeepEqual(final.IdentityMapping, &owner) || final.Staging == nil || final.Deployment != nil {
				t.Fatalf("retained authority changed through guards: %v", err)
			}
		})
	}
}
