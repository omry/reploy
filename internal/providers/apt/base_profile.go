package apt

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/omry/reploy/internal/blueprint"
)

const (
	BaseProfileSchemaV1       = "apt-base-profile-evidence-v1"
	DebianUbuntuBaseProfileV1 = "debian-ubuntu-apt-v1"
	ResolverScratchDirectory  = "/tmp/reploy-apt-resolve"
)

// OSReleaseFieldV1 preserves one exact /etc/os-release field as evidence.
// Profile selection uses only ID, ID_LIKE, and VERSION_ID.
type OSReleaseFieldV1 struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// RequiredToolEvidenceV1 identifies one fixed absolute executable interface.
// Executable link, file, and digest evidence is collected separately against
// the exact prefix image.
type RequiredToolEvidenceV1 struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Interface string `json:"interface"`
	Version   string `json:"version"`
}

// BaseProfileEvidenceV1 is the version-independent acceptance evidence for an
// APT-capable base. Distribution and tool versions are diagnostic evidence;
// they are deliberately not an allowlist.
type BaseProfileEvidenceV1 struct {
	Schema               string                   `json:"schema"`
	Profile              string                   `json:"profile"`
	OSRelease            []OSReleaseFieldV1       `json:"os_release"`
	MatchedBy            string                   `json:"matched_by"`
	Tools                []RequiredToolEvidenceV1 `json:"tools"`
	Platform             blueprint.Platform       `json:"platform"`
	NativeArchitecture   string                   `json:"native_architecture"`
	ForeignArchitectures []string                 `json:"foreign_architectures"`
}

var requiredBaseToolsV1 = []RequiredToolEvidenceV1{
	{Name: "apt_get", Path: "/usr/bin/apt-get", Interface: "apt-get-v1"},
	{Name: "dpkg", Path: "/usr/bin/dpkg", Interface: "dpkg-v1"},
	{Name: "dpkg_deb", Path: "/usr/bin/dpkg-deb", Interface: "dpkg-deb-v1"},
	{Name: "dpkg_query", Path: "/usr/bin/dpkg-query", Interface: "dpkg-query-v1"},
	{Name: "env", Path: "/usr/bin/env", Interface: "env-i-v1"},
	{Name: "sh", Path: "/bin/sh", Interface: "posix-sh-v1"},
}

// RequiredBaseToolsV1 returns the fixed command interfaces that a resolver or
// materializer must observe before using an APT-capable prefix.
func RequiredBaseToolsV1() []RequiredToolEvidenceV1 {
	return append([]RequiredToolEvidenceV1{}, requiredBaseToolsV1...)
}

// DebianArchitectureForPlatformV1 maps one supported OCI platform to the
// single native Debian architecture accepted by the initial provider.
func DebianArchitectureForPlatformV1(platform blueprint.Platform) (string, error) {
	if err := platform.Validate(); err != nil {
		return "", fmt.Errorf("APT platform: %w", err)
	}
	if platform.OS != "linux" {
		return "", fmt.Errorf("APT provider does not support platform %q", platform.Canonical)
	}
	switch platform.Architecture {
	case "amd64":
		return "amd64", nil
	case "arm64":
		return "arm64", nil
	case "arm":
		if platform.Variant == "v7" {
			return "armhf", nil
		}
	}
	return "", fmt.Errorf("APT provider does not support platform %q", platform.Canonical)
}

// NewBaseProfileEvidenceV1 validates observed base facts and returns their
// canonical ordering. It does not perform the observations itself.
func NewBaseProfileEvidenceV1(
	platform blueprint.Platform,
	osRelease map[string]string,
	tools []RequiredToolEvidenceV1,
	nativeArchitecture string,
	foreignArchitectures []string,
) (BaseProfileEvidenceV1, error) {
	fields, matchedBy, err := normalizeOSReleaseV1(osRelease)
	if err != nil {
		return BaseProfileEvidenceV1{}, err
	}
	tools = append([]RequiredToolEvidenceV1{}, tools...)
	sort.Slice(tools, func(left int, right int) bool { return tools[left].Name < tools[right].Name })
	foreignArchitectures = append([]string{}, foreignArchitectures...)
	sort.Strings(foreignArchitectures)
	evidence := BaseProfileEvidenceV1{
		Schema: BaseProfileSchemaV1, Profile: DebianUbuntuBaseProfileV1,
		OSRelease: fields, MatchedBy: matchedBy, Tools: tools, Platform: platform,
		NativeArchitecture: nativeArchitecture, ForeignArchitectures: foreignArchitectures,
	}
	if err := ValidateBaseProfileEvidenceV1(evidence); err != nil {
		return BaseProfileEvidenceV1{}, err
	}
	return evidence, nil
}

