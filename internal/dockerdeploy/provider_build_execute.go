package dockerdeploy

import (
	"context"
	"fmt"
	"reflect"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

type LockedProviderBuildExecutionInputV1 struct {
	Preparation      LockedProviderBuildPreparationV1
	SourceWheels     []providerstore.ArtifactDescriptor
	WorkspaceSources []PythonWorkspaceSource
	ValidateLayers   bool
	RunValidation    FullImageValidationRunner
	RunOptions       RunOptions
}

type LockedProviderBuildExecutionResultV1 struct {
	State  deploy.StateV1
	Lock   deploy.BuildLockV1
	Reused bool
}

type providerBuildExecutionBackend struct {
	executeGraph      func(context.Context, PreparedPythonGraphExecutionInput) (providers.GraphExecutionResult, error)
	prepareValidation func(
		context.Context,
		deploy.ImageDescriptor,
		[]providers.RealizedOutput,
		providers.GraphExecutionResult,
		deploy.RuntimePolicyV1,
	) (ProviderGraphValidationPlan, error)
	complete func(
		context.Context,
		*deploy.OperationLock,
		providerstore.Store,
		ProviderBuildCompletionInput,
	) (ProviderBuildCompletionResult, error)
}

// ExecuteLockedProviderBuildV1 consumes a preparation made under the same held
// operation lock. It either returns the exact reused generation without backend
// work or executes the provider graph, plans complete validation, and publishes
// the resulting generation. Source observation and lock acquisition belong to
// the caller.
func ExecuteLockedProviderBuildV1(
	ctx context.Context,
	input LockedProviderBuildExecutionInputV1,
) (LockedProviderBuildExecutionResultV1, error) {
	if input.RunValidation == nil {
		runner := ProviderFullImageValidationRunner{Store: input.Preparation.Store}
		input.RunValidation = runner.Run
	}
	return executeLockedProviderBuildV1(ctx, input, providerBuildExecutionBackend{
		executeGraph:      ExecutePreparedPythonGraph,
		prepareValidation: PrepareProviderGraphValidation,
		complete:          CompleteProviderBuild,
	})
}

func executeLockedProviderBuildV1(
	ctx context.Context,
	input LockedProviderBuildExecutionInputV1,
	backend providerBuildExecutionBackend,
) (LockedProviderBuildExecutionResultV1, error) {
	if ctx == nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("execute locked provider build requires a context")
	}
	if err := ctx.Err(); err != nil {
		return LockedProviderBuildExecutionResultV1{}, err
	}
	preparation := input.Preparation
	if err := preparation.Operation.RequireHeld(); err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("execute locked provider build: %w", err)
	}
	if input.SourceWheels == nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("execute locked provider build source wheels must use an array")
	}
	if input.WorkspaceSources == nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("execute locked provider build workspace sources must use an array")
	}
	if backend.executeGraph == nil || backend.prepareValidation == nil || backend.complete == nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("execute locked provider build requires a complete backend")
	}

	if preparation.Reused {
		if preparation.Current == nil || preparation.PreparedBase != nil || preparation.ReusableLock == nil {
			return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("reused provider build preparation is incomplete")
		}
		if !reflect.DeepEqual(*preparation.ReusableLock, preparation.Current.Lock) {
			return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("reused provider build lock does not match the current generation")
		}
		return LockedProviderBuildExecutionResultV1{
			State: preparation.Current.State, Lock: preparation.Current.Lock, Reused: true,
		}, nil
	}
	if preparation.PreparedBase == nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("provider build preparation has no realized base")
	}
	if input.RunValidation == nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("provider build requires a final image validation runner")
	}
	preparedBase := *preparation.PreparedBase
	if !reflect.DeepEqual(preparedBase.Plan, preparation.SelectedBase.Plan) ||
		!reflect.DeepEqual(preparedBase.Descriptor, preparation.SelectedBase.Descriptor) ||
		!reflect.DeepEqual(preparedBase.Config, preparation.SelectedBase.Config) {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("provider build realized base does not match its selection")
	}
	if err := providers.ValidateImageConfigPolicy(preparation.FinalImageConfig); err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("provider build final image config: %w", err)
	}

	options := input.RunOptions
	options.Context = ctx
	graph, err := backend.executeGraph(ctx, PreparedPythonGraphExecutionInput{
		Store: preparation.Store, Plan: preparedBase.Plan, BaseDescriptor: preparedBase.Descriptor,
		BaseCatalog: preparedBase.Catalog, Sources: preparation.Loaded.Request.Sources,
		SourceWheels:     append([]providerstore.ArtifactDescriptor{}, input.SourceWheels...),
		WorkspaceSources: append([]PythonWorkspaceSource{}, input.WorkspaceSources...),
		CurrentLock:      preparation.ReusableLock, FinalImageConfig: preparation.FinalImageConfig,
		RunOptions: options,
	})
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("execute provider graph: %w", err)
	}
	resolvedRequest, err := finalizeResolvedRequestV1(
		preparation.Loaded.Document, preparation.Loaded.State.Overlay, preparation.Loaded.Request,
		append([]providers.ResolvedSourceInput{}, graph.SelectedSources...),
	)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("finalize provider request: %w", err)
	}
	plans, err := RuntimePlansV1(preparation.Loaded.Document, preparation.DockerPlan)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, err
	}
	policy, err := CompileRuntimePolicyV1(preparation.Loaded.Document, graph, plans)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, err
	}
	validation, err := backend.prepareValidation(
		ctx, preparedBase.Descriptor, preparedBase.Catalog, graph, policy,
	)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("prepare provider build validation: %w", err)
	}
	completed, err := backend.complete(ctx, preparation.Operation, preparation.Store, ProviderBuildCompletionInput{
		Environment: preparation.Environment, DeploymentDir: preparation.DeploymentDir,
		Document: preparation.Loaded.Document, DockerPlan: preparation.DockerPlan,
		ResolvedRequest: resolvedRequest, Overlay: preparation.Loaded.State.Overlay,
		Base: preparedBase.Descriptor, BaseCatalog: preparedBase.Catalog,
		Graph: graph, Validation: validation, ValidateLayers: input.ValidateLayers,
		RunValidation: input.RunValidation, RunOptions: options,
	})
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, err
	}
	return LockedProviderBuildExecutionResultV1{State: completed.State, Lock: completed.Lock}, nil
}
