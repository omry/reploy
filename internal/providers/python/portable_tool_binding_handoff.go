package python

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/omry/reploy/internal/canonical"
	providerapi "github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

// PortableToolPythonSelectionV1 owns a validated snapshot of the selected
// plan. Its projection is derived from that snapshot, not supplied as another
// independently mutable authority.
type PortableToolPythonSelectionV1 struct {
	plan       providerapi.PortableToolPlanV1
	projection PortableToolPythonProjectionV1
}

func NewPortableToolPythonSelectionV1(plan providerapi.PortableToolPlanV1) (*PortableToolPythonSelectionV1, error) {
	if err := providerapi.ValidatePortableToolPlanV1(plan); err != nil {
		return nil, fmt.Errorf("selected portable Python plan: %w", err)
	}
	copy, err := cloneAndNormalizePortableToolPlanV1(plan)
	if err != nil {
		return nil, err
	}
	if err := providerapi.ValidatePortableToolPlanV1(copy); err != nil {
		return nil, fmt.Errorf("selected portable Python plan: %w", err)
	}
	_, projection, err := ProjectPortableToolPythonBindingsV1(copy, []providerapi.ResolvedComponentRequestV1{})
	if err != nil {
		return nil, fmt.Errorf("selected portable Python projection: %w", err)
	}
	return &PortableToolPythonSelectionV1{plan: copy, projection: projection}, nil
}

// Plan returns a detached view of the validated selected plan.
func (selection *PortableToolPythonSelectionV1) Plan() (providerapi.PortableToolPlanV1, error) {
	if selection == nil {
		return providerapi.PortableToolPlanV1{}, fmt.Errorf("portable Python selection is required")
	}
	encoded, err := canonical.Marshal(selection.plan)
	if err != nil {
		return providerapi.PortableToolPlanV1{}, err
	}
	var result providerapi.PortableToolPlanV1
	if err := json.Unmarshal(encoded, &result); err != nil {
		return providerapi.PortableToolPlanV1{}, err
	}
	return result, nil
}

// Projection returns a detached provider view. Mutating it cannot change the
// selected handoff or the bindings later resolved from it.
func (selection *PortableToolPythonSelectionV1) Projection() (PortableToolPythonProjectionV1, error) {
	if selection == nil {
		return PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python selection is required")
	}
	encoded, err := canonical.Marshal(selection.projection)
	if err != nil {
		return PortableToolPythonProjectionV1{}, err
	}
	var result PortableToolPythonProjectionV1
	if err := json.Unmarshal(encoded, &result); err != nil {
		return PortableToolPythonProjectionV1{}, err
	}
	return result, nil
}

// PortableToolPythonSelectedBindingV1 is the construction-controlled join of
// one projected binding with its exact selected plan entry and records.
type PortableToolPythonSelectedBindingV1 struct {
	selection *PortableToolPythonSelectionV1
	entry     providerapi.PortableToolPlanEntryV1
	component PortableToolPythonComponentV1
	binding   PortableToolPythonBindingV1
	contract  providerapi.PortableToolSelectedRecordV1
	artifact  providerapi.PortableToolSelectedRecordV1
}

// PlanEntry returns a detached selected entry for the existing release and
// acquisition record lookups. The join itself is not repeated by callers.
func (binding *PortableToolPythonSelectedBindingV1) PlanEntry() (providerapi.PortableToolPlanEntryV1, error) {
	if binding == nil || binding.selection == nil {
		return providerapi.PortableToolPlanEntryV1{}, fmt.Errorf("selected portable Python binding is required")
	}
	encoded, err := canonical.Marshal(binding.entry)
	if err != nil {
		return providerapi.PortableToolPlanEntryV1{}, err
	}
	var result providerapi.PortableToolPlanEntryV1
	if err := json.Unmarshal(encoded, &result); err != nil {
		return providerapi.PortableToolPlanEntryV1{}, err
	}
	return result, nil
}

