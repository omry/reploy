package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

// SelectPythonInterpreter validates candidates in their established
// lower-layer-first order inside an already-open resolver session. The runtime
// itself supplies the accepted version; catalog or package metadata cannot.
func SelectPythonInterpreter(
	ctx context.Context,
	session *PythonResolverSession,
	launcher providers.ValidatedExecutableInput,
	requirement providers.ExecutableRequirement,
	candidates []providers.RealizedOutput,
) (providers.ExecutableEvidence, error) {
	if ctx == nil {
		return providers.ExecutableEvidence{}, fmt.Errorf("select Python interpreter requires a context")
	}
	if err := ctx.Err(); err != nil {
		return providers.ExecutableEvidence{}, err
	}
	if session == nil || session.closed {
		return providers.ExecutableEvidence{}, fmt.Errorf("Python resolver session is not open")
	}
	if err := providers.ValidateValidatedExecutableInput(launcher); err != nil {
		return providers.ExecutableEvidence{}, fmt.Errorf("select Python interpreter environment launcher: %w", err)
	}
	if launcher.Role != providers.ExecutableRoleEnvironmentLauncher {
		return providers.ExecutableEvidence{}, fmt.Errorf("select Python interpreter launcher role must be %q", providers.ExecutableRoleEnvironmentLauncher)
	}
	if candidates == nil {
		return providers.ExecutableEvidence{}, fmt.Errorf("Python interpreter candidates must use an array")
	}
	lastInvalidCandidate := ""
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return providers.ExecutableEvidence{}, err
		}
		if candidate.Name != requirement.Command || requirement.Supplier != "" && candidate.SupplierComponent != requirement.Supplier {
			return providers.ExecutableEvidence{}, fmt.Errorf("Python interpreter candidate %s.%s does not match requirement %q", candidate.SupplierComponent, candidate.Name, requirement.ID)
		}
		request := probe.RequestV1{
			Schema: probe.RequestSchemaV1,
			Inspections: []probe.ExecutableInspectionV1{{
				ID: requirement.ID, InvocationPath: candidate.Candidate.InvocationPath,
			}},
		}
		if _, err := session.Probe(ctx, request); err != nil {
			candidateErr := fmt.Errorf(
				"Python interpreter candidate %s.%s at %s is unavailable; configure an explicit Python interpreter executable path: %w",
				candidate.SupplierComponent, candidate.Name, candidate.Candidate.InvocationPath, err,
			)
			if requirement.Supplier != "" {
				return providers.ExecutableEvidence{}, candidateErr
			}
			lastInvalidCandidate = candidateErr.Error()
			continue
		}
		output := providers.QualifiedOutput{Component: candidate.SupplierComponent, Name: candidate.Name}
		selected, version, err := session.InspectAndBindInterpreter(ctx, launcher, requirement, output)
		if err != nil {
			candidateErr := fmt.Errorf(
				"Python interpreter candidate %s.%s at %s is not a usable Python interpreter; configure an explicit Python interpreter executable path: %w",
				candidate.SupplierComponent, candidate.Name, candidate.Candidate.InvocationPath, err,
			)
			if requirement.Supplier != "" {
				return providers.ExecutableEvidence{}, candidateErr
			}
			lastInvalidCandidate = candidateErr.Error()
			continue
		}
		matches, err := pythonprovider.InterpreterVersionSatisfies(requirement.VersionConstraint, version)
		if err != nil {
			return providers.ExecutableEvidence{}, err
		}
		if !matches {
			continue
		}
		return selected.Evidence, nil
	}
	err := fmt.Errorf(
		"no Python interpreter candidate satisfies requirement %q with version constraint %q; configure an explicit Python interpreter executable path",
		requirement.Command, requirement.VersionConstraint,
	)
	if lastInvalidCandidate != "" {
		return providers.ExecutableEvidence{}, fmt.Errorf("%w; last invalid candidate: %s", err, lastInvalidCandidate)
	}
	return providers.ExecutableEvidence{}, err
}
