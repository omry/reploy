//go:build !windows

package cli

import "os"

func atomicReplacePackIndexCacheFile(source string, destination string) error {
	return os.Rename(source, destination)
}

func syncPackIndexCacheParent(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
