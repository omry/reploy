package controlledsession

import (
	"errors"
	"fmt"
	"sync"
)

type StateV1 string

const (
	StatePreparingV1   StateV1 = "preparing"
	StateActiveV1      StateV1 = "active"
	StateTerminatingV1 StateV1 = "terminating"
	StateTerminatedV1  StateV1 = "terminated"
)

type FinishV1 struct {
	WorkloadStatus                   ProcessStatusV1
	WorkloadOutputFinalizationStatus WorkloadOutputFinalizationStatusV1
	ControllerFinalizationStatus     ControllerFinalizationStatusV1
	CleanupStatus                    CleanupStatusV1
	RecoveryAction                   RecoveryActionV1
}

type ObservationKindV1 string

const (
	ObservationActivatedV1                         ObservationKindV1 = "activated"
	ObservationWorkloadExitV1                      ObservationKindV1 = "workload-exit"
	ObservationHostCancelV1                        ObservationKindV1 = "host-cancel"
	ObservationControllerLostV1                    ObservationKindV1 = "controller-lost"
	ObservationRuntimeObservationLostV1            ObservationKindV1 = "runtime-observation-lost"
	ObservationStartupFailureV1                    ObservationKindV1 = "startup-failure"
	ObservationWorkloadOutputsFinalizedV1          ObservationKindV1 = "workload-outputs-finalized"
	ObservationWorkloadOutputFinalizationExpiredV1 ObservationKindV1 = "workload-output-finalization-expired"
	ObservationControllerFinalizationExpiredV1     ObservationKindV1 = "controller-finalization-expired"
	ObservationFinishedV1                          ObservationKindV1 = "finished"
	ObservationResultDeliveredV1                   ObservationKindV1 = "result-delivered"
)

type ObservationV1 struct {
	Kind                             ObservationKindV1
	WorkloadStatus                   *ProcessStatusV1
	WorkloadOutputFinalizationStatus *WorkloadOutputFinalizationStatusV1
	Reason                           string
	Finish                           *FinishV1
}

type SnapshotV1 struct {
	State                              StateV1
	Cause                              TerminationCauseV1
	WorkloadStatus                     ProcessStatusV1
	WorkloadOutputFinalizationStatus   WorkloadOutputFinalizationStatusV1
	RuntimeObservationStatus           RuntimeObservationStatusV1
	ControllerFinalizationStatus       ControllerFinalizationStatusV1
	AwaitingWorkloadOutputFinalization bool
	AwaitingControllerFinalization     bool
	AwaitingResultAcknowledgement      bool
	ResultAcknowledged                 bool
	Result                             *ResultV1
}

type TransitionV1 struct {
	Before                             StateV1
	After                              StateV1
	Cause                              TerminationCauseV1
	CauseLatched                       bool
	BeginTermination                   bool
	WorkloadOutputFinalizationStatus   WorkloadOutputFinalizationStatusV1
	AwaitingWorkloadOutputFinalization bool
	AwaitingControllerFinalization     bool
	AwaitingResultAcknowledgement      bool
	RequestAccepted                    bool
	ResultAcknowledged                 bool
	Result                             *ResultV1
}

var ErrRequestRejected = errors.New("controlled-session request rejected")
var ErrObservationRejected = errors.New("controlled-session observation rejected")

// MachineV1 serializes the authoritative lifecycle of one session. Its mutex
// makes the first accepted termination cause deterministic even when host
// observations race.
type MachineV1 struct {
	mu                 sync.Mutex
	authorization      AuthorizationV1
	state              StateV1
	activated          bool
	cause              TerminationCauseV1
	workload           ProcessStatusV1
	workloadOutputs    WorkloadOutputFinalizationStatusV1
	controller         ControllerFinalizationStatusV1
	runtimeObservation RuntimeObservationStatusV1
	waitingOutputs     bool
	waitingFinalize    bool
	resultDelivered    bool
	resultAcknowledged bool
	result             *ResultV1
}

