package providers

import (
	"fmt"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providerstore"
)

const ResolvedBundleSchemaV1 = "resolved-bundle-v1"

type ResolvedBundle struct {
	Identity canonical.Digest         `json:"identity"`
	Payload  ResolvedBundleIdentityV1 `json:"payload"`
}

type ResolvedBundleIdentityV1 struct {
	Schema                   string                             `json:"schema"`
	NodeID                   NodeID                             `json:"node_id"`
	Provider                 blueprint.ComponentType            `json:"provider"`
	Request                  CanonicalProviderRequest           `json:"request"`
	RequirementProfileDigest canonical.Digest                   `json:"requirement_profile_digest"`
	RecipeVersion            string                             `json:"recipe_version"`
	Platform                 blueprint.Platform                 `json:"platform"`
	Upstream                 RealizedImageV1                    `json:"upstream"`
	Artifacts                []providerstore.ArtifactDescriptor `json:"artifacts"`
	Outputs                  []ResolvedOutput                   `json:"outputs"`
	ProviderPayload          CanonicalProviderData              `json:"provider_payload"`
}

type RealizedImageV1 struct {
	Digest        canonical.Digest `json:"digest"`
	ConfigDigest  canonical.Digest `json:"config_digest"`
	RootFSSubject canonical.Digest `json:"rootfs_subject"`
}

type ResolvedOutput struct {
	SupplierComponent string              `json:"supplier_component"`
	SupplierNode      NodeID              `json:"supplier_node"`
	Name              string              `json:"name"`
	Candidate         ExecutableCandidate `json:"candidate"`
}

type RealizedOutput struct {
	SupplierComponent string              `json:"supplier_component"`
	SupplierNode      NodeID              `json:"supplier_node"`
	Name              string              `json:"name"`
	Candidate         ExecutableCandidate `json:"candidate"`
	Evidence          ExecutableEvidence  `json:"evidence"`
}

type ExecutableCandidate struct {
	InvocationPath string                `json:"invocation_path"`
	Provenance     CanonicalProviderData `json:"provenance"`
}

// ResolvedBundleOwnerValidator validates the provider-owned request, payload,
// and nested provenance schemas after common structural checks have passed.
type ResolvedBundleOwnerValidator func(ResolvedBundleIdentityV1) error

func NewResolvedBundle(payload ResolvedBundleIdentityV1, validateOwner ResolvedBundleOwnerValidator) (ResolvedBundle, error) {
	if err := ValidateResolvedBundlePayload(payload, validateOwner); err != nil {
		return ResolvedBundle{}, err
	}
	identity, err := canonical.Sum("bundle", ResolvedBundleSchemaV1, payload)
	if err != nil {
		return ResolvedBundle{}, err
	}
	return ResolvedBundle{Identity: identity, Payload: payload}, nil
}

func ValidateResolvedBundle(bundle ResolvedBundle, validateOwner ResolvedBundleOwnerValidator) error {
	if err := ValidateResolvedBundlePayload(bundle.Payload, validateOwner); err != nil {
		return err
	}
	identity, err := canonical.Sum("bundle", ResolvedBundleSchemaV1, bundle.Payload)
	if err != nil {
		return err
	}
	if bundle.Identity != identity {
		return fmt.Errorf("resolved bundle identity %q does not match payload identity %q", bundle.Identity, identity)
	}
	return nil
}

