package dockerdeploy

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"sort"

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
	SourceSnapshots   []PreparedPythonSourceSnapshot
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
	if err := providers.ValidateProviderNodeResolution(request, cached, operations.Validators); err != nil {
		return providers.GraphConsumerValidation{}, err
	}
	node, found := graphBackendNode(request.Plan, request.NodeID)
	if !found || len(node.Components) != 1 {
		return providers.GraphConsumerValidation{}, fmt.Errorf("cached Python node %q does not identify one component", request.NodeID)
	}
	component := node.Components[0]
	pythonBundle, err := pythonprovider.DecodeCanonicalBundleDataV1(component, cached.Bundle.Payload.ProviderPayload)
	if err != nil {
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
	if len(operations.SourceSnapshots) != 0 {
		if err := session.BuildSourceWheels(
			ctx, consumer.EnvironmentLauncher, requirement, interpreter, operations.SourceSnapshots,
		); err != nil {
			return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
		}
		builtSources, stagedWheels, err := PublishBuiltPythonSourceWheels(
			ctx, operations.Store, operations.Artifacts, node.Components[0], operations.SourceSnapshots, verifiedWheels,
		)
		if err != nil {
			return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
		}
		effectiveSources, err = mergePythonSourceCandidates(effectiveSources, builtSources)
		if err != nil {
			return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
		}
		effectiveWheels = stagedWheels
	}
	effectiveRequest := request
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
		PrepareWheels: func(ctx context.Context, input providers.ResolveInput, interpreter providers.ExecutableEvidence) (string, error) {
			if err := session.ResolveWheels(
				ctx, consumer.EnvironmentLauncher, requirement, interpreter,
				input.Node.Request, input.SourceCandidates, effectiveWheels,
			); err != nil {
				return "", err
			}
			if err := session.Stop(ctx); err != nil {
				return "", err
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
