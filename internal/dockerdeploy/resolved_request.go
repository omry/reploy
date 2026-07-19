package dockerdeploy

import (
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/apt"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
)

func BuildResolvedRequestV1(
	document blueprint.Document,
	overlay deploy.RequestOverlayV1,
	blueprintDigest canonical.Digest,
	platform blueprint.Platform,
	sources []providers.ResolvedSourceInput,
) (providers.ResolvedRequestV1, error) {
	if err := blueprintDigest.Validate(); err != nil {
		return providers.ResolvedRequestV1{}, fmt.Errorf("resolved request blueprint digest: %w", err)
	}
	if err := platform.Validate(); err != nil {
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
			request, err = providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{
				Image: component.Base.Image, Exports: component.Base.Exports,
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
		Schema: providers.ResolvedRequestSchemaV1, BlueprintDigest: blueprintDigest, OverlayDigest: overlayDigest,
		Platform: platform, Components: components, Sources: append([]providers.ResolvedSourceInput{}, sources...),
	}
	if err := providers.ValidateResolvedRequestV1(result, registry.ValidateResolvedRequestOwnersV1); err != nil {
		return providers.ResolvedRequestV1{}, err
	}
	return result, nil
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
