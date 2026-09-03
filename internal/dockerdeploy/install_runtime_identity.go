package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type installedRuntimeIdentityBuildV1 struct {
	Lock      deploy.BuildLockV1
	Candidate BuiltImageCandidate
	Adapted   bool
}

type installedRuntimeIdentityBackendV1 struct {
	inspect  func(context.Context, BuiltImageCandidate, blueprint.Platform) (InspectedImageCandidate, error)
	finalize func(context.Context, providerstore.Store, []FullImageValidationInput, FullImageValidationInput, providers.RequirementProfileOwnerValidator, FullImageValidationRunner, deploy.ApplicationStartupVerifierV1, deploy.ApplicationLocalAccountV1, RunOptions) (FinalizedBuildValidationResult, error)
	remove   func(context.Context, BuiltImageCandidate) error
}

func buildInstalledRuntimeIdentityV1(
	ctx context.Context,
	store providerstore.Store,
	source CurrentBuild,
	plan DockerExecutionPlan,
	options RunOptions,
) (installedRuntimeIdentityBuildV1, error) {
	return buildInstalledRuntimeIdentityWithV1(ctx, store, source, plan, options, installedRuntimeIdentityBackendV1{
		inspect: InspectBuiltImageCandidate, finalize: ValidateAndFinalizeBuild, remove: RemoveBuiltImageCandidate,
	})
}

func buildInstalledRuntimeIdentityWithV1(
	ctx context.Context,
	store providerstore.Store,
	source CurrentBuild,
	plan DockerExecutionPlan,
	options RunOptions,
	backend installedRuntimeIdentityBackendV1,
) (installedRuntimeIdentityBuildV1, error) {
	if backend.inspect == nil || backend.finalize == nil || backend.remove == nil {
		return installedRuntimeIdentityBuildV1{}, fmt.Errorf("build installed runtime identity requires a complete backend")
	}
	account, err := applicationLocalAccountV1(plan.Sandbox)
	if err != nil {
		return installedRuntimeIdentityBuildV1{}, fmt.Errorf("prepare installed application local account: %w", err)
	}
	if reflect.DeepEqual(account, source.Lock.RuntimeLayer.Account) {
		return installedRuntimeIdentityBuildV1{Lock: source.Lock}, nil
	}
	upstream, err := backend.inspect(
		ctx,
		BuiltImageCandidate{ImageID: source.Lock.RuntimeLayer.Upstream.ConfigDigest},
		source.Lock.Platform,
	)
	if err != nil {
		return installedRuntimeIdentityBuildV1{}, fmt.Errorf("inspect installed runtime identity upstream: %w", err)
	}
	baseImage, err := realizedImageFromDescriptor(source.Lock.Base)
	if err != nil {
		return installedRuntimeIdentityBuildV1{}, fmt.Errorf("resolve installed runtime identity base: %w", err)
	}
	if source.Lock.RuntimeLayer.Upstream == baseImage {
		if upstream.Image.ConfigDigest != baseImage.ConfigDigest || upstream.Image.RootFSSubject != baseImage.RootFSSubject {
			return installedRuntimeIdentityBuildV1{}, fmt.Errorf("installed runtime identity upstream no longer matches the staged build")
		}
		// Inspection by config ID proves that the selected base still exists,
		// but Docker reports it using a local config-ID descriptor. Restore the
		// locked registry identity before the account-specific runtime rebuild.
		upstream.Descriptor = source.Lock.Base
		upstream.Image = baseImage
	}
	if upstream.Image != source.Lock.RuntimeLayer.Upstream {
		return installedRuntimeIdentityBuildV1{}, fmt.Errorf("installed runtime identity upstream no longer matches the staged build")
	}
	profiles := make([]providers.RequirementProfile, 0, len(source.Lock.Nodes))
	for _, node := range source.Lock.Nodes {
		profiles = append(profiles, node.RequirementProfile)
	}
	// Reattach the locked portable-tool schedule so the republished validation
	// record carries the same portable evidence the staged build proved,
	// rather than silently omitting it.
	portableTools, err := PortableToolValidationScheduleFromBuildLockV1(source.Lock.PortableTools)
	if err != nil {
		return installedRuntimeIdentityBuildV1{}, err
	}
	runner := ProviderFullImageValidationRunner{Store: store}
	options.Context = ctx
	finalized, err := backend.finalize(
		ctx,
		store,
		nil,
		FullImageValidationInput{
			Image: upstream, Profiles: profiles,
			Outputs:       append([]providers.RealizedOutput{}, source.Lock.Catalog...),
			PortableTools: portableTools,
			RuntimePolicy: source.Lock.RuntimePolicy,
		},
		registry.ValidateRequirementProfileV1,
		runner.Run,
		source.Lock.RuntimeLayer.Verifier,
		account,
		options,
	)
	if err != nil {
		return installedRuntimeIdentityBuildV1{}, fmt.Errorf("build installed runtime identity: %w", err)
	}
	lock := source.Lock
	lock.RuntimeLayer = finalized.RuntimeLayer
	lock.ValidationRecord = finalized.Validation.Final.Reference
	lock.FinalImage = finalized.Image.Image
	if err := deploy.ValidateBuildLockV1(lock, registry.ValidateRequirementProfileV1); err != nil {
		cleanupErr := backend.remove(context.WithoutCancel(ctx), finalized.Candidate)
		return installedRuntimeIdentityBuildV1{}, fmt.Errorf("validate installed runtime identity build: %w", errors.Join(err, cleanupErr))
	}
	return installedRuntimeIdentityBuildV1{Lock: lock, Candidate: finalized.Candidate, Adapted: true}, nil
}

func validateInstalledRuntimeIdentityBuildV1(result installedRuntimeIdentityBuildV1, source CurrentBuild, plan DockerExecutionPlan) error {
	if err := deploy.ValidateBuildLockV1(result.Lock, registry.ValidateRequirementProfileV1); err != nil {
		return fmt.Errorf("validate installed runtime build: %w", err)
	}
	wantAccount, err := applicationLocalAccountV1(plan.Sandbox)
	if err != nil {
		return fmt.Errorf("validate installed runtime account: %w", err)
	}
	if !reflect.DeepEqual(result.Lock.RuntimeLayer.Account, wantAccount) {
		return fmt.Errorf("installed runtime build does not match the planned local account")
	}
	if !result.Adapted {
		if !reflect.DeepEqual(result.Lock, source.Lock) || result.Candidate != (BuiltImageCandidate{}) {
			return fmt.Errorf("unadapted installed runtime build must reuse the exact staged build")
		}
		return nil
	}
	if reflect.DeepEqual(result.Lock.RuntimeLayer.Account, source.Lock.RuntimeLayer.Account) {
		return fmt.Errorf("adapted installed runtime build must select a different local account")
	}
	if result.Candidate.ImageID != result.Lock.FinalImage.ConfigDigest {
		return fmt.Errorf("adapted installed runtime candidate does not match its final image")
	}
	return nil
}
