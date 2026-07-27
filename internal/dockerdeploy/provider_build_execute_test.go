package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestExecuteLockedProviderBuildV1ReturnsExactReuseWithoutBackendWork(t *testing.T) {
	input, _, current, _, _ := providerBuildPreparationFixture(t)
	lock := current.Lock
	var progress strings.Builder
	execution := LockedProviderBuildExecutionInputV1{
		SourceWheels: []providerstore.ArtifactDescriptor{}, LocalOverrides: []PythonLocalOverrideV1{},
		Progress: &progress,
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
		prepareValidation: func(context.Context, deploy.ImageDescriptor, []providers.RealizedOutput, providers.GraphExecutionResult, deploy.RuntimePolicyV1, bool) (ProviderGraphValidationPlan, error) {
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
	if got := progress.String(); got != "reusing current validated image\n" {
		t.Fatalf("progress = %q", got)
	}
	publicResult, err := ExecuteLockedProviderBuildV1(context.Background(), execution)
	if err != nil || !publicResult.Reused || !reflect.DeepEqual(publicResult.Lock, current.Lock) {
		t.Fatalf("public exact reuse result = %#v, error = %v", publicResult, err)
	}
}

func TestExecuteLockedProviderBuildV1ValidatesAnExactCurrentBuildWithoutReplacingIt(t *testing.T) {
	input, _, current, _, _ := providerBuildPreparationFixture(t)
	lock := current.Lock
	published := 0
	var progress strings.Builder
	result, err := executeLockedProviderBuildV1(t.Context(), LockedProviderBuildExecutionInputV1{
		SourceWheels: []providerstore.ArtifactDescriptor{}, LocalOverrides: []PythonLocalOverrideV1{},
		ValidateChoices: true, Progress: &progress,
		Preparation: LockedProviderBuildPreparationV1{
			Operation: input.Operation, Store: input.Store, Environment: "demo", DeploymentDir: input.DeploymentDir,
			Current: &current, ReusableLock: &lock, Reused: true,
		},
	}, providerBuildExecutionBackend{
		publishValidated: func(_ context.Context, operation *deploy.OperationLock, store providerstore.Store, environment, dir string, got deploy.BuildLockV1, _ ValidatedBuildInputsV1) (deploy.ValidatedBuildV1, error) {
			published++
			if operation != input.Operation || store.Root() != input.Store.Root() || environment != "demo" || dir != input.DeploymentDir || !reflect.DeepEqual(got, lock) {
				t.Fatal("validated publication input changed")
			}
			return deploy.ValidatedBuildV1{PendingCleanup: []deploy.ValidatedBuildReferenceV1{{
				ImageReference: "superseded",
			}}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if published != 1 || !result.Validated || !result.Reused || !reflect.DeepEqual(result.State, current.State) {
		t.Fatalf("published=%d result=%#v", published, result)
	}
	if !strings.Contains(progress.String(), "cleanup of 1 superseded cached image reference is pending") {
		t.Fatalf("progress = %q", progress.String())
	}
}

func TestExecuteLockedProviderBuildV1PromotesAnExactValidatedBuild(t *testing.T) {
	input, _, current, _, _ := providerBuildPreparationFixture(t)
	lock := current.Lock
	record := deploy.ValidatedBuildV1{ImageReference: current.Generation.Reference}
	candidate := ValidatedBuildCandidateV1{Record: record, Current: current}
	wantState := current.State
	document, err := blueprint.DecodeResolvedDocumentV1(current.State.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	order := []string{}
	result, err := executeLockedProviderBuildV1(t.Context(), LockedProviderBuildExecutionInputV1{
		SourceWheels: []providerstore.ArtifactDescriptor{}, LocalOverrides: []PythonLocalOverrideV1{},
		Preparation: LockedProviderBuildPreparationV1{
			Operation: input.Operation, Store: input.Store, Environment: "demo", DeploymentDir: input.DeploymentDir,
			ReusableLock: &lock, Reused: true, ReusedCandidate: true, ValidatedCandidate: &candidate,
			Loaded: LoadedBuildRequestV1{Document: document},
		},
	}, providerBuildExecutionBackend{
		verifyReference: func(context.Context, providers.RealizedImageV1, string, string, string) error {
			order = append(order, "verify")
			return nil
		},
		publishBuild: func(context.Context, *deploy.OperationLock, providerstore.Store, BuildPublicationInput) (deploy.StateV1, error) {
			order = append(order, "publish")
			return wantState, nil
		},
		discardValidated: func(context.Context, *deploy.OperationLock, string, string) error {
			order = append(order, "discard")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Reused || result.Validated || !reflect.DeepEqual(result.State, wantState) ||
		!reflect.DeepEqual(order, []string{"verify", "publish", "discard"}) {
		t.Fatalf("result/order = %#v/%#v", result, order)
	}
}

func TestExecuteLockedProviderBuildV1PromotionDefersCleanupWithoutFailingPublishedBuild(t *testing.T) {
	input, _, current, _, _ := providerBuildPreparationFixture(t)
	lock := current.Lock
	candidate := ValidatedBuildCandidateV1{
		Record:  deploy.ValidatedBuildV1{ImageReference: current.Generation.Reference},
		Current: current,
	}
	document, err := blueprint.DecodeResolvedDocumentV1(current.State.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	cleanupErr := errors.New("Docker reference is busy")
	var progress strings.Builder
	result, err := executeLockedProviderBuildV1(t.Context(), LockedProviderBuildExecutionInputV1{
		SourceWheels: []providerstore.ArtifactDescriptor{}, LocalOverrides: []PythonLocalOverrideV1{},
		Progress: &progress,
		Preparation: LockedProviderBuildPreparationV1{
			Operation: input.Operation, Store: input.Store, Environment: "demo", DeploymentDir: input.DeploymentDir,
			ReusableLock: &lock, Reused: true, ReusedCandidate: true, ValidatedCandidate: &candidate,
			Loaded: LoadedBuildRequestV1{Document: document},
		},
	}, providerBuildExecutionBackend{
		verifyReference: func(context.Context, providers.RealizedImageV1, string, string, string) error {
			return nil
		},
		publishBuild: func(context.Context, *deploy.OperationLock, providerstore.Store, BuildPublicationInput) (deploy.StateV1, error) {
			return current.State, nil
		},
		discardValidated: func(context.Context, *deploy.OperationLock, string, string) error {
			return cleanupErr
		},
	})
	if err != nil {
		t.Fatalf("published build reported failure: %v", err)
	}
	if !result.Reused || !reflect.DeepEqual(result.State, current.State) {
		t.Fatalf("result = %#v", result)
	}
	if got := progress.String(); !strings.Contains(got, "cleanup of superseded cached image references is pending") ||
		!strings.Contains(got, cleanupErr.Error()) {
		t.Fatalf("progress = %q", got)
	}
}

func TestExecuteLockedProviderBuildV1RetriesPendingCleanupWhenRevalidatingCandidate(t *testing.T) {
	input, _, current, _, _ := providerBuildPreparationFixture(t)
	lock := current.Lock
	record := deploy.ValidatedBuildV1{
		ImageReference: current.Generation.Reference,
		PendingCleanup: []deploy.ValidatedBuildReferenceV1{{ImageReference: "superseded"}},
	}
	candidate := ValidatedBuildCandidateV1{Record: record, Current: current}
	var progress strings.Builder
	order := []string{}
	result, err := executeLockedProviderBuildV1(t.Context(), LockedProviderBuildExecutionInputV1{
		SourceWheels: []providerstore.ArtifactDescriptor{}, LocalOverrides: []PythonLocalOverrideV1{},
		ValidateChoices: true, Progress: &progress,
		Preparation: LockedProviderBuildPreparationV1{
			Operation: input.Operation, Store: input.Store, Environment: "demo", DeploymentDir: input.DeploymentDir,
			ReusableLock: &lock, Reused: true, ReusedCandidate: true, ValidatedCandidate: &candidate,
		},
	}, providerBuildExecutionBackend{
		retryValidatedCleanup: func(context.Context, *deploy.OperationLock, string, string) (deploy.ValidatedBuildV1, bool, error) {
			order = append(order, "retry-cleanup")
			return record, true, nil
		},
		verifyReference: func(context.Context, providers.RealizedImageV1, string, string, string) error {
			order = append(order, "verify")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Validated || !result.Reused ||
		!reflect.DeepEqual(order, []string{"retry-cleanup", "verify"}) {
		t.Fatalf("result/order = %#v/%#v", result, order)
	}
	if !strings.Contains(progress.String(), "cleanup of 1 superseded cached image reference is pending") {
		t.Fatalf("progress = %q", progress.String())
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
	unusedSource.SourceInputDigest = reuseTestDigest("a")
	unusedSource.OutputArtifactDigest = reuseTestDigest("b")
	candidateRequest.Sources = append(append([]providers.ResolvedSourceInput{}, candidateRequest.Sources...), unusedSource)
	selected := SelectedProviderBase{Plan: completionInput.Graph.Plan, Descriptor: completionInput.Base, Config: providerBuildExecutionBaseConfig()}
	prepared := PreparedProviderBase{
		Plan: selected.Plan, Descriptor: selected.Descriptor, Config: selected.Config,
		Image: completionInput.Graph.PrefixImages[0], Catalog: completionInput.BaseCatalog,
	}
	localOverrides := []PythonLocalOverrideV1{{Distribution: "demo-server", HostDir: "/tmp/demo-server"}}
	input := LockedProviderBuildExecutionInputV1{
		Preparation: LockedProviderBuildPreparationV1{
			Operation: operation, Store: store, Environment: completionInput.Environment,
			DeploymentDir: completionInput.DeploymentDir, DockerPlan: completionInput.DockerPlan,
			Loaded: LoadedBuildRequestV1{
				State: deploy.StateV1{Overlay: completionInput.Overlay}, Document: completionInput.Document,
				PackageOverrides: completionInput.PackageOverrides, Request: candidateRequest,
			},
			SelectedBase: selected, PreparedBase: &prepared, FinalImageConfig: pythonConsumerTestImageConfig(),
			NoCache: true,
		},
		SourceWheels:   []providerstore.ArtifactDescriptor{},
		LocalOverrides: localOverrides,
		ValidateLayers: true, RunValidation: completionInput.RunValidation,
	}
	wantState := deploy.StateV1{Schema: deploy.StateSchemaV1}
	wantLock := deploy.BuildLockV1{Schema: deploy.BuildLockSchemaV1}
	order := []string{}
	result, err := executeLockedProviderBuildV1(context.Background(), input, providerBuildExecutionBackend{
		executeGraph: func(_ context.Context, got PreparedPythonGraphExecutionInput) (providers.GraphExecutionResult, error) {
			order = append(order, "graph")
			if !reflect.DeepEqual(got.Plan, prepared.Plan) || !reflect.DeepEqual(got.Sources, candidateRequest.Sources) ||
				!reflect.DeepEqual(got.LocalOverrides, localOverrides) || got.CurrentLock != nil || got.RunOptions.Context == nil {
				t.Fatalf("graph input = %#v", got)
			}
			return completionInput.Graph, nil
		},
		prepareValidation: func(_ context.Context, base deploy.ImageDescriptor, catalog []providers.RealizedOutput, graph providers.GraphExecutionResult, policy deploy.RuntimePolicyV1, validateLayers bool) (ProviderGraphValidationPlan, error) {
			order = append(order, "validation")
			if !reflect.DeepEqual(base, completionInput.Base) || !reflect.DeepEqual(catalog, completionInput.BaseCatalog) || !reflect.DeepEqual(graph, completionInput.Graph) || !reflect.DeepEqual(policy, completionInput.Validation.Final.RuntimePolicy) || !validateLayers {
				t.Fatal("validation input changed")
			}
			return completionInput.Validation, nil
		},
		complete: func(_ context.Context, gotOperation *deploy.OperationLock, gotStore providerstore.Store, got ProviderBuildCompletionInput) (ProviderBuildCompletionResult, error) {
			order = append(order, "complete")
			if gotOperation != operation || gotStore.Root() != store.Root() || !reflect.DeepEqual(got.ResolvedRequest, completionInput.ResolvedRequest) || !reflect.DeepEqual(got.Graph, completionInput.Graph) || !reflect.DeepEqual(got.Validation, completionInput.Validation) || !got.ValidateLayers || !got.NoCache || got.RunValidation == nil || got.RunOptions.Context == nil {
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
		SourceWheels:   []providerstore.ArtifactDescriptor{},
		LocalOverrides: []PythonLocalOverrideV1{},
		RunValidation: func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
			return nil, nil, nil
		},
		Preparation: LockedProviderBuildPreparationV1{
			Operation: operation, Store: store, DockerPlan: completionInput.DockerPlan,
			Loaded: LoadedBuildRequestV1{
				Document: completionInput.Document, PackageOverrides: completionInput.PackageOverrides,
				Request: completionInput.ResolvedRequest,
			},
			SelectedBase: selected, PreparedBase: &prepared, FinalImageConfig: pythonConsumerTestImageConfig(),
		},
	}
	_, err := executeLockedProviderBuildV1(context.Background(), input, providerBuildExecutionBackend{
		executeGraph: func(context.Context, PreparedPythonGraphExecutionInput) (providers.GraphExecutionResult, error) {
			return providers.GraphExecutionResult{}, want
		},
		prepareValidation: func(context.Context, deploy.ImageDescriptor, []providers.RealizedOutput, providers.GraphExecutionResult, deploy.RuntimePolicyV1, bool) (ProviderGraphValidationPlan, error) {
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
		prepareValidation: func(context.Context, deploy.ImageDescriptor, []providers.RealizedOutput, providers.GraphExecutionResult, deploy.RuntimePolicyV1, bool) (ProviderGraphValidationPlan, error) {
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
		SourceWheels:   []providerstore.ArtifactDescriptor{},
		LocalOverrides: []PythonLocalOverrideV1{},
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
		prepareValidation: func(context.Context, deploy.ImageDescriptor, []providers.RealizedOutput, providers.GraphExecutionResult, deploy.RuntimePolicyV1, bool) (ProviderGraphValidationPlan, error) {
			return ProviderGraphValidationPlan{}, nil
		},
		complete: func(context.Context, *deploy.OperationLock, providerstore.Store, ProviderBuildCompletionInput) (ProviderBuildCompletionResult, error) {
			return ProviderBuildCompletionResult{}, nil
		},
	}
	_, err := executeLockedProviderBuildV1(context.Background(), LockedProviderBuildExecutionInputV1{
		Preparation:  LockedProviderBuildPreparationV1{Operation: input.Operation},
		SourceWheels: []providerstore.ArtifactDescriptor{}, LocalOverrides: []PythonLocalOverrideV1{},
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
