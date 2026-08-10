//go:build windows

package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func createControlledSessionIncidentDirectoryV1(path string) error {
	parentDescriptor, err := windows.GetNamedSecurityInfo(
		filepath.Dir(path),
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("inspect incident receipt parent owner: %w", err)
	}
	owner, _, err := parentDescriptor.Owner()
	if err != nil {
		return fmt.Errorf("read incident receipt parent owner: %w", err)
	}
	securityDescriptor, err := windows.SecurityDescriptorFromString(
		"O:" + owner.String() + "D:P" +
			"(A;;GA;;;" + owner.String() + ")" +
			"(A;;GA;;;SY)" +
			"(A;;GA;;;BA)",
	)
	if err != nil {
		return fmt.Errorf("create private incident receipt directory ACL: %w", err)
	}
	attributes := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: securityDescriptor,
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.CreateDirectory(pointer, &attributes)
}

func validateControlledSessionIncidentDirectorySecurityV1(path string, _ os.FileInfo) error {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return fmt.Errorf("open controlled-session incident receipt directory: %w", err)
	}
	defer windows.CloseHandle(handle)
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		return fmt.Errorf("inspect controlled-session incident receipt directory: %w", err)
	}
	if information.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 ||
		information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("controlled-session incident receipt path must be a real directory: %s", path)
	}
	descriptor, err := windows.GetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("inspect controlled-session incident receipt directory ACL: %w", err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("read controlled-session incident receipt directory owner: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read controlled-session incident receipt directory ACL: %w", err)
	}
	if dacl == nil {
		return fmt.Errorf("controlled-session incident receipt directory must have a restrictive ACL")
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("resolve Windows SYSTEM identity for incident receipts: %w", err)
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("resolve Windows Administrators identity for incident receipts: %w", err)
	}
	ownerCanRead := false
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			return fmt.Errorf("read controlled-session incident receipt directory ACL entry %d: %w", index, err)
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("controlled-session incident receipt directory ACL contains an unsupported access rule")
		}
		if ace.Mask == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.Equals(owner) && !sid.Equals(system) && !sid.Equals(administrators) {
			return fmt.Errorf("controlled-session incident receipt directory ACL grants access beyond its owner, SYSTEM, or Administrators")
		}
		if sid.Equals(owner) && ace.Mask&(windows.GENERIC_READ|windows.GENERIC_ALL|windows.FILE_LIST_DIRECTORY) != 0 {
			ownerCanRead = true
		}
	}
	if !ownerCanRead {
		return fmt.Errorf("controlled-session incident receipt directory ACL must grant its owner read access")
	}
	return nil
}
