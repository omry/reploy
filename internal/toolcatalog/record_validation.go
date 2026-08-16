package toolcatalog

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aquasecurity/go-version/pkg/semver"
	dockerreference "github.com/distribution/reference"
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
		return validateReferenceListV1("tool releases", value.Releases)
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
		prefix := fmt.Sprintf("tool:%s/releases/%s/revisions/%s/manifest", value.Tool, versionSegment, value.Revision)
		if value.ID != prefix {
			return fmt.Errorf("release manifest ID must be %q", prefix)
		}
		if value.Aliases == nil || len(value.Aliases) > maxDefinitionReferences {
			return fmt.Errorf("release aliases must use a bounded array")
		}
		for index, alias := range value.Aliases {
			if !validRecordSegmentV1(alias) || alias == value.Version || index > 0 && value.Aliases[index-1] >= alias {
				return fmt.Errorf("release aliases must be canonical, unique, sorted, and different from the exact version")
			}
		}
		if err := validateRecordReferenceV1(value.Contract); err != nil {
			return fmt.Errorf("release contract: %w", err)
		}
		if err := validateRecordReferenceV1(value.ValidationProfile); err != nil {
			return fmt.Errorf("release validation profile: %w", err)
		}
		if len(value.Targets) == 0 {
			return fmt.Errorf("release manifest targets must not be empty")
		}
		if err := validateReferenceListV1("release targets", value.Targets); err != nil {
			return err
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
			if err := validateRecordReferenceV1(mapping.Source); err != nil {
				return fmt.Errorf("artifact source mapping %d source: %w", index, err)
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
			canonicalURL, err := canonicalSourceURLV1(raw)
			if err != nil {
				return fmt.Errorf("release provenance %d: %w", index, err)
			}
			if index > 0 && previousProvenance >= canonicalURL {
				return fmt.Errorf("release provenance must be unique and sorted")
			}
			previousProvenance = canonicalURL
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
		if value.Probes == nil || len(value.Probes) > maxDefinitionReferences {
			return fmt.Errorf("contract probes must use a bounded array")
		}
		for index, probe := range value.Probes {
			if err := validateProbeV1(probe); err != nil {
				return fmt.Errorf("contract probe %d: %w", index, err)
			}
		}
		if value.Exports == nil || len(value.Exports) > maxDefinitionReferences {
			return fmt.Errorf("contract exports must use a bounded array")
		}
		for index, exported := range value.Exports {
			if !validRecordIdentifierV1(exported.Name) || validateAbsoluteRecordPathV1(exported.Path) != nil || index > 0 && value.Exports[index-1].Name >= exported.Name {
				return fmt.Errorf("contract exports must be unique, sorted, and absolute")
			}
		}
		return validateRuntimeV1(value.Contexts, value.Runtime)
	case *TargetRecordV1:
		if record.Schema != TargetRecordSchemaV1 || value.Schema != TargetRecordSchemaV1 || value.ID != record.ID {
			return fmt.Errorf("target record identity or validation contract is incomplete")
		}
		if err := validateTargetIdentityV1(value.Target); err != nil {
			return err
		}
		expectedSuffix := fmt.Sprintf("/targets/%s/%s/%s", value.Target.OSReleaseID, value.Target.VersionID, value.Target.OCIArchitecture)
		if !strings.HasSuffix(value.ID, expectedSuffix) {
			return fmt.Errorf("target record ID must end with %q", expectedSuffix)
		}
		if err := validateRecordReferenceV1(value.IntegrationFixture); err != nil {
			return fmt.Errorf("target integration fixture: %w", err)
		}
		if err := validateRecordReferenceV1(value.ValidationProfile); err != nil {
			return fmt.Errorf("target validation profile: %w", err)
		}
		if err := validateReferenceListV1("target package sets", value.PackageSets); err != nil {
			return err
		}
		if err := validateReferenceListV1("target payloads", value.Payloads); err != nil {
			return err
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
			if err := validateRecordReferenceV1(binding.Artifact); err != nil {
				return fmt.Errorf("target binding %q artifact: %w", binding.Name, err)
			}
		}
		if value.Selections == nil || len(value.Selections) > maxDefinitionReferences {
			return fmt.Errorf("target selections must use a bounded array")
		}
		for index, selection := range value.Selections {
			if !validRecordIdentifierV1(selection.Name) || index > 0 && value.Selections[index-1].Name >= selection.Name || len(selection.Payloads) == 0 {
				return fmt.Errorf("target selections must be unique, sorted, and nonempty")
			}
			if err := validateReferenceListV1("target selection payloads", selection.Payloads); err != nil {
				return err
			}
		}
		if value.Probes == nil || len(value.Probes) > maxDefinitionReferences {
			return fmt.Errorf("target probes must use a bounded array")
		}
		for index, probe := range value.Probes {
			if err := validateProbeV1(probe); err != nil {
				return fmt.Errorf("target probe %d: %w", index, err)
			}
		}
		return nil
	case *BindingContractV1:
		if record.Schema != BindingContractSchemaV1 || value.Schema != BindingContractSchemaV1 || value.ID != record.ID || !validRecordIdentifierV1(value.Name) || !validPackageNameV1(value.Package) || value.CLI == "" {
			return fmt.Errorf("binding contract is incomplete")
		}
		if !strings.HasSuffix(value.ID, "/bindings/"+value.Name+"/contract") {
			return fmt.Errorf("binding contract ID is inconsistent with its name")
		}
		if err := validateAbsoluteRecordPathV1(value.CLI); err != nil {
			return fmt.Errorf("binding CLI: %w", err)
		}
		if err := requireNonemptySortedStringsV1("binding requirements", value.Requirements); err != nil {
			return err
		}
		if err := requireNonemptySortedStringsV1("supported Python", value.SupportedPython); err != nil {
			return err
		}
		for _, version := range value.SupportedPython {
			if !validRecordSegmentV1(version) {
				return fmt.Errorf("supported Python versions must be canonical version values")
			}
		}
		return nil
	case *BindingArtifactRecordV1:
		if record.Schema != BindingArtifactSchemaV1 || value.Schema != BindingArtifactSchemaV1 || value.ID != record.ID || !validRecordIdentifierV1(value.Binding) || !validPlatformV1(value.Platform) || validateRecordPathV1(value.Filename, false) != nil || path.Dir(value.Filename) != "." {
			return fmt.Errorf("binding artifact identity is incomplete")
		}
		if err := validateCanonicalDecimalV1("binding artifact size", value.Size, true); err != nil {
			return err
		}
		if err := value.SHA256.Validate(); err != nil {
			return fmt.Errorf("binding artifact digest: %w", err)
		}
		if err := requireNonemptySortedStringsV1("binding artifact tags", value.Tags); err != nil || !validRecordTokenV1(value.RequiresPython) {
			if err != nil {
				return err
			}
			return fmt.Errorf("binding artifact requires_python is incomplete")
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
			canonicalURL, err := canonicalSourceURLV1(mirror)
			if err != nil {
				return fmt.Errorf("artifact source mirror %d: %w", index, err)
			}
			if _, exists := seenMirrors[canonicalURL]; exists {
				return fmt.Errorf("artifact source mirrors must be unique")
			}
			seenMirrors[canonicalURL] = struct{}{}
		}
		if len(value.Provenance) == 0 || len(value.Provenance) > maxDefinitionReferences {
			return fmt.Errorf("artifact source provenance must use a nonempty bounded array")
		}
		previousProvenance := ""
		for index, provenance := range value.Provenance {
			canonicalURL, err := canonicalSourceURLV1(provenance)
			if err != nil {
				return fmt.Errorf("artifact source provenance %d: %w", index, err)
			}
			if index > 0 && previousProvenance >= canonicalURL {
				return fmt.Errorf("artifact source provenance must be unique and sorted")
			}
			previousProvenance = canonicalURL
		}
		return nil
	case *NativePackageSetV1:
		if record.Schema != NativePackageSetSchemaV1 || value.Schema != NativePackageSetSchemaV1 || value.ID != record.ID || value.Manager != "apt" {
			return fmt.Errorf("native package-set identity is incomplete")
		}
		return requireNonemptySortedStringsV1("native package requirements", value.Requirements)
	case *IntegrationFixtureRecordV1:
		if record.Schema != IntegrationFixtureSchemaV1 || value.Schema != IntegrationFixtureSchemaV1 || value.ID != record.ID {
			return fmt.Errorf("integration fixture identity is inconsistent")
		}
		if err := validateTargetIdentityV1(value.Target); err != nil {
			return fmt.Errorf("integration fixture target: %w", err)
		}
		expectedSuffix := fmt.Sprintf("/validation/fixtures/%s-%s-%s", value.Target.OSReleaseID, value.Target.VersionID, value.Target.OCIArchitecture)
		if !strings.HasSuffix(value.ID, expectedSuffix) {
			return fmt.Errorf("integration fixture ID must end with %q", expectedSuffix)
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
		return validateSortedUniqueStringsV1("integration fixture selections", value.Selections, false)
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
		if value.Network != "none" {
			return fmt.Errorf("validation profile must disable networking")
		}
		return nil
	default:
		return fmt.Errorf("unsupported record value %T", record.Value)
	}
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
	if !validRecordTokenV1(evidence.Fixture) || !validRecordTokenV1(evidence.ValidatorVersion) || evidence.Result != "pass" && evidence.Result != "fail" {
		return fmt.Errorf("validation evidence fixture, validator, or result is invalid")
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
