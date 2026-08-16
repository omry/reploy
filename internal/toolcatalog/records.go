package toolcatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"path"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/omry/reploy/internal/canonical"
)

const (
	ToolRecordSchemaV1           = "portable-tool-v1"
	ReleaseManifestSchemaV1      = "portable-tool-release-manifest-v1"
	ReleaseContractSchemaV1      = "portable-tool-release-contract-v1"
	TargetRecordSchemaV1         = "portable-tool-target-v1"
	BindingContractSchemaV1      = "portable-tool-binding-v1"
	BindingArtifactSchemaV1      = "portable-tool-binding-artifact-v1"
	PayloadRecordSchemaV1        = "portable-tool-payload-v1"
	ArtifactSourceRecordSchemaV1 = "portable-tool-artifact-source-v1"
	NativePackageSetSchemaV1     = "portable-tool-package-set-v1"
	IntegrationFixtureSchemaV1   = "portable-tool-integration-fixture-v1"
	ValidationProfileSchemaV1    = "portable-tool-validation-profile-v1"
	ValidationEvidenceSchemaV1   = "portable-tool-validation-evidence-v1"
	SelectedClosureIdentityV1    = "portable-tool-selected-closure-v1"
	portableToolRecordIdentityV1 = "portable-tool-record-v1"
	maxDefinitionFileBytes       = 1 << 20
	maxDefinitionJSONDepth       = 32
	maxDefinitionJSONMembers     = 4096
	maxDefinitionJSONStringBytes = 64 << 10
	maxDefinitionReferences      = 1024
	maxDefinitionArtifactMirrors = 8
)

var canonicalDecimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

type RecordReferenceV1 struct {
	ID     string           `json:"id"`
	Digest canonical.Digest `json:"digest"`
}

type ToolRecordV1 struct {
	Schema         string              `json:"schema"`
	ID             string              `json:"id"`
	Name           string              `json:"name"`
	VersionScheme  string              `json:"version_scheme"`
	DefaultVersion string              `json:"default_version,omitempty"`
	Summary        string              `json:"summary"`
	Upstream       string              `json:"upstream"`
	Source         string              `json:"source"`
	License        string              `json:"license"`
	Releases       []RecordReferenceV1 `json:"releases"`
}

type ReleaseManifestV1 struct {
	Schema            string                    `json:"schema"`
	ID                string                    `json:"id"`
	Tool              string                    `json:"tool"`
	Version           string                    `json:"version"`
	Revision          string                    `json:"revision"`
	Contract          RecordReferenceV1         `json:"contract"`
	Targets           []RecordReferenceV1       `json:"targets"`
	ArtifactSources   []ArtifactSourceMappingV1 `json:"artifact_sources"`
	Provenance        []string                  `json:"provenance"`
	ValidationProfile RecordReferenceV1         `json:"validation_profile"`
}

type ArtifactSourceMappingV1 struct {
	ArtifactSHA256 canonical.Digest  `json:"artifact_sha256"`
	Artifact       RecordReferenceV1 `json:"artifact"`
	Source         RecordReferenceV1 `json:"source"`
}

type ReleaseContractV1 struct {
	Schema             string             `json:"schema"`
	ID                 string             `json:"id"`
	Contexts           []string           `json:"contexts"`
	SupportedReploy    string             `json:"supported_reploy"`
	Binding            BindingRequestV1   `json:"binding"`
	Selections         SelectionRequestV1 `json:"selections"`
	Runtime            *RecordRuntimeV1   `json:"runtime,omitempty"`
	Probes             []RecordProbeV1    `json:"probes"`
	Exports            []ToolExportV1     `json:"exports"`
	ResolverPrimitives []string           `json:"resolver_primitives"`
}

type BindingRequestV1 struct {
	Options  []string `json:"options"`
	Required bool     `json:"required"`
	Default  string   `json:"default"`
}

type SelectionRequestV1 struct {
	Options             []string   `json:"options"`
	Minimum             string     `json:"minimum"`
	Maximum             string     `json:"maximum"`
	Defaults            []string   `json:"defaults"`
	CompatibilityGroups [][]string `json:"compatibility_groups"`
}

type ToolExportV1 struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type RecordProbeV1 struct {
	Path    string   `json:"path"`
	Args    []string `json:"args"`
	Network string   `json:"network"`
}

