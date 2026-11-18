package apt

import (
	"fmt"
	"strconv"
	"strings"
)

// ArchiveRepositoryV1 is the authenticated source selected after a Debian
// repository failure. The caller must still let APT verify Release/InRelease signatures,
// checksums, dependencies, architecture, and Valid-Until freshness.
type ArchiveRepositoryV1 struct {
	URL        string
	Suite      string
	Components []string
	EOL        bool
}

// DebianArchiveRepositoryV1 returns the official Debian archive source for a
// suite that can be mechanically identified. It is used after a configured
// repository failure or mechanical EOL identification; APT still authenticates the archive
// metadata and enforces freshness. The official HTTP archive works without a
// CA bootstrap in a minimal base; APT's Release signatures, package checksums
// and freshness policy remain authoritative.
func DebianArchiveRepositoryV1(evidence BaseProfileEvidenceV1) (ArchiveRepositoryV1, bool, error) {
	if err := ValidateBaseProfileEvidenceV1(evidence); err != nil {
		return ArchiveRepositoryV1{}, false, err
	}
	values := make(map[string]string, len(evidence.OSRelease))
	for _, field := range evidence.OSRelease {
		values[field.Name] = field.Value
	}
	if values["ID"] != "debian" {
		return ArchiveRepositoryV1{}, false, nil
	}
	major, err := debianMajorVersionV1(values["VERSION_ID"])
	if err != nil {
		return ArchiveRepositoryV1{}, false, nil
	}
	suiteByMajor := map[int]string{
		6: "squeeze", 7: "wheezy", 8: "jessie", 9: "stretch",
		10: "buster", 11: "bullseye", 12: "bookworm", 13: "trixie",
	}
	suite, known := suiteByMajor[major]
	if !known {
		suite = values["VERSION_CODENAME"]
	}
	if !validArchiveSuiteV1(suite) {
		return ArchiveRepositoryV1{}, false, nil
	}
	return ArchiveRepositoryV1{
		URL: "http://archive.debian.org/debian", Suite: suite,
		Components: []string{"main", "contrib", "non-free"}, EOL: major <= 11,
	}, true, nil
}

// SourceListV1 preserves the release's archived update and security indexes.
// Separate -updates suites are retained for Debian 10 and later; older
// releases have no such suite in the official archive.
func (repository ArchiveRepositoryV1) SourceListV1() (string, error) {
	if repository.URL != "http://archive.debian.org/debian" {
		return "", fmt.Errorf("unsupported Debian archive URL %q", repository.URL)
	}
	if !validArchiveSuiteV1(repository.Suite) {
		return "", fmt.Errorf("Debian archive suite is invalid")
	}
	if len(repository.Components) == 0 {
		return "", fmt.Errorf("Debian archive components are required")
	}
	seen := make(map[string]struct{}, len(repository.Components))
	for _, component := range repository.Components {
		if component == "" || strings.TrimSpace(component) != component || strings.ContainsAny(component, "\r\n\t ") {
			return "", fmt.Errorf("Debian archive component %q is invalid", component)
		}
		if _, duplicate := seen[component]; duplicate {
			return "", fmt.Errorf("Debian archive components must be unique")
		}
		seen[component] = struct{}{}
	}
	components := strings.Join(repository.Components, " ")
	var sources strings.Builder
	fmt.Fprintf(&sources, "deb %s %s %s\n", repository.URL, repository.Suite, components)
	switch repository.Suite {
	case "squeeze", "wheezy", "jessie", "stretch":
		// These releases' separate updates suites were not retained.
	default:
		fmt.Fprintf(&sources, "deb %s %s-updates %s\n", repository.URL, repository.Suite, components)
	}
	securitySuite := repository.Suite + "-security"
	switch repository.Suite {
	case "squeeze", "wheezy", "jessie", "stretch", "buster":
		securitySuite = repository.Suite + "/updates"
	}
	fmt.Fprintf(&sources, "deb http://archive.debian.org/debian-security %s %s\n", securitySuite, components)
	return sources.String(), nil
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
