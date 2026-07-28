package blueprint

import (
	"encoding/json"
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// LegacyComponentsRecoveryV1 is the narrow, explicit conversion result for
// staging state written by the unreleased components-based blueprint model.
type LegacyComponentsRecoveryV1 struct {
	Source                string
	Document              Document
	ComponentTypes        map[string]ComponentType
	PreviousEnvironmentID string
}

type legacyComponentsResolvedProbeV1 struct {
	Schema   string
	Document struct {
		Environment struct {
			ID         string
			Components json.RawMessage
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
	legacy, err := probeLegacyComponentsResolvedDocumentV1(resolved)
	if err != nil {
		return LegacyComponentsRecoveryV1{}, err
	}
	convertedSource, componentTypes, err := convertLegacyComponentsBlueprintSourceV1([]byte(source))
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
	return LegacyComponentsRecoveryV1{
		Source:                string(convertedSource),
		Document:              document,
		ComponentTypes:        componentTypes,
		PreviousEnvironmentID: legacy.Document.Environment.ID,
	}, nil
}

func probeLegacyComponentsResolvedDocumentV1(
	payload ResolvedDocumentV1,
) (legacyComponentsResolvedProbeV1, error) {
	if !HasLegacyComponentsResolvedShapeV1(payload) {
		return legacyComponentsResolvedProbeV1{}, fmt.Errorf(
			"resolved blueprint is not the recognized legacy components shape",
		)
	}
	var probe legacyComponentsResolvedProbeV1
	if err := json.Unmarshal([]byte(payload), &probe); err != nil {
		return legacyComponentsResolvedProbeV1{}, fmt.Errorf(
			"decode legacy resolved blueprint: %w",
			err,
		)
	}
	if probe.Schema != ResolvedDocumentSchemaV1 {
		return legacyComponentsResolvedProbeV1{}, fmt.Errorf(
			"legacy resolved blueprint schema must be %q",
			ResolvedDocumentSchemaV1,
		)
	}
	if probe.Document.Environment.ID == "" {
		return legacyComponentsResolvedProbeV1{}, fmt.Errorf(
			"legacy resolved blueprint has no environment ID for owned-resource cleanup",
		)
	}
	return probe, nil
}

func convertLegacyComponentsBlueprintSourceV1(
	source []byte,
) ([]byte, map[string]ComponentType, error) {
	var root map[string]any
	if err := yaml.Unmarshal(source, &root); err != nil {
		return nil, nil, fmt.Errorf("decode legacy blueprint source: %w", err)
	}
	environment, err := legacyStringMapV1(root["environment"], "environment")
	if err != nil {
		return nil, nil, err
	}
	if _, found := environment["base"]; found {
		return nil, nil, fmt.Errorf("legacy blueprint source mixes components with current base")
	}
	if _, found := environment["applications"]; found {
		return nil, nil, fmt.Errorf("legacy blueprint source mixes components with current applications")
	}
	if _, found := environment["packages"]; found {
		return nil, nil, fmt.Errorf("legacy blueprint source mixes components with current packages")
	}
	components, err := legacyStringMapV1(
		environment["components"],
		"environment.components",
	)
	if err != nil {
		return nil, nil, err
	}
	base, found := components["base"]
	if !found {
		return nil, nil, fmt.Errorf("legacy blueprint source has no base component")
	}
	componentTypes := make(map[string]ComponentType, len(components))
	componentTypes["base"] = ComponentTypeBase
	applications := make(map[string]any, len(components)-1)
	names := make([]string, 0, len(components))
	for name := range components {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name == "base" {
			continue
		}
		application, err := convertLegacyPythonComponentSourceV1(name, components[name])
		if err != nil {
			return nil, nil, err
		}
		componentTypes[name] = ComponentTypePython
		applications[name] = application
	}
	delete(environment, "components")
	environment["base"] = base
	environment["applications"] = applications
	converted, err := yaml.Marshal(root)
	if err != nil {
		return nil, nil, fmt.Errorf("encode converted legacy blueprint source: %w", err)
	}
	return converted, componentTypes, nil
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

func legacyStringMapV1(value any, field string) (map[string]any, error) {
	mapping, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a mapping", field)
	}
	return mapping, nil
}
