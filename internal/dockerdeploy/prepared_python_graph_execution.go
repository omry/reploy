package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

// PreparedPythonGraphExecutionInput contains the complete temporary Python
// graph path until APT joins the same registry-backed executor.
type PreparedPythonGraphExecutionInput struct {
	Store             providerstore.Store
	Plan              providers.ProviderPlanV1
	BaseDescriptor    deploy.ImageDescriptor
	BaseCatalog       []providers.RealizedOutput
	Sources           []providers.ResolvedSourceInput
	SourceWheels      []providerstore.ArtifactDescriptor
	PriorSources      []providers.ResolvedSourceInput
	PriorSourceWheels []providerstore.ArtifactDescriptor
	LocalOverrides    []PythonLocalOverrideV1
	CurrentLock       *deploy.BuildLockV1
	FinalImageConfig  providers.ImageConfigPolicy
	Progress          io.Writer
	RunOptions        RunOptions
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
		config.LocalOverrides = append([]PythonLocalOverrideV1{}, input.LocalOverrides...)
		node, found := graphBackendNode(input.Plan, id)
		if found && len(node.Components) == 1 {
			for _, source := range input.PriorSources {
				if source.Component == node.Components[0] {
					config.PriorSources = append(config.PriorSources, source)
				}
			}
		}
		config.PriorSourceWheels = append(
			[]providerstore.ArtifactDescriptor{}, input.PriorSourceWheels...,
		)
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
	prepareNode := backend.PrepareNode
	materializeNode := backend.MaterializeNode
	if input.Progress != nil {
		prepareNode = func(ctx context.Context, request providers.GraphNodePrepareRequest) (providers.GraphNodePreparation, error) {
			writeProviderNodeProgress(input.Progress, "resolving", request.Resolve.Plan, request.Resolve.NodeID)
			return backend.PrepareNode(ctx, request)
		}
		materializeNode = func(ctx context.Context, request providers.GraphNodeMaterializeRequest) (providers.GraphNodeMaterializeResult, error) {
			writeProviderNodeProgress(input.Progress, "building", input.Plan, request.Node.ID)
			return backend.MaterializeNode(ctx, request)
		}
	}
	return executePreparedPythonProviderGraph(ctx, providers.GraphExecutionRequest{
		Plan: input.Plan, Platform: input.BaseDescriptor.Platform,
		SourceCandidates: append([]providers.ResolvedSourceInput{}, input.Sources...),
		BaseImage:        baseImage, BaseCatalog: append([]providers.RealizedOutput{}, input.BaseCatalog...),
		ReusableArtifacts: reuse.ReusableArtifacts, CachedResolutions: reuse.CachedResolutions,
		Validators:  registry.OwnerValidatorsForNode,
		PrepareNode: prepareNode, MaterializeNode: materializeNode,
	})
}

func writeProviderNodeProgress(output io.Writer, action string, plan providers.ProviderPlanV1, id providers.NodeID) {
	node, found := graphBackendNode(plan, id)
	if !found {
		return
	}
	components := append([]string{}, node.Components...)
	sort.Strings(components)
	componentLabel := "component " + strings.Join(components, ", ")
	if len(components) != 1 {
		componentLabel = "components " + strings.Join(components, ", ")
	}
	provider := string(node.Provider)
	switch node.Provider {
	case blueprint.ComponentTypeAPT:
		provider = "APT"
	case blueprint.ComponentTypePython:
		provider = "Python"
	}
	if action == "building" {
		writeProviderBuildProgress(output, "building %s layer for %s", provider, componentLabel)
		return
	}
	writeProviderBuildProgress(output, "resolving %s packages for %s", provider, componentLabel)
}
