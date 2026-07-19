package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOperationLockPublishesAndLoadsImmutableBuildLock(t *testing.T) {
	dir := t.TempDir()
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	record := validBuildLock(t)
	digest, err := operation.PublishBuildLock(record, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".reploy", buildLockDirectoryName, buildLockFilename(digest))
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("build lock mode = %v", info.Mode())
	}
	loaded, found, err := operation.ReadBuildLock(digest, acceptBuildLockProfile)
	if err != nil || !found {
		t.Fatalf("found = %v, error = %v", found, err)
	}
	loadedDigest, err := BuildLockDigestV1(loaded, acceptBuildLockProfile)
	if err != nil || loadedDigest != digest {
		t.Fatalf("loaded digest = %s, want %s; error = %v", loadedDigest, digest, err)
	}
	if again, err := operation.PublishBuildLock(record, acceptBuildLockProfile); err != nil || again != digest {
		t.Fatalf("idempotent publish = %s, error = %v", again, err)
	}
}

func TestOperationLockRejectsCorruptBuildLockAndPreservesIt(t *testing.T) {
	dir := t.TempDir()
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	record := validBuildLock(t)
	digest, err := operation.PublishBuildLock(record, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".reploy", buildLockDirectoryName, buildLockFilename(digest))
	if err := os.WriteFile(path, []byte(`{"schema":"corrupt"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := operation.ReadBuildLock(digest, acceptBuildLockProfile); err == nil {
		t.Fatal("corrupt build lock loaded")
	}
	if err := operation.RemoveBuildLock(digest, acceptBuildLockProfile); err == nil {
		t.Fatal("corrupt build lock was removed")
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("corrupt build lock was not preserved: %v", err)
	}
}

func TestOperationLockRemovesOnlyVerifiedBuildLock(t *testing.T) {
	dir := t.TempDir()
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	digest, err := operation.PublishBuildLock(validBuildLock(t), acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.RemoveBuildLock(digest, acceptBuildLockProfile); err != nil {
		t.Fatal(err)
	}
	if _, found, err := operation.ReadBuildLock(digest, acceptBuildLockProfile); err != nil || found {
		t.Fatalf("found = %v, error = %v", found, err)
	}
	if err := operation.RemoveBuildLock(digest, acceptBuildLockProfile); err != nil {
		t.Fatalf("absent removal: %v", err)
	}
}

func TestOperationLockRejectsSymlinkBuildLockDirectory(t *testing.T) {
	dir := t.TempDir()
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	target := t.TempDir()
	path := filepath.Join(dir, ".reploy", buildLockDirectoryName)
	if err := os.Symlink(target, path); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "privilege") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	if _, err := operation.PublishBuildLock(validBuildLock(t), acceptBuildLockProfile); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestOperationLockRetainsOnlySelectedBuildLock(t *testing.T) {
	dir := t.TempDir()
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	current := validBuildLock(t)
	currentDigest, err := operation.PublishBuildLock(current, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	old := current
	old.BlueprintDigest = buildLockTestDigest("f")
	oldDigest, err := operation.PublishBuildLock(old, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.RemoveOtherBuildLocks(currentDigest, acceptBuildLockProfile); err != nil {
		t.Fatal(err)
	}
	if _, found, err := operation.ReadBuildLock(currentDigest, acceptBuildLockProfile); err != nil || !found {
		t.Fatalf("current found = %v, error = %v", found, err)
	}
	if _, found, err := operation.ReadBuildLock(oldDigest, acceptBuildLockProfile); err != nil || found {
		t.Fatalf("old found = %v, error = %v", found, err)
	}
}

func TestOperationLockValidatesAllBuildLocksBeforeRetentionCleanup(t *testing.T) {
	dir := t.TempDir()
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	current := validBuildLock(t)
	currentDigest, err := operation.PublishBuildLock(current, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	old := current
	old.BlueprintDigest = buildLockTestDigest("f")
	oldDigest, err := operation.PublishBuildLock(old, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(dir, ".reploy", buildLockDirectoryName, buildLockFilename(oldDigest))
	if err := os.WriteFile(oldPath, []byte(`{"schema":"corrupt"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := operation.RemoveOtherBuildLocks(currentDigest, acceptBuildLockProfile); err == nil {
		t.Fatal("cleanup accepted corrupt lock")
	}
	currentPath := filepath.Join(dir, ".reploy", buildLockDirectoryName, buildLockFilename(currentDigest))
	if _, err := os.Lstat(currentPath); err != nil {
		t.Fatalf("cleanup changed directory before full validation: %v", err)
	}
	if _, err := os.Lstat(oldPath); err != nil {
		t.Fatalf("cleanup removed corrupt lock: %v", err)
	}
}

func TestOperationLockRejectsUnknownBuildLockDirectoryEntry(t *testing.T) {
	dir := t.TempDir()
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	currentDigest, err := operation.PublishBuildLock(validBuildLock(t), acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(dir, ".reploy", buildLockDirectoryName, "notes.txt")
	if err := os.WriteFile(unknown, []byte("unknown"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := operation.RemoveOtherBuildLocks(currentDigest, acceptBuildLockProfile); err == nil || !strings.Contains(err.Error(), "unrecognized") {
		t.Fatalf("unknown entry error = %v", err)
	}
}

func TestOperationLockRemovesOwnedBuildLockTemporary(t *testing.T) {
	dir := t.TempDir()
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	currentDigest, err := operation.PublishBuildLock(validBuildLock(t), acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(dir, ".reploy", buildLockDirectoryName, ".lock-ABC123.tmp")
	if err := os.WriteFile(temporary, []byte("interrupted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := operation.RemoveOtherBuildLocks(currentDigest, acceptBuildLockProfile); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(temporary); !os.IsNotExist(err) {
		t.Fatalf("temporary build lock remains: %v", err)
	}
}

func TestOperationLockRemovesAllVerifiedBuildLocksAndTemporaries(t *testing.T) {
	dir := t.TempDir()
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	digest, err := operation.PublishBuildLock(validBuildLock(t), acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(dir, ".reploy", buildLockDirectoryName, ".lock-ABC123.tmp")
	if err := os.WriteFile(temporary, []byte("interrupted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := operation.RemoveAllBuildLocks(acceptBuildLockProfile); err != nil {
		t.Fatal(err)
	}
	if _, found, err := operation.ReadBuildLock(digest, acceptBuildLockProfile); err != nil || found {
		t.Fatalf("build lock found = %v, error = %v", found, err)
	}
	if _, err := os.Lstat(temporary); !os.IsNotExist(err) {
		t.Fatalf("temporary build lock remains: %v", err)
	}
}

func TestOperationLockValidatesAllBuildLocksBeforeRemovingAll(t *testing.T) {
	dir := t.TempDir()
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	digest, err := operation.PublishBuildLock(validBuildLock(t), acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".reploy", buildLockDirectoryName, buildLockFilename(digest))
	unknown := filepath.Join(dir, ".reploy", buildLockDirectoryName, "notes.txt")
	if err := os.WriteFile(unknown, []byte("unknown"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := operation.RemoveAllBuildLocks(acceptBuildLockProfile); err == nil {
		t.Fatal("cleanup accepted an unknown build-lock entry")
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("cleanup changed directory before full validation: %v", err)
	}
}
