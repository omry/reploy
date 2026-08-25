package toolcatalog

// Immutable-value clone helpers for the portable tool record model. Records are
// treated as immutable, so every accessor that hands a record to a caller hands
// out a copy rather than a shared pointer.

func cloneSliceV1[T any](values []T) []T {
	if values == nil {
		return nil
	}
	return append([]T{}, values...)
}

func cloneToolRecordV1(value *ToolRecordV1) ToolRecordV1 {
	result := *value
	result.Releases = cloneSliceV1(value.Releases)
	return result
}

func cloneReleaseManifestV1(value *ReleaseManifestV1) ReleaseManifestV1 {
	result := *value
	result.Aliases = cloneSliceV1(value.Aliases)
	result.Targets = cloneSliceV1(value.Targets)
	result.ArtifactSources = cloneSliceV1(value.ArtifactSources)
	result.Provenance = cloneSliceV1(value.Provenance)
	result.ValidationProfiles = cloneSliceV1(value.ValidationProfiles)
	return result
}

func cloneReleaseContractV1(value *ReleaseContractV1) ReleaseContractV1 {
	result := *value
	result.Contexts = cloneSliceV1(value.Contexts)
	result.Binding.Options = cloneSliceV1(value.Binding.Options)
	result.Selections.Dimensions = cloneSelectionDimensionsV1(value.Selections.Dimensions)
	result.Selections.Combinations = cloneSelectionCombinationsV1(value.Selections.Combinations)
	result.Exports = cloneSliceV1(value.Exports)
	result.ResolverPrimitives = cloneSliceV1(value.ResolverPrimitives)
	result.CompatibilityConstraints = cloneSliceV1(value.CompatibilityConstraints)
	result.Runtime = cloneRuntimeV1(value.Runtime)
	return result
}

func cloneTargetRecordV1(value *TargetRecordV1) TargetRecordV1 {
	result := *value
	result.SupportCases = cloneTargetSupportCasesV1(value.SupportCases)
	result.PackageSets = cloneSliceV1(value.PackageSets)
	result.Bindings = cloneTargetBindingsV1(value.Bindings)
	result.Payloads = cloneSliceV1(value.Payloads)
	result.Selections = cloneTargetSelectionsV1(value.Selections)
	result.Exports = cloneSliceV1(value.Exports)
	result.IntegrationFixtures = cloneSliceV1(value.IntegrationFixtures)
	result.ValidationProfiles = cloneSliceV1(value.ValidationProfiles)
	return result
}

func cloneTargetSupportCasesV1(values []TargetSupportCaseV1) []TargetSupportCaseV1 {
	result := cloneSliceV1(values)
	for index := range result {
		result[index].Bindings = cloneSliceV1(values[index].Bindings)
		result[index].Selections = cloneStringSetMapV1(values[index].Selections)
	}
	return result
}

func cloneTargetBindingsV1(values []TargetBindingV1) []TargetBindingV1 {
	result := cloneSliceV1(values)
	for index := range result {
		result[index].Artifacts = cloneSliceV1(values[index].Artifacts)
		result[index].Payloads = cloneSliceV1(values[index].Payloads)
		result[index].PackageSets = cloneSliceV1(values[index].PackageSets)
		result[index].Exports = cloneSliceV1(values[index].Exports)
		result[index].ValidationProfiles = cloneSliceV1(values[index].ValidationProfiles)
	}
	return result
}

func cloneTargetSelectionsV1(values []TargetSelectionV1) []TargetSelectionV1 {
	result := cloneSliceV1(values)
	for index := range result {
		result[index].Payloads = cloneSliceV1(values[index].Payloads)
		result[index].PackageSets = cloneSliceV1(values[index].PackageSets)
		result[index].Exports = cloneSliceV1(values[index].Exports)
		result[index].ValidationProfiles = cloneSliceV1(values[index].ValidationProfiles)
	}
	return result
}

func cloneSelectionDimensionsV1(values []SelectionDimensionV1) []SelectionDimensionV1 {
	result := cloneSliceV1(values)
	for index := range result {
		result[index].Options = cloneSliceV1(values[index].Options)
	}
	return result
}

func cloneSelectionCombinationsV1(values []SelectionCombinationV1) []SelectionCombinationV1 {
	result := cloneSliceV1(values)
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
	result.Environment = cloneSliceV1(value.Environment)
	return &result
}

func cloneProbesV1(values []RecordProbeV1) []RecordProbeV1 {
	result := cloneSliceV1(values)
	for index := range result {
		result[index].Args = cloneSliceV1(values[index].Args)
	}
	return result
}

func cloneBindingContractV1(value *BindingContractV1) BindingContractV1 {
	result := *value
	result.Requirements = cloneSliceV1(value.Requirements)
	result.SupportedPython = cloneSliceV1(value.SupportedPython)
	result.SupportedTags = cloneSliceV1(value.SupportedTags)
	result.BundledComponents = cloneSliceV1(value.BundledComponents)
	return result
}

func cloneBindingArtifactV1(value *BindingArtifactRecordV1) BindingArtifactRecordV1 {
	result := *value
	result.Tags = cloneSliceV1(value.Tags)
	result.BundledComponents = cloneSliceV1(value.BundledComponents)
	return result
}

func clonePayloadRecordV1(value *PayloadRecordV1) PayloadRecordV1 {
	result := *value
	result.Executables = cloneSliceV1(value.Executables)
	return result
}

func cloneNativePackageSetV1(value *NativePackageSetV1) NativePackageSetV1 {
	result := *value
	result.Requirements = cloneSliceV1(value.Requirements)
	result.Repositories = cloneSliceV1(value.Repositories)
	result.ValidationMetadata = cloneSliceV1(value.ValidationMetadata)
	return result
}

func cloneArtifactSourceV1(value *ArtifactSourceRecordV1) ArtifactSourceRecordV1 {
	result := *value
	result.Mirrors = cloneSliceV1(value.Mirrors)
	result.Provenance = cloneSliceV1(value.Provenance)
	result.Diagnostics = cloneSliceV1(value.Diagnostics)
	return result
}

func cloneValidationEvidenceV1(value *ValidationEvidenceV1) ValidationEvidenceV1 {
	result := *value
	result.Bindings = cloneSliceV1(value.Bindings)
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
	result.Bindings = cloneSliceV1(value.Bindings)
	result.Selections = cloneStringSetMapV1(value.Selections)
	result.ValidationProfiles = cloneSliceV1(value.ValidationProfiles)
	return result
}

func cloneStringSetMapV1(value map[string][]string) map[string][]string {
	if value == nil {
		return nil
	}
	result := make(map[string][]string, len(value))
	for key, values := range value {
		result[key] = cloneSliceV1(values)
	}
	return result
}