func ValidateResolvedBundlePayload(payload ResolvedBundleIdentityV1, validateOwner ResolvedBundleOwnerValidator) error {
	if payload.Schema != ResolvedBundleSchemaV1 {
		return fmt.Errorf("resolved bundle schema must be %q", ResolvedBundleSchemaV1)
	}
	if payload.Provider == blueprint.ComponentTypeBase {
		return fmt.Errorf("base root cannot have a resolved provider bundle")
	}
	if err := validateBundleNodeID(payload.NodeID, payload.Provider); err != nil {
		return err
	}
	if payload.Request.Provider != payload.Provider {
		return fmt.Errorf("resolved bundle request provider %q does not match %q", payload.Request.Provider, payload.Provider)
	}
	if _, err := CanonicalProviderRequestBytes(payload.Request); err != nil {
		return err
	}
	if err := payload.RequirementProfileDigest.Validate(); err != nil {
		return fmt.Errorf("resolved bundle requirement profile digest: %w", err)
	}
	if payload.RecipeVersion == "" {
		return fmt.Errorf("resolved bundle recipe version is required")
	}
	if err := payload.Platform.Validate(); err != nil {
		return fmt.Errorf("resolved bundle platform: %w", err)
	}
	if err := payload.Upstream.Validate(); err != nil {
		return fmt.Errorf("resolved bundle upstream: %w", err)
	}
	if payload.Artifacts == nil || payload.Outputs == nil {
		return fmt.Errorf("resolved bundle artifacts and outputs must use arrays")
	}
	for index, artifact := range payload.Artifacts {
		if index > 0 && payload.Artifacts[index-1].LogicalPath >= artifact.LogicalPath {
			return fmt.Errorf("resolved bundle artifacts must be unique and sorted by logical path")
		}
		if err := artifact.Validate(); err != nil {
			return fmt.Errorf("resolved bundle artifact %q: %w", artifact.LogicalPath, err)
		}
	}
	for index, output := range payload.Outputs {
		if index > 0 && compareResolvedOutputs(payload.Outputs[index-1], output) >= 0 {
			return fmt.Errorf("resolved bundle outputs must be unique and sorted by supplier component and name")
		}
		if output.SupplierNode != payload.NodeID {
			return fmt.Errorf("resolved output %s.%s has supplier node %q, want %q", output.SupplierComponent, output.Name, output.SupplierNode, payload.NodeID)
		}
		if err := blueprint.ValidateProviderIdentifier("resolved output component", output.SupplierComponent); err != nil {
			return err
		}
		if err := blueprint.ValidateProviderIdentifier("resolved output name", output.Name); err != nil {
			return err
		}
		if err := validateAbsoluteLinuxPath("resolved output invocation path", output.Candidate.InvocationPath); err != nil {
			return err
		}
		if err := validateCanonicalProviderData("resolved output provenance", output.Candidate.Provenance); err != nil {
			return err
		}
	}
	if err := validateCanonicalProviderData("resolved bundle provider payload", payload.ProviderPayload); err != nil {
		return err
	}
	if validateOwner == nil {
		return fmt.Errorf("resolved bundle provider-owned validator is required")
	}
	if err := validateOwner(payload); err != nil {
		return fmt.Errorf("resolved bundle provider-owned data: %w", err)
	}
	if _, err := canonical.Marshal(payload); err != nil {
		return fmt.Errorf("resolved bundle canonical payload: %w", err)
	}
	return nil
}

func (image RealizedImageV1) Validate() error {
	if err := image.Digest.Validate(); err != nil {
		return fmt.Errorf("image digest: %w", err)
	}
	if err := image.ConfigDigest.Validate(); err != nil {
		return fmt.Errorf("image config digest: %w", err)
	}
	if err := image.RootFSSubject.Validate(); err != nil {
		return fmt.Errorf("image rootfs subject: %w", err)
	}
	return nil
}

func validateBundleNodeID(id NodeID, provider blueprint.ComponentType) error {
	switch provider {
	case blueprint.ComponentTypeAPT:
		if id != "apt" {
			return fmt.Errorf("APT resolved bundle node ID must be %q", "apt")
		}
	case blueprint.ComponentTypePython:
		component, ok := strings.CutPrefix(string(id), "python/")
		if !ok || component == "" || component == "base" {
			return fmt.Errorf("Python resolved bundle node ID must use python/<component>")
		}
		if err := blueprint.ValidateProviderIdentifier("Python bundle component", component); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported resolved bundle provider %q", provider)
	}
	return nil
}

func compareResolvedOutputs(left ResolvedOutput, right ResolvedOutput) int {
	if left.SupplierComponent < right.SupplierComponent {
		return -1
	}
	if left.SupplierComponent > right.SupplierComponent {
		return 1
	}
	return strings.Compare(left.Name, right.Name)
}
