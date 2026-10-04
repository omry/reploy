package deploy

import (
	"fmt"
	"path/filepath"
)

// IsolateOriginalDirectory moves this lock's original deployment directory to
// an unoccupied sibling path. The caller must first bind terminal removal and
// finish retiring every owner. This primitive does not retire or erase anything.
//
// On Unix the operation lock remains held until the caller unlocks it. Windows
// captures the original directory before releasing the child-file lock, then
// renames that captured object. A true result means the directory moved, even
// if closing a Windows handle subsequently failed; retry must use that location.
func (lock *OperationLock) IsolateOriginalDirectory(destination string) (bool, error) {
	if lock == nil {
		return false, fmt.Errorf("directory isolation requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	if len(lock.retirementState) == 0 {
		return false, fmt.Errorf("directory isolation requires explicitly bound terminal authority")
	}
	if err := lock.requireRetirementLocked(); err != nil {
		return false, err
	}
	if destination == "" {
		return false, fmt.Errorf("directory isolation requires a destination")
	}
	destination, err := filepath.Abs(destination)
	if err != nil {
		return false, err
	}
	source := filepath.Dir(filepath.Dir(lock.path))
	if destination == source || filepath.Dir(destination) != filepath.Dir(source) {
		return false, fmt.Errorf("directory isolation destination must be a different sibling path")
	}
	return isolateOriginalDirectoryLocked(lock, source, destination)
}
