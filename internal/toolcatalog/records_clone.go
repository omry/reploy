package toolcatalog

import "github.com/omry/reploy/internal/canonical"

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
	return result
}

func cloneReleaseContractV1(value *ReleaseContractV1) ReleaseContractV1 {
	result := *value
	result.Contexts = append([]string{}, value.Contexts...)
	result.Binding.Options = append([]string{}, value.Binding.Options...)
	result.Selections.Options = append([]string{}, value.Selections.Options...)
	result.Selections.Defaults = append([]string{}, value.Selections.Defaults...)
	result.Selections.CompatibilityGroups = cloneStringGroupsV1(value.Selections.CompatibilityGroups)
	result.Parameters = cloneParameterSchemasV1(value.Parameters)
	result.Probes = cloneProbesV1(value.Probes)
	result.Exports = append([]ToolExportV1{}, value.Exports...)
	result.ResolverPrimitives = append([]string{}, value.ResolverPrimitives...)
	result.Runtime = cloneRuntimeV1(value.Runtime)
	return result
}

func cloneTargetRecordV1(value *TargetRecordV1) TargetRecordV1 {
	result := *value
	result.PackageSets = append([]RecordReferenceV1{}, value.PackageSets...)
	result.Bindings = cloneTargetBindingsV1(value.Bindings)
	result.Payloads = append([]RecordReferenceV1{}, value.Payloads...)
	result.Selections = cloneTargetSelectionsV1(value.Selections)
	result.Parameters = cloneTargetParameterConstraintsV1(value.Parameters)
	result.Exports = append([]ToolExportV1{}, value.Exports...)
	result.Probes = cloneProbesV1(value.Probes)
	result.IntegrationFixtures = append([]RecordReferenceV1{}, value.IntegrationFixtures...)
	return result
}

func cloneTargetBindingsV1(values []TargetBindingV1) []TargetBindingV1 {
	result := append([]TargetBindingV1{}, values...)
	for index := range result {
		result[index].Artifacts = append([]RecordReferenceV1{}, values[index].Artifacts...)
		result[index].PackageSets = append([]RecordReferenceV1{}, values[index].PackageSets...)
		result[index].Exports = append([]ToolExportV1{}, values[index].Exports...)
		result[index].Probes = cloneProbesV1(values[index].Probes)
	}
	return result
}

func cloneTargetSelectionsV1(values []TargetSelectionV1) []TargetSelectionV1 {
	result := append([]TargetSelectionV1{}, values...)
	for index := range result {
		result[index].Payloads = append([]RecordReferenceV1{}, values[index].Payloads...)
		result[index].PackageSets = append([]RecordReferenceV1{}, values[index].PackageSets...)
		result[index].Exports = append([]ToolExportV1{}, values[index].Exports...)
		result[index].Probes = cloneProbesV1(values[index].Probes)
	}
	return result
}

func cloneTargetParameterConstraintsV1(values []TargetParameterConstraintV1) []TargetParameterConstraintV1 {
	result := append([]TargetParameterConstraintV1{}, values...)
	for index := range result {
		result[index].Values = append([]string{}, values[index].Values...)
	}
	return result
}

func cloneParameterSchemasV1(values []ParameterSchemaV1) []ParameterSchemaV1 {
	result := append([]ParameterSchemaV1{}, values...)
	for index := range result {
		result[index].Values = append([]string{}, values[index].Values...)
		if values[index].Default != nil {
			defaultValue := *values[index].Default
			result[index].Default = &defaultValue
		}
	}
	return result
}

func cloneStringGroupsV1(values [][]string) [][]string {
	result := append([][]string{}, values...)
	for index := range result {
		result[index] = append([]string{}, values[index]...)
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

func clonePayloadRecordV1(value *PayloadRecordV1) PayloadRecordV1 { return *value }

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
	result.Selections = append([]string{}, value.Selections...)
	result.Parameters = append([]ParameterValueV1{}, value.Parameters...)
	result.ProbeDigests = append([]canonical.Digest{}, value.ProbeDigests...)
	return result
}

func cloneValidationProfileV1(value *ValidationProfileRecordV1) ValidationProfileRecordV1 {
	result := *value
	result.Probes = cloneProbesV1(value.Probes)
	return result
}

func cloneIntegrationFixtureV1(value *IntegrationFixtureRecordV1) IntegrationFixtureRecordV1 {
	result := *value
	result.Selections = append([]string{}, value.Selections...)
	result.Parameters = append([]ParameterValueV1{}, value.Parameters...)
	return result
}
