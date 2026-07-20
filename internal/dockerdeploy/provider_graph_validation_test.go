package dockerdeploy

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
)

func TestPrepareProviderGraphValidationBuildsCumulativeLayerAndFinalInputs(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	bundle, err := providers.LoadResolvedBundleManifest(fixture.store, fixture.lock.Nodes[0].BundleManifest, pythonprovider.ValidateResolvedBundlePayloadV1)
	if err != nil {
		t.Fatal(err)
	}
	layerDescriptor := providerBaseDescriptor(t, false)
	layerDescriptor.ConfigDigest = rendererDigest("d")
	layerDescriptor.AuthorReference = string(layerDescriptor.ConfigDigest)
	layerDescriptor.ImmutableReference = string(layerDescriptor.ConfigDigest)
	layerDescriptor.RootFSDiffIDs = []canonical.Digest{rendererDigest("e")}
	layerImage, err := realizedImageFromDescriptor(layerDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	baseImage, err := realizedImageFromDescriptor(fixture.lock.Base)
	if err != nil {
		t.Fatal(err)
	}
	materialized := fixture.lock.Nodes[0]
	graph := providers.GraphExecutionResult{
		Plan: fixture.request.Plan, SelectedEdges: append([]providers.ProviderEdgeV1{}, fixture.request.Plan.Edges...),
		Bundles: []providers.ResolvedBundle{bundle}, Profiles: []providers.RequirementProfile{materialized.RequirementProfile},
		ValidationEvidence: []providers.ValidationEvidence{materialized.ValidationEvidence},
		PrefixImages:       []providers.RealizedImageV1{baseImage, layerImage},
		Materializations: []providers.GraphNodeMaterializeResult{{
			Image: layerImage, TransactionDigest: materialized.TransactionDigest,
			GeneratedExecutables: []providers.RealizedGeneratedExecutable{}, Outputs: []providers.RealizedOutput{},
		}},
		Catalog: append([]providers.RealizedOutput{}, fixture.request.EarlierCatalog...),
	}
	inspections := 0
	plan, err := prepareProviderGraphValidation(
		context.Background(), fixture.lock.Base, fixture.request.EarlierCatalog, graph, fixture.lock.RuntimePolicy,
		func(_ context.Context, candidate BuiltImageCandidate, platform blueprint.Platform) (InspectedImageCandidate, error) {
			inspections++
			if candidate.ImageID != layerImage.ConfigDigest || platform != fixture.request.Platform {
				t.Fatalf("inspection candidate/platform = %#v/%#v", candidate, platform)
			}
			return inspectedValidationCandidate(t, layerDescriptor), nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if inspections != 1 || len(plan.Layers) != 1 || !reflect.DeepEqual(plan.Final, plan.Layers[0]) {
		t.Fatalf("validation plan = %#v, inspections=%d", plan, inspections)
	}
	if len(plan.Final.Profiles) != 1 || !reflect.DeepEqual(plan.Final.Outputs, fixture.request.EarlierCatalog) || plan.Final.Image.Image != layerImage {
		t.Fatalf("final validation input = %#v", plan.Final)
	}
}

func TestPrepareProviderGraphValidationInspectsBaseOnlyGraph(t *testing.T) {
	request := providerBaseResolvedRequest(t)
	plan, err := registry.Plan(providers.PlanInput{Components: request.Components, Platform: request.Platform})
	if err != nil {
		t.Fatal(err)
	}
	descriptor := providerBaseDescriptor(t, false)
	baseImage, err := realizedImageFromDescriptor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	policy := deploy.RuntimePolicyV1{
		Schema: deploy.RuntimePolicySchemaV1, AllowedRoots: []string{"/mnt"},
		ProtectedPaths: []deploy.ProtectedPathV1{}, Plans: []deploy.RuntimePlanV1{},
	}
	result, err := prepareProviderGraphValidation(
		context.Background(), descriptor, []providers.RealizedOutput{}, providers.GraphExecutionResult{
			Plan: plan, SelectedEdges: []providers.ProviderEdgeV1{}, Bundles: []providers.ResolvedBundle{},
			Profiles: []providers.RequirementProfile{}, ValidationEvidence: []providers.ValidationEvidence{},
			PrefixImages: []providers.RealizedImageV1{baseImage}, Materializations: []providers.GraphNodeMaterializeResult{},
			Catalog: []providers.RealizedOutput{},
		}, policy,
		func(_ context.Context, candidate BuiltImageCandidate, _ blueprint.Platform) (InspectedImageCandidate, error) {
			if candidate.ImageID != descriptor.ConfigDigest {
				t.Fatalf("base inspection candidate = %#v", candidate)
			}
			return inspectedValidationCandidate(t, descriptor), nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Layers) != 0 || result.Final.Image.Image != baseImage {
		t.Fatalf("base-only validation plan = %#v", result)
	}
}

func TestPrepareProviderGraphValidationRejectsChangedLayerAndCatalogDrift(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	bundle, err := providers.LoadResolvedBundleManifest(fixture.store, fixture.lock.Nodes[0].BundleManifest, pythonprovider.ValidateResolvedBundlePayloadV1)
	if err != nil {
		t.Fatal(err)
	}
	baseImage, err := realizedImageFromDescriptor(fixture.lock.Base)
	if err != nil {
		t.Fatal(err)
	}
	graph := providers.GraphExecutionResult{
		Plan: fixture.request.Plan, SelectedEdges: []providers.ProviderEdgeV1{},
		Bundles: []providers.ResolvedBundle{bundle}, Profiles: []providers.RequirementProfile{fixture.lock.Nodes[0].RequirementProfile},
		ValidationEvidence: []providers.ValidationEvidence{fixture.lock.Nodes[0].ValidationEvidence},
		PrefixImages:       []providers.RealizedImageV1{baseImage, fixture.lock.Nodes[0].Result},
		Materializations: []providers.GraphNodeMaterializeResult{{
			Image: fixture.lock.Nodes[0].Result, TransactionDigest: fixture.lock.Nodes[0].TransactionDigest,
			GeneratedExecutables: []providers.RealizedGeneratedExecutable{}, Outputs: []providers.RealizedOutput{},
		}},
		Catalog: []providers.RealizedOutput{},
	}
	_, err = prepareProviderGraphValidation(context.Background(), fixture.lock.Base, fixture.request.EarlierCatalog, graph, fixture.lock.RuntimePolicy, func(context.Context, BuiltImageCandidate, blueprint.Platform) (InspectedImageCandidate, error) {
		return InspectedImageCandidate{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "catalog") {
		t.Fatalf("catalog drift error = %v", err)
	}
	graph.Catalog = append([]providers.RealizedOutput{}, fixture.request.EarlierCatalog...)
	_, err = prepareProviderGraphValidation(context.Background(), fixture.lock.Base, fixture.request.EarlierCatalog, graph, fixture.lock.RuntimePolicy, func(context.Context, BuiltImageCandidate, blueprint.Platform) (InspectedImageCandidate, error) {
		changed := inspectedValidationCandidate(t, providerBaseDescriptor(t, false))
		return changed, nil
	})
	if err == nil || !strings.Contains(err.Error(), "changed after materialization") {
		t.Fatalf("changed layer error = %v", err)
	}
}

func inspectedValidationCandidate(t *testing.T, descriptor deploy.ImageDescriptor) InspectedImageCandidate {
	t.Helper()
	image, err := realizedImageFromDescriptor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	return InspectedImageCandidate{Descriptor: descriptor, Config: deploy.BaseConfig{}, Labels: map[string]string{}, Image: image}
}
