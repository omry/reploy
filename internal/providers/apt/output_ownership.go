package apt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

const DPKGOwnerDataSchemaV1 = "apt-dpkg-owner-v1"

const AlternativeSelectionSchemaV1 = "apt-alternative-selection-v1"

type AlternativeSelectionV1 struct {
	Group string `json:"group"`
	Link  string `json:"link"`
	Value string `json:"value"`
}

func AlternativeGroupForPathV1(value string) (string, bool) {
	const root = "/etc/alternatives/"
	if !strings.HasPrefix(value, root) || strings.TrimPrefix(value, root) == "" || strings.Contains(strings.TrimPrefix(value, root), "/") {
		return "", false
	}
	group := path.Base(value)
	if !validAlternativeGroupV1(group) {
		return "", false
	}
	return group, true
}

// ParseAlternativeQueryV1 accepts only the selected group identity, public
// link, and selected value needed to validate an already-observed chain.
func ParseAlternativeQueryV1(output []byte, expectedGroup string) (AlternativeSelectionV1, error) {
	if !validAlternativeGroupV1(expectedGroup) || len(output) == 0 || output[len(output)-1] != '\n' || bytes.IndexAny(output, "\x00\r") >= 0 {
		return AlternativeSelectionV1{}, fmt.Errorf("APT alternatives query output is malformed")
	}
	fields := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(output), "\n"), "\n") {
		if strings.HasPrefix(line, " ") || line == "" {
			continue
		}
		if line == "Slaves:" {
			continue
		}
		name, value, found := strings.Cut(line, ": ")
		if !found {
			return AlternativeSelectionV1{}, fmt.Errorf("APT alternatives query output is malformed")
		}
		switch name {
		case "Name", "Link", "Value":
			if _, duplicate := fields[name]; duplicate {
				return AlternativeSelectionV1{}, fmt.Errorf("APT alternatives query repeated %s", name)
			}
			fields[name] = value
		case "Status", "Best", "Alternative", "Priority", "Slaves":
		default:
			return AlternativeSelectionV1{}, fmt.Errorf("APT alternatives query returned unknown field %q", name)
		}
	}
	selection := AlternativeSelectionV1{Group: fields["Name"], Link: fields["Link"], Value: fields["Value"]}
	if selection.Group != expectedGroup || !strings.HasPrefix(selection.Link, "/") || !strings.HasPrefix(selection.Value, "/") {
		return AlternativeSelectionV1{}, fmt.Errorf("APT alternatives query does not match group %q", expectedGroup)
	}
	return selection, nil
}

// ParseDPKGSearchOutputV1 accepts exactly one literal dpkg owner for every
// requested observed path. Pattern expansion, omissions, and multiple owners
// are rejected rather than interpreted as discovery.
func ParseDPKGSearchOutputV1(output []byte, expectedPaths []string, nativeArchitecture string) (map[string]string, error) {
	if !validResolverArchitectureV1(nativeArchitecture) {
		return nil, fmt.Errorf("APT dpkg owner native architecture is invalid")
	}
	expectedPaths = append([]string{}, expectedPaths...)
	sort.Strings(expectedPaths)
	expected := make(map[string]bool, len(expectedPaths))
	for index, path := range expectedPaths {
		if path == "" || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\x00\r\n") {
			return nil, fmt.Errorf("APT dpkg owner path %d is invalid", index)
		}
		if index > 0 && expectedPaths[index-1] == path {
			return nil, fmt.Errorf("APT dpkg owner paths must be unique")
		}
		expected[path] = true
	}
	if len(expectedPaths) == 0 {
		if len(output) != 0 {
			return nil, fmt.Errorf("APT dpkg owner query returned unrequested output")
		}
		return map[string]string{}, nil
	}
	if len(output) == 0 || output[len(output)-1] != '\n' || bytes.IndexAny(output, "\x00\r") >= 0 {
		return nil, fmt.Errorf("APT dpkg owner query returned malformed output")
	}
	owners := make(map[string]string, len(expectedPaths))
	for lineNumber, line := range strings.Split(strings.TrimSuffix(string(output), "\n"), "\n") {
		delimiter := strings.Index(line, ": ")
		if delimiter <= 0 {
			return nil, fmt.Errorf("APT dpkg owner output line %d is malformed", lineNumber+1)
		}
		binaryName, path := line[:delimiter], line[delimiter+2:]
		if !expected[path] {
			return nil, fmt.Errorf("APT dpkg owner query expanded beyond requested path %q", path)
		}
		name := binaryName
		if plain, architecture, found := strings.Cut(binaryName, ":"); found {
			if architecture != nativeArchitecture {
				return nil, fmt.Errorf("APT dpkg owner %q has unexpected architecture", plain)
			}
			name = plain
		}
		if !debianPackageNameV1.MatchString(name) {
			return nil, fmt.Errorf("APT dpkg owner for %q is invalid", path)
		}
		if _, duplicate := owners[path]; duplicate {
			return nil, fmt.Errorf("APT dpkg owner query returned multiple owners for %q", path)
		}
		owners[path] = name
	}
	for _, path := range expectedPaths {
		if _, found := owners[path]; !found {
			return nil, fmt.Errorf("APT dpkg owner query did not identify %q", path)
		}
	}
	return owners, nil
}

