package dockerdeploy

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

func mappingInputOverridesV1(t *testing.T, id, host string) deploy.PackageOverridesV1 {
	t.Helper()
	raw, err := deploy.DecodePackageOverridesV1([]byte(fmt.Sprintf("environment:\n  id: %s\n  runtime:\n    identity_mapping:\n      uids: [{container_start: 0, host_start: %s, count: 16}]\n", id, host)))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestMappingInputUsesLockedSelectedStateAndHasNoEffects(t *testing.T) {
	dir, state := identityGuardFixtureV1(t, true, false)
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	_, err = loadStagedIdentityMappingV1(operation, dir)
	if err == nil || !strings.Contains(err.Error(), ".uids[0].host_start") {
		t.Fatalf("missing input did not fail at its consumer: %v", err)
	}
	raw := mappingInputOverridesV1(t, document.Environment.ID, "300000")
	if err := operation.CommitPackageOverridesV1(raw); err != nil {
		t.Fatal(err)
	}
	before := identityGuardTreeV1(t, dir)
	mapping, err := loadStagedIdentityMappingV1(operation, dir)
	if err != nil || mapping.UIDs[0].HostStart.Value != "300000" {
		t.Fatalf("effective selected input = %#v / %v", mapping, err)
	}
	after := identityGuardTreeV1(t, dir)
	if !maps.Equal(before, after) {
		t.Fatal("pure input resolution changed deployment files")
	}
	current, found, err := operation.ReadStateV1()
	if err != nil || !found || current.Blueprint != state.Blueprint {
		t.Fatalf("host override changed portable blueprint: %v", err)
	}
	if _, err := unmappedRuntimeDocumentV1(current); err == nil {
		t.Fatal("effective metadata candidate granted execution admission")
	}
	if _, err := loadStagedIdentityMappingV1(operation, t.TempDir()); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("wrong deployment lock accepted: %v", err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := loadStagedIdentityMappingV1(operation, dir); err == nil {
		t.Fatal("released operation lock accepted")
	}
}

func TestMappingInputCapturedSidecarDoesNotRereadChangedFile(t *testing.T) {
	dir, state := identityGuardFixtureV1(t, true, false)
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	first := mappingInputOverridesV1(t, document.Environment.ID, "300000")
	if err := operation.CommitPackageOverridesV1(first); err != nil {
		t.Fatal(err)
	}
	captured, _, _, err := LoadStagedPackageOverridesV1(operation, dir, document)
	if err != nil {
		t.Fatal(err)
	}
	second := mappingInputOverridesV1(t, document.Environment.ID, "400000")
	content, err := deploy.EncodePackageOverridesV1(second)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, deploy.PackageOverridesFilename), content, 0o600); err != nil {
		t.Fatal(err)
	}
	oldCandidate, err := blueprint.ResolveIdentityMappingV1(document, captured.IdentityMappingOverride())
	if err != nil || oldCandidate.UIDs[0].HostStart.Value != "300000" {
		t.Fatalf("captured bytes were replaced during resolution: %#v / %v", oldCandidate, err)
	}
	newCandidate, err := loadStagedIdentityMappingV1(operation, dir)
	if err != nil || newCandidate.UIDs[0].HostStart.Value != "400000" {
		t.Fatalf("new operation did not capture current input: %#v / %v", newCandidate, err)
	}
}

