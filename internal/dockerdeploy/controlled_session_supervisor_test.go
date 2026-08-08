package dockerdeploy

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/omry/reploy/internal/controlledsession"
)

func TestRunControlledSessionV1OwnsNormalLifecycle(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	requests := make(chan controlledsession.RequestV1, 8)
	controller := newFakeControlledSessionProcessV1()
	transport := &fakeControlledSessionTransportV1{requests: requests}
	transport.onEvent = func(event controlledsession.EventV1) {
		switch event.Kind {
		case controlledsession.EventWorkloadOutputsFinalizedV1:
			requests <- controlledsession.RequestV1{Kind: controlledsession.RequestCompleteV1}
		case controlledsession.EventTerminatedV1:
			requests <- controlledsession.RequestV1{Kind: controlledsession.RequestAcknowledgeTerminatedV1}
			code := 0
			controller.exit <- controlledSessionProcessResultV1{
				status: controlledsession.ProcessStatusV1{Kind: controlledsession.ProcessStatusExitedV1, Code: &code},
			}
			close(requests)
		}
	}
	workload := newFakeControlledSessionWorkloadV1([]byte("hello from workload\n"), 17)
	channel := &fakeControlledSessionChannelV1{transport: transport}

	result, err := runControlledSessionV1(t.Context(), plan, testControlledSessionRunOptionsV1(), controlledSessionSupervisorBackendV1{
		prepareChannel: func(ControlledSessionExecutionPlanV1) (controlledSessionChannelRuntimeV1, error) {
			return channel, nil
		},
		prepareController: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionControllerRuntimeV1, error) {
			return controller, nil
		},
		prepareWorkload: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionWorkloadRuntimeV1, error) {
			return workload, nil
		},
		now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionResult.Cause != controlledsession.CauseWorkloadExitV1 ||
		result.SessionResult.WorkloadStatus.Code == nil || *result.SessionResult.WorkloadStatus.Code != 17 {
		t.Fatalf("session result = %#v", result.SessionResult)
	}
	if !result.ResultDelivered || !result.ResultAcknowledged {
		t.Fatalf("result delivery = delivered %t acknowledged %t", result.ResultDelivered, result.ResultAcknowledged)
	}
	if result.DeliveryTailCleanupStatus.Kind != controlledsession.CleanupStatusSucceededV1 ||
		result.DeliveryTailRecoveryAction != controlledsession.RecoveryNoneV1 {
		t.Fatalf("delivery-tail cleanup = %#v / %q", result.DeliveryTailCleanupStatus, result.DeliveryTailRecoveryAction)
	}
	if !controller.cleaned || !workload.cleaned || !channel.closed {
		t.Fatalf("cleanup = controller %t workload %t channel %t", controller.cleaned, workload.cleaned, channel.closed)
	}

	events := transport.snapshotEvents()
	if len(events) != 5 {
		t.Fatalf("events = %#v", events)
	}
	indices := map[controlledsession.EventKindV1]int{}
	for index, event := range events {
		indices[event.Kind] = index
	}
	for _, kind := range []controlledsession.EventKindV1{
		controlledsession.EventOutputV1,
		controlledsession.EventWorkloadExitV1,
		controlledsession.EventTerminatingV1,
		controlledsession.EventWorkloadOutputsFinalizedV1,
		controlledsession.EventTerminatedV1,
	} {
		if _, found := indices[kind]; !found {
			t.Fatalf("event %q missing from %#v", kind, events)
		}
	}
	if indices[controlledsession.EventWorkloadExitV1] > indices[controlledsession.EventTerminatingV1] ||
		indices[controlledsession.EventOutputV1] > indices[controlledsession.EventWorkloadOutputsFinalizedV1] ||
		indices[controlledsession.EventWorkloadOutputsFinalizedV1] > indices[controlledsession.EventTerminatedV1] {
		t.Fatalf("event order = %#v", events)
	}
	if string(events[indices[controlledsession.EventOutputV1]].Bytes) != "hello from workload\n" {
		t.Fatalf("output events = %#v", events)
	}
}

