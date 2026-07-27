package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type ProviderGraphMaterializer struct {
	Store             providerstore.Store
	Platform          blueprint.Platform
	RunEvidence       MaterializationEvidenceRunner
	RunOptions        RunOptions
	verifiedArtifacts map[providers.NodeID]map[canonical.Digest]string
}

var materializeProviderGraphNode = registry.MaterializeNode
var buildAndAcceptProviderGraphLayer = buildAndAcceptMaterializationLayerWithVerifiedArtifacts

// Materialize is the graph callback for the typed Docker backend. Provider
// registry materialization is followed by the sole build-and-accept pipeline;
// an unvalidated image candidate is never returned to the graph.
func (materializer ProviderGraphMaterializer) Materialize(
	ctx context.Context,
	request providers.GraphNodeMaterializeRequest,
) (providers.GraphNodeMaterializeResult, error) {
	if ctx == nil {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("materialize provider graph node requires a context")
	}
	if err := ctx.Err(); err != nil {
		return providers.GraphNodeMaterializeResult{}, err
	}
	if err := materializer.Platform.Validate(); err != nil {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("materialize provider graph platform: %w", err)
	}
	if materializer.RunEvidence == nil {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("materialize provider graph node requires an evidence runner")
	}
	transaction, err := materializeProviderGraphNode(request.Node, request.Input)
	if err != nil {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("prepare provider graph node %q transaction: %w", request.Node.ID, err)
	}
	options := materializer.RunOptions
	options.Context = ctx
	result, err := buildAndAcceptProviderGraphLayer(
		ctx,
		materializer.Store,
		transaction,
		request.Input.Bundle,
		materializer.Platform,
		materializer.RunEvidence,
		materializer.verifiedArtifacts[request.Node.ID],
		options,
	)
	if err != nil {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("build provider graph node %q: %w", request.Node.ID, err)
	}
	return result, nil
}
