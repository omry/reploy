//go:build windows

package dockerdeploy

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func readPrivateWorkloadEnvironmentFileV1(deploymentDir string) (content []byte, found bool, err error) {
	directory, directoryInfo, err := openPrivateWorkloadEnvironmentWindowsHandleV1(
		deploymentDir,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
	)
	if err != nil {
		return nil, false, fmt.Errorf("open deployment directory for %s: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	defer windows.CloseHandle(directory)
	if directoryInfo.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || directoryInfo.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, false, fmt.Errorf("deployment directory for %s must be a real directory", PrivateWorkloadEnvironmentFileName)
	}
	path := filepath.Join(deploymentDir, PrivateWorkloadEnvironmentFileName)
	file, fileInfo, err := openPrivateWorkloadEnvironmentWindowsHandleV1(path, windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("open %s without following links: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	if fileInfo.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 || fileInfo.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(file)
		return nil, false, fmt.Errorf("%s must be a real regular file", PrivateWorkloadEnvironmentFileName)
	}
	if fileInfo.NumberOfLinks != 1 {
		_ = windows.CloseHandle(file)
		return nil, false, fmt.Errorf("%s must not have hard links", PrivateWorkloadEnvironmentFileName)
	}
	if err := validatePrivateWorkloadEnvironmentWindowsSecurityV1(directory, file); err != nil {
		_ = windows.CloseHandle(file)
		return nil, false, err
	}
	opened := os.NewFile(uintptr(file), PrivateWorkloadEnvironmentFileName)
	if opened == nil {
		_ = windows.CloseHandle(file)
		return nil, false, fmt.Errorf("open %s: invalid file handle", PrivateWorkloadEnvironmentFileName)
	}
	defer func() {
		if closeErr := opened.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	content, err = io.ReadAll(io.LimitReader(opened, privateWorkloadEnvironmentMaxBytes+1))
	if err != nil {
		return nil, false, fmt.Errorf("read opened %s: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	if len(content) > privateWorkloadEnvironmentMaxBytes {
		return nil, false, fmt.Errorf("%s exceeds the %d-byte limit", PrivateWorkloadEnvironmentFileName, privateWorkloadEnvironmentMaxBytes)
	}
	return content, true, nil
}

func openPrivateWorkloadEnvironmentWindowsHandleV1(path string, flags uint32) (windows.Handle, windows.ByHandleFileInformation, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, windows.ByHandleFileInformation{}, err
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.GENERIC_READ|windows.READ_CONTROL,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		flags,
		0,
	)
	if err != nil {
		return 0, windows.ByHandleFileInformation{}, err
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		_ = windows.CloseHandle(handle)
		return 0, windows.ByHandleFileInformation{}, err
	}
	return handle, information, nil
}

func validatePrivateWorkloadEnvironmentWindowsSecurityV1(directory windows.Handle, file windows.Handle) error {
	directoryDescriptor, err := windows.GetSecurityInfo(directory, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("inspect deployment directory owner for %s: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	directoryOwner, _, err := directoryDescriptor.Owner()
	if err != nil {
		return fmt.Errorf("read deployment directory owner for %s: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	fileDescriptor, err := windows.GetSecurityInfo(
		file,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("inspect %s ACL: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	fileOwner, _, err := fileDescriptor.Owner()
	if err != nil {
		return fmt.Errorf("read %s owner: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	if !fileOwner.Equals(directoryOwner) {
		return fmt.Errorf("%s must be owned by the deployment directory owner", PrivateWorkloadEnvironmentFileName)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("resolve Windows SYSTEM identity for %s: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("resolve Windows Administrators identity for %s: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	dacl, _, err := fileDescriptor.DACL()
	if err != nil {
		return fmt.Errorf("read %s ACL: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	if dacl == nil {
		return fmt.Errorf("%s must have a restrictive ACL", PrivateWorkloadEnvironmentFileName)
	}
	ownerCanRead := false
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			return fmt.Errorf("read %s ACL entry %d: %w", PrivateWorkloadEnvironmentFileName, index, err)
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("%s ACL contains an unsupported access rule", PrivateWorkloadEnvironmentFileName)
		}
		if ace.Mask == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		allowed := sid.Equals(fileOwner) || sid.Equals(system) || sid.Equals(administrators)
		if !allowed {
			return fmt.Errorf("%s ACL grants access beyond its owner, SYSTEM, or Administrators", PrivateWorkloadEnvironmentFileName)
		}
		if sid.Equals(fileOwner) && ace.Mask&(windows.GENERIC_READ|windows.GENERIC_ALL|windows.FILE_READ_DATA) != 0 {
			ownerCanRead = true
		}
	}
	if !ownerCanRead {
		return fmt.Errorf("%s ACL must grant its owner read access", PrivateWorkloadEnvironmentFileName)
	}
	return nil
}

func publishPrivateWorkloadEnvironmentFileV1(target string, content []byte, replace bool) (bool, error) {
	parent := filepath.Dir(target)
	descriptor, err := windows.GetNamedSecurityInfo(parent, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return false, fmt.Errorf("inspect private environment destination owner: %w", err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return false, fmt.Errorf("read private environment destination owner: %w", err)
	}
	sddl := "O:" + owner.String() + "D:P" +
		"(A;;GA;;;" + owner.String() + ")" +
		"(A;;GA;;;SY)" +
		"(A;;GA;;;BA)"
	securityDescriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return false, fmt.Errorf("create private environment ACL: %w", err)
	}
	attributes := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: securityDescriptor,
	}
	destination := target
	if replace {
		destination, err = privateWorkloadEnvironmentWindowsTemporaryPathV1(parent)
		if err != nil {
			return false, err
		}
	}
	pointer, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return false, err
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.GENERIC_WRITE|windows.READ_CONTROL,
		0,
		&attributes,
		windows.CREATE_NEW,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if !replace && (errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS)) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	opened := os.NewFile(uintptr(handle), destination)
	if opened == nil {
		_ = windows.CloseHandle(handle)
		_ = windows.DeleteFile(pointer)
		return false, fmt.Errorf("create private environment destination: invalid file handle")
	}
	if _, err := opened.Write(content); err != nil {
		return false, errors.Join(err, opened.Close(), windows.DeleteFile(pointer))
	}
	if err := opened.Sync(); err != nil {
		return false, errors.Join(err, opened.Close(), windows.DeleteFile(pointer))
	}
	if err := opened.Close(); err != nil {
		return false, errors.Join(err, windows.DeleteFile(pointer))
	}
	if !replace {
		return true, nil
	}
	targetPointer, err := windows.UTF16PtrFromString(target)
	if err != nil {
		_ = windows.DeleteFile(pointer)
		return false, err
	}
	if err := windows.MoveFileEx(pointer, targetPointer, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return false, errors.Join(err, windows.DeleteFile(pointer))
	}
	return true, nil
}

func privateWorkloadEnvironmentWindowsTemporaryPathV1(parent string) (string, error) {
	for attempt := 0; attempt < 32; attempt++ {
		random := make([]byte, 12)
		if _, err := rand.Read(random); err != nil {
			return "", fmt.Errorf("generate private environment temporary name: %w", err)
		}
		candidate := filepath.Join(parent, ".reploy-private-env-"+hex.EncodeToString(random))
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("allocate private environment temporary name")
}
