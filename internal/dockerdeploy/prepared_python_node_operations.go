package dockerdeploy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

// PreparedPythonNodeOperations connects one-session wheel resolution to the
// typed provider graph and deployment-owned store.
type PreparedPythonNodeOperations struct {
	Store                  providerstore.Store
	Validators             providers.ProviderOwnerValidators
	FinalImageConfig       providers.ImageConfigPolicy
	Artifacts              PreparedPythonResolverArtifacts
	ReusableWheels         []providerstore.ArtifactDescriptor
	PriorSources           []providers.ResolvedSourceInput
	PriorSourceWheels      []providerstore.ArtifactDescriptor
	LocalOverrides         []PythonLocalOverrideV1
	Progress               io.Writer
	ShowApplicationContext bool
	RunOptions             RunOptions
	verifiedArtifacts      map[canonical.Digest]string
}

func (operations PreparedPythonNodeOperations) Preparer(
	descriptor deploy.ImageDescriptor,
	workspace PreparedProbeWorkspace,
) PythonNodePreparer {
	return PythonNodePreparer{
		Descriptor:     descriptor,
		Workspace:      workspace,
		Artifacts:      operations.Artifacts,
		ReusableWheels: append([]providerstore.ArtifactDescriptor{}, operations.ReusableWheels...),
		ValidateCached: operations.validateCached,
		ResolveFresh:   operations.resolveFresh,
		PrepareBuildTools: func(ctx context.Context, tools []string) (PythonBuildToolEnvironmentV1, error) {
			writeProviderBuildProgress(
				operations.Progress, "preparing build tools %s",
				strings.Join(tools, ", "),
			)
			return PreparePythonBuildToolEnvironmentV1(ctx, PythonBuildToolEnvironmentInputV1{
				Store: operations.Store, Upstream: descriptor, Workspace: workspace,
				FinalImageConfig: cloneImageConfigPolicy(operations.FinalImageConfig),
				Tools:            append([]string{}, tools...),
				RunOptions:       operations.RunOptions,
			})
		},
		PrepareRetryArtifacts: func() (PreparedPythonResolverArtifacts, func(), error) {
			return PreparePythonResolverArtifacts(operations.Store, operations.ReusableWheels)
		},
	}
}

