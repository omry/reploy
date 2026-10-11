package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/omry/reploy/internal/fsowner"
)

const operationLockRelativePath = ".reploy/operation.lock"

const operationLockPollInterval = 10 * time.Millisecond

// OperationLock holds the kernel advisory lock for one deployment directory.
// The lock-file path is stable, but file existence never indicates ownership.
type OperationLock struct {
	file            *os.File
	path            string
	mutex           sync.Mutex
	released        bool
	directory       os.FileInfo
	stateDirectory  os.FileInfo
	retirementState []byte
}

// AcquireOperationLock acquires the deployment's exclusive advisory operation
// lock and keeps its descriptor open until Unlock. Waiting is bounded only by
// caller cancellation.
func AcquireOperationLock(ctx context.Context, deploymentDir string) (*OperationLock, error) {
	return acquireOperationLock(ctx, deploymentDir, true)
}

// AcquireExistingOperationLock acquires an already-initialized deployment's
// operation lock without creating the state directory or lock file. Read-only
// diagnostics use it so a missing or malformed deployment is never changed by
// inspection.
func AcquireExistingOperationLock(ctx context.Context, deploymentDir string) (*OperationLock, error) {
	return acquireOperationLock(ctx, deploymentDir, false)
}

func acquireOperationLock(ctx context.Context, deploymentDir string, create bool) (*OperationLock, error) {
	if ctx == nil {
		return nil, fmt.Errorf("operation lock requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	absoluteDir, err := filepath.Abs(deploymentDir)
	if err != nil {
		return nil, fmt.Errorf("resolve deployment directory for operation lock: %w", err)
	}
	stateRoot, directory, stateDirectory, err := prepareOperationLockRoot(absoluteDir, create)
	if err != nil {
		return nil, err
	}
	defer stateRoot.Close()
	lockPath := filepath.Join(absoluteDir, operationLockRelativePath)
	// Force identity capture now: Windows Lstat otherwise defers file IDs
	// until SameFile, after a waiting acquisition's directory may have moved.
	if !os.SameFile(directory, directory) || !os.SameFile(stateDirectory, stateDirectory) {
		return nil, fmt.Errorf("capture operation lock directory identity")
	}
	if create {
		guard, err := readTerminalStateV1(filepath.Join(filepath.Dir(lockPath), stateFilenameV1))
		if err != nil || len(guard) != 0 {
			return nil, fmt.Errorf("ordinary operation admission rejected terminal authority: %v", err)
		}
	}
	file, err := openOperationLockFile(stateRoot, create)
	if err != nil {
		return nil, fmt.Errorf("open operation lock: %w", err)
	}
	// Transfer a newly opened metadata file before waiting, so cancellation
	// cannot strand an operator-owned deployment with a root-only lock.
	owner := fsowner.FromInfo(stateDirectory)
	if create && owner.Enabled() {
		identity := &OperationLock{file: file, path: lockPath, directory: directory, stateDirectory: stateDirectory}
		if err := identity.requireHeldLocked(); err != nil {
			_ = file.Close()
			return nil, err
		}
		if err := owner.File(file); err != nil {
			_ = file.Close()
			return nil, err
		}
	}
	for {
		acquired, err := tryLockOperationFile(file)
		if err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("acquire operation lock: %w", err)
		}
		if acquired {
			lock := &OperationLock{file: file, path: lockPath, directory: directory, stateDirectory: stateDirectory}
			err := lock.RequireHeld()
			if err == nil && create {
				err = lock.RequireWritable()
			}
			if err != nil {
				_ = lock.Unlock()
				return nil, err
			}
			return lock, nil
		}
		timer := time.NewTimer(operationLockPollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func prepareOperationLockRoot(deploymentDir string, create bool) (*os.Root, os.FileInfo, os.FileInfo, error) {
	root, owner, err := fsowner.OpenDirectory(deploymentDir)
	if err != nil {
		return nil, nil, nil, err
	}
	defer root.Close()
	directory, err := operationDirectoryInfo(root)
	if err != nil {
		return nil, nil, nil, err
	}
	var stateRoot *os.Root
	if create {
		stateRoot, err = owner.EnsureDirectory(root, ".reploy", 0o755)
	} else {
		stateRoot, err = fsowner.OpenChildDirectory(root, ".reploy")
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("prepare operation lock directory: %w", err)
	}
	stateDirectory, err := operationDirectoryInfo(stateRoot)
	if err != nil {
		_ = stateRoot.Close()
		return nil, nil, nil, err
	}
	return stateRoot, directory, stateDirectory, nil
}

func operationDirectoryInfo(root *os.Root) (os.FileInfo, error) {
	file, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return file.Stat()
}

// Path returns the stable lock-file path for diagnostics and tests. It does
// not reveal whether another process owns the advisory lock.
func (lock *OperationLock) Path() string {
	if lock == nil {
		return ""
	}
	return lock.path
}

// RequireHeld fails when the operation lock has not been acquired or has
// already been released, or its original directory or lock file was replaced.
func (lock *OperationLock) RequireHeld() error {
	if lock == nil {
		return fmt.Errorf("operation lock is not held")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	return lock.requireHeldLocked()
}

// Unlock releases the kernel lock and closes its descriptor. Repeated calls
// are harmless so cleanup paths can defer it safely.
func (lock *OperationLock) Unlock() error {
	if lock == nil {
		return nil
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	return lock.unlockLocked()
}

func (lock *OperationLock) unlockLocked() error {
	if lock.released {
		return nil
	}
	lock.released = true
	unlockErr := unlockOperationFile(lock.file)
	closeErr := lock.file.Close()
	if unlockErr != nil {
		return fmt.Errorf("release operation lock: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close operation lock: %w", closeErr)
	}
	return nil
}
