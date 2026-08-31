package providers

import (
	"fmt"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/omry/reploy/internal/canonical"
)

const (
	// PortableToolPlanSchemaV1 identifies the provider-neutral portable-tool
	// plan exchanged between closure compilation and provider planning.
	PortableToolPlanSchemaV1 = "portable-tool-plan-v1"

	portableToolBindingContractSchemaV1   = "portable-tool-binding-v1"
	portableToolBindingArtifactSchemaV1   = "portable-tool-binding-artifact-v1"
	portableToolPayloadSchemaV1           = "portable-tool-payload-v1"
	portableToolPackageSetSchemaV1        = "portable-tool-package-set-v1"
	portableToolValidationProfileSchemaV1 = "portable-tool-validation-profile-v1"
	portableToolRecordIdentityKindV1      = "portable-tool-record"
	portableToolRecordIdentitySchemaV1    = "portable-tool-record-v1"
)

// PortableToolPlanV1 is the canonical provider-neutral projection of one or
// more selected portable-tool closures. Tools are ordered by scope and tool
// name; all nested collections are ordered by their stable semantic key.
// Validation profiles are carried for scheduling, but deliberately do not
// participate in SelectedClosureDigest.
type PortableToolPlanV1 struct {
	Schema string                    `json:"schema"`
	Tools  []PortableToolPlanEntryV1 `json:"tools"`
}

// PortableToolPlanEntryV1 carries one selected tool in one resolution scope.
type PortableToolPlanEntryV1 struct {
	Scope                 string                            `json:"scope"`
	SelectedClosureDigest canonical.Digest                  `json:"selected_closure_digest"`
	Provenance            PortableToolReleaseProvenanceV1   `json:"provenance"`
	Runtime               *PortableToolRuntimeProjectionV1  `json:"runtime"`
	Responsibilities      PortableToolResponsibilitiesV1    `json:"responsibilities"`
	Exports               []PortableToolExportV1            `json:"exports"`
	ValidationProfiles    []PortableToolValidationProfileV1 `json:"validation_profiles"`
}

// PortableToolReleaseProvenanceV1 records every release identity component
// that authorizes a selected closure. Version is scheme-native (it is not
// interpreted by this provider-neutral package).
type PortableToolReleaseProvenanceV1 struct {
	Tool           string           `json:"tool"`
	Version        string           `json:"version"`
	Revision       string           `json:"revision"`
	ManifestDigest canonical.Digest `json:"manifest_digest"`
}

// PortableToolRuntimeProjectionV1 carries the contract-owned final-image
// install root and environment projection. Runtime projection is selected
// behavior already covered by SelectedClosureDigest; only validation profiles
// remain outside selected-closure identity.
type PortableToolRuntimeProjectionV1 struct {
	InstallRoot string                              `json:"install_root"`
	Environment []PortableToolEnvironmentVariableV1 `json:"environment"`
}

// PortableToolEnvironmentVariableV1 is one canonical runtime environment
// entry. Names use the portable-tool contract's uppercase environment-name
// grammar.
type PortableToolEnvironmentVariableV1 struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// PortableToolResponsibilitiesV1 groups selected records by their generic
// provider responsibility. The envelopes retain canonical data without
// importing or duplicating toolcatalog's concrete record structs.
type PortableToolResponsibilitiesV1 struct {
	BindingContracts  []PortableToolSelectedRecordV1 `json:"binding_contracts"`
	BindingArtifacts  []PortableToolSelectedRecordV1 `json:"binding_artifacts"`
	Payloads          []PortableToolSelectedRecordV1 `json:"payloads"`
	NativePackageSets []PortableToolSelectedRecordV1 `json:"native_package_sets"`
}

// PortableToolRecordReferenceV1 identifies one exact immutable catalog
// record. IDs are canonical tool-qualified record IDs.
type PortableToolRecordReferenceV1 struct {
	ID     string           `json:"id"`
	Digest canonical.Digest `json:"digest"`
}

// PortableToolSelectedRecordV1 retains both the exact record reference and
// its canonical provider-owned data. Record is a canonical.Envelope alias,
// rather than a toolcatalog concrete value, so later providers can decode
// only the schemas they own.
type PortableToolSelectedRecordV1 struct {
	Reference PortableToolRecordReferenceV1 `json:"reference"`
	Record    CanonicalProviderData         `json:"record"`
}

