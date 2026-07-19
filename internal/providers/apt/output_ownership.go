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
