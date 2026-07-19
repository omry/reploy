package providers

import (
	"context"
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providerstore"
)

type GraphNodeMaterializeRequest struct {
	Node  NodeSpec
	Input MaterializeInput
}

type GraphNodeMaterializeResult struct {
	Image                RealizedImageV1
	TransactionDigest    canonical.Digest
	GeneratedExecutables []RealizedGeneratedExecutable
	Outputs              []RealizedOutput
}

type GraphConsumerValidation struct {
	Carrier             ValidatedExecutableInput
	EnvironmentLauncher ValidatedExecutableInput
	FinalImageConfig    ImageConfigPolicy
}

// GraphNodePrepareRequest gives one backend session both the current node
// inputs and the optional prior result it may accept or replace.
type GraphNodePrepareRequest struct {
	Resolve          ResolveNodeRequest
	CachedResolution *ResolveResult
}

// GraphNodePreparation binds a resolution to consumer evidence observed in
// the same backend session. Refreshed is true only when a supplied cached
// resolution was rejected and replaced once.
type GraphNodePreparation struct {
	Resolution ResolveResult
	Consumer   GraphConsumerValidation
	Refreshed  bool
}

// GraphNodePreparer validates cached state and, when needed, performs one
// fresh resolution without opening a second backend session.
type GraphNodePreparer func(context.Context, GraphNodePrepareRequest) (GraphNodePreparation, error)

type GraphNodeMaterializer func(context.Context, GraphNodeMaterializeRequest) (GraphNodeMaterializeResult, error)

type GraphOwnerValidators func(NodeSpec) (ProviderOwnerValidators, error)

type GraphExecutionRequest struct {
	Plan              ProviderPlanV1
	Platform          blueprint.Platform
	Sources           []ResolvedSourceInput
	BaseImage         RealizedImageV1
	BaseCatalog       []RealizedOutput
	ReusableArtifacts map[NodeID][]providerstore.StoreObjectRef
	CachedResolutions map[NodeID]ResolveResult
	Validators        GraphOwnerValidators
	PrepareNode       GraphNodePreparer
	MaterializeNode   GraphNodeMaterializer
}

type GraphExecutionResult struct {
	Plan               ProviderPlanV1
	SelectedEdges      []ProviderEdgeV1
	Bundles            []ResolvedBundle
	Profiles           []RequirementProfile
	ValidationEvidence []ValidationEvidence
	PrefixImages       []RealizedImageV1
	Materializations   []GraphNodeMaterializeResult
	Catalog            []RealizedOutput
}

