package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/omry/reploy/internal/toolcatalog"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve toolcatalog directory: %w", err)
	}
	if filepath.Base(root) != "toolcatalog" {
		return fmt.Errorf("catalog generator must run from the toolcatalog package directory")
	}
	authoringRoot := filepath.Join(root, "authoring")
	entries, err := readEntries(filepath.Join(authoringRoot, "entries.json"))
	if err != nil {
		return err
	}
	result, err := toolcatalog.LoadPortableToolAuthoringV1(authoringRoot, entries)
	if err != nil {
		return fmt.Errorf("generate portable tool catalog: %w", err)
	}

	staging, err := os.MkdirTemp(root, ".definitions-")
	if err != nil {
		return fmt.Errorf("create generated catalog staging directory: %w", err)
	}
	defer os.RemoveAll(staging)
	for _, record := range result.Records {
		filename := filepath.Join(staging, filepath.FromSlash(record.Path))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			return fmt.Errorf("create generated catalog directory: %w", err)
		}
		if err := os.WriteFile(filename, record.CanonicalBytes, 0o644); err != nil {
			return fmt.Errorf("write generated catalog record %q: %w", record.Path, err)
		}
	}

	output := filepath.Join(root, "definitions")
	if err := os.RemoveAll(output); err != nil {
		return fmt.Errorf("replace generated catalog: %w", err)
	}
	if err := os.Rename(staging, output); err != nil {
		return fmt.Errorf("install generated catalog: %w", err)
	}
	return nil
}

func readEntries(filename string) ([]toolcatalog.PortableToolAuthoringEntryV1, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("open authoring entries: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var entries []toolcatalog.PortableToolAuthoringEntryV1
	if err := decoder.Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode authoring entries: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decode authoring entries: trailing content")
	}
	return entries, nil
}
