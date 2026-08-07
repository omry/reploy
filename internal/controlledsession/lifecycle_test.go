package controlledsession

import (
	"errors"
	"sync"
	"testing"
)

func activatedMachineV1(t *testing.T) *MachineV1 {
	t.Helper()
	machine, err := NewMachineV1(testAuthorizationV1())
	if err != nil {
		t.Fatalf("NewMachineV1() error = %v", err)
	}
	transition, err := machine.Observe(ObservationV1{Kind: ObservationActivatedV1})
	if err != nil {
		t.Fatalf("Observe(activated) error = %v", err)
	}
	if transition.Before != StatePreparingV1 || transition.After != StateActiveV1 {
		t.Fatalf("activation transition = %#v", transition)
	}
	return machine
}

func observeWorkloadExitV1(t *testing.T, machine *MachineV1, code int) {
	t.Helper()
	if _, err := machine.Observe(ObservationV1{
		Kind:           ObservationWorkloadExitV1,
		WorkloadStatus: &ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code},
	}); err != nil {
		t.Fatalf("Observe(workload exit) error = %v", err)
	}
}

func observeOutputsFinalizedV1(t *testing.T, machine *MachineV1, status WorkloadOutputFinalizationStatusV1) TransitionV1 {
	t.Helper()
	transition, err := machine.Observe(ObservationV1{
		Kind:                             ObservationWorkloadOutputsFinalizedV1,
		WorkloadOutputFinalizationStatus: &status,
	})
	if err != nil {
		t.Fatalf("Observe(workload outputs finalized) error = %v", err)
	}
	return transition
}

func finishLifecycleV1(t *testing.T, machine *MachineV1, finish FinishV1) TransitionV1 {
	t.Helper()
	transition, err := machine.Observe(ObservationV1{Kind: ObservationFinishedV1, Finish: &finish})
	if err != nil {
		t.Fatalf("Observe(finished) error = %v", err)
	}
	return transition
}

func TestLifecycleOutputBarrierPrecedesControllerFinalization(t *testing.T) {
	machine := activatedMachineV1(t)
	code := 0
	observeWorkloadExitV1(t, machine, code)
	if _, err := machine.ApplyRequest(RequestV1{Kind: RequestInputV1, Bytes: []byte("late")}); !errors.Is(err, ErrRequestRejected) {
		t.Fatalf("late input error = %v", err)
	}

	if snapshot := machine.Snapshot(); snapshot.AwaitingControllerFinalization {
		t.Fatalf("controller finalization began before output barrier: %#v", snapshot)
	}
	if _, err := machine.ApplyRequest(RequestV1{Kind: RequestCompleteV1}); !errors.Is(err, ErrRequestRejected) {
		t.Fatalf("complete before output barrier error = %v", err)
	}
	if _, err := machine.Observe(ObservationV1{Kind: ObservationFinishedV1, Finish: &FinishV1{
		WorkloadStatus:                   ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code},
		WorkloadOutputFinalizationStatus: WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1},
		ControllerFinalizationStatus:     ControllerFinalizationStatusV1{Kind: ControllerFinalizationNotCompletedV1},
		CleanupStatus:                    CleanupStatusV1{Kind: CleanupStatusSucceededV1},
		RecoveryAction:                   RecoveryNoneV1,
	}}); !errors.Is(err, ErrObservationRejected) {
		t.Fatalf("finish before output barrier error = %v", err)
	}

	outputStatus := WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1}
	barrier := observeOutputsFinalizedV1(t, machine, outputStatus)
	if !barrier.AwaitingControllerFinalization || barrier.WorkloadOutputFinalizationStatus != outputStatus {
		t.Fatalf("output-barrier transition = %#v", barrier)
	}
	complete, err := machine.ApplyRequest(RequestV1{Kind: RequestCompleteV1})
	if err != nil || complete.AwaitingControllerFinalization {
		t.Fatalf("ApplyRequest(complete) = %#v, %v", complete, err)
	}

	finished := finishLifecycleV1(t, machine, FinishV1{
		WorkloadStatus:                   ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code},
		WorkloadOutputFinalizationStatus: outputStatus,
		ControllerFinalizationStatus:     ControllerFinalizationStatusV1{Kind: ControllerFinalizationCompletedV1},
		CleanupStatus:                    CleanupStatusV1{Kind: CleanupStatusSucceededV1},
		RecoveryAction:                   RecoveryNoneV1,
	})
	if finished.After != StateTerminatedV1 || finished.Result == nil {
		t.Fatalf("finish transition = %#v", finished)
	}
	if err := ValidateResultV1(*finished.Result); err != nil {
		t.Fatalf("terminal result is invalid: %v", err)
	}
}

