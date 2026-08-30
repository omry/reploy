package providerstore

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type zipTestEntry struct {
	name    string
	content string
	mode    os.FileMode
	flags   uint16
	extra   []byte
	nonUTF8 bool
}

func TestArchiveMaterializationZipExtractsIntoTransaction(t *testing.T) {
	materializer, _ := newArchiveTransactionTestMaterializer(t, 2, 4)
	materializer.request.ArchiveRoot = "playwright"
	materializer.request.archiveRoot = "playwright"
	materializer.executablePaths["playwright/node"] = ""
	err := extractZipArchiveForTest(t, materializer, makeZip(t, []zipTestEntry{
		{name: "playwright/", mode: os.ModeDir | 0o777},
		{name: "playwright/node", content: "node", mode: 0o777},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := materializer.validateExecutablePaths(); err != nil {
		t.Fatal(err)
	}
	if err := materializer.validateExpectedInventory(); err != nil {
		t.Fatal(err)
	}
	if err := normalizeMaterializedTree(materializer.stageRoot); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(materializer.stage, "playwright", "node")
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "node" {
		t.Fatalf("materialized content = %q, %v", content, err)
	}
	assertArchiveMaterializedRegularMode(t, path, true)
	assertArchiveMaterializedDirectoryMode(t, filepath.Join(materializer.stage, "playwright"))
}

func TestArchiveMaterializationZipRejectsSpecialEntriesAndMetadata(t *testing.T) {
	tests := []struct {
		name  string
		entry zipTestEntry
		want  string
	}{
		{name: "symlink", entry: zipTestEntry{name: "link", mode: os.ModeSymlink | 0o777, content: "target"}, want: "special type"},
		{name: "fifo", entry: zipTestEntry{name: "pipe", mode: os.ModeNamedPipe | 0o644}, want: "special type"},
		{name: "device", entry: zipTestEntry{name: "device", mode: os.ModeDevice | 0o600}, want: "special type"},
		{name: "encrypted", entry: zipTestEntry{name: "secret", flags: 1, content: "x"}, want: "encrypted"},
		{name: "non-utf8", entry: zipTestEntry{name: string([]byte{0xff}), nonUTF8: true, content: "x"}, want: "not UTF-8"},
		{name: "malformed-extra", entry: zipTestEntry{name: "file", extra: []byte{1}, content: "x"}, want: "malformed extra"},
		{name: "ntfs-security-extra", entry: zipTestEntry{name: "file", extra: zipExtraField(0x000a), content: "x"}, want: "security or encryption"},
		{name: "aes-encryption-extra", entry: zipTestEntry{name: "file", extra: zipExtraField(0x9901), content: "x"}, want: "security or encryption"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			materializer, _ := newArchiveTransactionTestMaterializer(t, 1, 6)
			err := extractZipArchiveForTest(t, materializer, makeZip(t, []zipTestEntry{test.entry}))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("unsafe ZIP entry error = %v, want %q", err, test.want)
			}
		})
	}

	materializer, _ := newArchiveTransactionTestMaterializer(t, 1, 1)
	reader := &zip.Reader{File: []*zip.File{{FileHeader: zip.FileHeader{Name: "dir/", UncompressedSize64: 1}}}}
	if err := materializer.extractZip(reader); err == nil || !strings.Contains(err.Error(), "nonzero payload") {
		t.Fatalf("directory payload error = %v", err)
	}
}

func TestArchiveMaterializationZipRejectsCentralDirectoryLimits(t *testing.T) {
	tests := []struct {
		name    string
		archive func(*testing.T) []byte
		want    string
		entries uint64
		size    uint64
	}{
		{name: "actual-record-count", archive: func(t *testing.T) []byte {
			entries := make([]zipTestEntry, CoreMaxArchiveEntries+1)
			for index := range entries {
				entries[index].name = fmt.Sprintf("entry-%05d", index)
			}
			archive := makeZip(t, entries)
			forgeZipEOCDRecordCount(t, archive, 1)
			return archive
		}, want: "central directory", entries: 1, size: 1},
		{name: "record-metadata", archive: func(t *testing.T) []byte {
			return makeZip(t, []zipTestEntry{{name: "file", content: "x", extra: bytes.Repeat([]byte{0}, CoreMaxArchivePathBytes+1)}})
		}, want: "central directory", entries: 1, size: 1},
		{name: "directory-byte-budget", archive: func(t *testing.T) []byte {
			archive := makeZip(t, []zipTestEntry{{name: "file", content: "x"}})
			forgeZipEOCDDirectorySize(t, archive, uint32(archiveMaterializationMaxZipDirectoryBytes+1))
			return archive
		}, want: "central directory", entries: 1, size: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			materializer, _ := newArchiveTransactionTestMaterializer(t, test.entries, test.size)
			err := extractZipArchiveForTest(t, materializer, test.archive(t))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ZIP preflight error = %v, want %q", err, test.want)
			}
			if len(materializer.entries) != 0 {
				t.Fatalf("preflight accepted entries: %#v", materializer.entries)
			}
		})
	}
}

