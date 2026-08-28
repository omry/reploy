package providerstore

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type archiveTestEntry struct {
	header  tar.Header
	content string
}

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
	if mode := fileMode(t, path); mode != 0o555 {
		t.Fatalf("executable mode = %o, want 555", mode)
	}
	if mode := fileMode(t, filepath.Join(destination, "java", "bin")); mode != 0o555 {
		t.Fatalf("directory mode = %o, want 555", mode)
	}
	if mode := fileMode(t, filepath.Join(destination, "java", "lib.jar")); mode != 0o444 {
		t.Fatalf("ordinary file mode = %o, want 444", mode)
	}
	if len(result.ObservedEntries) != 3 || result.ObservedEntries[1].DestinationPath != "bin/java" {
		t.Fatalf("inventory = %#v", result.ObservedEntries)
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
	if mode := fileMode(t, path); mode != 0o555 {
		t.Fatalf("executable mode = %o, want 555", mode)
	}
	if result.ObservedEntries[1].DestinationPath != "playwright/node" {
		t.Fatalf("inventory = %#v", result.ObservedEntries)
	}
}

func TestMaterializeArchiveRejectsUnsafePathsAndCollisionsWithoutDestination(t *testing.T) {
	tests := []struct {
		name    string
		entries []archiveTestEntry
	}{
		{name: "traversal", entries: []archiveTestEntry{{header: tar.Header{Name: "../escape", Typeflag: tar.TypeReg}, content: "x"}}},
		{name: "absolute", entries: []archiveTestEntry{{header: tar.Header{Name: "/escape", Typeflag: tar.TypeReg}, content: "x"}}},
		{name: "backslash", entries: []archiveTestEntry{{header: tar.Header{Name: `dir\escape`, Typeflag: tar.TypeReg}, content: "x"}}},
		{name: "duplicate-normalized", entries: []archiveTestEntry{
			{header: tar.Header{Name: "bin/tool", Typeflag: tar.TypeReg}, content: "x"},
			{header: tar.Header{Name: "./bin/tool", Typeflag: tar.TypeReg}, content: "x"},
		}},
		{name: "parent-collision", entries: []archiveTestEntry{
			{header: tar.Header{Name: "bin", Typeflag: tar.TypeReg}, content: "x"},
			{header: tar.Header{Name: "bin/tool", Typeflag: tar.TypeReg}, content: "x"},
		}},
		{name: "reverse-parent-collision", entries: []archiveTestEntry{
			{header: tar.Header{Name: "bin/tool", Typeflag: tar.TypeReg}, content: "x"},
			{header: tar.Header{Name: "bin", Typeflag: tar.TypeReg}, content: "x"},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, destination := newArchiveTestStore(t)
			descriptor := publishArchiveTestBytes(t, store, makeTarGz(t, test.entries))
			_, err := store.MaterializeArchive(context.Background(), ArchiveMaterializationRequest{
				Artifact: descriptor, Format: ArchiveFormatTarGz, DestinationRoot: destination,
				InstallDirectory: "install", ArchiveRoot: ".", ExpectedEntryCount: "2", ExpectedUnpackedSize: "1",
			})
			if err == nil {
				t.Fatal("unsafe archive unexpectedly materialized")
			}
			assertArchiveDestinationAbsent(t, destination, "install")
			assertArchiveTempClean(t, destination)
		})
	}
}

