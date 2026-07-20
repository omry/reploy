package dockerdeploy

import (
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
)

// buildLockSelectedSourcesV1 reads selected-source identity from the profiles
// of an already validated current lock. It performs no store or Docker I/O.
func buildLockSelectedSourcesV1(lock deploy.BuildLockV1) ([]providers.ResolvedSourceInput, error) {
	sources := []providers.ResolvedSourceInput{}
	for _, node := range lock.Nodes {
		selected, err := registry.RequirementProfileSelectedSourcesV1(node.Provider, node.RequirementProfile)
		if err != nil {
			return nil, fmt.Errorf("build lock node %q selected sources: %w", node.NodeID, err)
		}
		sources = append(sources, selected...)
	}
	sort.Slice(sources, func(left int, right int) bool {
		return compareResolvedSources(sources[left], sources[right]) < 0
	})
	for index := 1; index < len(sources); index++ {
		if compareResolvedSources(sources[index-1], sources[index]) >= 0 {
			return nil, fmt.Errorf("build lock selected sources must be unique")
		}
	}
	return sources, nil
}
