//go:build linux

package providerstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// publishArchiveMaterializedDirectory uses renameat2 with RENAME_NOREPLACE,
// which keeps an existing destination untouched and leaves the stage in
// place when publication loses a destination race.
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
	if err := unix.Renameat2(
		int(sourceParent.Fd()), filepath.Base(stage),
		int(destinationParent.Fd()), filepath.Base(destination),
		unix.RENAME_NOREPLACE,
	); err != nil {
		return fmt.Errorf("rename archive materialization directory without replacement: %w", err)
	}
	var syncErr error
	if err := sourceParent.Sync(); err != nil {
		syncErr = fmt.Errorf("sync archive materialization stage parent after publication: %w", err)
	}
	if err := destinationParent.Sync(); err != nil {
		syncErr = errors.Join(syncErr, fmt.Errorf("sync archive materialization destination parent after publication: %w", err))
	}
	if syncErr != nil {
		return syncErr
	}
	return nil
}
