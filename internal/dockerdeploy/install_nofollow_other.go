//go:build !linux && !darwin

package dockerdeploy

import "os"

func openInstallTargetNoFollow(path string, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
}
