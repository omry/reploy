//go:build !linux

package controlledsession

import (
	"strings"
	"testing"
)

func TestControllerTerminalListenerV1FailsGracefullyOffLinux(t *testing.T) {
	if _, err := PrepareControllerTerminalListenerV1(t.TempDir()); err == nil || !strings.Contains(err.Error(), "requires Linux") {
		t.Fatalf("non-Linux terminal listener error = %v", err)
	}
}