type RecordRuntimeV1 struct {
	InstallRoot string                        `json:"install_root"`
	Environment []RecordEnvironmentVariableV1 `json:"environment"`
}

type RecordEnvironmentVariableV1 struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type TargetRecordV1 struct {
	Schema             string              `json:"schema"`
	ID                 string              `json:"id"`
	Target             TargetIdentityV1    `json:"target"`
	PackageSets        []RecordReferenceV1 `json:"package_sets"`
	Bindings           []TargetBindingV1   `json:"bindings"`
	Payloads           []RecordReferenceV1 `json:"payloads"`
	Selections         []TargetSelectionV1 `json:"selections"`
	Probes             []RecordProbeV1     `json:"probes"`
	IntegrationFixture RecordReferenceV1   `json:"integration_fixture"`
	ValidationProfile  RecordReferenceV1   `json:"validation_profile"`
}

type TargetIdentityV1 struct {
	Platform           string `json:"platform"`
	OSReleaseID        string `json:"os_release_id"`
	VersionID          string `json:"version_id"`
	OCIArchitecture    string `json:"oci_architecture"`
	NativeArchitecture string `json:"native_architecture"`
	PackageManager     string `json:"package_manager"`
}

type TargetBindingV1 struct {
	Name     string            `json:"name"`
	Contract RecordReferenceV1 `json:"contract"`
	Artifact RecordReferenceV1 `json:"artifact"`
}

type TargetSelectionV1 struct {
	Name     string              `json:"name"`
	Payloads []RecordReferenceV1 `json:"payloads"`
}

type BindingContractV1 struct {
	Schema          string   `json:"schema"`
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Package         string   `json:"package"`
	Requirements    []string `json:"requirements"`
	SupportedPython []string `json:"supported_python"`
	CLI             string   `json:"cli"`
}

type BindingArtifactRecordV1 struct {
	Schema            string               `json:"schema"`
	ID                string               `json:"id"`
	Binding           string               `json:"binding"`
	Platform          string               `json:"platform"`
	Filename          string               `json:"filename"`
	Size              string               `json:"size"`
	SHA256            canonical.Digest     `json:"sha256"`
	Tags              []string             `json:"tags"`
	RequiresPython    string               `json:"requires_python"`
	BundledComponents []BundledComponentV1 `json:"bundled_components"`
}

type BundledComponentV1 struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Path    string `json:"path"`
}

type PayloadRecordV1 struct {
	Schema           string           `json:"schema"`
	ID               string           `json:"id"`
	Selection        string           `json:"selection"`
	Name             string           `json:"name"`
	Revision         string           `json:"revision"`
	UpstreamVersion  string           `json:"upstream_version"`
	Platform         string           `json:"platform"`
	LogicalPath      string           `json:"logical_path"`
	Kind             string           `json:"kind"`
	Size             string           `json:"size"`
	SHA256           canonical.Digest `json:"sha256"`
	Entries          string           `json:"entries"`
	UnpackedSize     string           `json:"unpacked_size"`
	InstallDirectory string           `json:"install_directory"`
	ArchiveRoot      string           `json:"archive_root"`
	Executable       string           `json:"executable"`
}

type ArtifactSourceRecordV1 struct {
	Schema     string           `json:"schema"`
	ID         string           `json:"id"`
	SHA256     canonical.Digest `json:"sha256"`
	Size       string           `json:"size"`
	Resolver   string           `json:"resolver"`
	Mirrors    []string         `json:"mirrors"`
	Provenance []string         `json:"provenance"`
}

type NativePackageSetV1 struct {
	Schema       string   `json:"schema"`
	ID           string   `json:"id"`
	Manager      string   `json:"manager"`
	Requirements []string `json:"requirements"`
}

type IntegrationFixtureRecordV1 struct {
	Schema          string           `json:"schema"`
	ID              string           `json:"id"`
	Target          TargetIdentityV1 `json:"target"`
	BaseImage       string           `json:"base_image"`
	BaseImageDigest canonical.Digest `json:"base_image_digest"`
	Context         string           `json:"context"`
	Binding         string           `json:"binding"`
	Selections      []string         `json:"selections"`
}

