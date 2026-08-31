package dockerdeploy

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type BuildLockAssemblyInput struct {
	BlueprintDigest  canonical.Digest
	ResolvedRequest  providers.ResolvedRequestV1
	Overlay          deploy.RequestOverlayV1
	PackageOverrides deploy.PackageOverrideIntentV1
	Base             deploy.ImageDescriptor
	Graph            providers.GraphExecutionResult
	PortableTools    *providers.PortableToolLockV1
	RuntimePolicy    deploy.RuntimePolicyV1
	RuntimeLayer     deploy.ApplicationRuntimeLayerV1
	ValidationRecord providerstore.StoreObjectRef
	FinalImage       providers.RealizedImageV1
}

// AssembleBuildLock publishes the graph's canonical bundle manifests and
// constructs the immutable lock. State and image-reference publication remain
// a separate crash-recoverable boundary.
func AssembleBuildLock(
	ctx context.Context,
	store providerstore.Store,
	input BuildLockAssemblyInput,
) (deploy.BuildLockV1, error) {
	if ctx == nil {
		return deploy.BuildLockV1{}, fmt.Errorf("assemble build lock requires a context")
	}
	if err := ctx.Err(); err != nil {
		return deploy.BuildLockV1{}, err
	}
	if err := input.BlueprintDigest.Validate(); err != nil {
		return deploy.BuildLockV1{}, fmt.Errorf("assemble build lock blueprint digest: %w", err)
	}
	requestDigest, err := providers.ResolvedRequestDigest(input.ResolvedRequest, registry.ValidateResolvedRequestOwnersV1)
	if err != nil {
		return deploy.BuildLockV1{}, fmt.Errorf("assemble build lock resolved request: %w", err)
	}
	overlayDigest, err := deploy.RequestOverlayDigestV1(input.Overlay)
	if err != nil {
		return deploy.BuildLockV1{}, err
	}
	if overlayDigest != input.ResolvedRequest.OverlayDigest {
		return deploy.BuildLockV1{}, fmt.Errorf("assemble build lock overlay does not match the resolved request")
	}
	if err := deploy.ValidatePackageOverrideIntentV1(input.PackageOverrides); err != nil {
		return deploy.BuildLockV1{}, err
	}
	if err := input.Base.Validate(); err != nil {
		return deploy.BuildLockV1{}, fmt.Errorf("assemble build lock base: %w", err)
	}
	if input.Base.Platform != input.ResolvedRequest.Platform {
		return deploy.BuildLockV1{}, fmt.Errorf("assemble build lock base platform does not match the resolved request")
	}
	planned, err := registry.Plan(providers.PlanInput{
		Components: input.ResolvedRequest.Components,
		Platform:   input.ResolvedRequest.Platform,
	})
	if err != nil {
		return deploy.BuildLockV1{}, err
	}
	plannedBytes, err := canonical.Marshal(planned)
	if err != nil {
		return deploy.BuildLockV1{}, err
	}
	graphBytes, err := canonical.Marshal(input.Graph.Plan)
	if err != nil {
		return deploy.BuildLockV1{}, err
	}
	if !bytes.Equal(plannedBytes, graphBytes) {
		return deploy.BuildLockV1{}, fmt.Errorf("assemble build lock graph plan does not match the resolved request")
	}
	if err := validateGraphLockAssemblyShape(input); err != nil {
		return deploy.BuildLockV1{}, err
	}

	nodesByID := make(map[providers.NodeID]providers.NodeSpec, len(input.Graph.Plan.Nodes))
	graphNodes := make([]providers.NodeID, 0, len(input.Graph.Plan.Nodes))
	for _, node := range input.Graph.Plan.Nodes {
		nodesByID[node.ID] = node
		graphNodes = append(graphNodes, node.ID)
	}
	locks := make([]deploy.NodeLockV1, 0, len(input.Graph.Bundles))
	for index, bundle := range input.Graph.Bundles {
		node, found := nodesByID[bundle.Payload.NodeID]
		if !found || node.ID == "base" || node.Provider != bundle.Payload.Provider {
			return deploy.BuildLockV1{}, fmt.Errorf("assemble build lock bundle %d does not identify a planned provider node", index)
		}
		if bundle.Payload.Upstream != input.Graph.PrefixImages[index] {
			return deploy.BuildLockV1{}, fmt.Errorf("assemble build lock bundle for node %q does not name its exact upstream prefix", node.ID)
		}
		validators, err := registry.OwnerValidatorsForNode(node)
		if err != nil {
			return deploy.BuildLockV1{}, err
		}
		manifest, err := providers.PublishResolvedBundleManifest(ctx, store, bundle, validators.Bundle)
		if err != nil {
			return deploy.BuildLockV1{}, fmt.Errorf("publish bundle manifest for node %q: %w", node.ID, err)
		}
		planDigest, err := providers.ProviderNodePlanDigest(node)
		if err != nil {
			return deploy.BuildLockV1{}, err
		}
		nodeRequestDigest, err := providers.ProviderRequestDigest(node.Request)
		if err != nil {
			return deploy.BuildLockV1{}, err
		}
		profile := input.Graph.Profiles[index]
		profileDigest, err := providers.RequirementProfileDigest(profile, validators.Profile)
		if err != nil {
			return deploy.BuildLockV1{}, err
		}
		cacheKey, err := providers.ResolverCacheKeyDigest(providers.ResolverCacheKeyV1{
			Schema: providers.ResolverCacheKeySchemaV1, NodeID: node.ID,
			RequestDigest: nodeRequestDigest, ProfileDigest: profileDigest,
			ResolverRecipe: bundle.Payload.RecipeVersion, Platform: input.ResolvedRequest.Platform,
		})
		if err != nil {
			return deploy.BuildLockV1{}, err
		}
		materialized := input.Graph.Materializations[index]
		locks = append(locks, deploy.NodeLockV1{
			NodeID: node.ID, Provider: node.Provider, PlanDigest: planDigest, ResolverCacheKey: cacheKey,
			RequirementProfile: profile, ValidationEvidence: input.Graph.ValidationEvidence[index],
			BundleManifest: manifest, TransactionDigest: materialized.TransactionDigest,
			Upstream: bundle.Payload.Upstream, Result: materialized.Image,
			GeneratedExecutables: append([]providers.RealizedGeneratedExecutable{}, materialized.GeneratedExecutables...),
			Outputs:              append([]providers.RealizedOutput{}, materialized.Outputs...),
		})
	}
	sort.Slice(locks, func(left int, right int) bool { return locks[left].NodeID < locks[right].NodeID })
	var portableTools *providers.PortableToolLockV1
	if input.PortableTools != nil {
		if err := providers.ValidatePortableToolLockV1(*input.PortableTools); err != nil {
			return deploy.BuildLockV1{}, fmt.Errorf("assemble build lock portable tools: %w", err)
		}
		cloned := providers.ClonePortableToolLockV1(*input.PortableTools)
		portableTools = &cloned
	}
	lock := deploy.BuildLockV1{
		Schema: deploy.BuildLockSchemaV1, BlueprintDigest: input.BlueprintDigest,
		Overlay: input.Overlay, PackageOverrides: input.PackageOverrides,
		ResolvedRequestDigest: requestDigest, Platform: input.ResolvedRequest.Platform,
		Base: input.Base, Graph: deploy.ProviderGraphLockV1{
			Nodes: graphNodes, Edges: append([]providers.ProviderEdgeV1{}, input.Graph.SelectedEdges...),
		},
		Nodes: locks, Catalog: append([]providers.RealizedOutput{}, input.Graph.Catalog...),
		PortableTools: portableTools,
		RuntimePolicy: input.RuntimePolicy, RuntimeLayer: input.RuntimeLayer,
		ValidationRecord: input.ValidationRecord, FinalImage: input.FinalImage,
	}
	if err := deploy.ValidateBuildLockV1(lock, registry.ValidateRequirementProfileV1); err != nil {
		return deploy.BuildLockV1{}, fmt.Errorf("assemble build lock: %w", err)
	}
	if _, err := deploy.BuildLockStoreClosure(lock, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
		return deploy.BuildLockV1{}, fmt.Errorf("assemble build lock closure: %w", err)
	}
	return lock, nil
}

