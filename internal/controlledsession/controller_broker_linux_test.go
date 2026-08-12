//go:build linux

package controlledsession

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunControllerBrokerV1CompletesAcknowledgedSession(t *testing.T) {
	hostListener, hostSocket := newControllerBrokerHostListenerV1(t)
	defer hostListener.Close()
	publicInputReader, publicInputWriter := io.Pipe()
	defer publicInputWriter.Close()
	publicOutputReader, publicOutputWriter := io.Pipe()
	defer publicOutputReader.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hostDone := make(chan error, 1)
	go runControllerBrokerTestHostV1(hostListener, hostDone)
	brokerDone := make(chan error, 1)
	temporaryHome := shortControllerBrokerTempHomeV1(t)
	go func() {
		err := RunControllerBrokerV1(ctx, ControllerBrokerOptionsV1{
			SessionSocket: hostSocket,
			TemporaryHome: temporaryHome,
			Input:         publicInputReader,
			Output:        publicOutputWriter,
		})
		_ = publicOutputWriter.CloseWithError(err)
		brokerDone <- err
	}()
	public := bufio.NewReader(publicOutputReader)
	brokerReady := readControllerBrokerJSONLineV1(t, public)
	if brokerReady["type"] != string(ControllerStreamEventBrokerReadyV1) {
		t.Fatalf("first public event = %#v", brokerReady)
	}
	terminalSocket, ok := brokerReady["terminal_socket"].(string)
	if !ok || filepath.Dir(filepath.Dir(terminalSocket)) != temporaryHome {
		t.Fatalf("terminal socket = %#v", brokerReady["terminal_socket"])
	}
	opened := readControllerBrokerJSONLineV1(t, public)
	if opened["type"] != string(ControllerStreamEventOpenedV1) || opened["authorization"] != nil {
		t.Fatalf("opened public event = %#v", opened)
	}
	terminal, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: terminalSocket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	if event := readControllerBrokerJSONLineV1(t, public); event["type"] != string(ControllerStreamEventReadyV1) {
		t.Fatalf("ready public event = %#v", event)
	}
	terminalOutput, err := ReadTerminalEventV1(terminal)
	if err != nil || terminalOutput.Kind != TerminalEventOutputV1 || string(terminalOutput.Bytes) != "hello from workload" {
		t.Fatalf("terminal output = %#v, %v", terminalOutput, err)
	}
	for _, kind := range []ControllerStreamEventKindV1{ControllerStreamEventWorkloadExitV1, ControllerStreamEventTerminatingV1, ControllerStreamEventWorkloadOutputsFinalizedV1} {
		if event := readControllerBrokerJSONLineV1(t, public); event["type"] != string(kind) {
			t.Fatalf("public event = %#v, want %q", event, kind)
		}
	}
	terminalEnd, err := ReadTerminalEventV1(terminal)
	if err != nil || terminalEnd.Kind != TerminalEventEndV1 || terminalEnd.Status == nil || terminalEnd.Status.Kind != WorkloadOutputFinalizationDrainedV1 {
		t.Fatalf("terminal end = %#v, %v", terminalEnd, err)
	}
	writeControllerBrokerPublicRequestV1(t, publicInputWriter, ControllerStreamRequestCompleteV1, 0, 0)
	terminated := readControllerBrokerJSONLineV1(t, public)
	if terminated["type"] != string(ControllerStreamEventTerminatedV1) || terminated["result"] == nil {
		t.Fatalf("terminated public event = %#v", terminated)
	}
	writeControllerBrokerPublicRequestV1(t, publicInputWriter, ControllerStreamRequestAcknowledgeTerminatedV1, 0, 0)
	select {
	case err := <-brokerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := <-hostDone; err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(temporaryHome)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary home after broker exit = %#v, %v", entries, err)
	}
}

