package dockerdeploy

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

var errCurrentPythonSourceWheelMissing = errors.New("current Python source wheel is missing")

// PreparedPythonGraphReuse contains only provider content reachable from the
// current deployment lock. The maps plug directly into GraphExecutionRequest
// and the per-node configs used by PreparePreparedPythonGraphBackend.
type PreparedPythonGraphReuse struct {
	ReusableArtifacts map[providers.NodeID][]providerstore.StoreObjectRef
	CachedResolutions map[providers.NodeID]providers.ResolveResult
	NodeConfigs       map[providers.NodeID]PreparedPythonNodeConfig
	APTNodeConfigs    map[providers.NodeID]PreparedAPTNodeConfig
}

// LoadPreparedPythonGraphReuse derives APT and Python cache candidates from one
// current build lock. It never scans older locks or unreferenced store blobs.
func LoadPreparedPythonGraphReuse(
	store providerstore.Store,
	plan providers.ProviderPlanV1,
	platform blueprint.Platform,
	sources []providers.ResolvedSourceInput,
	sourceWheels []providerstore.ArtifactDescriptor,
	current *deploy.BuildLockV1,
) (PreparedPythonGraphReuse, error) {
	reuse, nodes, nodeSources, err := emptyPreparedPythonGraphReuse(plan, platform, sources)
	if err != nil {
		return PreparedPythonGraphReuse{}, err
	}
	currentSourceWheels, err := validateCurrentPythonSourceWheels(store, sources, sourceWheels)
	if err != nil {
		return PreparedPythonGraphReuse{}, err
	}
	exclusiveRoots := plannedAPTExclusiveRoots(plan)
	for id, node := range nodes {
		switch node.Provider {
		case blueprint.ComponentTypeAPT:
			reuse.APTNodeConfigs[id] = PreparedAPTNodeConfig{ReusableDebs: []providerstore.ArtifactDescriptor{}, ExclusiveRoots: exclusiveRoots}
			reuse.ReusableArtifacts[id] = []providerstore.StoreObjectRef{}
		case blueprint.ComponentTypePython:
			wheels := sourceWheelsForNode(currentSourceWheels, nodeSources[id])
			reuse.NodeConfigs[id] = PreparedPythonNodeConfig{ReusableWheels: wheels}
			reuse.ReusableArtifacts[id] = artifactStoreReferences(wheels)
		}
	}
	if current == nil {
		return reuse, nil
	}
	if err := deploy.ValidateBuildLockV1(*current, registry.ValidateRequirementProfileV1); err != nil {
		return PreparedPythonGraphReuse{}, fmt.Errorf("load current provider build lock: %w", err)
	}
	if current.Platform != platform {
		return reuse, nil
	}

	locked := make(map[providers.NodeID]deploy.NodeLockV1, len(current.Nodes))
	for _, node := range current.Nodes {
		locked[node.NodeID] = node
	}
	for id, node := range nodes {
		lockedNode, found := locked[id]
		if !found || lockedNode.Provider != node.Provider {
			continue
		}
		validators, err := registry.OwnerValidatorsForNode(node)
		if err != nil {
			return PreparedPythonGraphReuse{}, err
		}
		bundle, err := providers.LoadResolvedBundleManifest(store, lockedNode.BundleManifest, validators.Bundle)
		if err != nil {
			return PreparedPythonGraphReuse{}, fmt.Errorf("load current provider bundle for node %q: %w", id, err)
		}
		if err := validateLockedProviderBundle(lockedNode, bundle, validators.Profile); err != nil {
			return PreparedPythonGraphReuse{}, fmt.Errorf("current provider bundle for node %q: %w", id, err)
		}
		compatible := true
		cacheNode := node
		switch node.Provider {
		case blueprint.ComponentTypeAPT:
			aptBundle, err := aptprovider.DecodeCanonicalBundleDataV1(bundle.Payload.ProviderPayload)
			if err != nil {
				return PreparedPythonGraphReuse{}, fmt.Errorf("decode current APT bundle for node %q: %w", id, err)
			}
			debs := reusableAPTArchives(store, aptBundle)
			reuse.APTNodeConfigs[id] = PreparedAPTNodeConfig{ReusableDebs: debs, ExclusiveRoots: exclusiveRoots}
			reuse.ReusableArtifacts[id] = artifactStoreReferences(debs)
		case blueprint.ComponentTypePython:
			component := node.Components[0]
			pythonBundle, err := pythonprovider.DecodeCanonicalBundleDataV1(component, bundle.Payload.ProviderPayload)
			if err != nil {
				return PreparedPythonGraphReuse{}, fmt.Errorf("decode current Python bundle for node %q: %w", id, err)
			}
			wheels := reusablePythonWheels(store, pythonBundle, nodeSources[id], currentSourceWheels)
			reuse.NodeConfigs[id] = PreparedPythonNodeConfig{ReusableWheels: wheels}
			reuse.ReusableArtifacts[id] = artifactStoreReferences(wheels)
			compatible = exactSelectedSourceCandidatesV1(nodeSources[id], pythonBundle.Sources)
			closure := make([]string, 0, len(pythonBundle.Wheels))
			for _, wheel := range pythonBundle.Wheels {
				closure = append(closure, wheel.Distribution)
			}
			sort.Strings(closure)
			cacheNode.Request, err = pythonprovider.FilterProviderRequestOverridesV1(node.Request, closure)
			if err != nil {
				return PreparedPythonGraphReuse{}, fmt.Errorf("project current Python request for node %q: %w", id, err)
			}
			cacheNode.Requirements.ProviderData = providers.CanonicalProviderData{
				Schema: cacheNode.Request.Schema, Value: cacheNode.Request.Value,
			}
		}

		planDigest, err := providers.ProviderNodePlanDigest(cacheNode)
		if err != nil {
			return PreparedPythonGraphReuse{}, err
		}
		if planDigest != lockedNode.PlanDigest || !compatible {
			continue
		}
		if !allBundleArtifactsPresent(store, bundle.Payload.Artifacts) {
			continue
		}
		reuse.CachedResolutions[id] = providers.ResolveResult{
			Bundle: bundle, Profile: lockedNode.RequirementProfile, Evidence: lockedNode.ValidationEvidence,
			SelectedSources: append([]providers.ResolvedSourceInput{}, bundle.Payload.SelectedSources...),
		}
	}
	return reuse, nil
}