func TestLifecycleSkipsFinalizationWaitWithoutCompleteGrant(t *testing.T) {
	authorization := testAuthorizationV1()
	authorization.Operations = []OperationV1{OperationInputV1, OperationResizeV1, OperationTerminateV1}
	machine, err := NewMachineV1(authorization)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := machine.Observe(ObservationV1{Kind: ObservationActivatedV1}); err != nil {
		t.Fatal(err)
	}

	code := 0
	observeWorkloadExitV1(t, machine, code)
	outputStatus := WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1}
	barrier := observeOutputsFinalizedV1(t, machine, outputStatus)
	if barrier.AwaitingControllerFinalization {
		t.Fatalf("output barrier entered an impossible finalization wait: %#v", barrier)
	}
	if _, err := machine.Observe(ObservationV1{Kind: ObservationControllerFinalizationExpiredV1}); !errors.Is(err, ErrObservationRejected) {
		t.Fatalf("finalization expiry without a wait error = %v", err)
	}

	finished := finishLifecycleV1(t, machine, FinishV1{
		WorkloadStatus:                   ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code},
		WorkloadOutputFinalizationStatus: outputStatus,
		ControllerFinalizationStatus:     ControllerFinalizationStatusV1{Kind: ControllerFinalizationNotCompletedV1},
		CleanupStatus:                    CleanupStatusV1{Kind: CleanupStatusSucceededV1},
		RecoveryAction:                   RecoveryNoneV1,
	})
	if finished.Result == nil || finished.Result.ControllerFinalizationStatus.Kind != ControllerFinalizationNotCompletedV1 {
		t.Fatalf("finish transition = %#v", finished)
	}
}

func TestLifecycleCompleteNeverTerminatesActiveWorkload(t *testing.T) {
	machine := activatedMachineV1(t)
	if _, err := machine.ApplyRequest(RequestV1{Kind: RequestCompleteV1}); !errors.Is(err, ErrRequestRejected) {
		t.Fatalf("active complete error = %v", err)
	}
	if snapshot := machine.Snapshot(); snapshot.State != StateActiveV1 || snapshot.Cause != "" || snapshot.ControllerFinalizationStatus.Kind != ControllerFinalizationActiveV1 {
		t.Fatalf("active complete changed lifecycle = %#v", snapshot)
	}
}

func TestLifecycleControllerTerminationUsesOutputBarrier(t *testing.T) {
	machine := activatedMachineV1(t)
	first, err := machine.ApplyRequest(RequestV1{Kind: RequestTerminateV1})
	if err != nil || !first.CauseLatched || first.Cause != CauseControllerTerminateV1 {
		t.Fatalf("first terminate = %#v, %v", first, err)
	}
	second, err := machine.ApplyRequest(RequestV1{Kind: RequestTerminateV1})
	if err != nil || second.CauseLatched || second.Cause != CauseControllerTerminateV1 {
		t.Fatalf("repeated terminate = %#v, %v", second, err)
	}
	outputStatus := WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1}
	if _, err := machine.Observe(ObservationV1{
		Kind:                             ObservationWorkloadOutputsFinalizedV1,
		WorkloadOutputFinalizationStatus: &outputStatus,
	}); !errors.Is(err, ErrObservationRejected) {
		t.Fatalf("output barrier before workload status error = %v", err)
	}

	code := 0
	observeWorkloadExitV1(t, machine, code)
	observeOutputsFinalizedV1(t, machine, outputStatus)
	third, err := machine.ApplyRequest(RequestV1{Kind: RequestTerminateV1})
	if err != nil || !third.AwaitingControllerFinalization {
		t.Fatalf("repeated terminate during finalization = %#v, %v", third, err)
	}
	if _, err := machine.ApplyRequest(RequestV1{Kind: RequestCompleteV1}); err != nil {
		t.Fatalf("complete after output barrier error = %v", err)
	}
}

