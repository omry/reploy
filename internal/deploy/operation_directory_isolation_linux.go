package deploy

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func isolateOriginalDirectoryLocked(_ *OperationLock, source, destination string) (bool, error) {
	if err := unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE); err != nil {
		return false, fmt.Errorf("isolate original deployment directory: %w", err)
	}
	return true, nil
}
