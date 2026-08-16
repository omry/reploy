package toolcatalog

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	pep440 "github.com/aquasecurity/go-pep440-version"
	"github.com/aquasecurity/go-version/pkg/semver"
	dockerreference "github.com/distribution/reference"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

func validateLoadedRecordV1(record loadedRecordV1) error {
	if err := validateRecordIDV1(record.ID); err != nil {
		return err
	}
	switch value := record.Value.(type) {
	case *ToolRecordV1:
		if record.Schema != ToolRecordSchemaV1 || value.Schema != ToolRecordSchemaV1 || value.ID != record.ID || !validRecordIdentifierV1(value.Name) || value.ID != "tool:"+value.Name {
			return fmt.Errorf("tool record identity is inconsistent")
		}
		if err := validateToolVersionPolicyV1(value.VersionScheme, value.DefaultVersion); err != nil {
			return err
		}
		if !validRecordTokenV1(value.Summary) || value.Upstream == "" || value.Source == "" || value.License == "" || len(value.Releases) == 0 {
			return fmt.Errorf("tool metadata and releases must not be empty")
		}
		for _, raw := range []string{value.Upstream, value.Source} {
			if err := validateSourceURLV1(raw); err != nil {
				return fmt.Errorf("tool reference URL: %w", err)
			}
		}
		if !validRecordTokenV1(value.License) {
			return fmt.Errorf("tool license is invalid")
		}
		if err := validateReferenceListV1("tool releases", value.Releases); err != nil {
			return err
		}
		prefix := value.ID + "/releases/"
		for index, reference := range value.Releases {
			segments := strings.Split(reference.ID, "/")
			if !strings.HasPrefix(reference.ID, prefix) || len(segments) != 6 || segments[3] != "revisions" || segments[5] != "manifest" {
				return fmt.Errorf("tool release reference %d must identify a manifest beneath %q", index, prefix)
			}
		}
		return nil
	case *ReleaseManifestV1:
		if record.Schema != ReleaseManifestSchemaV1 || value.Schema != ReleaseManifestSchemaV1 || value.ID != record.ID || !validRecordIdentifierV1(value.Tool) {
			return fmt.Errorf("release manifest identity is incomplete")
		}
		versionSegment, err := encodeToolVersionSegmentV1(value.Version)
		if err != nil {
			return fmt.Errorf("release manifest version: %w", err)
		}
		if err := validateCanonicalDecimalV1("release revision", value.Revision, true); err != nil {
			return err
		}
		if err := validateSortedUniqueStringsV1("release aliases", value.Aliases, false); err != nil {
			return err
		}
		for _, alias := range value.Aliases {
			if _, err := encodeToolVersionSegmentV1(alias); err != nil {
				return fmt.Errorf("release alias %q: %w", alias, err)
			}
			if alias == value.Version {
				return fmt.Errorf("release alias %q redundantly equals its exact version", alias)
			}
		}
		releasePrefix := fmt.Sprintf("tool:%s/releases/%s", value.Tool, versionSegment)
		manifestID := fmt.Sprintf("%s/revisions/%s/manifest", releasePrefix, value.Revision)
		if value.ID != manifestID {
			return fmt.Errorf("release manifest ID must be %q", manifestID)
		}
		if err := validateRecordReferenceV1(value.Contract); err != nil {
			return fmt.Errorf("release contract: %w", err)
		}
		if value.Contract.ID != releasePrefix+"/contract" {
			return fmt.Errorf("release contract reference must identify the current release contract")
		}
		if err := validateRecordReferenceV1(value.ValidationProfile); err != nil {
			return fmt.Errorf("release validation profile: %w", err)
		}
		if err := validateReferenceUnderV1("release validation profile", value.ValidationProfile, releasePrefix+"/validation/profiles"); err != nil {
			return err
		}
		if len(value.Targets) == 0 {
			return fmt.Errorf("release manifest targets must not be empty")
		}
		if err := validateReferenceListV1("release targets", value.Targets); err != nil {
			return err
		}
		for _, reference := range value.Targets {
			if err := validateReferenceUnderV1("release target", reference, releasePrefix+"/targets"); err != nil {
				return err
			}
		}
		if value.ArtifactSources == nil || len(value.ArtifactSources) > maxDefinitionReferences {
			return fmt.Errorf("artifact source mappings must use a bounded array")
		}
		for index, mapping := range value.ArtifactSources {
			if err := mapping.ArtifactSHA256.Validate(); err != nil {
				return fmt.Errorf("artifact source mapping %d digest: %w", index, err)
			}
			if err := validateRecordReferenceV1(mapping.Artifact); err != nil {
				return fmt.Errorf("artifact source mapping %d artifact: %w", index, err)
			}
			if err := validateReferenceUnderV1("artifact source mapping artifact", mapping.Artifact, releasePrefix); err != nil {
				return err
			}
			if err := validateRecordReferenceV1(mapping.Source); err != nil {
				return fmt.Errorf("artifact source mapping %d source: %w", index, err)
			}
			if err := validateReferenceUnderV1("artifact source mapping source", mapping.Source, fmt.Sprintf("%s/revisions/%s/sources", releasePrefix, value.Revision)); err != nil {
				return err
			}
			if index > 0 && value.ArtifactSources[index-1].ArtifactSHA256 >= mapping.ArtifactSHA256 {
				return fmt.Errorf("artifact source mappings must be unique and sorted by artifact digest")
			}
		}
		if value.Provenance == nil || len(value.Provenance) > maxDefinitionReferences {
			return fmt.Errorf("release provenance must use a bounded array")
		}
		previousProvenance := ""
		for index, raw := range value.Provenance {
			if err := validateSourceURLV1(raw); err != nil {
				return fmt.Errorf("release provenance %d: %w", index, err)
			}
			if index > 0 && previousProvenance >= raw {
				return fmt.Errorf("release provenance must be unique and sorted")
			}
			previousProvenance = raw
		}
		return nil
	case *ReleaseContractV1:
		if record.Schema != ReleaseContractSchemaV1 || value.Schema != ReleaseContractSchemaV1 || value.ID != record.ID {
			return fmt.Errorf("release contract identity is inconsistent")
		}
		if err := validateReleaseContractIDV1(value.ID); err != nil {
			return err
		}
		if err := requireNonemptySortedStringsV1("contract contexts", value.Contexts); err != nil {
			return err
		}
		for _, context := range value.Contexts {
			if context != "build" && context != "runtime" {
				return fmt.Errorf("contract context %q is unsupported", context)
			}
		}
		if err := validateSupportedReployRequirementV1(value.SupportedReploy); err != nil {
			return err
		}
		if err := requireNonemptySortedStringsV1("resolver primitives", value.ResolverPrimitives); err != nil {
			return err
		}
		for _, primitive := range value.ResolverPrimitives {
			if primitive != "https-sha256" {
				return fmt.Errorf("resolver primitive %q is unsupported", primitive)
			}
		}
		if err := validateBindingRequestV1(value.Binding); err != nil {
			return err
		}
		if err := validateSelectionRequestV1(value.Selections); err != nil {
			return err
		}
		if err := validateParameterSchemasV1(value.Parameters); err != nil {
			return err
		}
		if err := validateProbeListV1("contract probes", value.Probes, true); err != nil {
			return err
		}
		if err := validateExportsV1("contract exports", value.Exports); err != nil {
			return err
		}
		return validateRuntimeV1(value.Contexts, value.Runtime)
	case *TargetRecordV1:
		if record.Schema != TargetRecordSchemaV1 || value.Schema != TargetRecordSchemaV1 || value.ID != record.ID {
			return fmt.Errorf("target record identity or validation contract is incomplete")
		}
		if err := validateTargetIdentityV1(value.Target); err != nil {
			return err
		}
		if err := validateTargetRecordIDV1(value.ID, value.Target); err != nil {
			return err
		}
		releasePrefix := strings.Join(strings.Split(value.ID, "/")[:3], "/")
		if len(value.IntegrationFixtures) == 0 {
			return fmt.Errorf("target integration fixtures must not be empty")
		}
		if err := validateReferenceListV1("target integration fixtures", value.IntegrationFixtures); err != nil {
			return err
		}
		for _, reference := range value.IntegrationFixtures {
			if err := validateReferenceUnderV1("target integration fixture", reference, releasePrefix+"/validation/fixtures"); err != nil {
				return err
			}
		}
		if err := validateRecordReferenceV1(value.ValidationProfile); err != nil {
			return fmt.Errorf("target validation profile: %w", err)
		}
		if err := validateReferenceUnderV1("target validation profile", value.ValidationProfile, releasePrefix+"/validation/profiles"); err != nil {
			return err
		}
		if err := validateReferenceListV1("target package sets", value.PackageSets); err != nil {
			return err
		}
		for _, reference := range value.PackageSets {
			if err := validateReferenceUnderV1("target package set", reference, releasePrefix+"/package-sets"); err != nil {
				return err
			}
		}
		if err := validateReferenceListV1("target payloads", value.Payloads); err != nil {
			return err
		}
		for _, reference := range value.Payloads {
			if err := validateReferenceUnderV1("target payload", reference, releasePrefix+"/payloads"); err != nil {
				return err
			}
		}
		if value.Bindings == nil || len(value.Bindings) > maxDefinitionReferences {
			return fmt.Errorf("target bindings must use a bounded array")
		}
		for index, binding := range value.Bindings {
			if !validRecordIdentifierV1(binding.Name) || index > 0 && value.Bindings[index-1].Name >= binding.Name {
				return fmt.Errorf("target bindings must be unique and sorted")
			}
			if err := validateRecordReferenceV1(binding.Contract); err != nil {
				return fmt.Errorf("target binding %q contract: %w", binding.Name, err)
			}
			if binding.Contract.ID != fmt.Sprintf("%s/bindings/%s/contract", releasePrefix, binding.Name) {
				return fmt.Errorf("target binding %q contract must identify its current-release binding contract", binding.Name)
			}
			if len(binding.Artifacts) == 0 {
				return fmt.Errorf("target binding %q artifacts must not be empty", binding.Name)
			}
			if err := validateReferenceListV1("target binding artifacts", binding.Artifacts); err != nil {
				return err
			}
			for _, reference := range binding.Artifacts {
				if err := validateReferenceUnderV1("target binding artifact", reference, fmt.Sprintf("%s/bindings/%s/artifacts", releasePrefix, binding.Name)); err != nil {
					return err
				}
			}
			if err := validateReferenceListV1("target binding package sets", binding.PackageSets); err != nil {
				return err
			}
			for _, reference := range binding.PackageSets {
				if err := validateReferenceUnderV1("target binding package set", reference, releasePrefix+"/package-sets"); err != nil {
					return err
				}
			}
			if err := validateExportsV1("target binding exports", binding.Exports); err != nil {
				return err
			}
			if err := validateProbeListV1("target binding probes", binding.Probes, true); err != nil {
				return err
			}
		}
		if value.Selections == nil || len(value.Selections) > maxDefinitionReferences {
			return fmt.Errorf("target selections must use a bounded array")
		}
		for index, selection := range value.Selections {
			if !validRecordIdentifierV1(selection.Name) || index > 0 && value.Selections[index-1].Name >= selection.Name {
				return fmt.Errorf("target selections must be unique, sorted, and nonempty")
			}
			if err := validateReferenceListV1("target selection payloads", selection.Payloads); err != nil {
				return err
			}
			for _, reference := range selection.Payloads {
				if err := validateReferenceUnderV1("target selection payload", reference, releasePrefix+"/payloads"); err != nil {
					return err
				}
			}
			if err := validateReferenceListV1("target selection package sets", selection.PackageSets); err != nil {
				return err
			}
			for _, reference := range selection.PackageSets {
				if err := validateReferenceUnderV1("target selection package set", reference, releasePrefix+"/package-sets"); err != nil {
					return err
				}
			}
			if err := validateExportsV1("target selection exports", selection.Exports); err != nil {
				return err
			}
			if err := validateProbeListV1("target selection probes", selection.Probes, true); err != nil {
				return err
			}
			if len(selection.Payloads)+len(selection.PackageSets)+len(selection.Exports)+len(selection.Probes) == 0 {
				return fmt.Errorf("target selection %q must contribute at least one record, export, or probe", selection.Name)
			}
		}
		if err := validateTargetParameterConstraintsV1(value.Parameters); err != nil {
			return err
		}
		if err := validateExportsV1("target exports", value.Exports); err != nil {
			return err
		}
		return validateProbeListV1("target probes", value.Probes, true)
	case *BindingContractV1:
		if record.Schema != BindingContractSchemaV1 || value.Schema != BindingContractSchemaV1 || value.ID != record.ID || !validRecordIdentifierV1(value.Name) || !validPackageNameV1(value.Package) || value.CLI == "" {
			return fmt.Errorf("binding contract is incomplete")
		}
		if err := validateBindingContractIDV1(value.ID, value.Name); err != nil {
			return err
		}
		if err := validateAbsoluteRecordPathV1(value.CLI); err != nil {
			return fmt.Errorf("binding CLI: %w", err)
		}
		if err := requireNonemptySortedStringsV1("binding requirements", value.Requirements); err != nil {
			return err
		}
		distributions := make(map[string]string, len(value.Requirements))
		for _, requirement := range value.Requirements {
			distribution, err := pythonprovider.PackageRootDistributionNameV1(requirement)
			if err != nil {
				return fmt.Errorf("binding requirement %q: %w", requirement, err)
			}
			if previous, found := distributions[distribution]; found {
				return fmt.Errorf("binding requirements %q and %q name the same distribution %q", previous, requirement, distribution)
			}
			distributions[distribution] = requirement
		}
		if err := requireNonemptySortedStringsV1("supported Python", value.SupportedPython); err != nil {
			return err
		}
		for _, version := range value.SupportedPython {
			if err := pythonprovider.ValidateInterpreterVersionV1(version); err != nil {
				return fmt.Errorf("supported Python version %q: %w", version, err)
			}
		}
		return nil
	case *BindingArtifactRecordV1:
		if record.Schema != BindingArtifactSchemaV1 || value.Schema != BindingArtifactSchemaV1 || value.ID != record.ID || !validRecordIdentifierV1(value.Binding) || !validPlatformV1(value.Platform) || validateRecordPathV1(value.Filename, false) != nil || path.Dir(value.Filename) != "." {
			return fmt.Errorf("binding artifact identity is incomplete")
		}
		if err := validateBindingArtifactIDV1(value.ID, value.Binding, value.Platform); err != nil {
			return err
		}
		if err := validateBindingArtifactCompatibilityV1(value); err != nil {
			return err
		}
		if err := validateCanonicalDecimalV1("binding artifact size", value.Size, true); err != nil {
			return err
		}
		if err := value.SHA256.Validate(); err != nil {
			return fmt.Errorf("binding artifact digest: %w", err)
		}
		if value.BundledComponents == nil || len(value.BundledComponents) > maxDefinitionReferences {
			return fmt.Errorf("binding artifact bundled components must use a bounded array")
		}
		for index, component := range value.BundledComponents {
			if !validRecordIdentifierV1(component.Name) || !validRecordSegmentV1(component.Version) || validateRecordPathV1(component.Path, false) != nil || index > 0 && value.BundledComponents[index-1].Name >= component.Name {
				return fmt.Errorf("binding artifact bundled components must be complete, unique, and sorted")
			}
		}
		return nil
	case *PayloadRecordV1:
		if record.Schema != PayloadRecordSchemaV1 || value.Schema != PayloadRecordSchemaV1 || value.ID != record.ID || !validRecordIdentifierV1(value.Name) || !validRecordSegmentV1(value.Revision) || !validRecordSegmentV1(value.UpstreamVersion) || !validPlatformV1(value.Platform) || !supportedPayloadKindV1(value.Kind) {
			return fmt.Errorf("payload identity is incomplete")
		}
		if value.Selection != "" && !validRecordIdentifierV1(value.Selection) {
			return fmt.Errorf("payload selection is invalid")
		}
		if err := validatePayloadIDV1(value); err != nil {
			return err
		}
		if err := validateRecordPathV1(value.LogicalPath, false); err != nil {
			return fmt.Errorf("payload logical path: %w", err)
		}
		if err := validateCanonicalDecimalV1("payload size", value.Size, true); err != nil {
			return err
		}
		if err := validateCanonicalDecimalV1("payload entries", value.Entries, true); err != nil {
			return err
		}
		if err := validateCanonicalDecimalV1("payload unpacked size", value.UnpackedSize, true); err != nil {
			return err
		}
		if err := value.SHA256.Validate(); err != nil {
			return fmt.Errorf("payload digest: %w", err)
		}
		if err := validateRecordPathV1(value.InstallDirectory, false); err != nil {
			return fmt.Errorf("payload install directory: %w", err)
		}
		if err := validateRecordPathV1(value.ArchiveRoot, true); err != nil {
			return fmt.Errorf("payload archive root: %w", err)
		}
		if err := validateRecordPathV1(value.Executable, false); err != nil {
			return fmt.Errorf("payload executable: %w", err)
		}
		if path.Dir(value.InstallDirectory) != "." || value.ArchiveRoot != "." && value.Executable != value.ArchiveRoot && !strings.HasPrefix(value.Executable, value.ArchiveRoot+"/") {
			return fmt.Errorf("payload paths are inconsistent")
		}
		if value.Kind == "raw-executable" && (value.Entries != "1" || value.UnpackedSize != value.Size || value.ArchiveRoot != ".") {
			return fmt.Errorf("raw executable payload inventory is inconsistent")
		}
		return nil
	case *ArtifactSourceRecordV1:
		if record.Schema != ArtifactSourceRecordSchemaV1 || value.Schema != ArtifactSourceRecordSchemaV1 || value.ID != record.ID || value.Resolver != "https-sha256" {
			return fmt.Errorf("artifact source identity or resolver is unsupported")
		}
		if err := validateArtifactSourceIDV1(value.ID); err != nil {
			return err
		}
		if err := value.SHA256.Validate(); err != nil {
			return fmt.Errorf("artifact source digest: %w", err)
		}
		if err := validateCanonicalDecimalV1("artifact source size", value.Size, true); err != nil {
			return err
		}
		if len(value.Mirrors) == 0 || len(value.Mirrors) > maxDefinitionArtifactMirrors {
			return fmt.Errorf("artifact source mirrors must contain between 1 and %d entries", maxDefinitionArtifactMirrors)
		}
		seenMirrors := make(map[string]struct{}, len(value.Mirrors))
		for index, mirror := range value.Mirrors {
			if err := validateSourceURLV1(mirror); err != nil {
				return fmt.Errorf("artifact source mirror %d: %w", index, err)
			}
			if _, exists := seenMirrors[mirror]; exists {
				return fmt.Errorf("artifact source mirrors must be unique")
			}
			seenMirrors[mirror] = struct{}{}
		}
		if len(value.Provenance) == 0 || len(value.Provenance) > maxDefinitionReferences {
			return fmt.Errorf("artifact source provenance must use a nonempty bounded array")
		}
		previousProvenance := ""
		for index, provenance := range value.Provenance {
			if err := validateSourceURLV1(provenance); err != nil {
				return fmt.Errorf("artifact source provenance %d: %w", index, err)
			}
			if index > 0 && previousProvenance >= provenance {
				return fmt.Errorf("artifact source provenance must be unique and sorted")
			}
			previousProvenance = provenance
		}
		return nil
	case *NativePackageSetV1:
		if record.Schema != NativePackageSetSchemaV1 || value.Schema != NativePackageSetSchemaV1 || value.ID != record.ID || value.Manager != "apt" {
			return fmt.Errorf("native package-set identity is incomplete")
		}
		if err := validateNativePackageSetIDV1(value.ID); err != nil {
			return err
		}
		if err := requireNonemptySortedStringsV1("native package requirements", value.Requirements); err != nil {
			return err
		}
		packages := make(map[string]string, len(value.Requirements))
		for _, requirement := range value.Requirements {
			parsed, err := blueprint.ParseAPTPackageRequest(requirement)
			if err != nil {
				return fmt.Errorf("native package requirement %q: %w", requirement, err)
			}
			if previous, found := packages[parsed.Name]; found {
				return fmt.Errorf("native package requirements %q and %q name the same package %q", previous, requirement, parsed.Name)
			}
			packages[parsed.Name] = requirement
		}
		return nil
	case *IntegrationFixtureRecordV1:
		if record.Schema != IntegrationFixtureSchemaV1 || value.Schema != IntegrationFixtureSchemaV1 || value.ID != record.ID || !validRecordIdentifierV1(value.Name) {
			return fmt.Errorf("integration fixture identity is inconsistent")
		}
		if err := validateTargetIdentityV1(value.Target); err != nil {
			return fmt.Errorf("integration fixture target: %w", err)
		}
		segments := strings.Split(value.ID, "/")
		if len(segments) != 6 || segments[1] != "releases" || segments[3] != "validation" || segments[4] != "fixtures" || segments[5] != value.Name {
			return fmt.Errorf("integration fixture ID must use its name in a release validation fixture namespace")
		}
		if _, err := decodeToolVersionSegmentV1(segments[2]); err != nil {
			return fmt.Errorf("integration fixture ID version: %w", err)
		}
		if !validBaseImageReferenceV1(value.BaseImage) {
			return fmt.Errorf("integration fixture base image must be a canonical tagged OCI reference")
		}
		if err := value.BaseImageDigest.Validate(); err != nil {
			return fmt.Errorf("integration fixture base image digest: %w", err)
		}
		if value.Context != "build" && value.Context != "runtime" {
			return fmt.Errorf("integration fixture context is unsupported")
		}
		if value.Binding != "" && !validRecordIdentifierV1(value.Binding) {
			return fmt.Errorf("integration fixture binding is invalid")
		}
		if err := validateSortedUniqueStringsV1("integration fixture selections", value.Selections, false); err != nil {
			return err
		}
		return validateParameterValuesV1("integration fixture parameters", value.Parameters)
	case *ValidationProfileRecordV1:
		if record.Schema != ValidationProfileSchemaV1 || value.Schema != ValidationProfileSchemaV1 || value.ID != record.ID || !validRecordIdentifierV1(value.Tool) {
			return fmt.Errorf("validation profile identity is inconsistent")
		}
		versionSegment, err := encodeToolVersionSegmentV1(value.Version)
		if err != nil {
			return fmt.Errorf("validation profile version: %w", err)
		}
		expectedID := fmt.Sprintf("tool:%s/releases/%s/validation/profiles/default", value.Tool, versionSegment)
		if value.ID != expectedID {
			return fmt.Errorf("validation profile ID must be %q", expectedID)
		}
		if value.Validator != "java-jdk" && value.Validator != "playwright-python-browser" {
			return fmt.Errorf("validation profile validator is unsupported")
		}
		if _, err := encodeToolVersionSegmentV1(value.ValidatorVersion); err != nil {
			return fmt.Errorf("validation profile validator version: %w", err)
		}
		if err := validateProbeListV1("validation profile probes", value.Probes, false); err != nil {
			return err
		}
		if value.Network != "none" {
			return fmt.Errorf("validation profile must disable networking")
		}
		return nil
	default:
		return fmt.Errorf("unsupported record value %T", record.Value)
	}
}

