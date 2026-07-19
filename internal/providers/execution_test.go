package providers

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providerstore"
)

func validResolveContract(t *testing.T) (ResolveInput, ResolveResult) {
	t.Helper()
	profile := validRequirementProfile()
	node := pythonPlanNode("application", ExecutableRequirement{})
	node.Requirements = profile.Declaration
	upstream := RealizedImageV1{Digest: testDigest("2"), ConfigDigest: testDigest("3"), RootFSSubject: testDigest("4")}
	input := ResolveInput{
		Node: node,
		Candidates: []RequirementCandidatesV1{{
			RequirementID: "interpreter",
			Outputs:       []RealizedOutput{catalogOutput("base", "base", "python", "/usr/bin/python")},
		}},
		Platform: profile.Platform,
		Sources:  []ResolvedSourceInput{},
		Upstream: upstream,
		ReusableArtifacts: []providerstore.StoreObjectRef{
			{Kind: providerstore.BlobKind, Digest: testDigest("8")},
			{Kind: providerstore.BlobKind, Digest: testDigest("9")},
		},
	}
	profileDigest, err := RequirementProfileDigest(profile, validateTestProfileOwner)
	if err != nil {
		t.Fatal(err)
	}
	payload := validPythonBundlePayload()
	payload.Request = node.Request
	payload.RequirementProfileDigest = profileDigest
	payload.Platform = input.Platform
	payload.Upstream = upstream
	bundle, err := NewResolvedBundle(payload, acceptTestBundleOwner)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := NewValidationEvidence(upstream.RootFSSubject, profileDigest)
	if err != nil {
		t.Fatal(err)
	}
	return input, ResolveResult{Bundle: bundle, Profile: profile, Evidence: evidence}
}

func TestResolveContractBindsPlatformUpstreamAndProfile(t *testing.T) {
	input, result := validResolveContract(t)
	if err := ValidateResolveResult(input, result, validateTestProfileOwner, acceptTestBundleOwner); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMaterializeInput(MaterializeInput{
		Bundle: result.Bundle, Profile: result.Profile,
		AssemblyParent: RealizedImageV1{Digest: testDigest("a"), ConfigDigest: testDigest("b"), RootFSSubject: testDigest("c")},
	}, validateTestProfileOwner, acceptTestBundleOwner); err != nil {
		t.Fatal(err)
	}
}

func TestResolveContractRejectsPlatformMismatchAndMalformedInputs(t *testing.T) {
	input, result := validResolveContract(t)
	arm64, err := blueprint.ParsePlatform("linux/arm64")
	if err != nil {
		t.Fatal(err)
	}
	result.Profile.Platform = arm64
	if err := ValidateResolveResult(input, result, validateTestProfileOwner, acceptTestBundleOwner); err == nil || !strings.Contains(err.Error(), "platform") {
		t.Fatalf("platform mismatch error = %v", err)
	}

	input, _ = validResolveContract(t)
	input.Sources = nil
	if err := ValidateResolveInput(input); err == nil || !strings.Contains(err.Error(), "must use arrays") {
		t.Fatalf("nil sources error = %v", err)
	}

	input, _ = validResolveContract(t)
	input.Sources = []ResolvedSourceInput{{
		Schema: ResolvedSourceInputSchemaV1, Component: "other", LogicalPackage: "demo",
		SourceManifestDigest: testDigest("1"), BuilderProfile: "python-wheel-v1",
		BuildSettings:     providerData("python-source-build-settings-v1"),
		EcosystemMetadata: providerData("python-source-metadata-v1"), ArtifactDigest: testDigest("2"),
	}}
	if err := ValidateResolveInput(input); err == nil || !strings.Contains(err.Error(), "outside node") {
		t.Fatalf("outside-node source error = %v", err)
	}

	input, _ = validResolveContract(t)
	input.ReusableArtifacts[0], input.ReusableArtifacts[1] = input.ReusableArtifacts[1], input.ReusableArtifacts[0]
	if err := ValidateResolveInput(input); err == nil || !strings.Contains(err.Error(), "sorted") {
		t.Fatalf("reusable artifact order error = %v", err)
	}

	input, _ = validResolveContract(t)
	input.Candidates[0].Outputs[0].Name = "python3"
	if err := ValidateResolveInput(input); err == nil || !strings.Contains(err.Error(), "different output") {
		t.Fatalf("candidate mismatch error = %v", err)
	}
}

func TestResolveContractFreezesOnlySuppliedCandidates(t *testing.T) {
	input, result := validResolveContract(t)
	edges, err := resolvedSelectionEdges(input, result)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 || edges[0].Supplier != "base" || edges[0].Consumer != "python/application" || edges[0].RequirementID != "interpreter" {
		t.Fatalf("selection edges = %#v", edges)
	}

	input.Node.Requirements.Executables[0].Supplier = ""
	input.Candidates[0].Outputs[0] = catalogOutput("apt", "system", "python", "/usr/bin/python")
	result.Profile.Declaration = input.Node.Requirements
	profileDigest, err := RequirementProfileDigest(result.Profile, validateTestProfileOwner)
	if err != nil {
		t.Fatal(err)
	}
	result.Evidence, err = NewValidationEvidence(input.Upstream.RootFSSubject, profileDigest)
	if err != nil {
		t.Fatal(err)
	}
	result.Bundle.Payload.RequirementProfileDigest = profileDigest
	result.Bundle, err = NewResolvedBundle(result.Bundle.Payload, acceptTestBundleOwner)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateResolveResult(input, result, validateTestProfileOwner, acceptTestBundleOwner); err == nil || !strings.Contains(err.Error(), "does not match an input candidate") {
		t.Fatalf("fabricated selection error = %v", err)
	}
}
