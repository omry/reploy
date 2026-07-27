package dockerdeploy

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func TestPythonLocalOverridesV1ExtractsSortedLocatorsWithoutFilesystemReads(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	resolved := deploy.ResolvedPackageOverridesV1{
		EnvironmentID: "demo",
		Providers: map[string]map[string]deploy.ResolvedPackageOverrideChoiceV1{
			"python": {
				"other":    {Version: "2.0"},
				"demo-pkg": {Path: missing},
			},
		},
	}
	overrides, err := PythonLocalOverridesV1(resolved)
	if err != nil {
		t.Fatal(err)
	}
	want := []PythonLocalOverrideV1{{Distribution: "demo-pkg", HostDir: missing}}
	if !reflect.DeepEqual(overrides, want) {
		t.Fatalf("local overrides = %#v, want %#v", overrides, want)
	}
}

func TestObserveSelectedPythonLocalSourcesDoesNotReadUnselectedPaths(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "demo")
	if err := os.Mkdir(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(selected, "pyproject.toml"), []byte("[project]\nname='demo'\nversion='1.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	overrides := []PythonLocalOverrideV1{
		{Distribution: "demo", HostDir: selected},
		{Distribution: "unused", HostDir: filepath.Join(root, "missing")},
	}
	sources, err := ObserveSelectedPythonLocalSources(overrides, []string{"demo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Distribution != "demo" ||
		sources[0].HostDir != selected || len(sources[0].Manifest.Entries) == 0 {
		t.Fatalf("local sources = %#v", sources)
	}
}

func TestObserveSelectedPythonLocalSourcesBindsCurrentContent(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "demo")
	if err := os.Mkdir(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(sourceDir, "pyproject.toml")
	if err := os.WriteFile(filename, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	overrides := []PythonLocalOverrideV1{{Distribution: "demo", HostDir: sourceDir}}
	first, err := ObserveSelectedPythonLocalSources(overrides, []string{"demo"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := ObserveSelectedPythonLocalSources(overrides, []string{"demo"})
	if err != nil {
		t.Fatal(err)
	}
	if first[0].SourceManifestDigest == second[0].SourceManifestDigest {
		t.Fatal("source content change did not change the manifest digest")
	}
}

func TestObserveSelectedPythonLocalSourcesRejectsMissingSelectedPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	_, err := ObserveSelectedPythonLocalSources(
		[]PythonLocalOverrideV1{{Distribution: "demo", HostDir: missing}},
		[]string{"demo"},
	)
	if err == nil || !strings.Contains(err.Error(), `local Python override "demo" source`) {
		t.Fatalf("missing-path error = %v", err)
	}
}