func validateReferenceUnderV1(field string, reference RecordReferenceV1, namespace string) error {
	if !strings.HasPrefix(reference.ID, strings.TrimSuffix(namespace, "/")+"/") {
		return fmt.Errorf("%s %q must remain inside namespace %q", field, reference.ID, namespace)
	}
	return nil
}

// validateManifestArtifactContentV1 verifies relationships that require the
// immutable records referenced by a manifest to have been resolved.
func validateManifestArtifactContentV1(manifest *ReleaseManifestV1, records map[string]loadedRecordV1) error {
	for index, mapping := range manifest.ArtifactSources {
		artifact, exists := records[mapping.Artifact.ID]
		if !exists || artifact.ID != mapping.Artifact.ID || artifact.Digest != mapping.Artifact.Digest {
			return fmt.Errorf("artifact source mapping %d does not resolve its exact artifact record", index)
		}
		var artifactSHA256 canonical.Digest
		var artifactSize string
		switch value := artifact.Value.(type) {
		case *BindingArtifactRecordV1:
			artifactSHA256, artifactSize = value.SHA256, value.Size
		case *PayloadRecordV1:
			artifactSHA256, artifactSize = value.SHA256, value.Size
		default:
			return fmt.Errorf("artifact source mapping %d references a non-artifact record", index)
		}

		sourceRecord, exists := records[mapping.Source.ID]
		if !exists || sourceRecord.ID != mapping.Source.ID || sourceRecord.Digest != mapping.Source.Digest {
			return fmt.Errorf("artifact source mapping %d does not resolve its exact source record", index)
		}
		source, ok := sourceRecord.Value.(*ArtifactSourceRecordV1)
		if !ok {
			return fmt.Errorf("artifact source mapping %d references a non-source record", index)
		}
		if mapping.ArtifactSHA256 != artifactSHA256 || mapping.ArtifactSHA256 != source.SHA256 {
			return fmt.Errorf("artifact source mapping %d content digests disagree", index)
		}
		if artifactSize != source.Size {
			return fmt.Errorf("artifact source mapping %d content sizes disagree", index)
		}
	}
	return nil
}

