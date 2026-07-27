package blueprint

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"gopkg.in/yaml.v3"
)

// LegacyComponentsRecoveryV1 is the narrow, explicit conversion result for
// staging state written by the unreleased components-based blueprint model.
type LegacyComponentsRecoveryV1 struct {
	Source         string
	Document       Document
	ComponentTypes map[string]ComponentType
}

type legacyComponentsResolvedProbeV1 struct {
	Schema   string
	Document struct {
		Blueprint   Metadata
		Environment struct {
			ID         string
			Components map[string]Component
		}
	}
}

// HasLegacyComponentsResolvedShapeV1 identifies the one unreleased resolved
// blueprint shape eligible for explicit forced recovery.
func HasLegacyComponentsResolvedShapeV1(payload ResolvedDocumentV1) bool {
	var probe struct {
		Schema   string
		Document struct {
			Environment struct {
				Components json.RawMessage
			}
		}
	}
	if json.Unmarshal([]byte(payload), &probe) != nil {
		return false
	}
	return probe.Schema == ResolvedDocumentSchemaV1 &&
		len(probe.Document.Environment.Components) != 0 &&
		string(probe.Document.Environment.Components) != "null"
}

// RecoverLegacyComponentsBlueprintV1 converts only the unambiguous
// components-based base/Python development shape. The converted author source
// is decoded and resolved by the current strict blueprint implementation.
func RecoverLegacyComponentsBlueprintV1(
	source string,
	resolved ResolvedDocumentV1,
) (LegacyComponentsRecoveryV1, error) {
	legacy, componentTypes, err := probeLegacyComponentsResolvedDocumentV1(resolved)
	if err != nil {
		return LegacyComponentsRecoveryV1{}, err
	}
	convertedSource, err := convertLegacyComponentsBlueprintSourceV1(
		[]byte(source),
		componentTypes,
	)
	if err != nil {
		return LegacyComponentsRecoveryV1{}, err
	}
	syntax, err := Decode(convertedSource)
	if err != nil {
		return LegacyComponentsRecoveryV1{}, fmt.Errorf(
			"decode converted legacy blueprint source: %w",
			err,
		)
	}
	document, err := Resolve(syntax)
	if err != nil {
		return LegacyComponentsRecoveryV1{}, fmt.Errorf(
			"resolve converted legacy blueprint source: %w",
			err,
		)
	}
	if document.Environment.ID != legacy.Document.Environment.ID {
		return LegacyComponentsRecoveryV1{}, fmt.Errorf(
			"converted legacy blueprint environment %q does not match stored environment %q",
			document.Environment.ID,
			legacy.Document.Environment.ID,
		)
	}
	if !reflect.DeepEqual(document.Blueprint, legacy.Document.Blueprint) {
		return LegacyComponentsRecoveryV1{}, fmt.Errorf(
			"converted legacy blueprint metadata does not match its stored resolved blueprint",
		)
	}
	if err := verifyConvertedLegacyComponentsV1(
		document,
		legacy.Document.Environment.Components,
	); err != nil {
		return LegacyComponentsRecoveryV1{}, err
	}
	return LegacyComponentsRecoveryV1{
		Source:         string(convertedSource),
		Document:       document,
		ComponentTypes: componentTypes,
	}, nil
}

