package dockerdeploy

import (
	"context"
	"errors"
	"fmt"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

// PreparedPythonGraphExecutionInput contains the complete temporary Python
// graph path until APT joins the same registry-backed executor.
type PreparedPythonGraphExecutionInput struct {
	Store            providerstore.Store
	Plan             providers.ProviderPlanV1
	BaseDescriptor   deploy.ImageDescriptor
	BaseCatalog      []providers.RealizedOutput
	Sources          []providers.ResolvedSourceInput
	SourceWheels     []providerstore.ArtifactDescriptor
	WorkspaceSources []PythonWorkspaceSource
	CurrentLock      *deploy.BuildLockV1
	FinalImageConfig providers.ImageConfigPolicy
	RunOptions       RunOptions
}

var preparePythonGraphExecutionBackend = PreparePreparedPythonGraphBackend
var executePreparedPythonProviderGraph = providers.ExecuteProviderGraph

// ExecutePreparedPythonGraph derives all cache inputs from CurrentLock,
// prepares the matching backend workspaces, and executes the graph. Callers do
// not supply reusable-artifact, cached-resolution, or per-node config maps.
func ExecutePreparedPythonGraph(
	ctx context.Context,
	input PreparedPythonGraphExecutionInput,
) (result providers.GraphExecutionResult, err error) {
	if ctx == nil {
		return providers.GraphExecutionResult{}, fmt.Errorf("execute prepared Python graph requires a context")
	}
	if err := ctx.Err(); err != nil {
		return providers.GraphExecutionResult{}, err
	}
	baseImage, err := realizedImageFromDescriptor(input.BaseDescriptor)
	if err != nil {
		return providers.GraphExecutionResult{}, fmt.Errorf("execute prepared Python graph base: %w", err)
	}
	reuse, err := LoadPreparedPythonGraphReuse(
		input.Store, input.Plan, input.BaseDescriptor.Platform, input.Sources, input.SourceWheels, input.CurrentLock,
	)
	if err != nil {
		return providers.GraphExecutionResult{}, err
	}
	for id, config := range reuse.NodeConfigs {
		config.WorkspaceSources = append([]PythonWorkspaceSource{}, input.WorkspaceSources...)
		reuse.NodeConfigs[id] = config
	}
	backend, cleanup, err := preparePythonGraphExecutionBackend(
		ctx, input.Store, input.Plan, input.BaseDescriptor, input.FinalImageConfig, reuse.NodeConfigs,
		reuse.APTNodeConfigs, input.RunOptions,
	)
	if err != nil {
		return providers.GraphExecutionResult{}, err
	}
	defer func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			result = providers.GraphExecutionResult{}
			err = errors.Join(err, cleanupErr)
		}
	}()
	return executePreparedPythonProviderGraph(ctx, providers.GraphExecutionRequest{
		Plan: input.Plan, Platform: input.BaseDescriptor.Platform,
		SourceCandidates: append([]providers.ResolvedSourceInput{}, input.Sources...),
		BaseImage:        baseImage, BaseCatalog: append([]providers.RealizedOutput{}, input.BaseCatalog...),
		ReusableArtifacts: reuse.ReusableArtifacts, CachedResolutions: reuse.CachedResolutions,
		Validators:  registry.OwnerValidatorsForNode,
		PrepareNode: backend.PrepareNode, MaterializeNode: backend.MaterializeNode,
	})
}
