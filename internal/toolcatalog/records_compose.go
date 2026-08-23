package toolcatalog

import (
	"fmt"
	"sort"
	"strings"

	pep440 "github.com/aquasecurity/go-pep440-version"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

// supportTupleV1 is one exact combination a target advertises. Bindings and
// selection values are canonical sets; selection dimensions retain their
// names, so values from different dimensions can never collapse together.
type supportTupleV1 struct {
	Context    string              `json:"context"`
	Bindings   []string            `json:"bindings"`
	Selections map[string][]string `json:"selections"`
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

func cloneSelectionMapV1(value map[string][]string) map[string][]string {
	result := make(map[string][]string, len(value))
	for dimension, selections := range value {
		result[dimension] = append([]string{}, selections...)
	}
	return result
}

func normalizedFixtureTupleV1(fixture *IntegrationFixtureRecordV1) supportTupleV1 {
	return supportTupleV1{
		Context: fixture.Context, Bindings: append([]string{}, fixture.Bindings...),
		Selections: cloneSelectionMapV1(fixture.Selections),
	}
}

func targetAdvertisesBindingV1(target *TargetRecordV1, name string) bool {
	for _, binding := range target.Bindings {
		if binding.Name == name {
			return true
		}
	}
	return false
}

func targetAdvertisesSelectionV1(target *TargetRecordV1, dimension string, value string) bool {
	for _, selection := range target.Selections {
		if selection.Dimension == dimension && selection.Value == value {
			return true
		}
	}
	return false
}

func targetBindingSetsV1(target *TargetRecordV1) ([][]string, error) {
	if len(target.Bindings) == 0 {
		return [][]string{{}}, nil
	}
	result := make([][]string, 0)
	current := make([]string, 0, len(target.Bindings))
	var enumerate func(int) error
	enumerate = func(index int) error {
		if index == len(target.Bindings) {
			if len(current) == 0 {
				return nil
			}
			if len(result) == maxDefinitionValidationCases {
				return fmt.Errorf("binding-set coverage exceeds the validation case limit")
			}
			result = append(result, append([]string{}, current...))
			return nil
		}
		if err := enumerate(index + 1); err != nil {
			return err
		}
		current = append(current, target.Bindings[index].Name)
		if err := enumerate(index + 1); err != nil {
			return err
		}
		current = current[:len(current)-1]
		return nil
	}
	if err := enumerate(0); err != nil {
		return nil, err
	}
	sort.Slice(result, func(left int, right int) bool {
		return compareRecordStringSlicesV1(result[left], result[right]) < 0
	})
	return result, nil
}

func selectionMapForCombinationV1(schema SelectionSchemaV1, combination SelectionCombinationV1) map[string][]string {
	result := make(map[string][]string, len(schema.Dimensions))
	for index, dimension := range schema.Dimensions {
		result[dimension.Name] = append([]string{}, combination.Values[index]...)
	}
	return result
}

func targetSupportsSelectionCombinationV1(target *TargetRecordV1, schema SelectionSchemaV1, combination SelectionCombinationV1) bool {
	if len(combination.Values) != len(schema.Dimensions) {
		return false
	}
	for index, values := range combination.Values {
		for _, value := range values {
			if !targetAdvertisesSelectionV1(target, schema.Dimensions[index].Name, value) {
				return false
			}
		}
	}
	return true
}

func targetSelectionCombinationsV1(contract *ReleaseContractV1, target *TargetRecordV1) []map[string][]string {
	if len(contract.Selections.Dimensions) == 0 {
		return []map[string][]string{{}}
	}
	result := make([]map[string][]string, 0, len(contract.Selections.Combinations))
	for _, combination := range contract.Selections.Combinations {
		if targetSupportsSelectionCombinationV1(target, contract.Selections, combination) {
			result = append(result, selectionMapForCombinationV1(contract.Selections, combination))
		}
	}
	return result
}

func targetSupportTuplesV1(contract *ReleaseContractV1, target *TargetRecordV1) ([]supportTupleV1, error) {
	bindings, err := targetBindingSetsV1(target)
	if err != nil {
		return nil, err
	}
	selections := targetSelectionCombinationsV1(contract, target)
	result := make([]supportTupleV1, 0)
	for _, context := range contract.Contexts {
		for _, bindingSet := range bindings {
			for _, selectionMap := range selections {
				if len(result) == maxDefinitionValidationCases {
					return nil, fmt.Errorf("target support tuple coverage exceeds the validation case limit")
				}
				result = append(result, supportTupleV1{Context: context, Bindings: append([]string{}, bindingSet...), Selections: cloneSelectionMapV1(selectionMap)})
			}
		}
	}
	return result, nil
}

func selectionDeclaredV1(schema SelectionSchemaV1, dimension string, value string) bool {
	for _, declared := range schema.Dimensions {
		if declared.Name == dimension {
			return containsRecordValueV1(declared.Options, value)
		}
	}
	return false
}

func selectionPairInSupportedCombinationV1(contract *ReleaseContractV1, target *TargetRecordV1, dimension string, value string) bool {
	for _, combination := range contract.Selections.Combinations {
		if !targetSupportsSelectionCombinationV1(target, contract.Selections, combination) {
			continue
		}
		for index, declared := range contract.Selections.Dimensions {
			if declared.Name == dimension && containsRecordValueV1(combination.Values[index], value) {
				return true
			}
		}
	}
	return false
}

func validateTargetAgainstContractV1(contract *ReleaseContractV1, target *TargetRecordV1) error {
	for _, binding := range target.Bindings {
		if !containsRecordValueV1(contract.Binding.Options, binding.Name) {
			return fmt.Errorf("target binding mapping %q is not declared by the release contract", binding.Name)
		}
	}
	for _, selection := range target.Selections {
		if !selectionDeclaredV1(contract.Selections, selection.Dimension, selection.Value) {
			return fmt.Errorf("target selection mapping %q=%q is not declared by the release contract", selection.Dimension, selection.Value)
		}
		if !selectionPairInSupportedCombinationV1(contract, target, selection.Dimension, selection.Value) {
			return fmt.Errorf("target selection mapping %q=%q participates in no supported combination", selection.Dimension, selection.Value)
		}
	}
	if len(contract.Selections.Dimensions) != 0 && len(targetSelectionCombinationsV1(contract, target)) == 0 {
		return fmt.Errorf("target advertises no complete release-contract selection combination")
	}
	if _, err := targetBindingSetsV1(target); err != nil {
		return err
	}
	if _, err := targetSupportTuplesV1(contract, target); err != nil {
		return err
	}
	return nil
}

func validateBindingArtifactAgainstContractV1(contract *BindingContractV1, artifact *BindingArtifactRecordV1) error {
	distribution := pythonprovider.NormalizeDistributionName(artifact.Name)
	if distribution != pythonprovider.NormalizeDistributionName(contract.Package) {
		return fmt.Errorf("artifact distribution %q does not match contract package %q", distribution, contract.Package)
	}
	wheelVersion, err := pep440.Parse(artifact.EcosystemVersion)
	if err != nil {
		return fmt.Errorf("artifact ecosystem version is invalid")
	}
	requirementFound := false
	for _, requirement := range contract.Requirements {
		requirementDistribution, err := pythonprovider.PackageRootDistributionNameV1(requirement)
		if err != nil {
			return fmt.Errorf("contract requirement %q: %w", requirement, err)
		}
		if requirementDistribution != distribution {
			continue
		}
		requirementFound = true
		remainder := ""
		for index, character := range requirement {
			if strings.ContainsRune("<>=!~", character) {
				remainder = requirement[index:]
				break
			}
		}
		if remainder != "" {
			specifiers, err := pep440.NewSpecifiers(remainder)
			if err != nil || !specifiers.Check(wheelVersion) {
				return fmt.Errorf("artifact version %q does not satisfy contract requirement %q", artifact.EcosystemVersion, requirement)
			}
		}
		break
	}
	if !requirementFound {
		return fmt.Errorf("contract package %q has no binding requirement", contract.Package)
	}
	compatibleTag := false
	for _, tag := range artifact.Tags {
		compatibleTag = compatibleTag || containsRecordValueV1(contract.SupportedTags, tag)
	}
	if !compatibleTag {
		return fmt.Errorf("artifact tags are incompatible with the binding contract")
	}
	pythonSpecifiers, err := pep440.NewSpecifiers(artifact.RequiresPython)
	if err != nil {
		return fmt.Errorf("requires_python is invalid")
	}
	bundled := make(map[string]BundledComponentV1, len(artifact.BundledComponents))
	for _, component := range artifact.BundledComponents {
		bundled[component.Name] = component
	}
	for _, declared := range contract.BundledComponents {
		present, exists := bundled[declared.Name]
		if !exists {
			return fmt.Errorf("contract declares bundled component %q which the artifact does not bundle", declared.Name)
		}
		if present.Version != declared.Version || present.Path != declared.Path {
			return fmt.Errorf("bundled component %q does not match the contract", declared.Name)
		}
	}
	for _, version := range contract.SupportedPython {
		parsed, err := pep440.Parse(version)
		if err == nil && pythonSpecifiers.Check(parsed) {
			return nil
		}
	}
	return fmt.Errorf("requires_python %q excludes every contract interpreter", artifact.RequiresPython)
}

func validateTargetBindingsAgainstContractsV1(records map[string]loadedRecordV1, target *TargetRecordV1) error {
	for _, binding := range target.Bindings {
		record, err := resolvedRecordV1(records, binding.Contract)
		if err != nil {
			return err
		}
		contract, ok := record.Value.(*BindingContractV1)
		if !ok || contract.Name != binding.Name {
			return fmt.Errorf("target binding %q contract resolves to an incompatible record", binding.Name)
		}
		artifacts := make([]*BindingArtifactRecordV1, 0, len(binding.Artifacts))
		for _, reference := range binding.Artifacts {
			record, err := resolvedRecordV1(records, reference)
			if err != nil {
				return err
			}
			artifact, ok := record.Value.(*BindingArtifactRecordV1)
			if !ok || artifact.Binding != binding.Name || artifact.Contract != binding.Contract {
				return fmt.Errorf("target binding %q artifact %q resolves to an incompatible record", binding.Name, reference.ID)
			}
			if artifact.Platform != target.Target.Platform {
				return fmt.Errorf("target binding %q artifact %q is built for platform %q, not target platform %q", binding.Name, reference.ID, artifact.Platform, target.Target.Platform)
			}
			if err := validateBindingArtifactAgainstContractV1(contract, artifact); err != nil {
				return fmt.Errorf("target binding %q artifact %q: %w", binding.Name, reference.ID, err)
			}
			artifacts = append(artifacts, artifact)
		}
		if err := validateBindingInterpreterCoverageV1(contract, artifacts); err != nil {
			return fmt.Errorf("target binding %q: %w", binding.Name, err)
		}
	}
	return nil
}

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
				return fmt.Errorf("artifact %q requires_python is invalid", artifact.ID)
			}
			covered = covered || specifiers.Check(parsed)
		}
		if !covered {
			return fmt.Errorf("binding %q has no artifact covering interpreter %q", contract.Name, version)
		}
	}
	return nil
}

