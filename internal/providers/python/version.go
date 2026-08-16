package python

import (
	"fmt"
	"strconv"
	"strings"

	pep440 "github.com/aquasecurity/go-pep440-version"
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
	parts := strings.Split(value, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return fmt.Errorf("Python interpreter version %q must use major.minor or major.minor.patch", value)
	}
	for _, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
			return fmt.Errorf("Python interpreter version %q is not canonical", value)
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return fmt.Errorf("Python interpreter version %q is not canonical", value)
			}
		}
	}
	return nil
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
	return versionSpecifiersAllowVersion(remainder, version)
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
	actual, ok := parseReleaseVersion(version)
	if !ok {
		return false, false
	}
	for _, raw := range strings.Split(specifiers, ",") {
		specifier := strings.TrimSpace(raw)
		operator, expectedText, ok := splitVersionSpecifier(specifier)
		if !ok {
			return false, false
		}
		if operator == "===" {
			if version != expectedText {
				return false, true
			}
			continue
		}
		wildcard := strings.HasSuffix(expectedText, ".*")
		if wildcard {
			if operator != "==" && operator != "!=" {
				return false, false
			}
			expectedText = strings.TrimSuffix(expectedText, ".*")
		}
		expected, ok := parseReleaseVersion(expectedText)
		if !ok {
			return false, false
		}
		comparison := compareReleaseVersions(actual, expected)
		matches := false
		switch operator {
		case "==":
			if wildcard {
				matches = releaseHasPrefix(actual, expected)
			} else {
				matches = comparison == 0
			}
		case "!=":
			if wildcard {
				matches = !releaseHasPrefix(actual, expected)
			} else {
				matches = comparison != 0
			}
		case ">=":
			matches = comparison >= 0
		case "<=":
			matches = comparison <= 0
		case ">":
			matches = comparison > 0
		case "<":
			matches = comparison < 0
		case "~=":
			prefix := expected
			if len(prefix) > 1 {
				prefix = prefix[:len(prefix)-1]
			}
			matches = comparison >= 0 && releaseHasPrefix(actual, prefix)
		}
		if !matches {
			return false, true
		}
	}
	return true, true
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
	if len(prefix) > len(version) {
		return false
	}
	for index := range prefix {
		if version[index] != prefix[index] {
			return false
		}
	}
	return true
}
