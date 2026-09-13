package portabletool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	pep440 "github.com/aquasecurity/go-pep440-version"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providerstore"
)

const portableToolCatalogMaxReferencesV1 = 1024

// RecordArrayMaxEntriesV1 is the shared per-record array ceiling applied by
// strict portable-tool records and their provider-owned projections.
const RecordArrayMaxEntriesV1 = portableToolCatalogMaxReferencesV1

type portableToolCatalogBundledComponentV1 = BundledComponentV1
type portableToolCatalogExportV1 = ToolExportV1
type portableToolCatalogBindingContractV1 = BindingContractV1
type portableToolCatalogBindingArtifactV1 = BindingArtifactRecordV1
type portableToolCatalogPayloadV1 = PayloadRecordV1
type portableToolCatalogArtifactSourceV1 = ArtifactSourceRecordV1
type portableToolCatalogPackageSetV1 = NativePackageSetV1
type portableToolCatalogProbeV1 = RecordProbeV1
type portableToolCatalogValidationProfileV1 = ValidationProfileRecordV1

// ValidateRecordEnvelopeV1 applies the complete canonical catalog
// constraints for record kinds persisted in provider plans and build locks.
// Catalog loading and locked replay call this same implementation so a record
// rejected at publication cannot become acceptable after it is locked.
func ValidateRecordEnvelopeV1(record canonical.Envelope) error {
	id, ok := record.Value["id"].(string)
	if !ok {
		return fmt.Errorf("portable tool catalog record ID must be a string")
	}
	var err error
	switch record.Schema {
	case ReleaseManifestSchemaV1:
		err = validatePortableToolCatalogReleaseManifestV1(record.Value)
	case BindingContractSchemaV1:
		err = validatePortableToolCatalogBindingContractV1(record.Value)
	case BindingArtifactSchemaV1:
		err = validatePortableToolCatalogBindingArtifactV1(record.Value)
	case PayloadRecordSchemaV1:
		err = validatePortableToolCatalogPayloadV1(record.Value)
	case ArtifactSourceRecordSchemaV1:
		err = validatePortableToolCatalogArtifactSourceV1(record.Value)
	case NativePackageSetSchemaV1:
		err = validatePortableToolCatalogPackageSetV1(record.Value)
	case ValidationProfileSchemaV1:
		err = validatePortableToolCatalogValidationProfileV1(record.Value)
	default:
		return fmt.Errorf("portable tool catalog record schema %q is unsupported", record.Schema)
	}
	if err != nil {
		return err
	}
	return validatePortableRecordID(id)
}

// ValidateBindingRecordReferencesV1 validates the exact contract and artifact
// references carried by a projected Python binding. The references must use
// their canonical record categories and name the same release and binding.
func ValidateBindingRecordReferencesV1(contract, artifact RecordReferenceV1) error {
	if err := validatePortableToolRecordReferenceV1(contract); err != nil {
		return fmt.Errorf("binding contract reference: %w", err)
	}
	if err := validatePortableToolRecordReferenceV1(artifact); err != nil {
		return fmt.Errorf("binding artifact reference: %w", err)
	}
	contractSegments := strings.Split(contract.ID, "/")
	if len(contractSegments) != 6 || contractSegments[3] != "bindings" ||
		validatePortableRecordIdentifier("binding", contractSegments[4]) != nil || contractSegments[5] != "contract" {
		return fmt.Errorf("binding contract reference %q does not use the canonical binding-contract namespace", contract.ID)
	}
	artifactSegments := strings.Split(artifact.ID, "/")
	if len(artifactSegments) != 7 || artifactSegments[3] != "bindings" ||
		validatePortableRecordIdentifier("binding", artifactSegments[4]) != nil || artifactSegments[5] != "artifacts" ||
		!portableToolCatalogPlatformLeafV1(artifactSegments[6]) {
		return fmt.Errorf("binding artifact reference %q does not use the canonical binding-artifact namespace", artifact.ID)
	}
	if strings.Join(contractSegments[:5], "/") != strings.Join(artifactSegments[:5], "/") {
		return fmt.Errorf("binding contract and artifact references do not name the same release binding")
	}
	return nil
}

// ValidateBindingArtifactReferencePlatformV1 requires a canonical binding
// artifact reference to name the same target as its projected wheel metadata.
func ValidateBindingArtifactReferencePlatformV1(artifact RecordReferenceV1, platform string) error {
	if err := validatePortableToolRecordReferenceV1(artifact); err != nil {
		return fmt.Errorf("binding artifact reference: %w", err)
	}
	if !portableToolCatalogPlatformV1(platform) {
		return fmt.Errorf("binding artifact platform %q is unsupported", platform)
	}
	segments := strings.Split(artifact.ID, "/")
	expectedPlatform := strings.ReplaceAll(platform, "/", "-")
	if len(segments) != 7 || segments[1] != "releases" || validatePortableToolVersionSegment(segments[2]) != nil ||
		segments[3] != "bindings" || validatePortableRecordIdentifier("binding", segments[4]) != nil ||
		segments[5] != "artifacts" || segments[6] != expectedPlatform {
		return fmt.Errorf("binding artifact reference %q does not match platform %q", artifact.ID, platform)
	}
	return nil
}

// ValidateBindingWheelTagsForPlatformV1 applies the strict artifact-record
// platform check to every exact wheel tag without imposing provider runtime
// eligibility policy on the tag's Python or ABI fields.
func ValidateBindingWheelTagsForPlatformV1(tags []string, platform string) error {
	if !portableToolCatalogPlatformV1(platform) {
		return fmt.Errorf("binding artifact platform %q is unsupported", platform)
	}
	for _, tag := range tags {
		segments := strings.Split(tag, "-")
		if len(segments) != 3 || !portableToolCatalogWheelTagGroupV1(segments[0]) || !portableToolCatalogWheelTagGroupV1(segments[1]) || !portableToolCatalogWheelTagGroupV1(segments[2]) {
			return fmt.Errorf("binding artifact wheel tag %q is invalid", tag)
		}
		if _, err := ProjectWheelPlatformForTargetV1(segments[2], platform); err != nil {
			return fmt.Errorf("binding artifact wheel tag %q is incompatible with platform %q", tag, platform)
		}
	}
	return nil
}

