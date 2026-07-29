package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func retainedProviderLayerFixture() providers.RealizedImageV1 {
	return providers.RealizedImageV1{
		Digest:        rendererDigest("7"),
		ConfigDigest:  rendererDigest("7"),
		RootFSSubject: rendererDigest("8"),
	}
}

func TestRetainVerifiedProviderLayerCreatesContentAddressedReference(t *testing.T) {
	image := retainedProviderLayerFixture()
	candidate := BuiltImageCandidate{
		ImageID: image.ConfigDigest, TemporaryReference: temporaryBuildReferencePrefix + "12345678:build-output",
	}
	reference := verifiedProviderLayerReference(image)
	calls := [][]string{}
	err := retainVerifiedProviderLayer(t.Context(), candidate, image, func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		switch len(calls) {
		case 1:
			return "", errors.New("Error response from daemon: No such image: " + reference)
		case 2:
			return "", nil
		case 3:
			return string(image.ConfigDigest) + "\n", nil
		case 4:
			return string(image.ConfigDigest) + "\n", nil
		case 5:
			return "", nil
		default:
			t.Fatalf("unexpected Docker call %d: %#v", len(calls), args)
			return "", nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"image", "inspect", "--format", "{{.Id}}", reference},
		{"image", "tag", string(image.ConfigDigest), reference},
		{"image", "inspect", "--format", "{{.Id}}", reference},
		{"image", "ls", "--quiet", "--no-trunc", candidate.TemporaryReference},
		{"image", "rm", candidate.TemporaryReference},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("Docker calls = %#v, want %#v", calls, want)
	}
}

func TestRetainVerifiedProviderLayerReusesExactReference(t *testing.T) {
	image := retainedProviderLayerFixture()
	candidate := BuiltImageCandidate{
		ImageID: image.ConfigDigest, TemporaryReference: temporaryBuildReferencePrefix + "12345678:build-output",
	}
	calls := 0
	err := retainVerifiedProviderLayer(t.Context(), candidate, image, func(_ context.Context, args ...string) (string, error) {
		calls++
		switch calls {
		case 1, 2:
			return string(image.ConfigDigest), nil
		case 3:
			return "", nil
		default:
			t.Fatalf("unexpected Docker call %d: %v", calls, args)
			return "", nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("Docker calls = %d, want 3", calls)
	}
}

func TestRetainVerifiedProviderLayerRejectsReferenceMismatch(t *testing.T) {
	image := retainedProviderLayerFixture()
	candidate := BuiltImageCandidate{
		ImageID: image.ConfigDigest, TemporaryReference: temporaryBuildReferencePrefix + "12345678:build-output",
	}
	err := retainVerifiedProviderLayer(t.Context(), candidate, image, func(_ context.Context, args ...string) (string, error) {
		return string(rendererDigest("9")), nil
	})
	if err == nil || !strings.Contains(err.Error(), "want "+string(image.ConfigDigest)) {
		t.Fatalf("mismatch error = %v", err)
	}
}

func TestBuildAndAcceptMaterializationLayerRemovesCandidateWhenRetentionFails(t *testing.T) {
	store, request := materializationLayerFixture(t)
	transaction := request.Transaction
	transaction.Argv[2].RelativePath = transaction.Script.LogicalPath
	bundle := acceptanceBundle(transaction, request.Platform)
	bundle.Payload.Artifacts = []providerstore.ArtifactDescriptor{
		{
			LogicalPath: "hydra.whl",
			Kind:        "wheel",
			Size:        "10",
			SHA256:      rendererDigest("6"),
		},
		transaction.Script,
	}
	wantCandidate := BuiltImageCandidate{ImageID: rendererDigest("7")}
	removed := false

	result, err := buildAndAcceptMaterializationLayer(
		t.Context(), store, transaction, bundle, request.Platform,
		func(context.Context, MaterializationEvidenceInput) ([]providers.RealizedGeneratedExecutable, []providers.RealizedOutput, error) {
			return []providers.RealizedGeneratedExecutable{acceptedGeneratedExecutable(transaction)}, []providers.RealizedOutput{}, nil
		},
		nil,
		RunOptions{},
		func(providerstore.Store, MaterializationLayerRequest, RunOptions) (MaterializationLayerCandidate, error) {
			key, digest, keyErr := MaterializationAssemblyKey(transaction, request.Platform)
			return MaterializationLayerCandidate{
				Built: wantCandidate, AssemblyKey: key, AssemblyKeyDigest: digest,
			}, keyErr
		},
		func(context.Context, MaterializationLayerCandidate, MaterializationLayerRequest) (InspectedMaterializationLayerCandidate, error) {
			return acceptedMaterializationCandidate(t, transaction, request.Platform), nil
		},
		func(context.Context, BuiltImageCandidate, providers.RealizedImageV1) error {
			return errors.New("tag failed")
		},
		func(_ context.Context, candidate BuiltImageCandidate) error {
			removed = candidate == wantCandidate
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "retain verified provider layer: tag failed") ||
		!removed || !reflect.DeepEqual(result, providers.GraphNodeMaterializeResult{}) {
		t.Fatalf("result = %#v; removed = %t; error = %v", result, removed, err)
	}
}