func TestRunControlledSessionV1CapsShutdownAtOutputDeadline(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	requests := make(chan controlledsession.RequestV1, 4)
	requests <- controlledsession.RequestV1{Kind: controlledsession.RequestTerminateV1}
	transport := &fakeControlledSessionTransportV1{requests: requests}
	transport.onEvent = func(event controlledsession.EventV1) {
		switch event.Kind {
		case controlledsession.EventWorkloadOutputsFinalizedV1:
			requests <- controlledsession.RequestV1{Kind: controlledsession.RequestCompleteV1}
		case controlledsession.EventTerminatedV1:
			requests <- controlledsession.RequestV1{Kind: controlledsession.RequestAcknowledgeTerminatedV1}
		}
	}
	controller := newFakeControlledSessionProcessV1()
	workload := newFakeControlledSessionWorkloadV1(nil, 137)
	workload.exitOnStart = false
	workload.exitOnGraceful = false
	options := testControlledSessionRunOptionsV1()
	options.TerminationGrace = time.Hour

	started := time.Now()
	_, err := runControlledSessionV1(t.Context(), plan, options, controlledSessionSupervisorBackendV1{
		prepareChannel: func(ControlledSessionExecutionPlanV1) (controlledSessionChannelRuntimeV1, error) {
			return &fakeControlledSessionChannelV1{transport: transport}, nil
		},
		prepareController: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionControllerRuntimeV1, error) {
			return controller, nil
		},
		prepareWorkload: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionWorkloadRuntimeV1, error) {
			return workload, nil
		},
		now: func() time.Time {
			return time.Now().Add(-controlledSessionOutputFinalizationTimeoutV1)
		},
	})
	if err == nil {
		t.Fatal("expired output deadline unexpectedly produced a successful session")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("expired output deadline still waited %s", elapsed)
	}
	if !workload.forceStopped {
		t.Fatal("expired output deadline did not attempt forced shutdown")
	}
}

func TestRunControlledSessionV1AppliesControllerRequests(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	requests := make(chan controlledsession.RequestV1, 8)
	requests <- controlledsession.RequestV1{Kind: controlledsession.RequestInputV1, Bytes: []byte{0, 1, 0xff}}
	requests <- controlledsession.RequestV1{Kind: controlledsession.RequestResizeV1, Columns: 132, Rows: 43}
	requests <- controlledsession.RequestV1{Kind: controlledsession.RequestTerminateV1}
	transport := &fakeControlledSessionTransportV1{requests: requests}
	transport.onEvent = func(event controlledsession.EventV1) {
		switch event.Kind {
		case controlledsession.EventWorkloadOutputsFinalizedV1:
			requests <- controlledsession.RequestV1{Kind: controlledsession.RequestCompleteV1}
		case controlledsession.EventTerminatedV1:
			requests <- controlledsession.RequestV1{Kind: controlledsession.RequestAcknowledgeTerminatedV1}
		}
	}
	controller := newFakeControlledSessionProcessV1()
	workload := newFakeControlledSessionWorkloadV1(nil, 143)
	workload.exitOnStart = false

	result, err := runControlledSessionV1(t.Context(), plan, testControlledSessionRunOptionsV1(), controlledSessionSupervisorBackendV1{
		prepareChannel: func(ControlledSessionExecutionPlanV1) (controlledSessionChannelRuntimeV1, error) {
			return &fakeControlledSessionChannelV1{transport: transport}, nil
		},
		prepareController: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionControllerRuntimeV1, error) {
			return controller, nil
		},
		prepareWorkload: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionWorkloadRuntimeV1, error) {
			return workload, nil
		},
		now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionResult.Cause != controlledsession.CauseControllerTerminateV1 {
		t.Fatalf("cause = %q", result.SessionResult.Cause)
	}
	if !workload.gracefulStopped {
		t.Fatal("workload did not receive graceful stop")
	}
	if got := workload.snapshotInput(); string(got) != string([]byte{0, 1, 0xff}) {
		t.Fatalf("input = %v", got)
	}
	if workload.columns != 132 || workload.rows != 43 {
		t.Fatalf("dimensions = %dx%d", workload.columns, workload.rows)
	}
}