func validateToolVersionPolicyV1(scheme string, defaultVersion string) error {
	switch scheme {
	case "semver", "pep440", "integer":
		if defaultVersion != "" {
			return fmt.Errorf("ordered tool version schemes must not declare a default version")
		}
	case "opaque":
		if _, err := encodeToolVersionSegmentV1(defaultVersion); err != nil {
			return fmt.Errorf("opaque tool version scheme requires a canonical default version")
		}
	default:
		return fmt.Errorf("tool version scheme is unsupported")
	}
	return nil
}

func validateToolReleaseIndexV1(tool *ToolRecordV1, manifests []*ReleaseManifestV1) error {
	if tool == nil {
		return fmt.Errorf("tool release index requires a tool record")
	}
	if len(manifests) == 0 || len(manifests) > maxDefinitionReferences {
		return fmt.Errorf("tool release index must contain a nonempty bounded manifest list")
	}
	coordinates := make(map[string]string, len(manifests))
	exactVersions := make(map[string]struct{}, len(manifests))
	for index, manifest := range manifests {
		if manifest == nil || manifest.Tool != tool.Name {
			return fmt.Errorf("tool release index manifest %d belongs to a different tool", index)
		}
		if err := validateToolVersionV1(tool.VersionScheme, manifest.Version); err != nil {
			return fmt.Errorf("tool release index manifest %d version: %w", index, err)
		}
		exactVersions[manifest.Version] = struct{}{}
		for aliasIndex, alias := range manifest.Aliases {
			if err := validateToolVersionAliasV1(tool.VersionScheme, alias); err != nil {
				return fmt.Errorf("tool release index manifest %d alias %d: %w", index, aliasIndex, err)
			}
		}
		for _, token := range append([]string{manifest.Version}, manifest.Aliases...) {
			if existing, found := coordinates[token]; found && existing != manifest.Version {
				return fmt.Errorf("tool release index token %q maps to both %q and %q", token, existing, manifest.Version)
			}
			coordinates[token] = manifest.Version
		}
	}
	if tool.VersionScheme == "opaque" {
		if _, found := exactVersions[tool.DefaultVersion]; !found {
			return fmt.Errorf("opaque default version %q is not an advertised exact release", tool.DefaultVersion)
		}
	}
	return nil
}