func TestLifecycleRepeatedTerminationSignalsPreserveFinalizationWait(t *testing.T) {
	actions := []struct {
		name  string
		apply func(*MachineV1) (TransitionV1, error)
	}{
		{name: "controller terminate", apply: func(machine *MachineV1) (TransitionV1, error) {
			return machine.ApplyRequest(RequestV1{Kind: RequestTerminateV1})
		}},
		{name: "host cancel", apply: func(machine *MachineV1) (TransitionV1, error) {
			return machine.Observe(ObservationV1{Kind: ObservationHostCancelV1, Reason: "host interrupted"})
		}},
		{name: "runtime observation lost", apply: func(machine *MachineV1) (TransitionV1, error) {
			return machine.Observe(ObservationV1{Kind: ObservationRuntimeObservationLostV1, Reason: "docker unavailable"})
		}},
	}
	for _, action := range actions {
		t.Run(action.name, func(t *testing.T) {
			machine := activatedMachineV1(t)
			observeWorkloadExitV1(t, machine, 0)
			observeOutputsFinalizedV1(t, machine, WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1})
			transition, err := action.apply(machine)
			if err != nil || transition.Cause != CauseWorkloadExitV1 || !transition.AwaitingControllerFinalization {
				t.Fatalf("repeated termination signal = %#v, %v", transition, err)
			}
		})
	}
}

func TestLifecycleFailedOutputFinalizationRemainsAuthoritative(t *testing.T) {
	machine := activatedMachineV1(t)
	code := 1
	observeWorkloadExitV1(t, machine, code)
	outputStatus := WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationFailedV1, Reason: "output deadline expired"}
	observeOutputsFinalizedV1(t, machine, outputStatus)
	if _, err := machine.ApplyRequest(RequestV1{Kind: RequestCompleteV1}); err != nil {
		t.Fatal(err)
	}
	finished := finishLifecycleV1(t, machine, FinishV1{
		WorkloadStatus:                   ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code},
		WorkloadOutputFinalizationStatus: outputStatus,
		ControllerFinalizationStatus:     ControllerFinalizationStatusV1{Kind: ControllerFinalizationCompletedV1},
		CleanupStatus:                    CleanupStatusV1{Kind: CleanupStatusSucceededV1},
		RecoveryAction:                   RecoveryNoneV1,
	})
	if finished.Result.WorkloadOutputFinalizationStatus != outputStatus {
		t.Fatalf("result rewrote failed output finalization = %#v", finished.Result)
	}
}

func TestLifecycleOutputFinalizationIsSingleAndImmutable(t *testing.T) {
	machine := activatedMachineV1(t)
	observeWorkloadExitV1(t, machine, 0)
	drained := WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1}
	observeOutputsFinalizedV1(t, machine, drained)
	for _, status := range []WorkloadOutputFinalizationStatusV1{
		drained,
		{Kind: WorkloadOutputFinalizationFailedV1, Reason: "late failure"},
	} {
		if _, err := machine.Observe(ObservationV1{Kind: ObservationWorkloadOutputsFinalizedV1, WorkloadOutputFinalizationStatus: &status}); !errors.Is(err, ErrObservationRejected) {
			t.Fatalf("duplicate output finalization %#v error = %v", status, err)
		}
	}
}

func TestLifecycleOutputBarrierRejectsLateTerminalFacts(t *testing.T) {
	machine, err := NewMachineV1(testAuthorizationV1())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := machine.Observe(ObservationV1{Kind: ObservationHostCancelV1, Reason: "host interrupted startup"}); err != nil {
		t.Fatal(err)
	}
	observeOutputsFinalizedV1(t, machine, WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1})
	code := 0
	if _, err := machine.Observe(ObservationV1{
		Kind:           ObservationWorkloadExitV1,
		WorkloadStatus: &ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code},
	}); !errors.Is(err, ErrObservationRejected) {
		t.Fatalf("workload exit after output barrier error = %v", err)
	}
	if _, err := machine.Observe(ObservationV1{Kind: ObservationStartupFailureV1, Reason: "late startup failure"}); !errors.Is(err, ErrObservationRejected) {
		t.Fatalf("startup failure after output barrier error = %v", err)
	}
}

