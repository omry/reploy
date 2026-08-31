package toolcatalog

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"unicode"

	pep440 "github.com/aquasecurity/go-pep440-version"
	"github.com/aquasecurity/go-version/pkg/semver"
	dockerreference "github.com/distribution/reference"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

const (
	maxDefinitionValidationCases = 1024
	maxDefinitionArtifactMirrors = providerstore.CoreMaxArtifactMirrors
)

func validateLoadedRecordV1(record loadedRecordV1) error {
	if err := validateRecordIDV1(record.ID); err != nil {
		return err
	}
	switch record.Schema {
	case BindingArtifactSchemaV1, PayloadRecordSchemaV1, ArtifactSourceRecordSchemaV1, NativePackageSetSchemaV1, ValidationProfileSchemaV1:
		envelope, err := portableToolRecordEnvelopeV1(record.Value)
		if err != nil {
			return err
		}
		if valueID, _ := envelope.Value["id"].(string); valueID != record.ID {
			return fmt.Errorf("catalog record value ID must match loaded record ID %q", record.ID)
		}
		return providers.ValidatePortableToolCatalogRecordV1(envelope)
	}
	switch value := record.Value.(type) {
	case *ToolRecordV1:
		if record.Schema != ToolRecordSchemaV1 || value.Schema != ToolRecordSchemaV1 || value.ID != record.ID || !validRecordIdentifierV1(value.Name) || value.ID != "tool:"+value.Name {
			return fmt.Errorf("tool record identity is inconsistent")
		}
		if err := validateToolVersionPolicyV1(value.VersionScheme, value.DefaultVersion); err != nil {
			return err
		}
		if !validRecordTokenV1(value.Summary) || value.Upstream == "" || value.Source == "" || value.License == "" || value.Documentation == "" || len(value.Releases) == 0 {
			return fmt.Errorf("tool metadata and releases must not be empty")
		}
		for _, raw := range []string{value.Upstream, value.Source, value.Documentation} {
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
		defaultAdvertised := false
		for index, reference := range value.Releases {
			segments := strings.Split(reference.ID, "/")
			if !strings.HasPrefix(reference.ID, prefix) || len(segments) != 6 || segments[3] != "revisions" || segments[5] != "manifest" {
				return fmt.Errorf("tool release reference %d must identify a manifest beneath %q", index, prefix)
			}
			// The tool record is the only record that knows the version scheme,
			// so it is the only one that can reject a release coordinate the
			// scheme forbids. The revision rule is the manifest's own.
			version, err := decodeToolVersionSegmentV1(segments[2])
			if err != nil {
				return fmt.Errorf("tool release reference %d version: %w", index, err)
			}
			if err := validateToolVersionV1(value.VersionScheme, version); err != nil {
				return fmt.Errorf("tool release reference %d: %w", index, err)
			}
			if err := validateCanonicalDecimalV1(fmt.Sprintf("tool release reference %d revision", index), segments[4], true); err != nil {
				return err
			}
			if version == value.DefaultVersion {
				defaultAdvertised = true
			}
		}
		// A versionless opaque request normalizes to equality with the default,
		// so a default naming no advertised release makes the tool record
		// unsatisfiable. Eligibility beyond advertisement is a graph concern.
		if value.VersionScheme == "opaque" && !defaultAdvertised {
			return fmt.Errorf("opaque default version %q must name an advertised release", value.DefaultVersion)
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
		if err := validateProfileReferenceListV1("release validation profiles", value.ValidationProfiles, releasePrefix, false); err != nil {
			return err
		}
		if len(value.Targets) == 0 {
			return fmt.Errorf("release manifest targets must not be empty")
		}
		if err := validateReferenceListV1("release targets", value.Targets); err != nil {
			return err
		}
		for _, reference := range value.Targets {
			if err := validateTargetReferenceV1(reference, releasePrefix); err != nil {
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
			if err := validateArtifactSourceTargetV1(mapping.Artifact, releasePrefix); err != nil {
				return err
			}
			if err := validateRecordReferenceV1(mapping.Source); err != nil {
				return fmt.Errorf("artifact source mapping %d source: %w", index, err)
			}
			if err := validateArtifactSourceReferenceV1(mapping.Source, releasePrefix, value.Revision); err != nil {
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
		if err := validateBindingSetSchemaV1(value.Binding); err != nil {
			return err
		}
		if err := validateSelectionSchemaV1(value.Selections); err != nil {
			return err
		}
		if err := validateSortedUniqueStringsV1("compatibility constraints", value.CompatibilityConstraints, false); err != nil {
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
		if value.SupportCases == nil || len(value.SupportCases) == 0 || len(value.SupportCases) > maxDefinitionValidationCases {
			return fmt.Errorf("target support cases must use between 1 and %d entries", maxDefinitionValidationCases)
		}
		var previousSupportCase []byte
		for index, supportCase := range value.SupportCases {
			if supportCase.Context != "build" && supportCase.Context != "runtime" {
				return fmt.Errorf("target support case %d has invalid context %q", index, supportCase.Context)
			}
			if err := validateSortedUniqueStringsV1("target support case bindings", supportCase.Bindings, false); err != nil {
				return err
			}
			for _, binding := range supportCase.Bindings {
				if !validRecordIdentifierV1(binding) {
					return fmt.Errorf("target support case binding %q must be a canonical identifier", binding)
				}
			}
			if supportCase.Selections == nil {
				return fmt.Errorf("target support case %d selections must be a dimension-keyed map", index)
			}
			dimensionNames := make([]string, 0, len(supportCase.Selections))
			for dimension := range supportCase.Selections {
				dimensionNames = append(dimensionNames, dimension)
			}
			sort.Strings(dimensionNames)
			for _, dimension := range dimensionNames {
				selections := supportCase.Selections[dimension]
				if !validRecordIdentifierV1(dimension) {
					return fmt.Errorf("target support case selection dimension %q must be a canonical identifier", dimension)
				}
				if err := requireNonemptySortedStringsV1("target support case selection values", selections); err != nil {
					return err
				}
				for _, selection := range selections {
					if !validRecordIdentifierV1(selection) {
						return fmt.Errorf("target support case selection %q/%q must use canonical identifiers", dimension, selection)
					}
				}
			}
			encoded, err := canonical.Marshal(supportCase)
			if err != nil {
				return fmt.Errorf("encode target support case %d: %w", index, err)
			}
			if index > 0 && bytes.Compare(previousSupportCase, encoded) >= 0 {
				return fmt.Errorf("target support cases must be unique and sorted")
			}
			previousSupportCase = encoded
		}
		releasePrefix := strings.Join(strings.Split(value.ID, "/")[:3], "/")
		if len(value.IntegrationFixtures) == 0 {
			return fmt.Errorf("target integration fixtures must not be empty")
		}
		if err := validateReferenceListV1("target integration fixtures", value.IntegrationFixtures); err != nil {
			return err
		}
		for _, reference := range value.IntegrationFixtures {
			if err := validateFixtureReferenceV1(reference, releasePrefix); err != nil {
				return err
			}
		}
		if err := validateProfileReferenceListV1("target validation profiles", value.ValidationProfiles, releasePrefix, false); err != nil {
			return err
		}
		if err := validateReferenceListV1("target package sets", value.PackageSets); err != nil {
			return err
		}
		for _, reference := range value.PackageSets {
			if err := validatePackageSetReferenceV1("target package set", reference, releasePrefix); err != nil {
				return err
			}
		}
		if err := validateReferenceListV1("target payloads", value.Payloads); err != nil {
			return err
		}
		for _, reference := range value.Payloads {
			if err := validatePayloadReferenceV1("target payload", reference, releasePrefix); err != nil {
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
				if err := validateBindingArtifactReferenceV1(reference, releasePrefix, binding.Name); err != nil {
					return err
				}
			}
			if err := validateReferenceListV1("target binding payloads", binding.Payloads); err != nil {
				return err
			}
			for _, reference := range binding.Payloads {
				if err := validatePayloadReferenceV1("target binding payload", reference, releasePrefix); err != nil {
					return err
				}
			}
			if err := validateReferenceListV1("target binding package sets", binding.PackageSets); err != nil {
				return err
			}
			for _, reference := range binding.PackageSets {
				if err := validatePackageSetReferenceV1("target binding package set", reference, releasePrefix); err != nil {
					return err
				}
			}
			if err := validateExportsV1("target binding exports", binding.Exports); err != nil {
				return err
			}
			if err := validateProfileReferenceListV1("target binding validation profiles", binding.ValidationProfiles, releasePrefix, true); err != nil {
				return err
			}
		}
		if value.Selections == nil || len(value.Selections) > maxDefinitionReferences {
			return fmt.Errorf("target selections must use a bounded array")
		}
		for index, selection := range value.Selections {
			if !validRecordIdentifierV1(selection.Dimension) || !validRecordIdentifierV1(selection.Value) || index > 0 && compareTargetSelectionV1(value.Selections[index-1], selection) >= 0 {
				return fmt.Errorf("target selections must be unique, sorted, and nonempty")
			}
			if err := validateReferenceListV1("target selection payloads", selection.Payloads); err != nil {
				return err
			}
			for _, reference := range selection.Payloads {
				if err := validatePayloadReferenceV1("target selection payload", reference, releasePrefix); err != nil {
					return err
				}
			}
			if err := validateReferenceListV1("target selection package sets", selection.PackageSets); err != nil {
				return err
			}
			for _, reference := range selection.PackageSets {
				if err := validatePackageSetReferenceV1("target selection package set", reference, releasePrefix); err != nil {
					return err
				}
			}
			if err := validateExportsV1("target selection exports", selection.Exports); err != nil {
				return err
			}
			if err := validateProfileReferenceListV1("target selection validation profiles", selection.ValidationProfiles, releasePrefix, true); err != nil {
				return err
			}
			if len(selection.Payloads)+len(selection.PackageSets)+len(selection.Exports)+len(selection.ValidationProfiles) == 0 {
				return fmt.Errorf("target selection %q/%q must contribute at least one record, export, or validation profile", selection.Dimension, selection.Value)
			}
		}
		if err := validateExportsV1("target exports", value.Exports); err != nil {
			return err
		}
		return nil
	case *BindingContractV1:
		if record.Schema != BindingContractSchemaV1 || value.Schema != BindingContractSchemaV1 || value.ID != record.ID || !validRecordIdentifierV1(value.Name) || !validPackageNameV1(value.Package) {
			return fmt.Errorf("binding contract is incomplete")
		}
		if err := validateBindingContractIDV1(value.ID, value.Name); err != nil {
			return err
		}
		if !validRecordIdentifierV1(value.CLI.Name) || validateAbsoluteRecordPathV1(value.CLI.Path) != nil {
			return fmt.Errorf("binding CLI must use a canonical name and absolute path")
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
		if value.BundledComponents == nil || len(value.BundledComponents) > maxDefinitionReferences {
			return fmt.Errorf("binding contract bundled components must use a bounded array")
		}
		for index, component := range value.BundledComponents {
			if !validRecordIdentifierV1(component.Name) || !validRecordSegmentV1(component.Version) || validateRecordPathV1(component.Path, false) != nil {
				return fmt.Errorf("binding contract bundled component %d is not canonical", index)
			}
			if index > 0 && value.BundledComponents[index-1].Name >= component.Name {
				return fmt.Errorf("binding contract bundled components must be unique and sorted by name")
			}
		}
		for _, version := range value.SupportedPython {
			if err := pythonprovider.ValidateInterpreterVersionV1(version); err != nil {
				return fmt.Errorf("supported Python version %q: %w", version, err)
			}
		}
		if err := requireNonemptySortedStringsV1("binding supported tags", value.SupportedTags); err != nil {
			return err
		}
		for _, tag := range value.SupportedTags {
			segments := strings.Split(tag, "-")
			if len(segments) != 3 || !validWheelTagGroupV1(segments[0]) || !validWheelTagGroupV1(segments[1]) || !validWheelTagGroupV1(segments[2]) {
				return fmt.Errorf("binding supported tag %q must be a canonical three-part wheel tag", tag)
			}
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
		if err := validateSortedUniqueStringsV1("integration fixture bindings", value.Bindings, false); err != nil {
			return err
		}
		for _, binding := range value.Bindings {
			if !validRecordIdentifierV1(binding) {
				return fmt.Errorf("integration fixture bindings must be canonical identifiers")
			}
		}
		if value.Selections == nil || len(value.Selections) > maxDefinitionReferences {
			return fmt.Errorf("integration fixture selections must use a bounded map")
		}
		dimensions := make([]string, 0, len(value.Selections))
		for dimension := range value.Selections {
			dimensions = append(dimensions, dimension)
		}
		sort.Strings(dimensions)
		for _, dimension := range dimensions {
			if !validRecordIdentifierV1(dimension) {
				return fmt.Errorf("integration fixture selection dimension %q is invalid", dimension)
			}
			selections := value.Selections[dimension]
			if err := requireNonemptySortedStringsV1("integration fixture selection values", selections); err != nil {
				return err
			}
			for _, selection := range selections {
				if !validRecordIdentifierV1(selection) {
					return fmt.Errorf("integration fixture selections must be canonical identifiers")
				}
			}
		}
		return validateProfileReferenceListV1("integration fixture validation profiles", value.ValidationProfiles, strings.Join(segments[:3], "/"), false)
	default:
		return fmt.Errorf("unsupported record value %T", record.Value)
	}
}

// Artifact source mappings carry the size and digest of a concrete downloadable
// file, so they may only name records that own one: release payloads and
// binding artifacts. Both ID shapes are matched in full against the grammar
// their owning records enforce, because a mapping that names a structurally
// impossible ID can never be satisfied by any record in the release.
func validateArtifactSourceTargetV1(reference RecordReferenceV1, releasePrefix string) error {
	segments := strings.Split(reference.ID, "/")
	if len(segments) >= 5 && strings.Join(segments[:3], "/") == releasePrefix {
		switch {
		case len(segments) == 5 && segments[3] == "payloads" && validPayloadLeafV1(segments[4]):
			return nil
		case len(segments) == 6 && segments[3] == "payloads" &&
			validRecordIdentifierV1(segments[4]) && validPayloadLeafV1(segments[5]):
			return nil
		case len(segments) == 7 && segments[3] == "bindings" && validRecordIdentifierV1(segments[4]) &&
			segments[5] == "artifacts" && validPlatformLeafV1(segments[6]):
			return nil
		}
	}
	return fmt.Errorf("artifact source mapping artifact %q must reference a payload or binding artifact record inside namespace %q", reference.ID, releasePrefix)
}

// Payload IDs end in the leaf validatePayloadIDV1 builds: the payload name
// followed by its platform. The name is not knowable from the manifest, so only
// its shape is checked here.
func validPayloadLeafV1(value string) bool {
	platform := strings.LastIndex(value, "-")
	if platform < 0 {
		return false
	}
	name := strings.LastIndex(value[:platform], "-")
	if name < 0 {
		return false
	}
	return validRecordIdentifierV1(value[:name]) && validPlatformLeafV1(value[name+1:])
}

// Payload and binding artifact IDs spell a platform with a dash where the
// platform value itself uses a slash.
func validPlatformLeafV1(value string) bool {
	return validPlatformV1(strings.ReplaceAll(value, "-", "/"))
}

// A source mapping must name a record an artifact source could own, so the whole
// ID shape is checked here rather than its namespace prefix alone. The shape is
// the one validateArtifactSourceIDV1 enforces on the owning record.
func validateArtifactSourceReferenceV1(reference RecordReferenceV1, releasePrefix string, revision string) error {
	segments := strings.Split(reference.ID, "/")
	if len(segments) != 7 || strings.Join(segments[:3], "/") != releasePrefix || segments[3] != "revisions" ||
		segments[4] != revision || segments[5] != "sources" || !validRecordIdentifierV1(segments[6]) {
		return fmt.Errorf("artifact source mapping source %q must name an artifact source record in revision %q", reference.ID, revision)
	}
	return nil
}

// Cross-record references must name an ID the owning record could actually hold.
// A namespace prefix alone admits IDs no record can own, so each reference below
// is checked against the same shape its owning record's ID validator enforces.

func referenceSegmentsUnderV1(reference RecordReferenceV1, releasePrefix string, count int) ([]string, bool) {
	segments := strings.Split(reference.ID, "/")
	if len(segments) != count || strings.Join(segments[:3], "/") != releasePrefix {
		return nil, false
	}
	return segments, true
}

// Mirrors validateTargetRecordIDV1.
func validateTargetReferenceV1(reference RecordReferenceV1, releasePrefix string) error {
	segments, ok := referenceSegmentsUnderV1(reference, releasePrefix, 7)
	if !ok || segments[3] != "targets" || !validRecordIdentifierV1(segments[4]) ||
		!validRecordSegmentV1(segments[5]) || !supportedArchitectureV1(segments[6]) {
		return fmt.Errorf("release target %q must name a target record under %q", reference.ID, releasePrefix+"/targets")
	}
	return nil
}

// Mirrors validateNativePackageSetIDV1.
func validatePackageSetReferenceV1(field string, reference RecordReferenceV1, releasePrefix string) error {
	segments, ok := referenceSegmentsUnderV1(reference, releasePrefix, 5)
	if !ok || segments[3] != "package-sets" || !validRecordIdentifierV1(segments[4]) {
		return fmt.Errorf("%s %q must name a native package-set record under %q", field, reference.ID, releasePrefix+"/package-sets")
	}
	return nil
}

// Mirrors validatePayloadIDV1, which admits an unconditional and a selected form.
func validatePayloadReferenceV1(field string, reference RecordReferenceV1, releasePrefix string) error {
	segments := strings.Split(reference.ID, "/")
	unconditional := len(segments) == 5 && validPayloadLeafV1(segments[4])
	selected := len(segments) == 6 && validRecordIdentifierV1(segments[4]) && validPayloadLeafV1(segments[5])
	if len(segments) < 5 || strings.Join(segments[:3], "/") != releasePrefix || segments[3] != "payloads" ||
		!unconditional && !selected {
		return fmt.Errorf("%s %q must name a payload record under %q", field, reference.ID, releasePrefix+"/payloads")
	}
	return nil
}

// Mirrors validateBindingArtifactIDV1 for the binding that advertises it.
func validateBindingArtifactReferenceV1(reference RecordReferenceV1, releasePrefix string, binding string) error {
	segments, ok := referenceSegmentsUnderV1(reference, releasePrefix, 7)
	if !ok || segments[3] != "bindings" || segments[4] != binding || segments[5] != "artifacts" ||
		!validPlatformLeafV1(segments[6]) {
		return fmt.Errorf("target binding artifact %q must name an artifact of binding %q", reference.ID, binding)
	}
	return nil
}

// Mirrors the integration fixture ID rule: the fixture name is its leaf.
func validateFixtureReferenceV1(reference RecordReferenceV1, releasePrefix string) error {
	segments, ok := referenceSegmentsUnderV1(reference, releasePrefix, 6)
	if !ok || segments[3] != "validation" || segments[4] != "fixtures" || !validRecordIdentifierV1(segments[5]) {
		return fmt.Errorf("target integration fixture %q must name a fixture record under %q", reference.ID, releasePrefix+"/validation/fixtures")
	}
	return nil
}

func validateProfileReferenceV1(field string, reference RecordReferenceV1, releasePrefix string) error {
	segments, ok := referenceSegmentsUnderV1(reference, releasePrefix, 6)
	if !ok || segments[3] != "validation" || segments[4] != "profiles" || !validRecordIdentifierV1(segments[5]) {
		return fmt.Errorf("%s %q must name a validation profile under %q", field, reference.ID, releasePrefix+"/validation/profiles")
	}
	return nil
}

func validateProfileReferenceListV1(field string, references []RecordReferenceV1, releasePrefix string, allowEmpty bool) error {
	if err := validateReferenceListV1(field, references); err != nil {
		return err
	}
	if !allowEmpty && len(references) == 0 {
		return fmt.Errorf("%s must not be empty", field)
	}
	for _, reference := range references {
		if err := validateProfileReferenceV1(field, reference, releasePrefix); err != nil {
			return err
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
	if len(segments) != 5 && len(segments) != 6 || segments[1] != "releases" || segments[3] != "payloads" {
		return fmt.Errorf("payload ID must use a release payload namespace")
	}
	if _, err := decodeToolVersionSegmentV1(segments[2]); err != nil {
		return fmt.Errorf("payload ID version: %w", err)
	}
	expectedLeaf := value.Name + "-" + strings.ReplaceAll(value.Platform, "/", "-")
	if len(segments) == 5 && segments[4] == expectedLeaf {
		return nil
	}
	if len(segments) != 6 || !validRecordIdentifierV1(segments[4]) || segments[5] != expectedLeaf {
		return fmt.Errorf("payload ID must end with /payloads/<scope>/%s or /payloads/%s", expectedLeaf, expectedLeaf)
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
	if validateAbsoluteRecordPathV1(probe.Path) != nil || probe.Args == nil || len(probe.Args) > maxDefinitionReferences {
		return fmt.Errorf("probe must use an absolute path and bounded argument array")
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

func validateBindingSetSchemaV1(binding BindingSetSchemaV1) error {
	if err := validateSortedUniqueStringsV1("binding options", binding.Options, false); err != nil {
		return err
	}
	for _, option := range binding.Options {
		if !validRecordIdentifierV1(option) {
			return fmt.Errorf("binding options must be canonical identifiers")
		}
	}
	return nil
}

func validateSelectionSchemaV1(selections SelectionSchemaV1) error {
	if selections.Dimensions == nil || len(selections.Dimensions) > maxDefinitionReferences {
		return fmt.Errorf("selection dimensions must use a bounded array")
	}
	dimensionOptions := make(map[string][]string, len(selections.Dimensions))
	for index, dimension := range selections.Dimensions {
		if !validRecordIdentifierV1(dimension.Name) || index > 0 && selections.Dimensions[index-1].Name >= dimension.Name {
			return fmt.Errorf("selection dimensions must have unique sorted canonical names")
		}
		if err := requireNonemptySortedStringsV1("selection dimension options", dimension.Options); err != nil {
			return err
		}
		for _, option := range dimension.Options {
			if !validRecordIdentifierV1(option) {
				return fmt.Errorf("selection dimension %q options must be canonical identifiers", dimension.Name)
			}
		}
		dimensionOptions[dimension.Name] = dimension.Options
	}
	if selections.Combinations == nil || len(selections.Combinations) > maxDefinitionValidationCases {
		return fmt.Errorf("selection combinations must use at most %d entries", maxDefinitionValidationCases)
	}
	if len(selections.Dimensions) == 0 && len(selections.Combinations) != 0 || len(selections.Dimensions) != 0 && len(selections.Combinations) == 0 {
		return fmt.Errorf("selection dimensions and combinations must either both be empty or both be nonempty")
	}
	var previousEncoded []byte
	for index, combination := range selections.Combinations {
		if combination == nil {
			return fmt.Errorf("selection combination %d must be a dimension-keyed map", index)
		}
		dimensionNames := make([]string, 0, len(combination))
		for dimensionName := range combination {
			dimensionNames = append(dimensionNames, dimensionName)
		}
		sort.Strings(dimensionNames)
		for _, dimensionName := range dimensionNames {
			options, ok := dimensionOptions[dimensionName]
			if !ok {
				return fmt.Errorf("selection combination dimension %q is not declared", dimensionName)
			}
			values := combination[dimensionName]
			if err := requireNonemptySortedStringsV1("selection combination values", values); err != nil {
				return err
			}
			for _, value := range values {
				if !containsRecordValueV1(options, value) {
					return fmt.Errorf("selection combination value %q is not advertised for dimension %q", value, dimensionName)
				}
			}
		}
		encoded, err := canonical.Marshal(combination)
		if err != nil {
			return fmt.Errorf("encode selection combination %d: %w", index, err)
		}
		if index > 0 && bytes.Compare(previousEncoded, encoded) >= 0 {
			return fmt.Errorf("selection combinations must be unique and sorted")
		}
		previousEncoded = encoded
	}
	return nil
}

func compareTargetSelectionV1(left TargetSelectionV1, right TargetSelectionV1) int {
	if left.Dimension < right.Dimension {
		return -1
	}
	if left.Dimension > right.Dimension {
		return 1
	}
	return strings.Compare(left.Value, right.Value)
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
	if policy == "linux" || policy == "manylinux2014" {
		return true
	}
	if policy == "manylinux1" || policy == "manylinux2010" {
		// PEP 513 and PEP 571 defined these policies for x86_64 and i686 only.
		// aarch64 support first appears in manylinux2014 under PEP 599, so an
		// ARM64 interpreter never selects a manylinux1 or manylinux2010 wheel.
		return architecture == "x86_64"
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
