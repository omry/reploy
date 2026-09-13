package python

import (
	"fmt"
	"strconv"
	"strings"

	pep440 "github.com/aquasecurity/go-pep440-version"
	"github.com/omry/reploy/internal/portabletool"
	providerapi "github.com/omry/reploy/internal/providers"
)

// ValidatePackageVersionV1 accepts one exact Python distribution version.
// Parsing it here keeps provider-owned version syntax out of pip's constraint
// grammar and gives every caller the same PEP 440 contract.
func ValidatePackageVersionV1(value string) error {
	if value == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("Python package version must be nonempty PEP 440 text without surrounding whitespace")
	}
	if _, err := pep440.Parse(value); err != nil {
		return fmt.Errorf("Python package version %q is not valid PEP 440: %w", value, err)
	}
	return nil
}

// ValidateInterpreterVersionV1 accepts the canonical major.minor or
// major.minor.patch release form used by portable binding compatibility lists.
func ValidateInterpreterVersionV1(value string) error {
	return portabletool.ValidatePythonInterpreterVersionV1(value)
}

func parseSupportedPythonClaimV1(value string) (providerapi.SupportedPythonClaimV1, error) {
	return providerapi.ParseSupportedPythonClaimV1(value)
}

func NormalizeSupportedPythonClaimsV1(values []string) ([]string, error) {
	return providerapi.NormalizeSupportedPythonClaimsV1(values)
}

func IntersectSupportedPythonClaimsV1(left, right []string) ([]string, error) {
	return providerapi.IntersectSupportedPythonClaimsV1(left, right)
}

// ComparePackageVersionsV1 compares valid PEP 440 versions.
func ComparePackageVersionsV1(left string, right string) (int, error) {
	leftVersion, err := pep440.Parse(left)
	if err != nil {
		return 0, fmt.Errorf("parse Python package version %q: %w", left, err)
	}
	rightVersion, err := pep440.Parse(right)
	if err != nil {
		return 0, fmt.Errorf("parse Python package version %q: %w", right, err)
	}
	return leftVersion.Compare(rightVersion), nil
}

// requirementAllowsVersion validates the ordinary release-number specifiers
// Reploy can establish from a built wheel without re-running dependency
// resolution. Complex PEP 440 forms remain the Python resolver's authority.
func requirementAllowsVersion(requirement string, version string) (bool, bool) {
	name := requirementNamePattern.FindString(strings.TrimSpace(requirement))
	if name == "" {
		return false, false
	}
	remainder := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(requirement), name))
	if strings.HasPrefix(remainder, "[") {
		end := strings.IndexByte(remainder, ']')
		if end < 0 {
			return false, false
		}
		remainder = strings.TrimSpace(remainder[end+1:])
	}
	if marker := strings.IndexByte(remainder, ';'); marker >= 0 {
		remainder = strings.TrimSpace(remainder[:marker])
	}
	if remainder == "" || strings.HasPrefix(remainder, "@") {
		return true, true
	}
	return packageVersionSpecifiersAllowVersion(remainder, version)
}

// packageVersionSpecifiersAllowVersion preserves the arbitrary-equality
// comparison available to package requirements even when its raw version text
// is outside the canonical public-release subset used for interpreter proofs.
func packageVersionSpecifiersAllowVersion(specifiers string, version string) (bool, bool) {
	remaining := make([]string, 0)
	for _, raw := range strings.Split(specifiers, ",") {
		specifier := strings.TrimSpace(raw)
		operator, expectedText, ok := splitVersionSpecifier(specifier)
		if !ok {
			return false, false
		}
		if operator != "===" {
			remaining = append(remaining, specifier)
			continue
		}
		if version != expectedText {
			return false, true
		}
	}
	if len(remaining) == 0 {
		return true, true
	}
	return versionSpecifiersAllowVersion(strings.Join(remaining, ","), version)
}

// InterpreterVersionSatisfies evaluates the normalized release specifiers
// accepted for a Python interpreter requirement against an observed runtime
// version.
func InterpreterVersionSatisfies(constraint string, version string) (bool, error) {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		if _, ok := parseReleaseVersion(version); !ok {
			return false, fmt.Errorf("Python interpreter version %q is not a normalized release version", version)
		}
		return true, nil
	}
	matches, supported := versionSpecifiersAllowVersion(constraint, version)
	if !supported {
		return false, fmt.Errorf("Python interpreter version constraint %q is unsupported", constraint)
	}
	return matches, nil
}

