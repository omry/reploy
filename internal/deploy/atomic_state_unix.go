//go:build !windows

package deploy

import "os"

func atomicReplaceFile(source string, destination string) error {
	return os.Rename(source, destination)
}

func syncAtomicStateDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
