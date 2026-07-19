package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const operationLockRelativePath = ".reploy/operation.lock"

const operationLockPollInterval = 10 * time.Millisecond

// OperationLock holds the kernel advisory lock for one deployment directory.
// The lock-file path is stable, but file existence never indicates ownership.
type OperationLock struct {
	file     *os.File
	path     string
	mutex    sync.Mutex
	released bool
}

// AcquireOperationLock acquires the deployment's exclusive advisory operation
// lock and keeps its descriptor open until Unlock. Waiting is bounded only by
// caller cancellation.
func AcquireOperationLock(ctx context.Context, deploymentDir string) (*OperationLock, error) {
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
	lockPath, err := prepareOperationLockPath(absoluteDir)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open operation lock: %w", err)
	}
	for {
		acquired, err := tryLockOperationFile(file)
		if err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("acquire operation lock: %w", err)
		}
		if acquired {
			return &OperationLock{file: file, path: lockPath}, nil
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

func prepareOperationLockPath(deploymentDir string) (string, error) {
	info, err := os.Lstat(deploymentDir)
	if err != nil {
		return "", fmt.Errorf("inspect deployment directory for operation lock: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("operation lock deployment path must be a real directory: %s", deploymentDir)
	}
	stateDir := filepath.Join(deploymentDir, ".reploy")
	info, err = os.Lstat(stateDir)
	if os.IsNotExist(err) {
		if mkdirErr := os.Mkdir(stateDir, 0o755); mkdirErr != nil && !os.IsExist(mkdirErr) {
			return "", fmt.Errorf("create operation lock directory: %w", mkdirErr)
		}
		info, err = os.Lstat(stateDir)
	}
	if err != nil {
		return "", fmt.Errorf("inspect operation lock directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("operation lock state path must be a real directory: %s", stateDir)
	}
	lockPath := filepath.Join(stateDir, "operation.lock")
	info, err = os.Lstat(lockPath)
	if err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return "", fmt.Errorf("operation lock path must be a regular file: %s", lockPath)
	}
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect operation lock path: %w", err)
	}
	return lockPath, nil
}

// Path returns the stable lock-file path for diagnostics and tests. It does
// not reveal whether another process owns the advisory lock.
func (lock *OperationLock) Path() string {
	if lock == nil {
		return ""
	}
	return lock.path
}

// Unlock releases the kernel lock and closes its descriptor. Repeated calls
// are harmless so cleanup paths can defer it safely.
func (lock *OperationLock) Unlock() error {
	if lock == nil {
		return nil
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
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
