package blueprint

import (
	"fmt"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// APTPackageRequest is the resolved form shared by scalar and structured APT
// package entries. Name and Version are never retained as an executable APT
// expression.
type APTPackageRequest struct {
	Name    string
	Version string
	Exports map[string]ExecutableExport
}

// ExecutableExport declares one singleton absolute executable candidate.
type ExecutableExport struct {
	Executable string
}

// CommandRequirement is a source-neutral logical executable requirement.
type CommandRequirement struct {
	Command  string
	Version  string
	Supplier string
}

// ParseAPTPackageRequest parses Reploy's strict name or name=exact-version
// subset. The returned fields are the only values later rendered for APT.
func ParseAPTPackageRequest(value string) (APTPackageRequest, error) {
	if value == "" || !utf8.ValidString(value) || strings.TrimSpace(value) != value || containsControl(value) {
		return APTPackageRequest{}, fmt.Errorf("APT package request must not be empty or contain surrounding whitespace or control characters")
	}
	if strings.Count(value, "=") > 1 {
		return APTPackageRequest{}, fmt.Errorf("APT package request %q must contain at most one '='", value)
	}
	name, version, pinned := strings.Cut(value, "=")
	if !validDebianPackageName(name) {
		return APTPackageRequest{}, fmt.Errorf("APT package name %q is not an exact Debian binary package name", name)
	}
	if pinned {
		if err := validateDebianVersion(version); err != nil {
			return APTPackageRequest{}, fmt.Errorf("APT package %s version: %w", name, err)
		}
	}
	return APTPackageRequest{Name: name, Version: version, Exports: map[string]ExecutableExport{}}, nil
}

// ValidateAPTPackageRequest validates a resolved request, including structured
// executable exports, without reparsing an author string as an APT expression.
func ValidateAPTPackageRequest(request APTPackageRequest) error {
	canonical := request.Name
	if request.Version != "" {
		canonical += "=" + request.Version
	}
	parsed, err := ParseAPTPackageRequest(canonical)
	if err != nil {
		return err
	}
	if parsed.Name != request.Name || parsed.Version != request.Version {
		return fmt.Errorf("APT package request fields do not match their canonical value")
	}
	for name, export := range request.Exports {
		if err := validateProviderIdentifier("APT export name", name); err != nil {
			return err
		}
		if err := validateExecutablePath("APT export "+name+" executable", export.Executable); err != nil {
			return err
		}
	}
	return nil
}

// Canonical returns the only APT root-operand spelling permitted for request.
func (request APTPackageRequest) Canonical() (string, error) {
	if err := ValidateAPTPackageRequest(request); err != nil {
		return "", err
	}
	if request.Version == "" {
		return request.Name, nil
	}
	return request.Name + "=" + request.Version, nil
}

func (requirement CommandRequirement) Validate(field string) error {
	if err := validateProviderIdentifier(field+".command", requirement.Command); err != nil {
		return err
	}
	if requirement.Version != strings.TrimSpace(requirement.Version) {
		return fmt.Errorf("%s.version must not contain surrounding whitespace", field)
	}
	if requirement.Supplier != "" {
		if err := validateProviderIdentifier(field+".supplier", requirement.Supplier); err != nil {
			return err
		}
	}
	return nil
}

func validDebianPackageName(value string) bool {
	if len(value) < 2 {
		return false
	}
	for index, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			continue
		}
		if index > 0 && (char == '+' || char == '-' || char == '.') {
			continue
		}
		return false
	}
	return true
}

func validateDebianVersion(value string) error {
	if value == "" || !utf8.ValidString(value) || strings.TrimSpace(value) != value || containsControl(value) {
		return fmt.Errorf("must be a nonempty exact Debian version")
	}
	upstreamAndRevision := value
	if epoch, remainder, found := strings.Cut(value, ":"); found {
		if epoch == "" || !allASCIIDigits(epoch) {
			return fmt.Errorf("has an invalid epoch")
		}
		upstreamAndRevision = remainder
	}
	upstream := upstreamAndRevision
	revision := ""
	if index := strings.LastIndexByte(upstreamAndRevision, '-'); index >= 0 {
		upstream = upstreamAndRevision[:index]
		revision = upstreamAndRevision[index+1:]
		if revision == "" {
			return fmt.Errorf("has an empty Debian revision")
		}
	}
	if upstream == "" || upstream[0] < '0' || upstream[0] > '9' {
		return fmt.Errorf("upstream version must begin with a digit")
	}
	for _, char := range upstream {
		if isASCIIAlphanumeric(char) || char == '.' || char == '+' || char == '~' || char == '-' || char == ':' {
			continue
		}
		return fmt.Errorf("upstream version contains invalid character %q", char)
	}
	for _, char := range revision {
		if isASCIIAlphanumeric(char) || char == '.' || char == '+' || char == '~' {
			continue
		}
		return fmt.Errorf("Debian revision contains invalid character %q", char)
	}
	return nil
}

func validateExecutablePath(field string, value string) error {
	if value == "" || !utf8.ValidString(value) || strings.TrimSpace(value) != value || containsControl(value) || strings.Contains(value, `\`) {
		return fmt.Errorf("%s must be a normalized absolute Linux path", field)
	}
	if !path.IsAbs(value) || path.Clean(value) != value || value == "/" {
		return fmt.Errorf("%s must be a normalized absolute Linux path", field)
	}
	return nil
}

func containsControl(value string) bool {
	for _, char := range value {
		if unicode.IsControl(char) {
			return true
		}
	}
	return false
}

func allASCIIDigits(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func isASCIIAlphanumeric(char rune) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9'
}
