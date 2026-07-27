package providers

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
)

type BaseOutputValidator func(context.Context, OutputDeclaration, RealizedImageV1) (ExecutableEvidence, error)

func RealizeBaseCatalog(
	ctx context.Context,
	plan ProviderPlanV1,
	image RealizedImageV1,
	validate BaseOutputValidator,
) ([]RealizedOutput, error) {
	if ctx == nil {
		return nil, fmt.Errorf("base output validation context is required")
	}
	if err := ValidateProviderPlanV1(plan); err != nil {
		return nil, err
	}
	if err := image.Validate(); err != nil {
		return nil, fmt.Errorf("base output image: %w", err)
	}
	base, found := providerPlanNode(plan, "base")
	if !found || base.Provider != blueprint.ComponentTypeBase {
		return nil, fmt.Errorf("provider plan has no base root")
	}
	if len(base.OutputDeclarations) != 0 && validate == nil {
		return nil, fmt.Errorf("base output validator is required")
	}
	catalog := make([]RealizedOutput, 0, len(base.OutputDeclarations))
	for _, declaration := range base.OutputDeclarations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		evidence, err := validate(ctx, declaration, image)
		if err != nil {
			return nil, fmt.Errorf("validate base output %q: %w", declaration.Name, err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		output := RealizedOutput{
			SupplierComponent: declaration.SupplierComponent,
			SupplierNode:      "base",
			Name:              declaration.Name,
			Candidate: ExecutableCandidate{
				InvocationPath: declaration.CandidatePath,
				Provenance:     declaration.Provenance,
			},
			Evidence: evidence,
		}
		if err := validateRealizedCatalogOutput(output); err != nil {
			return nil, fmt.Errorf("base output %q: %w", declaration.Name, err)
		}
		catalog = append(catalog, output)
	}
	return catalog, nil
}
