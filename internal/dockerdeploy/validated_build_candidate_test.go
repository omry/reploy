package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
)

func TestLoadValidatedBuildCandidateRequiresExactSavedInputs(t *testing.T) {
	dir, operation, store, lock, state := currentBuildFixture(t, true)
	defer operation.Unlock()
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	overrides := deploy.EmptyPackageOverridesV1(document.Environment.ID)
	inputs, err := ValidatedBuildInputs(document, state.Overlay, overrides, dir, state.Platform)
	if err != nil {
		t.Fatal(err)
	}
	record := deploy.ValidatedBuildV1{
		Schema:          deploy.ValidatedBuildSchemaV1,
		BlueprintDigest: inputs.BlueprintDigest, OverlayDigest: inputs.OverlayDigest,
		PackageOverridesDigest: inputs.PackageOverridesDigest, Platform: inputs.Platform,
		BuildLockDigest: state.Current.BuildLockDigest, Image: lock.FinalImage,
		ImageReference: state.Current.Reference,
	}
	if err := operation.CommitValidatedBuildV1(record); err != nil {
		t.Fatal(err)
	}
	candidate, found, err := LoadValidatedBuildCandidate(
		t.Context(), operation, store, document, state, overrides, dir, true, false,
	)
	if err != nil || !found || !reflect.DeepEqual(candidate.Current.Lock, lock) {
		t.Fatalf("candidate=%#v found=%v err=%v", candidate, found, err)
	}
	overrides.Environment.PackageOverrides["python"] = map[string]deploy.PackageOverrideChoiceV1{
		"demo": {Version: "2"},
	}
	if _, found, err := LoadValidatedBuildCandidate(
		t.Context(), operation, store, document, state, overrides, dir, false, false,
	); err != nil || found {
		t.Fatalf("changed choices candidate found=%v err=%v", found, err)
	}
}

func TestLoadValidatedBuildCandidateCanSkipCacheVerification(t *testing.T) {
	dir, operation, store, lock, state := currentBuildFixture(t, true)
	defer operation.Unlock()
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	overrides := deploy.EmptyPackageOverridesV1(document.Environment.ID)
	inputs, err := ValidatedBuildInputs(document, state.Overlay, overrides, dir, state.Platform)
	if err != nil {
		t.Fatal(err)
	}
	record := deploy.ValidatedBuildV1{
		Schema:          deploy.ValidatedBuildSchemaV1,
		BlueprintDigest: inputs.BlueprintDigest, OverlayDigest: inputs.OverlayDigest,
		PackageOverridesDigest: inputs.PackageOverridesDigest, Platform: inputs.Platform,
		BuildLockDigest: state.Current.BuildLockDigest, Image: lock.FinalImage,
		ImageReference: state.Current.Reference,
	}
	if err := operation.CommitValidatedBuildV1(record); err != nil {
		t.Fatal(err)
	}
	if _, err := operation.RemoveProviderStore(store); err != nil {
		t.Fatal(err)
	}

	if _, found, err := LoadValidatedBuildCandidate(
		t.Context(), operation, store, document, state, overrides, dir, false, false,
	); err != nil || !found {
		t.Fatalf("candidate without cache verification found=%v err=%v", found, err)
	}
	if _, found, err := LoadValidatedBuildCandidate(
		t.Context(), operation, store, document, state, overrides, dir, true, false,
	); err == nil || found {
		t.Fatalf("candidate with missing cache found=%v err=%v", found, err)
	}
}

