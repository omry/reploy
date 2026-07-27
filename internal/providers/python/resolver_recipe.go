package python

import (
	"fmt"
	"net/url"
	"path"
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
) ([]string, error) {
	if interpreter == "" || !path.IsAbs(interpreter) || path.Clean(interpreter) != interpreter || strings.Contains(interpreter, `\`) {
		return nil, fmt.Errorf("Python wheel resolver interpreter must be a normalized absolute path")
	}
	decoded, err := decodeCanonicalProviderRequestV1(request)
	if err != nil {
		return nil, err
	}
	constraints, err := wheelResolverSourceConstraints(decoded, sources, reusable)
	if err != nil {
		return nil, err
	}
	argv := []string{
		interpreter, "-m", "pip", "--disable-pip-version-check",
		"wheel", "--no-cache-dir", "--progress-bar", "off",
		"--find-links", ResolverInputDirectory, "--wheel-dir", ResolverOutputDirectory,
	}
	if len(constraints) != 0 {
		argv = append(argv, "--constraint", ResolverSourceConstraintsPath)
	}
	for _, requirement := range decoded.Requirements {
		argv = append(argv, requirement.Value["requirement"].(string))
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
) ([]byte, error) {
	decoded, err := decodeCanonicalProviderRequestV1(request)
	if err != nil {
		return nil, err
	}
	return wheelResolverSourceConstraints(decoded, sources, reusable)
}

func wheelResolverSourceConstraints(
	request PythonProviderRequestV1,
	sources []providers.ResolvedSourceInput,
	reusable []providerstore.ArtifactDescriptor,
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
		if override.Kind == "version" {
			fmt.Fprintf(&constraints, "%s==%s\n", override.Distribution, override.Version)
		}
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
		matches := artifactsByDigest[string(source.ArtifactDigest)]
		if len(matches) != 1 {
			return nil, fmt.Errorf("Python wheel resolver source %q must identify exactly one reusable wheel", source.LogicalPackage)
		}
		wheelURL := (&url.URL{Scheme: "file", Path: path.Join(ResolverInputDirectory, path.Base(matches[0].LogicalPath))}).String()
		fmt.Fprintf(&constraints, "%s @ %s\n", distribution, wheelURL)
	}
	return []byte(constraints.String()), nil
}