func validateGraphLockAssemblyShape(input BuildLockAssemblyInput) error {
	if len(input.Graph.PrefixImages) != len(input.Graph.Materializations)+1 || len(input.Graph.Bundles) != len(input.Graph.Materializations) || len(input.Graph.Profiles) != len(input.Graph.Bundles) || len(input.Graph.ValidationEvidence) != len(input.Graph.Bundles) {
		return fmt.Errorf("assemble build lock graph result collections do not align")
	}
	baseImage, err := realizedImageFromDescriptor(input.Base)
	if err != nil {
		return err
	}
	if input.Graph.PrefixImages[0] != baseImage {
		return fmt.Errorf("assemble build lock graph does not start from the selected base")
	}
	for index, materialized := range input.Graph.Materializations {
		if input.Graph.PrefixImages[index+1] != materialized.Image {
			return fmt.Errorf("assemble build lock graph prefix %d does not match its materialization", index+1)
		}
	}
	if err := deploy.ValidateRuntimePolicyV1(input.RuntimePolicy); err != nil {
		return err
	}
	if err := deploy.ValidateApplicationRuntimeLayerV1(input.RuntimeLayer, input.ResolvedRequest.Platform); err != nil {
		return err
	}
	if err := input.ValidationRecord.Validate(); err != nil || input.ValidationRecord.Kind != providerstore.ValidationRecordKind {
		return fmt.Errorf("assemble build lock validation record is invalid")
	}
	if err := input.FinalImage.Validate(); err != nil {
		return fmt.Errorf("assemble build lock final image: %w", err)
	}
	last := input.Graph.PrefixImages[len(input.Graph.PrefixImages)-1]
	if input.RuntimeLayer.Upstream != last || input.FinalImage.RootFSSubject != input.RuntimeLayer.Result.RootFSSubject {
		return fmt.Errorf("assemble build lock application runtime layer does not connect the graph result to the final image")
	}
	return nil
}
