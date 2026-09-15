package wheelinventory

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"io/fs"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/providerstore"
)

func TestReadInventoriesWheelWithoutOpeningMembers(t *testing.T) {
	data := makeArchive(t, []archiveEntry{
		{name: "pkg-1.dist-info/", directory: true},
		{name: "pkg-1.dist-info/METADATA", content: "Name: pkg\n"},
	})
	inventory, err := Read(context.Background(), bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Files) != 2 || inventory.Entries[1].Path != "pkg-1.dist-info/METADATA" {
		t.Fatalf("inventory = %#v", inventory.Entries)
	}
	if got, err := inventory.Files[1].Open(); err != nil {
		t.Fatal(err)
	} else {
		content, readErr := io.ReadAll(got)
		_ = got.Close()
		if readErr != nil || string(content) != "Name: pkg\n" {
			t.Fatalf("member content = %q, err = %v", content, readErr)
		}
	}
}

func TestReadRejectsUnsafeStructure(t *testing.T) {
	tests := []struct {
		name    string
		entries []archiveEntry
		want    string
	}{
		{name: "escaping", entries: []archiveEntry{{name: "../outside", content: "x"}}, want: "invalid component"},
		{name: "absolute", entries: []archiveEntry{{name: "/outside", content: "x"}}, want: "relative UTF-8"},
		{name: "empty path", entries: []archiveEntry{{name: "", content: "x"}}, want: "path"},
		{name: "duplicate", entries: []archiveEntry{{name: "a", content: "x"}, {name: "a", content: "y"}}, want: "duplicate"},
		{name: "file prefix", entries: []archiveEntry{{name: "a", content: "x"}, {name: "a/b", content: "y"}}, want: "regular-file directory prefix"},
		{name: "reverse prefix", entries: []archiveEntry{{name: "a/b", content: "x"}, {name: "a", content: "y"}}, want: "directory prefix"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := makeArchive(t, test.entries)
			_, err := Read(context.Background(), bytes.NewReader(data), int64(len(data)))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestReadUsesSharedPreflightOnGenericReader(t *testing.T) {
	data := makeArchive(t, []archiveEntry{{name: "pkg.dist-info/METADATA", content: "x"}})
	if err := providerstore.PreflightZipCentralDirectory(context.Background(), bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(context.Background(), bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
}

type archiveEntry struct {
	name      string
	content   string
	directory bool
}

func makeArchive(t *testing.T, entries []archiveEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		if entry.directory {
			header.SetMode(0o755 | fs.ModeDir)
		}
		writer, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(entry.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
