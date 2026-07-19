package python

import (
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

// Selection is deployment-local input. It activates optional blueprint
// components and carries direct roots added through the retained bundle UX.
type Selection struct {
	OptionalComponents map[string]bool
	DirectRoots        []string
}

// SelectionFromBundleState bridges the retained deployment-local bundle UX to
// the new component provider request. Blueprint requirements are intentionally
// absent from Roots; they come from active components.
func SelectionFromBundleState(state deploy.BundleState) Selection {
	selection := Selection{OptionalComponents: map[string]bool{}}
	for _, name := range state.SelectedComponents {
		selection.OptionalComponents[name] = true
	}
	for _, root := range state.Roots {
		if root.Provider == ProviderName {
			selection.DirectRoots = append(selection.DirectRoots, root.Source)
		}
	}
	return selection
}

// ResolveRequest projects active Python components into the provider contract.
// It does not resolve packages; that remains the provider's next operation.
func ResolveRequest(document blueprint.Document, selection Selection, platform string, baseImage string) (providers.ResolveRequest, error) {
	if platform == "" {
		return providers.ResolveRequest{}, fmt.Errorf("Python provider platform is required")
	}
	if baseImage == "" {
		return providers.ResolveRequest{}, fmt.Errorf("Python provider base image is required")
	}

	componentNames := make([]string, 0, len(document.Environment.Components))
	for name := range document.Environment.Components {
		componentNames = append(componentNames, name)
	}
	sort.Strings(componentNames)

	knownOptional := map[string]bool{}
	active := map[string]bool{}
	request := providers.ResolveRequest{
		Platform:    platform,
		BaseImage:   baseImage,
		DirectRoots: append([]string(nil), selection.DirectRoots...),
	}
	for _, name := range componentNames {
		component := document.Environment.Components[name]
		if component.Type != blueprint.ComponentTypePython {
			continue
		}
		if component.Optional != nil {
			knownOptional[name] = true
			if !selection.OptionalComponents[name] {
				continue
			}
		}
		active[name] = true
		request.Components = append(request.Components, providers.Component{
			Name:         name,
			Requirements: append([]string(nil), component.Requirements...),
		})
	}
	for name, selected := range selection.OptionalComponents {
		if selected && !knownOptional[name] {
			return providers.ResolveRequest{}, fmt.Errorf("selected Python component %q is not an optional component", name)
		}
	}
	translationNames := make([]string, 0, len(document.Environment.Translations))
	for name := range document.Environment.Translations {
		translationNames = append(translationNames, name)
	}
	sort.Strings(translationNames)
	translationOwners := map[string]string{}
	for _, name := range translationNames {
		translation := document.Environment.Translations[name]
		if translation.Type != blueprint.ComponentTypePython {
			continue
		}
		if translation.Scope != blueprint.TranslationScopeDevelopment {
			return providers.ResolveRequest{}, fmt.Errorf("Python translation %q must have development scope", name)
		}
		mappings := make(map[string]string, len(translation.Mappings))
		for distribution, relativePath := range translation.Mappings {
			normalized := NormalizeDistributionName(distribution)
			if normalized == "" {
				return providers.ResolveRequest{}, fmt.Errorf("Python translation %q has an empty distribution name", name)
			}
			if owner, exists := translationOwners[normalized]; exists {
				return providers.ResolveRequest{}, fmt.Errorf("Python translations %q and %q both map normalized distribution %q", owner, name, normalized)
			}
			translationOwners[normalized] = name
			mappings[normalized] = relativePath
		}
		request.Translations = append(request.Translations, providers.Translation{
			Name: name, Root: translation.Root, Mappings: mappings,
		})
	}

	executableNames := make([]string, 0, len(document.Environment.Executables))
	for name := range document.Environment.Executables {
		executableNames = append(executableNames, name)
	}
	sort.Strings(executableNames)
	for _, name := range executableNames {
		executable := document.Environment.Executables[name]
		component := document.Environment.Components[executable.Component]
		if component.Type != blueprint.ComponentTypePython {
			continue
		}
		if !active[executable.Component] {
			return providers.ResolveRequest{}, fmt.Errorf("executable %q references inactive optional component %q", name, executable.Component)
		}
		request.Executables = append(request.Executables, providers.ExecutableRequest{
			Name: name, Component: executable.Component, Binary: executable.Binary,
		})
	}

	return providers.NormalizeResolveRequest(request), nil
}
