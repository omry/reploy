package deploy

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
)

type PackageRequestParser func(blueprint.ComponentType, string) (providers.CanonicalPackageRequest, error)

// ParseQualifiedOptionGroups parses COMPONENT/OPTION[,OPTION...] arguments
// into sorted, duplicate-free overlay entries.
func ParseQualifiedOptionGroups(arguments []string) ([]QualifiedOption, error) {
	if len(arguments) == 0 {
		return nil, fmt.Errorf("expected at least one component/option selection")
	}
	selected := map[string]QualifiedOption{}
	for _, argument := range arguments {
		if strings.Count(argument, "/") != 1 {
			return nil, fmt.Errorf("option selection %q must use COMPONENT/OPTION[,OPTION...]", argument)
		}
		component, optionList, _ := strings.Cut(argument, "/")
		if err := blueprint.ValidateProviderIdentifier("option component", component); err != nil {
			return nil, err
		}
		options := strings.Split(optionList, ",")
		if len(options) == 0 || optionList == "" {
			return nil, fmt.Errorf("option selection %q has no options", argument)
		}
		for _, option := range options {
			if err := blueprint.ValidateProviderIdentifier("option name", option); err != nil {
				return nil, err
			}
			qualified := QualifiedOption{Component: component, Option: option}
			selected[component+"\x00"+option] = qualified
		}
	}
	result := make([]QualifiedOption, 0, len(selected))
	for _, option := range selected {
		result = append(result, option)
	}
	sort.Slice(result, func(left int, right int) bool {
		if result[left].Component != result[right].Component {
			return result[left].Component < result[right].Component
		}
		return result[left].Option < result[right].Option
	})
	return result, nil
}

func ParseDirectPackageRequests(
	document blueprint.Document,
	componentName string,
	requirements []string,
	parse PackageRequestParser,
) ([]DirectPackageRequest, error) {
	if err := blueprint.ValidateProviderIdentifier("package component", componentName); err != nil {
		return nil, err
	}
	component, exists := document.Environment.Components[componentName]
	if !exists {
		return nil, fmt.Errorf("package component %q does not exist", componentName)
	}
	if component.Type == blueprint.ComponentTypeBase {
		return nil, fmt.Errorf("base component does not support direct package requests")
	}
	if len(requirements) == 0 {
		return nil, fmt.Errorf("expected at least one package requirement")
	}
	if parse == nil {
		return nil, fmt.Errorf("package request parser is unavailable")
	}
	type parsedRequest struct {
		request DirectPackageRequest
		encoded []byte
	}
	unique := map[string]parsedRequest{}
	for _, requirement := range requirements {
		packageRequest, err := parse(component.Type, requirement)
		if err != nil {
			return nil, fmt.Errorf("package requirement %q for component %q: %w", requirement, componentName, err)
		}
		encoded, err := providers.CanonicalPackageRequestBytes(packageRequest)
		if err != nil {
			return nil, fmt.Errorf("package requirement %q for component %q: %w", requirement, componentName, err)
		}
		unique[string(encoded)] = parsedRequest{
			request: DirectPackageRequest{Component: componentName, Package: packageRequest},
			encoded: encoded,
		}
	}
	ordered := make([]parsedRequest, 0, len(unique))
	for _, request := range unique {
		ordered = append(ordered, request)
	}
	sort.Slice(ordered, func(left int, right int) bool {
		if ordered[left].request.Package.Schema != ordered[right].request.Package.Schema {
			return ordered[left].request.Package.Schema < ordered[right].request.Package.Schema
		}
		return bytes.Compare(ordered[left].encoded, ordered[right].encoded) < 0
	})
	result := make([]DirectPackageRequest, 0, len(ordered))
	for _, request := range ordered {
		result = append(result, request.request)
	}
	return result, nil
}

func AddOverlayOptions(overlay RequestOverlayV1, options []QualifiedOption) RequestOverlayV1 {
	result := cloneOverlayCollections(overlay)
	result.SelectedOptions = append(result.SelectedOptions, options...)
	return result
}

func RemoveOverlayOptions(overlay RequestOverlayV1, options []QualifiedOption) (RequestOverlayV1, error) {
	remove := make(map[string]bool, len(options))
	selected := make(map[string]bool, len(overlay.SelectedOptions))
	for _, option := range overlay.SelectedOptions {
		selected[qualifiedOptionKey(option)] = true
	}
	for _, option := range options {
		key := qualifiedOptionKey(option)
		if !selected[key] {
			return RequestOverlayV1{}, fmt.Errorf("component option %s/%s is not selected", option.Component, option.Option)
		}
		remove[key] = true
	}
	result := cloneOverlayCollections(overlay)
	result.SelectedOptions = result.SelectedOptions[:0]
	for _, option := range overlay.SelectedOptions {
		if !remove[qualifiedOptionKey(option)] {
			result.SelectedOptions = append(result.SelectedOptions, option)
		}
	}
	return result, nil
}

func AddOverlayPackages(overlay RequestOverlayV1, packages []DirectPackageRequest) RequestOverlayV1 {
	result := cloneOverlayCollections(overlay)
	result.DirectPackages = append(result.DirectPackages, packages...)
	return result
}

func RemoveOverlayPackages(overlay RequestOverlayV1, packages []DirectPackageRequest) (RequestOverlayV1, error) {
	remove := make(map[string]bool, len(packages))
	selected := make(map[string]bool, len(overlay.DirectPackages))
	for _, request := range overlay.DirectPackages {
		key, err := directPackageRequestKey(request)
		if err != nil {
			return RequestOverlayV1{}, err
		}
		selected[key] = true
	}
	for _, request := range packages {
		key, err := directPackageRequestKey(request)
		if err != nil {
			return RequestOverlayV1{}, err
		}
		if !selected[key] {
			return RequestOverlayV1{}, fmt.Errorf("direct package request is not selected for component %q", request.Component)
		}
		remove[key] = true
	}
	result := cloneOverlayCollections(overlay)
	result.DirectPackages = result.DirectPackages[:0]
	for _, request := range overlay.DirectPackages {
		key, err := directPackageRequestKey(request)
		if err != nil {
			return RequestOverlayV1{}, err
		}
		if !remove[key] {
			result.DirectPackages = append(result.DirectPackages, request)
		}
	}
	return result, nil
}

func cloneOverlayCollections(overlay RequestOverlayV1) RequestOverlayV1 {
	result := overlay
	result.SelectedOptions = append([]QualifiedOption{}, overlay.SelectedOptions...)
	result.DirectPackages = append([]DirectPackageRequest{}, overlay.DirectPackages...)
	return result
}

func qualifiedOptionKey(option QualifiedOption) string {
	return option.Component + "\x00" + option.Option
}

func directPackageRequestKey(request DirectPackageRequest) (string, error) {
	encoded, err := providers.CanonicalPackageRequestBytes(request.Package)
	if err != nil {
		return "", err
	}
	return request.Component + "\x00" + string(encoded), nil
}
