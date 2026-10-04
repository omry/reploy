package deploy

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func isolateOriginalDirectoryLocked(_ *OperationLock, source, destination string) (bool, error) {
	if err := unix.RenamexNp(source, destination, unix.RENAME_EXCL); err != nil {
		return false, fmt.Errorf("isolate original deployment directory: %w", err)
	}
	return true, nil
}
