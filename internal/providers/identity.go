package providers

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

const (
	ResolvedSourceInputSchemaV1 = "resolved-source-input-v1"
	ResolvedRequestSchemaV1     = "resolved-request-v1"
	ResolverCacheKeySchemaV1    = "resolver-cache-key-v1"
	AssemblyKeySchemaV1         = "assembly-key-v1"
)

type ResolvedSourceInput struct {
	Schema               string                `json:"schema"`
	Component            string                `json:"component"`
	LogicalPackage       string                `json:"logical_package"`
	SourceManifestDigest canonical.Digest      `json:"source_manifest_digest"`
	BuilderProfile       string                `json:"builder_profile"`
	BuildSettings        CanonicalProviderData `json:"build_settings"`
	EcosystemMetadata    CanonicalProviderData `json:"ecosystem_metadata"`
	ArtifactDigest       canonical.Digest      `json:"artifact_digest"`
}

type ResolvedRequestV1 struct {
	Schema        string                       `json:"schema"`
	OverlayDigest canonical.Digest             `json:"overlay_digest"`
	Platform      blueprint.Platform           `json:"platform"`
	Components    []ResolvedComponentRequestV1 `json:"components"`
	Sources       []ResolvedSourceInput        `json:"sources"`
}

type ResolverCacheKeyV1 struct {
	Schema         string             `json:"schema"`
	NodeID         NodeID             `json:"node_id"`
	RequestDigest  canonical.Digest   `json:"request_digest"`
	ProfileDigest  canonical.Digest   `json:"profile_digest"`
	ResolverRecipe string             `json:"resolver_recipe"`
	Platform       blueprint.Platform `json:"platform"`
}

type AssemblyKeyV1 struct {
	Schema             string             `json:"schema"`
	Parent             RealizedImageV1    `json:"parent"`
	TransactionDigest  canonical.Digest   `json:"transaction_digest"`
	RendererProfile    string             `json:"renderer_profile"`
	DockerfileFrontend canonical.Digest   `json:"dockerfile_frontend"`
	Platform           blueprint.Platform `json:"platform"`
}

type ResolvedRequestOwnerValidator func(ResolvedRequestV1) error

func ResolvedRequestDigest(request ResolvedRequestV1, validateOwner ResolvedRequestOwnerValidator) (canonical.Digest, error) {
	if err := ValidateResolvedRequestV1(request, validateOwner); err != nil {
		return "", err
	}
	return canonical.Sum("resolved-request", ResolvedRequestSchemaV1, request)
}

func ValidateResolvedRequestV1(request ResolvedRequestV1, validateOwner ResolvedRequestOwnerValidator) error {
	if request.Schema != ResolvedRequestSchemaV1 {
		return fmt.Errorf("resolved request schema must be %q", ResolvedRequestSchemaV1)
	}
	if err := request.OverlayDigest.Validate(); err != nil {
		return fmt.Errorf("resolved request overlay digest: %w", err)
	}
	if err := request.Platform.Validate(); err != nil {
		return fmt.Errorf("resolved request platform: %w", err)
	}
	if request.Components == nil || request.Sources == nil {
		return fmt.Errorf("resolved request components and sources must use arrays")
	}
	components := make(map[string]blueprint.ComponentType, len(request.Components))
	for index, component := range request.Components {
		if index > 0 && request.Components[index-1].Component >= component.Component {
			return fmt.Errorf("resolved request components must be unique and sorted by component name")
		}
		if err := blueprint.ValidateProviderIdentifier("resolved request component", component.Component); err != nil {
			return err
		}
		if err := validateComponentProvider(component.Provider); err != nil {
			return err
		}
		if component.Request.Provider != component.Provider {
			return fmt.Errorf("resolved component %q request provider does not match %q", component.Component, component.Provider)
		}
		if _, err := CanonicalProviderRequestBytes(component.Request); err != nil {
			return fmt.Errorf("resolved component %q: %w", component.Component, err)
		}
		components[component.Component] = component.Provider
	}
	for index, source := range request.Sources {
		if index > 0 && compareResolvedSourceInputs(request.Sources[index-1], source) >= 0 {
			return fmt.Errorf("resolved source inputs must be unique and sorted by component and logical package")
		}
		provider, exists := components[source.Component]
		if !exists || provider == blueprint.ComponentTypeBase {
			return fmt.Errorf("resolved source input targets missing or unsupported component %q", source.Component)
		}
		if err := ValidateResolvedSourceInput(source); err != nil {
			return err
		}
	}
	if validateOwner == nil {
		return fmt.Errorf("resolved request provider-owned validator is required")
	}
	if err := validateOwner(request); err != nil {
		return fmt.Errorf("resolved request provider-owned data: %w", err)
	}
	return nil
}

