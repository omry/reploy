//go:build !linux

package probe

import "os"

func copyVolumeTreeOwnership(os.FileInfo, string, bool) error {
	return nil
}

func volumeCopyHardlinkIdentity(os.FileInfo) (string, bool) {
	return "", false
}
