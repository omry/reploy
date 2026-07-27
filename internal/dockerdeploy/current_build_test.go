package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

func TestValidateCurrentBuildReturnsVerifiedLockAndClosure(t *testing.T) {
	dir, operation, store, lock, state := currentBuildFixture(t, true)
	defer operation.Unlock()
	calls := 0
	result, found, err := validateCurrentBuild(t.Context(), operation, store, "demo", dir, func(_ context.Context, image providers.RealizedImageV1, reference string, environment string, deploymentDir string) error {
		calls++
		if image != lock.FinalImage || reference != state.Current.Reference || environment != "demo" || deploymentDir != dir {
			t.Fatalf("reference verification arguments changed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !found || calls != 1 || !reflect.DeepEqual(result.State, state) || !reflect.DeepEqual(result.Lock, lock) || result.Generation != *state.Current {
		t.Fatalf("current build = %#v, found=%v calls=%d", result, found, calls)
	}
}

func TestValidateCurrentBuildReportsAbsenceWithoutDocker(t *testing.T) {
	dir := t.TempDir()
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	calls := 0
	result, found, err := validateCurrentBuild(t.Context(), operation, store, "demo", dir, func(context.Context, providers.RealizedImageV1, string, string, string) error {
		calls++
		return nil
	})
	if err != nil || found || calls != 0 || !reflect.DeepEqual(result, CurrentBuild{}) {
		t.Fatalf("current build = %#v, found=%v calls=%d err=%v", result, found, calls, err)
	}
}

func TestValidateCurrentBuildRequiresPendingPublicationRecovery(t *testing.T) {
	dir, pending := pendingReferenceFixture(t)
	pending.Old = nil
	pending.Phase = deploy.PendingBuildPhaseValidated
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	if err := operation.WritePendingBuild(pending); err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	_, found, err := validateCurrentBuild(t.Context(), operation, store, "demo", dir, func(context.Context, providers.RealizedImageV1, string, string, string) error {
		calls++
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "recovery is required") || found || calls != 0 {
		t.Fatalf("found=%v calls=%d error=%v", found, calls, err)
	}
}

func TestValidateCurrentBuildTreatsMissingLockAsCorruption(t *testing.T) {
	dir, operation, store, _, _ := currentBuildFixture(t, false)
	defer operation.Unlock()
	calls := 0
	_, found, err := validateCurrentBuild(t.Context(), operation, store, "demo", dir, func(context.Context, providers.RealizedImageV1, string, string, string) error {
		calls++
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "lock") || found || calls != 0 {
		t.Fatalf("found=%v calls=%d error=%v", found, calls, err)
	}
}

func TestValidateCurrentBuildDoesNotReadArtifactClosure(t *testing.T) {
	dir, operation, store, lock, _ := currentBuildFixture(t, true)
	defer operation.Unlock()
	path, err := store.ValidationRecordPath(lock.ValidationRecord)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	calls := 0
	_, found, err := validateCurrentBuild(t.Context(), operation, store, "demo", dir, func(context.Context, providers.RealizedImageV1, string, string, string) error {
		calls++
		return nil
	})
	if err != nil || !found || calls != 1 {
		t.Fatalf("found=%v calls=%d error=%v", found, calls, err)
	}
}

func TestVerifyEnvironmentGenerationReferenceRequiresExactConfigID(t *testing.T) {
	dir := t.TempDir()
	references := fixedPublicationReferences(t, dir, 0x61)
	image := providers.RealizedImageV1{Digest: rendererDigest("1"), ConfigDigest: rendererDigest("2"), RootFSSubject: rendererDigest("3")}
	commands := 0
	run := func(_ context.Context, args ...string) (string, error) {
		commands++
		if !reflect.DeepEqual(args, []string{"image", "inspect", "--format", "{{.Id}}", references.Generation}) {
			t.Fatalf("Docker args = %#v", args)
		}
		return string(image.ConfigDigest), nil
	}
	if err := verifyEnvironmentGenerationReference(t.Context(), image, references.Generation, "demo", dir, run); err != nil {
		t.Fatal(err)
	}
	err := verifyEnvironmentGenerationReference(t.Context(), image, references.Generation, "demo", dir, func(context.Context, ...string) (string, error) {
		return string(rendererDigest("4")), nil
	})
	if err == nil || !strings.Contains(err.Error(), "want") || commands != 1 {
		t.Fatalf("commands=%d error=%v", commands, err)
	}
	want := errors.New("Docker unavailable")
	err = verifyEnvironmentGenerationReference(t.Context(), image, references.Generation, "demo", dir, func(context.Context, ...string) (string, error) {
		return "", want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func currentBuildFixture(t *testing.T, publishLock bool) (string, *deploy.OperationLock, providerstore.Store, deploy.BuildLockV1, deploy.StateV1) {
	t.Helper()
	dir := t.TempDir()
	store, lock := publicationLockFixture(t, dir, "4", "5", "6")
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	lockDigest, err := deploy.BuildLockDigestV1(lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	if publishLock {
		published, err := operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1)
		if err != nil || published != lockDigest {
			t.Fatalf("publish lock = %s, error=%v", published, err)
		}
	}
	references := fixedPublicationReferences(t, dir, 0x62)
	policyDigest, err := deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	generation := deploy.EnvironmentGenerationState{
		Reference: references.Generation, ImageDigest: lock.FinalImage.Digest,
		RootFSSubject: lock.FinalImage.RootFSSubject, BuildLockDigest: lockDigest,
		Platform: lock.Platform, RuntimePolicyDigest: policyDigest,
	}
	document, _ := testSelectedPlatformDocumentV1(t)
	state := deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, document),
		Platform: lock.Platform, Overlay: lock.Overlay, Current: &generation,
	}
	if err := operation.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	return dir, operation, store, lock, state
}
