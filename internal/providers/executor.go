package providers

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providerstore"
)

type NodeResolver interface {
	Type() blueprint.ComponentType
	Resolve(context.Context, ResolveInput, ArtifactSink) (ResolveResult, error)
}

type ProviderOwnerValidators struct {
	Profile RequirementProfileOwnerValidator
	Bundle  ResolvedBundleOwnerValidator
}

type ResolveNodeRequest struct {
	Plan              ProviderPlanV1
	NodeID            NodeID
	EarlierCatalog    []RealizedOutput
	Platform          blueprint.Platform
	SourceCandidates  []ResolvedSourceInput
	Upstream          RealizedImageV1
	ReusableArtifacts []providerstore.StoreObjectRef
}

func ResolveProviderNode(
	ctx context.Context,
	request ResolveNodeRequest,
	resolver NodeResolver,
	sink ArtifactSink,
	validators ProviderOwnerValidators,
) (ResolveResult, error) {
	if ctx == nil {
		return ResolveResult{}, fmt.Errorf("provider node resolution context is required")
	}
	if resolver == nil {
		return ResolveResult{}, fmt.Errorf("provider node resolver is required")
	}
	if sink == nil {
		return ResolveResult{}, fmt.Errorf("provider artifact sink is required")
	}
	node, input, err := buildResolveInput(request)
	if err != nil {
		return ResolveResult{}, err
	}
	if resolver.Type() != node.Provider {
		return ResolveResult{}, fmt.Errorf("resolver type %q does not match node %q provider %q", resolver.Type(), node.ID, node.Provider)
	}
	if err := ctx.Err(); err != nil {
		return ResolveResult{}, err
	}
	result, err := resolver.Resolve(ctx, input, sink)
	if err != nil {
		return ResolveResult{}, fmt.Errorf("resolve provider node %q: %w", node.ID, err)
	}
	if err := ctx.Err(); err != nil {
		return ResolveResult{}, err
	}
	if err := validateProviderNodeResolution(input, node.ID, result, validators); err != nil {
		return ResolveResult{}, err
	}
	return result, nil
}

// ValidateProviderNodeResolution validates an existing result against the
// exact graph node inputs without invoking its resolver. Consumers use this
// before accepting a cached result.
func ValidateProviderNodeResolution(
	request ResolveNodeRequest,
	result ResolveResult,
	validators ProviderOwnerValidators,
) error {
	node, input, err := buildResolveInput(request)
	if err != nil {
		return err
	}
	return validateProviderNodeResolution(input, node.ID, result, validators)
}

func validateProviderNodeResolution(
	input ResolveInput,
	nodeID NodeID,
	result ResolveResult,
	validators ProviderOwnerValidators,
) error {
	if err := ValidateResolveResult(input, result, validators.Profile, validators.Bundle); err != nil {
		return fmt.Errorf("validate provider node %q result: %w", nodeID, err)
	}
	return nil
}

func buildResolveInput(request ResolveNodeRequest) (NodeSpec, ResolveInput, error) {
	if err := ValidateProviderPlanV1(request.Plan); err != nil {
		return NodeSpec{}, ResolveInput{}, err
	}
	if request.NodeID == "base" {
		return NodeSpec{}, ResolveInput{}, fmt.Errorf("base root does not have a provider resolver")
	}
	node, found := providerPlanNode(request.Plan, request.NodeID)
	if !found {
		return NodeSpec{}, ResolveInput{}, fmt.Errorf("provider node %q is not present in the plan", request.NodeID)
	}
	candidates, err := BuildRequirementCandidates(request.Plan, node.ID, request.EarlierCatalog)
	if err != nil {
		return NodeSpec{}, ResolveInput{}, err
	}
	sources, err := filterNodeSources(request.Plan, node, request.SourceCandidates)
	if err != nil {
		return NodeSpec{}, ResolveInput{}, err
	}
	input := ResolveInput{
		Node: node, Candidates: candidates, Platform: request.Platform, SourceCandidates: sources, Upstream: request.Upstream,
		ReusableArtifacts: append([]providerstore.StoreObjectRef{}, request.ReusableArtifacts...),
	}
	if err := ValidateResolveInput(input); err != nil {
		return NodeSpec{}, ResolveInput{}, err
	}
	return node, input, nil
}

func filterNodeSources(plan ProviderPlanV1, node NodeSpec, sources []ResolvedSourceInput) ([]ResolvedSourceInput, error) {
	if sources == nil {
		return nil, fmt.Errorf("resolved source inputs must use an array")
	}
	componentNodes := make(map[string]NodeID)
	for _, candidate := range plan.Nodes {
		for _, component := range candidate.Components {
			componentNodes[component] = candidate.ID
		}
	}
	filtered := make([]ResolvedSourceInput, 0)
	for index, source := range sources {
		if index > 0 && compareResolvedSourceInputs(sources[index-1], source) >= 0 {
			return nil, fmt.Errorf("resolved source inputs must be unique and sorted by component and logical package")
		}
		if err := ValidateResolvedSourceInput(source); err != nil {
			return nil, fmt.Errorf("resolved source input %d: %w", index, err)
		}
		owner, exists := componentNodes[source.Component]
		if !exists || owner == "base" {
			return nil, fmt.Errorf("resolved source input targets missing or unsupported component %q", source.Component)
		}
		if owner == node.ID {
			filtered = append(filtered, source)
		}
	}
	return filtered, nil
}

func providerPlanNode(plan ProviderPlanV1, id NodeID) (NodeSpec, bool) {
	for _, node := range plan.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return NodeSpec{}, false
}
