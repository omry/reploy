package apt

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

const (
	ProviderRequestSchemaV1 = "apt-provider-request-v1"
	ExplicitExportSchemaV1  = "explicit-apt-export-v1"
	RecipeVersion           = "apt-v1"
)

type APTProviderRequestV1 struct {
	Components []APTComponentRequestV1
}

type APTComponentRequestV1 struct {
	Component string
	Packages  []blueprint.APTPackageRequest
}

type ComponentProvider struct{}

func CanonicalProviderRequestV1(request APTProviderRequestV1) (providers.CanonicalProviderRequest, error) {
	components := append([]APTComponentRequestV1{}, request.Components...)
	sort.Slice(components, func(left int, right int) bool { return components[left].Component < components[right].Component })
	if len(components) == 0 {
		return providers.CanonicalProviderRequest{}, fmt.Errorf("APT provider request must contain at least one active component")
	}
	values := make([]any, 0, len(components))
	packageDeclarations := map[string][]byte{}
	for index, component := range components {
		if err := blueprint.ValidateContributionReference("APT contribution", component.Component); err != nil {
			return providers.CanonicalProviderRequest{}, err
		}
		if component.Component == "base" || index > 0 && components[index-1].Component == component.Component {
			return providers.CanonicalProviderRequest{}, fmt.Errorf("APT components must be unique and cannot use base")
		}
		if len(component.Packages) == 0 {
			return providers.CanonicalProviderRequest{}, fmt.Errorf("active APT component %q must contain at least one package", component.Component)
		}
		type encodedPackage struct {
			value   providers.CanonicalPackageRequest
			encoded []byte
		}
		packages := make([]encodedPackage, 0, len(component.Packages))
		seen := map[string]bool{}
		for _, item := range component.Packages {
			value, err := CanonicalPackageRequestV1(item)
			if err != nil {
				return providers.CanonicalProviderRequest{}, err
			}
			encoded, err := providers.CanonicalPackageRequestBytes(value)
			if err != nil {
				return providers.CanonicalProviderRequest{}, err
			}
			if previous, exists := packageDeclarations[item.Name]; exists && !bytes.Equal(previous, encoded) {
				return providers.CanonicalProviderRequest{}, fmt.Errorf("shared APT authority contains conflicting declarations for package %q", item.Name)
			}
			packageDeclarations[item.Name] = encoded
			if !seen[string(encoded)] {
				packages = append(packages, encodedPackage{value: value, encoded: encoded})
				seen[string(encoded)] = true
			}
		}
		sort.Slice(packages, func(left int, right int) bool {
			return bytes.Compare(packages[left].encoded, packages[right].encoded) < 0
		})
		packageValues := make([]any, 0, len(packages))
		for _, item := range packages {
			packageValues = append(packageValues, canonical.Object{"schema": item.value.Schema, "value": item.value.Value})
		}
		values = append(values, canonical.Object{"component": component.Component, "packages": packageValues})
	}
	return providers.CanonicalProviderRequest{
		Schema: ProviderRequestSchemaV1, Provider: blueprint.ComponentTypeAPT,
		Value: canonical.Object{"components": values},
	}, nil
}

func ValidateCanonicalProviderRequestV1(request providers.CanonicalProviderRequest) error {
	_, err := decodeCanonicalProviderRequestV1(request)
	return err
}

func ValidateCanonicalProviderRequestForComponentV1(component string, request providers.CanonicalProviderRequest) error {
	decoded, err := decodeCanonicalProviderRequestV1(request)
	if err != nil {
		return err
	}
	if len(decoded.Components) != 1 || decoded.Components[0].Component != component {
		return fmt.Errorf("APT resolved component request must contain exactly component %q", component)
	}
	return nil
}

