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

func TestStagePackDesiredStateV1ImportsLocalBlueprintSidecarOnCreate(t *testing.T) {
	ref, manifestPath := writeDesiredStateStagePack(t, "0.1.0")
	sourceDir := filepath.Dir(manifestPath)
	localProject := filepath.Clean(filepath.Join(sourceDir, "..", "checkout"))
	sidecar := []byte(`environment:
  id: omegaconf-inspector
  base:
    image: python:3.13-slim
  vars:
    workspace_root: ..
  package_additions:
    os:
      - default-jre-headless
  package_overrides:
    python:
      omegaconf-inspector:
        path: "{{ workspace_root }}/checkout"
`)
	if err := os.WriteFile(filepath.Join(sourceDir, deploy.PackageOverridesFilename), sidecar, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "staging")
	if _, err := StagePackDesiredStateV1(t.Context(), PackDesiredStateStageInputV1{
		DeploymentDir: dir, Pack: ref, ExplicitPlatform: "linux/amd64", Create: true,
	}); err != nil {
		t.Fatal(err)
	}
	staged, found, err := deploy.ReadPackageOverridesV1(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("local blueprint sidecar was not imported")
	}
	choice := staged.Environment.PackageOverrides["python"]["omegaconf-inspector"]
	if choice.Path != "{{ workspace_root }}/checkout" {
		t.Fatalf("staged path = %q", choice.Path)
	}
	if got := staged.Environment.Vars["workspace_root"]; got != filepath.Dir(localProject) {
		t.Fatalf("staged workspace_root = %#v, want %q", got, filepath.Dir(localProject))
	}
	if staged.Environment.Base == nil || staged.Environment.Base.Image != "python:3.13-slim" {
		t.Fatalf("staged base override = %#v", staged.Environment.Base)
	}
	if got := staged.Environment.PackageAdditions["os"]; len(got) != 1 || got[0] != "default-jre-headless" {
		t.Fatalf("staged package additions = %#v", staged.Environment.PackageAdditions)
	}

	updatedSidecar := strings.Replace(string(sidecar), "/checkout", "/replacement", 1)
	if err := os.WriteFile(filepath.Join(sourceDir, deploy.PackageOverridesFilename), []byte(updatedSidecar), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := StagePackDesiredStateV1(t.Context(), PackDesiredStateStageInputV1{
		DeploymentDir: dir, Pack: ref, ExplicitPlatform: "linux/amd64",
	}); err != nil {
		t.Fatal(err)
	}
	staged, found, err = deploy.ReadPackageOverridesV1(dir)
	if err != nil || !found {
		t.Fatalf("read retained sidecar: found=%v err=%v", found, err)
	}
	if got := staged.Environment.PackageOverrides["python"]["omegaconf-inspector"].Path; got != "{{ workspace_root }}/checkout" {
		t.Fatalf("stage update changed imported sidecar path to %q", got)
	}
}

func TestStagePackDesiredStateV1RejectsMismatchedLocalBlueprintSidecar(t *testing.T) {
	ref, manifestPath := writeDesiredStateStagePack(t, "0.1.0")
	sidecar := []byte(`environment:
  id: another-environment
  package_overrides: {}
`)
	if err := os.WriteFile(filepath.Join(filepath.Dir(manifestPath), deploy.PackageOverridesFilename), sidecar, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "staging")
	_, err := StagePackDesiredStateV1(t.Context(), PackDesiredStateStageInputV1{
		DeploymentDir: dir, Pack: ref, ExplicitPlatform: "linux/amd64", Create: true,
	})
	if err == nil || !strings.Contains(err.Error(), `targets "another-environment"`) {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, StateFileName)); !os.IsNotExist(statErr) {
		t.Fatalf("state was created after rejected sidecar: %v", statErr)
	}
}

func TestStageLoadedPackDesiredStateV1DoesNotImportRemoteBlueprintSidecar(t *testing.T) {
	ref, manifestPath := writeDesiredStateStagePack(t, "0.1.0")
	sidecar := []byte(`environment:
  id: omegaconf-inspector
  package_overrides:
    python:
      omegaconf-inspector:
        path: ../checkout
`)
	if err := os.WriteFile(filepath.Join(filepath.Dir(manifestPath), deploy.PackageOverridesFilename), sidecar, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := deploy.LoadBlueprint(ref)
	if err != nil {
		t.Fatal(err)
	}
	loaded.RequestedRef.Scheme = "git"
	dir := filepath.Join(t.TempDir(), "staging")
	if _, err := StageLoadedPackDesiredStateV1(t.Context(), LoadedPackDesiredStateStageInputV1{
		DeploymentDir: dir, Blueprint: loaded, ExplicitPlatform: "linux/amd64", Create: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := deploy.ReadPackageOverridesV1(dir); err != nil || found {
		t.Fatalf("remote blueprint sidecar import: found=%v err=%v", found, err)
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