func ValidateBaseProfileEvidenceV1(evidence BaseProfileEvidenceV1) error {
	if evidence.Schema != BaseProfileSchemaV1 || evidence.Profile != DebianUbuntuBaseProfileV1 {
		return fmt.Errorf("APT base evidence must use schema %q and profile %q", BaseProfileSchemaV1, DebianUbuntuBaseProfileV1)
	}
	osRelease := make(map[string]string, len(evidence.OSRelease))
	for index, field := range evidence.OSRelease {
		if !validOSReleaseNameV1(field.Name) || !validOSReleaseValueV1(field.Value) {
			return fmt.Errorf("APT base OS release field %d is invalid", index)
		}
		if index > 0 && evidence.OSRelease[index-1].Name >= field.Name {
			return fmt.Errorf("APT base OS release fields must be unique and sorted")
		}
		osRelease[field.Name] = field.Value
	}
	normalized, matchedBy, err := normalizeOSReleaseV1(osRelease)
	if err != nil {
		return err
	}
	if len(normalized) != len(evidence.OSRelease) || matchedBy != evidence.MatchedBy {
		return fmt.Errorf("APT base OS release evidence is not canonical")
	}

	expectedArchitecture, err := DebianArchitectureForPlatformV1(evidence.Platform)
	if err != nil {
		return err
	}
	if evidence.NativeArchitecture != expectedArchitecture {
		return fmt.Errorf("APT base native Debian architecture %q does not match platform %q mapped architecture %q", evidence.NativeArchitecture, evidence.Platform.Canonical, expectedArchitecture)
	}
	if evidence.ForeignArchitectures == nil {
		return fmt.Errorf("APT base foreign architectures must use an array")
	}
	if len(evidence.ForeignArchitectures) != 0 {
		return fmt.Errorf("APT base has unsupported foreign Debian architecture %q", evidence.ForeignArchitectures[0])
	}
	if err := validateRequiredToolsV1(evidence.Tools); err != nil {
		return err
	}
	return nil
}

func normalizeOSReleaseV1(values map[string]string) ([]OSReleaseFieldV1, string, error) {
	if values == nil {
		return nil, "", fmt.Errorf("APT base OS release fields are required")
	}
	for name, value := range values {
		if !validOSReleaseNameV1(name) || !validOSReleaseValueV1(value) {
			return nil, "", fmt.Errorf("APT base OS release field %q is invalid", name)
		}
	}
	id, idLike, version := values["ID"], values["ID_LIKE"], values["VERSION_ID"]
	if !validEvidenceValueV1(version) {
		return nil, "", fmt.Errorf("APT base OS release VERSION_ID is required")
	}
	matchedBy := ""
	if id == "debian" || id == "ubuntu" {
		matchedBy = "id"
	} else {
		for _, token := range strings.Fields(idLike) {
			if token == "debian" || token == "ubuntu" {
				matchedBy = "id_like"
				break
			}
		}
	}
	if matchedBy == "" {
		return nil, "", fmt.Errorf("APT base OS release ID %q and ID_LIKE %q do not identify the Debian/Ubuntu family", id, idLike)
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := make([]OSReleaseFieldV1, 0, len(names))
	for _, name := range names {
		fields = append(fields, OSReleaseFieldV1{Name: name, Value: values[name]})
	}
	return fields, matchedBy, nil
}

func validateRequiredToolsV1(tools []RequiredToolEvidenceV1) error {
	if tools == nil || len(tools) != len(requiredBaseToolsV1) {
		return fmt.Errorf("APT base tool evidence must contain the exact required tool set")
	}
	for index, expected := range requiredBaseToolsV1 {
		actual := tools[index]
		if actual.Name != expected.Name || actual.Path != expected.Path || actual.Interface != expected.Interface {
			return fmt.Errorf("APT base tool evidence %d does not match required %s interface at %s", index, expected.Name, expected.Path)
		}
		if actual.Name != "env" && actual.Name != "sh" && !validEvidenceValueV1(actual.Version) {
			return fmt.Errorf("APT base tool %s version evidence is required", actual.Name)
		}
		if (actual.Name == "env" || actual.Name == "sh") && actual.Version != "" {
			return fmt.Errorf("APT base tool %s must not invent version evidence", actual.Name)
		}
	}
	return nil
}

func validOSReleaseNameV1(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < 'A' || char > 'Z' {
			if char < '0' || char > '9' {
				if char != '_' {
					return false
				}
			}
		}
	}
	return true
}

func validEvidenceValueV1(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n") && utf8.ValidString(value)
}

func validOSReleaseValueV1(value string) bool {
	return !strings.ContainsAny(value, "\x00\r\n") && utf8.ValidString(value)
}
