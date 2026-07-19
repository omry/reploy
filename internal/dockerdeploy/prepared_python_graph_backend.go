package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

type PreparedPythonGraphBackend struct {
	BaseDescriptor deploy.ImageDescriptor
	Workspace      PreparedProbeWorkspace
	Operations     map[providers.NodeID]PreparedPythonNodeOperations
	Materializer   ProviderGraphMaterializer
}

// PrepareNode is the graph preparation callback for the temporary
// prepared-wheel Python path. Every supported node must have an explicit
// operation; APT and unknown providers remain rejected.
func (backend PreparedPythonGraphBackend) PrepareNode(
	ctx context.Context,
	request providers.GraphNodePrepareRequest,
) (providers.GraphNodePreparation, error) {
	if backend.Operations == nil {
		return providers.GraphNodePreparation{}, fmt.Errorf("prepared Python graph operations must use a map")
	}
	operation, found := backend.Operations[request.Resolve.NodeID]
	if !found {
		return providers.GraphNodePreparation{}, fmt.Errorf("prepared Python graph has no operation for node %q", request.Resolve.NodeID)
	}
	node, found := graphBackendNode(request.Resolve.Plan, request.Resolve.NodeID)
	if !found {
		return providers.GraphNodePreparation{}, fmt.Errorf("prepared Python graph node %q is absent from the plan", request.Resolve.NodeID)
	}
	if node.Provider != blueprint.ComponentTypePython {
		return providers.GraphNodePreparation{}, fmt.Errorf("prepared Python graph does not support provider %q for node %q", node.Provider, node.ID)
	}
	descriptor, err := ResolveProviderPrefixDescriptor(ctx, backend.BaseDescriptor, request.Resolve.Upstream, request.Resolve.Platform)
	if err != nil {
		return providers.GraphNodePreparation{}, err
	}
	return operation.Preparer(descriptor, backend.Workspace).Prepare(ctx, request)
}

// MaterializeNode is the graph materialization callback paired with
// PrepareNode. Only the explicitly configured Python nodes can reach Docker.
func (backend PreparedPythonGraphBackend) MaterializeNode(
	ctx context.Context,
	request providers.GraphNodeMaterializeRequest,
) (providers.GraphNodeMaterializeResult, error) {
	if backend.Operations == nil {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("prepared Python graph operations must use a map")
	}
	if _, found := backend.Operations[request.Node.ID]; !found {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("prepared Python graph has no operation for node %q", request.Node.ID)
	}
	if request.Node.Provider != blueprint.ComponentTypePython {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("prepared Python graph does not support provider %q for node %q", request.Node.Provider, request.Node.ID)
	}
	return backend.Materializer.Materialize(ctx, request)
}

func graphBackendNode(plan providers.ProviderPlanV1, id providers.NodeID) (providers.NodeSpec, bool) {
	for _, node := range plan.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return providers.NodeSpec{}, false
}
