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

func TestRecoverPendingImageReferencesWithLocksCleansPortableIntermediateExactly(t *testing.T) {
	dir, pending := pendingReferenceFixture(t)
	oldImage := providers.RealizedImageV1{
		Digest: pending.Old.ImageDigest, ConfigDigest: pending.Old.ImageDigest,
		RootFSSubject: pending.Old.RootFSSubject,
	}
	candidateLock := &deploy.BuildLockV1{PortableRuntimeLayer: &deploy.PortableRuntimeLayerV1{Result: pending.Candidate.Image}}
	oldLock := &deploy.BuildLockV1{PortableRuntimeLayer: &deploy.PortableRuntimeLayerV1{Result: oldImage}}
	var removed []string
	remove := func(_ context.Context, image providers.RealizedImageV1, references EnvironmentImageReferences, kind EnvironmentReferenceKind, _ string, _ string) error {
		reference, err := selectEnvironmentReference(references, kind)
		if err != nil {
			return err
		}
		removed = append(removed, "image:"+string(image.ConfigDigest)+":"+reference)
		return nil
	}
	removePortable := func(_ context.Context, image providers.RealizedImageV1, generation, _ string, _ string) error {
		removed = append(removed, "portable:"+string(image.ConfigDigest)+":"+generation)
		return nil
	}
	removePortableDigest := func(_ context.Context, image providers.RealizedImageV1, _ string, _ string) error {
		removed = append(removed, "digest:"+string(image.ConfigDigest))
		return nil
	}
	removePortableDigestReference := func(_ context.Context, reference, _, _ string) error {
		removed = append(removed, "digest-reference:"+reference)
		return nil
	}
	if err := recoverPendingImageReferencesWithLocks(
		context.Background(), pending, deploy.PendingRecoveryDiscardCandidate, &oldImage,
		candidateLock, oldLock, "demo", dir, remove, removePortable, removePortableDigest, removePortableDigestReference,
	); err != nil {
		t.Fatal(err)
	}
	if len(removed) != 4 || !strings.HasPrefix(removed[2], "portable:"+string(pending.Candidate.Image.ConfigDigest)+":") ||
		removed[3] != "digest:"+string(pending.Candidate.Image.ConfigDigest) {
		t.Fatalf("discard cleanup = %#v", removed)
	}
	removed = nil
	if err := recoverPendingImageReferencesWithLocks(
		context.Background(), pending, deploy.PendingRecoveryKeepCandidate, &oldImage,
		candidateLock, oldLock, "demo", dir, remove, removePortable, removePortableDigest, removePortableDigestReference,
	); err != nil {
		t.Fatal(err)
	}
	if len(removed) != 5 || !strings.HasPrefix(removed[2], "portable:"+string(oldImage.ConfigDigest)+":") ||
		removed[3] != "digest:"+string(oldImage.ConfigDigest) ||
		removed[4] != "digest:"+string(pending.Candidate.Image.ConfigDigest) {
		t.Fatalf("keep cleanup = %#v", removed)
	}
	removed = nil
	pending.Old = nil
	if err := recoverPendingImageReferencesWithLocks(
		context.Background(), pending, deploy.PendingRecoveryKeepCandidate, nil,
		candidateLock, nil, "demo", dir, remove, removePortable, removePortableDigest, removePortableDigestReference,
	); err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 || !strings.HasPrefix(removed[0], "image:"+string(pending.Candidate.Image.ConfigDigest)+":") ||
		removed[1] != "digest:"+string(pending.Candidate.Image.ConfigDigest) {
		t.Fatalf("first-generation keep cleanup = %#v", removed)
	}
}

func TestRecoverPendingImageReferencesWithLocksDiscardsPreLockWithoutCandidateLock(t *testing.T) {
	dir, pending := pendingReferenceFixture(t)
	pending.Old = nil
	pending.Phase = deploy.PendingBuildPhaseValidated
	var removed []removedReference
	remove := func(_ context.Context, image providers.RealizedImageV1, references EnvironmentImageReferences, kind EnvironmentReferenceKind, _ string, _ string) error {
		reference, err := selectEnvironmentReference(references, kind)
		if err != nil {
			return err
		}
		removed = append(removed, removedReference{image: image, reference: reference, kind: kind})
		return nil
	}
	removePortable := func(context.Context, providers.RealizedImageV1, string, string, string) error {
		t.Fatal("pre-lock discard attempted portable cleanup without a candidate lock")
		return nil
	}
	removePortableDigest := func(context.Context, providers.RealizedImageV1, string, string) error {
		t.Fatal("pre-lock discard attempted digest cleanup without a candidate lock")
		return nil
	}
	removePortableDigestReference := func(context.Context, string, string, string) error {
		t.Fatal("pre-lock discard attempted provisional digest cleanup without a recorded alias")
		return nil
	}
	if err := recoverPendingImageReferencesWithLocks(
		context.Background(), pending, deploy.PendingRecoveryDiscardCandidate, nil,
		nil, nil, "demo", dir, remove, removePortable, removePortableDigest, removePortableDigestReference,
	); err != nil {
		t.Fatal(err)
	}
	want := []removedReference{
		{image: pending.Candidate.Image, reference: pending.Candidate.GenerationReference, kind: EnvironmentReferenceGeneration},
		{image: pending.Candidate.Image, reference: pending.Candidate.TemporaryReference, kind: EnvironmentReferenceTemporary},
	}
	if !reflect.DeepEqual(removed, want) {
		t.Fatalf("pre-lock discard cleanup = %#v, want %#v", removed, want)
	}
}

func TestRecoverPendingImageReferencesWithLocksRemovesRecordedPortableDigestAfterPreLockCrash(t *testing.T) {
	dir, pending := pendingReferenceFixture(t)
	pending.Old = nil
	portableImage := providers.RealizedImageV1{
		Digest: rendererDigest("a"), ConfigDigest: rendererDigest("a"), RootFSSubject: rendererDigest("b"),
	}
	provisional, err := NewEnvironmentPortableRuntimeLayerReference(
		portableImage.ConfigDigest, "demo", dir,
	)
	if err != nil {
		t.Fatal(err)
	}
	pending.Cleanup = []deploy.CleanupItemV1{{
		Kind: deploy.CleanupKindTemporaryImageReference, Identity: provisional,
	}}
	for _, phase := range []string{deploy.PendingBuildPhaseValidated, deploy.PendingBuildPhaseGenerationCreated} {
		t.Run(phase, func(t *testing.T) {
			pending.Phase = phase
			aliases := map[string]bool{provisional: true}
			remove := func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
				return nil
			}
			removePortable := func(context.Context, providers.RealizedImageV1, string, string, string) error {
				t.Fatal("pre-lock discard attempted generation portable cleanup without a candidate lock")
				return nil
			}
			removePortableDigest := func(context.Context, providers.RealizedImageV1, string, string) error {
				t.Fatal("pre-lock discard attempted digest portable cleanup without a candidate lock")
				return nil
			}
			removePortableDigestReference := func(_ context.Context, reference, _, _ string) error {
				if !aliases[reference] {
					t.Fatalf("provisional alias %q was not present at crash recovery", reference)
				}
				delete(aliases, reference)
				return nil
			}
			if err := recoverPendingImageReferencesWithLocks(
				context.Background(), pending, deploy.PendingRecoveryDiscardCandidate, nil,
				nil, nil, "demo", dir, remove, removePortable, removePortableDigest,
				removePortableDigestReference,
			); err != nil {
				t.Fatal(err)
			}
			if aliases[provisional] {
				t.Fatalf("provisional alias %q survived %s crash recovery", provisional, phase)
			}
		})
	}
}
