//go:build linux

package probe

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestReadApplicationKernelStatusUsesCallingThread(t *testing.T) {
	type result struct {
		tid     int
		content []byte
		err     error
	}
	results := make(chan result, 2)
	release := make(chan struct{})
	done := make(chan struct{}, 2)
	defer func() {
		close(release)
		for range 2 {
			<-done
		}
	}()
	for range 2 {
		go func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			defer func() { done <- struct{}{} }()
			content, err := readApplicationKernelStatus()
			results <- result{tid: unix.Gettid(), content: content, err: err}
			<-release
		}()
	}

	observedNonLeader := false
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.tid != os.Getpid() {
			observedNonLeader = true
		}
		var statusPID string
		for _, line := range strings.Split(string(result.content), "\n") {
			name, raw, found := strings.Cut(line, ":")
			if found && name == "Pid" {
				fields := strings.Fields(raw)
				if len(fields) == 1 {
					statusPID = fields[0]
				}
				break
			}
		}
		if statusPID != strconv.Itoa(result.tid) {
			t.Fatalf("status Pid = %q, want calling thread %d", statusPID, result.tid)
		}
	}
	if !observedNonLeader {
		t.Fatal("test did not observe a non-leader OS thread")
	}
}

func TestApplicationKernelStatusFallbackPathUsesCallingThread(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	want := "/proc/self/task/" + strconv.Itoa(unix.Gettid()) + "/status"
	got, err := applicationKernelStatusFallbackPathV1()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("fallback path = %q, want %q", got, want)
	}
}
