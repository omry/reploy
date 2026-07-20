package dockerdeploy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
)

type runtimeProtectedMaterialization struct {
	NodeID               providers.NodeID
	GeneratedExecutables []providers.RealizedGeneratedExecutable
}

// CompileRuntimePolicyV1 binds already-resolved runtime plans to the blueprint
// mount allowlist and the complete provider graph output/protected-root set.
func CompileRuntimePolicyV1(
	document blueprint.Document,
	graph providers.GraphExecutionResult,
	plans []deploy.RuntimePlanV1,
) (deploy.RuntimePolicyV1, error) {
	protected, err := providerGraphProtectedPaths(graph, plans)
	if err != nil {
		return deploy.RuntimePolicyV1{}, err
	}
	return compileRuntimePolicyV1(document, graph.Catalog, protected, plans)
}

// CompileRuntimePolicyFromLockV1 performs the same policy compilation using
// only canonical lock contents. Runtime staleness checks therefore do not need
// provider bundles, artifacts, or Docker inspection.
func CompileRuntimePolicyFromLockV1(
	document blueprint.Document,
	lock deploy.BuildLockV1,
	plans []deploy.RuntimePlanV1,
) (deploy.RuntimePolicyV1, error) {
	if err := deploy.ValidateBuildLockV1(lock, registry.ValidateRequirementProfileV1); err != nil {
		return deploy.RuntimePolicyV1{}, err
	}
	materializations := make([]runtimeProtectedMaterialization, 0, len(lock.Nodes))
	for _, node := range lock.Nodes {
		materializations = append(materializations, runtimeProtectedMaterialization{
			NodeID: node.NodeID, GeneratedExecutables: append([]providers.RealizedGeneratedExecutable{}, node.GeneratedExecutables...),
		})
	}
	protected, err := runtimeProtectedPaths(materializations, lock.Catalog, runtimeReferencedOutputs(plans))
	if err != nil {
		return deploy.RuntimePolicyV1{}, err
	}
	return compileRuntimePolicyV1(document, lock.Catalog, protected, plans)
}

func compileRuntimePolicyV1(
	document blueprint.Document,
	catalog []providers.RealizedOutput,
	protected []deploy.ProtectedPathV1,
	plans []deploy.RuntimePlanV1,
) (deploy.RuntimePolicyV1, error) {
	allowedRoots := append([]string{"/mnt"}, document.Docker.AdditionalMountRoots...)
	sort.Strings(allowedRoots)
	canonicalPlans := append([]deploy.RuntimePlanV1{}, plans...)
	for index := range canonicalPlans {
		canonicalPlans[index].Mounts = append([]deploy.RuntimeMountV1{}, canonicalPlans[index].Mounts...)
		canonicalPlans[index].Executables = append([]providers.QualifiedOutput{}, canonicalPlans[index].Executables...)
		sort.Slice(canonicalPlans[index].Mounts, func(left int, right int) bool {
			return canonicalPlans[index].Mounts[left].Destination < canonicalPlans[index].Mounts[right].Destination
		})
		sort.Slice(canonicalPlans[index].Executables, func(left int, right int) bool {
			leftOutput := canonicalPlans[index].Executables[left]
			rightOutput := canonicalPlans[index].Executables[right]
			if leftOutput.Component != rightOutput.Component {
				return leftOutput.Component < rightOutput.Component
			}
			return leftOutput.Name < rightOutput.Name
		})
	}
	sort.Slice(canonicalPlans, func(left int, right int) bool { return canonicalPlans[left].ID < canonicalPlans[right].ID })
	policy := deploy.RuntimePolicyV1{
		Schema: deploy.RuntimePolicySchemaV1, AllowedRoots: allowedRoots,
		ProtectedPaths: protected, Plans: canonicalPlans,
	}
	if err := deploy.ValidateRuntimePolicyV1(policy); err != nil {
		return deploy.RuntimePolicyV1{}, err
	}
	if err := validateRuntimePolicyExecutables(policy, catalog); err != nil {
		return deploy.RuntimePolicyV1{}, err
	}
	if err := validateRuntimePolicyOverlays(policy); err != nil {
		return deploy.RuntimePolicyV1{}, err
	}
	return policy, nil
}

func providerGraphProtectedPaths(graph providers.GraphExecutionResult, plans []deploy.RuntimePlanV1) ([]deploy.ProtectedPathV1, error) {
	if graph.Materializations == nil || graph.Bundles == nil || graph.Catalog == nil {
		return nil, fmt.Errorf("runtime policy graph collections must use arrays")
	}
	if len(graph.Materializations) != len(graph.Bundles) {
		return nil, fmt.Errorf("runtime policy graph materializations and bundles do not align")
	}
	materializations := make([]runtimeProtectedMaterialization, 0, len(graph.Materializations))
	for index, materialized := range graph.Materializations {
		materializations = append(materializations, runtimeProtectedMaterialization{
			NodeID: graph.Bundles[index].Payload.NodeID, GeneratedExecutables: materialized.GeneratedExecutables,
		})
	}
	return runtimeProtectedPaths(materializations, graph.Catalog, runtimeReferencedOutputs(plans))
}

func runtimeReferencedOutputs(plans []deploy.RuntimePlanV1) map[providers.QualifiedOutput]bool {
	result := map[providers.QualifiedOutput]bool{}
	for _, plan := range plans {
		for _, output := range plan.Executables {
			result[output] = true
		}
	}
	return result
}

