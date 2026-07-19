package dockerdeploy

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
)

func TestExecutableEvidenceFromProbeBindsRequirementAndPortableAccess(t *testing.T) {
	observation := testExecutableObservation()
	requirement := providers.ExecutableRequirement{
		ID: "interpreter", Command: "python3", Supplier: "runtime",
		ValidationPolicy: providers.ValidationPolicyCompatible,
	}
	evidence, err := ExecutableEvidenceFromProbe(observation, ProbeExecutableBinding{
		Requirement: &requirement,
		Output:      providers.QualifiedOutput{Component: "runtime", Name: "python3"},
		Facts:       providers.CanonicalProviderData{Schema: "python-interpreter-facts-v1", Value: canonical.Object{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.RequirementID != requirement.ID || evidence.InvocationPath != "/usr/bin/python3" || len(evidence.LinkChain) != 1 {
		t.Fatalf("executable evidence = %#v", evidence)
	}
	link := evidence.LinkChain[0]
	if link.Kind != "ordinary" || link.Owner != nil || link.ProviderDetail != nil || link.ResolvedPath != "/usr/bin/python3.13" {
		t.Fatalf("link evidence = %#v", link)
	}
	if evidence.Terminal.Path != "/usr/bin/python3.13" || evidence.Terminal.RequirementID != requirement.ID || evidence.Terminal.SHA256 == "" {
		t.Fatalf("terminal evidence = %#v", evidence.Terminal)
	}
	if got := evidence.Access.Paths[len(evidence.Access.Paths)-1]; got.Path != evidence.Terminal.Path || got.Required != "other-read-execute" {
		t.Fatalf("terminal access = %#v", got)
	}
}

func TestExecutableEvidenceFromProbeCreatesFinalOutputEvidence(t *testing.T) {
	observation := testExecutableObservation()
	observation.ID = "runtime_python3"
	evidence, err := ExecutableEvidenceFromProbe(observation, ProbeExecutableBinding{
		Output: providers.QualifiedOutput{Component: "runtime", Name: "python3"},
		Facts:  providers.CanonicalProviderData{Schema: "python-interpreter-facts-v1", Value: canonical.Object{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.RequirementID != "" || evidence.Terminal.RequirementID != "" {
		t.Fatalf("final evidence retained requirement: %#v", evidence)
	}
}

func TestExecutableEvidenceFromProbeRejectsNonportableTerminalMode(t *testing.T) {
	observation := testExecutableObservation()
	observation.Terminal.Mode = "0750"
	observation.Access[len(observation.Access)-1].Mode = "0750"
	requirement := providers.ExecutableRequirement{
		ID: "interpreter", Command: "python3", Supplier: "runtime",
		ValidationPolicy: providers.ValidationPolicyCompatible,
	}
	_, err := ExecutableEvidenceFromProbe(observation, ProbeExecutableBinding{
		Requirement: &requirement,
		Output:      providers.QualifiedOutput{Component: "runtime", Name: "python3"},
		Facts:       providers.CanonicalProviderData{Schema: "python-interpreter-facts-v1", Value: canonical.Object{}},
	})
	if err == nil || !strings.Contains(err.Error(), "other-read-execute") {
		t.Fatalf("nonportable access error = %v", err)
	}
}

func TestExecutableEvidenceFromProbeRejectsWrongRequestBinding(t *testing.T) {
	observation := testExecutableObservation()
	requirement := providers.ExecutableRequirement{
		ID: "python", Command: "python3", Supplier: "runtime",
		ValidationPolicy: providers.ValidationPolicyCompatible,
	}
	_, err := ExecutableEvidenceFromProbe(observation, ProbeExecutableBinding{
		Requirement: &requirement,
		Output:      providers.QualifiedOutput{Component: "runtime", Name: "python3"},
		Facts:       providers.CanonicalProviderData{Schema: "python-interpreter-facts-v1", Value: canonical.Object{}},
	})
	if err == nil || !strings.Contains(err.Error(), "does not match requirement") {
		t.Fatalf("request binding error = %v", err)
	}
}

func testExecutableObservation() probe.ExecutableObservationV1 {
	digest := canonical.Digest("sha256:" + strings.Repeat("d", 64))
	return probe.ExecutableObservationV1{
		ID: "interpreter", InvocationPath: "/usr/bin/python3",
		Links: []probe.LinkObservationV1{{
			Path: "/usr/bin/python3", Target: "python3.13", ResolvedPath: "/usr/bin/python3.13",
			Mode: "0777", UID: "0", GID: "0",
		}},
		Terminal: probe.FileObservationV1{
			Path: "/usr/bin/python3.13", Kind: "regular", Mode: "0755", Size: "6831736", SHA256: digest, UID: "0", GID: "0",
		},
		Access: []probe.AccessObservationV1{
			{Path: "/", Kind: "directory", Mode: "0755", UID: "0", GID: "0"},
			{Path: "/usr", Kind: "directory", Mode: "0755", UID: "0", GID: "0"},
			{Path: "/usr/bin", Kind: "directory", Mode: "0755", UID: "0", GID: "0"},
			{Path: "/usr/bin/python3.13", Kind: "regular", Mode: "0755", UID: "0", GID: "0"},
		},
	}
}