// ExecuteProviderGraph coordinates resolution and materialization in stable
// graph order. The injected callbacks own backend work; this function
// validates every returned provider record and advances the visible catalog
// only after the corresponding image prefix and outputs are complete.
func ExecuteProviderGraph(ctx context.Context, request GraphExecutionRequest) (GraphExecutionResult, error) {
	if ctx == nil {
		return GraphExecutionResult{}, fmt.Errorf("provider graph execution context is required")
	}
	if err := ValidateProviderPlanV1(request.Plan); err != nil {
		return GraphExecutionResult{}, err
	}
	if err := request.Platform.Validate(); err != nil {
		return GraphExecutionResult{}, fmt.Errorf("provider graph platform: %w", err)
	}
	if request.Sources == nil || request.BaseCatalog == nil || request.ReusableArtifacts == nil || request.CachedResolutions == nil {
		return GraphExecutionResult{}, fmt.Errorf("provider graph sources, base catalog, reusable artifacts, and cached resolutions must use collections")
	}
	if err := request.BaseImage.Validate(); err != nil {
		return GraphExecutionResult{}, fmt.Errorf("provider graph base image: %w", err)
	}
	if request.Validators == nil {
		return GraphExecutionResult{}, fmt.Errorf("provider graph owner validators are required")
	}
	if err := validateBaseExecutionCatalog(request.Plan, request.BaseCatalog); err != nil {
		return GraphExecutionResult{}, err
	}
	order, err := StableProviderInitializationOrder(request.Plan)
	if err != nil {
		return GraphExecutionResult{}, err
	}
	knownNodes := make(map[NodeID]bool, len(order))
	for _, id := range order {
		knownNodes[id] = true
	}
	for id := range request.ReusableArtifacts {
		if id == "base" || !knownNodes[id] {
			return GraphExecutionResult{}, fmt.Errorf("reusable artifacts target missing or non-resolving node %q", id)
		}
	}
	for id := range request.CachedResolutions {
		if id == "base" || !knownNodes[id] {
			return GraphExecutionResult{}, fmt.Errorf("cached resolution targets missing or non-resolving node %q", id)
		}
	}
	if len(order) > 1 && (request.PrepareNode == nil || request.MaterializeNode == nil) {
		return GraphExecutionResult{}, fmt.Errorf("provider graph node preparer and materializer are required")
	}

	result := GraphExecutionResult{
		Plan: request.Plan, SelectedEdges: []ProviderEdgeV1{}, Bundles: []ResolvedBundle{},
		Profiles: []RequirementProfile{}, ValidationEvidence: []ValidationEvidence{},
		PrefixImages: []RealizedImageV1{request.BaseImage}, Materializations: []GraphNodeMaterializeResult{},
		Catalog: append([]RealizedOutput{}, request.BaseCatalog...),
	}
	currentImage := request.BaseImage
	for _, id := range order {
		if id == "base" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return GraphExecutionResult{}, err
		}
		node, _ := providerPlanNode(request.Plan, id)
		validators, err := request.Validators(node)
		if err != nil {
			return GraphExecutionResult{}, fmt.Errorf("provider node %q validators: %w", id, err)
		}
		resolveRequest := ResolveNodeRequest{
			Plan: request.Plan, NodeID: id, EarlierCatalog: append([]RealizedOutput{}, result.Catalog...),
			Platform: request.Platform, Sources: append([]ResolvedSourceInput{}, request.Sources...), Upstream: currentImage,
			ReusableArtifacts: append([]providerstore.StoreObjectRef{}, request.ReusableArtifacts[id]...),
		}
		_, input, err := buildResolveInput(resolveRequest)
		if err != nil {
			return GraphExecutionResult{}, err
		}
		var cached *ResolveResult
		if value, found := request.CachedResolutions[id]; found {
			cached = &value
		}
		prepared, err := request.PrepareNode(ctx, GraphNodePrepareRequest{Resolve: resolveRequest, CachedResolution: cached})
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return GraphExecutionResult{}, ctxErr
			}
			return GraphExecutionResult{}, fmt.Errorf("prepare provider node %q: %w", id, err)
		}
		if err := ctx.Err(); err != nil {
			return GraphExecutionResult{}, err
		}
		if prepared.Refreshed && cached == nil {
			return GraphExecutionResult{}, fmt.Errorf("prepare provider node %q reported a cache refresh without a cached resolution", id)
		}
		resolution := prepared.Resolution
		if err := ValidateResolveResult(input, resolution, validators.Profile, validators.Bundle); err != nil {
			return GraphExecutionResult{}, fmt.Errorf("prepare provider node %q resolution contract: %w", id, err)
		}
		materializeInput := MaterializeInput{
			Bundle: resolution.Bundle, Profile: resolution.Profile, AssemblyParent: currentImage,
			Carrier: prepared.Consumer.Carrier, EnvironmentLauncher: prepared.Consumer.EnvironmentLauncher,
			FinalImageConfig: prepared.Consumer.FinalImageConfig,
		}
		if err := ValidateMaterializeInput(materializeInput, validators.Profile, validators.Bundle); err != nil {
			return GraphExecutionResult{}, fmt.Errorf("materialize provider node %q input: %w", id, err)
		}
		materialized, err := request.MaterializeNode(ctx, GraphNodeMaterializeRequest{Node: node, Input: materializeInput})
		if err != nil {
			return GraphExecutionResult{}, fmt.Errorf("materialize provider node %q: %w", id, err)
		}
		if err := ctx.Err(); err != nil {
			return GraphExecutionResult{}, err
		}
		if err := materialized.Image.Validate(); err != nil {
			return GraphExecutionResult{}, fmt.Errorf("materialize provider node %q image: %w", id, err)
		}
		if err := materialized.TransactionDigest.Validate(); err != nil {
			return GraphExecutionResult{}, fmt.Errorf("materialize provider node %q transaction digest: %w", id, err)
		}
		if err := ValidateRealizedGeneratedExecutableCollection(materialized.GeneratedExecutables); err != nil {
			return GraphExecutionResult{}, fmt.Errorf("materialize provider node %q generated executables: %w", id, err)
		}
		if err := validateRealizedNodeOutputs(resolution.Bundle, materialized.Outputs); err != nil {
			return GraphExecutionResult{}, fmt.Errorf("materialize provider node %q outputs: %w", id, err)
		}
		edges, err := resolvedSelectionEdges(input, resolution)
		if err != nil {
			return GraphExecutionResult{}, fmt.Errorf("execute provider node %q selections: %w", id, err)
		}
		result.SelectedEdges = append(result.SelectedEdges, edges...)
		result.Bundles = append(result.Bundles, resolution.Bundle)
		result.Profiles = append(result.Profiles, resolution.Profile)
		result.ValidationEvidence = append(result.ValidationEvidence, resolution.Evidence)
		result.PrefixImages = append(result.PrefixImages, materialized.Image)
		result.Materializations = append(result.Materializations, materialized)
		result.Catalog = append(result.Catalog, materialized.Outputs...)
		currentImage = materialized.Image
	}
	sort.Slice(result.SelectedEdges, func(left int, right int) bool {
		return compareProviderEdges(result.SelectedEdges[left], result.SelectedEdges[right]) < 0
	})
	for index := 1; index < len(result.SelectedEdges); index++ {
		if compareProviderEdges(result.SelectedEdges[index-1], result.SelectedEdges[index]) >= 0 {
			return GraphExecutionResult{}, fmt.Errorf("provider graph selected edges are not unique")
		}
	}
	return result, nil
}

