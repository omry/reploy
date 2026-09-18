//go:build windows

package dockerdeploy

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

type portablePythonAliasWindowsRenameInformationV1 struct {
	replaceIfExists uint32
	rootDirectory   windows.Handle
	fileNameLength  uint32
	fileName        [1]uint16
}

// publishPortablePythonAliasNoReplaceV1 uses the Windows native rename
// operation with ReplaceIfExists=false and a root directory handle. The
// temporary entry is opened as a reparse point so the symlink itself is
// renamed rather than its final-image target.
func publishPortablePythonAliasNoReplaceV1(parent *os.Root, temporary, destination string) error {
	if parent == nil {
		return fmt.Errorf("Python alias destination parent handle is unavailable")
	}
	directory, err := parent.Open(".")
	if err != nil {
		return fmt.Errorf("open Python alias destination directory handle: %w", err)
	}
	defer directory.Close()
	parentHandle := windows.Handle(directory.Fd())
	stageName, err := windows.NewNTUnicodeString(temporary)
	if err != nil {
		return fmt.Errorf("encode temporary Python alias name: %w", err)
	}
	attributes := &windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: parentHandle,
		ObjectName:    stageName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	var stageHandle windows.Handle
	if err := windows.NtCreateFile(
		&stageHandle,
		windows.DELETE|windows.SYNCHRONIZE,
		attributes,
		&windows.IO_STATUS_BLOCK{},
		nil,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		windows.FILE_WRITE_THROUGH|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		0,
		0,
	); err != nil {
		return fmt.Errorf("open temporary Python alias for publication: %w", err)
	}
	defer windows.CloseHandle(stageHandle)

	destinationName, err := windows.UTF16FromString(destination)
	if err != nil {
		return fmt.Errorf("encode Python alias destination name: %w", err)
	}
	fileNameLength := (len(destinationName) - 1) * 2
	var layout portablePythonAliasWindowsRenameInformationV1
	bufferSize := int(unsafe.Offsetof(layout.fileName)) + fileNameLength
	buffer := make([]byte, bufferSize)
	rename := (*portablePythonAliasWindowsRenameInformationV1)(unsafe.Pointer(&buffer[0]))
	rename.rootDirectory = parentHandle
	rename.fileNameLength = uint32(fileNameLength)
	copy((*[1 << 16]uint16)(unsafe.Pointer(&rename.fileName[0]))[:len(destinationName)-1], destinationName)
	if err := windows.NtSetInformationFile(
		stageHandle,
		&windows.IO_STATUS_BLOCK{},
		&buffer[0],
		uint32(bufferSize),
		windows.FileRenameInformation,
	); err != nil {
		return fmt.Errorf("rename temporary Python alias without replacement: %w", err)
	}
	runtime.KeepAlive(parent)
	return nil
}
