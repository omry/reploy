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

func TestLifecycleWorkloadExitAllowsOnlyBoundedControllerFinalization(t *testing.T) {
	machine := activatedMachineV1(t)
	code := 0
	transition, err := machine.Observe(ObservationV1{
		Kind:           ObservationWorkloadExitV1,
		WorkloadStatus: &ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code},
	})
	if err != nil {
		t.Fatalf("Observe(workload exit) error = %v", err)
	}
	if !transition.CauseLatched || !transition.BeginTermination || !transition.AwaitingControllerFinalization || transition.Cause != CauseWorkloadExitV1 {
		t.Fatalf("workload-exit transition = %#v", transition)
	}
	if _, err := machine.ApplyRequest(RequestV1{Kind: RequestInputV1, Bytes: []byte("late")}); !errors.Is(err, ErrRequestRejected) {
		t.Fatalf("late input error = %v", err)
	}
	transition, err = machine.ApplyRequest(RequestV1{Kind: RequestCompleteV1})
	if err != nil {
		t.Fatalf("ApplyRequest(complete) error = %v", err)
	}
	if transition.Cause != CauseWorkloadExitV1 || transition.AwaitingControllerFinalization {
		t.Fatalf("finalization transition = %#v", transition)
	}
	transition, err = machine.Observe(ObservationV1{Kind: ObservationFinishedV1, Finish: &FinishV1{
		WorkloadStatus:   ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code},
		ControllerStatus: ControllerStatusV1{Kind: ControllerStatusCompletedV1},
		CleanupStatus:    CleanupStatusV1{Kind: CleanupStatusSucceededV1}, RecoveryAction: RecoveryNoneV1,
	}})
	if err != nil {
		t.Fatalf("Observe(finished) error = %v", err)
	}
	if transition.After != StateTerminatedV1 || transition.Result == nil || transition.Result.Cause != CauseWorkloadExitV1 {
		t.Fatalf("finish transition = %#v", transition)
	}
}

func TestLifecycleConnectionClosureCannotBecomeSuccess(t *testing.T) {
	machine := activatedMachineV1(t)
	transition, err := machine.Observe(ObservationV1{Kind: ObservationControllerLostV1, Reason: "session channel closed"})
	if err != nil {
		t.Fatalf("Observe(controller lost) error = %v", err)
	}
	if transition.Cause != CauseControllerLostV1 || !transition.BeginTermination {
		t.Fatalf("controller-loss transition = %#v", transition)
	}
	_, err = machine.Observe(ObservationV1{Kind: ObservationFinishedV1, Finish: &FinishV1{
		WorkloadStatus:   ProcessStatusV1{Kind: ProcessStatusTerminatedV1, Reason: "lease teardown"},
		ControllerStatus: ControllerStatusV1{Kind: ControllerStatusCompletedV1},
		CleanupStatus:    CleanupStatusV1{Kind: CleanupStatusSucceededV1}, RecoveryAction: RecoveryNoneV1,
	}})
	if !errors.Is(err, ErrObservationRejected) {
		t.Fatalf("forged successful controller status error = %v", err)
	}
	transition, err = machine.Observe(ObservationV1{Kind: ObservationFinishedV1, Finish: &FinishV1{
		WorkloadStatus:   ProcessStatusV1{Kind: ProcessStatusTerminatedV1, Reason: "lease teardown"},
		ControllerStatus: ControllerStatusV1{Kind: ControllerStatusLostV1, Reason: "session channel closed"},
		CleanupStatus:    CleanupStatusV1{Kind: CleanupStatusSucceededV1}, RecoveryAction: RecoveryNoneV1,
	}})
	if err != nil {
		t.Fatalf("Observe(finished lost controller) error = %v", err)
	}
	if transition.Result.ControllerStatus.Kind != ControllerStatusLostV1 {
		t.Fatalf("result = %#v", transition.Result)
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
	if err != nil {
		t.Fatalf("pre-activation startup failure error = %v", err)
	}
	if transition.Cause != CauseHostCancelV1 {
		t.Fatalf("cause = %q, want %q", transition.Cause, CauseHostCancelV1)
	}
	if snapshot := preActivation.Snapshot(); snapshot.ControllerStatus.Kind != ControllerStatusStartupFailedV1 {
		t.Fatalf("controller status = %#v", snapshot.ControllerStatus)
	}
}

func TestLifecycleAbortiveActionsEndControllerFinalizationWait(t *testing.T) {
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
			code := 0
			if _, err := machine.Observe(ObservationV1{
				Kind:           ObservationWorkloadExitV1,
				WorkloadStatus: &ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code},
			}); err != nil {
				t.Fatal(err)
			}
			transition, err := action.apply(machine)
			if err != nil {
				t.Fatalf("abort controller finalization error = %v", err)
			}
			if transition.Cause != CauseWorkloadExitV1 || transition.AwaitingControllerFinalization {
				t.Fatalf("abort transition = %#v", transition)
			}
			if _, err := machine.Observe(ObservationV1{Kind: ObservationFinishedV1, Finish: &FinishV1{
				WorkloadStatus:   ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code},
				ControllerStatus: ControllerStatusV1{Kind: ControllerStatusStoppedV1},
				CleanupStatus:    CleanupStatusV1{Kind: CleanupStatusSucceededV1}, RecoveryAction: RecoveryNoneV1,
			}}); err != nil {
				t.Fatalf("finish after abort error = %v", err)
			}
		})
	}
}

