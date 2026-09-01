package providers

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	pep440 "github.com/aquasecurity/go-pep440-version"
)

// PythonPackageRootDistributionNameV1 extracts the immutable direct
// distribution identity from a binding requirement.  This intentionally
// lives above the Python provider so generic plan validation and provider
// resolution share one compatibility implementation without an import cycle.
func PythonPackageRootDistributionNameV1(requirement string) (string, error) {
	requirement = strings.TrimSpace(requirement)
	if requirement == "" || !utf8.ValidString(requirement) {
		return "", fmt.Errorf("Python package requirement must be nonempty valid UTF-8")
	}
	if strings.HasPrefix(requirement, "-") {
		return "", fmt.Errorf("Python package requirement must not be a package-manager option")
	}
	for _, char := range requirement {
		if unicode.IsControl(char) {
			return "", fmt.Errorf("Python package requirement must not contain control characters")
		}
	}
	if strings.IndexFunc(requirement, unicode.IsSpace) >= 0 {
		return "", fmt.Errorf("Python package root requirement must not contain whitespace")
	}
	name := portableToolPythonRequirementNamePatternV1.FindString(requirement)
	if !portableToolPythonValidRequirementIdentifierV1(name) {
		return "", fmt.Errorf("invalid Python package root requirement %q", requirement)
	}
	remainder := strings.TrimPrefix(requirement, name)
	if strings.HasPrefix(remainder, "[") {
		return "", fmt.Errorf("Python package root requirement %q must not request extras", requirement)
	}
	if remainder != "" {
		specifiers, err := pep440.NewSpecifiers(remainder)
		if err != nil || specifiers.String() != remainder {
			return "", fmt.Errorf("invalid Python package root requirement %q", requirement)
		}
	}
	return portableToolPythonNormalizeDistributionNameV1(name), nil
}

// PythonPackageRootRequirementsCompatibleV1 reports whether ordinary release
// constraints for one direct distribution have a nonempty intersection.
// Complex PEP 440 forms remain resolver authority and are ignored here, so
// this local check rejects only conjunctions it can prove unsatisfiable.
func PythonPackageRootRequirementsCompatibleV1(requirements []string) (bool, error) {
	interval := portableToolPythonRequirementIntervalV1{
		lower: portableToolPythonRequirementBoundV1{version: []int{0}, inclusive: true, set: true},
	}
	distribution := ""
	excludedExact := make([][]int, 0)
	excludedPrefixes := make([]portableToolPythonRequirementIntervalV1, 0)
	for _, requirement := range requirements {
		currentDistribution, err := PythonPackageRootDistributionNameV1(requirement)
		if err != nil {
			return false, err
		}
		if distribution == "" {
			distribution = currentDistribution
		} else if currentDistribution != distribution {
			return false, fmt.Errorf("Python package root requirements name different distributions %q and %q",
				distribution, currentDistribution)
		}
		name := portableToolPythonRequirementNamePatternV1.FindString(requirement)
		remainder := strings.TrimPrefix(requirement, name)
		for _, raw := range strings.Split(remainder, ",") {
			if raw == "" {
				continue
			}
			operator, expectedText, ok := portableToolPythonSplitVersionSpecifierV1(raw)
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
			expected, ok := portableToolPythonParseReleaseVersionV1(expectedText)
			if !ok {
				continue
			}
			switch operator {
			case "==":
				if wildcard {
					if !portableToolPythonConstrainPrefixV1(&interval, expected) {
						continue
					}
				} else {
					portableToolPythonConstrainLowerV1(&interval, expected, true)
					portableToolPythonConstrainUpperV1(&interval, expected, true)
				}
			case "!=":
				if wildcard {
					upper, ok := portableToolPythonNextPrefixV1(expected)
					if ok {
						excludedPrefixes = append(excludedPrefixes, portableToolPythonRequirementIntervalV1{
							lower: portableToolPythonRequirementBoundV1{version: expected, inclusive: true, set: true},
							upper: portableToolPythonRequirementBoundV1{version: upper, inclusive: false, set: true},
						})
					}
				} else {
					excludedExact = append(excludedExact, expected)
				}
			case ">=":
				portableToolPythonConstrainLowerV1(&interval, expected, true)
			case ">":
				portableToolPythonConstrainLowerV1(&interval, expected, false)
			case "<=":
				portableToolPythonConstrainUpperV1(&interval, expected, true)
			case "<":
				portableToolPythonConstrainUpperV1(&interval, expected, false)
			case "~=":
				if len(expected) < 2 {
					continue
				}
				portableToolPythonConstrainLowerV1(&interval, expected, true)
				portableToolPythonConstrainPrefixV1(&interval, expected[:len(expected)-1])
			}
		}
	}
	if portableToolPythonIntervalEmptyV1(interval) {
		return false, nil
	}
	if portableToolPythonIntervalSingletonV1(interval) {
		for _, excluded := range excludedExact {
			if portableToolPythonCompareReleaseVersionsV1(interval.lower.version, excluded) == 0 {
				return false, nil
			}
		}
		for _, excluded := range excludedPrefixes {
			if portableToolPythonVersionInIntervalV1(interval.lower.version, excluded) {
				return false, nil
			}
		}
		return true, nil
	}
	return !portableToolPythonIntervalCoveredV1(interval, excludedPrefixes), nil
}