// ParseInstalledPackageStateV1 accepts only the exact dpkg-query tuple for
// each already-known owner package. It does not interpret the output as package
// discovery and rejects missing, extra, duplicate, or changed state.
func ParseInstalledPackageStateV1(output []byte, expected []PackageTuple, nativeArchitecture string) ([]PackageTuple, error) {
	if !validResolverArchitectureV1(nativeArchitecture) {
		return nil, fmt.Errorf("APT installed package native architecture is invalid")
	}
	expected = append([]PackageTuple{}, expected...)
	sort.Slice(expected, func(left int, right int) bool { return expected[left].Name < expected[right].Name })
	byName := make(map[string]PackageTuple, len(expected))
	for index, tuple := range expected {
		if err := validatePackageTupleV1(tuple, nativeArchitecture); err != nil {
			return nil, fmt.Errorf("APT expected installed package %d: %w", index, err)
		}
		if _, duplicate := byName[tuple.Name]; duplicate {
			return nil, fmt.Errorf("APT expected installed packages repeat %q", tuple.Name)
		}
		byName[tuple.Name] = tuple
	}
	if len(expected) == 0 {
		if len(output) != 0 {
			return nil, fmt.Errorf("APT installed package query returned unrequested output")
		}
		return []PackageTuple{}, nil
	}
	if len(output) == 0 || output[len(output)-1] != '\n' || bytes.IndexAny(output, "\x00\r") >= 0 {
		return nil, fmt.Errorf("APT installed package query returned malformed output")
	}
	observed := make(map[string]PackageTuple, len(expected))
	for lineNumber, line := range strings.Split(strings.TrimSuffix(string(output), "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			return nil, fmt.Errorf("APT installed package output line %d is malformed", lineNumber+1)
		}
		binaryName, version, architecture, status := fields[0], fields[1], fields[2], fields[3]
		name := binaryName
		if plain, qualifier, found := strings.Cut(binaryName, ":"); found {
			if qualifier != nativeArchitecture {
				return nil, fmt.Errorf("APT installed package %q has unexpected binary-name architecture", plain)
			}
			name = plain
		}
		want, requested := byName[name]
		if !requested {
			return nil, fmt.Errorf("APT installed package query returned unrequested package %q", name)
		}
		if _, duplicate := observed[name]; duplicate {
			return nil, fmt.Errorf("APT installed package query repeated package %q", name)
		}
		got := PackageTuple{Name: name, Version: version, Architecture: architecture, Status: status}
		if got != want {
			return nil, fmt.Errorf("APT installed package %q does not match its locked tuple", name)
		}
		observed[name] = got
	}
	if len(observed) != len(expected) {
		for _, tuple := range expected {
			if _, found := observed[tuple.Name]; !found {
				return nil, fmt.Errorf("APT installed package query did not report %q", tuple.Name)
			}
		}
	}
	return expected, nil
}

