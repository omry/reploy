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
  base:
    image: debian:13
  applications:
    tools:
      packages:
        os:
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
	if loaded.Document.Environment.ID != "apt-demo" ||
		len(loaded.Document.Environment.Applications["tools"].Packages.OS) != 1 {
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

func TestSourceProjectNameParsesTOML(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "basic string with inline comment",
			content: "[project]\nname = \"demo-server\" # retained comment\n",
			want:    "demo-server",
		},
		{
			name:    "literal string",
			content: "[project]\nname = 'demo-server'\n",
			want:    "demo-server",
		},
		{
			name:    "multiline basic string",
			content: "[project]\nname = \"\"\"demo-server\"\"\"\n",
			want:    "demo-server",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte(test.content), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := sourceProjectName(root)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("sourceProjectName() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSourceProjectNameRejectsInvalidProjectName(t *testing.T) {
	for _, test := range []struct {
		name      string
		content   string
		wantError string
	}{
		{
			name:      "missing",
			content:   "[project]\nversion = \"1.0\"\n",
			wantError: "requires [project].name",
		},
		{
			name:      "non-string",
			content:   "[project]\nname = 123\n",
			wantError: "requires [project].name to be a string",
		},
		{
			name:      "empty",
			content:   "[project]\nname = \"  \"\n",
			wantError: "requires [project].name to be non-empty",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte(test.content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := sourceProjectName(root); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("sourceProjectName() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}

func TestSourceBlueprintManifestPathDerivesNormalizedProjectSubdirFromTOML(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "pyproject.toml"),
		[]byte("[project]\nname = \"Demo.Server-Pkg\" # valid inline comment\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	blueprintDir := filepath.Join(root, "demo_server_pkg", "reploy")
	if err := os.MkdirAll(blueprintDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(blueprintDir, "demo.blueprint.yaml")
	if err := os.WriteFile(manifest, []byte(aptOnlyBlueprintFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	gotManifest, gotSubdir, err := sourceBlueprintManifestPath(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if gotManifest != manifest || gotSubdir != "demo_server_pkg/reploy" {
		t.Fatalf("sourceBlueprintManifestPath() = (%q, %q), want (%q, %q)", gotManifest, gotSubdir, manifest, "demo_server_pkg/reploy")
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

func TestLoadEnvironmentBlueprintManifestUsesAuthoritativeContent(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "demo.blueprint.yaml")
	if err := os.WriteFile(manifest, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, document, err := loadEnvironmentBlueprintManifest(
		manifest, []byte(aptOnlyBlueprintFixture), true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != aptOnlyBlueprintFixture || document.Environment.ID != "apt-demo" {
		t.Fatalf("loaded content = %q, document = %#v", content, document)
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

func TestLoadBlueprintRepairsTamperedPyPIManifestCache(t *testing.T) {
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
	if err := os.WriteFile(loaded.ManifestPath, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadBlueprint(ref)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Document.Environment.ID != "apt-demo" || reloaded.BlueprintSource != aptOnlyBlueprintFixture {
		t.Fatalf("reloaded blueprint = %#v", reloaded)
	}
	repaired, err := os.ReadFile(reloaded.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(repaired) != aptOnlyBlueprintFixture {
		t.Fatalf("repaired manifest = %q", repaired)
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
