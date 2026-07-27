package deploy

import (
	"fmt"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

type PendingBuildLockLoader func(canonical.Digest) (BuildLockV1, error)

func CandidateGenerationFromBuildLock(
	pending PendingBuildV1,
	lock BuildLockV1,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
) (EnvironmentGenerationState, error) {
	if err := ValidatePendingBuild(pending); err != nil {
		return EnvironmentGenerationState{}, fmt.Errorf("derive pending candidate generation: %w", err)
	}
	digest, err := BuildLockDigestV1(lock, validateProfileOwner)
	if err != nil {
		return EnvironmentGenerationState{}, fmt.Errorf("derive pending candidate generation lock: %w", err)
	}
	if digest != pending.Candidate.BuildLockDigest {
		return EnvironmentGenerationState{}, fmt.Errorf("pending candidate build lock identity is %s, want %s", digest, pending.Candidate.BuildLockDigest)
	}
	if lock.FinalImage != pending.Candidate.Image {
		return EnvironmentGenerationState{}, fmt.Errorf("pending candidate image does not match its build lock")
	}
	policyDigest, err := RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		return EnvironmentGenerationState{}, err
	}
	candidate := EnvironmentGenerationState{
		Reference: pending.Candidate.GenerationReference, ImageDigest: lock.FinalImage.Digest,
		RootFSSubject: lock.FinalImage.RootFSSubject, BuildLockDigest: digest,
		Platform: lock.Platform, RuntimePolicyDigest: policyDigest,
	}
	if err := ValidateEnvironmentGenerationState(candidate); err != nil {
		return EnvironmentGenerationState{}, err
	}
	return candidate, nil
}

func DecidePendingRecoveryWithBuildLock(
	current *EnvironmentGenerationState,
	pending PendingBuildV1,
	load PendingBuildLockLoader,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
) (PendingRecoveryDecision, *EnvironmentGenerationState, error) {
	if err := ValidatePendingBuild(pending); err != nil {
		return "", nil, fmt.Errorf("decide pending recovery: %w", err)
	}
	if current != nil {
		if err := ValidateEnvironmentGenerationState(*current); err != nil {
			return "", nil, fmt.Errorf("decide pending recovery current generation: %w", err)
		}
	}
	if generationStatePointersEqual(current, pending.Old) {
		return PendingRecoveryDiscardCandidate, nil, nil
	}
	if load == nil {
		return "", nil, fmt.Errorf("pending recovery requires the candidate build lock after state changed")
	}
	lock, err := load(pending.Candidate.BuildLockDigest)
	if err != nil {
		return "", nil, fmt.Errorf("load pending candidate build lock: %w", err)
	}
	candidate, err := CandidateGenerationFromBuildLock(pending, lock, validateProfileOwner)
	if err != nil {
		return "", nil, err
	}
	decision, err := DecidePendingRecovery(current, pending, candidate)
	if err != nil {
		return "", nil, err
	}
	return decision, &candidate, nil
}
