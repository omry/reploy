package dockerdeploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
)

func TestPreparedPythonGraphBackendRejectsUnconfiguredNodes(t *testing.T) {
	backend := PreparedPythonGraphBackend{Operations: map[providers.NodeID]PreparedPythonNodeOperations{}}
	_, err := backend.PrepareNode(context.Background(), providers.GraphNodePrepareRequest{
		Resolve: providers.ResolveNodeRequest{
			NodeID: "python/missing",
			Plan: providers.ProviderPlanV1{Nodes: []providers.NodeSpec{{
				ID: "python/missing", Provider: blueprint.ComponentTypePython,
			}}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "no Python operation") {
		t.Fatalf("missing operation error = %v", err)
	}
	if _, err := backend.MaterializeNode(context.Background(), providers.GraphNodeMaterializeRequest{Node: providers.NodeSpec{ID: "python/missing", Provider: blueprint.ComponentTypePython}}); err == nil || !strings.Contains(err.Error(), "no Python operation") {
		t.Fatalf("missing materialization error = %v", err)
	}
}

func TestPreparedPythonGraphBackendDispatchesConfiguredAPTMaterialization(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	backend := PreparedPythonGraphBackend{
		APTOperations: map[providers.NodeID]PreparedAPTNodeOperations{"apt": {}},
		Materializer:  ProviderGraphMaterializer{Platform: platform},
	}
	_, err = backend.MaterializeNode(context.Background(), providers.GraphNodeMaterializeRequest{
		Node: providers.NodeSpec{ID: "apt", Provider: blueprint.ComponentTypeAPT},
	})
	if err == nil || !strings.Contains(err.Error(), "evidence runner") {
		t.Fatalf("configured APT dispatch error = %v", err)
	}
}

func TestPreparedPythonGraphBackendResolvesExactPrefixBeforeSession(t *testing.T) {
	base := testProbeImageDescriptor(t, "linux/amd64")
	child := providers.RealizedImageV1{
		Digest: rendererDigest("a"), ConfigDigest: rendererDigest("a"), RootFSSubject: rendererDigest("b"),
	}
	previous := inspectProviderPrefixImage
	t.Cleanup(func() { inspectProviderPrefixImage = previous })
	inspectCalls := 0
	inspectProviderPrefixImage = func(context.Context, BuiltImageCandidate, blueprint.Platform) (InspectedImageCandidate, error) {
		inspectCalls++
		return InspectedImageCandidate{}, errors.New("prefix inspection failed")
	}
	nodeID := providers.NodeID("python/web")
	backend := PreparedPythonGraphBackend{
		BaseDescriptor: base,
		Operations:     map[providers.NodeID]PreparedPythonNodeOperations{nodeID: {}},
	}
	_, err := backend.PrepareNode(context.Background(), providers.GraphNodePrepareRequest{
		Resolve: providers.ResolveNodeRequest{
			NodeID: nodeID, Platform: base.Platform, Upstream: child,
			Plan: providers.ProviderPlanV1{Nodes: []providers.NodeSpec{{ID: nodeID, Provider: blueprint.ComponentTypePython}}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "prefix inspection failed") || inspectCalls != 1 {
		t.Fatalf("calls = %d, error = %v", inspectCalls, err)
	}
}
