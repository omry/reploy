//go:build !windows

package providerstore

import "os"

func openArchiveMaterializationStageDirectory(root *os.Root) (*os.File, error) {
	return root.Open(".")
}
