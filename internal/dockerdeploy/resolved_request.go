package dockerdeploy

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/apt"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
)

func finalizeResolvedRequestV1(
	document blueprint.Document,
	overlay deploy.RequestOverlayV1,
	candidateRequest providers.ResolvedRequestV1,
	selected []providers.ResolvedSourceInput,
) (providers.ResolvedRequestV1, error) {
	if selected == nil {
		return providers.ResolvedRequestV1{}, fmt.Errorf("finalize resolved request selected sources must use an array")
	}
	expectedCandidates, err := BuildResolvedRequestV1(
		document, overlay, candidateRequest.Platform,
		append([]providers.ResolvedSourceInput{}, candidateRequest.Sources...),
	)
	if err != nil {
		return providers.ResolvedRequestV1{}, err
	}
	if !reflect.DeepEqual(expectedCandidates, candidateRequest) {
		return providers.ResolvedRequestV1{}, fmt.Errorf("candidate resolved request does not match the document, overlay, platform, and source candidates")
	}
	// Source candidates in the loaded request are reusable identities known
	// before provider execution. Fresh workspace wheels do not have a complete
	// identity until the node preparation builds and inspects them. The provider
	// graph validates its selected sources against that node's effective,
	// post-build candidate list; finalization records exactly those selections.
	return BuildResolvedRequestV1(
		document, overlay, candidateRequest.Platform,
		append([]providers.ResolvedSourceInput{}, selected...),
	)
}

func resolvedRequestForLockedSourcesV1(
	document blueprint.Document,
	overlay deploy.RequestOverlayV1,
	candidateRequest providers.ResolvedRequestV1,
	lockedSources []providers.ResolvedSourceInput,
) (providers.ResolvedRequestV1, bool, error) {
	if lockedSources == nil {
		return providers.ResolvedRequestV1{}, false, fmt.Errorf("locked resolved request sources must use an array")
	}
	if !exactSelectedSourceCandidatesV1(candidateRequest.Sources, lockedSources) {
		return providers.ResolvedRequestV1{}, false, nil
	}
	request, err := BuildResolvedRequestV1(
		document, overlay, candidateRequest.Platform,
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
	if err := platform.Validate(); err != nil {
		return providers.ResolvedRequestV1{}, fmt.Errorf("resolved request platform: %w", err)
	}
	if err := blueprint.ValidateSelectedPlatform(document, platform); err != nil {
		return providers.ResolvedRequestV1{}, fmt.Errorf("resolved request platform: %w", err)
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
		if selectedOptions[option.Component] == nil {
			selectedOptions[option.Component] = map[string]bool{}
		}
		selectedOptions[option.Component][option.Option] = true
	}
	directPackages := make(map[string][]providers.CanonicalPackageRequest)
	for _, request := range normalizedOverlay.DirectPackages {
		directPackages[request.Component] = append(directPackages[request.Component], request.Package)
	}
	implicitBasePython := resolvedRequestNeedsImplicitBasePython(document, selectedOptions, directPackages)

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
			request, err = buildPythonComponentRequest(name, component, selectedOptions[name], directPackages[name])
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

func resolvedRequestNeedsImplicitBasePython(
	document blueprint.Document,
	selected map[string]map[string]bool,
	direct map[string][]providers.CanonicalPackageRequest,
) bool {
	base := document.Environment.Components["base"]
	if base.Base == nil {
		return false
	}
	if _, explicit := base.Base.Exports["python"]; explicit {
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
	})
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
