package registry

import (
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

// NormalizePackageOverrideV1 dispatches package identity ownership without
// inspecting a local path or consulting an upstream registry.
func NormalizePackageOverrideV1(
	provider string,
	packageID string,
	choice deploy.PackageOverrideChoiceV1,
) (string, error) {
	switch blueprint.ComponentType(provider) {
	case blueprint.ComponentTypePython:
		if err := blueprint.ValidatePythonDistributionName("Python package override", packageID); err != nil {
			return "", err
		}
		if choice.Version != "" {
			if err := pythonprovider.ValidatePackageVersionV1(choice.Version); err != nil {
				return "", err
			}
		}
		return pythonprovider.NormalizeDistributionName(packageID), nil
	case blueprint.ComponentTypeAPT:
		return "", fmt.Errorf("APT package overrides are not supported in v1")
	default:
		return "", fmt.Errorf("provider %q does not support package overrides", provider)
	}
}
