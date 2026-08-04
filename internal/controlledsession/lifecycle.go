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
	WorkloadStatus               ProcessStatusV1
	ControllerFinalizationStatus ControllerFinalizationStatusV1
	CleanupStatus                CleanupStatusV1
	RecoveryAction               RecoveryActionV1
}

type ObservationKindV1 string

const (
	ObservationActivatedV1                     ObservationKindV1 = "activated"
	ObservationWorkloadExitV1                  ObservationKindV1 = "workload-exit"
	ObservationHostCancelV1                    ObservationKindV1 = "host-cancel"
	ObservationControllerLostV1                ObservationKindV1 = "controller-lost"
	ObservationRuntimeObservationLostV1        ObservationKindV1 = "runtime-observation-lost"
	ObservationStartupFailureV1                ObservationKindV1 = "startup-failure"
	ObservationControllerFinalizationExpiredV1 ObservationKindV1 = "controller-finalization-expired"
	ObservationFinishedV1                      ObservationKindV1 = "finished"
	ObservationResultDeliveredV1               ObservationKindV1 = "result-delivered"
)

type ObservationV1 struct {
	Kind           ObservationKindV1
	WorkloadStatus *ProcessStatusV1
	Reason         string
	Finish         *FinishV1
}

type SnapshotV1 struct {
	State                          StateV1
	Cause                          TerminationCauseV1
	WorkloadStatus                 ProcessStatusV1
	ControllerFinalizationStatus   ControllerFinalizationStatusV1
	AwaitingControllerFinalization bool
	AwaitingResultAcknowledgement  bool
	ResultAcknowledged             bool
	Result                         *ResultV1
}

