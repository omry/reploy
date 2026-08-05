package dockerdeploy

import (
	"context"
	"errors"
	"fmt"

	"github.com/omry/reploy/internal/buildprofile"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

type FinalizedBuildValidationResult struct {
	Validation   BuildValidationResult
	RuntimeLayer deploy.ApplicationRuntimeLayerV1
	Image        InspectedImageCandidate
	Candidate    BuiltImageCandidate
}

type finalizationBuilder func(providerstore.Store, FinalizationBuildRequest, RunOptions) (BuiltImageCandidate, error)
type finalizationInspector func(context.Context, BuiltImageCandidate, FinalizationBuildRequest) (InspectedImageCandidate, error)
type finalizationCandidateRemover func(context.Context, BuiltImageCandidate) error
type applicationRuntimeLayerBuilder func(providerstore.Store, ApplicationRuntimeLayerBuildRequest, RunOptions) (BuiltImageCandidate, error)
type applicationRuntimeLayerInspector func(context.Context, BuiltImageCandidate, ApplicationRuntimeLayerBuildRequest) (InspectedImageCandidate, error)
type applicationRuntimeLayerRetainer func(context.Context, BuiltImageCandidate, providers.RealizedImageV1) error

func ValidateAndFinalizeBuild(
	ctx context.Context,
	store providerstore.Store,
	layers []FullImageValidationInput,
	final FullImageValidationInput,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
	runValidation FullImageValidationRunner,
	verifier deploy.ApplicationStartupVerifierV1,
	account deploy.ApplicationLocalAccountV1,
	options RunOptions,
) (FinalizedBuildValidationResult, error) {
	return validateAndFinalizeBuild(
		ctx, store, layers, final, validateProfileOwner, runValidation, verifier, account, options,
		BuildApplicationRuntimeLayerCandidate, InspectApplicationRuntimeLayerCandidate,
		RetainVerifiedApplicationRuntimeLayer,
		BuildFinalizedImageCandidate, InspectFinalizedImageCandidate, RemoveBuiltImageCandidate,
	)
}

func validateAndFinalizeBuild(
	ctx context.Context,
	store providerstore.Store,
	layers []FullImageValidationInput,
	final FullImageValidationInput,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
	runValidation FullImageValidationRunner,
	verifier deploy.ApplicationStartupVerifierV1,
	account deploy.ApplicationLocalAccountV1,
	options RunOptions,
	buildRuntime applicationRuntimeLayerBuilder,
	inspectRuntime applicationRuntimeLayerInspector,
	retainRuntime applicationRuntimeLayerRetainer,
	build finalizationBuilder,
	inspect finalizationInspector,
	remove finalizationCandidateRemover,
) (FinalizedBuildValidationResult, error) {
	if buildRuntime == nil || inspectRuntime == nil || retainRuntime == nil || build == nil || inspect == nil || remove == nil {
		return FinalizedBuildValidationResult{}, fmt.Errorf("final validation pipeline requires build and inspection backends")
	}
	runtimeRequest := ApplicationRuntimeLayerBuildRequest{
		Source: final.Image, Verifier: verifier, Account: account, Platform: final.Image.Descriptor.Platform,
	}
	runtimeBuildCtx, endRuntimeBuild := buildprofile.Start(ctx, "Package application startup verifier")
	options.Context = runtimeBuildCtx
	runtimeCandidate, err := buildRuntime(store, runtimeRequest, options)
	endRuntimeBuild(err)
	if err != nil {
		return FinalizedBuildValidationResult{}, err
	}
	rejectRuntime := func(rejection error) (FinalizedBuildValidationResult, error) {
		return FinalizedBuildValidationResult{}, errors.Join(
			rejection,
			remove(context.WithoutCancel(ctx), runtimeCandidate),
		)
	}
	runtimeInspectCtx, endRuntimeInspect := buildprofile.Start(ctx, "Inspect application runtime layer")
	runtimeImage, err := inspectRuntime(runtimeInspectCtx, runtimeCandidate, runtimeRequest)
	endRuntimeInspect(err)
	if err != nil {
		return rejectRuntime(err)
	}
	runtimeFinal := final
	runtimeFinal.Image = runtimeImage
	validateCtx, endValidate := buildprofile.Start(ctx, "Validate final image")
	validation, err := ValidateBuildImages(validateCtx, store, layers, runtimeFinal, validateProfileOwner, runValidation)
	endValidate(err)
	if err != nil {
		return rejectRuntime(err)
	}
	retainCtx, endRetain := buildprofile.Start(ctx, "Retain application runtime layer")
	err = retainRuntime(retainCtx, runtimeCandidate, runtimeImage.Image)
	endRetain(err)
	if err != nil {
		return rejectRuntime(fmt.Errorf("retain application runtime layer: %w", err))
	}
	transaction, err := deploy.ApplicationRuntimeLayerTransactionDigestV1(verifier, account, final.Image.Image, final.Image.Descriptor.Platform)
	if err != nil {
		return FinalizedBuildValidationResult{}, err
	}
	request := FinalizationBuildRequest{
		Source: runtimeImage, Validation: validation.Final.Record,
		ValidationReference: validation.Final.Reference, Platform: runtimeImage.Descriptor.Platform,
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
		cleanupErr := remove(context.WithoutCancel(ctx), built)
		return FinalizedBuildValidationResult{}, errors.Join(
			fmt.Errorf("inspect finalized validated image: %w", err),
			cleanupErr,
		)
	}
	return FinalizedBuildValidationResult{
		Validation: validation,
		RuntimeLayer: deploy.ApplicationRuntimeLayerV1{
			Schema: deploy.ApplicationRuntimeLayerSchemaV1, Verifier: verifier, Account: account,
			TransactionDigest: transaction, Upstream: final.Image.Image, Result: runtimeImage.Image,
		},
		Image: image, Candidate: built,
	}, nil
}
