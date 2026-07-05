package python

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/omry/reploy/internal/deploy"
)

const ProviderName = "python"

var packageRequirementPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*(?:\[[A-Za-z0-9_.-]+(?:,[A-Za-z0-9_.-]+)*\])?(?:==[^<>=!~\s#]+)?$`)
var pinnedRequirementPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*(?:\[[A-Za-z0-9_.-]+(?:,[A-Za-z0-9_.-]+)*\])?==[^<>=!~\s#]+$`)
var releaseLinePattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)
var distributionSeparatorPattern = regexp.MustCompile(`[-_.]+`)

type UpgradeRoot struct {
	Index      int
	Name       string
	Normalized string
}

func ValidateExplicitRequirement(requirement string) error {
	if strings.HasPrefix(requirement, "/") {
		return nil
	}
	if pinnedRequirementPattern.MatchString(requirement) {
		return nil
	}
	return fmt.Errorf("requirement must be an exact package pin or absolute container path: %s", requirement)
}

func ClassifyRoot(source string) (deploy.ArtifactRoot, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return deploy.ArtifactRoot{}, fmt.Errorf("bundle root must not be empty")
	}
	switch {
	case packageRequirementPattern.MatchString(source):
		return PackageRoot(source), nil
	case isAbsoluteRootPath(source) && strings.HasSuffix(source, ".whl"):
		return WheelRoot(source), nil
	case isAbsoluteRootPath(source):
		return SourceRoot(source), nil
	default:
		return deploy.ArtifactRoot{}, fmt.Errorf("bundle root must be a package name, exact package pin, absolute wheel path, or absolute source path: %s", source)
	}
}

func ClassifyPackRoot(source string) deploy.ArtifactRoot {
	if isAbsoluteRootPath(source) && strings.HasSuffix(source, ".whl") {
		return WheelRoot(source)
	}
	if isAbsoluteRootPath(source) {
		return SourceRoot(source)
	}
	return PackageRoot(source)
}

func isAbsoluteRootPath(source string) bool {
	return filepath.IsAbs(source) || strings.HasPrefix(source, "/")
}

func PackageRoot(source string) deploy.ArtifactRoot {
	return deploy.ArtifactRoot{Provider: ProviderName, Kind: "package", Source: source}
}

func WheelRoot(source string) deploy.ArtifactRoot {
	return deploy.ArtifactRoot{Provider: ProviderName, Kind: "wheel", Source: source}
}

func SourceRoot(source string) deploy.ArtifactRoot {
	return deploy.ArtifactRoot{Provider: ProviderName, Kind: "source", Source: source}
}

func HasPackage(roots []deploy.ArtifactRoot, packageName string) bool {
	_, ok := PackageVersion(roots, packageName)
	if ok {
		return true
	}
	for _, root := range roots {
		if root.Provider == ProviderName && root.Kind == "package" && root.Source == packageName {
			return true
		}
	}
	return false
}

func RootPackageName(root deploy.ArtifactRoot) string {
	if root.Provider != ProviderName || root.Kind != "package" {
		return ""
	}
	name, _, _ := strings.Cut(root.Source, "==")
	return name
}

func PackageVersion(roots []deploy.ArtifactRoot, packageName string) (string, bool) {
	prefix := packageName + "=="
	for _, root := range roots {
		if root.Provider != ProviderName || root.Kind != "package" {
			continue
		}
		if strings.HasPrefix(root.Source, prefix) {
			return strings.TrimPrefix(root.Source, prefix), true
		}
	}
	return "", false
}

func RootRequirements(roots []deploy.ArtifactRoot) map[string]bool {
	requirements := map[string]bool{}
	for _, root := range roots {
		if root.Provider != ProviderName {
			continue
		}
		switch root.Kind {
		case "package":
			name, version, pinned := strings.Cut(root.Source, "==")
			normalized := NormalizeRequirementName(name)
			requirements[normalized] = true
			if pinned {
				requirements[normalized+"=="+version] = true
			}
		case "wheel":
			if requirement, ok := WheelFilenameRequirement(filepath.Base(root.Source)); ok {
				requirements[requirement] = true
			}
		}
	}
	return requirements
}

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

func RejectPersistentSourceRoots(roots []deploy.ArtifactRoot, action string) error {
	for _, root := range roots {
		if root.Provider == ProviderName && root.Kind == "source" {
			return fmt.Errorf("%s does not support persistent source roots: %s; add a package or wheel artifact instead", action, root.Source)
		}
	}
	return nil
}

func BundleUpgradeInput(roots []deploy.ArtifactRoot, target string) ([]string, []UpgradeRoot, error) {
	target = strings.TrimSpace(target)
	mode := "all"
	targetNormalized := ""
	releaseUpper := ""
	if target != "" {
		switch {
		case releaseLinePattern.MatchString(target):
			mode = "release"
			releaseUpper = releaseLineUpperBound(target)
		case strings.Contains(target, "=="):
			if !pinnedRequirementPattern.MatchString(target) {
				return nil, nil, fmt.Errorf("upgrade target must be a package name, exact package pin, or release line: %s", target)
			}
			mode = "exact"
			name, _, _ := strings.Cut(target, "==")
			targetNormalized = NormalizeRequirementName(name)
		default:
			if !packageRequirementPattern.MatchString(target) {
				return nil, nil, fmt.Errorf("upgrade target must be a package name, exact package pin, or release line: %s", target)
			}
			mode = "package"
			targetNormalized = NormalizeRequirementName(target)
		}
	}

	input := []string{}
	upgradeRoots := []UpgradeRoot{}
	foundTarget := false
	for index, root := range roots {
		if root.Provider != ProviderName || root.Kind != "package" {
			return nil, nil, fmt.Errorf("bundle upgrade only supports package roots; found %s root: %s", root.Kind, root.Source)
		}
		name, version, pinned := strings.Cut(root.Source, "==")
		normalized := NormalizeRequirementName(name)
		spec := root.Source
		switch mode {
		case "all":
			if pinned {
				spec = name + ">=" + version
			}
		case "release":
			if pinned {
				spec = name + ">=" + version + ",>=" + target + ",<" + releaseUpper
			} else {
				spec = name + ">=" + target + ",<" + releaseUpper
			}
		case "package":
			if normalized == targetNormalized {
				foundTarget = true
				if pinned {
					spec = name + ">=" + version
				}
			}
		case "exact":
			if normalized == targetNormalized {
				foundTarget = true
				spec = target
				name, _, _ = strings.Cut(target, "==")
				normalized = NormalizeRequirementName(name)
			}
		}
		input = append(input, spec)
		upgradeRoots = append(upgradeRoots, UpgradeRoot{Index: index, Name: name, Normalized: normalized})
	}
	if len(input) == 0 {
		return nil, nil, fmt.Errorf("bundle has no package roots to upgrade")
	}
	if (mode == "package" || mode == "exact") && !foundTarget {
		return nil, nil, fmt.Errorf("package is not a bundle root: %s", target)
	}
	return input, upgradeRoots, nil
}

func ResolvedUpgradeRoots(reportPath string, roots []UpgradeRoot) ([]deploy.ArtifactRoot, error) {
	content, err := os.ReadFile(reportPath)
	if err != nil {
		return nil, err
	}
	var report struct {
		Install []struct {
			Metadata struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"metadata"`
		} `json:"install"`
	}
	if err := json.Unmarshal(content, &report); err != nil {
		return nil, err
	}
	versions := map[string]string{}
	for _, entry := range report.Install {
		if entry.Metadata.Name == "" || entry.Metadata.Version == "" {
			continue
		}
		versions[NormalizeDistributionName(entry.Metadata.Name)] = entry.Metadata.Version
	}
	resolved := make([]deploy.ArtifactRoot, len(roots))
	for _, root := range roots {
		version := versions[root.Normalized]
		if version == "" {
			return nil, fmt.Errorf("missing resolved root package version: %s", root.Name)
		}
		resolved[root.Index] = PackageRoot(root.Name + "==" + version)
	}
	return resolved, nil
}

func InstallCheckArgv() []string {
	return []string{
		"python",
		"-m",
		"pip",
		"--disable-pip-version-check",
		"install",
		"--no-cache-dir",
		"--target",
		"/tmp/reploy-wheelhouse-check",
		"--no-index",
		"--find-links",
		"/bundle",
		"-r",
		"/requirements.txt",
	}
}

func PrepareWheelhouseArgv(pypiOnly bool) []string {
	args := []string{
		"python",
		"-m",
		"pip",
		"--disable-pip-version-check",
		"wheel",
		"--no-cache-dir",
	}
	if !pypiOnly {
		args = append(args, "--find-links", "/bundle")
	}
	return append(args, "--wheel-dir", "/wheelhouse", "-r", "/requirements.txt")
}

func SourceWheelArgv() []string {
	return []string{
		"python",
		"-m",
		"pip",
		"--disable-pip-version-check",
		"wheel",
		"--no-deps",
		"--no-build-isolation",
		"--wheel-dir",
		"/wheelhouse",
		"/source",
	}
}

func UpgradeResolveArgv(pypiOnly bool) []string {
	args := []string{
		"python",
		"-m",
		"pip",
		"--disable-pip-version-check",
		"install",
		"--dry-run",
		"--ignore-installed",
	}
	if !pypiOnly {
		args = append(args, "--find-links", "/bundle")
	}
	return append(args, "--report", "/work/report.json", "-r", "/work/requirements.in")
}

func NormalizeRequirementName(requirementName string) string {
	baseName, _, _ := strings.Cut(requirementName, "[")
	return NormalizeDistributionName(baseName)
}

func NormalizeDistributionName(name string) string {
	return distributionSeparatorPattern.ReplaceAllString(strings.ToLower(name), "-")
}

func releaseLineUpperBound(line string) string {
	major, minor, hasMinor := strings.Cut(line, ".")
	if !hasMinor {
		return incrementDecimalString(major)
	}
	return major + "." + incrementDecimalString(minor)
}

func incrementDecimalString(value string) string {
	carry := byte(1)
	digits := []byte(value)
	for index := len(digits) - 1; index >= 0; index-- {
		if digits[index] < '0' || digits[index] > '9' {
			return value
		}
		next := digits[index] + carry
		if next <= '9' {
			digits[index] = next
			return string(digits)
		}
		digits[index] = '0'
		carry = 1
	}
	return "1" + string(digits)
}
