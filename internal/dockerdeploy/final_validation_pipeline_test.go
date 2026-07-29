package dockerdeploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestValidateAndFinalizeBuildUsesPublishedFinalEvidence(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	final := fullValidationInput(t, "d")
	validated := false
	built := false
	result, err := validateAndFinalizeBuild(
		context.Background(), store, []FullImageValidationInput{}, final, acceptFullValidationProfile,
		func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
			validated = true
			return []providers.ValidationEvidence{}, []providers.ExecutableEvidence{}, nil
		}, RunOptions{},
		func(_ providerstore.Store, request FinalizationBuildRequest, _ RunOptions) (BuiltImageCandidate, error) {
			if !validated || request.Source.Image != final.Image.Image || request.ValidationReference.Kind != providerstore.ValidationRecordKind {
				t.Fatalf("finalization request = %#v", request)
			}
			built = true
			return BuiltImageCandidate{ImageID: rendererDigest("e")}, nil
		},
		func(_ context.Context, candidate BuiltImageCandidate, request FinalizationBuildRequest) (InspectedImageCandidate, error) {
			if !built || candidate.ImageID != rendererDigest("e") || request.ValidationReference.Kind != providerstore.ValidationRecordKind {
				t.Fatalf("candidate = %#v, request = %#v", candidate, request)
			}
			image := final.Image
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
	_, err = validateAndFinalizeBuild(
		context.Background(), store, []FullImageValidationInput{}, fullValidationInput(t, "f"), acceptFullValidationProfile,
		func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
			return nil, nil, errors.New("validation failed")
		}, RunOptions{},
		func(providerstore.Store, FinalizationBuildRequest, RunOptions) (BuiltImageCandidate, error) {
			built = true
			return BuiltImageCandidate{}, nil
		},
		func(context.Context, BuiltImageCandidate, FinalizationBuildRequest) (InspectedImageCandidate, error) {
			return InspectedImageCandidate{}, nil
		},
		func(context.Context, BuiltImageCandidate) error {
			t.Fatal("validation failure attempted candidate cleanup")
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "validation failed") || built {
		t.Fatalf("built = %v, error = %v", built, err)
	}
}

func TestValidateAndFinalizeBuildRemovesCandidateAfterInspectionFailure(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	final := fullValidationInput(t, "7")
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
		RunOptions{},
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
