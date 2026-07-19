package deploy

import (
	"strings"
	"testing"
)

func pendingCandidateGeneration(t *testing.T, pending PendingBuildV1) EnvironmentGenerationState {
	t.Helper()
	candidate := *pending.Old
	candidate.Reference = pending.Candidate.GenerationReference
	candidate.ImageDigest = pending.Candidate.Image.Digest
	candidate.RootFSSubject = pending.Candidate.Image.RootFSSubject
	candidate.BuildLockDigest = pending.Candidate.BuildLockDigest
	candidate.RuntimePolicyDigest = pendingBuildTestDigest("a")
	return candidate
}

func TestDecidePendingRecoveryUsesCommittedStateAsAuthority(t *testing.T) {
	pending := validPendingBuild(t)
	candidate := pendingCandidateGeneration(t, pending)
	old := *pending.Old
	conflict := old
	conflict.Reference = "reploy/env/demo-abcd:g-unrelated"

	tests := []struct {
		name    string
		current *EnvironmentGenerationState
		want    PendingRecoveryDecision
	}{
		{name: "old", current: &old, want: PendingRecoveryDiscardCandidate},
		{name: "candidate", current: &candidate, want: PendingRecoveryKeepCandidate},
		{name: "conflict", current: &conflict, want: PendingRecoveryStateConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, err := DecidePendingRecovery(test.current, pending, candidate)
			if err != nil {
				t.Fatal(err)
			}
			if decision != test.want {
				t.Fatalf("decision = %q, want %q", decision, test.want)
			}
		})
	}
}

func TestDecidePendingRecoveryHandlesFirstBuild(t *testing.T) {
	pending := validPendingBuild(t)
	candidate := pendingCandidateGeneration(t, pending)
	pending.Old = nil
	decision, err := DecidePendingRecovery(nil, pending, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if decision != PendingRecoveryDiscardCandidate {
		t.Fatalf("decision = %q", decision)
	}
}

func TestDecidePendingRecoveryRejectsCandidateOutsideInventory(t *testing.T) {
	pending := validPendingBuild(t)
	candidate := pendingCandidateGeneration(t, pending)
	candidate.BuildLockDigest = pendingBuildTestDigest("b")
	decision, err := DecidePendingRecovery(pending.Old, pending, candidate)
	if err == nil || !strings.Contains(err.Error(), "does not match pending inventory") || decision != "" {
		t.Fatalf("decision = %q, error = %v", decision, err)
	}
}
