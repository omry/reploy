//go:build linux

package cli

import (
	"bytes"
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/omry/reploy/internal/controlledsession"
)

func TestControlledSessionAttachCancelsOnSIGTERM(t *testing.T) {
	original := runControlledSessionAttachment
	t.Cleanup(func() { runControlledSessionAttachment = original })
	started := make(chan struct{})
	runControlledSessionAttachment = func(ctx context.Context, _ controlledsession.TerminalAttachmentOptionsV1) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	result := make(chan int, 1)
	var stdout, stderr bytes.Buffer
	go func() {
		result <- runControlledSession([]string{
			"attach",
			"--socket",
			"/mnt/reploy-home/reploy-controlled-session-00000000000000000000000000000000/terminal.sock",
		}, &stdout, &stderr)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("attachment did not start")
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-result:
		if code != 1 {
			t.Fatalf("SIGTERM exit code = %d, want 1", code)
		}
	case <-time.After(time.Second):
		t.Fatal("attachment did not cancel after SIGTERM")
	}
}
