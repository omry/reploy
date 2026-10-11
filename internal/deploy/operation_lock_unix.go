//go:build !windows

package deploy

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Opening relative to a held directory with O_NOFOLLOW prevents a staging
// operator from redirecting the privileged ownership transfer to another inode.
func openOperationLockFile(root *os.Root, create bool) (*os.File, error) {
	parent, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	flags := unix.O_RDWR | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if create {
		flags |= unix.O_CREAT
	}
	fd, err := unix.Openat(int(parent.Fd()), "operation.lock", flags, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "operation.lock")
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("operation lock path must be a regular file: %v", err)
	}
	return file, nil
}

func tryLockOperationFile(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return false, err
}

func unlockOperationFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
