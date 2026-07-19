package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOperationLockSerializesOneDeploymentDirectory(t *testing.T) {
	dir := t.TempDir()
	first, err := AcquireOperationLock(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Unlock() })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	second, err := AcquireOperationLock(ctx, dir)
	if !errors.Is(err, context.DeadlineExceeded) || second != nil {
		t.Fatalf("contended lock = %#v, %v", second, err)
	}
	if err := first.Unlock(); err != nil {
		t.Fatal(err)
	}

	second, err = AcquireOperationLock(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestOperationLockFileExistenceDoesNotClaimOwnership(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, filepath.FromSlash(operationLockRelativePath))
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte("not ownership state\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	lock, err := AcquireOperationLock(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if lock.Path() != lockPath {
		t.Fatalf("lock path = %q, want %q", lock.Path(), lockPath)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("persistent lock file: %v", err)
	}
}

func TestOperationLockHonorsCancellationBeforeFilesystemMutation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "deployment")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if lock, err := AcquireOperationLock(ctx, dir); !errors.Is(err, context.Canceled) || lock != nil {
		t.Fatalf("cancelled lock = %#v, %v", lock, err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled acquisition created deployment state: %v", err)
	}
}

func TestOperationLockRequiresExistingRealDeploymentDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	if lock, err := AcquireOperationLock(context.Background(), dir); err == nil || lock != nil {
		t.Fatalf("missing-directory lock = %#v, %v", lock, err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock created missing deployment directory: %v", err)
	}

	realState := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(realState, 0o755); err != nil {
		t.Fatal(err)
	}
	deployment := t.TempDir()
	if err := os.Symlink(realState, filepath.Join(deployment, ".reploy")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if lock, err := AcquireOperationLock(context.Background(), deployment); err == nil || lock != nil {
		t.Fatalf("symlink-state lock = %#v, %v", lock, err)
	}
}
