package dockerdeploy

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/apt"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

var errPackageOverrideProjectionMismatch = errors.New("package override projection does not match the completed closure")

func finalizeResolvedRequestV1(
	document blueprint.Document,
	overlay deploy.RequestOverlayV1,
	packageOverrides deploy.PackageOverrideIntentV1,
	candidateRequest providers.ResolvedRequestV1,
	graph providers.GraphExecutionResult,
) (providers.ResolvedRequestV1, deploy.PackageOverrideIntentV1, error) {
	if graph.SelectedSources == nil {
		return providers.ResolvedRequestV1{}, deploy.PackageOverrideIntentV1{}, fmt.Errorf("finalize resolved request selected sources must use an array")
	}
	expectedCandidates, err := BuildResolvedRequestWithPackageOverridesV1(
		document, overlay, packageOverrides, candidateRequest.Platform,
		append([]providers.ResolvedSourceInput{}, candidateRequest.Sources...),
	)
	if err != nil {
		return providers.ResolvedRequestV1{}, deploy.PackageOverrideIntentV1{}, err
	}
	if !reflect.DeepEqual(expectedCandidates, candidateRequest) {
		return providers.ResolvedRequestV1{}, deploy.PackageOverrideIntentV1{}, fmt.Errorf("candidate resolved request does not match the document, overlay, platform, and source candidates")
	}
	relevant, err := relevantPackageOverrideIntentV1(packageOverrides, candidateRequest, graph)
	if err != nil {
		return providers.ResolvedRequestV1{}, deploy.PackageOverrideIntentV1{}, err
	}
	result := candidateRequest
	result.Components = append([]providers.ResolvedComponentRequestV1{}, candidateRequest.Components...)
	result.Sources = append([]providers.ResolvedSourceInput{}, graph.SelectedSources...)
	requestByComponent := make(map[string]providers.CanonicalProviderRequest)
	for _, node := range graph.Plan.Nodes {
		if node.Provider == blueprint.ComponentTypePython && len(node.Components) == 1 {
			requestByComponent[node.Components[0]] = node.Request
		}
	}
	for index := range result.Components {
		if request, found := requestByComponent[result.Components[index].Component]; found {
			result.Components[index].Request = request
		}
	}
	if err := providers.ValidateResolvedRequestV1(result, registry.ValidateResolvedRequestOwnersV1); err != nil {
		return providers.ResolvedRequestV1{}, deploy.PackageOverrideIntentV1{}, err
	}
	planned, err := registry.Plan(providers.PlanInput{Components: result.Components, Platform: result.Platform})
	if err != nil {
		return providers.ResolvedRequestV1{}, deploy.PackageOverrideIntentV1{}, err
	}
	plannedBytes, err := canonical.Marshal(planned)
	if err != nil {
		return providers.ResolvedRequestV1{}, deploy.PackageOverrideIntentV1{}, err
	}
	graphBytes, err := canonical.Marshal(graph.Plan)
	if err != nil {
		return providers.ResolvedRequestV1{}, deploy.PackageOverrideIntentV1{}, err
	}
	if !bytes.Equal(plannedBytes, graphBytes) {
		return providers.ResolvedRequestV1{}, deploy.PackageOverrideIntentV1{}, fmt.Errorf("final provider graph plan does not match its resolved component requests")
	}
	return result, relevant, nil
}

