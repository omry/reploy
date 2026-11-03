package dockerdeploy

import (
	"fmt"
	"io"
)

const managedMountSetupArgumentV1 = "__reploy-managed-mount-setup-v1"

// RunManagedMountSetupChildV1 handles only the fixed private setup operation.
// Its complete input comes from sealed and inherited descriptors, never argv.
func RunManagedMountSetupChildV1(args []string, stderr io.Writer) (int, bool) {
	if len(args) != 1 || args[0] != managedMountSetupArgumentV1 {
		return 0, false
	}
	if err := runManagedMountSetupChildV1(); err != nil {
		if stderr != nil {
			_, _ = fmt.Fprintf(stderr, "managed mount setup: %v\n", err)
		}
		return 1, true
	}
	return 0, true
}
