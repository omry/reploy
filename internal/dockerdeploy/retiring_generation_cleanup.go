package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

// recoverRetiringGenerationReferencesV1 consumes the former owner's exact
// reference authority retained in staging state. Every removal is retryable;
// the intent is cleared only after both references have been removed.
func recoverRetiringGenerationReferencesV1(
	ctx context.Context,
	operation *deploy.OperationLock,
	deploymentDir string,
	removeFinal func(context.Context, providers.RealizedImageV1, string, string, string) error,
	removePortable func(context.Context, providers.RealizedImageV1, string, string, string) error,
) (bool, error) {
	if ctx == nil || operation == nil || removeFinal == nil {
		return false, fmt.Errorf("retiring generation cleanup requires a complete backend")
	}
	state, found, err := operation.ReadStateV1()
	if err != nil || !found || state.Staging == nil || state.Staging.Retiring == nil {
		return false, err
	}
	retiring := state.Staging.Retiring
	if retiring.PortableRuntimeLayer != nil && removePortable == nil {
		return false, fmt.Errorf("retiring portable generation cleanup requires a remover")
	}
	if err := ValidateEnvironmentGenerationReference(retiring.Generation.Reference, retiring.Environment, deploymentDir); err != nil {
		return false, fmt.Errorf("retiring generation reference: %w", err)
	}
	if retiring.PortableRuntimeLayer != nil {
		if _, err := NewEnvironmentPortableRuntimeLayerReferenceForGeneration(
			retiring.PortableRuntimeLayer.ConfigDigest, retiring.Generation.Reference,
			retiring.Environment, deploymentDir,
		); err != nil {
			return false, fmt.Errorf("retiring portable runtime reference: %w", err)
		}
		if err := removePortable(context.WithoutCancel(ctx), *retiring.PortableRuntimeLayer,
			retiring.Generation.Reference, retiring.Environment, deploymentDir); err != nil {
			return false, fmt.Errorf("remove retiring portable runtime reference: %w", err)
		}
	}
	if err := removeFinal(context.WithoutCancel(ctx), retiring.FinalImage,
		retiring.Generation.Reference, retiring.Environment, deploymentDir); err != nil {
		return false, fmt.Errorf("remove retiring final image reference: %w", err)
	}
	state.Staging.Retiring = nil
	if err := operation.CommitStateV1(state.Current, state); err != nil {
		return false, fmt.Errorf("complete retiring generation cleanup: %w", err)
	}
	return true, nil
}