func versionSpecifiersAllowVersion(specifiers string, version string) (bool, bool) {
	constraint, ok := parsePythonReleaseConstraintV1(specifiers)
	if !ok {
		return false, false
	}
	actual, valid := parseReleaseVersion(version)
	if !valid {
		return false, false
	}
	return constraint.allows(actual, version), true
}

type pythonReleaseBoundV1 struct {
	version   []int
	inclusive bool
	set       bool
}

type pythonReleaseConstraintV1 struct {
	lower            pythonReleaseBoundV1
	upper            pythonReleaseBoundV1
	equality         []int
	equalityPrefix   bool
	strict           string
	excludedExact    [][]int
	excludedPrefixes [][]int
	unsatisfiable    bool
}

func parsePythonReleaseConstraintV1(specifiers string) (pythonReleaseConstraintV1, bool) {
	constraint := pythonReleaseConstraintV1{lower: pythonReleaseBoundV1{version: []int{0}, inclusive: true, set: true}}
	if strings.TrimSpace(specifiers) == "" {
		return constraint, true
	}
	for _, raw := range strings.Split(specifiers, ",") {
		specifier := strings.TrimSpace(raw)
		if specifier == "" {
			return pythonReleaseConstraintV1{}, false
		}
		operator, expectedText, ok := splitVersionSpecifier(specifier)
		if !ok {
			return pythonReleaseConstraintV1{}, false
		}
		if operator == "===" {
			if strings.ContainsAny(expectedText, "+-*") {
				return pythonReleaseConstraintV1{}, false
			}
			expected, valid := parseReleaseVersion(expectedText)
			if !valid {
				return pythonReleaseConstraintV1{}, false
			}
			if constraint.strict != "" && constraint.strict != expectedText {
				constraint.unsatisfiable = true
			}
			if !constrainPythonEqualityV1(&constraint, expected, false) {
				constraint.unsatisfiable = true
			}
			constraint.strict = expectedText
			continue
		}
		// Interpreter evidence is a canonical public release. Local-version
		// labels cannot be compared faithfully after projecting to that domain,
		// so keep those specifiers outside the normalized release subset.
		if strings.Contains(expectedText, "+") {
			return pythonReleaseConstraintV1{}, false
		}
		wildcard := strings.HasSuffix(expectedText, ".*")
		if wildcard {
			if operator != "==" && operator != "!=" {
				return pythonReleaseConstraintV1{}, false
			}
			expectedText = strings.TrimSuffix(expectedText, ".*")
		}
		expected, ok := parseReleaseVersion(expectedText)
		if !ok || len(expected) == 0 {
			return pythonReleaseConstraintV1{}, false
		}
		switch operator {
		case "==":
			if !constrainPythonEqualityV1(&constraint, expected, wildcard) {
				constraint.unsatisfiable = true
			}
		case "!=":
			if wildcard {
				constraint.excludedPrefixes = append(constraint.excludedPrefixes, expected)
			} else {
				constraint.excludedExact = append(constraint.excludedExact, expected)
			}
		case ">=":
			constrainPythonLowerV1(&constraint, expected, true)
		case ">":
			constrainPythonLowerV1(&constraint, expected, false)
		case "<=":
			constrainPythonUpperV1(&constraint, expected, true)
		case "<":
			constrainPythonUpperV1(&constraint, expected, false)
		case "~=":
			if len(expected) < 2 {
				return pythonReleaseConstraintV1{}, false
			}
			constrainPythonLowerV1(&constraint, expected, true)
			prefix := expected[:len(expected)-1]
			if !constrainPythonPrefixV1(&constraint, prefix) {
				return pythonReleaseConstraintV1{}, false
			}
		default:
			return pythonReleaseConstraintV1{}, false
		}
	}
	constraint.unsatisfiable = constraint.unsatisfiable || pythonConstraintIntervalEmptyV1(constraint)
	return constraint, true
}

