package toolcatalog

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

// CompilePortableToolPlanV1 projects selected catalog closures into the
// provider-neutral plan consumed by provider planning. The compiler owns the
// projection boundary: catalog record structs stay on the catalog side while
// the resulting envelopes are canonical provider data.
//
// The input is treated as immutable. Every output collection is freshly
// allocated and ordered according to the provider plan contract.
func CompilePortableToolPlanV1(closures []SelectedClosureV1) (providers.PortableToolPlanV1, error) {
	plan := providers.PortableToolPlanV1{
		Schema: providers.PortableToolPlanSchemaV1,
		Tools:  make([]providers.PortableToolPlanEntryV1, 0, len(closures)),
	}
	for index := range closures {
		entry, err := compilePortableToolPlanEntryV1(&closures[index])
		if err != nil {
			return providers.PortableToolPlanV1{}, fmt.Errorf("compile portable tool closure %d: %w", index, err)
		}
		plan.Tools = append(plan.Tools, entry)
	}
	sort.Slice(plan.Tools, func(left int, right int) bool {
		return portableToolPlanEntryLessV1(plan.Tools[left], plan.Tools[right])
	})
	if err := providers.ValidatePortableToolPlanV1(plan); err != nil {
		return providers.PortableToolPlanV1{}, fmt.Errorf("validate compiled portable tool plan: %w", err)
	}
	return plan, nil
}

func compilePortableToolPlanEntryV1(closure *SelectedClosureV1) (providers.PortableToolPlanEntryV1, error) {
	responsibilities, err := compilePortableToolResponsibilitiesV1(&closure.Records)
	if err != nil {
		return providers.PortableToolPlanEntryV1{}, err
	}
	exports, err := compilePortableToolExportsV1(closure)
	if err != nil {
		return providers.PortableToolPlanEntryV1{}, err
	}
	profiles, err := compilePortableToolValidationProfilesV1(closure.Profiles)
	if err != nil {
		return providers.PortableToolPlanEntryV1{}, err
	}
	runtime, err := compilePortableToolRuntimeV1(closure.Contract.Runtime)
	if err != nil {
		return providers.PortableToolPlanEntryV1{}, err
	}
	return providers.PortableToolPlanEntryV1{
		Scope:                 closure.Scope,
		SelectedClosureDigest: closure.Identity,
		Provenance: providers.PortableToolReleaseProvenanceV1{
			Tool:           closure.Provenance.Tool,
			Version:        closure.Provenance.Version,
			Revision:       closure.Provenance.Revision,
			ManifestDigest: closure.Provenance.ManifestDigest,
		},
		Runtime:            runtime,
		Responsibilities:   responsibilities,
		Exports:            exports,
		ValidationProfiles: profiles,
	}, nil
}

func compilePortableToolResponsibilitiesV1(records *SelectedClosureRecordsV1) (providers.PortableToolResponsibilitiesV1, error) {
	result := providers.PortableToolResponsibilitiesV1{
		BindingContracts:  make([]providers.PortableToolSelectedRecordV1, 0, len(records.BindingContracts)),
		BindingArtifacts:  make([]providers.PortableToolSelectedRecordV1, 0, len(records.BindingArtifacts)),
		Payloads:          make([]providers.PortableToolSelectedRecordV1, 0, len(records.Payloads)),
		NativePackageSets: make([]providers.PortableToolSelectedRecordV1, 0, len(records.PackageSets)),
	}
	for index := range records.BindingContracts {
		record, err := portableToolRecordEnvelopeV1(records.BindingContracts[index].Record)
		if err != nil {
			return providers.PortableToolResponsibilitiesV1{}, fmt.Errorf("binding contract %d: %w", index, err)
		}
		result.BindingContracts = append(result.BindingContracts, providers.PortableToolSelectedRecordV1{
			Reference: portableToolRecordReferenceV1(records.BindingContracts[index].Reference), Record: record,
		})
	}
	for index := range records.BindingArtifacts {
		record, err := portableToolRecordEnvelopeV1(records.BindingArtifacts[index].Record)
		if err != nil {
			return providers.PortableToolResponsibilitiesV1{}, fmt.Errorf("binding artifact %d: %w", index, err)
		}
		result.BindingArtifacts = append(result.BindingArtifacts, providers.PortableToolSelectedRecordV1{
			Reference: portableToolRecordReferenceV1(records.BindingArtifacts[index].Reference), Record: record,
		})
	}
	for index := range records.Payloads {
		record, err := portableToolRecordEnvelopeV1(records.Payloads[index].Record)
		if err != nil {
			return providers.PortableToolResponsibilitiesV1{}, fmt.Errorf("payload %d: %w", index, err)
		}
		result.Payloads = append(result.Payloads, providers.PortableToolSelectedRecordV1{
			Reference: portableToolRecordReferenceV1(records.Payloads[index].Reference), Record: record,
		})
	}
	for index := range records.PackageSets {
		record, err := portableToolRecordEnvelopeV1(records.PackageSets[index].Record)
		if err != nil {
			return providers.PortableToolResponsibilitiesV1{}, fmt.Errorf("native package set %d: %w", index, err)
		}
		result.NativePackageSets = append(result.NativePackageSets, providers.PortableToolSelectedRecordV1{
			Reference: portableToolRecordReferenceV1(records.PackageSets[index].Reference), Record: record,
		})
	}
	sortPortableToolRecordsV1(result.BindingContracts)
	sortPortableToolRecordsV1(result.BindingArtifacts)
	sortPortableToolRecordsV1(result.Payloads)
	sortPortableToolRecordsV1(result.NativePackageSets)
	return result, nil
}