func TestRunControlledSessionV1CleansPreparedResourcesAfterStartupFailure(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	controller := newFakeControlledSessionProcessV1()
	workload := newFakeControlledSessionWorkloadV1(nil, 0)
	startupErr := errors.New("workload start failed")
	workload.startErr = startupErr
	requests := make(chan controlledsession.RequestV1, 1)
	transport := &fakeControlledSessionTransportV1{requests: requests}
	transport.onEvent = func(event controlledsession.EventV1) {
		if event.Kind == controlledsession.EventTerminatedV1 {
			requests <- controlledsession.RequestV1{Kind: controlledsession.RequestAcknowledgeTerminatedV1}
		}
	}
	channel := &fakeControlledSessionChannelV1{transport: transport}

	result, err := runControlledSessionV1(t.Context(), plan, testControlledSessionRunOptionsV1(), controlledSessionSupervisorBackendV1{
		prepareChannel: func(ControlledSessionExecutionPlanV1) (controlledSessionChannelRuntimeV1, error) { return channel, nil },
		prepareController: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionControllerRuntimeV1, error) {
			return controller, nil
		},
		prepareWorkload: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionWorkloadRuntimeV1, error) {
			return workload, nil
		},
		now: time.Now,
	})
	if !errors.Is(err, startupErr) {
		t.Fatalf("error = %v", err)
	}
	if result.SessionResult.Cause != controlledsession.CauseStartupFailureV1 {
		t.Fatalf("result = %#v", result.SessionResult)
	}
	if !result.ResultDelivered || !result.ResultAcknowledged {
		t.Fatalf("startup result delivery = delivered %t acknowledged %t", result.ResultDelivered, result.ResultAcknowledged)
	}
	if !controller.cleaned || !workload.cleaned || !channel.closed {
		t.Fatalf("cleanup = controller %t workload %t channel %t", controller.cleaned, workload.cleaned, channel.closed)
	}
}

func TestRunControlledSessionV1DrainsPartiallyStartedWorkloadAfterStartupFailure(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	controller := newFakeControlledSessionProcessV1()
	workload := newFakeControlledSessionWorkloadV1([]byte("output before initial resize failed\n"), 143)
	startupErr := errors.New("initial terminal resize failed")
	workload.startPartially = true
	workload.startErr = startupErr
	requests := make(chan controlledsession.RequestV1, 1)
	transport := &fakeControlledSessionTransportV1{requests: requests}
	transport.onEvent = func(event controlledsession.EventV1) {
		if event.Kind == controlledsession.EventTerminatedV1 {
			requests <- controlledsession.RequestV1{Kind: controlledsession.RequestAcknowledgeTerminatedV1}
		}
	}

	result, err := runControlledSessionV1(t.Context(), plan, testControlledSessionRunOptionsV1(), controlledSessionSupervisorBackendV1{
		prepareChannel: func(ControlledSessionExecutionPlanV1) (controlledSessionChannelRuntimeV1, error) {
			return &fakeControlledSessionChannelV1{transport: transport}, nil
		},
		prepareController: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionControllerRuntimeV1, error) {
			return controller, nil
		},
		prepareWorkload: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionWorkloadRuntimeV1, error) {
			return workload, nil
		},
		now: time.Now,
	})
	if !errors.Is(err, startupErr) {
		t.Fatalf("error = %v", err)
	}
	if result.SessionResult.Cause != controlledsession.CauseStartupFailureV1 ||
		result.SessionResult.WorkloadStatus.Code == nil || *result.SessionResult.WorkloadStatus.Code != 143 ||
		result.SessionResult.WorkloadOutputFinalizationStatus.Kind != controlledsession.WorkloadOutputFinalizationDrainedV1 {
		t.Fatalf("session result = %#v", result.SessionResult)
	}
	if !workload.gracefulStopped || !workload.cleaned {
		t.Fatalf("workload teardown = graceful %t cleaned %t", workload.gracefulStopped, workload.cleaned)
	}
	events := transport.snapshotEvents()
	indices := map[controlledsession.EventKindV1]int{}
	for index, event := range events {
		indices[event.Kind] = index
	}
	for _, kind := range []controlledsession.EventKindV1{
		controlledsession.EventOutputV1,
		controlledsession.EventWorkloadExitV1,
		controlledsession.EventTerminatingV1,
		controlledsession.EventWorkloadOutputsFinalizedV1,
		controlledsession.EventTerminatedV1,
	} {
		if _, found := indices[kind]; !found {
			t.Fatalf("event %q missing from %#v", kind, events)
		}
	}
	if indices[controlledsession.EventOutputV1] > indices[controlledsession.EventWorkloadOutputsFinalizedV1] ||
		indices[controlledsession.EventWorkloadOutputsFinalizedV1] > indices[controlledsession.EventTerminatedV1] {
		t.Fatalf("event order = %#v", events)
	}
}

