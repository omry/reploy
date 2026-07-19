package providers

import (
	"context"
	"strings"
	"testing"
)

func TestRealizeBaseCatalogValidatesDeclaredOutputs(t *testing.T) {
	declaration := OutputDeclaration{
		SupplierComponent: "base", Name: "python", Kind: OutputKindExecutable,
		CandidatePath: "/usr/bin/python3", Provenance: providerData("base-export-v1"),
	}
	plan := ProviderPlanV1{Schema: ProviderPlanSchemaV1, Nodes: []NodeSpec{basePlanNode(declaration)}, Edges: []ProviderEdgeV1{}}
	image := RealizedImageV1{Digest: testDigest("1"), ConfigDigest: testDigest("2"), RootFSSubject: testDigest("3")}
	catalog, err := RealizeBaseCatalog(context.Background(), plan, image, func(_ context.Context, got OutputDeclaration, gotImage RealizedImageV1) (ExecutableEvidence, error) {
		if got.Name != declaration.Name || gotImage != image {
			t.Fatal("base validator received the wrong declaration or image")
		}
		output := RealizedOutput{SupplierComponent: "base", SupplierNode: "base", Name: got.Name, Candidate: ExecutableCandidate{InvocationPath: got.CandidatePath, Provenance: got.Provenance}}
		return selectionEvidence(ExecutableRequirement{}, output), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 1 || catalog[0].Evidence.Terminal.Path != "/usr/bin/python3" || catalog[0].Candidate.Provenance.Schema != "base-export-v1" {
		t.Fatalf("catalog = %#v", catalog)
	}
}

func TestRealizeBaseCatalogRejectsMismatchedOrMalformedEvidence(t *testing.T) {
	declaration := OutputDeclaration{
		SupplierComponent: "base", Name: "python", Kind: OutputKindExecutable,
		CandidatePath: "/usr/bin/python3", Provenance: providerData("base-export-v1"),
	}
	plan := ProviderPlanV1{Schema: ProviderPlanSchemaV1, Nodes: []NodeSpec{basePlanNode(declaration)}, Edges: []ProviderEdgeV1{}}
	image := RealizedImageV1{Digest: testDigest("1"), ConfigDigest: testDigest("2"), RootFSSubject: testDigest("3")}
	_, err := RealizeBaseCatalog(context.Background(), plan, image, func(context.Context, OutputDeclaration, RealizedImageV1) (ExecutableEvidence, error) {
		output := catalogOutput("base", "base", "python", "/usr/bin/python3")
		output.Evidence.Access.Paths[0].Mode = "0700"
		return output.Evidence, nil
	})
	if err == nil || !strings.Contains(err.Error(), "does not prove") {
		t.Fatalf("weak access evidence error = %v", err)
	}

	_, err = RealizeBaseCatalog(context.Background(), plan, image, func(context.Context, OutputDeclaration, RealizedImageV1) (ExecutableEvidence, error) {
		return catalogOutput("base", "base", "python", "/usr/local/bin/python").Evidence, nil
	})
	if err == nil || !strings.Contains(err.Error(), "invocation paths differ") {
		t.Fatalf("mismatched path error = %v", err)
	}
}

func TestRealizeBaseCatalogHonorsCancellationBeforeValidation(t *testing.T) {
	declaration := OutputDeclaration{
		SupplierComponent: "base", Name: "python", Kind: OutputKindExecutable,
		CandidatePath: "/usr/bin/python3", Provenance: providerData("base-export-v1"),
	}
	plan := ProviderPlanV1{Schema: ProviderPlanSchemaV1, Nodes: []NodeSpec{basePlanNode(declaration)}, Edges: []ProviderEdgeV1{}}
	image := RealizedImageV1{Digest: testDigest("1"), ConfigDigest: testDigest("2"), RootFSSubject: testDigest("3")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	_, err := RealizeBaseCatalog(ctx, plan, image, func(context.Context, OutputDeclaration, RealizedImageV1) (ExecutableEvidence, error) {
		called = true
		return ExecutableEvidence{}, nil
	})
	if err != context.Canceled || called {
		t.Fatalf("cancellation error = %v; validator called = %t", err, called)
	}
}

func TestRealizeBaseCatalogNeedsNoValidatorWithoutExports(t *testing.T) {
	plan := ProviderPlanV1{Schema: ProviderPlanSchemaV1, Nodes: []NodeSpec{basePlanNode()}, Edges: []ProviderEdgeV1{}}
	image := RealizedImageV1{Digest: testDigest("1"), ConfigDigest: testDigest("2"), RootFSSubject: testDigest("3")}
	catalog, err := RealizeBaseCatalog(context.Background(), plan, image, nil)
	if err != nil {
		t.Fatal(err)
	}
	if catalog == nil || len(catalog) != 0 {
		t.Fatalf("catalog = %#v", catalog)
	}
}
