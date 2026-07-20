package providers

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providerstore"
)

type testNodeResolver struct {
	provider blueprint.ComponentType
	result   ResolveResult
	input    ResolveInput
	cancel   context.CancelFunc
}

func (resolver *testNodeResolver) Type() blueprint.ComponentType { return resolver.provider }

func (resolver *testNodeResolver) Resolve(_ context.Context, input ResolveInput, _ ArtifactSink) (ResolveResult, error) {
	resolver.input = input
	if resolver.cancel != nil {
		resolver.cancel()
	}
	return resolver.result, nil
}

type testArtifactSink struct{}

func (testArtifactSink) Publish(_ context.Context, logicalPath string, kind string, reader io.Reader) (providerstore.ArtifactDescriptor, error) {
	content, err := io.ReadAll(reader)
	if err != nil {
		return providerstore.ArtifactDescriptor{}, err
	}
	digest := sha256.Sum256(content)
	return providerstore.ArtifactDescriptor{
		LogicalPath: logicalPath, Kind: kind, Size: strconv.Itoa(len(content)),
		SHA256: canonical.Digest(fmt.Sprintf("sha256:%x", digest)),
	}, nil
}

func testResolvePlan(input ResolveInput) ProviderPlanV1 {
	baseOutput := OutputDeclaration{
		SupplierComponent: "base", Name: "python", Kind: OutputKindExecutable,
		CandidatePath: "/usr/bin/python", Provenance: providerData("base-export-v1"),
	}
	return ProviderPlanV1{
		Schema: ProviderPlanSchemaV1,
		Nodes:  []NodeSpec{basePlanNode(baseOutput), input.Node},
		Edges: []ProviderEdgeV1{{
			Supplier: "base", Consumer: input.Node.ID, RequirementID: "interpreter",
			Output: QualifiedOutput{Component: "base", Name: "python"},
		}},
	}
}

func TestResolveProviderNodeBuildsCandidatesAndValidatesResult(t *testing.T) {
	input, result := validResolveContract(t)
	plan := testResolvePlan(input)
	resolver := &testNodeResolver{provider: blueprint.ComponentTypePython, result: result}
	resolved, err := ResolveProviderNode(context.Background(), ResolveNodeRequest{
		Plan: plan, NodeID: input.Node.ID,
		EarlierCatalog:    []RealizedOutput{catalogOutput("base", "base", "python", "/usr/bin/python")},
		Platform:          input.Platform,
		SourceCandidates:  input.SourceCandidates,
		Upstream:          input.Upstream,
		ReusableArtifacts: input.ReusableArtifacts,
	}, resolver, testArtifactSink{}, ProviderOwnerValidators{Profile: validateTestProfileOwner, Bundle: acceptTestBundleOwner})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Bundle.Identity != result.Bundle.Identity || len(resolver.input.Candidates) != 1 || resolver.input.Candidates[0].Outputs[0].SupplierNode != "base" {
		t.Fatalf("resolved = %#v; resolver input = %#v", resolved, resolver.input)
	}
	if resolver.input.Platform.Canonical != "linux/amd64" {
		t.Fatalf("resolver platform = %#v", resolver.input.Platform)
	}
}

func TestValidateProviderNodeResolutionChecksCachedResultAgainstExactInput(t *testing.T) {
	input, result := validResolveContract(t)
	request := ResolveNodeRequest{
		Plan: testResolvePlan(input), NodeID: input.Node.ID,
		EarlierCatalog:    []RealizedOutput{catalogOutput("base", "base", "python", "/usr/bin/python")},
		Platform:          input.Platform,
		SourceCandidates:  input.SourceCandidates,
		Upstream:          input.Upstream,
		ReusableArtifacts: input.ReusableArtifacts,
	}
	validators := ProviderOwnerValidators{Profile: validateTestProfileOwner, Bundle: acceptTestBundleOwner}
	if err := ValidateProviderNodeResolution(request, result, validators); err != nil {
		t.Fatal(err)
	}
	request.Upstream.RootFSSubject = testDigest("changed")
	if err := ValidateProviderNodeResolution(request, result, validators); err == nil || !strings.Contains(err.Error(), "upstream") {
		t.Fatalf("changed upstream error = %v", err)
	}
}