func exactReferenceUnionV1(groups ...[]RecordReferenceV1) ([]RecordReferenceV1, error) {
	byID := make(map[string]RecordReferenceV1)
	for _, group := range groups {
		for _, reference := range group {
			if previous, exists := byID[reference.ID]; exists && previous != reference {
				return nil, fmt.Errorf("reference %q has conflicting exact identities", reference.ID)
			}
			byID[reference.ID] = reference
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]RecordReferenceV1, 0, len(ids))
	for _, id := range ids {
		result = append(result, byID[id])
	}
	return result, nil
}

func selectedProfileReferencesV1(target *TargetRecordV1, tuple supportTupleV1) ([]RecordReferenceV1, error) {
	groups := [][]RecordReferenceV1{target.ValidationProfiles}
	for _, selected := range tuple.Bindings {
		for _, binding := range target.Bindings {
			if binding.Name == selected {
				groups = append(groups, binding.ValidationProfiles)
				break
			}
		}
	}
	dimensions := make([]string, 0, len(tuple.Selections))
	for dimension := range tuple.Selections {
		dimensions = append(dimensions, dimension)
	}
	sort.Strings(dimensions)
	for _, dimension := range dimensions {
		for _, value := range tuple.Selections[dimension] {
			for _, selection := range target.Selections {
				if selection.Dimension == dimension && selection.Value == value {
					groups = append(groups, selection.ValidationProfiles)
					break
				}
			}
		}
	}
	return exactReferenceUnionV1(groups...)
}

