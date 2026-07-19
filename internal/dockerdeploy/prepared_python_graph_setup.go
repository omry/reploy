package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type PreparedPythonNodeConfig struct {
	ReusableWheels []providerstore.ArtifactDescriptor
}

// PreparePreparedPythonGraphBackend assembles the deployment-scoped Python
// graph backend and extracts one platform probe helper beneath the same
// deployment-owned provider store. The returned cleanup removes that scratch
// helper workspace.
func PreparePreparedPythonGraphBackend(
	ctx context.Context,
	store providerstore.Store,
	plan providers.ProviderPlanV1,
	baseDescriptor deploy.ImageDescriptor,
	finalImageConfig providers.ImageConfigPolicy,
	configs map[providers.NodeID]PreparedPythonNodeConfig,
	options RunOptions,
) (PreparedPythonGraphBackend, func() error, error) {
	noCleanup := func() error { return nil }
	if ctx == nil {
		return PreparedPythonGraphBackend{}, noCleanup, fmt.Errorf("prepare Python graph backend requires a context")
	}
	if err := ctx.Err(); err != nil {
		return PreparedPythonGraphBackend{}, noCleanup, err
	}
	if err := providers.ValidateProviderPlanV1(plan); err != nil {
		return PreparedPythonGraphBackend{}, noCleanup, err
	}
	if err := baseDescriptor.Validate(); err != nil {
		return PreparedPythonGraphBackend{}, noCleanup, fmt.Errorf("prepare Python graph base descriptor: %w", err)
	}
	if err := providers.ValidateImageConfigPolicy(finalImageConfig); err != nil {
		return PreparedPythonGraphBackend{}, noCleanup, fmt.Errorf("prepare Python graph final image config: %w", err)
	}
	if configs == nil {
		return PreparedPythonGraphBackend{}, noCleanup, fmt.Errorf("prepared Python node configs must use a map")
	}
	nodes := make(map[providers.NodeID]providers.NodeSpec, len(plan.Nodes))
	for _, node := range plan.Nodes {
		nodes[node.ID] = node
	}
	for id := range configs {
		node, found := nodes[id]
		if !found || id == "base" || node.Provider != blueprint.ComponentTypePython {
			return PreparedPythonGraphBackend{}, noCleanup, fmt.Errorf("prepared Python config targets unsupported node %q", id)
		}
	}
	operations := make(map[providers.NodeID]PreparedPythonNodeOperations)
	verifiedArtifacts := make(map[providers.NodeID]map[canonical.Digest]string)
	artifactCleanups := []func(){}
	cleanupArtifacts := func() {
		for index := len(artifactCleanups) - 1; index >= 0; index-- {
			artifactCleanups[index]()
		}
	}
	for _, node := range plan.Nodes {
		if node.ID == "base" {
			continue
		}
		if node.Provider != blueprint.ComponentTypePython {
			cleanupArtifacts()
			return PreparedPythonGraphBackend{}, noCleanup, fmt.Errorf("prepared Python graph does not support provider %q for node %q", node.Provider, node.ID)
		}
		config, found := configs[node.ID]
		if !found {
			cleanupArtifacts()
			return PreparedPythonGraphBackend{}, noCleanup, fmt.Errorf("prepared Python graph node %q has no config", node.ID)
		}
		validators, err := registry.OwnerValidatorsForNode(node)
		if err != nil {
			cleanupArtifacts()
			return PreparedPythonGraphBackend{}, noCleanup, err
		}
		artifacts, cleanupArtifactsForNode, err := PreparePythonResolverArtifacts(store, config.ReusableWheels)
		if err != nil {
			cleanupArtifacts()
			return PreparedPythonGraphBackend{}, noCleanup, fmt.Errorf("prepare Python graph node %q resolver artifacts: %w", node.ID, err)
		}
		artifactCleanups = append(artifactCleanups, cleanupArtifactsForNode)
		verified := map[canonical.Digest]string{}
		verifiedArtifacts[node.ID] = verified
		operations[node.ID] = PreparedPythonNodeOperations{
			Store: store, Validators: validators,
			FinalImageConfig: cloneImageConfigPolicy(finalImageConfig), Artifacts: artifacts,
			ReusableWheels:    append([]providerstore.ArtifactDescriptor{}, config.ReusableWheels...),
			verifiedArtifacts: verified,
		}
	}
	workspace, cleanup, err := PrepareProbeWorkspace(ctx, store, baseDescriptor.Platform)
	if err != nil {
		cleanupArtifacts()
		return PreparedPythonGraphBackend{}, noCleanup, err
	}
	cleanupAll := func() error {
		err := cleanup()
		cleanupArtifacts()
		return err
	}
	evidence := ProviderMaterializationEvidenceRunner{Store: store}
	return PreparedPythonGraphBackend{
		BaseDescriptor: baseDescriptor,
		Workspace:      workspace,
		Operations:     operations,
		Materializer: ProviderGraphMaterializer{
			Store: store, Platform: baseDescriptor.Platform, RunEvidence: evidence.Run, RunOptions: options,
			verifiedArtifacts: verifiedArtifacts,
		},
	}, cleanupAll, nil
}

func cloneImageConfigPolicy(policy providers.ImageConfigPolicy) providers.ImageConfigPolicy {
	clone := policy
	clone.Environment = append([]providers.EnvironmentVariable{}, policy.Environment...)
	clone.Entrypoint = append([]string{}, policy.Entrypoint...)
	clone.Command = append([]string{}, policy.Command...)
	clone.Labels = append([]providers.ImageLabel{}, policy.Labels...)
	return clone
}
