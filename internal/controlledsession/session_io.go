package controlledsession

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// ControllerTransportV1 is the claimed, typed controller channel used by one
// controlled session. Implementations must admit at most one request read and
// one event write at a time and make both operations cancelable. Any event
// write failure is terminal because the framed stream may contain a partial
// frame; implementations must not admit another event write afterward.
type ControllerTransportV1 interface {
	ReadRequest(context.Context) (RequestV1, error)
	WriteEvent(context.Context, EventV1) error
}

type sessionEventWriteGateV1 struct {
	transport ControllerTransportV1

	mu               sync.Mutex
	writing          bool
	lifecycleWaiting int
	writeFailure     error
	changed          chan struct{}
}

// WorkloadPTYControlV1 is the backend-neutral part of a workload PTY that can
// receive already-authorized controller operations.
type WorkloadPTYControlV1 interface {
	WriteInput(context.Context, []byte) error
	Resize(context.Context, uint32, uint32) error
}

// ControllerRequestHandlerV1 applies lifecycle authorization and the effects
// of one typed controller request. It must return only after the request has
// been accepted or rejected and must stop promptly when ctx is canceled.
type ControllerRequestHandlerV1 func(context.Context, RequestV1) error

// ApplyAcceptedWorkloadPTYRequestV1 applies only the PTY effects of a request
// that the lifecycle supervisor has already accepted. It returns false for
// lifecycle-only requests so their effects remain owned by that supervisor.
func ApplyAcceptedWorkloadPTYRequestV1(
	ctx context.Context,
	workload WorkloadPTYControlV1,
	request RequestV1,
) (bool, error) {
	if ctx == nil || ctx.Done() == nil {
		return false, fmt.Errorf("apply accepted controlled-session workload PTY request: cancelable context is required")
	}
	if workload == nil {
		return false, fmt.Errorf("apply accepted controlled-session workload PTY request: workload is required")
	}
	if err := ValidateRequestV1(request); err != nil {
		return false, fmt.Errorf("apply accepted controlled-session workload PTY request: %w", err)
	}
	switch request.Kind {
	case RequestInputV1:
		if err := workload.WriteInput(ctx, request.Bytes); err != nil {
			return true, fmt.Errorf("apply accepted controlled-session input request: %w", err)
		}
		return true, nil
	case RequestResizeV1:
		if err := workload.Resize(ctx, request.Columns, request.Rows); err != nil {
			return true, fmt.Errorf("apply accepted controlled-session resize request: %w", err)
		}
		return true, nil
	case RequestTerminateV1, RequestCompleteV1, RequestAcknowledgeTerminatedV1:
		return false, nil
	default:
		panic("validated controlled-session request has an unsupported kind")
	}
}

// SessionIOBridgeV1 connects one claimed controller channel to one workload
// PTY without owning lifecycle policy, the channel, or the workload container.
// Requests remain serialized through the injected handler. PTY output is
// delivered as exact ordered protocol events with the output pump's single
// bounded flow-control chunk.
type SessionIOBridgeV1 struct {
	transport  ControllerTransportV1
	eventWrite *sessionEventWriteGateV1
	output     *PTYOutputPumpV1

	requestCtx    context.Context
	cancelRequest context.CancelFunc
	requestDone   chan struct{}
	stopOnce      sync.Once

	requestResultMu sync.Mutex
	requestResult   error
}

// StartSessionIOBridgeV1 starts request dispatch and PTY output delivery
// immediately. Callers may therefore establish the bridge before starting the
// workload, preventing early output loss.
func StartSessionIOBridgeV1(
	transport ControllerTransportV1,
	output io.ReadCloser,
	handle ControllerRequestHandlerV1,
) (*SessionIOBridgeV1, error) {
	if transport == nil {
		return nil, fmt.Errorf("start controlled-session I/O bridge: controller transport is required")
	}
	if handle == nil {
		return nil, fmt.Errorf("start controlled-session I/O bridge: controller request handler is required")
	}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	bridge := &SessionIOBridgeV1{
		transport:     transport,
		eventWrite:    newSessionEventWriteGateV1(transport),
		requestCtx:    requestCtx,
		cancelRequest: cancelRequest,
		requestDone:   make(chan struct{}),
	}
	pump, err := StartPTYOutputPumpV1(output, func(ctx context.Context, bytes []byte) error {
		return bridge.eventWrite.write(ctx, EventV1{Kind: EventOutputV1, Bytes: bytes}, false)
	})
	if err != nil {
		cancelRequest()
		return nil, fmt.Errorf("start controlled-session I/O bridge output: %w", err)
	}
	bridge.output = pump
	go bridge.runRequests(handle)
	return bridge, nil
}

// SendLifecycleEvent gives one lifecycle event priority over the next output
// frame while preserving the frame already being written. Opened remains owned
// by channel claim, output remains owned by the PTY pump, and final event-order
// decisions remain the lifecycle supervisor's responsibility.
func (bridge *SessionIOBridgeV1) SendLifecycleEvent(ctx context.Context, event EventV1) error {
	if !isBridgeLifecycleEventV1(event.Kind) {
		return fmt.Errorf("send controlled-session lifecycle event: event kind %q is not lifecycle-owned", event.Kind)
	}
	if err := ValidateEventV1(event); err != nil {
		return fmt.Errorf("send controlled-session lifecycle event: %w", err)
	}
	if err := bridge.eventWrite.write(ctx, event, true); err != nil {
		return fmt.Errorf("send controlled-session lifecycle event: %w", err)
	}
	return nil
}