func TestMappingInputRejectsInstalledAndUndeclaredOwners(t *testing.T) {
	for _, installed := range []bool{false, true} {
		dir, state := identityGuardFixtureV1(t, false, installed)
		operation, err := deploy.AcquireOperationLock(t.Context(), dir)
		if err != nil {
			t.Fatal(err)
		}
		if installed {
			if _, err := loadStagedIdentityMappingV1(operation, dir); err == nil || !strings.Contains(err.Error(), "staging") {
				t.Fatalf("installed input silently used staging sidecar: %v", err)
			}
		} else {
			state = identityGuardDocumentV1(t, state, false, false)
			if err := operation.CommitStateV1(nil, state); err != nil {
				t.Fatal(err)
			}
			document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
			if err != nil {
				t.Fatal(err)
			}
			if mapping, err := loadStagedIdentityMappingV1(operation, dir); err != nil || mapping != nil {
				t.Fatalf("ordinary owner changed: %#v / %v", mapping, err)
			}
			if err := operation.CommitPackageOverridesV1(mappingInputOverridesV1(t, document.Environment.ID, "300000")); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := LoadStagedPackageOverridesV1(operation, dir, document); err == nil || !strings.Contains(err.Error(), "declaration") {
				t.Fatalf("build input accepted undeclared mapping: %v", err)
			}
		}
		if err := operation.Unlock(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMappingInputRealStageImportsAndPreservesChoices(t *testing.T) {
	ref, manifestPath := writeDesiredStateStagePack(t, "0.1.0")
	original, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	declaration := "  runtime:\n    identity_mapping: {schema: 1, profile: exact, uids: [{container_start: 0, host_start: '???', count: 16}], gids: [{container_start: 0, host_start: 200000, count: 16}]}\n"
	originalText := strings.ReplaceAll(string(original), "\r\n", "\n")
	source := strings.Replace(originalText, "  id: omegaconf-inspector\n", "  id: omegaconf-inspector\n"+declaration, 1)
	if source == originalText {
		t.Fatal("fixture did not add a declaration")
	}
	if err := os.WriteFile(manifestPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	local := mappingInputOverridesV1(t, "omegaconf-inspector", "300000")
	encoded, err := deploy.EncodePackageOverridesV1(local)
	if err != nil {
		t.Fatal(err)
	}
	localPath := filepath.Join(filepath.Dir(manifestPath), deploy.PackageOverridesFilename)
	if err := os.WriteFile(localPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "staged")
	input := PackDesiredStateStageInputV1{DeploymentDir: dir, Pack: ref, ExplicitPlatform: "linux/amd64", Create: true}
	staged, err := StagePackDesiredStateV1(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	imported, found, err := deploy.ReadPackageOverridesV1(dir)
	if err != nil || !found || imported.IdentityMappingOverride() == nil || (*imported.IdentityMappingOverride().UIDs)[0].HostStart.Value != "300000" {
		t.Fatalf("real stage dropped mapping input: %#v / %v", imported, err)
	}
	portable, err := blueprint.DecodeResolvedDocumentV1(staged.DesiredState.State.Blueprint)
	if err != nil || portable.Environment.Runtime.IdentityMapping.UIDs[0].HostStart.Kind != "missing" {
		t.Fatalf("host override replaced original portable intent: %#v / %v", portable, err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, deploy.PackageOverridesFilename))
	if err := os.WriteFile(localPath, []byte(strings.Replace(string(encoded), "300000", "400000", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	input.Create = false
	if _, err := StagePackDesiredStateV1(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, deploy.PackageOverridesFilename))
	if !bytes.Equal(before, after) {
		t.Fatal("update rediscovered source sidecar and replaced selected choices")
	}
	// The same local sidecar cannot opt a new ordinary blueprint into mappings.
	if err := os.WriteFile(manifestPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	input.Create = true
	input.DeploymentDir = filepath.Join(t.TempDir(), "undeclared")
	if _, err := StagePackDesiredStateV1(t.Context(), input); err == nil || !strings.Contains(err.Error(), "declaration") {
		t.Fatalf("stage imported mapping without a declaration: %v", err)
	}
	if _, err := os.Stat(filepath.Join(input.DeploymentDir, ".reploy", "state.json")); !os.IsNotExist(err) {
		t.Fatalf("rejected import published state: %v", err)
	}
}

func TestMappingInputRealProviderBuildPreservesValidatedInputs(t *testing.T) {
	dir, state := identityGuardFixtureV1(t, true, false)
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	want, err := ValidatedBuildInputs(document, state.Overlay, deploy.EmptyPackageOverridesV1(document.Environment.ID), dir, state.Platform)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"100000", "300000"} {
		operation, err := deploy.AcquireOperationLock(t.Context(), dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := operation.CommitPackageOverridesV1(mappingInputOverridesV1(t, document.Environment.ID, host)); err != nil {
			t.Fatal(err)
		}
		if err := operation.Unlock(); err != nil {
			t.Fatal(err)
		}
		before := identityGuardTreeV1(t, dir)
		prepared, executed := false, false
		_, err = runProviderBuildV1(t.Context(), ProviderBuildRunInputV1{DeploymentDir: dir, NoCache: true, Runtime: blueprintRuntimeFixtureV1()}, providerBuildRunBackend{
			acquire: deploy.AcquireOperationLock, newStore: providerstore.NewStore,
			prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
				prepared = true
				if input.ValidatedInputs.BlueprintDigest != want.BlueprintDigest || input.ValidatedInputs.PackageOverridesDigest != want.PackageOverridesDigest || input.ValidatedInputs.OverlayDigest != want.OverlayDigest || input.ValidatedInputs.BaseImage != want.BaseImage {
					t.Fatal("deployment-only host change invalidated original build inputs")
				}
				if err := input.Operation.RequireHeld(); err != nil {
					t.Fatal(err)
				}
				return LockedProviderBuildPreparationV1{Operation: input.Operation, Store: input.Store}, nil
			},
			execute: func(_ context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
				executed = true
				return LockedProviderBuildExecutionResultV1{Reused: true}, nil
			},
		})
		if err != nil || !prepared || !executed {
			t.Fatalf("portable explicit build under mapping override: %v", err)
		}
		if !maps.Equal(before, identityGuardTreeV1(t, dir)) {
			t.Fatal("portable build modified mapping or blueprint authority")
		}
	}
}
