package dockerdeploy

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
	"github.com/omry/reploy/internal/toolrequest"
)

// ApplicationPortableToolPlanV1 is selection and provider preflight only. The
// ordinary request boundary continues to reject tools until materialization
// owns the complete selected plan.
type ApplicationPortableToolPlanV1 struct {
	Target              toolcatalog.TargetIdentityV1
	Plan                providers.PortableToolPlanV1
	DAG                 providers.PortableToolProviderDAGV1
	Closures            []toolcatalog.SelectedClosureV1
	Snapshot            toolcatalog.ImmutableOperationSnapshotV1
	ProjectedComponents []providers.ResolvedComponentRequestV1
	PythonProjection    pythonprovider.PortableToolPythonProjectionV1
	sealed              *applicationPortablePythonSelectionSealV1
}

type PlanApplicationPortableToolsInputV1 struct {
	Document         blueprint.Document
	Components       []providers.ResolvedComponentRequestV1
	Platform         blueprint.Platform
	Store            providerstore.Store
	Base             deploy.ImageDescriptor
	FinalImageConfig providers.ImageConfigPolicy
	ReployVersion    string
}

var observeApplicationPortableTargetV1 = ObserveSourceBuilderTargetIdentityV1

// PlanApplicationPortableToolsV1 selects exactly the resolved application's
// runtime requirements. Ordinary component requests are projected into the
// same immutable selection operation, and the existing provider projections
// and DAG reject conflicts before any portable artifact is acquired.
func PlanApplicationPortableToolsV1(ctx context.Context, input PlanApplicationPortableToolsInputV1) (*ApplicationPortableToolPlanV1, error) {
	if ctx == nil {
		return nil, fmt.Errorf("plan application portable tools requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	groups, scopes, err := applicationPortableRequirementGroupsV1(input.Document)
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, nil
	}
	if input.Components == nil {
		return nil, fmt.Errorf("application portable tool components must use an array")
	}
	ordinary, err := registry.Plan(providers.PlanInput{Components: input.Components, Platform: input.Platform})
	if err != nil {
		return nil, fmt.Errorf("plan ordinary providers for application portable tools: %w", err)
	}
	if err := input.Base.Validate(); err != nil {
		return nil, fmt.Errorf("application portable tool base: %w", err)
	}
	if input.Base.Platform != input.Platform {
		return nil, fmt.Errorf("application portable tool base platform does not match ordinary provider request")
	}
	if err := providers.ValidateImageConfigPolicy(input.FinalImageConfig); err != nil {
		return nil, fmt.Errorf("application portable tool final image config: %w", err)
	}
	digest, err := blueprint.DocumentDigestV1(input.Document)
	if err != nil {
		return nil, fmt.Errorf("application portable tool document identity: %w", err)
	}
	capabilities, err := toolcatalog.EmbeddedClientCapabilitiesV1(input.ReployVersion)
	if err != nil {
		return nil, err
	}
	target, err := observeApplicationPortableTargetV1(ctx, input.Store, input.Base)
	if err != nil {
		return nil, fmt.Errorf("observe application portable tool target: %w", err)
	}
	active, err := applicationOrdinaryProviderConstraintsV1(scopes, input.Components, ordinary, input.FinalImageConfig)
	if err != nil {
		return nil, err
	}
	solverDomains := make([]toolcatalog.ProviderDomainSetV1, 0, len(scopes))
	activeBindingsByScope := applicationActiveBindingsByScopeV1(scopes, input.Components)
	for _, scope := range scopes {
		application := strings.TrimPrefix(scope, "application:")
		solverDomains = append(solverDomains, toolcatalog.ProviderDomainSetV1{
			Scope: scope, PackageManager: "provider/apt/package-manager",
			Binding:    "provider/python/" + application + "/binding",
			Filesystem: "final-image/filesystem", Environment: "final-image/environment",
			Exports: "final-image/exports", Capabilities: "final-image/capabilities",
		})
	}
	operation := toolcatalog.ResolutionOperationInputsV1{
		Blueprint:       canonical.Envelope{Schema: sourceBuilderBlueprintEnvelopeV1, Value: canonical.Object{"digest": string(digest)}},
		Reploy:          canonical.Envelope{Schema: sourceBuilderClientEnvelopeV1, Value: canonical.Object{"version": capabilities.ReployVersion}},
		ActiveProviders: active,
	}
	plan, resolution, err := toolcatalog.ResolveEmbeddedPortableToolPlanByScopeV1(groups, target, capabilities, activeBindingsByScope, solverDomains, operation)
	if err != nil {
		return nil, fmt.Errorf("resolve application portable tools: %w", err)
	}
	withAPT, err := aptprovider.ProjectPortableToolAPTRootsV1(plan, input.Components)
	if err != nil {
		return nil, fmt.Errorf("preflight application portable APT contributions: %w", err)
	}
	projected, pythonProjection, err := pythonprovider.ProjectPortableToolPythonBindingsV1(plan, withAPT)
	if err != nil {
		return nil, fmt.Errorf("preflight application portable Python contributions: %w", err)
	}
	if err := preflightApplicationOrdinaryPythonInterpretersV1(input.Components, pythonProjection); err != nil {
		return nil, fmt.Errorf("preflight application portable Python interpreter: %w", err)
	}
	providerPlan, err := registry.Plan(providers.PlanInput{Components: projected, Platform: input.Platform})
	if err != nil {
		return nil, fmt.Errorf("plan projected application providers: %w", err)
	}
	domains, err := applicationPortableProviderDomainsV1(plan, providerPlan)
	if err != nil {
		return nil, err
	}
	dag, err := providers.BuildPortableToolProviderDAGV1(providerPlan, plan, domains)
	if err != nil {
		return nil, fmt.Errorf("preflight application portable provider DAG: %w", err)
	}
	selected := &ApplicationPortableToolPlanV1{
		Target: target, Plan: plan, DAG: dag, Closures: resolution.Closures,
		Snapshot: resolution.Snapshot, ProjectedComponents: projected, PythonProjection: pythonProjection,
	}
	selected.sealed, err = sealApplicationPortablePythonSelectionV1(selected, input.Base, input.FinalImageConfig)
	if err != nil {
		return nil, fmt.Errorf("seal application portable tool selection: %w", err)
	}
	return selected, nil
}

// A Python provider request constrains the interpreter independently of its
// package roots. The package projection checks roots, but a selected binding
// must also have an interpreter version that can satisfy the ordinary request
// and its exact wheel's Requires-Python claim before acquisition begins.
func preflightApplicationOrdinaryPythonInterpretersV1(
	ordinary []providers.ResolvedComponentRequestV1,
	projection pythonprovider.PortableToolPythonProjectionV1,
) error {
	byComponent := make(map[string]providers.CanonicalProviderRequest, len(ordinary))
	for _, component := range ordinary {
		if component.Provider == blueprint.ComponentTypePython {
			byComponent[component.Component] = component.Request
		}
	}
	for _, component := range projection.Components {
		request, found := byComponent[component.Component]
		if !found {
			continue
		}
		interpreter, ok := ordinaryCanonicalObjectV1(request.Value["interpreter"])
		if !ok {
			return fmt.Errorf("ordinary Python component %q has no canonical interpreter", component.Component)
		}
		version, _ := interpreter["version"].(string)
		if version == "" {
			continue
		}
		for _, binding := range component.Bindings {
			constraint := version
			if binding.Wheel.RequiresPython != "" {
				constraint += "," + binding.Wheel.RequiresPython
			}
			compatible := false
			for _, claim := range binding.SupportedPython {
				intersects, err := pythonprovider.PythonRequiresPythonIntersectsClaimV1(constraint, claim)
				if err != nil {
					return fmt.Errorf("ordinary Python component %q interpreter %q and binding %q: %w", component.Component, version, binding.Distribution, err)
				}
				compatible = compatible || intersects
			}
			if !compatible {
				return fmt.Errorf("ordinary Python component %q interpreter %q conflicts with portable binding %q", component.Component, version, binding.Distribution)
			}
		}
	}
	return nil
}

func applicationPortableRequirementGroupsV1(document blueprint.Document) ([]toolrequest.CanonicalRequirementGroupV1, []string, error) {
	names := make([]string, 0, len(document.Environment.Applications))
	for name := range document.Environment.Applications {
		names = append(names, name)
	}
	sort.Strings(names)
	groups := []toolrequest.CanonicalRequirementGroupV1{}
	scopes := []string{}
	for _, name := range names {
		scope := "application:" + name
		selected := document.Environment.Applications[name].Packages.Tools
		if len(selected) == 0 {
			continue
		}
		for _, group := range selected {
			if group.Scope != scope || group.Context != "runtime" {
				return nil, nil, fmt.Errorf("application %q carries non-runtime or foreign portable tool requirement %q", name, group.Tool)
			}
			groups = append(groups, group)
		}
		scopes = append(scopes, scope)
	}
	return groups, scopes, nil
}

func applicationActiveBindingsByScopeV1(scopes []string, components []providers.ResolvedComponentRequestV1) map[string][]string {
	active := make(map[string][]string, len(scopes))
	for _, scope := range scopes {
		application := strings.TrimPrefix(scope, "application:")
		bindings := []string{}
		for _, component := range components {
			if !applicationOrdinaryComponentAppliesV1(application, component.Component) {
				continue
			}
			switch component.Provider {
			case blueprint.ComponentTypePython:
				bindings = append(bindings, blueprint.ContributionProviderPython)
			}
		}
		sort.Strings(bindings)
		unique := bindings[:0]
		for _, binding := range bindings {
			if len(unique) == 0 || unique[len(unique)-1] != binding {
				unique = append(unique, binding)
			}
		}
		active[scope] = unique
	}
	return active
}

func applicationPortableProviderDomainsV1(plan providers.PortableToolPlanV1, providerPlan providers.ProviderPlanV1) ([]providers.PortableToolProviderDomainSetV1, error) {
	nodeForComponent := map[string]providers.NodeID{}
	for _, node := range providerPlan.Nodes {
		for _, component := range node.Components {
			nodeForComponent[component] = node.ID
		}
	}
	seen := map[string]bool{}
	domains := []providers.PortableToolProviderDomainSetV1{}
	for _, tool := range plan.Tools {
		if seen[tool.Scope] {
			continue
		}
		seen[tool.Scope] = true
		name, ok := strings.CutPrefix(tool.Scope, "application:")
		if !ok || name == "" {
			return nil, fmt.Errorf("portable tool scope %q is not application-owned", tool.Scope)
		}
		aptOwner := providers.NodeID("base")
		for _, node := range providerPlan.Nodes {
			if node.Provider == blueprint.ComponentTypeAPT {
				aptOwner = node.ID
				break
			}
		}
		pythonOwner := nodeForComponent[blueprint.ApplicationContributionID(name, blueprint.ContributionProviderPython)]
		if pythonOwner == "" {
			pythonOwner = "base"
		}
		finalImageAuthority := func(family string) providers.PortableToolDomainAuthorityV1 {
			return providers.PortableToolDomainAuthorityV1{ID: "final-image/" + family, Owner: "base"}
		}
		domains = append(domains, providers.PortableToolProviderDomainSetV1{
			Scope:          tool.Scope,
			PackageManager: providers.PortableToolDomainAuthorityV1{ID: "provider/apt/package-manager", Owner: aptOwner},
			Binding:        providers.PortableToolDomainAuthorityV1{ID: "provider/python/" + name + "/binding", Owner: pythonOwner},
			Filesystem:     finalImageAuthority("filesystem"),
			Environment:    finalImageAuthority("environment"),
			Exports:        finalImageAuthority("exports"),
			Capabilities:   finalImageAuthority("capabilities"),
		})
	}
	return domains, nil
}
