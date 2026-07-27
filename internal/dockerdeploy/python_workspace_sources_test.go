package dockerdeploy

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func TestResolvePythonWorkspaceSourcesProducesSortedPathFreeManifests(t *testing.T) {
	root := t.TempDir()
	demo := filepath.Join(root, "demo")
	other := filepath.Join(root, "other")
	for _, directory := range []string{demo, other, filepath.Join(demo, ".git"), filepath.Join(demo, ".sl"), filepath.Join(demo, ".jj"), filepath.Join(demo, "__pycache__")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(demo, "pyproject.toml"), []byte("[build-system]\nrequires = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "pyproject.toml"), []byte("[build-system]\nrequires = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ignored := filepath.Join(demo, ".git", "index")
	if err := os.WriteFile(ignored, []byte("ignored one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(demo, ".sl", "store"), []byte("ignored Sapling metadata"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(demo, ".jj", "repo"), []byte("ignored Jujutsu metadata"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(demo, "__pycache__", "demo.pyc"), []byte("ignored bytecode"), 0o644); err != nil {
		t.Fatal(err)
	}
	document := blueprint.Document{Environment: blueprint.Environment{Workspace: blueprint.Workspace{
		PythonPackages: map[string]string{"Other": "other", "Demo_Pkg": "demo"},
	}}}

	sources, err := ResolvePythonWorkspaceSources(document, root)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{sources[0].Distribution, sources[1].Distribution}; !reflect.DeepEqual(got, []string{"demo-pkg", "other"}) {
		t.Fatalf("distributions = %#v", got)
	}
	if sources[0].HostDir != demo || sources[0].SourceManifestDigest == "" {
		t.Fatalf("demo source = %#v", sources[0])
	}
	clone := filepath.Join(t.TempDir(), "different-checkout")
	if err := os.Mkdir(clone, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "pyproject.toml"), []byte("[build-system]\nrequires = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, cloneDigest, err := ObservePythonSourceManifest(clone)
	if err != nil {
		t.Fatal(err)
	}
	if cloneDigest != sources[0].SourceManifestDigest {
		t.Fatalf("checkout path changed manifest: %s != %s", cloneDigest, sources[0].SourceManifestDigest)
	}
	for _, entry := range sources[0].Manifest.Entries {
		if strings.Contains(entry.Path, ".git") || strings.Contains(entry.Path, ".sl") || strings.Contains(entry.Path, ".jj") || strings.HasSuffix(entry.Path, ".pyc") {
			t.Fatalf("ignored entry appears in manifest: %#v", entry)
		}
	}
	original := sources[0].SourceManifestDigest
	if err := os.WriteFile(ignored, []byte("ignored two"), 0o644); err != nil {
		t.Fatal(err)
	}
	unchanged, err := ResolvePythonWorkspaceSources(document, root)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged[0].SourceManifestDigest != original {
		t.Fatalf("ignored content changed manifest: %s != %s", unchanged[0].SourceManifestDigest, original)
	}
	if err := os.Chmod(filepath.Join(demo, "pyproject.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, modeChangedDigest, err := ObservePythonSourceManifest(demo)
	if err != nil {
		t.Fatal(err)
	}
	if modeChangedDigest == original {
		t.Fatal("source permission change did not change manifest digest")
	}
	if err := os.Chmod(filepath.Join(demo, "pyproject.toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(demo, "pyproject.toml"), []byte("[build-system]\nrequires = [\"wheel\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := ResolvePythonWorkspaceSources(document, root)
	if err != nil {
		t.Fatal(err)
	}
	if changed[0].SourceManifestDigest == original {
		t.Fatal("source content change did not change manifest")
	}
}

func TestResolvePythonWorkspaceSourcesRejectsDuplicateNamesAndEscapes(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one", "two"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	document := blueprint.Document{Environment: blueprint.Environment{Workspace: blueprint.Workspace{
		PythonPackages: map[string]string{"demo_pkg": "one", "demo-pkg": "two"},
	}}}
	if _, err := ResolvePythonWorkspaceSources(document, root); err == nil || !strings.Contains(err.Error(), "both normalize") {
		t.Fatalf("duplicate-name error = %v", err)
	}

	document.Environment.Workspace.PythonPackages = map[string]string{"demo": "../outside"}
	if _, err := ResolvePythonWorkspaceSources(document, root); err == nil || !strings.Contains(err.Error(), "stay within") {
		t.Fatalf("relative escape error = %v", err)
	}

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	document.Environment.Workspace.PythonPackages = map[string]string{"demo": "linked"}
	if _, err := ResolvePythonWorkspaceSources(document, root); err == nil || !strings.Contains(err.Error(), "escapes workspace root") {
		t.Fatalf("source symlink escape error = %v", err)
	}
}

func TestResolvePythonWorkspaceSourcesRejectsInvalidDistributionBeforeFilesystemAccess(t *testing.T) {
	document := blueprint.Document{Environment: blueprint.Environment{Workspace: blueprint.Workspace{
		PythonPackages: map[string]string{"demo/pkg": "missing"},
	}}}
	_, err := ResolvePythonWorkspaceSources(document, "")
	if err == nil || !strings.Contains(err.Error(), "valid Python distribution name") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveSelectedPythonWorkspaceSourcesDoesNotReadUnselectedDirectories(t *testing.T) {
	root := t.TempDir()
	demo := filepath.Join(root, "demo")
	if err := os.Mkdir(demo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(demo, "pyproject.toml"), []byte("[project]\nname='demo'\nversion='1.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	document := blueprint.Document{Environment: blueprint.Environment{Workspace: blueprint.Workspace{
		PythonPackages: map[string]string{"demo": "demo", "unused": "missing"},
	}}}
	sources, err := ResolveSelectedPythonWorkspaceSources(document, root, []string{"demo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Distribution != "demo" || sources[0].HostDir != demo {
		t.Fatalf("selected sources = %#v", sources)
	}
	if sources, err := ResolveSelectedPythonWorkspaceSources(document, "", []string{}); err != nil || len(sources) != 0 {
		t.Fatalf("empty selected sources/error = %#v/%v", sources, err)
	}
}

func TestCompletePythonWorkspaceSourcesKeepsPriorObservation(t *testing.T) {
	root := t.TempDir()
	document := blueprint.Document{Environment: blueprint.Environment{Workspace: blueprint.Workspace{
		PythonPackages: map[string]string{"demo": "demo", "other": "other"},
	}}}
	for _, name := range []string{"demo", "other"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name, "pyproject.toml"), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	observed, err := ResolveSelectedPythonWorkspaceSources(document, root, []string{"demo"})
	if err != nil {
		t.Fatal(err)
	}
	original := observed[0].SourceManifestDigest
	if err := os.WriteFile(filepath.Join(root, "demo", "pyproject.toml"), []byte("changed after observation"), 0o644); err != nil {
		t.Fatal(err)
	}
	completed, err := CompletePythonWorkspaceSources(document, root, observed)
	if err != nil {
		t.Fatal(err)
	}
	if len(completed) != 2 || completed[0].Distribution != "demo" || completed[0].SourceManifestDigest != original || completed[1].Distribution != "other" {
		t.Fatalf("completed sources = %#v", completed)
	}
}

func TestObservePythonSourceManifestRejectsEscapingNestedSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.py")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside.py")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ObservePythonSourceManifest(root); err == nil || !strings.Contains(err.Error(), "absolute target") {
		t.Fatalf("nested symlink error = %v", err)
	}
}