func TestArchiveMaterializationZipAcceptsZip64CentralDirectory(t *testing.T) {
	materializer, _ := newArchiveTransactionTestMaterializer(t, 1, 1)
	if err := extractZipArchiveForTest(t, materializer, makeZip64(t, []zipTestEntry{{name: "file", content: "x"}})); err != nil {
		t.Fatal(err)
	}
	if err := materializer.validateExpectedInventory(); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveMaterializationZipAcceptsZip64DirectorySizeFFFFSentinel(t *testing.T) {
	materializer, _ := newArchiveTransactionTestMaterializer(t, 1, 1)
	archive := makeZip64WithDirectorySizeFFFFSentinel(t, []zipTestEntry{{name: "file", content: "x"}})
	if err := extractZipArchiveForTest(t, materializer, archive); err != nil {
		t.Fatal(err)
	}
	if err := materializer.validateExpectedInventory(); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveMaterializationZipAcceptsClassicDirectorySizeFFFFWithoutLocator(t *testing.T) {
	archive := makeClassicZipWithDirectorySizeFFFF(t)
	if _, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive))); err != nil {
		t.Fatalf("stdlib classic ZIP = %v", err)
	}
	materializer, _ := newArchiveTransactionTestMaterializer(t, 1000, 0)
	if err := extractZipArchiveForTest(t, materializer, archive); err != nil {
		t.Fatal(err)
	}
	if err := materializer.validateExpectedInventory(); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveMaterializationZipRejectsAmbiguousPrependedDirectory(t *testing.T) {
	archive := makeAmbiguousPrependedZip(t)
	file, err := os.CreateTemp(t.TempDir(), "ambiguous-zip-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write(archive); err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(file, info.Size())
	if err != nil || len(reader.File) != 1 || reader.File[0].Name != "raw!" {
		t.Fatalf("stdlib ZIP fallback = %#v, %v", reader, err)
	}

	materializer, _ := newArchiveTransactionTestMaterializer(t, 1, 1)
	err = extractZipArchiveForTest(t, materializer, archive)
	if err == nil || !strings.Contains(err.Error(), "ambiguous raw offset") {
		t.Fatalf("ambiguous prepended ZIP error = %v", err)
	}
	if len(materializer.entries) != 0 {
		t.Fatalf("ambiguous ZIP accepted entries before rejection: %#v", materializer.entries)
	}
}

func TestArchiveMaterializationZipAcceptsPrependedArchive(t *testing.T) {
	materializer, _ := newArchiveTransactionTestMaterializer(t, 1, 1)
	if err := extractZipArchiveForTest(t, materializer, makePrependedZip(t)); err != nil {
		t.Fatal(err)
	}
	if err := materializer.validateExpectedInventory(); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveMaterializationZipAcceptsInvalidRawOffsetDirectoryCandidate(t *testing.T) {
	archive := makePrependedZipWithInvalidRawDirectoryCandidate(t)
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil || len(reader.File) != 1 || reader.File[0].Name != "safe" {
		t.Fatalf("stdlib prepended ZIP = %#v, %v", reader, err)
	}
	materializer, _ := newArchiveTransactionTestMaterializer(t, 1, 1)
	if err := extractZipArchiveForTest(t, materializer, archive); err != nil {
		t.Fatal(err)
	}
	if err := materializer.validateExpectedInventory(); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveMaterializationZipAcceptsLaterRawOffsetDirectory(t *testing.T) {
	archive := makeLaterRawOffsetZip(t)
	file, err := os.CreateTemp(t.TempDir(), "later-offset-zip-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write(archive); err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(file, info.Size())
	if err != nil || len(reader.File) != 2 || reader.File[0].Name != "one" || reader.File[1].Name != "two" {
		t.Fatalf("stdlib negative-base ZIP = %#v, %v", reader, err)
	}

	materializer, _ := newArchiveTransactionTestMaterializer(t, 2, 2)
	if err := extractZipArchiveForTest(t, materializer, archive); err != nil {
		t.Fatal(err)
	}
	if err := materializer.validateExpectedInventory(); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveMaterializationZipRejectsAliasesAndInvalidPaths(t *testing.T) {
	tests := []struct {
		name    string
		entries []zipTestEntry
	}{
		{name: "ascii-case-implicit-parent", entries: []zipTestEntry{{name: "Bin/a", content: "x"}, {name: "bin/b", content: "y"}}},
		{name: "simple-fold-implicit-parent", entries: []zipTestEntry{{name: "Σ/a", content: "x"}, {name: "ς/b", content: "y"}}},
		{name: "normalization-implicit-parent", entries: []zipTestEntry{{name: "café/a", content: "x"}, {name: "café/b", content: "y"}}},
		{name: "explicit-directories", entries: []zipTestEntry{{name: "Bin/"}, {name: "bin/"}, {name: "tail", content: "x"}}},
		{name: "archive-root-mapping", entries: []zipTestEntry{{name: "payload/Bin/a", content: "x"}, {name: "payload/bin/b", content: "y"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			materializer, _ := newArchiveTransactionTestMaterializer(t, uint64(len(test.entries)), 2)
			if test.name == "archive-root-mapping" {
				materializer.request.InstallDirectory = "payload"
				materializer.request.ArchiveRoot = "payload"
				materializer.request.archiveRoot = "payload"
			}
			err := extractZipArchiveForTest(t, materializer, makeZip(t, test.entries))
			if err == nil || !strings.Contains(err.Error(), "case-insensitive") {
				t.Fatalf("ZIP destination alias error = %v", err)
			}
		})
	}
}

func TestArchiveMaterializationZipRejectsInventoryAndPathLimits(t *testing.T) {
	tests := []struct {
		name           string
		entries        []zipTestEntry
		entryLimit     uint64
		sizeLimit      uint64
		wantExtraction string
		wantInventory  string
	}{
		{name: "observed-count-below-expected", entries: []zipTestEntry{{name: "one", content: "x"}}, entryLimit: 2, sizeLimit: 1, wantInventory: "entry count"},
		{name: "observed-count-above-expected", entries: []zipTestEntry{{name: "one", content: "x"}, {name: "two", content: "x"}}, entryLimit: 1, sizeLimit: 2, wantExtraction: "expected count"},
		{name: "observed-size-below-expected", entries: []zipTestEntry{{name: "one", content: "x"}}, entryLimit: 1, sizeLimit: 2, wantInventory: "unpacked size"},
		{name: "observed-size-above-expected", entries: []zipTestEntry{{name: "one", content: "xx"}}, entryLimit: 1, sizeLimit: 1, wantExtraction: "expected or core limit"},
		{name: "path-over-limit", entries: []zipTestEntry{{name: strings.Repeat("a", CoreMaxArchivePathBytes+1), content: "x"}}, entryLimit: 1, sizeLimit: 1, wantExtraction: "central directory"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			materializer, _ := newArchiveTransactionTestMaterializer(t, test.entryLimit, test.sizeLimit)
			err := extractZipArchiveForTest(t, materializer, makeZip(t, test.entries))
			if test.wantExtraction != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantExtraction) {
					t.Fatalf("extraction error = %v, want %q", err, test.wantExtraction)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected extraction error = %v", err)
			}
			if err := materializer.validateExpectedInventory(); err == nil || !strings.Contains(err.Error(), test.wantInventory) {
				t.Fatalf("inventory error = %v, want %q", err, test.wantInventory)
			}
		})
	}
}

func TestArchiveMaterializationZipRejectsMalformedAndCanceledInput(t *testing.T) {
	materializer, _ := newArchiveTransactionTestMaterializer(t, 1, 1)
	if err := extractZipArchiveForTest(t, materializer, []byte("not a ZIP")); err == nil {
		t.Fatal("malformed ZIP unexpectedly accepted")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	materializer, _ = newArchiveTransactionTestMaterializer(t, 1, 1)
	materializer.ctx = canceled
	if err := extractZipArchiveForTest(t, materializer, makeZip(t, []zipTestEntry{{name: "one", content: "x"}})); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled extraction error = %v", err)
	}
}

func TestArchiveMaterializationZipNormalizesOrdinaryModes(t *testing.T) {
	materializer, _ := newArchiveTransactionTestMaterializer(t, 1, 1)
	if err := extractZipArchiveForTest(t, materializer, makeZip(t, []zipTestEntry{{name: "file", content: "x", mode: 0o7777}})); err != nil {
		t.Fatal(err)
	}
	assertArchiveMaterializedRegularMode(t, filepath.Join(materializer.stage, "file"), false)
}

func TestArchiveMaterializationZipRejectsForgedMemberSize(t *testing.T) {
	archive := makeZip(t, []zipTestEntry{{name: "file", content: "x"}})
	forgeZipCentralDirectoryUncompressedSize(t, archive, 2)
	materializer, _ := newArchiveTransactionTestMaterializer(t, 1, 2)
	if err := extractZipArchiveForTest(t, materializer, archive); err == nil || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("forged ZIP member size error = %v", err)
	}
}

func extractZipArchiveForTest(t *testing.T, materializer *archiveMaterializer, archive []byte) error {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "archive-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	name := file.Name()
	t.Cleanup(func() { _ = os.Remove(name) })
	if _, err := file.Write(archive); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	return materializer.extractZipFile(file)
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

func forgeZipEOCDDirectorySize(t *testing.T, archive []byte, size uint32) {
	t.Helper()
	for index := len(archive) - 22; index >= 0; index-- {
		if archive[index] != 'P' || archive[index+1] != 'K' || archive[index+2] != 0x05 || archive[index+3] != 0x06 {
			continue
		}
		commentSize := int(archive[index+20]) | int(archive[index+21])<<8
		if index+22+commentSize > len(archive) {
			continue
		}
		archive[index+12] = byte(size)
		archive[index+13] = byte(size >> 8)
		archive[index+14] = byte(size >> 16)
		archive[index+15] = byte(size >> 24)
		return
	}
	t.Fatal("ZIP end-of-central-directory record not found")
}

func forgeZipEOCDDirectoryOffset(t *testing.T, archive []byte, offset uint32) {
	t.Helper()
	for index := len(archive) - 22; index >= 0; index-- {
		if archive[index] != 'P' || archive[index+1] != 'K' || archive[index+2] != 0x05 || archive[index+3] != 0x06 {
			continue
		}
		commentSize := int(archive[index+20]) | int(archive[index+21])<<8
		if index+22+commentSize > len(archive) {
			continue
		}
		archive[index+16] = byte(offset)
		archive[index+17] = byte(offset >> 8)
		archive[index+18] = byte(offset >> 16)
		archive[index+19] = byte(offset >> 24)
		return
	}
	t.Fatal("ZIP end-of-central-directory record not found")
}

func forgeZipCentralDirectoryUncompressedSize(t *testing.T, archive []byte, size uint32) {
	t.Helper()
	for index := len(archive) - 46; index >= 0; index-- {
		if archive[index] != 'P' || archive[index+1] != 'K' || archive[index+2] != 0x01 || archive[index+3] != 0x02 {
			continue
		}
		archive[index+24] = byte(size)
		archive[index+25] = byte(size >> 8)
		archive[index+26] = byte(size >> 16)
		archive[index+27] = byte(size >> 24)
		return
	}
	t.Fatal("ZIP central-directory record not found")
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

func makeZip64WithDirectorySizeFFFFSentinel(t *testing.T, entries []zipTestEntry) []byte {
	t.Helper()
	archive := makeZip64(t, entries)
	const (
		directory64EndLen = 56
		directory64LocLen = 20
		directoryEndLen   = 22
	)
	eocdOffset := len(archive) - directoryEndLen
	zip64Offset := eocdOffset - directory64LocLen - directory64EndLen
	if zip64Offset < 0 || binary.LittleEndian.Uint32(archive[zip64Offset:zip64Offset+4]) != 0x06064b50 {
		t.Fatal("ZIP64 end-of-central-directory record not found")
	}
	if len(entries) > int(^uint16(0)) {
		t.Fatalf("ZIP64 test fixture has too many entries: %d", len(entries))
	}
	directoryOffset := binary.LittleEndian.Uint64(archive[zip64Offset+48 : zip64Offset+56])
	if directoryOffset > uint64(^uint32(0)) {
		t.Fatalf("ZIP64 test directory offset is too large: %d", directoryOffset)
	}
	binary.LittleEndian.PutUint16(archive[eocdOffset+8:eocdOffset+10], uint16(len(entries)))
	binary.LittleEndian.PutUint16(archive[eocdOffset+10:eocdOffset+12], uint16(len(entries)))
	binary.LittleEndian.PutUint32(archive[eocdOffset+12:eocdOffset+16], 0xffff)
	binary.LittleEndian.PutUint32(archive[eocdOffset+16:eocdOffset+20], uint32(directoryOffset))
	return archive
}

func makeClassicZipWithDirectorySizeFFFF(t *testing.T) []byte {
	t.Helper()
	entries := make([]zipTestEntry, 1000)
	for index := range entries {
		padding := 8
		if index >= 465 {
			padding = 9
		}
		entries[index].name = fmt.Sprintf("entry-%04d-%s", index, strings.Repeat("x", padding))
	}
	archive := makeZip(t, entries)
	eocdOffset := len(archive) - 22
	if binary.LittleEndian.Uint32(archive[eocdOffset:eocdOffset+4]) != 0x06054b50 {
		t.Fatal("ZIP end-of-central-directory record not found")
	}
	if got := binary.LittleEndian.Uint32(archive[eocdOffset+12 : eocdOffset+16]); got != 0xffff {
		t.Fatalf("classic ZIP central-directory size = %d, want 65535", got)
	}
	if binary.LittleEndian.Uint32(archive[eocdOffset-20:eocdOffset-16]) == 0x07064b50 {
		t.Fatal("classic ZIP unexpectedly contains a ZIP64 locator")
	}
	return archive
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

func makePrependedZip(t *testing.T) []byte {
	t.Helper()
	base := makeZip(t, []zipTestEntry{{name: "file", content: "x"}})
	return append(make([]byte, 128), base...)
}

func makeAmbiguousPrependedZip(t *testing.T) []byte {
	t.Helper()
	base := makeZip(t, []zipTestEntry{{name: "safe", content: "x"}})
	eocd := -1
	for index := len(base) - 22; index >= 0; index-- {
		if base[index] == 'P' && base[index+1] == 'K' && base[index+2] == 0x05 && base[index+3] == 0x06 {
			eocd = index
			break
		}
	}
	if eocd < 0 {
		t.Fatal("ZIP end-of-central-directory record not found")
	}
	directoryOffset := int(binary.LittleEndian.Uint32(base[eocd+16 : eocd+20]))
	directorySize := int(binary.LittleEndian.Uint32(base[eocd+12 : eocd+16]))
	prefixSize := 128
	if directoryOffset+directorySize > prefixSize {
		t.Fatalf("test ZIP directory does not fit prefix: offset=%d size=%d", directoryOffset, directorySize)
	}
	archive := append(make([]byte, prefixSize), base...)
	copy(archive[directoryOffset:directoryOffset+directorySize], base[directoryOffset:directoryOffset+directorySize])
	copy(archive[directoryOffset+46:directoryOffset+50], []byte("raw!"))
	return archive
}

func makePrependedZipWithInvalidRawDirectoryCandidate(t *testing.T) []byte {
	t.Helper()
	archive := makeAmbiguousPrependedZip(t)
	eocdOffset := len(archive) - 22
	if binary.LittleEndian.Uint32(archive[eocdOffset:eocdOffset+4]) != 0x06054b50 {
		t.Fatal("ZIP end-of-central-directory record not found")
	}
	rawDirectoryOffset := int(binary.LittleEndian.Uint32(archive[eocdOffset+16 : eocdOffset+20]))
	if rawDirectoryOffset < 0 || rawDirectoryOffset > len(archive)-46 {
		t.Fatalf("raw ZIP directory offset = %d", rawDirectoryOffset)
	}
	binary.LittleEndian.PutUint32(archive[rawDirectoryOffset+20:rawDirectoryOffset+24], 0xffffffff)
	return archive
}

func makeLaterRawOffsetZip(t *testing.T) []byte {
	t.Helper()
	archive := makeZip(t, []zipTestEntry{{name: "one", content: "x"}, {name: "two", content: "y"}})
	eocd := -1
	for index := len(archive) - 22; index >= 0; index-- {
		if archive[index] == 'P' && archive[index+1] == 'K' && archive[index+2] == 0x05 && archive[index+3] == 0x06 {
			eocd = index
			break
		}
	}
	if eocd < 0 {
		t.Fatal("ZIP end-of-central-directory record not found")
	}
	directoryOffset := binary.LittleEndian.Uint32(archive[eocd+16 : eocd+20])
	firstHeaderSize := 46 + len("one")
	for _, headerOffset := range []int{int(directoryOffset), int(directoryOffset) + firstHeaderSize} {
		localOffset := binary.LittleEndian.Uint32(archive[headerOffset+42 : headerOffset+46])
		binary.LittleEndian.PutUint32(archive[headerOffset+42:headerOffset+46], localOffset+uint32(firstHeaderSize))
	}
	forgeZipEOCDDirectoryOffset(t, archive, directoryOffset+uint32(firstHeaderSize))
	return archive
}
