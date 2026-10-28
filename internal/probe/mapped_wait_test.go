//go:build linux

package probe

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/mappedlaunch"
	"golang.org/x/sys/unix"
)

func mappedWaitBindingV1() mappedlaunch.Binding {
	d := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	return mappedlaunch.Binding{Schema: "mapped-release-v1", Installation: "installation", Operation: "operation", Generation: d, Revision: "1", Image: d, Role: "app", Plan: d}
}

func mappedWaitPipeV1(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	return r, w
}

func TestMappedWaitHandoffLeavesStdinAndClearsDeadlineV1(t *testing.T) {
	r, w := mappedWaitPipeV1(t)
	binding := mappedWaitBindingV1()
	frame, err := mappedlaunch.Frame(mappedlaunch.Request{Binding: binding, Container: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	canary := []byte("application\x00stdin\n")
	go func() { _, _ = w.Write(append(frame, canary...)); _ = w.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := waitMappedReleaseV1(ctx, r, binding); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, canary) {
		t.Fatalf("stdin: %q %v", got, err)
	}
}

func TestMappedWaitAbortsBeforeContinuationV1(t *testing.T) {
	for _, mode := range []string{"unbounded", "nil", "closed", "cancelled", "EOF", "partial", "malformed", "deadline", "cancel-during-read", "regular file"} {
		t.Run(mode, func(t *testing.T) {
			r, w := mappedWaitPipeV1(t)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			input := r
			switch mode {
			case "unbounded":
				ctx = t.Context()
			case "nil":
				input = nil
			case "closed":
				_ = input.Close()
			case "cancelled":
				cancel()
			case "EOF":
				_ = w.Close()
			case "partial":
				go func() { _, _ = w.Write([]byte{0, 0}); _ = w.Close() }()
			case "malformed":
				go func() { _, _ = w.Write([]byte{0, 0, 0, 0}) }()
			case "deadline":
				ctx, cancel = context.WithTimeout(t.Context(), 10*time.Millisecond)
				defer cancel()
			case "cancel-during-read":
				go func() { _, _ = w.Write([]byte{0}); cancel() }()
			case "regular file":
				f, err := os.CreateTemp(t.TempDir(), "input")
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				input = f
			}
			if err := waitMappedReleaseV1(ctx, input, mappedWaitBindingV1()); err == nil {
				t.Fatal("failed input released waiter")
			}
		})
	}
}

func TestMappedWaitRawDescriptorErrorsAndSocketInputV1(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	input := os.NewFile(uintptr(fds[0]), "mapped-stdin-socket")
	defer input.Close()
	defer unix.Close(fds[1])
	b := mappedWaitBindingV1()
	flags, err := unix.FcntlInt(uintptr(fds[0]), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	frame, _ := mappedlaunch.Frame(mappedlaunch.Request{Binding: b, Container: strings.Repeat("b", 64)})
	go func() { _, _ = unix.Write(fds[1], frame) }()
	if err := waitMappedReleaseV1(ctx, input, b); err != nil {
		t.Fatal("socket stdin handoff", err)
	}
	after, err := unix.FcntlInt(uintptr(fds[0]), unix.F_GETFL, 0)
	if err != nil || after != flags {
		t.Fatal("handoff changed application stdin flags", after, flags, err)
	}
	fd, err := unix.Dup(fds[0])
	if err != nil {
		t.Fatal(err)
	}
	_ = unix.Close(fd)
	if _, err := (mappedWaitInputV1{ctx, fd}).Read(make([]byte, 1)); err == nil {
		t.Fatal("closed raw stdin descriptor accepted")
	}
}

func TestMappedWaitFarFutureDeadlineStillObservesCancellationV1(t *testing.T) {
	r, w := mappedWaitPipeV1(t)
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().AddDate(500, 0, 0))
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- waitMappedReleaseV1(ctx, r, mappedWaitBindingV1()) }()
	time.AfterFunc(10*time.Millisecond, cancel)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("far-future cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		_ = w.Close()
		<-done
		t.Fatal("deadline arithmetic overflow blocked cancellation")
	}
}

func TestMappedProbeModeRetainsE2QualificationRefusalV1(t *testing.T) {
	b := mappedWaitBindingV1()
	encoded, _ := canonical.Marshal(b)
	deadline := time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano)
	args := []string{string(encoded), deadline, "sandbox-exec", "--uid", "200", "--gid", "200", "--public", "deny", "--local", "deny", "--ambiguous", "require-both", "--", "/bin/true"}
	for _, mode := range []string{"valid frame", "missing args", "bad binding", "bad deadline", "bad sandbox", "wrong command", "non-descriptor stdin"} {
		t.Run(mode, func(t *testing.T) {
			r, w := mappedWaitPipeV1(t)
			a := append([]string(nil), args...)
			var input io.Reader = r
			switch mode {
			case "missing args":
				a = nil
			case "bad binding":
				a[0] = "{}"
			case "bad deadline":
				a[1] = "bad"
			case "bad sandbox":
				a[3] = "--unknown"
			case "wrong command":
				a[2] = "verify-exec"
			case "non-descriptor stdin":
				input = bytes.NewReader(nil)
			}
			if mode == "valid frame" {
				frame, _ := mappedlaunch.Frame(mappedlaunch.Request{Binding: b, Container: strings.Repeat("b", 64)})
				go func() { _, _ = w.Write(frame); _ = w.Close() }()
			}
			var output bytes.Buffer
			if status := Main(append([]string{mappedlaunch.Command}, a...), input, &output, &output); status != 1 {
				t.Fatalf("unqualified mode status %d", status)
			}
			if mode == "valid frame" && !strings.Contains(output.String(), "qualified final exec transition") {
				t.Fatalf("unexpected refusal: %s", output.String())
			}
		})
	}
}

