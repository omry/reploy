package dockerdeploy

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
)

// OwnedImageReferenceV1 is an exact pair retained by a generation owner or its
// pending cleanup inventory. Its presence is not proof of build acceptance.
type OwnedImageReferenceV1 = deploy.OwnedImageReferenceV1

// ProjectEnvironmentOwnedReferencesV1 returns the primary pair first, followed
// by the optional portable companion. The accepted lock is the only source of
// portable image identity; no Docker access or persisted-state change occurs.
func ProjectEnvironmentOwnedReferencesV1(
	generation deploy.EnvironmentGenerationState,
	lock deploy.BuildLockV1,
	environment, deploymentDir string,
) ([]OwnedImageReferenceV1, error) {
	return projectEnvironmentOwnedReferencesV1(generation, lock, environment, deploymentDir, registry.ValidateRequirementProfileV1)
}

// Recovery can project a retained owner under the explicit no-cache policy.
// New publication continues to use the strict public entry point above.
func projectEnvironmentOwnedReferencesV1(
	generation deploy.EnvironmentGenerationState,
	lock deploy.BuildLockV1,
	environment, deploymentDir string,
	validateProfile providers.RequirementProfileOwnerValidator,
) ([]OwnedImageReferenceV1, error) {
	if err := validateOwnedGenerationScopeV1(generation, environment, deploymentDir); err != nil {
		return nil, err
	}
	if err := validateGenerationBuildLock(generation, lock, validateProfile); err != nil {
		return nil, fmt.Errorf("project owned references: %w", err)
	}
	if lock.PackageOverrides.EnvironmentID != environment {
		return nil, fmt.Errorf("owned generation lock names another environment")
	}
	pairs := []OwnedImageReferenceV1{{Reference: generation.Reference, Image: lock.FinalImage}}
	if lock.PortableRuntimeLayer != nil {
		reference, err := portableGenerationReferenceV1(generation.Reference, environment, deploymentDir)
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, OwnedImageReferenceV1{Reference: reference, Image: lock.PortableRuntimeLayer.Result})
	}
	return pairs, nil
}

func validateOwnedGenerationScopeV1(generation deploy.EnvironmentGenerationState, environment, deploymentDir string) error {
	if err := deploy.ValidateEnvironmentGenerationState(generation); err != nil {
		return fmt.Errorf("owned generation: %w", err)
	}
	if err := blueprint.ValidateEnvironmentID("owned generation environment", environment); err != nil {
		return err
	}
	return ValidateEnvironmentGenerationReference(generation.Reference, environment, deploymentDir)
}

func portableGenerationReferenceV1(generationReference, environment, deploymentDir string) (string, error) {
	if err := blueprint.ValidateEnvironmentID("portable reference environment", environment); err != nil {
		return "", err
	}
	if err := ValidateEnvironmentGenerationReference(generationReference, environment, deploymentDir); err != nil {
		return "", err
	}
	canonicalDir, err := canonicalIdentityPath(deploymentDir)
	if err != nil {
		return "", err
	}
	// New ownership uses full hashes for both the deployment path and the exact
	// environment spelling. Existing primary references retain their algorithm.
	deploymentHash := sha256.Sum256([]byte(canonicalDir))
	environmentHash := sha256.Sum256([]byte(environment))
	_, suffix, _ := strings.Cut(generationReference, ":g-")
	return fmt.Sprintf("reploy/portable/%x/%x:g-%s", deploymentHash, environmentHash, suffix), nil
}

func validatePortableOwnedReferenceV1(owned OwnedImageReferenceV1, generation deploy.EnvironmentGenerationState, environment, deploymentDir string) error {
	if err := validateOwnedGenerationScopeV1(generation, environment, deploymentDir); err != nil {
		return err
	}
	if err := owned.Image.Validate(); err != nil {
		return fmt.Errorf("portable owned image: %w", err)
	}
	expected, err := portableGenerationReferenceV1(generation.Reference, environment, deploymentDir)
	if err != nil {
		return err
	}
	if owned.Reference != expected || owned.Reference == generation.Reference {
		return fmt.Errorf("portable reference is not the exact companion of this generation and scope")
	}
	return nil
}