// ApplyOutputOwnershipV1 binds every ordinary observed link and terminal to
// the exact tuple for its dpkg owner in the complete locked bundle.
func ApplyOutputOwnershipV1(
	bundle BundleV1,
	evidence []providers.ExecutableEvidence,
	ownerByPath map[string]string,
	alternatives map[string]AlternativeSelectionV1,
) ([]providers.ExecutableEvidence, error) {
	if err := ValidateBundleV1(bundle); err != nil {
		return nil, err
	}
	closure := make(map[string]PackageTuple, len(bundle.BasePackages)+len(bundle.BundlePackages))
	for _, pkg := range bundle.BasePackages {
		closure[pkg.Tuple.Name] = pkg.Tuple
	}
	for _, pkg := range bundle.BundlePackages {
		closure[pkg.Tuple.Name] = pkg.Tuple
	}
	return applyOutputOwnershipWithClosureV1(evidence, ownerByPath, alternatives, closure)
}

// ReproduceOutputOwnershipV1 rebuilds APT ownership on fresh filesystem
// evidence while requiring every current owner package to match the exact
// locked tuple and installed state. The locked evidence supplies only the
// accepted owner tuples, not filesystem observations.
func ReproduceOutputOwnershipV1(
	nativeArchitecture string,
	fresh []providers.ExecutableEvidence,
	locked []providers.ExecutableEvidence,
	ownerByPath map[string]string,
	installed []PackageTuple,
	alternatives map[string]AlternativeSelectionV1,
) ([]providers.ExecutableEvidence, error) {
	if !validResolverArchitectureV1(nativeArchitecture) {
		return nil, fmt.Errorf("APT reproduced output native architecture is invalid")
	}
	if fresh == nil || locked == nil || ownerByPath == nil || installed == nil || alternatives == nil {
		return nil, fmt.Errorf("APT reproduced output inputs must use collections")
	}
	if len(fresh) != len(locked) {
		return nil, fmt.Errorf("APT fresh and locked output counts differ")
	}
	fresh = append([]providers.ExecutableEvidence{}, fresh...)
	locked = append([]providers.ExecutableEvidence{}, locked...)
	sort.Slice(fresh, func(left int, right int) bool {
		return compareQualifiedOutputV1(fresh[left].Output, fresh[right].Output) < 0
	})
	sort.Slice(locked, func(left int, right int) bool {
		return compareQualifiedOutputV1(locked[left].Output, locked[right].Output) < 0
	})
	lockedTuples, err := LockedOutputOwnerTuplesV1(nativeArchitecture, locked)
	if err != nil {
		return nil, err
	}
	closure := make(map[string]PackageTuple, len(lockedTuples))
	for _, tuple := range lockedTuples {
		closure[tuple.Name] = tuple
	}
	for index := range locked {
		if index > 0 && (fresh[index-1].Output == fresh[index].Output || locked[index-1].Output == locked[index].Output) {
			return nil, fmt.Errorf("APT reproduced outputs must be unique")
		}
		if err := providers.ValidateFinalExecutableEvidence(fresh[index]); err != nil {
			return nil, fmt.Errorf("APT fresh output %d: %w", index, err)
		}
		for _, link := range fresh[index].LinkChain {
			if link.Kind != "ordinary" || link.Owner != nil || link.ProviderDetail != nil {
				return nil, fmt.Errorf("APT fresh output %d contains prebound provider evidence", index)
			}
		}
		if fresh[index].Terminal.Owner != nil {
			return nil, fmt.Errorf("APT fresh output %d contains a prebound terminal owner", index)
		}
		freshFacts, _ := canonical.Marshal(fresh[index].Facts)
		lockedFacts, _ := canonical.Marshal(locked[index].Facts)
		if fresh[index].Output != locked[index].Output || fresh[index].InvocationPath != locked[index].InvocationPath || !bytes.Equal(freshFacts, lockedFacts) {
			return nil, fmt.Errorf("APT fresh output %d does not match its locked identity", index)
		}
	}
	if len(installed) != len(closure) {
		return nil, fmt.Errorf("APT installed owner package set does not match locked tuples")
	}
	seenInstalled := map[string]bool{}
	for _, tuple := range installed {
		lockedTuple, found := closure[tuple.Name]
		if !found || tuple != lockedTuple || seenInstalled[tuple.Name] {
			return nil, fmt.Errorf("APT installed owner package %q does not match its locked tuple", tuple.Name)
		}
		seenInstalled[tuple.Name] = true
	}
	return applyOutputOwnershipWithClosureV1(fresh, ownerByPath, alternatives, closure)
}

