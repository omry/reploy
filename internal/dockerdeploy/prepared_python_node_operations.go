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
	Store             providerstore.Store
	Validators        providers.ProviderOwnerValidators
	FinalImageConfig  providers.ImageConfigPolicy
	Artifacts         PreparedPythonResolverArtifacts
	ReusableWheels    []providerstore.ArtifactDescriptor
	LocalOverrides    []PythonLocalOverrideV1
	Progress          io.Writer
	verifiedArtifacts map[canonical.Digest]string
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
	if err := VerifyPythonResolverArtifacts(operations.Artifacts, cachedWheels); err != nil {
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
	verifiedWheels, err := FilterVerifiedPythonResolverArtifacts(operations.Artifacts, operations.ReusableWheels)
	if err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	effectiveSources := append([]providers.ResolvedSourceInput{}, request.SourceCandidates...)
	effectiveWheels := verifiedWheels
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
			node.Components[0], directOverrides, effectiveSources, effectiveWheels,
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
		closure, err = pythonprovider.InspectPreparedWheelDistributionsV1(ctx, operations.Artifacts.OutputHostDir)
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
		if err := ResetPythonResolverOutput(operations.Artifacts); err != nil {
			return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
		}
		effectiveSources, effectiveWheels, err = operations.materializeLocalOverrides(
			ctx, session, consumer.EnvironmentLauncher, requirement, interpreter,
			node.Components[0], selected, effectiveSources, effectiveWheels,
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
			return operations.Artifacts.OutputHostDir, nil
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
	writeProviderBuildProgress(
		operations.Progress, "observing local Python sources %s for component %s",
		strings.Join(distributions, ", "), component,
	)
	sources, err := ObserveSelectedPythonLocalSources(selected, distributions)
	if err != nil {
		return nil, nil, err
	}
	snapshots, err := StagePythonLocalSourceSnapshots(operations.Artifacts, sources)
	if err != nil {
		return nil, nil, err
	}
	writeProviderBuildProgress(
		operations.Progress, "building local Python source artifacts %s for component %s",
		strings.Join(distributions, ", "), component,
	)
	if err := session.BuildSourceWheels(ctx, launcher, requirement, interpreter, snapshots); err != nil {
		return nil, nil, err
	}
	builtSources, stagedWheels, err := PublishBuiltPythonSourceWheels(
		ctx, operations.Store, operations.Artifacts, component, snapshots, effectiveWheels,
	)
	if err != nil {
		return nil, nil, err
	}
	merged, err := mergePythonSourceCandidates(effectiveSources, builtSources)
	if err != nil {
		return nil, nil, err
	}
	return merged, stagedWheels, nil
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
		if err := pythonprovider.ValidateResolvedSourceInputV1(source); err != nil {
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
			if err := pythonprovider.ValidateResolvedSourceInputV1(source); err != nil {
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
