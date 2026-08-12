//go:build !linux

package controlledsession

import "fmt"

func prepareControllerTerminalTransportV1(string, string, string) (controllerTerminalTransportV1, error) {
	return nil, fmt.Errorf("controlled-session controller broker requires Linux")
}