func relevantPackageOverrideIntentV1(
	intent deploy.PackageOverrideIntentV1,
	candidate providers.ResolvedRequestV1,
	graph providers.GraphExecutionResult,
) (deploy.PackageOverrideIntentV1, error) {
	relevant := map[string]map[string]bool{}
	candidates := make(map[string]providers.CanonicalProviderRequest)
	for _, component := range candidate.Components {
		candidates[component.Component] = component.Request
	}
	nodes := make(map[providers.NodeID]providers.NodeSpec)
	for _, node := range graph.Plan.Nodes {
		nodes[node.ID] = node
	}
	for _, bundle := range graph.Bundles {
		if bundle.Payload.Provider != blueprint.ComponentTypePython {
			continue
		}
		node, found := nodes[bundle.Payload.NodeID]
		if !found || len(node.Components) != 1 {
			return deploy.PackageOverrideIntentV1{}, fmt.Errorf("Python bundle %q does not identify one final graph node", bundle.Payload.NodeID)
		}
		component := node.Components[0]
		pythonBundle, err := pythonprovider.DecodeCanonicalBundleDataV1(component, bundle.Payload.ProviderPayload)
		if err != nil {
			return deploy.PackageOverrideIntentV1{}, err
		}
		closure := make([]string, 0, len(pythonBundle.Wheels))
		for _, wheel := range pythonBundle.Wheels {
			closure = append(closure, wheel.Distribution)
			if relevant[string(blueprint.ComponentTypePython)] == nil {
				relevant[string(blueprint.ComponentTypePython)] = map[string]bool{}
			}
			relevant[string(blueprint.ComponentTypePython)][wheel.Distribution] = true
		}
		sort.Strings(closure)
		expected, err := pythonprovider.FilterProviderRequestOverridesV1(candidates[component], closure)
		if err != nil {
			return deploy.PackageOverrideIntentV1{}, err
		}
		matches, err := canonicalProviderRequestsEqualV1(expected, node.Request)
		if err != nil {
			return deploy.PackageOverrideIntentV1{}, err
		}
		if !matches {
			return deploy.PackageOverrideIntentV1{}, fmt.Errorf(
				"%w: Python node %q request does not contain exactly its closure-relevant overrides",
				errPackageOverrideProjectionMismatch, node.ID,
			)
		}
		matches, err = canonicalProviderRequestsEqualV1(node.Request, bundle.Payload.Request)
		if err != nil {
			return deploy.PackageOverrideIntentV1{}, err
		}
		if !matches {
			return deploy.PackageOverrideIntentV1{}, fmt.Errorf("Python node %q request does not match its resolved bundle", node.ID)
		}
	}
	result := deploy.EmptyPackageOverrideIntentV1(intent.EnvironmentID)
	result.Additions = append([]deploy.PackageAdditionIntentV1(nil), intent.Additions...)
	for _, choice := range intent.Choices {
		if relevant[choice.Provider][choice.Package] {
			result.Choices = append(result.Choices, choice)
		}
	}
	if err := deploy.ValidatePackageOverrideIntentV1(result); err != nil {
		return deploy.PackageOverrideIntentV1{}, err
	}
	return result, nil
}