func ValidateResolvedSourceInput(source ResolvedSourceInput) error {
	if source.Schema != ResolvedSourceInputSchemaV1 {
		return fmt.Errorf("resolved source input schema must be %q", ResolvedSourceInputSchemaV1)
	}
	if err := blueprint.ValidateProviderIdentifier("resolved source component", source.Component); err != nil {
		return err
	}
	if source.Component == "base" {
		return fmt.Errorf("resolved source input cannot target the base component")
	}
	if !isNonemptyPlainString(source.LogicalPackage) {
		return fmt.Errorf("resolved source logical package must be nonempty valid text")
	}
	if err := source.SourceManifestDigest.Validate(); err != nil {
		return fmt.Errorf("resolved source manifest digest: %w", err)
	}
	if !isNonemptyPlainString(source.BuilderProfile) {
		return fmt.Errorf("resolved source builder profile must be nonempty valid text")
	}
	if err := validateCanonicalProviderData("resolved source build settings", source.BuildSettings); err != nil {
		return err
	}
	if err := validateCanonicalProviderData("resolved source ecosystem metadata", source.EcosystemMetadata); err != nil {
		return err
	}
	if err := source.ArtifactDigest.Validate(); err != nil {
		return fmt.Errorf("resolved source artifact digest: %w", err)
	}
	return nil
}

func ProviderNodePlanDigest(node NodeSpec) (canonical.Digest, error) {
	if err := ValidateNodeSpec(node); err != nil {
		return "", err
	}
	return canonical.Sum("provider-plan", ProviderPlanSchemaV1, node)
}

func ProviderRequestDigest(request CanonicalProviderRequest) (canonical.Digest, error) {
	if _, err := CanonicalProviderRequestBytes(request); err != nil {
		return "", err
	}
	return canonical.Sum("provider-request", request.Schema, request)
}

func ResolverCacheKeyDigest(key ResolverCacheKeyV1) (canonical.Digest, error) {
	if key.Schema != ResolverCacheKeySchemaV1 {
		return "", fmt.Errorf("resolver cache key schema must be %q", ResolverCacheKeySchemaV1)
	}
	if err := validateResolvableNodeID(key.NodeID); err != nil {
		return "", err
	}
	if err := key.RequestDigest.Validate(); err != nil {
		return "", fmt.Errorf("resolver cache request digest: %w", err)
	}
	if err := key.ProfileDigest.Validate(); err != nil {
		return "", fmt.Errorf("resolver cache profile digest: %w", err)
	}
	if !isNonemptyPlainString(key.ResolverRecipe) {
		return "", fmt.Errorf("resolver cache recipe must be nonempty valid text")
	}
	if err := key.Platform.Validate(); err != nil {
		return "", fmt.Errorf("resolver cache platform: %w", err)
	}
	return canonical.Sum("resolver-cache-key", ResolverCacheKeySchemaV1, key)
}

func AssemblyKeyDigest(key AssemblyKeyV1) (canonical.Digest, error) {
	if key.Schema != AssemblyKeySchemaV1 {
		return "", fmt.Errorf("assembly key schema must be %q", AssemblyKeySchemaV1)
	}
	if err := key.Parent.Validate(); err != nil {
		return "", fmt.Errorf("assembly key parent: %w", err)
	}
	if err := key.TransactionDigest.Validate(); err != nil {
		return "", fmt.Errorf("assembly key transaction digest: %w", err)
	}
	if !isNonemptyPlainString(key.RendererProfile) {
		return "", fmt.Errorf("assembly key renderer profile must be nonempty valid text")
	}
	if err := key.DockerfileFrontend.Validate(); err != nil {
		return "", fmt.Errorf("assembly key Dockerfile frontend: %w", err)
	}
	if err := key.Platform.Validate(); err != nil {
		return "", fmt.Errorf("assembly key platform: %w", err)
	}
	return canonical.Sum("assembly-key", AssemblyKeySchemaV1, key)
}

func compareResolvedSourceInputs(left ResolvedSourceInput, right ResolvedSourceInput) int {
	if left.Component < right.Component {
		return -1
	}
	if left.Component > right.Component {
		return 1
	}
	return strings.Compare(left.LogicalPackage, right.LogicalPackage)
}

func validateResolvableNodeID(id NodeID) error {
	if id == "apt" {
		return nil
	}
	component, ok := strings.CutPrefix(string(id), "python/")
	if !ok || component == "" || component == "base" {
		return fmt.Errorf("resolver node ID must be %q or python/<component>", "apt")
	}
	return blueprint.ValidateProviderIdentifier("resolver node component", component)
}

func isNonemptyPlainString(value string) bool {
	if value == "" || !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}
