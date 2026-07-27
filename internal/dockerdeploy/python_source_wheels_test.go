package dockerdeploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

func TestPublishBuiltPythonSourceWheelsReplacesConflictingReusableWheel(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const wheelName = "demo_pkg-1.2.3-py3-none-any.whl"
	old, err := store.Publish(context.Background(), "wheels/"+wheelName, "wheel", strings.NewReader("stale wheel"))
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PreparePythonResolverArtifacts(store, []providerstore.ArtifactDescriptor{old})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	snapshots := stageSourceWheelTestSnapshot(t, prepared, "demo-pkg")
	output := filepath.Join(prepared.OutputHostDir, wheelName)
	writeReuseTestWheel(t, output, "demo-pkg", "1.2.3")

	sources, effective, err := PublishBuiltPythonSourceWheels(
		context.Background(), store, prepared, "application", snapshots, []providerstore.ArtifactDescriptor{old},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].LogicalPackage != "demo-pkg" ||
		sources[0].SourceManifestDigest != snapshots[0].SourceManifestDigest {
		t.Fatalf("sources = %#v", sources)
	}
	if err := pythonprovider.ValidateResolvedSourceInputV1(sources[0]); err != nil {
		t.Fatal(err)
	}
	if len(effective) != 1 || effective[0].SHA256 != sources[0].ArtifactDigest || effective[0].SHA256 == old.SHA256 {
		t.Fatalf("effective wheels = %#v; old = %#v", effective, old)
	}
	if err := providerstore.VerifyArtifactFile(filepath.Join(prepared.InputHostDir, wheelName), effective[0]); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(prepared.OutputHostDir); err != nil || len(entries) != 0 {
		t.Fatalf("source build output = %#v, %v", entries, err)
	}
	if info, err := os.Stat(prepared.InputHostDir); err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("resolver input mode = %v, %v", info, err)
	}
}

func TestPublishBuiltPythonSourceWheelsRejectsWrongDistribution(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PreparePythonResolverArtifacts(store, []providerstore.ArtifactDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	snapshots := stageSourceWheelTestSnapshot(t, prepared, "demo-pkg")
	writeReuseTestWheel(t, filepath.Join(prepared.OutputHostDir, "other-1.0-py3-none-any.whl"), "other", "1.0")

	if _, _, err := PublishBuiltPythonSourceWheels(
		context.Background(), store, prepared, "application", snapshots, []providerstore.ArtifactDescriptor{},
	); err == nil || !strings.Contains(err.Error(), "no wheel for distribution \"demo-pkg\"") {
		t.Fatalf("wrong-distribution error = %v", err)
	}
	if info, err := os.Stat(prepared.InputHostDir); err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("resolver input mode = %v, %v", info, err)
	}
}

func stageSourceWheelTestSnapshot(t *testing.T, prepared PreparedPythonResolverArtifacts, distribution string) []PreparedPythonSourceSnapshot {
	t.Helper()
	sources := sourceWheelTestWorkspaceSources(t, distribution)
	snapshots, err := StagePythonWorkspaceSourceSnapshots(prepared, sources)
	if err != nil {
		t.Fatal(err)
	}
	return snapshots
}

func sourceWheelTestWorkspaceSources(t *testing.T, distribution string) []PythonWorkspaceSource {
	t.Helper()
	workspace := t.TempDir()
	sourceDir := filepath.Join(workspace, "source")
	if err := os.Mkdir(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "pyproject.toml"), []byte("[build-system]\nrequires = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	document := blueprint.Document{Environment: blueprint.Environment{Workspace: blueprint.Workspace{
		PythonPackages: map[string]string{distribution: "source"},
	}}}
	sources, err := ResolvePythonWorkspaceSources(document, workspace)
	if err != nil {
		t.Fatal(err)
	}
	return sources
}
