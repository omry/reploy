package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestBuildAndAcceptMaterializationLayerStopsOnBuildPreflightFailure(t *testing.T) {
	store, request := materializationLayerFixture(t)
	transaction := request.Transaction
	transaction.Argv[2].RelativePath = transaction.Script.LogicalPath
	wheel := providerstore.ArtifactDescriptor{
		LogicalPath: "hydra.whl", Kind: "wheel", Size: "10", SHA256: rendererDigest("6"),
	}
	bundle := acceptanceBundle(transaction, request.Platform)
	bundle.Payload.Artifacts = []providerstore.ArtifactDescriptor{wheel, transaction.Script}
	preflightErr := errors.New("Python runtime root preflight failed")
	calls := []string{}
	result, err := buildAndAcceptMaterializationLayer(
		context.Background(), store, transaction, bundle, request.Platform,
		func(context.Context, MaterializationEvidenceInput) ([]providers.RealizedGeneratedExecutable, []providers.RealizedOutput, error) {
			calls = append(calls, "evidence")
			return nil, nil, nil
		}, nil, RunOptions{},
		func(providerstore.Store, MaterializationLayerRequest, RunOptions) (MaterializationLayerCandidate, error) {
			calls = append(calls, "build")
			return MaterializationLayerCandidate{}, preflightErr
		},
		func(context.Context, MaterializationLayerCandidate, MaterializationLayerRequest) (InspectedMaterializationLayerCandidate, error) {
			calls = append(calls, "inspect")
			return InspectedMaterializationLayerCandidate{}, errors.New("inspection ran after build preflight failure")
		},
		func(context.Context, BuiltImageCandidate, providers.RealizedImageV1) error {
			calls = append(calls, "retain")
			return errors.New("retention ran after build preflight failure")
		},
		func(context.Context, BuiltImageCandidate) error {
			calls = append(calls, "remove")
			return errors.New("cleanup ran without a built candidate")
		},
	)
	if !errors.Is(err, preflightErr) || !reflect.DeepEqual(result, providers.GraphNodeMaterializeResult{}) || !reflect.DeepEqual(calls, []string{"build"}) {
		t.Fatalf("result = %#v; calls = %#v; error = %v", result, calls, err)
	}
}
