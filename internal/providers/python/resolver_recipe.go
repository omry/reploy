package python

import (
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

const (
	ResolverInputDirectory        = "/.reploy-resolver/input"
	ResolverOutputDirectory       = "/.reploy-resolver/output"
	ResolverSourceConstraintsPath = ResolverInputDirectory + "/reploy-source-constraints.txt"
)

// WheelResolverArgv returns the fixed one-shot pip recipe for the complete
// component wheel closure. Reusable wheels are candidates through find-links;
// local-source constraints make a candidate override the index only when its
// distribution is part of the requested closure.
func WheelResolverArgv(
	interpreter string,
	request providers.CanonicalProviderRequest,
	sources []providers.ResolvedSourceInput,
	reusable []providerstore.ArtifactDescriptor,
	selected []PortableToolVerifiedWheelInputV1,
) ([]string, error) {
	prefix, err := IsolatedInterpreterCommandPrefixV2(interpreter)
	if err != nil {
		return nil, fmt.Errorf("Python wheel resolver interpreter: %w", err)
	}
	decoded, err := decodeCanonicalProviderRequestV1(request)
	if err != nil {
		return nil, err
	}
	selectedByDistribution, err := validateSelectedPortableToolWheelsV1(decoded, sources, selected)
	if err != nil {
		return nil, err
	}
	constraints, err := wheelResolverSourceConstraints(decoded, sources, reusable, selectedByDistribution)
	if err != nil {
		return nil, err
	}
	argv := append(prefix,
		"-m", "pip", "--disable-pip-version-check",
		"wheel", "--no-cache-dir", "--progress-bar", "off",
		"--find-links", ResolverInputDirectory, "--wheel-dir", ResolverOutputDirectory)
	if len(constraints) != 0 {
		argv = append(argv, "--constraint", ResolverSourceConstraintsPath)
	}
	for _, requirement := range decoded.Requirements {
		argv = append(argv, requirement.Value["requirement"].(string))
	}
	for _, distribution := range sortedSelectedPortableToolDistributionsV1(selectedByDistribution) {
		argv = append(argv, distribution)
	}
	return argv, nil
}

// WheelResolverSourceConstraints returns the complete deterministic contents
// of the resolver-owned constraints file. Each candidate remains optional,
// but if its distribution is selected pip must use this exact staged wheel.
func WheelResolverSourceConstraints(
	request providers.CanonicalProviderRequest,
	sources []providers.ResolvedSourceInput,
	reusable []providerstore.ArtifactDescriptor,
	selected []PortableToolVerifiedWheelInputV1,
) ([]byte, error) {
	decoded, err := decodeCanonicalProviderRequestV1(request)
	if err != nil {
		return nil, err
	}
	selectedByDistribution, err := validateSelectedPortableToolWheelsV1(decoded, sources, selected)
	if err != nil {
		return nil, err
	}
	return wheelResolverSourceConstraints(decoded, sources, reusable, selectedByDistribution)
}

func wheelResolverSourceConstraints(
	request PythonProviderRequestV1,
	sources []providers.ResolvedSourceInput,
	reusable []providerstore.ArtifactDescriptor,
	selectedByDistribution map[string]PortableToolVerifiedWheelInputV1,
) ([]byte, error) {
	artifactsByDigest := map[string][]providerstore.ArtifactDescriptor{}
	for _, artifact := range reusable {
		if err := artifact.Validate(); err != nil {
			return nil, fmt.Errorf("Python wheel resolver reusable artifact: %w", err)
		}
		if artifact.Kind != "wheel" || !strings.HasSuffix(strings.ToLower(path.Base(artifact.LogicalPath)), ".whl") {
			return nil, fmt.Errorf("Python wheel resolver reusable artifact %q must be a wheel", artifact.LogicalPath)
		}
		artifactsByDigest[string(artifact.SHA256)] = append(artifactsByDigest[string(artifact.SHA256)], artifact)
	}
	var constraints strings.Builder
	overrideByDistribution := make(map[string]PythonPackageOverrideV1, len(request.Overrides))
	for _, override := range request.Overrides {
		overrideByDistribution[override.Distribution] = override
		if _, selected := selectedByDistribution[override.Distribution]; selected {
			// A matching version override carries no additional information once
			// the exact local wheel constraint below is present. Suppress it so
			// pip receives one authoritative constraint for the selected root.
			continue
		}
		if override.Kind == "version" {
			fmt.Fprintf(&constraints, "%s==%s\n", override.Distribution, override.Version)
		}
	}
	for _, distribution := range sortedSelectedPortableToolDistributionsV1(selectedByDistribution) {
		selected := selectedByDistribution[distribution]
		wheelURL := (&url.URL{Scheme: "file", Path: path.Join(ResolverInputDirectory, path.Base(selected.Descriptor.LogicalPath))}).String()
		fmt.Fprintf(&constraints, "%s @ %s\n", distribution, wheelURL)
	}
	distributions := map[string]string{}
	for index, source := range sources {
		if index > 0 && (sources[index-1].Component > source.Component || sources[index-1].Component == source.Component && sources[index-1].LogicalPackage >= source.LogicalPackage) {
			return nil, fmt.Errorf("Python wheel resolver sources must be unique and sorted")
		}
		if err := providers.ValidateResolvedSourceInput(source); err != nil {
			return nil, err
		}
		if source.Component != request.Component {
			return nil, fmt.Errorf("Python wheel resolver source %q targets component %q, want %q", source.LogicalPackage, source.Component, request.Component)
		}
		distribution := NormalizeDistributionName(source.LogicalPackage)
		if prior, found := distributions[distribution]; found {
			return nil, fmt.Errorf("Python wheel resolver sources %q and %q normalize to the same distribution", prior, source.LogicalPackage)
		}
		distributions[distribution] = source.LogicalPackage
		override, found := overrideByDistribution[distribution]
		if !found || override.Kind != "local" {
			return nil, fmt.Errorf("Python wheel resolver source %q has no matching local package override", source.LogicalPackage)
		}
		matches := artifactsByDigest[string(source.OutputArtifactDigest)]
		if len(matches) != 1 {
			return nil, fmt.Errorf("Python wheel resolver source %q must identify exactly one reusable wheel", source.LogicalPackage)
		}
		wheelURL := (&url.URL{Scheme: "file", Path: path.Join(ResolverInputDirectory, path.Base(matches[0].LogicalPath))}).String()
		fmt.Fprintf(&constraints, "%s @ %s\n", distribution, wheelURL)
	}
	return []byte(constraints.String()), nil
}

// validateSelectedPortableToolWheelsV1 validates the small amount of identity
// needed by the resolver recipe and returns a detached distribution index. The
// verified-wheel producer owns the full acquisition and metadata proof; this
// boundary only prevents an unsafe or ambiguous local constraint from being
// rendered into the pip input.
func validateSelectedPortableToolWheelsV1(
	request PythonProviderRequestV1,
	sources []providers.ResolvedSourceInput,
	selected []PortableToolVerifiedWheelInputV1,
) (map[string]PortableToolVerifiedWheelInputV1, error) {
	selectedByDistribution := make(map[string]PortableToolVerifiedWheelInputV1, len(selected))
	for index, input := range selected {
		if err := input.Descriptor.Validate(); err != nil {
			return nil, fmt.Errorf("selected portable Python wheel %d descriptor: %w", index, err)
		}
		if input.Descriptor.Kind != "wheel" || path.Dir(input.Descriptor.LogicalPath) != "wheels" ||
			!strings.HasSuffix(strings.ToLower(path.Base(input.Descriptor.LogicalPath)), ".whl") {
			return nil, fmt.Errorf("selected portable Python wheel %d descriptor must be a wheel directly beneath wheels", index)
		}
		distribution := NormalizeDistributionName(input.Inspection.Distribution)
		if distribution == "" || distribution != input.Inspection.Distribution {
			return nil, fmt.Errorf("selected portable Python wheel %d distribution must be normalized", index)
		}
		if input.Inspection.Filename != "" && input.Inspection.Filename != path.Base(input.Descriptor.LogicalPath) {
			return nil, fmt.Errorf("selected portable Python wheel %d filename does not match its descriptor", index)
		}
		if input.Inspection.Artifact != (providerstore.ArtifactDescriptor{}) && input.Inspection.Artifact != input.Descriptor {
			return nil, fmt.Errorf("selected portable Python wheel %d inspection does not match its descriptor", index)
		}
		if _, exists := selectedByDistribution[distribution]; exists {
			return nil, fmt.Errorf("selected portable Python wheels contain duplicate distribution %q", distribution)
		}
		selectedByDistribution[distribution] = input
	}

	for _, override := range request.Overrides {
		if selected, exists := selectedByDistribution[override.Distribution]; !exists {
			continue
		} else if override.Kind == "local" {
			return nil, fmt.Errorf("selected portable Python distribution %q conflicts with local package override", selected.Inspection.Distribution)
		} else if override.Kind == "version" {
			if selected.Inspection.Version == "" {
				return nil, fmt.Errorf("selected portable Python distribution %q has no inspected version for version override", selected.Inspection.Distribution)
			}
			comparison, err := ComparePackageVersionsV1(override.Version, selected.Inspection.Version)
			if err != nil {
				return nil, fmt.Errorf("selected portable Python distribution %q version override: %w", selected.Inspection.Distribution, err)
			}
			if comparison != 0 {
				return nil, fmt.Errorf("selected portable Python distribution %q conflicts with version override %q", selected.Inspection.Distribution, override.Version)
			}
		}
	}

	for _, source := range sources {
		distribution := NormalizeDistributionName(source.LogicalPackage)
		if selected, exists := selectedByDistribution[distribution]; exists {
			return nil, fmt.Errorf("selected portable Python distribution %q conflicts with source %q", selected.Inspection.Distribution, source.LogicalPackage)
		}
	}
	return selectedByDistribution, nil
}

func sortedSelectedPortableToolDistributionsV1(selected map[string]PortableToolVerifiedWheelInputV1) []string {
	result := make([]string, 0, len(selected))
	for distribution := range selected {
		result = append(result, distribution)
	}
	sort.Strings(result)
	return result
}