func NewMachineV1(authorization AuthorizationV1) (*MachineV1, error) {
	if err := ValidateAuthorizationV1(authorization); err != nil {
		return nil, fmt.Errorf("create controlled-session lifecycle: %w", err)
	}
	return &MachineV1{
		authorization:      cloneAuthorizationV1(authorization),
		state:              StatePreparingV1,
		workload:           ProcessStatusV1{Kind: ProcessStatusUnknownV1},
		runtimeObservation: RuntimeObservationStatusV1{Kind: RuntimeObservationMaintainedV1},
		controller:         ControllerFinalizationStatusV1{Kind: ControllerFinalizationUnknownV1},
	}, nil
}

func (machine *MachineV1) Snapshot() SnapshotV1 {
	machine.mu.Lock()
	defer machine.mu.Unlock()
	return machine.snapshotLocked()
}

func (machine *MachineV1) Observe(observation ObservationV1) (TransitionV1, error) {
	machine.mu.Lock()
	defer machine.mu.Unlock()
	before := machine.state
	transition := TransitionV1{Before: before, After: before, Cause: machine.cause}
	if observation.Kind == ObservationResultDeliveredV1 {
		if observation.WorkloadStatus != nil || observation.WorkloadOutputFinalizationStatus != nil || observation.Reason != "" || observation.Finish != nil || machine.state != StateTerminatedV1 || machine.result == nil {
			return transition, fmt.Errorf("%w: result delivery is valid only after termination and carries no payload", ErrObservationRejected)
		}
		machine.resultDelivered = true
		transition.AwaitingResultAcknowledgement = !machine.resultAcknowledged
		transition.ResultAcknowledged = machine.resultAcknowledged
		transition.Result = cloneResultV1(machine.result)
		return transition, nil
	}
	if machine.state == StateTerminatedV1 {
		return transition, fmt.Errorf("%w: lifecycle is already terminated", ErrObservationRejected)
	}

	switch observation.Kind {
	case ObservationActivatedV1:
		if observation.WorkloadStatus != nil || observation.WorkloadOutputFinalizationStatus != nil || observation.Reason != "" || observation.Finish != nil || machine.state != StatePreparingV1 {
			return transition, fmt.Errorf("%w: activation is valid only while preparing and carries no payload", ErrObservationRejected)
		}
		machine.state = StateActiveV1
		machine.activated = true
		machine.controller = ControllerFinalizationStatusV1{Kind: ControllerFinalizationActiveV1}
	case ObservationWorkloadExitV1:
		if observation.WorkloadStatus == nil || observation.WorkloadOutputFinalizationStatus != nil || observation.Reason != "" || observation.Finish != nil {
			return transition, fmt.Errorf("%w: workload exit requires exactly one workload status", ErrObservationRejected)
		}
		if machine.workloadOutputs.Kind != "" && !(machine.activated &&
			machine.state == StateTerminatingV1 &&
			machine.workload.Kind == ProcessStatusUnknownV1 &&
			machine.workloadOutputs.Kind == WorkloadOutputFinalizationFailedV1) {
			return transition, fmt.Errorf("%w: workload exit cannot follow workload output finalization", ErrObservationRejected)
		}
		if machine.state == StatePreparingV1 {
			return transition, fmt.Errorf("%w: workload exit is invalid before activation", ErrObservationRejected)
		}
		if err := validateProcessStatusV1(*observation.WorkloadStatus, false); err != nil {
			return transition, fmt.Errorf("%w: %v", ErrObservationRejected, err)
		}
		if machine.workload.Kind != ProcessStatusUnknownV1 && !equalProcessStatusV1(machine.workload, *observation.WorkloadStatus) {
			return transition, fmt.Errorf("%w: workload exit conflicts with the status already observed", ErrObservationRejected)
		}
		machine.workload = cloneProcessStatusV1(*observation.WorkloadStatus)
		if machine.state == StateActiveV1 {
			machine.latchLocked(CauseWorkloadExitV1, &transition)
		}
	case ObservationHostCancelV1:
		if err := validateCauseObservationV1(observation); err != nil {
			return transition, err
		}
		machine.latchLocked(CauseHostCancelV1, &transition)
	case ObservationControllerLostV1:
		if observation.WorkloadStatus != nil || observation.WorkloadOutputFinalizationStatus != nil || observation.Finish != nil {
			return transition, fmt.Errorf("%w: controller loss carries only an optional reason", ErrObservationRejected)
		}
		if err := validateOptionalSafeTextV1("controller-loss reason", observation.Reason); err != nil {
			return transition, fmt.Errorf("%w: %v", ErrObservationRejected, err)
		}
		if machine.controller.Kind == ControllerFinalizationUnknownV1 || machine.controller.Kind == ControllerFinalizationActiveV1 {
			machine.controller = ControllerFinalizationStatusV1{Kind: ControllerFinalizationLostV1, Reason: observation.Reason}
		}
		machine.waitingFinalize = false
		machine.latchLocked(CauseControllerLostV1, &transition)
	case ObservationRuntimeObservationLostV1:
		if err := validateCauseObservationV1(observation); err != nil {
			return transition, err
		}
		if machine.runtimeObservation.Kind != RuntimeObservationLostV1 {
			machine.runtimeObservation = RuntimeObservationStatusV1{Kind: RuntimeObservationLostV1, Reason: observation.Reason}
		}
		machine.latchLocked(CauseRuntimeObservationLostV1, &transition)
		// Observation loss does not close an activated workload's output
		// surfaces. Keep that barrier pending until the supervisor explicitly
		// reports failed closure or its bounded finalization deadline expires.
		machine.finalizePreActivationOutputsForRuntimeObservationLossLocked(observation.Reason)
	case ObservationStartupFailureV1:
		if observation.WorkloadStatus != nil || observation.WorkloadOutputFinalizationStatus != nil || observation.Finish != nil {
			return transition, fmt.Errorf("%w: startup failure carries only a reason", ErrObservationRejected)
		}
		if err := validateRequiredSafeTextV1("startup-failure reason", observation.Reason); err != nil {
			return transition, fmt.Errorf("%w: %v", ErrObservationRejected, err)
		}
		if machine.controller.Kind != ControllerFinalizationUnknownV1 {
			return transition, fmt.Errorf("%w: startup failure is invalid after controller activation", ErrObservationRejected)
		}
		machine.controller = ControllerFinalizationStatusV1{Kind: ControllerFinalizationStartupFailedV1, Reason: observation.Reason}
		machine.latchLocked(CauseStartupFailureV1, &transition)
	case ObservationWorkloadOutputsFinalizedV1:
		if observation.WorkloadStatus != nil || observation.WorkloadOutputFinalizationStatus == nil || observation.Reason != "" || observation.Finish != nil || !machine.waitingOutputs {
			return transition, fmt.Errorf("%w: workload output finalization requires exactly one status while output finalization is pending", ErrObservationRejected)
		}
		if machine.activated && machine.workload.Kind == ProcessStatusUnknownV1 &&
			!(machine.runtimeObservation.Kind == RuntimeObservationLostV1 &&
				observation.WorkloadOutputFinalizationStatus.Kind == WorkloadOutputFinalizationFailedV1) {
			return transition, fmt.Errorf("%w: workload output finalization requires an observed terminal workload status", ErrObservationRejected)
		}
		if err := validateWorkloadOutputFinalizationStatusV1(*observation.WorkloadOutputFinalizationStatus); err != nil {
			return transition, fmt.Errorf("%w: %v", ErrObservationRejected, err)
		}
		if machine.runtimeObservation.Kind == RuntimeObservationLostV1 && observation.WorkloadOutputFinalizationStatus.Kind == WorkloadOutputFinalizationDrainedV1 {
			return transition, fmt.Errorf("%w: runtime observation loss requires failed workload output finalization", ErrObservationRejected)
		}
		machine.completeOutputFinalizationLocked(*observation.WorkloadOutputFinalizationStatus)
	case ObservationWorkloadOutputFinalizationExpiredV1:
		if observation.WorkloadStatus != nil || observation.WorkloadOutputFinalizationStatus != nil || observation.Finish != nil || !machine.waitingOutputs {
			return transition, fmt.Errorf("%w: output-finalization expiry carries only a required reason while output finalization is pending", ErrObservationRejected)
		}
		if err := validateRequiredSafeTextV1("workload output finalization expiry reason", observation.Reason); err != nil {
			return transition, fmt.Errorf("%w: %v", ErrObservationRejected, err)
		}
		machine.completeOutputFinalizationLocked(WorkloadOutputFinalizationStatusV1{
			Kind:   WorkloadOutputFinalizationFailedV1,
			Reason: observation.Reason,
		})
	case ObservationControllerFinalizationExpiredV1:
		if observation.WorkloadStatus != nil || observation.WorkloadOutputFinalizationStatus != nil || observation.Finish != nil || observation.Reason != "" || !machine.waitingFinalize {
			return transition, fmt.Errorf("%w: finalization expiry requires an active controller-finalization wait", ErrObservationRejected)
		}
		machine.waitingFinalize = false
		machine.controller = ControllerFinalizationStatusV1{Kind: ControllerFinalizationTimeoutV1}
	case ObservationFinishedV1:
		if observation.WorkloadStatus != nil || observation.WorkloadOutputFinalizationStatus != nil || observation.Reason != "" || observation.Finish == nil {
			return transition, fmt.Errorf("%w: finish requires exactly one terminal status set", ErrObservationRejected)
		}
		if machine.state != StateTerminatingV1 || machine.waitingOutputs || machine.workloadOutputs.Kind == "" || machine.waitingFinalize {
			return transition, fmt.Errorf("%w: finish requires finalized workload output and no pending output or controller finalization", ErrObservationRejected)
		}
		if err := validateFinishV1(*observation.Finish); err != nil {
			return transition, fmt.Errorf("%w: %v", ErrObservationRejected, err)
		}
		if machine.workload.Kind != ProcessStatusUnknownV1 && !equalProcessStatusV1(machine.workload, observation.Finish.WorkloadStatus) {
			return transition, fmt.Errorf("%w: terminal workload status conflicts with the status already observed", ErrObservationRejected)
		}
		if machine.workloadOutputs != observation.Finish.WorkloadOutputFinalizationStatus {
			return transition, fmt.Errorf("%w: terminal workload output finalization status conflicts with the status already observed", ErrObservationRejected)
		}
		if err := machine.validateControllerFinishLocked(observation.Finish.ControllerFinalizationStatus); err != nil {
			return transition, fmt.Errorf("%w: %v", ErrObservationRejected, err)
		}
		result := ResultV1{
			Cause: machine.cause, WorkloadStatus: cloneProcessStatusV1(observation.Finish.WorkloadStatus),
			WorkloadOutputFinalizationStatus: observation.Finish.WorkloadOutputFinalizationStatus,
			RuntimeObservationStatus:         machine.runtimeObservation,
			ControllerFinalizationStatus:     observation.Finish.ControllerFinalizationStatus, CleanupStatus: observation.Finish.CleanupStatus,
			RecoveryAction: observation.Finish.RecoveryAction,
		}
		if err := ValidateResultV1(result); err != nil {
			return transition, fmt.Errorf("%w: %v", ErrObservationRejected, err)
		}
		machine.workload = result.WorkloadStatus
		machine.controller = result.ControllerFinalizationStatus
		machine.result = &result
		machine.state = StateTerminatedV1
	default:
		return transition, fmt.Errorf("%w: observation kind %q is unsupported", ErrObservationRejected, observation.Kind)
	}

	transition.After = machine.state
	transition.Cause = machine.cause
	transition.WorkloadOutputFinalizationStatus = machine.workloadOutputs
	transition.AwaitingWorkloadOutputFinalization = machine.waitingOutputs
	transition.AwaitingControllerFinalization = machine.waitingFinalize
	transition.AwaitingResultAcknowledgement = machine.resultDelivered && !machine.resultAcknowledged
	transition.ResultAcknowledged = machine.resultAcknowledged
	transition.Result = cloneResultV1(machine.result)
	return transition, nil
}

