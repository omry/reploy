package apt

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// ResolveInstallParserV1 captures APT's concrete install transaction from the
// simulation output. The dependency marker stream can omit upgrades pulled in
// to keep an already-installed dependency compatible with a selected package;
// APT's Inst records contain those archive-producing changes.
type ResolveInstallParserV1 struct {
	nativeArchitecture string
	partial            []byte
	packages           map[string]ResolvePlanPackageV1
	line               int
	err                error
}

func NewResolveInstallParserV1(nativeArchitecture string) (*ResolveInstallParserV1, error) {
	if !validResolverArchitectureV1(nativeArchitecture) {
		return nil, fmt.Errorf("APT install transaction native architecture is invalid")
	}
	return &ResolveInstallParserV1{
		nativeArchitecture: nativeArchitecture,
		packages:           map[string]ResolvePlanPackageV1{},
	}, nil
}

func (parser *ResolveInstallParserV1) Write(input []byte) (int, error) {
	if parser == nil {
		return 0, fmt.Errorf("APT install transaction parser is required")
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

func (parser *ResolveInstallParserV1) Finish() ([]ResolvePlanPackageV1, error) {
	if parser == nil {
		return nil, fmt.Errorf("APT install transaction parser is required")
	}
	if parser.err != nil {
		return nil, parser.err
	}
	if len(parser.partial) != 0 {
		parser.line++
		if err := parser.consumeLine(parser.partial); err != nil {
			return nil, err
		}
		parser.partial = nil
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
	return packages, nil
}

func (parser *ResolveInstallParserV1) consumeLine(line []byte) error {
	if bytes.IndexAny(line, "\x00\r") >= 0 {
		return fmt.Errorf("APT install transaction output line %d is malformed", parser.line)
	}
	text := string(line)
	if !strings.HasPrefix(text, "Inst ") {
		return nil
	}
	packageToken, detail, found := strings.Cut(strings.TrimPrefix(text, "Inst "), " ")
	if !found || packageToken == "" || detail == "" {
		return fmt.Errorf("APT install transaction record at line %d is malformed", parser.line)
	}
	name, explicitArchitecture, hasArchitecture := strings.Cut(packageToken, ":")
	if !debianPackageNameV1.MatchString(name) || hasArchitecture && explicitArchitecture != parser.nativeArchitecture {
		return fmt.Errorf("APT install transaction package at line %d is invalid", parser.line)
	}
	current := ""
	if strings.HasPrefix(detail, "[") {
		end := strings.Index(detail, "] ")
		if end < 0 {
			return fmt.Errorf("APT install transaction record for package %q is malformed", name)
		}
		current = detail[1:end]
		detail = detail[end+2:]
	}
	detail = strings.TrimSuffix(detail, " []")
	if len(detail) < 5 || detail[0] != '(' || detail[len(detail)-1] != ')' {
		return fmt.Errorf("APT install transaction record for package %q is malformed", name)
	}
	selection := detail[1 : len(detail)-1]
	architectureStart := strings.LastIndex(selection, " [")
	if architectureStart < 0 || !strings.HasSuffix(selection, "]") {
		return fmt.Errorf("APT install transaction record for package %q has no architecture", name)
	}
	archiveArchitecture := selection[architectureStart+2 : len(selection)-1]
	if archiveArchitecture != parser.nativeArchitecture && archiveArchitecture != "all" {
		return fmt.Errorf("APT install transaction package %q has unsupported archive architecture %q", name, archiveArchitecture)
	}
	selected, _, _ := strings.Cut(selection[:architectureStart], " ")
	if !validDebianVersionTokenV1(selected) || current != "" && !validDebianVersionTokenV1(current) || current != "" && current == selected {
		return fmt.Errorf("APT install transaction package %q has invalid version evidence", name)
	}
	pkg := ResolvePlanPackageV1{
		Name: name, ResolverArchitecture: parser.nativeArchitecture,
		CurrentVersion: current, SelectedVersion: selected,
	}
	if previous, exists := parser.packages[name]; exists && previous != pkg {
		return fmt.Errorf("APT install transaction package %q has conflicting selections", name)
	}
	parser.packages[name] = pkg
	return nil
}

// CompleteResolvePlanV1 reconciles the strict dependency-marker plan with the
// concrete Inst records that identify every archive-producing selection.
func CompleteResolvePlanV1(marker ResolvePlanV1, changes []ResolvePlanPackageV1) (ResolvePlanV1, error) {
	if _, err := ResolveBasePackageNamesV1(marker); err != nil {
		return ResolvePlanV1{}, err
	}
	if len(marker.Packages) == 0 {
		return ResolvePlanV1{}, fmt.Errorf("APT dependency plan has no packages")
	}
	nativeArchitecture := marker.Packages[0].ResolverArchitecture
	byName := make(map[string]ResolvePlanPackageV1, len(marker.Packages)+len(changes))
	for _, pkg := range marker.Packages {
		if pkg.ResolverArchitecture != nativeArchitecture {
			return ResolvePlanV1{}, fmt.Errorf("APT dependency plan mixes resolver architectures")
		}
		byName[pkg.Name] = pkg
	}
	changed := make(map[string]bool, len(changes))
	for _, pkg := range changes {
		if !debianPackageNameV1.MatchString(pkg.Name) || pkg.ResolverArchitecture != nativeArchitecture ||
			!validDebianVersionTokenV1(pkg.SelectedVersion) ||
			pkg.CurrentVersion != "" && (!validDebianVersionTokenV1(pkg.CurrentVersion) || pkg.CurrentVersion == pkg.SelectedVersion) {
			return ResolvePlanV1{}, fmt.Errorf("APT install transaction package %q is invalid", pkg.Name)
		}
		if previous, found := byName[pkg.Name]; found && previous != pkg {
			return ResolvePlanV1{}, fmt.Errorf("APT dependency and install plans disagree for package %q", pkg.Name)
		}
		byName[pkg.Name] = pkg
		changed[pkg.Name] = true
	}
	for _, pkg := range marker.Packages {
		if (pkg.CurrentVersion == "" || pkg.CurrentVersion != pkg.SelectedVersion) && !changed[pkg.Name] {
			return ResolvePlanV1{}, fmt.Errorf("APT changed package %q has no install transaction record", pkg.Name)
		}
	}
	packages := make([]ResolvePlanPackageV1, 0, len(byName))
	for _, pkg := range byName {
		packages = append(packages, pkg)
	}
	sort.Slice(packages, func(left int, right int) bool {
		if packages[left].Name != packages[right].Name {
			return packages[left].Name < packages[right].Name
		}
		return packages[left].ResolverArchitecture < packages[right].ResolverArchitecture
	})
	plan := ResolvePlanV1{Schema: ResolvePlanSchemaV1, Packages: packages}
	if _, err := ResolveBasePackageNamesV1(plan); err != nil {
		return ResolvePlanV1{}, err
	}
	return plan, nil
}
