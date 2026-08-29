//go:build windows

package providerstore

import (
	"io/fs"
	"testing"
)

func assertArchiveMaterializedRegularMode(t *testing.T, path string, executable bool) {
	t.Helper()
	if mode := fileMode(t, path); mode != fs.FileMode(0o444) {
		t.Fatalf("regular file mode = %o, want read-only 0444 representation", mode)
	}
}

func assertArchiveMaterializedDirectoryMode(t *testing.T, path string) {
	t.Helper()
	if mode := fileMode(t, path); mode != fs.FileMode(0o555) {
		t.Fatalf("directory mode = %o, want 0555", mode)
	}
}
