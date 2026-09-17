package dockerdeploy

import (
	"bytes"
	"context"
	"fmt"

	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

// PortableToolPythonFreshPlanV1 is the complete selected input needed to
// acquire application-scoped Python binding wheels during fresh resolution.
// Selection and provider projection happen before graph execution; this value
// keeps the selected plan, projection, and catalog closures together until the
// owning Python node has observed an eligible interpreter.
type PortableToolPythonFreshPlanV1 struct {
	Plan       providers.PortableToolPlanV1
	Projection pythonprovider.PortableToolPythonProjectionV1
	Closures   []toolcatalog.SelectedClosureV1
}

var portableToolPythonFreshLockRecordsV1 = toolcatalog.EmbeddedPortableToolLockRecordsV1
var producePortableToolPythonFreshWheelsV1 = pythonprovider.ProducePortableToolPythonVerifiedWheelsV1

func validatePortableToolPythonFreshPlanV1(input *PortableToolPythonFreshPlanV1) error {
	if input == nil {
		return nil
	}
	if err := providers.ValidatePortableToolPlanV1(input.Plan); err != nil {
		return fmt.Errorf("fresh portable Python plan: %w", err)
	}
	projectionBytes, err := pythonprovider.CanonicalPortableToolPythonProjectionBytesV1(input.Projection)
	if err != nil {
		return fmt.Errorf("fresh portable Python projection: %w", err)
	}
	_, expectedProjection, err := pythonprovider.ProjectPortableToolPythonBindingsV1(
		input.Plan,
		[]providers.ResolvedComponentRequestV1{},
	)
	if err != nil {
		return fmt.Errorf("fresh portable Python projection from selected plan: %w", err)
	}
	expectedProjectionBytes, err := pythonprovider.CanonicalPortableToolPythonProjectionBytesV1(expectedProjection)
	if err != nil {
		return fmt.Errorf("fresh portable Python projection from selected plan: %w", err)
	}
	if !bytes.Equal(projectionBytes, expectedProjectionBytes) {
		return fmt.Errorf("fresh portable Python projection does not exactly match selected plan")
	}
	if input.Closures == nil {
		return fmt.Errorf("fresh portable Python closures must use an array")
	}
	if len(input.Closures) != len(input.Plan.Tools) {
		return fmt.Errorf("fresh portable Python closures cover %d of %d selected plan entries", len(input.Closures), len(input.Plan.Tools))
	}
	for index, entry := range input.Plan.Tools {
		matches := 0
		for _, closure := range input.Closures {
			if closure.Scope == entry.Scope && closure.Provenance.Tool == entry.Provenance.Tool &&
				closure.Provenance.Version == entry.Provenance.Version && closure.Provenance.Revision == entry.Provenance.Revision &&
				closure.Provenance.ManifestDigest == entry.Provenance.ManifestDigest && closure.Identity == entry.SelectedClosureDigest {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("fresh portable Python plan entry %d joins %d exact selected closures", index, matches)
		}
	}
	return nil
}

func acquirePortableToolPythonFreshWheelsV1(
	ctx context.Context,
	store providerstore.Store,
	input *PortableToolPythonFreshPlanV1,
	component pythonprovider.PortableToolPythonComponentV1,
	interpreter providers.ExecutableEvidence,
) ([]pythonprovider.PortableToolVerifiedWheelInputV1, error) {
	if input == nil {
		return nil, fmt.Errorf("fresh portable Python bindings require their selected plan and closures")
	}
	if err := validatePortableToolPythonFreshPlanV1(input); err != nil {
		return nil, err
	}
	records, err := portableToolPythonFreshLockRecordsV1(input.Closures)
	if err != nil {
		return nil, fmt.Errorf("fresh portable Python lock records: %w", err)
	}
	requests := make([]pythonprovider.PortableToolPythonVerifiedWheelRequestV1, 0, len(component.Bindings))
	for index, binding := range component.Bindings {
		entry, err := portableToolPythonPlanEntryForBindingV1(input.Plan, binding)
		if err != nil {
			return nil, fmt.Errorf("fresh portable Python binding %d: %w", index, err)
		}
		contract, err := portableToolPythonSelectedRecordV1(
			entry.Responsibilities.BindingContracts, binding.Contract, "contract",
		)
		if err != nil {
			return nil, fmt.Errorf("fresh portable Python binding %d: %w", index, err)
		}
		artifact, err := portableToolPythonSelectedRecordV1(
			entry.Responsibilities.BindingArtifacts, binding.Artifact, "artifact",
		)
		if err != nil {
			return nil, fmt.Errorf("fresh portable Python binding %d: %w", index, err)
		}
		manifest, err := portableToolPythonManifestForBindingV1(records.Releases, entry)
		if err != nil {
			return nil, fmt.Errorf("fresh portable Python binding %d: %w", index, err)
		}
		source, err := portableToolPythonArtifactSourceForBindingV1(records.Artifacts, entry, binding.Artifact)
		if err != nil {
			return nil, fmt.Errorf("fresh portable Python binding %d: %w", index, err)
		}
		requests = append(requests, pythonprovider.PortableToolPythonVerifiedWheelRequestV1{
			SelectedPlanEntry: entry,
			Component:         component,
			Binding:           binding,
			ContractRecord:    contract,
			ArtifactRecord:    artifact,
			SourceRecord:      source.Source,
			ManifestRecord:    manifest,
			Interpreter:       interpreter,
			Acquisition: providerstore.AcquisitionRequest{
				Artifact: source.Descriptor,
				Source: providerstore.ArtifactSource{
					ID: source.Source.Reference.ID, SHA256: source.Descriptor.SHA256,
					Mirrors: append([]string{}, source.Mirrors...),
				},
				Policy: providerstore.DefaultAcquisitionPolicy(),
			},
			Mode: pythonprovider.PortableToolVerifiedWheelFreshV1,
		})
	}
	projection := pythonprovider.PortableToolPythonProjectionV1{
		Schema:     pythonprovider.PortableToolPythonProjectionSchemaV1,
		Components: []pythonprovider.PortableToolPythonComponentV1{component},
	}
	verified, err := producePortableToolPythonFreshWheelsV1(ctx, store, projection, requests)
	if err != nil {
		return nil, fmt.Errorf("produce fresh portable Python wheels: %w", err)
	}
	return verified, nil
}

func portableToolPythonPlanEntryForBindingV1(
	plan providers.PortableToolPlanV1,
	binding pythonprovider.PortableToolPythonBindingV1,
) (providers.PortableToolPlanEntryV1, error) {
	var selected *providers.PortableToolPlanEntryV1
	for index := range plan.Tools {
		entry := &plan.Tools[index]
		if entry.Scope != binding.Scope || entry.SelectedClosureDigest != binding.SelectedClosureDigest {
			continue
		}
		if !portableToolPythonRecordReferenceMemberV1(entry.Responsibilities.BindingContracts, binding.Contract) ||
			!portableToolPythonRecordReferenceMemberV1(entry.Responsibilities.BindingArtifacts, binding.Artifact) {
			continue
		}
		if selected != nil {
			return providers.PortableToolPlanEntryV1{}, fmt.Errorf("binding joins more than one selected plan entry")
		}
		selected = entry
	}
	if selected == nil {
		return providers.PortableToolPlanEntryV1{}, fmt.Errorf("binding joins no exact selected plan entry")
	}
	return *selected, nil
}

func portableToolPythonRecordReferenceMemberV1(
	records []providers.PortableToolSelectedRecordV1,
	reference providers.PortableToolRecordReferenceV1,
) bool {
	for _, record := range records {
		if record.Reference == reference {
			return true
		}
	}
	return false
}

func portableToolPythonSelectedRecordV1(
	records []providers.PortableToolSelectedRecordV1,
	reference providers.PortableToolRecordReferenceV1,
	kind string,
) (providers.PortableToolSelectedRecordV1, error) {
	var selected *providers.PortableToolSelectedRecordV1
	for index := range records {
		if records[index].Reference != reference {
			continue
		}
		if selected != nil {
			return providers.PortableToolSelectedRecordV1{}, fmt.Errorf("binding joins more than one selected %s record", kind)
		}
		selected = &records[index]
	}
	if selected == nil {
		return providers.PortableToolSelectedRecordV1{}, fmt.Errorf("binding joins no exact selected %s record", kind)
	}
	return *selected, nil
}

func portableToolPythonManifestForBindingV1(
	releases []providers.PortableToolReleaseManifestInputV1,
	entry providers.PortableToolPlanEntryV1,
) (providers.PortableToolSelectedRecordV1, error) {
	var selected *providers.PortableToolSelectedRecordV1
	for index := range releases {
		release := &releases[index]
		if release.Scope != entry.Scope || release.Tool != entry.Provenance.Tool {
			continue
		}
		if selected != nil {
			return providers.PortableToolSelectedRecordV1{}, fmt.Errorf("binding joins more than one selected release manifest")
		}
		selected = &release.Manifest
	}
	if selected == nil {
		return providers.PortableToolSelectedRecordV1{}, fmt.Errorf("binding joins no selected release manifest")
	}
	return *selected, nil
}

func portableToolPythonArtifactSourceForBindingV1(
	artifacts []toolcatalog.EmbeddedPortableToolArtifactSourceV1,
	entry providers.PortableToolPlanEntryV1,
	reference providers.PortableToolRecordReferenceV1,
) (toolcatalog.EmbeddedPortableToolArtifactSourceV1, error) {
	var selected *toolcatalog.EmbeddedPortableToolArtifactSourceV1
	for index := range artifacts {
		artifact := &artifacts[index]
		if artifact.Scope != entry.Scope || artifact.Tool != entry.Provenance.Tool || artifact.Artifact != reference {
			continue
		}
		if selected != nil {
			return toolcatalog.EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("binding joins more than one selected artifact source")
		}
		selected = artifact
	}
	if selected == nil {
		return toolcatalog.EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("binding joins no exact selected artifact source")
	}
	return *selected, nil
}
