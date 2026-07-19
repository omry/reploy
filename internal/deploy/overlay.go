package deploy

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

const RequestOverlaySchemaV1 = "overlay-v1"

type RequestOverlayV1 struct {
	Schema          string                 `json:"schema"`
	SelectedOptions []QualifiedOption      `json:"selected_options"`
	DirectPackages  []DirectPackageRequest `json:"direct_packages"`
}

type QualifiedOption struct {
	Component string `json:"component"`
	Option    string `json:"option"`
}

type DirectPackageRequest struct {
	Component string                            `json:"component"`
	Package   providers.CanonicalPackageRequest `json:"package"`
}

// PackageRequestValidator is supplied by the provider registry. It binds the
// target component type to an exact provider-owned package schema and value.
type PackageRequestValidator func(blueprint.ComponentType, providers.CanonicalPackageRequest) error

func EmptyRequestOverlayV1() RequestOverlayV1 {
	return RequestOverlayV1{
		Schema:          RequestOverlaySchemaV1,
		SelectedOptions: []QualifiedOption{},
		DirectPackages:  []DirectPackageRequest{},
	}
}

// NormalizeRequestOverlayV1 validates deployment intent against the resolved
// blueprint, removes exact duplicates, and returns the sole canonical order.
func NormalizeRequestOverlayV1(document blueprint.Document, overlay RequestOverlayV1, validatePackage PackageRequestValidator) (RequestOverlayV1, error) {
	if overlay.Schema == "" && overlay.SelectedOptions == nil && overlay.DirectPackages == nil {
		overlay = EmptyRequestOverlayV1()
	}
	if overlay.Schema != RequestOverlaySchemaV1 {
		return RequestOverlayV1{}, fmt.Errorf("request overlay schema must be %q", RequestOverlaySchemaV1)
	}

	normalized := EmptyRequestOverlayV1()
	selected := map[string]QualifiedOption{}
	for _, option := range overlay.SelectedOptions {
		component, exists := document.Environment.Components[option.Component]
		if !exists {
			return RequestOverlayV1{}, fmt.Errorf("request overlay option targets missing component %q", option.Component)
		}
		if component.Type == blueprint.ComponentTypeBase {
			return RequestOverlayV1{}, fmt.Errorf("base component does not support options")
		}
		if _, exists := component.Options[option.Option]; !exists {
			return RequestOverlayV1{}, fmt.Errorf("request overlay selects missing option %q on component %q", option.Option, option.Component)
		}
		key := option.Component + "\x00" + option.Option
		selected[key] = option
	}
	for _, option := range selected {
		normalized.SelectedOptions = append(normalized.SelectedOptions, option)
	}
	sort.Slice(normalized.SelectedOptions, func(left int, right int) bool {
		if normalized.SelectedOptions[left].Component != normalized.SelectedOptions[right].Component {
			return normalized.SelectedOptions[left].Component < normalized.SelectedOptions[right].Component
		}
		return normalized.SelectedOptions[left].Option < normalized.SelectedOptions[right].Option
	})

	type canonicalDirectPackage struct {
		request DirectPackageRequest
		encoded []byte
	}
	direct := map[string]canonicalDirectPackage{}
	for _, request := range overlay.DirectPackages {
		component, exists := document.Environment.Components[request.Component]
		if !exists {
			return RequestOverlayV1{}, fmt.Errorf("direct package request targets missing component %q", request.Component)
		}
		if component.Type == blueprint.ComponentTypeBase {
			return RequestOverlayV1{}, fmt.Errorf("base component does not support direct package requests")
		}
		if validatePackage == nil {
			return RequestOverlayV1{}, fmt.Errorf("direct package request validation is unavailable")
		}
		if err := validatePackage(component.Type, request.Package); err != nil {
			return RequestOverlayV1{}, fmt.Errorf("direct package request for component %q: %w", request.Component, err)
		}
		encoded, err := providers.CanonicalPackageRequestBytes(request.Package)
		if err != nil {
			return RequestOverlayV1{}, fmt.Errorf("direct package request for component %q: %w", request.Component, err)
		}
		key := request.Component + "\x00" + string(encoded)
		direct[key] = canonicalDirectPackage{request: request, encoded: encoded}
	}
	ordered := make([]canonicalDirectPackage, 0, len(direct))
	for _, request := range direct {
		ordered = append(ordered, request)
	}
	sort.Slice(ordered, func(left int, right int) bool {
		if ordered[left].request.Component != ordered[right].request.Component {
			return ordered[left].request.Component < ordered[right].request.Component
		}
		if ordered[left].request.Package.Schema != ordered[right].request.Package.Schema {
			return ordered[left].request.Package.Schema < ordered[right].request.Package.Schema
		}
		return bytes.Compare(ordered[left].encoded, ordered[right].encoded) < 0
	})
	for _, request := range ordered {
		normalized.DirectPackages = append(normalized.DirectPackages, request.request)
	}
	return normalized, nil
}

// RequestOverlayDigestV1 rejects noncanonical ordering before hashing so two
// spellings cannot acquire distinct identities for the same overlay intent.
func RequestOverlayDigestV1(overlay RequestOverlayV1) (canonical.Digest, error) {
	if overlay.Schema != RequestOverlaySchemaV1 {
		return "", fmt.Errorf("request overlay schema must be %q", RequestOverlaySchemaV1)
	}
	if overlay.SelectedOptions == nil || overlay.DirectPackages == nil {
		return "", fmt.Errorf("request overlay collections must be arrays")
	}
	if !sort.SliceIsSorted(overlay.SelectedOptions, func(left int, right int) bool {
		if overlay.SelectedOptions[left].Component != overlay.SelectedOptions[right].Component {
			return overlay.SelectedOptions[left].Component < overlay.SelectedOptions[right].Component
		}
		return overlay.SelectedOptions[left].Option < overlay.SelectedOptions[right].Option
	}) {
		return "", fmt.Errorf("request overlay selected options are not canonically ordered")
	}
	for index := 1; index < len(overlay.SelectedOptions); index++ {
		if overlay.SelectedOptions[index] == overlay.SelectedOptions[index-1] {
			return "", fmt.Errorf("request overlay contains duplicate selected option")
		}
	}

	var previousComponent string
	var previousSchema string
	var previousBytes []byte
	for index, request := range overlay.DirectPackages {
		encoded, err := providers.CanonicalPackageRequestBytes(request.Package)
		if err != nil {
			return "", fmt.Errorf("direct package request %d: %w", index, err)
		}
		if index > 0 {
			comparison := compareDirectPackageKey(request.Component, request.Package.Schema, encoded, previousComponent, previousSchema, previousBytes)
			if comparison == 0 {
				return "", fmt.Errorf("request overlay contains duplicate direct package request")
			}
			if comparison < 0 {
				return "", fmt.Errorf("request overlay direct packages are not canonically ordered")
			}
		}
		previousComponent, previousSchema, previousBytes = request.Component, request.Package.Schema, encoded
	}
	return canonical.Sum("request-overlay", RequestOverlaySchemaV1, overlay)
}

func compareDirectPackageKey(component string, schema string, encoded []byte, previousComponent string, previousSchema string, previousBytes []byte) int {
	if component < previousComponent {
		return -1
	}
	if component > previousComponent {
		return 1
	}
	if schema < previousSchema {
		return -1
	}
	if schema > previousSchema {
		return 1
	}
	return bytes.Compare(encoded, previousBytes)
}
