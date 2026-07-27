package python

import (
	"regexp"
	"strings"
)

const ProviderName = "python"

var distributionSeparatorPattern = regexp.MustCompile(`[-_.]+`)

func WheelFilenameRequirement(filename string) (string, bool) {
	if !strings.HasSuffix(filename, ".whl") {
		return "", false
	}
	base := strings.TrimSuffix(filename, ".whl")
	parts := strings.Split(base, "-")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return NormalizeDistributionName(parts[0]) + "==" + parts[1], true
}

func NormalizeDistributionName(name string) string {
	return distributionSeparatorPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
}
