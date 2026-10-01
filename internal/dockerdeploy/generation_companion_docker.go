package dockerdeploy

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

// CreatePortableEnvironmentReferenceV1 accepts an exact pair already retained
// in durable publication intent. Persisting that intent belongs to the calling
// transition. An existing matching companion is an idempotent retry; every
// observed mismatch is preserved, including one observed after tagging.
func CreatePortableEnvironmentReferenceV1(ctx context.Context, operation *deploy.OperationLock, owned OwnedImageReferenceV1, generation deploy.EnvironmentGenerationState, environment, deploymentDir string) error {
	return createPortableEnvironmentReferenceV1(ctx, operation, owned, generation, environment, deploymentDir, runDockerOutput)
}

// RemovePortableEnvironmentReferenceV1 retires only the exact retained pair.
// Cleanup inventory remains authoritative after the supporting lock is pruned.
func RemovePortableEnvironmentReferenceV1(ctx context.Context, operation *deploy.OperationLock, owned OwnedImageReferenceV1, generation deploy.EnvironmentGenerationState, environment, deploymentDir string) error {
	return removePortableEnvironmentReferenceV1(ctx, operation, owned, generation, environment, deploymentDir, runDockerOutput)
}

func VerifyPortableEnvironmentReferenceV1(ctx context.Context, operation *deploy.OperationLock, owned OwnedImageReferenceV1, generation deploy.EnvironmentGenerationState, environment, deploymentDir string) error {
	return verifyPortableEnvironmentReferenceV1(ctx, operation, owned, generation, environment, deploymentDir, runDockerOutput)
}

func verifyPortableEnvironmentReferenceV1(ctx context.Context, operation *deploy.OperationLock, owned OwnedImageReferenceV1, generation deploy.EnvironmentGenerationState, environment, deploymentDir string, run dockerOutputRunner) error {
	if err := validatePortableReferenceOperationV1(ctx, operation, owned, generation, environment, deploymentDir, run); err != nil {
		return err
	}
	present, err := inspectPortableOwnedImageV1(ctx, owned.Reference, owned, generation.Platform, run)
	if err == nil && !present {
		return fmt.Errorf("portable owned reference %q is missing", owned.Reference)
	}
	return err
}

func createPortableEnvironmentReferenceV1(ctx context.Context, operation *deploy.OperationLock, owned OwnedImageReferenceV1, generation deploy.EnvironmentGenerationState, environment, deploymentDir string, run dockerOutputRunner) error {
	if err := validatePortableReferenceOperationV1(ctx, operation, owned, generation, environment, deploymentDir, run); err != nil {
		return err
	}
	present, err := inspectPortableOwnedImageV1(ctx, owned.Reference, owned, generation.Platform, run)
	if err != nil || present {
		return err
	}
	present, err = inspectPortableOwnedImageV1(ctx, string(owned.Image.Digest), owned, generation.Platform, run)
	if err != nil {
		return err
	}
	if !present {
		return fmt.Errorf("portable owned source image %s is missing", owned.Image.ConfigDigest)
	}
	if _, err := run(ctx, "image", "tag", string(owned.Image.ConfigDigest), owned.Reference); err != nil {
		return fmt.Errorf("create portable owned reference: %w", err)
	}
	present, err = inspectPortableOwnedImageV1(ctx, owned.Reference, owned, generation.Platform, run)
	if err == nil && !present {
		return fmt.Errorf("portable owned reference disappeared after tagging")
	}
	// Never erase an observed mismatch or uncertain result to repair a check.
	return err
}

func removePortableEnvironmentReferenceV1(ctx context.Context, operation *deploy.OperationLock, owned OwnedImageReferenceV1, generation deploy.EnvironmentGenerationState, environment, deploymentDir string, run dockerOutputRunner) error {
	if err := validatePortableReferenceOperationV1(ctx, operation, owned, generation, environment, deploymentDir, run); err != nil {
		return err
	}
	present, err := inspectPortableOwnedImageV1(ctx, owned.Reference, owned, generation.Platform, run)
	if err != nil || !present {
		return err
	}
	// Force removes only this tag, including when an exited container uses it.
	// It does not remove an image ID or any other owner's reference.
	if _, err := run(ctx, "image", "rm", "--force", owned.Reference); err != nil {
		return fmt.Errorf("remove portable owned reference: %w", err)
	}
	return nil
}

func validatePortableReferenceOperationV1(ctx context.Context, operation *deploy.OperationLock, owned OwnedImageReferenceV1, generation deploy.EnvironmentGenerationState, environment, deploymentDir string, run dockerOutputRunner) error {
	if ctx == nil || run == nil {
		return fmt.Errorf("portable reference operation requires a context and Docker runner")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := operation.RequireHeld(); err != nil {
		return err
	}
	expectedPath, err := canonicalIdentityPath(filepath.Join(deploymentDir, ".reploy", "operation.lock"))
	if err != nil {
		return err
	}
	actualPath, err := canonicalIdentityPath(operation.Path())
	if err != nil {
		return err
	}
	if actualPath != expectedPath {
		return fmt.Errorf("portable reference operation lock belongs to another deployment")
	}
	return validatePortableOwnedReferenceV1(owned, generation, environment, deploymentDir)
}

func inspectPortableOwnedImageV1(ctx context.Context, reference string, owned OwnedImageReferenceV1, platform blueprint.Platform, run dockerOutputRunner) (bool, error) {
	output, err := run(ctx, "image", "inspect", reference)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return false, contextErr
		}
		if dockerImageInspectReportsMissing(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspect portable owned image %q: %w", reference, err)
	}
	// A local companion tag has no registry manifest. Validate the observation
	// against its retained immutable config ID using the existing local parser.
	inspection, err := parseDockerImageInspectionDetails(string(owned.Image.ConfigDigest), platform, []byte(output))
	if err != nil {
		return false, err
	}
	rootFS, err := deploy.RootFSSubject(inspection.Descriptor.RootFSDiffIDs)
	if err != nil {
		return false, err
	}
	if inspection.Descriptor.ConfigDigest != owned.Image.ConfigDigest || rootFS != owned.Image.RootFSSubject {
		return false, fmt.Errorf("portable owned reference %q no longer names its exact image", reference)
	}
	return true, nil
}
