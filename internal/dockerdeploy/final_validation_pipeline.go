package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

type FinalizedBuildValidationResult struct {
	Validation BuildValidationResult
	Image      InspectedImageCandidate
}

type finalizationBuilder func(providerstore.Store, FinalizationBuildRequest, RunOptions) (BuiltImageCandidate, error)
type finalizationInspector func(context.Context, BuiltImageCandidate, FinalizationBuildRequest) (InspectedImageCandidate, error)

func ValidateAndFinalizeBuild(
	ctx context.Context,
	store providerstore.Store,
	layers []FullImageValidationInput,
	final FullImageValidationInput,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
	runValidation FullImageValidationRunner,
	options RunOptions,
) (FinalizedBuildValidationResult, error) {
	return validateAndFinalizeBuild(
		ctx, store, layers, final, validateProfileOwner, runValidation, options,
		BuildFinalizedImageCandidate, InspectFinalizedImageCandidate,
	)
}

func validateAndFinalizeBuild(
	ctx context.Context,
	store providerstore.Store,
	layers []FullImageValidationInput,
	final FullImageValidationInput,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
	runValidation FullImageValidationRunner,
	options RunOptions,
	build finalizationBuilder,
	inspect finalizationInspector,
) (FinalizedBuildValidationResult, error) {
	if build == nil || inspect == nil {
		return FinalizedBuildValidationResult{}, fmt.Errorf("final validation pipeline requires build and inspection backends")
	}
	validation, err := ValidateBuildImages(ctx, store, layers, final, validateProfileOwner, runValidation)
	if err != nil {
		return FinalizedBuildValidationResult{}, err
	}
	request := FinalizationBuildRequest{
		Source: final.Image, Validation: validation.Final.Record,
		ValidationReference: validation.Final.Reference, Platform: final.Image.Descriptor.Platform,
	}
	built, err := build(store, request, options)
	if err != nil {
		return FinalizedBuildValidationResult{}, fmt.Errorf("finalize validated image: %w", err)
	}
	image, err := inspect(ctx, built, request)
	if err != nil {
		return FinalizedBuildValidationResult{}, fmt.Errorf("inspect finalized validated image: %w", err)
	}
	return FinalizedBuildValidationResult{Validation: validation, Image: image}, nil
}
