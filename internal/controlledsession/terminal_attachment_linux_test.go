//go:build linux

package controlledsession

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRunTerminalAttachmentV1ConnectsToPrivateBrokerSocket(t *testing.T) {
	temporaryHome := shortControllerBrokerTempHomeV1(t)
	listener, err := PrepareControllerTerminalListenerV1(temporaryHome)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	brokerDone := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept(ctx)
		if acceptErr != nil {
			brokerDone <- acceptErr
			return
		}
		request, readErr := connection.ReadRequest(ctx)
		if readErr != nil {
			brokerDone <- readErr
			return
		}
		if request.Kind != RequestInputV1 || string(request.Bytes) != "headless input" {
			brokerDone <- fmt.Errorf("unexpected input request: %#v", request)
			return
		}
		if writeErr := connection.WriteOutput(ctx, []byte("broker output")); writeErr != nil {
			brokerDone <- writeErr
			return
		}
		brokerDone <- connection.WriteEnd(ctx, WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1})
	}()
	var output bytes.Buffer
	if err := runTerminalAttachmentAtHomeV1(ctx, TerminalAttachmentOptionsV1{
		SocketPath: listener.SocketPath(),
		Input:      strings.NewReader("headless input"),
		Output:     &output,
	}, temporaryHome); err != nil {
		t.Fatal(err)
	}
	if err := <-brokerDone; err != nil {
		t.Fatal(err)
	}
	if output.String() != "broker output" {
		t.Fatalf("attachment output = %q", output.String())
	}
}

func TestRunTerminalAttachmentV1RejectsNonBrokerSocketPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := RunTerminalAttachmentV1(ctx, TerminalAttachmentOptionsV1{
		SocketPath: "/tmp/arbitrary.sock",
		Input:      strings.NewReader(""),
		Output:     &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "broker path grammar") {
		t.Fatalf("non-broker socket error = %v", err)
	}
}
