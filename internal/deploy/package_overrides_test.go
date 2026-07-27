package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

func TestPackageOverridesRoundTripAndResolve(t *testing.T) {
	dir := t.TempDir()
	overrides := PackageOverridesV1{Environment: PackageOverridesEnvironmentV1{
		ID: "demo",
		Vars: map[string]any{
			"workspace_root": filepath.Join(dir, "workspace"),
		},
		PackageOverrides: map[string]map[string]PackageOverrideChoiceV1{
			"python": {
				"Demo_Pkg": {Path: "{{ workspace_root }}/demo"},
				"other":    {Version: "2.4.0"},
			},
		},
	}}
	content, err := EncodePackageOverridesV1(overrides)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePackageOverridesV1(content)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolvePackageOverridesV1(decoded, dir, func(provider string, packageID string, _ PackageOverrideChoiceV1) (string, error) {
		if provider != "python" {
			t.Fatalf("provider = %q", provider)
		}
		return pythonprovider.NormalizeDistributionName(packageID), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.EnvironmentID != "demo" {
		t.Fatalf("environment ID = %q", resolved.EnvironmentID)
	}
	if got := resolved.Providers["python"]["demo-pkg"].Path; got != filepath.Join(dir, "workspace", "demo") {
		t.Fatalf("resolved local path = %q", got)
	}
	if got := resolved.Providers["python"]["other"].Version; got != "2.4.0" {
		t.Fatalf("resolved version = %q", got)
	}
	intent, err := resolved.Intent()
	if err != nil {
		t.Fatal(err)
	}
	if len(intent.Choices) != 2 ||
		intent.Choices[0] != (PackageOverrideIntentChoiceV1{Provider: "python", Package: "demo-pkg", Kind: "local"}) ||
		intent.Choices[1] != (PackageOverrideIntentChoiceV1{Provider: "python", Package: "other", Kind: "version", Version: "2.4.0"}) {
		t.Fatalf("intent = %#v", intent)
	}
}

func TestPackageOverridesRejectInvalidShape(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{name: "unknown field", yaml: "environment:\n  id: demo\n  package_overrides: {}\n  mystery: true\n", want: "field mystery not found"},
		{name: "multiple documents", yaml: "environment:\n  id: demo\n  package_overrides: {}\n---\n{}\n", want: "multiple YAML documents"},
		{name: "both choices", yaml: "environment:\n  id: demo\n  package_overrides:\n    python:\n      demo: {path: ../demo, version: 1.0}\n", want: "exactly one"},
		{name: "neither choice", yaml: "environment:\n  id: demo\n  package_overrides:\n    python:\n      demo: {}\n", want: "exactly one"},
		{name: "missing mappings", yaml: "environment:\n  id: demo\n", want: "package_overrides must use a mapping"},
		{name: "variable cycle", yaml: "environment:\n  id: demo\n  vars: {a: '{{ b }}', b: '{{ a }}'}\n  package_overrides: {}\n", want: "cycle"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodePackageOverridesV1([]byte(test.yaml)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestResolvePackageOverridesDoesNotInspectPaths(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	overrides := PackageOverridesV1{Environment: PackageOverridesEnvironmentV1{
		ID: "demo", Vars: map[string]any{}, PackageOverrides: map[string]map[string]PackageOverrideChoiceV1{
			"python": {"unused": {Path: missing}},
		},
	}}
	resolved, err := ResolvePackageOverridesV1(overrides, dir, func(_ string, packageID string, _ PackageOverrideChoiceV1) (string, error) {
		return packageID, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Providers["python"]["unused"].Path != missing {
		t.Fatalf("resolved path = %q", resolved.Providers["python"]["unused"].Path)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("unused path was unexpectedly created or inspected: %v", err)
	}
}

func TestResolvePackageOverridesRejectsNormalizedDuplicates(t *testing.T) {
	dir := t.TempDir()
	overrides := PackageOverridesV1{Environment: PackageOverridesEnvironmentV1{
		ID: "demo", Vars: map[string]any{}, PackageOverrides: map[string]map[string]PackageOverrideChoiceV1{
			"python": {
				"demo_pkg": {Version: "1"},
				"demo-pkg": {Version: "2"},
			},
		},
	}}
	_, err := ResolvePackageOverridesV1(overrides, dir, func(_ string, packageID string, _ PackageOverrideChoiceV1) (string, error) {
		return pythonprovider.NormalizeDistributionName(packageID), nil
	})
	if err == nil || !strings.Contains(err.Error(), "both normalize") {
		t.Fatalf("error = %v", err)
	}
}

func TestCommitPackageOverridesUsesDeploymentLockAndAtomicFile(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	overrides := EmptyPackageOverridesV1("demo")
	overrides.Environment.PackageOverrides["python"] = map[string]PackageOverrideChoiceV1{
		"demo": {Version: "1.0"},
	}
	if err := lock.CommitPackageOverridesV1(overrides); err != nil {
		t.Fatal(err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := ReadPackageOverridesV1(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !found || loaded.Environment.PackageOverrides["python"]["demo"].Version != "1.0" {
		t.Fatalf("loaded overrides = %#v, found = %v", loaded, found)
	}
	info, err := os.Stat(filepath.Join(dir, PackageOverridesFilename))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o", got)
	}
}
