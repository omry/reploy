package toolcatalog

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	pep440 "github.com/aquasecurity/go-pep440-version"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

// Target composition and fixture coverage for the portable tool record model.
// Record-local validation lives in records_validate.go; this file validates one
// target against its release contract and proves every support tuple that
// target advertises is covered by an integration fixture.

// supportTupleV1 is one exact combination a target advertises: a context, a
// binding, a normalized selection set, and normalized parameter values.
type supportTupleV1 struct {
	Context    string             `json:"context"`
	Binding    string             `json:"binding"`
	Selections []string           `json:"selections"`
	Parameters []ParameterValueV1 `json:"parameters"`
}

func resolvedRecordV1(records map[string]loadedRecordV1, reference RecordReferenceV1) (loadedRecordV1, error) {
	record, exists := records[reference.ID]
	if !exists || record.ID != reference.ID || record.Digest != reference.Digest {
		return loadedRecordV1{}, fmt.Errorf("reference %q does not resolve to its exact record", reference.ID)
	}
	return record, nil
}

func supportTupleKeyV1(tuple supportTupleV1) (string, error) {
	payload, err := canonical.Marshal(tuple)
	if err != nil {
		return "", fmt.Errorf("support tuple canonical form: %w", err)
	}
	return string(payload), nil
}

func normalizedFixtureTupleV1(contract *ReleaseContractV1, fixture *IntegrationFixtureRecordV1) supportTupleV1 {
	binding := fixture.Binding
	if binding == "" {
		binding = contract.Binding.Default
	}
	selections := append([]string{}, fixture.Selections...)
	if len(selections) == 0 && len(contract.Selections.Defaults) != 0 {
		selections = append([]string{}, contract.Selections.Defaults...)
	}
	provided := make(map[string]string, len(fixture.Parameters))
	for _, parameter := range fixture.Parameters {
		provided[parameter.Name] = parameter.Value
	}
	parameters := make([]ParameterValueV1, 0, len(contract.Parameters))
	for _, parameter := range contract.Parameters {
		value, exists := provided[parameter.Name]
		if !exists && parameter.Default != nil {
			value, exists = *parameter.Default, true
		}
		if exists {
			parameters = append(parameters, ParameterValueV1{Name: parameter.Name, Value: value})
		}
	}
	return supportTupleV1{Context: fixture.Context, Binding: binding, Selections: selections, Parameters: parameters}
}

func targetParameterAllowsV1(constraints []TargetParameterConstraintV1, name string, value string) bool {
	for _, constraint := range constraints {
		if constraint.Name != name {
			continue
		}
		if len(constraint.Values) != 0 {
			return containsRecordValueV1(constraint.Values, value)
		}
		parsed, err := parseCanonicalIntegerV1("fixture parameter", value)
		minimum, minimumErr := parseCanonicalIntegerV1("target parameter minimum", constraint.Minimum)
		maximum, maximumErr := parseCanonicalIntegerV1("target parameter maximum", constraint.Maximum)
		return err == nil && minimumErr == nil && maximumErr == nil && parsed >= minimum && parsed <= maximum
	}
	return true
}

func targetParameterDomainV1(parameter ParameterSchemaV1, constraints []TargetParameterConstraintV1) ([]*string, error) {
	values := make([]string, 0)
	constrained := false
	for _, constraint := range constraints {
		if constraint.Name != parameter.Name {
			continue
		}
		constrained = true
		if len(constraint.Values) != 0 {
			values = append(values, constraint.Values...)
		} else {
			minimum, _ := parseCanonicalIntegerV1("target parameter minimum", constraint.Minimum)
			maximum, _ := parseCanonicalIntegerV1("target parameter maximum", constraint.Maximum)
			for value := minimum; ; value++ {
				values = append(values, strconv.FormatInt(value, 10))
				if value == maximum {
					break
				}
			}
		}
		break
	}
	if !constrained {
		switch parameter.Type {
		case "boolean":
			values = []string{"false", "true"}
		case "enum":
			values = append(values, parameter.Values...)
		case "integer":
			minimum, _ := parseCanonicalIntegerV1("parameter minimum", parameter.Minimum)
			maximum, _ := parseCanonicalIntegerV1("parameter maximum", parameter.Maximum)
			for value := minimum; ; value++ {
				values = append(values, strconv.FormatInt(value, 10))
				if value == maximum {
					break
				}
			}
		default:
			return nil, fmt.Errorf("parameter %q has an unsupported domain", parameter.Name)
		}
	}
	result := make([]*string, 0, len(values)+1)
	if !parameter.Required && parameter.Default == nil {
		result = append(result, nil)
	}
	for _, value := range values {
		value := value
		result = append(result, &value)
	}
	return result, nil
}

