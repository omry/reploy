//go:build !windows

package providerstore

import (
	"io/fs"
	"testing"
)

func assertArchiveMaterializedRegularMode(t *testing.T, path string, executable bool) {
	t.Helper()
	want := fs.FileMode(0o444)
	if executable {
		want = 0o555
	}
	if mode := fileMode(t, path); mode != want {
		t.Fatalf("regular file mode = %o, want %o", mode, want)
	}
}

func assertArchiveMaterializedDirectoryMode(t *testing.T, path string) {
	t.Helper()
	if mode := fileMode(t, path); mode != 0o555 {
		t.Fatalf("directory mode = %o, want 555", mode)
	}
}
