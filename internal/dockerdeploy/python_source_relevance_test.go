package dockerdeploy

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

func TestReusablePythonLocalSourcesLearnsSdistRelevantDirectories(t *testing.T) {
	deploymentDir := t.TempDir()
	store, err := providerstore.NewStore(deploymentDir)
	if err != nil {
		t.Fatal(err)
	}
	sourceDir := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(filepath.Join(sourceDir, ".venv"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(relative string, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(sourceDir, relative), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("pyproject.toml", "[build-system]\n")
	write("demo_server.py", "value = 1\n")
	write(filepath.Join(".venv", "generated.py"), "ignored = 1\n")
	_, sourceInput, err := ObservePythonSourceManifest(sourceDir)
	if err != nil {
		t.Fatal(err)
	}

	archive := filepath.Join(t.TempDir(), "demo-1.tar.gz")
	writeDockerdeployTestSourceDistribution(t, archive, "demo-1", "demo", "1")
	descriptor, _, err := pythonprovider.DescribeSourceDistributionFileV1(
		archive,
		"sdists/demo-1.tar.gz",
	)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	_, publishErr := store.PublishExpected(t.Context(), descriptor, file)
	closeErr := file.Close()
	if publishErr != nil || closeErr != nil {
		t.Fatal(publishErr, closeErr)
	}
	locked := testPythonResolvedSourceWithSourceArtifact(
		"application",
		"demo",
		"1",
		sourceInput,
		descriptor,
		canonical.Digest("sha256:"+strings.Repeat("f", 64)),
	)
	overrides := []PythonLocalOverrideV1{{Distribution: "demo", HostDir: sourceDir}}

	reusable, err := ReusablePythonLocalSourcesV1(store, overrides, []providers.ResolvedSourceInput{locked})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reusable, []providers.ResolvedSourceInput{locked}) {
		t.Fatalf("first reusable sources = %#v", reusable)
	}
	relevance, found := readPythonSourceRelevance(store.Root(), "demo", sourceDir)
	if !found {
		t.Fatal("full observation did not learn source relevance")
	}
	if !reflect.DeepEqual(relevance.WatchedRootFiles, []string{
		".reploy.yaml", "MANIFEST.in", "demo_server.py", "pyproject.toml", "setup.cfg", "setup.py",
	}) || len(relevance.RelevantDirs) != 0 {
		t.Fatalf("learned relevance = %#v", relevance)
	}

	write(filepath.Join(".venv", "generated.py"), "ignored = 2\n")
	_, changedFullDigest, err := ObservePythonSourceManifest(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	if changedFullDigest == sourceInput {
		t.Fatal("fixture did not change the complete source identity")
	}
	reusable, err = ReusablePythonLocalSourcesV1(store, overrides, []providers.ResolvedSourceInput{locked})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reusable, []providers.ResolvedSourceInput{locked}) {
		t.Fatalf("irrelevant environment change invalidated learned source: %#v", reusable)
	}

	write("demo_server.py", "value = 2\n")
	reusable, err = ReusablePythonLocalSourcesV1(store, overrides, []providers.ResolvedSourceInput{locked})
	if err != nil {
		t.Fatal(err)
	}
	if len(reusable) != 0 {
		t.Fatalf("relevant source change retained learned source: %#v", reusable)
	}
}

func TestPythonSourceRelevanceDetectsRootTopologyAndBuildControlChanges(t *testing.T) {
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "pyproject.toml"), []byte("[build-system]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "demo.py"), []byte("value = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, digest, err := ObservePythonSourceManifest(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	relevance := pythonSourceRelevanceV1{
		Schema: pythonSourceRelevanceSchemaV1, Distribution: "demo",
		SourceDir: sourceDir, SourceInputDigest: digest,
		RootTopology: pythonSourceRootTopologyFromManifest(manifest),
		RelevantDirs: []string{}, WatchedRootFiles: []string{
			".reploy.yaml", "demo.py", "pyproject.toml",
		},
		RelevantManifest: manifest,
	}
	unchanged, err := pythonSourceRelevanceUnchanged(relevance)
	if err != nil || !unchanged {
		t.Fatalf("initial relevance = %v, %v", unchanged, err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, ".reploy.yaml"), []byte("build: pep517\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unchanged, err = pythonSourceRelevanceUnchanged(relevance)
	if err != nil || unchanged {
		t.Fatalf("build control change = %v, %v", unchanged, err)
	}
	if err := os.Remove(filepath.Join(sourceDir, ".reploy.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(sourceDir, "new-package"), 0o755); err != nil {
		t.Fatal(err)
	}
	unchanged, err = pythonSourceRelevanceUnchanged(relevance)
	if err != nil || unchanged {
		t.Fatalf("root topology change = %v, %v", unchanged, err)
	}
}

func TestPythonSourceRelevanceRejectsInternalSymlinkCaches(t *testing.T) {
	t.Run("root symlink", func(t *testing.T) {
		sourceDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(sourceDir, "packages", "demo"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sourceDir, "pyproject.toml"), []byte("[build-system]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sourceDir, "packages", "demo", "code.py"), []byte("value = 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join("packages", "demo"), filepath.Join(sourceDir, "demo")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		manifest, digest, err := ObservePythonSourceManifest(sourceDir)
		if err != nil {
			t.Fatal(err)
		}
		controlDigest, err := digestPythonSourceFile(filepath.Join(sourceDir, "pyproject.toml"))
		if err != nil {
			t.Fatal(err)
		}
		relevance := pythonSourceRelevanceV1{
			Schema: pythonSourceRelevanceSchemaV1, Distribution: "demo",
			SourceDir: sourceDir, SourceInputDigest: digest,
			RootTopology: pythonSourceRootTopologyFromManifest(manifest),
			RelevantDirs: []string{}, WatchedRootFiles: []string{"pyproject.toml"},
			RelevantManifest: PythonSourceManifestV1{
				Schema: pythonSourceManifestSchemaV1,
				Entries: []PythonSourceManifestEntryV1{{
					Path: "pyproject.toml", Kind: "file", Mode: "0644",
					ContentDigest: controlDigest,
				}},
			},
		}
		unchanged, err := pythonSourceRelevanceUnchanged(relevance)
		if err != nil || unchanged {
			t.Fatalf("root-symlink relevance = %v, %v", unchanged, err)
		}
	})

	t.Run("relevant nested symlink", func(t *testing.T) {
		relevance := pythonSourceRelevanceV1{
			Schema: pythonSourceRelevanceSchemaV1, Distribution: "demo",
			SourceDir:         filepath.Join(t.TempDir(), "demo"),
			SourceInputDigest: canonical.Digest("sha256:" + strings.Repeat("a", 64)),
			RootTopology:      []pythonSourceRootEntryV1{{Path: "src", Kind: "directory"}},
			RelevantDirs:      []string{"src"}, WatchedRootFiles: []string{},
			RelevantManifest: PythonSourceManifestV1{
				Schema: pythonSourceManifestSchemaV1,
				Entries: []PythonSourceManifestEntryV1{
					{Path: "src", Kind: "directory", Mode: "0755"},
					{Path: "src/demo", Kind: "symlink", LinkTarget: "../packages/demo"},
				},
			},
		}
		if err := validatePythonSourceRelevance(relevance); err == nil ||
			!strings.Contains(err.Error(), "relevant symlink") {
			t.Fatalf("nested-symlink relevance error = %v", err)
		}
	})
}
