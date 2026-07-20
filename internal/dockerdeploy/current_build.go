package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type CurrentBuild struct {
	State      deploy.StateV1
	Generation deploy.EnvironmentGenerationState
	Lock       deploy.BuildLockV1
}

type currentBuildReferenceVerifier func(context.Context, providers.RealizedImageV1, string, string, string) error

// ValidateCurrentBuild is read-only and intentionally does not rehash the
// provider-store closure. Runtime operations need the selected lock and one
// Docker reference check; install transfer validates artifacts separately when
// it consumes them. Absence is reported separately, while a state, lock, or
// Docker-reference mismatch is corruption rather than absence.
func ValidateCurrentBuild(
	ctx context.Context,
	operation *deploy.OperationLock,
	store providerstore.Store,
	environment string,
	deploymentDir string,
) (CurrentBuild, bool, error) {
	return validateCurrentBuild(ctx, operation, store, environment, deploymentDir, VerifyEnvironmentGenerationReference)
}

func validateCurrentBuild(
	ctx context.Context,
	operation *deploy.OperationLock,
	store providerstore.Store,
	environment string,
	deploymentDir string,
	verifyReference currentBuildReferenceVerifier,
) (CurrentBuild, bool, error) {
	if ctx == nil {
		return CurrentBuild{}, false, fmt.Errorf("validate current build requires a context")
	}
	if err := ctx.Err(); err != nil {
		return CurrentBuild{}, false, err
	}
	if operation == nil {
		return CurrentBuild{}, false, fmt.Errorf("validate current build requires an operation lock")
	}
	if verifyReference == nil {
		return CurrentBuild{}, false, fmt.Errorf("validate current build requires a reference verifier")
	}
	if err := validatePublicationDeployment(operation, store, deploymentDir); err != nil {
		return CurrentBuild{}, false, err
	}
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return CurrentBuild{}, false, err
	}
	if _, pending, err := operation.ReadPendingBuild(); err != nil {
		return CurrentBuild{}, false, err
	} else if pending {
		return CurrentBuild{}, false, fmt.Errorf("current build has a pending publication; recovery is required")
	}
	if !found || state.Current == nil {
		return CurrentBuild{}, false, nil
	}
	generation := *state.Current
	if err := ValidateEnvironmentGenerationReference(generation.Reference, environment, deploymentDir); err != nil {
		return CurrentBuild{}, false, err
	}
	lock, found, err := operation.ReadBuildLock(generation.BuildLockDigest, registry.ValidateRequirementProfileV1)
	if err != nil {
		return CurrentBuild{}, false, err
	}
	if !found {
		return CurrentBuild{}, false, fmt.Errorf("current build lock %s is missing", generation.BuildLockDigest)
	}
	if err := validateGenerationBuildLock(generation, lock, registry.ValidateRequirementProfileV1); err != nil {
		return CurrentBuild{}, false, fmt.Errorf("current build: %w", err)
	}
	if err := verifyReference(ctx, lock.FinalImage, generation.Reference, environment, deploymentDir); err != nil {
		return CurrentBuild{}, false, err
	}
	return CurrentBuild{State: state, Generation: generation, Lock: lock}, true, nil
}