func TestRunControlledSessionV1FailsClosedAfterRuntimeObservationLoss(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	requests := make(chan controlledsession.RequestV1, 4)
	transport := &fakeControlledSessionTransportV1{requests: requests}
	transport.onEvent = func(event controlledsession.EventV1) {
		switch event.Kind {
		case controlledsession.EventWorkloadOutputsFinalizedV1:
			requests <- controlledsession.RequestV1{Kind: controlledsession.RequestCompleteV1}
		case controlledsession.EventTerminatedV1:
			requests <- controlledsession.RequestV1{Kind: controlledsession.RequestAcknowledgeTerminatedV1}
		}
	}
	controller := newFakeControlledSessionProcessV1()
	workload := newFakeControlledSessionWorkloadV1(nil, 0)
	observationErr := errors.New("runtime observation unavailable")
	workload.waitErr = observationErr
	workload.exitOnGraceful = false

	result, err := runControlledSessionV1(t.Context(), plan, testControlledSessionRunOptionsV1(), controlledSessionSupervisorBackendV1{
		prepareChannel: func(ControlledSessionExecutionPlanV1) (controlledSessionChannelRuntimeV1, error) {
			return &fakeControlledSessionChannelV1{transport: transport}, nil
		},
		prepareController: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionControllerRuntimeV1, error) {
			return controller, nil
		},
		prepareWorkload: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionWorkloadRuntimeV1, error) {
			return workload, nil
		},
		now: time.Now,
	})
	if !errors.Is(err, observationErr) {
		t.Fatalf("error = %v", err)
	}
	if result.SessionResult.Cause != controlledsession.CauseRuntimeObservationLostV1 ||
		result.SessionResult.RuntimeObservationStatus.Kind != controlledsession.RuntimeObservationLostV1 ||
		result.SessionResult.WorkloadStatus.Kind != controlledsession.ProcessStatusUnavailableV1 ||
		result.SessionResult.WorkloadOutputFinalizationStatus.Kind != controlledsession.WorkloadOutputFinalizationFailedV1 {
		t.Fatalf("session result = %#v", result.SessionResult)
	}
	if !workload.gracefulStopped || !workload.forceStopped || !workload.cleaned {
		t.Fatalf("workload teardown = graceful %t force %t cleaned %t", workload.gracefulStopped, workload.forceStopped, workload.cleaned)
	}
}

func TestRunControlledSessionV1StopsWorkloadAfterPTYReadFailure(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	requests := make(chan controlledsession.RequestV1, 4)
	transport := &fakeControlledSessionTransportV1{requests: requests}
	transport.onEvent = func(event controlledsession.EventV1) {
		switch event.Kind {
		case controlledsession.EventWorkloadOutputsFinalizedV1:
			requests <- controlledsession.RequestV1{Kind: controlledsession.RequestCompleteV1}
		case controlledsession.EventTerminatedV1:
			requests <- controlledsession.RequestV1{Kind: controlledsession.RequestAcknowledgeTerminatedV1}
		}
	}
	controller := newFakeControlledSessionProcessV1()
	workload := newFakeControlledSessionWorkloadV1(nil, 143)
	workload.exitOnStart = false
	readErr := errors.New("PTY attachment failed")
	if err := workload.writer.CloseWithError(readErr); err != nil {
		t.Fatal(err)
	}

	result, err := runControlledSessionV1(t.Context(), plan, testControlledSessionRunOptionsV1(), controlledSessionSupervisorBackendV1{
		prepareChannel: func(ControlledSessionExecutionPlanV1) (controlledSessionChannelRuntimeV1, error) {
			return &fakeControlledSessionChannelV1{transport: transport}, nil
		},
		prepareController: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionControllerRuntimeV1, error) {
			return controller, nil
		},
		prepareWorkload: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionWorkloadRuntimeV1, error) {
			return workload, nil
		},
		now: time.Now,
	})
	if !errors.Is(err, readErr) {
		t.Fatalf("error = %v", err)
	}
	if result.SessionResult.Cause != controlledsession.CauseRuntimeObservationLostV1 ||
		result.SessionResult.WorkloadOutputFinalizationStatus.Kind != controlledsession.WorkloadOutputFinalizationFailedV1 ||
		!result.ResultDelivered || !workload.gracefulStopped || !workload.cleaned {
		t.Fatalf("output-loss result = %#v, workload = %#v", result, workload)
	}
}