func probeLegacyComponentsResolvedDocumentV1(
	payload ResolvedDocumentV1,
) (legacyComponentsResolvedProbeV1, map[string]ComponentType, error) {
	if !HasLegacyComponentsResolvedShapeV1(payload) {
		return legacyComponentsResolvedProbeV1{}, nil, fmt.Errorf(
			"resolved blueprint is not the recognized legacy components shape",
		)
	}
	var probe legacyComponentsResolvedProbeV1
	if err := json.Unmarshal([]byte(payload), &probe); err != nil {
		return legacyComponentsResolvedProbeV1{}, nil, fmt.Errorf(
			"decode legacy resolved blueprint: %w",
			err,
		)
	}
	if probe.Schema != ResolvedDocumentSchemaV1 {
		return legacyComponentsResolvedProbeV1{}, nil, fmt.Errorf(
			"legacy resolved blueprint schema must be %q",
			ResolvedDocumentSchemaV1,
		)
	}
	base, found := probe.Document.Environment.Components["base"]
	if !found || base.Type != ComponentTypeBase || base.Base == nil {
		return legacyComponentsResolvedProbeV1{}, nil, fmt.Errorf(
			"legacy resolved blueprint has no valid base component",
		)
	}
	types := make(map[string]ComponentType, len(probe.Document.Environment.Components))
	for name, component := range probe.Document.Environment.Components {
		types[name] = component.Type
		if name == "base" {
			continue
		}
		if component.Type != ComponentTypePython || component.Python == nil || component.APT != nil {
			return legacyComponentsResolvedProbeV1{}, nil, fmt.Errorf(
				"legacy component %q uses %q; forced recovery supports only base and Python components",
				name,
				component.Type,
			)
		}
		if component.Python.Interpreter.Supplier != "" &&
			component.Python.Interpreter.Supplier != "base" {
			return legacyComponentsResolvedProbeV1{}, nil, fmt.Errorf(
				"legacy Python component %q selects component supplier %q; forced recovery cannot infer current application ownership",
				name,
				component.Python.Interpreter.Supplier,
			)
		}
	}
	return probe, types, nil
}

func convertLegacyComponentsBlueprintSourceV1(
	source []byte,
	componentTypes map[string]ComponentType,
) ([]byte, error) {
	var root map[string]any
	if err := yaml.Unmarshal(source, &root); err != nil {
		return nil, fmt.Errorf("decode legacy blueprint source: %w", err)
	}
	environment, err := legacyStringMapV1(root["environment"], "environment")
	if err != nil {
		return nil, err
	}
	if _, found := environment["base"]; found {
		return nil, fmt.Errorf("legacy blueprint source mixes components with current base")
	}
	if _, found := environment["applications"]; found {
		return nil, fmt.Errorf("legacy blueprint source mixes components with current applications")
	}
	if _, found := environment["packages"]; found {
		return nil, fmt.Errorf("legacy blueprint source mixes components with current packages")
	}
	components, err := legacyStringMapV1(
		environment["components"],
		"environment.components",
	)
	if err != nil {
		return nil, err
	}
	if len(components) != len(componentTypes) {
		return nil, fmt.Errorf(
			"legacy blueprint source components do not match the stored resolved blueprint",
		)
	}
	base, found := components["base"]
	if !found || componentTypes["base"] != ComponentTypeBase {
		return nil, fmt.Errorf("legacy blueprint source has no valid base component")
	}
	applications := make(map[string]any, len(components)-1)
	names := make([]string, 0, len(components))
	for name := range components {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, found := componentTypes[name]; !found {
			return nil, fmt.Errorf(
				"legacy blueprint source component %q is absent from the stored resolved blueprint",
				name,
			)
		}
		if name == "base" {
			continue
		}
		if componentTypes[name] != ComponentTypePython {
			return nil, fmt.Errorf(
				"legacy component %q uses %q; forced recovery supports only base and Python components",
				name,
				componentTypes[name],
			)
		}
		application, err := convertLegacyPythonComponentSourceV1(name, components[name])
		if err != nil {
			return nil, err
		}
		applications[name] = application
	}
	delete(environment, "components")
	environment["base"] = base
	environment["applications"] = applications
	converted, err := yaml.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("encode converted legacy blueprint source: %w", err)
	}
	return converted, nil
}