// portableToolPythonSupportedIntersectionV1 reports whether all nonempty
// supported-Python sets share at least one version. Empty sets are unconstrained
// in the same way as an omitted binding constraint.
func portableToolPythonSupportedIntersectionV1(supported [][]string) bool {
	var intersection map[string]struct{}
	for _, values := range supported {
		if len(values) == 0 {
			continue
		}
		current := make(map[string]struct{}, len(values))
		for _, value := range values {
			current[value] = struct{}{}
		}
		if intersection == nil {
			intersection = current
			continue
		}
		for value := range intersection {
			if _, exists := current[value]; !exists {
				delete(intersection, value)
			}
		}
		if len(intersection) == 0 {
			return false
		}
	}
	return true
}

type portableToolPythonRequirementBoundV1 struct {
	version   []int
	inclusive bool
	set       bool
}

type portableToolPythonRequirementIntervalV1 struct {
	lower portableToolPythonRequirementBoundV1
	upper portableToolPythonRequirementBoundV1
}

func portableToolPythonConstrainLowerV1(interval *portableToolPythonRequirementIntervalV1, version []int, inclusive bool) {
	comparison := 1
	if interval.lower.set {
		comparison = portableToolPythonCompareReleaseVersionsV1(version, interval.lower.version)
	}
	if !interval.lower.set || comparison > 0 {
		interval.lower = portableToolPythonRequirementBoundV1{version: version, inclusive: inclusive, set: true}
	} else if comparison == 0 {
		interval.lower.inclusive = interval.lower.inclusive && inclusive
	}
}

func portableToolPythonConstrainUpperV1(interval *portableToolPythonRequirementIntervalV1, version []int, inclusive bool) {
	comparison := -1
	if interval.upper.set {
		comparison = portableToolPythonCompareReleaseVersionsV1(version, interval.upper.version)
	}
	if !interval.upper.set || comparison < 0 {
		interval.upper = portableToolPythonRequirementBoundV1{version: version, inclusive: inclusive, set: true}
	} else if comparison == 0 {
		interval.upper.inclusive = interval.upper.inclusive && inclusive
	}
}

func portableToolPythonConstrainPrefixV1(interval *portableToolPythonRequirementIntervalV1, prefix []int) bool {
	upper, ok := portableToolPythonNextPrefixV1(prefix)
	if !ok {
		return false
	}
	portableToolPythonConstrainLowerV1(interval, prefix, true)
	portableToolPythonConstrainUpperV1(interval, upper, false)
	return true
}

func portableToolPythonNextPrefixV1(prefix []int) ([]int, bool) {
	if len(prefix) == 0 || prefix[len(prefix)-1] == int(^uint(0)>>1) {
		return nil, false
	}
	next := append([]int{}, prefix...)
	next[len(next)-1]++
	return next, true
}

func portableToolPythonIntervalEmptyV1(interval portableToolPythonRequirementIntervalV1) bool {
	if !interval.upper.set {
		return false
	}
	comparison := portableToolPythonCompareReleaseVersionsV1(interval.lower.version, interval.upper.version)
	return comparison > 0 || comparison == 0 && (!interval.lower.inclusive || !interval.upper.inclusive)
}

func portableToolPythonIntervalSingletonV1(interval portableToolPythonRequirementIntervalV1) bool {
	return interval.upper.set && interval.lower.inclusive && interval.upper.inclusive &&
		portableToolPythonCompareReleaseVersionsV1(interval.lower.version, interval.upper.version) == 0
}

