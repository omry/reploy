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
	ApplicationStatus ProcessStatusV1
	ControllerStatus  ControllerStatusV1
	CleanupStatus     CleanupStatusV1
	RecoveryAction    RecoveryActionV1
}

type ObservationKindV1 string

const (
	ObservationActivatedV1                     ObservationKindV1 = "activated"
	ObservationApplicationExitV1               ObservationKindV1 = "application-exit"
	ObservationHostCancelV1                    ObservationKindV1 = "host-cancel"
	ObservationControllerLostV1                ObservationKindV1 = "controller-lost"
	ObservationRuntimeObservationLostV1        ObservationKindV1 = "runtime-observation-lost"
	ObservationStartupFailureV1                ObservationKindV1 = "startup-failure"
	ObservationControllerFinalizationExpiredV1 ObservationKindV1 = "controller-finalization-expired"
	ObservationFinishedV1                      ObservationKindV1 = "finished"
)

type ObservationV1 struct {
	Kind              ObservationKindV1
	ApplicationStatus *ProcessStatusV1
	Reason            string
	Finish            *FinishV1
}

type SnapshotV1 struct {
	State                          StateV1
	Cause                          TerminationCauseV1
	ApplicationStatus              ProcessStatusV1
	ControllerStatus               ControllerStatusV1
	AwaitingControllerFinalization bool
	Result                         *ResultV1
}

type TransitionV1 struct {
	Before                         StateV1
	After                          StateV1
	Cause                          TerminationCauseV1
	CauseLatched                   bool
	BeginTermination               bool
	AwaitingControllerFinalization bool
	RequestAccepted                bool
	Result                         *ResultV1
}

var ErrRequestRejected = errors.New("controlled-session request rejected")
var ErrObservationRejected = errors.New("controlled-session observation rejected")

// MachineV1 serializes the authoritative lifecycle of one session. Its mutex
// makes the first accepted termination cause deterministic even when host
// observations race.
type MachineV1 struct {
	mu              sync.Mutex
	authorization   AuthorizationV1
	state           StateV1
	cause           TerminationCauseV1
	application     ProcessStatusV1
	controller      ControllerStatusV1
	waitingFinalize bool
	result          *ResultV1
}

