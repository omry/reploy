package dockerdeploy

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omry/reploy/internal/controlledsession"
	"github.com/omry/reploy/internal/deploy"
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
	watchdog := &fakeControlledSessionWatchdogV1{onDisarm: func() {
		if !controller.cleaned || !workload.cleaned || !channel.closed {
			t.Fatal("watchdog disarmed before verified workload, controller, and channel cleanup")
		}
	}}
	var launchObserved bool

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
		recordPlannedOwnership:    func() error { return nil },
		recordControllerOwnership: func(string) error { return nil },
		recordOwnership: func(controllerID string, workloadID string) (deploy.ControlledSessionCleanupManifest, error) {
			ownership := controlledSessionOwnershipFromPlanV1(plan, controllerID, workloadID)
			ownership.BootSession = "boot-session"
			return deploy.ControlledSessionCleanupManifestFromOwnership(ownership)
		},
		startWatchdog: func(_ context.Context, manifest deploy.ControlledSessionCleanupManifest) (controlledSessionWatchdogRuntimeV1, error) {
			launchObserved = true
			if controller.started || workload.started {
				t.Fatal("controlled-session process started before watchdog readiness")
			}
			if manifest.LiveRunID != plan.LiveRunID {
				t.Fatalf("watchdog manifest live run = %q", manifest.LiveRunID)
			}
			return watchdog, nil
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
	if !launchObserved || !watchdog.disarmed {
		t.Fatalf("watchdog = launched %t disarmed %t", launchObserved, watchdog.disarmed)
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

func TestRunControlledSessionV1PersistsExactOwnershipBeforeStarting(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	controller := newFakeControlledSessionProcessV1()
	workload := newFakeControlledSessionWorkloadV1(nil, 0)
	channel := &fakeControlledSessionChannelV1{}
	persistErr := errors.New("injected durable ownership failure")
	calls := []string{}

	result, err := runControlledSessionV1(t.Context(), plan, testControlledSessionRunOptionsV1(), controlledSessionSupervisorBackendV1{
		prepareChannel: func(ControlledSessionExecutionPlanV1) (controlledSessionChannelRuntimeV1, error) {
			calls = append(calls, "channel")
			return channel, nil
		},
		prepareController: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionControllerRuntimeV1, error) {
			calls = append(calls, "prepare-controller")
			return controller, nil
		},
		prepareWorkload: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionWorkloadRuntimeV1, error) {
			calls = append(calls, "prepare-workload")
			return workload, nil
		},
		recordPlannedOwnership: func() error {
			calls = append(calls, "planned")
			if controller.started || workload.started {
				t.Fatal("controlled-session process started before planned ownership")
			}
			return nil
		},
		recordControllerOwnership: func(controllerID string) error {
			calls = append(calls, "controller")
			if controllerID != dockerControllerTestContainerIDV1 {
				t.Fatalf("controller ID = %q", controllerID)
			}
			return nil
		},
		recordOwnership: func(controllerID string, workloadID string) (deploy.ControlledSessionCleanupManifest, error) {
			calls = append(calls, "complete")
			if controller.started || workload.started {
				t.Fatal("controlled-session process started before durable ownership")
			}
			if controllerID != dockerControllerTestContainerIDV1 || workloadID != dockerWorkloadTestContainerIDV1 {
				t.Fatalf("container IDs = %q / %q", controllerID, workloadID)
			}
			return deploy.ControlledSessionCleanupManifest{}, persistErr
		},
		now: time.Now,
	})
	if !reflect.DeepEqual(calls, []string{"planned", "channel", "prepare-controller", "controller", "prepare-workload", "complete"}) || !errors.Is(err, persistErr) {
		t.Fatalf("ownership persistence calls=%v, error=%v", calls, err)
	}
	if controller.started || workload.started {
		t.Fatalf("started after persistence failure = controller %t workload %t", controller.started, workload.started)
	}
	if !controller.cleaned || !workload.cleaned || !channel.closed {
		t.Fatalf("inert cleanup = controller %t workload %t channel %t", controller.cleaned, workload.cleaned, channel.closed)
	}
	if result.SessionResult.CleanupStatus.Kind != controlledsession.CleanupStatusSucceededV1 ||
		result.DeliveryTailCleanupStatus.Kind != controlledsession.CleanupStatusSucceededV1 {
		t.Fatalf("cleanup result = %#v", result)
	}
}

