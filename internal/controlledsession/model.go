package controlledsession

import "fmt"

type TerminationCauseV1 string

const (
	CauseControllerTerminateV1    TerminationCauseV1 = "controller-terminate"
	CauseWorkloadExitV1           TerminationCauseV1 = "workload-exit"
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

type ControllerFinalizationStatusKindV1 string

const (
	ControllerFinalizationUnknownV1       ControllerFinalizationStatusKindV1 = "unknown"
	ControllerFinalizationActiveV1        ControllerFinalizationStatusKindV1 = "active"
	ControllerFinalizationCompletedV1     ControllerFinalizationStatusKindV1 = "completed"
	ControllerFinalizationLostV1          ControllerFinalizationStatusKindV1 = "lost"
	ControllerFinalizationTimeoutV1       ControllerFinalizationStatusKindV1 = "finalization-timeout"
	ControllerFinalizationNotCompletedV1  ControllerFinalizationStatusKindV1 = "not-completed"
	ControllerFinalizationStartupFailedV1 ControllerFinalizationStatusKindV1 = "startup-failed"
)

type ControllerFinalizationStatusV1 struct {
	Kind   ControllerFinalizationStatusKindV1 `json:"kind"`
	Reason string                             `json:"reason,omitempty"`
}

type WorkloadOutputFinalizationStatusKindV1 string

const (
	WorkloadOutputFinalizationDrainedV1 WorkloadOutputFinalizationStatusKindV1 = "drained"
	WorkloadOutputFinalizationFailedV1  WorkloadOutputFinalizationStatusKindV1 = "failed"
)

type WorkloadOutputFinalizationStatusV1 struct {
	Kind   WorkloadOutputFinalizationStatusKindV1 `json:"kind"`
	Reason string                                 `json:"reason,omitempty"`
}

type RuntimeObservationStatusKindV1 string

const (
	RuntimeObservationMaintainedV1 RuntimeObservationStatusKindV1 = "maintained"
	RuntimeObservationLostV1       RuntimeObservationStatusKindV1 = "lost"
)

type RuntimeObservationStatusV1 struct {
	Kind   RuntimeObservationStatusKindV1 `json:"kind"`
	Reason string                         `json:"reason,omitempty"`
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
	Cause                            TerminationCauseV1                 `json:"cause"`
	WorkloadStatus                   ProcessStatusV1                    `json:"workload_status"`
	WorkloadOutputFinalizationStatus WorkloadOutputFinalizationStatusV1 `json:"workload_output_finalization_status"`
	RuntimeObservationStatus         RuntimeObservationStatusV1         `json:"runtime_observation_status"`
	ControllerFinalizationStatus     ControllerFinalizationStatusV1     `json:"controller_finalization_status"`
	CleanupStatus                    CleanupStatusV1                    `json:"cleanup_status"`
	RecoveryAction                   RecoveryActionV1                   `json:"recovery_action"`
}

func ValidateResultV1(result ResultV1) error {
	if !validTerminationCauseV1(result.Cause) {
		return fmt.Errorf("controlled-session termination cause %q is invalid", result.Cause)
	}
	if err := validateProcessStatusV1(result.WorkloadStatus, true); err != nil {
		return err
	}
	if err := validateWorkloadOutputFinalizationStatusV1(result.WorkloadOutputFinalizationStatus); err != nil {
		return err
	}
	if err := validateRuntimeObservationStatusV1(result.RuntimeObservationStatus); err != nil {
		return err
	}
	if err := validateTerminalControllerFinalizationStatusV1(result.ControllerFinalizationStatus); err != nil {
		return err
	}
	if err := validateCleanupResultV1(result.CleanupStatus, result.RecoveryAction); err != nil {
		return err
	}
	return validateResultConsistencyV1(result)
}

func validateResultConsistencyV1(result ResultV1) error {
	if result.Cause == CauseRuntimeObservationLostV1 {
		if result.RuntimeObservationStatus.Kind != RuntimeObservationLostV1 {
			return fmt.Errorf("runtime-observation-loss termination requires lost runtime observation status")
		}
		if result.WorkloadOutputFinalizationStatus.Kind != WorkloadOutputFinalizationFailedV1 {
			return fmt.Errorf("runtime-observation-loss termination requires failed workload output finalization")
		}
	}
	if result.Cause == CauseControllerLostV1 && result.ControllerFinalizationStatus.Kind != ControllerFinalizationLostV1 {
		return fmt.Errorf("controller-loss termination requires lost controller finalization status")
	}
	if result.Cause == CauseStartupFailureV1 && result.ControllerFinalizationStatus.Kind != ControllerFinalizationStartupFailedV1 {
		return fmt.Errorf("startup-failure termination requires startup-failed controller finalization status")
	}
	return nil
}

func validateRuntimeObservationStatusV1(status RuntimeObservationStatusV1) error {
	switch status.Kind {
	case RuntimeObservationMaintainedV1:
		if status.Reason != "" {
			return fmt.Errorf("maintained runtime observation must not contain a reason")
		}
	case RuntimeObservationLostV1:
		if err := validateOptionalSafeTextV1("runtime observation loss reason", status.Reason); err != nil {
			return err
		}
	default:
		return fmt.Errorf("runtime observation status %q is invalid", status.Kind)
	}
	return nil
}

func validateWorkloadOutputFinalizationStatusV1(status WorkloadOutputFinalizationStatusV1) error {
	switch status.Kind {
	case WorkloadOutputFinalizationDrainedV1:
		if status.Reason != "" {
			return fmt.Errorf("drained workload output finalization must not contain a reason")
		}
	case WorkloadOutputFinalizationFailedV1:
		if err := validateRequiredSafeTextV1("workload output finalization failure reason", status.Reason); err != nil {
			return err
		}
	default:
		return fmt.Errorf("workload output finalization status %q is invalid", status.Kind)
	}
	return nil
}

func validateProcessStatusV1(status ProcessStatusV1, allowUnknown bool) error {
	switch status.Kind {
	case ProcessStatusUnknownV1:
		if !allowUnknown || status.Code != nil || status.Reason != "" {
			return fmt.Errorf("unknown workload status must have no code or reason")
		}
	case ProcessStatusExitedV1:
		if status.Code == nil {
			return fmt.Errorf("exited workload status requires an exit code")
		}
	case ProcessStatusTerminatedV1, ProcessStatusUnavailableV1:
		if status.Code != nil {
			return fmt.Errorf("workload status %q must not contain an exit code", status.Kind)
		}
	default:
		return fmt.Errorf("workload status %q is invalid", status.Kind)
	}
	return validateOptionalSafeTextV1("workload status reason", status.Reason)
}

func validateTerminalControllerFinalizationStatusV1(status ControllerFinalizationStatusV1) error {
	switch status.Kind {
	case ControllerFinalizationCompletedV1, ControllerFinalizationLostV1, ControllerFinalizationTimeoutV1, ControllerFinalizationNotCompletedV1, ControllerFinalizationStartupFailedV1:
	default:
		return fmt.Errorf("terminal controller finalization status %q is invalid", status.Kind)
	}
	return validateOptionalSafeTextV1("controller finalization status reason", status.Reason)
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
	case CauseControllerTerminateV1, CauseWorkloadExitV1, CauseHostCancelV1,
		CauseControllerLostV1, CauseRuntimeObservationLostV1, CauseStartupFailureV1:
		return true
	default:
		return false
	}
}