func validateToolVersionAliasV1(scheme string, value string) error {
	switch scheme {
	case "semver":
		if _, err := semver.Parse(value); err != nil {
			parts := strings.Split(value, ".")
			if len(parts) == 0 || len(parts) > 2 {
				return fmt.Errorf("alias %q is invalid under SemVer", value)
			}
			for _, part := range parts {
				if err := validateCanonicalDecimalV1("SemVer alias component", part, false); err != nil {
					return fmt.Errorf("alias %q is invalid under SemVer", value)
				}
			}
		}
	case "pep440":
		if _, err := pep440.Parse(value); err != nil {
			return fmt.Errorf("alias %q is invalid under PEP 440", value)
		}
	case "integer":
		if err := validateCanonicalDecimalV1("integer tool version alias", value, false); err != nil {
			return err
		}
	case "opaque":
		if _, err := encodeToolVersionSegmentV1(value); err != nil {
			return err
		}
	default:
		return fmt.Errorf("tool version scheme is unsupported")
	}
	return nil
}

func validateToolVersionV1(scheme string, value string) error {
	switch scheme {
	case "semver":
		parsed, err := semver.Parse(value)
		if err != nil || parsed.String() != value {
			return fmt.Errorf("version %q is not canonical SemVer", value)
		}
	case "pep440":
		parsed, err := pep440.Parse(value)
		if err != nil || parsed.String() != value {
			return fmt.Errorf("version %q is not canonical PEP 440", value)
		}
	case "integer":
		if err := validateCanonicalDecimalV1("integer tool version", value, false); err != nil {
			return err
		}
	case "opaque":
		if _, err := encodeToolVersionSegmentV1(value); err != nil {
			return err
		}
	default:
		return fmt.Errorf("tool version scheme is unsupported")
	}
	return nil
}

func validateSupportedReployRequirementV1(requirement string) error {
	if !validRecordTokenV1(requirement) {
		return fmt.Errorf("supported Reploy requirement is invalid")
	}
	if strings.IndexFunc(requirement, unicode.IsSpace) >= 0 {
		return fmt.Errorf("supported Reploy requirement is not canonical SemVer")
	}
	constraints, err := semver.NewConstraints(requirement)
	if err != nil || constraints.String() != requirement {
		return fmt.Errorf("supported Reploy requirement is not canonical SemVer")
	}
	return nil
}

func validatePayloadIDV1(value *PayloadRecordV1) error {
	segments := strings.Split(value.ID, "/")
	if len(segments) < 5 || segments[1] != "releases" || segments[3] != "payloads" {
		return fmt.Errorf("payload ID must use a release payload namespace")
	}
	if _, err := decodeToolVersionSegmentV1(segments[2]); err != nil {
		return fmt.Errorf("payload ID version: %w", err)
	}
	expectedLeaf := value.Name + "-" + strings.ReplaceAll(value.Platform, "/", "-")
	if value.Selection == "" {
		if len(segments) != 5 || segments[4] != expectedLeaf {
			return fmt.Errorf("unconditional payload ID must end with /payloads/%s", expectedLeaf)
		}
		return nil
	}
	if len(segments) != 6 || segments[4] != value.Selection || segments[5] != expectedLeaf {
		return fmt.Errorf("selected payload ID must end with /payloads/%s/%s", value.Selection, expectedLeaf)
	}
	return nil
}

func validBaseImageReferenceV1(value string) bool {
	if !validRecordTokenV1(value) || strings.ToLower(value) != value || strings.ContainsAny(value, "@?#") || strings.Contains(value, "://") {
		return false
	}
	named, err := dockerreference.ParseNormalizedNamed(value)
	if err != nil || named.String() != value {
		return false
	}
	_, tagged := named.(dockerreference.NamedTagged)
	_, digested := named.(dockerreference.Canonical)
	return tagged && !digested
}

