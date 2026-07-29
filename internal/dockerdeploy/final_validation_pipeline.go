package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/buildprofile"
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
	validateCtx, endValidate := buildprofile.Start(ctx, "Validate final image")
	validation, err := ValidateBuildImages(validateCtx, store, layers, final, validateProfileOwner, runValidation)
	endValidate(err)
	if err != nil {
		return FinalizedBuildValidationResult{}, err
	}
	request := FinalizationBuildRequest{
		Source: final.Image, Validation: validation.Final.Record,
		ValidationReference: validation.Final.Reference, Platform: final.Image.Descriptor.Platform,
	}
	buildCtx, endBuild := buildprofile.Start(ctx, "Finalize validated image")
	options.Context = buildCtx
	built, err := build(store, request, options)
	endBuild(err)
	if err != nil {
		return FinalizedBuildValidationResult{}, fmt.Errorf("finalize validated image: %w", err)
	}
	inspectCtx, endInspect := buildprofile.Start(ctx, "Inspect finalized image")
	image, err := inspect(inspectCtx, built, request)
	endInspect(err)
	if err != nil {
		return FinalizedBuildValidationResult{}, fmt.Errorf("inspect finalized validated image: %w", err)
	}
	return FinalizedBuildValidationResult{Validation: validation, Image: image}, nil
}
