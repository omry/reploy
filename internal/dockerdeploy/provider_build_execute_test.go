package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestExecuteLockedProviderBuildV1ReturnsExactReuseWithoutBackendWork(t *testing.T) {
	input, _, current, _, _ := providerBuildPreparationFixture(t)
	lock := current.Lock
	execution := LockedProviderBuildExecutionInputV1{
		SourceWheels: []providerstore.ArtifactDescriptor{},
		Preparation: LockedProviderBuildPreparationV1{
			Operation: input.Operation, Store: input.Store,
			Current: &current, ReusableLock: &lock, Reused: true,
		},
	}
	fail := func() { t.Fatal("exact reuse reached build backend") }
	result, err := executeLockedProviderBuildV1(context.Background(), execution, providerBuildExecutionBackend{
		executeGraph: func(context.Context, PreparedPythonGraphExecutionInput) (providers.GraphExecutionResult, error) {
			fail()
			return providers.GraphExecutionResult{}, nil
		},
		prepareValidation: func(context.Context, deploy.ImageDescriptor, []providers.RealizedOutput, providers.GraphExecutionResult, deploy.RuntimePolicyV1) (ProviderGraphValidationPlan, error) {
			fail()
			return ProviderGraphValidationPlan{}, nil
		},
		complete: func(context.Context, *deploy.OperationLock, providerstore.Store, ProviderBuildCompletionInput) (ProviderBuildCompletionResult, error) {
			fail()
			return ProviderBuildCompletionResult{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Reused || !reflect.DeepEqual(result.State, current.State) || !reflect.DeepEqual(result.Lock, current.Lock) {
		t.Fatalf("result = %#v", result)
	}
	publicResult, err := ExecuteLockedProviderBuildV1(context.Background(), execution)
	if err != nil || !publicResult.Reused || !reflect.DeepEqual(publicResult.Lock, current.Lock) {
		t.Fatalf("public exact reuse result = %#v, error = %v", publicResult, err)
	}
}

func TestExecuteLockedProviderBuildV1OrdersGraphValidationAndCompletion(t *testing.T) {
	completionInput, operation, store := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	if len(completionInput.ResolvedRequest.Sources) != 1 || !reflect.DeepEqual(completionInput.ResolvedRequest.Sources, completionInput.Graph.SelectedSources) {
		t.Fatalf("completion fixture does not distinguish source candidates from selected sources")
	}
	candidateRequest := completionInput.ResolvedRequest
	unusedSource := candidateRequest.Sources[0]
	unusedSource.LogicalPackage = "unused-source"
	unusedSource.SourceManifestDigest = reuseTestDigest("a")
	unusedSource.ArtifactDigest = reuseTestDigest("b")
	candidateRequest.Sources = append(append([]providers.ResolvedSourceInput{}, candidateRequest.Sources...), unusedSource)
	selected := SelectedProviderBase{Plan: completionInput.Graph.Plan, Descriptor: completionInput.Base, Config: providerBuildExecutionBaseConfig()}
	prepared := PreparedProviderBase{
		Plan: selected.Plan, Descriptor: selected.Descriptor, Config: selected.Config,
		Image: completionInput.Graph.PrefixImages[0], Catalog: completionInput.BaseCatalog,
	}
	input := LockedProviderBuildExecutionInputV1{
		Preparation: LockedProviderBuildPreparationV1{
			Operation: operation, Store: store, Environment: completionInput.Environment,
			DeploymentDir: completionInput.DeploymentDir, DockerPlan: completionInput.DockerPlan,
			Loaded: LoadedBuildRequestV1{
				State: deploy.StateV1{Overlay: completionInput.Overlay}, Document: completionInput.Document,
				Request: candidateRequest,
			},
			SelectedBase: selected, PreparedBase: &prepared, FinalImageConfig: pythonConsumerTestImageConfig(),
		},
		SourceWheels:   []providerstore.ArtifactDescriptor{},
		ValidateLayers: true, RunValidation: completionInput.RunValidation,
	}
	wantState := deploy.StateV1{Schema: deploy.StateSchemaV1}
	wantLock := deploy.BuildLockV1{Schema: deploy.BuildLockSchemaV1}
	order := []string{}
	result, err := executeLockedProviderBuildV1(context.Background(), input, providerBuildExecutionBackend{
		executeGraph: func(_ context.Context, got PreparedPythonGraphExecutionInput) (providers.GraphExecutionResult, error) {
			order = append(order, "graph")
			if !reflect.DeepEqual(got.Plan, prepared.Plan) || !reflect.DeepEqual(got.Sources, candidateRequest.Sources) || got.CurrentLock != nil || got.RunOptions.Context == nil {
				t.Fatalf("graph input = %#v", got)
			}
			return completionInput.Graph, nil
		},
		prepareValidation: func(_ context.Context, base deploy.ImageDescriptor, catalog []providers.RealizedOutput, graph providers.GraphExecutionResult, policy deploy.RuntimePolicyV1) (ProviderGraphValidationPlan, error) {
			order = append(order, "validation")
			if !reflect.DeepEqual(base, completionInput.Base) || !reflect.DeepEqual(catalog, completionInput.BaseCatalog) || !reflect.DeepEqual(graph, completionInput.Graph) || !reflect.DeepEqual(policy, completionInput.Validation.Final.RuntimePolicy) {
				t.Fatal("validation input changed")
			}
			return completionInput.Validation, nil
		},
		complete: func(_ context.Context, gotOperation *deploy.OperationLock, gotStore providerstore.Store, got ProviderBuildCompletionInput) (ProviderBuildCompletionResult, error) {
			order = append(order, "complete")
			if gotOperation != operation || gotStore.Root() != store.Root() || !reflect.DeepEqual(got.ResolvedRequest, completionInput.ResolvedRequest) || !reflect.DeepEqual(got.Graph, completionInput.Graph) || !reflect.DeepEqual(got.Validation, completionInput.Validation) || !got.ValidateLayers || got.RunValidation == nil || got.RunOptions.Context == nil {
				t.Fatalf("completion input = %#v", got)
			}
			return ProviderBuildCompletionResult{State: wantState, Lock: wantLock}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Reused || !reflect.DeepEqual(result.State, wantState) || !reflect.DeepEqual(result.Lock, wantLock) || !reflect.DeepEqual(order, []string{"graph", "validation", "complete"}) {
		t.Fatalf("result/order = %#v/%#v", result, order)
	}
}

func TestExecuteLockedProviderBuildV1StopsAfterGraphFailure(t *testing.T) {
	completionInput, operation, store := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	want := errors.New("graph failed")
	selected := SelectedProviderBase{Plan: completionInput.Graph.Plan, Descriptor: completionInput.Base, Config: providerBuildExecutionBaseConfig()}
	prepared := PreparedProviderBase{Plan: selected.Plan, Descriptor: selected.Descriptor, Config: selected.Config}
	input := LockedProviderBuildExecutionInputV1{
		SourceWheels: []providerstore.ArtifactDescriptor{},
		RunValidation: func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
			return nil, nil, nil
		},
		Preparation: LockedProviderBuildPreparationV1{
			Operation: operation, Store: store, DockerPlan: completionInput.DockerPlan,
			Loaded:       LoadedBuildRequestV1{Document: completionInput.Document, Request: completionInput.ResolvedRequest},
			SelectedBase: selected, PreparedBase: &prepared, FinalImageConfig: pythonConsumerTestImageConfig(),
		},
	}
	_, err := executeLockedProviderBuildV1(context.Background(), input, providerBuildExecutionBackend{
		executeGraph: func(context.Context, PreparedPythonGraphExecutionInput) (providers.GraphExecutionResult, error) {
			return providers.GraphExecutionResult{}, want
		},
		prepareValidation: func(context.Context, deploy.ImageDescriptor, []providers.RealizedOutput, providers.GraphExecutionResult, deploy.RuntimePolicyV1) (ProviderGraphValidationPlan, error) {
			t.Fatal("failed graph prepared validation")
			return ProviderGraphValidationPlan{}, nil
		},
		complete: func(context.Context, *deploy.OperationLock, providerstore.Store, ProviderBuildCompletionInput) (ProviderBuildCompletionResult, error) {
			t.Fatal("failed graph completed build")
			return ProviderBuildCompletionResult{}, nil
		},
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
}

func TestExecuteLockedProviderBuildV1RequiresValidationRunnerBeforeGraph(t *testing.T) {
	completionInput, operation, store := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	selected := SelectedProviderBase{Plan: completionInput.Graph.Plan, Descriptor: completionInput.Base, Config: providerBuildExecutionBaseConfig()}
	prepared := PreparedProviderBase{Plan: selected.Plan, Descriptor: selected.Descriptor, Config: selected.Config}
	backend := providerBuildExecutionBackend{
		executeGraph: func(context.Context, PreparedPythonGraphExecutionInput) (providers.GraphExecutionResult, error) {
			t.Fatal("missing validation runner reached graph execution")
			return providers.GraphExecutionResult{}, nil
		},
		prepareValidation: func(context.Context, deploy.ImageDescriptor, []providers.RealizedOutput, providers.GraphExecutionResult, deploy.RuntimePolicyV1) (ProviderGraphValidationPlan, error) {
			return ProviderGraphValidationPlan{}, nil
		},
		complete: func(context.Context, *deploy.OperationLock, providerstore.Store, ProviderBuildCompletionInput) (ProviderBuildCompletionResult, error) {
			return ProviderBuildCompletionResult{}, nil
		},
	}
	_, err := executeLockedProviderBuildV1(context.Background(), LockedProviderBuildExecutionInputV1{
		Preparation: LockedProviderBuildPreparationV1{
			Operation: operation, Store: store, SelectedBase: selected, PreparedBase: &prepared,
			FinalImageConfig: pythonConsumerTestImageConfig(),
		},
		SourceWheels: []providerstore.ArtifactDescriptor{},
	}, backend)
	if err == nil || !strings.Contains(err.Error(), "validation runner") {
		t.Fatalf("error = %v", err)
	}
}

func TestExecuteLockedProviderBuildV1RejectsReleasedPreparationLock(t *testing.T) {
	input, _, _, _, _ := providerBuildPreparationFixture(t)
	if err := input.Operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	backend := providerBuildExecutionBackend{
		executeGraph: func(context.Context, PreparedPythonGraphExecutionInput) (providers.GraphExecutionResult, error) {
			t.Fatal("released lock reached graph execution")
			return providers.GraphExecutionResult{}, nil
		},
		prepareValidation: func(context.Context, deploy.ImageDescriptor, []providers.RealizedOutput, providers.GraphExecutionResult, deploy.RuntimePolicyV1) (ProviderGraphValidationPlan, error) {
			return ProviderGraphValidationPlan{}, nil
		},
		complete: func(context.Context, *deploy.OperationLock, providerstore.Store, ProviderBuildCompletionInput) (ProviderBuildCompletionResult, error) {
			return ProviderBuildCompletionResult{}, nil
		},
	}
	_, err := executeLockedProviderBuildV1(context.Background(), LockedProviderBuildExecutionInputV1{
		Preparation:  LockedProviderBuildPreparationV1{Operation: input.Operation},
		SourceWheels: []providerstore.ArtifactDescriptor{},
	}, backend)
	if err == nil || !strings.Contains(err.Error(), "not held") {
		t.Fatalf("error = %v", err)
	}
}

func providerBuildExecutionBaseConfig() deploy.BaseConfig {
	return deploy.BaseConfig{
		Schema: deploy.BaseConfigSchemaV1, Environment: []deploy.ConfigEnvironmentVariable{},
		Entrypoint: []string{}, Command: []string{}, OnBuild: []string{}, Volumes: []string{},
	}
}