type TransitionV1 struct {
	Before                         StateV1
	After                          StateV1
	Cause                          TerminationCauseV1
	CauseLatched                   bool
	BeginTermination               bool
	AwaitingControllerFinalization bool
	AwaitingResultAcknowledgement  bool
	RequestAccepted                bool
	ResultAcknowledged             bool
	Result                         *ResultV1
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
	cause              TerminationCauseV1
	workload           ProcessStatusV1
	controller         ControllerFinalizationStatusV1
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
		authorization: cloneAuthorizationV1(authorization),
		state:         StatePreparingV1,
		workload:      ProcessStatusV1{Kind: ProcessStatusUnknownV1},
		controller:    ControllerFinalizationStatusV1{Kind: ControllerFinalizationUnknownV1},
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
		if observation.WorkloadStatus != nil || observation.Reason != "" || observation.Finish != nil || machine.state != StateTerminatedV1 || machine.result == nil {
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
		if observation.WorkloadStatus != nil || observation.Reason != "" || observation.Finish != nil || machine.state != StatePreparingV1 {
			return transition, fmt.Errorf("%w: activation is valid only while preparing and carries no payload", ErrObservationRejected)
		}
		machine.state = StateActiveV1
		machine.controller = ControllerFinalizationStatusV1{Kind: ControllerFinalizationActiveV1}
	case ObservationWorkloadExitV1:
		if observation.WorkloadStatus == nil || observation.Reason != "" || observation.Finish != nil {
			return transition, fmt.Errorf("%w: workload exit requires exactly one workload status", ErrObservationRejected)
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
			machine.waitingFinalize = machine.controller.Kind == ControllerFinalizationActiveV1
		}
	case ObservationHostCancelV1:
		if err := validateCauseObservationV1(observation); err != nil {
			return transition, err
		}
		machine.waitingFinalize = false
		machine.latchLocked(CauseHostCancelV1, &transition)
	case ObservationControllerLostV1:
		if observation.WorkloadStatus != nil || observation.Finish != nil {
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
		machine.waitingFinalize = false
		machine.latchLocked(CauseRuntimeObservationLostV1, &transition)
	case ObservationStartupFailureV1:
		if observation.WorkloadStatus != nil || observation.Finish != nil {
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
	case ObservationControllerFinalizationExpiredV1:
		if observation.WorkloadStatus != nil || observation.Finish != nil || observation.Reason != "" || !machine.waitingFinalize {
			return transition, fmt.Errorf("%w: finalization expiry requires an active workload-exit finalization wait", ErrObservationRejected)
		}
		machine.waitingFinalize = false
		machine.controller = ControllerFinalizationStatusV1{Kind: ControllerFinalizationTimeoutV1}
	case ObservationFinishedV1:
		if observation.WorkloadStatus != nil || observation.Reason != "" || observation.Finish == nil {
			return transition, fmt.Errorf("%w: finish requires exactly one terminal status set", ErrObservationRejected)
		}
		if machine.state != StateTerminatingV1 || machine.waitingFinalize {
			return transition, fmt.Errorf("%w: finish requires termination with no pending controller finalization", ErrObservationRejected)
		}
		if err := validateFinishV1(*observation.Finish); err != nil {
			return transition, fmt.Errorf("%w: %v", ErrObservationRejected, err)
		}
		if machine.workload.Kind != ProcessStatusUnknownV1 && !equalProcessStatusV1(machine.workload, observation.Finish.WorkloadStatus) {
			return transition, fmt.Errorf("%w: terminal workload status conflicts with the status already observed", ErrObservationRejected)
		}
		if err := machine.validateControllerFinishLocked(observation.Finish.ControllerFinalizationStatus); err != nil {
			return transition, fmt.Errorf("%w: %v", ErrObservationRejected, err)
		}
		result := ResultV1{
			Cause: machine.cause, WorkloadStatus: cloneProcessStatusV1(observation.Finish.WorkloadStatus),
			ControllerFinalizationStatus: observation.Finish.ControllerFinalizationStatus, CleanupStatus: observation.Finish.CleanupStatus,
			RecoveryAction: observation.Finish.RecoveryAction,
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
		case RequestInputV1, RequestResizeV1, RequestOpenEndpointV1:
			if request.Kind == RequestOpenEndpointV1 && !containsStringV1(machine.authorization.EndpointIDs, request.EndpointID) {
				return transition, fmt.Errorf("%w: endpoint %q was not granted", ErrRequestRejected, request.EndpointID)
			}
		case RequestTerminateV1:
			machine.latchLocked(CauseControllerTerminateV1, &transition)
		case RequestCompleteV1:
			machine.controller = ControllerFinalizationStatusV1{Kind: ControllerFinalizationCompletedV1}
			machine.latchLocked(CauseControllerCompleteV1, &transition)
		}
	case StateTerminatingV1:
		switch request.Kind {
		case RequestTerminateV1:
			// Repeated graceful termination is idempotent. When workload
			// exit started a controller-finalization wait, terminate abandons
			// that wait so teardown can continue immediately.
			machine.waitingFinalize = false
		case RequestCompleteV1:
			if !machine.waitingFinalize || machine.controller.Kind != ControllerFinalizationActiveV1 {
				return transition, fmt.Errorf("%w: completion is not awaiting controller finalization", ErrRequestRejected)
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
	transition.CauseLatched = true
	transition.BeginTermination = true
}

func (machine *MachineV1) snapshotLocked() SnapshotV1 {
	return SnapshotV1{
		State: machine.state, Cause: machine.cause, WorkloadStatus: cloneProcessStatusV1(machine.workload),
		ControllerFinalizationStatus: machine.controller, AwaitingControllerFinalization: machine.waitingFinalize,
		AwaitingResultAcknowledgement: machine.resultDelivered && !machine.resultAcknowledged,
		ResultAcknowledged:            machine.resultAcknowledged, Result: cloneResultV1(machine.result),
	}
}

func validateCauseObservationV1(observation ObservationV1) error {
	if observation.WorkloadStatus != nil || observation.Finish != nil {
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
	if err := validateTerminalControllerFinalizationStatusV1(finish.ControllerFinalizationStatus); err != nil {
		return err
	}
	return validateCleanupResultV1(finish.CleanupStatus, finish.RecoveryAction)
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

func containsStringV1(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
