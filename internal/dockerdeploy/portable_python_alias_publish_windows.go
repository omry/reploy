//go:build windows

package dockerdeploy

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func createPortablePythonAliasEntryV1(parent *os.Root, target, name string) error {
	if parent == nil {
		return fmt.Errorf("Python alias destination parent handle is unavailable")
	}
	file, err := parent.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	owned, statErr := file.Stat()
	committed := false
	defer func() {
		if !committed {
			if owned == nil {
				owned, _ = file.Stat()
			}
			_ = file.Close()
			if owned != nil {
				_ = removePortablePythonAliasOwnedEntryV1(parent, name, owned)
			}
		}
	}()
	if statErr != nil {
		return statErr
	}
	if _, err := io.WriteString(file, portablePythonAliasEntryPrefixV1+target); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	committed = true
	return nil
}

func readPortablePythonAliasEntryV1(parent *os.Root, name string) (string, error) {
	if parent == nil {
		return "", fmt.Errorf("Python alias parent handle is unavailable")
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return parent.Readlink(name)
	}
	if !info.Mode().IsRegular() {
		return "", errPortablePythonAliasUnownedEntryV1
	}
	file, err := parent.Open(name)
	if err != nil {
		return "", err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, portablePythonAliasMaxRecordBytesV1+1))
	closeErr := file.Close()
	if readErr != nil {
		return "", readErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if len(data) > portablePythonAliasMaxRecordBytesV1 {
		return "", errPortablePythonAliasUnownedEntryV1
	}
	return parsePortablePythonAliasEntryV1(data)
}

func createPortablePythonAliasStagedEntryV1(filename, target string) error {
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = file.Close()
			_ = os.Remove(filename)
		}
	}()
	if _, err := io.WriteString(file, portablePythonAliasEntryPrefixV1+target); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	committed = true
	return nil
}

func readPortablePythonAliasStagedEntryV1(filename string) (string, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return os.Readlink(filename)
	}
	if !info.Mode().IsRegular() {
		return "", errPortablePythonAliasUnownedEntryV1
	}
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, portablePythonAliasMaxRecordBytesV1+1))
	closeErr := file.Close()
	if readErr != nil {
		return "", readErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if len(data) > portablePythonAliasMaxRecordBytesV1 {
		return "", errPortablePythonAliasUnownedEntryV1
	}
	return parsePortablePythonAliasEntryV1(data)
}

type portablePythonAliasWindowsRenameInformationV1 struct {
	replaceIfExists uint32
	rootDirectory   windows.Handle
	fileNameLength  uint32
	fileName        [1]uint16
}

const portablePythonAliasMaxRecordBytesV1 = 1 << 20

// publishPortablePythonAliasNoReplaceV1 uses the Windows native rename
// operation with ReplaceIfExists=false and a root directory handle. The
// temporary alias record is an ordinary file, so this path does not require
// the privilege normally needed to create a Windows symlink.
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
		// Reopen the ordinary staging record itself. In particular, do not
		// traverse a reparse point inserted after the initial rooted creation.
		Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
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
		windows.FILE_WRITE_THROUGH|windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT,
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
