package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/buildprofile"
	"github.com/omry/reploy/internal/buildprogress"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
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
	LocalOverrides   []PythonLocalOverrideV1
	PortablePython   *PortableToolPythonFreshPlanV1
	SourceBuilder    *SourceBuilderCoordinatorV1
	CurrentLock      *deploy.BuildLockV1
	FinalImageConfig providers.ImageConfigPolicy
	Progress         io.Writer
	BuildProgress    buildprogress.Reporter
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
	dropSourceBuilderPythonCachedResolutionsV1(input.Plan, input.CurrentLock, reuse.CachedResolutions)
	var projection *pythonprovider.PortableToolPythonProjectionV1
	if input.PortablePython != nil {
		if err := validatePortableToolPythonFreshPlanV1(input.PortablePython); err != nil {
			return providers.GraphExecutionResult{}, err
		}
		projection = &input.PortablePython.Projection
	}
	bindingsByComponent, err := portablePythonProjectionComponentsV1(input.Plan, projection)
	if err != nil {
		return providers.GraphExecutionResult{}, err
	}
	for id, config := range reuse.NodeConfigs {
		config.LocalOverrides = append([]PythonLocalOverrideV1{}, input.LocalOverrides...)
		node, found := graphBackendNode(input.Plan, id)
		if found && len(node.Components) == 1 {
			config.PortableToolBindings = bindingsByComponent[node.Components[0]]
			if config.PortableToolBindings != nil {
				config.PortableToolFreshPlan = input.PortablePython
				// PTD-23.3.3 owns authenticated locked replay. Until that
				// path is supplied, a selected binding always takes the fresh
				// orchestration path instead of accepting a cached resolution.
				delete(reuse.CachedResolutions, id)
			}
		}
		config.SourceBuilder = input.SourceBuilder
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
		if providerHelperCleanupFailed(err) {
			return
		}
		if cleanupErr := cleanup(); cleanupErr != nil {
			result = providers.GraphExecutionResult{}
			err = errors.Join(err, cleanupErr)
		}
	}()
	prepareNode, materializeNode := providerGraphProgressCallbacks(
		input.Plan, input.Progress, input.BuildProgress,
		backend.PrepareNode, backend.MaterializeNode,
	)
	return executePreparedPythonProviderGraph(ctx, providers.GraphExecutionRequest{
		Plan: input.Plan, Platform: input.BaseDescriptor.Platform,
		SourceCandidates: append([]providers.ResolvedSourceInput{}, input.Sources...),
		BaseImage:        baseImage, BaseCatalog: append([]providers.RealizedOutput{}, input.BaseCatalog...),
		ReusableArtifacts: reuse.ReusableArtifacts, CachedResolutions: reuse.CachedResolutions,
		Validators:  registry.OwnerValidatorsForNode,
		PrepareNode: prepareNode, MaterializeNode: materializeNode,
	})
}

func portablePythonProjectionComponentsV1(
	plan providers.ProviderPlanV1,
	projection *pythonprovider.PortableToolPythonProjectionV1,
) (map[string]*pythonprovider.PortableToolPythonComponentV1, error) {
	result := map[string]*pythonprovider.PortableToolPythonComponentV1{}
	if projection == nil {
		return result, nil
	}
	if _, err := pythonprovider.CanonicalPortableToolPythonProjectionBytesV1(*projection); err != nil {
		return nil, err
	}
	pythonComponents := map[string]struct{}{}
	for _, node := range plan.Nodes {
		if node.Provider == blueprint.ComponentTypePython && len(node.Components) == 1 {
			pythonComponents[node.Components[0]] = struct{}{}
		}
	}
	for _, component := range projection.Components {
		if _, found := pythonComponents[component.Component]; !found {
			return nil, fmt.Errorf("portable Python projection component %q has no planned Python node", component.Component)
		}
		clone := component
		clone.TestedTags = append([]string{}, component.TestedTags...)
		clone.Bindings = append([]pythonprovider.PortableToolPythonBindingV1{}, component.Bindings...)
		for index := range clone.Bindings {
			clone.Bindings[index].Requirements = append([]string{}, component.Bindings[index].Requirements...)
			clone.Bindings[index].SupportedPython = append([]string{}, component.Bindings[index].SupportedPython...)
			clone.Bindings[index].SupportedTags = append([]string{}, component.Bindings[index].SupportedTags...)
			clone.Bindings[index].Wheel.Tags = append([]string{}, component.Bindings[index].Wheel.Tags...)
		}
		result[component.Component] = &clone
	}
	return result, nil
}

// A source-builder lock proves which portable tools were selected for the
// previous build, but the coordinator that prepares those tools is driven by
// fresh Python resolution. Preserve reusable wheel candidates while forcing
// each Python node through that resolution path again.
func dropSourceBuilderPythonCachedResolutionsV1(
	plan providers.ProviderPlanV1,
	current *deploy.BuildLockV1,
	cached map[providers.NodeID]providers.ResolveResult,
) {
	if current == nil || !portableToolPlanHasSourceBuilderScopesV1(current.PortableTools) {
		return
	}
	for _, node := range plan.Nodes {
		if node.Provider == blueprint.ComponentTypePython {
			delete(cached, node.ID)
		}
	}
}