func validateProbeV1(probe RecordProbeV1) error {
	if validateAbsoluteRecordPathV1(probe.Path) != nil || probe.Args == nil || len(probe.Args) > maxDefinitionReferences || probe.Network != "none" {
		return fmt.Errorf("probe must use an absolute path, argument array, and network=none")
	}
	for _, argument := range probe.Args {
		if containsControlV1(argument) {
			return fmt.Errorf("probe arguments must not contain control characters")
		}
	}
	return nil
}

func validateProbeListV1(field string, probes []RecordProbeV1, allowEmpty bool) error {
	if probes == nil || len(probes) > maxDefinitionReferences || !allowEmpty && len(probes) == 0 {
		if allowEmpty {
			return fmt.Errorf("%s must use a bounded array", field)
		}
		return fmt.Errorf("%s must use a nonempty bounded array", field)
	}
	var previous []byte
	for index, probe := range probes {
		if err := validateProbeV1(probe); err != nil {
			return fmt.Errorf("%s[%d]: %w", field, index, err)
		}
		key, err := canonical.Marshal(probe)
		if err != nil {
			return fmt.Errorf("%s[%d] canonical form: %w", field, index, err)
		}
		if index > 0 && bytes.Compare(previous, key) >= 0 {
			return fmt.Errorf("%s must be unique and sorted", field)
		}
		previous = key
	}
	return nil
}

func validateExportsV1(field string, exports []ToolExportV1) error {
	if exports == nil || len(exports) > maxDefinitionReferences {
		return fmt.Errorf("%s must use a bounded array", field)
	}
	for index, exported := range exports {
		if !validRecordIdentifierV1(exported.Name) || validateAbsoluteRecordPathV1(exported.Path) != nil || index > 0 && exports[index-1].Name >= exported.Name {
			return fmt.Errorf("%s must be unique, sorted, and absolute", field)
		}
	}
	return nil
}

func validateRuntimeV1(contexts []string, runtime *RecordRuntimeV1) error {
	hasRuntime := containsRecordValueV1(contexts, "runtime")
	if runtime == nil {
		if hasRuntime {
			return fmt.Errorf("runtime context requires a runtime contract")
		}
		return nil
	}
	if !hasRuntime || validateAbsoluteRecordPathV1(runtime.InstallRoot) != nil {
		return fmt.Errorf("runtime contract is inconsistent with contexts")
	}
	if runtime.Environment == nil || len(runtime.Environment) > maxDefinitionReferences {
		return fmt.Errorf("runtime environment must use a bounded array")
	}
	for index, variable := range runtime.Environment {
		if !validEnvironmentNameV1(variable.Name) || containsControlV1(variable.Value) || index > 0 && runtime.Environment[index-1].Name >= variable.Name {
			return fmt.Errorf("runtime environment variables must be unique and sorted")
		}
	}
	return nil
}

func validateBindingRequestV1(binding BindingRequestV1) error {
	if err := validateSortedUniqueStringsV1("binding options", binding.Options, false); err != nil {
		return err
	}
	if binding.Required && len(binding.Options) == 0 {
		return fmt.Errorf("required binding must declare at least one option")
	}
	for _, option := range binding.Options {
		if !validRecordIdentifierV1(option) {
			return fmt.Errorf("binding options must be canonical identifiers")
		}
	}
	if binding.Default != "" && !containsRecordValueV1(binding.Options, binding.Default) {
		return fmt.Errorf("default binding must be one of the declared options")
	}
	return nil
}

func validateSelectionRequestV1(selections SelectionRequestV1) error {
	if err := validateSortedUniqueStringsV1("selection options", selections.Options, false); err != nil {
		return err
	}
	if err := validateCanonicalDecimalV1("minimum selections", selections.Minimum, false); err != nil {
		return err
	}
	if err := validateCanonicalDecimalV1("maximum selections", selections.Maximum, false); err != nil {
		return err
	}
	for _, option := range selections.Options {
		if !validRecordIdentifierV1(option) {
			return fmt.Errorf("selection options must be canonical identifiers")
		}
	}
	minimum, _ := strconv.ParseUint(selections.Minimum, 10, 63)
	maximum, _ := strconv.ParseUint(selections.Maximum, 10, 63)
	if minimum > maximum || maximum > uint64(len(selections.Options)) {
		return fmt.Errorf("selection cardinality is inconsistent with the declared options")
	}
	if err := validateSelectionCompatibilityGroupsV1(selections.Options, selections.CompatibilityGroups); err != nil {
		return err
	}
	if err := validateSortedUniqueStringsV1("default selections", selections.Defaults, false); err != nil {
		return err
	}
	for _, selection := range selections.Defaults {
		if !containsRecordValueV1(selections.Options, selection) {
			return fmt.Errorf("default selection %q is not a declared option", selection)
		}
	}
	if len(selections.Defaults) != 0 && (uint64(len(selections.Defaults)) < minimum || uint64(len(selections.Defaults)) > maximum || !selectionSetCompatibleV1(selections.Defaults, selections.CompatibilityGroups)) {
		return fmt.Errorf("default selections do not satisfy the selection contract")
	}
	return nil
}

func validateSelectionCompatibilityGroupsV1(options []string, groups [][]string) error {
	if groups == nil || len(groups) > maxDefinitionReferences {
		return fmt.Errorf("selection compatibility groups must use a bounded array")
	}
	covered := make(map[string]bool, len(options))
	totalOptions := 0
	for index, group := range groups {
		if len(group) == 0 || len(group) > maxDefinitionReferences {
			return fmt.Errorf("selection compatibility groups must be nonempty and bounded")
		}
		totalOptions += len(group)
		if totalOptions > maxDefinitionReferences {
			return fmt.Errorf("selection compatibility groups exceed the total option limit")
		}
		if err := validateSortedUniqueStringsV1("selection compatibility group", group, false); err != nil {
			return err
		}
		for _, option := range group {
			if !containsRecordValueV1(options, option) {
				return fmt.Errorf("selection compatibility group contains undeclared option %q", option)
			}
			covered[option] = true
		}
		if index > 0 && compareRecordStringSlicesV1(groups[index-1], group) >= 0 {
			return fmt.Errorf("selection compatibility groups must be unique and sorted")
		}
	}
	for _, option := range options {
		if !covered[option] {
			return fmt.Errorf("selection compatibility groups do not cover option %q", option)
		}
	}
	for left := range groups {
		for right := range groups {
			if left != right && recordStringSliceSubsetV1(groups[left], groups[right]) {
				return fmt.Errorf("selection compatibility groups must be maximal")
			}
		}
	}
	return nil
}

func validateParameterSchemasV1(parameters []ParameterSchemaV1) error {
	if parameters == nil || len(parameters) > maxDefinitionReferences {
		return fmt.Errorf("contract parameters must use a bounded array")
	}
	for index, parameter := range parameters {
		if !validRecordIdentifierV1(parameter.Name) || index > 0 && parameters[index-1].Name >= parameter.Name {
			return fmt.Errorf("contract parameters must have unique sorted canonical names")
		}
		if parameter.Values == nil {
			return fmt.Errorf("parameter %q values must use an array", parameter.Name)
		}
		switch parameter.Type {
		case "boolean":
			if len(parameter.Values) != 0 || parameter.Minimum != "" || parameter.Maximum != "" {
				return fmt.Errorf("boolean parameter %q must not declare enum or range constraints", parameter.Name)
			}
		case "enum":
			if parameter.Minimum != "" || parameter.Maximum != "" {
				return fmt.Errorf("enum parameter %q must not declare range constraints", parameter.Name)
			}
			if err := requireNonemptySortedStringsV1("enum parameter values", parameter.Values); err != nil {
				return err
			}
		case "integer":
			if len(parameter.Values) != 0 {
				return fmt.Errorf("integer parameter %q must not declare enum values", parameter.Name)
			}
			minimum, err := parseCanonicalIntegerV1("integer parameter minimum", parameter.Minimum)
			if err != nil {
				return err
			}
			maximum, err := parseCanonicalIntegerV1("integer parameter maximum", parameter.Maximum)
			if err != nil {
				return err
			}
			if minimum > maximum {
				return fmt.Errorf("integer parameter %q range is inverted", parameter.Name)
			}
			if !boundedParameterRangeV1(minimum, maximum) {
				return fmt.Errorf("integer parameter %q range exceeds the enumerable domain limit", parameter.Name)
			}
		default:
			return fmt.Errorf("parameter %q type is unsupported", parameter.Name)
		}
		if parameter.Default != nil && !parameterValueInSchemaV1(*parameter.Default, parameter) {
			return fmt.Errorf("parameter %q default is outside its declared domain", parameter.Name)
		}
	}
	return nil
}