func emptyPreparedPythonGraphReuse(
	plan providers.ProviderPlanV1,
	platform blueprint.Platform,
	sources []providers.ResolvedSourceInput,
) (PreparedPythonGraphReuse, map[providers.NodeID]providers.NodeSpec, map[providers.NodeID][]providers.ResolvedSourceInput, error) {
	if err := providers.ValidateProviderPlanV1(plan); err != nil {
		return PreparedPythonGraphReuse{}, nil, nil, err
	}
	if err := platform.Validate(); err != nil {
		return PreparedPythonGraphReuse{}, nil, nil, fmt.Errorf("load provider graph reuse platform: %w", err)
	}
	if sources == nil {
		return PreparedPythonGraphReuse{}, nil, nil, fmt.Errorf("load Python graph reuse sources must use an array")
	}
	reuse := PreparedPythonGraphReuse{
		ReusableArtifacts: map[providers.NodeID][]providerstore.StoreObjectRef{},
		CachedResolutions: map[providers.NodeID]providers.ResolveResult{},
		NodeConfigs:       map[providers.NodeID]PreparedPythonNodeConfig{},
		APTNodeConfigs:    map[providers.NodeID]PreparedAPTNodeConfig{},
	}
	nodes := map[providers.NodeID]providers.NodeSpec{}
	componentNodes := map[string]providers.NodeID{}
	nodeSources := map[providers.NodeID][]providers.ResolvedSourceInput{}
	for _, node := range plan.Nodes {
		if node.ID == "base" {
			continue
		}
		if node.Provider != blueprint.ComponentTypePython && node.Provider != blueprint.ComponentTypeAPT {
			return PreparedPythonGraphReuse{}, nil, nil, fmt.Errorf("prepared provider reuse does not support provider %q for node %q", node.Provider, node.ID)
		}
		if node.Provider == blueprint.ComponentTypePython && len(node.Components) != 1 {
			return PreparedPythonGraphReuse{}, nil, nil, fmt.Errorf("prepared Python node %q must identify one component", node.ID)
		}
		nodes[node.ID] = node
		if node.Provider == blueprint.ComponentTypePython {
			componentNodes[node.Components[0]] = node.ID
			nodeSources[node.ID] = []providers.ResolvedSourceInput{}
			reuse.NodeConfigs[node.ID] = PreparedPythonNodeConfig{ReusableWheels: []providerstore.ArtifactDescriptor{}}
		} else {
			reuse.APTNodeConfigs[node.ID] = PreparedAPTNodeConfig{ReusableDebs: []providerstore.ArtifactDescriptor{}, ExclusiveRoots: []string{}}
		}
		reuse.ReusableArtifacts[node.ID] = []providerstore.StoreObjectRef{}
	}
	for index, source := range sources {
		if err := providers.ValidateResolvedSourceInput(source); err != nil {
			return PreparedPythonGraphReuse{}, nil, nil, fmt.Errorf("load Python graph reuse source %d: %w", index, err)
		}
		if index > 0 && compareResolvedSources(sources[index-1], source) >= 0 {
			return PreparedPythonGraphReuse{}, nil, nil, fmt.Errorf("load Python graph reuse sources must be unique and sorted")
		}
		id, found := componentNodes[source.Component]
		if !found {
			return PreparedPythonGraphReuse{}, nil, nil, fmt.Errorf("load Python graph reuse source targets missing Python component %q", source.Component)
		}
		nodeSources[id] = append(nodeSources[id], source)
	}
	return reuse, nodes, nodeSources, nil
}

