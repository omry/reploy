package dockerdeploy

import (
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

// MatchReusablePythonLocalSources returns exact prior source identities
// whose currently observed local manifests are unchanged. Missing or
// changed observations are cache misses, not errors.
func MatchReusablePythonLocalSources(
	locked []providers.ResolvedSourceInput,
	observed []PythonLocalSource,
) ([]providers.ResolvedSourceInput, error) {
	if locked == nil || observed == nil {
		return nil, fmt.Errorf("locked and observed local Python sources must use arrays")
	}
	if err := validatePythonLocalSourcesForSnapshot(observed); err != nil {
		return nil, err
	}
	observedByDistribution := make(map[string]PythonLocalSource, len(observed))
	for _, source := range observed {
		observedByDistribution[source.Distribution] = source
	}

	reusable := make([]providers.ResolvedSourceInput, 0, len(locked))
	lockedKeys := make(map[string]struct{}, len(locked))
	for _, source := range locked {
		if err := pythonprovider.ValidateResolvedSourceInputV2(source); err != nil {
			return nil, fmt.Errorf("locked local Python source %s.%s: %w", source.Component, source.LogicalPackage, err)
		}
		key := source.Component + "\x00" + source.LogicalPackage
		if _, found := lockedKeys[key]; found {
			return nil, fmt.Errorf("locked local Python sources contain duplicate %s.%s", source.Component, source.LogicalPackage)
		}
		lockedKeys[key] = struct{}{}
		current, found := observedByDistribution[source.LogicalPackage]
		if found && current.SourceInputDigest == source.SourceInputDigest {
			reusable = append(reusable, source)
		}
	}
	sort.Slice(reusable, func(left int, right int) bool {
		return compareResolvedSources(reusable[left], reusable[right]) < 0
	})
	return reusable, nil
}
