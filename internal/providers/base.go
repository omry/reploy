package providers

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

const (
	BaseProviderRequestSchemaV1 = "base-provider-request-v1"
	BaseExportSchemaV1          = "base-export-v1"
)

type BaseProviderRequestV1 struct {
	Image   string
	Exports map[string]blueprint.BaseExecutableExport
}

func CanonicalBaseProviderRequestV1(request BaseProviderRequestV1) (CanonicalProviderRequest, error) {
	if !isNonemptyPlainString(request.Image) || strings.TrimSpace(request.Image) != request.Image {
		return CanonicalProviderRequest{}, fmt.Errorf("base image must be nonempty valid text without surrounding whitespace")
	}
	names := make([]string, 0, len(request.Exports))
	for name := range request.Exports {
		names = append(names, name)
	}
	sort.Strings(names)
	exports := make([]any, 0, len(names))
	for _, name := range names {
		if err := blueprint.ValidateProviderIdentifier("base export name", name); err != nil {
			return CanonicalProviderRequest{}, err
		}
		executable := request.Exports[name].Executable
		if err := validateAbsoluteLinuxPath("base export executable", executable); err != nil {
			return CanonicalProviderRequest{}, err
		}
		exports = append(exports, canonical.Object{"name": name, "executable": executable})
	}
	return CanonicalProviderRequest{
		Schema:   BaseProviderRequestSchemaV1,
		Provider: blueprint.ComponentTypeBase,
		Value:    canonical.Object{"image": request.Image, "exports": exports},
	}, nil
}

func ValidateCanonicalBaseProviderRequestV1(request CanonicalProviderRequest) error {
	_, err := decodeCanonicalBaseProviderRequestV1(request)
	return err
}

func decodeCanonicalBaseProviderRequestV1(request CanonicalProviderRequest) (BaseProviderRequestV1, error) {
	if request.Schema != BaseProviderRequestSchemaV1 || request.Provider != blueprint.ComponentTypeBase || len(request.Value) != 2 {
		return BaseProviderRequestV1{}, fmt.Errorf("base provider request must use schema %q and the exact value shape", BaseProviderRequestSchemaV1)
	}
	image, imageOK := request.Value["image"].(string)
	exportValues, exportsOK := request.Value["exports"].([]any)
	if !imageOK || !exportsOK {
		return BaseProviderRequestV1{}, fmt.Errorf("base provider request image and exports have invalid types")
	}
	exports := make(map[string]blueprint.BaseExecutableExport, len(exportValues))
	for index, value := range exportValues {
		object, ok := canonicalProviderObject(value)
		if !ok || len(object) != 2 {
			return BaseProviderRequestV1{}, fmt.Errorf("base export %d has invalid shape", index)
		}
		name, nameOK := object["name"].(string)
		executable, executableOK := object["executable"].(string)
		if !nameOK || !executableOK {
			return BaseProviderRequestV1{}, fmt.Errorf("base export %d has invalid fields", index)
		}
		if _, exists := exports[name]; exists {
			return BaseProviderRequestV1{}, fmt.Errorf("base provider request contains duplicate export %q", name)
		}
		exports[name] = blueprint.BaseExecutableExport{Executable: executable}
	}
	result := BaseProviderRequestV1{Image: image, Exports: exports}
	normalized, err := CanonicalBaseProviderRequestV1(result)
	if err != nil {
		return BaseProviderRequestV1{}, err
	}
	actual, err := CanonicalProviderRequestBytes(request)
	if err != nil {
		return BaseProviderRequestV1{}, err
	}
	expected, err := CanonicalProviderRequestBytes(normalized)
	if err != nil {
		return BaseProviderRequestV1{}, err
	}
	if !bytes.Equal(actual, expected) {
		return BaseProviderRequestV1{}, fmt.Errorf("base provider request is not canonically normalized")
	}
	return result, nil
}

func BaseNodeSpec(request CanonicalProviderRequest) (NodeSpec, error) {
	decoded, err := decodeCanonicalBaseProviderRequestV1(request)
	if err != nil {
		return NodeSpec{}, err
	}
	names := make([]string, 0, len(decoded.Exports))
	for name := range decoded.Exports {
		names = append(names, name)
	}
	sort.Strings(names)
	outputs := make([]OutputDeclaration, 0, len(names))
	for _, name := range names {
		outputs = append(outputs, OutputDeclaration{
			SupplierComponent: "base",
			Name:              name,
			Kind:              OutputKindExecutable,
			CandidatePath:     decoded.Exports[name].Executable,
			Provenance: CanonicalProviderData{
				Schema: BaseExportSchemaV1,
				Value:  canonical.Object{"image": decoded.Image, "output": name},
			},
		})
	}
	node := NodeSpec{
		ID: "base", Provider: blueprint.ComponentTypeBase, Components: []string{"base"},
		Request: request, OutputDeclarations: outputs,
		Requirements: RequirementDeclaration{
			Executables: []ExecutableRequirement{}, Files: []FileRequirement{},
			ProviderData: CanonicalProviderData{Schema: request.Schema, Value: request.Value},
		},
	}
	if err := ValidateNodeSpec(node); err != nil {
		return NodeSpec{}, err
	}
	return node, nil
}

func canonicalProviderObject(value any) (canonical.Object, bool) {
	switch object := value.(type) {
	case canonical.Object:
		return object, true
	case map[string]any:
		return canonical.Object(object), true
	default:
		return nil, false
	}
}
