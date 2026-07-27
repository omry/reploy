package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type ProviderFullImageValidationRunner struct {
	Store providerstore.Store
}

var prepareFullValidationProbeWorkspace = PrepareProbeWorkspace
var prepareFullValidationAPTWorkspace = PrepareAPTResolverWorkspace
var openFullValidationSession = OpenImageValidationSession
var openFullAPTValidationSession = OpenAPTImageValidationSession

// Run performs one complete image validation in one held networkless
// container. Provider scratch and the embedded probe workspace are
// deployment-local and removed on every return path.
func (runner ProviderFullImageValidationRunner) Run(
	ctx context.Context,
	input FullImageValidationInput,
) (profiles []providers.ValidationEvidence, outputs []providers.ExecutableEvidence, resultErr error) {
	if ctx == nil {
		return nil, nil, fmt.Errorf("provider full image validation requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	plan, err := planFullImageValidationProbe(input)
	if err != nil {
		return nil, nil, err
	}
	workspace, cleanupProbe, err := prepareFullValidationProbeWorkspace(ctx, runner.Store, input.Image.Descriptor.Platform)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if cleanupErr := cleanupProbe(); cleanupErr != nil {
			profiles, outputs = nil, nil
			resultErr = errors.Join(resultErr, cleanupErr)
		}
	}()

	var session *ImageValidationSession
	if plan.APT == nil {
		session, err = openFullValidationSession(ctx, input.Image.Descriptor, workspace)
	} else {
		aptWorkspace, cleanupAPT, prepareErr := prepareFullValidationAPTWorkspace(runner.Store)
		if prepareErr != nil {
			return nil, nil, prepareErr
		}
		defer cleanupAPT()
		session, err = openFullAPTValidationSession(ctx, input.Image.Descriptor, workspace, aptWorkspace)
	}
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if closeErr := session.Close(context.WithoutCancel(ctx)); closeErr != nil {
			profiles, outputs = nil, nil
			resultErr = errors.Join(resultErr, closeErr)
		}
	}()

	response, err := session.Probe(ctx, plan.Request)
	if err != nil {
		return nil, nil, err
	}
	observations := make(map[string]probe.ExecutableObservationV1, len(response.Observations))
	for _, observation := range response.Observations {
		observations[observation.ID] = observation
	}
	if err := validateFullImageBackendBaseline(plan, observations); err != nil {
		return nil, nil, err
	}
	if err := session.ValidateBuildScratchAbsent(ctx); err != nil {
		return nil, nil, err
	}
	profiles = make([]providers.ValidationEvidence, 0, len(input.Profiles))
	aptNativeArchitecture := ""
	if plan.APT != nil {
		fresh, err := validateAPTProfileObservation(
			ctx, session, input.Profiles[plan.APT.ProfileIndex], plan.APT.ToolInspection, observations,
		)
		if err != nil {
			return nil, nil, err
		}
		aptNativeArchitecture = fresh.Profile.NativeArchitecture
		evidence, err := fullValidationProfileEvidence(input, plan.APT.ProfileIndex)
		if err != nil {
			return nil, nil, err
		}
		profiles = append(profiles, evidence)
	}
	for _, planned := range plan.PythonProfiles {
		launcher, launcherFound := observations[planned.LauncherInspection]
		interpreter, interpreterFound := observations[planned.InterpreterInspection]
		if !launcherFound || !interpreterFound {
			return nil, nil, fmt.Errorf("combined image probe omitted a Python profile observation")
		}
		if _, err := validatePythonProfileObservation(
			ctx, session, input.Profiles[planned.ProfileIndex], launcher, interpreter,
		); err != nil {
			return nil, nil, err
		}
		evidence, err := fullValidationProfileEvidence(input, planned.ProfileIndex)
		if err != nil {
			return nil, nil, err
		}
		profiles = append(profiles, evidence)
	}

	outputs = make([]providers.ExecutableEvidence, 0, len(plan.Outputs))
	aptFresh := []providers.ExecutableEvidence{}
	aptLocked := []providers.ExecutableEvidence{}
	for _, planned := range plan.Outputs {
		observation, found := observations[planned.InspectionID]
		if !found {
			return nil, nil, fmt.Errorf("combined image probe omitted output %s.%s", planned.Binding.Output.Component, planned.Binding.Output.Name)
		}
		fresh, err := ExecutableEvidenceFromProbe(observation, planned.Binding)
		if err != nil {
			return nil, nil, err
		}
		locked := input.Outputs[planned.OutputIndex]
		if locked.SupplierNode == providers.NodeID("apt") {
			aptFresh = append(aptFresh, fresh)
			aptLocked = append(aptLocked, locked.Evidence)
		} else {
			outputs = append(outputs, fresh)
		}
	}
	if len(aptFresh) != 0 {
		if aptNativeArchitecture == "" {
			return nil, nil, fmt.Errorf("APT outputs require an APT validation profile")
		}
		aptOutputs, err := reproduceAPTOutputEvidence(ctx, session, aptNativeArchitecture, aptFresh, aptLocked)
		if err != nil {
			return nil, nil, err
		}
		outputs = append(outputs, aptOutputs...)
	}
	sort.Slice(profiles, func(left int, right int) bool { return profiles[left].ProfileDigest < profiles[right].ProfileDigest })
	sort.Slice(outputs, func(left int, right int) bool {
		if outputs[left].Output.Component != outputs[right].Output.Component {
			return outputs[left].Output.Component < outputs[right].Output.Component
		}
		return outputs[left].Output.Name < outputs[right].Output.Name
	})
	return profiles, outputs, nil
}