type ValidationProfileRecordV1 struct {
	Schema    string `json:"schema"`
	ID        string `json:"id"`
	Tool      string `json:"tool"`
	Version   string `json:"version"`
	Validator string `json:"validator"`
	Network   string `json:"network"`
}

type ValidationEvidenceV1 struct {
	Schema                string             `json:"schema"`
	Tool                  string             `json:"tool"`
	Version               string             `json:"version"`
	Revision              string             `json:"revision"`
	ManifestDigest        canonical.Digest   `json:"manifest_digest"`
	SelectedClosureDigest canonical.Digest   `json:"selected_closure_digest"`
	Target                TargetIdentityV1   `json:"target"`
	BaseImageDigest       canonical.Digest   `json:"base_image_digest"`
	Binding               string             `json:"binding"`
	Selections            []string           `json:"selections"`
	Fixture               string             `json:"fixture"`
	ValidatorVersion      string             `json:"validator_version"`
	Result                string             `json:"result"`
	ProbeDigests          []canonical.Digest `json:"probe_digests"`
}

type recordHeaderV1 struct {
	Schema string `json:"schema"`
	ID     string `json:"id"`
}

type loadedRecordV1 struct {
	ID     string
	Schema string
	Digest canonical.Digest
	Path   string
	Value  any
}

func decodeRecordV1(filename string, payload []byte) (loadedRecordV1, error) {
	if err := validateStrictJSONV1(payload); err != nil {
		return loadedRecordV1{}, fmt.Errorf("decode %s: %w", filename, err)
	}
	var header recordHeaderV1
	if err := json.Unmarshal(payload, &header); err != nil {
		return loadedRecordV1{}, fmt.Errorf("decode %s header: %w", filename, err)
	}
	if header.Schema == "" {
		return loadedRecordV1{}, fmt.Errorf("decode %s: record schema is required", filename)
	}
	if header.ID == "" {
		return loadedRecordV1{}, fmt.Errorf("decode %s: record ID is required", filename)
	}
	var value any
	switch header.Schema {
	case ToolRecordSchemaV1:
		value = &ToolRecordV1{}
	case ReleaseManifestSchemaV1:
		value = &ReleaseManifestV1{}
	case ReleaseContractSchemaV1:
		value = &ReleaseContractV1{}
	case TargetRecordSchemaV1:
		value = &TargetRecordV1{}
	case BindingContractSchemaV1:
		value = &BindingContractV1{}
	case BindingArtifactSchemaV1:
		value = &BindingArtifactRecordV1{}
	case PayloadRecordSchemaV1:
		value = &PayloadRecordV1{}
	case ArtifactSourceRecordSchemaV1:
		value = &ArtifactSourceRecordV1{}
	case NativePackageSetSchemaV1:
		value = &NativePackageSetV1{}
	case IntegrationFixtureSchemaV1:
		value = &IntegrationFixtureRecordV1{}
	case ValidationProfileSchemaV1:
		value = &ValidationProfileRecordV1{}
	default:
		return loadedRecordV1{}, fmt.Errorf("decode %s: unsupported schema %q", filename, header.Schema)
	}
	if err := decodeExactJSONV1(payload, value); err != nil {
		return loadedRecordV1{}, fmt.Errorf("decode %s: %w", filename, err)
	}
	record := loadedRecordV1{ID: header.ID, Schema: header.Schema, Path: filename, Value: value}
	if err := validateLoadedRecordV1(record); err != nil {
		return loadedRecordV1{}, fmt.Errorf("validate %s: %w", filename, err)
	}
	digest, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, value)
	if err != nil {
		return loadedRecordV1{}, fmt.Errorf("digest %s: %w", filename, err)
	}
	record.Digest = digest
	return record, nil
}

func decodeValidationEvidenceV1(filename string, payload []byte) (ValidationEvidenceV1, error) {
	if err := validateStrictJSONV1(payload); err != nil {
		return ValidationEvidenceV1{}, fmt.Errorf("decode %s: %w", filename, err)
	}
	var evidence ValidationEvidenceV1
	if err := decodeExactJSONV1(payload, &evidence); err != nil {
		return ValidationEvidenceV1{}, fmt.Errorf("decode %s: %w", filename, err)
	}
	if err := validateValidationEvidenceV1(evidence); err != nil {
		return ValidationEvidenceV1{}, fmt.Errorf("validate %s: %w", filename, err)
	}
	return evidence, nil
}

