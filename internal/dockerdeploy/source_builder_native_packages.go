package dockerdeploy

import (
	"context"
	"errors"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type sourceBuilderNativePackagesV1 struct {
	Image     InspectedImageCandidate
	Graph     providers.GraphExecutionResult
	candidate BuiltImageCandidate
}

var prepareSourceBuilderNativePackagesV1 = prepareSourceBuilderNativePackages

// Native builder requirements use the ordinary APT graph and acceptance
// pipeline, with fresh resolution on the exact current prefix. Its accepted
// layer remains a disposable builder artifact rather than a runtime supplier.
func prepareSourceBuilderNativePackages(
	ctx context.Context, store providerstore.Store, tools *SourceBuilderPortableToolsV1,
	upstream deploy.ImageDescriptor, options RunOptions,
) (result *sourceBuilderNativePackagesV1, resultErr error) {
	components, err := aptprovider.ProjectSourceBuilderPortableToolAPTRootsV1(tools.Lock.Plan.PortableToolPlan)
	if err != nil {
		return nil, err
	}
	if len(components) == 0 {
		return nil, nil
	}
	base, err := providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{
		Image: string(upstream.ConfigDigest), Exports: map[string]blueprint.BaseExecutableExport{},
	})
	if err != nil {
		return nil, err
	}
	components = append(components, providers.ResolvedComponentRequestV1{Component: "base", Provider: blueprint.ComponentTypeBase, Request: base})
	plan, err := registry.Plan(providers.PlanInput{Components: components, Platform: upstream.Platform})
	if err != nil {
		return nil, err
	}
	image, err := realizedImageFromDescriptor(upstream)
	if err != nil {
		return nil, err
	}
	inspected, err := inspectSourceBuilderLayerV1(ctx, BuiltImageCandidate{ImageID: upstream.ConfigDigest}, upstream.Platform)
	if err != nil {
		return nil, err
	}
	if inspected.Image.ConfigDigest != image.ConfigDigest || inspected.Image.RootFSSubject != image.RootFSSubject {
		return nil, fmt.Errorf("native builder prefix changed before APT preparation")
	}
	config, err := ProviderFinalImageConfigV1(inspected.Config)
	if err != nil {
		return nil, err
	}
	backend, cleanup, err := PreparePreparedPythonGraphBackend(ctx, store, plan, upstream, config,
		map[providers.NodeID]PreparedPythonNodeConfig{}, map[providers.NodeID]PreparedAPTNodeConfig{"apt": {}}, options)
	if err != nil {
		return nil, err
	}
	defer func() {
		if providerHelperCleanupFailed(resultErr) {
			return
		}
		if err := cleanup(); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
		if resultErr != nil && result != nil {
			resultErr = errors.Join(resultErr, removeSourceBuilderLayerV1(context.WithoutCancel(ctx), result.candidate))
			result = nil
		}
	}()
	accepted := &sourceBuilderNativePackagesV1{}
	backend.Materializer.RetainLayer = func(_ context.Context, candidate BuiltImageCandidate, _ providers.RealizedImageV1) error {
		accepted.candidate = candidate
		return nil
	}
	graph, err := providers.ExecuteProviderGraph(ctx, providers.GraphExecutionRequest{
		Plan: plan, Platform: upstream.Platform, BaseImage: image,
		BaseCatalog: []providers.RealizedOutput{}, SourceCandidates: []providers.ResolvedSourceInput{},
		ReusableArtifacts: map[providers.NodeID][]providerstore.StoreObjectRef{}, CachedResolutions: map[providers.NodeID]providers.ResolveResult{},
		Validators: registry.OwnerValidatorsForNode,
		PrepareNode: func(ctx context.Context, request providers.GraphNodePrepareRequest) (providers.GraphNodePreparation, error) {
			if request.Resolve.NodeID != "apt" {
				return providers.GraphNodePreparation{}, fmt.Errorf("native builder graph cannot prepare node %q", request.Resolve.NodeID)
			}
			return backend.APTOperations["apt"].Prepare(ctx, upstream, backend.Workspace, request)
		},
		MaterializeNode: backend.Materializer.Materialize,
	})
	if err != nil {
		// A graph failure after acceptance still owns this temporary layer.
		if accepted.candidate.ImageID != "" {
			err = errors.Join(err, removeSourceBuilderLayerV1(context.WithoutCancel(ctx), accepted.candidate))
		}
		return nil, err
	}
	accepted.Graph = graph
	result = accepted
	accepted.Image, err = inspectSourceBuilderLayerV1(ctx, accepted.candidate, upstream.Platform)
	if err != nil {
		return result, err
	}
	if len(graph.Materializations) != 1 || accepted.Image.Image != graph.PrefixImages[len(graph.PrefixImages)-1] {
		return result, fmt.Errorf("native builder APT image differs from its accepted graph")
	}
	return result, nil
}