func decodeCanonicalProviderRequestV1(request providers.CanonicalProviderRequest) (APTProviderRequestV1, error) {
	if request.Schema != ProviderRequestSchemaV1 || request.Provider != blueprint.ComponentTypeAPT || len(request.Value) != 1 {
		return APTProviderRequestV1{}, fmt.Errorf("APT provider request must use schema %q and the exact value shape", ProviderRequestSchemaV1)
	}
	componentValues, ok := request.Value["components"].([]any)
	if !ok {
		return APTProviderRequestV1{}, fmt.Errorf("APT provider request components must be an array")
	}
	result := APTProviderRequestV1{Components: make([]APTComponentRequestV1, 0, len(componentValues))}
	for index, value := range componentValues {
		object, ok := canonicalObject(value)
		if !ok || len(object) != 2 {
			return APTProviderRequestV1{}, fmt.Errorf("APT provider component %d has invalid shape", index)
		}
		component, componentOK := object["component"].(string)
		packageValues, packagesOK := object["packages"].([]any)
		if !componentOK || !packagesOK {
			return APTProviderRequestV1{}, fmt.Errorf("APT provider component %d has invalid fields", index)
		}
		item := APTComponentRequestV1{Component: component, Packages: make([]blueprint.APTPackageRequest, 0, len(packageValues))}
		for packageIndex, packageValue := range packageValues {
			packageObject, ok := canonicalObject(packageValue)
			if !ok || len(packageObject) != 2 {
				return APTProviderRequestV1{}, fmt.Errorf("APT provider package %d has invalid shape", packageIndex)
			}
			schema, schemaOK := packageObject["schema"].(string)
			value, valueOK := canonicalObject(packageObject["value"])
			if !schemaOK || !valueOK {
				return APTProviderRequestV1{}, fmt.Errorf("APT provider package %d has invalid fields", packageIndex)
			}
			decoded, err := decodePackageRequest(providers.CanonicalPackageRequest{Schema: schema, Value: value})
			if err != nil {
				return APTProviderRequestV1{}, err
			}
			item.Packages = append(item.Packages, decoded)
		}
		result.Components = append(result.Components, item)
	}
	normalized, err := CanonicalProviderRequestV1(result)
	if err != nil {
		return APTProviderRequestV1{}, err
	}
	actual, err := providers.CanonicalProviderRequestBytes(request)
	if err != nil {
		return APTProviderRequestV1{}, err
	}
	expected, err := providers.CanonicalProviderRequestBytes(normalized)
	if err != nil {
		return APTProviderRequestV1{}, err
	}
	if !bytes.Equal(actual, expected) {
		return APTProviderRequestV1{}, fmt.Errorf("APT provider request is not canonically normalized")
	}
	return result, nil
}

