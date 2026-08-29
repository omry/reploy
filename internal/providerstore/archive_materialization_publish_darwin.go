//go:build darwin

package providerstore

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// publishArchiveMaterializedDirectory uses renamex_np with RENAME_EXCL,
// the Darwin atomic no-replace directory rename.
func publishArchiveMaterializedDirectory(stage string, destination string) error {
	stageDirectory, err := os.Open(stage)
	if err != nil {
		return fmt.Errorf("open archive materialization stage directory: %w", err)
	}
	defer stageDirectory.Close()
	info, err := stageDirectory.Stat()
	if err != nil {
		return fmt.Errorf("inspect archive materialization stage directory: %w", err)
	}
	originalMode := info.Mode().Perm()
	// Darwin/XNU rename authorization requires add-subdirectory rights on the
	// source directory, so a normalized 0555 stage needs a private temporary
	// owner-write bit.
	ownerWriteAdded := originalMode&0o200 == 0
	if ownerWriteAdded {
		if err := stageDirectory.Chmod(originalMode | 0o200); err != nil {
			return fmt.Errorf("temporarily enable stage directory owner-write permission: %w", err)
		}
	}

	renameErr := unix.RenamexNp(stage, destination, unix.RENAME_EXCL)
	var restoreErr error
	if ownerWriteAdded {
		restoreErr = stageDirectory.Chmod(originalMode)
	}
	if renameErr != nil {
		return errors.Join(
			fmt.Errorf("rename archive materialization directory without replacement: %w", renameErr),
			archiveMaterializationDarwinRestoreError(restoreErr),
		)
	}
	if restoreErr != nil {
		rollbackErr := unix.RenamexNp(destination, stage, unix.RENAME_EXCL)
		retryRestoreErr := stageDirectory.Chmod(originalMode)
		if rollbackErr == nil {
			return errors.Join(
				archiveMaterializationDarwinRestoreError(restoreErr),
				archiveMaterializationDarwinRetryRestoreError(retryRestoreErr),
			)
		}
		if retryRestoreErr == nil {
			return nil
		}
		return errors.Join(
			archiveMaterializationDarwinRestoreError(restoreErr),
			archiveMaterializationDarwinRollbackError(rollbackErr),
			archiveMaterializationDarwinRetryRestoreError(retryRestoreErr),
		)
	}
	return nil
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
