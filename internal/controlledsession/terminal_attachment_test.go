package controlledsession

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunTerminalAttachmentV1ForwardsRawInputOutputResizeAndRestores(t *testing.T) {
	attachmentSide, brokerSide := net.Pipe()
	defer brokerSide.Close()
	resizes := make(chan terminalAttachmentResizeV1, 1)
	resizes <- terminalAttachmentResizeV1{request: RequestV1{Kind: RequestResizeV1, Columns: 100, Rows: 40}}
	close(resizes)
	initial := RequestV1{Kind: RequestResizeV1, Columns: 80, Rows: 24}
	var restored atomic.Bool
	var output bytes.Buffer
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		done <- runTerminalAttachmentV1(ctx, &terminalAttachmentConnectionV1{connection: attachmentSide}, bytes.NewReader([]byte{0x03, 'x'}), &output, terminalAttachmentTTYV1{
			initialResize: &initial,
			resizes:       resizes,
			restore: func() error {
				restored.Store(true)
				return nil
			},
		})
	}()

	requests := make([]RequestV1, 0, 3)
	for len(requests) < 3 {
		request, err := ReadTerminalRequestV1(brokerSide)
		if err != nil {
			t.Fatal(err)
		}
		requests = append(requests, request)
	}
	if requests[0].Kind != RequestResizeV1 || requests[0].Columns != 80 || requests[0].Rows != 24 {
		t.Fatalf("initial request = %#v", requests[0])
	}
	var sawInput, sawResize bool
	for _, request := range requests[1:] {
		switch request.Kind {
		case RequestInputV1:
			sawInput = bytes.Equal(request.Bytes, []byte{0x03, 'x'})
		case RequestResizeV1:
			sawResize = request.Columns == 100 && request.Rows == 40
		}
	}
	if !sawInput || !sawResize {
		t.Fatalf("terminal requests = %#v", requests)
	}
	if err := WriteTerminalOutputV1(brokerSide, []byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	if err := WriteTerminalOutputV1(brokerSide, []byte("world")); err != nil {
		t.Fatal(err)
	}
	if err := WriteTerminalEndV1(brokerSide, WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if output.String() != "hello\nworld" {
		t.Fatalf("terminal output = %q", output.String())
	}
	if !restored.Load() {
		t.Fatal("raw terminal state was not restored")
	}
}

func TestRunTerminalAttachmentV1CanonicalInputHasNoLocalEcho(t *testing.T) {
	attachmentSide, brokerSide := net.Pipe()
	defer brokerSide.Close()
	var output bytes.Buffer
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		done <- runTerminalAttachmentV1(ctx, &terminalAttachmentConnectionV1{connection: attachmentSide}, strings.NewReader("typed\n"), &output, terminalAttachmentTTYV1{})
	}()
	request, err := ReadTerminalRequestV1(brokerSide)
	if err != nil || request.Kind != RequestInputV1 || string(request.Bytes) != "typed\n" {
		t.Fatalf("canonical input = %#v, %v", request, err)
	}
	if err := WriteTerminalOutputV1(brokerSide, []byte("typed\r\n")); err != nil {
		t.Fatal(err)
	}
	if err := WriteTerminalEndV1(brokerSide, WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if output.String() != "typed\r\n" {
		t.Fatalf("canonical output contains local echo: %q", output.String())
	}
}

func TestRunTerminalAttachmentV1PreservesLargeOutputOrdering(t *testing.T) {
	attachmentSide, brokerSide := net.Pipe()
	defer brokerSide.Close()
	inputReader, inputWriter := io.Pipe()
	defer inputWriter.Close()
	var output bytes.Buffer
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		done <- runTerminalAttachmentV1(ctx, &terminalAttachmentConnectionV1{connection: attachmentSide}, inputReader, &output, terminalAttachmentTTYV1{})
	}()
	first := bytes.Repeat([]byte("a"), MaxFramePayloadV1)
	second := []byte("tail")
	if err := WriteTerminalOutputV1(brokerSide, first); err != nil {
		t.Fatal(err)
	}
	if err := WriteTerminalOutputV1(brokerSide, second); err != nil {
		t.Fatal(err)
	}
	if err := WriteTerminalEndV1(brokerSide, WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	want := append(first, second...)
	if !bytes.Equal(output.Bytes(), want) {
		t.Fatalf("large terminal output length = %d, want %d", output.Len(), len(want))
	}
}

func TestRunTerminalAttachmentV1ReportsFailedFinalizationAndDisconnect(t *testing.T) {
	for _, test := range []struct {
		name string
		peer func(net.Conn) error
		want string
	}{
		{
			name: "failed finalization",
			peer: func(connection net.Conn) error {
				return WriteTerminalEndV1(connection, WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationFailedV1, Reason: "drain timed out"})
			},
			want: "drain timed out",
		},
		{
			name: "abrupt disconnect",
			peer: func(connection net.Conn) error { return connection.Close() },
			want: "read controlled-session terminal event",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			attachmentSide, brokerSide := net.Pipe()
			inputReader, inputWriter := io.Pipe()
			defer inputWriter.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- runTerminalAttachmentV1(ctx, &terminalAttachmentConnectionV1{connection: attachmentSide}, inputReader, io.Discard, terminalAttachmentTTYV1{})
			}()
			if err := test.peer(brokerSide); err != nil {
				t.Fatal(err)
			}
			err := <-done
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("attachment error = %v, want containing %q", err, test.want)
			}
			if !errors.Is(err, net.ErrClosed) && test.name == "abrupt disconnect" && !strings.Contains(err.Error(), "EOF") && !strings.Contains(err.Error(), "closed pipe") {
				t.Fatalf("disconnect error does not preserve transport cause: %v", err)
			}
		})
	}
}
