package python

import (
	"strconv"
	"strings"
)

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

	actual, ok := parseReleaseVersion(version)
	if !ok {
		return false, false
	}
	for _, raw := range strings.Split(remainder, ",") {
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
