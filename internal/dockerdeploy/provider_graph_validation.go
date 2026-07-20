package dockerdeploy

import (
	"context"
	"fmt"
	"reflect"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
)

type ProviderGraphValidationPlan struct {
	Layers []FullImageValidationInput
	Final  FullImageValidationInput
}

type providerGraphImageInspector func(context.Context, BuiltImageCandidate, blueprint.Platform) (InspectedImageCandidate, error)

// PrepareProviderGraphValidation converts a completed provider graph into the
// cumulative immutable-image checks used by final validation. Every validation
// target is re-inspected by config ID; graph records alone are not accepted as
// proof that the local image still exists unchanged.
func PrepareProviderGraphValidation(
	ctx context.Context,
	base deploy.ImageDescriptor,
	baseCatalog []providers.RealizedOutput,
	graph providers.GraphExecutionResult,
	policy deploy.RuntimePolicyV1,
) (ProviderGraphValidationPlan, error) {
	return prepareProviderGraphValidation(ctx, base, baseCatalog, graph, policy, InspectBuiltImageCandidate)
}

func prepareProviderGraphValidation(
	ctx context.Context,
	base deploy.ImageDescriptor,
	baseCatalog []providers.RealizedOutput,
	graph providers.GraphExecutionResult,
	policy deploy.RuntimePolicyV1,
	inspect providerGraphImageInspector,
) (ProviderGraphValidationPlan, error) {
	if ctx == nil {
		return ProviderGraphValidationPlan{}, fmt.Errorf("prepare provider graph validation requires a context")
	}
	if err := ctx.Err(); err != nil {
		return ProviderGraphValidationPlan{}, err
	}
	if inspect == nil {
		return ProviderGraphValidationPlan{}, fmt.Errorf("prepare provider graph validation requires an image inspector")
	}
	if err := validateProviderGraphValidationShape(base, baseCatalog, graph, policy); err != nil {
		return ProviderGraphValidationPlan{}, err
	}

	profiles := []providers.RequirementProfile{}
	outputs := append([]providers.RealizedOutput{}, baseCatalog...)
	layers := make([]FullImageValidationInput, 0, len(graph.Materializations))
	for index, materialized := range graph.Materializations {
		if err := ctx.Err(); err != nil {
			return ProviderGraphValidationPlan{}, err
		}
		candidate, err := inspect(ctx, BuiltImageCandidate{ImageID: materialized.Image.ConfigDigest}, base.Platform)
		if err != nil {
			return ProviderGraphValidationPlan{}, fmt.Errorf("inspect provider graph layer %d: %w", index+1, err)
		}
		if candidate.Image != materialized.Image {
			return ProviderGraphValidationPlan{}, fmt.Errorf("provider graph layer %d changed after materialization", index+1)
		}
		profiles = append(profiles, graph.Profiles[index])
		outputs = append(outputs, materialized.Outputs...)
		input := FullImageValidationInput{
			Image: candidate, Profiles: append([]providers.RequirementProfile{}, profiles...),
			Outputs: append([]providers.RealizedOutput{}, outputs...), RuntimePolicy: policy,
		}
		if err := validateFullImageValidationInput(input, registry.ValidateRequirementProfileV1); err != nil {
			return ProviderGraphValidationPlan{}, fmt.Errorf("prepare provider graph layer %d validation: %w", index+1, err)
		}
		layers = append(layers, input)
	}

	var final FullImageValidationInput
	if len(layers) != 0 {
		final = layers[len(layers)-1]
	} else {
		candidate, err := inspect(ctx, BuiltImageCandidate{ImageID: base.ConfigDigest}, base.Platform)
		if err != nil {
			return ProviderGraphValidationPlan{}, fmt.Errorf("inspect provider graph base: %w", err)
		}
		if candidate.Image.RootFSSubject != graph.PrefixImages[0].RootFSSubject || candidate.Image.ConfigDigest != graph.PrefixImages[0].ConfigDigest {
			return ProviderGraphValidationPlan{}, fmt.Errorf("provider graph base changed after resolution")
		}
		final = FullImageValidationInput{
			Image: candidate, Profiles: []providers.RequirementProfile{},
			Outputs: append([]providers.RealizedOutput{}, baseCatalog...), RuntimePolicy: policy,
		}
		if err := validateFullImageValidationInput(final, registry.ValidateRequirementProfileV1); err != nil {
			return ProviderGraphValidationPlan{}, fmt.Errorf("prepare provider graph base validation: %w", err)
		}
	}
	return ProviderGraphValidationPlan{Layers: layers, Final: final}, nil
}

func validateProviderGraphValidationShape(
	base deploy.ImageDescriptor,
	baseCatalog []providers.RealizedOutput,
	graph providers.GraphExecutionResult,
	policy deploy.RuntimePolicyV1,
) error {
	if err := base.Validate(); err != nil {
		return fmt.Errorf("prepare provider graph validation base: %w", err)
	}
	if err := providers.ValidateProviderPlanV1(graph.Plan); err != nil {
		return err
	}
	if baseCatalog == nil || graph.Profiles == nil || graph.Bundles == nil || graph.ValidationEvidence == nil || graph.PrefixImages == nil || graph.Materializations == nil || graph.Catalog == nil {
		return fmt.Errorf("provider graph validation collections must use arrays")
	}
	count := len(graph.Materializations)
	if len(graph.Profiles) != count || len(graph.Bundles) != count || len(graph.ValidationEvidence) != count || len(graph.PrefixImages) != count+1 {
		return fmt.Errorf("provider graph validation collections do not align")
	}
	baseImage, err := realizedImageFromDescriptor(base)
	if err != nil {
		return err
	}
	if graph.PrefixImages[0] != baseImage {
		return fmt.Errorf("provider graph validation does not start from the selected base")
	}
	catalog := append([]providers.RealizedOutput{}, baseCatalog...)
	for index, materialized := range graph.Materializations {
		if graph.PrefixImages[index+1] != materialized.Image {
			return fmt.Errorf("provider graph validation prefix %d does not match its materialization", index+1)
		}
		catalog = append(catalog, materialized.Outputs...)
	}
	if !reflect.DeepEqual(catalog, graph.Catalog) {
		return fmt.Errorf("provider graph validation catalog does not match cumulative outputs")
	}
	if err := deploy.ValidateRuntimePolicyV1(policy); err != nil {
		return err
	}
	return nil
}
