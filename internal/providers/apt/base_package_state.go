package apt

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

const InstalledPackageStatusV1 = "install ok installed"

// PackageTuple is the exact dpkg identity and state used by the APT bundle.
type PackageTuple struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	Status       string `json:"status"`
}

// BasePackageStateParserV1 incrementally consumes the exact dpkg-query rows
// requested for a resolution plan. It does not retain raw command output.
type BasePackageStateParserV1 struct {
	nativeArchitecture string
	expected           map[string]string
	partial            []byte
	tuples             map[string]PackageTuple
	line               int
	err                error
}

// ResolveBasePackageNamesV1 returns the sorted literal names whose installed
// tuples must be verified. This includes retained packages and predecessors of
// planned upgrades, but not packages absent from the base.
func ResolveBasePackageNamesV1(plan ResolvePlanV1) ([]string, error) {
	if plan.Schema != ResolvePlanSchemaV1 || plan.Packages == nil {
		return nil, fmt.Errorf("APT resolution plan is invalid")
	}
	names := make([]string, 0, len(plan.Packages))
	previous := ""
	for index, pkg := range plan.Packages {
		if !debianPackageNameV1.MatchString(pkg.Name) || !validResolverArchitectureV1(pkg.ResolverArchitecture) || !validDebianVersionTokenV1(pkg.SelectedVersion) {
			return nil, fmt.Errorf("APT resolution plan package %d is invalid", index)
		}
		key := pkg.Name + "\x00" + pkg.ResolverArchitecture
		if index > 0 && previous >= key {
			return nil, fmt.Errorf("APT resolution plan packages must be unique and sorted")
		}
		previous = key
		if pkg.CurrentVersion != "" {
			if !validDebianVersionTokenV1(pkg.CurrentVersion) {
				return nil, fmt.Errorf("APT resolution plan package %q has invalid current version", pkg.Name)
			}
			names = append(names, pkg.Name)
		}
	}
	return names, nil
}

func NewBasePackageStateParserV1(nativeArchitecture string, plan ResolvePlanV1) (*BasePackageStateParserV1, error) {
	names, err := ResolveBasePackageNamesV1(plan)
	if err != nil {
		return nil, err
	}
	if !validResolverArchitectureV1(nativeArchitecture) {
		return nil, fmt.Errorf("APT base package native architecture is invalid")
	}
	expected := make(map[string]string, len(names))
	for _, pkg := range plan.Packages {
		if pkg.ResolverArchitecture != nativeArchitecture {
			return nil, fmt.Errorf("APT resolution plan package %q does not use the native resolver architecture", pkg.Name)
		}
		if pkg.CurrentVersion != "" {
			expected[pkg.Name] = pkg.CurrentVersion
		}
	}
	return &BasePackageStateParserV1{
		nativeArchitecture: nativeArchitecture,
		expected:           expected,
		tuples:             map[string]PackageTuple{},
	}, nil
}

func (parser *BasePackageStateParserV1) Write(input []byte) (int, error) {
	if parser == nil {
		return 0, fmt.Errorf("APT base package state parser is required")
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

func (parser *BasePackageStateParserV1) Finish() ([]PackageTuple, error) {
	if parser == nil {
		return nil, fmt.Errorf("APT base package state parser is required")
	}
	if parser.err != nil {
		return nil, parser.err
	}
	if len(parser.partial) != 0 {
		return nil, fmt.Errorf("APT base package state output ended with an incomplete line")
	}
	if len(parser.tuples) != len(parser.expected) {
		missing := make([]string, 0, len(parser.expected)-len(parser.tuples))
		for name := range parser.expected {
			if _, found := parser.tuples[name]; !found {
				missing = append(missing, name)
			}
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("APT base package state did not report planned package %q", missing[0])
	}
	tuples := make([]PackageTuple, 0, len(parser.tuples))
	for _, tuple := range parser.tuples {
		tuples = append(tuples, tuple)
	}
	sort.Slice(tuples, func(left int, right int) bool {
		if tuples[left].Name != tuples[right].Name {
			return tuples[left].Name < tuples[right].Name
		}
		if tuples[left].Architecture != tuples[right].Architecture {
			return tuples[left].Architecture < tuples[right].Architecture
		}
		return tuples[left].Version < tuples[right].Version
	})
	return tuples, nil
}

func (parser *BasePackageStateParserV1) consumeLine(line []byte) error {
	if len(line) == 0 || bytes.IndexAny(line, "\x00\r") >= 0 {
		return fmt.Errorf("APT base package state output line %d is malformed", parser.line)
	}
	fields := strings.Split(string(line), "\t")
	if len(fields) != 4 {
		return fmt.Errorf("APT base package state output line %d is malformed", parser.line)
	}
	binaryName, version, architecture, status := fields[0], fields[1], fields[2], fields[3]
	name := binaryName
	if plain, qualifier, found := strings.Cut(binaryName, ":"); found {
		if qualifier != parser.nativeArchitecture {
			return fmt.Errorf("APT base package %q has unexpected binary-name architecture", plain)
		}
		name = plain
	}
	expectedVersion, expected := parser.expected[name]
	if !expected {
		return fmt.Errorf("APT base package state reported unplanned package %q", name)
	}
	if _, duplicate := parser.tuples[name]; duplicate {
		return fmt.Errorf("APT base package state repeated planned package %q", name)
	}
	if version != expectedVersion {
		return fmt.Errorf("APT base package %q version does not match the dependency plan", name)
	}
	if architecture != parser.nativeArchitecture && architecture != "all" {
		return fmt.Errorf("APT base package %q has unsupported architecture %q", name, architecture)
	}
	if status != InstalledPackageStatusV1 {
		return fmt.Errorf("APT base package %q is not in exact installed state", name)
	}
	parser.tuples[name] = PackageTuple{Name: name, Version: version, Architecture: architecture, Status: status}
	return nil
}
