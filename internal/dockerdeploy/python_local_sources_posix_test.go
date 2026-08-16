//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestObservePythonSourceManifestExcludesFIFOWithoutInspectingIt(t *testing.T) {
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "pyproject.toml"), []byte("[build-system]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	generated := filepath.Join(sourceDir, "recordings", ".omegaflow", "run")
	if err := os.MkdirAll(generated, 0o755); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(generated, "input.pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}

	manifest, _, err := ObservePythonSourceManifestWithExclusions(
		sourceDir, []string{"recordings/.omegaflow"},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range manifest.Entries {
		if strings.HasPrefix(entry.Path, "recordings/.omegaflow") {
			t.Fatalf("excluded FIFO subtree entered source manifest: %#v", entry)
		}
	}
	if _, _, err := ObservePythonSourceManifest(sourceDir); err == nil ||
		!strings.Contains(err.Error(), "unsupported file type") {
		t.Fatalf("selected FIFO error = %v", err)
	}
}