func TestRunControlledSessionV1StopsWorkloadAfterOutputDeliveryFailure(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	deliveryErr := errors.New("controller event transport failed")
	transport := &fakeControlledSessionTransportV1{
		requests: make(chan controlledsession.RequestV1),
		writeErr: deliveryErr,
	}
	controller := newFakeControlledSessionProcessV1()
	workload := newFakeControlledSessionWorkloadV1(nil, 143)
	workload.exitOnStart = false
	writeDone := make(chan error, 1)
	go func() {
		_, err := workload.writer.Write([]byte("undeliverable output"))
		writeDone <- err
	}()

	result, err := runControlledSessionV1(t.Context(), plan, testControlledSessionRunOptionsV1(), controlledSessionSupervisorBackendV1{
		prepareChannel: func(ControlledSessionExecutionPlanV1) (controlledSessionChannelRuntimeV1, error) {
			return &fakeControlledSessionChannelV1{transport: transport}, nil
		},
		prepareController: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionControllerRuntimeV1, error) {
			return controller, nil
		},
		prepareWorkload: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionWorkloadRuntimeV1, error) {
			return workload, nil
		},
		now: time.Now,
	})
	if !errors.Is(err, deliveryErr) {
		t.Fatalf("error = %v", err)
	}
	if writeErr := <-writeDone; writeErr != nil {
		t.Fatalf("write workload output: %v", writeErr)
	}
	if result.SessionResult.Cause != controlledsession.CauseControllerLostV1 ||
		result.SessionResult.WorkloadOutputFinalizationStatus.Kind != controlledsession.WorkloadOutputFinalizationFailedV1 ||
		result.ResultDelivered || !workload.gracefulStopped || !workload.cleaned {
		t.Fatalf("delivery-loss result = %#v, workload = %#v", result, workload)
	}
}

func TestRunControlledSessionV1StopsWorkloadAfterHostCancellation(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	requests := make(chan controlledsession.RequestV1, 4)
	transport := &fakeControlledSessionTransportV1{requests: requests}
	transport.onEvent = func(event controlledsession.EventV1) {
		switch event.Kind {
		case controlledsession.EventWorkloadOutputsFinalizedV1:
			requests <- controlledsession.RequestV1{Kind: controlledsession.RequestCompleteV1}
		case controlledsession.EventTerminatedV1:
			requests <- controlledsession.RequestV1{Kind: controlledsession.RequestAcknowledgeTerminatedV1}
		}
	}
	controller := newFakeControlledSessionProcessV1()
	workload := newFakeControlledSessionWorkloadV1(nil, 143)
	workload.exitOnStart = false
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := runControlledSessionV1(ctx, plan, testControlledSessionRunOptionsV1(), controlledSessionSupervisorBackendV1{
		prepareChannel: func(ControlledSessionExecutionPlanV1) (controlledSessionChannelRuntimeV1, error) {
			return &fakeControlledSessionChannelV1{transport: transport}, nil
		},
		prepareController: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionControllerRuntimeV1, error) {
			return controller, nil
		},
		prepareWorkload: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionWorkloadRuntimeV1, error) {
			return workload, nil
		},
		now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionResult.Cause != controlledsession.CauseHostCancelV1 || !workload.gracefulStopped || !workload.cleaned {
		t.Fatalf("host cancellation result = %#v, workload = %#v", result, workload)
	}
}