func targetParameterAssignmentsV1(parameters []ParameterSchemaV1, constraints []TargetParameterConstraintV1) ([][]ParameterValueV1, error) {
	domains := make([][]*string, len(parameters))
	for index, parameter := range parameters {
		values, err := targetParameterDomainV1(parameter, constraints)
		if err != nil {
			return nil, err
		}
		domains[index] = values
	}
	result := make([][]ParameterValueV1, 0)
	current := make([]ParameterValueV1, 0, len(parameters))
	var enumerate func(int) error
	enumerate = func(index int) error {
		if index == len(parameters) {
			if len(result) == maxDefinitionValidationCases {
				return fmt.Errorf("parameter coverage exceeds the validation case limit")
			}
			result = append(result, append([]ParameterValueV1{}, current...))
			return nil
		}
		for _, value := range domains[index] {
			if value == nil {
				if err := enumerate(index + 1); err != nil {
					return err
				}
				continue
			}
			current = append(current, ParameterValueV1{Name: parameters[index].Name, Value: *value})
			if err := enumerate(index + 1); err != nil {
				return err
			}
			current = current[:len(current)-1]
		}
		return nil
	}
	if err := enumerate(0); err != nil {
		return nil, err
	}
	return result, nil
}

func validSelectionSetsForCoverageV1(request SelectionRequestV1) ([][]string, error) {
	minimum, _ := strconv.ParseUint(request.Minimum, 10, 63)
	maximum, _ := strconv.ParseUint(request.Maximum, 10, 63)
	sets := make(map[string][]string)
	add := func(value []string) error {
		key := strings.Join(value, "\x00")
		if _, exists := sets[key]; exists {
			return nil
		}
		if len(sets) == maxDefinitionValidationCases {
			return fmt.Errorf("selection-set coverage exceeds the validation case limit")
		}
		sets[key] = append([]string{}, value...)
		return nil
	}
	if minimum == 0 {
		if err := add([]string{}); err != nil {
			return nil, err
		}
	}
	for _, group := range request.CompatibilityGroups {
		lower := int(minimum)
		if lower < 1 {
			lower = 1
		}
		upper := int(maximum)
		if upper > len(group) {
			upper = len(group)
		}
		for count := lower; count <= upper; count++ {
			chosen := make([]string, 0, count)
			var enumerate func(int) error
			enumerate = func(start int) error {
				if len(chosen) == count {
					return add(chosen)
				}
				remaining := count - len(chosen)
				for index := start; index <= len(group)-remaining; index++ {
					chosen = append(chosen, group[index])
					if err := enumerate(index + 1); err != nil {
						return err
					}
					chosen = chosen[:len(chosen)-1]
				}
				return nil
			}
			if err := enumerate(0); err != nil {
				return nil, err
			}
		}
	}
	result := make([][]string, 0, len(sets))
	for _, value := range sets {
		result = append(result, value)
	}
	sort.Slice(result, func(left int, right int) bool { return compareRecordStringSlicesV1(result[left], result[right]) < 0 })
	return result, nil
}