func (selection *PortableToolPythonSelectionV1) Binding(componentName, distribution string) (*PortableToolPythonSelectedBindingV1, error) {
	if selection == nil {
		return nil, fmt.Errorf("portable Python selection is required")
	}
	var component *PortableToolPythonComponentV1
	for index := range selection.projection.Components {
		candidate := &selection.projection.Components[index]
		if candidate.Component == componentName {
			component = candidate
			break
		}
	}
	if component == nil {
		return nil, fmt.Errorf("portable Python component %q is not selected", componentName)
	}
	var binding *PortableToolPythonBindingV1
	for index := range component.Bindings {
		candidate := &component.Bindings[index]
		if candidate.Distribution == distribution {
			binding = candidate
			break
		}
	}
	if binding == nil {
		return nil, fmt.Errorf("portable Python distribution %q is not selected in component %q", distribution, componentName)
	}
	var entry *providerapi.PortableToolPlanEntryV1
	for index := range selection.plan.Tools {
		candidate := &selection.plan.Tools[index]
		if candidate.Scope != binding.Scope || candidate.SelectedClosureDigest != binding.SelectedClosureDigest ||
			!portableToolPythonReferenceMemberV1(candidate.Responsibilities.BindingContracts, binding.Contract) ||
			!portableToolPythonReferenceMemberV1(candidate.Responsibilities.BindingArtifacts, binding.Artifact) {
			continue
		}
		if entry != nil {
			return nil, fmt.Errorf("portable Python binding joins more than one selected plan entry")
		}
		entry = candidate
	}
	if entry == nil {
		return nil, fmt.Errorf("portable Python binding joins no selected plan entry")
	}
	contract, err := portableToolPythonRecordForReferenceV1(entry.Responsibilities.BindingContracts, binding.Contract)
	if err != nil {
		return nil, fmt.Errorf("portable Python binding contract: %w", err)
	}
	artifact, err := portableToolPythonRecordForReferenceV1(entry.Responsibilities.BindingArtifacts, binding.Artifact)
	if err != nil {
		return nil, fmt.Errorf("portable Python binding artifact: %w", err)
	}
	return &PortableToolPythonSelectedBindingV1{
		selection: selection,
		entry:     *entry, component: *component, binding: *binding,
		contract: contract, artifact: artifact,
	}, nil
}

// PortableToolPythonWheelHandoffV1 freezes the selected binding and its
// authorized acquisition declaration before any store or network operation.
type PortableToolPythonWheelHandoffV1 struct {
	selection *PortableToolPythonSelectionV1
	request   portableToolPythonVerifiedWheelRequestV1
}

// PortableToolPythonVerifiedWheelHandoffV1 owns the wheel observation produced
// by acquisition, descriptor verification, inspection, and selected-binding
// verification. Callers only receive detached materialization and lock views.
type PortableToolPythonVerifiedWheelHandoffV1 struct {
	input *PortableToolVerifiedWheelInputV1
}

func (verified PortableToolPythonVerifiedWheelHandoffV1) MaterializationInput() (PortableToolVerifiedWheelInputV1, error) {
	if verified.input == nil {
		return PortableToolVerifiedWheelInputV1{}, fmt.Errorf("verified portable Python wheel handoff is required")
	}
	encoded, err := json.Marshal(verified.input)
	if err != nil {
		return PortableToolVerifiedWheelInputV1{}, err
	}
	var result PortableToolVerifiedWheelInputV1
	if err := json.Unmarshal(encoded, &result); err != nil {
		return PortableToolVerifiedWheelInputV1{}, err
	}
	return result, nil
}

func (verified PortableToolPythonVerifiedWheelHandoffV1) ArtifactAcquisitionInput() (providerapi.PortableToolArtifactAcquisitionInputV1, error) {
	if verified.input == nil {
		return providerapi.PortableToolArtifactAcquisitionInputV1{}, fmt.Errorf("verified portable Python wheel handoff is required")
	}
	return verified.input.PortableToolArtifactAcquisitionInputV1()
}

// MatchEmbeddedSource checks the catalog's detached acquisition view against
// the authenticated source record without exposing a mutable request copy.
func (handoff *PortableToolPythonWheelHandoffV1) MatchEmbeddedSource(
	descriptor providerstore.ArtifactDescriptor, mirrors []string,
) error {
	if handoff == nil || handoff.selection == nil {
		return fmt.Errorf("portable Python wheel handoff is required")
	}
	if handoff.request.Mode != PortableToolVerifiedWheelFreshV1 ||
		handoff.request.Acquisition.Artifact != descriptor ||
		!slices.Equal(handoff.request.Acquisition.Source.Mirrors, mirrors) {
		return fmt.Errorf("embedded source differs from authenticated source record")
	}
	return nil
}

// FreshWheel derives the exact descriptor and transport source from the
// selected binding and its authenticated source record.
func (binding *PortableToolPythonSelectedBindingV1) FreshWheel(
	manifest, source providerapi.PortableToolSelectedRecordV1,
	interpreter providerapi.ExecutableEvidence,
) (*PortableToolPythonWheelHandoffV1, error) {
	if binding == nil || binding.selection == nil {
		return nil, fmt.Errorf("selected portable Python binding is required")
	}
	descriptor := portableToolVerifiedWheelDescriptorV1(binding.binding)
	transport, err := portableToolPythonSourceFromSelectedRecordV1(source, descriptor)
	if err != nil {
		return nil, fmt.Errorf("selected portable Python source: %w", err)
	}
	return binding.wheelHandoff(manifest, source, interpreter, providerstore.AcquisitionRequest{
		Artifact: descriptor, Source: transport, Policy: providerstore.DefaultAcquisitionPolicy(),
	}, PortableToolVerifiedWheelFreshV1, nil)
}