func (machine *MachineV1) validateControllerFinishLocked(status ControllerFinalizationStatusV1) error {
	switch machine.controller.Kind {
	case ControllerFinalizationCompletedV1, ControllerFinalizationLostV1, ControllerFinalizationTimeoutV1, ControllerFinalizationStartupFailedV1:
		if machine.controller != status {
			return fmt.Errorf("terminal controller finalization status conflicts with the status already observed")
		}
	case ControllerFinalizationActiveV1, ControllerFinalizationUnknownV1:
		if status.Kind != ControllerFinalizationNotCompletedV1 {
			return fmt.Errorf("controller without explicit completion must finish as not-completed")
		}
	default:
		return fmt.Errorf("recorded controller finalization status %q is invalid before finish", machine.controller.Kind)
	}
	return nil
}

func (machine *MachineV1) ApplyRequest(request RequestV1) (TransitionV1, error) {
	machine.mu.Lock()
	defer machine.mu.Unlock()
	transition := TransitionV1{Before: machine.state, After: machine.state, Cause: machine.cause}
	if err := ValidateRequestV1(request); err != nil {
		return transition, fmt.Errorf("%w: %v", ErrRequestRejected, err)
	}
	// A terminal acknowledgement is protocol flow control, not a granted
	// controller capability. Its lifecycle position is the authorization.
	if request.Kind == RequestAcknowledgeTerminatedV1 {
		if machine.state != StateTerminatedV1 || machine.result == nil || !machine.resultDelivered {
			return transition, fmt.Errorf("%w: terminal result has not been delivered", ErrRequestRejected)
		}
		machine.resultAcknowledged = true
		transition.RequestAccepted = true
		transition.AwaitingResultAcknowledgement = false
		transition.ResultAcknowledged = true
		transition.Result = cloneResultV1(machine.result)
		return transition, nil
	}
	operation := operationForRequestV1(request.Kind)
	if !containsOperationV1(machine.authorization.Operations, operation) {
		return transition, fmt.Errorf("%w: operation %q was not granted", ErrRequestRejected, operation)
	}

	switch machine.state {
	case StateActiveV1:
		switch request.Kind {
		case RequestInputV1, RequestResizeV1:
		case RequestTerminateV1:
			machine.latchLocked(CauseControllerTerminateV1, &transition)
		case RequestCompleteV1:
			return transition, fmt.Errorf("%w: completion is valid only after workload outputs are finalized", ErrRequestRejected)
		}
	case StateTerminatingV1:
		switch request.Kind {
		case RequestTerminateV1:
			// Repeated graceful termination is idempotent and does not alter
			// output or controller finalization already in progress.
		case RequestCompleteV1:
			if machine.waitingOutputs || !machine.waitingFinalize || machine.controller.Kind != ControllerFinalizationActiveV1 {
				return transition, fmt.Errorf("%w: completion requires finalized workload outputs and a pending controller finalization", ErrRequestRejected)
			}
			machine.waitingFinalize = false
			machine.controller = ControllerFinalizationStatusV1{Kind: ControllerFinalizationCompletedV1}
		default:
			return transition, fmt.Errorf("%w: %s is not accepted while terminating", ErrRequestRejected, request.Kind)
		}
	case StatePreparingV1, StateTerminatedV1:
		return transition, fmt.Errorf("%w: requests are not accepted while %s", ErrRequestRejected, machine.state)
	default:
		return transition, fmt.Errorf("%w: lifecycle state %q is invalid", ErrRequestRejected, machine.state)
	}

	transition.After = machine.state
	transition.Cause = machine.cause
	transition.WorkloadOutputFinalizationStatus = machine.workloadOutputs
	transition.AwaitingWorkloadOutputFinalization = machine.waitingOutputs
	transition.AwaitingControllerFinalization = machine.waitingFinalize
	transition.AwaitingResultAcknowledgement = machine.resultDelivered && !machine.resultAcknowledged
	transition.RequestAccepted = true
	transition.ResultAcknowledged = machine.resultAcknowledged
	return transition, nil
}

