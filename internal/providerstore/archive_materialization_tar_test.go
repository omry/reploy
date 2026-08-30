package providerstore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type archiveTestEntry struct {
	header  tar.Header
	content string
}

func TestArchiveMaterializationTarExtractsIntoTransaction(t *testing.T) {
	materializer, _ := newArchiveTransactionTestMaterializer(t, 2, 3)
	err := extractTarArchiveForTest(t, materializer, makeTarGz(t, []archiveTestEntry{
		{header: tar.Header{Name: "bin", Typeflag: tar.TypeDir}},
		{header: tar.Header{Name: "bin/tool", Typeflag: tar.TypeReg}, content: "abc"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := materializer.validateExpectedInventory(); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(materializer.stage, "bin", "tool")); err != nil || string(content) != "abc" {
		t.Fatalf("materialized content = %q, %v", content, err)
	}
	if err := normalizeMaterializedTree(materializer.stageRoot); err != nil {
		t.Fatal(err)
	}
	assertArchiveMaterializedDirectoryMode(t, filepath.Join(materializer.stage, "bin"))
}

func TestArchiveMaterializationTarRejectsHeadersAndMetadata(t *testing.T) {
	tests := []struct {
		name   string
		header tar.Header
	}{
		{name: "nil", header: tar.Header{}},
		{name: "symlink", header: tar.Header{Name: "link", Typeflag: tar.TypeSymlink}},
		{name: "hardlink", header: tar.Header{Name: "link", Typeflag: tar.TypeLink}},
		{name: "fifo", header: tar.Header{Name: "pipe", Typeflag: tar.TypeFifo}},
		{name: "device", header: tar.Header{Name: "device", Typeflag: tar.TypeChar}},
		{name: "pax", header: tar.Header{Name: "pax", Typeflag: tar.TypeReg, Format: tar.FormatPAX}},
		{name: "xattr", header: tar.Header{Name: "xattr", Typeflag: tar.TypeReg, Xattrs: map[string]string{"user.test": "x"}}},
		{name: "acl", header: tar.Header{Name: "acl", Typeflag: tar.TypeReg, PAXRecords: map[string]string{"SCHILY.acl.access": "user::r--"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "nil" {
				if err := validateTarHeader(nil); err == nil {
					t.Fatal("nil tar header accepted")
				}
				return
			}
			if err := validateTarHeader(&test.header); err == nil {
				t.Fatalf("unsafe tar header accepted: %#v", test.header)
			}
		})
	}

	for _, test := range []struct {
		name    string
		entries []archiveTestEntry
	}{
		{name: "symlink-reader-path", entries: []archiveTestEntry{{header: tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "target"}}}},
		{name: "fifo-reader-path", entries: []archiveTestEntry{{header: tar.Header{Name: "pipe", Typeflag: tar.TypeFifo}}}},
		{name: "acl-pax-reader-path", entries: []archiveTestEntry{{header: tar.Header{Name: "acl", Typeflag: tar.TypeReg, PAXRecords: map[string]string{"SCHILY.acl.access": "user::r--"}, Format: tar.FormatPAX}, content: "x"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			materializer, _ := newArchiveTransactionTestMaterializer(t, 1, 1)
			err := extractTarArchiveForTest(t, materializer, makeTarGz(t, test.entries))
			if err == nil {
				t.Fatalf("unsafe TAR entry unexpectedly accepted: %#v", test.entries)
			}
		})
	}

	materializer, _ := newArchiveTransactionTestMaterializer(t, 1, 1)
	err := extractTarArchiveForTest(t, materializer, makeTarGz(t, []archiveTestEntry{{header: tar.Header{Name: "dir", Typeflag: tar.TypeDir, Size: 1}}}))
	if err == nil || !strings.Contains(err.Error(), "nonzero payload") {
		t.Fatalf("nonzero directory payload error = %v", err)
	}
}

func TestArchiveMaterializationTarRejectsMetadataBudgetAndUnicodeAliases(t *testing.T) {
	materializer, _ := newArchiveTransactionTestMaterializer(t, 1, 1)
	err := extractTarArchiveForTest(t, materializer, makeTarGzWithGNUlongNameChain(t, 2))
	if err == nil || !strings.Contains(err.Error(), "decompressed byte budget") {
		t.Fatalf("metadata budget error = %v", err)
	}

	alias, _ := newArchiveTransactionTestMaterializer(t, 2, 2)
	err = extractTarArchiveForTest(t, alias, makeTarGz(t, []archiveTestEntry{
		{header: tar.Header{Name: "café/a", Typeflag: tar.TypeReg, Format: tar.FormatGNU}, content: "x"},
		{header: tar.Header{Name: "café/b", Typeflag: tar.TypeReg, Format: tar.FormatGNU}, content: "y"},
	}))
	if err == nil || !strings.Contains(err.Error(), "aliases") {
		t.Fatalf("Unicode destination alias error = %v", err)
	}
}

func TestArchiveMaterializationTarValidatesGzipTrailerAndPadding(t *testing.T) {
	tests := []struct {
		name       string
		trailer    []byte
		mutate     func([]byte)
		wantErr    string
		wantAccept bool
	}{
		{name: "corrupt-trailer", mutate: func(archive []byte) { archive[len(archive)-1] ^= 1 }, wantErr: "checksum"},
		{name: "nonzero-trailing-data", trailer: append([]byte{1}, make([]byte, 511)...), wantErr: "nonzero trailing"},
		{name: "partial-padding", trailer: []byte{0}, wantErr: "whole tar block"},
		{name: "excessive-padding", trailer: bytes.Repeat([]byte{0}, int(archiveMaterializationMaxTrailingBytes)+512), wantErr: "exceeds core limit"},
		{name: "one-block-padding", trailer: bytes.Repeat([]byte{0}, 512), wantAccept: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			materializer, _ := newArchiveTransactionTestMaterializer(t, 1, 1)
			archive := makeTarGzWithTrailing(t, []archiveTestEntry{{header: tar.Header{Name: "one", Typeflag: tar.TypeReg}, content: "x"}}, test.trailer)
			if test.mutate != nil {
				test.mutate(archive)
			}
			err := extractTarArchiveForTest(t, materializer, archive)
			if test.wantAccept {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("trailer error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestArchiveMaterializationTarRequiresCompleteEndMarker(t *testing.T) {
	tests := []struct {
		name      string
		endBlocks int
	}{
		{name: "missing-end-marker", endBlocks: 0},
		{name: "partial-end-marker", endBlocks: 1},
		{name: "complete-end-marker", endBlocks: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			materializer, _ := newArchiveTransactionTestMaterializer(t, 1, 1)
			err := extractTarArchiveForTest(t, materializer, makeTarGzWithEndBlocks(t, []archiveTestEntry{{header: tar.Header{Name: "one", Typeflag: tar.TypeReg}, content: "x"}}, test.endBlocks))
			if test.endBlocks == 2 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, errArchiveTarMissingEndMarker) {
				t.Fatalf("end marker error = %v", err)
			}
		})
	}
}

func TestArchiveMaterializationTarRejectsOrphanGNUlongNameBeforeEndMarker(t *testing.T) {
	materializer, _ := newArchiveTransactionTestMaterializer(t, 1, 1)
	err := extractTarArchiveForTest(t, materializer, makeTarGzWithOrphanGNUlongName(t))
	if !errors.Is(err, errArchiveTarMissingEndMarker) {
		t.Fatalf("orphan GNU long-name error = %v", err)
	}
}

func TestArchiveMaterializationTarRejectsMalformedAndCanceledInput(t *testing.T) {
	materializer, _ := newArchiveTransactionTestMaterializer(t, 1, 1)
	if err := extractTarArchiveForTest(t, materializer, []byte("not gzip")); err == nil {
		t.Fatal("malformed gzip unexpectedly accepted")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	materializer, _ = newArchiveTransactionTestMaterializer(t, 1, 1)
	materializer.ctx = canceled
	if err := extractTarArchiveForTest(t, materializer, makeTarGz(t, []archiveTestEntry{{header: tar.Header{Name: "one", Typeflag: tar.TypeReg}, content: "x"}})); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled extraction error = %v", err)
	}
}

func extractTarArchiveForTest(t *testing.T, materializer *archiveMaterializer, archive []byte) error {
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
	return materializer.extractTarGz(file)
}

func makeTarGz(t *testing.T, entries []archiveTestEntry) []byte {
	return makeTarGzWithTrailing(t, entries, nil)
}

func makeTarGzWithEndBlocks(t *testing.T, entries []archiveTestEntry, endBlocks int) []byte {
	t.Helper()
	return rewriteTarGzEndBlocks(t, makeTarGz(t, entries), endBlocks)
}

func makeTarGzWithOrphanGNUlongName(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	header := tar.Header{
		Name:     strings.Repeat("a", CoreMaxArchivePathBytes),
		Linkname: "target",
		Typeflag: tar.TypeReg,
		Format:   tar.FormatGNU,
		Size:     1,
	}
	if err := tarWriter.WriteHeader(&header); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	tarBytes := readTarGzBytes(t, output.Bytes())
	const (
		tarBlockBytes = 512
		tarTypeOffset = 156
	)
	for offset := 0; offset+tarBlockBytes <= len(tarBytes); offset += tarBlockBytes {
		if tarBytes[offset+tarTypeOffset] == tar.TypeReg {
			tarBytes = tarBytes[:offset]
			tarBytes = append(tarBytes, bytes.Repeat([]byte{0}, tarBlockBytes)...)
			return writeTarGzBytes(t, tarBytes)
		}
	}
	t.Fatal("GNU long-name fixture has no regular member header")
	return nil
}

func rewriteTarGzEndBlocks(t *testing.T, archive []byte, endBlocks int) []byte {
	t.Helper()
	tarBytes := readTarGzBytes(t, archive)
	const endMarkerBytes = 2 * 512
	if len(tarBytes) < endMarkerBytes {
		t.Fatalf("tar fixture is shorter than its end marker: %d bytes", len(tarBytes))
	}
	tarBytes = tarBytes[:len(tarBytes)-endMarkerBytes]
	tarBytes = append(tarBytes, bytes.Repeat([]byte{0}, endBlocks*512)...)
	return writeTarGzBytes(t, tarBytes)
}

func readTarGzBytes(t *testing.T, archive []byte) []byte {
	t.Helper()
	gzipReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	tarBytes, err := io.ReadAll(gzipReader)
	if err != nil {
		t.Fatal(err)
	}
	if err := gzipReader.Close(); err != nil {
		t.Fatal(err)
	}
	return tarBytes
}

func writeTarGzBytes(t *testing.T, tarBytes []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	if _, err := gzipWriter.Write(tarBytes); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func makeTarGzWithGNUlongNameChain(t *testing.T, count int) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	if count != 2 {
		t.Fatalf("test fixture requires a two-record metadata chain, got %d", count)
	}
	longName := strings.Repeat("a", CoreMaxArchivePathBytes)
	header := tar.Header{Name: longName, Linkname: longName, Typeflag: tar.TypeReg, Format: tar.FormatGNU, Size: 1}
	if err := tarWriter.WriteHeader(&header); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
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
