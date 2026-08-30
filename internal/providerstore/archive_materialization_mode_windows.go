//go:build windows

package providerstore

import (
	"os"
)

// Windows directory handles opened for reading do not have FILE_WRITE_ATTRIBUTES
// access, so chmod through the os.File returned by Root.Open fails with
// ERROR_ACCESS_DENIED. Root.Chmod opens a scoped write-attributes handle after
// the caller has validated the opened entry identity.
func chmodArchiveMaterializationDirectory(root *os.Root, path string, _ *os.File, mode os.FileMode) error {
	return root.Chmod(path, mode)
}
