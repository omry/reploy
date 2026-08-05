package dockerdeploy

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
)

func TestAssembleBuildLockPublishesCompleteGraphLock(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	lockedNode := fixture.lock.Nodes[0]
	bundle, err := providers.LoadResolvedBundleManifest(fixture.store, lockedNode.BundleManifest, pythonprovider.ValidateResolvedBundlePayloadV1)
	if err != nil {
		t.Fatal(err)
	}
	baseImage, err := realizedImageFromDescriptor(fixture.lock.Base)
	if err != nil {
		t.Fatal(err)
	}
	overlay := deploy.EmptyRequestOverlayV1()
	overlayDigest, err := deploy.RequestOverlayDigestV1(overlay)
	if err != nil {
		t.Fatal(err)
	}
	request := providers.ResolvedRequestV1{
		Schema:        providers.ResolvedRequestSchemaV1,
		OverlayDigest: overlayDigest, Platform: fixture.request.Platform,
		Components: []providers.ResolvedComponentRequestV1{
			{Component: fixture.request.Plan.Nodes[1].Components[0], Provider: blueprint.ComponentTypePython, Request: fixture.request.Plan.Nodes[1].Request},
			{Component: "base", Provider: blueprint.ComponentTypeBase, Request: fixture.request.Plan.Nodes[0].Request},
		},
		Sources: fixture.request.SourceCandidates,
	}
	if err := providers.ValidateResolvedRequestV1(request, registry.ValidateResolvedRequestOwnersV1); err != nil {
		t.Fatal(err)
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(fixture.lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	finalEvidence := lockedNode.ValidationEvidence
	finalEvidence.SubjectRootFS = fixture.lock.RuntimeLayer.Result.RootFSSubject
	validationReference, err := deploy.PublishPrefixValidation(context.Background(), fixture.store, deploy.PrefixValidationV1{
		Schema: deploy.PrefixValidationSchemaV1, SubjectRootFS: fixture.lock.RuntimeLayer.Result.RootFSSubject,
		Profiles: []providers.ValidationEvidence{finalEvidence}, RuntimePolicy: policyDigest,
		ExposedOutputs: []providers.ExecutableEvidence{},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := providers.GraphExecutionResult{
		Plan: fixture.request.Plan, SelectedEdges: fixture.request.Plan.Edges,
		Bundles: []providers.ResolvedBundle{bundle}, Profiles: []providers.RequirementProfile{lockedNode.RequirementProfile},
		ValidationEvidence: []providers.ValidationEvidence{lockedNode.ValidationEvidence},
		PrefixImages:       []providers.RealizedImageV1{baseImage, lockedNode.Result},
		Materializations: []providers.GraphNodeMaterializeResult{{
			Image: lockedNode.Result, TransactionDigest: lockedNode.TransactionDigest,
			GeneratedExecutables: lockedNode.GeneratedExecutables, Outputs: lockedNode.Outputs,
		}},
		Catalog: append([]providers.RealizedOutput{}, fixture.request.EarlierCatalog...),
	}
	lock, err := AssembleBuildLock(context.Background(), fixture.store, BuildLockAssemblyInput{
		BlueprintDigest: rendererDigest("b"), ResolvedRequest: request, Overlay: overlay,
		PackageOverrides: fixture.lock.PackageOverrides, Base: fixture.lock.Base, Graph: graph,
		RuntimePolicy: fixture.lock.RuntimePolicy, RuntimeLayer: fixture.lock.RuntimeLayer,
		ValidationRecord: validationReference, FinalImage: fixture.lock.FinalImage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if lock.BlueprintDigest != rendererDigest("b") || len(lock.Nodes) != 1 || lock.Nodes[0].NodeID != fixture.request.NodeID || lock.Nodes[0].ResolverCacheKey == "" || lock.ResolvedRequestDigest == "" || !reflect.DeepEqual(lock.Catalog, graph.Catalog) {
		t.Fatalf("assembled lock = %#v", lock)
	}
	if _, err := providers.LoadResolvedBundleManifest(fixture.store, lock.Nodes[0].BundleManifest, pythonprovider.ValidateResolvedBundlePayloadV1); err != nil {
		t.Fatal(err)
	}
	if err := deploy.ValidateBuildLockV1(lock, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
}

func TestAssembleBuildLockRejectsMisalignedGraphBeforePublication(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	overlay := deploy.EmptyRequestOverlayV1()
	overlayDigest, err := deploy.RequestOverlayDigestV1(overlay)
	if err != nil {
		t.Fatal(err)
	}
	request := providers.ResolvedRequestV1{
		Schema: providers.ResolvedRequestSchemaV1, OverlayDigest: overlayDigest,
		Platform: fixture.request.Platform,
		Components: []providers.ResolvedComponentRequestV1{
			{Component: fixture.request.Plan.Nodes[1].Components[0], Provider: blueprint.ComponentTypePython, Request: fixture.request.Plan.Nodes[1].Request},
			{Component: "base", Provider: blueprint.ComponentTypeBase, Request: fixture.request.Plan.Nodes[0].Request},
		},
		Sources: fixture.request.SourceCandidates,
	}
	_, err = AssembleBuildLock(context.Background(), fixture.store, BuildLockAssemblyInput{
		BlueprintDigest: rendererDigest("b"), ResolvedRequest: request, Overlay: overlay,
		PackageOverrides: fixture.lock.PackageOverrides, Base: fixture.lock.Base,
		Graph:         providers.GraphExecutionResult{Plan: fixture.request.Plan, PrefixImages: []providers.RealizedImageV1{}},
		RuntimePolicy: fixture.lock.RuntimePolicy, ValidationRecord: fixture.lock.ValidationRecord, FinalImage: fixture.lock.FinalImage,
	})
	if err == nil || !strings.Contains(err.Error(), "collections do not align") {
		t.Fatalf("misaligned graph error = %v", err)
	}
}
