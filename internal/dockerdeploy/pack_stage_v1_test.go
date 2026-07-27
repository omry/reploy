package dockerdeploy

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestStagePackDesiredStateV1CreatesOnlyStateFiles(t *testing.T) {
	ref, manifestPath := writeDesiredStateStagePack(t, "0.1.0")
	dir := filepath.Join(t.TempDir(), "nested", "staging")

	result, err := StagePackDesiredStateV1(t.Context(), PackDesiredStateStageInputV1{
		DeploymentDir: dir, Pack: ref, ExplicitPlatform: "linux/amd64", Create: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AppID != "omegaconf-inspector" || !result.DesiredState.Changed || result.DesiredState.State.Current != nil {
		t.Fatalf("result = %#v", result)
	}
	wantSource, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	state := result.DesiredState.State
	if state.BlueprintSource != string(wantSource) || state.Staging == nil {
		t.Fatalf("retained staging source = %#v", state)
	}
	wantWorkspaceRoot := filepath.Clean(filepath.Join(filepath.Dir(manifestPath), stateDocumentWorkspaceRoot(t, state)))
	if state.Staging.WorkspaceRoot != wantWorkspaceRoot {
		t.Fatalf("workspace root = %q, want %q", state.Staging.WorkspaceRoot, wantWorkspaceRoot)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != ReployInternalDir || !entries[0].IsDir() {
		t.Fatalf("staging entries = %#v", entries)
	}
	internalEntries, err := os.ReadDir(filepath.Join(dir, ReployInternalDir))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(internalEntries))
	for index, entry := range internalEntries {
		names[index] = entry.Name()
	}
	if !reflect.DeepEqual(names, []string{"operation.lock", "state.json"}) {
		t.Fatalf("internal entries = %q", names)
	}
}

func TestStagePackDesiredStateV1StoresWorkspaceRootOverrideOutsideStagingDirectory(t *testing.T) {
	ref, _ := writeDesiredStateStagePack(t, "0.1.0")
	stagingDir := filepath.Join(t.TempDir(), "detached", "staging")
	override := filepath.Join(t.TempDir(), "checkout")

	result, err := StagePackDesiredStateV1(t.Context(), PackDesiredStateStageInputV1{
		DeploymentDir: stagingDir, Pack: ref, ExplicitPlatform: "linux/amd64",
		WorkspaceRoot: override, Create: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.DesiredState.State.Staging == nil || result.DesiredState.State.Staging.WorkspaceRoot != override {
		t.Fatalf("staging state = %#v", result.DesiredState.State.Staging)
	}
}

func stateDocumentWorkspaceRoot(t *testing.T, state deploy.StateV1) string {
	t.Helper()
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.FromSlash(document.Environment.Workspace.Root)
}

func TestStagePackDesiredStateV1UpdatesResolvedBlueprint(t *testing.T) {
	ref, manifestPath := writeDesiredStateStagePack(t, "0.1.0")
	dir := t.TempDir()
	input := PackDesiredStateStageInputV1{
		DeploymentDir: dir, Pack: ref, ExplicitPlatform: "linux/amd64", Create: true,
	}
	if _, err := StagePackDesiredStateV1(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(content), "version: 0.1.0", "version: 0.2.0", 1)
	if err := os.WriteFile(manifestPath, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	input.Create = false
	result, err := StagePackDesiredStateV1(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.DesiredState.Changed {
		t.Fatal("updated blueprint was reported as unchanged")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(result.DesiredState.State.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	if document.Blueprint.Version != "0.2.0" {
		t.Fatalf("blueprint version = %q", document.Blueprint.Version)
	}
}

func TestStagePackDesiredStateV1CreateRefusesExistingState(t *testing.T) {
	ref, _ := writeDesiredStateStagePack(t, "0.1.0")
	input := PackDesiredStateStageInputV1{
		DeploymentDir: t.TempDir(), Pack: ref, ExplicitPlatform: "linux/amd64", Create: true,
	}
	if _, err := StagePackDesiredStateV1(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if _, err := StagePackDesiredStateV1(t.Context(), input); !errors.Is(err, deploy.ErrDesiredStateAlreadyExists) {
		t.Fatalf("second create error = %v", err)
	}
}

func writeDesiredStateStagePack(t *testing.T, version string) (deploy.PackRef, string) {
	t.Helper()
	source := filepath.Join("..", "..", "examples", "omegaconf-inspector", "reploy", "omegaconf-inspector.blueprint.yaml")
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	content = []byte(strings.Replace(string(content), "version: 0.1.0", "version: "+version, 1))
	dir := t.TempDir()
	path := filepath.Join(dir, "demo.blueprint.yaml")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := deploy.ParsePackRef("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	return ref, path
}