func TestRunControllerBrokerV1TimesOutWaitingForAttachment(t *testing.T) {
	hostListener, hostSocket := newControllerBrokerHostListenerV1(t)
	defer hostListener.Close()
	hostDone := make(chan error, 1)
	go func() {
		connection, err := hostListener.AcceptUnix()
		if err != nil {
			hostDone <- err
			return
		}
		defer connection.Close()
		if err := WriteEventV1(connection, EventV1{Kind: EventOpenedV1, Opened: pointerToOpenedV1(testOpenedV1())}); err != nil {
			hostDone <- err
			return
		}
		_, err = ReadRequestV1(connection)
		hostDone <- err
	}()
	var output bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	temporaryHome := shortControllerBrokerTempHomeV1(t)
	err := RunControllerBrokerV1(ctx, ControllerBrokerOptionsV1{
		SessionSocket: hostSocket,
		TemporaryHome: temporaryHome,
		Input:         strings.NewReader(""),
		Output:        &output,
		AttachTimeout: 20 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("attach timeout error = %v", err)
	}
	if !strings.Contains(output.String(), `"type":"client-error"`) || !strings.Contains(output.String(), `"code":"attach_timeout"`) {
		t.Fatalf("attach timeout output = %q", output.String())
	}
	if entries, readErr := os.ReadDir(temporaryHome); readErr != nil || len(entries) != 0 {
		t.Fatalf("temporary home after timeout = %#v, %v", entries, readErr)
	}
	select {
	case <-hostDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestRunControllerBrokerV1StartsAttachmentDeadlineAtBrokerReady(t *testing.T) {
	hostListener, hostSocket := newControllerBrokerHostListenerV1(t)
	defer hostListener.Close()
	hostDone := make(chan struct{})
	go func() {
		defer close(hostDone)
		connection, err := hostListener.AcceptUnix()
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = io.Copy(io.Discard, connection)
	}()
	var output bytes.Buffer
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := RunControllerBrokerV1(ctx, ControllerBrokerOptionsV1{
		SessionSocket: hostSocket,
		TemporaryHome: shortControllerBrokerTempHomeV1(t),
		Input:         strings.NewReader(""),
		Output:        &output,
		AttachTimeout: 50 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("attachment deadline error = %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 200*time.Millisecond {
		t.Fatalf("attachment deadline began too late: %s", elapsed)
	}
	if !strings.Contains(output.String(), `"code":"attach_timeout"`) {
		t.Fatalf("attachment deadline output = %q", output.String())
	}
	<-hostDone
}

func TestRunControllerBrokerV1FailsWhenAttachmentIsLost(t *testing.T) {
	hostListener, hostSocket := newControllerBrokerHostListenerV1(t)
	defer hostListener.Close()
	hostDone := make(chan struct{})
	go func() {
		defer close(hostDone)
		connection, err := hostListener.AcceptUnix()
		if err != nil {
			return
		}
		defer connection.Close()
		_ = WriteEventV1(connection, EventV1{Kind: EventOpenedV1, Opened: pointerToOpenedV1(testOpenedV1())})
		_ = WriteEventV1(connection, EventV1{Kind: EventReadyV1})
		_, _ = ReadRequestV1(connection)
	}()
	publicInputReader, publicInputWriter := io.Pipe()
	defer publicInputWriter.Close()
	publicOutputReader, publicOutputWriter := io.Pipe()
	defer publicOutputReader.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	brokerDone := make(chan error, 1)
	temporaryHome := shortControllerBrokerTempHomeV1(t)
	go func() {
		err := RunControllerBrokerV1(ctx, ControllerBrokerOptionsV1{
			SessionSocket: hostSocket,
			TemporaryHome: temporaryHome,
			Input:         publicInputReader,
			Output:        publicOutputWriter,
		})
		_ = publicOutputWriter.CloseWithError(err)
		brokerDone <- err
	}()
	public := bufio.NewReader(publicOutputReader)
	brokerReady := readControllerBrokerJSONLineV1(t, public)
	_ = readControllerBrokerJSONLineV1(t, public)
	terminal, err := net.Dial("unix", brokerReady["terminal_socket"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if err := terminal.Close(); err != nil {
		t.Fatal(err)
	}
	for {
		event := readControllerBrokerJSONLineV1(t, public)
		if event["type"] == string(ControllerStreamEventClientErrorV1) {
			if event["code"] != "terminal_attachment_error" {
				t.Fatalf("socket-loss event = %#v", event)
			}
			break
		}
	}
	select {
	case err := <-brokerDone:
		if err == nil {
			t.Fatal("attachment loss returned nil")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	<-hostDone
}

func TestControllerBrokerStateV1RejectsPrematureDuplicateAndUnauthorizedRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &controllerBrokerFakeSessionV1{}
	state := controllerBrokerStateV1{operations: map[OperationV1]bool{OperationCompleteV1: true, OperationTerminateV1: true}}
	publicComplete := controllerBrokerRequestV1{source: controllerBrokerRequestPublicV1, request: RequestV1{Kind: RequestCompleteV1}}
	if err := state.applyRequest(ctx, session, publicComplete); err == nil || !strings.Contains(err.Error(), "before ready") {
		t.Fatalf("premature complete error = %v", err)
	}
	state.ready = true
	unauthorizedResize := controllerBrokerRequestV1{source: controllerBrokerRequestPublicV1, request: RequestV1{Kind: RequestResizeV1, Columns: 80, Rows: 24}}
	if err := state.applyRequest(ctx, session, unauthorizedResize); err == nil || !strings.Contains(err.Error(), "not granted") {
		t.Fatalf("unauthorized resize error = %v", err)
	}
	state.terminating = true
	state.outputsFinalized = true
	if err := state.applyRequest(ctx, session, publicComplete); err != nil {
		t.Fatal(err)
	}
	if err := state.applyRequest(ctx, session, publicComplete); err == nil || !strings.Contains(err.Error(), "once") {
		t.Fatalf("duplicate complete error = %v", err)
	}
	state.terminated = true
	ack := controllerBrokerRequestV1{source: controllerBrokerRequestPublicV1, request: RequestV1{Kind: RequestAcknowledgeTerminatedV1}}
	if err := state.applyRequest(ctx, session, ack); err != nil {
		t.Fatal(err)
	}
	if err := state.applyRequest(ctx, session, ack); err == nil || !strings.Contains(err.Error(), "exactly once") {
		t.Fatalf("duplicate acknowledgement error = %v", err)
	}
	if got := session.requestKinds(); strings.Join(got, ",") != "complete,acknowledge-terminated" {
		t.Fatalf("forwarded requests = %#v", got)
	}
}

type controllerBrokerFakeSessionV1 struct {
	mu       sync.Mutex
	requests []RequestV1
}

func (*controllerBrokerFakeSessionV1) Opened() OpenedV1 { return testOpenedV1() }
func (*controllerBrokerFakeSessionV1) ReadEvent(context.Context) (EventV1, error) {
	return EventV1{}, io.EOF
}
func (session *controllerBrokerFakeSessionV1) WriteRequest(_ context.Context, request RequestV1) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.requests = append(session.requests, request)
	return nil
}
func (*controllerBrokerFakeSessionV1) Close() error { return nil }
func (session *controllerBrokerFakeSessionV1) requestKinds() []string {
	session.mu.Lock()
	defer session.mu.Unlock()
	result := make([]string, len(session.requests))
	for index, request := range session.requests {
		result[index] = string(request.Kind)
	}
	return result
}

func newControllerBrokerHostListenerV1(t *testing.T) (*net.UnixListener, string) {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "rph-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "host.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	return listener, socket
}

func shortControllerBrokerTempHomeV1(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "rpb-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	return home
}

func runControllerBrokerTestHostV1(listener *net.UnixListener, done chan<- error) {
	connection, err := listener.AcceptUnix()
	if err != nil {
		done <- err
		return
	}
	defer connection.Close()
	code := 0
	result := ResultV1{
		Cause:                            CauseWorkloadExitV1,
		WorkloadStatus:                   ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code},
		WorkloadOutputFinalizationStatus: WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1},
		RuntimeObservationStatus:         RuntimeObservationStatusV1{Kind: RuntimeObservationMaintainedV1},
		ControllerFinalizationStatus:     ControllerFinalizationStatusV1{Kind: ControllerFinalizationCompletedV1},
		CleanupStatus:                    CleanupStatusV1{Kind: CleanupStatusSucceededV1},
		RecoveryAction:                   RecoveryNoneV1,
	}
	events := []EventV1{
		{Kind: EventOpenedV1, Opened: pointerToOpenedV1(testOpenedV1())},
		{Kind: EventOutputV1, Bytes: []byte("hello from workload")},
		{Kind: EventReadyV1},
		{Kind: EventWorkloadExitV1, WorkloadExit: &WorkloadExitV1{Status: result.WorkloadStatus}},
		{Kind: EventTerminatingV1, Terminating: &TerminatingV1{Cause: CauseWorkloadExitV1}},
		{Kind: EventWorkloadOutputsFinalizedV1, WorkloadOutputsFinalized: &WorkloadOutputsFinalizedV1{Status: WorkloadOutputFinalizationDrainedV1}},
	}
	for _, event := range events {
		if err := WriteEventV1(connection, event); err != nil {
			done <- err
			return
		}
	}
	request, err := ReadRequestV1(connection)
	if err != nil || request.Kind != RequestCompleteV1 {
		done <- errors.Join(err, errors.New("host expected complete request"))
		return
	}
	if err := WriteEventV1(connection, EventV1{Kind: EventTerminatedV1, Terminated: &result}); err != nil {
		done <- err
		return
	}
	request, err = ReadRequestV1(connection)
	if err != nil || request.Kind != RequestAcknowledgeTerminatedV1 {
		done <- errors.Join(err, errors.New("host expected acknowledge-terminated request"))
		return
	}
	done <- nil
}

func readControllerBrokerJSONLineV1(t *testing.T, reader *bufio.Reader) map[string]any {
	t.Helper()
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(line, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func writeControllerBrokerPublicRequestV1(t *testing.T, writer io.Writer, kind ControllerStreamRequestKindV1, columns uint32, rows uint32) {
	t.Helper()
	message := map[string]any{"schema": ControllerStreamSchemaV1, "type": kind}
	if kind == ControllerStreamRequestResizeV1 {
		message["columns"] = columns
		message["rows"] = rows
	}
	payload, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	payload = append(payload, '\n')
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
}
