package dockerdeploy

import (
	"context"
	"fmt"
	"strings"

	"github.com/omry/reploy/internal/providers"
)

const verifiedApplicationRuntimeLayerRepository = "reploy/cache/application-runtime-layer"

func RetainVerifiedApplicationRuntimeLayer(
	ctx context.Context,
	candidate BuiltImageCandidate,
	image providers.RealizedImageV1,
) error {
	return retainVerifiedApplicationRuntimeLayer(ctx, candidate, image, runDockerOutput)
}

func retainVerifiedApplicationRuntimeLayer(
	ctx context.Context,
	candidate BuiltImageCandidate,
	image providers.RealizedImageV1,
	run dockerOutputRunner,
) error {
	if ctx == nil {
		return fmt.Errorf("retain application runtime layer requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := image.Validate(); err != nil {
		return fmt.Errorf("retain application runtime layer image: %w", err)
	}
	if run == nil {
		return fmt.Errorf("retain application runtime layer requires a Docker runner")
	}
	reference := verifiedApplicationRuntimeLayerReference(image)
	output, err := run(ctx, "image", "inspect", "--format", "{{.Id}}", reference)
	if err == nil {
		if got := strings.TrimSpace(output); got != string(image.ConfigDigest) {
			return fmt.Errorf("application runtime layer cache reference %q names config ID %q, want %s", reference, got, image.ConfigDigest)
		}
		return removeBuiltImageCandidate(ctx, candidate, run)
	}
	if !dockerImageInspectReportsMissing(err) {
		return fmt.Errorf("inspect application runtime layer cache reference %q: %w", reference, err)
	}
	if _, err := run(ctx, "image", "tag", string(image.ConfigDigest), reference); err != nil {
		return fmt.Errorf("create application runtime layer cache reference %q: %w", reference, err)
	}
	output, inspectErr := run(ctx, "image", "inspect", "--format", "{{.Id}}", reference)
	if inspectErr == nil && strings.TrimSpace(output) == string(image.ConfigDigest) {
		return removeBuiltImageCandidate(ctx, candidate, run)
	}
	cleanupErr := removeExactEnvironmentImageReference(ctx, image, reference, run)
	if inspectErr != nil {
		if cleanupErr != nil {
			return fmt.Errorf("verify application runtime layer cache reference %q: %v; cleanup failed: %w", reference, inspectErr, cleanupErr)
		}
		return fmt.Errorf("verify application runtime layer cache reference %q: %w", reference, inspectErr)
	}
	if cleanupErr != nil {
		return fmt.Errorf("application runtime layer cache reference %q names config ID %q, want %s; cleanup failed: %w", reference, strings.TrimSpace(output), image.ConfigDigest, cleanupErr)
	}
	return fmt.Errorf("application runtime layer cache reference %q names config ID %q, want %s", reference, strings.TrimSpace(output), image.ConfigDigest)
}

func verifiedApplicationRuntimeLayerReference(image providers.RealizedImageV1) string {
	return verifiedApplicationRuntimeLayerRepository + ":sha256-" + strings.TrimPrefix(string(image.ConfigDigest), "sha256:")
}
