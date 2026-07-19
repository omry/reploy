package dockerdeploy

import (
	"bytes"
	"context"
	"fmt"

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
	resolver := pythonprovider.WheelNodeResolver{
		ResolveInterpreter: func(
			ctx context.Context,
			requirement providers.ExecutableRequirement,
			candidates []providers.RealizedOutput,
			_ providers.RealizedImageV1,
			_ blueprint.Platform,
		) (providers.ExecutableEvidence, error) {
			return SelectPythonInterpreter(ctx, session, consumer.EnvironmentLauncher, requirement, candidates)
		},
		PrepareWheels: func(ctx context.Context, input providers.ResolveInput, interpreter providers.ExecutableEvidence) (string, error) {
			requirement := input.Node.Requirements.Executables[0]
			verifiedWheels, err := FilterVerifiedPythonResolverArtifacts(operations.Artifacts, operations.ReusableWheels)
			if err != nil {
				return "", err
			}
			if err := session.ResolveWheels(
				ctx, consumer.EnvironmentLauncher, requirement, interpreter,
				input.Node.Request, input.Sources, verifiedWheels,
			); err != nil {
				return "", err
			}
			if err := session.Stop(ctx); err != nil {
				return "", err
			}
			return operations.Artifacts.OutputHostDir, nil
		},
	}
	resolution, err := providers.ResolveProviderNode(ctx, request, resolver, operations.Store, operations.Validators)
	if err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	if err := operations.recordVerifiedPythonArtifacts(resolution.Bundle); err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	return resolution, consumer, nil
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
