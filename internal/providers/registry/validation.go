package registry

import (
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/apt"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

func ValidatePackageRequest(componentType blueprint.ComponentType, request providers.CanonicalPackageRequest) error {
	switch componentType {
	case blueprint.ComponentTypeAPT:
		return apt.ValidateCanonicalPackageRequestV1(request)
	case blueprint.ComponentTypePython:
		return pythonprovider.ValidateCanonicalPackageRequestV1(request)
	case blueprint.ComponentTypeBase:
		return fmt.Errorf("base component does not support package requests")
	default:
		return fmt.Errorf("unsupported component type %q", componentType)
	}
}

func ValidateResolvedRequestOwnersV1(request providers.ResolvedRequestV1) error {
	baseCount := 0
	for _, component := range request.Components {
		var err error
		switch component.Provider {
		case blueprint.ComponentTypeBase:
			baseCount++
			if component.Component != "base" {
				err = fmt.Errorf("base provider request must belong to component base")
			} else {
				err = providers.ValidateCanonicalBaseProviderRequestV1(component.Request)
			}
		case blueprint.ComponentTypeAPT:
			err = apt.ValidateCanonicalProviderRequestForComponentV1(component.Component, component.Request)
		case blueprint.ComponentTypePython:
			err = pythonprovider.ValidateCanonicalProviderRequestForComponentV1(component.Component, component.Request)
		default:
			err = fmt.Errorf("unsupported provider %q", component.Provider)
		}
		if err != nil {
			return fmt.Errorf("component %q: %w", component.Component, err)
		}
	}
	if baseCount != 1 {
		return fmt.Errorf("resolved request must contain exactly one base component")
	}
	return nil
}
