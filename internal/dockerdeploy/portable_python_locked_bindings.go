package dockerdeploy

import (
	"bytes"
	"context"
	"fmt"

	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

// PortableToolPythonLockedPlanV1 carries the selected portable Python
// projection together with the persisted lock that authorizes replay. The
// lock is the only source of release and acquisition records on this path;
// the embedded catalog is deliberately not consulted.
type PortableToolPythonLockedPlanV1 struct {
	Plan       providers.PortableToolPlanV1
	Projection pythonprovider.PortableToolPythonProjectionV1
	Lock       providers.PortableToolLockV1
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
	plan := cloned.Plan.PortableToolPlan
	_, projection, err := pythonprovider.ProjectPortableToolPythonBindingsV1(
		plan, []providers.ResolvedComponentRequestV1{},
	)
	if err != nil {
		return nil, fmt.Errorf("locked portable Python projection: %w", err)
	}
	result := &PortableToolPythonLockedPlanV1{
		Plan: plan, Projection: projection, Lock: cloned,
	}
	if err := validatePortableToolPythonLockedPlanV1(result); err != nil {
		return nil, err
	}
	return result, nil
}

func validatePortableToolPythonLockedPlanV1(input *PortableToolPythonLockedPlanV1) error {
	if input == nil {
		return fmt.Errorf("locked portable Python plan is required")
	}
	if err := providers.ValidatePortableToolLockV1(input.Lock); err != nil {
		return fmt.Errorf("locked portable Python lock: %w", err)
	}
	if err := providers.ValidatePortableToolPlanV1(input.Plan); err != nil {
		return fmt.Errorf("locked portable Python plan: %w", err)
	}
	planBytes, err := providers.CanonicalPortableToolPlanBytesV1(input.Plan)
	if err != nil {
		return fmt.Errorf("locked portable Python plan: %w", err)
	}
	lockedPlanBytes, err := providers.CanonicalPortableToolPlanBytesV1(input.Lock.Plan.PortableToolPlan)
	if err != nil {
		return fmt.Errorf("locked portable Python lock plan: %w", err)
	}
	if !bytes.Equal(planBytes, lockedPlanBytes) {
		return fmt.Errorf("locked portable Python plan does not match its lock")
	}
	_, expectedProjection, err := pythonprovider.ProjectPortableToolPythonBindingsV1(
		input.Plan, []providers.ResolvedComponentRequestV1{},
	)
	if err != nil {
		return fmt.Errorf("locked portable Python projection from plan: %w", err)
	}
	projectionBytes, err := pythonprovider.CanonicalPortableToolPythonProjectionBytesV1(input.Projection)
	if err != nil {
		return fmt.Errorf("locked portable Python projection: %w", err)
	}
	expectedProjectionBytes, err := pythonprovider.CanonicalPortableToolPythonProjectionBytesV1(expectedProjection)
	if err != nil {
		return fmt.Errorf("locked portable Python projection from plan: %w", err)
	}
	if !bytes.Equal(projectionBytes, expectedProjectionBytes) {
		return fmt.Errorf("locked portable Python projection does not exactly match its plan")
	}
	for _, component := range input.Projection.Components {
		for index, binding := range component.Bindings {
			entry, err := portableToolPythonPlanEntryForBindingV1(input.Plan, binding)
			if err != nil {
				return fmt.Errorf("locked portable Python binding %d in component %q: %w", index, component.Component, err)
			}
			if _, err := portableToolPythonLockedAcquisitionForBindingV1(input.Lock, entry, binding); err != nil {
				return fmt.Errorf("locked portable Python binding %d in component %q: %w", index, component.Component, err)
			}
		}
	}
	return nil
}

// acquirePortableToolPythonLockedWheelsV1 builds replay requests from the
// persisted lock and sends them through the provider's locked-replay mode.
// No embedded catalog lookup or acquisition callback is reachable here.
func acquirePortableToolPythonLockedWheelsV1(
	ctx context.Context,
	store providerstore.Store,
	input *PortableToolPythonLockedPlanV1,
	component pythonprovider.PortableToolPythonComponentV1,
	interpreter providers.ExecutableEvidence,
) ([]pythonprovider.PortableToolVerifiedWheelInputV1, error) {
	if input == nil {
		return nil, fmt.Errorf("locked portable Python bindings require their selected lock")
	}
	if err := validatePortableToolPythonLockedPlanV1(input); err != nil {
		return nil, err
	}
	var selected *pythonprovider.PortableToolPythonComponentV1
	for index := range input.Projection.Components {
		candidate := &input.Projection.Components[index]
		if candidate.Component != component.Component {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("locked portable Python component %q is duplicated", component.Component)
		}
		selected = candidate
	}
	if selected == nil {
		return nil, fmt.Errorf("locked portable Python component %q is absent from its projection", component.Component)
	}
	componentBytes, err := pythonprovider.CanonicalPortableToolPythonProjectionBytesV1(
		pythonprovider.PortableToolPythonProjectionV1{
			Schema:     pythonprovider.PortableToolPythonProjectionSchemaV1,
			Components: []pythonprovider.PortableToolPythonComponentV1{component},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("locked portable Python component: %w", err)
	}
	selectedBytes, err := pythonprovider.CanonicalPortableToolPythonProjectionBytesV1(
		pythonprovider.PortableToolPythonProjectionV1{
			Schema:     pythonprovider.PortableToolPythonProjectionSchemaV1,
			Components: []pythonprovider.PortableToolPythonComponentV1{*selected},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("locked portable Python projected component: %w", err)
	}
	if !bytes.Equal(componentBytes, selectedBytes) {
		return nil, fmt.Errorf("locked portable Python component does not match its projection")
	}
	requests := make([]pythonprovider.PortableToolPythonVerifiedWheelRequestV1, 0, len(component.Bindings))
	for index, binding := range component.Bindings {
		entry, err := portableToolPythonPlanEntryForBindingV1(input.Plan, binding)
		if err != nil {
			return nil, fmt.Errorf("locked portable Python binding %d: %w", index, err)
		}
		contract, err := portableToolPythonSelectedRecordV1(
			entry.Responsibilities.BindingContracts, binding.Contract, "contract",
		)
		if err != nil {
			return nil, fmt.Errorf("locked portable Python binding %d: %w", index, err)
		}
		artifact, err := portableToolPythonSelectedRecordV1(
			entry.Responsibilities.BindingArtifacts, binding.Artifact, "artifact",
		)
		if err != nil {
			return nil, fmt.Errorf("locked portable Python binding %d: %w", index, err)
		}
		manifest, err := portableToolPythonLockedReleaseForEntryV1(input.Lock, entry)
		if err != nil {
			return nil, fmt.Errorf("locked portable Python binding %d: %w", index, err)
		}
		acquisition, err := portableToolPythonLockedAcquisitionForBindingV1(input.Lock, entry, binding)
		if err != nil {
			return nil, fmt.Errorf("locked portable Python binding %d: %w", index, err)
		}
		source, err := pythonprovider.PortableToolPythonArtifactSourceFromRecordV1(
			acquisition.Source, acquisition.Descriptor,
		)
		if err != nil {
			return nil, fmt.Errorf("locked portable Python binding %d source: %w", index, err)
		}
		requests = append(requests, pythonprovider.PortableToolPythonVerifiedWheelRequestV1{
			SelectedPlanEntry: entry, Component: component, Binding: binding,
			ContractRecord: contract, ArtifactRecord: artifact,
			ManifestRecord: manifest, Interpreter: interpreter,
			Acquisition: providerstore.AcquisitionRequest{
				Artifact: acquisition.Descriptor, Source: source,
				Policy: providerstore.DefaultAcquisitionPolicy(),
			},
			Mode:              pythonprovider.PortableToolVerifiedWheelLockedReplayV1,
			LockedAcquisition: &acquisition,
		})
	}
	verified, err := pythonprovider.ProducePortableToolPythonVerifiedWheelsV1(
		ctx, store,
		pythonprovider.PortableToolPythonProjectionV1{
			Schema:     pythonprovider.PortableToolPythonProjectionSchemaV1,
			Components: []pythonprovider.PortableToolPythonComponentV1{component},
		}, requests,
	)
	if err != nil {
		return nil, fmt.Errorf("produce locked portable Python wheels: %w", err)
	}
	return verified, nil
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
