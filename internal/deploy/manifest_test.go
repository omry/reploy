package deploy

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteAndLoadDeploymentManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".reploy.json")
	manifest := NewDeploymentManifest("test")
	manifest.Files["compose.yaml"] = GeneratedFile{Kind: "template", SHA256: HashBytes([]byte("compose"))}

	if err := WriteDeploymentManifest(path, manifest); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDeploymentManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SchemaVersion != 1 || loaded.Generator != "test" {
		t.Fatalf("unexpected manifest: %#v", loaded)
	}
	if loaded.Files["compose.yaml"].SHA256 != HashBytes([]byte("compose")) {
		t.Fatalf("unexpected file hash: %#v", loaded.Files["compose.yaml"])
	}
}

func TestWriteGeneratedFileRecordsManifestHash(t *testing.T) {
	dir := t.TempDir()
	manifest := NewDeploymentManifest("test")

	if err := WriteGeneratedFile(dir, "bin/tool", []byte("hello\n"), true, &manifest); err != nil {
		t.Fatal(err)
	}
	if got := manifest.Files["bin/tool"].SHA256; got != HashBytes([]byte("hello\n")) {
		t.Fatalf("hash = %q", got)
	}
	info, err := os.Stat(filepath.Join(dir, "bin", "tool"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("expected executable bit on %s", info.Mode())
	}
}

func TestUpdateGeneratedFileSkipsLocalEdits(t *testing.T) {
	dir := t.TempDir()
	manifest := NewDeploymentManifest("test")
	if err := WriteGeneratedFile(dir, "compose.yaml", []byte("old\n"), false, &manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	status, err := UpdateGeneratedFile(dir, "compose.yaml", []byte("new\n"), false, &manifest, false)
	if err != nil {
		t.Fatal(err)
	}
	if status != UpdateStatusSkipped {
		t.Fatalf("status = %q", status)
	}
	content, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "local edit\n" {
		t.Fatalf("content = %q", content)
	}
}

func TestUpdateGeneratedFileForceOverwritesLocalEdits(t *testing.T) {
	dir := t.TempDir()
	manifest := NewDeploymentManifest("test")
	if err := WriteGeneratedFile(dir, "compose.yaml", []byte("old\n"), false, &manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	status, err := UpdateGeneratedFile(dir, "compose.yaml", []byte("new\n"), false, &manifest, true)
	if err != nil {
		t.Fatal(err)
	}
	if status != UpdateStatusUpdated {
		t.Fatalf("status = %q", status)
	}
	content, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new\n" {
		t.Fatalf("content = %q", content)
	}
}
