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
	Store          providerstore.Store
	Plan           providers.ProviderPlanV1
	BaseDescriptor deploy.ImageDescriptor
	BaseCatalog    []providers.RealizedOutput
	Sources        []providers.ResolvedSourceInput
	SourceWheels   []providerstore.ArtifactDescriptor
	LocalOverrides []PythonLocalOverrideV1
	PortablePython *PortableToolPythonFreshPlanV1
	// DesiredPortableToolPlan identifies the application-scoped portable
	// selections for this graph. A nil value means that the graph has no
	// current runtime portable-tool selection; an old lock must not supply one.
	DesiredPortableToolPlan *providers.PortableToolPlanV1
	SourceBuilder           *SourceBuilderCoordinatorV1
	CurrentLock             *deploy.BuildLockV1
	FinalImageConfig        providers.ImageConfigPolicy
	Progress                io.Writer
	BuildProgress           buildprogress.Reporter
	RunOptions              RunOptions
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
	lockedBindingComponents, err := portableToolPythonBindingComponentsV1(input.CurrentLock)
	if err != nil {
		return providers.GraphExecutionResult{}, err
	}
	// A current lock can contain a binding that the desired graph no longer
	// selects. Its cached provider bundle is not evidence for the new graph,
	// even when the provider node itself is unchanged.
	dropPortableToolPythonCachedResolutionsV1(
		input.Plan, lockedBindingComponents, reuse.CachedResolutions,
	)
	desiredBindingPlan := providers.PortableToolPlanV1{
		Schema: providers.PortableToolPlanSchemaV1,
		Tools:  []providers.PortableToolPlanEntryV1{},
	}
	desiredBindingCount := 0
	if input.DesiredPortableToolPlan != nil {
		var err error
		desiredBindingPlan, _, err = portableToolPythonSelectionPlanV1(*input.DesiredPortableToolPlan)
		if err != nil {
			return providers.GraphExecutionResult{}, fmt.Errorf("desired portable Python selection: %w", err)
		}
		desiredBindingCount = len(desiredBindingPlan.Tools)
	}
	var projection *pythonprovider.PortableToolPythonProjectionV1
	var lockedPortablePython *PortableToolPythonLockedPlanV1
	var freshSelectedPlan providers.PortableToolPlanV1
	if input.PortablePython != nil {
		if err := validatePortableToolPythonFreshPlanV1(input.PortablePython); err != nil {
			return providers.GraphExecutionResult{}, err
		}
		var err error
		freshSelectedPlan, err = input.PortablePython.sealed.selection.Plan()
		if err != nil {
			return providers.GraphExecutionResult{}, err
		}
		if input.DesiredPortableToolPlan != nil {
			freshBindingPlan, _, err := portableToolPythonSelectionPlanV1(freshSelectedPlan)
			if err != nil {
				return providers.GraphExecutionResult{}, fmt.Errorf("fresh portable Python selection: %w", err)
			}
			matches, err := portableToolPythonPlansMatchCurrentBuildV1(freshBindingPlan, desiredBindingPlan)
			if err != nil {
				return providers.GraphExecutionResult{}, fmt.Errorf("compare fresh portable Python selection: %w", err)
			}
			if !matches {
				return providers.GraphExecutionResult{}, fmt.Errorf("fresh portable Python selection does not match desired selection")
			}
		}
		derived, err := input.PortablePython.sealed.selection.Projection()
		if err != nil {
			return providers.GraphExecutionResult{}, err
		}
		projection = &derived
	} else if input.CurrentLock != nil && input.CurrentLock.PortableTools != nil {
		matches, err := portableToolPythonSelectionsMatchCurrentBuildV1(
			input.CurrentLock.PortableTools, input.DesiredPortableToolPlan,
		)
		if err != nil {
			return providers.GraphExecutionResult{}, err
		}
		if matches && len(lockedBindingComponents) != 0 {
			lockedPortablePython, err = buildPortableToolPythonLockedPlanV1(input.CurrentLock.PortableTools)
			if err != nil {
				return providers.GraphExecutionResult{}, err
			}
			if lockedPortablePython != nil {
				derived, err := lockedPortablePython.selection.Projection()
				if err != nil {
					return providers.GraphExecutionResult{}, err
				}
				projection = &derived
			}
		}
	}
	if input.PortablePython == nil && desiredBindingCount != 0 && lockedPortablePython == nil {
		return providers.GraphExecutionResult{}, fmt.Errorf("desired portable Python selection requires a fresh plan when locked selection does not match")
	}
	bindingsByComponent, err := portablePythonProjectionComponentsV1(input.Plan, projection)
	if err != nil {
		return providers.GraphExecutionResult{}, err
	}
	if projection != nil {
		selectedPlan := providers.PortableToolPlanV1{}
		if input.DesiredPortableToolPlan != nil {
			selectedPlan = *input.DesiredPortableToolPlan
		} else if input.PortablePython != nil {
			selectedPlan = freshSelectedPlan
		} else if lockedPortablePython != nil {
			selectedPlan = lockedPortablePython.lock.Plan.PortableToolPlan
		}
		if err := validatePortablePythonAliasSelectionClaimsV1(selectedPlan, bindingsByComponent); err != nil {
			return providers.GraphExecutionResult{}, fmt.Errorf("selected portable Python aliases: %w", err)
		}
	}
	for id, config := range reuse.NodeConfigs {
		config.LocalOverrides = append([]PythonLocalOverrideV1{}, input.LocalOverrides...)
		node, found := graphBackendNode(input.Plan, id)
		if found && len(node.Components) == 1 {
			config.PortableToolBindings = bindingsByComponent[node.Components[0]]
			if config.PortableToolBindings != nil {
				config.PortableToolFreshPlan = input.PortablePython
				config.PortableToolLockedPlan = lockedPortablePython
				// Selected bindings always repeat the provider-owned locked
				// descriptor and contract checks. A cached Python bundle is
				// not sufficient evidence for the selected binding.
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

func portableToolPythonBindingComponentsV1(
	current *deploy.BuildLockV1,
) (map[string]struct{}, error) {
	result := map[string]struct{}{}
	if current == nil || current.PortableTools == nil {
		return result, nil
	}
	if err := providers.ValidatePortableToolLockV1(*current.PortableTools); err != nil {
		return nil, fmt.Errorf("current portable Python lock: %w", err)
	}
	_, projection, err := pythonprovider.ProjectPortableToolPythonBindingsV1(
		current.PortableTools.Plan.PortableToolPlan, []providers.ResolvedComponentRequestV1{},
	)
	if err != nil {
		return nil, fmt.Errorf("current portable Python projection: %w", err)
	}
	for _, component := range projection.Components {
		if len(component.Bindings) != 0 {
			result[component.Component] = struct{}{}
		}
	}
	return result, nil
}

func dropPortableToolPythonCachedResolutionsV1(
	plan providers.ProviderPlanV1,
	components map[string]struct{},
	cached map[providers.NodeID]providers.ResolveResult,
) {
	if len(components) == 0 || len(cached) == 0 {
		return
	}
	for _, node := range plan.Nodes {
		if node.Provider != blueprint.ComponentTypePython || len(node.Components) != 1 {
			continue
		}
		if _, found := components[node.Components[0]]; found {
			delete(cached, node.ID)
		}
	}
}

// portableToolPythonSelectionsMatchCurrentBuildV1 compares only the
// application-scoped Python binding selections. Source-builder portable tools
// are planned and retained by their separate coordinator.
func portableToolPythonSelectionsMatchCurrentBuildV1(
	current *providers.PortableToolLockV1,
	desired *providers.PortableToolPlanV1,
) (bool, error) {
	currentPlan := providers.PortableToolPlanV1{
		Schema: providers.PortableToolPlanSchemaV1,
		Tools:  []providers.PortableToolPlanEntryV1{},
	}
	if current != nil {
		if err := providers.ValidatePortableToolLockV1(*current); err != nil {
			return false, fmt.Errorf("current portable Python lock: %w", err)
		}
		var err error
		currentPlan, _, err = portableToolPythonSelectionPlanV1(current.Plan.PortableToolPlan)
		if err != nil {
			return false, fmt.Errorf("current portable Python selection: %w", err)
		}
	}
	desiredPlan := providers.PortableToolPlanV1{
		Schema: providers.PortableToolPlanSchemaV1,
		Tools:  []providers.PortableToolPlanEntryV1{},
	}
	if desired != nil {
		var err error
		desiredPlan, _, err = portableToolPythonSelectionPlanV1(*desired)
		if err != nil {
			return false, fmt.Errorf("desired portable Python selection: %w", err)
		}
	}
	return portableToolPythonPlansMatchCurrentBuildV1(currentPlan, desiredPlan)
}

func portableToolPythonPlansMatchCurrentBuildV1(
	current providers.PortableToolPlanV1,
	desired providers.PortableToolPlanV1,
) (bool, error) {
	if len(current.Tools) == 0 || len(desired.Tools) == 0 {
		return len(current.Tools) == 0 && len(desired.Tools) == 0, nil
	}
	return portableToolSelectionsMatchCurrentBuildV1(current, desired)
}

func portableToolPythonSelectionPlanV1(
	plan providers.PortableToolPlanV1,
) (providers.PortableToolPlanV1, pythonprovider.PortableToolPythonProjectionV1, error) {
	selection, err := pythonprovider.NewPortableToolPythonSelectionV1(plan)
	if err != nil {
		return providers.PortableToolPlanV1{}, pythonprovider.PortableToolPythonProjectionV1{}, err
	}
	projection, err := selection.Projection()
	if err != nil {
		return providers.PortableToolPlanV1{}, pythonprovider.PortableToolPythonProjectionV1{}, err
	}
	selected := providers.PortableToolPlanV1{
		Schema: providers.PortableToolPlanSchemaV1,
		Tools:  []providers.PortableToolPlanEntryV1{},
	}
	selectedTools := map[string]struct{}{}
	for _, component := range projection.Components {
		for _, binding := range component.Bindings {
			selectedBinding, err := selection.Binding(component.Component, binding.Distribution)
			if err != nil {
				return providers.PortableToolPlanV1{}, pythonprovider.PortableToolPythonProjectionV1{}, err
			}
			entry, err := selectedBinding.PlanEntry()
			if err != nil {
				return providers.PortableToolPlanV1{}, pythonprovider.PortableToolPythonProjectionV1{}, err
			}
			toolKey := portableToolCompletionPlanKeyV1(entry.Scope, entry.Provenance.Tool)
			if _, found := selectedTools[toolKey]; found {
				continue
			}
			selectedTools[toolKey] = struct{}{}
			selected.Tools = append(selected.Tools, entry)
		}
	}
	sort.Slice(selected.Tools, func(left, right int) bool {
		return portableToolCompletionPlanKeyV1(
			selected.Tools[left].Scope, selected.Tools[left].Provenance.Tool,
		) < portableToolCompletionPlanKeyV1(
			selected.Tools[right].Scope, selected.Tools[right].Provenance.Tool,
		)
	})
	return selected, projection, nil
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