func constrainPythonEqualityV1(constraint *pythonReleaseConstraintV1, expected []int, prefix bool) bool {
	if constraint.equality == nil {
		constraint.equality = append([]int{}, expected...)
		constraint.equalityPrefix = prefix
		return true
	}

	current := constraint.equality
	switch {
	case !constraint.equalityPrefix && !prefix:
		return compareReleaseVersions(current, expected) == 0
	case !constraint.equalityPrefix && prefix:
		return pythonExactReleaseMatchesPrefixV1(current, expected)
	case constraint.equalityPrefix && !prefix:
		if !pythonExactReleaseMatchesPrefixV1(expected, current) {
			return false
		}
		constraint.equality = append([]int{}, expected...)
		constraint.equalityPrefix = false
		return true
	case releaseHasLiteralPrefixV1(current, expected):
		return true
	case releaseHasLiteralPrefixV1(expected, current):
		constraint.equality = append([]int{}, expected...)
		return true
	default:
		return false
	}
}

func pythonExactReleaseMatchesPrefixV1(exact, prefix []int) bool {
	for index := 3; index < len(exact); index++ {
		if exact[index] != 0 {
			return false
		}
	}
	canonical := []int{
		pythonReleaseComponentV1(exact, 0),
		pythonReleaseComponentV1(exact, 1),
		pythonReleaseComponentV1(exact, 2),
	}
	return releaseHasPrefix(canonical, prefix)
}

func constrainPythonLowerV1(constraint *pythonReleaseConstraintV1, version []int, inclusive bool) {
	if !constraint.lower.set || compareReleaseVersions(version, constraint.lower.version) > 0 {
		constraint.lower = pythonReleaseBoundV1{version: append([]int{}, version...), inclusive: inclusive, set: true}
	} else if compareReleaseVersions(version, constraint.lower.version) == 0 {
		constraint.lower.inclusive = constraint.lower.inclusive && inclusive
	}
}

func constrainPythonUpperV1(constraint *pythonReleaseConstraintV1, version []int, inclusive bool) {
	if !constraint.upper.set || compareReleaseVersions(version, constraint.upper.version) < 0 {
		constraint.upper = pythonReleaseBoundV1{version: append([]int{}, version...), inclusive: inclusive, set: true}
	} else if compareReleaseVersions(version, constraint.upper.version) == 0 {
		constraint.upper.inclusive = constraint.upper.inclusive && inclusive
	}
}

func constrainPythonPrefixV1(constraint *pythonReleaseConstraintV1, prefix []int) bool {
	next, ok := nextReleasePrefixV1(prefix)
	if !ok {
		return false
	}
	constrainPythonLowerV1(constraint, prefix, true)
	constrainPythonUpperV1(constraint, next, false)
	return true
}

func nextReleasePrefixV1(prefix []int) ([]int, bool) {
	if len(prefix) == 0 || prefix[len(prefix)-1] == int(^uint(0)>>1) {
		return nil, false
	}
	next := append([]int{}, prefix...)
	next[len(next)-1]++
	return next, true
}

func pythonConstraintIntervalEmptyV1(constraint pythonReleaseConstraintV1) bool {
	if constraint.equality != nil {
		if constraint.equalityPrefix {
			next, ok := nextReleasePrefixV1(constraint.equality)
			if !ok {
				return true
			}
			if constraint.lower.set && compareReleaseVersions(next, constraint.lower.version) <= 0 {
				return true
			}
			if constraint.upper.set {
				comparison := compareReleaseVersions(constraint.equality, constraint.upper.version)
				if comparison > 0 || comparison == 0 && !constraint.upper.inclusive {
					return true
				}
			}
		} else {
			if constraint.lower.set && (compareReleaseVersions(constraint.equality, constraint.lower.version) < 0 || compareReleaseVersions(constraint.equality, constraint.lower.version) == 0 && !constraint.lower.inclusive) {
				return true
			}
			if constraint.upper.set && (compareReleaseVersions(constraint.equality, constraint.upper.version) > 0 || compareReleaseVersions(constraint.equality, constraint.upper.version) == 0 && !constraint.upper.inclusive) {
				return true
			}
		}
	}
	if !constraint.upper.set {
		return false
	}
	comparison := compareReleaseVersions(constraint.lower.version, constraint.upper.version)
	return comparison > 0 || comparison == 0 && (!constraint.lower.inclusive || !constraint.upper.inclusive)
}

