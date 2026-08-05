package dockerdeploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestValidateAndFinalizeBuildUsesPublishedFinalEvidence(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	final := fullValidationInput(t, "d")
	verifier, runtimeCandidate, runtimeImage := finalValidationRuntimeFixture(t, final)
	validated := false
	built := false
	result, err := validateAndFinalizeBuild(
		context.Background(), store, []FullImageValidationInput{}, final, acceptFullValidationProfile,
		func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
			validated = true
			return []providers.ValidationEvidence{}, []providers.ExecutableEvidence{}, nil
		}, verifier, RunOptions{},
		func(providerstore.Store, ApplicationRuntimeLayerBuildRequest, RunOptions) (BuiltImageCandidate, error) {
			return runtimeCandidate, nil
		},
		func(context.Context, BuiltImageCandidate, ApplicationRuntimeLayerBuildRequest) (InspectedImageCandidate, error) {
			return runtimeImage, nil
		},
		func(context.Context, BuiltImageCandidate, providers.RealizedImageV1) error { return nil },
		func(_ providerstore.Store, request FinalizationBuildRequest, _ RunOptions) (BuiltImageCandidate, error) {
			if !validated || request.Source.Image != runtimeImage.Image || request.ValidationReference.Kind != providerstore.ValidationRecordKind {
				t.Fatalf("finalization request = %#v", request)
			}
			built = true
			return BuiltImageCandidate{ImageID: rendererDigest("e")}, nil
		},
		func(_ context.Context, candidate BuiltImageCandidate, request FinalizationBuildRequest) (InspectedImageCandidate, error) {
			if !built || candidate.ImageID != rendererDigest("e") || request.ValidationReference.Kind != providerstore.ValidationRecordKind {
				t.Fatalf("candidate = %#v, request = %#v", candidate, request)
			}
			image := runtimeImage
			image.Image.Digest = candidate.ImageID
			image.Image.ConfigDigest = candidate.ImageID
			image.Descriptor.AuthorReference = string(candidate.ImageID)
			image.Descriptor.ImmutableReference = string(candidate.ImageID)
			image.Descriptor.ConfigDigest = candidate.ImageID
			return image, nil
		},
		func(context.Context, BuiltImageCandidate) error {
			t.Fatal("successful inspection removed its candidate inside the validation pipeline")
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !validated || !built || result.Image.Image.Digest != rendererDigest("e") || result.Validation.Final.Reference.Kind != providerstore.ValidationRecordKind {
		t.Fatalf("result = %#v", result)
	}
}

func TestValidateAndFinalizeBuildDoesNotBuildAfterValidationFailure(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	built := false
	final := fullValidationInput(t, "f")
	verifier, runtimeCandidate, runtimeImage := finalValidationRuntimeFixture(t, final)
	removedRuntime := false
	_, err = validateAndFinalizeBuild(
		context.Background(), store, []FullImageValidationInput{}, final, acceptFullValidationProfile,
		func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
			return nil, nil, errors.New("validation failed")
		}, verifier, RunOptions{},
		func(providerstore.Store, ApplicationRuntimeLayerBuildRequest, RunOptions) (BuiltImageCandidate, error) {
			return runtimeCandidate, nil
		},
		func(context.Context, BuiltImageCandidate, ApplicationRuntimeLayerBuildRequest) (InspectedImageCandidate, error) {
			return runtimeImage, nil
		},
		func(context.Context, BuiltImageCandidate, providers.RealizedImageV1) error { return nil },
		func(providerstore.Store, FinalizationBuildRequest, RunOptions) (BuiltImageCandidate, error) {
			built = true
			return BuiltImageCandidate{}, nil
		},
		func(context.Context, BuiltImageCandidate, FinalizationBuildRequest) (InspectedImageCandidate, error) {
			return InspectedImageCandidate{}, nil
		},
		func(_ context.Context, got BuiltImageCandidate) error {
			removedRuntime = got == runtimeCandidate
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "validation failed") || built || !removedRuntime {
		t.Fatalf("built = %v removed runtime=%v error = %v", built, removedRuntime, err)
	}
}

func TestValidateAndFinalizeBuildRemovesRuntimeCandidateAfterInspectionFailure(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	final := fullValidationInput(t, "6")
	verifier, runtimeCandidate, _ := finalValidationRuntimeFixture(t, final)
	want := errors.New("runtime inspection failed")
	validated := false
	finalized := false
	removed := false
	_, err = validateAndFinalizeBuild(
		t.Context(), store, []FullImageValidationInput{}, final,
		acceptFullValidationProfile,
		func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
			validated = true
			return nil, nil, nil
		},
		verifier, RunOptions{},
		func(providerstore.Store, ApplicationRuntimeLayerBuildRequest, RunOptions) (BuiltImageCandidate, error) {
			return runtimeCandidate, nil
		},
		func(context.Context, BuiltImageCandidate, ApplicationRuntimeLayerBuildRequest) (InspectedImageCandidate, error) {
			return InspectedImageCandidate{}, want
		},
		func(context.Context, BuiltImageCandidate, providers.RealizedImageV1) error {
			t.Fatal("failed runtime inspection retained its candidate")
			return nil
		},
		func(providerstore.Store, FinalizationBuildRequest, RunOptions) (BuiltImageCandidate, error) {
			finalized = true
			return BuiltImageCandidate{}, nil
		},
		func(context.Context, BuiltImageCandidate, FinalizationBuildRequest) (InspectedImageCandidate, error) {
			return InspectedImageCandidate{}, nil
		},
		func(cleanupContext context.Context, got BuiltImageCandidate) error {
			removed = cleanupContext.Err() == nil && got == runtimeCandidate
			return nil
		},
	)
	if !errors.Is(err, want) || validated || finalized || !removed {
		t.Fatalf("validated = %v finalized = %v removed = %v error = %v", validated, finalized, removed, err)
	}
}

