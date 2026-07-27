package dockerdeploy

import (
	"context"
	"errors"
	"fmt"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

type APTCachedNodeValidator func(
	context.Context,
	*APTResolverSession,
	providers.ResolveNodeRequest,
	providers.ResolveResult,
) (providers.GraphConsumerValidation, error)

type APTFreshNodeResolver func(
	context.Context,
	*APTResolverSession,
	providers.ResolveNodeRequest,
) (providers.ResolveResult, providers.GraphConsumerValidation, error)

// APTNodePreparer owns one resolver-container lifecycle for the shared APT
// node. A rejected cache hit receives exactly one fresh resolution in the same
// session and never changes supplier or prefix.
type APTNodePreparer struct {
	Descriptor     deploy.ImageDescriptor
	ProbeWorkspace PreparedProbeWorkspace
	Resolver       PreparedAPTResolverWorkspace
	RunOptions     RunOptions
	ValidateCached APTCachedNodeValidator
	ResolveFresh   APTFreshNodeResolver
}

var openAPTNodePreparationSession = OpenAPTResolverSession

func (preparer APTNodePreparer) Prepare(
	ctx context.Context,
	request providers.GraphNodePrepareRequest,
) (result providers.GraphNodePreparation, resultErr error) {
	if ctx == nil {
		return providers.GraphNodePreparation{}, fmt.Errorf("prepare APT provider node requires a context")
	}
	if err := ctx.Err(); err != nil {
		return providers.GraphNodePreparation{}, err
	}
	if preparer.ValidateCached == nil || preparer.ResolveFresh == nil {
		return providers.GraphNodePreparation{}, fmt.Errorf("APT node preparer requires cached validation and fresh resolution operations")
	}
	if err := validateAPTPreparationImage(preparer.Descriptor, request.Resolve); err != nil {
		return providers.GraphNodePreparation{}, err
	}
	session, err := openAPTNodePreparationSession(ctx, preparer.Descriptor, preparer.ProbeWorkspace, preparer.Resolver, preparer.RunOptions)
	if err != nil {
		return providers.GraphNodePreparation{}, err
	}
	defer func() {
		if closeErr := session.Close(context.WithoutCancel(ctx)); closeErr != nil {
			result = providers.GraphNodePreparation{}
			resultErr = errors.Join(resultErr, closeErr)
		}
	}()

	var cachedMismatch error
	if request.CachedResolution != nil {
		consumer, validateErr := preparer.ValidateCached(ctx, session, request.Resolve, *request.CachedResolution)
		if validateErr == nil {
			return providers.GraphNodePreparation{Resolution: *request.CachedResolution, Consumer: consumer}, nil
		}
		cachedMismatch = validateErr
		if err := ctx.Err(); err != nil {
			return providers.GraphNodePreparation{}, err
		}
	}

	resolution, consumer, resolveErr := preparer.ResolveFresh(ctx, session, request.Resolve)
	if resolveErr != nil {
		if cachedMismatch != nil {
			return providers.GraphNodePreparation{}, fmt.Errorf("cached APT prefix evidence changed (%v); fresh resolution failed: %w", cachedMismatch, resolveErr)
		}
		return providers.GraphNodePreparation{}, fmt.Errorf("fresh APT resolution failed: %w", resolveErr)
	}
	if err := ctx.Err(); err != nil {
		return providers.GraphNodePreparation{}, err
	}
	return providers.GraphNodePreparation{
		Resolution: resolution, Consumer: consumer, Refreshed: request.CachedResolution != nil,
	}, nil
}

func validateAPTPreparationImage(descriptor deploy.ImageDescriptor, request providers.ResolveNodeRequest) error {
	if err := descriptor.Validate(); err != nil {
		return fmt.Errorf("APT node preparer image descriptor: %w", err)
	}
	if err := request.Platform.Validate(); err != nil {
		return fmt.Errorf("APT node preparer platform: %w", err)
	}
	if descriptor.Platform != request.Platform {
		return fmt.Errorf("APT node preparer image platform %s does not match node platform %s", descriptor.Platform.Canonical, request.Platform.Canonical)
	}
	if err := request.Upstream.Validate(); err != nil {
		return fmt.Errorf("APT node preparer upstream: %w", err)
	}
	image, err := realizedImageFromDescriptor(descriptor)
	if err != nil {
		return fmt.Errorf("APT node preparer image: %w", err)
	}
	if request.Upstream != image {
		return fmt.Errorf("APT node preparer descriptor does not identify the exact upstream image")
	}
	return nil
}
