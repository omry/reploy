package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

var inspectProviderPrefixImage = InspectBuiltImageCandidate

// ResolveProviderPrefixDescriptor returns the exact immutable Docker
// descriptor for a graph prefix. The selected base is reused directly; later
// local layers are recovered by immutable config ID without creating a tag or
// container.
func ResolveProviderPrefixDescriptor(
	ctx context.Context,
	base deploy.ImageDescriptor,
	prefix providers.RealizedImageV1,
	platform blueprint.Platform,
) (deploy.ImageDescriptor, error) {
	if ctx == nil {
		return deploy.ImageDescriptor{}, fmt.Errorf("resolve provider prefix descriptor requires a context")
	}
	if err := ctx.Err(); err != nil {
		return deploy.ImageDescriptor{}, err
	}
	if err := platform.Validate(); err != nil {
		return deploy.ImageDescriptor{}, fmt.Errorf("provider prefix platform: %w", err)
	}
	baseImage, err := realizedImageFromDescriptor(base)
	if err != nil {
		return deploy.ImageDescriptor{}, fmt.Errorf("provider prefix base descriptor: %w", err)
	}
	if base.Platform != platform {
		return deploy.ImageDescriptor{}, fmt.Errorf("provider prefix base platform %s does not match %s", base.Platform.Canonical, platform.Canonical)
	}
	if err := prefix.Validate(); err != nil {
		return deploy.ImageDescriptor{}, fmt.Errorf("provider prefix image: %w", err)
	}
	if prefix == baseImage {
		return base, nil
	}
	inspected, err := inspectProviderPrefixImage(ctx, BuiltImageCandidate{ImageID: prefix.ConfigDigest}, platform)
	if err != nil {
		return deploy.ImageDescriptor{}, fmt.Errorf("inspect provider prefix %s: %w", prefix.ConfigDigest, err)
	}
	if inspected.Image != prefix {
		return deploy.ImageDescriptor{}, fmt.Errorf("inspected provider prefix does not match the graph image identity")
	}
	return inspected.Descriptor, nil
}

func realizedImageFromDescriptor(descriptor deploy.ImageDescriptor) (providers.RealizedImageV1, error) {
	if err := descriptor.Validate(); err != nil {
		return providers.RealizedImageV1{}, err
	}
	rootFSSubject, err := deploy.RootFSSubject(descriptor.RootFSDiffIDs)
	if err != nil {
		return providers.RealizedImageV1{}, err
	}
	imageDigest := descriptor.ManifestDigest
	if imageDigest == "" {
		imageDigest = descriptor.ConfigDigest
	}
	image := providers.RealizedImageV1{
		Digest: imageDigest, ConfigDigest: descriptor.ConfigDigest, RootFSSubject: rootFSSubject,
	}
	if err := image.Validate(); err != nil {
		return providers.RealizedImageV1{}, err
	}
	return image, nil
}
