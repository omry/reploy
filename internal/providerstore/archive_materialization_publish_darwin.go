//go:build darwin

package providerstore

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// publishArchiveMaterializedDirectory uses renameatx_np with RENAME_EXCL,
// the Darwin atomic no-replace directory rename.
func publishArchiveMaterializedDirectory(stage string, destination string) error {
	sourceParent, err := os.Open(filepath.Dir(stage))
	if err != nil {
		return fmt.Errorf("open archive materialization stage parent: %w", err)
	}
	defer sourceParent.Close()
	destinationParent, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return fmt.Errorf("open archive materialization destination parent: %w", err)
	}
	defer destinationParent.Close()
	if err := unix.RenameatxNp(
		int(sourceParent.Fd()), filepath.Base(stage),
		int(destinationParent.Fd()), filepath.Base(destination),
		unix.RENAME_EXCL,
	); err != nil {
		return fmt.Errorf("rename archive materialization directory without replacement: %w", err)
	}
	return nil
}
