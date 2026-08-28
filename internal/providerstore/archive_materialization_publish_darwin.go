//go:build darwin

package providerstore

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// publishArchiveMaterializedDirectory uses renamex_np with RENAME_EXCL,
// the Darwin atomic no-replace directory rename.
func publishArchiveMaterializedDirectory(stage string, destination string) error {
	if err := unix.RenamexNp(stage, destination, unix.RENAME_EXCL); err != nil {
		return fmt.Errorf("rename archive materialization directory without replacement: %w", err)
	}
	return nil
}
