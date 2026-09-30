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
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
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
	document.Environment.Base.Image = lock.Base.AuthorReference
	if err := document.Environment.RebuildProviderContributions(); err != nil {
		t.Fatal(err)
	}
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
				pending, found, err := operation.ReadPendingValidatedBuildV1()
				if err != nil || !found || pending.Final.ImageReference != "validated-reference" {
					t.Fatalf("candidate intent was not durable before tag creation: found=%v err=%v pending=%#v", found, err, pending)
				}
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
	if _, found, err := operation.ReadPendingValidatedBuildV1(); err != nil || found {
		t.Fatalf("committed candidate retained intent: found=%v err=%v", found, err)
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
	references, err := NewEnvironmentImageReferences(document.Environment.ID, dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = publishValidatedBuild(
		t.Context(), operation, store, document.Environment.ID, dir, lock, inputs,
		publishValidatedBuildBackendV1{
			newReferences: func(string, string) (EnvironmentImageReferences, error) {
				return references, nil
			},
			createReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
				return errors.New("tag creation failed after mutation")
			},
			removeReference: func(_ context.Context, _ providers.RealizedImageV1, reference, _, _ string) error {
				removed = true
				if reference != references.Generation {
					t.Fatalf("removed reference = %q", reference)
				}
				return cleanupFailure
			},
		},
	)
	if !removed || !errors.Is(err, cleanupFailure) ||
		!strings.Contains(err.Error(), "recover pending validated final image reference") {
		t.Fatalf("removed=%v error=%v", removed, err)
	}
	if _, found, err := operation.ReadPendingValidatedBuildV1(); err != nil || !found {
		t.Fatalf("failed cleanup lost pending intent: found=%v err=%v", found, err)
	}
}

func TestRecoverPendingValidatedBuildPreservesPreviousCandidate(t *testing.T) {
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
	oldRefs := fixedPublicationReferences(t, dir, 0x51)
	old := deploy.ValidatedBuildV1{
		Schema: deploy.ValidatedBuildSchemaV1, BlueprintDigest: inputs.BlueprintDigest,
		OverlayDigest: inputs.OverlayDigest, PackageOverridesDigest: inputs.PackageOverridesDigest,
		Platform: inputs.Platform, BuildLockDigest: state.Current.BuildLockDigest,
		Image: lock.FinalImage, ImageReference: oldRefs.Generation,
	}
	if err := operation.CommitValidatedBuildV1(old); err != nil {
		t.Fatal(err)
	}
	newRefs := fixedPublicationReferences(t, dir, 0x52)
	portableAlias, err := NewEnvironmentPortableRuntimeLayerReferenceForGeneration(
		lock.FinalImage.ConfigDigest, newRefs.Generation, document.Environment.ID, dir,
	)
	if err != nil {
		t.Fatal(err)
	}
	lockDigest, err := deploy.BuildLockDigestV1(lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	pending := deploy.PendingValidatedBuildV1{
		Schema: deploy.PendingValidatedBuildSchemaV1, BuildLockDigest: lockDigest,
		Final:    deploy.ValidatedBuildReferenceV1{Image: lock.FinalImage, ImageReference: newRefs.Generation},
		Portable: &deploy.ValidatedBuildReferenceV1{Image: lock.FinalImage, ImageReference: portableAlias},
	}
	if err := operation.WritePendingValidatedBuildV1(pending); err != nil {
		t.Fatal(err)
	}
	removed := []string{}
	remove := func(_ context.Context, _ providers.RealizedImageV1, reference, _, _ string) error {
		removed = append(removed, reference)
		return nil
	}
	if err := recoverPendingValidatedBuildV1(t.Context(), operation, store, document.Environment.ID, dir, remove); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(removed, []string{portableAlias, newRefs.Generation}) {
		t.Fatalf("interrupted candidate cleanup = %#v", removed)
	}
	if _, found, err := operation.ReadPendingValidatedBuildV1(); err != nil || found {
		t.Fatalf("interrupted intent remains: found=%v err=%v", found, err)
	}
	preserved, found, err := operation.ReadValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(preserved, old) {
		t.Fatalf("previous candidate changed: found=%v err=%v record=%#v", found, err, preserved)
	}
	if err := operation.WritePendingValidatedBuildV1(pending); err != nil {
		t.Fatal(err)
	}
	committed := old
	committed.BuildLockDigest = pending.BuildLockDigest
	committed.ImageReference = pending.Final.ImageReference
	committed.PortableRuntimeLayer = pending.Portable
	if err := operation.CommitValidatedBuildV1(committed); err != nil {
		t.Fatal(err)
	}
	if err := recoverPendingValidatedBuildV1(t.Context(), operation, store, document.Environment.ID, dir, remove); err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Fatalf("committed candidate aliases were removed: %#v", removed)
	}
}

