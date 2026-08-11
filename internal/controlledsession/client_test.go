package controlledsession

import (
	"context"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSessionClientV1ConsumesOpenedAndExchangesTypedFrames(t *testing.T) {
	server, controller := net.Pipe()
	defer server.Close()
	opened := testOpenedV1()
	wantEvent := EventV1{Kind: EventDiagnosticV1, Diagnostic: &DiagnosticV1{Code: "ready", Message: "controller ready"}}
	wantRequest := RequestV1{Kind: RequestInputV1, Bytes: []byte{0, 3, 0xff}}
	type serverResultV1 struct {
		request RequestV1
		err     error
	}
	serverResult := make(chan serverResultV1, 1)
	go func() {
		if err := WriteEventV1(server, EventV1{Kind: EventOpenedV1, Opened: &opened}); err != nil {
			serverResult <- serverResultV1{err: err}
			return
		}
		if err := WriteEventV1(server, EventV1{Kind: EventReadyV1}); err != nil {
			serverResult <- serverResultV1{err: err}
			return
		}
		if err := WriteEventV1(server, wantEvent); err != nil {
			serverResult <- serverResultV1{err: err}
			return
		}
		request, err := ReadRequestV1(server)
		serverResult <- serverResultV1{request: request, err: err}
	}()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	client, err := newSessionClientV1(ctx, controller)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	gotOpened := client.Opened()
	if !reflect.DeepEqual(gotOpened, opened) {
		t.Fatalf("opened = %#v, want %#v", gotOpened, opened)
	}
	gotOpened.Endpoints[0].Host = "changed"
	if client.Opened().Endpoints[0].Host != WorkloadEndpointHostV1 {
		t.Fatal("opened endpoint copy mutated the client")
	}
	if client.Ready() {
		t.Fatal("client became ready from opened")
	}
	if err := client.WriteRequest(ctx, wantRequest); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("pre-ready request error = %v", err)
	}
	ready, err := client.ReadEvent(ctx)
	if err != nil || ready.Kind != EventReadyV1 || !client.Ready() {
		t.Fatalf("ready event = %#v, error = %v, ready = %t", ready, err, client.Ready())
	}
	event, err := client.ReadEvent(ctx)
	if err != nil || !reflect.DeepEqual(event, wantEvent) {
		t.Fatalf("event = %#v, error = %v", event, err)
	}
	if err := client.WriteRequest(ctx, wantRequest); err != nil {
		t.Fatal(err)
	}
	result := <-serverResult
	if result.err != nil {
		t.Fatal(result.err)
	}
	if !reflect.DeepEqual(result.request, wantRequest) {
		t.Fatalf("request = %#v, want %#v", result.request, wantRequest)
	}
}

func TestSessionClientV1RejectsRepeatedReady(t *testing.T) {
	server, controller := net.Pipe()
	defer server.Close()
	opened := testOpenedV1()
	go func() {
		_ = WriteEventV1(server, EventV1{Kind: EventOpenedV1, Opened: &opened})
		_ = WriteEventV1(server, EventV1{Kind: EventReadyV1})
		_ = WriteEventV1(server, EventV1{Kind: EventReadyV1})
	}()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	client, err := newSessionClientV1(ctx, controller)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if event, err := client.ReadEvent(ctx); err != nil || event.Kind != EventReadyV1 {
		t.Fatalf("first ready event = %#v, error = %v", event, err)
	}
	if _, err := client.ReadEvent(ctx); err == nil || !strings.Contains(err.Error(), "ready may appear only once") {
		t.Fatalf("repeated ready error = %v", err)
	}
}