func TestLifecycleConnectionClosureCannotBecomeSuccess(t *testing.T) {
	machine := activatedMachineV1(t)
	transition, err := machine.Observe(ObservationV1{Kind: ObservationControllerLostV1, Reason: "session channel closed"})
	if err != nil || transition.Cause != CauseControllerLostV1 || !transition.BeginTermination {
		t.Fatalf("controller-loss transition = %#v, %v", transition, err)
	}
	status := ProcessStatusV1{Kind: ProcessStatusTerminatedV1, Reason: "lease teardown"}
	if _, err := machine.Observe(ObservationV1{Kind: ObservationWorkloadExitV1, WorkloadStatus: &status}); err != nil {
		t.Fatalf("Observe(workload exit after controller loss) error = %v", err)
	}
	observeOutputsFinalizedV1(t, machine, WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationFailedV1, Reason: "controller disconnected"})
	if snapshot := machine.Snapshot(); snapshot.AwaitingControllerFinalization {
		t.Fatalf("lost controller entered finalization wait: %#v", snapshot)
	}

	_, err = machine.Observe(ObservationV1{Kind: ObservationFinishedV1, Finish: &FinishV1{
		WorkloadStatus:                   status,
		WorkloadOutputFinalizationStatus: WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationFailedV1, Reason: "controller disconnected"},
		ControllerFinalizationStatus:     ControllerFinalizationStatusV1{Kind: ControllerFinalizationCompletedV1},
		CleanupStatus:                    CleanupStatusV1{Kind: CleanupStatusSucceededV1},
		RecoveryAction:                   RecoveryNoneV1,
	}})
	if !errors.Is(err, ErrObservationRejected) {
		t.Fatalf("forged successful controller finalization status error = %v", err)
	}
	finished := finishLifecycleV1(t, machine, FinishV1{
		WorkloadStatus:                   status,
		WorkloadOutputFinalizationStatus: WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationFailedV1, Reason: "controller disconnected"},
		ControllerFinalizationStatus:     ControllerFinalizationStatusV1{Kind: ControllerFinalizationLostV1, Reason: "session channel closed"},
		CleanupStatus:                    CleanupStatusV1{Kind: CleanupStatusSucceededV1},
		RecoveryAction:                   RecoveryNoneV1,
	})
	if finished.Result.ControllerFinalizationStatus.Kind != ControllerFinalizationLostV1 {
		t.Fatalf("result = %#v", finished.Result)
	}
}

func TestLifecyclePreservesAcceptedCompletionAfterChannelCloses(t *testing.T) {
	machine := activatedMachineV1(t)
	observeWorkloadExitV1(t, machine, 0)
	observeOutputsFinalizedV1(t, machine, WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1})
	if _, err := machine.ApplyRequest(RequestV1{Kind: RequestCompleteV1}); err != nil {
		t.Fatal(err)
	}
	transition, err := machine.Observe(ObservationV1{Kind: ObservationControllerLostV1, Reason: "channel closed during teardown"})
	if err != nil || transition.Cause != CauseWorkloadExitV1 {
		t.Fatalf("controller loss after completion = %#v, %v", transition, err)
	}
	if snapshot := machine.Snapshot(); snapshot.ControllerFinalizationStatus.Kind != ControllerFinalizationCompletedV1 {
		t.Fatalf("controller finalization status = %#v", snapshot.ControllerFinalizationStatus)
	}
}

