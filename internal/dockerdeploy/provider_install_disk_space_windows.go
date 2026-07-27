//go:build windows

package dockerdeploy

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

func providerInstallFilesystemSpace(path string) (providerInstallFilesystemSpaceV1, error) {
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return providerInstallFilesystemSpaceV1{}, err
	}
	volumeBuffer := make([]uint16, 32768)
	if err := windows.GetVolumePathName(pathPointer, &volumeBuffer[0], uint32(len(volumeBuffer))); err != nil {
		return providerInstallFilesystemSpaceV1{}, err
	}
	volume := windows.UTF16ToString(volumeBuffer)
	if volume == "" {
		return providerInstallFilesystemSpaceV1{}, fmt.Errorf("filesystem volume path is empty")
	}
	volumePointer, err := windows.UTF16PtrFromString(volume)
	if err != nil {
		return providerInstallFilesystemSpaceV1{}, err
	}
	var available uint64
	if err := windows.GetDiskFreeSpaceEx(volumePointer, &available, nil, nil); err != nil {
		return providerInstallFilesystemSpaceV1{}, err
	}
	return providerInstallFilesystemSpaceV1{Key: strings.ToLower(volume), Available: available}, nil
}
