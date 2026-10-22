//go:build linux

package probe

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const privateEnvironmentHomeV1 = "/mnt/reploy-home"
const privateEnvironmentLimitV1 = 1024 * 1024
const privateEnvironmentDeadlineV1 = 30 * time.Second

// Both channels exist only before application exec. No application-owned
// file is used as evidence of a completed startup.
func privateEnvironmentChannelsV1(home string) (string, string) {
	return home + "/.reploy-private-environment", home + "/.reploy-private-launch"
}

func openPrivateEnvironmentFIFOV1(path string, flags int, deadline time.Time) (int, error) {
	for {
		fd, err := unix.Open(path, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if err == nil {
			var stat unix.Stat_t
			if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFIFO || stat.Mode&0o7777 != 0o600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
				_ = unix.Close(fd)
				return -1, fmt.Errorf("private startup channel has unsafe identity")
			}
			return fd, nil
		}
		if !errors.Is(err, unix.ENOENT) && !errors.Is(err, unix.ENXIO) {
			return -1, fmt.Errorf("open private startup channel: %w", err)
		}
		if err := waitPrivateEnvironmentV1(deadline); err != nil {
			return -1, err
		}
	}
}

func waitPrivateEnvironmentV1(deadline time.Time) error {
	if !time.Now().Before(deadline) {
		return fmt.Errorf("private startup handshake exceeded its deadline")
	}
	time.Sleep(10 * time.Millisecond)
	return nil
}

func writePrivateEnvironmentV1(fd int, content []byte, deadline time.Time) error {
	for len(content) != 0 {
		n, err := unix.Write(fd, content)
		if n > 0 {
			content = content[n:]
		}
		if err != nil && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			return fmt.Errorf("write private startup channel: %w", err)
		}
		if len(content) != 0 {
			if err := waitPrivateEnvironmentV1(deadline); err != nil {
				return err
			}
		}
	}
	return nil
}

func readPrivateEnvironmentV1(fd int, limit int, deadline time.Time, complete func([]byte, bool) (bool, error)) ([]byte, error) {
	content := []byte{}
	buffer := make([]byte, 4096)
	for {
		n, err := unix.Read(fd, buffer)
		if n > 0 {
			content = append(content, buffer[:n]...)
		}
		if len(content) > limit {
			return nil, fmt.Errorf("private startup channel exceeded its size limit")
		}
		if err != nil && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			return nil, fmt.Errorf("read private startup channel: %w", err)
		}
		done, checkErr := complete(content, n == 0 && err == nil)
		if checkErr != nil {
			return nil, checkErr
		}
		if done {
			return content, nil
		}
		if err := waitPrivateEnvironmentV1(deadline); err != nil {
			return nil, err
		}
	}
}

func privateEnvironmentAssignmentsV1(content []byte) ([]string, error) {
	if len(content) > privateEnvironmentLimitV1 || !utf8.Valid(content) || !bytes.HasSuffix(content, []byte("\n\n")) || bytes.ContainsAny(content, "\x00\r") {
		return nil, fmt.Errorf("private startup payload is invalid")
	}
	assignments := strings.Split(string(content[:len(content)-2]), "\n")
	seen := map[string]bool{}
	for _, assignment := range assignments {
		name, _, ok := strings.Cut(assignment, "=")
		if !ok || name == "" || seen[name] {
			return nil, fmt.Errorf("private startup payload is invalid")
		}
		for index, char := range []byte(name) {
			if char != '_' && (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') && (index == 0 || char < '0' || char > '9') {
				return nil, fmt.Errorf("private startup payload is invalid")
			}
		}
		seen[name] = true
	}
	return assignments, nil
}

func launchPrivateWorkloadEnvironmentV1(argv []string, environment []string) error {
	return launchPrivateWorkloadEnvironmentWithV1(privateEnvironmentHomeV1, argv, environment, privateEnvironmentDeadlineV1,
		func(argv, env []string, ready func() error) error {
			return verifyAndExecApplication(argv, readApplicationKernelStatus, func(argv []string) error {
				if err := ready(); err != nil {
					return err
				}
				return syscall.Exec(argv[0], argv, env)
			})
		})
}

