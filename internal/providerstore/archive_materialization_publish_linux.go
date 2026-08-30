//go:build linux

package providerstore

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// publishArchiveMaterializedDirectory uses renameat2 with RENAME_NOREPLACE,
// which keeps an existing destination untouched and leaves the stage in
// place when publication loses a destination race. Both names are resolved
// relative to the destination-root handle that owns the transaction.
func publishArchiveMaterializedDirectory(parent *os.File, _ *os.File, stage string, destination string) (bool, error) {
	if err := unix.Renameat2(
		int(parent.Fd()), stage,
		int(parent.Fd()), destination,
		unix.RENAME_NOREPLACE,
	); err != nil {
		return false, fmt.Errorf("rename archive materialization directory without replacement: %w", err)
	}
	if err := parent.Sync(); err != nil {
		return true, fmt.Errorf("sync archive materialization destination root after publication: %w", err)
	}
	return true, nil
}