func TestMaterializeArchiveRejectsSpecialEntriesAndMetadata(t *testing.T) {
	tests := []struct {
		name   string
		header tar.Header
	}{
		{name: "symlink", header: tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "target"}},
		{name: "hardlink", header: tar.Header{Name: "link", Typeflag: tar.TypeLink, Linkname: "target"}},
		{name: "fifo", header: tar.Header{Name: "pipe", Typeflag: tar.TypeFifo}},
		{name: "device", header: tar.Header{Name: "device", Typeflag: tar.TypeChar, Devmajor: 1, Devminor: 3}},
		{name: "xattr", header: tar.Header{Name: "xattr", Typeflag: tar.TypeReg, PAXRecords: map[string]string{"SCHILY.xattr.user.test": "x"}}},
		{name: "capability", header: tar.Header{Name: "cap", Typeflag: tar.TypeReg, PAXRecords: map[string]string{"SCHILY.capability": "x"}}},
		{name: "acl", header: tar.Header{Name: "acl", Typeflag: tar.TypeReg, PAXRecords: map[string]string{"SCHILY.acl.access": "user::r--"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, destination := newArchiveTestStore(t)
			archive := makeTarGz(t, []archiveTestEntry{{header: test.header, content: "x"}})
			descriptor := publishArchiveTestBytes(t, store, archive)
			_, err := store.MaterializeArchive(context.Background(), ArchiveMaterializationRequest{
				Artifact: descriptor, Format: ArchiveFormatTarGz, DestinationRoot: destination,
				InstallDirectory: "install", ArchiveRoot: ".", ExpectedEntryCount: "1", ExpectedUnpackedSize: "1",
			})
			if err == nil {
				t.Fatal("unsafe entry unexpectedly materialized")
			}
			assertArchiveDestinationAbsent(t, destination, "install")
		})
	}

	store, destination := newArchiveTestStore(t)
	archive := makeZip(t, []zipTestEntry{{name: "link", mode: os.ModeSymlink | 0o777, content: "target"}})
	descriptor := publishArchiveTestBytes(t, store, archive)
	_, err := store.MaterializeArchive(context.Background(), ArchiveMaterializationRequest{
		Artifact: descriptor, Format: ArchiveFormatZip, DestinationRoot: destination,
		InstallDirectory: "install", ArchiveRoot: ".", ExpectedEntryCount: "1", ExpectedUnpackedSize: "6",
	})
	if err == nil || !strings.Contains(err.Error(), "special type") {
		t.Fatalf("zip symlink error = %v", err)
	}
}

func TestMaterializeArchiveRejectsObservedInventoryMismatches(t *testing.T) {
	tests := []struct {
		name       string
		entries    []archiveTestEntry
		entryCount string
		size       string
	}{
		{name: "observed-count-below-expected", entries: []archiveTestEntry{{header: tar.Header{Name: "one", Typeflag: tar.TypeReg}, content: "x"}}, entryCount: "2", size: "1"},
		{name: "observed-count-above-expected", entries: []archiveTestEntry{{header: tar.Header{Name: "one", Typeflag: tar.TypeReg}, content: "x"}, {header: tar.Header{Name: "two", Typeflag: tar.TypeReg}, content: "x"}}, entryCount: "1", size: "2"},
		{name: "observed-size-below-expected", entries: []archiveTestEntry{{header: tar.Header{Name: "one", Typeflag: tar.TypeReg}, content: "x"}}, entryCount: "1", size: "2"},
		{name: "observed-size-above-expected", entries: []archiveTestEntry{{header: tar.Header{Name: "one", Typeflag: tar.TypeReg}, content: "xx"}}, entryCount: "1", size: "1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, destination := newArchiveTestStore(t)
			descriptor := publishArchiveTestBytes(t, store, makeTarGz(t, test.entries))
			_, err := store.MaterializeArchive(context.Background(), ArchiveMaterializationRequest{
				Artifact: descriptor, Format: ArchiveFormatTarGz, DestinationRoot: destination,
				InstallDirectory: "install", ArchiveRoot: ".", ExpectedEntryCount: test.entryCount, ExpectedUnpackedSize: test.size,
			})
			if err == nil {
				t.Fatal("inventory mismatch unexpectedly materialized")
			}
			assertArchiveDestinationAbsent(t, destination, "install")
			assertArchiveTempClean(t, destination)
		})
	}
}

