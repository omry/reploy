//go:build !windows

package dockerdeploy

import (
	"io/fs"
	"os"
)

func providerInstallFileModeMatches(actual fs.FileMode, expected fs.FileMode) bool {
	return actual.Perm() == expected.Perm()
}

func replaceProviderInstallFile(source string, destination string) error {
	return os.Rename(source, destination)
}

func syncProviderInstallDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
