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
	if first[0].SourceInputDigest == second[0].SourceInputDigest {
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

func TestObservePythonSourceManifestLeavesPackagingBoundaryToBackend(t *testing.T) {
	sourceDir := t.TempDir()
	for _, directory := range []string{".venv-demo", ".pytest_cache", ".hg"} {
		if err := os.Mkdir(filepath.Join(sourceDir, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{
		"pyproject.toml":          "[build-system]\n",
		".venv-demo/generated.py": "generated = True\n",
		".pytest_cache/state":     "state\n",
		".git":                    "gitdir: ../shared/worktrees/demo\n",
		".hg/store":               "repository metadata\n",
	} {
		if err := os.WriteFile(filepath.Join(sourceDir, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../../outside", filepath.Join(sourceDir, "external-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../outside-vcs", filepath.Join(sourceDir, ".jj")); err != nil {
		t.Fatal(err)
	}
	manifest, _, err := ObservePythonSourceManifest(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]PythonSourceManifestEntryV1{}
	for _, entry := range manifest.Entries {
		paths[entry.Path] = entry
	}
	for _, want := range []string{".venv-demo/generated.py", ".pytest_cache/state", "external-link"} {
		if _, found := paths[want]; !found {
			t.Fatalf("backend-visible source input %q was omitted: %#v", want, manifest.Entries)
		}
	}
	for _, omitted := range []string{".git", ".hg/store", ".jj"} {
		if _, found := paths[omitted]; found {
			t.Fatalf("VCS metadata %q entered source input: %#v", omitted, manifest.Entries)
		}
	}
	if paths["external-link"].LinkTarget != "../../outside" {
		t.Fatalf("source link target = %#v", paths["external-link"])
	}
}

func TestObservePythonSourceManifestCanonicalizesDepthFirstTraversal(t *testing.T) {
	sourceDir := t.TempDir()
	packageRoot := filepath.Join(sourceDir, "site-packages")
	if err := os.MkdirAll(filepath.Join(packageRoot, "alabaster"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(packageRoot, "alabaster-1.0.0.dist-info"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(packageRoot, "alabaster", "theme.conf"),
		[]byte("theme"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	manifest, _, err := ObservePythonSourceManifest(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePythonSourceManifestV1(manifest); err != nil {
		t.Fatalf("observed manifest is not canonical: %v", err)
	}
	want := []string{
		"site-packages",
		"site-packages/alabaster",
		"site-packages/alabaster-1.0.0.dist-info",
		"site-packages/alabaster/theme.conf",
	}
	got := make([]string, len(manifest.Entries))
	for index, entry := range manifest.Entries {
		got[index] = entry.Path
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest paths = %#v, want %#v", got, want)
	}
}
