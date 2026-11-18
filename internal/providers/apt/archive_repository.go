package apt

import (
	"fmt"
	"strconv"
	"strings"
)

// DebianArchiveSourcesV1 returns fixed official Debian archive sources and
// mechanical EOL status. APT must still authenticate metadata and packages and
// enforce freshness. HTTP also works in minimal bases without CA certificates.
func DebianArchiveSourcesV1(evidence BaseProfileEvidenceV1) (suite, sources string, eol bool, err error) {
	if err := ValidateBaseProfileEvidenceV1(evidence); err != nil {
		return "", "", false, err
	}
	values := make(map[string]string, len(evidence.OSRelease))
	for _, field := range evidence.OSRelease {
		values[field.Name] = field.Value
	}
	if values["ID"] != "debian" {
		return "", "", false, nil
	}
	major, err := debianMajorVersionV1(values["VERSION_ID"])
	if err != nil {
		return "", "", false, nil
	}
	suite = map[int]string{
		6: "squeeze", 7: "wheezy", 8: "jessie", 9: "stretch",
		10: "buster", 11: "bullseye", 12: "bookworm", 13: "trixie",
	}[major]
	if suite == "" {
		suite = values["VERSION_CODENAME"]
	}
	if !validArchiveSuiteV1(suite) {
		return "", "", false, nil
	}
	const archive = "http://archive.debian.org/"
	const components = " main contrib non-free\n"
	sources = "deb " + archive + "debian " + suite + components
	switch suite {
	case "squeeze", "wheezy", "jessie", "stretch":
		// These releases' separate updates suites were not retained.
	default:
		sources += "deb " + archive + "debian " + suite + "-updates" + components
	}
	securitySuite := suite + "-security"
	switch suite {
	case "squeeze", "wheezy", "jessie", "stretch", "buster":
		securitySuite = suite + "/updates"
	}
	sources += "deb " + archive + "debian-security " + securitySuite + components
	return suite, sources, major <= 11, nil
}

func debianMajorVersionV1(version string) (int, error) {
	majorToken, _, _ := strings.Cut(version, ".")
	if majorToken == "" {
		return 0, fmt.Errorf("Debian VERSION_ID is missing a major version")
	}
	for _, character := range majorToken {
		if character < '0' || character > '9' {
			return 0, fmt.Errorf("Debian VERSION_ID major version is invalid")
		}
	}
	major, err := strconv.Atoi(majorToken)
	if err != nil || major < 1 {
		return 0, fmt.Errorf("Debian VERSION_ID major version is invalid")
	}
	return major, nil
}

func validArchiveSuiteV1(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') &&
			(index == 0 || character != '+' && character != '.' && character != '-') {
			return false
		}
	}
	return true
}
