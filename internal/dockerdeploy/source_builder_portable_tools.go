package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
	"github.com/omry/reploy/internal/toolrequest"
)

const (
	// SourceBuilderExportsDirectoryV1 is the Reploy-owned directory inside a
	// prepared source-builder image that exposes exactly the selected
	// portable-tool exports by name. It is prepended to PATH only for
	// source-build commands and never reaches an application image.
	SourceBuilderExportsDirectoryV1 = "/opt/reploy/exports"

	sourceBuilderDomainPrefixV1         = "source-builder/"
	sourceBuilderBlueprintEnvelopeV1    = "reploy-blueprint-digest-v1"
	sourceBuilderClientEnvelopeV1       = "reploy-client-v1"
	sourceBuilderRecipeScopePrefixV1    = "source-builder:"
	sourceBuilderRequiredPackageManager = "apt"
)

// SourceBuilderRecipeIdentityV1 binds one local-source recipe's requirement
// groups to the immutable recipe digest that produced them, so the consumer
// can prove the snapshot it builds from is the recipe the plan resolved.
type SourceBuilderRecipeIdentityV1 struct {
	Scope  string
	Digest canonical.Digest
	Groups []toolrequest.CanonicalRequirementGroupV1
}

// SourceBuilderPortableToolPlanV1 is the resolved, not yet acquired,
// portable-tool plan for every isolated source builder of one build. It is
// derived before graph execution so exact reuse can compare selected
// identities without contacting the network.
type SourceBuilderPortableToolPlanV1 struct {
	Target   toolcatalog.TargetIdentityV1
	Plan     providers.PortableToolPlanV1
	DAG      providers.PortableToolProviderDAGV1
	Closures []toolcatalog.SelectedClosureV1
	Recipes  map[string]SourceBuilderRecipeIdentityV1
	Snapshot toolcatalog.ImmutableOperationSnapshotV1
}

// PlanSourceBuilderPortableToolsInputV1 carries the exact inputs one build
// preparation supplies to source-builder portable-tool planning: the selected
// base image, the provider plan that will own the builder domains, the
// configured local-source overrides whose recipes may declare tools, and the
// blueprint and client identities sealed into the resolution snapshot.
type PlanSourceBuilderPortableToolsInputV1 struct {
	Store           providerstore.Store
	Base            deploy.ImageDescriptor
	ProviderPlan    providers.ProviderPlanV1
	LocalOverrides  []PythonLocalOverrideV1
	BlueprintDigest canonical.Digest
	ReployVersion   string
}

var readSourceBuilderRecipeV1 = ReadPythonLocalSourceRecipeV1
var observeSourceBuilderTargetV1 = ObserveSourceBuilderTargetIdentityV1
var resolveSourceBuilderPortableToolPlanV1 = toolcatalog.ResolveEmbeddedPortableToolPlanV1
var prepareSourceBuilderProbeWorkspaceV1 = PrepareProbeWorkspace
var prepareSourceBuilderAPTWorkspaceV1 = PrepareAPTResolverWorkspace
var openSourceBuilderValidationSessionV1 = OpenAPTImageValidationSession
var probeSourceBuilderBaseProfileV1 = func(ctx context.Context, session *ImageValidationSession) (APTBaseValidation, error) {
	return session.ProbeAPTBaseProfile(ctx)
}