func TestRunControlledSessionV1RecordsControllerOwnershipBeforeWorkloadPreparationFailure(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	controller := newFakeControlledSessionProcessV1()
	channel := &fakeControlledSessionChannelV1{}
	prepareErr := errors.New("injected workload preparation failure")
	calls := []string{}

	result, err := runControlledSessionV1(t.Context(), plan, testControlledSessionRunOptionsV1(), controlledSessionSupervisorBackendV1{
		prepareChannel: func(ControlledSessionExecutionPlanV1) (controlledSessionChannelRuntimeV1, error) {
			calls = append(calls, "channel")
			return channel, nil
		},
		prepareController: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionControllerRuntimeV1, error) {
			calls = append(calls, "prepare-controller")
			return controller, nil
		},
		prepareWorkload: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionWorkloadRuntimeV1, error) {
			calls = append(calls, "prepare-workload")
			return nil, prepareErr
		},
		recordPlannedOwnership: func() error {
			calls = append(calls, "planned")
			return nil
		},
		recordControllerOwnership: func(controllerID string) error {
			calls = append(calls, "controller")
			if controllerID != dockerControllerTestContainerIDV1 {
				t.Fatalf("controller ID = %q", controllerID)
			}
			return nil
		},
		recordOwnership: func(string, string) (deploy.ControlledSessionCleanupManifest, error) {
			t.Fatal("complete ownership recorded without a workload")
			return deploy.ControlledSessionCleanupManifest{}, nil
		},
		now: time.Now,
	})
	if !errors.Is(err, prepareErr) {
		t.Fatalf("workload preparation error = %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"planned", "channel", "prepare-controller", "controller", "prepare-workload"}) {
		t.Fatalf("partial ownership calls = %v", calls)
	}
	if controller.started {
		t.Fatal("controller started after workload preparation failed")
	}
	if !controller.cleaned || !channel.closed {
		t.Fatalf("partial preparation cleanup = controller %t channel %t", controller.cleaned, channel.closed)
	}
	if result.SessionResult.CleanupStatus.Kind != controlledsession.CleanupStatusSucceededV1 ||
		result.DeliveryTailCleanupStatus.Kind != controlledsession.CleanupStatusSucceededV1 {
		t.Fatalf("cleanup result = %#v", result)
	}
}

func TestRunControlledSessionV1RejectsIncompleteOwnershipBackend(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	called := false
	_, err := runControlledSessionV1(t.Context(), plan, testControlledSessionRunOptionsV1(), controlledSessionSupervisorBackendV1{
		prepareChannel: func(ControlledSessionExecutionPlanV1) (controlledSessionChannelRuntimeV1, error) {
			called = true
			return nil, nil
		},
		prepareController: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionControllerRuntimeV1, error) {
			called = true
			return nil, nil
		},
		prepareWorkload: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionWorkloadRuntimeV1, error) {
			called = true
			return nil, nil
		},
		recordOwnership: func(string, string) (deploy.ControlledSessionCleanupManifest, error) {
			return deploy.ControlledSessionCleanupManifest{}, nil
		},
		now: time.Now,
	})
	if err == nil || !strings.Contains(err.Error(), "ownership backend is incomplete") {
		t.Fatalf("incomplete ownership backend error = %v", err)
	}
	if called {
		t.Fatal("incomplete ownership backend began preparation")
	}
}

