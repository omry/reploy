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
		Platform:         profile.Platform,
		SourceCandidates: []ResolvedSourceInput{},
		Upstream:         upstream,
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
	return input, ResolveResult{Bundle: bundle, Profile: profile, Evidence: evidence, SelectedSources: []ResolvedSourceInput{}}
}

func TestResolveContractBindsPlatformUpstreamAndProfile(t *testing.T) {
	input, result := validResolveContract(t)
	if err := ValidateResolveResult(input, result, validateTestProfileOwner, acceptTestBundleOwner); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMaterializeInput(MaterializeInput{
		Bundle: result.Bundle, Profile: result.Profile,
		AssemblyParent:      result.Bundle.Payload.Upstream,
		Carrier:             materializeTestExecutable("carrier", ExecutableRoleCarrier, "/bin/sh"),
		EnvironmentLauncher: materializeTestExecutable("cleanenv", ExecutableRoleEnvironmentLauncher, "/usr/bin/env"),
		FinalImageConfig:    materializeTestFinalImageConfig(),
	}, validateTestProfileOwner, acceptTestBundleOwner); err != nil {
		t.Fatal(err)
	}
}

func TestResolveContractAcceptsOnlyExactSelectedSourceCandidates(t *testing.T) {
	input, result := validResolveContract(t)
	source := ResolvedSourceInput{
		Schema: ResolvedSourceInputSchemaV2, Component: "application", LogicalPackage: "demo",
		SourceInputDigest: testDigest("a"), SourceArtifactDigest: testDigest("c"),
		BuildEnvironmentDigest: testDigest("d"), BuilderProfile: "python-wheel-v1",
		BuildSettings:     providerData("python-source-build-settings-v1"),
		EcosystemMetadata: providerData("python-source-metadata-v1"), OutputArtifactDigest: testDigest("b"),
	}
	input.SourceCandidates = []ResolvedSourceInput{source}
	result.SelectedSources = []ResolvedSourceInput{source}
	result.Bundle.Payload.SelectedSources = []ResolvedSourceInput{source}
	rebuilt, err := NewResolvedBundle(result.Bundle.Payload, acceptTestBundleOwner)
	if err != nil {
		t.Fatal(err)
	}
	result.Bundle = rebuilt
	if err := ValidateResolveResult(input, result, validateTestProfileOwner, acceptTestBundleOwner); err != nil {
		t.Fatal(err)
	}

	result.SelectedSources = nil
	if err := ValidateResolveResult(input, result, validateTestProfileOwner, acceptTestBundleOwner); err == nil || !strings.Contains(err.Error(), "must use an array") {
		t.Fatalf("nil selected sources error = %v", err)
	}
	result.SelectedSources = []ResolvedSourceInput{source}
	result.SelectedSources[0].OutputArtifactDigest = testDigest("c")
	if err := ValidateResolveResult(input, result, validateTestProfileOwner, acceptTestBundleOwner); err == nil || !strings.Contains(err.Error(), "exactly match") {
		t.Fatalf("changed selected source error = %v", err)
	}
	result.SelectedSources = []ResolvedSourceInput{source}
	result.SelectedSources[0].LogicalPackage = "other"
	if err := ValidateResolveResult(input, result, validateTestProfileOwner, acceptTestBundleOwner); err == nil || !strings.Contains(err.Error(), "not offered") {
		t.Fatalf("unknown selected source error = %v", err)
	}
}

func materializeTestFinalImageConfig() ImageConfigPolicy {
	return ImageConfigPolicy{
		User: "1000:1000", WorkingDir: "/work",
		Environment: []EnvironmentVariable{}, Entrypoint: []string{}, Command: []string{},
		Healthcheck: ImageHealthcheckNone, StopSignal: "SIGTERM", Labels: []ImageLabel{},
	}
}

func materializeTestExecutable(id string, role string, invocationPath string) ValidatedExecutableInput {
	return ValidatedExecutableInput{
		ID: id, Role: role, Policy: ValidationPolicyCompatible,
		Evidence: selectionEvidence(
			ExecutableRequirement{ID: id},
			catalogOutput("base", "base", id, invocationPath),
		),
	}
}

func TestMaterializeInputRequiresDistinctValidatedBackendExecutables(t *testing.T) {
	_, result := validResolveContract(t)
	valid := MaterializeInput{
		Bundle: result.Bundle, Profile: result.Profile,
		AssemblyParent:      result.Bundle.Payload.Upstream,
		Carrier:             materializeTestExecutable("carrier", ExecutableRoleCarrier, "/bin/sh"),
		EnvironmentLauncher: materializeTestExecutable("cleanenv", ExecutableRoleEnvironmentLauncher, "/usr/bin/env"),
		FinalImageConfig:    materializeTestFinalImageConfig(),
	}
	tests := []struct {
		name   string
		mutate func(*MaterializeInput)
		want   string
	}{
		{name: "missing carrier", mutate: func(input *MaterializeInput) { input.Carrier = ValidatedExecutableInput{} }, want: "carrier"},
		{name: "wrong assembly parent", mutate: func(input *MaterializeInput) { input.AssemblyParent.Digest = testDigest("f") }, want: "assembly parent"},
		{name: "carrier role", mutate: func(input *MaterializeInput) { input.Carrier.Role = ExecutableRoleProviderPrerequisite }, want: "carrier role"},
		{name: "launcher role", mutate: func(input *MaterializeInput) { input.EnvironmentLauncher.Role = ExecutableRoleProviderPrerequisite }, want: "environment launcher role"},
		{name: "duplicate ID", mutate: func(input *MaterializeInput) {
			input.EnvironmentLauncher = materializeTestExecutable("carrier", ExecutableRoleEnvironmentLauncher, "/usr/bin/env")
		}, want: "IDs must differ"},
		{name: "final image config", mutate: func(input *MaterializeInput) { input.FinalImageConfig.WorkingDir = "work" }, want: "final image config"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := ValidateMaterializeInput(candidate, validateTestProfileOwner, acceptTestBundleOwner); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
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
	input.SourceCandidates = nil
	if err := ValidateResolveInput(input); err == nil || !strings.Contains(err.Error(), "must use arrays") {
		t.Fatalf("nil sources error = %v", err)
	}

	input, _ = validResolveContract(t)
	input.SourceCandidates = []ResolvedSourceInput{{
		Schema: ResolvedSourceInputSchemaV2, Component: "other", LogicalPackage: "demo",
		SourceInputDigest: testDigest("1"), SourceArtifactDigest: testDigest("3"),
		BuildEnvironmentDigest: testDigest("4"), BuilderProfile: "python-wheel-v1",
		BuildSettings:     providerData("python-source-build-settings-v1"),
		EcosystemMetadata: providerData("python-source-metadata-v1"), OutputArtifactDigest: testDigest("2"),
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