func (operations PreparedPythonNodeOperations) validateCached(
	ctx context.Context,
	session *PythonResolverSession,
	request providers.ResolveNodeRequest,
	cached providers.ResolveResult,
) (providers.GraphConsumerValidation, error) {
	node, found := graphBackendNode(request.Plan, request.NodeID)
	if !found || len(node.Components) != 1 {
		return providers.GraphConsumerValidation{}, fmt.Errorf("cached Python node %q does not identify one component", request.NodeID)
	}
	component := node.Components[0]
	pythonBundle, err := pythonprovider.DecodeCanonicalBundleDataV1(component, cached.Bundle.Payload.ProviderPayload)
	if err != nil {
		return providers.GraphConsumerValidation{}, err
	}
	closure := make([]string, 0, len(pythonBundle.Wheels))
	for _, wheel := range pythonBundle.Wheels {
		closure = append(closure, wheel.Distribution)
	}
	sort.Strings(closure)
	effectiveProviderRequest, err := pythonprovider.FilterProviderRequestOverridesV1(node.Request, closure)
	if err != nil {
		return providers.GraphConsumerValidation{}, err
	}
	matches, err := canonicalProviderRequestsEqualV1(effectiveProviderRequest, cached.Bundle.Payload.Request)
	if err != nil {
		return providers.GraphConsumerValidation{}, err
	}
	if !matches {
		return providers.GraphConsumerValidation{}, fmt.Errorf("cached Python request no longer matches its closure-relevant package overrides")
	}
	effectiveRequest := request
	effectiveRequest.Plan.Nodes = append([]providers.NodeSpec{}, request.Plan.Nodes...)
	for index := range effectiveRequest.Plan.Nodes {
		if effectiveRequest.Plan.Nodes[index].ID != request.NodeID {
			continue
		}
		effectiveRequest.Plan.Nodes[index].Request = effectiveProviderRequest
		effectiveRequest.Plan.Nodes[index].Requirements.ProviderData = providers.CanonicalProviderData{
			Schema: effectiveProviderRequest.Schema, Value: effectiveProviderRequest.Value,
		}
		break
	}
	if err := providers.ValidateProviderNodeResolution(effectiveRequest, cached, operations.Validators); err != nil {
		return providers.GraphConsumerValidation{}, err
	}
	available := make(map[providerstore.ArtifactDescriptor]struct{}, len(operations.ReusableWheels))
	for _, wheel := range operations.ReusableWheels {
		available[wheel] = struct{}{}
	}
	cachedWheels := make([]providerstore.ArtifactDescriptor, 0, len(pythonBundle.Wheels))
	for _, wheel := range pythonBundle.Wheels {
		if _, found := available[wheel.Artifact]; !found {
			return providers.GraphConsumerValidation{}, fmt.Errorf("cached Python wheel %q is not a reusable artifact from the current lock", wheel.Artifact.LogicalPath)
		}
		cachedWheels = append(cachedWheels, wheel.Artifact)
	}
	artifacts := operations.Artifacts
	if session != nil {
		artifacts = session.artifacts
	}
	if err := VerifyPythonResolverArtifacts(artifacts, cachedWheels); err != nil {
		return providers.GraphConsumerValidation{}, err
	}
	if err := operations.Store.VerifyArtifact(pythonBundle.Script); err != nil {
		return providers.GraphConsumerValidation{}, fmt.Errorf("verify cached Python materialization script: %w", err)
	}
	consumer, err := ValidatePythonConsumer(ctx, session, operations.FinalImageConfig)
	if err != nil {
		return providers.GraphConsumerValidation{}, err
	}
	requirement := cached.Profile.Declaration.Executables[0]
	locked := cached.Profile.SelectedExecutables[0]
	candidate := providers.RealizedOutput{
		SupplierNode: providers.NodeID(locked.Output.Component), SupplierComponent: locked.Output.Component,
		Name: locked.Output.Name, Candidate: providers.ExecutableCandidate{InvocationPath: locked.InvocationPath},
	}
	observed, err := SelectPythonInterpreter(ctx, session, consumer.EnvironmentLauncher, requirement, []providers.RealizedOutput{candidate})
	if err != nil {
		return providers.GraphConsumerValidation{}, err
	}
	lockedBytes, err := canonical.Marshal(locked)
	if err != nil {
		return providers.GraphConsumerValidation{}, fmt.Errorf("encode locked Python interpreter evidence: %w", err)
	}
	observedBytes, err := canonical.Marshal(observed)
	if err != nil {
		return providers.GraphConsumerValidation{}, fmt.Errorf("encode observed Python interpreter evidence: %w", err)
	}
	if !bytes.Equal(lockedBytes, observedBytes) {
		return providers.GraphConsumerValidation{}, fmt.Errorf("cached Python interpreter evidence no longer matches the current image prefix")
	}
	if err := operations.recordVerifiedPythonArtifacts(cached.Bundle); err != nil {
		return providers.GraphConsumerValidation{}, err
	}
	return consumer, nil
}

