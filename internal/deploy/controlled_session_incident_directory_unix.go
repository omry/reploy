//go:build !windows

package deploy

import (
	"fmt"
	"os"
)

func createControlledSessionIncidentDirectoryV1(path string) error {
	return os.Mkdir(path, 0o700)
}

func validateControlledSessionIncidentDirectorySecurityV1(_ string, info os.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("controlled-session incident receipt directory must not be accessible to group or other users")
	}
	return nil
}
