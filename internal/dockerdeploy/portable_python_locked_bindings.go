package dockerdeploy

import (
	"bytes"
	"context"
	"fmt"

	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

// PortableToolPythonLockedPlanV1 owns a validated snapshot of the persisted
// lock and its derived Python selection. Neither the plan nor its projection
// is retained as an independently mutable authority.
type PortableToolPythonLockedPlanV1 struct {
	lock      providers.PortableToolLockV1
	selection *pythonprovider.PortableToolPythonSelectionV1
}

// portableToolPlanHasPythonBindingScopesV1 reports whether a portable lock
// contains binding artifacts that require the Python resolver to re-open and
// verify their exact store objects. Payload-only source-builder locks remain
// eligible for their existing reuse behavior.
func portableToolPlanHasPythonBindingScopesV1(lock *providers.PortableToolLockV1) bool {
	if lock == nil {
		return false
	}
	_, projection, err := pythonprovider.ProjectPortableToolPythonBindingsV1(
		lock.Plan.PortableToolPlan, []providers.ResolvedComponentRequestV1{},
	)
	if err != nil {
		// A malformed selected binding must force the replay path, where the
		// complete lock and projection validation returns the exact error. It
		// must never make an existing provider bundle eligible for reuse.
		for _, entry := range lock.Plan.PortableToolPlan.Tools {
			if len(entry.Responsibilities.BindingContracts) != 0 ||
				len(entry.Responsibilities.BindingArtifacts) != 0 {
				return true
			}
		}
		return false
	}
	for _, component := range projection.Components {
		if len(component.Bindings) != 0 {
			return true
		}
	}
	return false
}

// buildPortableToolPythonLockedPlanV1 projects the selected binding records
// from a validated build lock. A lock without Python binding artifacts does
// not need a replay plan.
func buildPortableToolPythonLockedPlanV1(
	lock *providers.PortableToolLockV1,
) (*PortableToolPythonLockedPlanV1, error) {
	if lock == nil || !portableToolPlanHasPythonBindingScopesV1(lock) {
		return nil, nil
	}
	if err := providers.ValidatePortableToolLockV1(*lock); err != nil {
		return nil, fmt.Errorf("locked portable Python plan: %w", err)
	}
	cloned := providers.ClonePortableToolLockV1(*lock)
	selection, err := pythonprovider.NewPortableToolPythonSelectionV1(cloned.Plan.PortableToolPlan)
	if err != nil {
		return nil, fmt.Errorf("locked portable Python selection: %w", err)
	}
	return &PortableToolPythonLockedPlanV1{lock: cloned, selection: selection}, nil
}

// acquirePortableToolPythonLockedHandoffsV1 reopens only descriptor-bound
// store bytes from the validated lock; no embedded catalog or network path is
// reachable.
func acquirePortableToolPythonLockedHandoffsV1(
	ctx context.Context,
	store providerstore.Store,
	input *PortableToolPythonLockedPlanV1,
	component pythonprovider.PortableToolPythonComponentV1,
	interpreter providers.ExecutableEvidence,
) ([]pythonprovider.PortableToolPythonVerifiedWheelHandoffV1, error) {
	selection, handoffs, err := preparePortableToolPythonLockedWheelsV1(input, component, interpreter)
	if err != nil {
		return nil, err
	}
	return pythonprovider.ProducePortableToolPythonSelectedWheelsV1(ctx, store, selection, component.Component, handoffs)
}

func preparePortableToolPythonLockedWheelsV1(
	input *PortableToolPythonLockedPlanV1,
	component pythonprovider.PortableToolPythonComponentV1,
	interpreter providers.ExecutableEvidence,
) (*pythonprovider.PortableToolPythonSelectionV1, []*pythonprovider.PortableToolPythonWheelHandoffV1, error) {
	if input == nil {
		return nil, nil, fmt.Errorf("locked portable Python bindings require their selected lock")
	}
	if input.selection == nil {
		return nil, nil, fmt.Errorf("locked portable Python plan is required")
	}
	selection := input.selection
	projection, err := selection.Projection()
	if err != nil {
		return nil, nil, err
	}
	var selected *pythonprovider.PortableToolPythonComponentV1
	for index := range projection.Components {
		candidate := &projection.Components[index]
		if candidate.Component != component.Component {
			continue
		}
		if selected != nil {
			return nil, nil, fmt.Errorf("locked portable Python component %q is duplicated", component.Component)
		}
		selected = candidate
	}
	if selected == nil {
		return nil, nil, fmt.Errorf("locked portable Python component %q is absent from its projection", component.Component)
	}
	componentBytes, err := pythonprovider.CanonicalPortableToolPythonProjectionBytesV1(
		pythonprovider.PortableToolPythonProjectionV1{
			Schema:     pythonprovider.PortableToolPythonProjectionSchemaV1,
			Components: []pythonprovider.PortableToolPythonComponentV1{component},
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("locked portable Python component: %w", err)
	}
	selectedBytes, err := pythonprovider.CanonicalPortableToolPythonProjectionBytesV1(
		pythonprovider.PortableToolPythonProjectionV1{
			Schema:     pythonprovider.PortableToolPythonProjectionSchemaV1,
			Components: []pythonprovider.PortableToolPythonComponentV1{*selected},
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("locked portable Python projected component: %w", err)
	}
	if !bytes.Equal(componentBytes, selectedBytes) {
		return nil, nil, fmt.Errorf("locked portable Python component does not match its projection")
	}
	handoffs := make([]*pythonprovider.PortableToolPythonWheelHandoffV1, 0, len(component.Bindings))
	for index, binding := range component.Bindings {
		selectedBinding, err := selection.Binding(component.Component, binding.Distribution)
		if err != nil {
			return nil, nil, fmt.Errorf("locked portable Python binding %d: %w", index, err)
		}
		entry, err := selectedBinding.PlanEntry()
		if err != nil {
			return nil, nil, fmt.Errorf("locked portable Python binding %d: %w", index, err)
		}
		manifest, err := portableToolPythonLockedReleaseForEntryV1(input.lock, entry)
		if err != nil {
			return nil, nil, fmt.Errorf("locked portable Python binding %d: %w", index, err)
		}
		acquisition, err := portableToolPythonLockedAcquisitionForBindingV1(input.lock, entry, binding)
		if err != nil {
			return nil, nil, fmt.Errorf("locked portable Python binding %d: %w", index, err)
		}
		handoff, err := selectedBinding.LockedWheel(manifest, acquisition, interpreter)
		if err != nil {
			return nil, nil, fmt.Errorf("locked portable Python binding %d: %w", index, err)
		}
		handoffs = append(handoffs, handoff)
	}
	return selection, handoffs, nil
}

func portableToolPythonLockedReleaseForEntryV1(
	lock providers.PortableToolLockV1,
	entry providers.PortableToolPlanEntryV1,
) (providers.PortableToolSelectedRecordV1, error) {
	var selected *providers.PortableToolReleaseManifestLockV1
	for index := range lock.Releases {
		release := &lock.Releases[index]
		if release.Scope != entry.Scope || release.Tool != entry.Provenance.Tool {
			continue
		}
		if selected != nil {
			return providers.PortableToolSelectedRecordV1{}, fmt.Errorf("lock contains duplicate release manifest")
		}
		selected = release
	}
	if selected == nil {
		return providers.PortableToolSelectedRecordV1{}, fmt.Errorf("lock has no release manifest for %s/%s", entry.Scope, entry.Provenance.Tool)
	}
	return selected.Manifest, nil
}

func portableToolPythonLockedAcquisitionForBindingV1(
	lock providers.PortableToolLockV1,
	entry providers.PortableToolPlanEntryV1,
	binding pythonprovider.PortableToolPythonBindingV1,
) (providers.PortableToolArtifactAcquisitionLockV1, error) {
	var selected *providers.PortableToolArtifactAcquisitionLockV1
	for index := range lock.Acquisitions {
		acquisition := &lock.Acquisitions[index]
		if acquisition.Scope != binding.Scope || acquisition.Tool != entry.Provenance.Tool || acquisition.Artifact != binding.Artifact {
			continue
		}
		if selected != nil {
			return providers.PortableToolArtifactAcquisitionLockV1{}, fmt.Errorf("lock contains duplicate acquisition for %s", binding.Artifact.ID)
		}
		selected = acquisition
	}
	if selected == nil {
		return providers.PortableToolArtifactAcquisitionLockV1{}, fmt.Errorf("lock has no acquisition for %s", binding.Artifact.ID)
	}
	return *selected, nil
}