func (ComponentProvider) Plan(input providers.PlanInput) ([]providers.NodeSpec, error) {
	if err := input.Platform.Validate(); err != nil {
		return nil, fmt.Errorf("APT plan platform: %w", err)
	}
	all := []APTComponentRequestV1{}
	for _, component := range input.Components {
		if component.Provider != blueprint.ComponentTypeAPT {
			continue
		}
		request, err := decodeCanonicalProviderRequestV1(component.Request)
		if err != nil {
			return nil, fmt.Errorf("plan APT component %q: %w", component.Component, err)
		}
		found := false
		for _, item := range request.Components {
			if item.Component == component.Component {
				all = append(all, item)
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("APT request does not contain resolved component %q", component.Component)
		}
	}
	if len(all) == 0 {
		return []providers.NodeSpec{}, nil
	}
	combined, err := CanonicalProviderRequestV1(APTProviderRequestV1{Components: all})
	if err != nil {
		return nil, err
	}
	decoded, err := decodeCanonicalProviderRequestV1(combined)
	if err != nil {
		return nil, err
	}
	components := make([]string, 0, len(decoded.Components))
	for _, component := range decoded.Components {
		components = append(components, component.Component)
	}
	outputs, err := OutputDeclarationsV1(combined)
	if err != nil {
		return nil, err
	}
	node := providers.NodeSpec{
		ID: "apt", Provider: blueprint.ComponentTypeAPT, Components: components, Request: combined, OutputDeclarations: outputs,
		Requirements: providers.RequirementDeclaration{
			Executables: []providers.ExecutableRequirement{}, Files: []providers.FileRequirement{},
			ProviderData: providers.CanonicalProviderData{Schema: combined.Schema, Value: combined.Value},
		},
	}
	if err := providers.ValidateNodeSpec(node); err != nil {
		return nil, err
	}
	return []providers.NodeSpec{node}, nil
}

// OutputDeclarationsV1 derives the exact public candidates declared by one
// canonical combined APT request. It performs no image or package ownership
// observation.
func OutputDeclarationsV1(request providers.CanonicalProviderRequest) ([]providers.OutputDeclaration, error) {
	decoded, err := decodeCanonicalProviderRequestV1(request)
	if err != nil {
		return nil, err
	}
	seenOutputs := map[string]providers.OutputDeclaration{}
	for _, component := range decoded.Components {
		for _, pkg := range component.Packages {
			mapping, mapped, err := ResolveWellKnownToolV1(pkg)
			if err != nil {
				return nil, err
			}
			if mapped {
				declaration := providers.OutputDeclaration{
					SupplierComponent: component.Component, Name: mapping.OutputName, Kind: providers.OutputKindExecutable, CandidatePath: mapping.CandidatePath,
					Provenance: providers.CanonicalProviderData{Schema: WellKnownToolSchemaV1, Value: canonical.Object{
						"profile": mapping.Profile, "package": mapping.PackageName, "version": mapping.PackageVersion, "consumer_kind": mapping.ConsumerKind, "explicit_replacement": mapping.ExplicitReplacement,
					}},
				}
				if err := addAPTOutput(seenOutputs, declaration); err != nil {
					return nil, err
				}
			}
			for name, export := range pkg.Exports {
				if mapped && name == mapping.OutputName {
					continue
				}
				declaration := providers.OutputDeclaration{
					SupplierComponent: component.Component, Name: name, Kind: providers.OutputKindExecutable, CandidatePath: export.Executable,
					Provenance: providers.CanonicalProviderData{Schema: ExplicitExportSchemaV1, Value: canonical.Object{"package": pkg.Name, "version": pkg.Version, "output": name}},
				}
				if err := addAPTOutput(seenOutputs, declaration); err != nil {
					return nil, err
				}
			}
		}
	}
	outputs := make([]providers.OutputDeclaration, 0, len(seenOutputs))
	for _, output := range seenOutputs {
		outputs = append(outputs, output)
	}
	sort.Slice(outputs, func(left int, right int) bool {
		if outputs[left].SupplierComponent != outputs[right].SupplierComponent {
			return outputs[left].SupplierComponent < outputs[right].SupplierComponent
		}
		return outputs[left].Name < outputs[right].Name
	})
	return outputs, nil
}

func decodePackageRequest(request providers.CanonicalPackageRequest) (blueprint.APTPackageRequest, error) {
	if err := ValidateCanonicalPackageRequestV1(request); err != nil {
		return blueprint.APTPackageRequest{}, err
	}
	name := request.Value["name"].(string)
	version, _ := request.Value["version"].(string)
	exports := map[string]blueprint.ExecutableExport{}
	for _, raw := range request.Value["exports"].([]any) {
		object, _ := canonicalObject(raw)
		exports[object["name"].(string)] = blueprint.ExecutableExport{Executable: object["executable"].(string)}
	}
	return blueprint.APTPackageRequest{Name: name, Version: version, Exports: exports}, nil
}

func addAPTOutput(outputs map[string]providers.OutputDeclaration, output providers.OutputDeclaration) error {
	key := output.SupplierComponent + "\x00" + output.Name
	if _, exists := outputs[key]; exists {
		return fmt.Errorf("APT component %q declares conflicting output %q", output.SupplierComponent, output.Name)
	}
	outputs[key] = output
	return nil
}
