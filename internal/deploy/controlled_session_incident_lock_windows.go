//go:build windows

package deploy

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// The controlled-session watchdog is Linux-only. Windows receipt targets can
// therefore never remain live in an inherited watchdog process.
func lockControlledSessionIncidentTargetV1(*os.File) error { return nil }

func controlledSessionIncidentTargetInUseV1(path string) (bool, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if err := windows.CloseHandle(handle); err != nil {
		return false, fmt.Errorf("close controlled-session incident target liveness handle: %w", err)
	}
	return false, nil
}