// LockedOutputOwnerTuplesV1 extracts the unique canonical package tuples from
// accepted APT output evidence. The result parameterizes a fresh exact-state
// query; it contains no filesystem observations.
func LockedOutputOwnerTuplesV1(nativeArchitecture string, locked []providers.ExecutableEvidence) ([]PackageTuple, error) {
	if !validResolverArchitectureV1(nativeArchitecture) {
		return nil, fmt.Errorf("APT locked output native architecture is invalid")
	}
	if locked == nil {
		return nil, fmt.Errorf("APT locked outputs must use an array")
	}
	closure := map[string]PackageTuple{}
	for index, evidence := range locked {
		if err := providers.ValidateFinalExecutableEvidence(evidence); err != nil {
			return nil, fmt.Errorf("APT locked output %d: %w", index, err)
		}
		for _, link := range evidence.LinkChain {
			if link.Kind == "alternative" {
				if link.Owner != nil || link.ProviderDetail == nil || link.ProviderDetail.Schema != AlternativeSelectionSchemaV1 {
					return nil, fmt.Errorf("APT locked alternative link %q has invalid provider evidence", link.Path)
				}
				continue
			}
			if err := addLockedOwnerTupleV1(closure, link.Owner, nativeArchitecture); err != nil {
				return nil, fmt.Errorf("APT locked output link %q: %w", link.Path, err)
			}
		}
		if err := addLockedOwnerTupleV1(closure, evidence.Terminal.Owner, nativeArchitecture); err != nil {
			return nil, fmt.Errorf("APT locked output terminal %q: %w", evidence.Terminal.Path, err)
		}
	}
	result := make([]PackageTuple, 0, len(closure))
	for _, tuple := range closure {
		result = append(result, tuple)
	}
	sort.Slice(result, func(left int, right int) bool { return result[left].Name < result[right].Name })
	return result, nil
}

