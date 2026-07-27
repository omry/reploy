package dockerdeploy

import (
	"context"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestExecutePreparedPythonGraphDerivesAllReuseFromCurrentLock(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	descriptor := fixture.lock.Base
	workspaceSources := sourceWheelTestWorkspaceSources(t, "demo-server")
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
		WorkspaceSources: workspaceSources,
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
	if !reflect.DeepEqual(configs[fixture.request.NodeID].WorkspaceSources, workspaceSources) {
		t.Fatalf("workspace sources = %#v", configs[fixture.request.NodeID].WorkspaceSources)
	}
	if _, found := execution.CachedResolutions[fixture.request.NodeID]; !found {
		t.Fatalf("cached resolutions = %#v", execution.CachedResolutions)
	}
	wantBase, err := realizedImageFromDescriptor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if execution.BaseImage != wantBase || execution.Validators == nil || execution.PrepareNode == nil || execution.MaterializeNode == nil {
		t.Fatalf("execution request = %#v", execution)
	}
}
