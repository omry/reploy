package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

// PortableToolPythonFreshPlanV1 is the complete selected input needed to
// acquire application-scoped Python binding wheels during fresh resolution.
// Selection and provider projection happen before graph execution; this value
// keeps the selected plan and catalog closures together until the
// owning Python node has observed an eligible interpreter.
type PortableToolPythonFreshPlanV1 struct {
	Plan     providers.PortableToolPlanV1
	Closures []toolcatalog.SelectedClosureV1
	sealed   *portableToolPythonFreshSelectionV1
}

type portableToolPythonFreshSelectionV1 struct {
	selection *pythonprovider.PortableToolPythonSelectionV1
	closures  []toolcatalog.SelectedClosureV1
}

var portableToolPythonFreshLockRecordsV1 = toolcatalog.EmbeddedPortableToolLockRecordsV1

func validatePortableToolPythonFreshPlanV1(input *PortableToolPythonFreshPlanV1) error {
	if input == nil {
		return nil
	}
	if input.sealed != nil {
		return nil
	}
	selection, err := pythonprovider.NewPortableToolPythonSelectionV1(input.Plan)
	if err != nil {
		return fmt.Errorf("fresh portable Python selection: %w", err)
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
	encoded, err := json.Marshal(input.Closures)
	if err != nil {
		return fmt.Errorf("freeze fresh portable Python closures: %w", err)
	}
	var closures []toolcatalog.SelectedClosureV1
	if err := json.Unmarshal(encoded, &closures); err != nil {
		return fmt.Errorf("freeze fresh portable Python closures: %w", err)
	}
	input.sealed = &portableToolPythonFreshSelectionV1{selection: selection, closures: closures}
	return nil
}

func acquirePortableToolPythonFreshHandoffsV1(
	ctx context.Context,
	store providerstore.Store,
	input *PortableToolPythonFreshPlanV1,
	component pythonprovider.PortableToolPythonComponentV1,
	interpreter providers.ExecutableEvidence,
) ([]pythonprovider.PortableToolPythonVerifiedWheelHandoffV1, error) {
	selection, handoffs, err := preparePortableToolPythonFreshWheelsV1(input, component, interpreter)
	if err != nil {
		return nil, err
	}
	return pythonprovider.ProducePortableToolPythonSelectedWheelsV1(ctx, store, selection, component.Component, handoffs)
}

func preparePortableToolPythonFreshWheelsV1(
	input *PortableToolPythonFreshPlanV1,
	component pythonprovider.PortableToolPythonComponentV1,
	interpreter providers.ExecutableEvidence,
) (*pythonprovider.PortableToolPythonSelectionV1, []*pythonprovider.PortableToolPythonWheelHandoffV1, error) {
	if input == nil {
		return nil, nil, fmt.Errorf("fresh portable Python bindings require their selected plan and closures")
	}
	if err := validatePortableToolPythonFreshPlanV1(input); err != nil {
		return nil, nil, err
	}
	selection := input.sealed.selection
	selectedProjection, err := selection.Projection()
	if err != nil {
		return nil, nil, err
	}
	var selectedComponent *pythonprovider.PortableToolPythonComponentV1
	for index := range selectedProjection.Components {
		if selectedProjection.Components[index].Component == component.Component {
			selectedComponent = &selectedProjection.Components[index]
			break
		}
	}
	if selectedComponent == nil {
		return nil, nil, fmt.Errorf("fresh portable Python component %q is absent from its selection", component.Component)
	}
	componentBytes, err := pythonprovider.CanonicalPortableToolPythonProjectionBytesV1(
		pythonprovider.PortableToolPythonProjectionV1{
			Schema:     pythonprovider.PortableToolPythonProjectionSchemaV1,
			Components: []pythonprovider.PortableToolPythonComponentV1{component},
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("fresh portable Python component: %w", err)
	}
	selectedBytes, err := pythonprovider.CanonicalPortableToolPythonProjectionBytesV1(
		pythonprovider.PortableToolPythonProjectionV1{
			Schema:     pythonprovider.PortableToolPythonProjectionSchemaV1,
			Components: []pythonprovider.PortableToolPythonComponentV1{*selectedComponent},
		},
	)
	if err != nil {
		return nil, nil, err
	}
	if !bytes.Equal(componentBytes, selectedBytes) {
		return nil, nil, fmt.Errorf("fresh portable Python component differs from its selection")
	}
	records, err := portableToolPythonFreshLockRecordsV1(input.sealed.closures)
	if err != nil {
		return nil, nil, fmt.Errorf("fresh portable Python lock records: %w", err)
	}
	handoffs := make([]*pythonprovider.PortableToolPythonWheelHandoffV1, 0, len(component.Bindings))
	for index, binding := range component.Bindings {
		selected, err := selection.Binding(component.Component, binding.Distribution)
		if err != nil {
			return nil, nil, fmt.Errorf("fresh portable Python binding %d: %w", index, err)
		}
		entry, err := selected.PlanEntry()
		if err != nil {
			return nil, nil, fmt.Errorf("fresh portable Python binding %d: %w", index, err)
		}
		manifest, err := portableToolPythonManifestForBindingV1(records.Releases, entry)
		if err != nil {
			return nil, nil, fmt.Errorf("fresh portable Python binding %d: %w", index, err)
		}
		source, err := portableToolPythonArtifactSourceForBindingV1(records.Artifacts, entry, binding.Artifact)
		if err != nil {
			return nil, nil, fmt.Errorf("fresh portable Python binding %d: %w", index, err)
		}
		handoff, err := selected.FreshWheel(manifest, source.Source, interpreter)
		if err != nil {
			return nil, nil, fmt.Errorf("fresh portable Python binding %d: %w", index, err)
		}
		if err := handoff.MatchEmbeddedSource(source.Descriptor, source.Mirrors); err != nil {
			return nil, nil, fmt.Errorf("fresh portable Python binding %d: %w", index, err)
		}
		handoffs = append(handoffs, handoff)
	}
	return selection, handoffs, nil
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
