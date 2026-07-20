package dockerdeploy

import (
	"context"
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
)

type aptBaseProfileProbe func(context.Context, probe.RequestV1) (probe.ResponseV1, error)
type aptBaseProfileCommand func(context.Context, string, ...string) ([]byte, error)

// observeAPTBaseProfile is the sole Docker-backend implementation of APT base
// profile observation. Resolver and final-image validation sessions supply
// only their closed probe and fixed-command transports.
func observeAPTBaseProfile(
	ctx context.Context,
	platform blueprint.Platform,
	probeBase aptBaseProfileProbe,
	run aptBaseProfileCommand,
) (APTBaseValidation, error) {
	if ctx == nil {
		return APTBaseValidation{}, fmt.Errorf("APT base profile observation requires a context")
	}
	if err := ctx.Err(); err != nil {
		return APTBaseValidation{}, err
	}
	if err := platform.Validate(); err != nil {
		return APTBaseValidation{}, err
	}
	if probeBase == nil || run == nil {
		return APTBaseValidation{}, fmt.Errorf("APT base profile observation requires probe and command transports")
	}

	requiredTools := aptprovider.RequiredBaseToolsV1()
	inspections := make([]probe.ExecutableInspectionV1, 0, len(requiredTools))
	for _, tool := range requiredTools {
		inspections = append(inspections, probe.ExecutableInspectionV1{ID: tool.Name, InvocationPath: tool.Path})
	}
	request := probe.RequestV1{Schema: probe.RequestSchemaV1, Inspections: inspections}
	response, err := probeBase(ctx, request)
	if err != nil {
		return APTBaseValidation{}, fmt.Errorf("validate APT base executables: %w", err)
	}
	observations := make(map[string]probe.ExecutableObservationV1, len(response.Observations))
	for _, observation := range response.Observations {
		observations[observation.ID] = observation
	}

	osReleaseOutput, err := run(ctx, "/bin/sh", "-c", aptOSReleaseProbeScriptV1)
	if err != nil {
		return APTBaseValidation{}, err
	}
	osRelease, err := parseAPTOSReleaseOutputV1(osReleaseOutput)
	if err != nil {
		return APTBaseValidation{}, err
	}
	versionCommands := map[string][]string{
		"apt_get":    {"/usr/bin/apt-get", "--version"},
		"dpkg":       {"/usr/bin/dpkg", "--version"},
		"dpkg_deb":   {"/usr/bin/dpkg-deb", "--version"},
		"dpkg_query": {"/usr/bin/dpkg-query", "--version"},
		"sha256sum":  {"/usr/bin/sha256sum", "--version"},
	}
	versions := map[string]string{}
	for _, tool := range requiredTools {
		command, exists := versionCommands[tool.Name]
		if !exists {
			continue
		}
		output, err := run(ctx, command[0], command[1:]...)
		if err != nil {
			return APTBaseValidation{}, err
		}
		version, err := firstAPTOutputLine(tool.Name+" version", output)
		if err != nil {
			return APTBaseValidation{}, err
		}
		versions[tool.Name] = version
	}
	nativeOutput, err := run(ctx, "/usr/bin/dpkg", "--print-architecture")
	if err != nil {
		return APTBaseValidation{}, err
	}
	native, err := singleAPTOutputLine("native architecture", nativeOutput)
	if err != nil {
		return APTBaseValidation{}, err
	}
	foreignOutput, err := run(ctx, "/usr/bin/dpkg", "--print-foreign-architectures")
	if err != nil {
		return APTBaseValidation{}, err
	}
	foreign, err := aptOutputLines("foreign architectures", foreignOutput)
	if err != nil {
		return APTBaseValidation{}, err
	}
	for index := range requiredTools {
		requiredTools[index].Version = versions[requiredTools[index].Name]
	}
	profile, err := aptprovider.NewBaseProfileEvidenceV1(platform, osRelease, requiredTools, native, foreign)
	if err != nil {
		return APTBaseValidation{}, err
	}
	executables, err := bindAPTBaseExecutables(observations, profile.Tools)
	if err != nil {
		return APTBaseValidation{}, err
	}
	return APTBaseValidation{Profile: profile, Executables: executables}, nil
}

func bindAPTBaseExecutables(
	observations map[string]probe.ExecutableObservationV1,
	tools []aptprovider.RequiredToolEvidenceV1,
) ([]providers.ValidatedExecutableInput, error) {
	result := make([]providers.ValidatedExecutableInput, 0, len(tools))
	for _, tool := range tools {
		observation, found := observations[tool.Name]
		if !found {
			return nil, fmt.Errorf("APT base tool %q was not probed in this container", tool.Name)
		}
		role := providers.ExecutableRoleProviderPrerequisite
		component := "apt"
		if tool.Name == "sh" {
			role, component = providers.ExecutableRoleCarrier, "backend"
		} else if tool.Name == "env" {
			role, component = providers.ExecutableRoleEnvironmentLauncher, "backend"
		}
		requirement := providers.ExecutableRequirement{
			ID: tool.Name, Command: tool.Name, Supplier: component,
			ValidationPolicy: providers.ValidationPolicyCompatible,
		}
		evidence, err := ExecutableEvidenceFromProbe(observation, ProbeExecutableBinding{
			Requirement: &requirement,
			Output:      providers.QualifiedOutput{Component: component, Name: tool.Name},
			Facts: providers.CanonicalProviderData{Schema: "apt-required-tool-v1", Value: canonical.Object{
				"interface": tool.Interface, "version": tool.Version,
			}},
		})
		if err != nil {
			return nil, err
		}
		input := providers.ValidatedExecutableInput{ID: tool.Name, Role: role, Policy: providers.ValidationPolicyCompatible, Evidence: evidence}
		if err := providers.ValidateValidatedExecutableInput(input); err != nil {
			return nil, err
		}
		result = append(result, input)
	}
	sort.Slice(result, func(left int, right int) bool { return result[left].ID < result[right].ID })
	return result, nil
}
