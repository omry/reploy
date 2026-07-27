package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const aptOnlyBlueprintFixture = `blueprint:
  schema: 1
  version: 0.1.0
  compatibility:
    platforms: [linux/amd64]
environment:
  id: apt-demo
  components:
    base:
      image: debian:13
    tools:
      type: apt
      packages:
        - package: curl
docker: {}
`

func TestLoadBlueprintAcceptsAPTOnlyEnvironment(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "apt-demo.blueprint.yaml")
	if err := os.WriteFile(manifest, []byte(aptOnlyBlueprintFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := ParsePackRef("file:" + dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadBlueprint(ref)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Document.Environment.ID != "apt-demo" || loaded.Document.Environment.Components["tools"].Type != "apt" {
		t.Fatalf("loaded blueprint = %#v", loaded.Document)
	}
	if loaded.ManifestPath != manifest || loaded.Ref.Source != manifest || loaded.RequestedRef.Raw != ref.Raw {
		t.Fatalf("loaded reference = %#v", loaded)
	}
}

func TestSourceBlueprintManifestPathRejectsPathOutsideSourceRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "source")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside.blueprint.yaml")
	if err := os.WriteFile(outside, []byte(aptOnlyBlueprintFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sourceBlueprintManifestPath(root, "../outside.blueprint.yaml"); err == nil || !strings.Contains(err.Error(), "must stay inside the source root") {
		t.Fatalf("source escape error = %v", err)
	}
}

func TestSourceBlueprintManifestPathRejectsSymlinkOutsideSourceRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "source")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside.blueprint.yaml")
	if err := os.WriteFile(outside, []byte(aptOnlyBlueprintFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "linked.blueprint.yaml")
	if err := os.Symlink(outside, linked); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := sourceBlueprintManifestPath(root, "linked.blueprint.yaml"); err == nil || !strings.Contains(err.Error(), "must stay inside the source root") {
		t.Fatalf("source symlink escape error = %v", err)
	}
}
func TestLoadBlueprintResolvesRelativeWorkspaceRootFromManifestDirectory(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "pkg", "reploy", "apt-demo.blueprint.yaml")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := strings.Replace(aptOnlyBlueprintFixture, "  id: apt-demo\n", "  id: apt-demo\n  workspace:\n    root: ../..\n    packages:\n      python: {demo: src}\n", 1)
	if err := os.WriteFile(manifest, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := ParsePackRef("file:" + manifest)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadBlueprint(ref)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.WorkspaceRoot != root {
		t.Fatalf("workspace root = %q, want %q", loaded.WorkspaceRoot, root)
	}
	if loaded.Document.Environment.Workspace.Root != "../.." {
		t.Fatalf("resolved blueprint retained root = %q", loaded.Document.Environment.Workspace.Root)
	}
}

func TestLoadBlueprintPreservesAbsoluteAndOmittedWorkspaceRoots(t *testing.T) {
	absoluteRoot := t.TempDir()
	for _, test := range []struct {
		name     string
		root     string
		expected string
	}{
		{name: "absolute", root: absoluteRoot, expected: absoluteRoot},
		{name: "omitted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			manifest := filepath.Join(dir, "apt-demo.blueprint.yaml")
			fixture := aptOnlyBlueprintFixture
			if test.root != "" {
				fixture = strings.Replace(fixture, "  id: apt-demo\n", "  id: apt-demo\n  workspace:\n    root: "+filepath.ToSlash(test.root)+"\n", 1)
			}
			if err := os.WriteFile(manifest, []byte(fixture), 0o644); err != nil {
				t.Fatal(err)
			}
			ref, err := ParsePackRef("file:" + manifest)
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := LoadBlueprint(ref)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.WorkspaceRoot != test.expected {
				t.Fatalf("workspace root = %q, want %q", loaded.WorkspaceRoot, test.expected)
			}
		})
	}
}

func TestLoadBlueprintRejectsSemanticErrors(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "invalid.blueprint.yaml")
	invalid := strings.Replace(aptOnlyBlueprintFixture, "  version: 0.1.0\n", "", 1)
	if err := os.WriteFile(manifest, []byte(invalid), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := ParsePackRef("file:" + manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBlueprint(ref); err == nil || !strings.Contains(err.Error(), "blueprint.version is required") {
		t.Fatalf("semantic validation error = %v", err)
	}
}

func TestLoadBlueprintResolvesPyPIReference(t *testing.T) {
	blueprintPath := "demo_pkg/reploy/demo.blueprint.yaml"
	wheel := testPackWheelWithFiles(t, map[string]string{
		blueprintPath: aptOnlyBlueprintFixture,
	})
	indexURL := testPyPIIndex(t, wheel, "1.2.3")
	t.Setenv("REPLOY_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	ref, err := ParsePackRef("pypi:demo-pkg==1.2.3#" + blueprintPath + "?index-url=" + indexURL)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadBlueprint(ref)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Document.Environment.ID != "apt-demo" || !loaded.Ref.IsPinned {
		t.Fatalf("loaded blueprint = %#v", loaded)
	}
	if loaded.ResolvedArtifact == nil || loaded.ResolvedArtifact.BlueprintPath != loaded.ManifestPath {
		t.Fatalf("resolved artifact = %#v, manifest = %q", loaded.ResolvedArtifact, loaded.ManifestPath)
	}
}

func TestLoadBlueprintResolvesGitReference(t *testing.T) {
	sourceRoot, commit := testGitBlueprintSource(t, aptOnlyBlueprintFixture)
	t.Setenv("REPLOY_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	sourceURL := localFileURL(sourceRoot)
	ref, err := ParsePackRef("git:" + sourceURL + "#demo_server/reploy/demo.blueprint.yaml?ref=main")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadBlueprint(ref)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Document.Environment.ID != "apt-demo" || loaded.Ref.Query.Get("ref") != commit {
		t.Fatalf("loaded blueprint = %#v", loaded)
	}
	if loaded.ResolvedArtifact == nil || loaded.ResolvedArtifact.BlueprintPath != filepath.Dir(loaded.ManifestPath) {
		t.Fatalf("resolved artifact = %#v, manifest = %q", loaded.ResolvedArtifact, loaded.ManifestPath)
	}
}
