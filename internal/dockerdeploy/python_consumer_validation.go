package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
)

const (
	pythonCarrierRequirementID  = "carrier"
	pythonLauncherRequirementID = "cleanenv"
	pythonCarrierPath           = "/bin/sh"
	pythonLauncherPath          = "/usr/bin/env"
)

// ValidatePythonConsumer observes the fixed backend carrier and clean
// environment launcher in the held resolver session. Repeated calls reuse the
// same session observations without another Docker exec.
func ValidatePythonConsumer(
	ctx context.Context,
	session *PythonResolverSession,
	finalImageConfig providers.ImageConfigPolicy,
) (providers.GraphConsumerValidation, error) {
	if ctx == nil {
		return providers.GraphConsumerValidation{}, fmt.Errorf("validate Python consumer requires a context")
	}
	if err := ctx.Err(); err != nil {
		return providers.GraphConsumerValidation{}, err
	}
	if session == nil || session.closed {
		return providers.GraphConsumerValidation{}, fmt.Errorf("Python resolver session is not open")
	}
	if err := providers.ValidateImageConfigPolicy(finalImageConfig); err != nil {
		return providers.GraphConsumerValidation{}, fmt.Errorf("Python consumer final image config: %w", err)
	}
	inspections := []probe.ExecutableInspectionV1{}
	if _, found := session.observations[pythonCarrierRequirementID]; !found {
		inspections = append(inspections, probe.ExecutableInspectionV1{ID: pythonCarrierRequirementID, InvocationPath: pythonCarrierPath})
	}
	if _, found := session.observations[pythonLauncherRequirementID]; !found {
		inspections = append(inspections, probe.ExecutableInspectionV1{ID: pythonLauncherRequirementID, InvocationPath: pythonLauncherPath})
	}
	if len(inspections) != 0 {
		if _, err := session.Probe(ctx, probe.RequestV1{Schema: probe.RequestSchemaV1, Inspections: inspections}); err != nil {
			return providers.GraphConsumerValidation{}, fmt.Errorf("validate Python consumer executables: %w", err)
		}
	}
	carrier, err := session.ValidatedExecutableInput(
		providers.ExecutableRoleCarrier,
		providers.ExecutableRequirement{
			ID: pythonCarrierRequirementID, Command: "sh", Supplier: "backend", ValidationPolicy: providers.ValidationPolicyCompatible,
		},
		providers.QualifiedOutput{Component: "backend", Name: "sh"},
		providers.CanonicalProviderData{Schema: "posix-carrier-v1", Value: canonical.Object{"interface": "posix-sh"}},
	)
	if err != nil {
		return providers.GraphConsumerValidation{}, err
	}
	launcher, err := session.ValidatedExecutableInput(
		providers.ExecutableRoleEnvironmentLauncher,
		providers.ExecutableRequirement{
			ID: pythonLauncherRequirementID, Command: "env", Supplier: "backend", ValidationPolicy: providers.ValidationPolicyCompatible,
		},
		providers.QualifiedOutput{Component: "backend", Name: "env"},
		providers.CanonicalProviderData{Schema: "clean-environment-launcher-v1", Value: canonical.Object{"interface": "env-i"}},
	)
	if err != nil {
		return providers.GraphConsumerValidation{}, err
	}
	return providers.GraphConsumerValidation{
		Carrier: carrier, EnvironmentLauncher: launcher, FinalImageConfig: finalImageConfig,
	}, nil
}