func TestControlledSessionChannelAbsentV1(t *testing.T) {
	existing := t.TempDir()
	if controlledSessionChannelAbsentV1(existing) {
		t.Fatal("existing channel directory reported absent")
	}
	if !controlledSessionChannelAbsentV1(existing + "/missing") {
		t.Fatal("missing channel directory not reported absent")
	}
}

func TestControlledSessionPreparationCanCompleteV1(t *testing.T) {
	tests := []struct {
		name                   string
		cleaned                bool
		ownershipRecorded      bool
		released               bool
		channelCleanupVerified bool
		want                   bool
	}{
		{name: "cleanup failed", ownershipRecorded: true, released: true},
		{name: "no ownership recorded", cleaned: true, want: true},
		{name: "full ownership released", cleaned: true, ownershipRecorded: true, released: true, want: true},
		{name: "partial ownership ambiguous", cleaned: true, ownershipRecorded: true},
		{name: "channel absence verified", cleaned: true, ownershipRecorded: true, channelCleanupVerified: true, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := controlledSessionPreparationCanCompleteV1(test.cleaned, test.ownershipRecorded, test.released, test.channelCleanupVerified); got != test.want {
				t.Fatalf("completion = %t, want %t", got, test.want)
			}
		})
	}
}

func TestRunControlledSessionV1RejectsCleanupResourcesNotInDurableOwnership(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	controller := newFakeControlledSessionProcessV1()
	workload := newFakeControlledSessionWorkloadV1(nil, 0)
	channel := &fakeControlledSessionChannelV1{}

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
		recordPlannedOwnership:    func() error { return nil },
		recordControllerOwnership: func(string) error { return nil },
		recordOwnership: func(controllerID string, workloadID string) (deploy.ControlledSessionCleanupManifest, error) {
			ownership := controlledSessionOwnershipFromPlanV1(plan, controllerID, workloadID)
			ownership.BootSession = "boot-session"
			manifest, manifestErr := deploy.ControlledSessionCleanupManifestFromOwnership(ownership)
			manifest.Networks = []string{"unrelated-network"}
			return manifest, manifestErr
		},
		now: time.Now,
	})
	if err == nil || !strings.Contains(err.Error(), "resources that this runtime does not create") {
		t.Fatalf("cleanup resource selection error = %v", err)
	}
	if controller.started || workload.started {
		t.Fatalf("started with invalid cleanup manifest = controller %t workload %t", controller.started, workload.started)
	}
	if !controller.cleaned || !workload.cleaned || !channel.closed {
		t.Fatalf("inert cleanup = controller %t workload %t channel %t", controller.cleaned, workload.cleaned, channel.closed)
	}
	if result.SessionResult.CleanupStatus.Kind != controlledsession.CleanupStatusSucceededV1 ||
		result.DeliveryTailCleanupStatus.Kind != controlledsession.CleanupStatusSucceededV1 {
		t.Fatalf("cleanup result = %#v", result)
	}
}

func TestRunControlledSessionV1DoesNotStartWhenWatchdogLaunchFails(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	controller := newFakeControlledSessionProcessV1()
	workload := newFakeControlledSessionWorkloadV1(nil, 0)
	channel := &fakeControlledSessionChannelV1{}
	launchErr := errors.New("injected watchdog launch failure")

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
		recordPlannedOwnership:    func() error { return nil },
		recordControllerOwnership: func(string) error { return nil },
		recordOwnership: func(controllerID string, workloadID string) (deploy.ControlledSessionCleanupManifest, error) {
			ownership := controlledSessionOwnershipFromPlanV1(plan, controllerID, workloadID)
			ownership.BootSession = "boot-session"
			return deploy.ControlledSessionCleanupManifestFromOwnership(ownership)
		},
		startWatchdog: func(context.Context, deploy.ControlledSessionCleanupManifest) (controlledSessionWatchdogRuntimeV1, error) {
			return nil, launchErr
		},
		now: time.Now,
	})
	if !errors.Is(err, launchErr) {
		t.Fatalf("watchdog launch error = %v", err)
	}
	if controller.started || workload.started {
		t.Fatalf("started after watchdog failure = controller %t workload %t", controller.started, workload.started)
	}
	if !controller.cleaned || !workload.cleaned || !channel.closed {
		t.Fatalf("inert cleanup = controller %t workload %t channel %t", controller.cleaned, workload.cleaned, channel.closed)
	}
	if result.SessionResult.CleanupStatus.Kind != controlledsession.CleanupStatusSucceededV1 ||
		result.DeliveryTailCleanupStatus.Kind != controlledsession.CleanupStatusSucceededV1 {
		t.Fatalf("cleanup result = %#v", result)
	}
}