func TestMaterializeArchiveRejectsInvalidPublicRequests(t *testing.T) {
	tests := []struct {
		name   string
		ctx    context.Context
		mutate func(*testing.T, *ArchiveMaterializationRequest)
	}{
		{name: "nil-context", ctx: nil},
		{name: "unsupported-format", mutate: func(_ *testing.T, request *ArchiveMaterializationRequest) {
			request.Format = ArchiveFormat("rar")
		}},
		{name: "relative-destination-root", mutate: func(_ *testing.T, request *ArchiveMaterializationRequest) {
			request.DestinationRoot = "relative"
		}},
		{name: "non-directory-destination-root", mutate: func(t *testing.T, request *ArchiveMaterializationRequest) {
			file := filepath.Join(request.DestinationRoot, "root-file")
			if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
			request.DestinationRoot = file
		}},
		{name: "multi-component-install-directory", mutate: func(_ *testing.T, request *ArchiveMaterializationRequest) {
			request.InstallDirectory = "install/child"
		}},
		{name: "non-normalized-archive-root", mutate: func(_ *testing.T, request *ArchiveMaterializationRequest) {
			request.ArchiveRoot = "./payload"
		}},
		{name: "traversal-archive-root", mutate: func(_ *testing.T, request *ArchiveMaterializationRequest) {
			request.ArchiveRoot = "../payload"
		}},
		{name: "zero-expected-count", mutate: func(_ *testing.T, request *ArchiveMaterializationRequest) {
			request.ExpectedEntryCount = "0"
		}},
		{name: "leading-zero-expected-count", mutate: func(_ *testing.T, request *ArchiveMaterializationRequest) {
			request.ExpectedEntryCount = "01"
		}},
		{name: "zero-expected-size", mutate: func(_ *testing.T, request *ArchiveMaterializationRequest) {
			request.ExpectedUnpackedSize = "0"
		}},
		{name: "leading-zero-expected-size", mutate: func(_ *testing.T, request *ArchiveMaterializationRequest) {
			request.ExpectedUnpackedSize = "01"
		}},
		{name: "unsorted-executable-paths", mutate: func(_ *testing.T, request *ArchiveMaterializationRequest) {
			request.ExecutablePaths = []string{"b", "a"}
		}},
		{name: "duplicate-executable-paths", mutate: func(_ *testing.T, request *ArchiveMaterializationRequest) {
			request.ExecutablePaths = []string{"a", "a"}
		}},
		{name: "out-of-root-executable-path", mutate: func(_ *testing.T, request *ArchiveMaterializationRequest) {
			request.ArchiveRoot = "payload"
			request.ExecutablePaths = []string{"other/file"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, destination := newArchiveTestStore(t)
			request := newArchiveMaterializationTestRequest(t, store, destination, makeTarGz(t, []archiveTestEntry{{header: tar.Header{Name: "one", Typeflag: tar.TypeReg}, content: "x"}}))
			ctx := test.ctx
			if ctx == nil && test.name != "nil-context" {
				ctx = context.Background()
			}
			if test.mutate != nil {
				test.mutate(t, &request)
			}
			_, err := store.MaterializeArchive(ctx, request)
			if err == nil {
				t.Fatal("invalid public request unexpectedly materialized")
			}
			assertArchiveDestinationAbsent(t, destination, "install")
			assertArchiveTempClean(t, destination)
		})
	}
}

func TestMaterializeArchiveRejectsWindowsUnsafeComponents(t *testing.T) {
	for _, member := range []string{
		"file:stream", "file<name", "file>name", "file\"name", "file|name", "file?name", "file*name", "file\x01name",
		"CON", "PRN", "AUX", "NUL", "CON.txt", "CON .txt", "conin$", "CONOUT$.log", "COM1", "COM1 .log", "COM9.txt", "LPT1.log", "LPT9.txt", "name.", "name ",
	} {
		t.Run("archive-member-"+strings.ReplaceAll(member, ":", "-"), func(t *testing.T) {
			store, destination := newArchiveTestStore(t)
			request := newArchiveMaterializationTestRequest(t, store, destination, makeTarGz(t, []archiveTestEntry{{header: tar.Header{Name: member, Typeflag: tar.TypeReg}, content: "x"}}))
			_, err := store.MaterializeArchive(context.Background(), request)
			if err == nil || !strings.Contains(err.Error(), "unsafe Windows") {
				t.Fatalf("Windows-unsafe archive member error = %v", err)
			}
			assertArchiveDestinationAbsent(t, destination, "install")
			assertArchiveTempClean(t, destination)
		})
	}
	for _, member := range []string{"COM¹", "LPT².txt", "COM³.log"} {
		t.Run("normalized-member-"+member, func(t *testing.T) {
			_, err := normalizeArchivePath(member, false)
			if err == nil || !strings.Contains(err.Error(), "unsafe Windows") {
				t.Fatalf("Windows-unsafe normalized archive member error = %v", err)
			}
		})
	}
	for _, member := range []string{"COM¹", "LPT².txt", "COM³.log", "COM¹ .txt", "LPT² .log", "file<name", "file>name", "file\"name", "file|name", "file?name", "file*name", "file\x01name"} {
		t.Run("zip-member-"+member, func(t *testing.T) {
			store, destination := newArchiveTestStore(t)
			archive := makeZip(t, []zipTestEntry{{name: member, content: "x"}})
			request := newArchiveMaterializationTestRequest(t, store, destination, archive)
			request.Format = ArchiveFormatZip
			_, err := store.MaterializeArchive(context.Background(), request)
			if err == nil || !strings.Contains(err.Error(), "unsafe Windows") {
				t.Fatalf("Windows-unsafe ZIP member error = %v", err)
			}
			assertArchiveDestinationAbsent(t, destination, "install")
			assertArchiveTempClean(t, destination)
		})
	}

	publicRequests := []struct {
		name    string
		archive []archiveTestEntry
		mutate  func(*ArchiveMaterializationRequest)
	}{
		{
			name:    "install-directory",
			archive: []archiveTestEntry{{header: tar.Header{Name: "one", Typeflag: tar.TypeReg}, content: "x"}},
			mutate:  func(request *ArchiveMaterializationRequest) { request.InstallDirectory = "install:name" },
		},
		{
			name:    "install-directory-forbidden-character",
			archive: []archiveTestEntry{{header: tar.Header{Name: "one", Typeflag: tar.TypeReg}, content: "x"}},
			mutate:  func(request *ArchiveMaterializationRequest) { request.InstallDirectory = "install<name" },
		},
		{
			name:    "archive-root",
			archive: []archiveTestEntry{{header: tar.Header{Name: "CON/file", Typeflag: tar.TypeReg}, content: "x"}},
			mutate:  func(request *ArchiveMaterializationRequest) { request.ArchiveRoot = "CON" },
		},
		{
			name:    "archive-root-forbidden-character",
			archive: []archiveTestEntry{{header: tar.Header{Name: "root?/file", Typeflag: tar.TypeReg}, content: "x"}},
			mutate:  func(request *ArchiveMaterializationRequest) { request.ArchiveRoot = "root?" },
		},
		{
			name:    "executable-path",
			archive: []archiveTestEntry{{header: tar.Header{Name: "CON", Typeflag: tar.TypeReg}, content: "x"}},
			mutate: func(request *ArchiveMaterializationRequest) {
				request.ExecutablePaths = []string{"CON"}
			},
		},
		{
			name:    "executable-path-forbidden-character",
			archive: []archiveTestEntry{{header: tar.Header{Name: "file*", Typeflag: tar.TypeReg}, content: "x"}},
			mutate: func(request *ArchiveMaterializationRequest) {
				request.ExecutablePaths = []string{"file*"}
			},
		},
	}
	for _, test := range publicRequests {
		t.Run("public-"+test.name, func(t *testing.T) {
			store, destination := newArchiveTestStore(t)
			request := newArchiveMaterializationTestRequest(t, store, destination, makeTarGz(t, test.archive))
			if test.mutate != nil {
				test.mutate(&request)
			}
			_, err := store.MaterializeArchive(context.Background(), request)
			if err == nil || !strings.Contains(err.Error(), "unsafe Windows") {
				t.Fatalf("Windows-unsafe public field error = %v", err)
			}
			assertArchiveDestinationAbsent(t, destination, "install")
			assertArchiveTempClean(t, destination)
		})
	}
}

func TestMaterializeArchiveRejectsZipCentralDirectoryOverCoreLimit(t *testing.T) {
	store, destination := newArchiveTestStore(t)
	entries := make([]zipTestEntry, CoreMaxArchiveEntries+1)
	for index := range entries {
		entries[index].name = fmt.Sprintf("entry-%05d", index)
	}
	archive := makeZip(t, entries)
	forgeZipEOCDRecordCount(t, archive, 1)
	request := newArchiveMaterializationTestRequest(t, store, destination, archive)
	request.Format = ArchiveFormatZip
	request.ExpectedEntryCount = "10000"
	_, err := store.MaterializeArchive(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "central directory") {
		t.Fatalf("oversized ZIP central directory error = %v", err)
	}
	assertArchiveDestinationAbsent(t, destination, "install")
	assertArchiveTempClean(t, destination)
}

func TestMaterializeArchiveAcceptsZip64CentralDirectory(t *testing.T) {
	store, destination := newArchiveTestStore(t)
	request := newArchiveMaterializationTestRequest(t, store, destination, makeZip64(t, []zipTestEntry{{name: "file", content: "x"}}))
	request.Format = ArchiveFormatZip
	if _, err := store.MaterializeArchive(context.Background(), request); err != nil {
		t.Fatal(err)
	}
}

func TestMaterializeArchiveRejectsPortableCaseFoldedDestinationAliases(t *testing.T) {
	tarCases := []struct {
		name        string
		entries     []archiveTestEntry
		installRoot string
		archiveRoot string
		expected    string
	}{
		{name: "files-and-implicit-parent", entries: []archiveTestEntry{
			{header: tar.Header{Name: "Bin/a", Typeflag: tar.TypeReg}, content: "x"},
			{header: tar.Header{Name: "bin/b", Typeflag: tar.TypeReg}, content: "y"},
		}, expected: "2"},
		{name: "explicit-directories", entries: []archiveTestEntry{
			{header: tar.Header{Name: "Bin", Typeflag: tar.TypeDir}},
			{header: tar.Header{Name: "bin", Typeflag: tar.TypeDir}},
			{header: tar.Header{Name: "tail", Typeflag: tar.TypeReg}, content: "x"},
		}, expected: "3"},
		{name: "archive-root-mapping", entries: []archiveTestEntry{
			{header: tar.Header{Name: "payload/Bin/a", Typeflag: tar.TypeReg}, content: "x"},
			{header: tar.Header{Name: "payload/bin/b", Typeflag: tar.TypeReg}, content: "y"},
		}, installRoot: "payload", archiveRoot: "payload", expected: "2"},
	}
	for _, test := range tarCases {
		t.Run("tar-"+test.name, func(t *testing.T) {
			store, destination := newArchiveTestStore(t)
			request := newArchiveMaterializationTestRequest(t, store, destination, makeTarGz(t, test.entries))
			if test.installRoot != "" {
				request.InstallDirectory = test.installRoot
			}
			if test.archiveRoot != "" {
				request.ArchiveRoot = test.archiveRoot
			}
			request.ExpectedEntryCount = test.expected
			request.ExpectedUnpackedSize = "2"
			if test.name == "explicit-directories" {
				request.ExpectedUnpackedSize = "1"
			}
			_, err := store.MaterializeArchive(context.Background(), request)
			if err == nil || !strings.Contains(err.Error(), "case-insensitive") {
				t.Fatalf("case-folded tar destination error = %v", err)
			}
			assertArchiveDestinationAbsent(t, destination, request.InstallDirectory)
			assertArchiveTempClean(t, destination)
		})
	}

	zipCases := []struct {
		name        string
		entries     []zipTestEntry
		installRoot string
		archiveRoot string
		expected    string
	}{
		{name: "files-and-implicit-parent", entries: []zipTestEntry{
			{name: "Bin/a", content: "x"},
			{name: "bin/b", content: "y"},
		}, expected: "2"},
		{name: "unicode-simple-fold-implicit-parent", entries: []zipTestEntry{
			{name: "Σ/a", content: "x"},
			{name: "ς/b", content: "y"},
		}, expected: "2"},
		{name: "explicit-directories", entries: []zipTestEntry{
			{name: "Bin/"},
			{name: "bin/"},
			{name: "tail", content: "x"},
		}, expected: "3"},
		{name: "archive-root-mapping", entries: []zipTestEntry{
			{name: "payload/Bin/a", content: "x"},
			{name: "payload/bin/b", content: "y"},
		}, installRoot: "payload", archiveRoot: "payload", expected: "2"},
	}
	for _, test := range zipCases {
		t.Run("zip-"+test.name, func(t *testing.T) {
			store, destination := newArchiveTestStore(t)
			request := newArchiveMaterializationTestRequest(t, store, destination, makeZip(t, test.entries))
			request.Format = ArchiveFormatZip
			if test.installRoot != "" {
				request.InstallDirectory = test.installRoot
			}
			if test.archiveRoot != "" {
				request.ArchiveRoot = test.archiveRoot
			}
			request.ExpectedEntryCount = test.expected
			request.ExpectedUnpackedSize = "2"
			if test.name == "explicit-directories" {
				request.ExpectedUnpackedSize = "1"
			}
			_, err := store.MaterializeArchive(context.Background(), request)
			if err == nil || !strings.Contains(err.Error(), "case-insensitive") {
				t.Fatalf("case-folded ZIP destination error = %v", err)
			}
			assertArchiveDestinationAbsent(t, destination, request.InstallDirectory)
			assertArchiveTempClean(t, destination)
		})
	}
}

func TestMaterializeArchiveRejectsMissingExecutableAndOutsideArchiveRoot(t *testing.T) {
	tests := []struct {
		name    string
		entries []archiveTestEntry
		mutate  func(*ArchiveMaterializationRequest)
	}{
		{
			name:    "missing-executable",
			entries: []archiveTestEntry{{header: tar.Header{Name: "present", Typeflag: tar.TypeReg}, content: "x"}},
			mutate:  func(request *ArchiveMaterializationRequest) { request.ExecutablePaths = []string{"missing"} },
		},
		{
			name: "executable-is-directory",
			entries: []archiveTestEntry{
				{header: tar.Header{Name: "dir", Typeflag: tar.TypeDir}},
				{header: tar.Header{Name: "file", Typeflag: tar.TypeReg}, content: "x"},
			},
			mutate: func(request *ArchiveMaterializationRequest) {
				request.ExecutablePaths = []string{"dir"}
				request.ExpectedEntryCount = "2"
			},
		},
		{
			name:    "member-outside-archive-root",
			entries: []archiveTestEntry{{header: tar.Header{Name: "outside/file", Typeflag: tar.TypeReg}, content: "x"}},
			mutate: func(request *ArchiveMaterializationRequest) {
				request.ArchiveRoot = "payload"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, destination := newArchiveTestStore(t)
			request := newArchiveMaterializationTestRequest(t, store, destination, makeTarGz(t, test.entries))
			if test.mutate != nil {
				test.mutate(&request)
			}
			_, err := store.MaterializeArchive(context.Background(), request)
			if err == nil {
				t.Fatal("invalid archive contract unexpectedly materialized")
			}
			assertArchiveDestinationAbsent(t, destination, "install")
			assertArchiveTempClean(t, destination)
		})
	}
}

func TestMaterializeArchiveValidatesGzipTrailerAndBoundedTarPadding(t *testing.T) {
	t.Run("corrupt-trailer", func(t *testing.T) {
		store, destination := newArchiveTestStore(t)
		archive := makeTarGz(t, []archiveTestEntry{{header: tar.Header{Name: "one", Typeflag: tar.TypeReg}, content: "x"}})
		archive[len(archive)-1] ^= 1
		descriptor := publishArchiveTestBytes(t, store, archive)
		_, err := store.MaterializeArchive(context.Background(), ArchiveMaterializationRequest{
			Artifact: descriptor, Format: ArchiveFormatTarGz, DestinationRoot: destination,
			InstallDirectory: "install", ArchiveRoot: ".", ExpectedEntryCount: "1", ExpectedUnpackedSize: "1",
		})
		if err == nil || !strings.Contains(err.Error(), "checksum") {
			t.Fatalf("corrupt gzip trailer error = %v", err)
		}
		assertArchiveDestinationAbsent(t, destination, "install")
		assertArchiveTempClean(t, destination)
	})

	t.Run("bounded-zero-padding", func(t *testing.T) {
		store, destination := newArchiveTestStore(t)
		descriptor := publishArchiveTestBytes(t, store, makeTarGzWithTrailing(t,
			[]archiveTestEntry{{header: tar.Header{Name: "one", Typeflag: tar.TypeReg}, content: "x"}},
			bytes.Repeat([]byte{0}, 512)))
		if _, err := store.MaterializeArchive(context.Background(), ArchiveMaterializationRequest{
			Artifact: descriptor, Format: ArchiveFormatTarGz, DestinationRoot: destination,
			InstallDirectory: "install", ArchiveRoot: ".", ExpectedEntryCount: "1", ExpectedUnpackedSize: "1",
		}); err != nil {
			t.Fatal(err)
		}
	})

	for _, test := range []struct {
		name    string
		trailer []byte
	}{
		{name: "nonzero-trailing-data", trailer: append([]byte{1}, make([]byte, 511)...)},
		{name: "partial-padding", trailer: []byte{0}},
		{name: "excessive-padding", trailer: bytes.Repeat([]byte{0}, int(archiveMaterializationMaxTrailingBytes)+512)},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, destination := newArchiveTestStore(t)
			descriptor := publishArchiveTestBytes(t, store, makeTarGzWithTrailing(t,
				[]archiveTestEntry{{header: tar.Header{Name: "one", Typeflag: tar.TypeReg}, content: "x"}}, test.trailer))
			_, err := store.MaterializeArchive(context.Background(), ArchiveMaterializationRequest{
				Artifact: descriptor, Format: ArchiveFormatTarGz, DestinationRoot: destination,
				InstallDirectory: "install", ArchiveRoot: ".", ExpectedEntryCount: "1", ExpectedUnpackedSize: "1",
			})
			if err == nil {
				t.Fatal("unsafe tar.gz trailing data unexpectedly materialized")
			}
			assertArchiveDestinationAbsent(t, destination, "install")
			assertArchiveTempClean(t, destination)
		})
	}
}