func parameterValueInSchemaV1(value string, parameter ParameterSchemaV1) bool {
	switch parameter.Type {
	case "boolean":
		return value == "false" || value == "true"
	case "enum":
		return containsRecordValueV1(parameter.Values, value)
	case "integer":
		parsed, err := parseCanonicalIntegerV1("parameter value", value)
		minimum, minimumErr := parseCanonicalIntegerV1("parameter minimum", parameter.Minimum)
		maximum, maximumErr := parseCanonicalIntegerV1("parameter maximum", parameter.Maximum)
		return err == nil && minimumErr == nil && maximumErr == nil && parsed >= minimum && parsed <= maximum
	default:
		return false
	}
}

func validateTargetParameterConstraintsV1(parameters []TargetParameterConstraintV1) error {
	if parameters == nil || len(parameters) > maxDefinitionReferences {
		return fmt.Errorf("target parameter constraints must use a bounded array")
	}
	for index, parameter := range parameters {
		if !validRecordIdentifierV1(parameter.Name) || index > 0 && parameters[index-1].Name >= parameter.Name {
			return fmt.Errorf("target parameter constraints must have unique sorted canonical names")
		}
		if parameter.Values == nil {
			return fmt.Errorf("target parameter constraint %q values must use an array", parameter.Name)
		}
		if len(parameter.Values) != 0 {
			if parameter.Minimum != "" || parameter.Maximum != "" {
				return fmt.Errorf("target parameter constraint %q cannot mix values and a range", parameter.Name)
			}
			if err := requireNonemptySortedStringsV1("target parameter constraint values", parameter.Values); err != nil {
				return err
			}
			continue
		}
		minimum, err := parseCanonicalIntegerV1("target parameter minimum", parameter.Minimum)
		if err != nil {
			return err
		}
		maximum, err := parseCanonicalIntegerV1("target parameter maximum", parameter.Maximum)
		if err != nil {
			return err
		}
		if minimum > maximum {
			return fmt.Errorf("target parameter constraint %q range is inverted", parameter.Name)
		}
		if !boundedParameterRangeV1(minimum, maximum) {
			return fmt.Errorf("target parameter constraint %q range exceeds the enumerable domain limit", parameter.Name)
		}
	}
	return nil
}

// validateTargetAgainstContractV1 verifies the relationships that cannot be
// established while validating either immutable record in isolation.
func validateTargetAgainstContractV1(contract *ReleaseContractV1, target *TargetRecordV1) error {
	if len(target.Bindings) != len(contract.Binding.Options) {
		return fmt.Errorf("target must provide exactly one contribution mapping for every contract binding option")
	}
	for index, option := range contract.Binding.Options {
		if target.Bindings[index].Name != option {
			return fmt.Errorf("target binding mappings must exactly match contract binding options")
		}
	}
	if len(target.Selections) != len(contract.Selections.Options) {
		return fmt.Errorf("target must provide exactly one contribution mapping for every contract selection option")
	}
	for index, option := range contract.Selections.Options {
		if target.Selections[index].Name != option {
			return fmt.Errorf("target selection mappings must exactly match contract selection options")
		}
	}

	contractParameters := make(map[string]ParameterSchemaV1, len(contract.Parameters))
	for _, parameter := range contract.Parameters {
		contractParameters[parameter.Name] = parameter
	}
	for _, constraint := range target.Parameters {
		parameter, exists := contractParameters[constraint.Name]
		if !exists {
			return fmt.Errorf("target parameter constraint %q is not declared by the release contract", constraint.Name)
		}
		if len(constraint.Values) != 0 {
			for _, value := range constraint.Values {
				if !parameterValueInSchemaV1(value, parameter) {
					return fmt.Errorf("target parameter constraint %q value %q is outside the contract domain", constraint.Name, value)
				}
			}
			if parameter.Default != nil && !containsRecordValueV1(constraint.Values, *parameter.Default) {
				return fmt.Errorf("target parameter constraint %q excludes the contract default", constraint.Name)
			}
			continue
		}
		if parameter.Type != "integer" {
			return fmt.Errorf("target parameter constraint %q range is incompatible with contract type %q", constraint.Name, parameter.Type)
		}
		minimum, _ := parseCanonicalIntegerV1("target parameter minimum", constraint.Minimum)
		maximum, _ := parseCanonicalIntegerV1("target parameter maximum", constraint.Maximum)
		contractMinimum, _ := parseCanonicalIntegerV1("contract parameter minimum", parameter.Minimum)
		contractMaximum, _ := parseCanonicalIntegerV1("contract parameter maximum", parameter.Maximum)
		if minimum < contractMinimum || maximum > contractMaximum {
			return fmt.Errorf("target parameter constraint %q range widens the contract domain", constraint.Name)
		}
		if parameter.Default != nil {
			defaultValue, _ := parseCanonicalIntegerV1("contract parameter default", *parameter.Default)
			if defaultValue < minimum || defaultValue > maximum {
				return fmt.Errorf("target parameter constraint %q excludes the contract default", constraint.Name)
			}
		}
	}
	return nil
}

func validateParameterValuesV1(field string, values []ParameterValueV1) error {
	if values == nil || len(values) > maxDefinitionReferences {
		return fmt.Errorf("%s must use a bounded array", field)
	}
	for index, value := range values {
		if !validRecordIdentifierV1(value.Name) || !validRecordTokenV1(value.Value) || index > 0 && values[index-1].Name >= value.Name {
			return fmt.Errorf("%s must have unique sorted names and canonical values", field)
		}
	}
	return nil
}

func parseCanonicalIntegerV1(field string, value string) (int64, error) {
	digits := value
	if strings.HasPrefix(digits, "-") {
		digits = strings.TrimPrefix(digits, "-")
		if digits == "0" {
			return 0, fmt.Errorf("%s must be a canonical bounded integer string", field)
		}
	}
	if !canonicalDecimalPattern.MatchString(digits) {
		return 0, fmt.Errorf("%s must be a canonical bounded integer string", field)
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a canonical bounded integer string", field)
	}
	return parsed, nil
}

func boundedParameterRangeV1(minimum int64, maximum int64) bool {
	if minimum > maximum {
		return false
	}
	const maximumValues = int64(maxDefinitionReferences)
	if minimum > int64(^uint64(0)>>1)-(maximumValues-1) {
		return true
	}
	return maximum <= minimum+maximumValues-1
}

func selectionSetCompatibleV1(selections []string, groups [][]string) bool {
	for _, group := range groups {
		if recordStringSliceSubsetV1(selections, group) {
			return true
		}
	}
	return len(selections) == 0 && len(groups) == 0
}

func recordStringSliceSubsetV1(subset []string, superset []string) bool {
	left, right := 0, 0
	for left < len(subset) && right < len(superset) {
		switch {
		case subset[left] == superset[right]:
			left++
			right++
		case subset[left] > superset[right]:
			right++
		default:
			return false
		}
	}
	return left == len(subset)
}

