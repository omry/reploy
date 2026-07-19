package deploy

import (
	"fmt"
	"os"
	"path/filepath"
)

const pendingBuildFilename = "pending-build.json"

func (lock *OperationLock) WritePendingBuild(record PendingBuildV1) error {
	if lock == nil {
		return fmt.Errorf("write pending build requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.pendingBuildPathLocked()
	if err != nil {
		return err
	}
	content, err := EncodePendingBuild(record)
	if err != nil {
		return err
	}
	if record.Phase != PendingBuildPhaseValidated {
		return fmt.Errorf("new pending build phase must be %q", PendingBuildPhaseValidated)
	}
	if err := requireAbsentPendingBuild(path); err != nil {
		return err
	}
	if err := writeAtomicStateFile(path, content, 0o600); err != nil {
		return fmt.Errorf("write pending build: %w", err)
	}
	return nil
}

func (lock *OperationLock) AdvancePendingBuildPhase(next string) error {
	if lock == nil {
		return fmt.Errorf("advance pending build requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.pendingBuildPathLocked()
	if err != nil {
		return err
	}
	record, found, err := readPendingBuildPath(path)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("cannot advance missing pending build")
	}
	want, ok := nextPendingBuildPhase(record.Phase)
	if !ok {
		return fmt.Errorf("pending build phase %q is terminal", record.Phase)
	}
	if next != want {
		return fmt.Errorf("pending build phase %q must advance to %q, not %q", record.Phase, want, next)
	}
	record.Phase = next
	content, err := EncodePendingBuild(record)
	if err != nil {
		return err
	}
	if err := writeAtomicStateFile(path, content, 0o600); err != nil {
		return fmt.Errorf("advance pending build phase: %w", err)
	}
	return nil
}

func (lock *OperationLock) ReadPendingBuild() (PendingBuildV1, bool, error) {
	if lock == nil {
		return PendingBuildV1{}, false, fmt.Errorf("read pending build requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.pendingBuildPathLocked()
	if err != nil {
		return PendingBuildV1{}, false, err
	}
	return readPendingBuildPath(path)
}

func (lock *OperationLock) RemovePendingBuild() error {
	if lock == nil {
		return fmt.Errorf("remove pending build requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.pendingBuildPathLocked()
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect pending build: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("pending build path must be a regular file: %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove pending build: %w", err)
	}
	if err := syncAtomicStateDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync pending build directory: %w", err)
	}
	return nil
}

func (lock *OperationLock) pendingBuildPathLocked() (string, error) {
	if lock.released || lock.file == nil || lock.path == "" {
		return "", fmt.Errorf("operation lock is not held")
	}
	return filepath.Join(filepath.Dir(lock.path), pendingBuildFilename), nil
}

func requireAbsentPendingBuild(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect pending build: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("pending build path must be a regular file: %s", path)
	}
	return fmt.Errorf("pending build already exists")
}

func readPendingBuildPath(path string) (PendingBuildV1, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return PendingBuildV1{}, false, nil
	}
	if err != nil {
		return PendingBuildV1{}, false, fmt.Errorf("inspect pending build: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return PendingBuildV1{}, false, fmt.Errorf("pending build path must be a regular file: %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return PendingBuildV1{}, false, fmt.Errorf("read pending build: %w", err)
	}
	record, err := DecodePendingBuild(content)
	if err != nil {
		return PendingBuildV1{}, false, err
	}
	return record, true, nil
}

func nextPendingBuildPhase(current string) (string, bool) {
	switch current {
	case PendingBuildPhaseValidated:
		return PendingBuildPhaseGenerationCreated, true
	case PendingBuildPhaseGenerationCreated:
		return PendingBuildPhaseLockPublished, true
	case PendingBuildPhaseLockPublished:
		return PendingBuildPhaseStateCommitted, true
	case PendingBuildPhaseStateCommitted:
		return PendingBuildPhaseCleanup, true
	default:
		return "", false
	}
}