func TestRunControlledSessionV1FailsClosedAfterControllerExit(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	controller := newFakeControlledSessionProcessV1()
	controllerCode := 9
	controller.exit <- controlledSessionProcessResultV1{
		status: controlledsession.ProcessStatusV1{Kind: controlledsession.ProcessStatusExitedV1, Code: &controllerCode},
	}
	workload := newFakeControlledSessionWorkloadV1(nil, 143)
	workload.exitOnStart = false
	transport := &fakeControlledSessionTransportV1{requests: make(chan controlledsession.RequestV1)}

	result, err := runControlledSessionV1(t.Context(), plan, testControlledSessionRunOptionsV1(), controlledSessionSupervisorBackendV1{
		prepareChannel: func(ControlledSessionExecutionPlanV1) (controlledSessionChannelRuntimeV1, error) {
			return &fakeControlledSessionChannelV1{transport: transport}, nil
		},
		prepareController: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionControllerRuntimeV1, error) {
			return controller, nil
		},
		prepareWorkload: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionWorkloadRuntimeV1, error) {
			return workload, nil
		},
		now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionResult.Cause != controlledsession.CauseControllerLostV1 ||
		result.SessionResult.ControllerFinalizationStatus.Kind != controlledsession.ControllerFinalizationLostV1 ||
		result.ResultDelivered || !workload.gracefulStopped || !workload.cleaned {
		t.Fatalf("controller-loss result = %#v, workload = %#v", result, workload)
	}
}

func testControlledSessionRunOptionsV1() ControlledSessionRunOptionsV1 {
	return ControlledSessionRunOptionsV1{
		StartupTimeout: 2 * time.Second, TerminationGrace: 100 * time.Millisecond,
		ControllerFinalizationTimeout: 2 * time.Second, ResultAcknowledgementTimeout: 2 * time.Second,
		CleanupTimeout: 2 * time.Second,
	}
}

type fakeControlledSessionTransportV1 struct {
	requests chan controlledsession.RequestV1
	mu       sync.Mutex
	events   []controlledsession.EventV1
	onEvent  func(controlledsession.EventV1)
	writeErr error
}

func (transport *fakeControlledSessionTransportV1) ReadRequest(ctx context.Context) (controlledsession.RequestV1, error) {
	select {
	case request, ok := <-transport.requests:
		if !ok {
			return controlledsession.RequestV1{}, io.EOF
		}
		return request, nil
	case <-ctx.Done():
		return controlledsession.RequestV1{}, ctx.Err()
	}
}

func (transport *fakeControlledSessionTransportV1) WriteEvent(_ context.Context, event controlledsession.EventV1) error {
	if transport.writeErr != nil {
		return transport.writeErr
	}
	event.Bytes = append([]byte(nil), event.Bytes...)
	transport.mu.Lock()
	transport.events = append(transport.events, event)
	transport.mu.Unlock()
	if transport.onEvent != nil {
		transport.onEvent(event)
	}
	return nil
}

func (transport *fakeControlledSessionTransportV1) snapshotEvents() []controlledsession.EventV1 {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return append([]controlledsession.EventV1(nil), transport.events...)
}

type fakeControlledSessionChannelV1 struct {
	transport controlledsession.ControllerTransportV1
	closed    bool
}

func (channel *fakeControlledSessionChannelV1) Claim(context.Context) (controlledsession.ControllerTransportV1, error) {
	return channel.transport, nil
}

func (channel *fakeControlledSessionChannelV1) Close() error {
	channel.closed = true
	return nil
}

type fakeControlledSessionProcessV1 struct {
	exit            chan controlledSessionProcessResultV1
	started         bool
	gracefulStopped bool
	forceStopped    bool
	cleaned         bool
}

func newFakeControlledSessionProcessV1() *fakeControlledSessionProcessV1 {
	return &fakeControlledSessionProcessV1{exit: make(chan controlledSessionProcessResultV1, 1)}
}

func (process *fakeControlledSessionProcessV1) Start(context.Context) error {
	process.started = true
	return nil
}

func (process *fakeControlledSessionProcessV1) Wait(ctx context.Context) (controlledsession.ProcessStatusV1, error) {
	select {
	case result := <-process.exit:
		return result.status, result.err
	case <-ctx.Done():
		return controlledsession.ProcessStatusV1{}, ctx.Err()
	}
}

