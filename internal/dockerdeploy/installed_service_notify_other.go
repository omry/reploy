//go:build !linux

package dockerdeploy

func notifyInstalledServiceReadyV1() error {
	return nil
}