func (machine *MachineV1) latchLocked(cause TerminationCauseV1, transition *TransitionV1) {
	if machine.cause != "" {
		return
	}
	machine.cause = cause
	machine.state = StateTerminatingV1
	if transition.Before == StateActiveV1 {
		machine.waitingOutputs = true
	} else {
		// No workload ran, so there are no workload-originated surfaces to
		// drain. Record the barrier as satisfied rather than inventing a wait.
		machine.workloadOutputs = WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1}
	}
	transition.CauseLatched = true
	transition.BeginTermination = true
}

func (machine *MachineV1) snapshotLocked() SnapshotV1 {
	return SnapshotV1{
		State: machine.state, Cause: machine.cause, WorkloadStatus: cloneProcessStatusV1(machine.workload),
		WorkloadOutputFinalizationStatus: machine.workloadOutputs,
		RuntimeObservationStatus:         machine.runtimeObservation,
		ControllerFinalizationStatus:     machine.controller, AwaitingControllerFinalization: machine.waitingFinalize,
		AwaitingWorkloadOutputFinalization: machine.waitingOutputs,
		AwaitingResultAcknowledgement:      machine.resultDelivered && !machine.resultAcknowledged,
		ResultAcknowledged:                 machine.resultAcknowledged, Result: cloneResultV1(machine.result),
	}
}