func (constraint pythonReleaseConstraintV1) allows(actual []int, raw string) bool {
	if constraint.unsatisfiable {
		return false
	}
	if constraint.strict != "" && raw != constraint.strict {
		return false
	}
	if constraint.equality != nil {
		if constraint.equalityPrefix {
			if !releaseHasPrefix(actual, constraint.equality) {
				return false
			}
		} else if compareReleaseVersions(actual, constraint.equality) != 0 {
			return false
		}
	}
	if constraint.lower.set {
		comparison := compareReleaseVersions(actual, constraint.lower.version)
		if comparison < 0 || comparison == 0 && !constraint.lower.inclusive {
			return false
		}
	}
	if constraint.upper.set {
		comparison := compareReleaseVersions(actual, constraint.upper.version)
		if comparison > 0 || comparison == 0 && !constraint.upper.inclusive {
			return false
		}
	}
	for _, excluded := range constraint.excludedExact {
		if compareReleaseVersions(actual, excluded) == 0 {
			return false
		}
	}
	for _, excluded := range constraint.excludedPrefixes {
		if releaseHasPrefix(actual, excluded) {
			return false
		}
	}
	return true
}

func splitVersionSpecifier(value string) (string, string, bool) {
	for _, operator := range []string{"===", "~=", "==", "!=", "<=", ">=", "<", ">"} {
		if expected, ok := strings.CutPrefix(value, operator); ok {
			expected = strings.TrimSpace(expected)
			return operator, expected, expected != ""
		}
	}
	return "", "", false
}

func parseReleaseVersion(value string) ([]int, bool) {
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
		if part == "" {
			return nil, false
		}
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 {
			return nil, false
		}
		result[index] = parsed
	}
	return result, true
}