// PortableToolExportV1 is a generic executable/capability export selected by
// a closure. The name is the semantic conflict key.
type PortableToolExportV1 struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// PortableToolValidationProfileV1 carries one selected validation-profile
// record outside selected-closure identity.
type PortableToolValidationProfileV1 struct {
	Reference PortableToolRecordReferenceV1 `json:"reference"`
	Record    CanonicalProviderData         `json:"record"`
}

// ValidatePortableToolPlanV1 strictly validates canonical plan structure. It
// never sorts, fills, or otherwise normalizes caller-owned data.
func ValidatePortableToolPlanV1(plan PortableToolPlanV1) error {
	if plan.Schema != PortableToolPlanSchemaV1 {
		return fmt.Errorf("portable tool plan schema must be %q", PortableToolPlanSchemaV1)
	}
	if err := validatePortableToolArray("portable tool plan tools", plan.Tools, true); err != nil {
		return err
	}
	seenTools := make(map[string]struct{}, len(plan.Tools))
	seenEnvironment := make(map[string]string)
	seenExports := make(map[string]string)
	for index, tool := range plan.Tools {
		if index > 0 && comparePortableToolPlanEntries(plan.Tools[index-1], tool) >= 0 {
			return fmt.Errorf("portable tool plan tools must be unique and sorted by scope and tool")
		}
		if err := validatePortableToolPlanEntryV1(tool); err != nil {
			return fmt.Errorf("portable tool plan tool %d: %w", index, err)
		}
		key := tool.Scope + "\x00" + tool.Provenance.Tool
		if _, exists := seenTools[key]; exists {
			return fmt.Errorf("portable tool plan repeats selected tool %s/%s", tool.Scope, tool.Provenance.Tool)
		}
		seenTools[key] = struct{}{}
		if tool.Runtime != nil {
			for _, variable := range tool.Runtime.Environment {
				key := tool.Scope + "\x00" + variable.Name
				if value, exists := seenEnvironment[key]; exists && value != variable.Value {
					return fmt.Errorf("portable tool plan scope %q has conflicting environment %q values", tool.Scope, variable.Name)
				}
				seenEnvironment[key] = variable.Value
			}
		}
		for _, exported := range tool.Exports {
			key := tool.Scope + "\x00" + exported.Name
			if value, exists := seenExports[key]; exists && value != exported.Path {
				return fmt.Errorf("portable tool plan scope %q has conflicting export %q paths", tool.Scope, exported.Name)
			}
			seenExports[key] = exported.Path
		}
	}
	// Validate the complete value tree as well as the semantic fields above so
	// invalid UTF-8 or any future canonical-json restriction cannot enter a
	// plan through a provider-owned envelope or projection string.
	if _, err := canonical.Marshal(plan); err != nil {
		return fmt.Errorf("portable tool plan canonical form: %w", err)
	}
	return nil
}

// CanonicalPortableToolPlanBytesV1 validates plan and returns its deterministic
// canonical-json-v1 representation. Canonicalization is intentionally not a
// normalization pass: callers must supply the required ordering themselves.
func CanonicalPortableToolPlanBytesV1(plan PortableToolPlanV1) ([]byte, error) {
	if err := ValidatePortableToolPlanV1(plan); err != nil {
		return nil, err
	}
	encoded, err := canonical.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("portable tool plan canonical form: %w", err)
	}
	return encoded, nil
}

func validatePortableToolPlanEntryV1(entry PortableToolPlanEntryV1) error {
	if err := validatePortableIdentifier("portable tool plan scope", entry.Scope); err != nil {
		return err
	}
	if err := entry.SelectedClosureDigest.Validate(); err != nil {
		return fmt.Errorf("selected closure digest: %w", err)
	}
	if err := validatePortableToolReleaseProvenanceV1(entry.Provenance); err != nil {
		return err
	}
	releaseNamespace, err := portableToolReleaseNamespaceV1(entry.Provenance)
	if err != nil {
		return err
	}
	if entry.Runtime != nil {
		if err := validatePortableToolRuntimeProjectionV1(*entry.Runtime); err != nil {
			return err
		}
	}
	if err := validatePortableToolResponsibilitiesV1(releaseNamespace, entry.Responsibilities); err != nil {
		return err
	}
	if err := validatePortableToolExportsV1("portable tool plan exports", entry.Exports); err != nil {
		return err
	}
	if err := validatePortableToolValidationProfilesV1(releaseNamespace, entry.ValidationProfiles); err != nil {
		return err
	}
	return nil
}

