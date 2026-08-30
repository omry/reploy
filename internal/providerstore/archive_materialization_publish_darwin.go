//go:build darwin

package providerstore

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// publishArchiveMaterializedDirectory uses renamex_np with RENAME_EXCL,
// the Darwin atomic no-replace directory rename. Both names are resolved
// relative to the destination-root handle that owns the transaction.
func publishArchiveMaterializedDirectory(parent *os.File, stageDirectory *os.File, stage string, destination string) (bool, error) {
	info, err := stageDirectory.Stat()
	if err != nil {
		return false, fmt.Errorf("inspect archive materialization stage directory: %w", err)
	}
	originalMode := info.Mode().Perm()
	// Darwin/XNU rename authorization requires add-subdirectory rights on the
	// source directory, so a normalized 0555 stage needs a private temporary
	// owner-write bit.
	ownerWriteAdded := originalMode&0o200 == 0
	if ownerWriteAdded {
		if err := stageDirectory.Chmod(originalMode | 0o200); err != nil {
			return false, fmt.Errorf("temporarily enable stage directory owner-write permission: %w", err)
		}
	}

	renameErr := unix.RenameatxNp(int(parent.Fd()), stage, int(parent.Fd()), destination, unix.RENAME_EXCL)
	var restoreErr error
	if ownerWriteAdded {
		restoreErr = stageDirectory.Chmod(originalMode)
	}
	if renameErr != nil {
		return false, errors.Join(
			fmt.Errorf("rename archive materialization directory without replacement: %w", renameErr),
			archiveMaterializationDarwinRestoreError(restoreErr),
		)
	}
	if restoreErr != nil {
		rollbackErr := unix.RenameatxNp(int(parent.Fd()), destination, int(parent.Fd()), stage, unix.RENAME_EXCL)
		retryRestoreErr := stageDirectory.Chmod(originalMode)
		if rollbackErr == nil {
			return false, errors.Join(
				archiveMaterializationDarwinRestoreError(restoreErr),
				archiveMaterializationDarwinRetryRestoreError(retryRestoreErr),
			)
		}
		if retryRestoreErr == nil {
			// The destination remains published with its normalized mode. Sync
			// the published directory and both rename parents below.
		} else {
			return true, errors.Join(
				archiveMaterializationDarwinRestoreError(restoreErr),
				archiveMaterializationDarwinRollbackError(rollbackErr),
				archiveMaterializationDarwinRetryRestoreError(retryRestoreErr),
			)
		}
	}
	if err := syncArchiveMaterializationDarwinPublication(stageDirectory, parent); err != nil {
		return true, err
	}
	return true, nil
}

func syncArchiveMaterializationDarwinPublication(stageDirectory *os.File, parent *os.File) error {
	var syncErr error
	if err := stageDirectory.Sync(); err != nil {
		syncErr = fmt.Errorf("sync published archive materialization directory: %w", err)
	}
	if err := parent.Sync(); err != nil {
		syncErr = errors.Join(syncErr, fmt.Errorf("sync archive materialization destination root after publication: %w", err))
	}
	return syncErr
}

func archiveMaterializationDarwinRestoreError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("restore archive materialization stage directory permissions: %w", err)
}

func archiveMaterializationDarwinRollbackError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("rollback archive materialization directory publication: %w", err)
}

func archiveMaterializationDarwinRetryRestoreError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("retry restore archive materialization stage directory permissions: %w", err)
}