// RequestsDone closes after request dispatch stops. The caller must inspect
// WaitRequests; closure alone does not mean that the controller completed.
func (bridge *SessionIOBridgeV1) RequestsDone() <-chan struct{} {
	return bridge.requestDone
}

// StopRequests cancels request dispatch without closing the controller channel.
// It is idempotent and is treated as normal host-owned shutdown by WaitRequests.
func (bridge *SessionIOBridgeV1) StopRequests() {
	bridge.stopOnce.Do(bridge.cancelRequest)
}

// WaitRequests waits for request dispatch to stop and returns its immutable
// diagnostic. Caller cancellation stops only this wait.
func (bridge *SessionIOBridgeV1) WaitRequests(ctx context.Context) error {
	if ctx == nil || ctx.Done() == nil {
		return fmt.Errorf("wait for controlled-session controller requests: cancelable context is required")
	}
	select {
	case <-bridge.requestDone:
		bridge.requestResultMu.Lock()
		defer bridge.requestResultMu.Unlock()
		return bridge.requestResult
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (bridge *SessionIOBridgeV1) OutputDone() <-chan struct{} {
	return bridge.output.Done()
}

func (bridge *SessionIOBridgeV1) FinalizeOutput(deadline time.Time) (PTYOutputFinalizationV1, error) {
	return bridge.output.Finalize(deadline)
}

func (bridge *SessionIOBridgeV1) runRequests(handle ControllerRequestHandlerV1) {
	defer close(bridge.requestDone)
	for {
		request, err := bridge.transport.ReadRequest(bridge.requestCtx)
		if err != nil {
			bridge.setRequestResult(bridge.requestFailure("read", err))
			return
		}
		if err := handle(bridge.requestCtx, request); err != nil {
			bridge.setRequestResult(bridge.requestFailure("handle", err))
			return
		}
	}
}

func (bridge *SessionIOBridgeV1) requestFailure(action string, err error) error {
	if errors.Is(err, context.Canceled) && bridge.requestCtx.Err() != nil {
		return nil
	}
	return fmt.Errorf("%s controlled-session controller request: %w", action, err)
}

func (bridge *SessionIOBridgeV1) setRequestResult(err error) {
	bridge.requestResultMu.Lock()
	defer bridge.requestResultMu.Unlock()
	bridge.requestResult = err
}

func newSessionEventWriteGateV1(transport ControllerTransportV1) *sessionEventWriteGateV1 {
	return &sessionEventWriteGateV1{transport: transport, changed: make(chan struct{})}
}

func (gate *sessionEventWriteGateV1) write(ctx context.Context, event EventV1, lifecycle bool) error {
	if err := gate.acquire(ctx, lifecycle); err != nil {
		return err
	}
	err := gate.transport.WriteEvent(ctx, event)
	gate.release(err)
	return err
}

func (gate *sessionEventWriteGateV1) acquire(ctx context.Context, lifecycle bool) error {
	if ctx == nil || ctx.Done() == nil {
		return fmt.Errorf("cancelable event context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	gate.mu.Lock()
	if lifecycle {
		gate.lifecycleWaiting++
		gate.notifyLocked()
	}
	for gate.writeFailure == nil && (gate.writing || !lifecycle && gate.lifecycleWaiting > 0) {
		changed := gate.changed
		gate.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			gate.mu.Lock()
			if lifecycle {
				gate.lifecycleWaiting--
				gate.notifyLocked()
			}
			gate.mu.Unlock()
			return ctx.Err()
		}
		gate.mu.Lock()
	}
	if gate.writeFailure != nil {
		if lifecycle {
			gate.lifecycleWaiting--
			gate.notifyLocked()
		}
		err := gate.writeFailure
		gate.mu.Unlock()
		return err
	}
	if err := ctx.Err(); err != nil {
		if lifecycle {
			gate.lifecycleWaiting--
			gate.notifyLocked()
		}
		gate.mu.Unlock()
		return err
	}
	if lifecycle {
		gate.lifecycleWaiting--
	}
	gate.writing = true
	gate.notifyLocked()
	gate.mu.Unlock()
	return nil
}

func (gate *sessionEventWriteGateV1) release(writeErr error) {
	gate.mu.Lock()
	if writeErr != nil && gate.writeFailure == nil {
		gate.writeFailure = fmt.Errorf("controlled-session event transport is unusable after write failure: %w", writeErr)
	}
	gate.writing = false
	gate.notifyLocked()
	gate.mu.Unlock()
}

func (gate *sessionEventWriteGateV1) notifyLocked() {
	close(gate.changed)
	gate.changed = make(chan struct{})
}

func isBridgeLifecycleEventV1(kind EventKindV1) bool {
	switch kind {
	case EventWorkloadExitV1, EventTerminatingV1, EventDiagnosticV1,
		EventWorkloadOutputsFinalizedV1, EventTerminatedV1:
		return true
	case EventOpenedV1, EventOutputV1:
		return false
	default:
		return false
	}
}
