package providers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providerstore"
)

type ResolveInput struct {
	Node              NodeSpec
	Candidates        []RequirementCandidatesV1
	Platform          blueprint.Platform
	Sources           []ResolvedSourceInput
	Upstream          RealizedImageV1
	ReusableArtifacts []providerstore.StoreObjectRef
}

type ResolveResult struct {
	Bundle   ResolvedBundle
	Profile  RequirementProfile
	Evidence ValidationEvidence
}

type MaterializeInput struct {
	Bundle         ResolvedBundle
	Profile        RequirementProfile
	AssemblyParent RealizedImageV1
}

type ArtifactSink interface {
	Publish(context.Context, string, string, io.Reader) (providerstore.ArtifactDescriptor, error)
}

func ValidateResolveInput(input ResolveInput) error {
	if err := ValidateNodeSpec(input.Node); err != nil {
		return fmt.Errorf("resolve input node: %w", err)
	}
	if input.Node.ID == "base" {
		return fmt.Errorf("base root does not have a provider resolution operation")
	}
	if input.Candidates == nil || input.Sources == nil || input.ReusableArtifacts == nil {
		return fmt.Errorf("resolve input candidates, sources, and reusable artifacts must use arrays")
	}
	if len(input.Candidates) != len(input.Node.Requirements.Executables) {
		return fmt.Errorf("resolve input candidate groups do not match executable requirements")
	}
	for index, requirement := range input.Node.Requirements.Executables {
		group := input.Candidates[index]
		if group.RequirementID != requirement.ID || group.Outputs == nil {
			return fmt.Errorf("resolve input candidate group %d does not match requirement %q", index, requirement.ID)
		}
		seen := map[string]bool{}
		for outputIndex, output := range group.Outputs {
			if err := validateRealizedCatalogOutput(output); err != nil {
				return fmt.Errorf("resolve input candidate %d for requirement %q: %w", outputIndex, requirement.ID, err)
			}
			if output.Name != requirement.Command || requirement.Supplier != "" && output.SupplierComponent != requirement.Supplier {
				return fmt.Errorf("resolve input candidate %s.%s does not match requirement %q", output.SupplierComponent, output.Name, requirement.ID)
			}
			key := string(output.SupplierNode) + "\x00" + output.SupplierComponent + "\x00" + output.Name
			if seen[key] {
				return fmt.Errorf("resolve input requirement %q contains duplicate candidate %s.%s", requirement.ID, output.SupplierComponent, output.Name)
			}
			seen[key] = true
		}
	}
	if err := input.Platform.Validate(); err != nil {
		return fmt.Errorf("resolve input platform: %w", err)
	}
	components := make(map[string]bool, len(input.Node.Components))
	for _, component := range input.Node.Components {
		components[component] = true
	}
	for index, source := range input.Sources {
		if index > 0 && compareResolvedSourceInputs(input.Sources[index-1], source) >= 0 {
			return fmt.Errorf("resolve input sources must be unique and sorted by component and logical package")
		}
		if err := ValidateResolvedSourceInput(source); err != nil {
			return fmt.Errorf("resolve input source %d: %w", index, err)
		}
		if !components[source.Component] {
			return fmt.Errorf("resolve input source %d targets component %q outside node %q", index, source.Component, input.Node.ID)
		}
	}
	if err := input.Upstream.Validate(); err != nil {
		return fmt.Errorf("resolve input upstream: %w", err)
	}
	for index, reference := range input.ReusableArtifacts {
		if err := reference.Validate(); err != nil {
			return fmt.Errorf("resolve input reusable artifact %d: %w", index, err)
		}
		if index > 0 && compareStoreObjectRefs(input.ReusableArtifacts[index-1], reference) >= 0 {
			return fmt.Errorf("resolve input reusable artifacts must be unique and sorted by kind and digest")
		}
	}
	return nil
}

func ValidateResolveResult(
	input ResolveInput,
	result ResolveResult,
	validateProfileOwner RequirementProfileOwnerValidator,
	validateBundleOwner ResolvedBundleOwnerValidator,
) error {
	if err := ValidateResolveInput(input); err != nil {
		return err
	}
	profileDigest, err := RequirementProfileDigest(result.Profile, validateProfileOwner)
	if err != nil {
		return fmt.Errorf("resolve result profile: %w", err)
	}
	declarationMatches, err := canonicalValuesEqual(input.Node.Requirements, result.Profile.Declaration)
	if err != nil {
		return err
	}
	if !declarationMatches {
		return fmt.Errorf("resolve result profile declaration does not match node requirements")
	}
	if result.Profile.Platform != input.Platform {
		return fmt.Errorf("resolve result profile platform does not match resolve input platform")
	}
	if _, err := matchResolveResultSelections(input, result); err != nil {
		return err
	}
	if err := result.Evidence.Validate(); err != nil {
		return fmt.Errorf("resolve result evidence: %w", err)
	}
	if result.Evidence.SubjectRootFS != input.Upstream.RootFSSubject || result.Evidence.ProfileDigest != profileDigest {
		return fmt.Errorf("resolve result evidence does not bind the input upstream and requirement profile")
	}
	if err := ValidateResolvedBundle(result.Bundle, validateBundleOwner); err != nil {
		return fmt.Errorf("resolve result bundle: %w", err)
	}
	payload := result.Bundle.Payload
	if payload.NodeID != input.Node.ID || payload.Provider != input.Node.Provider {
		return fmt.Errorf("resolve result bundle does not identify the input node")
	}
	requestMatches, err := canonicalValuesEqual(payload.Request, input.Node.Request)
	if err != nil {
		return err
	}
	if !requestMatches {
		return fmt.Errorf("resolve result bundle request does not match the input node")
	}
	if payload.RequirementProfileDigest != profileDigest {
		return fmt.Errorf("resolve result bundle does not bind the requirement profile")
	}
	if payload.Platform != input.Platform {
		return fmt.Errorf("resolve result bundle platform does not match resolve input platform")
	}
	upstreamMatches, err := canonicalValuesEqual(payload.Upstream, input.Upstream)
	if err != nil {
		return err
	}
	if !upstreamMatches {
		return fmt.Errorf("resolve result bundle upstream does not match resolve input upstream")
	}
	return nil
}

