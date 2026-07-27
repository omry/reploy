package dockerdeploy

import (
	"context"
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

type RequestOverlayOptionEntry struct {
	Name        string
	Description string
}

type RequestOverlayEntry struct {
	Kind      string
	Component string
	Value     string
}

func ListRequestOverlayOptions(ctx context.Context, dir string) ([]RequestOverlayOptionEntry, error) {
	document, _, err := readStagedRequestOverlay(ctx, dir)
	if err != nil {
		return nil, err
	}
	entries := []RequestOverlayOptionEntry{}
	for applicationName, application := range document.Environment.Applications {
		for optionName, option := range application.Options {
			entries = append(entries, RequestOverlayOptionEntry{
				Name: applicationName + "/" + optionName, Description: option.Description,
			})
		}
	}
	sort.Slice(entries, func(left int, right int) bool { return entries[left].Name < entries[right].Name })
	return entries, nil
}

func ListRequestOverlay(ctx context.Context, dir string) ([]RequestOverlayEntry, error) {
	_, overlay, err := readStagedRequestOverlay(ctx, dir)
	if err != nil {
		return nil, err
	}
	entries := make([]RequestOverlayEntry, 0, len(overlay.SelectedOptions)+len(overlay.DirectPackages))
	for _, option := range overlay.SelectedOptions {
		entries = append(entries, RequestOverlayEntry{
			Kind: "option", Component: option.Application, Value: option.Application + "/" + option.Option,
		})
	}
	for _, request := range overlay.DirectPackages {
		value, err := displayOverlayPackageRequest(request.Package)
		if err != nil {
			return nil, fmt.Errorf("display package request for contribution %q: %w", request.Contribution, err)
		}
		entries = append(entries, RequestOverlayEntry{Kind: "package", Component: request.Contribution, Value: value})
	}
	return entries, nil
}

func readStagedRequestOverlay(ctx context.Context, dir string) (document blueprint.Document, overlay deploy.RequestOverlayV1, err error) {
	lock, err := deploy.AcquireOperationLock(ctx, dir)
	if err != nil {
		return blueprint.Document{}, deploy.RequestOverlayV1{}, err
	}
	defer func() {
		if unlockErr := lock.Unlock(); err == nil && unlockErr != nil {
			err = unlockErr
		}
	}()
	state, found, err := lock.ReadStateV1()
	if err != nil {
		return blueprint.Document{}, deploy.RequestOverlayV1{}, fmt.Errorf("read deployment state: %w", err)
	}
	if !found {
		return blueprint.Document{}, deploy.RequestOverlayV1{}, fmt.Errorf("deployment state is missing; stage the deployment first")
	}
	if state.Deployment != nil {
		return blueprint.Document{}, deploy.RequestOverlayV1{}, fmt.Errorf("request overlay is only available on a staging deployment")
	}
	document, err = blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return blueprint.Document{}, deploy.RequestOverlayV1{}, fmt.Errorf("load resolved blueprint: %w", err)
	}
	overlay, err = deploy.NormalizeRequestOverlayV1(document, state.Overlay, validateOverlayPackageRequest)
	if err != nil {
		return blueprint.Document{}, deploy.RequestOverlayV1{}, fmt.Errorf("validate request overlay: %w", err)
	}
	return document, overlay, nil
}

func displayOverlayPackageRequest(request providers.CanonicalPackageRequest) (string, error) {
	switch request.Schema {
	case aptprovider.PackageRequestSchemaV1:
		decoded, err := aptprovider.DecodeCanonicalPackageRequestV1(request)
		if err != nil {
			return "", err
		}
		if decoded.Version != "" {
			return decoded.Name + "=" + decoded.Version, nil
		}
		return decoded.Name, nil
	case pythonprovider.PackageRequestSchemaV1:
		if err := pythonprovider.ValidateCanonicalPackageRequestV1(request); err != nil {
			return "", err
		}
		requirement, _ := request.Value["requirement"].(string)
		return requirement, nil
	default:
		return "", fmt.Errorf("unsupported package request schema %q", request.Schema)
	}
}

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
