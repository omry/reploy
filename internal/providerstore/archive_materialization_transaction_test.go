package providerstore

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveMaterializationTransactionWritesInventoryAndModes(t *testing.T) {
	materializer, _ := newArchiveTransactionTestMaterializer(t, 2, 3)
	materializer.executablePaths["bin/tool"] = ""
	if err := materializer.accept("bin/tool", ArchiveEntryKindRegular, 3, bytes.NewReader([]byte("bin"))); err != nil {
		t.Fatal(err)
	}
	if err := materializer.accept("docs", ArchiveEntryKindDirectory, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := materializer.validateExecutablePaths(); err != nil {
		t.Fatal(err)
	}
	if err := materializer.validateExpectedInventory(); err != nil {
		t.Fatal(err)
	}
	if len(materializer.entries) != 2 || materializer.entries[0].DestinationPath != "bin/tool" {
		t.Fatalf("inventory = %#v", materializer.entries)
	}
	if content, err := os.ReadFile(filepath.Join(materializer.stage, "bin", "tool")); err != nil || string(content) != "bin" {
		t.Fatalf("materialized content = %q, %v", content, err)
	}
	assertArchiveMaterializedRegularMode(t, filepath.Join(materializer.stage, "bin", "tool"), true)
	if err := normalizeMaterializedTree(materializer.stage); err != nil {
		t.Fatal(err)
	}
	assertArchiveMaterializedDirectoryMode(t, filepath.Join(materializer.stage, "docs"))
}

func TestArchiveMaterializationTransactionMapsDestinationRoot(t *testing.T) {
	materializer, _ := newArchiveTransactionTestMaterializer(t, 1, 1)
	materializer.request.InstallDirectory = "payload"
	materializer.request.ArchiveRoot = "payload"
	materializer.request.archiveRoot = "payload"
	for _, test := range []struct {
		archivePath string
		want        string
	}{
		{archivePath: "payload", want: "."},
		{archivePath: "payload/bin/tool", want: "bin/tool"},
	} {
		got, err := materializer.destinationPath(test.archivePath)
		if err != nil || got != test.want {
			t.Errorf("destinationPath(%q) = %q, %v; want %q", test.archivePath, got, err, test.want)
		}
	}
	materializer.request.ArchiveRoot = "."
	materializer.request.archiveRoot = "."
	got, err := materializer.destinationPath("bin/tool")
	if err != nil || got != "bin/tool" {
		t.Fatalf("preserved destination = %q, %v", got, err)
	}
}

func TestArchiveMaterializationTransactionRejectsCollisionsAndAliases(t *testing.T) {
	materializer, _ := newArchiveTransactionTestMaterializer(t, 4, 2)
	if err := materializer.accept("file", ArchiveEntryKindRegular, 1, bytes.NewReader([]byte("x"))); err != nil {
		t.Fatal(err)
	}
	if err := materializer.accept("file", ArchiveEntryKindRegular, 1, bytes.NewReader([]byte("x"))); err == nil || !strings.Contains(err.Error(), "duplicate normalized") {
		t.Fatalf("duplicate error = %v", err)
	}
	if err := materializer.accept("file/child", ArchiveEntryKindRegular, 1, bytes.NewReader([]byte("x"))); err == nil || !strings.Contains(err.Error(), "regular-file parent") {
		t.Fatalf("parent collision error = %v", err)
	}

	alias, _ := newArchiveTransactionTestMaterializer(t, 2, 2)
	if err := alias.accept("Bin/a", ArchiveEntryKindRegular, 1, bytes.NewReader([]byte("x"))); err != nil {
		t.Fatal(err)
	}
	if err := alias.accept("bin/b", ArchiveEntryKindRegular, 1, bytes.NewReader([]byte("y"))); err == nil || !strings.Contains(err.Error(), "aliases") {
		t.Fatalf("portable alias error = %v", err)
	}
}

func TestArchiveMaterializationTransactionRejectsMissingExecutableAndMismatchedInventory(t *testing.T) {
	materializer, _ := newArchiveTransactionTestMaterializer(t, 1, 1)
	materializer.executablePaths["missing"] = ""
	if err := materializer.validateExecutablePaths(); err == nil || !strings.Contains(err.Error(), "missing or not a regular file") {
		t.Fatalf("missing executable error = %v", err)
	}
	if err := materializer.validateExpectedInventory(); err == nil {
		t.Fatal("empty inventory unexpectedly matched expected values")
	}
}

func TestArchiveMaterializationTransactionCleansWorkspace(t *testing.T) {
	materializer, _ := newArchiveTransactionTestMaterializer(t, 1, 1)
	marker := filepath.Join(materializer.stage, "marker")
	if err := os.WriteFile(marker, []byte("staged"), 0o600); err != nil {
		t.Fatal(err)
	}
	cleanupArchiveMaterializationWorkspace(materializer.stage)
	if _, err := os.Lstat(materializer.stage); !os.IsNotExist(err) {
		t.Fatalf("workspace still exists after cleanup: %v", err)
	}
}

func TestPublishArchiveMaterializedDirectoryDoesNotReplaceExistingDestination(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(stage, "marker")
	if err := os.WriteFile(marker, []byte("staged"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := publishArchiveMaterializedDirectory(stage, destination); err == nil {
		t.Fatal("publication unexpectedly replaced an existing destination")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("stage was removed after no-replace failure: %v", err)
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("existing destination changed: %v", entries)
	}
}

func TestPublishArchiveMaterializedDirectoryPublishesStage(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(stage, "marker")
	if err := os.WriteFile(marker, []byte("published"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "destination")
	if err := publishArchiveMaterializedDirectory(stage, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stage); !os.IsNotExist(err) {
		t.Fatalf("stage still exists after publication: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(destination, "marker"))
	if err != nil || string(content) != "published" {
		t.Fatalf("published marker = %q, %v", content, err)
	}
}

func newArchiveTransactionTestMaterializer(t *testing.T, entryLimit uint64, sizeLimit uint64) (*archiveMaterializer, string) {
	t.Helper()
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupArchiveMaterializationWorkspace(stage) })
	request := ArchiveMaterializationRequest{InstallDirectory: "install", ArchiveRoot: "."}
	return &archiveMaterializer{
		ctx:              context.Background(),
		stage:            stage,
		request:          validatedArchiveMaterializationRequest{ArchiveMaterializationRequest: request, archiveRoot: ".", entryLimit: entryLimit, sizeLimit: sizeLimit},
		archivePaths:     map[string]struct{}{},
		nodes:            map[string]archiveMaterializedNode{".": {kind: ArchiveEntryKindDirectory}},
		destinationPaths: map[string]string{portableArchiveDestinationKey("."): "."},
		executablePaths:  map[string]string{},
	}, root
}

func fileMode(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