func convertLegacyPythonComponentSourceV1(name string, value any) (map[string]any, error) {
	component, err := legacyStringMapV1(value, "legacy Python component "+name)
	if err != nil {
		return nil, err
	}
	for field := range component {
		switch field {
		case "type", "interpreter", "requirements", "options", "executables":
		default:
			return nil, fmt.Errorf(
				"legacy Python component %q field %q cannot be converted safely",
				name,
				field,
			)
		}
	}
	if component["type"] != string(ComponentTypePython) {
		return nil, fmt.Errorf("legacy component %q must declare type: python", name)
	}
	application := map[string]any{}
	requirements, hasRequirements := component["requirements"]
	interpreter, hasInterpreter := component["interpreter"]
	if hasRequirements {
		python := map[string]any{"requirements": requirements}
		if hasInterpreter {
			interpreterMap, err := legacyStringMapV1(
				interpreter,
				"legacy Python component "+name+" interpreter",
			)
			if err != nil {
				return nil, err
			}
			if supplier, found := interpreterMap["supplier"]; found &&
				supplier != "" && supplier != "base" {
				return nil, fmt.Errorf(
					"legacy Python component %q selects component supplier %q; forced recovery cannot infer current application ownership",
					name,
					supplier,
				)
			}
			python["interpreter"] = interpreter
		}
		application["packages"] = map[string]any{"python": python}
	} else if hasInterpreter {
		return nil, fmt.Errorf(
			"legacy Python component %q has no direct requirements but declares an interpreter; forced recovery cannot preserve it",
			name,
		)
	}
	if rawOptions, found := component["options"]; found {
		options, err := legacyStringMapV1(rawOptions, "legacy Python component "+name+" options")
		if err != nil {
			return nil, err
		}
		converted := make(map[string]any, len(options))
		for optionName, rawOption := range options {
			option, err := legacyStringMapV1(
				rawOption,
				"legacy Python component "+name+" option "+optionName,
			)
			if err != nil {
				return nil, err
			}
			for field := range option {
				if field != "description" && field != "requirements" {
					return nil, fmt.Errorf(
						"legacy Python component %q option %q field %q cannot be converted safely",
						name,
						optionName,
						field,
					)
				}
			}
			current := map[string]any{"description": option["description"]}
			if optionRequirements, found := option["requirements"]; found {
				current["packages"] = map[string]any{
					"python": map[string]any{"requirements": optionRequirements},
				}
			}
			converted[optionName] = current
		}
		application["options"] = converted
	}
	if rawExecutables, found := component["executables"]; found {
		executables, err := legacyStringMapV1(
			rawExecutables,
			"legacy Python component "+name+" executables",
		)
		if err != nil {
			return nil, err
		}
		converted := make(map[string]any, len(executables))
		for executableName, rawExecutable := range executables {
			executable, err := legacyStringMapV1(
				rawExecutable,
				"legacy Python component "+name+" executable "+executableName,
			)
			if err != nil {
				return nil, err
			}
			for field := range executable {
				switch field {
				case "binary", "order", "argv_prefix", "argv_suffix":
				default:
					return nil, fmt.Errorf(
						"legacy Python component %q executable %q field %q cannot be converted safely",
						name,
						executableName,
						field,
					)
				}
			}
			executable["source"] = ContributionProviderPython
			converted[executableName] = executable
		}
		application["executables"] = converted
	}
	return application, nil
}

func verifyConvertedLegacyComponentsV1(
	document Document,
	legacy map[string]Component,
) error {
	base := legacy["base"]
	if !reflect.DeepEqual(document.Environment.Base, *base.Base) {
		return fmt.Errorf("converted legacy base does not match its stored resolved component")
	}
	if len(document.Environment.Applications) != len(legacy)-1 {
		return fmt.Errorf("converted legacy applications do not match stored components")
	}
	for name, component := range legacy {
		if name == "base" {
			continue
		}
		application, found := document.Environment.Applications[name]
		if !found {
			return fmt.Errorf("converted legacy application %q is missing", name)
		}
		if len(component.Python.Requirements) == 0 {
			if application.Packages.Python != nil {
				return fmt.Errorf("converted legacy application %q changed Python packages", name)
			}
		} else if !reflect.DeepEqual(application.Packages.Python, component.Python) {
			return fmt.Errorf("converted legacy application %q changed Python packages", name)
		}
		if len(application.Options) != len(component.Options) ||
			len(application.Executables) != len(component.Executables) {
			return fmt.Errorf("converted legacy application %q changed options or executables", name)
		}
		for optionName, option := range component.Options {
			converted, found := application.Options[optionName]
			if !found || converted.Description != option.Description ||
				converted.Packages.Python == nil ||
				!reflect.DeepEqual(
					converted.Packages.Python.Requirements,
					option.PythonRequirements,
				) {
				return fmt.Errorf("converted legacy application %q option %q changed", name, optionName)
			}
		}
		for executableName, executable := range component.Executables {
			executable.Source = ContributionProviderPython
			if !reflect.DeepEqual(application.Executables[executableName], executable) {
				return fmt.Errorf(
					"converted legacy application %q executable %q changed",
					name,
					executableName,
				)
			}
		}
	}
	return nil
}

func legacyStringMapV1(value any, field string) (map[string]any, error) {
	mapping, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a mapping", field)
	}
	return mapping, nil
}