// ValidateRealizedBundleOutputs binds final exposure evidence to the exact
// public output candidates declared by a resolved bundle.
func ValidateRealizedBundleOutputs(bundle ResolvedBundle, outputs []RealizedOutput) error {
	return validateRealizedNodeOutputs(bundle, outputs)
}

func validateBaseExecutionCatalog(plan ProviderPlanV1, catalog []RealizedOutput) error {
	base, found := providerPlanNode(plan, "base")
	if !found || len(catalog) != len(base.OutputDeclarations) {
		return fmt.Errorf("provider graph base catalog does not match declared outputs")
	}
	for index, declaration := range base.OutputDeclarations {
		output := catalog[index]
		if output.SupplierNode != "base" || output.SupplierComponent != declaration.SupplierComponent || output.Name != declaration.Name || output.Candidate.InvocationPath != declaration.CandidatePath {
			return fmt.Errorf("provider graph base catalog output %d does not match declaration %s.%s", index, declaration.SupplierComponent, declaration.Name)
		}
		matches, err := canonicalValuesEqual(output.Candidate.Provenance, declaration.Provenance)
		if err != nil {
			return err
		}
		if !matches {
			return fmt.Errorf("provider graph base catalog output %d provenance does not match declaration", index)
		}
		if err := validateRealizedCatalogOutput(output); err != nil {
			return fmt.Errorf("provider graph base catalog output %d: %w", index, err)
		}
	}
	return nil
}

func validateRealizedNodeOutputs(bundle ResolvedBundle, outputs []RealizedOutput) error {
	resolved := bundle.Payload.Outputs
	if outputs == nil || len(outputs) != len(resolved) {
		return fmt.Errorf("realized outputs do not match resolved bundle outputs")
	}
	for index, expected := range resolved {
		output := outputs[index]
		if output.SupplierNode != expected.SupplierNode || output.SupplierComponent != expected.SupplierComponent || output.Name != expected.Name {
			return fmt.Errorf("realized output %d does not match resolved identity", index)
		}
		matches, err := canonicalValuesEqual(output.Candidate, expected.Candidate)
		if err != nil {
			return err
		}
		if !matches {
			return fmt.Errorf("realized output %s.%s candidate changed", output.SupplierComponent, output.Name)
		}
		if err := validateRealizedCatalogOutput(output); err != nil {
			return fmt.Errorf("realized output %s.%s: %w", output.SupplierComponent, output.Name, err)
		}
	}
	return nil
}