func resolvedRequestForLockedBuildV1(
	document blueprint.Document,
	overlay deploy.RequestOverlayV1,
	packageOverrides deploy.PackageOverrideIntentV1,
	candidateRequest providers.ResolvedRequestV1,
	lockedSources []providers.ResolvedSourceInput,
	lock deploy.BuildLockV1,
	store providerstore.Store,
) (providers.ResolvedRequestV1, deploy.PackageOverrideIntentV1, bool, error) {
	if lockedSources == nil {
		return providers.ResolvedRequestV1{}, deploy.PackageOverrideIntentV1{}, false, fmt.Errorf("locked resolved request sources must use an array")
	}
	if !exactSelectedSourceCandidatesV1(candidateRequest.Sources, lockedSources) {
		return providers.ResolvedRequestV1{}, deploy.PackageOverrideIntentV1{}, false, nil
	}
	plan, err := registry.Plan(providers.PlanInput{
		Components: candidateRequest.Components, Platform: candidateRequest.Platform,
	})
	if err != nil {
		return providers.ResolvedRequestV1{}, deploy.PackageOverrideIntentV1{}, false, err
	}
	nodeIndexes := make(map[providers.NodeID]int, len(plan.Nodes))
	for index, node := range plan.Nodes {
		nodeIndexes[node.ID] = index
	}
	bundles := []providers.ResolvedBundle{}
	for _, lockedNode := range lock.Nodes {
		if lockedNode.Provider != blueprint.ComponentTypePython {
			continue
		}
		index, found := nodeIndexes[lockedNode.NodeID]
		if !found {
			return providers.ResolvedRequestV1{}, deploy.PackageOverrideIntentV1{}, false, nil
		}
		validators, err := registry.OwnerValidatorsForNode(plan.Nodes[index])
		if err != nil {
			return providers.ResolvedRequestV1{}, deploy.PackageOverrideIntentV1{}, false, err
		}
		bundle, err := providers.LoadResolvedBundleManifest(store, lockedNode.BundleManifest, validators.Bundle)
		if err != nil {
			return providers.ResolvedRequestV1{}, deploy.PackageOverrideIntentV1{}, false, err
		}
		plan.Nodes[index].Request = bundle.Payload.Request
		plan.Nodes[index].Requirements.ProviderData = providers.CanonicalProviderData{
			Schema: bundle.Payload.Request.Schema, Value: bundle.Payload.Request.Value,
		}
		bundles = append(bundles, bundle)
	}
	request, relevant, err := finalizeResolvedRequestV1(
		document, overlay, packageOverrides, candidateRequest,
		providers.GraphExecutionResult{
			Plan: plan, Bundles: bundles,
			SelectedSources: append([]providers.ResolvedSourceInput{}, lockedSources...),
		},
	)
	if errors.Is(err, errPackageOverrideProjectionMismatch) {
		return providers.ResolvedRequestV1{}, deploy.PackageOverrideIntentV1{}, false, nil
	}
	if err != nil {
		return providers.ResolvedRequestV1{}, deploy.PackageOverrideIntentV1{}, false, err
	}
	return request, relevant, true, nil
}

func canonicalProviderRequestsEqualV1(
	left providers.CanonicalProviderRequest,
	right providers.CanonicalProviderRequest,
) (bool, error) {
	leftDigest, err := providers.ProviderRequestDigest(left)
	if err != nil {
		return false, err
	}
	rightDigest, err := providers.ProviderRequestDigest(right)
	if err != nil {
		return false, err
	}
	return leftDigest == rightDigest, nil
}

func resolvedRequestForLockedSourcesV1(
	document blueprint.Document,
	overlay deploy.RequestOverlayV1,
	packageOverrides deploy.PackageOverrideIntentV1,
	candidateRequest providers.ResolvedRequestV1,
	lockedSources []providers.ResolvedSourceInput,
) (providers.ResolvedRequestV1, bool, error) {
	if lockedSources == nil {
		return providers.ResolvedRequestV1{}, false, fmt.Errorf("locked resolved request sources must use an array")
	}
	if !exactSelectedSourceCandidatesV1(candidateRequest.Sources, lockedSources) {
		return providers.ResolvedRequestV1{}, false, nil
	}
	request, err := BuildResolvedRequestWithPackageOverridesV1(
		document, overlay, packageOverrides, candidateRequest.Platform,
		append([]providers.ResolvedSourceInput{}, lockedSources...),
	)
	if err != nil {
		return providers.ResolvedRequestV1{}, false, err
	}
	return request, true, nil
}

func exactSelectedSourceCandidatesV1(
	candidates []providers.ResolvedSourceInput,
	selected []providers.ResolvedSourceInput,
) bool {
	if candidates == nil || selected == nil {
		return false
	}
	byKey := make(map[string]providers.ResolvedSourceInput, len(candidates))
	for _, source := range candidates {
		byKey[source.Component+"\x00"+source.LogicalPackage] = source
	}
	for _, source := range selected {
		candidate, found := byKey[source.Component+"\x00"+source.LogicalPackage]
		if !found || !reflect.DeepEqual(candidate, source) {
			return false
		}
	}
	return true
}

func BuildResolvedRequestV1(
	document blueprint.Document,
	overlay deploy.RequestOverlayV1,
	platform blueprint.Platform,
	sources []providers.ResolvedSourceInput,
) (providers.ResolvedRequestV1, error) {
	return BuildResolvedRequestWithPackageOverridesV1(
		document, overlay, deploy.EmptyPackageOverrideIntentV1(document.Environment.ID), platform, sources,
	)
}

