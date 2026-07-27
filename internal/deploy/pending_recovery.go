package deploy

import (
	"fmt"
	"reflect"
)

type PendingRecoveryDecision string

const (
	PendingRecoveryDiscardCandidate PendingRecoveryDecision = "discard-candidate"
	PendingRecoveryKeepCandidate    PendingRecoveryDecision = "keep-candidate"
	PendingRecoveryStateConflict    PendingRecoveryDecision = "state-conflict"
)

func DecidePendingRecovery(
	current *EnvironmentGenerationState,
	pending PendingBuildV1,
	candidate EnvironmentGenerationState,
) (PendingRecoveryDecision, error) {
	if err := ValidatePendingBuild(pending); err != nil {
		return "", fmt.Errorf("decide pending recovery: %w", err)
	}
	if current != nil {
		if err := ValidateEnvironmentGenerationState(*current); err != nil {
			return "", fmt.Errorf("decide pending recovery current generation: %w", err)
		}
	}
	if err := ValidateEnvironmentGenerationState(candidate); err != nil {
		return "", fmt.Errorf("decide pending recovery candidate generation: %w", err)
	}
	if candidate.Reference != pending.Candidate.GenerationReference ||
		candidate.ImageDigest != pending.Candidate.Image.Digest ||
		candidate.RootFSSubject != pending.Candidate.Image.RootFSSubject ||
		candidate.BuildLockDigest != pending.Candidate.BuildLockDigest {
		return "", fmt.Errorf("pending recovery candidate generation does not match pending inventory")
	}
	if generationStatePointersEqual(current, pending.Old) {
		return PendingRecoveryDiscardCandidate, nil
	}
	if current != nil && reflect.DeepEqual(*current, candidate) {
		return PendingRecoveryKeepCandidate, nil
	}
	return PendingRecoveryStateConflict, nil
}

func generationStatePointersEqual(left *EnvironmentGenerationState, right *EnvironmentGenerationState) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return reflect.DeepEqual(*left, *right)
}
