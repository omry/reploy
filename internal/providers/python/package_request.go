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
// external location or runtime state. Extras are rejected for the same reason:
// a root that selects optional dependency groups is not an exact, immutable
// coordinate.
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
		return "", fmt.Errorf("Python package root requirement %q must not request extras", requirement)
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

type packageRootRequirementBoundV1 struct {
	version   []int
	inclusive bool
	set       bool
}

type packageRootRequirementIntervalV1 struct {
	lower packageRootRequirementBoundV1
	upper packageRootRequirementBoundV1
}

// PackageRootRequirementsCompatibleV1 reports whether the ordinary release
// constraints for one direct distribution have a nonempty intersection.
// Complex PEP 440 forms remain resolver authority and are ignored here, so the
// local check rejects only conjunctions it can prove unsatisfiable.
func PackageRootRequirementsCompatibleV1(requirements []string) (bool, error) {
	interval := packageRootRequirementIntervalV1{
		lower: packageRootRequirementBoundV1{version: []int{0}, inclusive: true, set: true},
	}
	distribution := ""
	excludedExact := make([][]int, 0)
	excludedPrefixes := make([]packageRootRequirementIntervalV1, 0)
	for _, requirement := range requirements {
		currentDistribution, err := PackageRootDistributionNameV1(requirement)
		if err != nil {
			return false, err
		}
		if distribution == "" {
			distribution = currentDistribution
		} else if currentDistribution != distribution {
			return false, fmt.Errorf("Python package root requirements name different distributions %q and %q",
				distribution, currentDistribution)
		}
		name := requirementNamePattern.FindString(requirement)
		remainder := strings.TrimPrefix(requirement, name)
		for _, raw := range strings.Split(remainder, ",") {
			if raw == "" {
				continue
			}
			operator, expectedText, ok := splitVersionSpecifier(raw)
			if !ok || operator == "===" {
				continue
			}
			if strings.Contains(expectedText, "+") {
				continue
			}
			wildcard := strings.HasSuffix(expectedText, ".*")
			if wildcard {
				expectedText = strings.TrimSuffix(expectedText, ".*")
			}
			expected, ok := parseReleaseVersion(expectedText)
			if !ok {
				continue
			}
			switch operator {
			case "==":
				if wildcard {
					if !constrainPackageRootPrefixV1(&interval, expected) {
						continue
					}
				} else {
					constrainPackageRootLowerV1(&interval, expected, true)
					constrainPackageRootUpperV1(&interval, expected, true)
				}
			case "!=":
				if wildcard {
					upper, ok := nextPackageRootPrefixV1(expected)
					if ok {
						excludedPrefixes = append(excludedPrefixes, packageRootRequirementIntervalV1{
							lower: packageRootRequirementBoundV1{version: expected, inclusive: true, set: true},
							upper: packageRootRequirementBoundV1{version: upper, inclusive: false, set: true},
						})
					}
				} else {
					excludedExact = append(excludedExact, expected)
				}
			case ">=":
				constrainPackageRootLowerV1(&interval, expected, true)
			case ">":
				constrainPackageRootLowerV1(&interval, expected, false)
			case "<=":
				constrainPackageRootUpperV1(&interval, expected, true)
			case "<":
				constrainPackageRootUpperV1(&interval, expected, false)
			case "~=":
				if len(expected) < 2 {
					continue
				}
				constrainPackageRootLowerV1(&interval, expected, true)
				constrainPackageRootPrefixV1(&interval, expected[:len(expected)-1])
			}
		}
	}
	if packageRootIntervalEmptyV1(interval) {
		return false, nil
	}
	if packageRootIntervalSingletonV1(interval) {
		for _, excluded := range excludedExact {
			if compareReleaseVersions(interval.lower.version, excluded) == 0 {
				return false, nil
			}
		}
		for _, excluded := range excludedPrefixes {
			if packageRootVersionInIntervalV1(interval.lower.version, excluded) {
				return false, nil
			}
		}
		return true, nil
	}
	return !packageRootIntervalCoveredV1(interval, excludedPrefixes), nil
}