// ValidateBindingBundledComponentAgreementV1 requires every bundled component
// declared by a contract to be present with the same version and path in its
// selected artifact. Artifact-only inventory entries remain exact artifact
// metadata and are permitted.
func ValidateBindingBundledComponentAgreementV1(contract, artifact []BundledComponentV1) error {
	bundled := make(map[string]BundledComponentV1, len(artifact))
	for _, component := range artifact {
		bundled[component.Name] = component
	}
	for _, declared := range contract {
		present, exists := bundled[declared.Name]
		if !exists {
			return fmt.Errorf("contract declares bundled component %q which the artifact does not bundle", declared.Name)
		}
		if present.Version != declared.Version || present.Path != declared.Path {
			return fmt.Errorf("bundled component %q does not match the contract", declared.Name)
		}
	}
	return nil
}

// ValidateBindingCLIExportV1 applies the canonical name and absolute non-root
// slash-path grammar shared by binding contracts and provider projections.
func ValidateBindingCLIExportV1(export ToolExportV1) error {
	if validatePortableRecordIdentifier("binding CLI", export.Name) != nil || validatePortableToolCatalogAbsolutePathV1(export.Path) != nil {
		return fmt.Errorf("binding CLI must use a canonical name and absolute path")
	}
	return nil
}

// ValidatePythonRequiresPythonV1 requires the canonical PEP 440 spelling used
// by exact binding artifact records and provider projections.
func ValidatePythonRequiresPythonV1(value string) error {
	specifiers, err := pep440.NewSpecifiers(value)
	if value == "" || err != nil || specifiers.String() != value {
		return fmt.Errorf("binding artifact requires_python must be a canonical PEP 440 specifier set")
	}
	return nil
}

func validatePortableToolCatalogReleaseManifestV1(value canonical.Object) error {
	const schema = ReleaseManifestSchemaV1
	var record ReleaseManifestV1
	if err := decodePortableToolCatalogRecordV1(value, schema, []string{
		"schema", "id", "tool", "version", "aliases", "revision", "contract", "targets",
		"artifact_sources", "provenance", "validation_profiles",
	}, &record); err != nil {
		return err
	}
	if validatePortableRecordIdentifier("release manifest tool", record.Tool) != nil {
		return fmt.Errorf("release manifest identity is incomplete")
	}
	versionSegment, err := encodePortableToolVersionSegment(record.Version)
	if err != nil {
		return fmt.Errorf("release manifest version: %w", err)
	}
	if err := validatePortableToolCatalogDecimalV1("release revision", record.Revision, true); err != nil {
		return err
	}
	if err := validatePortableToolCatalogSortedStringsV1("release aliases", record.Aliases, false); err != nil {
		return err
	}
	for _, alias := range record.Aliases {
		if _, err := encodePortableToolVersionSegment(alias); err != nil {
			return fmt.Errorf("release alias %q: %w", alias, err)
		}
		if alias == record.Version {
			return fmt.Errorf("release alias %q redundantly equals its exact version", alias)
		}
	}
	releasePrefix := fmt.Sprintf("tool:%s/releases/%s", record.Tool, versionSegment)
	manifestID := fmt.Sprintf("%s/revisions/%s/manifest", releasePrefix, record.Revision)
	if record.ID != manifestID {
		return fmt.Errorf("release manifest ID must be %q", manifestID)
	}
	if err := validatePortableToolRecordReferenceV1(record.Contract); err != nil {
		return fmt.Errorf("release contract: %w", err)
	}
	if record.Contract.ID != releasePrefix+"/contract" {
		return fmt.Errorf("release contract reference must identify the current release contract")
	}
	if err := validatePortableToolCatalogProfileReferenceListV1(
		"release validation profiles", record.ValidationProfiles, releasePrefix, false,
	); err != nil {
		return err
	}
	if len(record.Targets) == 0 {
		return fmt.Errorf("release manifest targets must not be empty")
	}
	if err := validatePortableToolCatalogReferenceListV1("release targets", record.Targets); err != nil {
		return err
	}
	for _, reference := range record.Targets {
		if err := validatePortableToolCatalogTargetReferenceV1(reference, releasePrefix); err != nil {
			return err
		}
	}
	if record.ArtifactSources == nil || len(record.ArtifactSources) > portableToolCatalogMaxReferencesV1 {
		return fmt.Errorf("artifact source mappings must use a bounded array")
	}
	for index, mapping := range record.ArtifactSources {
		if err := mapping.ArtifactSHA256.Validate(); err != nil {
			return fmt.Errorf("artifact source mapping %d digest: %w", index, err)
		}
		if err := validatePortableToolRecordReferenceV1(mapping.Artifact); err != nil {
			return fmt.Errorf("artifact source mapping %d artifact: %w", index, err)
		}
		if err := validatePortableToolCatalogArtifactTargetReferenceV1(mapping.Artifact, releasePrefix); err != nil {
			return err
		}
		if err := validatePortableToolRecordReferenceV1(mapping.Source); err != nil {
			return fmt.Errorf("artifact source mapping %d source: %w", index, err)
		}
		if err := validatePortableToolCatalogSourceReferenceV1(mapping.Source, releasePrefix, record.Revision); err != nil {
			return err
		}
		if index > 0 && record.ArtifactSources[index-1].ArtifactSHA256 >= mapping.ArtifactSHA256 {
			return fmt.Errorf("artifact source mappings must be unique and sorted by artifact digest")
		}
	}
	if err := validatePortableToolCatalogCanonicalURLsV1("release provenance", record.Provenance); err != nil {
		return err
	}
	return nil
}

func validatePortableToolCatalogBindingContractV1(value canonical.Object) error {
	const schema = BindingContractSchemaV1
	var record portableToolCatalogBindingContractV1
	if err := decodePortableToolCatalogRecordV1(value, schema, []string{
		"schema", "id", "name", "package", "requirements", "supported_python", "supported_tags", "bundled_components", "cli",
	}, &record); err != nil {
		return err
	}
	if validatePortableRecordIdentifier("binding contract", record.Name) != nil || !portableToolCatalogPackageNameV1(record.Package) {
		return fmt.Errorf("binding contract is incomplete")
	}
	segments := strings.Split(record.ID, "/")
	if len(segments) != 6 || segments[1] != "releases" || segments[3] != "bindings" || segments[4] != record.Name || segments[5] != "contract" || validatePortableToolVersionSegment(segments[2]) != nil {
		return fmt.Errorf("binding contract ID must use tool:<name>/releases/<encoded-version>/bindings/%s/contract", record.Name)
	}
	if err := ValidateBindingCLIExportV1(record.CLI); err != nil {
		return err
	}
	if err := validatePortableToolCatalogSortedStringsV1("binding requirements", record.Requirements, true); err != nil {
		return err
	}
	if err := ValidatePythonPackageRootRequirementsV1(record.Requirements); err != nil {
		return fmt.Errorf("binding requirements: %w", err)
	}
	if err := validatePortableToolSupportedPythonClaimsV1(record.SupportedPython); err != nil {
		return err
	}
	if record.BundledComponents == nil || len(record.BundledComponents) > portableToolCatalogMaxReferencesV1 {
		return fmt.Errorf("binding contract bundled components must use a bounded array")
	}
	for index, component := range record.BundledComponents {
		if validatePortableRecordIdentifier("bundled component", component.Name) != nil || !portableToolCatalogRecordSegmentV1(component.Version) || validatePortableToolCatalogPathV1(component.Path, false) != nil {
			return fmt.Errorf("binding contract bundled component %d is not canonical", index)
		}
		if index > 0 && record.BundledComponents[index-1].Name >= component.Name {
			return fmt.Errorf("binding contract bundled components must be unique and sorted by name")
		}
	}
	if err := validatePortableToolCatalogSortedStringsV1("binding supported tags", record.SupportedTags, true); err != nil {
		return err
	}
	for _, tag := range record.SupportedTags {
		segments := strings.Split(tag, "-")
		if len(segments) != 3 || !portableToolCatalogWheelTagGroupV1(segments[0]) || !portableToolCatalogWheelTagGroupV1(segments[1]) || !portableToolCatalogWheelTagGroupV1(segments[2]) {
			return fmt.Errorf("binding supported tag %q must be a canonical three-part wheel tag", tag)
		}
	}
	return nil
}