func validateLockedProviderBundle(
	node deploy.NodeLockV1,
	bundle providers.ResolvedBundle,
	validateProfile providers.RequirementProfileOwnerValidator,
) error {
	profileDigest, err := providers.RequirementProfileDigest(node.RequirementProfile, validateProfile)
	if err != nil {
		return err
	}
	payload := bundle.Payload
	if payload.NodeID != node.NodeID || payload.Provider != node.Provider {
		return fmt.Errorf("manifest does not identify its locked node")
	}
	if payload.Platform != node.RequirementProfile.Platform || payload.Upstream != node.Upstream {
		return fmt.Errorf("manifest does not identify its locked platform and upstream image")
	}
	if payload.RequirementProfileDigest != profileDigest {
		return fmt.Errorf("manifest does not identify its locked requirement profile")
	}
	return nil
}

func reusablePythonWheels(
	store providerstore.Store,
	bundle pythonprovider.PythonBundleV1,
	currentSources []providers.ResolvedSourceInput,
	currentSourceWheels map[canonical.Digest]providerstore.ArtifactDescriptor,
) []providerstore.ArtifactDescriptor {
	current := make(map[string]providers.ResolvedSourceInput, len(currentSources))
	for _, source := range currentSources {
		current[source.Component+"\x00"+source.LogicalPackage] = source
	}
	oldLocal := make(map[string][]providers.ResolvedSourceInput, len(bundle.Sources))
	for _, source := range bundle.Sources {
		oldLocal[string(source.ArtifactDigest)] = append(oldLocal[string(source.ArtifactDigest)], source)
	}
	wheelsByDigest := make(map[canonical.Digest]providerstore.ArtifactDescriptor, len(bundle.Wheels)+len(currentSources))
	for _, source := range currentSources {
		wheelsByDigest[source.ArtifactDigest] = currentSourceWheels[source.ArtifactDigest]
	}
	for _, wheel := range bundle.Wheels {
		if oldSources := oldLocal[string(wheel.Artifact.SHA256)]; len(oldSources) > 0 {
			matches := false
			for _, old := range oldSources {
				if value, found := current[old.Component+"\x00"+old.LogicalPackage]; found && reflect.DeepEqual(value, old) {
					matches = true
					break
				}
			}
			if !matches {
				continue
			}
		}
		if _, err := store.InspectArtifactPath(wheel.Artifact); err != nil {
			continue
		}
		if _, found := wheelsByDigest[wheel.Artifact.SHA256]; !found {
			wheelsByDigest[wheel.Artifact.SHA256] = wheel.Artifact
		}
	}
	wheels := make([]providerstore.ArtifactDescriptor, 0, len(wheelsByDigest))
	for _, wheel := range wheelsByDigest {
		wheels = append(wheels, wheel)
	}
	sort.Slice(wheels, func(left int, right int) bool { return wheels[left].LogicalPath < wheels[right].LogicalPath })
	return wheels
}

func reusableAPTArchives(store providerstore.Store, bundle aptprovider.BundleV1) []providerstore.ArtifactDescriptor {
	debs := make([]providerstore.ArtifactDescriptor, 0, len(bundle.BundlePackages))
	for _, pkg := range bundle.BundlePackages {
		if _, err := store.InspectArtifactPath(pkg.Artifact); err == nil {
			debs = append(debs, pkg.Artifact)
		}
	}
	sort.Slice(debs, func(left int, right int) bool { return debs[left].LogicalPath < debs[right].LogicalPath })
	return debs
}

