//go:build windows

package deploy

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func openOperationLockFile(root *os.Root, create bool) (*os.File, error) {
	info, err := root.Lstat("operation.lock")
	flags := os.O_RDWR
	if os.IsNotExist(err) && create {
		flags |= os.O_CREATE | os.O_EXCL
	} else if err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("operation lock path must be a regular file")
	}
	file, err := root.OpenFile("operation.lock", flags, 0o600)
	if err != nil {
		return nil, err
	}
	opened, statErr := file.Stat()
	after, afterErr := root.Lstat("operation.lock")
	if statErr != nil || afterErr != nil || !after.Mode().IsRegular() || !os.SameFile(opened, after) {
		_ = file.Close()
		return nil, fmt.Errorf("operation lock file identity changed")
	}
	return file, nil
}

func tryLockOperationFile(file *os.File) (bool, error) {
	overlapped := new(windows.Overlapped)
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		overlapped,
	)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return false, err
}

func unlockOperationFile(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, new(windows.Overlapped))
}
