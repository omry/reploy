package dockerdeploy

import (
	"fmt"

	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
)

// ProbeExecutableBinding supplies the policy-owned fields that the low-level
// filesystem probe cannot determine. A nil Requirement creates evidence for a
// final exposed output; a non-nil requirement creates consumer-selection
// evidence.
type ProbeExecutableBinding struct {
	Requirement *providers.ExecutableRequirement
	Output      providers.QualifiedOutput
	Facts       providers.CanonicalProviderData
}

// ExecutableEvidenceFromProbe converts one validated filesystem observation
// into common executable evidence. Links start as ordinary and unowned; a
// provider such as APT may add ownership or alternatives evidence before
// applying its provider-specific acceptance rules.
func ExecutableEvidenceFromProbe(observation probe.ExecutableObservationV1, binding ProbeExecutableBinding) (providers.ExecutableEvidence, error) {
	request := probe.RequestV1{
		Schema: probe.RequestSchemaV1,
		Inspections: []probe.ExecutableInspectionV1{{
			ID: observation.ID, InvocationPath: observation.InvocationPath,
		}},
	}
	response := probe.ResponseV1{
		Schema: probe.ResponseSchemaV1, Observations: []probe.ExecutableObservationV1{observation},
	}
	if err := probe.ValidateResponseV1(request, response); err != nil {
		return providers.ExecutableEvidence{}, fmt.Errorf("convert probe observation: %w", err)
	}

	requirementID := ""
	if binding.Requirement != nil {
		requirementID = binding.Requirement.ID
		if observation.ID != requirementID {
			return providers.ExecutableEvidence{}, fmt.Errorf("probe observation %q does not match requirement %q", observation.ID, requirementID)
		}
	}
	links := make([]providers.LinkEvidence, 0, len(observation.Links))
	for _, link := range observation.Links {
		links = append(links, providers.LinkEvidence{
			Path: link.Path, Target: link.Target, ResolvedPath: link.ResolvedPath, Kind: "ordinary",
		})
	}
	access := make([]providers.AccessPathEvidence, 0, len(observation.Access))
	for _, item := range observation.Access {
		required := "other-search"
		if item.Path == observation.Terminal.Path {
			required = "other-read-execute"
		}
		access = append(access, providers.AccessPathEvidence{
			Path: item.Path, Kind: item.Kind, Mode: item.Mode, Required: required,
		})
	}
	evidence := providers.ExecutableEvidence{
		Schema: providers.ExecutableEvidenceSchemaV1, RequirementID: requirementID,
		Output: binding.Output, InvocationPath: observation.InvocationPath, LinkChain: links,
		Terminal: providers.FileEvidence{
			Schema: providers.FileEvidenceSchemaV1, RequirementID: requirementID,
			Path: observation.Terminal.Path, Kind: observation.Terminal.Kind,
			Mode: observation.Terminal.Mode, Size: observation.Terminal.Size, SHA256: observation.Terminal.SHA256,
		},
		Access: providers.PortableAccessEvidence{
			Schema: providers.PortableAccessSchemaV1, Profile: providers.PortableOutputAccessV1, Paths: access,
		},
		Facts: binding.Facts,
	}
	if binding.Requirement == nil {
		if err := providers.ValidateFinalExecutableEvidence(evidence); err != nil {
			return providers.ExecutableEvidence{}, fmt.Errorf("final executable evidence from probe: %w", err)
		}
	} else if err := providers.ValidateExecutableEvidence(evidence, *binding.Requirement); err != nil {
		return providers.ExecutableEvidence{}, fmt.Errorf("requirement executable evidence from probe: %w", err)
	}
	return evidence, nil
}
