package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

func TestProviderGraphMaterializerUsesTypedAcceptancePipeline(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	previousMaterialize := materializeProviderGraphNode
	previousAccept := buildAndAcceptProviderGraphLayer
	t.Cleanup(func() {
		materializeProviderGraphNode = previousMaterialize
		buildAndAcceptProviderGraphLayer = previousAccept
	})
	transaction := rendererTransaction()
	request := providers.GraphNodeMaterializeRequest{
		Node: providers.NodeSpec{ID: transaction.NodeID},
		Input: providers.MaterializeInput{Bundle: providers.ResolvedBundle{
			Payload: providers.ResolvedBundleIdentityV1{NodeID: transaction.NodeID},
		}},
	}
	materializeCalls := 0
	acceptCalls := 0
	materializeProviderGraphNode = func(node providers.NodeSpec, input providers.MaterializeInput) (providers.MaterializationTransaction, error) {
		materializeCalls++
		if node.ID != request.Node.ID || !reflect.DeepEqual(input, request.Input) {
			return providers.MaterializationTransaction{}, errors.New("wrong graph materialization input")
		}
		return transaction, nil
	}
	want := providers.GraphNodeMaterializeResult{Image: transaction.Upstream, TransactionDigest: rendererDigest("d"), GeneratedExecutables: []providers.RealizedGeneratedExecutable{}, Outputs: []providers.RealizedOutput{}}
	verified := map[canonical.Digest]string{rendererDigest("e"): "/tmp/wheel"}
	ctx := context.Background()
	evidence := func(context.Context, MaterializationEvidenceInput) ([]providers.RealizedGeneratedExecutable, []providers.RealizedOutput, error) {
		return nil, nil, nil
	}
	buildAndAcceptProviderGraphLayer = func(
		gotCtx context.Context,
		gotStore providerstore.Store,
		gotTransaction providers.MaterializationTransaction,
		gotBundle providers.ResolvedBundle,
		gotPlatform blueprint.Platform,
		gotEvidence MaterializationEvidenceRunner,
		gotVerified map[canonical.Digest]string,
		gotRetain materializationCandidateRetainer,
		options RunOptions,
	) (providers.GraphNodeMaterializeResult, error) {
		acceptCalls++
		if gotCtx != ctx || gotStore.Root() != store.Root() || !reflect.DeepEqual(gotTransaction, transaction) || !reflect.DeepEqual(gotBundle, request.Input.Bundle) || gotPlatform != platform || gotEvidence == nil || !reflect.DeepEqual(gotVerified, verified) || gotRetain == nil || options.Context != ctx {
			return providers.GraphNodeMaterializeResult{}, errors.New("wrong acceptance input")
		}
		return want, nil
	}
	materializer := ProviderGraphMaterializer{
		Store: store, Platform: platform, RunEvidence: evidence,
		RetainLayer:       func(context.Context, BuiltImageCandidate, providers.RealizedImageV1) error { return nil },
		verifiedArtifacts: map[providers.NodeID]map[canonical.Digest]string{request.Node.ID: verified},
	}
	got, err := materializer.Materialize(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if materializeCalls != 1 || acceptCalls != 1 || !reflect.DeepEqual(got, want) {
		t.Fatalf("materialize = %d, accept = %d, result = %#v", materializeCalls, acceptCalls, got)
	}
}

func TestProviderGraphMaterializerRoutesPortableBindingsThroughAliasFinalizer(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	component, transaction, _ := portablePythonAliasPlanFixtureV1(t, "application:alias-materializer")
	request := providers.GraphNodeMaterializeRequest{
		Node: providers.NodeSpec{ID: transaction.NodeID},
		Input: providers.MaterializeInput{Bundle: providers.ResolvedBundle{
			Payload: providers.ResolvedBundleIdentityV1{NodeID: transaction.NodeID},
		}},
	}
	previousMaterialize := materializeProviderGraphNode
	previousPlain := buildAndAcceptProviderGraphLayer
	previousAlias := buildAndAcceptProviderGraphAliasLayer
	t.Cleanup(func() {
		materializeProviderGraphNode = previousMaterialize
		buildAndAcceptProviderGraphLayer = previousPlain
		buildAndAcceptProviderGraphAliasLayer = previousAlias
	})
	materializeProviderGraphNode = func(node providers.NodeSpec, input providers.MaterializeInput) (providers.MaterializationTransaction, error) {
		if node.ID != request.Node.ID || !reflect.DeepEqual(input, request.Input) {
			return providers.MaterializationTransaction{}, errors.New("wrong portable graph materialization input")
		}
		return transaction, nil
	}
	buildAndAcceptProviderGraphLayer = func(context.Context, providerstore.Store, providers.MaterializationTransaction, providers.ResolvedBundle, blueprint.Platform, MaterializationEvidenceRunner, map[canonical.Digest]string, materializationCandidateRetainer, RunOptions) (providers.GraphNodeMaterializeResult, error) {
		t.Fatal("portable bindings used the plain acceptance pipeline")
		return providers.GraphNodeMaterializeResult{}, nil
	}
	want := providers.GraphNodeMaterializeResult{Image: transaction.Upstream, TransactionDigest: rendererDigest("d")}
	aliasCalls := 0
	buildAndAcceptProviderGraphAliasLayer = func(
		gotCtx context.Context,
		gotStore providerstore.Store,
		gotTransaction providers.MaterializationTransaction,
		gotBundle providers.ResolvedBundle,
		gotPlatform blueprint.Platform,
		gotEvidence MaterializationEvidenceRunner,
		_ map[canonical.Digest]string,
		options RunOptions,
		_ materializationLayerBuilder,
		_ materializationLayerInspector,
		_ materializationCandidateRetainer,
		_ materializationCandidateRemover,
		finalize acceptedMaterializationLayerFinalizer,
	) (providers.GraphNodeMaterializeResult, error) {
		aliasCalls++
		if gotCtx == nil || gotStore.Root() != store.Root() || !reflect.DeepEqual(gotTransaction, transaction) ||
			!reflect.DeepEqual(gotBundle, request.Input.Bundle) || gotPlatform != platform || gotEvidence == nil ||
			options.Context != gotCtx || finalize == nil {
			return providers.GraphNodeMaterializeResult{}, errors.New("wrong portable alias acceptance input")
		}
		return want, nil
	}
	materializer := ProviderGraphMaterializer{
		Store: store, Platform: platform,
		RunEvidence: func(context.Context, MaterializationEvidenceInput) ([]providers.RealizedGeneratedExecutable, []providers.RealizedOutput, error) {
			return nil, nil, nil
		},
		RetainLayer:      func(context.Context, BuiltImageCandidate, providers.RealizedImageV1) error { return nil },
		portableBindings: map[providers.NodeID]*pythonprovider.PortableToolPythonComponentV1{transaction.NodeID: &component},
	}
	got, err := materializer.Materialize(context.Background(), request)
	if err != nil || aliasCalls != 1 || !reflect.DeepEqual(got, want) {
		t.Fatalf("portable materialize = %#v, alias calls = %d, error = %v", got, aliasCalls, err)
	}
	transaction.GeneratedExecutables = transaction.GeneratedExecutables[1:]
	if _, err := materializer.Materialize(context.Background(), request); err == nil || !strings.Contains(err.Error(), "no generated executable declaration") {
		t.Fatalf("missing generated script error = %v", err)
	}
	if aliasCalls != 1 {
		t.Fatalf("alias acceptance ran after invalid transaction: %d calls", aliasCalls)
	}
}

func TestProviderGraphMaterializerStopsBeforeBuildOnRegistryFailure(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	previousMaterialize := materializeProviderGraphNode
	previousAccept := buildAndAcceptProviderGraphLayer
	t.Cleanup(func() {
		materializeProviderGraphNode = previousMaterialize
		buildAndAcceptProviderGraphLayer = previousAccept
	})
	materializeProviderGraphNode = func(providers.NodeSpec, providers.MaterializeInput) (providers.MaterializationTransaction, error) {
		return providers.MaterializationTransaction{}, errors.New("APT provider execution is not implemented")
	}
	buildAndAcceptProviderGraphLayer = func(context.Context, providerstore.Store, providers.MaterializationTransaction, providers.ResolvedBundle, blueprint.Platform, MaterializationEvidenceRunner, map[canonical.Digest]string, materializationCandidateRetainer, RunOptions) (providers.GraphNodeMaterializeResult, error) {
		t.Fatal("build ran after registry failure")
		return providers.GraphNodeMaterializeResult{}, nil
	}
	materializer := ProviderGraphMaterializer{
		Store: store, Platform: platform,
		RunEvidence: func(context.Context, MaterializationEvidenceInput) ([]providers.RealizedGeneratedExecutable, []providers.RealizedOutput, error) {
			return nil, nil, nil
		},
		RetainLayer: func(context.Context, BuiltImageCandidate, providers.RealizedImageV1) error { return nil },
	}
	_, err = materializer.Materialize(context.Background(), providers.GraphNodeMaterializeRequest{Node: providers.NodeSpec{ID: "apt"}})
	if err == nil || !strings.Contains(err.Error(), "APT provider execution is not implemented") {
		t.Fatalf("error = %v", err)
	}
}
