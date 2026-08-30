//go:build windows

package providerstore

import "os"

// Windows publication opens the stage itself with the DELETE and share modes
// required by NtSetInformationFile. An extra read-only stage handle can deny
// that open on native Windows, and the Windows publisher does not need it.
func openArchiveMaterializationStageDirectory(_ *os.Root) (*os.File, error) {
	return nil, nil
}
