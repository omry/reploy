package dockerdeploy

import (
	"path/filepath"
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

	resolved, intent, err := LoadStagedPackageOverridesV1(operation, dir, document)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Providers) != 0 || len(intent.Choices) != 0 {
		t.Fatalf("missing sidecar resolved to %#v / %#v", resolved, intent)
	}

	raw := deploy.EmptyPackageOverridesV1(document.Environment.ID)
	raw.Environment.Vars["root"] = filepath.Join(dir, "workspace")
	raw.Environment.PackageOverrides["python"] = map[string]deploy.PackageOverrideChoiceV1{
		"Demo_Pkg": {Path: "{{ root }}/demo"},
	}
	if err := operation.CommitPackageOverridesV1(raw); err != nil {
		t.Fatal(err)
	}
	resolved, intent, err = LoadStagedPackageOverridesV1(operation, dir, document)
	if err != nil {
		t.Fatal(err)
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
	_, _, err = LoadStagedPackageOverridesV1(operation, dir, document)
	if err == nil || !strings.Contains(err.Error(), "want") {
		t.Fatalf("error = %v", err)
	}
}
