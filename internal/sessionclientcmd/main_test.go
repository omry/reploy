package sessionclientcmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/controlledsession"
)

func TestHelpAndUsage(t *testing.T) {
	code, stdout, stderr := runCommand(t, "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage: reploy-session-client COMMAND") || !strings.Contains(stdout, "attach --socket PATH") {
		t.Fatalf("help code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runCommand(t, "client", "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage: reploy-session-client client") || !strings.Contains(stdout, "REPLOY_SESSION_SOCKET") {
		t.Fatalf("client help code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runCommand(t, "attach", "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage: reploy-session-client attach --socket PATH") {
		t.Fatalf("attach help code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runCommand(t)
	if code != 2 || stdout != "" || !strings.Contains(stderr, "expected client or attach") || !strings.Contains(stderr, "Usage: reploy-session-client {client | attach --socket PATH}") {
		t.Fatalf("usage code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, args := range [][]string{
		{"attach"},
		{"attach", "--socket"},
		{"attach", "--socket="},
		{"attach", "--socket", "/tmp/socket", "extra"},
	} {
		code, stdout, stderr = runCommand(t, args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "attach usage error") {
			t.Fatalf("attach usage args=%q code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
}

func TestAttachDispatchesTerminalAttachment(t *testing.T) {
	original := runTerminalAttachment
	t.Cleanup(func() { runTerminalAttachment = original })
	called := false
	runTerminalAttachment = func(ctx context.Context, options controlledsession.TerminalAttachmentOptionsV1) error {
		called = true
		if ctx == nil || options.SocketPath != "/run/reploy/terminal.sock" || options.Input == nil || options.Output == nil {
			t.Fatalf("attachment options = %#v", options)
		}
		return nil
	}
	code, stdout, stderr := runCommand(t, "attach", "--socket", "/run/reploy/terminal.sock")
	if code != 0 || stdout != "" || stderr != "" || !called {
		t.Fatalf("attach code=%d stdout=%q stderr=%q called=%t", code, stdout, stderr, called)
	}
}

func TestClientDispatchesBroker(t *testing.T) {
	original := runControllerBroker
	t.Cleanup(func() { runControllerBroker = original })
	t.Setenv("REPLOY_SESSION_SOCKET", "/run/reploy/session.sock")
	called := false
	runControllerBroker = func(ctx context.Context, options controlledsession.ControllerBrokerOptionsV1) error {
		called = true
		if ctx == nil || options.SessionSocket != "/run/reploy/session.sock" || options.TemporaryHome != controlledsession.ControllerTemporaryHomeV1 || options.Input == nil || options.Output == nil {
			t.Fatalf("broker options = %#v", options)
		}
		return nil
	}
	code, stdout, stderr := runCommand(t, "client")
	if code != 0 || stdout != "" || stderr != "" || !called {
		t.Fatalf("client code=%d stdout=%q stderr=%q called=%t", code, stdout, stderr, called)
	}
}

func TestRuntimeFailuresUseStderr(t *testing.T) {
	originalBroker := runControllerBroker
	originalAttachment := runTerminalAttachment
	t.Cleanup(func() {
		runControllerBroker = originalBroker
		runTerminalAttachment = originalAttachment
	})
	runControllerBroker = func(context.Context, controlledsession.ControllerBrokerOptionsV1) error {
		return errors.New("controlled-session controller broker requires Linux")
	}
	code, stdout, stderr := runCommand(t, "client")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "requires Linux") {
		t.Fatalf("client failure code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	runTerminalAttachment = func(context.Context, controlledsession.TerminalAttachmentOptionsV1) error {
		return errors.New("controlled-session terminal attachment requires Linux")
	}
	code, stdout, stderr = runCommand(t, "attach", "--socket=/run/reploy/terminal.sock")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "requires Linux") {
		t.Fatalf("attach failure code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func runCommand(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(t.Context(), args, strings.NewReader(""), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}
