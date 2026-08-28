//go:build windows

package providerstore

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// publishArchiveMaterializedDirectory uses MoveFileEx without
// MOVEFILE_REPLACE_EXISTING, so an existing destination fails atomically.
func publishArchiveMaterializedDirectory(stage string, destination string) error {
	from, err := windows.UTF16PtrFromString(stage)
	if err != nil {
		return fmt.Errorf("encode archive materialization stage path: %w", err)
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return fmt.Errorf("encode archive materialization destination path: %w", err)
	}
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return fmt.Errorf("rename archive materialization directory without replacement: %w", err)
	}
	return nil
}