func compilePortableToolRuntimeV1(runtime *RecordRuntimeV1) (*providers.PortableToolRuntimeProjectionV1, error) {
	if runtime == nil {
		return nil, nil
	}
	result := &providers.PortableToolRuntimeProjectionV1{
		InstallRoot: runtime.InstallRoot,
		Environment: make([]providers.PortableToolEnvironmentVariableV1, 0, len(runtime.Environment)),
	}
	for _, variable := range runtime.Environment {
		result.Environment = append(result.Environment, providers.PortableToolEnvironmentVariableV1{
			Name: variable.Name, Value: variable.Value,
		})
	}
	sort.Slice(result.Environment, func(left int, right int) bool {
		return result.Environment[left].Name < result.Environment[right].Name
	})
	for index := 1; index < len(result.Environment); index++ {
		previous, current := result.Environment[index-1], result.Environment[index]
		if previous.Name != current.Name {
			continue
		}
		if previous.Value != current.Value {
			return nil, fmt.Errorf("runtime environment has conflicting values for %q", current.Name)
		}
		result.Environment = append(result.Environment[:index], result.Environment[index+1:]...)
		index--
	}
	return result, nil
}

func compilePortableToolExportsV1(closure *SelectedClosureV1) ([]providers.PortableToolExportV1, error) {
	// The same export may be selected by multiple layers of a closure. A name
	// remains the semantic key, so identical repetitions collapse while a path
	// disagreement is rejected before provider validation.
	exports := make(map[string]string)
	add := func(values []ToolExportV1) error {
		for _, exported := range values {
			if path, exists := exports[exported.Name]; exists && path != exported.Path {
				return fmt.Errorf("exports have conflicting paths for %q", exported.Name)
			}
			exports[exported.Name] = exported.Path
		}
		return nil
	}
	if err := add(closure.Contract.Exports); err != nil {
		return nil, err
	}
	if err := add(closure.Target.Exports); err != nil {
		return nil, err
	}
	for _, binding := range closure.Target.Bindings {
		if err := add(binding.Exports); err != nil {
			return nil, err
		}
	}
	for _, selection := range closure.Target.Selections {
		if err := add(selection.Exports); err != nil {
			return nil, err
		}
	}
	result := make([]providers.PortableToolExportV1, 0, len(exports))
	for name, path := range exports {
		result = append(result, providers.PortableToolExportV1{Name: name, Path: path})
	}
	sort.Slice(result, func(left int, right int) bool { return result[left].Name < result[right].Name })
	return result, nil
}

func compilePortableToolValidationProfilesV1(profiles []ValidationProfileRecordV1) ([]providers.PortableToolValidationProfileV1, error) {
	result := make([]providers.PortableToolValidationProfileV1, 0, len(profiles))
	for index := range profiles {
		record, err := portableToolRecordEnvelopeV1(profiles[index])
		if err != nil {
			return nil, fmt.Errorf("validation profile %d: %w", index, err)
		}
		digest, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, profiles[index])
		if err != nil {
			return nil, fmt.Errorf("validation profile %d identity: %w", index, err)
		}
		result = append(result, providers.PortableToolValidationProfileV1{
			Reference: providers.PortableToolRecordReferenceV1{ID: profiles[index].ID, Digest: digest},
			Record:    record,
		})
	}
	sort.Slice(result, func(left int, right int) bool {
		return portableToolRecordReferenceLessV1(result[left].Reference, result[right].Reference)
	})
	return result, nil
}

func portableToolRecordEnvelopeV1(record any) (providers.CanonicalProviderData, error) {
	encoded, err := canonical.Marshal(record)
	if err != nil {
		return providers.CanonicalProviderData{}, err
	}
	value := canonical.Object{}
	if err := json.Unmarshal(encoded, &value); err != nil {
		return providers.CanonicalProviderData{}, fmt.Errorf("canonical record object: %w", err)
	}
	return providers.CanonicalProviderData{Schema: portableToolRecordSchemaV1(value), Value: value}, nil
}

func portableToolRecordSchemaV1(value canonical.Object) string {
	schema, _ := value["schema"].(string)
	return schema
}

func portableToolRecordReferenceV1(reference RecordReferenceV1) providers.PortableToolRecordReferenceV1 {
	return providers.PortableToolRecordReferenceV1{ID: reference.ID, Digest: reference.Digest}
}

func sortPortableToolRecordsV1(records []providers.PortableToolSelectedRecordV1) {
	sort.Slice(records, func(left int, right int) bool {
		return portableToolRecordReferenceLessV1(records[left].Reference, records[right].Reference)
	})
}

func portableToolRecordReferenceLessV1(left providers.PortableToolRecordReferenceV1, right providers.PortableToolRecordReferenceV1) bool {
	if left.ID != right.ID {
		return left.ID < right.ID
	}
	return left.Digest < right.Digest
}

func portableToolPlanEntryLessV1(left providers.PortableToolPlanEntryV1, right providers.PortableToolPlanEntryV1) bool {
	if left.Scope != right.Scope {
		return left.Scope < right.Scope
	}
	return left.Provenance.Tool < right.Provenance.Tool
}
