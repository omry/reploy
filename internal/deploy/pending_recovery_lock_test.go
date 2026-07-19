package deploy

import (
	"errors"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func pendingRecoveryLockFixture(t *testing.T) (PendingBuildV1, BuildLockV1, EnvironmentGenerationState) {
	t.Helper()
	pending := validPendingBuild(t)
	lock := validBuildLock(t)
	pending.Candidate.Image.RootFSSubject = lock.FinalImage.RootFSSubject
	lock.FinalImage = pending.Candidate.Image
	digest, err := BuildLockDigestV1(lock, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	pending.Candidate.BuildLockDigest = digest
	candidate, err := CandidateGenerationFromBuildLock(pending, lock, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	return pending, lock, candidate
}

func TestDecidePendingRecoveryWithBuildLockDiscardsWithoutLoadingCandidate(t *testing.T) {
	pending, _, _ := pendingRecoveryLockFixture(t)
	loadCalls := 0
	decision, candidate, err := DecidePendingRecoveryWithBuildLock(pending.Old, pending, func(_ canonical.Digest) (BuildLockV1, error) {
		loadCalls++
		return BuildLockV1{}, errors.New("must not load")
	}, acceptBuildLockProfile)
	if err != nil || decision != PendingRecoveryDiscardCandidate || candidate != nil || loadCalls != 0 {
		t.Fatalf("decision = %q, candidate = %#v, load calls = %d, error = %v", decision, candidate, loadCalls, err)
	}
	pending.Old = nil
	decision, candidate, err = DecidePendingRecoveryWithBuildLock(nil, pending, nil, acceptBuildLockProfile)
	if err != nil || decision != PendingRecoveryDiscardCandidate || candidate != nil {
		t.Fatalf("first-build decision = %q, candidate = %#v, error = %v", decision, candidate, err)
	}
}

func TestDecidePendingRecoveryWithBuildLockLoadsCommittedCandidate(t *testing.T) {
	pending, lock, candidate := pendingRecoveryLockFixture(t)
	loadCalls := 0
	decision, derived, err := DecidePendingRecoveryWithBuildLock(&candidate, pending, func(digest canonical.Digest) (BuildLockV1, error) {
		loadCalls++
		if digest != pending.Candidate.BuildLockDigest {
			t.Fatalf("digest = %s", digest)
		}
		return lock, nil
	}, acceptBuildLockProfile)
	if err != nil || decision != PendingRecoveryKeepCandidate || derived == nil || *derived != candidate || loadCalls != 1 {
		t.Fatalf("decision = %q, candidate = %#v, load calls = %d, error = %v", decision, derived, loadCalls, err)
	}
}

func TestDecidePendingRecoveryWithBuildLockRejectsMissingOrMismatchedCandidateLock(t *testing.T) {
	pending, lock, candidate := pendingRecoveryLockFixture(t)
	if _, _, err := DecidePendingRecoveryWithBuildLock(&candidate, pending, func(canonical.Digest) (BuildLockV1, error) {
		return BuildLockV1{}, errors.New("missing")
	}, acceptBuildLockProfile); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing lock error = %v", err)
	}
	lock.FinalImage.Digest = pendingBuildTestDigest("f")
	if _, _, err := DecidePendingRecoveryWithBuildLock(&candidate, pending, func(canonical.Digest) (BuildLockV1, error) {
		return lock, nil
	}, acceptBuildLockProfile); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("mismatched lock error = %v", err)
	}
}