// LockedWheel derives replay from the validated lock entry. The producer's
// locked mode only reopens verified store bytes and never acquires a network
// artifact.
func (binding *PortableToolPythonSelectedBindingV1) LockedWheel(
	manifest providerapi.PortableToolSelectedRecordV1,
	acquisition providerapi.PortableToolArtifactAcquisitionLockV1,
	interpreter providerapi.ExecutableEvidence,
) (*PortableToolPythonWheelHandoffV1, error) {
	if binding == nil || binding.selection == nil {
		return nil, fmt.Errorf("selected portable Python binding is required")
	}
	transport, err := portableToolPythonSourceFromSelectedRecordV1(acquisition.Source, acquisition.Descriptor)
	if err != nil {
		return nil, fmt.Errorf("locked portable Python source: %w", err)
	}
	return binding.wheelHandoff(manifest, acquisition.Source, interpreter, providerstore.AcquisitionRequest{
		Artifact: acquisition.Descriptor, Source: transport, Policy: providerstore.DefaultAcquisitionPolicy(),
	}, PortableToolVerifiedWheelLockedReplayV1, &acquisition)
}

func (binding *PortableToolPythonSelectedBindingV1) wheelHandoff(
	manifest, source providerapi.PortableToolSelectedRecordV1,
	interpreter providerapi.ExecutableEvidence,
	acquisition providerstore.AcquisitionRequest,
	mode PortableToolVerifiedWheelModeV1,
	locked *providerapi.PortableToolArtifactAcquisitionLockV1,
) (*PortableToolPythonWheelHandoffV1, error) {
	request := portableToolPythonVerifiedWheelRequestV1{
		SelectedPlanEntry: binding.entry, Component: binding.component, Binding: binding.binding,
		ContractRecord: binding.contract, ArtifactRecord: binding.artifact,
		SourceRecord: source, ManifestRecord: manifest, Interpreter: interpreter,
		Acquisition: acquisition, Mode: mode, LockedAcquisition: locked,
	}
	if err := preflightportableToolPythonVerifiedWheelRequestV1(request); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("freeze selected portable Python wheel: %w", err)
	}
	var frozen portableToolPythonVerifiedWheelRequestV1
	if err := json.Unmarshal(encoded, &frozen); err != nil {
		return nil, fmt.Errorf("freeze selected portable Python wheel: %w", err)
	}
	if err := preflightportableToolPythonVerifiedWheelRequestV1(frozen); err != nil {
		return nil, fmt.Errorf("frozen selected portable Python wheel: %w", err)
	}
	return &PortableToolPythonWheelHandoffV1{selection: binding.selection, request: frozen}, nil
}

// ProducePortableToolPythonSelectedWheelsV1 verifies complete coverage of one
// selected component before it lets the store perform any acquisition.
func ProducePortableToolPythonSelectedWheelsV1(
	ctx context.Context,
	store providerstore.Store,
	selection *PortableToolPythonSelectionV1,
	componentName string,
	handoffs []*PortableToolPythonWheelHandoffV1,
) ([]PortableToolPythonVerifiedWheelHandoffV1, error) {
	if selection == nil {
		return nil, fmt.Errorf("portable Python selection is required")
	}
	if handoffs == nil {
		return nil, fmt.Errorf("portable Python wheel handoffs must use an explicit array")
	}
	projection, err := selection.Projection()
	if err != nil {
		return nil, err
	}
	var component *PortableToolPythonComponentV1
	for index := range projection.Components {
		if projection.Components[index].Component == componentName {
			component = &projection.Components[index]
			break
		}
	}
	if component == nil {
		return nil, fmt.Errorf("portable Python component %q is not selected", componentName)
	}
	requests := make([]portableToolPythonVerifiedWheelRequestV1, len(handoffs))
	for index, handoff := range handoffs {
		if handoff == nil || handoff.selection != selection {
			return nil, fmt.Errorf("portable Python wheel handoff %d is not from this selection", index)
		}
		requests[index] = handoff.request
	}
	verified, err := producePortableToolPythonVerifiedWheelsV1(ctx, store, PortableToolPythonProjectionV1{
		Schema:     PortableToolPythonProjectionSchemaV1,
		Components: []PortableToolPythonComponentV1{*component},
	}, requests)
	if err != nil {
		return nil, err
	}
	result := make([]PortableToolPythonVerifiedWheelHandoffV1, len(verified))
	for index := range verified {
		input := verified[index]
		result[index] = PortableToolPythonVerifiedWheelHandoffV1{input: &input}
	}
	return result, nil
}

func portableToolPythonReferenceMemberV1(records []providerapi.PortableToolSelectedRecordV1, reference providerapi.PortableToolRecordReferenceV1) bool {
	for _, record := range records {
		if record.Reference == reference {
			return true
		}
	}
	return false
}

func portableToolPythonRecordForReferenceV1(records []providerapi.PortableToolSelectedRecordV1, reference providerapi.PortableToolRecordReferenceV1) (providerapi.PortableToolSelectedRecordV1, error) {
	for _, record := range records {
		if record.Reference == reference {
			return record, nil
		}
	}
	return providerapi.PortableToolSelectedRecordV1{}, fmt.Errorf("record %q is absent from selected plan", reference.ID)
}
