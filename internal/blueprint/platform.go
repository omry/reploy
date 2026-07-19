package blueprint

import (
	"fmt"
	"sort"
	"strings"
)

// Compatibility is the canonical target-platform set declared by a blueprint.
type Compatibility struct {
	Platforms []Platform `json:"platforms"`
}

// Platform is one parsed OCI os/architecture[/variant] target.
type Platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant"`
	Canonical    string `json:"canonical"`
}

// Validate rejects a record whose canonical value was not derived from its
// parsed fields. This is used again when platform records cross a boundary.
func (platform Platform) Validate() error {
	canonical := platform.OS + "/" + platform.Architecture
	if platform.Variant != "" {
		canonical += "/" + platform.Variant
	}
	parsed, err := ParsePlatform(canonical)
	if err != nil {
		return err
	}
	if platform.Canonical != parsed.Canonical {
		return fmt.Errorf("platform canonical value %q does not match parsed fields %q", platform.Canonical, parsed.Canonical)
	}
	return nil
}

// ParsePlatform parses one canonical lowercase OCI platform string. Backend
// support is deliberately checked later, after target selection.
func ParsePlatform(value string) (Platform, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return Platform{}, fmt.Errorf("platform must be a canonical lowercase os/architecture[/variant] value")
	}
	parts := strings.Split(value, "/")
	if len(parts) < 2 || len(parts) > 3 {
		return Platform{}, fmt.Errorf("platform %q must have the form os/architecture[/variant]", value)
	}
	for _, part := range parts {
		if !isCanonicalPlatformPart(part) {
			return Platform{}, fmt.Errorf("platform %q must contain lowercase slash-free OCI fields", value)
		}
	}
	platform := Platform{OS: parts[0], Architecture: parts[1]}
	if len(parts) == 3 {
		platform.Variant = parts[2]
	}
	platform.Canonical = platform.OS + "/" + platform.Architecture
	if platform.Variant != "" {
		platform.Canonical += "/" + platform.Variant
	}
	return platform, nil
}

// ParseCompatibility parses, deduplicates, and canonically orders a required
// blueprint platform set.
func ParseCompatibility(values []string) (Compatibility, error) {
	if len(values) == 0 {
		return Compatibility{}, fmt.Errorf("blueprint.compatibility.platforms must not be empty")
	}
	platforms := make([]Platform, 0, len(values))
	seen := make(map[string]bool, len(values))
	for index, value := range values {
		platform, err := ParsePlatform(value)
		if err != nil {
			return Compatibility{}, fmt.Errorf("blueprint.compatibility.platforms[%d]: %w", index, err)
		}
		if seen[platform.Canonical] {
			return Compatibility{}, fmt.Errorf("blueprint.compatibility.platforms contains duplicate %q", platform.Canonical)
		}
		seen[platform.Canonical] = true
		platforms = append(platforms, platform)
	}
	sort.Slice(platforms, func(left int, right int) bool {
		return platforms[left].Canonical < platforms[right].Canonical
	})
	return Compatibility{Platforms: platforms}, nil
}

func isCanonicalPlatformPart(value string) bool {
	if value == "" {
		return false
	}
	for index, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			continue
		}
		if index > 0 && (char == '.' || char == '_' || char == '-') {
			continue
		}
		return false
	}
	return true
}
