//go:build !linux

package controlledsession

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestControllerTerminalListenerV1FailsGracefullyOffLinux(t *testing.T) {
	if _, err := PrepareControllerTerminalListenerV1(t.TempDir()); err == nil || !strings.Contains(err.Error(), "requires Linux") {
		t.Fatalf("non-Linux terminal listener error = %v", err)
	}
}

func TestTerminalAttachmentV1FailsGracefullyOffLinux(t *testing.T) {
	err := RunTerminalAttachmentV1(context.Background(), TerminalAttachmentOptionsV1{
		SocketPath: "/mnt/reploy-home/reploy-controlled-session-00000000000000000000000000000000/terminal.sock",
		Input:      strings.NewReader(""),
		Output:     &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "requires Linux") {
		t.Fatalf("non-Linux terminal attachment error = %v", err)
	}
}
