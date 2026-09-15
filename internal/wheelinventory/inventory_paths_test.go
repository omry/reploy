package wheelinventory

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"io/fs"
	"strings"
	"testing"
)

func TestReadRejectsEntryKindsAndPathForms(t *testing.T) {
	tests := []struct {
		name  string
		entry archivePathEntry
		want  string
	}{
		{"encrypted", archivePathEntry{name: "file", flags: 1}, "encrypted"},
		{"symlink", archivePathEntry{name: "file", mode: fs.ModeSymlink | 0o777}, "unsupported special type"},
		{"device", archivePathEntry{name: "file", mode: fs.ModeDevice | 0o600}, "unsupported special type"},
		{"fifo", archivePathEntry{name: "file", mode: fs.ModeNamedPipe | 0o600}, "unsupported special type"},
		{"socket", archivePathEntry{name: "file", mode: fs.ModeSocket | 0o600}, "unsupported special type"},
		{"special trailing slash", archivePathEntry{name: "file/", mode: fs.ModeSymlink | 0o777, rawMode: true}, "unsupported special type"},
		{"regular trailing slash", archivePathEntry{name: "file/", rawRegularSlash: true}, "regular-file"},
		{"empty normalized", archivePathEntry{name: "."}, "path"},
		{"empty interior", archivePathEntry{name: "a//b"}, "invalid component"},
		{"dot interior", archivePathEntry{name: "a/./b"}, "invalid component"},
		{"dotdot interior", archivePathEntry{name: "a/../b"}, "invalid component"},
		{"backslash", archivePathEntry{name: `a\\b`}, "relative UTF-8"},
		{"windows absolute", archivePathEntry{name: `C:\\file`}, "relative UTF-8"},
		{"non UTF8", archivePathEntry{name: "café", nonUTF8: true}, "not UTF-8"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := makePathArchive(t, []archivePathEntry{test.entry})
			_, err := Read(context.Background(), bytes.NewReader(data), int64(len(data)))
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestReadRejectsPortableConflictsInBothOrders(t *testing.T) {
	for _, entries := range [][]archivePathEntry{
		{{name: "a"}, {name: "a/b"}},
		{{name: "a/b"}, {name: "a"}},
		{{name: "a/b/", directory: true}, {name: "a"}},
		{{name: "A"}, {name: "a/b"}},
	} {
		data := makePathArchive(t, entries)
		if _, err := Read(context.Background(), bytes.NewReader(data), int64(len(data))); err == nil {
			t.Fatalf("entries %#v unexpectedly accepted", entries)
		}
	}
}

func TestReadPreservesNormalizedPathsAndDirectoryKinds(t *testing.T) {
	data := makePathArchive(t, []archivePathEntry{
		{name: "./file"},
		{name: "dir/", directory: true},
		{name: "other", directory: true},
	})
	inventory, err := Read(context.Background(), bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Entries[0].Path != "file" || inventory.Entries[0].Kind != Regular {
		t.Fatalf("normalized file = %#v", inventory.Entries[0])
	}
	if inventory.Entries[1].Path != "dir" || inventory.Entries[1].Kind != Directory || inventory.Entries[2].Kind != Directory {
		t.Fatalf("directory entries = %#v", inventory.Entries)
	}
}

func TestReadRejectsPortableDirectoryPrefixAliases(t *testing.T) {
	for _, pair := range [][2]string{{"Bin", "bin"}, {"Σ", "ς"}, {"café", "cafe\u0301"}} {
		for _, explicit := range []bool{false, true} {
			entries := []archivePathEntry{{name: pair[0] + "/a"}, {name: pair[1] + "/b"}}
			if explicit {
				entries[0] = archivePathEntry{name: pair[0] + "/", directory: true}
			}
			for order := 0; order < 2; order++ {
				data := makePathArchive(t, entries)
				if _, err := Read(context.Background(), bytes.NewReader(data), int64(len(data))); err == nil || !strings.Contains(err.Error(), "aliases") {
					t.Fatalf("entries %#v: error = %v, want portable alias rejection", entries, err)
				}
				entries[0], entries[1] = entries[1], entries[0]
			}
		}
	}
}

func TestReadAcceptsRepeatedExactDirectoryPrefixes(t *testing.T) {
	for _, entries := range [][]archivePathEntry{
		{{name: "Bin/a"}, {name: "Bin/b"}, {name: "Bin/", directory: true}},
		{{name: "Bin/", directory: true}, {name: "Bin/a"}, {name: "Bin/b"}},
	} {
		data := makePathArchive(t, entries)
		if _, err := Read(context.Background(), bytes.NewReader(data), int64(len(data))); err != nil {
			t.Fatalf("entries %#v: %v", entries, err)
		}
	}
}

type archivePathEntry struct {
	name            string
	mode            fs.FileMode
	flags           uint16
	directory       bool
	rawRegularSlash bool
	rawMode         bool
	nonUTF8         bool
}

func makePathArchive(t *testing.T, entries []archivePathEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate, Flags: entry.flags}
		if entry.directory {
			header.SetMode(0o755 | fs.ModeDir)
		} else if entry.mode != 0 {
			header.SetMode(entry.mode)
		}
		header.NonUTF8 = entry.nonUTF8
		writer, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if !entry.directory && !strings.HasSuffix(entry.name, "/") {
			if _, err := writer.Write([]byte("x")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	data := output.Bytes()
	// archive/zip treats every slash-terminated name as a directory while
	// writing. Rewrite the central-directory mode for the explicit malformed
	// regular-file-with-slash case so the reader sees the hostile kind.
	for _, entry := range entries {
		if !entry.rawRegularSlash && !entry.rawMode {
			continue
		}
		for offset := 0; offset+46 <= len(data); offset++ {
			if data[offset] == 'P' && data[offset+1] == 'K' && data[offset+2] == 1 && data[offset+3] == 2 {
				nameLen := int(data[offset+28]) | int(data[offset+29])<<8
				nameStart := offset + 46
				if nameStart+nameLen <= len(data) && string(data[nameStart:nameStart+nameLen]) == entry.name {
					modeValue := entry.mode
					if entry.rawRegularSlash {
						modeValue = 0o644
					}
					var header zip.FileHeader
					header.SetMode(modeValue)
					binary.LittleEndian.PutUint16(data[offset+4:], header.CreatorVersion)
					binary.LittleEndian.PutUint32(data[offset+38:], header.ExternalAttrs)
				}
			}
		}
	}
	return data
}
