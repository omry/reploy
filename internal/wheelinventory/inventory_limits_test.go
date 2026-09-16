package wheelinventory

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadPathLimits(t *testing.T) {
	exactPath := strings.Repeat(strings.Repeat("a", 255)+"/", 15) + strings.Repeat("b", 254) + "/x"
	if len(exactPath) != 4096 {
		t.Fatal(len(exactPath))
	}
	for _, name := range []string{strings.Repeat("a", 255), exactPath} {
		data := makeArchive(t, []archiveEntry{{name: name}})
		if _, err := Read(context.Background(), bytes.NewReader(data), int64(len(data))); err != nil {
			t.Fatalf("path length %d: %v", len(name), err)
		}
	}
	for _, name := range []string{strings.Repeat("a", 256), exactPath + "x"} {
		data := makeArchive(t, []archiveEntry{{name: name}})
		if _, err := Read(context.Background(), bytes.NewReader(data), int64(len(data))); err == nil {
			t.Fatalf("accepted path length %d", len(name))
		}
	}
}

func TestReadEntryCountLimits(t *testing.T) {
	entries := make([]archiveEntry, 10001)
	for i := range entries {
		entries[i].name = fmt.Sprintf("file-%05d", i)
	}
	data := makeArchive(t, entries[:10000])
	if _, err := Read(context.Background(), bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		data  []byte
		count uint16
		want  string
	}{
		{"declared", data, 10001, "declares"},
		{"actual", makeArchive(t, entries), 1, "more than core limit"},
		{"mismatch", data, 9999, "not a valid zip file"},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := append([]byte(nil), test.data...)
			binary.LittleEndian.PutUint16(data[len(data)-12:len(data)-10], test.count)
			if _, err := Read(context.Background(), bytes.NewReader(data), int64(len(data))); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestReadDeclaredSizeLimitsWithoutReadingContent(t *testing.T) {
	for _, test := range []struct {
		name   string
		sizes  []uint64
		accept bool
	}{
		{"exact", []uint64{1 << 30}, true},
		{"single excess", []uint64{1<<30 + 1}, false},
		{"aggregate exact", []uint64{1 << 29, 1 << 29}, true},
		{"aggregate excess", []uint64{1 << 29, 1<<29 + 1}, false},
		{"overflow", []uint64{1, math.MaxUint64}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			writer := zip.NewWriter(&output)
			for i, size := range test.sizes {
				// Deliberately absent compressed content: inventory must only inspect
				// the declared sizes, and must not attempt to open or decompress members.
				_, err := writer.CreateRaw(&zip.FileHeader{Name: fmt.Sprintf("file-%d", i), Method: zip.Deflate, UncompressedSize64: size})
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			_, err := Read(context.Background(), bytes.NewReader(output.Bytes()), int64(output.Len()))
			if test.accept && err != nil {
				t.Fatal(err)
			}
			if !test.accept && (err == nil || !strings.Contains(err.Error(), "uncompressed size")) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestReadZIP64AndDirectoryBounds(t *testing.T) {
	classic := makeArchive(t, []archiveEntry{{name: "file", content: "x"}})
	end := len(classic) - 22
	zip64 := append([]byte(nil), classic[:end]...)
	record := make([]byte, 56)
	binary.LittleEndian.PutUint32(record, 0x06064b50)
	binary.LittleEndian.PutUint64(record[4:], 44)
	binary.LittleEndian.PutUint64(record[24:], 1)
	binary.LittleEndian.PutUint64(record[32:], 1)
	binary.LittleEndian.PutUint64(record[40:], uint64(binary.LittleEndian.Uint32(classic[end+12:])))
	binary.LittleEndian.PutUint64(record[48:], uint64(binary.LittleEndian.Uint32(classic[end+16:])))
	zip64 = append(zip64, record...)
	locator := make([]byte, 20)
	binary.LittleEndian.PutUint32(locator, 0x07064b50)
	binary.LittleEndian.PutUint64(locator[8:], uint64(end))
	binary.LittleEndian.PutUint32(locator[16:], 1)
	zip64 = append(zip64, locator...)
	eocd := append([]byte(nil), classic[end:]...)
	binary.LittleEndian.PutUint16(eocd[10:], 0xffff)
	zip64 = append(zip64, eocd...)
	if _, err := Read(context.Background(), bytes.NewReader(zip64), int64(len(zip64))); err != nil {
		t.Fatal(err)
	}
	malformed := append([]byte(nil), zip64...)
	binary.LittleEndian.PutUint64(malformed[end+4:], 43)
	if _, err := Read(context.Background(), bytes.NewReader(malformed), int64(len(malformed))); err == nil {
		t.Fatal("accepted malformed ZIP64")
	}
	oversized := append([]byte(nil), classic...)
	binary.LittleEndian.PutUint32(oversized[end+12:], 10000*(46+4096)+1)
	if _, err := Read(context.Background(), bytes.NewReader(oversized), int64(len(oversized))); err == nil || !strings.Contains(err.Error(), "byte budget") {
		t.Fatalf("directory budget: %v", err)
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	if _, err := writer.CreateHeader(&zip.FileHeader{Name: "file", Comment: strings.Repeat("x", 4096)}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(context.Background(), bytes.NewReader(output.Bytes()), int64(output.Len())); err == nil || !strings.Contains(err.Error(), "record metadata") {
		t.Fatalf("metadata budget: %v", err)
	}
}

func TestReadRejectsStructureBeforeOpeningBrokenMember(t *testing.T) {
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	if _, err := writer.CreateRaw(&zip.FileHeader{Name: "pkg.dist-info/METADATA", Method: 999, UncompressedSize64: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Create("../outside"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(context.Background(), bytes.NewReader(output.Bytes()), int64(output.Len())); err == nil || !strings.Contains(err.Error(), "invalid component") {
		t.Fatalf("structure must fail before decompression: %v", err)
	}
}

func TestReadFileAndReaderBoundaries(t *testing.T) {
	data := makeArchive(t, []archiveEntry{{name: "file", content: "x"}})
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, test := range []struct {
		ctx    context.Context
		reader io.ReaderAt
		size   int64
	}{
		{nil, bytes.NewReader(data), int64(len(data))},
		{cancelled, bytes.NewReader(data), int64(len(data))},
		{context.Background(), nil, 0},
		{context.Background(), (*os.File)(nil), 0},
		{context.Background(), bytes.NewReader(data), -1},
		{context.Background(), bytes.NewReader(data), int64(len(data) + 1)},
		{context.Background(), failingReader{}, int64(len(data))},
	} {
		if _, err := Read(test.ctx, test.reader, test.size); err == nil {
			t.Fatal("accepted invalid reader")
		}
	}
	dir := t.TempDir()
	if _, err := Open(context.Background(), dir); err == nil {
		t.Fatal("accepted directory")
	}
	if _, err := Open(context.Background(), filepath.Join(dir, "missing")); err == nil {
		t.Fatal("accepted missing file")
	}
	filename := filepath.Join(dir, "wheel.whl")
	if err := os.WriteFile(filename, data, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := Read(context.Background(), file, int64(len(data)-1)); err == nil {
		t.Fatal("accepted wrong file size")
	}
	inventory, err := Open(context.Background(), filename)
	if err != nil {
		t.Fatal(err)
	}
	if err := inventory.Close(); err != nil {
		t.Fatal(err)
	}
	if err := inventory.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(context.Background(), file, int64(len(data))); err == nil {
		t.Fatal("accepted closed file")
	}
}

type failingReader struct{}

func (failingReader) ReadAt([]byte, int64) (int, error) { return 0, io.ErrUnexpectedEOF }
