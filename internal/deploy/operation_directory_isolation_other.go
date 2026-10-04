//go:build !linux && !darwin && !windows

package deploy

import (
	"fmt"
	"runtime"
)

func isolateOriginalDirectoryLocked(_ *OperationLock, _, _ string) (bool, error) {
	return false, fmt.Errorf("non-replacing directory isolation is not implemented on %s", runtime.GOOS)
}
