package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

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

type PythonBuildToolEnvironmentPreparer func(
	context.Context,
	[]string,
) (PythonBuildToolEnvironmentV1, error)

type PythonRetryArtifactsPreparer func() (PreparedPythonResolverArtifacts, func(), error)

// PythonNodePreparer owns the resolver-container lifecycle for one Python
// graph node. A selected local recipe may cause one bounded restart on a
// tool-enabled disposable descendant image; the provider graph upstream does
// not change.
type PythonNodePreparer struct {
	Descriptor            deploy.ImageDescriptor
	Workspace             PreparedProbeWorkspace
	Artifacts             PreparedPythonResolverArtifacts
	ReusableWheels        []providerstore.ArtifactDescriptor
	ValidateCached        PythonCachedNodeValidator
	ResolveFresh          PythonFreshNodeResolver
	PrepareBuildTools     PythonBuildToolEnvironmentPreparer
	PrepareRetryArtifacts PythonRetryArtifactsPreparer
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
	session, err := openPythonNodePreparationSession(ctx, preparer.Descriptor, preparer.Workspace, preparer.Artifacts)
	if err != nil {
		return providers.GraphNodePreparation{}, err
	}
	retryCleanups := []func(){}
	buildToolCleanups := []func(context.Context) error{}
	defer func() {
		if session != nil {
			if closeErr := session.Close(context.WithoutCancel(ctx)); closeErr != nil {
				result = providers.GraphNodePreparation{}
				err = errors.Join(err, closeErr)
			}
		}
		for index := len(buildToolCleanups) - 1; index >= 0; index-- {
			if cleanupErr := buildToolCleanups[index](context.WithoutCancel(ctx)); cleanupErr != nil {
				result = providers.GraphNodePreparation{}
				err = errors.Join(err, fmt.Errorf("remove temporary Python build-tool image: %w", cleanupErr))
			}
		}
		for index := len(retryCleanups) - 1; index >= 0; index-- {
			retryCleanups[index]()
		}
	}()

	closeSession := func() error {
		if session == nil {
			return nil
		}
		closeErr := session.Close(context.WithoutCancel(ctx))
		session = nil
		return closeErr
	}

	var cachedMismatch error
	if request.CachedResolution != nil {
		consumer, validateErr := preparer.ValidateCached(ctx, session, request.Resolve, *request.CachedResolution)
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

	activeTools := []string{}
	for {
		resolution, consumer, resolveErr := preparer.ResolveFresh(ctx, session, request.Resolve)
		var required *pythonBuildToolsRequiredError
		if resolveErr != nil && errors.As(resolveErr, &required) {
			if preparer.PrepareBuildTools == nil || preparer.PrepareRetryArtifacts == nil {
				return providers.GraphNodePreparation{}, fmt.Errorf(
					"local source requires portable build tools %s, but the Python preparer has no build-tool environment",
					required.toolList(),
				)
			}
			nextTools, changed := mergePortableBuildToolsV1(activeTools, required.Tools)
			if !changed {
				return providers.GraphNodePreparation{}, fmt.Errorf(
					"portable build tools %s were requested again after being prepared", required.toolList(),
				)
			}
			if closeErr := closeSession(); closeErr != nil {
				return providers.GraphNodePreparation{}, closeErr
			}
			environment, prepareErr := preparer.PrepareBuildTools(ctx, nextTools)
			if prepareErr != nil {
				return providers.GraphNodePreparation{}, prepareErr
			}
			if environment.Cleanup == nil {
				return providers.GraphNodePreparation{}, fmt.Errorf("portable build-tool environment has no cleanup function")
			}
			buildToolCleanups = append(buildToolCleanups, environment.Cleanup)
			artifacts, cleanup, prepareErr := preparer.PrepareRetryArtifacts()
			if prepareErr != nil {
				return providers.GraphNodePreparation{}, prepareErr
			}
			retryCleanups = append(retryCleanups, cleanup)
			session, prepareErr = openPythonNodePreparationSession(ctx, environment.Descriptor, preparer.Workspace, artifacts)
			if prepareErr != nil {
				return providers.GraphNodePreparation{}, prepareErr
			}
			if _, prepareErr = session.ValidatePortableBuildToolsV1(ctx, nextTools); prepareErr != nil {
				return providers.GraphNodePreparation{}, prepareErr
			}
			activeTools = nextTools
			continue
		}
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
}

type pythonBuildToolsRequiredError struct {
	Tools []string
}

func (err *pythonBuildToolsRequiredError) Error() string {
	return "selected local source requires portable build tools " + err.toolList()
}

func (err *pythonBuildToolsRequiredError) toolList() string {
	return strings.Join(err.Tools, ", ")
}

func mergePortableBuildToolsV1(existing []string, requested []string) ([]string, bool) {
	set := make(map[string]bool, len(existing)+len(requested))
	for _, tool := range existing {
		set[tool] = true
	}
	changed := false
	for _, tool := range requested {
		if !set[tool] {
			changed = true
			set[tool] = true
		}
	}
	result := make([]string, 0, len(set))
	for tool := range set {
		result = append(result, tool)
	}
	sort.Strings(result)
	return result, changed
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
