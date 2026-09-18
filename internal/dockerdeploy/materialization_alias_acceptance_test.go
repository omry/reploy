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

func TestMaterializationAliasFinalizerRunsAfterGeneratedEvidenceAndBeforeRetention(t *testing.T) {
	store, request := materializationLayerFixture(t)
	transaction := request.Transaction
	transaction.Argv[2].RelativePath = transaction.Script.LogicalPath
	bundle := acceptanceBundle(transaction, request.Platform)
	wheel := providerstore.ArtifactDescriptor{LogicalPath: "hydra.whl", Kind: "wheel", Size: "10", SHA256: rendererDigest("6")}
	bundle.Payload.Artifacts = []providerstore.ArtifactDescriptor{wheel, transaction.Script}
	base := BuiltImageCandidate{ImageID: rendererDigest("7")}
	alias := BuiltImageCandidate{ImageID: rendererDigest("9")}
	order := []string{}
	result, err := buildAndAcceptMaterializationLayerWithFinalizer(
		context.Background(), store, transaction, bundle, request.Platform,
		func(context.Context, MaterializationEvidenceInput) ([]providers.RealizedGeneratedExecutable, []providers.RealizedOutput, error) {
			order = append(order, "generated-evidence")
			return []providers.RealizedGeneratedExecutable{acceptedGeneratedExecutable(transaction)}, []providers.RealizedOutput{}, nil
		}, nil, RunOptions{},
		func(providerstore.Store, MaterializationLayerRequest, RunOptions) (MaterializationLayerCandidate, error) {
			order = append(order, "build")
			candidate := acceptedMaterializationCandidate(t, transaction, request.Platform)
			return MaterializationLayerCandidate{Built: base, AssemblyKey: candidate.AssemblyKey, AssemblyKeyDigest: candidate.AssemblyKeyDigest}, nil
		},
		func(context.Context, MaterializationLayerCandidate, MaterializationLayerRequest) (InspectedMaterializationLayerCandidate, error) {
			order = append(order, "inspect")
			return acceptedMaterializationCandidate(t, transaction, request.Platform), nil
		},
		func(_ context.Context, candidate BuiltImageCandidate, image providers.RealizedImageV1) error {
			order = append(order, "retain")
			if candidate != alias || image.ConfigDigest != alias.ImageID {
				return errors.New("retention did not receive final alias image")
			}
			return nil
		},
		func(_ context.Context, candidate BuiltImageCandidate) error {
			if candidate != base {
				return errors.New("unexpected cleanup candidate")
			}
			order = append(order, "remove-base")
			return nil
		},
		func(_ context.Context, source InspectedImageCandidate, accepted providers.GraphNodeMaterializeResult, _ RunOptions) (BuiltImageCandidate, InspectedImageCandidate, error) {
			order = append(order, "publish-alias")
			if source.Image.ConfigDigest != base.ImageID || len(accepted.GeneratedExecutables) != 1 {
				return BuiltImageCandidate{}, InspectedImageCandidate{}, errors.New("alias ran before accepted generated evidence")
			}
			image := source
			image.Image.ConfigDigest = alias.ImageID
			image.Image.Digest = alias.ImageID
			return alias, image, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"build", "inspect", "generated-evidence", "publish-alias", "remove-base", "retain"}
	if !reflect.DeepEqual(order, want) || result.Image.ConfigDigest != alias.ImageID {
		t.Fatalf("order = %#v, result image = %#v", order, result.Image)
	}
}

func TestMaterializationAliasFinalizerFailureRollsBackBaseCandidate(t *testing.T) {
	store, request := materializationLayerFixture(t)
	transaction := request.Transaction
	transaction.Argv[2].RelativePath = transaction.Script.LogicalPath
	bundle := acceptanceBundle(transaction, request.Platform)
	wheel := providerstore.ArtifactDescriptor{LogicalPath: "hydra.whl", Kind: "wheel", Size: "10", SHA256: rendererDigest("6")}
	bundle.Payload.Artifacts = []providerstore.ArtifactDescriptor{wheel, transaction.Script}
	base := BuiltImageCandidate{ImageID: rendererDigest("7")}
	alias := BuiltImageCandidate{ImageID: rendererDigest("9")}
	removed := []BuiltImageCandidate{}
	result, err := buildAndAcceptMaterializationLayerWithFinalizer(
		context.Background(), store, transaction, bundle, request.Platform,
		func(context.Context, MaterializationEvidenceInput) ([]providers.RealizedGeneratedExecutable, []providers.RealizedOutput, error) {
			return []providers.RealizedGeneratedExecutable{acceptedGeneratedExecutable(transaction)}, []providers.RealizedOutput{}, nil
		}, nil, RunOptions{},
		func(providerstore.Store, MaterializationLayerRequest, RunOptions) (MaterializationLayerCandidate, error) {
			candidate := acceptedMaterializationCandidate(t, transaction, request.Platform)
			return MaterializationLayerCandidate{Built: base, AssemblyKey: candidate.AssemblyKey, AssemblyKeyDigest: candidate.AssemblyKeyDigest}, nil
		},
		func(context.Context, MaterializationLayerCandidate, MaterializationLayerRequest) (InspectedMaterializationLayerCandidate, error) {
			return acceptedMaterializationCandidate(t, transaction, request.Platform), nil
		},
		func(context.Context, BuiltImageCandidate, providers.RealizedImageV1) error {
			t.Fatal("failed alias was retained")
			return nil
		},
		func(ctx context.Context, candidate BuiltImageCandidate) error {
			if ctx.Err() != nil {
				return errors.New("cleanup context was canceled")
			}
			removed = append(removed, candidate)
			return nil
		},
		func(context.Context, InspectedImageCandidate, providers.GraphNodeMaterializeResult, RunOptions) (BuiltImageCandidate, InspectedImageCandidate, error) {
			return alias, InspectedImageCandidate{}, errors.New("alias verification failed")
		},
	)
	if err == nil || !strings.Contains(err.Error(), "alias verification failed") || !reflect.DeepEqual(removed, []BuiltImageCandidate{alias, base}) || !reflect.DeepEqual(result, providers.GraphNodeMaterializeResult{}) {
		t.Fatalf("result = %#v, removed = %v, error = %v", result, removed, err)
	}
}

func TestMaterializationAliasBaseCleanupFailureRetriesAfterAliasRollback(t *testing.T) {
	store, request := materializationLayerFixture(t)
	transaction := request.Transaction
	transaction.Argv[2].RelativePath = transaction.Script.LogicalPath
	bundle := acceptanceBundle(transaction, request.Platform)
	wheel := providerstore.ArtifactDescriptor{LogicalPath: "hydra.whl", Kind: "wheel", Size: "10", SHA256: rendererDigest("6")}
	bundle.Payload.Artifacts = []providerstore.ArtifactDescriptor{wheel, transaction.Script}
	base := BuiltImageCandidate{ImageID: rendererDigest("7")}
	alias := BuiltImageCandidate{ImageID: rendererDigest("9")}
	removed := []BuiltImageCandidate{}
	result, err := buildAndAcceptMaterializationLayerWithFinalizer(
		context.Background(), store, transaction, bundle, request.Platform,
		func(context.Context, MaterializationEvidenceInput) ([]providers.RealizedGeneratedExecutable, []providers.RealizedOutput, error) {
			return []providers.RealizedGeneratedExecutable{acceptedGeneratedExecutable(transaction)}, nil, nil
		}, nil, RunOptions{},
		func(providerstore.Store, MaterializationLayerRequest, RunOptions) (MaterializationLayerCandidate, error) {
			candidate := acceptedMaterializationCandidate(t, transaction, request.Platform)
			return MaterializationLayerCandidate{Built: base, AssemblyKey: candidate.AssemblyKey, AssemblyKeyDigest: candidate.AssemblyKeyDigest}, nil
		},
		func(context.Context, MaterializationLayerCandidate, MaterializationLayerRequest) (InspectedMaterializationLayerCandidate, error) {
			return acceptedMaterializationCandidate(t, transaction, request.Platform), nil
		},
		func(context.Context, BuiltImageCandidate, providers.RealizedImageV1) error {
			t.Fatal("failed cleanup retained alias")
			return nil
		},
		func(_ context.Context, candidate BuiltImageCandidate) error {
			removed = append(removed, candidate)
			if candidate == base && len(removed) == 1 {
				return errors.New("transient base cleanup failure")
			}
			return nil
		},
		func(_ context.Context, source InspectedImageCandidate, _ providers.GraphNodeMaterializeResult, _ RunOptions) (BuiltImageCandidate, InspectedImageCandidate, error) {
			image := source
			image.Image.ConfigDigest = alias.ImageID
			image.Image.Digest = alias.ImageID
			return alias, image, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "transient base cleanup failure") || !reflect.DeepEqual(removed, []BuiltImageCandidate{base, alias, base}) || !reflect.DeepEqual(result, providers.GraphNodeMaterializeResult{}) {
		t.Fatalf("result = %#v, removed = %v, error = %v", result, removed, err)
	}
}
