package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

func AddRequestOverlayOptions(ctx context.Context, dir string, arguments []string) (deploy.RequestOverlayMutationResult, error) {
	options, err := deploy.ParseQualifiedOptionGroups(arguments)
	if err != nil {
		return deploy.RequestOverlayMutationResult{}, err
	}
	return deploy.MutateRequestOverlayV1(ctx, dir, validateOverlayPackageRequest, func(_ blueprint.Document, overlay deploy.RequestOverlayV1) (deploy.RequestOverlayV1, error) {
		return deploy.AddOverlayOptions(overlay, options), nil
	})
}

func RemoveRequestOverlayOptions(ctx context.Context, dir string, arguments []string) (deploy.RequestOverlayMutationResult, error) {
	options, err := deploy.ParseQualifiedOptionGroups(arguments)
	if err != nil {
		return deploy.RequestOverlayMutationResult{}, err
	}
	return deploy.MutateRequestOverlayV1(ctx, dir, validateOverlayPackageRequest, func(_ blueprint.Document, overlay deploy.RequestOverlayV1) (deploy.RequestOverlayV1, error) {
		return deploy.RemoveOverlayOptions(overlay, options)
	})
}

func AddRequestOverlayPackages(ctx context.Context, dir string, component string, requirements []string) (deploy.RequestOverlayMutationResult, error) {
	return mutateRequestOverlayPackages(ctx, dir, component, requirements, false)
}

func RemoveRequestOverlayPackages(ctx context.Context, dir string, component string, requirements []string) (deploy.RequestOverlayMutationResult, error) {
	return mutateRequestOverlayPackages(ctx, dir, component, requirements, true)
}

func mutateRequestOverlayPackages(ctx context.Context, dir string, component string, requirements []string, remove bool) (deploy.RequestOverlayMutationResult, error) {
	return deploy.MutateRequestOverlayV1(ctx, dir, validateOverlayPackageRequest, func(document blueprint.Document, overlay deploy.RequestOverlayV1) (deploy.RequestOverlayV1, error) {
		packages, err := deploy.ParseDirectPackageRequests(document, component, requirements, parseOverlayPackageRequest)
		if err != nil {
			return deploy.RequestOverlayV1{}, err
		}
		if remove {
			return deploy.RemoveOverlayPackages(overlay, packages)
		}
		return deploy.AddOverlayPackages(overlay, packages), nil
	})
}

func parseOverlayPackageRequest(componentType blueprint.ComponentType, requirement string) (providers.CanonicalPackageRequest, error) {
	switch componentType {
	case blueprint.ComponentTypeAPT:
		request, err := blueprint.ParseAPTPackageRequest(requirement)
		if err != nil {
			return providers.CanonicalPackageRequest{}, err
		}
		return aptprovider.CanonicalPackageRequestV1(request)
	case blueprint.ComponentTypePython:
		return pythonprovider.CanonicalPackageRequestV1(requirement)
	default:
		return providers.CanonicalPackageRequest{}, fmt.Errorf("component type %q does not support direct package requests", componentType)
	}
}

func validateOverlayPackageRequest(componentType blueprint.ComponentType, request providers.CanonicalPackageRequest) error {
	switch componentType {
	case blueprint.ComponentTypeAPT:
		return aptprovider.ValidateCanonicalPackageRequestV1(request)
	case blueprint.ComponentTypePython:
		return pythonprovider.ValidateCanonicalPackageRequestV1(request)
	default:
		return fmt.Errorf("component type %q does not support direct package requests", componentType)
	}
}
