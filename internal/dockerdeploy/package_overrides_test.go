package dockerdeploy

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func TestLoadStagedPackageOverridesV1LoadsOptionalSidecar(t *testing.T) {
	dir := t.TempDir()
	document := resolvedRequestTestDocument()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()

	raw, resolved, intent, err := LoadStagedPackageOverridesV1(operation, dir, document)
	if err != nil {
		t.Fatal(err)
	}
	if raw.Environment.ID != document.Environment.ID ||
		len(raw.Environment.PackageOverrides) != 0 ||
		len(resolved.Providers) != 0 ||
		len(intent.Choices) != 0 {
		t.Fatalf("missing sidecar loaded as %#v / %#v / %#v", raw, resolved, intent)
	}

	wantRaw := deploy.EmptyPackageOverridesV1(document.Environment.ID)
	wantRaw.Environment.Vars["root"] = filepath.Join(dir, "workspace")
	wantRaw.Environment.PackageOverrides["python"] = map[string]deploy.PackageOverrideChoiceV1{
		"Demo_Pkg": {Path: "{{ root }}/demo"},
	}
	if err := operation.CommitPackageOverridesV1(wantRaw); err != nil {
		t.Fatal(err)
	}
	raw, resolved, intent, err = LoadStagedPackageOverridesV1(operation, dir, document)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(raw, wantRaw) {
		t.Fatalf("raw overrides = %#v, want %#v", raw, wantRaw)
	}
	if got := resolved.Providers["python"]["demo-pkg"].Path; got != filepath.Join(dir, "workspace", "demo") {
		t.Fatalf("resolved path = %q", got)
	}
	if len(intent.Choices) != 1 || intent.Choices[0].Kind != "local" {
		t.Fatalf("intent = %#v", intent)
	}
}

func TestLoadStagedPackageOverridesV1RejectsWrongEnvironment(t *testing.T) {
	dir := t.TempDir()
	document := resolvedRequestTestDocument()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	if err := operation.CommitPackageOverridesV1(deploy.EmptyPackageOverridesV1("other")); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = LoadStagedPackageOverridesV1(operation, dir, document)
	if err == nil || !strings.Contains(err.Error(), "want") {
		t.Fatalf("error = %v", err)
	}
}