func portableToolReleaseNamespaceV1(provenance PortableToolReleaseProvenanceV1) (string, error) {
	version, err := encodePortableToolVersionSegment(provenance.Version)
	if err != nil {
		return "", fmt.Errorf("portable tool provenance version: %w", err)
	}
	return "tool:" + provenance.Tool + "/releases/" + version, nil
}

func validatePortableToolReleaseProvenanceV1(provenance PortableToolReleaseProvenanceV1) error {
	if err := validatePortableRecordIdentifier("portable tool provenance tool", provenance.Tool); err != nil {
		return err
	}
	if err := validatePortableToken("portable tool provenance version", provenance.Version); err != nil {
		return err
	}
	if err := validatePositiveDecimal("portable tool provenance revision", provenance.Revision); err != nil {
		return err
	}
	if err := provenance.ManifestDigest.Validate(); err != nil {
		return fmt.Errorf("portable tool provenance manifest digest: %w", err)
	}
	return nil
}

func validatePortableToolRuntimeProjectionV1(runtime PortableToolRuntimeProjectionV1) error {
	if err := validateAbsolutePortableLinuxPath("portable tool runtime install root", runtime.InstallRoot); err != nil {
		return err
	}
	if err := validatePortableToolArray("portable tool runtime environment", runtime.Environment, false); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(runtime.Environment))
	for index, variable := range runtime.Environment {
		if index > 0 && runtime.Environment[index-1].Name >= variable.Name {
			return fmt.Errorf("portable tool runtime environment must be unique and sorted by name")
		}
		if !validPortableEnvironmentName(variable.Name) {
			return fmt.Errorf("portable tool runtime environment name %q is invalid", variable.Name)
		}
		if containsPortableControl(variable.Value) {
			return fmt.Errorf("portable tool runtime environment value for %q contains a control character", variable.Name)
		}
		if _, exists := seen[variable.Name]; exists {
			return fmt.Errorf("portable tool runtime environment repeats name %q", variable.Name)
		}
		seen[variable.Name] = struct{}{}
	}
	return nil
}

