//go:build linux

package controlledsession

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The PTY belongs to this test. The process beneath it is the real public
// attachment executable; no recorder changes its bytes or terminal behavior.
func TestTerminalAttachmentExecutablePTYV1(t *testing.T) {
	home := shortControllerBrokerTempHomeV1(t)
	client := filepath.Join(t.TempDir(), "reploy-session-client")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	linker := "github.com/omry/reploy/internal/controlledsession.controllerTerminalAttachmentHomeV1=" + home
	build := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-ldflags", "-X "+linker, "-o", client, "../../cmd/reploy-session-client")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build actual session client: %v\n%s", err, output)
	}
	for _, failed := range []bool{false, true} {
		name := "drained"
		if failed {
			name = "failed"
		}
		t.Run(name, func(t *testing.T) {
			runTerminalAttachmentExecutablePTYV1(t, ctx, client, home, failed)
		})
	}
}

func runTerminalAttachmentExecutablePTYV1(t *testing.T, parent context.Context, client, home string, failed bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	listener, socket := newControllerBrokerHostListenerV1(t)
	defer listener.Close()
	input := bytes.Repeat([]byte{0, 1, 3, 0x7f, 0xff, '\n', '\r'}, 9)
	output := append([]byte("DIRECT-PTY\r\n"), input...)
	output = append(output, bytes.Repeat([]byte{0, 1, 2, 0x7f, 0xff, 'a', 'z'}, 40*1024)...)
	output = append(output, []byte("\r\nDIRECT-PTY-END\r\n")...)
	steps := make(chan string, 2)
	hostDone := make(chan error, 1)
	go func() { hostDone <- runDirectPTYFixtureHostV1(ctx, listener, input, output, failed, steps) }()
	publicInput, publicWriter := io.Pipe()
	defer publicInput.Close()
	defer publicWriter.Close()
	publicReader, publicOutput := io.Pipe()
	defer publicReader.Close()
	brokerDone := make(chan error, 1)
	go func() {
		err := RunControllerBrokerV1(ctx, ControllerBrokerOptionsV1{
			SessionSocket: socket, TemporaryHome: home, Input: publicInput, Output: publicOutput,
		})
		_ = publicOutput.CloseWithError(err)
		brokerDone <- err
	}()
	public := bufio.NewReader(publicReader)
	ready := readControllerBrokerJSONLineV1(t, public)
	terminal, ok := ready["terminal_socket"].(string)
	if ready["type"] != string(ControllerStreamEventBrokerReadyV1) || !ok {
		t.Fatalf("broker ready = %#v", ready)
	}
	master, slave := openDirectFixturePTYV1(t)
	defer master.Close()
	defer slave.Close()
	initial, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: 80, Row: 24}); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command := exec.CommandContext(ctx, client, "attach", "--socket", terminal)
	command.Stdin, command.Stdout, command.Stderr = slave, slave, &stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer command.Process.Kill()
	// Read concurrently so a large final output cannot block the attachment.
	if deadline, ok := ctx.Deadline(); ok {
		if err := master.SetReadDeadline(deadline); err != nil {
			t.Fatal(err)
		}
	}
	captured := make([]byte, len(output))
	readDone := make(chan error, 1)
	go func() { _, err := io.ReadFull(master, captured); readDone <- err }()
	for _, kind := range []ControllerStreamEventKindV1{ControllerStreamEventOpenedV1, ControllerStreamEventReadyV1} {
		if event := readControllerBrokerJSONLineV1(t, public); event["type"] != string(kind) {
			t.Fatalf("public event = %#v, want %s", event, kind)
		}
	}
	waitStep := func(want string) {
		t.Helper()
		select {
		case got := <-steps:
			if got != want {
				t.Fatalf("host step = %q, want %q", got, want)
			}
		case err := <-hostDone:
			t.Fatalf("host stopped before %s: %v", want, err)
		case <-ctx.Done():
			t.Fatalf("waiting for %s: %v", want, ctx.Err())
		}
	}
	waitStep("initial resize")
	raw, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil || raw.Lflag&(unix.ECHO|unix.ICANON|unix.ISIG) != 0 || raw.Oflag&unix.OPOST != 0 {
		t.Fatalf("actual attachment did not enter raw/no-echo mode: %#v, %v", raw, err)
	}
	// Includes an ordinary Ctrl-C byte, NUL, non-UTF-8 and CR/LF. The host must
	// receive them in order without local canonical buffering or signal delivery.
	if _, err := master.Write(input); err != nil {
		t.Fatal(err)
	}
	waitStep("exact input")
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: 132, Row: 43}); err != nil {
		t.Fatal(err)
	}
	// TIOCSWINSZ signals the PTY's foreground process group. Sending another
	// SIGWINCH can deliver a duplicate resize after the fixture starts shutdown.
	for _, kind := range []ControllerStreamEventKindV1{ControllerStreamEventWorkloadExitV1, ControllerStreamEventTerminatingV1, ControllerStreamEventWorkloadOutputsFinalizedV1} {
		if event := readControllerBrokerJSONLineV1(t, public); event["type"] != string(kind) {
			t.Fatalf("public event = %#v, want %s", event, kind)
		}
	}
	waitErr := command.Wait()
	if failed {
		var exit *exec.ExitError
		if !errors.As(waitErr, &exit) || exit.ExitCode() != 1 || !strings.Contains(stderr.String(), "output finalization failed") {
			t.Fatalf("failed finalization exit = %v, stderr=%s", waitErr, stderr.String())
		}
	} else if waitErr != nil || stderr.Len() != 0 {
		t.Fatalf("drained exit = %v, stderr=%s", waitErr, stderr.String())
	}
	if err := <-readDone; err != nil {
		t.Fatalf("drain output: %v", err)
	}
	if !bytes.Equal(captured, output) {
		t.Fatalf("terminal bytes changed or echoed twice: got %d bytes, want %d", len(captured), len(output))
	}
	if err := master.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	extra := make([]byte, 1)
	if count, err := master.Read(extra); count != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("unexpected extra terminal output: %x, %v", extra[:count], err)
	}
	restored, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil || !reflect.DeepEqual(restored, initial) {
		t.Fatalf("terminal state not restored: got %#v, want %#v, %v", restored, initial, err)
	}
	artifact := filepath.Join(t.TempDir(), "terminal.raw")
	if err := os.WriteFile(artifact, captured, 0o600); err != nil {
		t.Fatal(err)
	}
	retained, err := os.ReadFile(artifact)
	if err != nil || !bytes.Equal(retained, output) {
		t.Fatalf("closed terminal artifact did not retain exact output: %v", err)
	}
	select {
	case err := <-brokerDone:
		t.Fatalf("broker exited before artifact finalization and complete: %v", err)
	default:
	}
	writeControllerBrokerPublicRequestV1(t, publicWriter, ControllerStreamRequestCompleteV1, 0, 0)
	if event := readControllerBrokerJSONLineV1(t, public); event["type"] != string(ControllerStreamEventTerminatedV1) {
		t.Fatalf("terminated = %#v", event)
	}
	select {
	case err := <-brokerDone:
		t.Fatalf("broker exited before acknowledgement: %v", err)
	default:
	}
	writeControllerBrokerPublicRequestV1(t, publicWriter, ControllerStreamRequestAcknowledgeTerminatedV1, 0, 0)
	if err := <-brokerDone; err != nil {
		t.Fatal(err)
	}
	if err := <-hostDone; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(terminal)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("broker-owned terminal directory survived cleanup: %v", err)
	}
}