func fullValidationProfileEvidence(input FullImageValidationInput, profileIndex int) (providers.ValidationEvidence, error) {
	digest, err := providers.RequirementProfileDigest(input.Profiles[profileIndex], registry.ValidateRequirementProfileV1)
	if err != nil {
		return providers.ValidationEvidence{}, err
	}
	return providers.NewValidationEvidence(input.Image.Image.RootFSSubject, digest)
}

func validateFullImageBackendBaseline(plan fullImageValidationProbePlan, observations map[string]probe.ExecutableObservationV1) error {
	checks := []struct {
		inspection  string
		requirement providers.ExecutableRequirement
		output      providers.QualifiedOutput
		facts       providers.CanonicalProviderData
	}{
		{
			inspection: plan.CarrierInspection,
			requirement: providers.ExecutableRequirement{
				ID: pythonCarrierRequirementID, Command: "sh", Supplier: "backend", ValidationPolicy: providers.ValidationPolicyCompatible,
			},
			output: providers.QualifiedOutput{Component: "backend", Name: "sh"},
			facts:  providers.CanonicalProviderData{Schema: "posix-carrier-v1", Value: canonical.Object{"interface": "posix-sh"}},
		},
		{
			inspection: plan.LauncherInspection,
			requirement: providers.ExecutableRequirement{
				ID: pythonLauncherRequirementID, Command: "env", Supplier: "backend", ValidationPolicy: providers.ValidationPolicyCompatible,
			},
			output: providers.QualifiedOutput{Component: "backend", Name: "env"},
			facts:  providers.CanonicalProviderData{Schema: "clean-environment-launcher-v1", Value: canonical.Object{"interface": "env-i"}},
		},
	}
	for _, check := range checks {
		observation, found := observations[check.inspection]
		if !found {
			return fmt.Errorf("combined image probe omitted backend executable %q", check.requirement.Command)
		}
		observation.ID = check.requirement.ID
		if _, err := ExecutableEvidenceFromProbe(observation, ProbeExecutableBinding{
			Requirement: &check.requirement, Output: check.output, Facts: check.facts,
		}); err != nil {
			return fmt.Errorf("validate full image backend executable %q: %w", check.requirement.Command, err)
		}
	}
	return nil
}
