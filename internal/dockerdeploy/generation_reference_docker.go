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

// removeLegacyEnvironmentGenerationReferenceV1 removes one validated
// deployment-owned generation tag without decoding the obsolete build lock
// that originally selected it. This is restricted to forced legacy-state
// recovery; current-state cleanup must verify the exact realized image.
func removeLegacyEnvironmentGenerationReferenceV1(
	ctx context.Context,
	reference string,
	environment string,
	deploymentDir string,
) error {
	return removeLegacyEnvironmentGenerationReference(
		ctx,
		reference,
		environment,
		deploymentDir,
		runDockerOutput,
	)
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

// CreateEnvironmentPortableRuntimeLayerReference creates the generation-
// lifetime tag for an exact portable intermediate image.
func CreateEnvironmentPortableRuntimeLayerReference(
	ctx context.Context,
	image providers.RealizedImageV1,
	generation string,
	environment string,
	deploymentDir string,
) error {
	reference, err := NewEnvironmentPortableRuntimeLayerReferenceForGeneration(
		image.ConfigDigest, generation, environment, deploymentDir,
	)
	if err != nil {
		return err
	}
	return createPortableRuntimeLayerReference(ctx, image, reference, environment, deploymentDir, false, runDockerOutput)
}

// CreateEnvironmentPortableRuntimeLayerDigestReference creates the
// provisional deployment-owned tag used before publication selects a
// generation reference.
func CreateEnvironmentPortableRuntimeLayerDigestReference(
	ctx context.Context,
	image providers.RealizedImageV1,
	environment string,
	deploymentDir string,
) error {
	reference, err := NewEnvironmentPortableRuntimeLayerReference(
		image.ConfigDigest, environment, deploymentDir,
	)
	if err != nil {
		return err
	}
	return createPortableRuntimeLayerReference(ctx, image, reference, environment, deploymentDir, true, runDockerOutput)
}

func RemoveEnvironmentPortableRuntimeLayerReference(
	ctx context.Context,
	image providers.RealizedImageV1,
	generation string,
	environment string,
	deploymentDir string,
) error {
	reference, err := NewEnvironmentPortableRuntimeLayerReferenceForGeneration(
		image.ConfigDigest, generation, environment, deploymentDir,
	)
	if err != nil {
		return err
	}
	return removePortableRuntimeLayerReference(ctx, image, reference, environment, deploymentDir, false, runDockerOutput)
}

func RemoveEnvironmentPortableRuntimeLayerDigestReference(
	ctx context.Context,
	image providers.RealizedImageV1,
	environment string,
	deploymentDir string,
) error {
	reference, err := NewEnvironmentPortableRuntimeLayerReference(
		image.ConfigDigest, environment, deploymentDir,
	)
	if err != nil {
		return err
	}
	return removePortableRuntimeLayerReference(ctx, image, reference, environment, deploymentDir, true, runDockerOutput)
}

// RemoveEnvironmentPortableRuntimeLayerDigestReferenceByReference removes a
// durable provisional alias using its persisted, deployment-owned identity.
// This is used by pre-lock recovery, where the candidate lock may not exist
// yet but the pending cleanup inventory already records the exact alias.
func RemoveEnvironmentPortableRuntimeLayerDigestReferenceByReference(
	ctx context.Context,
	reference string,
	environment string,
	deploymentDir string,
) error {
	return removePortableRuntimeLayerDigestReferenceByReference(
		ctx, reference, environment, deploymentDir, runDockerOutput,
	)
}

func createPortableRuntimeLayerReference(
	ctx context.Context,
	image providers.RealizedImageV1,
	reference string,
	environment string,
	deploymentDir string,
	digestReference bool,
	run dockerOutputRunner,
) error {
	if ctx == nil {
		return fmt.Errorf("create portable runtime layer reference requires a context")
	}
	if err := image.Validate(); err != nil {
		return fmt.Errorf("create portable runtime layer reference: %w", err)
	}
	var validationErr error
	if digestReference {
		validationErr = ValidateEnvironmentPortableRuntimeLayerDigestReference(reference, environment, deploymentDir)
	} else {
		validationErr = ValidateEnvironmentPortableRuntimeLayerReference(reference, environment, deploymentDir)
	}
	if validationErr != nil {
		return validationErr
	}
	if run == nil {
		return fmt.Errorf("create portable runtime layer reference requires a Docker runner")
	}
	output, err := run(ctx, "image", "ls", "--quiet", "--no-trunc", reference)
	if err != nil {
		return fmt.Errorf("check portable runtime layer reference %q before creation: %w", reference, err)
	}
	if strings.TrimSpace(output) != "" {
		return fmt.Errorf("portable runtime layer reference already exists as %s", strings.TrimSpace(output))
	}
	if _, err := run(ctx, "image", "tag", string(image.Digest), reference); err != nil {
		return fmt.Errorf("create portable runtime layer reference %q: %w", reference, err)
	}
	output, err = run(ctx, "image", "inspect", "--format", "{{.Id}}", reference)
	if err == nil && strings.TrimSpace(output) == string(image.ConfigDigest) {
		return nil
	}
	_, cleanupErr := run(ctx, "image", "rm", reference)
	if err != nil {
		if cleanupErr != nil {
			return fmt.Errorf("verify portable runtime layer reference %q: %v; cleanup failed: %w", reference, err, cleanupErr)
		}
		return fmt.Errorf("verify portable runtime layer reference %q: %w", reference, err)
	}
	mismatch := fmt.Errorf("Docker config ID is %q, want %q", strings.TrimSpace(output), image.ConfigDigest)
	if cleanupErr != nil {
		return fmt.Errorf("verify portable runtime layer reference %q: %v; cleanup failed: %w", reference, mismatch, cleanupErr)
	}
	return fmt.Errorf("verify portable runtime layer reference %q: %w", reference, mismatch)
}

func removePortableRuntimeLayerReference(
	ctx context.Context,
	image providers.RealizedImageV1,
	reference string,
	environment string,
	deploymentDir string,
	digestReference bool,
	run dockerOutputRunner,
) error {
	if ctx == nil {
		return fmt.Errorf("remove portable runtime layer reference requires a context")
	}
	if err := image.Validate(); err != nil {
		return fmt.Errorf("remove portable runtime layer reference: %w", err)
	}
	if run == nil {
		return fmt.Errorf("remove portable runtime layer reference requires a Docker runner")
	}
	if digestReference {
		if err := ValidateEnvironmentPortableRuntimeLayerDigestReference(reference, environment, deploymentDir); err != nil {
			return err
		}
	} else if err := ValidateEnvironmentPortableRuntimeLayerReference(reference, environment, deploymentDir); err != nil {
		return err
	}
	return removeExactEnvironmentImageReference(ctx, image, reference, run)
}

func removePortableRuntimeLayerDigestReferenceByReference(
	ctx context.Context,
	reference string,
	environment string,
	deploymentDir string,
	run dockerOutputRunner,
) error {
	if ctx == nil {
		return fmt.Errorf("remove portable runtime layer digest reference requires a context")
	}
	prefix, err := environmentReferencePrefix(environment, deploymentDir)
	if err != nil {
		return err
	}
	if err := ValidateEnvironmentPortableRuntimeLayerDigestReference(reference, environment, deploymentDir); err != nil {
		return err
	}
	if run == nil {
		return fmt.Errorf("remove portable runtime layer digest reference requires a Docker runner")
	}
	suffix := strings.TrimPrefix(reference, prefix+"d-")
	expected := "sha256:" + suffix
	output, err := run(ctx, "image", "ls", "--quiet", "--no-trunc", reference)
	if err != nil {
		return fmt.Errorf("inspect portable runtime layer digest reference %q for removal: %w", reference, err)
	}
	ids := strings.Fields(output)
	if len(ids) == 0 {
		return nil
	}
	if len(ids) != 1 || ids[0] != expected {
		return fmt.Errorf("portable runtime layer digest reference %q no longer names expected config ID %s", reference, expected)
	}
	if _, err := run(ctx, "image", "rm", "--force", reference); err != nil {
		return fmt.Errorf("remove portable runtime layer digest reference %q: %w", reference, err)
	}
	return nil
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

func removeLegacyEnvironmentGenerationReference(
	ctx context.Context,
	reference string,
	environment string,
	deploymentDir string,
	run dockerOutputRunner,
) error {
	if ctx == nil {
		return fmt.Errorf("remove legacy environment generation reference requires a context")
	}
	if err := ValidateEnvironmentGenerationReference(
		reference,
		environment,
		deploymentDir,
	); err != nil {
		return err
	}
	if run == nil {
		return fmt.Errorf("remove legacy environment generation reference requires a Docker runner")
	}
	output, err := run(ctx, "image", "ls", "--quiet", "--no-trunc", reference)
	if err != nil {
		return fmt.Errorf(
			"inspect legacy environment image reference %q for removal: %w",
			reference,
			err,
		)
	}
	ids := strings.Fields(output)
	if len(ids) == 0 {
		return nil
	}
	if len(ids) != 1 {
		return fmt.Errorf(
			"legacy environment image reference %q resolved to %d images",
			reference,
			len(ids),
		)
	}
	if _, err := run(ctx, "image", "rm", "--force", reference); err != nil {
		return fmt.Errorf(
			"remove legacy environment image reference %q: %w",
			reference,
			err,
		)
	}
	return nil
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