func TestControlledSessionWatchdogDisarmRequiresVerifiedCompleteCleanup(t *testing.T) {
	disarmErr := errors.New("injected disarm failure")
	for _, test := range []struct {
		name                string
		preCleanupSucceeded bool
		watchdogErr         error
		wantDisarmed        bool
		wantCleanup         controlledsession.CleanupStatusKindV1
	}{
		{name: "all cleanup succeeded", preCleanupSucceeded: true, wantDisarmed: true, wantCleanup: controlledsession.CleanupStatusSucceededV1},
		{name: "workload cleanup failed", preCleanupSucceeded: false, wantCleanup: controlledsession.CleanupStatusSucceededV1},
		{name: "disarm failed", preCleanupSucceeded: true, watchdogErr: disarmErr, wantDisarmed: true, wantCleanup: controlledsession.CleanupStatusFailedV1},
	} {
		t.Run(test.name, func(t *testing.T) {
			watchdog := &fakeControlledSessionWatchdogV1{err: test.watchdogErr}
			supervisor := &controlledSessionSupervisorV1{
				options: testControlledSessionRunOptionsV1(),
				channel: &fakeControlledSessionChannelV1{}, controller: newFakeControlledSessionProcessV1(),
				watchdog: watchdog, preCleanupSucceeded: test.preCleanupSucceeded,
			}
			_, cleanup, recovery := supervisor.cleanupDeliveryTail()
			if watchdog.disarmed != test.wantDisarmed || cleanup.Kind != test.wantCleanup {
				t.Fatalf("watchdog disarmed=%t, cleanup=%q", watchdog.disarmed, cleanup.Kind)
			}
			if test.watchdogErr != nil {
				if recovery != controlledsession.RecoveryRetryCleanupV1 || !errors.Is(supervisor.diagnosticErr, disarmErr) {
					t.Fatalf("disarm failure recovery=%q, diagnostic=%v", recovery, supervisor.diagnosticErr)
				}
			} else if recovery != controlledsession.RecoveryNoneV1 {
				t.Fatalf("cleanup recovery=%q", recovery)
			}
		})
	}
}