func compareRecordStringSlicesV1(left []string, right []string) int {
	for index := 0; index < len(left) && index < len(right); index++ {
		switch {
		case left[index] < right[index]:
			return -1
		case left[index] > right[index]:
			return 1
		}
	}
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	default:
		return 0
	}
}

func containsRecordValueV1(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func validateTargetIdentityV1(target TargetIdentityV1) error {
	if !validPlatformV1(target.Platform) || !validRecordIdentifierV1(target.OSReleaseID) || !validRecordSegmentV1(target.VersionID) || !supportedArchitectureV1(target.OCIArchitecture) || !supportedArchitectureV1(target.NativeArchitecture) || target.PackageManager != "apt" {
		return fmt.Errorf("target identity is incomplete")
	}
	if target.Platform != "linux/"+target.OCIArchitecture || target.NativeArchitecture != target.OCIArchitecture {
		return fmt.Errorf("target platform and OCI architecture are inconsistent")
	}
	return nil
}

func validateTargetRecordIDV1(id string, target TargetIdentityV1) error {
	segments := strings.Split(id, "/")
	if len(segments) != 7 || segments[1] != "releases" || segments[3] != "targets" || segments[4] != target.OSReleaseID || segments[5] != target.VersionID || segments[6] != target.OCIArchitecture {
		return fmt.Errorf("target record ID must use the complete tool release target namespace")
	}
	if _, err := decodeToolVersionSegmentV1(segments[2]); err != nil {
		return fmt.Errorf("target record ID version: %w", err)
	}
	return nil
}

func validateValidationEvidenceV1(evidence ValidationEvidenceV1) error {
	if evidence.Schema != ValidationEvidenceSchemaV1 || !validRecordIdentifierV1(evidence.Tool) {
		return fmt.Errorf("validation evidence identity is incomplete")
	}
	if _, err := encodeToolVersionSegmentV1(evidence.Version); err != nil {
		return fmt.Errorf("validation evidence version: %w", err)
	}
	if err := validateCanonicalDecimalV1("validation evidence revision", evidence.Revision, true); err != nil {
		return err
	}
	if err := evidence.ManifestDigest.Validate(); err != nil {
		return fmt.Errorf("validation evidence manifest digest: %w", err)
	}
	if err := evidence.SelectedClosureDigest.Validate(); err != nil {
		return fmt.Errorf("validation evidence selected closure digest: %w", err)
	}
	if err := evidence.BaseImageDigest.Validate(); err != nil {
		return fmt.Errorf("validation evidence base image digest: %w", err)
	}
	if err := validateTargetIdentityV1(evidence.Target); err != nil {
		return fmt.Errorf("validation evidence target: %w", err)
	}
	if evidence.Binding != "" && !validRecordIdentifierV1(evidence.Binding) {
		return fmt.Errorf("validation evidence binding is invalid")
	}
	if err := validateSortedUniqueStringsV1("validation evidence selections", evidence.Selections, false); err != nil {
		return err
	}
	if err := validateParameterValuesV1("validation evidence parameters", evidence.Parameters); err != nil {
		return err
	}
	if err := validateEvidenceFixtureIDV1(evidence); err != nil {
		return err
	}
	if !validRecordTokenV1(evidence.ValidatorVersion) || evidence.Result != "pass" && evidence.Result != "fail" {
		return fmt.Errorf("validation evidence validator or result is invalid")
	}
	if len(evidence.ProbeDigests) == 0 || len(evidence.ProbeDigests) > maxDefinitionReferences {
		return fmt.Errorf("validation evidence probe digests must use a nonempty bounded array")
	}
	for index, digest := range evidence.ProbeDigests {
		if err := digest.Validate(); err != nil {
			return fmt.Errorf("validation evidence probe digest %d: %w", index, err)
		}
		if index > 0 && evidence.ProbeDigests[index-1] >= digest {
			return fmt.Errorf("validation evidence probe digests must be unique and sorted")
		}
	}
	return nil
}

func requireNonemptySortedStringsV1(field string, values []string) error {
	if err := validateSortedUniqueStringsV1(field, values, false); err != nil {
		return err
	}
	if len(values) == 0 {
		return fmt.Errorf("%s must not be empty", field)
	}
	return nil
}

func validRecordTokenV1(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && !containsControlV1(value)
}

func containsControlV1(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func validRecordSegmentV1(value string) bool {
	if !validRecordTokenV1(value) || value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			continue
		}
		switch character {
		case '.', '+', '-':
		default:
			return false
		}
	}
	return true
}

func encodeToolVersionSegmentV1(value string) (string, error) {
	if !validRecordTokenV1(value) || !utf8.ValidString(value) {
		return "", fmt.Errorf("tool version must be canonical UTF-8 text")
	}
	encodeDots := value == "." || value == ".."
	const hex = "0123456789ABCDEF"
	var encoded strings.Builder
	encoded.Grow(len(value))
	for _, character := range []byte(value) {
		literal := character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune(".+-_", rune(character))
		if literal && !(encodeDots && character == '.') {
			encoded.WriteByte(character)
			continue
		}
		encoded.WriteByte('%')
		encoded.WriteByte(hex[character>>4])
		encoded.WriteByte(hex[character&0x0f])
	}
	return encoded.String(), nil
}

func decodeToolVersionSegmentV1(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("encoded tool version must not be empty")
	}
	decoded := make([]byte, 0, len(value))
	for index := 0; index < len(value); index++ {
		if value[index] != '%' {
			decoded = append(decoded, value[index])
			continue
		}
		if index+2 >= len(value) {
			return "", fmt.Errorf("encoded tool version contains an incomplete escape")
		}
		high, highOK := uppercaseHexValueV1(value[index+1])
		low, lowOK := uppercaseHexValueV1(value[index+2])
		if !highOK || !lowOK {
			return "", fmt.Errorf("encoded tool version escapes must use uppercase hexadecimal")
		}
		decoded = append(decoded, high<<4|low)
		index += 2
	}
	version := string(decoded)
	canonical, err := encodeToolVersionSegmentV1(version)
	if err != nil || canonical != value {
		return "", fmt.Errorf("encoded tool version is not canonical")
	}
	return version, nil
}

func uppercaseHexValueV1(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}

func validateReleaseContractIDV1(id string) error {
	segments := strings.Split(id, "/")
	if len(segments) != 4 || segments[1] != "releases" || segments[3] != "contract" {
		return fmt.Errorf("release contract ID must use tool:<name>/releases/<encoded-version>/contract")
	}
	if _, err := decodeToolVersionSegmentV1(segments[2]); err != nil {
		return fmt.Errorf("release contract ID version: %w", err)
	}
	return nil
}

func validateBindingContractIDV1(id string, binding string) error {
	segments := strings.Split(id, "/")
	if len(segments) != 6 || segments[1] != "releases" || segments[3] != "bindings" || segments[4] != binding || segments[5] != "contract" {
		return fmt.Errorf("binding contract ID must use tool:<name>/releases/<encoded-version>/bindings/%s/contract", binding)
	}
	if _, err := decodeToolVersionSegmentV1(segments[2]); err != nil {
		return fmt.Errorf("binding contract ID version: %w", err)
	}
	return nil
}

func validateBindingArtifactIDV1(id string, binding string, platform string) error {
	segments := strings.Split(id, "/")
	expectedPlatform := strings.ReplaceAll(platform, "/", "-")
	if len(segments) != 7 || segments[1] != "releases" || segments[3] != "bindings" || segments[4] != binding || segments[5] != "artifacts" || segments[6] != expectedPlatform {
		return fmt.Errorf("binding artifact ID must match binding %q and platform %q in a release namespace", binding, platform)
	}
	if _, err := decodeToolVersionSegmentV1(segments[2]); err != nil {
		return fmt.Errorf("binding artifact ID version: %w", err)
	}
	return nil
}

