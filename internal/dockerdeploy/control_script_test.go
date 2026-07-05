package dockerdeploy

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFakeEmbeddedReploy(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, embeddedRuntimeFileName())
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `#!/bin/sh
if [ -n "${REPLOY_ARGS_FILE:-}" ]; then
  printf '%s\n' "$@" > "$REPLOY_ARGS_FILE"
fi
if [ -n "${REPLOY_FAKE_OUTPUT:-}" ]; then
  printf '%s' "$REPLOY_FAKE_OUTPUT"
fi
if [ -n "${REPLOY_FAKE_EXIT:-}" ]; then
  exit "$REPLOY_FAKE_EXIT"
fi
`
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
