package providers

import (
	"fmt"
	"sort"
)

const PortableToolValidationScheduleSchemaV1 = "portable-tool-validation-schedule-v1"

// PortableToolValidationScheduleV1 is the provider-neutral scheduling
// projection consumed by production validation. It pairs every selected
// validation-profile reference with the contract runtime projection of the
// closure that selected it, so a scheduler can invoke the fixed executor
// without re-deriving selection.
//
// The schedule is derived from an already-validated plan and is never an
// input to selected-closure identity: validation references deliberately stay
// outside SelectedClosureDigest, and nothing here is fed back into it.
type PortableToolValidationScheduleV1 struct {
	Schema  string                              `json:"schema"`
	Entries []PortableToolScheduledValidationV1 `json:"entries"`
}

// PortableToolScheduledValidationV1 is one scheduled profile in one
// resolution scope. Runtime is nil when the selected closure declares no
// contract runtime projection.
type PortableToolScheduledValidationV1 struct {
	Scope   string                           `json:"scope"`
	Tool    string                           `json:"tool"`
	Profile PortableToolValidationProfileV1  `json:"profile"`
	Runtime *PortableToolRuntimeProjectionV1 `json:"runtime"`
}

// PortableToolValidationScheduleFromLockV1 projects the compiled portable-tool
// plan carried by a persisted build lock into its scheduling order, so locked
// replay schedules the exact profiles that were locked rather than re-reading
// moving catalog state. The order is deterministic: by scope, then tool, then
// profile reference, exactly as the plan already orders them.
//
// The lock is the only public projection source. Deriving a schedule from an
// unlocked DAG would let a caller validate against a plan the build never
// locked.
func PortableToolValidationScheduleFromLockV1(lock PortableToolLockV1) (PortableToolValidationScheduleV1, error) {
	if err := ValidatePortableToolLockV1(lock); err != nil {
		return PortableToolValidationScheduleV1{}, fmt.Errorf("portable tool validation schedule: %w", err)
	}
	return portableToolValidationScheduleFromPlanV1(lock.Plan.PortableToolPlan)
}

// PortableToolValidationScheduleForScopeV1 selects the exact resolution scope
// owned by a validation caller. Scope meaning remains consumer-owned: this
// projection does not infer image placement from runtime metadata or tool
// kind. An unmatched scope returns an explicit empty schedule because a scope
// may legitimately select no validation profiles. Callers that require proof
// that a scope exists must establish that from their own resolution input.
func PortableToolValidationScheduleForScopeV1(
	schedule PortableToolValidationScheduleV1,
	scope string,
) (PortableToolValidationScheduleV1, error) {
	if err := ValidatePortableToolValidationScheduleV1(schedule); err != nil {
		return PortableToolValidationScheduleV1{}, err
	}
	if scope == "" {
		return PortableToolValidationScheduleV1{}, fmt.Errorf("portable tool validation schedule scope must not be empty")
	}
	selected := PortableToolValidationScheduleV1{
		Schema:  PortableToolValidationScheduleSchemaV1,
		Entries: []PortableToolScheduledValidationV1{},
	}
	for _, entry := range schedule.Entries {
		if entry.Scope == scope {
			selected.Entries = append(selected.Entries, clonePortableToolScheduledValidationV1(entry))
		}
	}
	return selected, nil
}

func portableToolValidationScheduleFromPlanV1(plan PortableToolPlanV1) (PortableToolValidationScheduleV1, error) {
	schedule := PortableToolValidationScheduleV1{
		Schema:  PortableToolValidationScheduleSchemaV1,
		Entries: []PortableToolScheduledValidationV1{},
	}
	for _, entry := range plan.Tools {
		for _, profile := range entry.ValidationProfiles {
			schedule.Entries = append(schedule.Entries, PortableToolScheduledValidationV1{
				Scope:   entry.Scope,
				Tool:    entry.Provenance.Tool,
				Profile: clonePortableToolValidationProfileV1(profile),
				Runtime: clonePortableToolRuntimeProjectionV1(entry.Runtime),
			})
		}
	}
	sort.SliceStable(schedule.Entries, func(left int, right int) bool {
		return portableToolScheduledValidationLessV1(schedule.Entries[left], schedule.Entries[right])
	})
	if err := ValidatePortableToolValidationScheduleV1(schedule); err != nil {
		return PortableToolValidationScheduleV1{}, err
	}
	return schedule, nil
}

// ValidatePortableToolValidationScheduleV1 strictly validates schedule shape
// and ordering. An empty schedule is valid: a build that selects no portable
// tool schedules no portable-tool validation.
func ValidatePortableToolValidationScheduleV1(schedule PortableToolValidationScheduleV1) error {
	if schedule.Schema != PortableToolValidationScheduleSchemaV1 {
		return fmt.Errorf("portable tool validation schedule schema must be %q", PortableToolValidationScheduleSchemaV1)
	}
	if schedule.Entries == nil {
		return fmt.Errorf("portable tool validation schedule entries must use an explicit array")
	}
	for index, entry := range schedule.Entries {
		if entry.Scope == "" {
			return fmt.Errorf("portable tool validation schedule entry %d requires a scope", index)
		}
		if entry.Tool == "" {
			return fmt.Errorf("portable tool validation schedule entry %d requires a tool", index)
		}
		if err := validatePortableToolRecordReferenceV1(entry.Profile.Reference); err != nil {
			return fmt.Errorf("portable tool validation schedule entry %d profile: %w", index, err)
		}
		if entry.Profile.Record.Schema != portableToolValidationProfileSchemaV1 {
			return fmt.Errorf(
				"portable tool validation schedule entry %d profile record schema must be %q",
				index, portableToolValidationProfileSchemaV1,
			)
		}
		if entry.Runtime != nil {
			if err := ValidatePortableToolRuntimeProjectionV1(*entry.Runtime); err != nil {
				return fmt.Errorf("portable tool validation schedule entry %d runtime: %w", index, err)
			}
		}
		if index > 0 && !portableToolScheduledValidationLessV1(schedule.Entries[index-1], entry) {
			return fmt.Errorf("portable tool validation schedule entries must be unique and sorted at %d", index)
		}
	}
	return nil
}

func portableToolScheduledValidationLessV1(left PortableToolScheduledValidationV1, right PortableToolScheduledValidationV1) bool {
	if left.Scope != right.Scope {
		return left.Scope < right.Scope
	}
	if left.Tool != right.Tool {
		return left.Tool < right.Tool
	}
	if left.Profile.Reference.ID != right.Profile.Reference.ID {
		return left.Profile.Reference.ID < right.Profile.Reference.ID
	}
	return left.Profile.Reference.Digest < right.Profile.Reference.Digest
}

func clonePortableToolValidationProfileV1(profile PortableToolValidationProfileV1) PortableToolValidationProfileV1 {
	profile.Record.Value = clonePortableToolCanonicalObjectV1(profile.Record.Value)
	return profile
}

func clonePortableToolScheduledValidationV1(entry PortableToolScheduledValidationV1) PortableToolScheduledValidationV1 {
	entry.Profile = clonePortableToolValidationProfileV1(entry.Profile)
	entry.Runtime = clonePortableToolRuntimeProjectionV1(entry.Runtime)
	return entry
}

func clonePortableToolRuntimeProjectionV1(runtime *PortableToolRuntimeProjectionV1) *PortableToolRuntimeProjectionV1 {
	if runtime == nil {
		return nil
	}
	clone := *runtime
	clone.Environment = append([]PortableToolEnvironmentVariableV1{}, runtime.Environment...)
	return &clone
}
