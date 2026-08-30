//go:build !windows

package providerstore

import (
	"os"
)

func chmodArchiveMaterializationDirectory(_ *os.Root, _ string, directory *os.File, mode os.FileMode) error {
	return directory.Chmod(mode)
}
