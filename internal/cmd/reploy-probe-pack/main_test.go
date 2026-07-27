package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/omry/reploy/internal/probearchive"
)

func TestRunPacksExactProbeMatrix(t *testing.T) {
	dir := t.TempDir()
	executable := writeFile(t, dir, "reploy", "prefix")
	amd64 := writeFile(t, dir, "amd64", "amd64")
	armv7 := writeFile(t, dir, "arm-v7", "arm-v7")
	arm64 := writeFile(t, dir, "arm64", "arm64")
	var stderr bytes.Buffer
	code := run([]string{
		"--executable", executable,
		"--linux-amd64", amd64,
		"--linux-arm-v7", armv7,
		"--linux-arm64", arm64,
	}, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code = %d; stderr = %s", code, stderr.String())
	}
	if _, err := probearchive.Verify(executable); err != nil {
		t.Fatal(err)
	}
}

func TestRunRejectsIncompleteArguments(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"--executable", "reploy"}, &stderr); code != 2 || stderr.Len() == 0 {
		t.Fatalf("code = %d; stderr = %s", code, stderr.String())
	}
}

func writeFile(t *testing.T, dir string, name string, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
