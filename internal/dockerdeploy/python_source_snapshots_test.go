package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/providerstore"
)

func TestStagePythonLocalSourceSnapshotsCopiesOnlyManifestEntries(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PreparePythonResolverArtifacts(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	workspace := t.TempDir()
	sourceDir := filepath.Join(workspace, "demo")
	if err := os.MkdirAll(filepath.Join(sourceDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(sourceDir, "tool.py")
	if err := os.WriteFile(program, []byte("print('original')\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, ".git", "index"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, digest, err := ObservePythonSourceManifest(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	sources := []PythonLocalSource{{
		Distribution: "demo-pkg", HostDir: sourceDir,
		Manifest: manifest, SourceInputDigest: digest,
	}}

	snapshots, err := StagePythonLocalSourceSnapshots(prepared, sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || snapshots[0].Distribution != "demo-pkg" ||
		snapshots[0].ContainerDir != prepared.InputContainerDir+"/sources/demo-pkg" ||
		snapshots[0].SourceInputDigest != sources[0].SourceInputDigest {
		t.Fatalf("snapshots = %#v", snapshots)
	}
	snapshotProgram := filepath.Join(snapshots[0].HostDir, "tool.py")
	content, err := os.ReadFile(snapshotProgram)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "print('original')\n" {
		t.Fatalf("snapshot content = %q", content)
	}
	info, err := os.Stat(snapshotProgram)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("snapshot mode = %o", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(snapshots[0].HostDir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("ignored directory entered snapshot: %v", err)
	}
	if err := os.WriteFile(program, []byte("print('changed')\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	content, err = os.ReadFile(snapshotProgram)
	if err != nil || string(content) != "print('original')\n" {
		t.Fatalf("snapshot changed with live checkout: %q, %v", content, err)
	}
	inputInfo, err := os.Stat(prepared.InputHostDir)
	if err != nil {
		t.Fatal(err)
	}
	if inputInfo.Mode().Perm()&0o222 != 0 {
		t.Fatalf("resolver input remained writable: %o", inputInfo.Mode().Perm())
	}
}

func TestStagePythonLocalSourceSnapshotsExplainsInvalidManifestOrder(t *testing.T) {
	sourceDir := t.TempDir()
	manifest := PythonSourceManifestV1{
		Schema: pythonSourceManifestSchemaV1,
		Entries: []PythonSourceManifestEntryV1{
			{Path: "zeta", Kind: "directory", Mode: "0755"},
			{Path: "alpha", Kind: "directory", Mode: "0755"},
		},
	}
	source := PythonLocalSource{
		Distribution: "demo",
		HostDir:      sourceDir,
		Manifest:     manifest,
	}
	err := validatePythonLocalSourcesForSnapshot([]PythonLocalSource{source})
	if err == nil {
		t.Fatal("invalid manifest order was accepted")
	}
	for _, want := range []string{
		`prepare immutable snapshot for local Python source "demo" from "`,
		`entry 1 path "alpha" is not strictly after entry 0 path "zeta"`,
		"stable content digest",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error does not contain %q: %v", want, err)
		}
	}
}

func TestStagePythonLocalSourceSnapshotsSupportsSuccessiveWaves(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PreparePythonResolverArtifacts(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	source := func(distribution string) PythonLocalSource {
		dir := filepath.Join(t.TempDir(), distribution)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(distribution), 0o644); err != nil {
			t.Fatal(err)
		}
		manifest, digest, err := ObservePythonSourceManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		return PythonLocalSource{
			Distribution: distribution, HostDir: dir,
			Manifest: manifest, SourceInputDigest: digest,
		}
	}

	first, err := StagePythonLocalSourceSnapshots(prepared, []PythonLocalSource{source("direct")})
	if err != nil {
		t.Fatal(err)
	}
	second, err := StagePythonLocalSourceSnapshots(prepared, []PythonLocalSource{source("transitive")})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("snapshots = %#v / %#v", first, second)
	}
	for _, snapshot := range append(first, second...) {
		if _, err := os.Stat(filepath.Join(snapshot.HostDir, "pyproject.toml")); err != nil {
			t.Fatalf("snapshot %q was not retained: %v", snapshot.Distribution, err)
		}
	}
}

func TestStagePythonLocalSourceSnapshotsFailedLaterWavePreservesEarlierSnapshots(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PreparePythonResolverArtifacts(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	sourceDir := t.TempDir()
	sourceFile := filepath.Join(sourceDir, "pyproject.toml")
	if err := os.WriteFile(sourceFile, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, digest, err := ObservePythonSourceManifest(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	firstSource := PythonLocalSource{
		Distribution: "direct", HostDir: sourceDir,
		Manifest: manifest, SourceInputDigest: digest,
	}
	first, err := StagePythonLocalSourceSnapshots(prepared, []PythonLocalSource{firstSource})
	if err != nil {
		t.Fatal(err)
	}

	secondSource := firstSource
	secondSource.Distribution = "transitive"
	if err := os.WriteFile(sourceFile, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := StagePythonLocalSourceSnapshots(prepared, []PythonLocalSource{secondSource}); err == nil {
		t.Fatal("changed later-wave source was accepted")
	}
	if _, err := os.Stat(filepath.Join(first[0].HostDir, "pyproject.toml")); err != nil {
		t.Fatalf("failed later wave removed the earlier snapshot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(
		prepared.InputHostDir, pythonSourceSnapshotDirectory, secondSource.Distribution,
	)); !os.IsNotExist(err) {
		t.Fatalf("failed later wave left a partial snapshot: %v", err)
	}
}

func TestStagePythonLocalSourceSnapshotsRejectsChangedSourceAndCleansPartialSnapshot(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PreparePythonResolverArtifacts(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	workspace := t.TempDir()
	sourceDir := filepath.Join(workspace, "demo")
	if err := os.Mkdir(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(sourceDir, "pyproject.toml")
	if err := os.WriteFile(filename, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, digest, err := ObservePythonSourceManifest(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	sources := []PythonLocalSource{{
		Distribution: "demo", HostDir: sourceDir,
		Manifest: manifest, SourceInputDigest: digest,
	}}
	if err := os.WriteFile(filename, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := StagePythonLocalSourceSnapshots(prepared, sources); err == nil || !strings.Contains(err.Error(), "content changed") {
		t.Fatalf("changed-source error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(prepared.InputHostDir, pythonSourceSnapshotDirectory)); !os.IsNotExist(err) {
		t.Fatalf("failed staging left a snapshot directory: %v", err)
	}
	inputInfo, err := os.Stat(prepared.InputHostDir)
	if err != nil {
		t.Fatal(err)
	}
	if inputInfo.Mode().Perm()&0o222 != 0 {
		t.Fatalf("failed staging left resolver input writable: %o", inputInfo.Mode().Perm())
	}
}

func TestStagePythonLocalSourceSnapshotsRejectsInvalidDistributionBeforeCreatingSnapshotRoot(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PreparePythonResolverArtifacts(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	_, err = StagePythonLocalSourceSnapshots(prepared, []PythonLocalSource{{Distribution: "demo/pkg"}})
	if err == nil || !strings.Contains(err.Error(), "valid Python distribution name") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(prepared.InputHostDir, pythonSourceSnapshotDirectory)); !os.IsNotExist(err) {
		t.Fatalf("invalid distribution created snapshot root: %v", err)
	}
}