func BuildResolvedRequestWithPackageOverridesV1(
	document blueprint.Document,
	overlay deploy.RequestOverlayV1,
	packageOverrides deploy.PackageOverrideIntentV1,
	platform blueprint.Platform,
	sources []providers.ResolvedSourceInput,
) (providers.ResolvedRequestV1, error) {
	if err := platform.Validate(); err != nil {
		return providers.ResolvedRequestV1{}, fmt.Errorf("resolved request platform: %w", err)
	}
	if err := blueprint.ValidateSelectedPlatform(document, platform); err != nil {
		return providers.ResolvedRequestV1{}, fmt.Errorf("resolved request platform: %w", err)
	}
	if err := deploy.ValidatePackageOverrideIntentV1(packageOverrides); err != nil {
		return providers.ResolvedRequestV1{}, err
	}
	if packageOverrides.EnvironmentID != document.Environment.ID {
		return providers.ResolvedRequestV1{}, fmt.Errorf(
			"package overrides target environment %q, want %q",
			packageOverrides.EnvironmentID, document.Environment.ID,
		)
	}
	if err := rejectUnplannedRuntimePortableToolsV1(document); err != nil {
		return providers.ResolvedRequestV1{}, err
	}
	document, err := documentWithPackageAdditionsV1(document, packageOverrides)
	if err != nil {
		return providers.ResolvedRequestV1{}, err
	}
	normalizedOverlay, err := deploy.NormalizeRequestOverlayV1(document, overlay, registry.ValidatePackageRequest)
	if err != nil {
		return providers.ResolvedRequestV1{}, err
	}
	overlayDigest, err := deploy.RequestOverlayDigestV1(normalizedOverlay)
	if err != nil {
		return providers.ResolvedRequestV1{}, err
	}
	selectedOptions := make(map[string]map[string]bool)
	for _, option := range normalizedOverlay.SelectedOptions {
		application := document.Environment.Applications[option.Application]
		selected := application.Options[option.Option]
		contributions := []string{}
		if len(selected.Packages.OS) != 0 {
			contributions = append(contributions, blueprint.ApplicationContributionID(option.Application, blueprint.ContributionProviderOS))
		}
		if selected.Packages.Python != nil {
			contributions = append(contributions, blueprint.ApplicationContributionID(option.Application, blueprint.ContributionProviderPython))
		}
		for _, contribution := range contributions {
			if selectedOptions[contribution] == nil {
				selectedOptions[contribution] = map[string]bool{}
			}
			selectedOptions[contribution][option.Option] = true
		}
	}
	directPackages := make(map[string][]providers.CanonicalPackageRequest)
	for _, request := range normalizedOverlay.DirectPackages {
		directPackages[request.Contribution] = append(directPackages[request.Contribution], request.Package)
	}
	implicitBasePython := resolvedRequestNeedsImplicitBasePython(document, selectedOptions, directPackages)
	pythonOverrides := pythonPackageOverridesV1(packageOverrides)

	names := make([]string, 0, len(document.Environment.Components))
	for name := range document.Environment.Components {
		names = append(names, name)
	}
	sort.Strings(names)
	components := make([]providers.ResolvedComponentRequestV1, 0, len(names))
	for _, name := range names {
		component := document.Environment.Components[name]
		var request providers.CanonicalProviderRequest
		switch component.Type {
		case blueprint.ComponentTypeBase:
			if name != "base" || component.Base == nil {
				return providers.ResolvedRequestV1{}, fmt.Errorf("base component is missing its typed payload")
			}
			exports := component.Base.Exports
			if implicitBasePython {
				exports = make(map[string]blueprint.BaseExecutableExport, len(component.Base.Exports)+1)
				for exportName, export := range component.Base.Exports {
					exports[exportName] = export
				}
				if _, explicit := exports["python"]; !explicit {
					exports["python"] = blueprint.BaseExecutableExport{Executable: "/usr/local/bin/python"}
				}
			}
			request, err = providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{
				Image: component.Base.Image, Exports: exports,
			})
		case blueprint.ComponentTypePython:
			request, err = buildPythonComponentRequest(name, component, selectedOptions[name], directPackages[name], pythonOverrides)
		case blueprint.ComponentTypeAPT:
			request, err = buildAPTComponentRequest(name, component, selectedOptions[name], directPackages[name])
		default:
			return providers.ResolvedRequestV1{}, fmt.Errorf("component %q has unsupported type %q", name, component.Type)
		}
		if err != nil {
			return providers.ResolvedRequestV1{}, fmt.Errorf("resolve component %q: %w", name, err)
		}
		if request.Schema == "" {
			continue
		}
		components = append(components, providers.ResolvedComponentRequestV1{
			Component: name, Provider: component.Type, Request: request,
		})
	}
	result := providers.ResolvedRequestV1{
		Schema: providers.ResolvedRequestSchemaV1, OverlayDigest: overlayDigest,
		Platform: platform, Components: components, Sources: append([]providers.ResolvedSourceInput{}, sources...),
	}
	if err := providers.ValidateResolvedRequestV1(result, registry.ValidateResolvedRequestOwnersV1); err != nil {
		return providers.ResolvedRequestV1{}, err
	}
	return result, nil
}

