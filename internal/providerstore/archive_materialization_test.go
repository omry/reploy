package providerstore

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeArchiveTarGzStripsInstallRootAndNormalizesModes(t *testing.T) {
	store, destination := newArchiveTestStore(t)
	archive := makeTarGz(t, []archiveTestEntry{
		{header: tar.Header{Name: "java", Typeflag: tar.TypeDir, Mode: 0o777, Uid: 123, Gid: 456}},
		{header: tar.Header{Name: "java/bin/java", Typeflag: tar.TypeReg, Mode: 0o777, Uid: 123, Gid: 456}, content: "java"},
		{header: tar.Header{Name: "java/lib.jar", Typeflag: tar.TypeReg, Mode: 0o7777, Uid: 123, Gid: 456}, content: "jar"},
	})
	descriptor := publishArchiveTestBytes(t, store, archive)
	result, err := store.MaterializeArchive(context.Background(), ArchiveMaterializationRequest{
		Artifact: descriptor, Format: ArchiveFormatTarGz, DestinationRoot: destination,
		InstallDirectory: "java", ArchiveRoot: "java", ExpectedEntryCount: "3", ExpectedUnpackedSize: "7",
		ExecutablePaths: []string{"java/bin/java"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalPath != filepath.Join(destination, "java") || result.ObservedEntryCount != "3" || result.ObservedUnpackedSize != "7" {
		t.Fatalf("result = %#v", result)
	}
	path := filepath.Join(destination, "java", "bin", "java")
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "java" {
		t.Fatalf("materialized content = %q, err = %v", content, err)
	}
	assertArchiveMaterializedRegularMode(t, path, true)
	assertArchiveMaterializedDirectoryMode(t, filepath.Join(destination, "java", "bin"))
	assertArchiveMaterializedRegularMode(t, filepath.Join(destination, "java", "lib.jar"), false)
	if len(result.ObservedEntries) != 3 || result.ObservedEntries[1].DestinationPath != "bin/java" {
		t.Fatalf("inventory = %#v", result.ObservedEntries)
	}
}

func TestMaterializeArchiveZipPreservesArchiveRoot(t *testing.T) {
	store, destination := newArchiveTestStore(t)
	archive := makeZip(t, []zipTestEntry{
		{name: "playwright/", mode: os.ModeDir | 0o777},
		{name: "playwright/node", content: "node", mode: 0o777},
	})
	descriptor := publishArchiveTestBytes(t, store, archive)
	result, err := MaterializeArchive(context.Background(), store, ArchiveMaterializationRequest{
		Artifact: descriptor, Format: ArchiveFormatZip, DestinationRoot: destination,
		InstallDirectory: "payload", ArchiveRoot: "playwright", ExpectedEntryCount: "2", ExpectedUnpackedSize: "4",
		ExecutablePaths: []string{"playwright/node"},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(destination, "payload", "playwright", "node")
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "node" {
		t.Fatalf("materialized content = %q, err = %v", content, err)
	}
	assertArchiveMaterializedRegularMode(t, path, true)
	if result.ObservedEntries[1].DestinationPath != "playwright/node" {
		t.Fatalf("inventory = %#v", result.ObservedEntries)
	}
}

func TestMaterializeArchiveRejectsUnsafeArchiveWithoutPublication(t *testing.T) {
	store, destination := newArchiveTestStore(t)
	descriptor := publishArchiveTestBytes(t, store, makeTarGz(t, []archiveTestEntry{
		{header: tar.Header{Name: "../escape", Typeflag: tar.TypeReg}, content: "x"},
	}))
	_, err := store.MaterializeArchive(context.Background(), ArchiveMaterializationRequest{
		Artifact: descriptor, Format: ArchiveFormatTarGz, DestinationRoot: destination,
		InstallDirectory: "install", ArchiveRoot: ".", ExpectedEntryCount: "1", ExpectedUnpackedSize: "1",
	})
	if err == nil {
		t.Fatal("unsafe archive unexpectedly materialized")
	}
	assertArchiveDestinationAbsent(t, destination, "install")
	assertArchiveTempClean(t, destination)
}

func TestMaterializeArchiveRejectsInventoryMismatchWithoutPublication(t *testing.T) {
	store, destination := newArchiveTestStore(t)
	descriptor := publishArchiveTestBytes(t, store, makeTarGz(t, []archiveTestEntry{
		{header: tar.Header{Name: "one", Typeflag: tar.TypeReg}, content: "x"},
	}))
	_, err := store.MaterializeArchive(context.Background(), ArchiveMaterializationRequest{
		Artifact: descriptor, Format: ArchiveFormatTarGz, DestinationRoot: destination,
		InstallDirectory: "install", ArchiveRoot: ".", ExpectedEntryCount: "2", ExpectedUnpackedSize: "1",
	})
	if err == nil || !strings.Contains(err.Error(), "does not match expected count") {
		t.Fatalf("inventory mismatch error = %v", err)
	}
	assertArchiveDestinationAbsent(t, destination, "install")
	assertArchiveTempClean(t, destination)
}

func TestMaterializeArchivePreservesDestinationAndCleansAfterCancellationOrCorruption(t *testing.T) {
	store, destination := newArchiveTestStore(t)
	final := filepath.Join(destination, "install")
	if err := os.Mkdir(final, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(final, "keep")
	if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	descriptor := publishArchiveTestBytes(t, store, makeTarGz(t, []archiveTestEntry{{header: tar.Header{Name: "new", Typeflag: tar.TypeReg}, content: "new"}}))
	_, err := store.MaterializeArchive(context.Background(), ArchiveMaterializationRequest{
		Artifact: descriptor, Format: ArchiveFormatTarGz, DestinationRoot: destination,
		InstallDirectory: "install", ArchiveRoot: ".", ExpectedEntryCount: "1", ExpectedUnpackedSize: "3",
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("destination collision error = %v", err)
	}
	content, readErr := os.ReadFile(keep)
	if readErr != nil || string(content) != "keep" {
		t.Fatalf("existing destination changed: %q, %v", content, readErr)
	}

	if err := os.RemoveAll(final); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = store.MaterializeArchive(canceled, ArchiveMaterializationRequest{
		Artifact: descriptor, Format: ArchiveFormatTarGz, DestinationRoot: destination,
		InstallDirectory: "install", ArchiveRoot: ".", ExpectedEntryCount: "1", ExpectedUnpackedSize: "3",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
	assertArchiveDestinationAbsent(t, destination, "install")
	assertArchiveTempClean(t, destination)

	blobPath, err := store.BlobPath(descriptor.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blobPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blobPath, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = store.MaterializeArchive(context.Background(), ArchiveMaterializationRequest{
		Artifact: descriptor, Format: ArchiveFormatTarGz, DestinationRoot: destination,
		InstallDirectory: "install", ArchiveRoot: ".", ExpectedEntryCount: "1", ExpectedUnpackedSize: "3",
	})
	if err == nil || !strings.Contains(err.Error(), "open verified archive") {
		t.Fatalf("corruption error = %v", err)
	}
	assertArchiveDestinationAbsent(t, destination, "install")
}

func TestMaterializeArchiveLeavesCommandLookingDataInert(t *testing.T) {
	store, destination := newArchiveTestStore(t)
	archive := makeZip(t, []zipTestEntry{{name: ";touch pwned", content: "#!/bin/sh\ntouch pwned", mode: 0o777}})
	descriptor := publishArchiveTestBytes(t, store, archive)
	_, err := store.MaterializeArchive(context.Background(), ArchiveMaterializationRequest{
		Artifact: descriptor, Format: ArchiveFormatZip, DestinationRoot: destination,
		InstallDirectory: "install", ArchiveRoot: ".", ExpectedEntryCount: "1", ExpectedUnpackedSize: "21",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "pwned")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("command-looking data caused side effect: %v", err)
	}
}

func newArchiveTestStore(t *testing.T) (Store, string) {
	t.Helper()
	deployment := t.TempDir()
	store, err := NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.WalkDir(destination, func(path string, entry fs.DirEntry, err error) error {
			if err == nil {
				if entry.IsDir() {
					_ = os.Chmod(path, 0o700)
				} else {
					_ = os.Chmod(path, 0o600)
				}
			}
			return nil
		})
	})
	return store, destination
}

func publishArchiveTestBytes(t *testing.T, store Store, content []byte) ArtifactDescriptor {
	t.Helper()
	descriptor, err := store.Publish(context.Background(), "archives/payload", "archive", bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	return descriptor
}

func assertArchiveDestinationAbsent(t *testing.T, root string, install string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(root, install)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("destination exists, err = %v", err)
	}
}

func assertArchiveTempClean(t *testing.T, root string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, ".*.reploy-materialize-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("materialization temp entries remain: %v", matches)
	}
}