func (process *fakeControlledSessionProcessV1) RequestGracefulStop(context.Context) error {
	process.gracefulStopped = true
	code := 0
	process.exit <- controlledSessionProcessResultV1{status: controlledsession.ProcessStatusV1{Kind: controlledsession.ProcessStatusExitedV1, Code: &code}}
	return nil
}

func (process *fakeControlledSessionProcessV1) ForceStop(context.Context) error {
	process.forceStopped = true
	return nil
}

func (process *fakeControlledSessionProcessV1) Cleanup(context.Context) error {
	process.cleaned = true
	return nil
}

type fakeControlledSessionWorkloadV1 struct {
	*fakeControlledSessionProcessV1
	reader         *io.PipeReader
	writer         *io.PipeWriter
	output         []byte
	exitCode       int
	exitOnStart    bool
	exitOnGraceful bool
	startPartially bool
	startErr       error
	waitErr        error
	closed         bool
	inputMu        sync.Mutex
	input          []byte
	columns        uint32
	rows           uint32
}

func newFakeControlledSessionWorkloadV1(output []byte, exitCode int) *fakeControlledSessionWorkloadV1 {
	reader, writer := io.Pipe()
	return &fakeControlledSessionWorkloadV1{
		fakeControlledSessionProcessV1: newFakeControlledSessionProcessV1(),
		reader:                         reader, writer: writer, output: append([]byte(nil), output...), exitCode: exitCode, exitOnStart: true, exitOnGraceful: true,
	}
}

func (workload *fakeControlledSessionWorkloadV1) Output() (io.ReadCloser, error) {
	return workload.reader, nil
}

func (workload *fakeControlledSessionWorkloadV1) Start(context.Context) error {
	if workload.startErr != nil {
		workload.started = workload.startPartially
		if workload.startPartially && len(workload.output) > 0 {
			_, _ = workload.writer.Write(workload.output)
		}
		return workload.startErr
	}
	workload.started = true
	if workload.exitOnStart {
		go func() {
			_, _ = workload.writer.Write(workload.output)
			_ = workload.writer.Close()
			if workload.waitErr != nil {
				workload.exit <- controlledSessionProcessResultV1{
					status: controlledsession.ProcessStatusV1{Kind: controlledsession.ProcessStatusUnavailableV1, Reason: "workload runtime observation was lost"},
					err:    workload.waitErr,
				}
				return
			}
			code := workload.exitCode
			workload.exit <- controlledSessionProcessResultV1{status: controlledsession.ProcessStatusV1{Kind: controlledsession.ProcessStatusExitedV1, Code: &code}}
		}()
	}
	return nil
}

func (workload *fakeControlledSessionWorkloadV1) Started() bool {
	return workload.started
}

func (workload *fakeControlledSessionWorkloadV1) RequestGracefulStop(context.Context) error {
	workload.gracefulStopped = true
	_ = workload.writer.Close()
	if !workload.exitOnGraceful {
		return nil
	}
	code := workload.exitCode
	workload.exit <- controlledSessionProcessResultV1{status: controlledsession.ProcessStatusV1{Kind: controlledsession.ProcessStatusExitedV1, Code: &code}}
	return nil
}

func (workload *fakeControlledSessionWorkloadV1) ForceStop(context.Context) error {
	workload.forceStopped = true
	code := workload.exitCode
	workload.exit <- controlledSessionProcessResultV1{status: controlledsession.ProcessStatusV1{Kind: controlledsession.ProcessStatusExitedV1, Code: &code}}
	return nil
}

func (workload *fakeControlledSessionWorkloadV1) WriteInput(_ context.Context, input []byte) error {
	workload.inputMu.Lock()
	defer workload.inputMu.Unlock()
	workload.input = append(workload.input, input...)
	return nil
}

func (workload *fakeControlledSessionWorkloadV1) Resize(_ context.Context, columns uint32, rows uint32) error {
	workload.columns, workload.rows = columns, rows
	return nil
}

func (workload *fakeControlledSessionWorkloadV1) Close() error {
	workload.closed = true
	return workload.reader.Close()
}

func (workload *fakeControlledSessionWorkloadV1) snapshotInput() []byte {
	workload.inputMu.Lock()
	defer workload.inputMu.Unlock()
	return append([]byte(nil), workload.input...)
}
