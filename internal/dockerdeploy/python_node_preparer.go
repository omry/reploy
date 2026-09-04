package dockerdeploy

import (
	"context"
	"errors"
	"fmt"

	"github.com/omry/reploy/internal/buildprofile"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

type PythonCachedNodeValidator func(
	context.Context,
	*PythonResolverSession,
	providers.ResolveNodeRequest,
	providers.ResolveResult,
) (providers.GraphConsumerValidation, error)

type PythonFreshNodeResolver func(
	context.Context,
	*PythonResolverSession,
	providers.ResolveNodeRequest,
) (providers.ResolveResult, providers.GraphConsumerValidation, error)

// PythonNodePreparer owns the resolver-container lifecycle for one Python
// graph node. Portable-tool resolution and source-builder environment
// preparation happen before this consumer boundary.
type PythonNodePreparer struct {
	Descriptor     deploy.ImageDescriptor
	Workspace      PreparedProbeWorkspace
	Artifacts      PreparedPythonResolverArtifacts
	ReusableWheels []providerstore.ArtifactDescriptor
	ValidateCached PythonCachedNodeValidator
	ResolveFresh   PythonFreshNodeResolver
}

var openPythonNodePreparationSession = OpenPythonResolverSession

func (preparer PythonNodePreparer) Prepare(
	ctx context.Context,
	request providers.GraphNodePrepareRequest,
) (result providers.GraphNodePreparation, err error) {
	if ctx == nil {
		return providers.GraphNodePreparation{}, fmt.Errorf("prepare Python provider node requires a context")
	}
	if err := ctx.Err(); err != nil {
		return providers.GraphNodePreparation{}, err
	}
	if preparer.ValidateCached == nil || preparer.ResolveFresh == nil {
		return providers.GraphNodePreparation{}, fmt.Errorf("Python node preparer requires cached validation and fresh resolution operations")
	}
	if err := validatePythonPreparationImage(preparer.Descriptor, request.Resolve); err != nil {
		return providers.GraphNodePreparation{}, err
	}
	if err := validatePythonResolverReusableArtifacts(request.Resolve.ReusableArtifacts, preparer.ReusableWheels); err != nil {
		return providers.GraphNodePreparation{}, err
	}
	sessionCtx, endSession := buildprofile.Start(ctx, "Open Python resolver session")
	session, err := openPythonNodePreparationSession(sessionCtx, preparer.Descriptor, preparer.Workspace, preparer.Artifacts)
	endSession(err)
	if err != nil {
		return providers.GraphNodePreparation{}, err
	}
	defer func() {
		if closeErr := session.Close(context.WithoutCancel(ctx)); closeErr != nil {
			result = providers.GraphNodePreparation{}
			err = errors.Join(err, closeErr)
		}
	}()

	var cachedMismatch error
	if request.CachedResolution != nil {
		cachedCtx, endCached := buildprofile.Start(ctx, "Validate cached Python resolution")
		consumer, validateErr := preparer.ValidateCached(cachedCtx, session, request.Resolve, *request.CachedResolution)
		endCached(validateErr)
		if validateErr == nil {
			return providers.GraphNodePreparation{
				Resolution:       *request.CachedResolution,
				Consumer:         consumer,
				EffectiveRequest: &request.CachedResolution.Bundle.Payload.Request,
			}, nil
		}
		cachedMismatch = validateErr
		if err := ctx.Err(); err != nil {
			return providers.GraphNodePreparation{}, err
		}
	}

	resolveCtx, endResolve := buildprofile.Start(ctx, "Resolve fresh Python requirements")
	resolution, consumer, resolveErr := preparer.ResolveFresh(resolveCtx, session, request.Resolve)
	endResolve(resolveErr)
	if resolveErr != nil {
		if cachedMismatch != nil {
			return providers.GraphNodePreparation{}, fmt.Errorf("cached Python prerequisites changed (%v); fresh resolution failed: %w", cachedMismatch, resolveErr)
		}
		return providers.GraphNodePreparation{}, fmt.Errorf("fresh Python resolution failed: %w", resolveErr)
	}
	if err := ctx.Err(); err != nil {
		return providers.GraphNodePreparation{}, err
	}
	return providers.GraphNodePreparation{
		Resolution:       resolution,
		Consumer:         consumer,
		EffectiveRequest: &resolution.Bundle.Payload.Request,
		SourceCandidates: append([]providers.ResolvedSourceInput{}, resolution.SelectedSources...),
		Refreshed:        request.CachedResolution != nil,
	}, nil
}

func validatePythonResolverReusableArtifacts(
	references []providerstore.StoreObjectRef,
	wheels []providerstore.ArtifactDescriptor,
) error {
	available := make(map[providerstore.StoreObjectRef]struct{}, len(references))
	for _, reference := range references {
		if err := reference.Validate(); err != nil {
			return fmt.Errorf("Python resolver reusable reference: %w", err)
		}
		available[reference] = struct{}{}
	}
	seen := map[providerstore.StoreObjectRef]string{}
	for _, wheel := range wheels {
		reference, err := wheel.StoreObjectRef()
		if err != nil {
			return fmt.Errorf("Python resolver reusable wheel %q: %w", wheel.LogicalPath, err)
		}
		if reference.Kind != providerstore.BlobKind || wheel.Kind != "wheel" {
			return fmt.Errorf("Python resolver reusable artifact %q must be a wheel blob", wheel.LogicalPath)
		}
		if prior, found := seen[reference]; found {
			return fmt.Errorf("Python resolver reusable wheels %q and %q use the same blob", prior, wheel.LogicalPath)
		}
		if _, found := available[reference]; !found {
			return fmt.Errorf("Python resolver reusable wheel %q is absent from the node resolver inputs", wheel.LogicalPath)
		}
		seen[reference] = wheel.LogicalPath
	}
	return nil
}

func validatePythonPreparationImage(descriptor deploy.ImageDescriptor, request providers.ResolveNodeRequest) error {
	if err := descriptor.Validate(); err != nil {
		return fmt.Errorf("Python node preparer image descriptor: %w", err)
	}
	if err := request.Platform.Validate(); err != nil {
		return fmt.Errorf("Python node preparer platform: %w", err)
	}
	if descriptor.Platform != request.Platform {
		return fmt.Errorf("Python node preparer image platform %s does not match node platform %s", descriptor.Platform.Canonical, request.Platform.Canonical)
	}
	if err := request.Upstream.Validate(); err != nil {
		return fmt.Errorf("Python node preparer upstream: %w", err)
	}
	image, err := realizedImageFromDescriptor(descriptor)
	if err != nil {
		return fmt.Errorf("Python node preparer image: %w", err)
	}
	if request.Upstream != image {
		return fmt.Errorf("Python node preparer descriptor does not identify the exact upstream image")
	}
	return nil
}