func portableToolPythonVersionInIntervalV1(version []int, interval portableToolPythonRequirementIntervalV1) bool {
	lower := portableToolPythonCompareReleaseVersionsV1(version, interval.lower.version)
	upper := portableToolPythonCompareReleaseVersionsV1(version, interval.upper.version)
	return (lower > 0 || lower == 0 && interval.lower.inclusive) &&
		(upper < 0 || upper == 0 && interval.upper.inclusive)
}

func portableToolPythonIntervalCoveredV1(allowed portableToolPythonRequirementIntervalV1, excluded []portableToolPythonRequirementIntervalV1) bool {
	if !allowed.upper.set || len(excluded) == 0 {
		return false
	}
	ordered := append([]portableToolPythonRequirementIntervalV1{}, excluded...)
	sort.Slice(ordered, func(left int, right int) bool {
		comparison := portableToolPythonCompareReleaseVersionsV1(ordered[left].lower.version, ordered[right].lower.version)
		if comparison != 0 {
			return comparison < 0
		}
		return portableToolPythonCompareReleaseVersionsV1(ordered[left].upper.version, ordered[right].upper.version) > 0
	})
	var coveredUntil []int
	for _, current := range ordered {
		if portableToolPythonCompareReleaseVersionsV1(current.upper.version, allowed.lower.version) <= 0 {
			continue
		}
		if coveredUntil == nil {
			if portableToolPythonCompareReleaseVersionsV1(current.lower.version, allowed.lower.version) > 0 {
				return false
			}
			coveredUntil = current.upper.version
		} else {
			if portableToolPythonCompareReleaseVersionsV1(current.lower.version, coveredUntil) > 0 {
				return false
			}
			if portableToolPythonCompareReleaseVersionsV1(current.upper.version, coveredUntil) > 0 {
				coveredUntil = current.upper.version
			}
		}
		comparison := portableToolPythonCompareReleaseVersionsV1(coveredUntil, allowed.upper.version)
		if comparison > 0 || comparison == 0 && !allowed.upper.inclusive {
			return true
		}
	}
	return false
}

var portableToolPythonRequirementNamePatternV1 = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*`)

func portableToolPythonValidRequirementIdentifierV1(value string) bool {
	if value == "" || !portableToolPythonASCIIAlphaNumericV1(value[0]) || !portableToolPythonASCIIAlphaNumericV1(value[len(value)-1]) {
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

func portableToolPythonASCIIAlphaNumericV1(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func portableToolPythonNormalizeDistributionNameV1(name string) string {
	return portableToolPythonDistributionSeparatorPatternV1.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
}

var portableToolPythonDistributionSeparatorPatternV1 = regexp.MustCompile(`[-_.]+`)

func portableToolPythonSplitVersionSpecifierV1(value string) (string, string, bool) {
	for _, operator := range []string{"===", "~=", "==", "!=", "<=", ">=", "<", ">"} {
		if expected, ok := strings.CutPrefix(value, operator); ok {
			expected = strings.TrimSpace(expected)
			return operator, expected, expected != ""
		}
	}
	return "", "", false
}

func portableToolPythonParseReleaseVersionV1(value string) ([]int, bool) {
	value = strings.TrimSpace(value)
	if epoch := strings.IndexByte(value, '!'); epoch >= 0 {
		parsed, err := strconv.Atoi(value[:epoch])
		if err != nil || parsed != 0 {
			return nil, false
		}
		value = value[epoch+1:]
	}
	if local := strings.IndexByte(value, '+'); local >= 0 {
		value = value[:local]
	}
	parts := strings.Split(value, ".")
	if len(parts) == 0 {
		return nil, false
	}
	result := make([]int, len(parts))
	for index, part := range parts {
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 {
			return nil, false
		}
		result[index] = parsed
	}
	return result, true
}

func portableToolPythonCompareReleaseVersionsV1(left []int, right []int) int {
	length := len(left)
	if len(right) > length {
		length = len(right)
	}
	for index := 0; index < length; index++ {
		leftPart, rightPart := 0, 0
		if index < len(left) {
			leftPart = left[index]
		}
		if index < len(right) {
			rightPart = right[index]
		}
		if leftPart < rightPart {
			return -1
		}
		if leftPart > rightPart {
			return 1
		}
	}
	return 0
}
