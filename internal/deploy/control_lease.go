package deploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const controlLeaseSuffixV1 = ".lease"

// ControlLeaseV1 proves that the process which queued a lifecycle operation is
// still alive. The kernel releases the advisory lock if that process exits,
// even when it cannot run normal cleanup.
type ControlLeaseV1 struct {
	file     *os.File
	path     string
	mutex    sync.Mutex
	released bool
}

// AcquireControlLeaseV1 creates and holds the ownership lease for a control
// marker. The deployment operation lock prevents marker/lease publication from
// racing queue recovery.
func (lock *OperationLock) AcquireControlLeaseV1(id string) (*ControlLeaseV1, error) {
	if lock == nil {
		return nil, fmt.Errorf("acquire control lease requires an operation lock")
	}
	if err := ValidateControlMarkerIDV1(id); err != nil {
		return nil, err
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	directory, err := lock.controlLeaseDirectoryLockedV1()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, id+controlLeaseSuffixV1)
	if err := requireControlLeasePathV1(path, true); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open control lease: %w", err)
	}
	acquired, err := tryLockOperationFile(file)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("acquire control lease: %w", err)
	}
	if !acquired {
		_ = file.Close()
		return nil, fmt.Errorf("control lease %q is already owned", id)
	}
	return &ControlLeaseV1{file: file, path: path}, nil
}

// Release drops the ownership lease and removes its deployment-local file.
// Repeated calls are harmless so error cleanup can use it freely.
func (lease *ControlLeaseV1) Release() error {
	if lease == nil {
		return nil
	}
	lease.mutex.Lock()
	defer lease.mutex.Unlock()
	if lease.released {
		return nil
	}
	lease.released = true
	unlockErr := unlockOperationFile(lease.file)
	closeErr := lease.file.Close()
	removeErr := removeControlLeasePathV1(lease.path)
	return errors.Join(
		wrapControlLeaseErrorV1("release control lease", unlockErr),
		wrapControlLeaseErrorV1("close control lease", closeErr),
		removeErr,
	)
}

func (lock *OperationLock) controlLeaseDirectoryLockedV1() (string, error) {
	if lock.released || lock.file == nil || lock.path == "" {
		return "", fmt.Errorf("operation lock is not held")
	}
	return filepath.Dir(lock.path), nil
}

// controlLeaseAbandonedV1 returns true only when the marker's lease is absent
// or its advisory lock can be acquired. It is called while the operation lock
// is held, so a live owner cannot concurrently remove its marker and lease.
func controlLeaseAbandonedV1(directory string, id string) (bool, error) {
	path := filepath.Join(directory, id+controlLeaseSuffixV1)
	if err := requireControlLeasePathV1(path, false); err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, fmt.Errorf("open control lease for recovery: %w", err)
	}
	acquired, err := tryLockOperationFile(file)
	if err != nil {
		_ = file.Close()
		return false, fmt.Errorf("inspect control lease ownership: %w", err)
	}
	if !acquired {
		_ = file.Close()
		return false, nil
	}
	unclockErr := unlockOperationFile(file)
	closeErr := file.Close()
	removeErr := removeControlLeasePathV1(path)
	if err := errors.Join(
		wrapControlLeaseErrorV1("release recovered control lease", unclockErr),
		wrapControlLeaseErrorV1("close recovered control lease", closeErr),
		removeErr,
	); err != nil {
		return false, err
	}
	return true, nil
}

func requireControlLeasePathV1(path string, allowMissing bool) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) && allowMissing {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("control lease path must be a regular file: %s", path)
	}
	return nil
}

func removeControlLeasePathV1(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect control lease: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("control lease path must be a regular file: %s", path)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove control lease: %w", err)
	}
	return nil
}

func wrapControlLeaseErrorV1(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", action, err)
}