func constrainPackageRootLowerV1(interval *packageRootRequirementIntervalV1,
	version []int, inclusive bool) {
	comparison := 1
	if interval.lower.set {
		comparison = compareReleaseVersions(version, interval.lower.version)
	}
	if !interval.lower.set || comparison > 0 {
		interval.lower = packageRootRequirementBoundV1{version: version, inclusive: inclusive, set: true}
	} else if comparison == 0 {
		interval.lower.inclusive = interval.lower.inclusive && inclusive
	}
}

func constrainPackageRootUpperV1(interval *packageRootRequirementIntervalV1,
	version []int, inclusive bool) {
	comparison := -1
	if interval.upper.set {
		comparison = compareReleaseVersions(version, interval.upper.version)
	}
	if !interval.upper.set || comparison < 0 {
		interval.upper = packageRootRequirementBoundV1{version: version, inclusive: inclusive, set: true}
	} else if comparison == 0 {
		interval.upper.inclusive = interval.upper.inclusive && inclusive
	}
}

func constrainPackageRootPrefixV1(interval *packageRootRequirementIntervalV1, prefix []int) bool {
	upper, ok := nextPackageRootPrefixV1(prefix)
	if !ok {
		return false
	}
	constrainPackageRootLowerV1(interval, prefix, true)
	constrainPackageRootUpperV1(interval, upper, false)
	return true
}

func nextPackageRootPrefixV1(prefix []int) ([]int, bool) {
	if len(prefix) == 0 || prefix[len(prefix)-1] == int(^uint(0)>>1) {
		return nil, false
	}
	next := append([]int{}, prefix...)
	next[len(next)-1]++
	return next, true
}

func packageRootIntervalEmptyV1(interval packageRootRequirementIntervalV1) bool {
	if !interval.upper.set {
		return false
	}
	comparison := compareReleaseVersions(interval.lower.version, interval.upper.version)
	return comparison > 0 || comparison == 0 && (!interval.lower.inclusive || !interval.upper.inclusive)
}

func packageRootIntervalSingletonV1(interval packageRootRequirementIntervalV1) bool {
	return interval.upper.set && interval.lower.inclusive && interval.upper.inclusive &&
		compareReleaseVersions(interval.lower.version, interval.upper.version) == 0
}

func packageRootVersionInIntervalV1(version []int, interval packageRootRequirementIntervalV1) bool {
	lower := compareReleaseVersions(version, interval.lower.version)
	upper := compareReleaseVersions(version, interval.upper.version)
	return (lower > 0 || lower == 0 && interval.lower.inclusive) &&
		(upper < 0 || upper == 0 && interval.upper.inclusive)
}

func packageRootIntervalCoveredV1(allowed packageRootRequirementIntervalV1,
	excluded []packageRootRequirementIntervalV1) bool {
	if !allowed.upper.set || len(excluded) == 0 {
		return false
	}
	ordered := append([]packageRootRequirementIntervalV1{}, excluded...)
	sort.Slice(ordered, func(left int, right int) bool {
		comparison := compareReleaseVersions(ordered[left].lower.version, ordered[right].lower.version)
		if comparison != 0 {
			return comparison < 0
		}
		return compareReleaseVersions(ordered[left].upper.version, ordered[right].upper.version) > 0
	})
	var coveredUntil []int
	for _, current := range ordered {
		if compareReleaseVersions(current.upper.version, allowed.lower.version) <= 0 {
			continue
		}
		if coveredUntil == nil {
			if compareReleaseVersions(current.lower.version, allowed.lower.version) > 0 {
				return false
			}
			coveredUntil = current.upper.version
		} else {
			if compareReleaseVersions(current.lower.version, coveredUntil) > 0 {
				return false
			}
			if compareReleaseVersions(current.upper.version, coveredUntil) > 0 {
				coveredUntil = current.upper.version
			}
		}
		comparison := compareReleaseVersions(coveredUntil, allowed.upper.version)
		if comparison > 0 || comparison == 0 && !allowed.upper.inclusive {
			return true
		}
	}
	return false
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
