package dockerdeploy

import (
	"bytes"
	"context"
	"fmt"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	"github.com/omry/reploy/internal/providerstore"
)

// PreparedAPTNodeOperations connects the shared APT resolver session to the
// provider graph while keeping all scratch and artifacts deployment-local.
type PreparedAPTNodeOperations struct {
	Store            providerstore.Store
	Validators       providers.ProviderOwnerValidators
	FinalImageConfig providers.ImageConfigPolicy
	ReusableDebs     []providerstore.ArtifactDescriptor
	ExclusiveRoots   []string
	RunOptions       RunOptions
}

// Prepare creates and removes one deployment-local resolver workspace around
// the one-session cache-validation/fresh-resolution lifecycle.
func (operations PreparedAPTNodeOperations) Prepare(
	ctx context.Context,
	descriptor deploy.ImageDescriptor,
	probeWorkspace PreparedProbeWorkspace,
	request providers.GraphNodePrepareRequest,
) (result providers.GraphNodePreparation, resultErr error) {
	if err := validateAPTReusableArtifacts(request.Resolve.ReusableArtifacts, operations.ReusableDebs); err != nil {
		return providers.GraphNodePreparation{}, err
	}
	resolver, cleanup, err := PrepareAPTResolverWorkspace(operations.Store)
	if err != nil {
		return providers.GraphNodePreparation{}, err
	}
	defer func() {
		if !providerHelperCleanupFailed(resultErr) {
			cleanup()
		}
	}()
	resolver, err = SeedAPTResolverArchives(ctx, operations.Store, resolver, operations.ReusableDebs)
	if err != nil {
		return providers.GraphNodePreparation{}, err
	}
	preparer := APTNodePreparer{
		Descriptor: descriptor, ProbeWorkspace: probeWorkspace, Resolver: resolver, RunOptions: operations.RunOptions,
		ValidateCached: operations.validateCached,
		ResolveFresh:   operations.resolveFresh,
	}
	return preparer.Prepare(ctx, request)
}

func (operations PreparedAPTNodeOperations) validateCached(
	ctx context.Context,
	session *APTResolverSession,
	request providers.ResolveNodeRequest,
	cached providers.ResolveResult,
) (providers.GraphConsumerValidation, error) {
	if err := providers.ValidateProviderNodeResolution(request, cached, operations.Validators); err != nil {
		return providers.GraphConsumerValidation{}, err
	}
	current, err := session.ProbeBaseProfile(ctx)
	if err != nil {
		return providers.GraphConsumerValidation{}, err
	}
	currentFacts, err := aptprovider.CanonicalProfileFactsV1(current.Profile, current.Executables)
	if err != nil {
		return providers.GraphConsumerValidation{}, err
	}
	currentBytes, err := canonical.Marshal(currentFacts)
	if err != nil {
		return providers.GraphConsumerValidation{}, err
	}
	lockedBytes, err := canonical.Marshal(cached.Profile.Facts)
	if err != nil {
		return providers.GraphConsumerValidation{}, err
	}
	if !bytes.Equal(currentBytes, lockedBytes) {
		return providers.GraphConsumerValidation{}, fmt.Errorf("cached APT base and tool evidence no longer matches the current image prefix")
	}
	for _, artifact := range cached.Bundle.Payload.Artifacts {
		if err := operations.Store.VerifyArtifact(artifact); err != nil {
			return providers.GraphConsumerValidation{}, fmt.Errorf("verify cached APT artifact %q: %w", artifact.LogicalPath, err)
		}
	}
	return aptConsumerValidation(current, operations.FinalImageConfig)
}

func (operations PreparedAPTNodeOperations) resolveFresh(
	ctx context.Context,
	session *APTResolverSession,
	request providers.ResolveNodeRequest,
) (providers.ResolveResult, providers.GraphConsumerValidation, error) {
	validation, err := session.ProbeBaseProfile(ctx)
	if err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	consumer, err := aptConsumerValidation(validation, operations.FinalImageConfig)
	if err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	node, found := graphBackendNode(request.Plan, request.NodeID)
	if !found || node.ID != "apt" {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, fmt.Errorf("fresh APT resolution requires the shared apt graph node")
	}
	if err := session.RefreshIndexes(ctx); err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	if _, err := session.PlanPackages(ctx, node.Request); err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	if _, err := session.ReadBasePackageState(ctx); err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	if err := session.DownloadPackages(ctx, node.Request); err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	if _, err := session.InventoryArchives(ctx); err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	if _, err := session.InspectArchives(ctx, operations.ExclusiveRoots); err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	if _, err := session.PublishBundleArtifacts(ctx, operations.Store); err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	resolution, _, err := session.PublishResolvedBundle(ctx, operations.Store, node)
	if err != nil {
		return providers.ResolveResult{}, providers.GraphConsumerValidation{}, err
	}
	return resolution, consumer, nil
}

func aptConsumerValidation(validation APTBaseValidation, finalImageConfig providers.ImageConfigPolicy) (providers.GraphConsumerValidation, error) {
	if err := providers.ValidateImageConfigPolicy(finalImageConfig); err != nil {
		return providers.GraphConsumerValidation{}, err
	}
	var carrier, launcher providers.ValidatedExecutableInput
	for _, executable := range validation.Executables {
		switch executable.Role {
		case providers.ExecutableRoleCarrier:
			carrier = executable
		case providers.ExecutableRoleEnvironmentLauncher:
			launcher = executable
		}
	}
	if err := providers.ValidateValidatedExecutableInput(carrier); err != nil {
		return providers.GraphConsumerValidation{}, fmt.Errorf("APT consumer carrier: %w", err)
	}
	if err := providers.ValidateValidatedExecutableInput(launcher); err != nil {
		return providers.GraphConsumerValidation{}, fmt.Errorf("APT consumer environment launcher: %w", err)
	}
	return providers.GraphConsumerValidation{
		Carrier: carrier, EnvironmentLauncher: launcher, FinalImageConfig: cloneImageConfigPolicy(finalImageConfig),
	}, nil
}

func validateAPTReusableArtifacts(references []providerstore.StoreObjectRef, debs []providerstore.ArtifactDescriptor) error {
	available := make(map[providerstore.StoreObjectRef]bool, len(references))
	for _, reference := range references {
		if err := reference.Validate(); err != nil {
			return err
		}
		available[reference] = true
	}
	seen := map[providerstore.StoreObjectRef]bool{}
	for _, deb := range debs {
		if deb.Kind != "deb" {
			return fmt.Errorf("APT reusable artifact %q is not a deb", deb.LogicalPath)
		}
		reference, err := deb.StoreObjectRef()
		if err != nil {
			return err
		}
		if !available[reference] || seen[reference] {
			return fmt.Errorf("APT reusable deb %q does not uniquely match resolver inputs", deb.LogicalPath)
		}
		seen[reference] = true
	}
	return nil
}