func TestSessionClientV1AcknowledgesStartupFailureBeforeReady(t *testing.T) {
	server, controller := net.Pipe()
	defer server.Close()
	opened := testOpenedV1()
	terminated := EventV1{Kind: EventTerminatedV1, Terminated: &ResultV1{
		Cause:          CauseStartupFailureV1,
		WorkloadStatus: ProcessStatusV1{Kind: ProcessStatusUnknownV1},
		WorkloadOutputFinalizationStatus: WorkloadOutputFinalizationStatusV1{
			Kind: WorkloadOutputFinalizationDrainedV1,
		},
		RuntimeObservationStatus: RuntimeObservationStatusV1{Kind: RuntimeObservationMaintainedV1},
		ControllerFinalizationStatus: ControllerFinalizationStatusV1{
			Kind: ControllerFinalizationStartupFailedV1,
		},
		CleanupStatus:  CleanupStatusV1{Kind: CleanupStatusSucceededV1},
		RecoveryAction: RecoveryNoneV1,
	}}
	type serverResultV1 struct {
		request RequestV1
		err     error
	}
	serverResult := make(chan serverResultV1, 1)
	go func() {
		if err := WriteEventV1(server, EventV1{Kind: EventOpenedV1, Opened: &opened}); err != nil {
			serverResult <- serverResultV1{err: err}
			return
		}
		if err := WriteEventV1(server, terminated); err != nil {
			serverResult <- serverResultV1{err: err}
			return
		}
		request, err := ReadRequestV1(server)
		serverResult <- serverResultV1{request: request, err: err}
	}()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	client, err := newSessionClientV1(ctx, controller)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.WriteRequest(ctx, RequestV1{Kind: RequestAcknowledgeTerminatedV1}); err == nil || !strings.Contains(err.Error(), "not been received") {
		t.Fatalf("pre-terminated acknowledgement error = %v", err)
	}
	event, err := client.ReadEvent(ctx)
	if err != nil || !reflect.DeepEqual(event, terminated) {
		t.Fatalf("terminated event = %#v, error = %v", event, err)
	}
	if client.Ready() {
		t.Fatal("client became ready after startup failure")
	}
	if err := client.WriteRequest(ctx, RequestV1{Kind: RequestInputV1, Bytes: []byte("late")}); err == nil || !strings.Contains(err.Error(), "terminated") {
		t.Fatalf("post-terminated input error = %v", err)
	}
	wantRequest := RequestV1{Kind: RequestAcknowledgeTerminatedV1}
	if err := client.WriteRequest(ctx, wantRequest); err != nil {
		t.Fatal(err)
	}
	result := <-serverResult
	if result.err != nil {
		t.Fatal(result.err)
	}
	if !reflect.DeepEqual(result.request, wantRequest) {
		t.Fatalf("request = %#v, want %#v", result.request, wantRequest)
	}
}

func TestSessionClientV1RequiresOpenedFirstAndRejectsRepeatedOpened(t *testing.T) {
	for _, test := range []struct {
		name   string
		first  EventV1
		second *EventV1
		want   string
	}{
		{name: "first", first: EventV1{Kind: EventDiagnosticV1, Diagnostic: &DiagnosticV1{Code: "bad", Message: "not opened"}}, want: "first event must be opened"},
		{name: "repeated", first: EventV1{Kind: EventOpenedV1, Opened: pointerToOpenedV1(testOpenedV1())}, second: &EventV1{Kind: EventOpenedV1, Opened: pointerToOpenedV1(testOpenedV1())}, want: "only once"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, controller := net.Pipe()
			defer server.Close()
			go func() {
				_ = WriteEventV1(server, test.first)
				if test.second != nil {
					_ = WriteEventV1(server, *test.second)
				}
			}()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			client, err := newSessionClientV1(ctx, controller)
			if test.second == nil {
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("error = %v, want containing %q", err, test.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if _, err := client.ReadEvent(ctx); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func testOpenedV1() OpenedV1 {
	return OpenedV1{
		Authorization: testAuthorizationV1(), Endpoints: testEndpointsV1(), Columns: 80, Rows: 24,
		OutputFinalizationTimeoutMilliseconds: DefaultOutputFinalizationTimeoutMillisecondsV1,
	}
}

func pointerToOpenedV1(value OpenedV1) *OpenedV1 { return &value }