func plannedAPTExclusiveRoots(plan providers.ProviderPlanV1) []string {
	roots := []string{}
	for _, node := range plan.Nodes {
		if node.Provider != blueprint.ComponentTypePython {
			continue
		}
		for _, component := range node.Components {
			roots = append(roots, path.Join(pythonprovider.InstallRoot, component))
		}
	}
	sort.Strings(roots)
	return roots
}

func validateCurrentPythonSourceWheels(
	store providerstore.Store,
	sources []providers.ResolvedSourceInput,
	wheels []providerstore.ArtifactDescriptor,
) (map[canonical.Digest]providerstore.ArtifactDescriptor, error) {
	if wheels == nil && len(sources) != 0 {
		return nil, fmt.Errorf("current Python source wheels are required when resolved sources exist")
	}
	wheelsByDigest := make(map[canonical.Digest]providerstore.ArtifactDescriptor, len(wheels))
	for _, wheel := range wheels {
		filename := filepath.Base(filepath.FromSlash(wheel.LogicalPath))
		if err := wheel.Validate(); err != nil {
			return nil, fmt.Errorf("current Python source wheel: %w", err)
		}
		if wheel.Kind != "wheel" || !strings.HasSuffix(strings.ToLower(filename), ".whl") {
			return nil, fmt.Errorf("current Python source artifact %q must be a wheel", wheel.LogicalPath)
		}
		if _, found := wheelsByDigest[wheel.SHA256]; found {
			return nil, fmt.Errorf("current Python source wheels must have unique digests")
		}
		if _, err := store.InspectArtifactPath(wheel); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("%w: inspect %q: %w", errCurrentPythonSourceWheelMissing, wheel.LogicalPath, err)
			}
			return nil, fmt.Errorf("inspect current Python source wheel %q: %w", wheel.LogicalPath, err)
		}
		wheelsByDigest[wheel.SHA256] = wheel
	}
	required := make(map[canonical.Digest]bool, len(sources))
	for _, source := range sources {
		if _, found := wheelsByDigest[source.ArtifactDigest]; !found {
			return nil, fmt.Errorf("current Python source %s.%s has no wheel descriptor for %s", source.Component, source.LogicalPackage, source.ArtifactDigest)
		}
		required[source.ArtifactDigest] = true
	}
	for digest := range wheelsByDigest {
		if !required[digest] {
			return nil, fmt.Errorf("current Python source wheel %s does not match a resolved source", digest)
		}
	}
	return wheelsByDigest, nil
}

func sourceWheelsForNode(
	wheels map[canonical.Digest]providerstore.ArtifactDescriptor,
	sources []providers.ResolvedSourceInput,
) []providerstore.ArtifactDescriptor {
	result := make([]providerstore.ArtifactDescriptor, 0, len(sources))
	seen := map[canonical.Digest]bool{}
	for _, source := range sources {
		if !seen[source.ArtifactDigest] {
			result = append(result, wheels[source.ArtifactDigest])
			seen[source.ArtifactDigest] = true
		}
	}
	sort.Slice(result, func(left int, right int) bool { return result[left].LogicalPath < result[right].LogicalPath })
	return result
}

func artifactStoreReferences(artifacts []providerstore.ArtifactDescriptor) []providerstore.StoreObjectRef {
	unique := map[providerstore.StoreObjectRef]struct{}{}
	for _, artifact := range artifacts {
		reference, err := artifact.StoreObjectRef()
		if err == nil {
			unique[reference] = struct{}{}
		}
	}
	references := make([]providerstore.StoreObjectRef, 0, len(unique))
	for reference := range unique {
		references = append(references, reference)
	}
	sort.Slice(references, func(left int, right int) bool {
		if references[left].Kind != references[right].Kind {
			return references[left].Kind < references[right].Kind
		}
		return references[left].Digest < references[right].Digest
	})
	return references
}

func allBundleArtifactsPresent(store providerstore.Store, artifacts []providerstore.ArtifactDescriptor) bool {
	for _, artifact := range artifacts {
		if _, err := store.InspectArtifactPath(artifact); err != nil {
			return false
		}
	}
	return true
}

func compareResolvedSources(left providers.ResolvedSourceInput, right providers.ResolvedSourceInput) int {
	if left.Component != right.Component {
		return bytes.Compare([]byte(left.Component), []byte(right.Component))
	}
	return bytes.Compare([]byte(left.LogicalPackage), []byte(right.LogicalPackage))
}
