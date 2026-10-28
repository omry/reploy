//go:build linux

package probe

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/omry/reploy/internal/mappedlaunch"
	"golang.org/x/sys/unix"
)

// Poll the inherited descriptor directly: os.Stdin need not be registered with
// Go's deadline poller. Do not buffer or alter flags on application stdin.
func waitMappedReleaseV1(ctx context.Context, input *os.File, binding mappedlaunch.Binding) error {
	deadline, ok := ctx.Deadline()
	if !ok || input == nil {
		return fmt.Errorf("mapped waiter requires bounded descriptor input")
	}
	raw, err := input.SyscallConn()
	if err != nil {
		return err
	}
	var fd int
	if err := raw.Control(func(value uintptr) { fd = int(value) }); err != nil {
		return err
	}
	var status unix.Stat_t
	if err := unix.Fstat(fd, &status); err != nil || status.Mode&unix.S_IFMT != unix.S_IFIFO && status.Mode&unix.S_IFMT != unix.S_IFSOCK {
		return fmt.Errorf("mapped waiter requires non-TTY pipe or socket stdin")
	}
	_, err = mappedlaunch.Read(mappedWaitInputV1{ctx, fd}, binding)
	runtime.KeepAlive(input)
	if err == nil && !time.Now().Before(deadline) {
		err = context.DeadlineExceeded
	}
	if err == nil {
		err = ctx.Err()
	}
	return err
}

type mappedWaitInputV1 struct {
	ctx context.Context
	fd  int
}

func (r mappedWaitInputV1) Read(data []byte) (int, error) {
	deadline, _ := r.ctx.Deadline()
	for {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, context.DeadlineExceeded
		}
		poll := []unix.PollFd{{Fd: int32(r.fd), Events: unix.POLLIN}}
		n, err := unix.Poll(poll, int((min(remaining, 100*time.Millisecond)+time.Millisecond-1)/time.Millisecond))
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return 0, err
		}
		if n == 0 {
			continue
		}
		if poll[0].Revents&(unix.POLLERR|unix.POLLNVAL) != 0 {
			return 0, fmt.Errorf("mapped stdin disconnected")
		}
		// The trusted waiter is the sole reader before handoff. Read only the
		// requested header/body bytes; no app callback can consume readiness.
		n, err = unix.Read(r.fd, data)
		if err == unix.EINTR || err == unix.EAGAIN {
			continue
		}
		if n == 0 && err == nil {
			err = io.EOF
		}
		return n, err
	}
}

func mappedWaitV1(args []string, input *os.File) error {
	if len(args) < 3 || args[2] != "sandbox-exec" {
		return fmt.Errorf("mapped waiter requires fixed binding and admission deadline")
	}
	if _, err := parseSandboxExecPlanV1(args[3:], true); err != nil {
		return err
	}
	binding, err := mappedlaunch.DecodeBinding([]byte(args[0]))
	if err != nil {
		return err
	}
	deadline, err := time.Parse(time.RFC3339Nano, args[1])
	if err != nil {
		return fmt.Errorf("invalid mapped admission deadline")
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	if err := waitMappedReleaseV1(ctx, input, binding); err != nil {
		return err
	}
	// E2 owns the qualified credential/descriptor/direct-exec tail. An accepted
	// frame alone cannot substitute for that proof or execute an ordinary path.
	return fmt.Errorf("mapped startup requires a qualified final exec transition")
}
