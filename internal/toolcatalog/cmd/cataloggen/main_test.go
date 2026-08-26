package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/omry/reploy/internal/toolcatalog"
)

func TestReadEntriesHasNoAggregateByteCeiling(t *testing.T) {
	entries := make([]toolcatalog.PortableToolAuthoringEntryV1, 20_000)
	for index := range entries {
		entries[index] = toolcatalog.PortableToolAuthoringEntryV1{
			SourcePath: fmt.Sprintf("java/releases/21/records/%05d.yaml", index),
			OutputPath: fmt.Sprintf("java/releases/21/records/%05d.json", index),
		}
	}
	filename := filepath.Join(t.TempDir(), "entries.json")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(file).Encode(entries); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= 1<<20 {
		t.Fatalf("test manifest size = %d, want more than 1 MiB", info.Size())
	}
	loaded, err := readEntries(filename)
	if err != nil {
		t.Fatalf("read large entries manifest: %v", err)
	}
	if len(loaded) != len(entries) {
		t.Fatalf("loaded %d entries, want %d", len(loaded), len(entries))
	}
}

func TestReadEntriesRejectsTrailingContent(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "entries.json")
	if err := os.WriteFile(filename, []byte("[]\n{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readEntries(filename); err == nil {
		t.Fatal("readEntries accepted trailing content")
	}
}
