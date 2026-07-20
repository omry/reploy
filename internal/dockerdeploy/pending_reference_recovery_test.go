package dockerdeploy

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

type removedReference struct {
	image     providers.RealizedImageV1
	reference string
	kind      EnvironmentReferenceKind
}

func pendingReferenceFixture(t *testing.T) (string, deploy.PendingBuildV1) {
	t.Helper()
	dir := t.TempDir()
	references, err := newEnvironmentImageReferences("demo", dir, bytes.NewReader(bytes.Repeat([]byte{0x55}, environmentReferenceRandomBytes*2)))
	if err != nil {
		t.Fatal(err)
	}
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	oldReferences, err := newEnvironmentImageReferences("demo", dir, bytes.NewReader(bytes.Repeat([]byte{0x66}, environmentReferenceRandomBytes*2)))
	if err != nil {
		t.Fatal(err)
	}
	old := deploy.EnvironmentGenerationState{
		Reference: oldReferences.Generation, ImageDigest: rendererDigest("1"), RootFSSubject: rendererDigest("2"),
		BuildLockDigest: rendererDigest("3"), Platform: platform, RuntimePolicyDigest: rendererDigest("4"),
	}
	return dir, deploy.PendingBuildV1{
		Schema: deploy.PendingBuildSchemaV1, Phase: deploy.PendingBuildPhaseCleanup, Old: &old,
		Candidate: deploy.PendingCandidateV1{
			TemporaryReference: references.Temporary, GenerationReference: references.Generation,
			Image:           providers.RealizedImageV1{Digest: rendererDigest("5"), ConfigDigest: rendererDigest("5"), RootFSSubject: rendererDigest("6")},
			BuildLockDigest: rendererDigest("7"), StoreObjects: []providerstore.StoreObjectRef{},
		},
		Cleanup: []deploy.CleanupItemV1{},
	}
}

func TestRecoverPendingImageReferencesUsesDecisionSpecificOwnership(t *testing.T) {
	dir, pending := pendingReferenceFixture(t)
	oldImage := providers.RealizedImageV1{Digest: pending.Old.ImageDigest, ConfigDigest: rendererDigest("8"), RootFSSubject: pending.Old.RootFSSubject}
	tests := []struct {
		name     string
		decision deploy.PendingRecoveryDecision
		want     []removedReference
	}{
		{name: "discard", decision: deploy.PendingRecoveryDiscardCandidate, want: []removedReference{
			{image: pending.Candidate.Image, reference: pending.Candidate.GenerationReference, kind: EnvironmentReferenceGeneration},
			{image: pending.Candidate.Image, reference: pending.Candidate.TemporaryReference, kind: EnvironmentReferenceTemporary},
		}},
		{name: "keep", decision: deploy.PendingRecoveryKeepCandidate, want: []removedReference{
			{image: oldImage, reference: pending.Old.Reference, kind: EnvironmentReferenceGeneration},
			{image: pending.Candidate.Image, reference: pending.Candidate.TemporaryReference, kind: EnvironmentReferenceTemporary},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var removed []removedReference
			remove := func(_ context.Context, image providers.RealizedImageV1, references EnvironmentImageReferences, kind EnvironmentReferenceKind, _ string, _ string) error {
				reference, err := selectEnvironmentReference(references, kind)
				if err != nil {
					return err
				}
				removed = append(removed, removedReference{image: image, reference: reference, kind: kind})
				return nil
			}
			if err := recoverPendingImageReferences(context.Background(), pending, test.decision, &oldImage, "demo", dir, remove); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(removed, test.want) {
				t.Fatalf("removed = %#v, want %#v", removed, test.want)
			}
		})
	}
}

func TestRecoverPendingImageReferencesConflictChangesNothing(t *testing.T) {
	dir, pending := pendingReferenceFixture(t)
	calls := 0
	err := recoverPendingImageReferences(context.Background(), pending, deploy.PendingRecoveryStateConflict, nil, "demo", dir, func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
		calls++
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "state conflict") || calls != 0 {
		t.Fatalf("calls = %d, error = %v", calls, err)
	}
}

func TestRecoverPendingImageReferencesKeepsFirstGeneration(t *testing.T) {
	dir, pending := pendingReferenceFixture(t)
	pending.Old = nil
	var removed []removedReference
	remove := func(_ context.Context, image providers.RealizedImageV1, references EnvironmentImageReferences, kind EnvironmentReferenceKind, _ string, _ string) error {
		reference, err := selectEnvironmentReference(references, kind)
		if err != nil {
			return err
		}
		removed = append(removed, removedReference{image: image, reference: reference, kind: kind})
		return nil
	}
	if err := recoverPendingImageReferences(context.Background(), pending, deploy.PendingRecoveryKeepCandidate, nil, "demo", dir, remove); err != nil {
		t.Fatal(err)
	}
	want := []removedReference{{
		image: pending.Candidate.Image, reference: pending.Candidate.TemporaryReference, kind: EnvironmentReferenceTemporary,
	}}
	if !reflect.DeepEqual(removed, want) {
		t.Fatalf("removed = %#v, want %#v", removed, want)
	}
}