func rejectUnplannedRuntimePortableToolsV1(document blueprint.Document) error {
	applicationNames := make([]string, 0, len(document.Environment.Applications))
	for applicationName := range document.Environment.Applications {
		applicationNames = append(applicationNames, applicationName)
	}
	sort.Strings(applicationNames)
	for _, applicationName := range applicationNames {
		requirements := document.Environment.Applications[applicationName].Packages.Tools
		if len(requirements) == 0 {
			continue
		}
		toolNames := make([]string, 0, len(requirements))
		for _, requirement := range requirements {
			toolNames = append(toolNames, requirement.Tool)
		}
		sort.Strings(toolNames)
		return fmt.Errorf(
			"application %q requests runtime portable tools %v, but catalog-backed provider planning is not yet available",
			applicationName,
			toolNames,
		)
	}
	return nil
}

func documentWithPackageAdditionsV1(
	document blueprint.Document,
	intent deploy.PackageOverrideIntentV1,
) (blueprint.Document, error) {
	additions := intent.AdditionsForProvider("os")
	if len(additions) == 0 {
		return document, nil
	}
	packages := make([]blueprint.APTPackageRequest, 0, len(additions))
	for _, addition := range additions {
		request, err := blueprint.ParseAPTPackageRequest(addition.Requirement)
		if err != nil {
			return blueprint.Document{}, fmt.Errorf("resolve OS package addition %q: %w", addition.Requirement, err)
		}
		packages = append(packages, request)
	}

	document.Environment.Packages.OS = append(
		append([]blueprint.APTPackageRequest{}, document.Environment.Packages.OS...),
		packages...,
	)
	if err := document.Environment.RebuildProviderContributions(); err != nil {
		return blueprint.Document{}, err
	}
	return document, nil
}

func BuildResolvedRequestWithOverridesV1(
	document blueprint.Document,
	overlay deploy.RequestOverlayV1,
	packageOverrides deploy.PackageOverrideIntentV1,
	baseImage string,
	platform blueprint.Platform,
	sources []providers.ResolvedSourceInput,
) (providers.ResolvedRequestV1, error) {
	if baseImage == "" {
		return BuildResolvedRequestWithPackageOverridesV1(
			document, overlay, packageOverrides, platform, sources,
		)
	}
	if err := deploy.ValidateBaseImageReferenceV1(baseImage); err != nil {
		return providers.ResolvedRequestV1{}, fmt.Errorf("base image override: %w", err)
	}
	document.Environment.Base.Image = baseImage
	if err := document.Environment.RebuildProviderContributions(); err != nil {
		return providers.ResolvedRequestV1{}, fmt.Errorf("base image override: %w", err)
	}
	return BuildResolvedRequestWithPackageOverridesV1(
		document, overlay, packageOverrides, platform, sources,
	)
}

