package dockerdeploy

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
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
	distributions := stageSourceWheelTestDistribution(t, prepared, "demo-pkg", "1.2.3")
	output := filepath.Join(prepared.OutputHostDir, wheelName)
	writeReuseTestWheel(t, output, "demo-pkg", "1.2.3")

	sources, effective, err := PublishBuiltPythonSourceWheels(
		context.Background(), store, prepared, "application", distributions, []providerstore.ArtifactDescriptor{old},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].LogicalPackage != "demo-pkg" ||
		sources[0].SourceInputDigest != distributions[0].SourceInputDigest {
		t.Fatalf("sources = %#v", sources)
	}
	if err := pythonprovider.ValidateResolvedSourceInputV2(sources[0]); err != nil {
		t.Fatal(err)
	}
	if len(effective) != 1 || effective[0].SHA256 != sources[0].OutputArtifactDigest || effective[0].SHA256 == old.SHA256 {
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
	distributions := stageSourceWheelTestDistribution(t, prepared, "demo-pkg", "1.0")
	writeReuseTestWheel(t, filepath.Join(prepared.OutputHostDir, "other-1.0-py3-none-any.whl"), "other", "1.0")

	if _, _, err := PublishBuiltPythonSourceWheels(
		context.Background(), store, prepared, "application", distributions, []providerstore.ArtifactDescriptor{},
	); err == nil || !strings.Contains(err.Error(), "no wheel for distribution \"demo-pkg\"") {
		t.Fatalf("wrong-distribution error = %v", err)
	}
	if info, err := os.Stat(prepared.InputHostDir); err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("resolver input mode = %v, %v", info, err)
	}
}

func TestFilterPythonSourcesForBuildEnvironmentDropsStaleSourceWheel(t *testing.T) {
	currentEnvironment := canonical.Digest("sha256:" + strings.Repeat("e", 64))
	matchingWheel := providerstore.ArtifactDescriptor{
		LogicalPath: "wheels/matching-1-py3-none-any.whl", Kind: "wheel", Size: "1",
		SHA256: canonical.Digest("sha256:" + strings.Repeat("1", 64)),
	}
	staleWheel := providerstore.ArtifactDescriptor{
		LogicalPath: "wheels/stale-1-py3-none-any.whl", Kind: "wheel", Size: "1",
		SHA256: canonical.Digest("sha256:" + strings.Repeat("2", 64)),
	}
	matching := testPythonResolvedSource(
		"application", "matching", "1", canonical.Digest("sha256:"+strings.Repeat("3", 64)), matchingWheel.SHA256,
	)
	matching.BuildEnvironmentDigest = currentEnvironment
	stale := testPythonResolvedSource(
		"application", "stale", "1", canonical.Digest("sha256:"+strings.Repeat("4", 64)), staleWheel.SHA256,
	)
	stale.BuildEnvironmentDigest = canonical.Digest("sha256:" + strings.Repeat("f", 64))

	sources, wheels, err := filterPythonSourcesForBuildEnvironment(
		[]providers.ResolvedSourceInput{matching, stale},
		[]providerstore.ArtifactDescriptor{matchingWheel, staleWheel},
		currentEnvironment,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || !reflect.DeepEqual(sources[0], matching) ||
		len(wheels) != 1 || wheels[0] != matchingWheel {
		t.Fatalf("sources = %#v, wheels = %#v", sources, wheels)
	}
}

func TestPublishBuiltPythonSourceDistributionsRejectsDirectWheelFallback(t *testing.T) {
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
	if err := os.WriteFile(
		filepath.Join(prepared.OutputHostDir, "demo_pkg-1-py3-none-any.whl"),
		[]byte("not an sdist"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishBuiltPythonSourceDistributions(
		context.Background(), store, prepared, snapshots, snapshots[0].SourceInputDigest,
	); err == nil || !strings.Contains(err.Error(), "direct-wheel fallback is not supported") {
		t.Fatalf("direct-wheel error = %v", err)
	}
}

func stageSourceWheelTestSnapshot(t *testing.T, prepared PreparedPythonResolverArtifacts, distribution string) []PreparedPythonSourceSnapshot {
	t.Helper()
	sources := sourceWheelTestLocalSources(t, distribution)
	snapshots, err := StagePythonLocalSourceSnapshots(prepared, sources)
	if err != nil {
		t.Fatal(err)
	}
	return snapshots
}

func stageSourceWheelTestDistribution(
	t *testing.T,
	prepared PreparedPythonResolverArtifacts,
	distribution string,
	version string,
) []PreparedPythonSourceDistribution {
	t.Helper()
	snapshots := stageSourceWheelTestSnapshot(t, prepared, distribution)
	return stageSourceWheelTestDistributionFromSnapshot(t, prepared, snapshots[0], version)
}

func stageSourceWheelTestDistributionFromSnapshot(
	t *testing.T,
	prepared PreparedPythonResolverArtifacts,
	snapshot PreparedPythonSourceSnapshot,
	version string,
) []PreparedPythonSourceDistribution {
	t.Helper()
	distribution := snapshot.Distribution
	if err := os.Chmod(prepared.InputHostDir, 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(prepared.InputHostDir, pythonSourceDistributionDirectory)
	hostDir := filepath.Join(root, distribution)
	archiveRoot := distribution + "-" + version
	if err := os.MkdirAll(filepath.Join(hostDir, archiveRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(hostDir, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(prepared.InputHostDir, 0o500); err != nil {
		t.Fatal(err)
	}
	return []PreparedPythonSourceDistribution{{
		Distribution: distribution, Version: version, ArchiveRoot: archiveRoot,
		HostDir: hostDir,
		ContainerDir: path.Join(
			prepared.InputContainerDir, pythonSourceDistributionDirectory, distribution,
		),
		SourceInputDigest:      snapshot.SourceInputDigest,
		BuildEnvironmentDigest: snapshot.SourceInputDigest,
		BuilderProfile:         pythonprovider.SourceBuilderProfileV1,
		BuildSettings:          pythonprovider.CanonicalSourceBuildSettingsV1(),
		Artifact: providerstore.ArtifactDescriptor{
			LogicalPath: "sdists/" + distribution + "-" + version + ".tar.gz",
			Kind:        "sdist", Size: "1", SHA256: snapshot.SourceInputDigest,
		},
	}}
}

func sourceWheelTestLocalSources(t *testing.T, distribution string) []PythonLocalSource {
	t.Helper()
	workspace := t.TempDir()
	sourceDir := filepath.Join(workspace, "source")
	if err := os.Mkdir(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "pyproject.toml"), []byte("[build-system]\nrequires = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, digest, err := ObservePythonSourceManifest(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	return []PythonLocalSource{{
		Distribution: distribution, HostDir: sourceDir,
		Manifest: manifest, SourceInputDigest: digest,
	}}
}