func TestRecoverPendingValidatedBuildRetainsFailedCleanupIntent(t *testing.T) {
	dir, operation, store, lock, state := currentBuildFixture(t, true)
	defer operation.Unlock()
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	references := fixedPublicationReferences(t, dir, 0x53)
	lockDigest, err := deploy.BuildLockDigestV1(lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	pending := deploy.PendingValidatedBuildV1{
		Schema: deploy.PendingValidatedBuildSchemaV1, BuildLockDigest: lockDigest,
		Final: deploy.ValidatedBuildReferenceV1{Image: lock.FinalImage, ImageReference: references.Generation},
	}
	if err := operation.WritePendingValidatedBuildV1(pending); err != nil {
		t.Fatal(err)
	}
	cleanupFailure := errors.New("Docker unavailable")
	remove := func(context.Context, providers.RealizedImageV1, string, string, string) error {
		return cleanupFailure
	}
	if err := recoverPendingValidatedBuildV1(t.Context(), operation, store, document.Environment.ID, dir, remove); !errors.Is(err, cleanupFailure) {
		t.Fatalf("recovery error = %v", err)
	}
	loaded, found, err := operation.ReadPendingValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(loaded, pending) {
		t.Fatalf("failed cleanup lost intent: found=%v err=%v record=%#v", found, err, loaded)
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

func TestPublishValidatedBuildPrunesSupersededStorageAndRetainsCurrentAndCandidate(t *testing.T) {
	dir, operation, store, current, state := currentBuildFixture(t, true)
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
	orphan, err := store.Publish(t.Context(), "packages/superseded.whl", "wheel", strings.NewReader("superseded"))
	if err != nil {
		t.Fatal(err)
	}
	orphanReference, err := orphan.StoreObjectRef()
	if err != nil {
		t.Fatal(err)
	}
	old := validatedBuildStorageVariant(t, store, current, "7", "8")
	oldDigest, err := operation.PublishBuildLock(old, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	oldRecord := deploy.ValidatedBuildV1{
		Schema: deploy.ValidatedBuildSchemaV1, BlueprintDigest: inputs.BlueprintDigest,
		OverlayDigest: inputs.OverlayDigest, PackageOverridesDigest: inputs.PackageOverridesDigest,
		Platform: inputs.Platform, BuildLockDigest: oldDigest, Image: old.FinalImage,
		ImageReference: "reploy/env/demo:validated-old",
	}
	if err := operation.CommitValidatedBuildV1(oldRecord); err != nil {
		t.Fatal(err)
	}
	candidate := validatedBuildStorageVariant(t, store, current, "a", "b")
	record, err := publishValidatedBuild(
		t.Context(), operation, store, document.Environment.ID, dir, candidate, inputs,
		publishValidatedBuildBackendV1{
			newReferences: func(string, string) (EnvironmentImageReferences, error) {
				return EnvironmentImageReferences{
					Temporary:  "reploy/env/demo:temporary-new",
					Generation: "reploy/env/demo:validated-new",
				}, nil
			},
			createReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
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
	if record.PendingStorageCleanup {
		t.Fatalf("successful cleanup remained pending: %#v", record)
	}
	candidateDigest, err := deploy.BuildLockDigestV1(candidate, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	for _, digest := range []canonical.Digest{state.Current.BuildLockDigest, candidateDigest} {
		if _, found, err := operation.ReadBuildLock(digest, registry.ValidateRequirementProfileV1); err != nil || !found {
			t.Fatalf("retained lock %s found=%v err=%v", digest, found, err)
		}
	}
	if _, found, err := operation.ReadBuildLock(oldDigest, registry.ValidateRequirementProfileV1); err != nil || found {
		t.Fatalf("superseded lock found=%v err=%v", found, err)
	}
	validationPath, err := store.ValidationRecordPath(current.ValidationRecord)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(validationPath); err != nil {
		t.Fatalf("shared retained validation missing: %v", err)
	}
	orphanPath, err := store.BlobPath(orphanReference.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(orphanPath); !os.IsNotExist(err) {
		t.Fatalf("superseded provider-store object remains: %v", err)
	}
}

func TestPublishValidatedBuildPersistsStorageCleanupForRetry(t *testing.T) {
	dir, operation, store, current, state := currentBuildFixture(t, true)
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
	candidate := validatedBuildStorageVariant(t, store, current, "7", "8")
	blocker := filepath.Join(dir, ".reploy", "locks", "unknown")
	if err := os.WriteFile(blocker, []byte("block cleanup"), 0o600); err != nil {
		t.Fatal(err)
	}
	record, err := publishValidatedBuild(
		t.Context(), operation, store, document.Environment.ID, dir, candidate, inputs,
		publishValidatedBuildBackendV1{
			newReferences: func(string, string) (EnvironmentImageReferences, error) {
				return EnvironmentImageReferences{
					Temporary:  "reploy/env/demo:temporary-pending",
					Generation: "reploy/env/demo:validated-pending",
				}, nil
			},
			createReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
				return nil
			},
			removeReference: func(context.Context, providers.RealizedImageV1, string, string, string) error {
				return nil
			},
		},
	)
	if err != nil || !record.PendingStorageCleanup {
		t.Fatalf("validation result = %#v, err=%v", record, err)
	}
	var warning strings.Builder
	writeValidatedBuildCleanupWarning(&warning, record)
	if !strings.Contains(warning.String(), "validated build storage is pending") ||
		!strings.Contains(warning.String(), "retry automatically") {
		t.Fatalf("cleanup warning = %q", warning.String())
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	retried, found, err := RetryValidatedBuildCleanup(
		t.Context(), operation, store, document.Environment.ID, dir,
	)
	if err != nil || !found || retried.PendingStorageCleanup {
		t.Fatalf("retried cleanup = %#v, found=%v err=%v", retried, found, err)
	}
}

func TestDiscardValidatedBuildPersistsAndRetriesStorageCleanup(t *testing.T) {
	dir, operation, store, current, state := currentBuildFixture(t, true)
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
	candidate := validatedBuildStorageVariant(t, store, current, "7", "8")
	candidateDigest, err := operation.PublishBuildLock(candidate, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.CommitValidatedBuildV1(deploy.ValidatedBuildV1{
		Schema: deploy.ValidatedBuildSchemaV1, BlueprintDigest: inputs.BlueprintDigest,
		OverlayDigest: inputs.OverlayDigest, PackageOverridesDigest: inputs.PackageOverridesDigest,
		Platform: inputs.Platform, BuildLockDigest: candidateDigest, Image: candidate.FinalImage,
		ImageReference: "reploy/env/demo:validated-discard",
	}); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(dir, ".reploy", "locks", "unknown")
	if err := os.WriteFile(blocker, []byte("block cleanup"), 0o600); err != nil {
		t.Fatal(err)
	}
	removeReference := func(context.Context, providers.RealizedImageV1, string, string, string) error {
		return nil
	}
	restorePortable := func(context.Context, providers.RealizedImageV1, string, string, string) error {
		t.Fatal("unexpected portable reference restore")
		return nil
	}
	pending, err := discardValidatedBuild(
		t.Context(), operation, store, document.Environment.ID, dir, removeReference, restorePortable,
	)
	if err != nil || !pending {
		t.Fatalf("first discard pending=%v err=%v", pending, err)
	}
	record, found, err := operation.ReadValidatedBuildV1()
	if err != nil || !found || !record.Discarded || !record.PendingStorageCleanup {
		t.Fatalf("persisted discard = %#v, found=%v err=%v", record, found, err)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	pending, err = discardValidatedBuild(
		t.Context(), operation, store, document.Environment.ID, dir, removeReference, restorePortable,
	)
	if err != nil || pending {
		t.Fatalf("retried discard pending=%v err=%v", pending, err)
	}
	if _, found, err := operation.ReadValidatedBuildV1(); err != nil || found {
		t.Fatalf("discard cleanup record remained: found=%v err=%v", found, err)
	}
	if _, found, err := operation.ReadBuildLock(candidateDigest, registry.ValidateRequirementProfileV1); err != nil || found {
		t.Fatalf("discarded lock found=%v err=%v", found, err)
	}
	if _, found, err := operation.ReadBuildLock(state.Current.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil || !found {
		t.Fatalf("current lock found=%v err=%v", found, err)
	}
}

func validatedBuildStorageVariant(
	t *testing.T,
	store providerstore.Store,
	base deploy.BuildLockV1,
	imageChar string,
	configChar string,
) deploy.BuildLockV1 {
	t.Helper()
	lock := base
	lock.FinalImage = providers.RealizedImageV1{
		Digest: rendererDigest(imageChar), ConfigDigest: rendererDigest(configChar),
		RootFSSubject: base.FinalImage.RootFSSubject,
	}
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
	if err := deploy.ValidateBuildLockV1(lock, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	return lock
}

func TestRetryValidatedBuildCleanupRejectsNilContext(t *testing.T) {
	if _, _, err := RetryValidatedBuildCleanup(nil, nil, providerstore.Store{}, "demo", t.TempDir()); err == nil {
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
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	removed := []string{}
	pending, err := discardValidatedBuild(
		t.Context(), operation, store, "demo", dir,
		func(_ context.Context, gotImage providers.RealizedImageV1, reference, _, _ string) error {
			if !reflect.DeepEqual(gotImage, image) {
				t.Fatalf("removed image = %#v", gotImage)
			}
			removed = append(removed, reference)
			return nil
		},
		func(context.Context, providers.RealizedImageV1, string, string, string) error {
			t.Fatal("unexpected portable reference restore")
			return nil
		},
	)
	if err != nil || pending {
		t.Fatalf("discard pending=%v err=%v", pending, err)
	}
	if !reflect.DeepEqual(removed, []string{"reploy/env/demo:older", "reploy/env/demo:validated"}) {
		t.Fatalf("removed = %#v", removed)
	}
	if _, found, err := operation.ReadValidatedBuildV1(); err != nil || found {
		t.Fatalf("validated build remained: found=%v err=%v", found, err)
	}
}

func TestValidatedBuildRetainsAndCleansPortableRuntimeReference(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	dir := filepath.Dir(filepath.Dir(fixture.store.Root()))
	lock := fixture.lock
	document, platform := testSelectedPlatformDocumentV1(t)
	lock.BlueprintDigest = testResolvedBlueprintDigestV1(t, document)
	lock.Platform = platform
	lock.PackageOverrides = deploy.EmptyPackageOverrideIntentV1(document.Environment.ID)
	for _, node := range fixture.request.Plan.Nodes {
		if node.ID == "base" {
			var err error
			lock.BasePlanDigest, err = providers.ProviderNodePlanDigest(node)
			if err != nil {
				t.Fatal(err)
			}
			if author, ok := node.Request.Value["image"].(string); ok {
				lock.Base.AuthorReference = author
			}
		}
	}
	document.Environment.Base.Image = lock.Base.AuthorReference
	if err := document.Environment.RebuildProviderContributions(); err != nil {
		t.Fatal(err)
	}
	lock.BlueprintDigest = testResolvedBlueprintDigestV1(t, document)
	tools := buildLockAssemblyPortableToolsV1(t, fixture.store, fixture.request.Plan, fixture.request.NodeID)
	lock.PortableTools = &tools
	portableImage := providers.RealizedImageV1{
		Digest: rendererDigest("c"), ConfigDigest: rendererDigest("d"), RootFSSubject: rendererDigest("e"),
	}
	transaction, err := deploy.PortableRuntimeLayerTransactionDigestV1(tools, lock.RuntimeLayer.Upstream, portableImage)
	if err != nil {
		t.Fatal(err)
	}
	lock.PortableRuntimeLayer = &deploy.PortableRuntimeLayerV1{
		Schema: deploy.PortableRuntimeLayerSchemaV1, Upstream: lock.RuntimeLayer.Upstream,
		Result: portableImage, TransactionDigest: transaction,
	}
	lock.RuntimeLayer.Upstream = portableImage
	lock.RuntimeLayer.TransactionDigest, err = deploy.ApplicationRuntimeLayerTransactionDigestV1(
		lock.RuntimeLayer.Verifier, lock.RuntimeLayer.Account, portableImage, platform,
	)
	if err != nil {
		t.Fatal(err)
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	lock.ValidationRecord, err = deploy.PublishPrefixValidation(t.Context(), fixture.store, deploy.PrefixValidationV1{
		Schema: deploy.PrefixValidationSchemaV1, SubjectRootFS: lock.FinalImage.RootFSSubject,
		Profiles: []providers.ValidationEvidence{}, RuntimePolicy: policyDigest,
		ExposedOutputs: []providers.ExecutableEvidence{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := deploy.ValidateBuildLockV1(lock, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	inputs, err := ValidatedBuildInputs(
		document, lock.Overlay, deploy.EmptyPackageOverridesV1(document.Environment.ID), dir, platform,
	)
	if err != nil {
		t.Fatal(err)
	}
	sequence := byte(0x41)
	created := []string{}
	removed := []string{}
	failedPortableCleanup := ""
	backend := publishValidatedBuildBackendV1{
		newReferences: func(string, string) (EnvironmentImageReferences, error) {
			reference := fixedPublicationReferences(t, dir, sequence)
			sequence++
			return reference, nil
		},
		createReference: func(_ context.Context, _ providers.RealizedImageV1, refs EnvironmentImageReferences, _ EnvironmentReferenceKind, _, _ string) error {
			created = append(created, refs.Generation)
			return nil
		},
		createPortableReference: func(_ context.Context, image providers.RealizedImageV1, generation, environment, deploymentDir string) error {
			if image != portableImage {
				t.Fatalf("portable image = %#v", image)
			}
			lockDigest, err := deploy.BuildLockDigestV1(lock, registry.ValidateRequirementProfileV1)
			if err != nil {
				return err
			}
			if _, found, err := operation.ReadBuildLock(lockDigest, registry.ValidateRequirementProfileV1); err != nil || !found {
				t.Fatalf("portable alias was created before its lock: found=%v err=%v", found, err)
			}
			pending, found, err := operation.ReadPendingValidatedBuildV1()
			if err != nil || !found || pending.Portable == nil || pending.Portable.Image != image {
				t.Fatalf("portable alias has no durable intent: found=%v err=%v pending=%#v", found, err, pending)
			}
			reference, err := NewEnvironmentPortableRuntimeLayerReferenceForGeneration(image.ConfigDigest, generation, environment, deploymentDir)
			if err == nil {
				created = append(created, reference)
			}
			return err
		},
		removeReference: func(_ context.Context, _ providers.RealizedImageV1, reference, _, _ string) error {
			removed = append(removed, reference)
			if reference == failedPortableCleanup {
				return errors.New("portable alias cleanup interrupted")
			}
			return nil
		},
	}
	first, err := publishValidatedBuild(t.Context(), operation, fixture.store, document.Environment.ID, dir, lock, inputs, backend)
	if err != nil {
		t.Fatal(err)
	}
	if first.PortableRuntimeLayer == nil || len(created) != 2 || created[1] != first.PortableRuntimeLayer.ImageReference {
		t.Fatalf("first record=%#v created=%#v", first, created)
	}
	state := deploy.StateV1{Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, document), Platform: platform, Overlay: lock.Overlay}
	missingAlias := first
	missingAlias.PortableRuntimeLayer = nil
	if err := operation.CommitValidatedBuildV1(missingAlias); err != nil {
		t.Fatal(err)
	}
	if _, found, err := LoadValidatedBuildCandidate(t.Context(), operation, fixture.store, document, state,
		deploy.EmptyPackageOverridesV1(document.Environment.ID), dir, false, false); err == nil || found {
		t.Fatalf("candidate missing portable alias found=%v err=%v", found, err)
	}
	if err := operation.CommitValidatedBuildV1(first); err != nil {
		t.Fatal(err)
	}
	failedPortableCleanup = first.PortableRuntimeLayer.ImageReference
	second, err := publishValidatedBuild(t.Context(), operation, fixture.store, document.Environment.ID, dir, lock, inputs, backend)
	if err != nil {
		t.Fatal(err)
	}
	if second.PortableRuntimeLayer == nil || second.PortableRuntimeLayer.ImageReference == first.PortableRuntimeLayer.ImageReference ||
		!reflect.DeepEqual(removed, []string{first.ImageReference, first.PortableRuntimeLayer.ImageReference}) ||
		len(second.PendingCleanup) != 1 || second.PendingCleanup[0].ImageReference != first.PortableRuntimeLayer.ImageReference {
		t.Fatalf("second record=%#v removed=%#v", second, removed)
	}
	failedPortableCleanup = ""
	second, cleanupErrors := cleanupPendingValidatedBuildReferences(
		t.Context(), operation, second, document.Environment.ID, dir, backend.removeReference,
	)
	if len(cleanupErrors) != 0 || len(second.PendingCleanup) != 0 || removed[2] != first.PortableRuntimeLayer.ImageReference {
		t.Fatalf("retry record=%#v removed=%#v errors=%v", second, removed, cleanupErrors)
	}
	if _, found, err := LoadValidatedBuildCandidate(t.Context(), operation, fixture.store, document, state,
		deploy.EmptyPackageOverridesV1(document.Environment.ID), dir, false, false); err != nil || !found {
		t.Fatalf("portable validated candidate found=%v err=%v", found, err)
	}
	failedPortableCleanup = second.ImageReference
	restored := []string{}
	restorePortable := func(_ context.Context, image providers.RealizedImageV1, generation, environment, deploymentDir string) error {
		if image != portableImage || generation != second.ImageReference || environment != document.Environment.ID || deploymentDir != dir {
			t.Fatalf("portable restore = %#v/%q/%q/%q", image, generation, environment, deploymentDir)
		}
		restored = append(restored, second.PortableRuntimeLayer.ImageReference)
		return nil
	}
	if _, err := discardValidatedBuild(t.Context(), operation, fixture.store, document.Environment.ID, dir, backend.removeReference, restorePortable); err == nil {
		t.Fatal("discard unexpectedly succeeded after final-reference removal failed")
	}
	if !reflect.DeepEqual(restored, []string{second.PortableRuntimeLayer.ImageReference}) {
		t.Fatalf("portable alias was not restored: %#v", restored)
	}
	partial, found, err := operation.ReadValidatedBuildV1()
	if err != nil || !found || partial.Discarded || partial.PortableRuntimeLayer == nil {
		t.Fatalf("partially discarded candidate: found=%t record=%#v error=%v", found, partial, err)
	}
	previousInspect := inspectPortableRuntimeLayerReferenceV1
	t.Cleanup(func() { inspectPortableRuntimeLayerReferenceV1 = previousInspect })
	inspectPortableRuntimeLayerReferenceV1 = func(_ context.Context, reference string, _ blueprint.Platform) (InspectedImageCandidate, error) {
		if len(restored) != 1 || reference != restored[0] {
			return InspectedImageCandidate{}, errors.New("portable alias missing after partial discard")
		}
		return InspectedImageCandidate{Image: portableImage}, nil
	}
	if err := verifyValidatedBuildPortableReferenceV1(t.Context(), lock, partial, document.Environment.ID, dir); err != nil {
		t.Fatalf("restored portable alias verification error = %v", err)
	}
	inspectPortableRuntimeLayerReferenceV1 = previousInspect
	failedPortableCleanup = ""
	if _, err := discardValidatedBuild(t.Context(), operation, fixture.store, document.Environment.ID, dir, backend.removeReference, restorePortable); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(removed[3:], []string{
		second.PortableRuntimeLayer.ImageReference, second.ImageReference,
		second.PortableRuntimeLayer.ImageReference, second.ImageReference,
	}) {
		t.Fatalf("discard removed=%#v", removed)
	}
}

func TestRemoveValidatedPortableReferenceRejectsRecordDigestMismatchBeforeDocker(t *testing.T) {
	dir := t.TempDir()
	image := providers.RealizedImageV1{
		Digest: rendererDigest("a"), ConfigDigest: rendererDigest("b"), RootFSSubject: rendererDigest("c"),
	}
	generation := fixedPublicationReferences(t, dir, 0x53).Generation
	reference, err := NewEnvironmentPortableRuntimeLayerReferenceForGeneration(rendererDigest("d"), generation, "demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := removeEnvironmentValidatedBuildReference(t.Context(), image, reference, "demo", dir); err == nil ||
		!strings.Contains(err.Error(), "does not match its recorded image") {
		t.Fatalf("mismatched portable cleanup error = %v", err)
	}
}

func TestVerifyValidatedBuildPortableReferenceRejectsMissingOrRetargetedAlias(t *testing.T) {
	dir := t.TempDir()
	platform := blueprint.Platform{OS: "linux", Architecture: "amd64", Canonical: "linux/amd64"}
	image := providers.RealizedImageV1{
		Digest: rendererDigest("a"), ConfigDigest: rendererDigest("b"), RootFSSubject: rendererDigest("c"),
	}
	generation := fixedPublicationReferences(t, dir, 0x54).Generation
	alias, err := NewEnvironmentPortableRuntimeLayerReferenceForGeneration(image.ConfigDigest, generation, "demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	lock := deploy.BuildLockV1{
		Platform:             platform,
		PortableRuntimeLayer: &deploy.PortableRuntimeLayerV1{Result: image},
	}
	record := deploy.ValidatedBuildV1{
		ImageReference: generation,
		PortableRuntimeLayer: &deploy.ValidatedBuildReferenceV1{
			Image: image, ImageReference: alias,
		},
	}
	for _, tc := range []struct {
		name string
		mode string
		want string
	}{
		{name: "stable"},
		{name: "missing", mode: "missing", want: "portable alias missing"},
		{name: "retargeted", mode: "retargeted", want: "no longer names its locked image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := inspectPortableRuntimeLayerReferenceV1
			t.Cleanup(func() { inspectPortableRuntimeLayerReferenceV1 = previous })
			inspections := 0
			inspectPortableRuntimeLayerReferenceV1 = func(
				_ context.Context, reference string, selected blueprint.Platform,
			) (InspectedImageCandidate, error) {
				inspections++
				if reference != alias || selected != platform {
					t.Fatalf("reference=%q platform=%#v", reference, selected)
				}
				if tc.mode == "missing" {
					return InspectedImageCandidate{}, errors.New("portable alias missing")
				}
				observed := image
				if tc.mode == "retargeted" {
					observed.ConfigDigest = rendererDigest("d")
				}
				return InspectedImageCandidate{Image: observed}, nil
			}
			err := verifyValidatedBuildPortableReferenceV1(t.Context(), lock, record, "demo", dir)
			if inspections != 1 || (tc.want == "" && err != nil) ||
				(tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want))) {
				t.Fatalf("inspections=%d error=%v", inspections, err)
			}
		})
	}
}