func validatePortableToolSupportedPythonClaimsV1(values []string) error {
	if values == nil {
		return fmt.Errorf("supported Python must use an array")
	}
	if len(values) == 0 {
		return fmt.Errorf("supported Python must not be empty")
	}
	if len(values) > portableToolCatalogMaxReferencesV1 {
		return fmt.Errorf("supported Python must use at most %d entries", portableToolCatalogMaxReferencesV1)
	}
	var previous []int
	for _, value := range values {
		if err := ValidatePythonInterpreterVersionV1(value); err != nil {
			return fmt.Errorf("supported Python version %q: %w", value, err)
		}
		current, ok := portableToolPythonParseReleaseVersionV1(value)
		if !ok {
			return fmt.Errorf("supported Python version %q is not canonical", value)
		}
		if previous != nil {
			comparison := portableToolPythonCompareReleaseVersionsV1(previous, current)
			if comparison >= 0 || len(previous) == 2 && previous[0] == current[0] && previous[1] == current[1] {
				return fmt.Errorf("supported Python must contain numerically sorted normalized claims")
			}
		}
		previous = current
	}
	return nil
}

func validatePortableToolCatalogBindingArtifactV1(value canonical.Object) error {
	const schema = BindingArtifactSchemaV1
	var record portableToolCatalogBindingArtifactV1
	if err := decodePortableToolCatalogRecordV1(value, schema, []string{
		"schema", "id", "binding", "contract", "name", "ecosystem_version", "platform", "filename",
		"size", "sha256", "resolver", "tags", "requires_python", "bundled_components",
	}, &record); err != nil {
		return err
	}
	if validatePortableRecordIdentifier("binding", record.Binding) != nil || !portableToolCatalogPlatformV1(record.Platform) || validatePortableToolCatalogPathV1(record.Filename, false) != nil || path.Dir(record.Filename) != "." {
		return fmt.Errorf("binding artifact identity is incomplete")
	}
	segments := strings.Split(record.ID, "/")
	expectedPlatform := strings.ReplaceAll(record.Platform, "/", "-")
	if len(segments) != 7 || segments[1] != "releases" || segments[3] != "bindings" || segments[4] != record.Binding || segments[5] != "artifacts" || segments[6] != expectedPlatform || validatePortableToolVersionSegment(segments[2]) != nil {
		return fmt.Errorf("binding artifact ID must match binding %q and platform %q in a release namespace", record.Binding, record.Platform)
	}
	if record.Resolver != "https-sha256" {
		return fmt.Errorf("binding artifact resolver %q is unsupported", record.Resolver)
	}
	if validatePortableRecordIdentifier("binding artifact component", record.Name) != nil || !portableToolCatalogRecordSegmentV1(record.EcosystemVersion) {
		return fmt.Errorf("binding artifact component name and ecosystem version must be canonical")
	}
	if err := validatePortableToolRecordReferenceV1(record.Contract); err != nil {
		return fmt.Errorf("binding artifact contract: %w", err)
	}
	expectedContract := strings.Join(segments[:5], "/") + "/contract"
	if record.Contract.ID != expectedContract {
		return fmt.Errorf("binding artifact contract reference must be %q", expectedContract)
	}
	filename, err := validatePortableToolCatalogBindingCompatibilityV1(record)
	if err != nil {
		return err
	}
	if filename.Distribution != portableToolPythonNormalizeDistributionNameV1(record.Name) || filename.EcosystemVersion != record.EcosystemVersion {
		return fmt.Errorf("binding artifact name and ecosystem version must match the wheel filename %q", record.Filename)
	}
	if err := validatePortableToolCatalogDecimalV1("binding artifact size", record.Size, true); err != nil {
		return err
	}
	if err := record.SHA256.Validate(); err != nil {
		return fmt.Errorf("binding artifact digest: %w", err)
	}
	if record.BundledComponents == nil || len(record.BundledComponents) > portableToolCatalogMaxReferencesV1 {
		return fmt.Errorf("binding artifact bundled components must use a bounded array")
	}
	for index, component := range record.BundledComponents {
		if validatePortableRecordIdentifier("bundled component", component.Name) != nil || !portableToolCatalogRecordSegmentV1(component.Version) || validatePortableToolCatalogPathV1(component.Path, false) != nil || index > 0 && record.BundledComponents[index-1].Name >= component.Name {
			return fmt.Errorf("binding artifact bundled components must be complete, unique, and sorted")
		}
	}
	return nil
}

