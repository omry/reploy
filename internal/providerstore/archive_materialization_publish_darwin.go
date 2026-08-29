//go:build darwin

package providerstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
			// The destination remains published with its normalized mode. Sync
			// the published directory and both rename parents below.
		} else {
			return errors.Join(
				archiveMaterializationDarwinRestoreError(restoreErr),
				archiveMaterializationDarwinRollbackError(rollbackErr),
				archiveMaterializationDarwinRetryRestoreError(retryRestoreErr),
			)
		}
	}
	if err := syncArchiveMaterializationDarwinPublication(stageDirectory, sourceParent, destinationParent); err != nil {
		return err
	}
	return nil
}

func syncArchiveMaterializationDarwinPublication(stageDirectory *os.File, sourceParent *os.File, destinationParent *os.File) error {
	var syncErr error
	if err := stageDirectory.Sync(); err != nil {
		syncErr = fmt.Errorf("sync published archive materialization directory: %w", err)
	}
	if err := sourceParent.Sync(); err != nil {
		syncErr = errors.Join(syncErr, fmt.Errorf("sync archive materialization stage parent after publication: %w", err))
	}
	if err := destinationParent.Sync(); err != nil {
		syncErr = errors.Join(syncErr, fmt.Errorf("sync archive materialization destination parent after publication: %w", err))
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
