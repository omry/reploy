package dockerdeploy

import (
	"context"
	"fmt"
	"reflect"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type ProviderBuildCompletionInput struct {
	Environment      string
	DeploymentDir    string
	Document         blueprint.Document
	DockerPlan       DockerExecutionPlan
	ResolvedRequest  providers.ResolvedRequestV1
	Overlay          deploy.RequestOverlayV1
	PackageOverrides deploy.PackageOverrideIntentV1
	Base             deploy.ImageDescriptor
	BaseCatalog      []providers.RealizedOutput
	Graph            providers.GraphExecutionResult
	Validation       ProviderGraphValidationPlan
	RunValidation    FullImageValidationRunner
	RunOptions       RunOptions
	ValidateChoices  bool
	ValidatedInputs  ValidatedBuildInputsV1
	NoCache          bool
}

type ProviderBuildCompletionResult struct {
	State          deploy.StateV1
	Lock           deploy.BuildLockV1
	Validated      bool
	ValidatedBuild deploy.ValidatedBuildV1
}

type providerBuildCompletionBackend struct {
	validateAndFinalize func(
		context.Context,
		providerstore.Store,
		[]FullImageValidationInput,
		FullImageValidationInput,
		providers.RequirementProfileOwnerValidator,
		FullImageValidationRunner,
		RunOptions,
	) (FinalizedBuildValidationResult, error)
	assemble         func(context.Context, providerstore.Store, BuildLockAssemblyInput) (deploy.BuildLockV1, error)
	publish          func(context.Context, *deploy.OperationLock, providerstore.Store, BuildPublicationInput) (deploy.StateV1, error)
	publishValidated func(context.Context, *deploy.OperationLock, providerstore.Store, string, string, deploy.BuildLockV1, ValidatedBuildInputsV1) (deploy.ValidatedBuildV1, error)
}

// CompleteProviderBuild is the canonical completion path from a materialized
// provider graph to current deployment state. It finalizes validation first,
// assembles the complete immutable lock second, and enters recoverable
// publication last.
func CompleteProviderBuild(
	ctx context.Context,
	operation *deploy.OperationLock,
	store providerstore.Store,
	input ProviderBuildCompletionInput,
) (ProviderBuildCompletionResult, error) {
	return completeProviderBuild(ctx, operation, store, input, providerBuildCompletionBackend{
		validateAndFinalize: ValidateAndFinalizeBuild,
		assemble:            AssembleBuildLock,
		publish:             PublishBuild,
		publishValidated:    PublishValidatedBuild,
	})
}

func completeProviderBuild(
	ctx context.Context,
	operation *deploy.OperationLock,
	store providerstore.Store,
	input ProviderBuildCompletionInput,
	backend providerBuildCompletionBackend,
) (ProviderBuildCompletionResult, error) {
	if ctx == nil {
		return ProviderBuildCompletionResult{}, fmt.Errorf("complete provider build requires a context")
	}
	if err := ctx.Err(); err != nil {
		return ProviderBuildCompletionResult{}, err
	}
	if operation == nil {
		return ProviderBuildCompletionResult{}, fmt.Errorf("complete provider build requires an operation lock")
	}
	if backend.validateAndFinalize == nil || backend.assemble == nil ||
		(input.ValidateChoices && backend.publishValidated == nil) ||
		(!input.ValidateChoices && backend.publish == nil) {
		return ProviderBuildCompletionResult{}, fmt.Errorf("complete provider build requires a complete backend")
	}
	blueprintDigest, err := blueprint.DocumentDigestV1(input.Document)
	if err != nil {
		return ProviderBuildCompletionResult{}, err
	}
	policy, err := providerBuildRuntimePolicyV1(input)
	if err != nil {
		return ProviderBuildCompletionResult{}, err
	}
	if err := validateProviderBuildCompletionInput(input, policy); err != nil {
		return ProviderBuildCompletionResult{}, err
	}

	options := input.RunOptions
	options.Context = ctx
	finalized, err := backend.validateAndFinalize(
		ctx, store, input.Validation.Layers, input.Validation.Final,
		registry.ValidateRequirementProfileV1, input.RunValidation, options,
	)
	if err != nil {
		return ProviderBuildCompletionResult{}, err
	}
	lock, err := backend.assemble(ctx, store, BuildLockAssemblyInput{
		BlueprintDigest: blueprintDigest, ResolvedRequest: input.ResolvedRequest,
		Overlay: input.Overlay, PackageOverrides: input.PackageOverrides,
		Base: input.Base, Graph: input.Graph,
		RuntimePolicy:    policy,
		ValidationRecord: finalized.Validation.Final.Reference, FinalImage: finalized.Image.Image,
	})
	if err != nil {
		return ProviderBuildCompletionResult{}, err
	}
	if input.ValidateChoices {
		record, err := backend.publishValidated(
			ctx, operation, store, input.Environment, input.DeploymentDir, lock, input.ValidatedInputs,
		)
		if err != nil {
			return ProviderBuildCompletionResult{}, err
		}
		state, found, err := operation.ReadStateV1()
		if err != nil {
			return ProviderBuildCompletionResult{}, err
		}
		if !found {
			return ProviderBuildCompletionResult{}, fmt.Errorf("validated build state disappeared")
		}
		return ProviderBuildCompletionResult{
			State: state, Lock: lock, Validated: true, ValidatedBuild: record,
		}, nil
	}
	state, err := backend.publish(ctx, operation, store, BuildPublicationInput{
		Environment: input.Environment, DeploymentDir: input.DeploymentDir,
		Document: input.Document, Lock: lock, NoCache: input.NoCache,
	})
	if err != nil {
		return ProviderBuildCompletionResult{}, err
	}
	return ProviderBuildCompletionResult{State: state, Lock: lock}, nil
}

func providerBuildRuntimePolicyV1(input ProviderBuildCompletionInput) (deploy.RuntimePolicyV1, error) {
	plans, err := RuntimePlansV1(input.Document, input.DockerPlan)
	if err != nil {
		return deploy.RuntimePolicyV1{}, err
	}
	return CompileRuntimePolicyV1(input.Document, input.Graph, plans)
}

func validateProviderBuildCompletionInput(input ProviderBuildCompletionInput, policy deploy.RuntimePolicyV1) error {
	if err := providers.ValidateResolvedRequestV1(input.ResolvedRequest, registry.ValidateResolvedRequestOwnersV1); err != nil {
		return err
	}
	candidateRequest, err := BuildResolvedRequestWithPackageOverridesV1(
		input.Document, input.Overlay, input.PackageOverrides, input.ResolvedRequest.Platform,
		append([]providers.ResolvedSourceInput{}, input.ResolvedRequest.Sources...),
	)
	if err != nil {
		return fmt.Errorf("provider build derive resolved request: %w", err)
	}
	expectedRequest, expectedOverrides, err := finalizeResolvedRequestV1(
		input.Document, input.Overlay, input.PackageOverrides, candidateRequest, input.Graph,
	)
	if err != nil {
		return fmt.Errorf("provider build resolved request does not match the completed graph: %w", err)
	}
	if !reflect.DeepEqual(expectedRequest, input.ResolvedRequest) ||
		!reflect.DeepEqual(expectedOverrides, input.PackageOverrides) {
		return fmt.Errorf("provider build resolved request does not match the document, overlay, platform, and selected sources")
	}
	if err := blueprint.ValidateSelectedPlatform(input.Document, input.ResolvedRequest.Platform); err != nil {
		return fmt.Errorf("provider build platform: %w", err)
	}
	overlayDigest, err := deploy.RequestOverlayDigestV1(input.Overlay)
	if err != nil {
		return err
	}
	if overlayDigest != input.ResolvedRequest.OverlayDigest {
		return fmt.Errorf("provider build overlay does not match the resolved request")
	}
	planned, err := registry.Plan(providers.PlanInput{
		Components: input.ResolvedRequest.Components,
		Platform:   input.ResolvedRequest.Platform,
	})
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(planned, input.Graph.Plan) {
		return fmt.Errorf("provider build graph plan does not match the resolved request")
	}
	if input.Base.Platform != input.ResolvedRequest.Platform {
		return fmt.Errorf("provider build base platform does not match the resolved request")
	}
	if err := validateProviderGraphValidationShape(input.Base, input.BaseCatalog, input.Graph, policy); err != nil {
		return err
	}
	if len(input.Validation.Layers) != len(input.Graph.Materializations) {
		return fmt.Errorf("provider build validation layers do not match graph materializations")
	}
	profiles := []providers.RequirementProfile{}
	outputs := append([]providers.RealizedOutput{}, input.BaseCatalog...)
	for index, materialized := range input.Graph.Materializations {
		profiles = append(profiles, input.Graph.Profiles[index])
		outputs = append(outputs, materialized.Outputs...)
		layer := input.Validation.Layers[index]
		if layer.Image.Image != materialized.Image || !reflect.DeepEqual(layer.Profiles, profiles) || !reflect.DeepEqual(layer.Outputs, outputs) || !reflect.DeepEqual(layer.RuntimePolicy, policy) {
			return fmt.Errorf("provider build validation layer %d does not match its cumulative graph prefix", index+1)
		}
		if err := validateFullImageValidationInput(layer, registry.ValidateRequirementProfileV1); err != nil {
			return err
		}
	}
	if len(input.Graph.Materializations) != 0 {
		last := input.Graph.Materializations[len(input.Graph.Materializations)-1]
		if input.Validation.Final.Image.Image != last.Image ||
			!reflect.DeepEqual(input.Validation.Final.Profiles, profiles) ||
			!reflect.DeepEqual(input.Validation.Final.Outputs, outputs) ||
			!reflect.DeepEqual(input.Validation.Final.RuntimePolicy, policy) {
			return fmt.Errorf("provider build final validation does not match the final graph layer")
		}
		if err := validateFullImageValidationInput(input.Validation.Final, registry.ValidateRequirementProfileV1); err != nil {
			return err
		}
	}
	if len(input.Validation.Layers) != 0 {
		if !reflect.DeepEqual(input.Validation.Final, input.Validation.Layers[len(input.Validation.Layers)-1]) {
			return fmt.Errorf("provider build final validation does not match the final graph layer")
		}
	} else if len(input.Graph.Materializations) == 0 {
		base := input.Graph.PrefixImages[0]
		if input.Validation.Final.Image.Image.ConfigDigest != base.ConfigDigest || input.Validation.Final.Image.Image.RootFSSubject != base.RootFSSubject || len(input.Validation.Final.Profiles) != 0 || !reflect.DeepEqual(input.Validation.Final.Outputs, input.BaseCatalog) || !reflect.DeepEqual(input.Validation.Final.RuntimePolicy, policy) {
			return fmt.Errorf("provider build final validation does not match the base-only graph")
		}
		if err := validateFullImageValidationInput(input.Validation.Final, registry.ValidateRequirementProfileV1); err != nil {
			return err
		}
	}
	return nil
}
