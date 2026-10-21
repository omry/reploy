package overrideui

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func editorMappingInputV1(t *testing.T) deploy.PackageOverridesV1 {
	t.Helper()
	raw, err := deploy.DecodePackageOverridesV1([]byte("environment:\n  id: example\n  runtime:\n    identity_mapping:\n      uids: [{container_start: 0, host_start: 300000, count: 1}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestMappingEditorPreservesInputThroughRealSaveAndConflict(t *testing.T) {
	dir := stagedEditorDir(t)
	document := editorDocument("demo>=1")
	source, err := blueprint.Decode([]byte("blueprint: {schema: 1}\nenvironment:\n  runtime:\n    identity_mapping: {schema: 1, profile: exact, uids: [{container_start: 0, host_start: '???', count: 1}], gids: [{container_start: 0, host_start: 200000, count: 1}]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	document.Environment.Runtime.IdentityMapping = source.Environment.Runtime.IdentityMapping
	writeEditorState(t, dir, document)
	raw := editorMappingInputV1(t)
	commitOverrides(t, dir, raw)
	m, err := newModel(Config{Context: t.Context(), DeploymentDir: dir, Document: document, Versions: func(context.Context, string) ([]string, error) { return []string{"1.0"}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	m.baseImage = "python:3.13-slim"
	updated := m.buildRaw()
	if !reflect.DeepEqual(updated.IdentityMappingOverride(), raw.IdentityMappingOverride()) {
		t.Fatal("package editor dropped unrelated identity mapping")
	}
	snapshot, err := readEditorSnapshot(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := saveSidecarAt(t.Context(), dir, snapshot, updated); err != nil {
		t.Fatal(err)
	}
	retained, found, err := deploy.ReadPackageOverridesV1(dir)
	if err != nil || !found || retained.Environment.Base.Image != "python:3.13-slim" || !reflect.DeepEqual(retained.IdentityMappingOverride(), raw.IdentityMappingOverride()) {
		t.Fatalf("saved package/base change lost mapping input: %#v / %v", retained, err)
	}
	if _, err := saveSidecarAt(t.Context(), dir, snapshot, raw); err == nil || !strings.Contains(err.Error(), "changed while the editor was open") {
		t.Fatalf("mapping-aware save bypassed existing source-race guard: %v", err)
	}
}

func TestMappingEditorRejectsUndeclaredOverrideBeforeSaving(t *testing.T) {
	dir := stagedEditorDir(t)
	snapshot, err := readEditorSnapshot(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := saveSidecarAt(t.Context(), dir, snapshot, editorMappingInputV1(t)); err == nil || !strings.Contains(err.Error(), "declaration") {
		t.Fatalf("editor saved mapping without a blueprint declaration: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, deploy.PackageOverridesFilename)); !os.IsNotExist(err) {
		t.Fatalf("rejected editor save wrote a sidecar: %v", err)
	}
}