func targetSupportTuplesV1(contract *ReleaseContractV1, target *TargetRecordV1) ([]supportTupleV1, error) {
	bindings := append([]string{}, contract.Binding.Options...)
	if !contract.Binding.Required && contract.Binding.Default == "" {
		bindings = append([]string{""}, bindings...)
	}
	selections, err := validSelectionSetsForCoverageV1(contract.Selections)
	if err != nil {
		return nil, err
	}
	parameters, err := targetParameterAssignmentsV1(contract.Parameters, target.Parameters)
	if err != nil {
		return nil, err
	}
	result := make([]supportTupleV1, 0)
	for _, context := range contract.Contexts {
		for _, binding := range bindings {
			for _, selected := range selections {
				for _, values := range parameters {
					if len(result) == maxDefinitionValidationCases {
						return nil, fmt.Errorf("target support tuple coverage exceeds the validation case limit")
					}
					result = append(result, supportTupleV1{
						Context: context, Binding: binding,
						Selections: append([]string{}, selected...), Parameters: append([]ParameterValueV1{}, values...),
					})
				}
			}
		}
	}
	return result, nil
}

func validateTargetAgainstContractV1(contract *ReleaseContractV1, target *TargetRecordV1) error {
	if len(target.Bindings) != len(contract.Binding.Options) {
		return fmt.Errorf("target must provide exactly one contribution mapping for every contract binding option")
	}
	for index, option := range contract.Binding.Options {
		if target.Bindings[index].Name != option {
			return fmt.Errorf("target binding mappings must exactly match contract binding options")
		}
	}
	if len(target.Selections) != len(contract.Selections.Options) {
		return fmt.Errorf("target must provide exactly one contribution mapping for every contract selection option")
	}
	for index, option := range contract.Selections.Options {
		if target.Selections[index].Name != option {
			return fmt.Errorf("target selection mappings must exactly match contract selection options")
		}
	}

	contractParameters := make(map[string]ParameterSchemaV1, len(contract.Parameters))
	for _, parameter := range contract.Parameters {
		contractParameters[parameter.Name] = parameter
	}
	for _, constraint := range target.Parameters {
		parameter, exists := contractParameters[constraint.Name]
		if !exists {
			return fmt.Errorf("target parameter constraint %q is not declared by the release contract", constraint.Name)
		}
		if len(constraint.Values) != 0 {
			for _, value := range constraint.Values {
				if !parameterValueInSchemaV1(value, parameter) {
					return fmt.Errorf("target parameter constraint %q value %q is outside the contract domain", constraint.Name, value)
				}
			}
			if parameter.Default != nil && !containsRecordValueV1(constraint.Values, *parameter.Default) {
				return fmt.Errorf("target parameter constraint %q excludes the contract default", constraint.Name)
			}
			continue
		}
		if parameter.Type != "integer" {
			return fmt.Errorf("target parameter constraint %q range is incompatible with contract type %q", constraint.Name, parameter.Type)
		}
		minimum, _ := parseCanonicalIntegerV1("target parameter minimum", constraint.Minimum)
		maximum, _ := parseCanonicalIntegerV1("target parameter maximum", constraint.Maximum)
		contractMinimum, _ := parseCanonicalIntegerV1("contract parameter minimum", parameter.Minimum)
		contractMaximum, _ := parseCanonicalIntegerV1("contract parameter maximum", parameter.Maximum)
		if minimum < contractMinimum || maximum > contractMaximum {
			return fmt.Errorf("target parameter constraint %q range widens the contract domain", constraint.Name)
		}
		if parameter.Default != nil {
			defaultValue, _ := parseCanonicalIntegerV1("contract parameter default", *parameter.Default)
			if defaultValue < minimum || defaultValue > maximum {
				return fmt.Errorf("target parameter constraint %q excludes the contract default", constraint.Name)
			}
		}
	}
	return nil
}

