package dockerdeploy

import (
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
)

// providerBuildRecoveryValidatorsV1 keeps the common, canonical lock and bundle
// validation intact while allowing an explicit no-cache rebuild to retire
// records whose provider-owned payload schema predates the current binary.
func providerBuildRecoveryValidatorsV1(
	noCache bool,
) (providers.RequirementProfileOwnerValidator, providers.ResolvedBundleOwnerValidator) {
	if noCache {
		return acceptProviderProfileOwnerForCutoverV1, acceptProviderBundleOwnerForCutoverV1
	}
	return registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1
}

func acceptProviderProfileOwnerForCutoverV1(providers.RequirementProfile) error {
	return nil
}

func acceptProviderBundleOwnerForCutoverV1(providers.ResolvedBundleIdentityV1) error {
	return nil
}
