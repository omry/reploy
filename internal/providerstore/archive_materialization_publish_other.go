//go:build !linux && !darwin && !windows

package providerstore

import "errors"

// Other release targets fail closed because this operation requires an
// atomic no-replace directory rename; a check followed by os.Rename is not a
// safe substitute.
func publishArchiveMaterializedDirectory(string, string) error {
	return errors.New("atomic no-replace archive directory publication is unsupported on this operating system")
}