func validateBindingArtifactAgainstContractV1(contract *BindingContractV1, artifact *BindingArtifactRecordV1) error {
	parts := strings.Split(strings.TrimSuffix(artifact.Filename, ".whl"), "-")
	if len(parts) != 5 && len(parts) != 6 {
		return fmt.Errorf("wheel identity is invalid")
	}
	distribution := pythonprovider.NormalizeDistributionName(parts[0])
	if distribution != pythonprovider.NormalizeDistributionName(contract.Package) {
		return fmt.Errorf("wheel distribution %q does not match contract package %q", distribution, contract.Package)
	}
	wheelVersion, err := pep440.Parse(parts[1])
	if err != nil {
		return fmt.Errorf("wheel version is invalid")
	}
	requirementFound := false
	for _, requirement := range contract.Requirements {
		requirementDistribution, err := pythonprovider.PackageRootDistributionNameV1(requirement)
		if err != nil || requirementDistribution != distribution {
			continue
		}
		requirementFound = true
		remainder := ""
		for index, character := range requirement {
			if character == '[' || character == '<' || character == '>' || character == '=' || character == '!' || character == '~' {
				remainder = requirement[index:]
				break
			}
		}
		if strings.HasPrefix(remainder, "[") {
			end := strings.IndexByte(remainder, ']')
			if end < 0 {
				return fmt.Errorf("contract package requirement is invalid")
			}
			remainder = remainder[end+1:]
		}
		if remainder != "" {
			specifiers, err := pep440.NewSpecifiers(remainder)
			if err != nil || !specifiers.Check(wheelVersion) {
				return fmt.Errorf("wheel version %q does not satisfy contract requirement %q", parts[1], requirement)
			}
		}
		break
	}
	if !requirementFound {
		return fmt.Errorf("contract package %q has no binding requirement", contract.Package)
	}
	pythonSpecifiers, err := pep440.NewSpecifiers(artifact.RequiresPython)
	if err != nil {
		return fmt.Errorf("requires_python is invalid")
	}
	for _, version := range contract.SupportedPython {
		parsed, err := pep440.Parse(version)
		if err == nil && pythonSpecifiers.Check(parsed) {
			return nil
		}
	}
	return fmt.Errorf("requires_python %q excludes every contract interpreter", artifact.RequiresPython)
}

// Validate every binding contribution a target advertises against the binding
// contract it names: each selected artifact must agree with the contract, and
// the selected set together must cover every interpreter the contract
// advertises.
func validateTargetBindingsAgainstContractsV1(records map[string]loadedRecordV1, target *TargetRecordV1) error {
	for _, binding := range target.Bindings {
		record, err := resolvedRecordV1(records, binding.Contract)
		if err != nil {
			return err
		}
		contract, ok := record.Value.(*BindingContractV1)
		if !ok {
			return fmt.Errorf("target binding %q contract %q resolves to a non-contract record", binding.Name, binding.Contract.ID)
		}
		artifacts := make([]*BindingArtifactRecordV1, 0, len(binding.Artifacts))
		for _, reference := range binding.Artifacts {
			artifactRecord, err := resolvedRecordV1(records, reference)
			if err != nil {
				return err
			}
			artifact, ok := artifactRecord.Value.(*BindingArtifactRecordV1)
			if !ok {
				return fmt.Errorf("target binding %q artifact %q resolves to a non-artifact record", binding.Name, reference.ID)
			}
			if err := validateBindingArtifactAgainstContractV1(contract, artifact); err != nil {
				return fmt.Errorf("target binding %q artifact %q: %w", binding.Name, reference.ID, err)
			}
			artifacts = append(artifacts, artifact)
		}
		if err := validateBindingInterpreterCoverageV1(contract, artifacts); err != nil {
			return err
		}
	}
	return nil
}

// A binding advertises a set of interpreters, and the artifacts a target selects
// for that binding must cover every one of them. Checking each artifact against
// the contract in isolation only proves that artifact is usable by at least one
// advertised interpreter, which is satisfied by a single wheel while other
// advertised interpreters have nothing to install.
func validateBindingInterpreterCoverageV1(contract *BindingContractV1, artifacts []*BindingArtifactRecordV1) error {
	for _, version := range contract.SupportedPython {
		parsed, err := pep440.Parse(version)
		if err != nil {
			return fmt.Errorf("contract interpreter %q is invalid", version)
		}
		covered := false
		for _, artifact := range artifacts {
			specifiers, err := pep440.NewSpecifiers(artifact.RequiresPython)
			if err != nil {
				return fmt.Errorf("binding artifact %q requires_python is invalid", artifact.ID)
			}
			if specifiers.Check(parsed) {
				covered = true
				break
			}
		}
		if !covered {
			return fmt.Errorf("binding %q advertises interpreter %q but no selected artifact supports it", contract.Name, version)
		}
	}
	return nil
}

