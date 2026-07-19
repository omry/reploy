package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOperationLockPendingBuildFileLifecycle(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()

	if _, found, err := lock.ReadPendingBuild(); err != nil || found {
		t.Fatalf("found = %v, error = %v", found, err)
	}
	record := validPendingBuild(t)
	if err := lock.WritePendingBuild(record); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".reploy", pendingBuildFilename)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("pending build mode = %v", info.Mode())
	}
	loaded, found, err := lock.ReadPendingBuild()
	if err != nil {
		t.Fatal(err)
	}
	if !found || !reflect.DeepEqual(loaded, record) {
		t.Fatalf("loaded = %#v, found = %v", loaded, found)
	}
	for _, phase := range []string{
		PendingBuildPhaseGenerationCreated, PendingBuildPhaseLockPublished,
		PendingBuildPhaseStateCommitted, PendingBuildPhaseCleanup,
	} {
		if err := lock.AdvancePendingBuildPhase(phase); err != nil {
			t.Fatal(err)
		}
	}
	if err := lock.AdvancePendingBuildPhase(PendingBuildPhaseCleanup); err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("error = %v", err)
	}
	if err := lock.RemovePendingBuild(); err != nil {
		t.Fatal(err)
	}
	if err := lock.RemovePendingBuild(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("pending build remains: %v", err)
	}
}

func TestOperationLockPendingBuildReplaceFailurePreservesRecord(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	record := validPendingBuild(t)
	advanced := record
	advanced.Phase = PendingBuildPhaseGenerationCreated
	if err := lock.WritePendingBuild(advanced); err == nil || !strings.Contains(err.Error(), "new pending build phase") {
		t.Fatalf("error = %v", err)
	}
	if err := lock.WritePendingBuild(record); err != nil {
		t.Fatal(err)
	}
	originalReplace := replaceAtomicStateFile
	replaceAtomicStateFile = func(string, string) error { return errors.New("injected replace failure") }
	t.Cleanup(func() { replaceAtomicStateFile = originalReplace })
	if err := lock.AdvancePendingBuildPhase(PendingBuildPhaseGenerationCreated); err == nil || !strings.Contains(err.Error(), "injected replace failure") {
		t.Fatalf("error = %v", err)
	}
	loaded, found, err := lock.ReadPendingBuild()
	if err != nil {
		t.Fatal(err)
	}
	if !found || !reflect.DeepEqual(loaded, record) {
		t.Fatalf("failed replace changed record: %#v", loaded)
	}
}

func TestOperationLockPendingBuildRejectsReplacementAndSkippedPhase(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	record := validPendingBuild(t)
	if err := lock.WritePendingBuild(record); err != nil {
		t.Fatal(err)
	}
	if err := lock.WritePendingBuild(record); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v", err)
	}
	if err := lock.AdvancePendingBuildPhase(PendingBuildPhaseLockPublished); err == nil || !strings.Contains(err.Error(), "must advance") {
		t.Fatalf("error = %v", err)
	}
	loaded, found, err := lock.ReadPendingBuild()
	if err != nil || !found || loaded.Phase != PendingBuildPhaseValidated {
		t.Fatalf("loaded = %#v, found = %v, error = %v", loaded, found, err)
	}
}

func TestOperationLockPendingBuildRejectsSymlinkAndUseAfterUnlock(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".reploy", pendingBuildFilename)
	if err := os.Symlink(filepath.Join(dir, "outside"), path); err != nil {
		t.Fatal(err)
	}
	if err := lock.WritePendingBuild(validPendingBuild(t)); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("error = %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := lock.ReadPendingBuild(); err == nil || !strings.Contains(err.Error(), "not held") {
		t.Fatalf("error = %v", err)
	}
}
