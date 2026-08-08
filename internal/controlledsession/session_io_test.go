package controlledsession

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestApplyAcceptedWorkloadPTYRequestV1AppliesOnlyPTYEffects(t *testing.T) {
	workload := &recordingWorkloadPTYControlV1{}
	input := []byte{0x00, 0x03, 0x7f, 0xff}
	tests := []struct {
		request RequestV1
		handled bool
	}{
		{request: RequestV1{Kind: RequestInputV1, Bytes: input}, handled: true},
		{request: RequestV1{Kind: RequestResizeV1, Columns: 132, Rows: 43}, handled: true},
		{request: RequestV1{Kind: RequestTerminateV1}},
		{request: RequestV1{Kind: RequestCompleteV1}},
		{request: RequestV1{Kind: RequestAcknowledgeTerminatedV1}},
	}
	for _, test := range tests {
		handled, err := ApplyAcceptedWorkloadPTYRequestV1(t.Context(), workload, test.request)
		if err != nil {
			t.Fatalf("ApplyAcceptedWorkloadPTYRequestV1(%s): %v", test.request.Kind, err)
		}
		if handled != test.handled {
			t.Fatalf("ApplyAcceptedWorkloadPTYRequestV1(%s) handled = %t", test.request.Kind, handled)
		}
	}
	if !bytes.Equal(workload.input, input) {
		t.Fatalf("workload input = %v", workload.input)
	}
	if workload.columns != 132 || workload.rows != 43 {
		t.Fatalf("workload dimensions = %dx%d", workload.columns, workload.rows)
	}
}

func TestApplyAcceptedWorkloadPTYRequestV1ReportsValidationAndBackendFailures(t *testing.T) {
	if _, err := ApplyAcceptedWorkloadPTYRequestV1(context.Background(), &recordingWorkloadPTYControlV1{}, RequestV1{Kind: RequestTerminateV1}); err == nil {
		t.Fatal("ApplyAcceptedWorkloadPTYRequestV1() accepted a non-cancelable context")
	}
	if _, err := ApplyAcceptedWorkloadPTYRequestV1(t.Context(), nil, RequestV1{Kind: RequestTerminateV1}); err == nil {
		t.Fatal("ApplyAcceptedWorkloadPTYRequestV1() accepted a missing workload")
	}
	workload := &recordingWorkloadPTYControlV1{inputErr: errors.New("input failed"), resizeErr: errors.New("resize failed")}
	if _, err := ApplyAcceptedWorkloadPTYRequestV1(t.Context(), workload, RequestV1{Kind: RequestInputV1}); err == nil {
		t.Fatal("ApplyAcceptedWorkloadPTYRequestV1() accepted invalid input")
	}
	if _, err := ApplyAcceptedWorkloadPTYRequestV1(t.Context(), workload, RequestV1{Kind: RequestInputV1, Bytes: []byte("x")}); !errors.Is(err, workload.inputErr) {
		t.Fatalf("input error = %v", err)
	}
	if _, err := ApplyAcceptedWorkloadPTYRequestV1(t.Context(), workload, RequestV1{Kind: RequestResizeV1, Columns: 80, Rows: 24}); !errors.Is(err, workload.resizeErr) {
		t.Fatalf("resize error = %v", err)
	}
}