func validateFixtureAgainstTargetV1(contract *ReleaseContractV1, target *TargetRecordV1, fixture *IntegrationFixtureRecordV1) error {
	if !containsRecordValueV1(contract.Contexts, fixture.Context) {
		return fmt.Errorf("context %q is not declared by the release contract", fixture.Context)
	}
	binding := fixture.Binding
	if binding == "" {
		binding = contract.Binding.Default
	}
	if binding == "" && contract.Binding.Required {
		return fmt.Errorf("required binding is missing")
	}
	if binding != "" {
		if !containsRecordValueV1(contract.Binding.Options, binding) {
			return fmt.Errorf("binding %q is not declared by the release contract", binding)
		}
		available := false
		for _, candidate := range target.Bindings {
			available = available || candidate.Name == binding
		}
		if !available {
			return fmt.Errorf("binding %q is unavailable on the target", binding)
		}
	}
	selections := fixture.Selections
	if len(selections) == 0 && len(contract.Selections.Defaults) != 0 {
		selections = contract.Selections.Defaults
	}
	minimum, _ := strconv.ParseUint(contract.Selections.Minimum, 10, 63)
	maximum, _ := strconv.ParseUint(contract.Selections.Maximum, 10, 63)
	if uint64(len(selections)) < minimum || uint64(len(selections)) > maximum || !selectionSetCompatibleV1(selections, contract.Selections.CompatibilityGroups) {
		return fmt.Errorf("selections do not satisfy the release contract")
	}
	for _, selection := range selections {
		if !containsRecordValueV1(contract.Selections.Options, selection) {
			return fmt.Errorf("selection %q is not declared by the release contract", selection)
		}
		available := false
		for _, candidate := range target.Selections {
			available = available || candidate.Name == selection
		}
		if !available {
			return fmt.Errorf("selection %q is unavailable on the target", selection)
		}
	}
	contractParameters := make(map[string]ParameterSchemaV1, len(contract.Parameters))
	for _, parameter := range contract.Parameters {
		contractParameters[parameter.Name] = parameter
	}
	seenParameters := make(map[string]struct{}, len(fixture.Parameters))
	for _, value := range fixture.Parameters {
		parameter, exists := contractParameters[value.Name]
		if !exists || !parameterValueInSchemaV1(value.Value, parameter) || !targetParameterAllowsV1(target.Parameters, value.Name, value.Value) {
			return fmt.Errorf("parameter %q is outside the contract or target domain", value.Name)
		}
		seenParameters[value.Name] = struct{}{}
	}
	for _, parameter := range contract.Parameters {
		if _, exists := seenParameters[parameter.Name]; !exists && parameter.Required && parameter.Default == nil {
			return fmt.Errorf("required parameter %q is missing", parameter.Name)
		}
	}
	return nil
}

func validatePackageSetReferencesV1(records map[string]loadedRecordV1, references []RecordReferenceV1, target *TargetRecordV1) error {
	for _, reference := range references {
		record, err := resolvedRecordV1(records, reference)
		if err != nil {
			return fmt.Errorf("target %q package set: %w", target.ID, err)
		}
		packageSet, ok := record.Value.(*NativePackageSetV1)
		if !ok || packageSet.Manager != target.Target.PackageManager {
			return fmt.Errorf("target %q package set %q uses an incompatible package manager", target.ID, reference.ID)
		}
	}
	return nil
}