func NewMachineV1(authorization AuthorizationV1) (*MachineV1, error) {
	if err := ValidateAuthorizationV1(authorization); err != nil {
		return nil, fmt.Errorf("create controlled-session lifecycle: %w", err)
	}
	return &MachineV1{
		authorization: cloneAuthorizationV1(authorization),
		state:         StatePreparingV1,
		application:   ProcessStatusV1{Kind: ProcessStatusUnknownV1},
		controller:    ControllerStatusV1{Kind: ControllerStatusUnknownV1},
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
	if machine.state == StateTerminatedV1 {
		return transition, fmt.Errorf("%w: lifecycle is already terminated", ErrObservationRejected)
	}

	switch observation.Kind {
	case ObservationActivatedV1:
		if observation.ApplicationStatus != nil || observation.Reason != "" || observation.Finish != nil || machine.state != StatePreparingV1 {
			return transition, fmt.Errorf("%w: activation is valid only while preparing and carries no payload", ErrObservationRejected)
		}
		machine.state = StateActiveV1
		machine.controller = ControllerStatusV1{Kind: ControllerStatusActiveV1}
	case ObservationApplicationExitV1:
		if observation.ApplicationStatus == nil || observation.Reason != "" || observation.Finish != nil {
			return transition, fmt.Errorf("%w: application exit requires exactly one application status", ErrObservationRejected)
		}
		if machine.state == StatePreparingV1 {
			return transition, fmt.Errorf("%w: application exit is invalid before activation", ErrObservationRejected)
		}
		if err := validateProcessStatusV1(*observation.ApplicationStatus, false); err != nil {
			return transition, fmt.Errorf("%w: %v", ErrObservationRejected, err)
		}
		if machine.application.Kind != ProcessStatusUnknownV1 && !equalProcessStatusV1(machine.application, *observation.ApplicationStatus) {
			return transition, fmt.Errorf("%w: application exit conflicts with the status already observed", ErrObservationRejected)
		}
		machine.application = cloneProcessStatusV1(*observation.ApplicationStatus)
		if machine.state == StateActiveV1 {
			machine.latchLocked(CauseApplicationExitV1, &transition)
			machine.waitingFinalize = machine.controller.Kind == ControllerStatusActiveV1
		}
	case ObservationHostCancelV1:
		if err := validateCauseObservationV1(observation); err != nil {
			return transition, err
		}
		machine.waitingFinalize = false
		machine.latchLocked(CauseHostCancelV1, &transition)
	case ObservationControllerLostV1:
		if observation.ApplicationStatus != nil || observation.Finish != nil {
			return transition, fmt.Errorf("%w: controller loss carries only an optional reason", ErrObservationRejected)
		}
		if err := validateOptionalSafeTextV1("controller-loss reason", observation.Reason); err != nil {
			return transition, fmt.Errorf("%w: %v", ErrObservationRejected, err)
		}
		if machine.controller.Kind == ControllerStatusUnknownV1 || machine.controller.Kind == ControllerStatusActiveV1 {
			machine.controller = ControllerStatusV1{Kind: ControllerStatusLostV1, Reason: observation.Reason}
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
		if observation.ApplicationStatus != nil || observation.Finish != nil {
			return transition, fmt.Errorf("%w: startup failure carries only a reason", ErrObservationRejected)
		}
		if err := validateRequiredSafeTextV1("startup-failure reason", observation.Reason); err != nil {
			return transition, fmt.Errorf("%w: %v", ErrObservationRejected, err)
		}
		if machine.controller.Kind != ControllerStatusUnknownV1 {
			return transition, fmt.Errorf("%w: startup failure is invalid after controller activation", ErrObservationRejected)
		}
		machine.controller = ControllerStatusV1{Kind: ControllerStatusStartupFailedV1, Reason: observation.Reason}
		machine.latchLocked(CauseStartupFailureV1, &transition)
	case ObservationControllerFinalizationExpiredV1:
		if observation.ApplicationStatus != nil || observation.Finish != nil || observation.Reason != "" || !machine.waitingFinalize {
			return transition, fmt.Errorf("%w: finalization expiry requires an active application-exit finalization wait", ErrObservationRejected)
		}
		machine.waitingFinalize = false
		machine.controller = ControllerStatusV1{Kind: ControllerStatusFinalizationTimeoutV1}
	case ObservationFinishedV1:
		if observation.ApplicationStatus != nil || observation.Reason != "" || observation.Finish == nil {
			return transition, fmt.Errorf("%w: finish requires exactly one terminal status set", ErrObservationRejected)
		}
		if machine.state != StateTerminatingV1 || machine.waitingFinalize {
			return transition, fmt.Errorf("%w: finish requires termination with no pending controller finalization", ErrObservationRejected)
		}
		if err := validateFinishV1(*observation.Finish); err != nil {
			return transition, fmt.Errorf("%w: %v", ErrObservationRejected, err)
		}
		if machine.application.Kind != ProcessStatusUnknownV1 && !equalProcessStatusV1(machine.application, observation.Finish.ApplicationStatus) {
			return transition, fmt.Errorf("%w: terminal application status conflicts with the status already observed", ErrObservationRejected)
		}
		if err := machine.validateControllerFinishLocked(observation.Finish.ControllerStatus); err != nil {
			return transition, fmt.Errorf("%w: %v", ErrObservationRejected, err)
		}
		result := ResultV1{
			Cause: machine.cause, ApplicationStatus: cloneProcessStatusV1(observation.Finish.ApplicationStatus),
			ControllerStatus: observation.Finish.ControllerStatus, CleanupStatus: observation.Finish.CleanupStatus,
			RecoveryAction: observation.Finish.RecoveryAction,
		}
		machine.application = result.ApplicationStatus
		machine.controller = result.ControllerStatus
		machine.result = &result
		machine.state = StateTerminatedV1
	default:
		return transition, fmt.Errorf("%w: observation kind %q is unsupported", ErrObservationRejected, observation.Kind)
	}

	transition.After = machine.state
	transition.Cause = machine.cause
	transition.AwaitingControllerFinalization = machine.waitingFinalize
	transition.Result = cloneResultV1(machine.result)
	return transition, nil
}

func (machine *MachineV1) validateControllerFinishLocked(status ControllerStatusV1) error {
	switch machine.controller.Kind {
	case ControllerStatusCompletedV1, ControllerStatusLostV1, ControllerStatusFinalizationTimeoutV1, ControllerStatusStartupFailedV1:
		if machine.controller != status {
			return fmt.Errorf("terminal controller status conflicts with the status already observed")
		}
	case ControllerStatusActiveV1, ControllerStatusUnknownV1:
		if status.Kind != ControllerStatusStoppedV1 {
			return fmt.Errorf("controller without explicit completion must finish as stopped")
		}
	default:
		return fmt.Errorf("recorded controller status %q is invalid before finish", machine.controller.Kind)
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
			machine.controller = ControllerStatusV1{Kind: ControllerStatusCompletedV1}
			machine.latchLocked(CauseControllerCompleteV1, &transition)
		}
	case StateTerminatingV1:
		switch request.Kind {
		case RequestTerminateV1:
			// Repeated graceful termination is idempotent. When application
			// exit started a controller-finalization wait, terminate abandons
			// that wait so teardown can continue immediately.
			machine.waitingFinalize = false
		case RequestCompleteV1:
			if !machine.waitingFinalize || machine.controller.Kind != ControllerStatusActiveV1 {
				return transition, fmt.Errorf("%w: completion is not awaiting controller finalization", ErrRequestRejected)
			}
			machine.waitingFinalize = false
			machine.controller = ControllerStatusV1{Kind: ControllerStatusCompletedV1}
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
	transition.RequestAccepted = true
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
		State: machine.state, Cause: machine.cause, ApplicationStatus: cloneProcessStatusV1(machine.application),
		ControllerStatus: machine.controller, AwaitingControllerFinalization: machine.waitingFinalize,
		Result: cloneResultV1(machine.result),
	}
}

func validateCauseObservationV1(observation ObservationV1) error {
	if observation.ApplicationStatus != nil || observation.Finish != nil {
		return fmt.Errorf("%w: %s carries only an optional reason", ErrObservationRejected, observation.Kind)
	}
	if err := validateOptionalSafeTextV1(string(observation.Kind)+" reason", observation.Reason); err != nil {
		return fmt.Errorf("%w: %v", ErrObservationRejected, err)
	}
	return nil
}

func validateFinishV1(finish FinishV1) error {
	if err := validateProcessStatusV1(finish.ApplicationStatus, true); err != nil {
		return err
	}
	if err := validateTerminalControllerStatusV1(finish.ControllerStatus); err != nil {
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
	copy.ApplicationStatus = cloneProcessStatusV1(result.ApplicationStatus)
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