func TestMaterializeArchiveRejectsZipMetadataAndContainerBytes(t *testing.T) {
	tests := []struct {
		name  string
		entry zipTestEntry
	}{
		{name: "malformed-extra", entry: zipTestEntry{name: "file", content: "x", extra: []byte{1}}},
		{name: "ntfs-security-extra", entry: zipTestEntry{name: "file", content: "x", extra: zipExtraField(0x000a)}},
		{name: "aes-encryption-extra", entry: zipTestEntry{name: "file", content: "x", extra: zipExtraField(0x9901)}},
		{name: "non-utf8-name", entry: zipTestEntry{name: string([]byte{0xff}), content: "x", nonUTF8: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, destination := newArchiveTestStore(t)
			descriptor := publishArchiveTestBytes(t, store, makeZip(t, []zipTestEntry{test.entry}))
			_, err := store.MaterializeArchive(context.Background(), ArchiveMaterializationRequest{
				Artifact: descriptor, Format: ArchiveFormatZip, DestinationRoot: destination,
				InstallDirectory: "install", ArchiveRoot: ".", ExpectedEntryCount: "1", ExpectedUnpackedSize: "1",
			})
			if err == nil {
				t.Fatal("unsafe ZIP metadata unexpectedly materialized")
			}
			assertArchiveDestinationAbsent(t, destination, "install")
			assertArchiveTempClean(t, destination)
		})
	}

	t.Run("directory-with-nonzero-payload", func(t *testing.T) {
		materializer := archiveMaterializer{ctx: context.Background()}
		reader := &zip.Reader{File: []*zip.File{{FileHeader: zip.FileHeader{Name: "dir/", UncompressedSize64: 1}}}}
		if err := materializer.extractZip(reader); err == nil || !strings.Contains(err.Error(), "nonzero payload") {
			t.Fatalf("directory payload error = %v", err)
		}
	})

	for _, test := range []struct {
		name   string
		format ArchiveFormat
	}{
		{name: "invalid-gzip-container", format: ArchiveFormatTarGz},
		{name: "invalid-zip-container", format: ArchiveFormatZip},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, destination := newArchiveTestStore(t)
			descriptor := publishArchiveTestBytes(t, store, []byte("descriptor-valid but invalid archive bytes"))
			_, err := store.MaterializeArchive(context.Background(), ArchiveMaterializationRequest{
				Artifact: descriptor, Format: test.format, DestinationRoot: destination,
				InstallDirectory: "install", ArchiveRoot: ".", ExpectedEntryCount: "1", ExpectedUnpackedSize: "1",
			})
			if err == nil {
				t.Fatal("invalid archive container unexpectedly materialized")
			}
			assertArchiveDestinationAbsent(t, destination, "install")
			assertArchiveTempClean(t, destination)
		})
	}
}