func validateTupleContributionsV1(records map[string]loadedRecordV1, contract *ReleaseContractV1, target *TargetRecordV1, tuple supportTupleV1) error {
	packageReferences := append([]RecordReferenceV1{}, target.PackageSets...)
	// Unconditional target payloads belong to no selection, so they must not
	// declare one. Selection-scoped payloads must declare exactly the selection
	// whose entry references them.
	payloadReferences := make([]selectedPayloadReferenceV1, 0, len(target.Payloads))
	for _, reference := range target.Payloads {
		payloadReferences = append(payloadReferences, selectedPayloadReferenceV1{Reference: reference})
	}
	exports := append([]ToolExportV1{}, contract.Exports...)
	exports = append(exports, target.Exports...)
	probes := append([]RecordProbeV1{}, contract.Probes...)
	probes = append(probes, target.Probes...)
	if tuple.Binding != "" {
		for _, binding := range target.Bindings {
			if binding.Name == tuple.Binding {
				packageReferences = append(packageReferences, binding.PackageSets...)
				exports = append(exports, binding.Exports...)
				probes = append(probes, binding.Probes...)
				break
			}
		}
	}
	// Only the selected symbols contribute. An unselected selection's payloads,
	// package sets, exports, and probes never enter this tuple.
	for _, selected := range tuple.Selections {
		for _, selection := range target.Selections {
			if selection.Name == selected {
				packageReferences = append(packageReferences, selection.PackageSets...)
				for _, reference := range selection.Payloads {
					payloadReferences = append(payloadReferences, selectedPayloadReferenceV1{
						Reference: reference, Selection: selection.Name})
				}
				exports = append(exports, selection.Exports...)
				probes = append(probes, selection.Probes...)
				break
			}
		}
	}
	packages := make(map[string]string)
	for _, reference := range packageReferences {
		record, err := resolvedRecordV1(records, reference)
		if err != nil {
			return err
		}
		packageSet, ok := record.Value.(*NativePackageSetV1)
		if !ok {
			return fmt.Errorf("package set %q resolves to a non-package-set record", reference.ID)
		}
		for _, requirement := range packageSet.Requirements {
			parsed, err := blueprint.ParseAPTPackageRequest(requirement)
			if err != nil {
				return err
			}
			if previous, exists := packages[parsed.Name]; exists && previous != requirement {
				return fmt.Errorf("selected package sets conflict on package %q: %q and %q", parsed.Name, previous, requirement)
			}
			packages[parsed.Name] = requirement
		}
	}
	exportPaths := make(map[string]string)
	for _, exported := range exports {
		if previous, exists := exportPaths[exported.Name]; exists && previous != exported.Path {
			return fmt.Errorf("selected contributions conflict on export %q: %q and %q", exported.Name, previous, exported.Path)
		}
		exportPaths[exported.Name] = exported.Path
	}
	if err := validateTuplePayloadsV1(records, payloadReferences, target.Target.Platform); err != nil {
		return err
	}
	// Probe identity is a semantic key: identical probes deduplicate, and the
	// same executable invoked with different arguments is a conflict rather
	// than two probes.
	probeArgs := make(map[string]string)
	for _, probe := range probes {
		joined := strings.Join(probe.Args, "\x00")
		if previous, exists := probeArgs[probe.Path]; exists && previous != joined {
			return fmt.Errorf("selected contributions conflict on probe %q", probe.Path)
		}
		probeArgs[probe.Path] = joined
	}
	return nil
}

// selectedPayloadReferenceV1 pairs a payload reference with the selection whose
// contribution mapping supplied it. An empty selection means the target
// references the payload unconditionally.
type selectedPayloadReferenceV1 struct {
	Reference RecordReferenceV1
	Selection string
}