func validateCauseObservationV1(observation ObservationV1) error {
	if observation.WorkloadStatus != nil || observation.WorkloadOutputFinalizationStatus != nil || observation.Finish != nil {
		return fmt.Errorf("%w: %s carries only an optional reason", ErrObservationRejected, observation.Kind)
	}
	if err := validateOptionalSafeTextV1(string(observation.Kind)+" reason", observation.Reason); err != nil {
		return fmt.Errorf("%w: %v", ErrObservationRejected, err)
	}
	return nil
}

func validateFinishV1(finish FinishV1) error {
	if err := validateProcessStatusV1(finish.WorkloadStatus, true); err != nil {
		return err
	}
	if err := validateWorkloadOutputFinalizationStatusV1(finish.WorkloadOutputFinalizationStatus); err != nil {
		return err
	}
	if err := validateTerminalControllerFinalizationStatusV1(finish.ControllerFinalizationStatus); err != nil {
		return err
	}
	return validateCleanupResultV1(finish.CleanupStatus, finish.RecoveryAction)
}

func (machine *MachineV1) finalizePreActivationOutputsForRuntimeObservationLossLocked(reason string) {
	if machine.activated ||
		machine.cause != CauseRuntimeObservationLostV1 ||
		machine.workloadOutputs.Kind != WorkloadOutputFinalizationDrainedV1 {
		return
	}
	if reason == "" {
		reason = "runtime observation was lost before workload output finalization completed"
	}
	machine.completeOutputFinalizationLocked(WorkloadOutputFinalizationStatusV1{
		Kind:   WorkloadOutputFinalizationFailedV1,
		Reason: reason,
	})
}

func (machine *MachineV1) completeOutputFinalizationLocked(status WorkloadOutputFinalizationStatusV1) {
	machine.workloadOutputs = status
	machine.waitingOutputs = false
	machine.waitingFinalize = machine.controller.Kind == ControllerFinalizationActiveV1 &&
		containsOperationV1(machine.authorization.Operations, OperationCompleteV1)
}

func equalProcessStatusV1(left ProcessStatusV1, right ProcessStatusV1) bool {
	if left.Kind != right.Kind || left.Reason != right.Reason || (left.Code == nil) != (right.Code == nil) {
		return false
	}
	return left.Code == nil || *left.Code == *right.Code
}

func cloneProcessStatusV1(status ProcessStatusV1) ProcessStatusV1 {
	result := status
	if status.Code != nil {
		code := *status.Code
		result.Code = &code
	}
	return result
}

func cloneResultV1(result *ResultV1) *ResultV1 {
	if result == nil {
		return nil
	}
	copy := *result
	copy.WorkloadStatus = cloneProcessStatusV1(result.WorkloadStatus)
	return &copy
}

func containsOperationV1(operations []OperationV1, target OperationV1) bool {
	for _, operation := range operations {
		if operation == target {
			return true
		}
	}
	return false
}
