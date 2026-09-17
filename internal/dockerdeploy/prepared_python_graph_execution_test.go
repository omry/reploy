package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/buildprogress"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/portabletool"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

func TestWriteProviderNodeProgressUsesApplicationName(t *testing.T) {
	plan := providers.ProviderPlanV1{Nodes: []providers.NodeSpec{{
		ID:         "python/application/application",
		Provider:   blueprint.ComponentTypePython,
		Components: []string{"application/application/python"},
	}}}
	var progress strings.Builder
	writeProviderNodeProgress(&progress, "resolving", plan, "python/application/application")
	writeProviderNodeProgress(&progress, "building", plan, "python/application/application")

	got := progress.String()
	for _, want := range []string{
		"resolving Python packages",
		"building Python layer",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("progress missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "application/application/python") {
		t.Fatalf("progress exposed contribution ID:\n%s", got)
	}
	if strings.Contains(got, "app: application") {
		t.Fatalf("single-app progress exposed redundant app ownership:\n%s", got)
	}
}

func TestProviderNodeProgressDisambiguatesMultipleApplications(t *testing.T) {
	plan := providers.ProviderPlanV1{Nodes: []providers.NodeSpec{
		{
			ID:         "python/application/alpha",
			Provider:   blueprint.ComponentTypePython,
			Components: []string{blueprint.ApplicationContributionID("alpha", blueprint.ContributionProviderPython)},
		},
		{
			ID:         "python/application/beta",
			Provider:   blueprint.ComponentTypePython,
			Components: []string{blueprint.ApplicationContributionID("beta", blueprint.ContributionProviderPython)},
		},
	}}
	got := providerNodeProgressDescription("building", plan, plan.Nodes[0].ID)
	if got != "building Python layer (app: alpha)" {
		t.Fatalf("multi-app progress = %q", got)
	}
}

func TestProviderGraphProgressCountsResolveAndMaterializeOperations(t *testing.T) {
	plan := providers.ProviderPlanV1{Nodes: []providers.NodeSpec{
		{ID: "base", Provider: blueprint.ComponentTypeBase},
		{
			ID:         "python/application/application",
			Provider:   blueprint.ComponentTypePython,
			Components: []string{"application/application/python"},
		},
	}}
	var progress strings.Builder
	var events []buildprogress.Event
	prepare, materialize := providerGraphProgressCallbacks(
		plan,
		&progress,
		func(event buildprogress.Event) { events = append(events, event) },
		func(context.Context, providers.GraphNodePrepareRequest) (providers.GraphNodePreparation, error) {
			return providers.GraphNodePreparation{}, nil
		},
		func(context.Context, providers.GraphNodeMaterializeRequest) (providers.GraphNodeMaterializeResult, error) {
			return providers.GraphNodeMaterializeResult{}, nil
		},
	)
	node := plan.Nodes[1]
	if _, err := prepare(t.Context(), providers.GraphNodePrepareRequest{
		Resolve: providers.ResolveNodeRequest{Plan: plan, NodeID: node.ID},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := materialize(t.Context(), providers.GraphNodeMaterializeRequest{Node: node}); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); !strings.Contains(got, "resolving Python packages") ||
		!strings.Contains(got, "building Python layer") ||
		strings.Contains(got, "app: application") {
		t.Fatalf("provider progress = %q", got)
	}
	if len(events) != 4 {
		t.Fatalf("provider events = %#v", events)
	}
	wantCompleted := []int{0, 1, 1, 2}
	for index, event := range events {
		if event.Phase != buildprogress.PhaseProviders ||
			event.Completed != wantCompleted[index] || event.Total != 2 {
			t.Fatalf("provider event %d = %#v", index, event)
		}
	}
}

func TestExecutePreparedPythonGraphDerivesAllReuseFromCurrentLock(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	descriptor := fixture.lock.Base
	localOverrides := []PythonLocalOverrideV1{{Distribution: "demo-server", HostDir: "/tmp/demo-server"}}
	node, found := graphBackendNode(fixture.request.Plan, fixture.request.NodeID)
	if !found || len(node.Components) != 1 {
		t.Fatal("Python fixture node is missing")
	}
	component := node.Components[0]
	portablePython := portableToolPythonFreshPlaywrightFixtureV1(t, "application:application")
	previousPrepare := preparePythonGraphExecutionBackend
	previousExecute := executePreparedPythonProviderGraph
	t.Cleanup(func() {
		preparePythonGraphExecutionBackend = previousPrepare
		executePreparedPythonProviderGraph = previousExecute
	})
	var configs map[providers.NodeID]PreparedPythonNodeConfig
	cleaned := false
	preparePythonGraphExecutionBackend = func(
		_ context.Context,
		_ providerstore.Store,
		_ providers.ProviderPlanV1,
		_ deploy.ImageDescriptor,
		_ providers.ImageConfigPolicy,
		value map[providers.NodeID]PreparedPythonNodeConfig,
		_ map[providers.NodeID]PreparedAPTNodeConfig,
		_ RunOptions,
	) (PreparedPythonGraphBackend, func() error, error) {
		configs = value
		return PreparedPythonGraphBackend{}, func() error { cleaned = true; return nil }, nil
	}
	var execution providers.GraphExecutionRequest
	executePreparedPythonProviderGraph = func(_ context.Context, request providers.GraphExecutionRequest) (providers.GraphExecutionResult, error) {
		execution = request
		return providers.GraphExecutionResult{Plan: request.Plan}, nil
	}
	result, err := ExecutePreparedPythonGraph(context.Background(), PreparedPythonGraphExecutionInput{
		Store: fixture.store, Plan: fixture.request.Plan, BaseDescriptor: descriptor,
		BaseCatalog: fixture.request.EarlierCatalog, Sources: fixture.request.SourceCandidates, SourceWheels: fixture.sourceWheels, CurrentLock: &fixture.lock,
		LocalOverrides:   localOverrides,
		PortablePython:   &portablePython,
		FinalImageConfig: pythonConsumerTestImageConfig(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Plan, fixture.request.Plan) || !cleaned {
		t.Fatalf("result = %#v, cleaned = %v", result, cleaned)
	}
	if len(configs[fixture.request.NodeID].ReusableWheels) != 2 || len(execution.ReusableArtifacts[fixture.request.NodeID]) != 2 {
		t.Fatalf("configs = %#v, reusable = %#v", configs, execution.ReusableArtifacts)
	}
	if !reflect.DeepEqual(configs[fixture.request.NodeID].LocalOverrides, localOverrides) {
		t.Fatalf("local overrides = %#v", configs[fixture.request.NodeID].LocalOverrides)
	}
	bindings := configs[fixture.request.NodeID].PortableToolBindings
	if bindings == nil || bindings.Component != component ||
		!reflect.DeepEqual(bindings.TestedTags, []string{"py3-none-manylinux1_x86_64"}) {
		t.Fatalf("portable Python node bindings = %#v", bindings)
	}
	if configs[fixture.request.NodeID].PortableToolFreshPlan != &portablePython {
		t.Fatalf("portable Python fresh plan was not retained in node config")
	}
	if _, found := execution.CachedResolutions[fixture.request.NodeID]; found {
		t.Fatalf("unauthenticated cached binding resolution was retained: %#v", execution.CachedResolutions)
	}
	wantBase, err := realizedImageFromDescriptor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if execution.BaseImage != wantBase || execution.Validators == nil || execution.PrepareNode == nil || execution.MaterializeNode == nil {
		t.Fatalf("execution request = %#v", execution)
	}
}

func portablePythonExecutionProjectionForTest(component string) pythonprovider.PortableToolPythonProjectionV1 {
	closureDigest := rendererDigest("a")
	contractDigest := rendererDigest("b")
	artifactDigest := rendererDigest("c")
	tag := "py3-none-any"
	return pythonprovider.PortableToolPythonProjectionV1{
		Schema: pythonprovider.PortableToolPythonProjectionSchemaV1,
		Components: []pythonprovider.PortableToolPythonComponentV1{{
			Component: component, TestedTags: []string{tag},
			Bindings: []pythonprovider.PortableToolPythonBindingV1{{
				Scope: "application:application", Component: component,
				SelectedClosureDigest: closureDigest, Distribution: "demo-server",
				Contract: providers.PortableToolRecordReferenceV1{
					ID: "tool:demo/releases/1.0.0/bindings/python/contract", Digest: contractDigest,
				},
				Artifact: providers.PortableToolRecordReferenceV1{
					ID: "tool:demo/releases/1.0.0/bindings/python/artifacts/linux-amd64", Digest: artifactDigest,
				},
				Requirements: []string{"demo-server==1.0"}, SupportedPython: []string{"3.13"}, SupportedTags: []string{tag},
				CLI: portabletool.ToolExportV1{Name: "demo", Path: "/opt/demo/bin/demo"},
				Wheel: pythonprovider.PortableToolExactWheelConstraintV1{
					Platform: "linux/amd64", Filename: "demo_server-1.0-py3-none-any.whl",
					Distribution: "demo-server", EcosystemVersion: "1.0", Tags: []string{tag},
					Size: "1", SHA256: artifactDigest, RequiresPython: ">=3.13,<3.14",
				},
			}},
		}},
	}
}

func TestExecutePreparedPythonGraphRetainsScratchAfterExecutionFailure(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	previousPrepare := preparePythonGraphExecutionBackend
	previousExecute := executePreparedPythonProviderGraph
	t.Cleanup(func() {
		preparePythonGraphExecutionBackend = previousPrepare
		executePreparedPythonProviderGraph = previousExecute
	})
	cleaned := false
	preparePythonGraphExecutionBackend = func(
		context.Context,
		providerstore.Store,
		providers.ProviderPlanV1,
		deploy.ImageDescriptor,
		providers.ImageConfigPolicy,
		map[providers.NodeID]PreparedPythonNodeConfig,
		map[providers.NodeID]PreparedAPTNodeConfig,
		RunOptions,
	) (PreparedPythonGraphBackend, func() error, error) {
		return PreparedPythonGraphBackend{}, func() error { cleaned = true; return nil }, nil
	}
	executePreparedPythonProviderGraph = func(context.Context, providers.GraphExecutionRequest) (providers.GraphExecutionResult, error) {
		return providers.GraphExecutionResult{}, markProviderHelperCleanupError(errors.New("helper container removal failed"))
	}

	_, err := ExecutePreparedPythonGraph(context.Background(), PreparedPythonGraphExecutionInput{
		Store: fixture.store, Plan: fixture.request.Plan, BaseDescriptor: fixture.lock.Base,
		BaseCatalog: fixture.request.EarlierCatalog, Sources: fixture.request.SourceCandidates,
		SourceWheels: fixture.sourceWheels, CurrentLock: &fixture.lock,
		FinalImageConfig: pythonConsumerTestImageConfig(),
	})
	if err == nil {
		t.Fatal("execution succeeded")
	}
	if cleaned {
		t.Fatal("failed execution removed scratch before abandoned-container recovery")
	}
}