func runtimeProtectedPaths(
	materializations []runtimeProtectedMaterialization,
	catalog []providers.RealizedOutput,
	referenced map[providers.QualifiedOutput]bool,
) ([]deploy.ProtectedPathV1, error) {
	paths := map[string]deploy.ProtectedPathV1{
		deploy.ReployImageRoot:    {Path: deploy.ReployImageRoot, Kind: deploy.ProtectedPathReployRoot, Owner: "reploy"},
		deploy.ReployProviderRoot: {Path: deploy.ReployProviderRoot, Kind: deploy.ProtectedPathProviderRoot, Owner: "reploy"},
	}
	add := func(item deploy.ProtectedPathV1) {
		current, found := paths[item.Path]
		if !found || current.Kind == deploy.ProtectedPathExecutablePath && item.Kind != deploy.ProtectedPathExecutablePath || current.Kind == item.Kind && item.Owner < current.Owner {
			paths[item.Path] = item
		}
	}
	addExecutable := func(owner string, invocation string, links []providers.LinkEvidence, terminal string) {
		add(deploy.ProtectedPathV1{Path: invocation, Kind: deploy.ProtectedPathExecutablePath, Owner: owner})
		for _, link := range links {
			add(deploy.ProtectedPathV1{Path: link.Path, Kind: deploy.ProtectedPathExecutablePath, Owner: owner})
		}
		add(deploy.ProtectedPathV1{Path: terminal, Kind: deploy.ProtectedPathExecutablePath, Owner: owner})
	}
	for _, materialized := range materializations {
		if err := providers.ValidateRealizedGeneratedExecutableCollection(materialized.GeneratedExecutables); err != nil {
			return nil, err
		}
		for _, generated := range materialized.GeneratedExecutables {
			declaration := generated.Declaration
			owner := string(materialized.NodeID) + "." + declaration.ID
			add(deploy.ProtectedPathV1{Path: declaration.ExclusiveRoot, Kind: deploy.ProtectedPathProviderLeaf, Owner: owner})
		}
	}
	for _, output := range catalog {
		if err := providers.ValidateRealizedOutput(output); err != nil {
			return nil, err
		}
		qualified := providers.QualifiedOutput{Component: output.SupplierComponent, Name: output.Name}
		if !referenced[qualified] {
			continue
		}
		owner := qualified.Component + "." + qualified.Name
		addExecutable(owner, output.Evidence.InvocationPath, output.Evidence.LinkChain, output.Evidence.Terminal.Path)
	}
	result := make([]deploy.ProtectedPathV1, 0, len(paths))
	for _, item := range paths {
		result = append(result, item)
	}
	sort.Slice(result, func(left int, right int) bool { return result[left].Path < result[right].Path })
	return result, nil
}

func validateRuntimePolicyExecutables(policy deploy.RuntimePolicyV1, catalog []providers.RealizedOutput) error {
	available := make(map[providers.QualifiedOutput]bool, len(catalog))
	for _, output := range catalog {
		qualified := providers.QualifiedOutput{Component: output.SupplierComponent, Name: output.Name}
		if available[qualified] {
			return fmt.Errorf("runtime policy graph contains duplicate output %s.%s", qualified.Component, qualified.Name)
		}
		available[qualified] = true
	}
	for _, plan := range policy.Plans {
		for _, output := range plan.Executables {
			if !available[output] {
				return fmt.Errorf("runtime plan %q executable %s.%s is absent from the final provider graph", plan.ID, output.Component, output.Name)
			}
		}
	}
	return nil
}

func validateRuntimePolicyOverlays(policy deploy.RuntimePolicyV1) error {
	for _, root := range policy.AllowedRoots {
		if root == "/mnt" {
			continue
		}
		for _, protected := range policy.ProtectedPaths {
			if protected.Kind == deploy.ProtectedPathExecutablePath {
				continue
			}
			if runtimePolicyPathsOverlap(root, protected.Path) {
				return fmt.Errorf("runtime allowed root %q overlaps protected %s %q", root, protected.Kind, protected.Path)
			}
		}
	}
	for _, plan := range policy.Plans {
		for index, mount := range plan.Mounts {
			if !runtimeMountDestinationAllowed(mount.Destination, policy.AllowedRoots) {
				return fmt.Errorf("runtime plan %q mount destination %q is outside allowed roots", plan.ID, mount.Destination)
			}
			for _, previous := range plan.Mounts[:index] {
				if runtimePolicyPathsOverlap(previous.Destination, mount.Destination) {
					return fmt.Errorf("runtime plan %q mount destinations %q and %q overlap", plan.ID, previous.Destination, mount.Destination)
				}
			}
			for _, protected := range policy.ProtectedPaths {
				if runtimePolicyPathsOverlap(mount.Destination, protected.Path) {
					return fmt.Errorf("runtime plan %q mount destination %q overlaps protected %s %q", plan.ID, mount.Destination, protected.Kind, protected.Path)
				}
			}
		}
	}
	return nil
}

func runtimeMountDestinationAllowed(destination string, roots []string) bool {
	for _, root := range roots {
		if root == "/mnt" {
			if strings.HasPrefix(destination, "/mnt/") {
				return true
			}
			continue
		}
		if destination == root || strings.HasPrefix(destination, root+"/") {
			return true
		}
	}
	return false
}

func runtimePolicyPathsOverlap(left string, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}