func TestResolveProviderNodeRejectsWrongResolverAndResult(t *testing.T) {
	input, result := validResolveContract(t)
	plan := testResolvePlan(input)
	request := ResolveNodeRequest{
		Plan: plan, NodeID: input.Node.ID,
		EarlierCatalog: []RealizedOutput{catalogOutput("base", "base", "python", "/usr/bin/python")}, Platform: input.Platform,
		SourceCandidates: input.SourceCandidates, Upstream: input.Upstream, ReusableArtifacts: input.ReusableArtifacts,
	}
	wrong := &testNodeResolver{provider: blueprint.ComponentTypeAPT, result: result}
	if _, err := ResolveProviderNode(context.Background(), request, wrong, testArtifactSink{}, ProviderOwnerValidators{}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("wrong resolver error = %v", err)
	}

	arm64, err := blueprint.ParsePlatform("linux/arm64")
	if err != nil {
		t.Fatal(err)
	}
	result.Bundle.Payload.Platform = arm64
	result.Bundle, err = NewResolvedBundle(result.Bundle.Payload, acceptTestBundleOwner)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &testNodeResolver{provider: blueprint.ComponentTypePython, result: result}
	if _, err := ResolveProviderNode(context.Background(), request, resolver, testArtifactSink{}, ProviderOwnerValidators{Profile: validateTestProfileOwner, Bundle: acceptTestBundleOwner}); err == nil || !strings.Contains(err.Error(), "platform") {
		t.Fatalf("wrong result error = %v", err)
	}
}

func TestResolveProviderNodeRejectsSuccessAfterCancellation(t *testing.T) {
	input, result := validResolveContract(t)
	ctx, cancel := context.WithCancel(context.Background())
	resolver := &testNodeResolver{provider: blueprint.ComponentTypePython, result: result, cancel: cancel}
	_, err := ResolveProviderNode(ctx, ResolveNodeRequest{
		Plan: testResolvePlan(input), NodeID: input.Node.ID,
		EarlierCatalog: []RealizedOutput{catalogOutput("base", "base", "python", "/usr/bin/python")},
		Platform:       input.Platform, SourceCandidates: input.SourceCandidates, Upstream: input.Upstream, ReusableArtifacts: input.ReusableArtifacts,
	}, resolver, testArtifactSink{}, ProviderOwnerValidators{Profile: validateTestProfileOwner, Bundle: acceptTestBundleOwner})
	if err != context.Canceled {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestResolveProviderNodeFiltersSourcesToSelectedNode(t *testing.T) {
	input, result := validResolveContract(t)
	source := ResolvedSourceInput{
		Schema: ResolvedSourceInputSchemaV1, Component: "application", LogicalPackage: "demo",
		SourceManifestDigest: testDigest("1"), BuilderProfile: "python-wheel-v1",
		BuildSettings:     providerData("python-source-build-settings-v1"),
		EcosystemMetadata: providerData("python-source-metadata-v1"), ArtifactDigest: testDigest("2"),
	}
	resolver := &testNodeResolver{provider: blueprint.ComponentTypePython, result: result}
	_, err := ResolveProviderNode(context.Background(), ResolveNodeRequest{
		Plan: testResolvePlan(input), NodeID: input.Node.ID,
		EarlierCatalog: []RealizedOutput{catalogOutput("base", "base", "python", "/usr/bin/python")},
		Platform:       input.Platform, SourceCandidates: []ResolvedSourceInput{source}, Upstream: input.Upstream,
		ReusableArtifacts: input.ReusableArtifacts,
	}, resolver, testArtifactSink{}, ProviderOwnerValidators{Profile: validateTestProfileOwner, Bundle: acceptTestBundleOwner})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolver.input.SourceCandidates) != 1 || !reflect.DeepEqual(resolver.input.SourceCandidates[0], source) {
		t.Fatalf("resolver source candidates = %#v", resolver.input.SourceCandidates)
	}
}
