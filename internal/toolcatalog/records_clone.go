package toolcatalog

// Immutable-value clone helpers for the portable tool record model. Records are
// treated as immutable, so every accessor that hands a record to a caller hands
// out a copy rather than a shared pointer.

func cloneToolRecordV1(value *ToolRecordV1) ToolRecordV1 {
	result := *value
	result.Releases = append([]RecordReferenceV1{}, value.Releases...)
	return result
}

func cloneReleaseManifestV1(value *ReleaseManifestV1) ReleaseManifestV1 {
	result := *value
	result.Aliases = append([]string{}, value.Aliases...)
	result.Targets = append([]RecordReferenceV1{}, value.Targets...)
	result.ArtifactSources = append([]ArtifactSourceMappingV1{}, value.ArtifactSources...)
	result.Provenance = append([]string{}, value.Provenance...)
	result.ValidationProfiles = append([]RecordReferenceV1{}, value.ValidationProfiles...)
	return result
}

func cloneReleaseContractV1(value *ReleaseContractV1) ReleaseContractV1 {
	result := *value
	result.Contexts = append([]string{}, value.Contexts...)
	result.Binding.Options = append([]string{}, value.Binding.Options...)
	result.Selections.Dimensions = cloneSelectionDimensionsV1(value.Selections.Dimensions)
	result.Selections.Combinations = cloneSelectionCombinationsV1(value.Selections.Combinations)
	result.Exports = append([]ToolExportV1{}, value.Exports...)
	result.ResolverPrimitives = append([]string{}, value.ResolverPrimitives...)
	result.CompatibilityConstraints = append([]string{}, value.CompatibilityConstraints...)
	result.Runtime = cloneRuntimeV1(value.Runtime)
	return result
}

func cloneTargetRecordV1(value *TargetRecordV1) TargetRecordV1 {
	result := *value
	result.SupportCases = cloneTargetSupportCasesV1(value.SupportCases)
	result.PackageSets = append([]RecordReferenceV1{}, value.PackageSets...)
	result.Bindings = cloneTargetBindingsV1(value.Bindings)
	result.Payloads = append([]RecordReferenceV1{}, value.Payloads...)
	result.Selections = cloneTargetSelectionsV1(value.Selections)
	result.Exports = append([]ToolExportV1{}, value.Exports...)
	result.IntegrationFixtures = append([]RecordReferenceV1{}, value.IntegrationFixtures...)
	result.ValidationProfiles = append([]RecordReferenceV1{}, value.ValidationProfiles...)
	return result
}

func cloneTargetSupportCasesV1(values []TargetSupportCaseV1) []TargetSupportCaseV1 {
	result := append([]TargetSupportCaseV1{}, values...)
	for index := range result {
		result[index].Bindings = append([]string{}, values[index].Bindings...)
		result[index].Selections = cloneStringSetMapV1(values[index].Selections)
	}
	return result
}

func cloneTargetBindingsV1(values []TargetBindingV1) []TargetBindingV1 {
	result := append([]TargetBindingV1{}, values...)
	for index := range result {
		result[index].Artifacts = append([]RecordReferenceV1{}, values[index].Artifacts...)
		result[index].Payloads = append([]RecordReferenceV1{}, values[index].Payloads...)
		result[index].PackageSets = append([]RecordReferenceV1{}, values[index].PackageSets...)
		result[index].Exports = append([]ToolExportV1{}, values[index].Exports...)
		result[index].ValidationProfiles = append([]RecordReferenceV1{}, values[index].ValidationProfiles...)
	}
	return result
}

func cloneTargetSelectionsV1(values []TargetSelectionV1) []TargetSelectionV1 {
	result := append([]TargetSelectionV1{}, values...)
	for index := range result {
		result[index].Payloads = append([]RecordReferenceV1{}, values[index].Payloads...)
		result[index].PackageSets = append([]RecordReferenceV1{}, values[index].PackageSets...)
		result[index].Exports = append([]ToolExportV1{}, values[index].Exports...)
		result[index].ValidationProfiles = append([]RecordReferenceV1{}, values[index].ValidationProfiles...)
	}
	return result
}