func validatePortableToolCatalogPayloadV1(value canonical.Object) error {
	const schema = PayloadRecordSchemaV1
	var record portableToolCatalogPayloadV1
	if err := decodePortableToolCatalogRecordV1(value, schema, []string{
		"schema", "id", "name", "revision", "upstream_version", "platform", "logical_path", "kind",
		"size", "sha256", "resolver", "entries", "unpacked_size", "install_directory", "archive_root", "symbolic_link_policy", "executables",
	}, &record); err != nil {
		return err
	}
	if validatePortableRecordIdentifier("payload", record.Name) != nil || !portableToolCatalogRecordSegmentV1(record.Revision) || !portableToolCatalogRecordSegmentV1(record.UpstreamVersion) || !portableToolCatalogPlatformV1(record.Platform) || !portableToolCatalogPayloadKindV1(record.Kind) {
		return fmt.Errorf("payload identity is incomplete")
	}
	segments := strings.Split(record.ID, "/")
	expectedLeaf := record.Name + "-" + strings.ReplaceAll(record.Platform, "/", "-")
	validID := len(segments) == 5 && segments[1] == "releases" && segments[3] == "payloads" && segments[4] == expectedLeaf
	validScopedID := len(segments) == 6 && segments[1] == "releases" && segments[3] == "payloads" && validPortableRecordSegment(segments[4]) && segments[5] == expectedLeaf
	if !validID && !validScopedID {
		return fmt.Errorf("payload ID must use its canonical release payload namespace")
	}
	if validatePortableToolVersionSegment(segments[2]) != nil {
		return fmt.Errorf("payload ID must use its canonical release payload namespace")
	}
	if record.Resolver != "https-sha256" {
		return fmt.Errorf("payload resolver %q is unsupported", record.Resolver)
	}
	if err := validatePortableToolCatalogPathV1(record.LogicalPath, false); err != nil {
		return fmt.Errorf("payload logical path: %w", err)
	}
	for _, decimal := range []struct {
		field string
		value string
	}{
		{field: "payload size", value: record.Size},
		{field: "payload entries", value: record.Entries},
		{field: "payload unpacked size", value: record.UnpackedSize},
	} {
		if err := validatePortableToolCatalogDecimalV1(decimal.field, decimal.value, true); err != nil {
			return err
		}
	}
	if err := record.SHA256.Validate(); err != nil {
		return fmt.Errorf("payload digest: %w", err)
	}
	if err := validatePortableToolCatalogPathV1(record.InstallDirectory, false); err != nil {
		return fmt.Errorf("payload install directory: %w", err)
	}
	if err := validatePortableToolCatalogPathV1(record.ArchiveRoot, true); err != nil {
		return fmt.Errorf("payload archive root: %w", err)
	}
	switch record.SymbolicLinkPolicy {
	case PayloadSymbolicLinkPolicyRejectV1:
	case PayloadSymbolicLinkPolicyMaterializeRegularTargetV1:
		if !strings.HasSuffix(record.LogicalPath, ".tar.gz") && !strings.HasSuffix(record.LogicalPath, ".tgz") {
			return fmt.Errorf("payload symbolic-link policy %q requires a tar.gz payload", record.SymbolicLinkPolicy)
		}
	default:
		return fmt.Errorf("payload symbolic-link policy %q is unsupported", record.SymbolicLinkPolicy)
	}
	if err := validatePortableToolCatalogSortedStringsV1("payload executables", record.Executables, true); err != nil {
		return err
	}
	for _, executable := range record.Executables {
		if err := validatePortableToolCatalogPathV1(executable, false); err != nil {
			return fmt.Errorf("payload executable: %w", err)
		}
		if record.ArchiveRoot != "." && executable != record.ArchiveRoot && !strings.HasPrefix(executable, record.ArchiveRoot+"/") {
			return fmt.Errorf("payload executable %q is outside archive root %q", executable, record.ArchiveRoot)
		}
	}
	if path.Dir(record.InstallDirectory) != "." {
		return fmt.Errorf("payload paths are inconsistent")
	}
	if record.Kind == "raw-executable" && (record.Entries != "1" || record.UnpackedSize != record.Size || record.ArchiveRoot != "." || len(record.Executables) != 1) {
		return fmt.Errorf("raw executable payload inventory is inconsistent")
	}
	return nil
}

func validatePortableToolCatalogArtifactSourceV1(value canonical.Object) error {
	const schema = ArtifactSourceRecordSchemaV1
	var record portableToolCatalogArtifactSourceV1
	if err := decodePortableToolCatalogRecordV1(value, schema, []string{
		"schema", "id", "sha256", "mirrors", "provenance", "diagnostics",
	}, &record); err != nil {
		return err
	}
	segments := strings.Split(record.ID, "/")
	if len(segments) != 7 || segments[1] != "releases" || segments[3] != "revisions" || segments[5] != "sources" || !validPortableRecordSegment(segments[6]) || validatePortableToolVersionSegment(segments[2]) != nil || validatePortableToolCatalogDecimalV1("artifact source revision", segments[4], true) != nil {
		return fmt.Errorf("artifact source ID must use a canonical release revision source namespace")
	}
	if err := record.SHA256.Validate(); err != nil {
		return fmt.Errorf("artifact source digest: %w", err)
	}
	if len(record.Mirrors) == 0 || len(record.Mirrors) > providerstore.CoreMaxArtifactMirrors {
		return fmt.Errorf("artifact source mirrors must contain between 1 and %d entries", providerstore.CoreMaxArtifactMirrors)
	}
	seen := make(map[string]struct{}, len(record.Mirrors))
	for index, mirror := range record.Mirrors {
		canonicalURL, err := providerstore.CanonicalSourceURLV1(mirror)
		if err != nil {
			return fmt.Errorf("artifact source mirror %d: %w", index, err)
		}
		if canonicalURL != mirror {
			return fmt.Errorf("artifact source mirror %d must use canonical spelling %q", index, canonicalURL)
		}
		if _, exists := seen[mirror]; exists {
			return fmt.Errorf("artifact source mirrors must be unique")
		}
		seen[mirror] = struct{}{}
	}
	if len(record.Provenance) == 0 {
		return fmt.Errorf("artifact source provenance must use a nonempty bounded array")
	}
	if err := validatePortableToolCatalogCanonicalURLsV1("artifact source provenance", record.Provenance); err != nil {
		return err
	}
	return validatePortableToolCatalogSortedStringsV1("artifact source diagnostics", record.Diagnostics, false)
}

func validatePortableToolCatalogPackageSetV1(value canonical.Object) error {
	const schema = NativePackageSetSchemaV1
	var record portableToolCatalogPackageSetV1
	if err := decodePortableToolCatalogRecordV1(value, schema, []string{
		"schema", "id", "manager", "requirements", "repositories", "validation_metadata",
	}, &record); err != nil {
		return err
	}
	segments := strings.Split(record.ID, "/")
	if record.Manager != "apt" {
		return fmt.Errorf("native package-set identity is incomplete")
	}
	if len(segments) != 5 || segments[1] != "releases" || segments[3] != "package-sets" || !validPortableRecordSegment(segments[4]) || validatePortableToolVersionSegment(segments[2]) != nil {
		return fmt.Errorf("native package-set ID must use a release package-set namespace")
	}
	if err := validatePortableToolCatalogSortedStringsV1("native package requirements", record.Requirements, true); err != nil {
		return err
	}
	if err := validatePortableToolCatalogSortedStringsV1("native package repositories", record.Repositories, false); err != nil {
		return err
	}
	if err := validatePortableToolCatalogSortedStringsV1("native package validation metadata", record.ValidationMetadata, false); err != nil {
		return err
	}
	packages := make(map[string]string, len(record.Requirements))
	for _, requirement := range record.Requirements {
		parsed, err := blueprint.ParseAPTPackageRequest(requirement)
		if err != nil {
			return fmt.Errorf("native package requirement %q: %w", requirement, err)
		}
		if previous, exists := packages[parsed.Name]; exists {
			return fmt.Errorf("native package requirements %q and %q name the same package %q", previous, requirement, parsed.Name)
		}
		packages[parsed.Name] = requirement
	}
	return nil
}

