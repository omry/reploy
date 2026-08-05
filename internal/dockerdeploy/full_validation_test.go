package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func fullValidationInput(t *testing.T, digestChar string) FullImageValidationInput {
	t.Helper()
	_, request := finalizationBuildFixture(t)
	imageID := rendererDigest(digestChar)
	request.Source.Image.Digest = imageID
	request.Source.Image.ConfigDigest = imageID
	request.Source.Descriptor.AuthorReference = string(imageID)
	request.Source.Descriptor.ImmutableReference = string(imageID)
	request.Source.Descriptor.ConfigDigest = imageID
	return FullImageValidationInput{
		Image: request.Source, Profiles: []providers.RequirementProfile{}, Outputs: []providers.RealizedOutput{},
		RuntimePolicy: deploy.RuntimePolicyV1{
			Schema: deploy.RuntimePolicySchemaV1, StartupVerifier: deploy.ApplicationStartupVerifierContractV1(),
			ProtectedPaths: []deploy.ProtectedPathV1{}, Plans: []deploy.RuntimePlanV1{},
		},
	}
}

func acceptFullValidationProfile(providers.RequirementProfile) error { return nil }

func TestValidateBuildImagesValidatesLayersOnceAndReusesFinalEvidence(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	layers := []FullImageValidationInput{fullValidationInput(t, "7"), fullValidationInput(t, "8")}
	final := layers[len(layers)-1]
	calls := []canonical.Digest{}
	run := func(_ context.Context, input FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
		calls = append(calls, input.Image.Image.Digest)
		return []providers.ValidationEvidence{}, []providers.ExecutableEvidence{}, nil
	}
	result, err := ValidateBuildImages(context.Background(), store, layers, final, acceptFullValidationProfile, run)
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []canonical.Digest{layers[0].Image.Image.Digest, layers[1].Image.Image.Digest}
	if !reflect.DeepEqual(calls, wantCalls) || len(result.Layers) != len(layers) ||
		!reflect.DeepEqual(result.Final, result.Layers[len(result.Layers)-1]) ||
		result.Final.Reference.Kind != providerstore.ValidationRecordKind {
		t.Fatalf("calls = %v, result = %#v", calls, result)
	}
}

func TestValidateBuildImagesStopsAtFailedLayerWithoutFinalValidation(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	layer := fullValidationInput(t, "a")
	calls := 0
	_, err = ValidateBuildImages(context.Background(), store, []FullImageValidationInput{layer}, layer, acceptFullValidationProfile, func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
		calls++
		return nil, nil, errors.New("invalid layer")
	})
	if err == nil || !strings.Contains(err.Error(), "component layer 1") || calls != 1 {
		t.Fatalf("calls = %d, error = %v", calls, err)
	}
}

func TestValidateBuildImagesValidatesDistinctApplicationRuntimeImage(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	layer := fullValidationInput(t, "a")
	final := fullValidationInput(t, "b")
	calls := 0
	_, err = ValidateBuildImages(context.Background(), store, []FullImageValidationInput{layer}, final, acceptFullValidationProfile, func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
		calls++
		return []providers.ValidationEvidence{}, []providers.ExecutableEvidence{}, nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("calls = %d, error = %v", calls, err)
	}
}

func TestValidateAndPublishImageRejectsIncompleteEvidence(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	input := fullValidationInput(t, "c")
	input.Profiles = []providers.RequirementProfile{{
		Schema: providers.RequirementProfileSchemaV1, Provider: blueprint.ComponentTypePython,
		Declaration: providers.RequirementDeclaration{
			Executables: []providers.ExecutableRequirement{}, Files: []providers.FileRequirement{},
			ProviderData: canonical.Envelope{Schema: "empty-requirements-v1", Value: canonical.Object{}},
		},
		SelectedExecutables: []providers.ExecutableEvidence{}, SelectedFiles: []providers.FileEvidence{},
		Platform: input.Image.Descriptor.Platform, Facts: canonical.Envelope{Schema: "empty-facts-v1", Value: canonical.Object{}},
	}}
	if _, err := ValidateAndPublishImage(context.Background(), store, input, acceptFullValidationProfile, func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
		return []providers.ValidationEvidence{}, []providers.ExecutableEvidence{}, nil
	}); err == nil || !strings.Contains(err.Error(), "returned 0 profile records, want 1") {
		t.Fatalf("profile error = %v", err)
	}
}

func TestValidateImageReturnsEvidenceWithoutPublishing(t *testing.T) {
	input := fullValidationInput(t, "d")
	record, err := ValidateImage(
		t.Context(),
		input,
		acceptFullValidationProfile,
		func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
			return []providers.ValidationEvidence{}, []providers.ExecutableEvidence{}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if record.SubjectRootFS != input.Image.Image.RootFSSubject ||
		len(record.Profiles) != 0 ||
		len(record.ExposedOutputs) != 0 {
		t.Fatalf("validation record = %#v", record)
	}
}