func TestLifecycleEnforcesAuthorizationAndIdempotentTermination(t *testing.T) {
	authorization := testAuthorizationV1()
	authorization.EndpointIDs = []string{"browser"}
	machine, err := NewMachineV1(authorization)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := machine.ApplyRequest(RequestV1{Kind: RequestCompleteV1}); !errors.Is(err, ErrRequestRejected) {
		t.Fatalf("pre-activation request error = %v", err)
	}
	if _, err := machine.Observe(ObservationV1{Kind: ObservationActivatedV1}); err != nil {
		t.Fatal(err)
	}
	if _, err := machine.ApplyRequest(RequestV1{Kind: RequestOpenEndpointV1, EndpointID: "terminal"}); !errors.Is(err, ErrRequestRejected) {
		t.Fatalf("ungranted endpoint error = %v", err)
	}
	first, err := machine.ApplyRequest(RequestV1{Kind: RequestTerminateV1})
	if err != nil || !first.CauseLatched || first.Cause != CauseControllerTerminateV1 {
		t.Fatalf("first terminate = %#v, %v", first, err)
	}
	second, err := machine.ApplyRequest(RequestV1{Kind: RequestTerminateV1})
	if err != nil || second.CauseLatched || second.Cause != CauseControllerTerminateV1 {
		t.Fatalf("repeated terminate = %#v, %v", second, err)
	}
	if _, err := machine.ApplyRequest(RequestV1{Kind: RequestResizeV1, Columns: 80, Rows: 24}); !errors.Is(err, ErrRequestRejected) {
		t.Fatalf("resize while terminating error = %v", err)
	}
}

func TestLifecycleCopiesAuthorizationBeforeEnforcingIt(t *testing.T) {
	authorization := testAuthorizationV1()
	authorization.Operations = []OperationV1{OperationCompleteV1}
	authorization.EndpointIDs = []string{}
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

func TestLifecyclePreservesAcceptedCompletionAfterChannelCloses(t *testing.T) {
	machine := activatedMachineV1(t)
	if _, err := machine.ApplyRequest(RequestV1{Kind: RequestCompleteV1}); err != nil {
		t.Fatal(err)
	}
	transition, err := machine.Observe(ObservationV1{Kind: ObservationControllerLostV1, Reason: "channel closed during teardown"})
	if err != nil {
		t.Fatal(err)
	}
	if transition.Cause != CauseControllerCompleteV1 {
		t.Fatalf("cause = %q, want %q", transition.Cause, CauseControllerCompleteV1)
	}
	if snapshot := machine.Snapshot(); snapshot.ControllerStatus.Kind != ControllerStatusCompletedV1 {
		t.Fatalf("controller status = %#v", snapshot.ControllerStatus)
	}
}

func TestLifecycleFinalizationExpiryCannotBeRewrittenByLateComplete(t *testing.T) {
	machine := activatedMachineV1(t)
	code := 1
	if _, err := machine.Observe(ObservationV1{Kind: ObservationWorkloadExitV1, WorkloadStatus: &ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code}}); err != nil {
		t.Fatal(err)
	}
	if _, err := machine.Observe(ObservationV1{Kind: ObservationControllerFinalizationExpiredV1}); err != nil {
		t.Fatal(err)
	}
	if _, err := machine.ApplyRequest(RequestV1{Kind: RequestCompleteV1}); !errors.Is(err, ErrRequestRejected) {
		t.Fatalf("late complete error = %v", err)
	}
	snapshot := machine.Snapshot()
	if snapshot.Cause != CauseWorkloadExitV1 || snapshot.ControllerStatus.Kind != ControllerStatusFinalizationTimeoutV1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestLifecycleAcceptsOnlyPostResultAcknowledgement(t *testing.T) {
	machine := activatedMachineV1(t)
	if _, err := machine.ApplyRequest(RequestV1{Kind: RequestAcknowledgeTerminatedV1}); !errors.Is(err, ErrRequestRejected) {
		t.Fatalf("early acknowledgement error = %v", err)
	}
	if _, err := machine.ApplyRequest(RequestV1{Kind: RequestCompleteV1}); err != nil {
		t.Fatal(err)
	}
	code := 0
	if _, err := machine.Observe(ObservationV1{Kind: ObservationFinishedV1, Finish: &FinishV1{
		WorkloadStatus:   ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code},
		ControllerStatus: ControllerStatusV1{Kind: ControllerStatusCompletedV1},
		CleanupStatus:    CleanupStatusV1{Kind: CleanupStatusSucceededV1}, RecoveryAction: RecoveryNoneV1,
	}}); err != nil {
		t.Fatal(err)
	}
	if snapshot := machine.Snapshot(); snapshot.Result == nil || snapshot.ResultAcknowledged {
		t.Fatalf("pre-acknowledgement snapshot = %#v", snapshot)
	}
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
