package python

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	pep440 "github.com/aquasecurity/go-pep440-version"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

const PackageRequestSchemaV1 = "python-package-request-v1"

func CanonicalPackageRequestV1(requirement string) (providers.CanonicalPackageRequest, error) {
	requirement = strings.TrimSpace(requirement)
	if requirement == "" || !utf8.ValidString(requirement) {
		return providers.CanonicalPackageRequest{}, fmt.Errorf("Python package requirement must be nonempty valid UTF-8")
	}
	if strings.HasPrefix(requirement, "-") {
		return providers.CanonicalPackageRequest{}, fmt.Errorf("Python package requirement must not be a package-manager option")
	}
	for _, char := range requirement {
		if unicode.IsControl(char) {
			return providers.CanonicalPackageRequest{}, fmt.Errorf("Python package requirement must not contain control characters")
		}
	}
	return providers.CanonicalPackageRequest{
		Schema: PackageRequestSchemaV1,
		Value:  canonical.Object{"requirement": requirement},
	}, nil
}

func ValidateCanonicalPackageRequestV1(request providers.CanonicalPackageRequest) error {
	if request.Schema != PackageRequestSchemaV1 {
		return fmt.Errorf("Python package request schema must be %q", PackageRequestSchemaV1)
	}
	if len(request.Value) != 1 {
		return fmt.Errorf("Python package request must contain exactly requirement")
	}
	requirement, ok := request.Value["requirement"].(string)
	if !ok {
		return fmt.Errorf("Python package request requirement must be a string")
	}
	normalized, err := CanonicalPackageRequestV1(requirement)
	if err != nil {
		return err
	}
	actual, err := providers.CanonicalPackageRequestBytes(request)
	if err != nil {
		return err
	}
	expected, err := providers.CanonicalPackageRequestBytes(normalized)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, expected) {
		return fmt.Errorf("Python package request is not canonically normalized")
	}
	return nil
}

// PackageRootDistributionNameV1 validates the complete, resolver-supported
// grammar for a direct distribution root and returns its normalized
// distribution name. Catalog records intentionally exclude direct URLs and
// environment markers because those would make an immutable root depend on
// external location or runtime state. Extras must be canonical, matching the
// sorted unique rule every other record collection follows, so that one
// dependency cannot be spelled two ways with two different record digests.
func PackageRootDistributionNameV1(requirement string) (string, error) {
	request, err := CanonicalPackageRequestV1(requirement)
	if err != nil {
		return "", err
	}
	value := request.Value["requirement"].(string)
	if strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return "", fmt.Errorf("Python package root requirement must not contain whitespace")
	}
	name := requirementNamePattern.FindString(value)
	if !validPackageRequirementIdentifierV1(name) {
		return "", fmt.Errorf("invalid Python package root requirement %q", requirement)
	}
	remainder := strings.TrimPrefix(value, name)
	if strings.HasPrefix(remainder, "[") {
		end := strings.IndexByte(remainder, ']')
		if end < 0 {
			return "", fmt.Errorf("invalid Python package root requirement %q", requirement)
		}
		extras := strings.Split(remainder[1:end], ",")
		if len(extras) == 0 {
			return "", fmt.Errorf("invalid Python package root requirement %q", requirement)
		}
		for index, extra := range extras {
			if !validPackageRequirementIdentifierV1(extra) {
				return "", fmt.Errorf("invalid Python package root requirement %q", requirement)
			}
			if index > 0 && extras[index-1] >= extra {
				return "", fmt.Errorf("Python package root requirement %q must list unique sorted extras", requirement)
			}
		}
		remainder = remainder[end+1:]
	}
	if remainder == "" {
		return NormalizeDistributionName(name), nil
	}
	specifiers, err := pep440.NewSpecifiers(remainder)
	if err != nil || specifiers.String() != remainder {
		return "", fmt.Errorf("invalid Python package root requirement %q", requirement)
	}
	return NormalizeDistributionName(name), nil
}

func validPackageRequirementIdentifierV1(value string) bool {
	if value == "" || !asciiAlphaNumericV1(value[0]) || !asciiAlphaNumericV1(value[len(value)-1]) {
		return false
	}
	for _, character := range value {
		if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func asciiAlphaNumericV1(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

// ProviderRequestDistributionsV1 returns the normalized direct distribution
// roots in one canonical Python provider request. It does not evaluate or
// resolve dependencies.
func ProviderRequestDistributionsV1(request providers.CanonicalProviderRequest) ([]string, error) {
	decoded, err := decodeCanonicalProviderRequestV1(request)
	if err != nil {
		return nil, err
	}
	unique := make(map[string]struct{}, len(decoded.Requirements))
	for _, requirement := range decoded.Requirements {
		value, ok := requirement.Value["requirement"].(string)
		if !ok {
			return nil, fmt.Errorf("Python package request requirement must be a string")
		}
		distribution, err := pythonRequirementName(value)
		if err != nil {
			return nil, err
		}
		unique[distribution] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for distribution := range unique {
		result = append(result, distribution)
	}
	sort.Strings(result)
	return result, nil
}

// FilterProviderRequestOverridesV1 returns the same request with override
// intent restricted to distributions in the completed component closure.
func FilterProviderRequestOverridesV1(
	request providers.CanonicalProviderRequest,
	distributions []string,
) (providers.CanonicalProviderRequest, error) {
	decoded, err := decodeCanonicalProviderRequestV1(request)
	if err != nil {
		return providers.CanonicalProviderRequest{}, err
	}
	if distributions == nil {
		return providers.CanonicalProviderRequest{}, fmt.Errorf("Python closure distributions must use an array")
	}
	closure := make(map[string]struct{}, len(distributions))
	for index, distribution := range distributions {
		if distribution == "" || NormalizeDistributionName(distribution) != distribution {
			return providers.CanonicalProviderRequest{}, fmt.Errorf(
				"Python closure distribution %d is not normalized: %q", index, distribution,
			)
		}
		if index > 0 && distributions[index-1] >= distribution {
			return providers.CanonicalProviderRequest{}, fmt.Errorf("Python closure distributions must be unique and sorted")
		}
		closure[distribution] = struct{}{}
	}
	filtered := make([]PythonPackageOverrideV1, 0, len(decoded.Overrides))
	for _, override := range decoded.Overrides {
		if _, relevant := closure[override.Distribution]; relevant {
			filtered = append(filtered, override)
		}
	}
	decoded.Overrides = filtered
	return CanonicalProviderRequestV1(decoded)
}