func (operations PreparedPythonNodeOperations) resolveFresh(
	ctx context.Context,
	session *PythonResolverSession,
	request providers.ResolveNodeRequest,
) (providers.ResolveResult, providers.GraphConsumerValidation, error) {
	for digest := range operations.verifiedArtifacts {
		delete(operations.verifiedArtifacts, digest)
	}
	consumer, err := ValidatePythonConsumer(ctx, session, operations.FinalImageConfig)
	if err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	node, found := graphBackendNode(request.Plan, request.NodeID)
	if !found || node.Provider != blueprint.ComponentTypePython || len(node.Components) != 1 || len(node.Requirements.Executables) != 1 {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, fmt.Errorf("fresh Python node %q does not identify one component and interpreter requirement", request.NodeID)
	}
	candidateGroups, err := providers.BuildRequirementCandidates(request.Plan, node.ID, request.EarlierCatalog)
	if err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	if len(candidateGroups) != 1 || candidateGroups[0].RequirementID != node.Requirements.Executables[0].ID {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, fmt.Errorf("fresh Python node %q does not have one interpreter candidate group", request.NodeID)
	}
	requirement := node.Requirements.Executables[0]
	candidates := append([]providers.RealizedOutput{}, candidateGroups[0].Outputs...)
	interpreter, err := SelectPythonInterpreter(ctx, session, consumer.EnvironmentLauncher, requirement, candidates)
	if err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	verifiedWheels, err := FilterVerifiedPythonResolverArtifacts(session.artifacts, operations.ReusableWheels)
	if err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	buildEnvironmentDigest, err := session.SourceBuildEnvironmentDigest(interpreter)
	if err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	effectiveSources, effectiveWheels, err := filterPythonSourcesForBuildEnvironment(
		request.SourceCandidates, verifiedWheels, buildEnvironmentDigest,
	)
	if err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	directDistributions, err := pythonprovider.ProviderRequestDistributionsV1(node.Request)
	if err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	directOverrides, err := selectUnresolvedPythonLocalOverrides(
		operations.LocalOverrides, directDistributions, node.Components[0], effectiveSources,
	)
	if err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	if len(directOverrides) != 0 {
		effectiveSources, effectiveWheels, err = operations.materializeLocalOverrides(
			ctx, session, consumer.EnvironmentLauncher, requirement, interpreter,
			buildEnvironmentDigest, node.Components[0], directOverrides, effectiveSources, effectiveWheels,
		)
		if err != nil {
			return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
		}
	}
	var closure []string
	for {
		if err := session.ResolveWheels(
			ctx, consumer.EnvironmentLauncher, requirement, interpreter,
			node.Request, effectiveSources, effectiveWheels,
		); err != nil {
			return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
		}
		closure, err = pythonprovider.InspectPreparedWheelDistributionsV1(ctx, session.artifacts.OutputHostDir)
		if err != nil {
			return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
		}
		selected, err := selectUnresolvedPythonLocalOverrides(
			operations.LocalOverrides, closure, node.Components[0], effectiveSources,
		)
		if err != nil {
			return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
		}
		if len(selected) == 0 {
			break
		}
		if err := ResetPythonResolverOutput(session.artifacts); err != nil {
			return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
		}
		effectiveSources, effectiveWheels, err = operations.materializeLocalOverrides(
			ctx, session, consumer.EnvironmentLauncher, requirement, interpreter,
			buildEnvironmentDigest, node.Components[0], selected, effectiveSources, effectiveWheels,
		)
		if err != nil {
			return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
		}
	}
	if err := session.Stop(ctx); err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	effectiveProviderRequest, err := pythonprovider.FilterProviderRequestOverridesV1(node.Request, closure)
	if err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	effectiveRequest := request
	effectiveRequest.Plan.Nodes = append([]providers.NodeSpec{}, request.Plan.Nodes...)
	for index := range effectiveRequest.Plan.Nodes {
		if effectiveRequest.Plan.Nodes[index].ID != request.NodeID {
			continue
		}
		effectiveRequest.Plan.Nodes[index].Request = effectiveProviderRequest
		effectiveRequest.Plan.Nodes[index].Requirements.ProviderData = providers.CanonicalProviderData{
			Schema: effectiveProviderRequest.Schema, Value: effectiveProviderRequest.Value,
		}
		break
	}
	effectiveRequest.SourceCandidates = effectiveSources
	resolver := pythonprovider.WheelNodeResolver{
		ResolveInterpreter: func(
			_ context.Context,
			gotRequirement providers.ExecutableRequirement,
			gotCandidates []providers.RealizedOutput,
			_ providers.RealizedImageV1,
			_ blueprint.Platform,
		) (providers.ExecutableEvidence, error) {
			if gotRequirement != requirement || !reflect.DeepEqual(gotCandidates, candidates) {
				return providers.ExecutableEvidence{}, fmt.Errorf("Python resolver interpreter inputs changed after selection")
			}
			return interpreter, nil
		},
		PrepareWheels: func(_ context.Context, input providers.ResolveInput, gotInterpreter providers.ExecutableEvidence) (string, error) {
			if !reflect.DeepEqual(gotInterpreter, interpreter) ||
				!reflect.DeepEqual(input.SourceCandidates, effectiveSources) {
				return "", fmt.Errorf("Python resolver prepared inputs changed before wheel ingestion")
			}
			return session.artifacts.OutputHostDir, nil
		},
	}
	resolution, err := providers.ResolveProviderNode(ctx, effectiveRequest, resolver, operations.Store, operations.Validators)
	if err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	if err := operations.recordVerifiedPythonArtifacts(resolution.Bundle); err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	return resolution, consumer, nil
}

