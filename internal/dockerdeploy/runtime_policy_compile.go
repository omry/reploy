package dockerdeploy

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
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

// CompileRuntimePolicyV1 binds already-resolved runtime plans to the complete
// provider graph output/protected-root set.
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
	canonicalPlans := append([]deploy.RuntimePlanV1{}, plans...)
	for index := range canonicalPlans {
		inboundTCP, err := canonicalRuntimeInboundTCPV1(canonicalPlans[index].InboundTCP)
		if err != nil {
			return deploy.RuntimePolicyV1{}, fmt.Errorf("runtime plan %q inbound TCP grants: %w", canonicalPlans[index].ID, err)
		}
		canonicalPlans[index].InboundTCP = inboundTCP
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
	if err := validateRuntimePolicyInboundTCPV1(document, canonicalPlans); err != nil {
		return deploy.RuntimePolicyV1{}, err
	}
	policy := deploy.RuntimePolicyV1{
		Schema: deploy.RuntimePolicySchemaV1, StartupVerifier: deploy.ApplicationStartupVerifierContractV1(),
		Network:        normalizeRuntimeNetworkV1(document.Environment.Runtime.Network),
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

func canonicalRuntimeInboundTCPV1(values []string) ([]string, error) {
	ports := make([]int, 0, len(values))
	for _, value := range values {
		port, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("port %q is not a decimal integer", value)
		}
		ports = append(ports, port)
	}
	return deploy.CanonicalRuntimeInboundTCPV1(ports), nil
}

func validateRuntimePolicyInboundTCPV1(document blueprint.Document, plans []deploy.RuntimePlanV1) error {
	wantPorts := []int{}
	if workload := document.Environment.Workload; workload != nil {
		for _, endpoint := range workload.Endpoints {
			wantPorts = append(wantPorts, endpoint.Port)
		}
	}
	want := deploy.CanonicalRuntimeInboundTCPV1(wantPorts)
	for _, plan := range plans {
		planWant := []string{}
		if plan.ID == runtimeWorkloadPlanID {
			planWant = want
		}
		if !slices.Equal(plan.InboundTCP, planWant) {
			return fmt.Errorf("runtime plan %q inbound TCP grants do not match the resolved blueprint", plan.ID)
		}
	}
	return nil
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
		deploy.ApplicationStartupVerifierPathV1: {
			Path: deploy.ApplicationStartupVerifierPathV1, Kind: deploy.ProtectedPathExecutablePath, Owner: "reploy",
		},
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
			if err := rejectStartupVerifierExecutableCollision(owner, generated.Evidence.InvocationPath, generated.Evidence.LinkChain, generated.Evidence.Terminal.Path); err != nil {
				return nil, err
			}
			add(deploy.ProtectedPathV1{Path: declaration.ExclusiveRoot, Kind: deploy.ProtectedPathProviderLeaf, Owner: owner})
		}
	}
	for _, output := range catalog {
		if err := providers.ValidateRealizedOutput(output); err != nil {
			return nil, err
		}
		qualified := providers.QualifiedOutput{Component: output.SupplierComponent, Name: output.Name}
		owner := qualified.Component + "." + qualified.Name
		if err := rejectStartupVerifierExecutableCollision(owner, output.Evidence.InvocationPath, output.Evidence.LinkChain, output.Evidence.Terminal.Path); err != nil {
			return nil, err
		}
		if !referenced[qualified] {
			continue
		}
		addExecutable(owner, output.Evidence.InvocationPath, output.Evidence.LinkChain, output.Evidence.Terminal.Path)
	}
	result := make([]deploy.ProtectedPathV1, 0, len(paths))
	for _, item := range paths {
		result = append(result, item)
	}
	sort.Slice(result, func(left int, right int) bool { return result[left].Path < result[right].Path })
	return result, nil
}

func rejectStartupVerifierExecutableCollision(owner string, invocation string, links []providers.LinkEvidence, terminal string) error {
	paths := make([]string, 0, len(links)+2)
	paths = append(paths, invocation)
	for _, link := range links {
		paths = append(paths, link.Path)
	}
	paths = append(paths, terminal)
	for _, path := range paths {
		if runtimePolicyPathsOverlap(path, deploy.ApplicationStartupVerifierPathV1) {
			return fmt.Errorf(
				"runtime executable %q path %q overlaps reserved startup verifier %q",
				owner, path, deploy.ApplicationStartupVerifierPathV1,
			)
		}
	}
	return nil
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
	for _, plan := range policy.Plans {
		for index, mount := range plan.Mounts {
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

func runtimePolicyPathsOverlap(left string, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}
