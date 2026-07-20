package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOperationLockCommitsStateV1AgainstObservedGeneration(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Unlock() })
	state := StateV1{
		Schema: StateSchemaV1, Blueprint: stateV1TestBlueprint(t), Platform: stateV1TestPlatform(t),
		Overlay: EmptyRequestOverlayV1(),
	}
	if err := lock.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := lock.ReadStateV1()
	if err != nil || !found || loaded.Schema != StateSchemaV1 {
		t.Fatalf("loaded state = %#v, found = %v, err = %v", loaded, found, err)
	}
	wrong := &EnvironmentGenerationState{}
	if err := lock.CommitStateV1(wrong, state); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale state commit error = %v", err)
	}
}

func TestOperationLockRejectsLegacyAndSymlinkState(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".reploy")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(stateDir, stateFilenameV1)
	if err := os.WriteFile(statePath, []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := AcquireOperationLock(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := lock.ReadStateV1(); !errors.Is(err, ErrLegacyStateUnsupported) {
		t.Fatalf("legacy state read error = %v", err)
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", statePath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := lock.ReadStateV1(); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink state read error = %v", err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
}