func validatePortableToolCatalogValidationProfileV1(value canonical.Object) error {
	const schema = ValidationProfileSchemaV1
	var record portableToolCatalogValidationProfileV1
	if err := decodePortableToolCatalogRecordV1(value, schema, []string{
		"schema", "id", "tool", "version", "probes",
	}, &record); err != nil {
		return err
	}
	if validatePortableRecordIdentifier("validation profile tool", record.Tool) != nil {
		return fmt.Errorf("validation profile identity is inconsistent")
	}
	versionSegment, err := encodePortableToolVersionSegment(record.Version)
	if err != nil {
		return fmt.Errorf("validation profile version: %w", err)
	}
	prefix := fmt.Sprintf("tool:%s/releases/%s/validation/profiles/", record.Tool, versionSegment)
	if !strings.HasPrefix(record.ID, prefix) || !validPortableRecordSegment(strings.TrimPrefix(record.ID, prefix)) {
		return fmt.Errorf("validation profile ID must use a canonical name beneath %q", prefix)
	}
	if record.Probes == nil || len(record.Probes) == 0 || len(record.Probes) > portableToolCatalogMaxReferencesV1 {
		return fmt.Errorf("validation profile probes must use a nonempty bounded array")
	}
	var previous []byte
	for index, probe := range record.Probes {
		if err := validatePortableToolCatalogProbeV1(probe); err != nil {
			return fmt.Errorf("validation profile probes[%d]: %w", index, err)
		}
		key, err := canonical.Marshal(probe)
		if err != nil {
			return fmt.Errorf("validation profile probes[%d] canonical form: %w", index, err)
		}
		if index > 0 && bytes.Compare(previous, key) >= 0 {
			return fmt.Errorf("validation profile probes must be unique and sorted")
		}
		previous = key
	}
	return nil
}

func decodePortableToolCatalogRecordV1(value canonical.Object, schema string, fields []string, target any) error {
	if value == nil || len(value) != len(fields) {
		return fmt.Errorf("%s record must contain exactly the canonical fields", schema)
	}
	expected := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		expected[field] = struct{}{}
	}
	for field := range value {
		if _, exists := expected[field]; !exists {
			return fmt.Errorf("%s record contains unsupported field %q", schema, field)
		}
	}
	if actual, _ := value["schema"].(string); actual != schema {
		return fmt.Errorf("catalog record schema must be %q", schema)
	}
	encoded, err := canonical.Marshal(value)
	if err != nil {
		return fmt.Errorf("canonical catalog record: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s record: %w", schema, err)
	}
	return nil
}

func validatePortableToolCatalogBindingCompatibilityV1(record portableToolCatalogBindingArtifactV1) (WheelFilenameProjectionV1, error) {
	if err := validatePortableToolCatalogSortedStringsV1("binding artifact tags", record.Tags, true); err != nil {
		return WheelFilenameProjectionV1{}, err
	}
	filename, err := ProjectWheelFilenameV1(record.Filename)
	if err != nil {
		return WheelFilenameProjectionV1{}, fmt.Errorf("binding artifact filename: %w", err)
	}
	if !portableToolCatalogStringsEqualV1(filename.Tags, record.Tags) {
		return WheelFilenameProjectionV1{}, fmt.Errorf("binding artifact tags must exactly match the expanded wheel filename tags")
	}
	if err := ValidateBindingWheelTagsForPlatformV1(record.Tags, record.Platform); err != nil {
		return WheelFilenameProjectionV1{}, err
	}
	if err := ValidatePythonRequiresPythonV1(record.RequiresPython); err != nil {
		return WheelFilenameProjectionV1{}, err
	}
	return filename, nil
}

// WheelFilenameProjectionV1 is the canonical identity and expanded
// compatibility-tag projection of one wheel filename.
type WheelFilenameProjectionV1 struct {
	Distribution     string
	EcosystemVersion string
	Tags             []string
}

// ProjectWheelFilenameV1 parses one canonical wheel filename without reading
// the wheel or applying runtime eligibility policy.
func ProjectWheelFilenameV1(filename string) (WheelFilenameProjectionV1, error) {
	if !strings.HasSuffix(filename, ".whl") {
		return WheelFilenameProjectionV1{}, fmt.Errorf("wheel filename must end in .whl")
	}
	parts := strings.Split(strings.TrimSuffix(filename, ".whl"), "-")
	if len(parts) != 5 && len(parts) != 6 {
		return WheelFilenameProjectionV1{}, fmt.Errorf("wheel filename must contain distribution, version, Python, ABI, and platform tags")
	}
	if !portableToolCatalogWheelDistributionV1(parts[0]) {
		return WheelFilenameProjectionV1{}, fmt.Errorf("wheel filename contains an invalid distribution or version")
	}
	version, err := pep440.Parse(parts[1])
	if err != nil || version.String() != parts[1] {
		return WheelFilenameProjectionV1{}, fmt.Errorf("wheel filename contains an invalid distribution or version")
	}
	if len(parts) == 6 && !portableToolCatalogWheelBuildTagV1(parts[2]) {
		return WheelFilenameProjectionV1{}, fmt.Errorf("wheel filename contains an invalid build tag")
	}
	groups := [][]string{
		strings.Split(parts[len(parts)-3], "."), strings.Split(parts[len(parts)-2], "."), strings.Split(parts[len(parts)-1], "."),
	}
	count := 1
	for _, group := range groups {
		if len(group) == 0 || len(group) > portableToolCatalogMaxReferencesV1/count {
			return WheelFilenameProjectionV1{}, fmt.Errorf("wheel filename expands to more than %d compatibility tags", portableToolCatalogMaxReferencesV1)
		}
		count *= len(group)
		for _, component := range group {
			if !portableToolCatalogWheelTagComponentV1(component) {
				return WheelFilenameProjectionV1{}, fmt.Errorf("wheel filename contains an invalid compatibility tag")
			}
		}
	}
	tags := make([]string, 0, count)
	for _, pythonTag := range groups[0] {
		for _, abiTag := range groups[1] {
			for _, platformTag := range groups[2] {
				tags = append(tags, pythonTag+"-"+abiTag+"-"+platformTag)
			}
		}
	}
	sort.Strings(tags)
	for index := 1; index < len(tags); index++ {
		if tags[index-1] == tags[index] {
			return WheelFilenameProjectionV1{}, fmt.Errorf("wheel filename compatibility tags must be unique")
		}
	}
	return WheelFilenameProjectionV1{
		Distribution:     portableToolPythonNormalizeDistributionNameV1(parts[0]),
		EcosystemVersion: parts[1],
		Tags:             tags,
	}, nil
}

