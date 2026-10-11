//go:build !linux && !darwin

package dockerdeploy

import "os"

func openProviderPreflightStateV1(root *os.Root) (*os.File, error) {
	return root.Open("state.json")
}
