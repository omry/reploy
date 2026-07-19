package python

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

const (
	ResolverInputDirectory  = "/.reploy-resolver/input"
	ResolverOutputDirectory = "/.reploy-resolver/output"
)

// WheelResolverArgv returns the fixed one-shot pip recipe for the complete
// component wheel closure. Reusable wheels are candidates through find-links;
// resolved local-source wheels are also explicit roots so they supersede an
// index artifact with the same distribution name.
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
	sourceRoots := make([]string, 0, len(sources))
	for index, source := range sources {
		if index > 0 && (sources[index-1].Component > source.Component || sources[index-1].Component == source.Component && sources[index-1].LogicalPackage >= source.LogicalPackage) {
			return nil, fmt.Errorf("Python wheel resolver sources must be unique and sorted")
		}
		if err := providers.ValidateResolvedSourceInput(source); err != nil {
			return nil, err
		}
		if source.Component != decoded.Component {
			return nil, fmt.Errorf("Python wheel resolver source %q targets component %q, want %q", source.LogicalPackage, source.Component, decoded.Component)
		}
		matches := artifactsByDigest[string(source.ArtifactDigest)]
		if len(matches) != 1 {
			return nil, fmt.Errorf("Python wheel resolver source %q must identify exactly one reusable wheel", source.LogicalPackage)
		}
		sourceRoots = append(sourceRoots, path.Join(ResolverInputDirectory, path.Base(matches[0].LogicalPath)))
	}
	sort.Strings(sourceRoots)
	argv := []string{
		interpreter, "-m", "pip", "--disable-pip-version-check",
		"wheel", "--no-cache-dir", "--progress-bar", "off",
		"--find-links", ResolverInputDirectory, "--wheel-dir", ResolverOutputDirectory,
	}
	for _, requirement := range decoded.Requirements {
		argv = append(argv, requirement.Value["requirement"].(string))
	}
	argv = append(argv, sourceRoots...)
	return argv, nil
}
