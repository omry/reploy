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
	APTOperations  map[providers.NodeID]PreparedAPTNodeOperations
	Materializer   ProviderGraphMaterializer
}

// PrepareNode is the graph preparation callback for the prepared provider
// path. Every supported node must have an explicit provider-specific
// operation.
func (backend PreparedPythonGraphBackend) PrepareNode(
	ctx context.Context,
	request providers.GraphNodePrepareRequest,
) (providers.GraphNodePreparation, error) {
	node, found := graphBackendNode(request.Resolve.Plan, request.Resolve.NodeID)
	if !found {
		return providers.GraphNodePreparation{}, fmt.Errorf("prepared provider graph node %q is absent from the plan", request.Resolve.NodeID)
	}
	switch node.Provider {
	case blueprint.ComponentTypeAPT:
		operation, found := backend.APTOperations[node.ID]
		if !found {
			return providers.GraphNodePreparation{}, fmt.Errorf("prepared provider graph has no APT operation for node %q", node.ID)
		}
		descriptor, err := ResolveProviderPrefixDescriptor(ctx, backend.BaseDescriptor, request.Resolve.Upstream, request.Resolve.Platform)
		if err != nil {
			return providers.GraphNodePreparation{}, err
		}
		return operation.Prepare(ctx, descriptor, backend.Workspace, request)
	case blueprint.ComponentTypePython:
		operation, found := backend.Operations[node.ID]
		if !found {
			return providers.GraphNodePreparation{}, fmt.Errorf("prepared provider graph has no Python operation for node %q", node.ID)
		}
		descriptor, err := ResolveProviderPrefixDescriptor(ctx, backend.BaseDescriptor, request.Resolve.Upstream, request.Resolve.Platform)
		if err != nil {
			return providers.GraphNodePreparation{}, err
		}
		return operation.Preparer(descriptor, backend.Workspace).Prepare(ctx, request)
	default:
		return providers.GraphNodePreparation{}, fmt.Errorf("prepared provider graph does not support provider %q for node %q", node.Provider, node.ID)
	}
}

// MaterializeNode is the graph materialization callback paired with
// PrepareNode. Only explicitly configured provider nodes can reach Docker.
func (backend PreparedPythonGraphBackend) MaterializeNode(
	ctx context.Context,
	request providers.GraphNodeMaterializeRequest,
) (providers.GraphNodeMaterializeResult, error) {
	switch request.Node.Provider {
	case blueprint.ComponentTypeAPT:
		if _, found := backend.APTOperations[request.Node.ID]; !found {
			return providers.GraphNodeMaterializeResult{}, fmt.Errorf("prepared provider graph has no APT operation for node %q", request.Node.ID)
		}
	case blueprint.ComponentTypePython:
		if _, found := backend.Operations[request.Node.ID]; !found {
			return providers.GraphNodeMaterializeResult{}, fmt.Errorf("prepared provider graph has no Python operation for node %q", request.Node.ID)
		}
	default:
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("prepared provider graph does not support provider %q for node %q", request.Node.Provider, request.Node.ID)
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