func decodeExactJSONV1(payload []byte, target any) error {
	if err := validateExactJSONMembersV1(payload, reflect.TypeOf(target)); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

func validateExactJSONMembersV1(payload json.RawMessage, target reflect.Type) error {
	for target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	if bytes.Equal(bytes.TrimSpace(payload), []byte("null")) {
		return fmt.Errorf("JSON null is not valid for %s", target)
	}
	switch target.Kind() {
	case reflect.Struct:
		var members map[string]json.RawMessage
		if err := json.Unmarshal(payload, &members); err != nil {
			return nil
		}
		fields := make(map[string]reflect.Type, target.NumField())
		requiredFields := make([]string, 0, target.NumField())
		for index := 0; index < target.NumField(); index++ {
			field := target.Field(index)
			if !field.IsExported() {
				continue
			}
			tag := strings.Split(field.Tag.Get("json"), ",")
			name := tag[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			fields[name] = field.Type
			if !containsRecordValueV1(tag[1:], "omitempty") {
				requiredFields = append(requiredFields, name)
			}
		}
		for name, value := range members {
			fieldType, exists := fields[name]
			if !exists {
				return fmt.Errorf("unknown field %q", name)
			}
			if err := validateExactJSONMembersV1(value, fieldType); err != nil {
				return err
			}
		}
		for _, name := range requiredFields {
			if _, exists := members[name]; !exists {
				return fmt.Errorf("required field %q is missing", name)
			}
		}
	case reflect.Slice, reflect.Array:
		var elements []json.RawMessage
		if err := json.Unmarshal(payload, &elements); err != nil {
			return nil
		}
		for _, element := range elements {
			if err := validateExactJSONMembersV1(element, target.Elem()); err != nil {
				return err
			}
		}
	case reflect.Map:
		var members map[string]json.RawMessage
		if err := json.Unmarshal(payload, &members); err != nil {
			return nil
		}
		for _, value := range members {
			if err := validateExactJSONMembersV1(value, target.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateStrictJSONV1(payload []byte) error {
	if len(payload) == 0 || len(payload) > maxDefinitionFileBytes {
		return fmt.Errorf("record size must be between 1 and %d bytes", maxDefinitionFileBytes)
	}
	if !utf8.Valid(payload) {
		return fmt.Errorf("record must be valid UTF-8")
	}
	if err := validateJSONStringSurrogatesV1(payload); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	members := 0
	if err := scanStrictJSONValueV1(decoder, 0, &members); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON token %v", token)
		}
		return err
	}
	return nil
}

func validateJSONStringSurrogatesV1(payload []byte) error {
	inString := false
	for index := 0; index < len(payload); index++ {
		if !inString {
			if payload[index] == '"' {
				inString = true
			}
			continue
		}
		switch payload[index] {
		case '"':
			inString = false
		case '\\':
			if index+1 >= len(payload) {
				return fmt.Errorf("invalid JSON string escape")
			}
			if payload[index+1] != 'u' {
				index++
				continue
			}
			value, ok := parseJSONHexQuadV1(payload, index+2)
			if !ok {
				return fmt.Errorf("invalid JSON Unicode escape")
			}
			if value >= 0xdc00 && value <= 0xdfff {
				return fmt.Errorf("JSON string contains an unpaired UTF-16 surrogate escape")
			}
			if value >= 0xd800 && value <= 0xdbff {
				if index+12 > len(payload) || payload[index+6] != '\\' || payload[index+7] != 'u' {
					return fmt.Errorf("JSON string contains an unpaired UTF-16 surrogate escape")
				}
				low, validLow := parseJSONHexQuadV1(payload, index+8)
				if !validLow || low < 0xdc00 || low > 0xdfff {
					return fmt.Errorf("JSON string contains an unpaired UTF-16 surrogate escape")
				}
				index += 11
				continue
			}
			index += 5
		}
	}
	return nil
}

func parseJSONHexQuadV1(payload []byte, start int) (uint16, bool) {
	if start+4 > len(payload) {
		return 0, false
	}
	parsed, err := strconv.ParseUint(string(payload[start:start+4]), 16, 16)
	return uint16(parsed), err == nil
}

func scanStrictJSONValueV1(decoder *json.Decoder, depth int, members *int) error {
	if depth > maxDefinitionJSONDepth {
		return fmt.Errorf("JSON nesting exceeds %d", maxDefinitionJSONDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				nameToken, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := nameToken.(string)
				if !ok {
					return fmt.Errorf("object member name is not a string")
				}
				if seen[name] {
					return fmt.Errorf("duplicate object member %q", name)
				}
				if len(name) > maxDefinitionJSONStringBytes {
					return fmt.Errorf("object member name exceeds %d bytes", maxDefinitionJSONStringBytes)
				}
				seen[name] = true
				(*members)++
				if *members > maxDefinitionJSONMembers {
					return fmt.Errorf("JSON member count exceeds %d", maxDefinitionJSONMembers)
				}
				if err := scanStrictJSONValueV1(decoder, depth+1, members); err != nil {
					return err
				}
			}
			_, err := decoder.Token()
			return err
		case '[':
			for decoder.More() {
				(*members)++
				if *members > maxDefinitionJSONMembers {
					return fmt.Errorf("JSON member count exceeds %d", maxDefinitionJSONMembers)
				}
				if err := scanStrictJSONValueV1(decoder, depth+1, members); err != nil {
					return err
				}
			}
			_, err := decoder.Token()
			return err
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", value)
		}
	case string:
		if len(value) > maxDefinitionJSONStringBytes {
			return fmt.Errorf("JSON string exceeds %d bytes", maxDefinitionJSONStringBytes)
		}
	case json.Number:
		return fmt.Errorf("JSON numbers are not supported; encode schema quantities as decimal strings")
	case bool, nil:
		return nil
	default:
		return fmt.Errorf("unsupported JSON token %T", token)
	}
	return nil
}

func validateRecordReferenceV1(reference RecordReferenceV1) error {
	if err := validateRecordIDV1(reference.ID); err != nil {
		return err
	}
	if err := reference.Digest.Validate(); err != nil {
		return fmt.Errorf("reference %q digest: %w", reference.ID, err)
	}
	return nil
}

func validateRecordIDV1(value string) error {
	if value == "" || strings.TrimSpace(value) != value || !strings.HasPrefix(value, "tool:") {
		return fmt.Errorf("record ID %q must be a canonical tool-qualified ID", value)
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' {
			continue
		}
		switch character {
		case '.', '+', '-', '_', ':', '/':
		case '%':
			if index+2 >= len(value) {
				return fmt.Errorf("record ID %q contains an incomplete percent escape", value)
			}
			if _, ok := uppercaseHexValueV1(value[index+1]); !ok {
				return fmt.Errorf("record ID %q percent escapes must use uppercase hexadecimal", value)
			}
			if _, ok := uppercaseHexValueV1(value[index+2]); !ok {
				return fmt.Errorf("record ID %q percent escapes must use uppercase hexadecimal", value)
			}
			index += 2
		default:
			return fmt.Errorf("record ID %q contains unsupported character %q", value, character)
		}
	}
	segments := strings.Split(value, "/")
	toolName := strings.TrimPrefix(segments[0], "tool:")
	if !validRecordIdentifierV1(toolName) {
		return fmt.Errorf("record ID %q has an invalid tool name", value)
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("record ID %q contains an invalid path segment", value)
		}
	}
	return nil
}

func validRecordIdentifierV1(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value[1:] {
		if character < 'a' || character > 'z' {
			if character < '0' || character > '9' {
				if character != '-' {
					return false
				}
			}
		}
	}
	return true
}

func validateCanonicalDecimalV1(field string, value string, positive bool) error {
	if !canonicalDecimalPattern.MatchString(value) {
		return fmt.Errorf("%s must be a canonical decimal string", field)
	}
	parsed, err := strconv.ParseUint(value, 10, 63)
	if err != nil || positive && parsed == 0 {
		return fmt.Errorf("%s must be a bounded positive decimal string", field)
	}
	return nil
}

func validateReferenceListV1(field string, references []RecordReferenceV1) error {
	if references == nil || len(references) > maxDefinitionReferences {
		return fmt.Errorf("%s must use an array with at most %d entries", field, maxDefinitionReferences)
	}
	for index, reference := range references {
		if err := validateRecordReferenceV1(reference); err != nil {
			return fmt.Errorf("%s[%d]: %w", field, index, err)
		}
		if index > 0 && references[index-1].ID >= reference.ID {
			return fmt.Errorf("%s must be unique and sorted by ID", field)
		}
	}
	return nil
}

func validateSourceURLV1(raw string) error {
	_, err := canonicalSourceURLV1(raw)
	return err
}

func canonicalSourceURLV1(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Opaque != "" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Contains(raw, "#") || parsed.Host != strings.ToLower(parsed.Host) || strings.HasSuffix(parsed.Hostname(), ".") || parsed.Port() == "443" || !asciiURLHostV1(parsed.Hostname()) || !canonicalPercentEscapesV1(parsed.EscapedPath()) || hasURLDotSegmentV1(parsed.Path) {
		return "", fmt.Errorf("source URL must be a canonical credential-free HTTPS URL without query or fragment")
	}
	port := parsed.Port()
	if strings.HasSuffix(parsed.Host, ":") || port != "" && (!canonicalDecimalPattern.MatchString(port) || port == "0") {
		return "", fmt.Errorf("source URL must use a canonical authority")
	}
	if port != "" {
		parsedPort, err := strconv.ParseUint(port, 10, 16)
		if err != nil || parsedPort == 0 {
			return "", fmt.Errorf("source URL must use a canonical authority")
		}
	}
	host := parsed.Hostname()
	if address, err := netip.ParseAddr(host); err == nil {
		if address.Zone() != "" {
			return "", fmt.Errorf("source URL must use a canonical authority")
		}
		host = address.String()
		if address.Is6() {
			host = "[" + host + "]"
		}
	} else if strings.Contains(host, ":") || numericURLHostV1(host) {
		return "", fmt.Errorf("source URL must use a canonical authority")
	}
	if port != "" {
		host += ":" + port
	}
	escapedPath := parsed.EscapedPath()
	if escapedPath == "" {
		escapedPath = "/"
	}
	return "https://" + host + canonicalSourcePathV1(escapedPath), nil
}

func numericURLHostV1(host string) bool {
	if host == "" {
		return false
	}
	for _, character := range host {
		if character != '.' && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func canonicalSourcePathV1(escapedPath string) string {
	var normalized strings.Builder
	normalized.Grow(len(escapedPath))
	for index := 0; index < len(escapedPath); index++ {
		if escapedPath[index] != '%' {
			normalized.WriteByte(escapedPath[index])
			continue
		}
		value, err := strconv.ParseUint(escapedPath[index+1:index+3], 16, 8)
		if err != nil {
			normalized.WriteString(escapedPath[index:])
			break
		}
		character := byte(value)
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("-._~", rune(character)) {
			normalized.WriteByte(character)
		} else {
			normalized.WriteString(escapedPath[index : index+3])
		}
		index += 2
	}
	return normalized.String()
}

func asciiURLHostV1(host string) bool {
	for _, character := range host {
		if character > unicode.MaxASCII || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func canonicalPercentEscapesV1(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] != '%' {
			continue
		}
		if index+2 >= len(value) || !uppercaseHexV1(value[index+1]) || !uppercaseHexV1(value[index+2]) {
			return false
		}
		index += 2
	}
	return true
}

func uppercaseHexV1(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'A' && value <= 'F'
}

func hasURLDotSegmentV1(value string) bool {
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

func validateSortedUniqueStringsV1(field string, values []string, allowEmpty bool) error {
	if values == nil {
		return fmt.Errorf("%s must use an array", field)
	}
	for index, value := range values {
		if !allowEmpty && value == "" || strings.TrimSpace(value) != value || containsControlV1(value) || index > 0 && values[index-1] >= value {
			return fmt.Errorf("%s must contain unique sorted canonical values", field)
		}
	}
	return nil
}

func validateRecordPathV1(value string, allowDot bool) error {
	if value == "." && allowDot {
		return nil
	}
	if value == "" || containsControlV1(value) || path.IsAbs(value) || path.Clean(value) != value {
		return fmt.Errorf("path %q must be canonical and relative", value)
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("path %q contains an invalid segment", value)
		}
	}
	return nil
}

func validateAbsoluteRecordPathV1(value string) error {
	if value == "" || containsControlV1(value) || !path.IsAbs(value) || path.Clean(value) != value || value == "/" {
		return fmt.Errorf("path %q must be canonical, absolute, and non-root", value)
	}
	return nil
}
