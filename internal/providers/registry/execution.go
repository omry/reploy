package registry

import (
	"bytes"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

// ValidateRequirementProfileV1 dispatches one locked profile to its provider
// owner. It lets whole-graph locks validate mixed APT and Python nodes without
// weakening either provider's exact schema checks.
func ValidateRequirementProfileV1(profile providers.RequirementProfile) error {
	switch profile.Provider {
	case blueprint.ComponentTypeAPT:
		return aptprovider.ValidateRequirementProfileV1(profile)
	case blueprint.ComponentTypePython:
		return pythonprovider.ValidateRequirementProfileV1(profile)
	default:
		return fmt.Errorf("unsupported requirement profile provider %q", profile.Provider)
	}
}

func ValidateResolvedBundlePayloadV1(payload providers.ResolvedBundleIdentityV1) error {
	switch payload.Provider {
	case blueprint.ComponentTypeAPT:
		return aptprovider.ValidateResolvedBundlePayloadV1(payload)
	case blueprint.ComponentTypePython:
		return pythonprovider.ValidateResolvedBundlePayloadV1(payload)
	default:
		return fmt.Errorf("unsupported resolved bundle provider %q", payload.Provider)
	}
}

// OwnerValidatorsForNode returns the provider-owned validators for one
// executable provider node. Base is realized directly and therefore has no
// requirement profile or resolved bundle to validate.
func OwnerValidatorsForNode(node providers.NodeSpec) (providers.ProviderOwnerValidators, error) {
	if err := providers.ValidateNodeSpec(node); err != nil {
		return providers.ProviderOwnerValidators{}, fmt.Errorf("provider registry node: %w", err)
	}
	switch node.Provider {
	case blueprint.ComponentTypePython:
		return providers.ProviderOwnerValidators{
			Profile: pythonprovider.ValidateRequirementProfileV1,
			Bundle:  pythonprovider.ValidateResolvedBundlePayloadV1,
		}, nil
	case blueprint.ComponentTypeBase:
		return providers.ProviderOwnerValidators{}, fmt.Errorf("base root does not have provider resolution validators")
	case blueprint.ComponentTypeAPT:
		return providers.ProviderOwnerValidators{
			Profile: aptprovider.ValidateRequirementProfileV1,
			Bundle:  aptprovider.ValidateResolvedBundlePayloadV1,
		}, nil
	default:
		return providers.ProviderOwnerValidators{}, fmt.Errorf("unsupported provider %q", node.Provider)
	}
}

// MaterializeNode turns a validated provider input into the provider's typed,
// offline materialization transaction. It does not run Docker or access the
// provider store.
func MaterializeNode(node providers.NodeSpec, input providers.MaterializeInput) (providers.MaterializationTransaction, error) {
	if err := providers.ValidateNodeSpec(node); err != nil {
		return providers.MaterializationTransaction{}, fmt.Errorf("provider registry node: %w", err)
	}
	switch node.Provider {
	case blueprint.ComponentTypePython:
		transaction, err := (pythonprovider.ComponentProvider{}).Materialize(input)
		if err != nil {
			return providers.MaterializationTransaction{}, err
		}
		if err := validateMaterializationNodeBinding(node, input); err != nil {
			return providers.MaterializationTransaction{}, err
		}
		return transaction, nil
	case blueprint.ComponentTypeBase:
		return providers.MaterializationTransaction{}, fmt.Errorf("base root does not have a provider materialization operation")
	case blueprint.ComponentTypeAPT:
		transaction, err := (aptprovider.ComponentProvider{}).Materialize(input)
		if err != nil {
			return providers.MaterializationTransaction{}, err
		}
		if err := validateMaterializationNodeBinding(node, input); err != nil {
			return providers.MaterializationTransaction{}, err
		}
		return transaction, nil
	default:
		return providers.MaterializationTransaction{}, fmt.Errorf("unsupported provider %q", node.Provider)
	}
}

func validateMaterializationNodeBinding(node providers.NodeSpec, input providers.MaterializeInput) error {
	payload := input.Bundle.Payload
	if payload.NodeID != node.ID {
		return fmt.Errorf("materialization bundle node %q does not match planned node %q", payload.NodeID, node.ID)
	}
	if payload.Provider != node.Provider {
		return fmt.Errorf("materialization bundle provider %q does not match planned provider %q", payload.Provider, node.Provider)
	}
	planned, err := providers.CanonicalProviderRequestBytes(node.Request)
	if err != nil {
		return fmt.Errorf("planned provider request: %w", err)
	}
	resolved, err := providers.CanonicalProviderRequestBytes(payload.Request)
	if err != nil {
		return fmt.Errorf("resolved provider request: %w", err)
	}
	if !bytes.Equal(planned, resolved) {
		return fmt.Errorf("materialization bundle request does not match planned node %q", node.ID)
	}
	return nil
}
