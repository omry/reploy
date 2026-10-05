package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
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

func TestLoadValidatedBuildCandidateSkipsStaleProviderProfileV1(t *testing.T) {
	dir, operation, store, _, state := currentBuildFixture(t, true)
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
	old := newPreparedPythonGraphReuseFixture(t).lock
	old.BlueprintDigest = inputs.BlueprintDigest
	old.PackageOverrides = deploy.EmptyPackageOverrideIntentV1(document.Environment.ID)
	old.Nodes[0].RequirementProfile.Facts.Schema = "legacy-python-profile"
	old.Nodes[0].ValidationEvidence.ProfileDigest, err = providers.RequirementProfileDigest(old.Nodes[0].RequirementProfile, acceptProviderProfileOwnerForCutoverV1)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := operation.PublishBuildLock(old, acceptProviderProfileOwnerForCutoverV1)
	if err != nil {
		t.Fatal(err)
	}
	record := deploy.ValidatedBuildV1{
		Schema: deploy.ValidatedBuildSchemaV1, BlueprintDigest: inputs.BlueprintDigest,
		OverlayDigest: inputs.OverlayDigest, PackageOverridesDigest: inputs.PackageOverridesDigest,
		Platform: inputs.Platform, BuildLockDigest: digest, Image: old.FinalImage,
		ImageReference: state.Current.Reference,
	}
	if err := operation.CommitValidatedBuildV1(record); err != nil {
		t.Fatal(err)
	}
	before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
	changed := deploy.EmptyPackageOverridesV1(document.Environment.ID)
	changed.Environment.PackageOverrides["python"] = map[string]deploy.PackageOverrideChoiceV1{"demo": {Version: "2"}}
	if _, found, err := LoadValidatedBuildCandidate(t.Context(), operation, store, document, state, changed, dir, false, false); err != nil || found {
		t.Fatalf("stale legacy candidate found=%v err=%v", found, err)
	}
	if _, found, err := LoadValidatedBuildCandidate(t.Context(), operation, store, document, state, overrides, dir, false, false); err == nil || found {
		t.Fatalf("matching legacy candidate was not strictly rejected: found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(before, pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())) {
		t.Fatal("candidate reads changed retained lock, state or storage")
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
				return fixedPublicationReferences(t, dir, 31), nil
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
	if created != 1 || record.ImageReference != fixedPublicationReferences(t, dir, 31).Generation {
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

func TestPublishValidatedBuildRetainsIntentAfterTagFailure(t *testing.T) {
	dir, operation, store, lock, state := currentBuildFixture(t, true)
	defer operation.Unlock()
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := ValidatedBuildInputs(document, state.Overlay, deploy.EmptyPackageOverridesV1(document.Environment.ID), dir, state.Platform)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("tag failed after creation")
	removed := false
	backend := publishValidatedBuildBackendV1{
		newReferences: func(string, string) (EnvironmentImageReferences, error) {
			return fixedPublicationReferences(t, dir, 31), nil
		},
		createReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
			return failure
		},
		removeReference: func(context.Context, providers.RealizedImageV1, string, string, string) error {
			removed = true
			return nil
		},
		verifyReference: func(context.Context, providers.RealizedImageV1, string, string, string) error {
			t.Fatal("rollback must not verify an absent candidate")
			return nil
		},
	}
	_, err = publishValidatedBuild(t.Context(), operation, store, "demo", dir, lock, inputs, backend)
	if !errors.Is(err, failure) || removed {
		t.Fatalf("removed=%v error=%v", removed, err)
	}
	intent, found, err := operation.ReadPendingValidatedBuildV1()
	if err != nil || !found || intent.Candidate.ImageReference != fixedPublicationReferences(t, dir, 31).Generation {
		t.Fatalf("intent=%#v found=%v error=%v", intent, found, err)
	}
	if _, found, err := operation.ReadValidatedBuildV1(); err != nil || found {
		t.Fatalf("uncommitted candidate selected: %v %v", found, err)
	}
	recovered, err := recoverPendingValidatedPublicationV1(t.Context(), operation, store, "demo", dir, backend)
	if err != nil || !recovered || !removed {
		t.Fatalf("recovery=%v removed=%v error=%v", recovered, removed, err)
	}
	if _, found, err := operation.ReadPendingValidatedBuildV1(); err != nil || found {
		t.Fatalf("intent remained: %v %v", found, err)
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
		Image: oldImage, ImageReference: fixedPublicationReferences(t, dir, 31).Generation,
	}
	if err := operation.CommitValidatedBuildV1(old); err != nil {
		t.Fatal(err)
	}
	created := false
	_, err = publishValidatedBuild(
		t.Context(), operation, store, document.Environment.ID, dir, lock, inputs,
		publishValidatedBuildBackendV1{
			newReferences: func(string, string) (EnvironmentImageReferences, error) {
				return fixedPublicationReferences(t, dir, 31), nil
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
		Image: lock.FinalImage, ImageReference: fixedPublicationReferences(t, dir, 32).Generation,
	}
	if err := operation.CommitValidatedBuildV1(old); err != nil {
		t.Fatal(err)
	}
	cleanupFailure := errors.New("Docker is busy")
	record, err := publishValidatedBuild(
		t.Context(), operation, store, document.Environment.ID, dir, lock, inputs,
		publishValidatedBuildBackendV1{
			newReferences: func(string, string) (EnvironmentImageReferences, error) {
				return fixedPublicationReferences(t, dir, 31), nil
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
		ImageReference: fixedPublicationReferences(t, dir, 32).Generation,
	}
	if err := operation.CommitValidatedBuildV1(oldRecord); err != nil {
		t.Fatal(err)
	}
	candidate := validatedBuildStorageVariant(t, store, current, "a", "b")
	record, err := publishValidatedBuild(
		t.Context(), operation, store, document.Environment.ID, dir, candidate, inputs,
		publishValidatedBuildBackendV1{
			newReferences: func(string, string) (EnvironmentImageReferences, error) {
				return fixedPublicationReferences(t, dir, 31), nil
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
				return fixedPublicationReferences(t, dir, 31), nil
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
		ImageReference: fixedPublicationReferences(t, dir, 41).Generation,
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
	pending, err := discardValidatedBuild(
		t.Context(), operation, store, document.Environment.ID, dir, removeReference,
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
		t.Context(), operation, store, document.Environment.ID, dir, removeReference,
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

func TestDiscardValidatedBuildClosesEditorAdmissionBeforeAliasRemoval(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprintf("after-effect=%v", after), func(t *testing.T) {
			dir, operation, store, current, state := currentBuildFixture(t, true)
			defer func() { _ = operation.Unlock() }()
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
			candidate := validatedBuildStorageVariant(t, store, current, "7", "8")
			digest, err := operation.PublishBuildLock(candidate, registry.ValidateRequirementProfileV1)
			if err != nil {
				t.Fatal(err)
			}
			record := deploy.ValidatedBuildV1{
				Schema: deploy.ValidatedBuildSchemaV1, BlueprintDigest: inputs.BlueprintDigest,
				OverlayDigest: inputs.OverlayDigest, PackageOverridesDigest: inputs.PackageOverridesDigest,
				Platform: inputs.Platform, BuildLockDigest: digest, Image: candidate.FinalImage,
				ImageReference: fixedPublicationReferences(t, dir, 41).Generation,
			}
			if err := operation.CommitValidatedBuildV1(record); err != nil {
				t.Fatal(err)
			}
			if _, found, err := LoadValidatedBuildCandidate(t.Context(), operation, store, document, state, overrides, dir, false, false); err != nil || !found {
				t.Fatalf("initial offline candidate found=%v err=%v", found, err)
			}
			removed := false
			guardObserved := false
			backend := validatedRetirementBackendV1{
				removeReference: func(_ context.Context, image providers.RealizedImageV1, reference, _, _ string) error {
					guard, found, err := operation.ReadValidatedBuildV1()
					if err != nil || !found {
						t.Fatalf("discard guard found=%v err=%v", found, err)
					}
					guardObserved = guard.Discarded && guard.PendingStorageCleanup && len(guard.PendingCleanup) == 1 && guard.PendingCleanup[0].ImageReference == record.ImageReference
					if image != record.Image || reference != record.ImageReference {
						t.Fatal("retirement targeted another owner")
					}
					if after {
						removed = true
					}
					return errPendingPublicationFaultV1
				},
			}
			if _, _, err := discardValidatedBuildWithBackendV1(t.Context(), operation, document.Environment.ID, dir, backend); !errors.Is(err, errPendingPublicationFaultV1) {
				t.Fatalf("discard error=%v", err)
			}
			if err := operation.Unlock(); err != nil {
				t.Fatal(err)
			}
			status, err := InspectStagedOverrideValidation(t.Context(), dir)
			if err != nil || status.Validated {
				t.Fatalf("editor accepted interrupted discard: status=%#v err=%v", status, err)
			}
			operation, err = deploy.AcquireExistingOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			retained, found, err := operation.ReadValidatedBuildV1()
			if err != nil || !found || !guardObserved || !retained.Discarded || len(retained.PendingCleanup) != 1 || removed != after {
				t.Fatalf("discard did not retain guard and exact retry inventory: %#v found=%v guard=%v removed=%v err=%v", retained, found, guardObserved, removed, err)
			}
			if _, found, err := operation.ReadBuildLock(digest, registry.ValidateRequirementProfileV1); err != nil || !found {
				t.Fatalf("pending reference lost its lock: found=%v err=%v", found, err)
			}
			backend.removeReference = func(_ context.Context, image providers.RealizedImageV1, reference, _, _ string) error {
				if image != record.Image || reference != record.ImageReference {
					t.Fatal("retry targeted another owner")
				}
				removed = true
				return nil
			}
			retired, found, err := discardValidatedBuildWithBackendV1(t.Context(), operation, document.Environment.ID, dir, backend)
			if err != nil || !found || !retired.Discarded || len(retired.PendingCleanup) != 0 || !removed {
				t.Fatalf("restart retirement=%#v found=%v removed=%v err=%v", retired, found, removed, err)
			}
			if err := cleanupValidatedBuildStorage(operation, store, nil); err != nil {
				t.Fatal(err)
			}
			if _, found, err := operation.ReadBuildLock(state.Current.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil || !found {
				t.Fatalf("independent current owner lost its lock: found=%v err=%v", found, err)
			}
		})
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

func TestDiscardValidatedBuildPreservesLiveOwnershipWithoutBuildLock(t *testing.T) {
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
		ImageReference: fixedPublicationReferences(t, dir, 42).Generation,
		PendingCleanup: []deploy.ValidatedBuildReferenceV1{{
			Image: image, ImageReference: fixedPublicationReferences(t, dir, 43).Generation,
		}},
	}
	if err := operation.CommitValidatedBuildV1(record); err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".reploy", "locks"), 0o700); err != nil {
		t.Fatal(err)
	}
	beforeRecord, found, err := operation.ReadValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(beforeRecord, record) {
		t.Fatalf("validated build before discard = %#v, found=%v err=%v", beforeRecord, found, err)
	}
	before := pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())
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
	)
	if err == nil || !strings.Contains(err.Error(), "validated build lock") || !strings.Contains(err.Error(), "is missing") {
		t.Fatalf("discard without its live owner lock pending=%v err=%v", pending, err)
	}
	if pending {
		t.Fatal("missing-lock rejection reported a pending discard")
	}
	if len(removed) != 0 {
		t.Fatalf("removed references before rejecting the missing lock: %#v", removed)
	}
	afterRecord, found, err := operation.ReadValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(afterRecord, beforeRecord) {
		t.Fatalf("validated build after discard = %#v, found=%v err=%v", afterRecord, found, err)
	}
	if !reflect.DeepEqual(afterRecord.PendingCleanup, beforeRecord.PendingCleanup) {
		t.Fatalf("pending cleanup changed from %#v to %#v", beforeRecord.PendingCleanup, afterRecord.PendingCleanup)
	}
	if !reflect.DeepEqual(before, pendingOwnedFilesystemSnapshotV1(t, dir, store.Root())) {
		t.Fatal("missing-lock rejection mutated metadata, lock files, or provider-store contents")
	}
}
