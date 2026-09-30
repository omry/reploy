package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

type environmentReferenceRemover func(
	context.Context,
	providers.RealizedImageV1,
	EnvironmentImageReferences,
	EnvironmentReferenceKind,
	string,
	string,
) error

func RecoverPendingImageReferences(
	ctx context.Context,
	pending deploy.PendingBuildV1,
	decision deploy.PendingRecoveryDecision,
	oldImage *providers.RealizedImageV1,
	environment string,
	deploymentDir string,
) error {
	return recoverPendingImageReferences(ctx, pending, decision, oldImage, environment, deploymentDir, RemoveEnvironmentImageReference)
}

// RecoverPendingImageReferencesWithLocks also removes the exact retained
// portable intermediate tags. When a retained alias can exist, the locks are
// loaded during recovery preflight so cleanup remains bound to the recorded
// config digest rather than an unverified Docker reference. A provisional
// digest alias is also recorded in pending cleanup before it is created, which
// lets pre-lock discard remove that exact alias without a candidate lock.
func RecoverPendingImageReferencesWithLocks(
	ctx context.Context,
	pending deploy.PendingBuildV1,
	decision deploy.PendingRecoveryDecision,
	oldImage *providers.RealizedImageV1,
	candidateLock *deploy.BuildLockV1,
	oldLock *deploy.BuildLockV1,
	environment string,
	deploymentDir string,
) error {
	if candidateLock == nil && (decision != deploy.PendingRecoveryDiscardCandidate ||
		(pending.Phase != deploy.PendingBuildPhaseValidated && pending.Phase != deploy.PendingBuildPhaseGenerationCreated)) {
		return fmt.Errorf("recover pending image references requires the candidate build lock")
	}
	return recoverPendingImageReferencesWithLocks(
		ctx, pending, decision, oldImage, candidateLock, oldLock,
		environment, deploymentDir, RemoveEnvironmentImageReference,
		RemoveEnvironmentPortableRuntimeLayerReference,
		RemoveEnvironmentPortableRuntimeLayerDigestReference,
		RemoveEnvironmentPortableRuntimeLayerDigestReferenceByReference,
	)
}

func recoverPendingImageReferences(
	ctx context.Context,
	pending deploy.PendingBuildV1,
	decision deploy.PendingRecoveryDecision,
	oldImage *providers.RealizedImageV1,
	environment string,
	deploymentDir string,
	remove environmentReferenceRemover,
) error {
	if ctx == nil {
		return fmt.Errorf("recover pending image references requires a context")
	}
	if err := deploy.ValidatePendingBuild(pending); err != nil {
		return fmt.Errorf("recover pending image references: %w", err)
	}
	if remove == nil {
		return fmt.Errorf("recover pending image references requires a remover")
	}
	references := EnvironmentImageReferences{
		Temporary: pending.Candidate.TemporaryReference, Generation: pending.Candidate.GenerationReference,
	}
	if err := ValidateEnvironmentImageReferences(references, environment, deploymentDir); err != nil {
		return err
	}
	switch decision {
	case deploy.PendingRecoveryDiscardCandidate:
		if err := remove(ctx, pending.Candidate.Image, references, EnvironmentReferenceGeneration, environment, deploymentDir); err != nil {
			return err
		}
		return remove(ctx, pending.Candidate.Image, references, EnvironmentReferenceTemporary, environment, deploymentDir)
	case deploy.PendingRecoveryKeepCandidate:
		if pending.Old != nil {
			if oldImage == nil {
				return fmt.Errorf("recover committed candidate requires the old generation image")
			}
			if oldImage.Digest != pending.Old.ImageDigest || oldImage.RootFSSubject != pending.Old.RootFSSubject {
				return fmt.Errorf("old generation image does not match pending recovery state")
			}
			oldReferences := references
			oldReferences.Generation = pending.Old.Reference
			if err := remove(ctx, *oldImage, oldReferences, EnvironmentReferenceGeneration, environment, deploymentDir); err != nil {
				return err
			}
		}
		return remove(ctx, pending.Candidate.Image, references, EnvironmentReferenceTemporary, environment, deploymentDir)
	case deploy.PendingRecoveryStateConflict:
		return fmt.Errorf("pending publication state conflict; image references were not changed")
	default:
		return fmt.Errorf("pending recovery decision %q is unsupported", decision)
	}
}

func recoverPendingImageReferencesWithLocks(
	ctx context.Context,
	pending deploy.PendingBuildV1,
	decision deploy.PendingRecoveryDecision,
	oldImage *providers.RealizedImageV1,
	candidateLock *deploy.BuildLockV1,
	oldLock *deploy.BuildLockV1,
	environment string,
	deploymentDir string,
	remove environmentReferenceRemover,
	removePortable func(context.Context, providers.RealizedImageV1, string, string, string) error,
	removePortableDigest func(context.Context, providers.RealizedImageV1, string, string) error,
	removePortableDigestReference func(context.Context, string, string, string) error,
) error {
	if removePortable == nil || removePortableDigest == nil {
		return fmt.Errorf("recover pending image references requires portable-layer cleanup")
	}
	if err := recoverPendingImageReferences(ctx, pending, decision, oldImage, environment, deploymentDir, remove); err != nil {
		return err
	}
	removeCandidatePortable := func() error {
		if candidateLock.PortableRuntimeLayer == nil {
			return nil
		}
		image := candidateLock.PortableRuntimeLayer.Result
		if err := removePortable(ctx, image, pending.Candidate.GenerationReference, environment, deploymentDir); err != nil {
			return err
		}
		return removePortableDigest(ctx, image, environment, deploymentDir)
	}
	removeOldPortable := func() error {
		if oldLock == nil || oldLock.PortableRuntimeLayer == nil || pending.Old == nil {
			return nil
		}
		image := oldLock.PortableRuntimeLayer.Result
		if err := removePortable(ctx, image, pending.Old.Reference, environment, deploymentDir); err != nil {
			return err
		}
		return removePortableDigest(ctx, image, environment, deploymentDir)
	}
	removePendingPortableDigest := func() error {
		for _, item := range pending.Cleanup {
			if item.Kind != deploy.CleanupKindTemporaryImageReference {
				continue
			}
			if err := ValidateEnvironmentPortableRuntimeLayerDigestReference(item.Identity, environment, deploymentDir); err != nil {
				continue
			}
			if removePortableDigestReference == nil {
				return fmt.Errorf("pending recovery requires provisional portable-layer cleanup support")
			}
			if err := removePortableDigestReference(ctx, item.Identity, environment, deploymentDir); err != nil {
				return err
			}
		}
		return nil
	}
	switch decision {
	case deploy.PendingRecoveryDiscardCandidate:
		if candidateLock == nil {
			return removePendingPortableDigest()
		}
		return removeCandidatePortable()
	case deploy.PendingRecoveryKeepCandidate:
		if err := removeOldPortable(); err != nil {
			return err
		}
		if candidateLock.PortableRuntimeLayer == nil {
			return nil
		}
		return removePortableDigest(
			ctx, candidateLock.PortableRuntimeLayer.Result, environment, deploymentDir,
		)
	case deploy.PendingRecoveryStateConflict:
		return fmt.Errorf("pending publication state conflict; image references were not changed")
	default:
		return fmt.Errorf("pending recovery decision %q is unsupported", decision)
	}
}