func equalReferenceListsV1(left []RecordReferenceV1, right []RecordReferenceV1) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validateFixtureAgainstTargetV1(contract *ReleaseContractV1, target *TargetRecordV1, fixture *IntegrationFixtureRecordV1) error {
	if fixture.Target != target.Target {
		return fmt.Errorf("fixture target does not match the target record")
	}
	if !containsRecordValueV1(contract.Contexts, fixture.Context) {
		return fmt.Errorf("context %q is not declared by the release contract", fixture.Context)
	}
	if len(target.Bindings) == 0 && len(fixture.Bindings) != 0 || len(target.Bindings) != 0 && len(fixture.Bindings) == 0 {
		return fmt.Errorf("fixture binding set is not supported by the target")
	}
	for _, binding := range fixture.Bindings {
		if !containsRecordValueV1(contract.Binding.Options, binding) || !targetAdvertisesBindingV1(target, binding) {
			return fmt.Errorf("binding %q is unavailable on the target", binding)
		}
	}
	combinationSupported := len(contract.Selections.Dimensions) == 0 && len(fixture.Selections) == 0
	for _, combination := range contract.Selections.Combinations {
		if !targetSupportsSelectionCombinationV1(target, contract.Selections, combination) {
			continue
		}
		candidateKey, _ := canonical.Marshal(selectionMapForCombinationV1(contract.Selections, combination))
		fixtureKey, _ := canonical.Marshal(fixture.Selections)
		combinationSupported = combinationSupported || string(candidateKey) == string(fixtureKey)
	}
	if !combinationSupported {
		return fmt.Errorf("fixture selections do not match one advertised target combination")
	}
	tuple := normalizedFixtureTupleV1(fixture)
	expectedProfiles, err := selectedProfileReferencesV1(target, tuple)
	if err != nil {
		return err
	}
	fixtureProfiles, err := exactReferenceUnionV1(fixture.ValidationProfiles)
	if err != nil {
		return err
	}
	if !equalReferenceListsV1(expectedProfiles, fixtureProfiles) {
		return fmt.Errorf("fixture validation profiles do not match the selected target contributions")
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

func selectedContributionReferencesV1(target *TargetRecordV1, tuple supportTupleV1) (packages, payloads, artifacts, profiles []RecordReferenceV1, exports []ToolExportV1, err error) {
	packageGroups := [][]RecordReferenceV1{target.PackageSets}
	payloadGroups := [][]RecordReferenceV1{target.Payloads}
	artifactGroups := make([][]RecordReferenceV1, 0)
	profileGroups := [][]RecordReferenceV1{target.ValidationProfiles}
	exports = append(exports, target.Exports...)
	for _, selected := range tuple.Bindings {
		for _, binding := range target.Bindings {
			if binding.Name != selected {
				continue
			}
			packageGroups = append(packageGroups, binding.PackageSets)
			payloadGroups = append(payloadGroups, binding.Payloads)
			artifactGroups = append(artifactGroups, binding.Artifacts)
			profileGroups = append(profileGroups, binding.ValidationProfiles)
			exports = append(exports, binding.Exports...)
			break
		}
	}
	dimensions := make([]string, 0, len(tuple.Selections))
	for dimension := range tuple.Selections {
		dimensions = append(dimensions, dimension)
	}
	sort.Strings(dimensions)
	for _, dimension := range dimensions {
		for _, value := range tuple.Selections[dimension] {
			for _, selection := range target.Selections {
				if selection.Dimension != dimension || selection.Value != value {
					continue
				}
				packageGroups = append(packageGroups, selection.PackageSets)
				payloadGroups = append(payloadGroups, selection.Payloads)
				profileGroups = append(profileGroups, selection.ValidationProfiles)
				exports = append(exports, selection.Exports...)
				break
			}
		}
	}
	if packages, err = exactReferenceUnionV1(packageGroups...); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	if payloads, err = exactReferenceUnionV1(payloadGroups...); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	if artifacts, err = exactReferenceUnionV1(artifactGroups...); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	if profiles, err = exactReferenceUnionV1(profileGroups...); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	return packages, payloads, artifacts, profiles, exports, nil
}

func validateTupleContributionsV1(records map[string]loadedRecordV1, contract *ReleaseContractV1, target *TargetRecordV1, tuple supportTupleV1) error {
	packages, payloads, artifacts, profiles, exports, err := selectedContributionReferencesV1(target, tuple)
	if err != nil {
		return err
	}
	packagesByName := make(map[string]string)
	for _, reference := range packages {
		record, err := resolvedRecordV1(records, reference)
		if err != nil {
			return err
		}
		packageSet, ok := record.Value.(*NativePackageSetV1)
		if !ok || packageSet.Manager != target.Target.PackageManager {
			return fmt.Errorf("package set %q resolves to an incompatible record", reference.ID)
		}
		for _, requirement := range packageSet.Requirements {
			parsed, err := blueprint.ParseAPTPackageRequest(requirement)
			if err != nil {
				return err
			}
			if previous, exists := packagesByName[parsed.Name]; exists && previous != requirement {
				return fmt.Errorf("selected package sets conflict on package %q: %q and %q", parsed.Name, previous, requirement)
			}
			packagesByName[parsed.Name] = requirement
		}
	}
	exports = append(append([]ToolExportV1{}, contract.Exports...), exports...)
	for _, selected := range tuple.Bindings {
		for _, binding := range target.Bindings {
			if binding.Name != selected {
				continue
			}
			record, err := resolvedRecordV1(records, binding.Contract)
			if err != nil {
				return err
			}
			bindingContract, ok := record.Value.(*BindingContractV1)
			if !ok || bindingContract.Name != binding.Name {
				return fmt.Errorf("target binding %q contract resolves to an incompatible record", binding.Name)
			}
			exports = append(exports, bindingContract.CLI)
			break
		}
	}
	exportPaths := make(map[string]string)
	for _, exported := range exports {
		if previous, exists := exportPaths[exported.Name]; exists && previous != exported.Path {
			return fmt.Errorf("selected contributions conflict on export %q: %q and %q", exported.Name, previous, exported.Path)
		}
		exportPaths[exported.Name] = exported.Path
	}
	if err := validateTuplePayloadsV1(records, payloads, target.Target.Platform); err != nil {
		return err
	}
	for _, reference := range artifacts {
		record, err := resolvedRecordV1(records, reference)
		if err != nil {
			return err
		}
		if _, ok := record.Value.(*BindingArtifactRecordV1); !ok {
			return fmt.Errorf("binding artifact %q resolves to a non-artifact record", reference.ID)
		}
	}
	for _, reference := range profiles {
		record, err := resolvedRecordV1(records, reference)
		if err != nil {
			return err
		}
		if _, ok := record.Value.(*ValidationProfileRecordV1); !ok {
			return fmt.Errorf("validation profile %q resolves to a non-profile record", reference.ID)
		}
	}
	return nil
}

func validateTuplePayloadsV1(records map[string]loadedRecordV1, references []RecordReferenceV1, platform string) error {
	type owned struct {
		id        string
		directory string
	}
	logicalPaths := make(map[string]string)
	installed := make([]owned, 0, len(references))
	for _, reference := range references {
		record, err := resolvedRecordV1(records, reference)
		if err != nil {
			return err
		}
		payload, ok := record.Value.(*PayloadRecordV1)
		if !ok {
			return fmt.Errorf("payload %q resolves to a non-payload record", reference.ID)
		}
		if payload.Platform != platform {
			return fmt.Errorf("payload %q is built for platform %q but the target is %q", payload.ID, payload.Platform, platform)
		}
		if previous, exists := logicalPaths[payload.LogicalPath]; exists && previous != payload.ID {
			return fmt.Errorf("co-selectable payloads %q and %q share logical path %q", previous, payload.ID, payload.LogicalPath)
		}
		logicalPaths[payload.LogicalPath] = payload.ID
		for _, other := range installed {
			if other.id != payload.ID && recordPathOverlapsV1(other.directory, payload.InstallDirectory) {
				return fmt.Errorf("co-selectable payloads %q and %q overlap install destinations %q and %q", other.id, payload.ID, other.directory, payload.InstallDirectory)
			}
		}
		installed = append(installed, owned{id: payload.ID, directory: payload.InstallDirectory})
	}
	return nil
}

func recordPathOverlapsV1(left string, right string) bool {
	return left == right || strings.HasPrefix(right, left+"/") || strings.HasPrefix(left, right+"/")
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
		if err := validateFixtureAgainstTargetV1(contract, target, fixture); err != nil {
			return err
		}
		tuple := normalizedFixtureTupleV1(fixture)
		key, err := supportTupleKeyV1(tuple)
		if err != nil {
			return err
		}
		if previous, exists := actualKeys[key]; exists {
			return fmt.Errorf("integration fixtures %q and %q cover the same support tuple", previous, fixture.ID)
		}
		if _, supported := expectedKeys[key]; !supported {
			return fmt.Errorf("integration fixture %q covers an unsupported tuple", fixture.ID)
		}
		actualKeys[key] = fixture.ID
	}
	for key, tuple := range expectedKeys {
		if _, covered := actualKeys[key]; !covered {
			return fmt.Errorf("integration fixtures do not cover support tuple context=%q bindings=%v selections=%v", tuple.Context, tuple.Bindings, tuple.Selections)
		}
	}
	return nil
}