func (operations PreparedPythonNodeOperations) materializeLocalOverrides(
	ctx context.Context,
	session *PythonResolverSession,
	launcher providers.ValidatedExecutableInput,
	requirement providers.ExecutableRequirement,
	interpreter providers.ExecutableEvidence,
	buildEnvironmentDigest canonical.Digest,
	component string,
	selected []PythonLocalOverrideV1,
	effectiveSources []providers.ResolvedSourceInput,
	effectiveWheels []providerstore.ArtifactDescriptor,
) ([]providers.ResolvedSourceInput, []providerstore.ArtifactDescriptor, error) {
	distributions := make([]string, len(selected))
	for index, override := range selected {
		distributions[index] = override.Distribution
	}
	sort.Strings(distributions)
	sources, err := ObserveSelectedPythonLocalSources(selected, distributions)
	if err != nil {
		return nil, nil, err
	}
	snapshots, err := StagePythonLocalSourceSnapshots(session.artifacts, sources)
	if err != nil {
		return nil, nil, err
	}
	missingTools := map[string]bool{}
	recipes := make(map[string]PythonLocalSourceRecipeV1, len(snapshots))
	for _, snapshot := range snapshots {
		recipe, err := ReadPythonLocalSourceRecipeV1(snapshot.HostDir, snapshot.Distribution)
		if err != nil {
			return nil, nil, err
		}
		recipes[snapshot.Distribution] = recipe
		for _, tool := range recipe.Tools {
			if !session.HasPortableBuildToolV1(tool) {
				missingTools[tool] = true
			}
		}
	}
	if len(missingTools) != 0 {
		tools := make([]string, 0, len(missingTools))
		for tool := range missingTools {
			tools = append(tools, tool)
		}
		sort.Strings(tools)
		return nil, nil, &pythonBuildToolsRequiredError{Tools: tools}
	}
	projectKind := "project"
	if len(distributions) != 1 {
		projectKind = "projects"
	}
	writeProviderBuildProgress(
		operations.Progress, "building local Python %s %s%s",
		projectKind,
		strings.Join(distributions, ", "),
		providerProgressContextSuffix([]string{providerProgressComponentContext(
			blueprint.ComponentTypePython, component, operations.ShowApplicationContext,
		)}),
	)
	if err := session.BuildSourceDistributions(ctx, launcher, requirement, interpreter, snapshots); err != nil {
		return nil, nil, err
	}
	buildIdentities := make(map[string]PythonSourceBuildIdentityV1, len(snapshots))
	for _, snapshot := range snapshots {
		recipe := recipes[snapshot.Distribution]
		identity := PythonSourceBuildIdentityV1{
			BuildEnvironmentDigest: buildEnvironmentDigest,
			BuilderProfile:         pythonprovider.SourceBuilderProfileV1,
			BuildSettings:          pythonprovider.CanonicalSourceBuildSettingsV1(),
		}
		if recipe.Found {
			settings, err := pythonprovider.CanonicalSourceBuildSettingsV2(
				recipe.Build, recipe.Digest, recipe.Tools,
			)
			if err != nil {
				return nil, nil, err
			}
			identity.BuilderProfile = pythonprovider.SourceBuilderProfileV2
			identity.BuildSettings = settings
		}
		buildIdentities[snapshot.Distribution] = identity
	}
	preparedDistributions, err := PublishBuiltPythonSourceDistributionsWithIdentities(
		ctx, operations.Store, session.artifacts, snapshots, buildIdentities,
	)
	if err != nil {
		return nil, nil, err
	}
	sourceByDistribution := make(map[string]PythonLocalSource, len(sources))
	for _, source := range sources {
		sourceByDistribution[source.Distribution] = source
	}
	for _, distribution := range preparedDistributions {
		source, found := sourceByDistribution[distribution.Distribution]
		if found {
			_ = learnPythonSourceRelevance(operations.Store, source, distribution.Artifact)
		}
	}
	reusedSources, pendingDistributions, wheelsAfterSdistReuse, err := operations.reusePythonSourceWheels(
		session.artifacts, component, preparedDistributions, effectiveWheels,
	)
	if err != nil {
		return nil, nil, err
	}
	if err := session.BuildSourceWheels(ctx, launcher, requirement, interpreter, pendingDistributions); err != nil {
		return nil, nil, err
	}
	builtSources, stagedWheels, err := PublishBuiltPythonSourceWheels(
		ctx, operations.Store, session.artifacts, component, pendingDistributions, wheelsAfterSdistReuse,
	)
	if err != nil {
		return nil, nil, err
	}
	mergedBuilt, err := mergePythonSourceCandidates(reusedSources, builtSources)
	if err != nil {
		return nil, nil, err
	}
	merged, err := mergePythonSourceCandidates(effectiveSources, mergedBuilt)
	if err != nil {
		return nil, nil, err
	}
	return merged, stagedWheels, nil
}

