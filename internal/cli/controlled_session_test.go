package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/controlledsession"
)

func TestControlledSessionHelpAndUsage(t *testing.T) {
	code, stdout, stderr := runCLI("controlled-session", "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage: reploy controlled-session COMMAND") || !strings.Contains(stdout, "attach --socket PATH") {
		t.Fatalf("help code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI("controlled-session", "client", "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage: reploy controlled-session client") || !strings.Contains(stdout, "REPLOY_SESSION_SOCKET") {
		t.Fatalf("client help code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI("controlled-session", "attach", "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage: reploy controlled-session attach --socket PATH") {
		t.Fatalf("attach help code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI("controlled-session")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "expected client or attach") || !strings.Contains(stderr, "Usage: reploy controlled-session {client | attach --socket PATH}") {
		t.Fatalf("usage code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, args := range [][]string{
		{"controlled-session", "attach"},
		{"controlled-session", "attach", "--socket"},
		{"controlled-session", "attach", "--socket="},
		{"controlled-session", "attach", "--socket", "/tmp/socket", "extra"},
	} {
		code, stdout, stderr = runCLI(args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "controlled-session attach usage error") {
			t.Fatalf("attach usage args=%q code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	code, stdout, stderr = runCLI("--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "controlled-session") {
		t.Fatalf("top-level help code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestControlledSessionAttachDispatchesTerminalAttachment(t *testing.T) {
	original := runControlledSessionAttachment
	t.Cleanup(func() { runControlledSessionAttachment = original })
	called := false
	runControlledSessionAttachment = func(ctx context.Context, options controlledsession.TerminalAttachmentOptionsV1) error {
		called = true
		if ctx == nil || ctx.Done() == nil || options.SocketPath != "/mnt/reploy-home/reploy-controlled-session-00000000000000000000000000000000/terminal.sock" || options.Input == nil || options.Output == nil {
			t.Fatalf("attachment options = %#v", options)
		}
		return nil
	}
	code, stdout, stderr := runCLI("controlled-session", "attach", "--socket", "/mnt/reploy-home/reploy-controlled-session-00000000000000000000000000000000/terminal.sock")
	if code != 0 || stdout != "" || stderr != "" || !called {
		t.Fatalf("attach code=%d stdout=%q stderr=%q called=%t", code, stdout, stderr, called)
	}
}

func TestControlledSessionAttachReportsRuntimeFailureOnlyOnStderr(t *testing.T) {
	original := runControlledSessionAttachment
	t.Cleanup(func() { runControlledSessionAttachment = original })
	runControlledSessionAttachment = func(context.Context, controlledsession.TerminalAttachmentOptionsV1) error {
		return errors.New("controlled-session terminal attachment requires Linux")
	}
	code, stdout, stderr := runCLI("controlled-session", "attach", "--socket=/mnt/reploy-home/reploy-controlled-session-00000000000000000000000000000000/terminal.sock")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "requires Linux") {
		t.Fatalf("failure code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestControlledSessionClientDispatchesEmbeddedBroker(t *testing.T) {
	original := runControlledSessionBroker
	t.Cleanup(func() { runControlledSessionBroker = original })
	t.Setenv("REPLOY_SESSION_SOCKET", "/run/reploy/session.sock")
	called := false
	runControlledSessionBroker = func(ctx context.Context, options controlledsession.ControllerBrokerOptionsV1) error {
		called = true
		if ctx == nil || ctx.Done() == nil || options.SessionSocket != "/run/reploy/session.sock" || options.TemporaryHome != controlledsession.ControllerTemporaryHomeV1 || options.Input == nil || options.Output == nil {
			t.Fatalf("broker options = %#v", options)
		}
		return nil
	}
	code, stdout, stderr := runCLI("controlled-session", "client")
	if code != 0 || stdout != "" || stderr != "" || !called {
		t.Fatalf("client code=%d stdout=%q stderr=%q called=%t", code, stdout, stderr, called)
	}
}

func TestControlledSessionClientReportsRuntimeFailureOnlyOnStderr(t *testing.T) {
	original := runControlledSessionBroker
	t.Cleanup(func() { runControlledSessionBroker = original })
	runControlledSessionBroker = func(context.Context, controlledsession.ControllerBrokerOptionsV1) error {
		return errors.New("controlled-session controller broker requires Linux")
	}
	code, stdout, stderr := runCLI("controlled-session", "client")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "requires Linux") {
		t.Fatalf("failure code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