func TestLifecycleFirstAcceptedCauseWinsConcurrentRace(t *testing.T) {
	machine := activatedMachineV1(t)
	code := 1
	observations := []ObservationV1{
		{Kind: ObservationHostCancelV1, Reason: "host interrupted"},
		{Kind: ObservationControllerLostV1, Reason: "channel closed"},
		{Kind: ObservationRuntimeObservationLostV1, Reason: "docker unavailable"},
		{Kind: ObservationWorkloadExitV1, WorkloadStatus: &ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code}},
	}
	var wait sync.WaitGroup
	latched := make(chan TerminationCauseV1, len(observations))
	for _, observation := range observations {
		observation := observation
		wait.Add(1)
		go func() {
			defer wait.Done()
			transition, err := machine.Observe(observation)
			if err != nil {
				t.Errorf("Observe(%s) error = %v", observation.Kind, err)
				return
			}
			if transition.CauseLatched {
				latched <- transition.Cause
			}
		}()
	}
	wait.Wait()
	close(latched)
	var causes []TerminationCauseV1
	for cause := range latched {
		causes = append(causes, cause)
	}
	if len(causes) != 1 {
		t.Fatalf("latched causes = %v, want exactly one", causes)
	}
	if snapshot := machine.Snapshot(); snapshot.Cause != causes[0] || snapshot.State != StateTerminatingV1 {
		t.Fatalf("snapshot = %#v, latched = %v", snapshot, causes)
	}
}

func TestLifecycleStartupFailureIsLimitedToPreActivation(t *testing.T) {
	active := activatedMachineV1(t)
	if _, err := active.Observe(ObservationV1{Kind: ObservationStartupFailureV1, Reason: "late startup failure"}); !errors.Is(err, ErrObservationRejected) {
		t.Fatalf("active startup failure error = %v", err)
	}

	preActivation, err := NewMachineV1(testAuthorizationV1())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := preActivation.Observe(ObservationV1{Kind: ObservationHostCancelV1, Reason: "host interrupted"}); err != nil {
		t.Fatal(err)
	}
	transition, err := preActivation.Observe(ObservationV1{Kind: ObservationStartupFailureV1, Reason: "startup stopped"})
	if err != nil || transition.Cause != CauseHostCancelV1 {
		t.Fatalf("pre-activation startup failure = %#v, %v", transition, err)
	}
	if snapshot := preActivation.Snapshot(); snapshot.ControllerFinalizationStatus.Kind != ControllerFinalizationStartupFailedV1 {
		t.Fatalf("controller finalization status = %#v", snapshot.ControllerFinalizationStatus)
	}
}

func TestLifecycleEnforcesAuthorizationAndCopiesIt(t *testing.T) {
	authorization := testAuthorizationV1()
	authorization.Operations = []OperationV1{OperationCompleteV1}
	machine, err := NewMachineV1(authorization)
	if err != nil {
		t.Fatal(err)
	}
	authorization.Operations[0] = OperationInputV1
	if _, err := machine.Observe(ObservationV1{Kind: ObservationActivatedV1}); err != nil {
		t.Fatal(err)
	}
	if _, err := machine.ApplyRequest(RequestV1{Kind: RequestInputV1, Bytes: []byte("not granted")}); !errors.Is(err, ErrRequestRejected) {
		t.Fatalf("request enabled through caller mutation error = %v", err)
	}
}

