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

type pendingCompanionRemoverV1 func(deploy.EnvironmentGenerationState, OwnedImageReferenceV1) error

// Validate the complete retained inventory before any reference is changed.
func validatePendingOwnedReferencesV1(pending deploy.PendingBuildV1, environment, dir string) error {
	if err := deploy.ValidatePendingBuild(pending); err != nil {
		return err
	}
	refs := EnvironmentImageReferences{Temporary: pending.Candidate.TemporaryReference, Generation: pending.Candidate.GenerationReference}
	if err := ValidateEnvironmentImageReferences(refs, environment, dir); err != nil {
		return err
	}
	if pending.Old != nil {
		if err := validateOwnedGenerationScopeV1(*pending.Old, environment, dir); err != nil {
			return err
		}
		if len(pending.OldReferences) == 2 {
			if err := validatePortableOwnedReferenceV1(pending.OldReferences[1], *pending.Old, environment, dir); err != nil {
				return err
			}
		}
	}
	if pending.Candidate.Companion != nil {
		return validatePortableOwnedReferenceV1(*pending.Candidate.Companion, *pending.Candidate.Owner, environment, dir)
	}
	return nil
}

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
	companionRemovers ...pendingCompanionRemoverV1,
) error {
	if ctx == nil {
		return fmt.Errorf("recover pending image references requires a context")
	}
	if err := validatePendingOwnedReferencesV1(pending, environment, deploymentDir); err != nil {
		return fmt.Errorf("recover pending image references: %w", err)
	}
	if remove == nil {
		return fmt.Errorf("recover pending image references requires a remover")
	}
	var removeCompanion pendingCompanionRemoverV1
	if len(companionRemovers) == 1 {
		removeCompanion = companionRemovers[0]
	}
	if len(companionRemovers) > 1 || ((pending.Candidate.Companion != nil || len(pending.OldReferences) == 2) && removeCompanion == nil) {
		return fmt.Errorf("portable pending recovery requires an operation-locked companion remover")
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
		if pending.Candidate.Companion != nil {
			if err := removeCompanion(*pending.Candidate.Owner, *pending.Candidate.Companion); err != nil {
				return err
			}
		}
		return remove(ctx, pending.Candidate.Image, references, EnvironmentReferenceTemporary, environment, deploymentDir)
	case deploy.PendingRecoveryKeepCandidate:
		if pending.Old != nil {
			if len(pending.OldReferences) != 0 {
				retained := pending.OldReferences[0].Image
				if oldImage != nil && *oldImage != retained {
					return fmt.Errorf("old recovery image differs from retained cleanup authority")
				}
				oldImage = &retained
			}
			if oldImage == nil {
				return fmt.Errorf("recover committed candidate requires the old generation image")
			}
			if oldImage.Digest != pending.Old.ImageDigest || oldImage.RootFSSubject != pending.Old.RootFSSubject {
				return fmt.Errorf("old generation image does not match pending recovery state")
			}
			if err := oldImage.Validate(); err != nil {
				return err
			}
			oldReferences := references
			oldReferences.Generation = pending.Old.Reference
			if err := remove(ctx, *oldImage, oldReferences, EnvironmentReferenceGeneration, environment, deploymentDir); err != nil {
				return err
			}
			if len(pending.OldReferences) == 2 {
				if err := removeCompanion(*pending.Old, pending.OldReferences[1]); err != nil {
					return err
				}
			}
		}
		return remove(ctx, pending.Candidate.Image, references, EnvironmentReferenceTemporary, environment, deploymentDir)
	case deploy.PendingRecoveryStateConflict:
		return fmt.Errorf("pending publication state conflict; image references were not changed")
	default:
		return fmt.Errorf("pending recovery decision %q is unsupported", decision)
	}
}