func applyOutputOwnershipWithClosureV1(
	evidence []providers.ExecutableEvidence,
	ownerByPath map[string]string,
	alternatives map[string]AlternativeSelectionV1,
	closure map[string]PackageTuple,
) ([]providers.ExecutableEvidence, error) {
	result := append([]providers.ExecutableEvidence{}, evidence...)
	for evidenceIndex := range result {
		result[evidenceIndex].LinkChain = append([]providers.LinkEvidence{}, evidence[evidenceIndex].LinkChain...)
		for linkIndex := range result[evidenceIndex].LinkChain {
			link := &result[evidenceIndex].LinkChain[linkIndex]
			if linkIndex+1 < len(result[evidenceIndex].LinkChain) {
				next := &result[evidenceIndex].LinkChain[linkIndex+1]
				group, managed := AlternativeGroupForPathV1(next.Path)
				if managed {
					selection, found := alternatives[next.Path]
					if !found || selection.Group != group || selection.Link != link.Path || link.ResolvedPath != next.Path || selection.Value != next.ResolvedPath {
						return nil, fmt.Errorf("APT output %s.%s has mismatched alternatives group %q", result[evidenceIndex].Output.Component, result[evidenceIndex].Output.Name, group)
					}
					link.Kind = "alternative"
					next.Kind = "alternative"
					detail, err := canonicalAlternativeSelectionV1(selection)
					if err != nil {
						return nil, err
					}
					link.ProviderDetail = &detail
					nextDetail := detail
					next.ProviderDetail = &nextDetail
					continue
				}
			}
			if _, managed := AlternativeGroupForPathV1(link.Path); managed && link.Kind == "alternative" {
				continue
			}
			if _, managed := AlternativeGroupForPathV1(link.Path); managed {
				return nil, fmt.Errorf("APT output %s.%s has unregistered alternatives link %q", result[evidenceIndex].Output.Component, result[evidenceIndex].Output.Name, link.Path)
			}
			if link.Kind != "ordinary" {
				return nil, fmt.Errorf("APT output %s.%s has unresolved non-ordinary link %q", result[evidenceIndex].Output.Component, result[evidenceIndex].Output.Name, link.Path)
			}
			owner, err := aptOwnerEvidenceV1(link.Path, ownerByPath, closure)
			if err != nil {
				return nil, err
			}
			link.Owner = &owner
		}
		owner, err := aptOwnerEvidenceV1(result[evidenceIndex].Terminal.Path, ownerByPath, closure)
		if err != nil {
			return nil, err
		}
		result[evidenceIndex].Terminal.Owner = &owner
		if err := providers.ValidateFinalExecutableEvidence(result[evidenceIndex]); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func compareQualifiedOutputV1(left providers.QualifiedOutput, right providers.QualifiedOutput) int {
	if left.Component != right.Component {
		return strings.Compare(left.Component, right.Component)
	}
	return strings.Compare(left.Name, right.Name)
}

func addLockedOwnerTupleV1(closure map[string]PackageTuple, owner *providers.OwnerEvidence, nativeArchitecture string) error {
	if owner == nil || owner.Provider != "apt" || owner.Data.Schema != DPKGOwnerDataSchemaV1 || len(owner.Data.Value) != 4 {
		return fmt.Errorf("owner evidence is not an exact APT package tuple")
	}
	encoded, err := canonical.Marshal(owner.Data.Value)
	if err != nil {
		return err
	}
	var tuple PackageTuple
	if err := json.Unmarshal(encoded, &tuple); err != nil {
		return fmt.Errorf("decode locked APT owner tuple: %w", err)
	}
	if err := validatePackageTupleV1(tuple, nativeArchitecture); err != nil {
		return err
	}
	expected, err := aptOwnerEvidenceForTupleV1(tuple)
	if err != nil {
		return err
	}
	actualBytes, _ := canonical.Marshal(owner)
	expectedBytes, _ := canonical.Marshal(expected)
	if !bytes.Equal(actualBytes, expectedBytes) {
		return fmt.Errorf("APT owner tuple is not canonically normalized")
	}
	if existing, found := closure[tuple.Name]; found && existing != tuple {
		return fmt.Errorf("APT owner package %q has conflicting locked tuples", tuple.Name)
	}
	closure[tuple.Name] = tuple
	return nil
}

func canonicalAlternativeSelectionV1(selection AlternativeSelectionV1) (providers.CanonicalProviderData, error) {
	encoded, err := canonical.Marshal(selection)
	if err != nil {
		return providers.CanonicalProviderData{}, err
	}
	var value canonical.Object
	if err := json.Unmarshal(encoded, &value); err != nil {
		return providers.CanonicalProviderData{}, err
	}
	return providers.CanonicalProviderData{Schema: AlternativeSelectionSchemaV1, Value: value}, nil
}

func validAlternativeGroupV1(value string) bool {
	if value == "" || value[0] == '-' {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("+_.-", char) {
			continue
		}
		return false
	}
	return true
}

func aptOwnerEvidenceV1(path string, ownerByPath map[string]string, closure map[string]PackageTuple) (providers.OwnerEvidence, error) {
	packageName, found := ownerByPath[path]
	if !found {
		return providers.OwnerEvidence{}, fmt.Errorf("APT output path %q has no dpkg owner", path)
	}
	tuple, found := closure[packageName]
	if !found {
		return providers.OwnerEvidence{}, fmt.Errorf("APT output path %q is owned by package %q outside the locked closure", path, packageName)
	}
	return aptOwnerEvidenceForTupleV1(tuple)
}

func aptOwnerEvidenceForTupleV1(tuple PackageTuple) (providers.OwnerEvidence, error) {
	encoded, err := canonical.Marshal(tuple)
	if err != nil {
		return providers.OwnerEvidence{}, err
	}
	var value canonical.Object
	if err := json.Unmarshal(encoded, &value); err != nil {
		return providers.OwnerEvidence{}, err
	}
	return providers.OwnerEvidence{
		Provider: "apt", Data: providers.CanonicalProviderData{Schema: DPKGOwnerDataSchemaV1, Value: value},
	}, nil
}
