package controlledsession

import "fmt"

type TerminationCauseV1 string

const (
	CauseControllerCompleteV1     TerminationCauseV1 = "controller-complete"
	CauseControllerTerminateV1    TerminationCauseV1 = "controller-terminate"
	CauseApplicationExitV1        TerminationCauseV1 = "application-exit"
	CauseHostCancelV1             TerminationCauseV1 = "host-cancel"
	CauseControllerLostV1         TerminationCauseV1 = "controller-lost"
	CauseRuntimeObservationLostV1 TerminationCauseV1 = "runtime-observation-lost"
	CauseStartupFailureV1         TerminationCauseV1 = "startup-failure"
)

type ProcessStatusKindV1 string

const (
	ProcessStatusUnknownV1     ProcessStatusKindV1 = "unknown"
	ProcessStatusExitedV1      ProcessStatusKindV1 = "exited"
	ProcessStatusTerminatedV1  ProcessStatusKindV1 = "terminated"
	ProcessStatusUnavailableV1 ProcessStatusKindV1 = "unavailable"
)

type ProcessStatusV1 struct {
	Kind   ProcessStatusKindV1 `json:"kind"`
	Code   *int                `json:"code,omitempty"`
	Reason string              `json:"reason,omitempty"`
}

type ControllerStatusKindV1 string

const (
	ControllerStatusUnknownV1             ControllerStatusKindV1 = "unknown"
	ControllerStatusActiveV1              ControllerStatusKindV1 = "active"
	ControllerStatusCompletedV1           ControllerStatusKindV1 = "completed"
	ControllerStatusLostV1                ControllerStatusKindV1 = "lost"
	ControllerStatusFinalizationTimeoutV1 ControllerStatusKindV1 = "finalization-timeout"
	ControllerStatusStoppedV1             ControllerStatusKindV1 = "stopped"
	ControllerStatusStartupFailedV1       ControllerStatusKindV1 = "startup-failed"
)

type ControllerStatusV1 struct {
	Kind   ControllerStatusKindV1 `json:"kind"`
	Reason string                 `json:"reason,omitempty"`
}

type CleanupStatusKindV1 string

const (
	CleanupStatusSucceededV1 CleanupStatusKindV1 = "succeeded"
	CleanupStatusFailedV1    CleanupStatusKindV1 = "failed"
)

type CleanupStatusV1 struct {
	Kind    CleanupStatusKindV1 `json:"kind"`
	Message string              `json:"message,omitempty"`
}

type RecoveryActionV1 string

const (
	RecoveryNoneV1                   RecoveryActionV1 = "none"
	RecoveryRetryCleanupV1           RecoveryActionV1 = "retry-cleanup"
	RecoveryReconcileNextOperationV1 RecoveryActionV1 = "reconcile-next-operation"
)

type ResultV1 struct {
	Cause             TerminationCauseV1 `json:"cause"`
	ApplicationStatus ProcessStatusV1    `json:"application_status"`
	ControllerStatus  ControllerStatusV1 `json:"controller_status"`
	CleanupStatus     CleanupStatusV1    `json:"cleanup_status"`
	RecoveryAction    RecoveryActionV1   `json:"recovery_action"`
}

func ValidateResultV1(result ResultV1) error {
	if !validTerminationCauseV1(result.Cause) {
		return fmt.Errorf("controlled-session termination cause %q is invalid", result.Cause)
	}
	if err := validateProcessStatusV1(result.ApplicationStatus, true); err != nil {
		return err
	}
	if err := validateTerminalControllerStatusV1(result.ControllerStatus); err != nil {
		return err
	}
	return validateCleanupResultV1(result.CleanupStatus, result.RecoveryAction)
}

func validateProcessStatusV1(status ProcessStatusV1, allowUnknown bool) error {
	switch status.Kind {
	case ProcessStatusUnknownV1:
		if !allowUnknown || status.Code != nil || status.Reason != "" {
			return fmt.Errorf("unknown application status must have no code or reason")
		}
	case ProcessStatusExitedV1:
		if status.Code == nil {
			return fmt.Errorf("exited application status requires an exit code")
		}
	case ProcessStatusTerminatedV1, ProcessStatusUnavailableV1:
		if status.Code != nil {
			return fmt.Errorf("application status %q must not contain an exit code", status.Kind)
		}
	default:
		return fmt.Errorf("application status %q is invalid", status.Kind)
	}
	return validateOptionalSafeTextV1("application status reason", status.Reason)
}

func validateTerminalControllerStatusV1(status ControllerStatusV1) error {
	switch status.Kind {
	case ControllerStatusCompletedV1, ControllerStatusLostV1, ControllerStatusFinalizationTimeoutV1, ControllerStatusStoppedV1, ControllerStatusStartupFailedV1:
	default:
		return fmt.Errorf("terminal controller status %q is invalid", status.Kind)
	}
	return validateOptionalSafeTextV1("controller status reason", status.Reason)
}

func validateCleanupResultV1(status CleanupStatusV1, recovery RecoveryActionV1) error {
	switch status.Kind {
	case CleanupStatusSucceededV1:
		if status.Message != "" {
			return fmt.Errorf("successful cleanup status must not contain an error message")
		}
		if recovery != RecoveryNoneV1 {
			return fmt.Errorf("successful cleanup must not require recovery")
		}
	case CleanupStatusFailedV1:
		if err := validateRequiredSafeTextV1("cleanup failure message", status.Message); err != nil {
			return err
		}
		if recovery != RecoveryRetryCleanupV1 && recovery != RecoveryReconcileNextOperationV1 {
			return fmt.Errorf("failed cleanup requires a recovery action")
		}
	default:
		return fmt.Errorf("cleanup status %q is invalid", status.Kind)
	}
	return nil
}

func validateOptionalSafeTextV1(field string, value string) error {
	if value == "" {
		return nil
	}
	return validateSafeTextV1(field, value)
}

func validateRequiredSafeTextV1(field string, value string) error {
	if value == "" {
		return fmt.Errorf("controlled-session %s is required", field)
	}
	return validateSafeTextV1(field, value)
}

func validTerminationCauseV1(cause TerminationCauseV1) bool {
	switch cause {
	case CauseControllerCompleteV1, CauseControllerTerminateV1, CauseApplicationExitV1, CauseHostCancelV1,
		CauseControllerLostV1, CauseRuntimeObservationLostV1, CauseStartupFailureV1:
		return true
	default:
		return false
	}
}