func openDirectFixturePTYV1(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	master := os.NewFile(uintptr(fd), "direct-fixture-pty")
	t.Cleanup(func() { _ = master.Close() })
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	return master, slave
}

func runDirectPTYFixtureHostV1(ctx context.Context, listener *net.UnixListener, input, output []byte, failed bool, steps chan<- string) error {
	connection, err := listener.AcceptUnix()
	if err != nil {
		return err
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	for _, event := range []EventV1{{Kind: EventOpenedV1, Opened: pointerToOpenedV1(testOpenedV1())}, {Kind: EventReadyV1}} {
		if err := WriteEventV1(connection, event); err != nil {
			return err
		}
	}
	request, err := ReadRequestV1(connection)
	if err != nil || request.Kind != RequestResizeV1 || request.Columns != 80 || request.Rows != 24 {
		return errors.Join(err, fmt.Errorf("initial dimensions = %#v, want80x24", request))
	}
	steps <- "initial resize"
	var received []byte
	for len(received) < len(input) {
		request, err = ReadRequestV1(connection)
		if err != nil {
			return err
		}
		if request.Kind != RequestInputV1 {
			return fmt.Errorf("input request = %#v", request)
		}
		received = append(received, request.Bytes...)
	}
	if !bytes.Equal(received, input) {
		return fmt.Errorf("input bytes = %x, want%x", received, input)
	}
	steps <- "exact input"
	request, err = ReadRequestV1(connection)
	if err != nil || request.Kind != RequestResizeV1 || request.Columns != 132 || request.Rows != 43 {
		return errors.Join(err, fmt.Errorf("changed dimensions = %#v, want132x43", request))
	}
	for offset := 0; offset < len(output); offset += 16 * 1024 {
		end := min(offset+16*1024, len(output))
		if err := WriteEventV1(connection, EventV1{Kind: EventOutputV1, Bytes: output[offset:end]}); err != nil {
			return err
		}
	}
	code := 0
	status := WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1}
	if failed {
		status = WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationFailedV1, Reason: "direct fixture observation failed"}
	}
	result := ResultV1{Cause: CauseWorkloadExitV1, WorkloadStatus: ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code}, WorkloadOutputFinalizationStatus: status, RuntimeObservationStatus: RuntimeObservationStatusV1{Kind: RuntimeObservationMaintainedV1}, ControllerFinalizationStatus: ControllerFinalizationStatusV1{Kind: ControllerFinalizationCompletedV1}, CleanupStatus: CleanupStatusV1{Kind: CleanupStatusSucceededV1}, RecoveryAction: RecoveryNoneV1}
	for _, event := range []EventV1{{Kind: EventWorkloadExitV1, WorkloadExit: &WorkloadExitV1{Status: result.WorkloadStatus}}, {Kind: EventTerminatingV1, Terminating: &TerminatingV1{Cause: result.Cause}}, {Kind: EventWorkloadOutputsFinalizedV1, WorkloadOutputsFinalized: &WorkloadOutputsFinalizedV1{Status: status.Kind, Reason: status.Reason}}} {
		if err := WriteEventV1(connection, event); err != nil {
			return err
		}
	}
	request, err = ReadRequestV1(connection)
	if err != nil || request.Kind != RequestCompleteV1 {
		return errors.Join(err, fmt.Errorf("expected complete, got%#v", request))
	}
	if err := WriteEventV1(connection, EventV1{Kind: EventTerminatedV1, Terminated: &result}); err != nil {
		return err
	}
	request, err = ReadRequestV1(connection)
	if err != nil || request.Kind != RequestAcknowledgeTerminatedV1 {
		return errors.Join(err, fmt.Errorf("expected acknowledgement, got%#v", request))
	}
	return nil
}