func validatePortableToolResponsibilitiesV1(
	releaseNamespace string,
	responsibilities PortableToolResponsibilitiesV1,
) error {
	seen := make(map[string]string)
	categories := []struct {
		name   string
		schema string
		list   []PortableToolSelectedRecordV1
	}{
		{name: "binding contracts", schema: portableToolBindingContractSchemaV1, list: responsibilities.BindingContracts},
		{name: "binding artifacts", schema: portableToolBindingArtifactSchemaV1, list: responsibilities.BindingArtifacts},
		{name: "payloads", schema: portableToolPayloadSchemaV1, list: responsibilities.Payloads},
		{name: "native package sets", schema: portableToolPackageSetSchemaV1, list: responsibilities.NativePackageSets},
	}
	for _, category := range categories {
		if err := validatePortableToolArray("portable tool "+category.name, category.list, false); err != nil {
			return err
		}
		previousID := ""
		for index, selected := range category.list {
			if index > 0 && comparePortableToolRecordReferences(category.list[index-1].Reference, selected.Reference) >= 0 {
				return fmt.Errorf("portable tool %s must be unique and sorted by record ID and digest", category.name)
			}
			if previousID == selected.Reference.ID {
				return fmt.Errorf("portable tool %s repeats record ID %q", category.name, selected.Reference.ID)
			}
			previousID = selected.Reference.ID
			if owner, exists := seen[selected.Reference.ID]; exists {
				return fmt.Errorf("record ID %q appears in both %s and %s responsibilities", selected.Reference.ID, owner, category.name)
			}
			seen[selected.Reference.ID] = category.name
			if err := validatePortableToolSelectedRecordV1(
				releaseNamespace,
				category.name,
				category.schema,
				selected,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func validatePortableToolSelectedRecordV1(
	releaseNamespace string,
	category string,
	schema string,
	selected PortableToolSelectedRecordV1,
) error {
	if err := validatePortableToolRecordReferenceV1(selected.Reference); err != nil {
		return fmt.Errorf("portable tool %s record: %w", category, err)
	}
	if err := validatePortableToolRecordCategoryIDV1(
		"portable tool "+category+" record",
		releaseNamespace,
		schema,
		selected.Reference.ID,
	); err != nil {
		return err
	}
	if err := validatePortableToolRecordEnvelopeV1(
		"portable tool "+category+" record "+selected.Reference.ID,
		schema,
		selected.Reference,
		selected.Record,
	); err != nil {
		return err
	}
	return nil
}

func validatePortableToolValidationProfilesV1(
	releaseNamespace string,
	profiles []PortableToolValidationProfileV1,
) error {
	if err := validatePortableToolArray("portable tool validation profiles", profiles, false); err != nil {
		return err
	}
	previous := PortableToolRecordReferenceV1{}
	seen := make(map[string]struct{}, len(profiles))
	for index, profile := range profiles {
		if index > 0 && comparePortableToolRecordReferences(previous, profile.Reference) >= 0 {
			return fmt.Errorf("portable tool validation profiles must be unique and sorted by record ID and digest")
		}
		if err := validatePortableToolRecordReferenceV1(profile.Reference); err != nil {
			return fmt.Errorf("portable tool validation profile: %w", err)
		}
		if err := validatePortableToolRecordCategoryIDV1(
			"portable tool validation profile",
			releaseNamespace,
			portableToolValidationProfileSchemaV1,
			profile.Reference.ID,
		); err != nil {
			return err
		}
		if _, exists := seen[profile.Reference.ID]; exists {
			return fmt.Errorf("portable tool validation profiles repeat record ID %q", profile.Reference.ID)
		}
		seen[profile.Reference.ID] = struct{}{}
		if err := validatePortableToolRecordEnvelopeV1(
			"portable tool validation profile "+profile.Reference.ID,
			portableToolValidationProfileSchemaV1,
			profile.Reference,
			profile.Record,
		); err != nil {
			return err
		}
		previous = profile.Reference
	}
	return nil
}

func validatePortableToolRecordCategoryIDV1(
	field string,
	releaseNamespace string,
	schema string,
	id string,
) error {
	relative, found := strings.CutPrefix(id, releaseNamespace+"/")
	if !found {
		return fmt.Errorf("%s ID %q must remain beneath release namespace %q", field, id, releaseNamespace)
	}
	segments := strings.Split(relative, "/")
	validSegment := func(index int) bool {
		return index < len(segments) && validPortableRecordSegment(segments[index])
	}
	valid := false
	switch schema {
	case portableToolBindingContractSchemaV1:
		valid = len(segments) == 3 && segments[0] == "bindings" && validSegment(1) && segments[2] == "contract"
	case portableToolBindingArtifactSchemaV1:
		valid = len(segments) == 4 && segments[0] == "bindings" && validSegment(1) && segments[2] == "artifacts" && validSegment(3)
	case portableToolPayloadSchemaV1:
		valid = (len(segments) == 2 && segments[0] == "payloads" && validSegment(1)) ||
			(len(segments) == 3 && segments[0] == "payloads" && validSegment(1) && validSegment(2))
	case portableToolPackageSetSchemaV1:
		valid = len(segments) == 2 && segments[0] == "package-sets" && validSegment(1)
	case portableToolValidationProfileSchemaV1:
		valid = len(segments) == 3 && segments[0] == "validation" && segments[1] == "profiles" && validSegment(2)
	}
	if !valid {
		return fmt.Errorf("%s ID %q does not use the canonical %q record namespace", field, id, schema)
	}
	return nil
}

func validatePortableToolRecordReferenceV1(reference PortableToolRecordReferenceV1) error {
	if err := validatePortableRecordID(reference.ID); err != nil {
		return err
	}
	if err := reference.Digest.Validate(); err != nil {
		return fmt.Errorf("portable tool record %q digest: %w", reference.ID, err)
	}
	return nil
}

func validatePortableToolExportsV1(field string, exports []PortableToolExportV1) error {
	if err := validatePortableToolArray(field, exports, false); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(exports))
	for index, exported := range exports {
		if index > 0 && exports[index-1].Name >= exported.Name {
			return fmt.Errorf("%s must be unique and sorted by name", field)
		}
		if err := validatePortableRecordIdentifier(field+" name", exported.Name); err != nil {
			return err
		}
		if err := validateAbsolutePortableLinuxPath(field+" path", exported.Path); err != nil {
			return err
		}
		if _, exists := seen[exported.Name]; exists {
			return fmt.Errorf("%s has conflicting duplicate name %q", field, exported.Name)
		}
		seen[exported.Name] = struct{}{}
	}
	return nil
}

func validatePortableToolRecordEnvelopeV1(
	field string,
	expectedSchema string,
	reference PortableToolRecordReferenceV1,
	envelope CanonicalProviderData,
) error {
	if envelope.Schema != expectedSchema {
		return fmt.Errorf("%s schema must be %q", field, expectedSchema)
	}
	if envelope.Value == nil {
		return fmt.Errorf("%s must contain a non-nil object value", field)
	}
	recordSchema, ok := envelope.Value["schema"].(string)
	if !ok || recordSchema != expectedSchema {
		return fmt.Errorf("%s value schema must be %q", field, expectedSchema)
	}
	recordID, ok := envelope.Value["id"].(string)
	if !ok || recordID != reference.ID {
		return fmt.Errorf("%s value ID must match reference ID %q", field, reference.ID)
	}
	digest, err := canonical.Sum(
		portableToolRecordIdentityKindV1,
		portableToolRecordIdentitySchemaV1,
		envelope.Value,
	)
	if err != nil {
		return fmt.Errorf("%s canonical identity: %w", field, err)
	}
	if digest != reference.Digest {
		return fmt.Errorf("%s digest %q does not match carried record digest %q", field, reference.Digest, digest)
	}
	return nil
}

func validatePortableToolArray[T any](field string, values []T, requireNonempty bool) error {
	if values == nil {
		return fmt.Errorf("%s must use an explicit array", field)
	}
	if requireNonempty && len(values) == 0 {
		return fmt.Errorf("%s must contain at least one entry", field)
	}
	return nil
}

func validatePortableIdentifier(field string, value string) error {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return fmt.Errorf("%s %q must match [a-z][a-z0-9_-]*", field, value)
	}
	for _, char := range value[1:] {
		if char < 'a' || char > 'z' {
			if char < '0' || char > '9' {
				if char != '_' && char != '-' {
					return fmt.Errorf("%s %q must match [a-z][a-z0-9_-]*", field, value)
				}
			}
		}
	}
	return nil
}

func validatePortableRecordIdentifier(field string, value string) error {
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

func validatePortableToken(field string, value string) error {
	if value == "" || strings.TrimSpace(value) != value || containsPortableControl(value) {
		return fmt.Errorf("%s must be nonempty canonical text", field)
	}
	return nil
}

func validatePositiveDecimal(field string, value string) error {
	if value == "" || value[0] < '1' || value[0] > '9' {
		return fmt.Errorf("%s must be a canonical positive decimal string", field)
	}
	for _, char := range value[1:] {
		if char < '0' || char > '9' {
			return fmt.Errorf("%s must be a canonical positive decimal string", field)
		}
	}
	return nil
}

func validateAbsolutePortableLinuxPath(field string, value string) error {
	if value == "" || containsPortableControl(value) || !path.IsAbs(value) || path.Clean(value) != value ||
		strings.Contains(value, `\`) {
		return fmt.Errorf("%s %q must be a normalized absolute Linux path", field, value)
	}
	for index, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			// The leading empty segment is the root marker in an absolute
			// Linux path. Any other empty segment represents a repeated or
			// trailing separator and is not canonical.
			if index == 0 && segment == "" {
				continue
			}
			return fmt.Errorf("%s %q must be a normalized absolute Linux path", field, value)
		}
	}
	return nil
}

func validPortableEnvironmentName(value string) bool {
	if value == "" || value[0] < 'A' || value[0] > 'Z' {
		return false
	}
	for index := 1; index < len(value); index++ {
		char := value[index]
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
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
	canonical, err := encodePortableToolVersionSegment(version)
	if err != nil || canonical != value {
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

func comparePortableToolPlanEntries(left PortableToolPlanEntryV1, right PortableToolPlanEntryV1) int {
	if left.Scope < right.Scope {
		return -1
	}
	if left.Scope > right.Scope {
		return 1
	}
	return strings.Compare(left.Provenance.Tool, right.Provenance.Tool)
}

func comparePortableToolRecordReferences(left PortableToolRecordReferenceV1, right PortableToolRecordReferenceV1) int {
	if left.ID < right.ID {
		return -1
	}
	if left.ID > right.ID {
		return 1
	}
	return strings.Compare(string(left.Digest), string(right.Digest))
}
