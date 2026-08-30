//go:build windows

package providerstore

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

type archiveMaterializationWindowsRenameInformation struct {
	replaceIfExists uint32
	rootDirectory   windows.Handle
	fileNameLength  uint32
	fileName        [1]uint16
}

// publishArchiveMaterializedDirectory performs an atomic no-replace rename
// relative to the opened destination-root handle.
func publishArchiveMaterializedDirectory(parent *os.File, _ *os.File, stage string, destination string) (bool, error) {
	stageName, err := windows.NewNTUnicodeString(stage)
	if err != nil {
		return false, fmt.Errorf("encode archive materialization stage name: %w", err)
	}
	parentHandle := windows.Handle(parent.Fd())
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
		windows.FILE_DIRECTORY_FILE|windows.FILE_WRITE_THROUGH|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		0,
		0,
	); err != nil {
		return false, fmt.Errorf("open archive materialization stage for publication: %w", err)
	}
	defer windows.CloseHandle(stageHandle)

	destinationName, err := windows.UTF16FromString(destination)
	if err != nil {
		return false, fmt.Errorf("encode archive materialization destination name: %w", err)
	}
	fileNameLength := (len(destinationName) - 1) * 2
	var layout archiveMaterializationWindowsRenameInformation
	bufferSize := int(unsafe.Offsetof(layout.fileName)) + fileNameLength
	buffer := make([]byte, bufferSize)
	rename := (*archiveMaterializationWindowsRenameInformation)(unsafe.Pointer(&buffer[0]))
	rename.rootDirectory = parentHandle
	rename.fileNameLength = uint32(fileNameLength)
	copy((*[CoreMaxArchiveComponentBytes + 1]uint16)(unsafe.Pointer(&rename.fileName[0]))[:len(destinationName)-1], destinationName)
	if err := windows.NtSetInformationFile(
		stageHandle,
		&windows.IO_STATUS_BLOCK{},
		&buffer[0],
		uint32(bufferSize),
		windows.FileRenameInformation,
	); err != nil {
		return false, fmt.Errorf("rename archive materialization directory without replacement: %w", err)
	}
	runtime.KeepAlive(parent)
	return true, nil
}
