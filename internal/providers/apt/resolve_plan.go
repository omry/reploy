package apt

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const ResolvePlanSchemaV1 = "apt-resolve-plan-v1"

// ResolvePlanPackageV1 is one package selected by APT's dependency planner.
// ResolverArchitecture is APT's cache architecture, which is the native
// architecture even for some Architecture: all archives. Final Debian
// architecture and status are taken from dpkg state or inspected .deb control
// metadata, not invented from this intermediate record.
type ResolvePlanPackageV1 struct {
	Name                 string
	ResolverArchitecture string
	CurrentVersion       string
	SelectedVersion      string
}

// ResolvePlanV1 is the complete, canonical package set reported by one
// successfully parsed APT planning pass.
type ResolvePlanV1 struct {
	Schema   string
	Packages []ResolvePlanPackageV1
}

// ResolvePlanMarkerParserV1 incrementally consumes APT's dependency-marker
// stderr without retaining a second copy of the live command output.
type ResolvePlanMarkerParserV1 struct {
	nativeArchitecture string
	roots              []string
	partial            []byte
	packages           map[string]ResolvePlanPackageV1
	line               int
	err                error
}

var (
	resolveMarkInstallV1 = regexp.MustCompile(`^ +MarkInstall ([a-z0-9][a-z0-9+.-]+):([a-z0-9][a-z0-9-]*) < ([^ <>]+)(?: -> ([^ <>]+))? @([a-z]{2}) (?:[A-Za-z]+(?: [A-Za-z]+)*) > FU=([01])$`)
	resolveMarkGarbageV1 = regexp.MustCompile(`^ +Ignore MarkGarbage of [a-z0-9][a-z0-9+.-]+:[a-z0-9][a-z0-9-]* < [^ <>]+(?: -> [^ <>]+)? @[a-z]{2} (?:[A-Za-z]+(?: [A-Za-z]+)*) > as its mode \([A-Za-z]+\) is protected$`)
	debianPackageNameV1  = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+$`)
	debianVersionTokenV1 = regexp.MustCompile(`^[0-9A-Za-z.+:~-]+$`)
)

func NewResolvePlanMarkerParserV1(nativeArchitecture string, roots []string) (*ResolvePlanMarkerParserV1, error) {
	if !validResolverArchitectureV1(nativeArchitecture) {
		return nil, fmt.Errorf("APT resolution plan native architecture is invalid")
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("APT resolution plan requires at least one root package")
	}
	canonicalRoots := append([]string{}, roots...)
	if !sort.StringsAreSorted(canonicalRoots) {
		return nil, fmt.Errorf("APT resolution plan roots must be sorted")
	}
	for index, root := range canonicalRoots {
		name, version, err := parseResolveRootV1(root)
		if err != nil {
			return nil, err
		}
		if index > 0 {
			previousName, _, _ := parseResolveRootV1(canonicalRoots[index-1])
			if previousName >= name {
				return nil, fmt.Errorf("APT resolution plan roots must have unique sorted package names")
			}
		}
		_ = version
	}
	return &ResolvePlanMarkerParserV1{
		nativeArchitecture: nativeArchitecture,
		roots:              canonicalRoots,
		packages:           map[string]ResolvePlanPackageV1{},
	}, nil
}

func (parser *ResolvePlanMarkerParserV1) Write(input []byte) (int, error) {
	if parser == nil {
		return 0, fmt.Errorf("APT resolution plan parser is required")
	}
	length := len(input)
	if parser.err != nil {
		return length, nil
	}
	parser.partial = append(parser.partial, input...)
	for {
		newline := bytes.IndexByte(parser.partial, '\n')
		if newline < 0 {
			break
		}
		line := parser.partial[:newline]
		parser.partial = parser.partial[newline+1:]
		parser.line++
		if err := parser.consumeLine(line); err != nil {
			parser.err = err
			parser.partial = nil
			break
		}
	}
	return length, nil
}

func (parser *ResolvePlanMarkerParserV1) Finish() (ResolvePlanV1, error) {
	if parser == nil {
		return ResolvePlanV1{}, fmt.Errorf("APT resolution plan parser is required")
	}
	if parser.err != nil {
		return ResolvePlanV1{}, parser.err
	}
	if len(parser.partial) != 0 {
		return ResolvePlanV1{}, fmt.Errorf("APT dependency-marker output ended with an incomplete line")
	}
	packages := make([]ResolvePlanPackageV1, 0, len(parser.packages))
	for _, pkg := range parser.packages {
		packages = append(packages, pkg)
	}
	sort.Slice(packages, func(left int, right int) bool {
		if packages[left].Name != packages[right].Name {
			return packages[left].Name < packages[right].Name
		}
		return packages[left].ResolverArchitecture < packages[right].ResolverArchitecture
	})
	for _, root := range parser.roots {
		name, version, _ := parseResolveRootV1(root)
		index := sort.Search(len(packages), func(index int) bool { return packages[index].Name >= name })
		if index == len(packages) || packages[index].Name != name {
			return ResolvePlanV1{}, fmt.Errorf("APT dependency-marker capability did not report requested package %q", name)
		}
		if version != "" && packages[index].SelectedVersion != version {
			return ResolvePlanV1{}, fmt.Errorf("APT selected version for requested package %q does not match its exact request", name)
		}
	}
	return ResolvePlanV1{Schema: ResolvePlanSchemaV1, Packages: packages}, nil
}

func (parser *ResolvePlanMarkerParserV1) consumeLine(line []byte) error {
	if len(line) == 0 || bytes.IndexAny(line, "\x00\r") >= 0 {
		return fmt.Errorf("APT dependency-marker output line %d is malformed", parser.line)
	}
	text := string(line)
	if resolveMarkGarbageV1.MatchString(text) {
		return nil
	}
	match := resolveMarkInstallV1.FindStringSubmatch(text)
	if match == nil {
		return fmt.Errorf("APT dependency-marker capability returned unsupported output at line %d", parser.line)
	}
	name, architecture := match[1], match[2]
	current, selected, state := match[3], match[4], match[5]
	if architecture != parser.nativeArchitecture {
		return fmt.Errorf("APT dependency-marker package %q used unexpected resolver architecture %q", name, architecture)
	}
	if current == "none" {
		if selected == "" || state != "un" {
			return fmt.Errorf("APT dependency-marker package %q has inconsistent absent state", name)
		}
		current = ""
	} else {
		if !validDebianVersionTokenV1(current) || state != "ii" {
			return fmt.Errorf("APT dependency-marker package %q has unsupported installed state", name)
		}
		if selected == "" {
			selected = current
		}
	}
	if !validDebianVersionTokenV1(selected) || selected == current && current != "" && match[4] != "" {
		return fmt.Errorf("APT dependency-marker package %q has invalid selected version", name)
	}
	pkg := ResolvePlanPackageV1{
		Name: name, ResolverArchitecture: architecture,
		CurrentVersion: current, SelectedVersion: selected,
	}
	key := name + "\x00" + architecture
	if previous, exists := parser.packages[key]; exists && previous != pkg {
		return fmt.Errorf("APT dependency-marker package %q has conflicting selections", name)
	}
	parser.packages[key] = pkg
	return nil
}

func parseResolveRootV1(root string) (string, string, error) {
	name, version, hasVersion := strings.Cut(root, "=")
	if !debianPackageNameV1.MatchString(name) {
		return "", "", fmt.Errorf("APT resolution plan root is invalid")
	}
	if hasVersion && !validDebianVersionTokenV1(version) {
		return "", "", fmt.Errorf("APT resolution plan exact root version is invalid")
	}
	return name, version, nil
}

func validDebianVersionTokenV1(value string) bool {
	return value != "" && debianVersionTokenV1.MatchString(value)
}

func validResolverArchitectureV1(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}
