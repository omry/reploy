package dockerdeploy

import (
	"context"
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

type ProviderMaterializationEvidenceRunner struct {
	Store providerstore.Store
}

type materializationExecutableCheck struct {
	ID        string
	Generated int
	Output    int
	Probe     FullImageExecutableProbe
}

var collectMaterializationExecutableEvidence = CollectFullImageExecutableEvidence

// Run validates all generated and public executables from one completed layer
// in a single image-validation container and returns only observed evidence.
func (runner ProviderMaterializationEvidenceRunner) Run(
	ctx context.Context,
	input MaterializationEvidenceInput,
) ([]providers.RealizedGeneratedExecutable, []providers.RealizedOutput, error) {
	if ctx == nil {
		return nil, nil, fmt.Errorf("provider materialization evidence requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	checks := make([]materializationExecutableCheck, 0, len(input.Transaction.GeneratedExecutables)+len(input.Bundle.Payload.Outputs))
	for index, declaration := range input.Transaction.GeneratedExecutables {
		id := fmt.Sprintf("generated_%06d", index)
		checks = append(checks, materializationExecutableCheck{
			ID: id, Generated: index, Output: -1,
			Probe: FullImageExecutableProbe{
				ID: id, InvocationPath: declaration.Path,
				Binding: ProbeExecutableBinding{
					Output: providers.QualifiedOutput{Component: "backend", Name: declaration.ID},
					Facts: providers.CanonicalProviderData{
						Schema: "generated-executable-v1",
						Value:  canonical.Object{"id": declaration.ID, "node": string(input.Transaction.NodeID)},
					},
				},
			},
		})
	}
	for index, output := range input.Bundle.Payload.Outputs {
		id := fmt.Sprintf("output_%06d", index)
		checks = append(checks, materializationExecutableCheck{
			ID: id, Generated: -1, Output: index,
			Probe: FullImageExecutableProbe{
				ID: id, InvocationPath: output.Candidate.InvocationPath,
				Binding: ProbeExecutableBinding{
					Output: providers.QualifiedOutput{Component: output.SupplierComponent, Name: output.Name},
					Facts:  output.Candidate.Provenance,
				},
			},
		})
	}
	sort.Slice(checks, func(left int, right int) bool { return checks[left].ID < checks[right].ID })
	probes := make([]FullImageExecutableProbe, len(checks))
	for index := range checks {
		probes[index] = checks[index].Probe
	}
	evidence, err := collectMaterializationExecutableEvidence(ctx, runner.Store, input.Candidate.Image.Descriptor, probes)
	if err != nil {
		return nil, nil, err
	}
	if len(evidence) != len(checks) {
		return nil, nil, fmt.Errorf("provider materialization evidence count does not match requested executable checks")
	}
	generated := make([]providers.RealizedGeneratedExecutable, len(input.Transaction.GeneratedExecutables))
	outputs := make([]providers.RealizedOutput, len(input.Bundle.Payload.Outputs))
	for index, check := range checks {
		observed := evidence[index]
		if check.Generated >= 0 {
			declaration := input.Transaction.GeneratedExecutables[check.Generated]
			generated[check.Generated] = providers.RealizedGeneratedExecutable{
				Declaration: declaration,
				Evidence: providers.GeneratedExecutableEvidence{
					Schema: providers.GeneratedExecutableEvidenceSchemaV1, InvocationPath: observed.InvocationPath,
					LinkChain: append([]providers.LinkEvidence{}, observed.LinkChain...),
					Terminal: providers.GeneratedFileEvidence{
						Path: observed.Terminal.Path, Kind: observed.Terminal.Kind, Mode: observed.Terminal.Mode,
						Size: observed.Terminal.Size, SHA256: observed.Terminal.SHA256, Owner: observed.Terminal.Owner,
					},
					Access: observed.Access,
					Facts:  observed.Facts,
				},
			}
			continue
		}
		resolved := input.Bundle.Payload.Outputs[check.Output]
		outputs[check.Output] = providers.RealizedOutput{
			SupplierComponent: resolved.SupplierComponent, SupplierNode: resolved.SupplierNode,
			Name: resolved.Name, Candidate: resolved.Candidate, Evidence: observed,
		}
	}
	return generated, outputs, nil
}