func TestLifecycleFinalizationExpiryCannotBeRewrittenByLateComplete(t *testing.T) {
	machine := activatedMachineV1(t)
	observeWorkloadExitV1(t, machine, 1)
	observeOutputsFinalizedV1(t, machine, WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1})
	if _, err := machine.Observe(ObservationV1{Kind: ObservationControllerFinalizationExpiredV1}); err != nil {
		t.Fatal(err)
	}
	if _, err := machine.ApplyRequest(RequestV1{Kind: RequestCompleteV1}); !errors.Is(err, ErrRequestRejected) {
		t.Fatalf("late complete error = %v", err)
	}
	if snapshot := machine.Snapshot(); snapshot.Cause != CauseWorkloadExitV1 || snapshot.ControllerFinalizationStatus.Kind != ControllerFinalizationTimeoutV1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestLifecycleCompleteAndExpiryHaveOneAuthoritativeOutcome(t *testing.T) {
	for attempt := 0; attempt < 50; attempt++ {
		machine := activatedMachineV1(t)
		observeWorkloadExitV1(t, machine, 0)
		observeOutputsFinalizedV1(t, machine, WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1})
		var wait sync.WaitGroup
		wait.Add(2)
		errorsSeen := make(chan error, 2)
		go func() {
			defer wait.Done()
			_, err := machine.ApplyRequest(RequestV1{Kind: RequestCompleteV1})
			errorsSeen <- err
		}()
		go func() {
			defer wait.Done()
			_, err := machine.Observe(ObservationV1{Kind: ObservationControllerFinalizationExpiredV1})
			errorsSeen <- err
		}()
		wait.Wait()
		close(errorsSeen)
		accepted := 0
		for err := range errorsSeen {
			if err == nil {
				accepted++
			} else if !errors.Is(err, ErrRequestRejected) && !errors.Is(err, ErrObservationRejected) {
				t.Fatalf("unexpected race error = %v", err)
			}
		}
		if accepted != 1 {
			t.Fatalf("accepted outcomes = %d, want 1", accepted)
		}
		kind := machine.Snapshot().ControllerFinalizationStatus.Kind
		if kind != ControllerFinalizationCompletedV1 && kind != ControllerFinalizationTimeoutV1 {
			t.Fatalf("controller finalization status = %q", kind)
		}
	}
}

func TestLifecycleAcceptsOnlyPostResultAcknowledgement(t *testing.T) {
	machine := activatedMachineV1(t)
	if _, err := machine.ApplyRequest(RequestV1{Kind: RequestAcknowledgeTerminatedV1}); !errors.Is(err, ErrRequestRejected) {
		t.Fatalf("early acknowledgement error = %v", err)
	}
	code := 0
	observeWorkloadExitV1(t, machine, code)
	outputStatus := WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1}
	observeOutputsFinalizedV1(t, machine, outputStatus)
	if _, err := machine.ApplyRequest(RequestV1{Kind: RequestCompleteV1}); err != nil {
		t.Fatal(err)
	}
	finishLifecycleV1(t, machine, FinishV1{
		WorkloadStatus:                   ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code},
		WorkloadOutputFinalizationStatus: outputStatus,
		ControllerFinalizationStatus:     ControllerFinalizationStatusV1{Kind: ControllerFinalizationCompletedV1},
		CleanupStatus:                    CleanupStatusV1{Kind: CleanupStatusSucceededV1},
		RecoveryAction:                   RecoveryNoneV1,
	})
	if _, err := machine.ApplyRequest(RequestV1{Kind: RequestAcknowledgeTerminatedV1}); !errors.Is(err, ErrRequestRejected) {
		t.Fatalf("pre-delivery acknowledgement error = %v", err)
	}
	delivered, err := machine.Observe(ObservationV1{Kind: ObservationResultDeliveredV1})
	if err != nil || !delivered.AwaitingResultAcknowledgement || delivered.Result == nil {
		t.Fatalf("result-delivered observation = %#v, %v", delivered, err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		transition, err := machine.ApplyRequest(RequestV1{Kind: RequestAcknowledgeTerminatedV1})
		if err != nil || !transition.RequestAccepted || !transition.ResultAcknowledged || transition.Result == nil {
			t.Fatalf("acknowledgement %d = %#v, %v", attempt+1, transition, err)
		}
	}
	if snapshot := machine.Snapshot(); !snapshot.ResultAcknowledged {
		t.Fatalf("post-acknowledgement snapshot = %#v", snapshot)
	}
	if _, err := machine.ApplyRequest(RequestV1{Kind: RequestResizeV1, Columns: 80, Rows: 24}); !errors.Is(err, ErrRequestRejected) {
		t.Fatalf("late resize error = %v", err)
	}
}

func TestLifecycleRejectsResultDeliveryBeforeTermination(t *testing.T) {
	machine := activatedMachineV1(t)
	if _, err := machine.Observe(ObservationV1{Kind: ObservationResultDeliveredV1}); !errors.Is(err, ErrObservationRejected) {
		t.Fatalf("early result-delivered observation error = %v", err)
	}
}