// PlanSourceBuilderPortableToolsV1 reads every configured local-source recipe,
// resolves its canonical build-scope tool requirements against the embedded
// portable catalog for the observed base target, and compiles the PTD-21 plan
// and provider DAG. A build whose recipes declare no tool requirements plans
// nothing and returns nil. No acquisition or materialization happens here.
func PlanSourceBuilderPortableToolsV1(
	ctx context.Context,
	input PlanSourceBuilderPortableToolsInputV1,
) (*SourceBuilderPortableToolPlanV1, error) {
	if ctx == nil {
		return nil, fmt.Errorf("plan source-builder portable tools requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if input.LocalOverrides == nil {
		return nil, fmt.Errorf("plan source-builder portable tools local overrides must use an array")
	}
	recipes := map[string]SourceBuilderRecipeIdentityV1{}
	groups := []toolrequest.CanonicalRequirementGroupV1{}
	for index, override := range input.LocalOverrides {
		if override.Distribution == "" || pythonprovider.NormalizeDistributionName(override.Distribution) != override.Distribution {
			return nil, fmt.Errorf("local Python override %d distribution is not normalized: %q", index, override.Distribution)
		}
		if index > 0 && input.LocalOverrides[index-1].Distribution >= override.Distribution {
			return nil, fmt.Errorf("local Python overrides must be unique and sorted")
		}
		if override.HostDir == "" || !filepath.IsAbs(override.HostDir) || filepath.Clean(override.HostDir) != override.HostDir {
			return nil, fmt.Errorf("local Python override %q path must be absolute and clean", override.Distribution)
		}
		recipe, err := readSourceBuilderRecipeV1(override.HostDir, override.Distribution)
		if err != nil {
			return nil, err
		}
		if !recipe.Found || len(recipe.Requirements) == 0 {
			continue
		}
		scope := sourceBuilderRecipeScopePrefixV1 + override.Distribution
		for _, group := range recipe.Requirements {
			if group.Scope != scope {
				return nil, fmt.Errorf("local source recipe for %q carries requirement scope %q, want %q", override.Distribution, group.Scope, scope)
			}
		}
		recipes[override.Distribution] = SourceBuilderRecipeIdentityV1{
			Scope: scope, Digest: recipe.Digest,
			Groups: append([]toolrequest.CanonicalRequirementGroupV1{}, recipe.Requirements...),
		}
		groups = append(groups, recipe.Requirements...)
	}
	if len(groups) == 0 {
		return nil, nil
	}
	if err := input.Base.Validate(); err != nil {
		return nil, fmt.Errorf("plan source-builder portable tools base: %w", err)
	}
	owner, err := sourceBuilderDomainOwnerV1(input.ProviderPlan)
	if err != nil {
		return nil, err
	}
	if err := input.BlueprintDigest.Validate(); err != nil {
		return nil, fmt.Errorf("plan source-builder portable tools blueprint digest: %w", err)
	}
	capabilities, err := toolcatalog.EmbeddedClientCapabilitiesV1(input.ReployVersion)
	if err != nil {
		return nil, err
	}
	target, err := observeSourceBuilderTargetV1(ctx, input.Store, input.Base)
	if err != nil {
		return nil, fmt.Errorf("observe source-builder target: %w", err)
	}
	scopes := make([]string, 0, len(recipes))
	for _, recipe := range recipes {
		scopes = append(scopes, recipe.Scope)
	}
	sort.Strings(scopes)
	solverDomains := make([]toolcatalog.ProviderDomainSetV1, 0, len(scopes))
	for _, scope := range scopes {
		solverDomains = append(solverDomains, toolcatalog.ProviderDomainSetV1{
			Scope:          scope,
			PackageManager: sourceBuilderDomainPrefixV1 + "package-manager",
			Filesystem:     sourceBuilderDomainPrefixV1 + "filesystem",
			Environment:    sourceBuilderDomainPrefixV1 + "environment",
			Exports:        sourceBuilderDomainPrefixV1 + "exports",
			Capabilities:   sourceBuilderDomainPrefixV1 + "capabilities",
		})
	}
	operation := toolcatalog.ResolutionOperationInputsV1{
		Blueprint: canonical.Envelope{
			Schema: sourceBuilderBlueprintEnvelopeV1,
			Value:  canonical.Object{"digest": string(input.BlueprintDigest)},
		},
		Reploy: canonical.Envelope{
			Schema: sourceBuilderClientEnvelopeV1,
			Value:  canonical.Object{"version": capabilities.ReployVersion},
		},
		ActiveProviders: toolcatalog.ActiveProviderConstraintsV1{
			Schema:  toolcatalog.ActiveProviderConstraintsSchemaV1,
			Sources: []toolcatalog.ActiveProviderConstraintSourceV1{},
		},
	}
	plan, resolution, err := resolveSourceBuilderPortableToolPlanV1(groups, target, capabilities, nil, solverDomains, operation)
	if err != nil {
		return nil, fmt.Errorf("resolve source-builder portable tools: %w", err)
	}
	dagDomains := make([]providers.PortableToolProviderDomainSetV1, 0, len(plan.Tools))
	seenScopes := map[string]struct{}{}
	for _, entry := range plan.Tools {
		if _, seen := seenScopes[entry.Scope]; seen {
			continue
		}
		seenScopes[entry.Scope] = struct{}{}
		authority := func(name string) providers.PortableToolDomainAuthorityV1 {
			return providers.PortableToolDomainAuthorityV1{ID: sourceBuilderDomainPrefixV1 + name, Owner: owner}
		}
		dagDomains = append(dagDomains, providers.PortableToolProviderDomainSetV1{
			Scope: entry.Scope, PackageManager: authority("package-manager"), Binding: authority("binding"),
			Filesystem: authority("filesystem"), Environment: authority("environment"),
			Exports: authority("exports"), Capabilities: authority("capabilities"),
		})
	}
	dag, err := providers.BuildPortableToolProviderDAGV1(input.ProviderPlan, plan, dagDomains)
	if err != nil {
		return nil, fmt.Errorf("plan source-builder portable tool DAG: %w", err)
	}
	return &SourceBuilderPortableToolPlanV1{
		Target: target, Plan: plan, DAG: dag,
		Closures: append([]toolcatalog.SelectedClosureV1{}, resolution.Closures...),
		Recipes:  recipes, Snapshot: resolution.Snapshot,
	}, nil
}

// sourceBuilderDomainOwnerV1 names the provider node that owns the shared
// source-builder authority domains. Source builders run beside Python
// provider nodes, so the lexicographically first Python node owns the domains;
// every Python node still prepares its own disposable builder image.
func sourceBuilderDomainOwnerV1(plan providers.ProviderPlanV1) (providers.NodeID, error) {
	owners := []string{}
	for _, node := range plan.Nodes {
		if node.Provider == blueprint.ComponentTypePython {
			owners = append(owners, string(node.ID))
		}
	}
	if len(owners) == 0 {
		return "", fmt.Errorf("source-builder portable tools require a Python provider node in the provider plan")
	}
	sort.Strings(owners)
	return providers.NodeID(owners[0]), nil
}

// RecipeFor returns the recipe identity planned for one distribution's
// isolated source builder, when its recipe declared tools.
func (plan *SourceBuilderPortableToolPlanV1) RecipeFor(distribution string) (SourceBuilderRecipeIdentityV1, bool) {
	if plan == nil {
		return SourceBuilderRecipeIdentityV1{}, false
	}
	recipe, found := plan.Recipes[distribution]
	return recipe, found
}

// ObserveSourceBuilderTargetIdentityV1 observes the exact portable-tool target
// identity of one base image through the fixed APT base-profile probe in a
// networkless validation container. Image tags are never used as evidence.
func ObserveSourceBuilderTargetIdentityV1(
	ctx context.Context,
	store providerstore.Store,
	base deploy.ImageDescriptor,
) (target toolcatalog.TargetIdentityV1, resultErr error) {
	if ctx == nil {
		return toolcatalog.TargetIdentityV1{}, fmt.Errorf("observe source-builder target requires a context")
	}
	if err := base.Validate(); err != nil {
		return toolcatalog.TargetIdentityV1{}, fmt.Errorf("observe source-builder target base: %w", err)
	}
	workspace, cleanupWorkspace, err := prepareSourceBuilderProbeWorkspaceV1(ctx, store, base.Platform)
	if err != nil {
		return toolcatalog.TargetIdentityV1{}, err
	}
	defer func() {
		if providerHelperCleanupFailed(resultErr) {
			return
		}
		if err := cleanupWorkspace(); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	aptWorkspace, cleanupAPT, err := prepareSourceBuilderAPTWorkspaceV1(store)
	if err != nil {
		return toolcatalog.TargetIdentityV1{}, err
	}
	defer func() {
		if providerHelperCleanupFailed(resultErr) {
			// The container may still hold this mount; keep it for recovery.
			return
		}
		cleanupAPT()
	}()
	session, err := openSourceBuilderValidationSessionV1(ctx, base, workspace, aptWorkspace)
	if err != nil {
		return toolcatalog.TargetIdentityV1{}, err
	}
	defer func() {
		if err := session.Close(context.WithoutCancel(ctx)); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	observed, err := probeSourceBuilderBaseProfileV1(ctx, session)
	if err != nil {
		return toolcatalog.TargetIdentityV1{}, err
	}
	return sourceBuilderTargetIdentityV1(base.Platform, observed)
}

func sourceBuilderTargetIdentityV1(platform blueprint.Platform, observed APTBaseValidation) (toolcatalog.TargetIdentityV1, error) {
	fields := map[string]string{}
	for _, field := range observed.Profile.OSRelease {
		fields[field.Name] = field.Value
	}
	id, version := fields["ID"], fields["VERSION_ID"]
	if id == "" || version == "" {
		return toolcatalog.TargetIdentityV1{}, fmt.Errorf("base image os-release does not declare both ID and VERSION_ID")
	}
	if platform.OS != "linux" || platform.Architecture == "" {
		return toolcatalog.TargetIdentityV1{}, fmt.Errorf("source-builder target requires a Linux platform with an architecture")
	}
	return toolcatalog.TargetIdentityV1{
		Platform:           "linux/" + platform.Architecture,
		OSReleaseID:        id,
		VersionID:          version,
		OCIArchitecture:    platform.Architecture,
		NativeArchitecture: observed.Profile.NativeArchitecture,
		PackageManager:     sourceBuilderRequiredPackageManager,
	}, nil
}
