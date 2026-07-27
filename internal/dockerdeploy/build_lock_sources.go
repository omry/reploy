package dockerdeploy

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
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

// buildLockSelectedSourceWheelsV1 finds the exact wheel descriptors bound by a
// selected subset of the current lock's source identities. Bundle manifests
// are the descriptor authority; the provider store is verified before return.
func buildLockSelectedSourceWheelsV1(
	store providerstore.Store,
	lock deploy.BuildLockV1,
	sources []providers.ResolvedSourceInput,
) ([]providerstore.ArtifactDescriptor, error) {
	if sources == nil {
		return nil, fmt.Errorf("selected source wheels require a source array")
	}
	locked, err := buildLockSelectedSourcesV1(lock)
	if err != nil {
		return nil, err
	}
	lockedByKey := make(map[string]providers.ResolvedSourceInput, len(locked))
	for _, source := range locked {
		lockedByKey[source.Component+"\x00"+source.LogicalPackage] = source
	}
	required := make(map[canonical.Digest]struct{}, len(sources))
	seenSources := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		if err := pythonprovider.ValidateResolvedSourceInputV1(source); err != nil {
			return nil, err
		}
		key := source.Component + "\x00" + source.LogicalPackage
		if _, found := seenSources[key]; found {
			return nil, fmt.Errorf("selected source wheels contain duplicate %s.%s", source.Component, source.LogicalPackage)
		}
		seenSources[key] = struct{}{}
		if current, found := lockedByKey[key]; !found || !reflect.DeepEqual(current, source) {
			return nil, fmt.Errorf("selected source %s.%s is not an exact identity from the current lock", source.Component, source.LogicalPackage)
		}
		required[source.ArtifactDigest] = struct{}{}
	}

	byDigest := make(map[canonical.Digest]providerstore.ArtifactDescriptor, len(required))
	for _, node := range lock.Nodes {
		if node.Provider != blueprint.ComponentTypePython {
			continue
		}
		bundle, err := providers.LoadResolvedBundleManifest(store, node.BundleManifest, pythonprovider.ValidateResolvedBundlePayloadV1)
		if err != nil {
			return nil, fmt.Errorf("load current Python bundle for source wheels at node %q: %w", node.NodeID, err)
		}
		if err := validateLockedProviderBundle(node, bundle, pythonprovider.ValidateRequirementProfileV1); err != nil {
			return nil, fmt.Errorf("validate current Python bundle for source wheels at node %q: %w", node.NodeID, err)
		}
		for _, artifact := range bundle.Payload.Artifacts {
			if _, found := required[artifact.SHA256]; !found {
				continue
			}
			if current, found := byDigest[artifact.SHA256]; found && !reflect.DeepEqual(current, artifact) {
				return nil, fmt.Errorf("current source wheel %s has conflicting descriptors", artifact.SHA256)
			}
			byDigest[artifact.SHA256] = artifact
		}
	}
	wheels := make([]providerstore.ArtifactDescriptor, 0, len(byDigest))
	for _, wheel := range byDigest {
		wheels = append(wheels, wheel)
	}
	sort.Slice(wheels, func(left int, right int) bool { return wheels[left].LogicalPath < wheels[right].LogicalPath })
	if _, err := validateCurrentPythonSourceWheels(store, sources, wheels); err != nil {
		return nil, err
	}
	return wheels, nil
}