func TestSessionIOBridgeV1DispatchesRequestsAndOrderedBinaryOutput(t *testing.T) {
	transport := newBridgeTestTransportV1()
	reader, writer := io.Pipe()
	workload := &recordingWorkloadPTYControlV1{}
	handled := make(chan RequestV1, 5)
	bridge, err := StartSessionIOBridgeV1(transport, reader, func(ctx context.Context, request RequestV1) error {
		ptyRequest, err := ApplyAcceptedWorkloadPTYRequestV1(ctx, workload, request)
		if err != nil {
			return err
		}
		if ptyRequest && request.Kind != RequestInputV1 && request.Kind != RequestResizeV1 {
			return errors.New("lifecycle request was reported as a PTY request")
		}
		handled <- request
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	requests := []RequestV1{
		{Kind: RequestInputV1, Bytes: []byte{0x00, 0x03, 0xff}},
		{Kind: RequestResizeV1, Columns: 120, Rows: 40},
		{Kind: RequestTerminateV1},
		{Kind: RequestCompleteV1},
		{Kind: RequestAcknowledgeTerminatedV1},
	}
	for _, request := range requests {
		transport.requests <- bridgeTestRequestResultV1{request: request}
	}
	for index, want := range requests {
		select {
		case got := <-handled:
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("handled request %d = %#v, want %#v", index, got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for request %d", index)
		}
	}

	payload := []byte{0x00, 0x01, 0x7f, 0xff, 'R', 'P', 'S', 'N'}
	writeErr := make(chan error, 1)
	go func() {
		_, err := writer.Write(payload)
		writeErr <- errors.Join(err, writer.Close())
	}()
	select {
	case event := <-transport.events:
		if event.Kind != EventOutputV1 || !bytes.Equal(event.Bytes, payload) {
			t.Fatalf("output event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for output event")
	}
	if err := <-writeErr; err != nil {
		t.Fatal(err)
	}
	result, err := bridge.FinalizeOutput(time.Now().Add(time.Second))
	if err != nil || result.Status.Kind != WorkloadOutputFinalizationDrainedV1 || result.Err != nil {
		t.Fatalf("FinalizeOutput() = %#v, %v", result, err)
	}
	bridge.StopRequests()
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := bridge.WaitRequests(waitCtx); err != nil {
		t.Fatalf("WaitRequests() = %v", err)
	}
	if !bytes.Equal(workload.input, requests[0].Bytes) || workload.columns != 120 || workload.rows != 40 {
		t.Fatalf("workload effects = input %v, dimensions %dx%d", workload.input, workload.columns, workload.rows)
	}
}

func TestSessionIOBridgeV1SurfacesDisconnectAndHandlerFailure(t *testing.T) {
	t.Run("disconnect", func(t *testing.T) {
		transport := newBridgeTestTransportV1()
		bridge, err := StartSessionIOBridgeV1(transport, io.NopCloser(bytes.NewReader(nil)), func(context.Context, RequestV1) error {
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		disconnect := errors.New("controller disconnected")
		transport.requests <- bridgeTestRequestResultV1{err: disconnect}
		waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := bridge.WaitRequests(waitCtx); !errors.Is(err, disconnect) {
			t.Fatalf("WaitRequests() = %v", err)
		}
	})

	t.Run("handler", func(t *testing.T) {
		transport := newBridgeTestTransportV1()
		handleErr := errors.New("request rejected")
		bridge, err := StartSessionIOBridgeV1(transport, io.NopCloser(bytes.NewReader(nil)), func(context.Context, RequestV1) error {
			return handleErr
		})
		if err != nil {
			t.Fatal(err)
		}
		transport.requests <- bridgeTestRequestResultV1{request: RequestV1{Kind: RequestTerminateV1}}
		waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := bridge.WaitRequests(waitCtx); !errors.Is(err, handleErr) {
			t.Fatalf("WaitRequests() = %v", err)
		}
	})
}

func TestSessionIOBridgeV1LifecycleRejectionPreventsPTYEffect(t *testing.T) {
	authorization := testAuthorizationV1()
	authorization.Operations = []OperationV1{OperationResizeV1, OperationTerminateV1}
	machine, err := NewMachineV1(authorization)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := machine.Observe(ObservationV1{Kind: ObservationActivatedV1}); err != nil {
		t.Fatal(err)
	}
	transport := newBridgeTestTransportV1()
	workload := &recordingWorkloadPTYControlV1{}
	bridge, err := StartSessionIOBridgeV1(transport, io.NopCloser(bytes.NewReader(nil)), func(ctx context.Context, request RequestV1) error {
		if _, err := machine.ApplyRequest(request); err != nil {
			return err
		}
		_, err := ApplyAcceptedWorkloadPTYRequestV1(ctx, workload, request)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	transport.requests <- bridgeTestRequestResultV1{request: RequestV1{Kind: RequestInputV1, Bytes: []byte("denied")}}
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := bridge.WaitRequests(waitCtx); !errors.Is(err, ErrRequestRejected) {
		t.Fatalf("WaitRequests() = %v", err)
	}
	if len(workload.input) != 0 {
		t.Fatalf("rejected input reached the workload: %q", workload.input)
	}
}

func TestSessionIOBridgeV1BackpressureIsBoundedAndCancelable(t *testing.T) {
	transport := newBridgeTestTransportV1()
	transport.writeEntered = make(chan struct{}, 1)
	transport.blockWrites = true
	reader, writer := io.Pipe()
	bridge, err := StartSessionIOBridgeV1(transport, reader, func(context.Context, RequestV1) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	writeErr := make(chan error, 1)
	go func() {
		_, err := writer.Write(bytes.Repeat([]byte("x"), ptyOutputChunkSizeV1*2))
		writeErr <- err
	}()
	select {
	case <-transport.writeEntered:
	case <-time.After(time.Second):
		t.Fatal("output delivery did not reach the bounded write")
	}
	select {
	case <-bridge.OutputDone():
		t.Fatal("output pump crossed blocked backpressure")
	default:
	}
	result, err := bridge.FinalizeOutput(time.Now().Add(20 * time.Millisecond))
	if err != nil || result.Status.Kind != WorkloadOutputFinalizationFailedV1 || !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatalf("FinalizeOutput() = %#v, %v", result, err)
	}
	if err := <-writeErr; err != nil && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("blocked PTY writer error = %v", err)
	}
	_ = writer.Close()
	bridge.StopRequests()
}

func TestSessionIOBridgeV1PrioritizesLifecycleEventsBetweenOutputFrames(t *testing.T) {
	transport := newPriorityBridgeTestTransportV1()
	reader, writer := io.Pipe()
	bridge, err := StartSessionIOBridgeV1(transport, reader, func(context.Context, RequestV1) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("x"), ptyOutputChunkSizeV1*2)
	writeErr := make(chan error, 1)
	go func() {
		_, err := writer.Write(payload)
		writeErr <- errors.Join(err, writer.Close())
	}()
	select {
	case <-transport.firstWriteEntered:
	case <-time.After(time.Second):
		t.Fatal("first output event was not admitted")
	}
	lifecycle := EventV1{Kind: EventDiagnosticV1, Diagnostic: &DiagnosticV1{Code: "test", Message: "test diagnostic"}}
	lifecycleErr := make(chan error, 1)
	go func() { lifecycleErr <- bridge.SendLifecycleEvent(t.Context(), lifecycle) }()
	waitForLifecycleEventAdmissionV1(t, bridge)
	close(transport.releaseFirstWrite)
	if err := <-lifecycleErr; err != nil {
		t.Fatal(err)
	}
	if err := <-writeErr; err != nil {
		t.Fatal(err)
	}
	result, err := bridge.FinalizeOutput(time.Now().Add(time.Second))
	if err != nil || result.Status.Kind != WorkloadOutputFinalizationDrainedV1 {
		t.Fatalf("FinalizeOutput() = %#v, %v", result, err)
	}
	transport.mu.Lock()
	events := append([]EventV1(nil), transport.events...)
	transport.mu.Unlock()
	if len(events) != 3 || events[0].Kind != EventOutputV1 || events[1].Kind != EventDiagnosticV1 || events[2].Kind != EventOutputV1 {
		t.Fatalf("event order = %#v", events)
	}
	bridge.StopRequests()
}

func TestSessionIOBridgeV1RemovesCanceledLifecycleEventAdmission(t *testing.T) {
	transport := newPriorityBridgeTestTransportV1()
	reader, writer := io.Pipe()
	bridge, err := StartSessionIOBridgeV1(transport, reader, func(context.Context, RequestV1) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	writeErr := make(chan error, 1)
	go func() {
		_, err := writer.Write([]byte("output"))
		writeErr <- errors.Join(err, writer.Close())
	}()
	select {
	case <-transport.firstWriteEntered:
	case <-time.After(time.Second):
		t.Fatal("output event was not admitted")
	}
	lifecycleCtx, cancelLifecycle := context.WithCancel(context.Background())
	lifecycleErr := make(chan error, 1)
	go func() {
		lifecycleErr <- bridge.SendLifecycleEvent(lifecycleCtx, EventV1{
			Kind: EventDiagnosticV1, Diagnostic: &DiagnosticV1{Code: "test", Message: "test diagnostic"},
		})
	}()
	waitForLifecycleEventAdmissionV1(t, bridge)
	cancelLifecycle()
	if err := <-lifecycleErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("SendLifecycleEvent() = %v", err)
	}
	bridge.eventWrite.mu.Lock()
	waiting := bridge.eventWrite.lifecycleWaiting
	bridge.eventWrite.mu.Unlock()
	if waiting != 0 {
		t.Fatalf("lifecycle waiters after cancellation = %d", waiting)
	}
	close(transport.releaseFirstWrite)
	if err := <-writeErr; err != nil {
		t.Fatal(err)
	}
	result, err := bridge.FinalizeOutput(time.Now().Add(time.Second))
	if err != nil || result.Status.Kind != WorkloadOutputFinalizationDrainedV1 {
		t.Fatalf("FinalizeOutput() = %#v, %v", result, err)
	}
	bridge.StopRequests()
}

func waitForLifecycleEventAdmissionV1(t *testing.T, bridge *SessionIOBridgeV1) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		bridge.eventWrite.mu.Lock()
		waiting := bridge.eventWrite.lifecycleWaiting
		bridge.eventWrite.mu.Unlock()
		if waiting == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("lifecycle event did not enter its bounded admission path")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSessionIOBridgeV1SendsOnlyLifecycleEventsAndValidatesConfiguration(t *testing.T) {
	transport := newBridgeTestTransportV1()
	if _, err := StartSessionIOBridgeV1(nil, io.NopCloser(bytes.NewReader(nil)), func(context.Context, RequestV1) error { return nil }); err == nil {
		t.Fatal("StartSessionIOBridgeV1() accepted a missing transport")
	}
	if _, err := StartSessionIOBridgeV1(transport, io.NopCloser(bytes.NewReader(nil)), nil); err == nil {
		t.Fatal("StartSessionIOBridgeV1() accepted a missing handler")
	}
	if _, err := StartSessionIOBridgeV1(transport, nil, func(context.Context, RequestV1) error { return nil }); err == nil {
		t.Fatal("StartSessionIOBridgeV1() accepted a missing output source")
	}

	bridge, err := StartSessionIOBridgeV1(transport, io.NopCloser(bytes.NewReader(nil)), func(context.Context, RequestV1) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	event := EventV1{Kind: EventDiagnosticV1, Diagnostic: &DiagnosticV1{Code: "test", Message: "test diagnostic"}}
	if err := bridge.SendLifecycleEvent(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if got := <-transport.events; !reflect.DeepEqual(got, event) {
		t.Fatalf("event = %#v", got)
	}
	for _, reserved := range []EventV1{
		{Kind: EventOutputV1, Bytes: []byte("forged")},
		{Kind: EventOpenedV1, Opened: &OpenedV1{}},
	} {
		if err := bridge.SendLifecycleEvent(t.Context(), reserved); err == nil {
			t.Fatalf("SendLifecycleEvent() accepted reserved event %q", reserved.Kind)
		}
	}
	if err := bridge.SendLifecycleEvent(t.Context(), EventV1{
		Kind: EventDiagnosticV1, Diagnostic: &DiagnosticV1{Message: "missing code"},
	}); err == nil {
		t.Fatal("SendLifecycleEvent() accepted an invalid lifecycle payload")
	}
	if err := bridge.WaitRequests(context.Background()); err == nil {
		t.Fatal("WaitRequests() accepted a non-cancelable context")
	}
	bridge.StopRequests()
}

type bridgeTestRequestResultV1 struct {
	request RequestV1
	err     error
}

type bridgeTestTransportV1 struct {
	requests     chan bridgeTestRequestResultV1
	events       chan EventV1
	writeEntered chan struct{}
	blockWrites  bool
}

type priorityBridgeTestTransportV1 struct {
	firstWriteEntered chan struct{}
	releaseFirstWrite chan struct{}
	firstWriteOnce    sync.Once

	mu     sync.Mutex
	events []EventV1
}

func newPriorityBridgeTestTransportV1() *priorityBridgeTestTransportV1 {
	return &priorityBridgeTestTransportV1{
		firstWriteEntered: make(chan struct{}),
		releaseFirstWrite: make(chan struct{}),
	}
}

func (transport *priorityBridgeTestTransportV1) ReadRequest(ctx context.Context) (RequestV1, error) {
	<-ctx.Done()
	return RequestV1{}, ctx.Err()
}

func (transport *priorityBridgeTestTransportV1) WriteEvent(ctx context.Context, event EventV1) error {
	first := false
	transport.firstWriteOnce.Do(func() {
		first = true
		close(transport.firstWriteEntered)
	})
	if first {
		select {
		case <-transport.releaseFirstWrite:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	clone := event
	clone.Bytes = append([]byte(nil), event.Bytes...)
	transport.mu.Lock()
	transport.events = append(transport.events, clone)
	transport.mu.Unlock()
	return nil
}

func newBridgeTestTransportV1() *bridgeTestTransportV1 {
	return &bridgeTestTransportV1{
		requests: make(chan bridgeTestRequestResultV1, 16),
		events:   make(chan EventV1, 16),
	}
}

func (transport *bridgeTestTransportV1) ReadRequest(ctx context.Context) (RequestV1, error) {
	select {
	case result := <-transport.requests:
		return result.request, result.err
	case <-ctx.Done():
		return RequestV1{}, ctx.Err()
	}
}

func (transport *bridgeTestTransportV1) WriteEvent(ctx context.Context, event EventV1) error {
	if transport.blockWrites {
		select {
		case transport.writeEntered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}
	clone := event
	clone.Bytes = append([]byte(nil), event.Bytes...)
	select {
	case transport.events <- clone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type recordingWorkloadPTYControlV1 struct {
	mu        sync.Mutex
	input     []byte
	columns   uint32
	rows      uint32
	inputErr  error
	resizeErr error
}

func (workload *recordingWorkloadPTYControlV1) WriteInput(_ context.Context, data []byte) error {
	workload.mu.Lock()
	defer workload.mu.Unlock()
	workload.input = append(workload.input, data...)
	return workload.inputErr
}

func (workload *recordingWorkloadPTYControlV1) Resize(_ context.Context, columns uint32, rows uint32) error {
	workload.mu.Lock()
	defer workload.mu.Unlock()
	workload.columns = columns
	workload.rows = rows
	return workload.resizeErr
}