func validatePortableToolCatalogProbeV1(probe portableToolCatalogProbeV1) error {
	if probe.Path == "" || containsPortableControl(probe.Path) || !path.IsAbs(probe.Path) || path.Clean(probe.Path) != probe.Path || probe.Path == "/" || strings.Contains(probe.Path, `\`) || probe.Args == nil || len(probe.Args) > portableToolCatalogMaxReferencesV1 {
		return fmt.Errorf("probe must use an absolute path and bounded argument array")
	}
	for _, argument := range probe.Args {
		if containsPortableControl(argument) {
			return fmt.Errorf("probe arguments must not contain control characters")
		}
	}
	return nil
}

func validatePortableToolCatalogPathV1(value string, allowDot bool) error {
	if value == "." && allowDot {
		return nil
	}
	if value == "" || containsPortableControl(value) || path.IsAbs(value) || path.Clean(value) != value || strings.Contains(value, `\`) {
		return fmt.Errorf("path %q must be a canonical relative slash path", value)
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("path %q contains an invalid segment", value)
		}
	}
	return nil
}

func validatePortableToolCatalogAbsolutePathV1(value string) error {
	if value == "" || containsPortableControl(value) || !path.IsAbs(value) || path.Clean(value) != value || value == "/" || strings.Contains(value, `\`) {
		return fmt.Errorf("path %q must be a canonical absolute non-root slash path", value)
	}
	return nil
}

// ValidatePythonInterpreterVersionV1 accepts the canonical major.minor or
// major.minor.patch form used by portable-tool binding records.
func ValidatePythonInterpreterVersionV1(value string) error {
	parts := strings.Split(value, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return fmt.Errorf("Python interpreter version %q must use major.minor or major.minor.patch", value)
	}
	for _, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
			return fmt.Errorf("Python interpreter version %q is not canonical", value)
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return fmt.Errorf("Python interpreter version %q is not canonical", value)
			}
		}
		if _, err := strconv.Atoi(part); err != nil {
			return fmt.Errorf("Python interpreter version %q has an out-of-range component", value)
		}
	}
	return nil
}

func portableToolCatalogPackageNameV1(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value[1:] {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '.' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func validatePortableToolCatalogDecimalV1(field, value string, positive bool) error {
	if value == "" || len(value) > 1 && value[0] == '0' {
		return fmt.Errorf("%s must be a canonical decimal string", field)
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return fmt.Errorf("%s must be a canonical decimal string", field)
		}
	}
	parsed, err := strconv.ParseUint(value, 10, 63)
	if err != nil || positive && parsed == 0 {
		return fmt.Errorf("%s must be a bounded positive decimal string", field)
	}
	return nil
}

func validatePortableToolCatalogSortedStringsV1(field string, values []string, nonempty bool) error {
	if values == nil {
		return fmt.Errorf("%s must use an array", field)
	}
	if len(values) > portableToolCatalogMaxReferencesV1 {
		return fmt.Errorf("%s must use at most %d entries", field, portableToolCatalogMaxReferencesV1)
	}
	if nonempty && len(values) == 0 {
		return fmt.Errorf("%s must not be empty", field)
	}
	for index, value := range values {
		if value == "" || strings.TrimSpace(value) != value || containsPortableControl(value) || index > 0 && values[index-1] >= value {
			return fmt.Errorf("%s must contain unique sorted canonical values", field)
		}
	}
	return nil
}

func validatePortableToolCatalogCanonicalURLsV1(field string, values []string) error {
	if values == nil || len(values) > portableToolCatalogMaxReferencesV1 {
		return fmt.Errorf("%s must use a bounded array", field)
	}
	for index, value := range values {
		if value == "" || strings.TrimSpace(value) != value || containsPortableControl(value) {
			return fmt.Errorf("%s must contain canonical values", field)
		}
		canonicalURL, err := providerstore.CanonicalSourceURLV1(value)
		if err != nil {
			return fmt.Errorf("%s %d: %w", field, index, err)
		}
		if canonicalURL != value {
			return fmt.Errorf("%s %d must use canonical spelling %q", field, index, canonicalURL)
		}
		if index > 0 && values[index-1] >= value {
			return fmt.Errorf("%s must be unique and sorted", field)
		}
	}
	return nil
}

func portableToolCatalogRecordSegmentV1(value string) bool {
	if value == "" || value == "." || value == ".." || strings.TrimSpace(value) != value || containsPortableControl(value) {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '.' || character == '+' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func portableToolCatalogPlatformV1(value string) bool {
	return value == "linux/amd64" || value == "linux/arm64"
}

func portableToolCatalogPayloadKindV1(value string) bool {
	return value == "jdk-archive" || value == "playwright-browser-archive" || value == "raw-executable"
}

func portableToolCatalogWheelDistributionV1(value string) bool {
	if value == "" || value[0] == '_' {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' {
			continue
		}
		return false
	}
	return strings.ReplaceAll(portableToolPythonNormalizeDistributionNameV1(value), "-", "_") == value
}

func portableToolCatalogWheelBuildTagV1(value string) bool {
	if value == "" || value[0] < '0' || value[0] > '9' {
		return false
	}
	for _, character := range value[1:] {
		if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}

func portableToolCatalogWheelTagGroupV1(value string) bool {
	for _, component := range strings.Split(value, ".") {
		if !portableToolCatalogWheelTagComponentV1(component) {
			return false
		}
	}
	return true
}

func portableToolCatalogWheelTagComponentV1(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' {
			continue
		}
		return false
	}
	return true
}

// WheelPlatformProjectionV1 is the single portable-tool wheel platform
// policy projection. It deliberately contains only the policy facts needed
// by record validation and provider runtime guards; pip remains the runtime
// compatibility authority.
type WheelPlatformProjectionV1 struct {
	Kind              string
	Architecture      string
	MinimumGlibcMajor string
	MinimumGlibcMinor string
}

const (
	WheelPlatformAnyV1       = "any"
	WheelPlatformLinuxV1     = "linux"
	WheelPlatformManylinuxV1 = "manylinux"
)

// ProjectWheelPlatformV1 validates and projects one canonical wheel platform
// tag. Unsupported policy aliases, numeric versions, architectures, and
// floors are rejected so all consumers share this one fail-closed table.
func ProjectWheelPlatformV1(tag string) (WheelPlatformProjectionV1, error) {
	if tag == "any" {
		return WheelPlatformProjectionV1{Kind: WheelPlatformAnyV1}, nil
	}
	architecture := ""
	if strings.HasSuffix(tag, "_x86_64") {
		architecture = "x86_64"
	} else if strings.HasSuffix(tag, "_aarch64") {
		architecture = "aarch64"
	} else {
		return WheelPlatformProjectionV1{}, fmt.Errorf("wheel platform %q has an unsupported architecture", tag)
	}
	policy := strings.TrimSuffix(tag, "_"+architecture)
	projection := WheelPlatformProjectionV1{Architecture: architecture}
	switch policy {
	case "linux":
		projection.Kind = WheelPlatformLinuxV1
	case "manylinux1":
		if architecture != "x86_64" {
			return WheelPlatformProjectionV1{}, fmt.Errorf("wheel platform %q is unsupported on %s", tag, architecture)
		}
		projection.Kind, projection.MinimumGlibcMajor, projection.MinimumGlibcMinor = WheelPlatformManylinuxV1, "2", "5"
	case "manylinux2010":
		if architecture != "x86_64" {
			return WheelPlatformProjectionV1{}, fmt.Errorf("wheel platform %q is unsupported on %s", tag, architecture)
		}
		projection.Kind, projection.MinimumGlibcMajor, projection.MinimumGlibcMinor = WheelPlatformManylinuxV1, "2", "12"
	case "manylinux2014":
		projection.Kind, projection.MinimumGlibcMajor, projection.MinimumGlibcMinor = WheelPlatformManylinuxV1, "2", "17"
	default:
		version, found := strings.CutPrefix(policy, "manylinux_")
		if !found {
			return WheelPlatformProjectionV1{}, fmt.Errorf("wheel platform %q uses an unsupported policy", tag)
		}
		parts := strings.Split(version, "_")
		if len(parts) != 2 || validatePortableToolCatalogDecimalV1("manylinux major", parts[0], false) != nil || validatePortableToolCatalogDecimalV1("manylinux minor", parts[1], false) != nil {
			return WheelPlatformProjectionV1{}, fmt.Errorf("wheel platform %q has an invalid manylinux version", tag)
		}
		if parts[0] != "2" {
			return WheelPlatformProjectionV1{}, fmt.Errorf("wheel platform %q uses unsupported manylinux major %q", tag, parts[0])
		}
		minimum := uint64(5)
		if architecture == "aarch64" {
			minimum = 17
		}
		minor, _ := strconv.ParseUint(parts[1], 10, 63)
		if minor < minimum {
			return WheelPlatformProjectionV1{}, fmt.Errorf("wheel platform %q is below the %s architecture floor", tag, architecture)
		}
		projection.Kind, projection.MinimumGlibcMajor, projection.MinimumGlibcMinor = WheelPlatformManylinuxV1, parts[0], parts[1]
	}
	return projection, nil
}

// ProjectWheelPlatformForTargetV1 additionally proves that a projected
// architecture belongs to the selected Linux target. The any policy is
// architecture independent.
func ProjectWheelPlatformForTargetV1(tag, target string) (WheelPlatformProjectionV1, error) {
	projection, err := ProjectWheelPlatformV1(tag)
	if err != nil {
		return WheelPlatformProjectionV1{}, err
	}
	architecture := ""
	switch target {
	case "linux/amd64":
		architecture = "x86_64"
	case "linux/arm64":
		architecture = "aarch64"
	default:
		return WheelPlatformProjectionV1{}, fmt.Errorf("wheel target %q is unsupported", target)
	}
	if projection.Kind == WheelPlatformAnyV1 {
		return projection, nil
	}
	if projection.Architecture != architecture {
		return WheelPlatformProjectionV1{}, fmt.Errorf("wheel platform %q does not match target %q", tag, target)
	}
	return projection, nil
}

func portableToolCatalogStringsEqualV1(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validatePortableToolRecordReferenceV1(reference RecordReferenceV1) error {
	if err := validatePortableRecordID(reference.ID); err != nil {
		return err
	}
	if err := reference.Digest.Validate(); err != nil {
		return fmt.Errorf("portable tool record %q digest: %w", reference.ID, err)
	}
	return nil
}

func validatePortableToolCatalogReferenceListV1(field string, references []RecordReferenceV1) error {
	if references == nil || len(references) > portableToolCatalogMaxReferencesV1 {
		return fmt.Errorf("%s must use an array with at most %d entries", field, portableToolCatalogMaxReferencesV1)
	}
	for index, reference := range references {
		if err := validatePortableToolRecordReferenceV1(reference); err != nil {
			return fmt.Errorf("%s[%d]: %w", field, index, err)
		}
		if index > 0 && references[index-1].ID >= reference.ID {
			return fmt.Errorf("%s must be unique and sorted by ID", field)
		}
	}
	return nil
}

func validatePortableToolCatalogProfileReferenceListV1(
	field string,
	references []RecordReferenceV1,
	releasePrefix string,
	allowEmpty bool,
) error {
	if err := validatePortableToolCatalogReferenceListV1(field, references); err != nil {
		return err
	}
	if !allowEmpty && len(references) == 0 {
		return fmt.Errorf("%s must not be empty", field)
	}
	for _, reference := range references {
		segments := strings.Split(reference.ID, "/")
		if len(segments) != 6 || strings.Join(segments[:3], "/") != releasePrefix ||
			segments[3] != "validation" || segments[4] != "profiles" ||
			validatePortableRecordIdentifier("validation profile", segments[5]) != nil {
			return fmt.Errorf("%s %q must name a validation profile under %q", field, reference.ID, releasePrefix+"/validation/profiles")
		}
	}
	return nil
}

func validatePortableToolCatalogTargetReferenceV1(reference RecordReferenceV1, releasePrefix string) error {
	segments := strings.Split(reference.ID, "/")
	valid := len(segments) == 7 && strings.Join(segments[:3], "/") == releasePrefix &&
		segments[3] == "targets" && validatePortableRecordIdentifier("target OS release ID", segments[4]) == nil &&
		portableToolCatalogRecordSegmentV1(segments[5]) && (segments[6] == "amd64" || segments[6] == "arm64")
	if !valid {
		return fmt.Errorf("release target %q must name a target record under %q", reference.ID, releasePrefix+"/targets")
	}
	return nil
}

func validatePortableToolCatalogArtifactTargetReferenceV1(reference RecordReferenceV1, releasePrefix string) error {
	segments := strings.Split(reference.ID, "/")
	if len(segments) >= 5 && strings.Join(segments[:3], "/") == releasePrefix {
		switch {
		case len(segments) == 5 && segments[3] == "payloads" && portableToolCatalogPayloadLeafV1(segments[4]):
			return nil
		case len(segments) == 6 && segments[3] == "payloads" &&
			validatePortableRecordIdentifier("payload selector", segments[4]) == nil && portableToolCatalogPayloadLeafV1(segments[5]):
			return nil
		case len(segments) == 7 && segments[3] == "bindings" &&
			validatePortableRecordIdentifier("binding", segments[4]) == nil && segments[5] == "artifacts" &&
			portableToolCatalogPlatformLeafV1(segments[6]):
			return nil
		}
	}
	return fmt.Errorf("artifact source mapping artifact %q must reference a payload or binding artifact record inside namespace %q", reference.ID, releasePrefix)
}

func validatePortableToolCatalogSourceReferenceV1(reference RecordReferenceV1, releasePrefix, revision string) error {
	segments := strings.Split(reference.ID, "/")
	if len(segments) != 7 || strings.Join(segments[:3], "/") != releasePrefix || segments[3] != "revisions" ||
		segments[4] != revision || segments[5] != "sources" || validatePortableRecordIdentifier("artifact source", segments[6]) != nil {
		return fmt.Errorf("artifact source mapping source %q must name an artifact source record in revision %q", reference.ID, revision)
	}
	return nil
}

func portableToolCatalogPayloadLeafV1(value string) bool {
	platform := strings.LastIndex(value, "-")
	if platform < 0 {
		return false
	}
	name := strings.LastIndex(value[:platform], "-")
	if name < 0 {
		return false
	}
	return validatePortableRecordIdentifier("payload", value[:name]) == nil && portableToolCatalogPlatformLeafV1(value[name+1:])
}

func portableToolCatalogPlatformLeafV1(value string) bool {
	return value == "linux-amd64" || value == "linux-arm64"
}

func validatePortableRecordIdentifier(field, value string) error {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return fmt.Errorf("%s %q must match [a-z][a-z0-9-]*", field, value)
	}
	for _, char := range value[1:] {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return fmt.Errorf("%s %q must match [a-z][a-z0-9-]*", field, value)
		}
	}
	return nil
}

func containsPortableControl(value string) bool {
	for _, char := range value {
		if unicode.IsControl(char) {
			return true
		}
	}
	return false
}

func validatePortableRecordID(value string) error {
	if value == "" || strings.TrimSpace(value) != value || !strings.HasPrefix(value, "tool:") {
		return fmt.Errorf("record ID %q must be a canonical tool-qualified ID", value)
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' {
			continue
		}
		switch character {
		case '.', '+', '-', '_', ':', '/':
		case '%':
			if index+2 >= len(value) {
				return fmt.Errorf("record ID %q contains an incomplete percent escape", value)
			}
			if _, ok := portableUppercaseHexValue(value[index+1]); !ok {
				return fmt.Errorf("record ID %q percent escapes must use uppercase hexadecimal", value)
			}
			if _, ok := portableUppercaseHexValue(value[index+2]); !ok {
				return fmt.Errorf("record ID %q percent escapes must use uppercase hexadecimal", value)
			}
			index += 2
		default:
			return fmt.Errorf("record ID %q contains unsupported character %q", value, character)
		}
	}
	segments := strings.Split(value, "/")
	if len(segments) == 0 || !validPortableRecordSegment(strings.TrimPrefix(segments[0], "tool:")) {
		return fmt.Errorf("record ID %q has an invalid tool name", value)
	}
	for index, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("record ID %q contains an invalid path segment", value)
		}
		if index != 2 && strings.Contains(segment, "%") {
			return fmt.Errorf("record ID %q contains an escape outside its version segment", value)
		}
	}
	if len(segments) < 4 || segments[1] != "releases" {
		return fmt.Errorf("record ID %q must use a tool release namespace", value)
	}
	if err := validatePortableToolVersionSegment(segments[2]); err != nil {
		return fmt.Errorf("record ID %q version segment: %w", value, err)
	}
	return nil
}

func validPortableRecordSegment(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for index := 1; index < len(value); index++ {
		char := value[index]
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			continue
		}
		if char != '-' {
			return false
		}
	}
	return true
}

func validatePortableToolVersionSegment(value string) error {
	if value == "" {
		return fmt.Errorf("encoded tool version must not be empty")
	}
	decoded := make([]byte, 0, len(value))
	for index := 0; index < len(value); index++ {
		if value[index] != '%' {
			decoded = append(decoded, value[index])
			continue
		}
		if index+2 >= len(value) {
			return fmt.Errorf("encoded tool version contains an incomplete escape")
		}
		high, highOK := portableUppercaseHexValue(value[index+1])
		low, lowOK := portableUppercaseHexValue(value[index+2])
		if !highOK || !lowOK {
			return fmt.Errorf("encoded tool version escapes must use uppercase hexadecimal")
		}
		decoded = append(decoded, high<<4|low)
		index += 2
	}
	version := string(decoded)
	canonicalVersion, err := encodePortableToolVersionSegment(version)
	if err != nil || canonicalVersion != value {
		return fmt.Errorf("encoded tool version is not canonical")
	}
	return nil
}

func encodePortableToolVersionSegment(value string) (string, error) {
	if value == "" || strings.TrimSpace(value) != value || containsPortableControl(value) || !utf8.ValidString(value) {
		return "", fmt.Errorf("tool version must be canonical UTF-8 text")
	}
	encodeDots := value == "." || value == ".."
	const hexadecimal = "0123456789ABCDEF"
	var encoded strings.Builder
	encoded.Grow(len(value))
	for _, character := range []byte(value) {
		literal := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune(".+-_", rune(character))
		if literal && !(encodeDots && character == '.') {
			encoded.WriteByte(character)
			continue
		}
		encoded.WriteByte('%')
		encoded.WriteByte(hexadecimal[character>>4])
		encoded.WriteByte(hexadecimal[character&0x0f])
	}
	return encoded.String(), nil
}

func portableUppercaseHexValue(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}