func TestMappedProbeInheritedStdinV1(t *testing.T) {
	if os.Getenv("REPLOY_TEST_MAPPED_STDIN") == "child" {
		os.Exit(Main(os.Args[3:], os.Stdin, os.Stdout, os.Stderr))
	}
	b := mappedWaitBindingV1()
	encoded, _ := canonical.Marshal(b)
	frame, _ := mappedlaunch.Frame(mappedlaunch.Request{Binding: b, Container: strings.Repeat("b", 64)})
	for _, mode := range []string{"valid", "partial", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			deadline := time.Now().Add(5 * time.Second)
			if mode == "deadline" {
				deadline = time.Now().Add(50 * time.Millisecond)
			}
			args := []string{"-test.run=^TestMappedProbeInheritedStdinV1$", "--", mappedlaunch.Command, string(encoded), deadline.UTC().Format(time.RFC3339Nano), "sandbox-exec", "--uid", "200", "--gid", "200", "--public", "deny", "--local", "deny", "--ambiguous", "require-both", "--", "/bin/true"}
			cmd := exec.CommandContext(t.Context(), os.Args[0], args...)
			cmd.Env = append(os.Environ(), "REPLOY_TEST_MAPPED_STDIN=child")
			var output bytes.Buffer
			cmd.Stdout = &output
			cmd.Stderr = &output
			var hold *os.File
			switch mode {
			case "valid":
				cmd.Stdin = bytes.NewReader(frame)
			case "partial":
				cmd.Stdin = bytes.NewReader(frame[:7])
			case "deadline":
				r, w := mappedWaitPipeV1(t)
				cmd.Stdin = r
				hold = w
			}
			err := cmd.Run()
			if hold != nil {
				_ = hold.Close()
			}
			want := map[string]string{"valid": "qualified final exec transition", "partial": "body", "deadline": "deadline exceeded"}[mode]
			if err == nil || !strings.Contains(output.String(), want) {
				t.Fatalf("inherited stdin %s: %v %s", mode, err, output.String())
			}
		})
	}
}