func resolvedRequestNeedsImplicitBasePython(
	document blueprint.Document,
	selected map[string]map[string]bool,
	direct map[string][]providers.CanonicalPackageRequest,
) bool {
	base := document.Environment.Base
	if base.Image == "" {
		return false
	}
	if _, explicit := base.Exports["python"]; explicit {
		return false
	}
	for name, component := range document.Environment.Components {
		if component.Type != blueprint.ComponentTypePython || component.Python == nil {
			continue
		}
		interpreter := component.Python.Interpreter
		if interpreter.Command != "python" || interpreter.Supplier != "" {
			continue
		}
		active := len(component.Python.Requirements) != 0 || len(direct[name]) != 0
		for option := range selected[name] {
			if declared, found := component.Options[option]; found && len(declared.PythonRequirements) != 0 {
				active = true
			}
		}
		if active {
			return true
		}
	}
	return false
}

func buildPythonComponentRequest(
	name string,
	component blueprint.Component,
	selected map[string]bool,
	direct []providers.CanonicalPackageRequest,
	overrides []pythonprovider.PythonPackageOverrideV1,
) (providers.CanonicalProviderRequest, error) {
	if component.Python == nil {
		return providers.CanonicalProviderRequest{}, fmt.Errorf("Python component has no typed payload")
	}
	packages := make([]providers.CanonicalPackageRequest, 0, len(component.Python.Requirements)+len(direct))
	for _, requirement := range component.Python.Requirements {
		request, err := pythonprovider.CanonicalPackageRequestV1(requirement)
		if err != nil {
			return providers.CanonicalProviderRequest{}, err
		}
		packages = append(packages, request)
	}
	for optionName := range selected {
		for _, requirement := range component.Options[optionName].PythonRequirements {
			request, err := pythonprovider.CanonicalPackageRequestV1(requirement)
			if err != nil {
				return providers.CanonicalProviderRequest{}, err
			}
			packages = append(packages, request)
		}
	}
	packages = append(packages, direct...)
	if len(packages) == 0 {
		return providers.CanonicalProviderRequest{}, nil
	}
	return pythonprovider.CanonicalProviderRequestV1(pythonprovider.PythonProviderRequestV1{
		Component: name, Interpreter: component.Python.Interpreter, Requirements: packages,
		Overrides: append([]pythonprovider.PythonPackageOverrideV1{}, overrides...),
	})
}

func pythonPackageOverridesV1(intent deploy.PackageOverrideIntentV1) []pythonprovider.PythonPackageOverrideV1 {
	choices := intent.ChoicesForProvider(string(blueprint.ComponentTypePython))
	result := make([]pythonprovider.PythonPackageOverrideV1, 0, len(choices))
	for _, choice := range choices {
		result = append(result, pythonprovider.PythonPackageOverrideV1{
			Distribution: choice.Package, Kind: choice.Kind, Version: choice.Version,
		})
	}
	return result
}

func buildAPTComponentRequest(
	name string,
	component blueprint.Component,
	selected map[string]bool,
	direct []providers.CanonicalPackageRequest,
) (providers.CanonicalProviderRequest, error) {
	if component.APT == nil {
		return providers.CanonicalProviderRequest{}, fmt.Errorf("APT component has no typed payload")
	}
	packages := append([]blueprint.APTPackageRequest{}, component.APT.Packages...)
	for optionName := range selected {
		packages = append(packages, component.Options[optionName].APTPackages...)
	}
	for _, request := range direct {
		decoded, err := apt.DecodeCanonicalPackageRequestV1(request)
		if err != nil {
			return providers.CanonicalProviderRequest{}, err
		}
		packages = append(packages, decoded)
	}
	if len(packages) == 0 {
		return providers.CanonicalProviderRequest{}, nil
	}
	return apt.CanonicalProviderRequestV1(apt.APTProviderRequestV1{Components: []apt.APTComponentRequestV1{{
		Component: name, Packages: packages,
	}}})
}