func compareReleaseVersions(left []int, right []int) int {
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

func releaseHasPrefix(version []int, prefix []int) bool {
	for index, expected := range prefix {
		if pythonReleaseComponentV1(version, index) != expected {
			return false
		}
	}
	return true
}

func releaseHasLiteralPrefixV1(version []int, prefix []int) bool {
	if len(prefix) > len(version) {
		return false
	}
	for index, expected := range prefix {
		if version[index] != expected {
			return false
		}
	}
	return true
}

type pythonReleaseIntervalV1 struct {
	lower pythonReleaseBoundV1
	upper pythonReleaseBoundV1
}

func pythonConstraintIntervalV1(constraint pythonReleaseConstraintV1) (pythonReleaseIntervalV1, bool) {
	if constraint.unsatisfiable {
		return pythonReleaseIntervalV1{}, false
	}
	interval := pythonReleaseIntervalV1{lower: constraint.lower, upper: constraint.upper}
	if constraint.equality == nil {
		return interval, !pythonIntervalEmptyV1(interval)
	}
	if constraint.equalityPrefix {
		next, ok := nextReleasePrefixV1(constraint.equality)
		if !ok {
			return pythonReleaseIntervalV1{}, false
		}
		pythonIntervalConstrainLowerV1(&interval, constraint.equality, true)
		pythonIntervalConstrainUpperV1(&interval, next, false)
	} else {
		pythonIntervalConstrainLowerV1(&interval, constraint.equality, true)
		pythonIntervalConstrainUpperV1(&interval, constraint.equality, true)
	}
	return interval, !pythonIntervalEmptyV1(interval)
}

func pythonIntervalConstrainLowerV1(interval *pythonReleaseIntervalV1, version []int, inclusive bool) {
	if !interval.lower.set || compareReleaseVersions(version, interval.lower.version) > 0 {
		interval.lower = pythonReleaseBoundV1{version: append([]int{}, version...), inclusive: inclusive, set: true}
	} else if compareReleaseVersions(version, interval.lower.version) == 0 {
		interval.lower.inclusive = interval.lower.inclusive && inclusive
	}
}

func pythonIntervalConstrainUpperV1(interval *pythonReleaseIntervalV1, version []int, inclusive bool) {
	if !interval.upper.set || compareReleaseVersions(version, interval.upper.version) < 0 {
		interval.upper = pythonReleaseBoundV1{version: append([]int{}, version...), inclusive: inclusive, set: true}
	} else if compareReleaseVersions(version, interval.upper.version) == 0 {
		interval.upper.inclusive = interval.upper.inclusive && inclusive
	}
}

func pythonIntervalEmptyV1(interval pythonReleaseIntervalV1) bool {
	if !interval.upper.set {
		return false
	}
	comparison := compareReleaseVersions(interval.lower.version, interval.upper.version)
	return comparison > 0 || comparison == 0 && (!interval.lower.inclusive || !interval.upper.inclusive)
}

// PythonRequiresPythonIntersectsClaimV1 answers the weaker, nonempty-overlap
// question used when validating an individual artifact.
func PythonRequiresPythonIntersectsClaimV1(requiresPython, claim string) (bool, error) {
	parsed, ok := parsePythonReleaseConstraintV1(strings.TrimSpace(requiresPython))
	if !ok {
		return false, fmt.Errorf("Requires-Python %q is outside the normalized release subset", requiresPython)
	}
	parsedClaim, err := parseSupportedPythonClaimV1(claim)
	if err != nil {
		return false, err
	}
	if parsedClaim.Exact {
		return parsed.allows([]int{parsedClaim.Major, parsedClaim.Minor, parsedClaim.Patch}, parsedClaim.String()), nil
	}
	if _, ok := nextReleasePrefixV1([]int{parsedClaim.Major, parsedClaim.Minor}); !ok {
		return false, fmt.Errorf("Python interpreter claim %q has no representable minor successor", claim)
	}
	return pythonConstraintHasSeriesReleaseV1(parsed, parsedClaim.Major, parsedClaim.Minor), nil
}

func pythonConstraintHasSeriesReleaseV1(constraint pythonReleaseConstraintV1, major, minor int) bool {
	if constraint.unsatisfiable {
		return false
	}
	if constraint.strict != "" {
		patch, ok := pythonCanonicalInterpreterPatchV1(constraint.equality, major, minor)
		if !ok {
			return false
		}
		candidate, raw := pythonCanonicalInterpreterReleaseV1(major, minor, patch)
		return raw == constraint.strict && constraint.allows(candidate, raw)
	}

	patch, ok := pythonMinimumSeriesPatchV1(constraint.lower, major, minor)
	if !ok {
		return false
	}
	if constraint.equality != nil {
		if constraint.equalityPrefix {
			switch len(constraint.equality) {
			case 1:
				if constraint.equality[0] != major {
					return false
				}
			case 2:
				if constraint.equality[0] != major || constraint.equality[1] != minor {
					return false
				}
			default:
				equalPatch, valid := pythonCanonicalInterpreterPatchV1(constraint.equality, major, minor)
				if !valid || equalPatch < patch {
					return false
				}
				patch = equalPatch
			}
		} else {
			equalPatch, valid := pythonCanonicalInterpreterPatchV1(constraint.equality, major, minor)
			if !valid || equalPatch < patch {
				return false
			}
			patch = equalPatch
		}
	}

	excluded, wholeSeries := pythonExcludedSeriesPatchesV1(constraint, major, minor)
	if wholeSeries {
		return false
	}
	for remaining := len(excluded); remaining >= 0; remaining-- {
		if _, blocked := excluded[patch]; !blocked {
			candidate, raw := pythonCanonicalInterpreterReleaseV1(major, minor, patch)
			return constraint.allows(candidate, raw)
		}
		if patch == int(^uint(0)>>1) {
			return false
		}
		patch++
	}
	return false
}

func pythonMinimumSeriesPatchV1(lower pythonReleaseBoundV1, major, minor int) (int, bool) {
	if !lower.set {
		return 0, true
	}
	start := []int{major, minor, 0}
	comparison := compareReleaseVersions(start, lower.version)
	if comparison > 0 {
		return 0, true
	}
	if comparison == 0 {
		if lower.inclusive {
			return 0, true
		}
		return 1, true
	}
	if pythonReleaseComponentV1(lower.version, 0) != major || pythonReleaseComponentV1(lower.version, 1) != minor {
		return 0, false
	}
	patch := pythonReleaseComponentV1(lower.version, 2)
	candidate := []int{major, minor, patch}
	comparison = compareReleaseVersions(candidate, lower.version)
	if comparison > 0 || comparison == 0 && lower.inclusive {
		return patch, true
	}
	if patch == int(^uint(0)>>1) {
		return 0, false
	}
	return patch + 1, true
}

func pythonExcludedSeriesPatchesV1(constraint pythonReleaseConstraintV1, major, minor int) (map[int]struct{}, bool) {
	excluded := make(map[int]struct{}, len(constraint.excludedExact)+len(constraint.excludedPrefixes))
	for _, release := range constraint.excludedExact {
		if patch, ok := pythonCanonicalInterpreterPatchV1(release, major, minor); ok {
			excluded[patch] = struct{}{}
		}
	}
	for _, prefix := range constraint.excludedPrefixes {
		switch len(prefix) {
		case 1:
			if prefix[0] == major {
				return nil, true
			}
		case 2:
			if prefix[0] == major && prefix[1] == minor {
				return nil, true
			}
		default:
			if patch, ok := pythonCanonicalInterpreterPatchV1(prefix, major, minor); ok {
				excluded[patch] = struct{}{}
			}
		}
	}
	return excluded, false
}

func pythonCanonicalInterpreterPatchV1(release []int, major, minor int) (int, bool) {
	if pythonReleaseComponentV1(release, 0) != major || pythonReleaseComponentV1(release, 1) != minor {
		return 0, false
	}
	for index := 3; index < len(release); index++ {
		if release[index] != 0 {
			return 0, false
		}
	}
	return pythonReleaseComponentV1(release, 2), true
}

func pythonReleaseComponentV1(release []int, index int) int {
	if index < len(release) {
		return release[index]
	}
	return 0
}

func pythonCanonicalInterpreterReleaseV1(major, minor, patch int) ([]int, string) {
	return []int{major, minor, patch}, fmt.Sprintf("%d.%d.%d", major, minor, patch)
}

// PythonRequiresPythonCoversClaimV1 proves that the canonical constraint
// admits every release in a series, or admits the exact release of a point
// claim. Series proofs are interval based and never enumerate patch numbers.
func PythonRequiresPythonCoversClaimV1(requiresPython, claim string) (bool, error) {
	parsed, ok := parsePythonReleaseConstraintV1(strings.TrimSpace(requiresPython))
	if !ok {
		return false, fmt.Errorf("Requires-Python %q is outside the normalized release subset", requiresPython)
	}
	parsedClaim, err := parseSupportedPythonClaimV1(claim)
	if err != nil {
		return false, err
	}
	if parsedClaim.Exact {
		return parsed.allows([]int{parsedClaim.Major, parsedClaim.Minor, parsedClaim.Patch}, parsedClaim.String()), nil
	}
	if parsed.strict != "" || parsed.equality != nil && !parsed.equalityPrefix {
		return false, nil
	}
	minorSuccessor, ok := nextReleasePrefixV1([]int{parsedClaim.Major, parsedClaim.Minor})
	if !ok {
		return false, fmt.Errorf("Python interpreter claim %q has no representable minor successor", claim)
	}
	claimed := pythonReleaseIntervalV1{
		lower: pythonReleaseBoundV1{version: []int{parsedClaim.Major, parsedClaim.Minor}, inclusive: true, set: true},
		upper: pythonReleaseBoundV1{version: minorSuccessor, inclusive: false, set: true},
	}
	allowed, ok := pythonConstraintIntervalV1(parsed)
	if !ok {
		return false, nil
	}
	if compareReleaseVersions(allowed.lower.version, claimed.lower.version) > 0 ||
		compareReleaseVersions(allowed.lower.version, claimed.lower.version) == 0 && !allowed.lower.inclusive {
		return false, nil
	}
	if allowed.upper.set {
		comparison := compareReleaseVersions(allowed.upper.version, claimed.upper.version)
		if comparison < 0 || comparison == 0 && claimed.upper.inclusive && !allowed.upper.inclusive {
			return false, nil
		}
	}
	excluded, wholeSeries := pythonExcludedSeriesPatchesV1(parsed, parsedClaim.Major, parsedClaim.Minor)
	if wholeSeries || len(excluded) > 0 {
		return false, nil
	}
	return true, nil
}