func matchResolveResultSelections(input ResolveInput, result ResolveResult) ([]FrozenExecutableSelection, error) {
	if len(result.Profile.SelectedExecutables) != len(input.Node.Requirements.Executables) {
		return nil, fmt.Errorf("resolve result selections do not match executable requirements")
	}
	selections := make([]FrozenExecutableSelection, 0, len(result.Profile.SelectedExecutables))
	for index, requirement := range input.Node.Requirements.Executables {
		evidence := result.Profile.SelectedExecutables[index]
		group := input.Candidates[index]
		var selected *RealizedOutput
		for candidateIndex := range group.Outputs {
			candidate := &group.Outputs[candidateIndex]
			if evidence.Output != (QualifiedOutput{Component: candidate.SupplierComponent, Name: candidate.Name}) || evidence.InvocationPath != candidate.Candidate.InvocationPath {
				continue
			}
			if selected != nil {
				return nil, fmt.Errorf("resolve result requirement %q matches multiple input candidates", requirement.ID)
			}
			selected = candidate
		}
		if selected == nil {
			return nil, fmt.Errorf("resolve result selection for requirement %q does not match an input candidate", requirement.ID)
		}
		if err := validateFrozenEvidence(requirement, *selected, evidence); err != nil {
			return nil, err
		}
		selections = append(selections, FrozenExecutableSelection{
			RequirementID: requirement.ID, Output: *selected, Evidence: evidence,
		})
	}
	return selections, nil
}

func resolvedSelectionEdges(input ResolveInput, result ResolveResult) ([]ProviderEdgeV1, error) {
	selections, err := matchResolveResultSelections(input, result)
	if err != nil {
		return nil, err
	}
	edges := make([]ProviderEdgeV1, 0, len(selections))
	for _, selection := range selections {
		edges = append(edges, ProviderEdgeV1{
			Supplier: selection.Output.SupplierNode, Consumer: input.Node.ID,
			RequirementID: selection.RequirementID,
			Output:        QualifiedOutput{Component: selection.Output.SupplierComponent, Name: selection.Output.Name},
		})
	}
	sort.Slice(edges, func(left int, right int) bool { return compareProviderEdges(edges[left], edges[right]) < 0 })
	return edges, nil
}

func ValidateMaterializeInput(
	input MaterializeInput,
	validateProfileOwner RequirementProfileOwnerValidator,
	validateBundleOwner ResolvedBundleOwnerValidator,
) error {
	profileDigest, err := RequirementProfileDigest(input.Profile, validateProfileOwner)
	if err != nil {
		return fmt.Errorf("materialize input profile: %w", err)
	}
	if err := ValidateResolvedBundle(input.Bundle, validateBundleOwner); err != nil {
		return fmt.Errorf("materialize input bundle: %w", err)
	}
	if input.Bundle.Payload.RequirementProfileDigest != profileDigest {
		return fmt.Errorf("materialize input bundle does not bind the requirement profile")
	}
	if input.Bundle.Payload.Platform != input.Profile.Platform {
		return fmt.Errorf("materialize input bundle and profile platforms differ")
	}
	if err := input.AssemblyParent.Validate(); err != nil {
		return fmt.Errorf("materialize input assembly parent: %w", err)
	}
	return nil
}

func compareStoreObjectRefs(left providerstore.StoreObjectRef, right providerstore.StoreObjectRef) int {
	if left.Kind < right.Kind {
		return -1
	}
	if left.Kind > right.Kind {
		return 1
	}
	if left.Digest < right.Digest {
		return -1
	}
	if left.Digest > right.Digest {
		return 1
	}
	return 0
}

func canonicalValuesEqual(left any, right any) (bool, error) {
	leftBytes, err := canonical.Marshal(left)
	if err != nil {
		return false, fmt.Errorf("canonical comparison left value: %w", err)
	}
	rightBytes, err := canonical.Marshal(right)
	if err != nil {
		return false, fmt.Errorf("canonical comparison right value: %w", err)
	}
	return bytes.Equal(leftBytes, rightBytes), nil
}