func cloneSelectionDimensionsV1(values []SelectionDimensionV1) []SelectionDimensionV1 {
	result := append([]SelectionDimensionV1{}, values...)
	for index := range result {
		result[index].Options = append([]string{}, values[index].Options...)
	}
	return result
}

func cloneSelectionCombinationsV1(values []SelectionCombinationV1) []SelectionCombinationV1 {
	result := append([]SelectionCombinationV1{}, values...)
	for index := range result {
		result[index] = SelectionCombinationV1(cloneStringSetMapV1(values[index]))
	}
	return result
}

func cloneRuntimeV1(value *RecordRuntimeV1) *RecordRuntimeV1 {
	if value == nil {
		return nil
	}
	result := *value
	result.Environment = append([]RecordEnvironmentVariableV1{}, value.Environment...)
	return &result
}

func cloneProbesV1(values []RecordProbeV1) []RecordProbeV1 {
	result := append([]RecordProbeV1{}, values...)
	for index := range result {
		result[index].Args = append([]string{}, values[index].Args...)
	}
	return result
}

func cloneBindingContractV1(value *BindingContractV1) BindingContractV1 {
	result := *value
	result.Requirements = append([]string{}, value.Requirements...)
	result.SupportedPython = append([]string{}, value.SupportedPython...)
	result.SupportedTags = append([]string{}, value.SupportedTags...)
	result.BundledComponents = append([]BundledComponentV1{}, value.BundledComponents...)
	return result
}

func cloneBindingArtifactV1(value *BindingArtifactRecordV1) BindingArtifactRecordV1 {
	result := *value
	result.Tags = append([]string{}, value.Tags...)
	result.BundledComponents = append([]BundledComponentV1{}, value.BundledComponents...)
	return result
}

func clonePayloadRecordV1(value *PayloadRecordV1) PayloadRecordV1 {
	result := *value
	result.Executables = append([]string{}, value.Executables...)
	return result
}

func cloneNativePackageSetV1(value *NativePackageSetV1) NativePackageSetV1 {
	result := *value
	result.Requirements = append([]string{}, value.Requirements...)
	result.Repositories = append([]string{}, value.Repositories...)
	result.ValidationMetadata = append([]string{}, value.ValidationMetadata...)
	return result
}

func cloneArtifactSourceV1(value *ArtifactSourceRecordV1) ArtifactSourceRecordV1 {
	result := *value
	result.Mirrors = append([]string{}, value.Mirrors...)
	result.Provenance = append([]string{}, value.Provenance...)
	result.Diagnostics = append([]string{}, value.Diagnostics...)
	return result
}

func cloneValidationEvidenceV1(value *ValidationEvidenceV1) ValidationEvidenceV1 {
	result := *value
	result.Bindings = append([]string{}, value.Bindings...)
	result.Selections = cloneStringSetMapV1(value.Selections)
	return result
}

func cloneValidationProfileV1(value *ValidationProfileRecordV1) ValidationProfileRecordV1 {
	result := *value
	result.Probes = cloneProbesV1(value.Probes)
	return result
}

func cloneIntegrationFixtureV1(value *IntegrationFixtureRecordV1) IntegrationFixtureRecordV1 {
	result := *value
	result.Bindings = append([]string{}, value.Bindings...)
	result.Selections = cloneStringSetMapV1(value.Selections)
	result.ValidationProfiles = append([]RecordReferenceV1{}, value.ValidationProfiles...)
	return result
}

func cloneStringSetMapV1(value map[string][]string) map[string][]string {
	if value == nil {
		return nil
	}
	result := make(map[string][]string, len(value))
	for key, values := range value {
		result[key] = append([]string{}, values...)
	}
	return result
}
