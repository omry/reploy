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