func providerGraphProgressCallbacks(
	plan providers.ProviderPlanV1,
	progress io.Writer,
	report buildprogress.Reporter,
	prepareNode providers.GraphNodePreparer,
	materializeNode providers.GraphNodeMaterializer,
) (providers.GraphNodePreparer, providers.GraphNodeMaterializer) {
	totalOperations := 0
	for _, node := range plan.Nodes {
		if node.ID != "base" {
			totalOperations += 2
		}
	}
	completedOperations := 0
	originalPrepareNode := prepareNode
	originalMaterializeNode := materializeNode
	prepareNode = func(ctx context.Context, request providers.GraphNodePrepareRequest) (providers.GraphNodePreparation, error) {
		detail := providerNodeProgressDescription("resolving", request.Resolve.Plan, request.Resolve.NodeID)
		writeProviderBuildProgress(progress, "%s", detail)
		buildprogress.Report(report, buildprogress.Event{
			Phase: buildprogress.PhaseProviders, Detail: detail,
			Completed: completedOperations, Total: totalOperations,
		})
		profileCtx, end := buildprofile.Start(ctx, providerProfileLabel(detail))
		result, err := originalPrepareNode(profileCtx, request)
		end(err)
		if err == nil {
			completedOperations++
			buildprogress.Report(report, buildprogress.Event{
				Phase: buildprogress.PhaseProviders, Detail: detail,
				Completed: completedOperations, Total: totalOperations,
			})
		}
		return result, err
	}
	materializeNode = func(ctx context.Context, request providers.GraphNodeMaterializeRequest) (providers.GraphNodeMaterializeResult, error) {
		detail := providerNodeProgressDescription("building", plan, request.Node.ID)
		writeProviderBuildProgress(progress, "%s", detail)
		buildprogress.Report(report, buildprogress.Event{
			Phase: buildprogress.PhaseProviders, Detail: detail,
			Completed: completedOperations, Total: totalOperations,
		})
		profileCtx, end := buildprofile.Start(ctx, providerProfileLabel(detail))
		result, err := originalMaterializeNode(profileCtx, request)
		end(err)
		if err == nil {
			completedOperations++
			buildprogress.Report(report, buildprogress.Event{
				Phase: buildprogress.PhaseProviders, Detail: detail,
				Completed: completedOperations, Total: totalOperations,
			})
		}
		return result, err
	}
	return prepareNode, materializeNode
}

func providerProfileLabel(detail string) string {
	if detail == "" {
		return "Provider operation"
	}
	return strings.ToUpper(detail[:1]) + detail[1:]
}

func writeProviderNodeProgress(output io.Writer, action string, plan providers.ProviderPlanV1, id providers.NodeID) {
	detail := providerNodeProgressDescription(action, plan, id)
	if detail != "" {
		writeProviderBuildProgress(output, "%s", detail)
	}
}

func providerNodeProgressDescription(action string, plan providers.ProviderPlanV1, id providers.NodeID) string {
	node, found := graphBackendNode(plan, id)
	if !found {
		return ""
	}
	showApplicationContext := providerPlanApplicationCount(plan) > 1
	components := make([]string, 0, len(node.Components))
	for _, component := range node.Components {
		context := providerProgressComponentContext(node.Provider, component, showApplicationContext)
		if context != "" {
			components = append(components, context)
		}
	}
	sort.Strings(components)
	contextSuffix := providerProgressContextSuffix(components)
	provider := providerDisplayName(node.Provider)
	if action == "building" {
		return fmt.Sprintf("building %s layer%s", provider, contextSuffix)
	}
	return fmt.Sprintf("resolving %s packages%s", provider, contextSuffix)
}

func providerDisplayName(provider blueprint.ComponentType) string {
	switch provider {
	case blueprint.ComponentTypeAPT:
		return "APT"
	case blueprint.ComponentTypePython:
		return "Python"
	default:
		return string(provider)
	}
}

func providerPlanApplicationCount(plan providers.ProviderPlanV1) int {
	applications := map[string]struct{}{}
	for _, node := range plan.Nodes {
		for _, component := range node.Components {
			provider := ""
			switch node.Provider {
			case blueprint.ComponentTypeAPT:
				provider = blueprint.ContributionProviderOS
			case blueprint.ComponentTypePython:
				provider = blueprint.ContributionProviderPython
			}
			if application, ok := blueprint.ApplicationContributionOwner(component, provider); ok {
				applications[application] = struct{}{}
			}
		}
	}
	return len(applications)
}

func providerProgressComponentContext(
	provider blueprint.ComponentType,
	component string,
	showApplication bool,
) string {
	switch provider {
	case blueprint.ComponentTypeAPT:
		if application, ok := blueprint.ApplicationContributionOwner(
			component,
			blueprint.ContributionProviderOS,
		); ok {
			if !showApplication {
				return ""
			}
			return "app: " + application
		}
		if component == blueprint.EnvironmentContributionID(blueprint.ContributionProviderOS) {
			return "environment"
		}
	case blueprint.ComponentTypePython:
		if application, ok := blueprint.ApplicationContributionOwner(
			component,
			blueprint.ContributionProviderPython,
		); ok {
			if !showApplication {
				return ""
			}
			return "app: " + application
		}
	}
	return "component: " + component
}

func providerProgressContextSuffix(contexts []string) string {
	nonempty := make([]string, 0, len(contexts))
	for _, context := range contexts {
		if context != "" {
			nonempty = append(nonempty, context)
		}
	}
	if len(nonempty) == 0 {
		return ""
	}
	return " (" + strings.Join(nonempty, ", ") + ")"
}
