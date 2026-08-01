//go:build windows

package cli

import (
	"errors"
	"os"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var replaceFileW = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

func openPackIndexFile(path string) (*os.File, error) {
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		handle, err := windows.CreateFile(
			pathPointer,
			windows.GENERIC_READ,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			nil,
			windows.OPEN_EXISTING,
			windows.FILE_ATTRIBUTE_NORMAL,
			0,
		)
		if err == nil {
			return os.NewFile(uintptr(handle), path), nil
		}
		if !transientPackIndexOpenError(err) || !time.Now().Before(deadline) {
			return nil, err
		}
		time.Sleep(time.Millisecond)
	}
}

func transientPackIndexOpenError(err error) bool {
	return errors.Is(err, windows.ERROR_FILE_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED)
}

func atomicReplacePackIndexCacheFile(source string, destination string) error {
	sourcePath, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPath, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	result, _, callErr := replaceFileW.Call(
		uintptr(unsafe.Pointer(destinationPath)),
		uintptr(unsafe.Pointer(sourcePath)),
		0,
		0,
		0,
		0,
	)
	if result != 0 {
		return nil
	}
	if !errors.Is(callErr, windows.ERROR_FILE_NOT_FOUND) {
		if callErr == syscall.Errno(0) {
			return syscall.EINVAL
		}
		return callErr
	}
	return windows.MoveFileEx(sourcePath, destinationPath, windows.MOVEFILE_WRITE_THROUGH)
}

func syncPackIndexCacheParent(string) error { return nil }
