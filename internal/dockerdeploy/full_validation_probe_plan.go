package dockerdeploy

import (
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
)

type plannedAPTProfileProbe struct {
	ProfileIndex   int
	ToolInspection map[string]string
}

type plannedPythonProfileProbe struct {
	ProfileIndex          int
	ProfileDigest         canonical.Digest
	InterpreterInspection string
	LauncherInspection    string
}

type plannedOutputProbe struct {
	OutputIndex  int
	InspectionID string
	Binding      ProbeExecutableBinding
}

type fullImageValidationProbePlan struct {
	Request            probe.RequestV1
	CarrierInspection  string
	LauncherInspection string
	APT                *plannedAPTProfileProbe
	PythonProfiles     []plannedPythonProfileProbe
	Outputs            []plannedOutputProbe
}

// planFullImageValidationProbe combines every executable path needed by one
// image validation into one sorted probe request. Consumers retain typed
// mappings to the shared observations; repeated paths are hashed only once.
func planFullImageValidationProbe(input FullImageValidationInput) (fullImageValidationProbePlan, error) {
	if err := validateFullImageValidationInput(input, registry.ValidateRequirementProfileV1); err != nil {
		return fullImageValidationProbePlan{}, err
	}

	paths := map[string]bool{pythonCarrierPath: true, pythonLauncherPath: true}
	aptIndex := -1
	pythonProfiles := make([]plannedPythonProfileProbe, 0, len(input.Profiles))
	for index, profile := range input.Profiles {
		switch profile.Declaration.ProviderData.Schema {
		case aptprovider.ProviderRequestSchemaV1:
			if aptIndex >= 0 {
				return fullImageValidationProbePlan{}, fmt.Errorf("full image validation contains more than one APT profile")
			}
			aptIndex = index
			for _, tool := range aptprovider.RequiredBaseToolsV1() {
				paths[tool.Path] = true
			}
		case pythonprovider.ProviderRequestSchemaV1:
			digest, err := providers.RequirementProfileDigest(profile, pythonprovider.ValidateRequirementProfileV1)
			if err != nil {
				return fullImageValidationProbePlan{}, err
			}
			paths[profile.SelectedExecutables[0].InvocationPath] = true
			paths[pythonLauncherPath] = true
			pythonProfiles = append(pythonProfiles, plannedPythonProfileProbe{ProfileIndex: index, ProfileDigest: digest})
		default:
			return fullImageValidationProbePlan{}, fmt.Errorf("full image validation profile %d has unsupported provider schema %q", index, profile.Declaration.ProviderData.Schema)
		}
	}

	outputs := make([]plannedOutputProbe, len(input.Outputs))
	for index, output := range input.Outputs {
		paths[output.Candidate.InvocationPath] = true
		outputs[index] = plannedOutputProbe{
			OutputIndex: index,
			Binding: ProbeExecutableBinding{
				Output: providers.QualifiedOutput{Component: output.SupplierComponent, Name: output.Name},
				Facts:  output.Candidate.Provenance,
			},
		}
	}

	orderedPaths := make([]string, 0, len(paths))
	for path := range paths {
		orderedPaths = append(orderedPaths, path)
	}
	sort.Strings(orderedPaths)
	inspectionByPath := make(map[string]string, len(orderedPaths))
	inspections := make([]probe.ExecutableInspectionV1, len(orderedPaths))
	for index, path := range orderedPaths {
		id := fmt.Sprintf("path_%06d", index)
		inspectionByPath[path] = id
		inspections[index] = probe.ExecutableInspectionV1{ID: id, InvocationPath: path}
	}

	var aptPlan *plannedAPTProfileProbe
	if aptIndex >= 0 {
		tools := aptprovider.RequiredBaseToolsV1()
		toolInspections := make(map[string]string, len(tools))
		for _, tool := range tools {
			toolInspections[tool.Name] = inspectionByPath[tool.Path]
		}
		aptPlan = &plannedAPTProfileProbe{ProfileIndex: aptIndex, ToolInspection: toolInspections}
	}
	for index := range pythonProfiles {
		profile := input.Profiles[pythonProfiles[index].ProfileIndex]
		pythonProfiles[index].InterpreterInspection = inspectionByPath[profile.SelectedExecutables[0].InvocationPath]
		pythonProfiles[index].LauncherInspection = inspectionByPath[pythonLauncherPath]
	}
	sort.Slice(pythonProfiles, func(left int, right int) bool {
		return pythonProfiles[left].ProfileDigest < pythonProfiles[right].ProfileDigest
	})
	for index := range outputs {
		outputs[index].InspectionID = inspectionByPath[input.Outputs[index].Candidate.InvocationPath]
	}
	sort.Slice(outputs, func(left int, right int) bool {
		leftOutput := input.Outputs[outputs[left].OutputIndex]
		rightOutput := input.Outputs[outputs[right].OutputIndex]
		if leftOutput.SupplierComponent != rightOutput.SupplierComponent {
			return leftOutput.SupplierComponent < rightOutput.SupplierComponent
		}
		return leftOutput.Name < rightOutput.Name
	})

	request := probe.RequestV1{Schema: probe.RequestSchemaV1, Inspections: inspections}
	if err := probe.ValidateRequestV1(request); err != nil {
		return fullImageValidationProbePlan{}, err
	}
	return fullImageValidationProbePlan{
		Request:           request,
		CarrierInspection: inspectionByPath[pythonCarrierPath], LauncherInspection: inspectionByPath[pythonLauncherPath],
		APT: aptPlan, PythonProfiles: pythonProfiles, Outputs: outputs,
	}, nil
}
