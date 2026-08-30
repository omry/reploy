//go:build !windows

package providerstore

import "os"

func syncArchiveMaterializationDirectory(root *os.Root, path string) error {
	directory, err := root.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
