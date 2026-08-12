//go:build linux

package cli

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func TestControlledSessionHostCancellationReceivesSIGTERM(t *testing.T) {
	host := makeControlledSessionHostCancellation()
	defer host.Stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-host.Context.Done():
		if code := host.ExitCode(); code != 143 {
			t.Fatalf("SIGTERM exit code = %d, want 143", code)
		}
	case <-time.After(time.Second):
		t.Fatal("host context did not cancel after SIGTERM")
	}
}
