package dockerdeploy

import (
	"bytes"
	"context"
	"fmt"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

// validatePythonProfileObservation checks one locked Python requirement using
// observations from the combined full-image probe and one fixed interpreter
// inspection in the same held validation container.
func validatePythonProfileObservation(
	ctx context.Context,
	session *ImageValidationSession,
	profile providers.RequirementProfile,
	launcherObservation probe.ExecutableObservationV1,
	interpreterObservation probe.ExecutableObservationV1,
) (providers.ExecutableEvidence, error) {
	if ctx == nil {
		return providers.ExecutableEvidence{}, fmt.Errorf("validate Python image profile requires a context")
	}
	if err := ctx.Err(); err != nil {
		return providers.ExecutableEvidence{}, err
	}
	if session == nil || session.closed {
		return providers.ExecutableEvidence{}, fmt.Errorf("image validation session is not open")
	}
	if err := providers.ValidateRequirementProfile(profile, pythonprovider.ValidateRequirementProfileV1); err != nil {
		return providers.ExecutableEvidence{}, fmt.Errorf("validate Python image profile: %w", err)
	}
	requirement := profile.Declaration.Executables[0]
	locked := profile.SelectedExecutables[0]
	if launcherObservation.InvocationPath != pythonLauncherPath {
		return providers.ExecutableEvidence{}, fmt.Errorf("Python image profile launcher observation must use %s", pythonLauncherPath)
	}
	if interpreterObservation.InvocationPath != locked.InvocationPath {
		return providers.ExecutableEvidence{}, fmt.Errorf("Python image profile interpreter observation path %q does not match locked path %q", interpreterObservation.InvocationPath, locked.InvocationPath)
	}

	launcherObservation.ID = pythonLauncherRequirementID
	launcherRequirement := providers.ExecutableRequirement{
		ID: pythonLauncherRequirementID, Command: "env", Supplier: "backend",
		ValidationPolicy: providers.ValidationPolicyCompatible,
	}
	if _, err := ExecutableEvidenceFromProbe(launcherObservation, ProbeExecutableBinding{
		Requirement: &launcherRequirement,
		Output:      providers.QualifiedOutput{Component: "backend", Name: "env"},
		Facts: providers.CanonicalProviderData{
			Schema: "clean-environment-launcher-v1", Value: canonical.Object{"interface": "env-i"},
		},
	}); err != nil {
		return providers.ExecutableEvidence{}, fmt.Errorf("validate Python image profile launcher: %w", err)
	}
	interpreterObservation.ID = requirement.ID
	if _, err := ExecutableEvidenceFromProbe(interpreterObservation, ProbeExecutableBinding{
		Requirement: &requirement, Output: locked.Output, Facts: locked.Facts,
	}); err != nil {
		return providers.ExecutableEvidence{}, fmt.Errorf("validate Python image profile interpreter before execution: %w", err)
	}

	lockedFacts, err := pythonprovider.DecodeInterpreterFactsV2(locked.Facts)
	if err != nil {
		return providers.ExecutableEvidence{}, fmt.Errorf("validate Python image profile interpreter facts: %w", err)
	}
	facts, err := session.runPythonInterpreterInspection(ctx, locked.InvocationPath, lockedFacts.TestedTags)
	if err != nil {
		return providers.ExecutableEvidence{}, fmt.Errorf(
			"Python interpreter at %s is not usable; configure an explicit Python interpreter executable path: %w",
			locked.InvocationPath, err,
		)
	}
	matches, err := pythonprovider.InterpreterVersionSatisfies(requirement.VersionConstraint, facts.Version)
	if err != nil {
		return providers.ExecutableEvidence{}, err
	}
	if !matches {
		return providers.ExecutableEvidence{}, fmt.Errorf(
			"Python interpreter at %s has version %s, which does not satisfy %q; configure an explicit Python interpreter executable path",
			locked.InvocationPath, facts.Version, requirement.VersionConstraint,
		)
	}
	fresh, err := ExecutableEvidenceFromProbe(interpreterObservation, ProbeExecutableBinding{
		Requirement: &requirement, Output: locked.Output,
		Facts: pythonprovider.CanonicalInterpreterFactsV2(facts),
	})
	if err != nil {
		return providers.ExecutableEvidence{}, fmt.Errorf("validate Python image profile interpreter: %w", err)
	}
	return fresh, nil
}

func (session *ImageValidationSession) runPythonInterpreterInspection(
	ctx context.Context,
	interpreterPath string,
	testedTags []string,
) (pythonprovider.InterpreterInspectionFactsV2, error) {
	if session == nil || session.closed {
		return pythonprovider.InterpreterInspectionFactsV2{}, fmt.Errorf("image validation session is not open")
	}
	if ctx == nil {
		return pythonprovider.InterpreterInspectionFactsV2{}, fmt.Errorf("image validation Python inspection context is required")
	}
	if err := ctx.Err(); err != nil {
		return pythonprovider.InterpreterInspectionFactsV2{}, fmt.Errorf("run image validation Python inspection: %w", err)
	}
	targetArchitecture, err := pythonInspectionArchitectureV2(session.descriptor.Platform)
	if err != nil {
		return pythonprovider.InterpreterInspectionFactsV2{}, err
	}
	inspection, err := pythonprovider.InterpreterInspectionArgv(interpreterPath, testedTags, targetArchitecture)
	if err != nil {
		return pythonprovider.InterpreterInspectionFactsV2{}, err
	}
	args := []string{
		"exec", "--user", "0:0", "--workdir", "/", session.containerName,
		pythonLauncherPath, "-i",
		"HOME=/tmp", "LANG=C", "LC_ALL=C",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TMPDIR=/tmp",
	}
	args = append(args, inspection...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := session.runDockerCommand(CommandSpec{Name: "docker", Args: args}, RunOptions{
		Context: ctx, Stdout: &stdout, Stderr: &stderr,
	}); err != nil {
		return pythonprovider.InterpreterInspectionFactsV2{}, imageValidationCommandError("Python interpreter inspection", session.descriptor.Platform.Canonical, stderr.String(), err)
	}
	return pythonprovider.ParseInterpreterInspectionOutput(stdout.Bytes(), testedTags)
}