func TestLoadValidatedBuildCandidateUsesReusableDebVerification(t *testing.T) {
	store, _, _, lock, deb := newPreparedAPTGraphReuseFixture(t)
	dir := filepath.Dir(filepath.Dir(store.Root()))
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()

	document, _ := testSelectedPlatformDocumentV1(t)
	base := document.Environment.Components["base"]
	base.Base.Image = lock.Base.AuthorReference
	document.Environment.Components["base"] = base
	lock.BlueprintDigest = testResolvedBlueprintDigestV1(t, document)
	lock.Overlay = deploy.EmptyRequestOverlayV1()
	lock.PackageOverrides = deploy.EmptyPackageOverrideIntentV1(document.Environment.ID)
	policyDigest, err := deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	lock.ValidationRecord, err = deploy.PublishPrefixValidation(t.Context(), store, deploy.PrefixValidationV1{
		Schema: deploy.PrefixValidationSchemaV1, SubjectRootFS: lock.FinalImage.RootFSSubject,
		Profiles: []providers.ValidationEvidence{}, RuntimePolicy: policyDigest,
		ExposedOutputs: []providers.ExecutableEvidence{},
	})
	if err != nil {
		t.Fatal(err)
	}
	lockDigest, err := operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	state := deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, document),
		Platform: lock.Platform, Overlay: lock.Overlay,
	}
	overrides := deploy.EmptyPackageOverridesV1(document.Environment.ID)
	inputs, err := ValidatedBuildInputs(document, state.Overlay, overrides, dir, state.Platform)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.CommitValidatedBuildV1(deploy.ValidatedBuildV1{
		Schema: deploy.ValidatedBuildSchemaV1, BlueprintDigest: inputs.BlueprintDigest,
		OverlayDigest: inputs.OverlayDigest, PackageOverridesDigest: inputs.PackageOverridesDigest,
		Platform: inputs.Platform, BuildLockDigest: lockDigest, Image: lock.FinalImage,
		ImageReference: "validated-reference",
	}); err != nil {
		t.Fatal(err)
	}

	debPath, err := store.BlobPath(deb.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(debPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(debPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(debPath, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(debPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}

	if _, found, err := LoadValidatedBuildCandidate(
		t.Context(), operation, store, document, state, overrides, dir, true, false,
	); err != nil || !found {
		t.Fatalf("reusable candidate found=%v err=%v", found, err)
	}
	if _, err := deploy.BuildLockStoreClosure(
		lock, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1,
	); err == nil {
		t.Fatal("full cache verification accepted changed Debian bytes")
	}
}

func TestLoadValidatedBuildCandidateRejectsImageNotBoundToLock(t *testing.T) {
	dir, operation, store, lock, state := currentBuildFixture(t, true)
	defer operation.Unlock()
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	overrides := deploy.EmptyPackageOverridesV1(document.Environment.ID)
	inputs, err := ValidatedBuildInputs(document, state.Overlay, overrides, dir, state.Platform)
	if err != nil {
		t.Fatal(err)
	}
	wrongImage := lock.FinalImage
	wrongImage.ConfigDigest = rendererDigest("f")
	if err := operation.CommitValidatedBuildV1(deploy.ValidatedBuildV1{
		Schema:          deploy.ValidatedBuildSchemaV1,
		BlueprintDigest: inputs.BlueprintDigest, OverlayDigest: inputs.OverlayDigest,
		PackageOverridesDigest: inputs.PackageOverridesDigest, Platform: inputs.Platform,
		BuildLockDigest: state.Current.BuildLockDigest, Image: wrongImage,
		ImageReference: state.Current.Reference,
	}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := LoadValidatedBuildCandidate(
		t.Context(), operation, store, document, state, overrides, dir, false, false,
	); err == nil || found || !strings.Contains(err.Error(), "does not match its build lock") {
		t.Fatalf("found=%v error=%v", found, err)
	}
}

func TestInspectStagedOverrideValidationTreatsCleanedCacheAsNotValidated(t *testing.T) {
	dir, operation, store, lock, state := currentBuildFixture(t, true)
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	state.BlueprintSource = "file:///tmp/example.blueprint.yaml"
	if err := operation.CommitStateV1(state.Current, state); err != nil {
		t.Fatal(err)
	}
	overrides := deploy.EmptyPackageOverridesV1(document.Environment.ID)
	inputs, err := ValidatedBuildInputs(document, state.Overlay, overrides, dir, state.Platform)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.CommitValidatedBuildV1(deploy.ValidatedBuildV1{
		Schema:          deploy.ValidatedBuildSchemaV1,
		BlueprintDigest: inputs.BlueprintDigest, OverlayDigest: inputs.OverlayDigest,
		PackageOverridesDigest: inputs.PackageOverridesDigest, Platform: inputs.Platform,
		BuildLockDigest: state.Current.BuildLockDigest, Image: lock.FinalImage,
		ImageReference: state.Current.Reference,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := operation.RemoveProviderStore(store); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}

	status, err := InspectStagedOverrideValidation(t.Context(), dir)
	if err != nil || status.Validated || len(status.Packages) != 0 {
		t.Fatalf("status=%#v err=%v", status, err)
	}
}

func TestInspectStagedOverrideValidationRejectsMissingDeploymentState(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	_, err = InspectStagedOverrideValidation(t.Context(), dir)
	if err == nil || !strings.Contains(err.Error(), "existing staged deployment") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidatedBuildLockMustMatchEveryValidatedInput(t *testing.T) {
	dir, operation, _, lock, state := currentBuildFixture(t, true)
	defer operation.Unlock()
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := ValidatedBuildInputs(
		document, state.Overlay, deploy.EmptyPackageOverridesV1(document.Environment.ID), dir, state.Platform,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateBuildLockMatchesValidatedInputs(lock, inputs); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		change func(*ValidatedBuildInputsV1)
	}{
		{name: "blueprint", change: func(input *ValidatedBuildInputsV1) {
			input.BlueprintDigest = rendererDigest("f")
		}},
		{name: "overlay", change: func(input *ValidatedBuildInputsV1) {
			input.OverlayDigest = rendererDigest("e")
		}},
		{name: "package overrides", change: func(input *ValidatedBuildInputsV1) {
			input.PackageOverrides = deploy.PackageOverrideIntentV1{
				Schema: deploy.PackageOverrideIntentSchemaV1, EnvironmentID: document.Environment.ID,
				Choices: []deploy.PackageOverrideIntentChoiceV1{{
					Provider: "python", Package: "demo", Kind: "version", Version: "2",
				}},
			}
		}},
		{name: "platform", change: func(input *ValidatedBuildInputsV1) {
			input.Platform.Canonical = "linux/arm64"
			input.Platform.Architecture = "arm64"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := inputs
			test.change(&changed)
			if err := validateBuildLockMatchesValidatedInputs(lock, changed); err == nil {
				t.Fatal("mismatched validated input was accepted")
			}
		})
	}
}

func TestPublishValidatedBuildRecordsCandidateWithoutChangingState(t *testing.T) {
	dir, operation, store, lock, state := currentBuildFixture(t, true)
	defer operation.Unlock()
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := ValidatedBuildInputs(
		document, state.Overlay, deploy.EmptyPackageOverridesV1(document.Environment.ID), dir, state.Platform,
	)
	if err != nil {
		t.Fatal(err)
	}
	created := 0
	record, err := publishValidatedBuild(
		t.Context(), operation, store, document.Environment.ID, dir, lock, inputs,
		publishValidatedBuildBackendV1{
			newReferences: func(string, string) (EnvironmentImageReferences, error) {
				return EnvironmentImageReferences{Temporary: "temporary", Generation: "validated-reference"}, nil
			},
			createReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
				created++
				return nil
			},
			removeReference: func(context.Context, providers.RealizedImageV1, string, string, string) error {
				return nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if created != 1 || record.ImageReference != "validated-reference" {
		t.Fatalf("created=%d record=%#v", created, record)
	}
	after, found, err := operation.ReadStateV1()
	if err != nil || !found || !reflect.DeepEqual(after, state) {
		t.Fatalf("trial build changed current state: %#v/%v/%v", after, found, err)
	}
}

func TestPublishValidatedBuildRejectsMismatchedLockBeforeCreatingReference(t *testing.T) {
	dir, operation, store, lock, state := currentBuildFixture(t, true)
	defer operation.Unlock()
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := ValidatedBuildInputs(
		document, state.Overlay, deploy.EmptyPackageOverridesV1(document.Environment.ID), dir, state.Platform,
	)
	if err != nil {
		t.Fatal(err)
	}
	inputs.BlueprintDigest = rendererDigest("f")
	created := false
	_, err = publishValidatedBuild(
		t.Context(), operation, store, document.Environment.ID, dir, lock, inputs,
		publishValidatedBuildBackendV1{
			newReferences: func(string, string) (EnvironmentImageReferences, error) {
				created = true
				return EnvironmentImageReferences{}, nil
			},
			createReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
				return nil
			},
			removeReference: func(context.Context, providers.RealizedImageV1, string, string, string) error {
				return nil
			},
		},
	)
	if err == nil || created {
		t.Fatalf("error=%v created=%v", err, created)
	}
}

func TestPublishValidatedBuildWrapsNewReferenceCleanupFailure(t *testing.T) {
	dir, operation, store, lock, state := currentBuildFixture(t, true)
	defer operation.Unlock()
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := ValidatedBuildInputs(
		document, state.Overlay, deploy.EmptyPackageOverridesV1(document.Environment.ID), dir, state.Platform,
	)
	if err != nil {
		t.Fatal(err)
	}
	cleanupFailure := errors.New("cleanup failed")
	removed := false
	_, err = publishValidatedBuild(
		t.Context(), operation, store, document.Environment.ID, dir, lock, inputs,
		publishValidatedBuildBackendV1{
			newReferences: func(string, string) (EnvironmentImageReferences, error) {
				return EnvironmentImageReferences{Temporary: "temporary", Generation: "validated-reference"}, nil
			},
			createReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
				return os.Mkdir(filepath.Join(dir, ".reploy", "validated-build.json"), 0o700)
			},
			removeReference: func(_ context.Context, _ providers.RealizedImageV1, reference, _, _ string) error {
				removed = true
				if reference != "validated-reference" {
					t.Fatalf("removed reference = %q", reference)
				}
				return cleanupFailure
			},
		},
	)
	if !removed || !errors.Is(err, cleanupFailure) ||
		!strings.Contains(err.Error(), "cleanup newly created validated image reference") {
		t.Fatalf("removed=%v error=%v", removed, err)
	}
}

func TestPublishValidatedBuildRejectsReferenceCollisionBeforeCreatingReference(t *testing.T) {
	dir, operation, store, lock, state := currentBuildFixture(t, true)
	defer operation.Unlock()
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := ValidatedBuildInputs(
		document, state.Overlay, deploy.EmptyPackageOverridesV1(document.Environment.ID), dir, state.Platform,
	)
	if err != nil {
		t.Fatal(err)
	}
	oldImage := lock.FinalImage
	oldImage.ConfigDigest = rendererDigest("f")
	old := deploy.ValidatedBuildV1{
		Schema: deploy.ValidatedBuildSchemaV1, BlueprintDigest: inputs.BlueprintDigest,
		OverlayDigest: inputs.OverlayDigest, PackageOverridesDigest: inputs.PackageOverridesDigest,
		Platform: inputs.Platform, BuildLockDigest: state.Current.BuildLockDigest,
		Image: oldImage, ImageReference: "validated-reference",
	}
	if err := operation.CommitValidatedBuildV1(old); err != nil {
		t.Fatal(err)
	}
	created := false
	_, err = publishValidatedBuild(
		t.Context(), operation, store, document.Environment.ID, dir, lock, inputs,
		publishValidatedBuildBackendV1{
			newReferences: func(string, string) (EnvironmentImageReferences, error) {
				return EnvironmentImageReferences{Temporary: "temporary", Generation: old.ImageReference}, nil
			},
			createReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
				created = true
				return nil
			},
			removeReference: func(context.Context, providers.RealizedImageV1, string, string, string) error {
				return nil
			},
		},
	)
	if err == nil || created || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("error=%v created=%v", err, created)
	}
}

func TestPublishValidatedBuildRetainsFailedCleanupForRetry(t *testing.T) {
	dir, operation, store, lock, state := currentBuildFixture(t, true)
	defer operation.Unlock()
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := ValidatedBuildInputs(
		document, state.Overlay, deploy.EmptyPackageOverridesV1(document.Environment.ID), dir, state.Platform,
	)
	if err != nil {
		t.Fatal(err)
	}
	old := deploy.ValidatedBuildV1{
		Schema: deploy.ValidatedBuildSchemaV1, BlueprintDigest: inputs.BlueprintDigest,
		OverlayDigest: inputs.OverlayDigest, PackageOverridesDigest: inputs.PackageOverridesDigest,
		Platform: inputs.Platform, BuildLockDigest: state.Current.BuildLockDigest,
		Image: lock.FinalImage, ImageReference: state.Current.Reference,
	}
	if err := operation.CommitValidatedBuildV1(old); err != nil {
		t.Fatal(err)
	}
	cleanupFailure := errors.New("Docker is busy")
	record, err := publishValidatedBuild(
		t.Context(), operation, store, document.Environment.ID, dir, lock, inputs,
		publishValidatedBuildBackendV1{
			newReferences: func(string, string) (EnvironmentImageReferences, error) {
				return EnvironmentImageReferences{Temporary: "temporary", Generation: "validated-reference"}, nil
			},
			createReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
				return nil
			},
			removeReference: func(_ context.Context, _ providers.RealizedImageV1, reference string, _, _ string) error {
				if reference == old.ImageReference {
					return cleanupFailure
				}
				return nil
			},
		},
	)
	if err != nil {
		t.Fatalf("committed validated build reported failure: %v", err)
	}
	if len(record.PendingCleanup) != 1 || record.PendingCleanup[0].ImageReference != old.ImageReference {
		t.Fatalf("pending cleanup = %#v", record.PendingCleanup)
	}
	persisted, found, err := operation.ReadValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(persisted, record) {
		t.Fatalf("persisted = %#v, found=%v, err=%v", persisted, found, err)
	}
	record, cleanupErrors := cleanupPendingValidatedBuildReferences(
		t.Context(), operation, record, document.Environment.ID, dir,
		func(context.Context, providers.RealizedImageV1, string, string, string) error { return nil },
	)
	if len(cleanupErrors) != 0 || len(record.PendingCleanup) != 0 {
		t.Fatalf("record=%#v cleanup errors=%v", record, cleanupErrors)
	}
	persisted, found, err = operation.ReadValidatedBuildV1()
	if err != nil || !found || len(persisted.PendingCleanup) != 0 {
		t.Fatalf("persisted cleanup = %#v, found=%v, err=%v", persisted, found, err)
	}
}

func TestRetryValidatedBuildCleanupRejectsNilContext(t *testing.T) {
	if _, _, err := RetryValidatedBuildCleanup(nil, nil, "demo", t.TempDir()); err == nil {
		t.Fatal("nil cleanup context was accepted")
	}
}

func TestDiscardValidatedBuildDoesNotDependOnBuildLockForCleanup(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	image := providers.RealizedImageV1{
		Digest: rendererDigest("1"), ConfigDigest: rendererDigest("2"), RootFSSubject: rendererDigest("3"),
	}
	record := deploy.ValidatedBuildV1{
		Schema: deploy.ValidatedBuildSchemaV1, BlueprintDigest: rendererDigest("4"),
		OverlayDigest: rendererDigest("5"), PackageOverridesDigest: rendererDigest("6"),
		Platform: platform, BuildLockDigest: rendererDigest("7"), Image: image,
		ImageReference: "reploy/env/demo:validated",
		PendingCleanup: []deploy.ValidatedBuildReferenceV1{{
			Image: image, ImageReference: "reploy/env/demo:older",
		}},
	}
	if err := operation.CommitValidatedBuildV1(record); err != nil {
		t.Fatal(err)
	}
	removed := []string{}
	if err := discardValidatedBuild(
		t.Context(), operation, "demo", dir,
		func(_ context.Context, gotImage providers.RealizedImageV1, reference, _, _ string) error {
			if !reflect.DeepEqual(gotImage, image) {
				t.Fatalf("removed image = %#v", gotImage)
			}
			removed = append(removed, reference)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(removed, []string{"reploy/env/demo:older", "reploy/env/demo:validated"}) {
		t.Fatalf("removed = %#v", removed)
	}
	if _, found, err := operation.ReadValidatedBuildV1(); err != nil || found {
		t.Fatalf("validated build remained: found=%v err=%v", found, err)
	}
}