func launchPrivateWorkloadEnvironmentWithV1(home string, argv, environment []string, timeout time.Duration, exec func([]string, []string, func() error) error) error {
	deadline := time.Now().Add(timeout)
	payloadPath, ackPath := privateEnvironmentChannelsV1(home)
	for _, path := range []string{payloadPath, ackPath} {
		if err := unix.Mkfifo(path, 0o600); err != nil {
			return fmt.Errorf("create private startup channel: %w", err)
		}
		defer os.Remove(path)
	}
	payloadFD, err := openPrivateEnvironmentFIFOV1(payloadPath, unix.O_RDONLY, deadline)
	if err != nil {
		return err
	}
	defer unix.Close(payloadFD)
	payload, err := readPrivateEnvironmentV1(payloadFD, privateEnvironmentLimitV1, deadline, func(content []byte, _ bool) (bool, error) {
		return bytes.HasSuffix(content, []byte("\n\n")), nil
	})
	if err != nil {
		return err
	}
	assignments, err := privateEnvironmentAssignmentsV1(payload)
	if err != nil {
		return err
	}
	ackFD, err := openPrivateEnvironmentFIFOV1(ackPath, unix.O_WRONLY, deadline)
	if err != nil {
		return err
	}
	defer unix.Close(ackFD)
	// Existing descriptors complete the one-shot handshake. Removing names
	// prevents a second receiver or writer from joining this startup.
	if err := os.Remove(payloadPath); err != nil {
		return err
	}
	if err := os.Remove(ackPath); err != nil {
		return err
	}
	merged := append([]string(nil), environment...)
	for _, assignment := range assignments {
		name, _, _ := strings.Cut(assignment, "=")
		filtered := merged[:0]
		for _, existing := range merged {
			if !strings.HasPrefix(existing, name+"=") {
				filtered = append(filtered, existing)
			}
		}
		merged = append(filtered, assignment)
	}
	null, err := unix.Open("/dev/null", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open private application stdin")
	}
	if null != 0 {
		if err := unix.Dup2(null, 0); err != nil {
			_ = unix.Close(null)
			return fmt.Errorf("redirect private application stdin")
		}
		_ = unix.Close(null)
	} else if _, err := unix.FcntlInt(uintptr(null), unix.F_SETFD, 0); err != nil {
		return fmt.Errorf("prepare private application stdin")
	}
	err = exec(argv, merged, func() error { return writePrivateEnvironmentV1(ackFD, []byte("ready\n"), deadline) })
	// Only exec closes the CLOEXEC descriptor without a failure record.
	_ = writePrivateEnvironmentV1(ackFD, []byte("failed\n"), deadline)
	if err == nil {
		return fmt.Errorf("private application exec unexpectedly returned")
	}
	return fmt.Errorf("private application launch failed: %w", err)
}

func relayPrivateWorkloadEnvironmentV1(stdin io.Reader) error {
	return relayPrivateWorkloadEnvironmentWithV1(privateEnvironmentHomeV1, stdin, privateEnvironmentDeadlineV1)
}

func relayPrivateWorkloadEnvironmentWithV1(home string, stdin io.Reader, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	type inputResult struct {
		payload []byte
		err     error
	}
	input := make(chan inputResult, 1)
	go func() {
		payload, err := io.ReadAll(io.LimitReader(stdin, privateEnvironmentLimitV1+1))
		input <- inputResult{payload: payload, err: err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var payload []byte
	var err error
	select {
	case result := <-input:
		payload, err = result.payload, result.err
	case <-timer.C:
		return fmt.Errorf("private startup payload exceeded its deadline")
	}
	if err != nil {
		return fmt.Errorf("read private startup payload")
	}
	if _, err := privateEnvironmentAssignmentsV1(payload); err != nil {
		return err
	}
	payloadPath, ackPath := privateEnvironmentChannelsV1(home)
	ackFD, err := openPrivateEnvironmentFIFOV1(ackPath, unix.O_RDONLY, deadline)
	if err != nil {
		return err
	}
	defer unix.Close(ackFD)
	payloadFD, err := openPrivateEnvironmentFIFOV1(payloadPath, unix.O_WRONLY, deadline)
	if err != nil {
		return err
	}
	if err := writePrivateEnvironmentV1(payloadFD, payload, deadline); err != nil {
		_ = unix.Close(payloadFD)
		return err
	}
	_ = unix.Close(payloadFD)
	_, err = readPrivateEnvironmentV1(ackFD, 32, deadline, func(content []byte, eof bool) (bool, error) {
		if bytes.Contains(content, []byte("failed\n")) {
			return false, fmt.Errorf("private application launch failed")
		}
		if len(content) != 0 && !bytes.HasPrefix([]byte("ready\n"), content) {
			return false, fmt.Errorf("invalid private launch acknowledgement")
		}
		return eof && string(content) == "ready\n", nil
	})
	return err
}