func (operations PreparedPythonNodeOperations) reusePythonSourceWheels(
	artifacts PreparedPythonResolverArtifacts,
	component string,
	distributions []PreparedPythonSourceDistribution,
	effective []providerstore.ArtifactDescriptor,
) ([]providers.ResolvedSourceInput, []PreparedPythonSourceDistribution, []providerstore.ArtifactDescriptor, error) {
	priorByPackage := make(map[string]providers.ResolvedSourceInput, len(operations.PriorSources))
	for _, source := range operations.PriorSources {
		if err := pythonprovider.ValidateResolvedSourceInputV2(source); err != nil {
			return nil, nil, nil, err
		}
		if source.Component == component {
			priorByPackage[source.LogicalPackage] = source
		}
	}
	wheelsByDigest := make(map[canonical.Digest]providerstore.ArtifactDescriptor, len(operations.PriorSourceWheels))
	for _, wheel := range operations.PriorSourceWheels {
		if err := wheel.Validate(); err != nil {
			return nil, nil, nil, fmt.Errorf("prior Python source wheel: %w", err)
		}
		if wheel.Kind != "wheel" {
			return nil, nil, nil, fmt.Errorf("prior Python source artifact %q must be a wheel", wheel.LogicalPath)
		}
		wheelsByDigest[wheel.SHA256] = wheel
	}
	reused := []providers.ResolvedSourceInput{}
	pending := []PreparedPythonSourceDistribution{}
	for _, distribution := range distributions {
		prior, found := priorByPackage[distribution.Distribution]
		if !found ||
			prior.SourceArtifactDigest != distribution.Artifact.SHA256 ||
			prior.BuildEnvironmentDigest != distribution.BuildEnvironmentDigest ||
			prior.BuilderProfile != distribution.BuilderProfile ||
			!reflect.DeepEqual(prior.BuildSettings, distribution.BuildSettings) {
			pending = append(pending, distribution)
			continue
		}
		wheel, found := wheelsByDigest[prior.OutputArtifactDigest]
		if !found {
			pending = append(pending, distribution)
			continue
		}
		metadata, err := pythonprovider.SourceWheelMetadataV2(prior)
		if err != nil {
			return nil, nil, nil, err
		}
		if metadata.Version != distribution.Version {
			pending = append(pending, distribution)
			continue
		}
		if err := StagePythonResolverWheelArtifact(operations.Store, artifacts, wheel); err != nil {
			return nil, nil, nil, err
		}
		source, err := pythonprovider.NewResolvedSourceInputWithBuildV2(
			component, distribution.Distribution, distribution.SourceInputDigest,
			distribution.Artifact, distribution.BuildEnvironmentDigest, wheel, metadata,
			distribution.BuilderProfile, distribution.BuildSettings,
		)
		if err != nil {
			return nil, nil, nil, err
		}
		reused = append(reused, source)
		effective = replacePythonWheelByFilename(effective, wheel)
	}
	return reused, pending, effective, nil
}

func filterPythonSourcesForBuildEnvironment(
	sources []providers.ResolvedSourceInput,
	wheels []providerstore.ArtifactDescriptor,
	buildEnvironmentDigest canonical.Digest,
) ([]providers.ResolvedSourceInput, []providerstore.ArtifactDescriptor, error) {
	if sources == nil || wheels == nil {
		return nil, nil, fmt.Errorf("Python source candidates and reusable wheels must use arrays")
	}
	if err := buildEnvironmentDigest.Validate(); err != nil {
		return nil, nil, fmt.Errorf("Python source build environment digest: %w", err)
	}
	effectiveSources := make([]providers.ResolvedSourceInput, 0, len(sources))
	droppedWheelDigests := make(map[canonical.Digest]struct{})
	for _, source := range sources {
		if err := pythonprovider.ValidateResolvedSourceInputV2(source); err != nil {
			return nil, nil, err
		}
		if source.BuildEnvironmentDigest != buildEnvironmentDigest {
			droppedWheelDigests[source.OutputArtifactDigest] = struct{}{}
			continue
		}
		effectiveSources = append(effectiveSources, source)
	}
	effectiveWheels := make([]providerstore.ArtifactDescriptor, 0, len(wheels))
	for _, wheel := range wheels {
		if err := wheel.Validate(); err != nil {
			return nil, nil, fmt.Errorf("Python reusable wheel: %w", err)
		}
		if _, dropped := droppedWheelDigests[wheel.SHA256]; dropped {
			continue
		}
		effectiveWheels = append(effectiveWheels, wheel)
	}
	return effectiveSources, effectiveWheels, nil
}

