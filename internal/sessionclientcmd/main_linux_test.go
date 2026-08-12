//go:build linux

package sessionclientcmd

import (
	"bytes"
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/omry/reploy/internal/controlledsession"
)

func TestMainCancelsAttachmentOnSIGTERM(t *testing.T) {
	original := runTerminalAttachment
	t.Cleanup(func() { runTerminalAttachment = original })
	started := make(chan struct{})
	runTerminalAttachment = func(ctx context.Context, _ controlledsession.TerminalAttachmentOptionsV1) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	result := make(chan int, 1)
	var stdout, stderr bytes.Buffer
	go func() {
		result <- Main([]string{"attach", "--socket", "/run/reploy/terminal.sock"}, bytes.NewReader(nil), &stdout, &stderr)
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
