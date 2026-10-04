package deploy

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// These narrow filesystem seams let native tests inject errors and pause at
// lock release while retaining the real capture and handle-based rename.
type directoryIsolationWindowsOps struct {
	open   func(string) (*os.File, error)
	unlock func(*OperationLock) error
	rename func(*os.File, string) error
	close  func(*os.File) error
}

func defaultDirectoryIsolationWindowsOps() directoryIsolationWindowsOps {
	return directoryIsolationWindowsOps{
		open: openIsolationDirectoryWindows, unlock: (*OperationLock).unlockLocked,
		rename: renameIsolationDirectoryWindows, close: (*os.File).Close,
	}
}

func isolateOriginalDirectoryLocked(lock *OperationLock, source, destination string) (bool, error) {
	return isolateOriginalDirectoryWindowsLocked(lock, source, destination, defaultDirectoryIsolationWindowsOps())
}

func isolateOriginalDirectoryWindowsLocked(lock *OperationLock, source, destination string, ops directoryIsolationWindowsOps) (moved bool, err error) {
	directory, err := ops.open(source)
	if err != nil {
		return false, fmt.Errorf("capture original deployment directory: %w", err)
	}
	defer func() {
		if closeErr := ops.close(directory); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close captured deployment directory: %w", closeErr))
		}
	}()
	info, err := directory.Stat()
	if err != nil || !info.IsDir() || !os.SameFile(lock.directory, info) {
		return false, fmt.Errorf("captured deployment directory identity changed: %v", err)
	}
	var attributes windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(directory.Fd()), &attributes); err != nil {
		return false, fmt.Errorf("inspect captured deployment directory: %w", err)
	}
	if attributes.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return false, fmt.Errorf("captured deployment directory must not be a reparse point")
	}
	// Recheck the original path and retained guard after capture, before releasing
	// the lock. After release, only the captured object is the source of rename.
	if err := lock.requireRetirementLocked(); err != nil {
		return false, err
	}
	if err := ops.unlock(lock); err != nil {
		return false, fmt.Errorf("release operation lock before directory isolation: %w", err)
	}
	if err := ops.rename(directory, destination); err != nil {
		return false, fmt.Errorf("isolate captured deployment directory: %w", err)
	}
	return true, nil
}

func openIsolationDirectoryWindows(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(isolationLongPathWindows(path))
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.DELETE|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func renameIsolationDirectoryWindows(directory *os.File, destination string) error {
	name, err := windows.UTF16FromString(isolationLongPathWindows(destination))
	if err != nil {
		return err
	}
	// FILE_RENAME_INFO: zero ReplaceIfExists/Flags and RootDirectory, followed
	// by the byte length and UTF-16 name. Use the native HANDLE alignment on
	// both supported architectures; FileNameLength excludes the terminator.
	type renameInfo struct {
		flags          uint32
		rootDirectory  windows.Handle
		fileNameLength uint32
		fileName       uint16
	}
	var layout renameInfo
	offset := int(unsafe.Offsetof(layout.fileName))
	buffer := make([]byte, offset+2*len(name))
	binary.LittleEndian.PutUint32(buffer[unsafe.Offsetof(layout.fileNameLength):], uint32(2*(len(name)-1)))
	for i, unit := range name {
		binary.LittleEndian.PutUint16(buffer[offset+2*i:], unit)
	}
	return windows.SetFileInformationByHandle(windows.Handle(directory.Fd()), windows.FileRenameInfo, &buffer[0], uint32(len(buffer)))
}

func isolationLongPathWindows(path string) string {
	if strings.HasPrefix(path, `\\?\`) {
		return path
	}
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + path[2:]
	}
	return `\\?\` + path
}
