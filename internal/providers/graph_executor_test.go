package providers

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providerstore"
)

func acceptGraphConsumer() GraphConsumerValidation {
	return GraphConsumerValidation{
		Carrier:             materializeTestExecutable("carrier", ExecutableRoleCarrier, "/bin/sh"),
		EnvironmentLauncher: materializeTestExecutable("cleanenv", ExecutableRoleEnvironmentLauncher, "/usr/bin/env"),
		FinalImageConfig:    materializeTestFinalImageConfig(),
	}
}

func TestExecuteProviderGraphCarriesOnlyEarlierCatalogAndPrefix(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	baseDeclaration := OutputDeclaration{
		SupplierComponent: "base", Name: "python", Kind: OutputKindExecutable,
		CandidatePath: "/usr/bin/python", Provenance: providerData("test-output-v1"),
	}
	requirement := ExecutableRequirement{ID: "interpreter", Command: "python", Supplier: "base", ValidationPolicy: ValidationPolicyCompatible}
	a := pythonPlanNode("a", requirement)
	z := pythonPlanNode("z", requirement)
	plan := ProviderPlanV1{
		Schema: ProviderPlanSchemaV1, Nodes: []NodeSpec{basePlanNode(baseDeclaration), a, z},
		Edges: []ProviderEdgeV1{
			{Supplier: "base", Consumer: "python/a", RequirementID: "interpreter", Output: QualifiedOutput{Component: "base", Name: "python"}},
			{Supplier: "base", Consumer: "python/z", RequirementID: "interpreter", Output: QualifiedOutput{Component: "base", Name: "python"}},
		},
	}
	baseImage := RealizedImageV1{Digest: testDigest("1"), ConfigDigest: testDigest("2"), RootFSSubject: testDigest("3")}
	baseCatalog := []RealizedOutput{catalogOutput("base", "base", "python", "/usr/bin/python")}
	visited := []NodeID{}
	upstreams := []RealizedImageV1{}
	catalogSizes := []int{}
	nextDigest := byte('4')
	result, err := ExecuteProviderGraph(context.Background(), GraphExecutionRequest{
		Plan: plan, Platform: platform, Sources: []ResolvedSourceInput{}, BaseImage: baseImage, BaseCatalog: baseCatalog,
		ReusableArtifacts: map[NodeID][]providerstore.StoreObjectRef{},
		CachedResolutions: map[NodeID]ResolveResult{},
		Validators: func(NodeSpec) (ProviderOwnerValidators, error) {
			return ProviderOwnerValidators{Profile: func(RequirementProfile) error { return nil }, Bundle: acceptTestBundleOwner}, nil
		},
		PrepareNode: func(_ context.Context, request GraphNodePrepareRequest) (GraphNodePreparation, error) {
			visited = append(visited, request.Resolve.NodeID)
			upstreams = append(upstreams, request.Resolve.Upstream)
			catalogSizes = append(catalogSizes, len(request.Resolve.EarlierCatalog))
			return GraphNodePreparation{
				Resolution: graphTestResolution(t, request.Resolve, platform),
				Consumer:   acceptGraphConsumer(),
			}, nil
		},
		MaterializeNode: func(_ context.Context, request GraphNodeMaterializeRequest) (GraphNodeMaterializeResult, error) {
			expected := acceptGraphConsumer()
			if !reflect.DeepEqual(request.Input.Carrier, expected.Carrier) || !reflect.DeepEqual(request.Input.EnvironmentLauncher, expected.EnvironmentLauncher) || !reflect.DeepEqual(request.Input.FinalImageConfig, expected.FinalImageConfig) {
				return GraphNodeMaterializeResult{}, errors.New("consumer validation inputs were not passed to materialization")
			}
			image := RealizedImageV1{
				Digest: testDigest(string(nextDigest)), ConfigDigest: testDigest(string(nextDigest + 1)), RootFSSubject: testDigest(string(nextDigest + 2)),
			}
			transactionDigest := testDigest("d")
			nextDigest += 3
			outputs := graphTestRealizedOutputs(request.Input.Bundle)
			return GraphNodeMaterializeResult{Image: image, TransactionDigest: transactionDigest, GeneratedExecutables: []RealizedGeneratedExecutable{}, Outputs: outputs}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(visited, []NodeID{"python/a", "python/z"}) || !reflect.DeepEqual(catalogSizes, []int{1, 2}) {
		t.Fatalf("visited = %#v; catalog sizes = %#v", visited, catalogSizes)
	}
	if upstreams[0] != baseImage || upstreams[1] != result.PrefixImages[1] {
		t.Fatalf("upstreams = %#v; prefixes = %#v", upstreams, result.PrefixImages)
	}
	if len(result.Bundles) != 2 || len(result.Materializations) != 2 || len(result.Catalog) != 3 || len(result.SelectedEdges) != 2 {
		t.Fatalf("graph result = %#v", result)
	}
	if result.Materializations[0].Image != result.PrefixImages[1] || result.Materializations[1].Image != result.PrefixImages[2] {
		t.Fatalf("materializations do not align with prefix images: %#v; %#v", result.Materializations, result.PrefixImages)
	}
}

func TestExecuteProviderGraphRejectsOutputDriftBeforePublishingCatalog(t *testing.T) {
	input, resolution := validResolveContract(t)
	plan := testResolvePlan(input)
	baseCatalog := []RealizedOutput{catalogOutput("base", "base", "python", "/usr/bin/python")}
	baseCatalog[0].Candidate.Provenance = plan.Nodes[0].OutputDeclarations[0].Provenance
	badOutputs := graphTestRealizedOutputs(resolution.Bundle)
	badOutputs[0].Candidate.InvocationPath = "/changed"
	_, err := ExecuteProviderGraph(context.Background(), GraphExecutionRequest{
		Plan: plan, Platform: input.Platform, Sources: input.Sources, BaseImage: input.Upstream, BaseCatalog: baseCatalog,
		ReusableArtifacts: map[NodeID][]providerstore.StoreObjectRef{input.Node.ID: input.ReusableArtifacts},
		CachedResolutions: map[NodeID]ResolveResult{},
		Validators: func(NodeSpec) (ProviderOwnerValidators, error) {
			return ProviderOwnerValidators{Profile: validateTestProfileOwner, Bundle: acceptTestBundleOwner}, nil
		},
		PrepareNode: func(context.Context, GraphNodePrepareRequest) (GraphNodePreparation, error) {
			return GraphNodePreparation{Resolution: resolution, Consumer: acceptGraphConsumer()}, nil
		},
		MaterializeNode: func(context.Context, GraphNodeMaterializeRequest) (GraphNodeMaterializeResult, error) {
			return GraphNodeMaterializeResult{Image: input.Upstream, TransactionDigest: testDigest("d"), GeneratedExecutables: []RealizedGeneratedExecutable{}, Outputs: badOutputs}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "candidate changed") {
		t.Fatalf("error = %v", err)
	}
}

func TestExecuteProviderGraphRejectsNodeSuccessAfterCancellation(t *testing.T) {
	input, resolution := validResolveContract(t)
	plan := testResolvePlan(input)
	baseCatalog := []RealizedOutput{catalogOutput("base", "base", "python", "/usr/bin/python")}
	baseCatalog[0].Candidate.Provenance = plan.Nodes[0].OutputDeclarations[0].Provenance
	ctx, cancel := context.WithCancel(context.Background())
	_, err := ExecuteProviderGraph(ctx, GraphExecutionRequest{
		Plan: plan, Platform: input.Platform, Sources: input.Sources, BaseImage: input.Upstream, BaseCatalog: baseCatalog,
		ReusableArtifacts: map[NodeID][]providerstore.StoreObjectRef{input.Node.ID: input.ReusableArtifacts},
		CachedResolutions: map[NodeID]ResolveResult{},
		Validators: func(NodeSpec) (ProviderOwnerValidators, error) {
			return ProviderOwnerValidators{Profile: validateTestProfileOwner, Bundle: acceptTestBundleOwner}, nil
		},
		PrepareNode: func(context.Context, GraphNodePrepareRequest) (GraphNodePreparation, error) {
			cancel()
			return GraphNodePreparation{Resolution: resolution, Consumer: acceptGraphConsumer()}, nil
		},
		MaterializeNode: func(context.Context, GraphNodeMaterializeRequest) (GraphNodeMaterializeResult, error) {
			t.Fatal("materialization ran after resolution cancellation")
			return GraphNodeMaterializeResult{}, nil
		},
	})
	if err != context.Canceled {
		t.Fatalf("error = %v", err)
	}
}

func TestExecuteProviderGraphValidatesResolutionBeforeMaterialization(t *testing.T) {
	input, resolution := validResolveContract(t)
	plan := testResolvePlan(input)
	baseCatalog := []RealizedOutput{catalogOutput("base", "base", "python", "/usr/bin/python")}
	baseCatalog[0].Candidate.Provenance = plan.Nodes[0].OutputDeclarations[0].Provenance
	arm64, err := blueprint.ParsePlatform("linux/arm64")
	if err != nil {
		t.Fatal(err)
	}
	resolution.Profile.Platform = arm64
	materialized := false
	_, err = ExecuteProviderGraph(context.Background(), GraphExecutionRequest{
		Plan: plan, Platform: input.Platform, Sources: input.Sources, BaseImage: input.Upstream, BaseCatalog: baseCatalog,
		ReusableArtifacts: map[NodeID][]providerstore.StoreObjectRef{input.Node.ID: input.ReusableArtifacts},
		CachedResolutions: map[NodeID]ResolveResult{},
		Validators: func(NodeSpec) (ProviderOwnerValidators, error) {
			return ProviderOwnerValidators{Profile: validateTestProfileOwner, Bundle: acceptTestBundleOwner}, nil
		},
		PrepareNode: func(context.Context, GraphNodePrepareRequest) (GraphNodePreparation, error) {
			return GraphNodePreparation{Resolution: resolution, Consumer: acceptGraphConsumer()}, nil
		},
		MaterializeNode: func(context.Context, GraphNodeMaterializeRequest) (GraphNodeMaterializeResult, error) {
			materialized = true
			return GraphNodeMaterializeResult{}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "platform") || materialized {
		t.Fatalf("error = %v; materialized = %v", err, materialized)
	}
}

func TestExecuteProviderGraphPassesCachedResolutionToOnePreparation(t *testing.T) {
	input, resolution := validResolveContract(t)
	plan := testResolvePlan(input)
	baseCatalog := []RealizedOutput{catalogOutput("base", "base", "python", "/usr/bin/python")}
	baseCatalog[0].Candidate.Provenance = plan.Nodes[0].OutputDeclarations[0].Provenance
	preparationCalls := 0
	materialized := false
	_, err := ExecuteProviderGraph(context.Background(), GraphExecutionRequest{
		Plan: plan, Platform: input.Platform, Sources: input.Sources, BaseImage: input.Upstream, BaseCatalog: baseCatalog,
		ReusableArtifacts: map[NodeID][]providerstore.StoreObjectRef{input.Node.ID: input.ReusableArtifacts},
		CachedResolutions: map[NodeID]ResolveResult{input.Node.ID: resolution},
		Validators: func(NodeSpec) (ProviderOwnerValidators, error) {
			return ProviderOwnerValidators{Profile: validateTestProfileOwner, Bundle: acceptTestBundleOwner}, nil
		},
		PrepareNode: func(_ context.Context, request GraphNodePrepareRequest) (GraphNodePreparation, error) {
			preparationCalls++
			if request.Resolve.NodeID != input.Node.ID || request.CachedResolution == nil || !reflect.DeepEqual(*request.CachedResolution, resolution) {
				return GraphNodePreparation{}, errors.New("preparer did not receive the exact cached resolution")
			}
			return GraphNodePreparation{Resolution: resolution, Consumer: acceptGraphConsumer(), Refreshed: true}, nil
		},
		MaterializeNode: func(context.Context, GraphNodeMaterializeRequest) (GraphNodeMaterializeResult, error) {
			materialized = true
			return GraphNodeMaterializeResult{Image: input.Upstream, TransactionDigest: testDigest("d"), GeneratedExecutables: []RealizedGeneratedExecutable{}, Outputs: graphTestRealizedOutputs(resolution.Bundle)}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if preparationCalls != 1 || !materialized {
		t.Fatalf("preparation calls = %d, materialized = %v", preparationCalls, materialized)
	}
}

func TestExecuteProviderGraphRejectsPreparationFailureBeforeMaterialization(t *testing.T) {
	input, resolution := validResolveContract(t)
	plan := testResolvePlan(input)
	baseCatalog := []RealizedOutput{catalogOutput("base", "base", "python", "/usr/bin/python")}
	baseCatalog[0].Candidate.Provenance = plan.Nodes[0].OutputDeclarations[0].Provenance
	preparationCalls := 0
	materialized := false
	_, err := ExecuteProviderGraph(context.Background(), GraphExecutionRequest{
		Plan: plan, Platform: input.Platform, Sources: input.Sources, BaseImage: input.Upstream, BaseCatalog: baseCatalog,
		ReusableArtifacts: map[NodeID][]providerstore.StoreObjectRef{input.Node.ID: input.ReusableArtifacts},
		CachedResolutions: map[NodeID]ResolveResult{input.Node.ID: resolution},
		Validators: func(NodeSpec) (ProviderOwnerValidators, error) {
			return ProviderOwnerValidators{Profile: validateTestProfileOwner, Bundle: acceptTestBundleOwner}, nil
		},
		PrepareNode: func(context.Context, GraphNodePrepareRequest) (GraphNodePreparation, error) {
			preparationCalls++
			return GraphNodePreparation{}, errors.New("cached interpreter changed and fresh resolution failed")
		},
		MaterializeNode: func(context.Context, GraphNodeMaterializeRequest) (GraphNodeMaterializeResult, error) {
			materialized = true
			return GraphNodeMaterializeResult{}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "cached interpreter changed and fresh resolution failed") {
		t.Fatalf("error = %v", err)
	}
	if preparationCalls != 1 || materialized {
		t.Fatalf("preparation calls = %d, materialized = %v", preparationCalls, materialized)
	}
}

func graphTestResolution(t *testing.T, request ResolveNodeRequest, platform blueprint.Platform) ResolveResult {
	t.Helper()
	node, input, err := buildResolveInput(request)
	if err != nil {
		t.Fatal(err)
	}
	selected := make([]ExecutableEvidence, 0, len(node.Requirements.Executables))
	for index, requirement := range node.Requirements.Executables {
		selected = append(selected, selectionEvidence(requirement, input.Candidates[index].Outputs[0]))
	}
	profile := RequirementProfile{
		Schema: RequirementProfileSchemaV1, Declaration: node.Requirements,
		SelectedExecutables: selected, SelectedFiles: []FileEvidence{}, Platform: platform,
		Facts: providerData("graph-profile-v1"),
	}
	profileDigest, err := RequirementProfileDigest(profile, func(RequirementProfile) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	component := node.Components[0]
	payload := validPythonBundlePayload()
	payload.NodeID = node.ID
	payload.Request = node.Request
	payload.RequirementProfileDigest = profileDigest
	payload.Platform = platform
	payload.Upstream = request.Upstream
	payload.Artifacts = []providerstore.ArtifactDescriptor{}
	payload.Outputs = []ResolvedOutput{{
		SupplierComponent: component, SupplierNode: node.ID, Name: "tool",
		Candidate: ExecutableCandidate{InvocationPath: "/opt/reploy/python/" + component + "/bin/tool", Provenance: providerData("python-console-script-v1")},
	}}
	bundle, err := NewResolvedBundle(payload, acceptTestBundleOwner)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := NewValidationEvidence(request.Upstream.RootFSSubject, profileDigest)
	if err != nil {
		t.Fatal(err)
	}
	return ResolveResult{Bundle: bundle, Profile: profile, Evidence: evidence}
}

func graphTestRealizedOutputs(bundle ResolvedBundle) []RealizedOutput {
	result := make([]RealizedOutput, 0, len(bundle.Payload.Outputs))
	for _, resolved := range bundle.Payload.Outputs {
		output := RealizedOutput{
			SupplierComponent: resolved.SupplierComponent, SupplierNode: resolved.SupplierNode,
			Name: resolved.Name, Candidate: resolved.Candidate,
		}
		output.Evidence = selectionEvidence(ExecutableRequirement{}, output)
		result = append(result, output)
	}
	return result
}
