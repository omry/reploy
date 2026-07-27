package deploy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

func TestPackageOverridesRoundTripAndResolve(t *testing.T) {
	dir := t.TempDir()
	overrides := PackageOverridesV1{Environment: PackageOverridesEnvironmentV1{
		ID: "demo",
		Vars: map[string]any{
			"workspace_root": filepath.Join(dir, "workspace"),
		},
		PackageAdditions: map[string][]string{
			"os": {"default-jre-headless"},
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
	if got := resolved.Additions["os"]; len(got) != 1 || got[0] != "default-jre-headless" {
		t.Fatalf("resolved additions = %#v", resolved.Additions)
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
	if len(intent.Additions) != 1 ||
		intent.Additions[0] != (PackageAdditionIntentV1{Provider: "os", Requirement: "default-jre-headless"}) {
		t.Fatalf("intent additions = %#v", intent.Additions)
	}
}

func TestPackageOverridesRoundTripsBaseImageOverride(t *testing.T) {
	overrides := EmptyPackageOverridesV1("demo")
	overrides.Environment.Base = &BaseImageOverrideV1{Image: "python:3.13-slim"}
	content, err := EncodePackageOverridesV1(overrides)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "base:") || !strings.Contains(string(content), "image: python:3.13-slim") {
		t.Fatalf("encoded overrides = %q", content)
	}
	decoded, err := DecodePackageOverridesV1(content)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Environment.Base == nil || decoded.Environment.Base.Image != "python:3.13-slim" {
		t.Fatalf("decoded base override = %#v", decoded.Environment.Base)
	}
}

func TestEffectiveBaseImageUsesOverrideOrBlueprint(t *testing.T) {
	document := blueprint.Document{Environment: blueprint.Environment{
		ID:   "demo",
		Base: blueprint.BaseComponent{Image: "python:3.11-slim"},
	}}
	overrides := EmptyPackageOverridesV1("demo")
	image, err := EffectiveBaseImageV1(document, overrides)
	if err != nil || image != "python:3.11-slim" {
		t.Fatalf("blueprint base = %q, error = %v", image, err)
	}
	overrides.Environment.Base = &BaseImageOverrideV1{Image: "sha256:" + strings.Repeat("a", 64)}
	image, err = EffectiveBaseImageV1(document, overrides)
	if err != nil || image != overrides.Environment.Base.Image {
		t.Fatalf("overridden base = %q, error = %v", image, err)
	}
}

func TestResolvePackageOverridesExpandsWorkspaceHomeAtUse(t *testing.T) {
	home := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("USERPROFILE", home)
	default:
		t.Setenv("HOME", home)
	}
	dir := t.TempDir()
	overrides := PackageOverridesV1{Environment: PackageOverridesEnvironmentV1{
		ID: "demo",
		Vars: map[string]any{
			"workspace_root": "~/dev",
			"project_root":   "{{ workspace_root }}/projects",
		},
		PackageOverrides: map[string]map[string]PackageOverrideChoiceV1{
			"python": {
				"demo":  {Path: "{{ workspace_root }}/demo"},
				"other": {Path: "{{ project_root }}/other"},
			},
		},
	}}
	resolved, err := ResolvePackageOverridesV1(
		overrides,
		dir,
		func(_ string, packageID string, _ PackageOverrideChoiceV1) (string, error) {
			return packageID, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := resolved.Providers["python"]["demo"].Path, filepath.Join(home, "dev", "demo"); got != want {
		t.Fatalf("resolved local path = %q, want %q", got, want)
	}
	if got, want := resolved.Providers["python"]["other"].Path, filepath.Join(home, "dev", "projects", "other"); got != want {
		t.Fatalf("derived local path = %q, want %q", got, want)
	}
	if got := overrides.Environment.Vars["workspace_root"]; got != "~/dev" {
		t.Fatalf("source workspace_root was mutated to %#v", got)
	}
}

func TestResolvePackageOverridesRejectsNonStringWorkspaceRoot(t *testing.T) {
	dir := t.TempDir()
	overrides := PackageOverridesV1{Environment: PackageOverridesEnvironmentV1{
		ID: "demo",
		Vars: map[string]any{
			"workspace_root": 42,
		},
		PackageOverrides: map[string]map[string]PackageOverrideChoiceV1{
			"python": {
				"demo": {Path: "{{ workspace_root }}/demo"},
			},
		},
	}}
	_, err := ResolvePackageOverridesV1(
		overrides,
		dir,
		func(_ string, packageID string, _ PackageOverrideChoiceV1) (string, error) {
			return packageID, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "workspace_root must resolve to a string") {
		t.Fatalf("error = %v", err)
	}
}

func TestPackageOverridesRejectsControlCharactersInResolvedWorkspaceRoot(t *testing.T) {
	overrides := EmptyPackageOverridesV1("demo")
	overrides.Environment.Vars = map[string]any{
		"workspace_base": "/workspace\nforged",
		"workspace_root": "{{ workspace_base }}",
	}

	err := ValidatePackageOverridesV1(overrides)
	if err == nil || !strings.Contains(err.Error(), "workspace_root must not contain control characters") {
		t.Fatalf("error = %v", err)
	}
}

func TestPackageOverridesRejectInvalidShape(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{name: "unknown field", yaml: "environment:\n  id: demo\n  package_overrides: {}\n  mystery: true\n", want: "field mystery not found"},
		{name: "empty base", yaml: "environment:\n  id: demo\n  base: {}\n  package_overrides: {}\n", want: "base.image"},
		{name: "short local image ID", yaml: "environment:\n  id: demo\n  base: {image: 'sha256:abcd'}\n  package_overrides: {}\n", want: "local image ID"},
		{name: "multiple documents", yaml: "environment:\n  id: demo\n  package_overrides: {}\n---\n{}\n", want: "multiple YAML documents"},
		{name: "both choices", yaml: "environment:\n  id: demo\n  package_overrides:\n    python:\n      demo: {path: ../demo, version: 1.0}\n", want: "exactly one"},
		{name: "neither choice", yaml: "environment:\n  id: demo\n  package_overrides:\n    python:\n      demo: {}\n", want: "exactly one"},
		{name: "missing mappings", yaml: "environment:\n  id: demo\n", want: "package_overrides must use a mapping"},
		{name: "variable cycle", yaml: "environment:\n  id: demo\n  vars: {a: '{{ b }}', b: '{{ a }}'}\n  package_overrides: {}\n", want: "cycle"},
		{name: "unsupported addition provider", yaml: "environment:\n  id: demo\n  package_additions: {apt: [default-jre-headless]}\n  package_overrides: {}\n", want: "use os"},
		{name: "invalid native package", yaml: "environment:\n  id: demo\n  package_additions: {os: [Java]}\n  package_overrides: {}\n", want: "APT package"},
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