func TestMaterializeArchiveRejectsPathOverCoreByteLimit(t *testing.T) {
	store, destination := newArchiveTestStore(t)
	archive := makeZip(t, []zipTestEntry{{name: strings.Repeat("a", CoreMaxArchivePathBytes+1), content: "x"}})
	descriptor := publishArchiveTestBytes(t, store, archive)
	_, err := store.MaterializeArchive(context.Background(), ArchiveMaterializationRequest{
		Artifact: descriptor, Format: ArchiveFormatZip, DestinationRoot: destination,
		InstallDirectory: "install", ArchiveRoot: ".", ExpectedEntryCount: "1", ExpectedUnpackedSize: "1",
	})
	if err == nil || !strings.Contains(err.Error(), "path exceeds") {
		t.Fatalf("path limit error = %v", err)
	}
	assertArchiveDestinationAbsent(t, destination, "install")
	assertArchiveTempClean(t, destination)
}

func TestMaterializeArchiveRejectsEncryptedZipAndCoreLimits(t *testing.T) {
	store, destination := newArchiveTestStore(t)
	archive := makeZip(t, []zipTestEntry{{name: "payload", content: "x", flags: 1}})
	descriptor := publishArchiveTestBytes(t, store, archive)
	_, err := store.MaterializeArchive(context.Background(), ArchiveMaterializationRequest{
		Artifact: descriptor, Format: ArchiveFormatZip, DestinationRoot: destination,
		InstallDirectory: "install", ArchiveRoot: ".", ExpectedEntryCount: "1", ExpectedUnpackedSize: "1",
	})
	if err == nil || !strings.Contains(err.Error(), "encrypted") {
		t.Fatalf("encrypted zip error = %v", err)
	}

	for _, test := range []struct {
		name    string
		request ArchiveMaterializationRequest
	}{
		{name: "entry-limit", request: ArchiveMaterializationRequest{
			Artifact: descriptor, Format: ArchiveFormatZip, DestinationRoot: destination,
			InstallDirectory: "entry-limit", ArchiveRoot: ".", ExpectedEntryCount: "10001", ExpectedUnpackedSize: "1",
		}},
		{name: "size-limit", request: ArchiveMaterializationRequest{
			Artifact: descriptor, Format: ArchiveFormatZip, DestinationRoot: destination,
			InstallDirectory: "size-limit", ArchiveRoot: ".", ExpectedEntryCount: "1", ExpectedUnpackedSize: "1073741825",
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := store.MaterializeArchive(context.Background(), test.request)
			if err == nil || !strings.Contains(err.Error(), "core limit") {
				t.Fatalf("limit error = %v", err)
			}
		})
	}
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

type zipTestEntry struct {
	name    string
	content string
	mode    os.FileMode
	flags   uint16
	extra   []byte
	nonUTF8 bool
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

func newArchiveMaterializationTestRequest(t *testing.T, store Store, destination string, archive []byte) ArchiveMaterializationRequest {
	t.Helper()
	return ArchiveMaterializationRequest{
		Artifact: publishArchiveTestBytes(t, store, archive), Format: ArchiveFormatTarGz, DestinationRoot: destination,
		InstallDirectory: "install", ArchiveRoot: ".", ExpectedEntryCount: "1", ExpectedUnpackedSize: "1",
	}
}

func zipExtraField(id uint16) []byte {
	return []byte{byte(id), byte(id >> 8), 0, 0}
}

func forgeZipEOCDRecordCount(t *testing.T, archive []byte, count uint16) {
	t.Helper()
	for index := len(archive) - 22; index >= 0; index-- {
		if archive[index] != 'P' || archive[index+1] != 'K' || archive[index+2] != 0x05 || archive[index+3] != 0x06 {
			continue
		}
		commentSize := int(archive[index+20]) | int(archive[index+21])<<8
		if index+22+commentSize > len(archive) {
			continue
		}
		archive[index+8] = byte(count)
		archive[index+9] = byte(count >> 8)
		archive[index+10] = byte(count)
		archive[index+11] = byte(count >> 8)
		return
	}
	t.Fatal("ZIP end-of-central-directory record not found")
}

func makeZip64(t *testing.T, entries []zipTestEntry) []byte {
	t.Helper()
	classic := makeZip(t, entries)
	eocd := -1
	for index := len(classic) - 22; index >= 0; index-- {
		if classic[index] == 'P' && classic[index+1] == 'K' && classic[index+2] == 0x05 && classic[index+3] == 0x06 {
			eocd = index
			break
		}
	}
	if eocd < 0 {
		t.Fatal("ZIP end-of-central-directory record not found")
	}
	directorySize := binary.LittleEndian.Uint32(classic[eocd+12 : eocd+16])
	directoryOffset := binary.LittleEndian.Uint32(classic[eocd+16 : eocd+20])
	var zip64End [56]byte
	binary.LittleEndian.PutUint32(zip64End[0:4], 0x06064b50)
	binary.LittleEndian.PutUint64(zip64End[4:12], 44)
	binary.LittleEndian.PutUint16(zip64End[12:14], 45)
	binary.LittleEndian.PutUint16(zip64End[14:16], 45)
	binary.LittleEndian.PutUint64(zip64End[24:32], uint64(len(entries)))
	binary.LittleEndian.PutUint64(zip64End[32:40], uint64(len(entries)))
	binary.LittleEndian.PutUint64(zip64End[40:48], uint64(directorySize))
	binary.LittleEndian.PutUint64(zip64End[48:56], uint64(directoryOffset))
	var locator [20]byte
	binary.LittleEndian.PutUint32(locator[0:4], 0x07064b50)
	binary.LittleEndian.PutUint64(locator[8:16], uint64(len(classic)-22))
	binary.LittleEndian.PutUint32(locator[16:20], 1)
	var zip32End [22]byte
	binary.LittleEndian.PutUint32(zip32End[0:4], 0x06054b50)
	binary.LittleEndian.PutUint16(zip32End[8:10], 0xffff)
	binary.LittleEndian.PutUint16(zip32End[10:12], 0xffff)
	binary.LittleEndian.PutUint32(zip32End[12:16], 0xffffffff)
	binary.LittleEndian.PutUint32(zip32End[16:20], 0xffffffff)
	result := append([]byte(nil), classic[:eocd]...)
	result = append(result, zip64End[:]...)
	result = append(result, locator[:]...)
	result = append(result, zip32End[:]...)
	return result
}

func makeTarGz(t *testing.T, entries []archiveTestEntry) []byte {
	return makeTarGzWithTrailing(t, entries, nil)
}

func makeTarGzWithTrailing(t *testing.T, entries []archiveTestEntry, trailing []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		header := entry.header
		if header.Size == 0 && header.Typeflag != tar.TypeDir && header.Typeflag != tar.TypeSymlink && header.Typeflag != tar.TypeLink && header.Typeflag != tar.TypeFifo && header.Typeflag != tar.TypeChar {
			header.Size = int64(len(entry.content))
		}
		if err := tarWriter.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA || header.Typeflag == 0 {
			if _, err := io.WriteString(tarWriter, entry.content); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := gzipWriter.Write(trailing); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func makeZip(t *testing.T, entries []zipTestEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Flags: entry.flags}
		header.Extra = entry.extra
		header.NonUTF8 = entry.nonUTF8
		header.SetMode(entry.mode)
		member, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(member, entry.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func fileMode(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
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