func validateBindingArtifactCompatibilityV1(value *BindingArtifactRecordV1) error {
	if err := requireNonemptySortedStringsV1("binding artifact tags", value.Tags); err != nil {
		return err
	}
	filenameTags, err := wheelFilenameTagsV1(value.Filename)
	if err != nil {
		return fmt.Errorf("binding artifact filename: %w", err)
	}
	if compareRecordStringSlicesV1(filenameTags, value.Tags) != 0 {
		return fmt.Errorf("binding artifact tags must exactly match the expanded wheel filename tags")
	}
	for _, tag := range value.Tags {
		segments := strings.Split(tag, "-")
		if len(segments) != 3 || !validWheelTagGroupV1(segments[0]) || !validWheelTagGroupV1(segments[1]) || !validWheelTagGroupV1(segments[2]) {
			return fmt.Errorf("binding artifact wheel tag %q is invalid", tag)
		}
		if !wheelPlatformTagCompatibleV1(segments[2], value.Platform) {
			return fmt.Errorf("binding artifact wheel tag %q is incompatible with platform %q", tag, value.Platform)
		}
	}
	specifiers, err := pep440.NewSpecifiers(value.RequiresPython)
	if err != nil || specifiers.String() != value.RequiresPython {
		return fmt.Errorf("binding artifact requires_python must be a canonical PEP 440 specifier set")
	}
	return nil
}

func wheelFilenameTagsV1(filename string) ([]string, error) {
	if !strings.HasSuffix(filename, ".whl") {
		return nil, fmt.Errorf("wheel filename must end in .whl")
	}
	parts := strings.Split(strings.TrimSuffix(filename, ".whl"), "-")
	if len(parts) != 5 && len(parts) != 6 {
		return nil, fmt.Errorf("wheel filename must contain distribution, version, Python, ABI, and platform tags")
	}
	if !validWheelDistributionV1(parts[0]) {
		return nil, fmt.Errorf("wheel filename contains an invalid distribution or version")
	}
	version, err := pep440.Parse(parts[1])
	if err != nil || version.String() != parts[1] {
		return nil, fmt.Errorf("wheel filename contains an invalid distribution or version")
	}
	if len(parts) == 6 && !validWheelBuildTagV1(parts[2]) {
		return nil, fmt.Errorf("wheel filename contains an invalid build tag")
	}
	pythonTags := strings.Split(parts[len(parts)-3], ".")
	abiTags := strings.Split(parts[len(parts)-2], ".")
	platformTags := strings.Split(parts[len(parts)-1], ".")
	expandedTagCount := 1
	for _, group := range [][]string{pythonTags, abiTags, platformTags} {
		if len(group) > maxDefinitionReferences/expandedTagCount {
			return nil, fmt.Errorf("wheel filename expands to more than %d compatibility tags", maxDefinitionReferences)
		}
		expandedTagCount *= len(group)
		for _, component := range group {
			if !validWheelTagComponentV1(component) {
				return nil, fmt.Errorf("wheel filename contains an invalid compatibility tag")
			}
		}
	}
	tags := make([]string, 0, expandedTagCount)
	for _, pythonTag := range pythonTags {
		for _, abiTag := range abiTags {
			for _, platformTag := range platformTags {
				tags = append(tags, pythonTag+"-"+abiTag+"-"+platformTag)
			}
		}
	}
	sort.Strings(tags)
	for index := 1; index < len(tags); index++ {
		if tags[index-1] == tags[index] {
			return nil, fmt.Errorf("wheel filename compatibility tags must be unique")
		}
	}
	return tags, nil
}

func validWheelDistributionV1(component string) bool {
	if component == "" || component[0] == '_' {
		return false
	}
	for _, character := range component {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' {
			continue
		}
		return false
	}
	return strings.ReplaceAll(pythonprovider.NormalizeDistributionName(component), "-", "_") == component
}

func validWheelBuildTagV1(tag string) bool {
	if tag == "" || tag[0] < '0' || tag[0] > '9' {
		return false
	}
	for _, character := range tag[1:] {
		if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}

func validWheelTagGroupV1(group string) bool {
	for _, component := range strings.Split(group, ".") {
		if !validWheelTagComponentV1(component) {
			return false
		}
	}
	return true
}

func validWheelTagComponentV1(component string) bool {
	if component == "" {
		return false
	}
	for _, character := range component {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func wheelPlatformTagCompatibleV1(tag string, platform string) bool {
	if tag == "any" {
		return true
	}
	architecture := ""
	switch platform {
	case "linux/amd64":
		architecture = "x86_64"
	case "linux/arm64":
		architecture = "aarch64"
	default:
		return false
	}
	suffix := "_" + architecture
	if !strings.HasSuffix(tag, suffix) {
		return false
	}
	policy := strings.TrimSuffix(tag, suffix)
	if policy == "linux" || policy == "manylinux1" || policy == "manylinux2010" || policy == "manylinux2014" {
		return true
	}
	if components, found := strings.CutPrefix(policy, "manylinux_"); found {
		parts := strings.Split(components, "_")
		return len(parts) == 2 && canonicalDecimalPattern.MatchString(parts[0]) && canonicalDecimalPattern.MatchString(parts[1])
	}
	return false
}

func validateArtifactSourceIDV1(id string) error {
	segments := strings.Split(id, "/")
	if len(segments) != 7 || segments[1] != "releases" || segments[3] != "revisions" || segments[5] != "sources" || !validRecordIdentifierV1(segments[6]) {
		return fmt.Errorf("artifact source ID must use a release revision source namespace")
	}
	if _, err := decodeToolVersionSegmentV1(segments[2]); err != nil {
		return fmt.Errorf("artifact source ID version: %w", err)
	}
	if err := validateCanonicalDecimalV1("artifact source ID revision", segments[4], true); err != nil {
		return err
	}
	return nil
}

func validateNativePackageSetIDV1(id string) error {
	segments := strings.Split(id, "/")
	if len(segments) != 5 || segments[1] != "releases" || segments[3] != "package-sets" || !validRecordIdentifierV1(segments[4]) {
		return fmt.Errorf("native package-set ID must use a release package-set namespace")
	}
	if _, err := decodeToolVersionSegmentV1(segments[2]); err != nil {
		return fmt.Errorf("native package-set ID version: %w", err)
	}
	return nil
}

func validateEvidenceFixtureIDV1(evidence ValidationEvidenceV1) error {
	versionSegment, err := encodeToolVersionSegmentV1(evidence.Version)
	if err != nil {
		return fmt.Errorf("validation evidence fixture version: %w", err)
	}
	segments := strings.Split(evidence.Fixture, "/")
	if len(segments) != 6 || segments[0] != "tool:"+evidence.Tool || segments[1] != "releases" || segments[2] != versionSegment || segments[3] != "validation" || segments[4] != "fixtures" || !validRecordIdentifierV1(segments[5]) {
		return fmt.Errorf("validation evidence fixture must be a canonical ID in the evidence tool and version namespace")
	}
	return validateRecordIDV1(evidence.Fixture)
}

func validPackageNameV1(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value[1:] {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			continue
		}
		switch character {
		case '.', '-', '_':
		default:
			return false
		}
	}
	return true
}

func supportedArchitectureV1(value string) bool {
	return value == "amd64" || value == "arm64"
}

func validPlatformV1(value string) bool {
	return value == "linux/amd64" || value == "linux/arm64"
}

func supportedPayloadKindV1(value string) bool {
	return value == "jdk-archive" || value == "playwright-browser-archive" || value == "raw-executable"
}

func validEnvironmentNameV1(value string) bool {
	if value == "" || value[0] < 'A' || value[0] > 'Z' {
		return false
	}
	for _, character := range value[1:] {
		if character < 'A' || character > 'Z' {
			if character < '0' || character > '9' {
				if character != '_' {
					return false
				}
			}
		}
	}
	return true
}
