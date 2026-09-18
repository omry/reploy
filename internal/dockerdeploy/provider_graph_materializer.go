package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type ProviderGraphMaterializer struct {
	Store             providerstore.Store
	Platform          blueprint.Platform
	RunEvidence       MaterializationEvidenceRunner
	RetainLayer       materializationCandidateRetainer
	RunOptions        RunOptions
	verifiedArtifacts map[providers.NodeID]map[canonical.Digest]string
	portableBindings  map[providers.NodeID]*pythonprovider.PortableToolPythonComponentV1
}

var materializeProviderGraphNode = registry.MaterializeNode
var (
	buildAndAcceptProviderGraphLayer      = buildAndAcceptMaterializationLayerWithVerifiedArtifacts
	buildAndAcceptProviderGraphAliasLayer = buildAndAcceptMaterializationLayerWithFinalizer
)

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
	if materializer.RetainLayer == nil {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("materialize provider graph node requires a layer retention policy")
	}
	transaction, err := materializeProviderGraphNode(request.Node, request.Input)
	if err != nil {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("prepare provider graph node %q transaction: %w", request.Node.ID, err)
	}
	options := materializer.RunOptions
	options.Context = ctx
	var result providers.GraphNodeMaterializeResult
	bindings := materializer.portableBindings[request.Node.ID]
	if bindings != nil && len(bindings.Bindings) != 0 {
		aliases, planErr := planPortablePythonAliasesV1(bindings, transaction)
		if planErr != nil {
			return providers.GraphNodeMaterializeResult{}, fmt.Errorf("plan provider graph node %q Python aliases: %w", request.Node.ID, planErr)
		}
		if len(aliases) == 0 {
			return providers.GraphNodeMaterializeResult{}, fmt.Errorf("provider graph node %q selected Python bindings have no aliases", request.Node.ID)
		}
		result, err = buildAndAcceptProviderGraphAliasLayer(
			ctx, materializer.Store, transaction, request.Input.Bundle,
			materializer.Platform, materializer.RunEvidence,
			materializer.verifiedArtifacts[request.Node.ID], options,
			BuildMaterializationLayer, InspectMaterializationLayerCandidate,
			materializer.RetainLayer, RemoveBuiltImageCandidate,
			func(
				finalizeCtx context.Context,
				source InspectedImageCandidate,
				_ providers.GraphNodeMaterializeResult,
				finalizeOptions RunOptions,
			) (BuiltImageCandidate, InspectedImageCandidate, error) {
				return buildAndValidatePortablePythonAliasLayerV1(
					finalizeCtx, materializer.Store, source, aliases,
					materializer.Platform, finalizeOptions,
				)
			},
		)
	} else {
		result, err = buildAndAcceptProviderGraphLayer(
			ctx,
			materializer.Store,
			transaction,
			request.Input.Bundle,
			materializer.Platform,
			materializer.RunEvidence,
			materializer.verifiedArtifacts[request.Node.ID],
			materializer.RetainLayer,
			options,
		)
	}
	if err != nil {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("build provider graph node %q: %w", request.Node.ID, err)
	}
	return result, nil
}