// Payloads reachable in one support tuple are installed together, so they must
// not claim the same logical artifact or overlap each other's owned directory
// trees. Sharing an unowned parent is allowed; owning a path inside another
// payload's tree is not, however well their package requirements agree.
func validateTuplePayloadsV1(records map[string]loadedRecordV1, references []selectedPayloadReferenceV1, platform string) error {
	type owned struct {
		id        string
		directory string
	}
	logicalPaths := make(map[string]string)
	installed := make([]owned, 0, len(references))
	for _, entry := range references {
		reference := entry.Reference
		record, err := resolvedRecordV1(records, reference)
		if err != nil {
			return err
		}
		payload, ok := record.Value.(*PayloadRecordV1)
		if !ok {
			return fmt.Errorf("payload %q resolves to a non-payload record", reference.ID)
		}
		// A target leaf owns data specific to one architecture, so a payload it
		// installs must be built for that architecture. Record-local validation
		// only proves the payload agrees with its own ID.
		if payload.Platform != platform {
			return fmt.Errorf("payload %q is built for platform %q but the target is %q",
				payload.ID, payload.Platform, platform)
		}
		// Ownership is declared, not inferred: the payload's own selection must
		// be the selection entry that references it, and an unconditional
		// reference must name a payload that belongs to no selection.
		if payload.Selection != entry.Selection {
			if entry.Selection == "" {
				return fmt.Errorf("unconditional target payload %q belongs to selection %q", payload.ID, payload.Selection)
			}
			return fmt.Errorf("selection %q references payload %q, which belongs to selection %q",
				entry.Selection, payload.ID, payload.Selection)
		}
		if previous, exists := logicalPaths[payload.LogicalPath]; exists && previous != payload.ID {
			return fmt.Errorf("co-selectable payloads %q and %q share logical path %q", previous, payload.ID, payload.LogicalPath)
		}
		logicalPaths[payload.LogicalPath] = payload.ID
		for _, other := range installed {
			if other.id == payload.ID {
				continue
			}
			if recordPathOverlapsV1(other.directory, payload.InstallDirectory) {
				return fmt.Errorf("co-selectable payloads %q and %q overlap install destinations %q and %q",
					other.id, payload.ID, other.directory, payload.InstallDirectory)
			}
		}
		installed = append(installed, owned{id: payload.ID, directory: payload.InstallDirectory})
	}
	return nil
}

// Two owned trees overlap when they are equal or one contains the other. A
// shared prefix that is not itself a path segment boundary is not containment.
func recordPathOverlapsV1(left string, right string) bool {
	return left == right ||
		strings.HasPrefix(right, left+"/") ||
		strings.HasPrefix(left, right+"/")
}

func validateTargetFixtureCoverageV1(records map[string]loadedRecordV1, contract *ReleaseContractV1, target *TargetRecordV1, fixtures []*IntegrationFixtureRecordV1) error {
	expected, err := targetSupportTuplesV1(contract, target)
	if err != nil {
		return err
	}
	expectedKeys := make(map[string]supportTupleV1, len(expected))
	for _, tuple := range expected {
		key, err := supportTupleKeyV1(tuple)
		if err != nil {
			return err
		}
		if err := validateTupleContributionsV1(records, contract, target, tuple); err != nil {
			return err
		}
		expectedKeys[key] = tuple
	}
	actualKeys := make(map[string]string, len(fixtures))
	for _, fixture := range fixtures {
		tuple := normalizedFixtureTupleV1(contract, fixture)
		key, err := supportTupleKeyV1(tuple)
		if err != nil {
			return err
		}
		if previous, exists := actualKeys[key]; exists {
			return fmt.Errorf("integration fixtures %q and %q cover the same support tuple", previous, fixture.ID)
		}
		if _, expected := expectedKeys[key]; !expected {
			return fmt.Errorf("integration fixture %q covers an unsupported tuple", fixture.ID)
		}
		actualKeys[key] = fixture.ID
	}
	for _, tuple := range expected {
		key, err := supportTupleKeyV1(tuple)
		if err != nil {
			return err
		}
		if _, covered := actualKeys[key]; !covered {
			return fmt.Errorf("integration fixtures do not cover support tuple context=%q binding=%q selections=%v parameters=%v", tuple.Context, tuple.Binding, tuple.Selections, tuple.Parameters)
		}
	}
	return nil
}