func TestControlledSessionCleanupFailureRetainsDurableOwnership(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	operation, err := deploy.AcquireOperationLock(t.Context(), plan.Workload.DeploymentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if status, err := operation.AdmitLiveRunV1(deploy.LiveRunV1{
		ID: plan.LiveRunID, Kind: deploy.LiveRunKindShellV1, Name: plan.Workload.DeploymentID,
		GenerationReference: plan.Workload.GenerationReference, Exclusive: true,
	}, false); err != nil || status != deploy.LiveRunStatusActiveV1 {
		t.Fatalf("admission = %q, %v", status, err)
	}
	if _, err := operation.RecordControlledSessionOwnershipV1(controlledSessionOwnershipFromPlanV1(
		plan, dockerControllerTestContainerIDV1, dockerWorkloadTestContainerIDV1,
	)); err != nil {
		t.Fatal(err)
	}
	if err := finishControlledSessionOwnershipV1(t.Context(), plan.Workload.DeploymentDirectory, operation, false, plan.LiveRunID, false); err != nil {
		t.Fatal(err)
	}
	check, err := deploy.AcquireOperationLock(t.Context(), plan.Workload.DeploymentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Unlock()
	queue, found, err := check.ReadLiveRunQueueV1()
	if err != nil || !found || len(queue.Runs) != 1 || len(queue.ControlledSessions) != 1 {
		t.Fatalf("retained queue = %#v, found=%t, error=%v", queue, found, err)
	}
}

func TestRunControlledSessionV1RemovesAdmissionOnLockDirectoryMismatch(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	workloadDir := plan.Workload.DeploymentDirectory
	operation, err := deploy.AcquireOperationLock(t.Context(), workloadDir)
	if err != nil {
		t.Fatal(err)
	}
	if status, err := operation.AdmitLiveRunV1(deploy.LiveRunV1{
		ID: plan.LiveRunID, Kind: deploy.LiveRunKindShellV1, Name: plan.Workload.DeploymentID,
		GenerationReference: plan.Workload.GenerationReference, Exclusive: true,
	}, false); err != nil || status != deploy.LiveRunStatusActiveV1 {
		t.Fatalf("admission = %q, %v", status, err)
	}
	plan.Workload.DeploymentDirectory = t.TempDir()
	if _, err := RunControlledSessionV1(t.Context(), operation, plan, testControlledSessionRunOptionsV1()); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("lock-directory mismatch error = %v", err)
	}
	check, err := deploy.AcquireOperationLock(t.Context(), workloadDir)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Unlock()
	if queue, found, err := check.ReadLiveRunQueueV1(); err != nil || found {
		t.Fatalf("mismatched operation retained admission: %#v, found=%t, error=%v", queue, found, err)
	}
}

func TestRunControlledSessionV1RequiresQueueEntryLease(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	operation, err := deploy.AcquireOperationLock(t.Context(), plan.Workload.DeploymentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if status, err := operation.AdmitLiveRunV1(deploy.LiveRunV1{
		ID: plan.LiveRunID, Kind: deploy.LiveRunKindShellV1, Name: plan.Workload.DeploymentID,
		GenerationReference: plan.Workload.GenerationReference, Exclusive: true,
	}, false); err != nil || status != deploy.LiveRunStatusActiveV1 {
		t.Fatalf("admission = %q, %v", status, err)
	}
	if _, err := RunControlledSessionV1(t.Context(), operation, plan, testControlledSessionRunOptionsV1()); err == nil || !strings.Contains(err.Error(), "queue-entry lease") {
		t.Fatalf("missing queue-entry lease error = %v", err)
	}
	check, err := deploy.AcquireOperationLock(t.Context(), plan.Workload.DeploymentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Unlock()
	if queue, found, err := check.ReadLiveRunQueueV1(); err != nil || found {
		t.Fatalf("missing-lease operation retained admission: %#v, found=%t, error=%v", queue, found, err)
	}
}

func TestRunControlledSessionV1HoldsCompleteUntilOutputFinalizationPublication(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	requests := make(chan controlledsession.RequestV1, 8)
	completeRead := make(chan struct{})
	requestAfterCompleteRead := make(chan struct{})
	controller := newFakeControlledSessionProcessV1()
	transport := &fakeControlledSessionTransportV1{
		requests:          requests,
		blockEventKind:    controlledsession.EventWorkloadOutputsFinalizedV1,
		eventWriteBlocked: make(chan struct{}),
		releaseEventWrite: make(chan struct{}),
	}
	var readMu sync.Mutex
	readCount := 0
	transport.onRequest = func(request controlledsession.RequestV1) {
		readMu.Lock()
		defer readMu.Unlock()
		if request.Kind == controlledsession.RequestCompleteV1 {
			readCount++
			if readCount == 1 {
				close(completeRead)
			}
			return
		}
		if request.Kind == controlledsession.RequestTerminateV1 && readCount == 1 {
			close(requestAfterCompleteRead)
		}
	}
	transport.onEvent = func(event controlledsession.EventV1) {
		switch event.Kind {
		case controlledsession.EventWorkloadOutputsFinalizedV1:
			requests <- controlledsession.RequestV1{Kind: controlledsession.RequestCompleteV1}
			requests <- controlledsession.RequestV1{Kind: controlledsession.RequestTerminateV1}
		case controlledsession.EventTerminatedV1:
			requests <- controlledsession.RequestV1{Kind: controlledsession.RequestAcknowledgeTerminatedV1}
			code := 0
			controller.exit <- controlledSessionProcessResultV1{
				status: controlledsession.ProcessStatusV1{Kind: controlledsession.ProcessStatusExitedV1, Code: &code},
			}
			close(requests)
		}
	}
	workload := newFakeControlledSessionWorkloadV1(nil, 0)
	result := make(chan error, 1)
	go func() {
		_, err := runControlledSessionV1(t.Context(), plan, testControlledSessionRunOptionsV1(), controlledSessionSupervisorBackendV1{
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
		result <- err
	}()

	select {
	case <-transport.eventWriteBlocked:
	case <-time.After(time.Second):
		t.Fatal("workload output finalization event publication did not begin")
	}
	select {
	case <-completeRead:
	case <-time.After(time.Second):
		t.Fatal("controller completion request was not read during event publication")
	}
	select {
	case <-requestAfterCompleteRead:
		t.Fatal("completion was accepted before workload output finalization publication resolved")
	case <-time.After(50 * time.Millisecond):
	}
	close(transport.releaseEventWrite)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("controlled session did not finish after output publication resolved")
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

func TestRunControlledSessionV1RecordsHostCancellationDuringStartup(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	channel := &fakeControlledSessionChannelV1{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := runControlledSessionV1(ctx, plan, testControlledSessionRunOptionsV1(), controlledSessionSupervisorBackendV1{
		prepareChannel: func(ControlledSessionExecutionPlanV1) (controlledSessionChannelRuntimeV1, error) {
			return channel, nil
		},
		prepareController: func(ctx context.Context, _ ControlledSessionContainerPlanV1) (controlledSessionControllerRuntimeV1, error) {
			return nil, ctx.Err()
		},
		prepareWorkload: func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionWorkloadRuntimeV1, error) {
			t.Fatal("workload preparation continued after host cancellation")
			return nil, nil
		},
		now: time.Now,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if result.SessionResult.Cause != controlledsession.CauseHostCancelV1 ||
		result.SessionResult.ControllerFinalizationStatus.Kind != controlledsession.ControllerFinalizationStartupFailedV1 ||
		result.ResultDelivered || !channel.closed {
		t.Fatalf("startup cancellation result = %#v, channel closed = %t", result, channel.closed)
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

func TestRunControlledSessionV1RecordsLatePTYFailureWithoutReplacingHostCancellation(t *testing.T) {
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
	lateReadErr := errors.New("PTY failed during teardown")
	workload.gracefulOutputErr = lateReadErr
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
	if !errors.Is(err, lateReadErr) {
		t.Fatalf("error = %v", err)
	}
	if result.SessionResult.Cause != controlledsession.CauseHostCancelV1 ||
		result.SessionResult.RuntimeObservationStatus.Kind != controlledsession.RuntimeObservationLostV1 ||
		result.SessionResult.WorkloadOutputFinalizationStatus.Kind != controlledsession.WorkloadOutputFinalizationFailedV1 ||
		!result.ResultDelivered || !result.ResultAcknowledged {
		t.Fatalf("late output failure result = %#v", result)
	}
}

func TestRunControlledSessionV1CancelsBlockedInputBeforeStoppingWorkload(t *testing.T) {
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
	workload.blockInput = true
	ctx, cancel := context.WithCancel(context.Background())
	resultDone := make(chan struct {
		result ControlledSessionRunResultV1
		err    error
	}, 1)
	go func() {
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
		resultDone <- struct {
			result ControlledSessionRunResultV1
			err    error
		}{result: result, err: err}
	}()
	requests <- controlledsession.RequestV1{Kind: controlledsession.RequestInputV1, Bytes: []byte("blocked")}
	select {
	case <-workload.inputStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("input request did not reach workload")
	}
	cancel()
	select {
	case invocation := <-resultDone:
		if invocation.err != nil {
			t.Fatal(invocation.err)
		}
		if invocation.result.SessionResult.Cause != controlledsession.CauseHostCancelV1 ||
			!invocation.result.ResultDelivered || !invocation.result.ResultAcknowledged ||
			!workload.gracefulStopped || !workload.cleaned {
			t.Fatalf("host cancellation result = %#v, workload = %#v", invocation.result, workload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("controlled session did not cancel blocked input before workload teardown")
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
	requests          chan controlledsession.RequestV1
	mu                sync.Mutex
	events            []controlledsession.EventV1
	onRequest         func(controlledsession.RequestV1)
	onEvent           func(controlledsession.EventV1)
	writeErr          error
	blockEventKind    controlledsession.EventKindV1
	eventWriteBlocked chan struct{}
	releaseEventWrite chan struct{}
}

func (transport *fakeControlledSessionTransportV1) ReadRequest(ctx context.Context) (controlledsession.RequestV1, error) {
	select {
	case request, ok := <-transport.requests:
		if !ok {
			return controlledsession.RequestV1{}, io.EOF
		}
		if transport.onRequest != nil {
			transport.onRequest(request)
		}
		return request, nil
	case <-ctx.Done():
		return controlledsession.RequestV1{}, ctx.Err()
	}
}

func (transport *fakeControlledSessionTransportV1) WriteEvent(ctx context.Context, event controlledsession.EventV1) error {
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
	if event.Kind == transport.blockEventKind {
		close(transport.eventWriteBlocked)
		select {
		case <-transport.releaseEventWrite:
		case <-ctx.Done():
			return ctx.Err()
		}
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

type fakeControlledSessionWatchdogV1 struct {
	disarmed bool
	err      error
	onDisarm func()
}

func (watchdog *fakeControlledSessionWatchdogV1) Disarm(context.Context) error {
	watchdog.disarmed = true
	if watchdog.onDisarm != nil {
		watchdog.onDisarm()
	}
	return watchdog.err
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

func (process *fakeControlledSessionProcessV1) ContainerID() string {
	return dockerControllerTestContainerIDV1
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
	reader            *io.PipeReader
	writer            *io.PipeWriter
	output            []byte
	exitCode          int
	exitOnStart       bool
	exitOnGraceful    bool
	gracefulOutputErr error
	startPartially    bool
	startErr          error
	waitErr           error
	closed            bool
	inputMu           sync.Mutex
	input             []byte
	blockInput        bool
	inputStarted      chan struct{}
	inputStartOnce    sync.Once
	columns           uint32
	rows              uint32
}

func (workload *fakeControlledSessionWorkloadV1) ContainerID() string {
	return dockerWorkloadTestContainerIDV1
}

func newFakeControlledSessionWorkloadV1(output []byte, exitCode int) *fakeControlledSessionWorkloadV1 {
	reader, writer := io.Pipe()
	return &fakeControlledSessionWorkloadV1{
		fakeControlledSessionProcessV1: newFakeControlledSessionProcessV1(),
		reader:                         reader, writer: writer, output: append([]byte(nil), output...), exitCode: exitCode, exitOnStart: true, exitOnGraceful: true,
		inputStarted: make(chan struct{}),
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
	if workload.gracefulOutputErr != nil {
		_ = workload.writer.CloseWithError(workload.gracefulOutputErr)
	} else {
		_ = workload.writer.Close()
	}
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

func (workload *fakeControlledSessionWorkloadV1) WriteInput(ctx context.Context, input []byte) error {
	workload.inputStartOnce.Do(func() { close(workload.inputStarted) })
	if workload.blockInput {
		<-ctx.Done()
		return ctx.Err()
	}
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