func selectUnresolvedPythonLocalOverrides(
	overrides []PythonLocalOverrideV1,
	closure []string,
	component string,
	sources []providers.ResolvedSourceInput,
) ([]PythonLocalOverrideV1, error) {
	if overrides == nil || closure == nil || sources == nil {
		return nil, fmt.Errorf("Python local override selection inputs must use arrays")
	}
	closureSet := make(map[string]struct{}, len(closure))
	for index, distribution := range closure {
		if distribution == "" || pythonprovider.NormalizeDistributionName(distribution) != distribution {
			return nil, fmt.Errorf("Python closure distribution %d is not normalized: %q", index, distribution)
		}
		if index > 0 && closure[index-1] >= distribution {
			return nil, fmt.Errorf("Python closure distributions must be unique and sorted")
		}
		closureSet[distribution] = struct{}{}
	}
	resolved := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		if err := pythonprovider.ValidateResolvedSourceInputV2(source); err != nil {
			return nil, err
		}
		if source.Component == component {
			resolved[source.LogicalPackage] = struct{}{}
		}
	}
	selected := make([]PythonLocalOverrideV1, 0)
	for index, override := range overrides {
		if override.Distribution == "" || pythonprovider.NormalizeDistributionName(override.Distribution) != override.Distribution {
			return nil, fmt.Errorf("local Python override %d distribution is not normalized: %q", index, override.Distribution)
		}
		if index > 0 && overrides[index-1].Distribution >= override.Distribution {
			return nil, fmt.Errorf("local Python overrides must be unique and sorted")
		}
		if override.HostDir == "" || !filepath.IsAbs(override.HostDir) || filepath.Clean(override.HostDir) != override.HostDir {
			return nil, fmt.Errorf("local Python override %q path must be absolute and clean", override.Distribution)
		}
		if _, needed := closureSet[override.Distribution]; !needed {
			continue
		}
		if _, alreadyResolved := resolved[override.Distribution]; alreadyResolved {
			continue
		}
		selected = append(selected, override)
	}
	return selected, nil
}

func mergePythonSourceCandidates(
	existing []providers.ResolvedSourceInput,
	built []providers.ResolvedSourceInput,
) ([]providers.ResolvedSourceInput, error) {
	if existing == nil || built == nil {
		return nil, fmt.Errorf("Python source candidate collections must use arrays")
	}
	byKey := make(map[string]providers.ResolvedSourceInput, len(existing)+len(built))
	for _, collection := range [][]providers.ResolvedSourceInput{existing, built} {
		for _, source := range collection {
			if err := pythonprovider.ValidateResolvedSourceInputV2(source); err != nil {
				return nil, err
			}
			byKey[source.Component+"\x00"+source.LogicalPackage] = source
		}
	}
	merged := make([]providers.ResolvedSourceInput, 0, len(byKey))
	for _, source := range byKey {
		merged = append(merged, source)
	}
	sort.Slice(merged, func(left int, right int) bool {
		if merged[left].Component != merged[right].Component {
			return merged[left].Component < merged[right].Component
		}
		return merged[left].LogicalPackage < merged[right].LogicalPackage
	})
	return merged, nil
}

func (operations PreparedPythonNodeOperations) recordVerifiedPythonArtifacts(bundle providers.ResolvedBundle) error {
	if operations.verifiedArtifacts == nil {
		return nil
	}
	component, ok := bundle.Payload.Request.Value["component"].(string)
	if !ok {
		return fmt.Errorf("record verified Python wheels bundle has no component")
	}
	pythonBundle, err := pythonprovider.DecodeCanonicalBundleDataV1(component, bundle.Payload.ProviderPayload)
	if err != nil {
		return err
	}
	for _, wheel := range pythonBundle.Wheels {
		path, err := operations.Store.BlobPath(wheel.Artifact.SHA256)
		if err != nil {
			return err
		}
		if err := providerstore.InspectArtifactFile(path, wheel.Artifact); err != nil {
			return fmt.Errorf("inspect verified Python wheel %q: %w", wheel.Artifact.LogicalPath, err)
		}
		operations.verifiedArtifacts[wheel.Artifact.SHA256] = path
	}
	scriptPath, err := operations.Store.BlobPath(pythonBundle.Script.SHA256)
	if err != nil {
		return err
	}
	if err := providerstore.InspectArtifactFile(scriptPath, pythonBundle.Script); err != nil {
		return fmt.Errorf("inspect verified Python materialization script: %w", err)
	}
	operations.verifiedArtifacts[pythonBundle.Script.SHA256] = scriptPath
	return nil
}