func TestValidateAndFinalizeBuildRemovesRuntimeCandidateAfterRetentionFailure(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	final := fullValidationInput(t, "5")
	verifier, runtimeCandidate, runtimeImage := finalValidationRuntimeFixture(t, final)
	want := errors.New("runtime retention failed")
	finalized := false
	removed := false
	_, err = validateAndFinalizeBuild(
		t.Context(), store, []FullImageValidationInput{}, final,
		acceptFullValidationProfile,
		func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
			return []providers.ValidationEvidence{}, []providers.ExecutableEvidence{}, nil
		},
		verifier, RunOptions{},
		func(providerstore.Store, ApplicationRuntimeLayerBuildRequest, RunOptions) (BuiltImageCandidate, error) {
			return runtimeCandidate, nil
		},
		func(context.Context, BuiltImageCandidate, ApplicationRuntimeLayerBuildRequest) (InspectedImageCandidate, error) {
			return runtimeImage, nil
		},
		func(context.Context, BuiltImageCandidate, providers.RealizedImageV1) error {
			return want
		},
		func(providerstore.Store, FinalizationBuildRequest, RunOptions) (BuiltImageCandidate, error) {
			finalized = true
			return BuiltImageCandidate{}, nil
		},
		func(context.Context, BuiltImageCandidate, FinalizationBuildRequest) (InspectedImageCandidate, error) {
			return InspectedImageCandidate{}, nil
		},
		func(cleanupContext context.Context, got BuiltImageCandidate) error {
			removed = cleanupContext.Err() == nil && got == runtimeCandidate
			return nil
		},
	)
	if !errors.Is(err, want) || finalized || !removed {
		t.Fatalf("finalized = %v removed = %v error = %v", finalized, removed, err)
	}
}

func TestValidateAndFinalizeBuildRemovesCandidateAfterInspectionFailure(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	final := fullValidationInput(t, "7")
	verifier, runtimeCandidate, runtimeImage := finalValidationRuntimeFixture(t, final)
	candidate := BuiltImageCandidate{
		ImageID:            rendererDigest("8"),
		TemporaryReference: temporaryBuildReferencePrefix + "12345678:finalize-output",
	}
	want := errors.New("inspection failed")
	removed := false
	_, err = validateAndFinalizeBuild(
		t.Context(), store, []FullImageValidationInput{}, final,
		acceptFullValidationProfile,
		func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
			return []providers.ValidationEvidence{}, []providers.ExecutableEvidence{}, nil
		},
		verifier, RunOptions{},
		func(providerstore.Store, ApplicationRuntimeLayerBuildRequest, RunOptions) (BuiltImageCandidate, error) {
			return runtimeCandidate, nil
		},
		func(context.Context, BuiltImageCandidate, ApplicationRuntimeLayerBuildRequest) (InspectedImageCandidate, error) {
			return runtimeImage, nil
		},
		func(context.Context, BuiltImageCandidate, providers.RealizedImageV1) error { return nil },
		func(providerstore.Store, FinalizationBuildRequest, RunOptions) (BuiltImageCandidate, error) {
			return candidate, nil
		},
		func(context.Context, BuiltImageCandidate, FinalizationBuildRequest) (InspectedImageCandidate, error) {
			return InspectedImageCandidate{}, want
		},
		func(cleanupContext context.Context, got BuiltImageCandidate) error {
			removed = cleanupContext.Err() == nil && got == candidate
			return nil
		},
	)
	if !errors.Is(err, want) || !removed {
		t.Fatalf("error = %v; removed = %t", err, removed)
	}
}

func finalValidationRuntimeFixture(
	t *testing.T,
	final FullImageValidationInput,
) (deploy.ApplicationStartupVerifierV1, BuiltImageCandidate, InspectedImageCandidate) {
	t.Helper()
	verifier := deploy.ApplicationStartupVerifierContractV1()
	verifier.Artifact = rendererDigest("a")
	verifier.Size = "123"
	candidate := BuiltImageCandidate{ImageID: rendererDigest("b")}
	image := final.Image
	image.Descriptor.RootFSDiffIDs = append(append([]canonical.Digest{}, image.Descriptor.RootFSDiffIDs...), rendererDigest("c"))
	rootFS, err := deploy.RootFSSubject(image.Descriptor.RootFSDiffIDs)
	if err != nil {
		t.Fatal(err)
	}
	image.Descriptor.AuthorReference = string(candidate.ImageID)
	image.Descriptor.ImmutableReference = string(candidate.ImageID)
	image.Descriptor.ConfigDigest = candidate.ImageID
	image.Image = providers.RealizedImageV1{Digest: candidate.ImageID, ConfigDigest: candidate.ImageID, RootFSSubject: rootFS}
	return verifier, candidate, image
}
