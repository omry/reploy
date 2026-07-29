package dockerdeploy

import (
	"context"
	"fmt"
	"strings"

	"github.com/omry/reploy/internal/providers"
)

const verifiedProviderLayerRepository = "reploy/cache/provider-layer"

// RetainVerifiedProviderLayer gives an accepted provider-layer image a stable,
// content-addressed Docker reference. The build lock records the image config
// digest, and reploy verify must be able to inspect that exact image after the
// temporary build-base reference is removed during finalization.
func RetainVerifiedProviderLayer(
	ctx context.Context,
	candidate BuiltImageCandidate,
	image providers.RealizedImageV1,
) error {
	return retainVerifiedProviderLayer(ctx, candidate, image, runDockerOutput)
}

func retainVerifiedProviderLayer(
	ctx context.Context,
	candidate BuiltImageCandidate,
	image providers.RealizedImageV1,
	run dockerOutputRunner,
) error {
	if ctx == nil {
		return fmt.Errorf("retain provider layer requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := image.Validate(); err != nil {
		return fmt.Errorf("retain provider layer image: %w", err)
	}
	if run == nil {
		return fmt.Errorf("retain provider layer requires a Docker runner")
	}
	reference := verifiedProviderLayerReference(image)
	output, err := run(ctx, "image", "inspect", "--format", "{{.Id}}", reference)
	if err == nil {
		if got := strings.TrimSpace(output); got != string(image.ConfigDigest) {
			return fmt.Errorf(
				"provider layer cache reference %q names config ID %q, want %s",
				reference,
				got,
				image.ConfigDigest,
			)
		}
		return removeBuiltImageCandidate(ctx, candidate, run)
	}
	if !dockerImageInspectReportsMissing(err) {
		return fmt.Errorf("inspect provider layer cache reference %q: %w", reference, err)
	}
	if _, err := run(ctx, "image", "tag", string(image.ConfigDigest), reference); err != nil {
		return fmt.Errorf("create provider layer cache reference %q: %w", reference, err)
	}
	output, inspectErr := run(ctx, "image", "inspect", "--format", "{{.Id}}", reference)
	if inspectErr == nil && strings.TrimSpace(output) == string(image.ConfigDigest) {
		return removeBuiltImageCandidate(ctx, candidate, run)
	}
	cleanupErr := removeExactEnvironmentImageReference(ctx, image, reference, run)
	if inspectErr != nil {
		if cleanupErr != nil {
			return fmt.Errorf(
				"verify provider layer cache reference %q: %v; cleanup failed: %w",
				reference,
				inspectErr,
				cleanupErr,
			)
		}
		return fmt.Errorf("verify provider layer cache reference %q: %w", reference, inspectErr)
	}
	if cleanupErr != nil {
		return fmt.Errorf(
			"provider layer cache reference %q names config ID %q, want %s; cleanup failed: %w",
			reference,
			strings.TrimSpace(output),
			image.ConfigDigest,
			cleanupErr,
		)
	}
	return fmt.Errorf(
		"provider layer cache reference %q names config ID %q, want %s",
		reference,
		strings.TrimSpace(output),
		image.ConfigDigest,
	)
}

func verifiedProviderLayerReference(image providers.RealizedImageV1) string {
	return verifiedProviderLayerRepository + ":sha256-" +
		strings.TrimPrefix(string(image.ConfigDigest), "sha256:")
}
