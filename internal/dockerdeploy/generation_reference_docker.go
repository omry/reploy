package dockerdeploy

import (
	"context"
	"fmt"
	"strings"

	"github.com/omry/reploy/internal/providers"
)

type EnvironmentReferenceKind string

const (
	EnvironmentReferenceTemporary  EnvironmentReferenceKind = "temporary"
	EnvironmentReferenceGeneration EnvironmentReferenceKind = "generation"
)

func CreateEnvironmentImageReference(
	ctx context.Context,
	image providers.RealizedImageV1,
	references EnvironmentImageReferences,
	kind EnvironmentReferenceKind,
	environment string,
	deploymentDir string,
) error {
	return createEnvironmentImageReference(ctx, image, references, kind, environment, deploymentDir, runDockerOutput)
}

func RemoveEnvironmentImageReference(
	ctx context.Context,
	image providers.RealizedImageV1,
	references EnvironmentImageReferences,
	kind EnvironmentReferenceKind,
	environment string,
	deploymentDir string,
) error {
	return removeEnvironmentImageReference(ctx, image, references, kind, environment, deploymentDir, runDockerOutput)
}

// RemoveEnvironmentGenerationReference removes one state-recorded,
// deployment-owned generation reference after verifying its exact image ID.
func RemoveEnvironmentGenerationReference(
	ctx context.Context,
	image providers.RealizedImageV1,
	reference string,
	environment string,
	deploymentDir string,
) error {
	return removeEnvironmentGenerationReference(ctx, image, reference, environment, deploymentDir, runDockerOutput)
}

func VerifyEnvironmentGenerationReference(
	ctx context.Context,
	image providers.RealizedImageV1,
	reference string,
	environment string,
	deploymentDir string,
) error {
	return verifyEnvironmentGenerationReference(ctx, image, reference, environment, deploymentDir, runDockerOutput)
}

func verifyEnvironmentGenerationReference(
	ctx context.Context,
	image providers.RealizedImageV1,
	reference string,
	environment string,
	deploymentDir string,
	run dockerOutputRunner,
) error {
	if ctx == nil {
		return fmt.Errorf("verify environment generation reference requires a context")
	}
	if err := image.Validate(); err != nil {
		return fmt.Errorf("verify environment generation reference: %w", err)
	}
	if err := ValidateEnvironmentGenerationReference(reference, environment, deploymentDir); err != nil {
		return err
	}
	if run == nil {
		return fmt.Errorf("verify environment generation reference requires a Docker runner")
	}
	output, err := run(ctx, "image", "inspect", "--format", "{{.Id}}", reference)
	if err != nil {
		return fmt.Errorf("inspect environment generation reference %q: %w", reference, err)
	}
	if strings.TrimSpace(output) != string(image.ConfigDigest) {
		return fmt.Errorf("environment generation reference %q names config ID %q, want %s", reference, strings.TrimSpace(output), image.ConfigDigest)
	}
	return nil
}

func removeEnvironmentImageReference(
	ctx context.Context,
	image providers.RealizedImageV1,
	references EnvironmentImageReferences,
	kind EnvironmentReferenceKind,
	environment string,
	deploymentDir string,
	run dockerOutputRunner,
) error {
	if ctx == nil {
		return fmt.Errorf("remove environment image reference requires a context")
	}
	if err := image.Validate(); err != nil {
		return fmt.Errorf("remove environment image reference: %w", err)
	}
	if err := ValidateEnvironmentImageReferences(references, environment, deploymentDir); err != nil {
		return err
	}
	reference, err := selectEnvironmentReference(references, kind)
	if err != nil {
		return err
	}
	if run == nil {
		return fmt.Errorf("remove environment image reference requires a Docker runner")
	}
	return removeExactEnvironmentImageReference(ctx, image, reference, run)
}

func removeEnvironmentGenerationReference(
	ctx context.Context,
	image providers.RealizedImageV1,
	reference string,
	environment string,
	deploymentDir string,
	run dockerOutputRunner,
) error {
	if ctx == nil {
		return fmt.Errorf("remove environment generation reference requires a context")
	}
	if err := image.Validate(); err != nil {
		return fmt.Errorf("remove environment generation reference: %w", err)
	}
	if err := ValidateEnvironmentGenerationReference(reference, environment, deploymentDir); err != nil {
		return err
	}
	if run == nil {
		return fmt.Errorf("remove environment generation reference requires a Docker runner")
	}
	return removeExactEnvironmentImageReference(ctx, image, reference, run)
}

func removeExactEnvironmentImageReference(
	ctx context.Context,
	image providers.RealizedImageV1,
	reference string,
	run dockerOutputRunner,
) error {
	output, err := run(ctx, "image", "ls", "--quiet", "--no-trunc", reference)
	if err != nil {
		return fmt.Errorf("inspect environment image reference %q for removal: %w", reference, err)
	}
	ids := strings.Fields(output)
	if len(ids) == 0 {
		return nil
	}
	if len(ids) != 1 || ids[0] != string(image.ConfigDigest) {
		return fmt.Errorf("environment image reference %q no longer names expected config ID %s", reference, image.ConfigDigest)
	}
	// Docker rejects an ordinary untag when any container was created from
	// this reference, including an exited container. Force applies only to the
	// exact deployment-owned tag verified above; Docker retains the container
	// and its immutable image while removing the stale repository reference.
	if _, err := run(ctx, "image", "rm", "--force", reference); err != nil {
		return fmt.Errorf("remove environment image reference %q: %w", reference, err)
	}
	return nil
}

func createEnvironmentImageReference(
	ctx context.Context,
	image providers.RealizedImageV1,
	references EnvironmentImageReferences,
	kind EnvironmentReferenceKind,
	environment string,
	deploymentDir string,
	run dockerOutputRunner,
) error {
	if ctx == nil {
		return fmt.Errorf("create environment image reference requires a context")
	}
	if err := image.Validate(); err != nil {
		return fmt.Errorf("create environment image reference: %w", err)
	}
	if err := ValidateEnvironmentImageReferences(references, environment, deploymentDir); err != nil {
		return err
	}
	reference, err := selectEnvironmentReference(references, kind)
	if err != nil {
		return err
	}
	if run == nil {
		return fmt.Errorf("create environment image reference requires a Docker runner")
	}
	if output, err := run(ctx, "image", "inspect", "--format", "{{.Id}}", reference); err == nil {
		return fmt.Errorf("environment image reference already exists as %s", strings.TrimSpace(output))
	}
	if _, err := run(ctx, "image", "tag", string(image.Digest), reference); err != nil {
		return fmt.Errorf("create environment image reference %q: %w", reference, err)
	}
	output, inspectErr := run(ctx, "image", "inspect", "--format", "{{.Id}}", reference)
	if inspectErr == nil && strings.TrimSpace(output) == string(image.ConfigDigest) {
		return nil
	}
	_, cleanupErr := run(ctx, "image", "rm", reference)
	if inspectErr != nil {
		if cleanupErr != nil {
			return fmt.Errorf("verify environment image reference %q: %v; cleanup failed: %w", reference, inspectErr, cleanupErr)
		}
		return fmt.Errorf("verify environment image reference %q: %w", reference, inspectErr)
	}
	mismatch := fmt.Errorf("Docker config ID is %q, want %q", strings.TrimSpace(output), image.ConfigDigest)
	if cleanupErr != nil {
		return fmt.Errorf("verify environment image reference %q: %v; cleanup failed: %w", reference, mismatch, cleanupErr)
	}
	return fmt.Errorf("verify environment image reference %q: %w", reference, mismatch)
}

func selectEnvironmentReference(references EnvironmentImageReferences, kind EnvironmentReferenceKind) (string, error) {
	switch kind {
	case EnvironmentReferenceTemporary:
		return references.Temporary, nil
	case EnvironmentReferenceGeneration:
		return references.Generation, nil
	default:
		return "", fmt.Errorf("environment image reference kind %q is unsupported", kind)
	}
}
