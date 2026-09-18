package dockerdeploy

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

// planPortablePythonAliasesV1 derives the alias specs owned by one Python
// materialization. Planning runs before Docker build, so it validates the
// transaction shape here and only accepts the fixed Python materialization
// recipe and its exact generated output declarations.
//
// Facts are a small Reploy-owned envelope that binds the final-image probe to
// this exact alias claim. The materialization transaction owns generated
// executable declarations; it does not carry portable-tool output provenance,
// so the alias probe uses this independently validated claim envelope.
func planPortablePythonAliasesV1(
	component *pythonprovider.PortableToolPythonComponentV1,
	transaction providers.MaterializationTransaction,
) ([]portablePythonAliasSpecV1, error) {
	if component == nil {
		return nil, fmt.Errorf("portable Python alias component is required")
	}
	if err := validatePortablePythonAliasComponentV1(*component); err != nil {
		return nil, err
	}
	if err := providers.ValidateMaterializationTransaction(transaction); err != nil {
		return nil, fmt.Errorf("portable Python alias materialization transaction: %w", err)
	}
	if transaction.RecipeVersion != pythonprovider.MaterializationRecipeVersion {
		return nil, fmt.Errorf("portable Python alias transaction recipe must be %q", pythonprovider.MaterializationRecipeVersion)
	}
	expectedNodeID := portablePythonAliasNodeIDV1(component.Component)
	if transaction.NodeID != expectedNodeID {
		return nil, fmt.Errorf("portable Python alias transaction node %q does not own component %q", transaction.NodeID, component.Component)
	}
	runtimeRoot, err := pythonprovider.RuntimeRootV1(component.Component)
	if err != nil {
		return nil, fmt.Errorf("resolve portable Python alias runtime root: %w", err)
	}

	declarations := make(map[string]providers.GeneratedExecutableDeclaration, len(transaction.GeneratedExecutables))
	for _, declaration := range transaction.GeneratedExecutables {
		declarations[declaration.ID] = declaration
	}
	result := make([]portablePythonAliasSpecV1, 0, len(component.Bindings))
	for _, binding := range component.Bindings {
		name := binding.CLI.Name
		target := path.Join(runtimeRoot, "bin", name)
		declaration, found := declarations["output_"+name]
		if !found {
			return nil, fmt.Errorf("portable Python alias %q has no generated executable declaration", name)
		}
		if declaration.Path != target || declaration.ExclusiveRoot != runtimeRoot {
			return nil, fmt.Errorf("portable Python alias %q generated executable path %q does not equal %q", name, declaration.Path, target)
		}
		if err := validatePortablePythonAliasDestinationV1(binding.CLI.Path); err != nil {
			return nil, fmt.Errorf("portable Python alias %q destination: %w", name, err)
		}
		identity, err := portablePythonAliasBindingIdentityV1(binding)
		if err != nil {
			return nil, fmt.Errorf("portable Python alias %q binding identity: %w", name, err)
		}
		if pathsOverlapPortablePythonAliasV1(binding.CLI.Path, runtimeRoot) {
			return nil, fmt.Errorf("portable Python alias %q destination %q overlaps Python runtime root %q", name, binding.CLI.Path, runtimeRoot)
		}
		result = append(result, portablePythonAliasSpecV1{
			Name:            name,
			Destination:     binding.CLI.Path,
			Target:          target,
			BindingIdentity: identity,
			Output:          providers.QualifiedOutput{Component: component.Component, Name: name},
			Facts: providers.CanonicalProviderData{
				Schema: "portable-python-alias-v1",
				Value: canonical.Object{
					"binding_identity": identity,
					"target":           target,
				},
			},
		})
	}
	return validatePortablePythonAliasSpecClaimsV1(result)
}

// validatePortablePythonAliasSelectionClaimsV1 performs the selection-wide
// preflight available before individual materialization transactions exist.
// The effective portable-tool plan supplies selected runtime install roots and
// export claims; component bindings supply the exact aliases that will be
// published. Nil component pointers mean that the Python node has no selected
// portable binding and are ignored.
func validatePortablePythonAliasSelectionClaimsV1(
	plan providers.PortableToolPlanV1,
	components map[string]*pythonprovider.PortableToolPythonComponentV1,
) error {
	if err := providers.ValidatePortableToolPlanV1(plan); err != nil {
		return fmt.Errorf("portable Python alias selection plan: %w", err)
	}
	componentIDs := make([]string, 0, len(components))
	for componentID := range components {
		componentIDs = append(componentIDs, componentID)
	}
	sort.Strings(componentIDs)

	type selectedComponent struct {
		componentID string
		component   *pythonprovider.PortableToolPythonComponentV1
		runtimeRoot string
	}
	selected := make([]selectedComponent, 0, len(componentIDs))
	for _, componentID := range componentIDs {
		component := components[componentID]
		if component == nil {
			continue
		}
		if err := validatePortablePythonAliasComponentV1(*component); err != nil {
			return fmt.Errorf("portable Python alias selection %q: %w", componentID, err)
		}
		if componentID != component.Component {
			return fmt.Errorf("portable Python alias selection key %q does not own component %q", componentID, component.Component)
		}
		runtimeRoot, err := pythonprovider.RuntimeRootV1(component.Component)
		if err != nil {
			return fmt.Errorf("portable Python alias selection %q runtime root: %w", componentID, err)
		}
		selected = append(selected, selectedComponent{componentID: componentID, component: component, runtimeRoot: runtimeRoot})
	}

	type selectedExport struct {
		name, path, scope string
		closure           canonical.Digest
	}
	type selectedFilesystemPath struct {
		path, owner string
	}
	runtimeRoots := make([]string, 0, len(plan.Tools))
	selectedExports := make([]selectedExport, 0)
	selectedFilesystemPaths := make([]selectedFilesystemPath, 0)
	for _, tool := range plan.Tools {
		if tool.Runtime != nil {
			runtimeRoots = append(runtimeRoots, tool.Runtime.InstallRoot)
			selectedFilesystemPaths = append(selectedFilesystemPaths, selectedFilesystemPath{
				path: tool.Runtime.InstallRoot, owner: tool.Scope + "/" + tool.Provenance.Tool + " runtime",
			})
		}
		for _, exported := range tool.Exports {
			selectedExports = append(selectedExports, selectedExport{
				name: exported.Name, path: exported.Path, scope: tool.Scope,
				closure: tool.SelectedClosureDigest,
			})
		}
		for _, selected := range tool.Responsibilities.BindingArtifacts {
			selectedFilesystemPaths = append(selectedFilesystemPaths, selectedFilesystemPath{
				path:  portablePythonAliasFilesystemDestinationV1(selected),
				owner: tool.Scope + "/" + tool.Provenance.Tool + " binding artifact",
			})
		}
		for _, selected := range tool.Responsibilities.Payloads {
			selectedFilesystemPaths = append(selectedFilesystemPaths, selectedFilesystemPath{
				path:  portablePythonAliasPayloadFilesystemDestinationV1(tool, selected),
				owner: tool.Scope + "/" + tool.Provenance.Tool + " payload",
			})
		}
	}
	for _, current := range selected {
		// Python runtime roots are filesystem claims too, even though they are
		// derived from the component rather than carried by the portable plan.
		runtimeRoots = append(runtimeRoots, current.runtimeRoot)
		selectedFilesystemPaths = append(selectedFilesystemPaths, selectedFilesystemPath{
			path: current.runtimeRoot, owner: current.componentID + " Python runtime",
		})
	}

	for _, current := range selected {
		for _, root := range runtimeRoots {
			for _, binding := range current.component.Bindings {
				if pathsOverlapPortablePythonAliasV1(binding.CLI.Path, root) {
					return fmt.Errorf("portable Python alias destination %q overlaps selected Python runtime root %q", binding.CLI.Path, root)
				}
			}
		}
		for _, filesystemPath := range selectedFilesystemPaths {
			for _, binding := range current.component.Bindings {
				if pathsOverlapPortablePythonAliasV1(binding.CLI.Path, filesystemPath.path) {
					return fmt.Errorf("portable Python alias destination %q overlaps selected filesystem path %q (%s)", binding.CLI.Path, filesystemPath.path, filesystemPath.owner)
				}
			}
		}
	}

	type claim struct {
		target   string
		identity string
	}
	claims := make(map[string]claim)
	for _, current := range selected {
		for _, binding := range current.component.Bindings {
			identity, err := portablePythonAliasBindingIdentityV1(binding)
			if err != nil {
				return fmt.Errorf("portable Python alias %q binding identity: %w", binding.CLI.Name, err)
			}
			target := path.Join(current.runtimeRoot, "bin", binding.CLI.Name)
			ownedExport := false
			for _, exported := range selectedExports {
				if exported.scope == binding.Scope && exported.closure == binding.SelectedClosureDigest &&
					exported.name == binding.CLI.Name && exported.path == binding.CLI.Path {
					ownedExport = true
					break
				}
			}
			if !ownedExport {
				return fmt.Errorf("portable Python alias destination %q is not an exact selected export", binding.CLI.Path)
			}
			for _, exported := range selectedExports {
				if !pathsOverlapPortablePythonAliasV1(binding.CLI.Path, exported.path) {
					continue
				}
				if exported.scope != binding.Scope || exported.closure != binding.SelectedClosureDigest ||
					exported.name != binding.CLI.Name || exported.path != binding.CLI.Path {
					return fmt.Errorf("portable Python alias destination %q overlaps selected export %q", binding.CLI.Path, exported.path)
				}
			}
			previous, found := claims[binding.CLI.Path]
			if found {
				if previous.target != target || previous.identity != identity {
					return fmt.Errorf("portable Python alias destination %q has conflicting selected targets or binding identities", binding.CLI.Path)
				}
				// Exact target and binding identity is the only permitted replay.
				continue
			}
			for destination := range claims {
				if pathsOverlapPortablePythonAliasV1(binding.CLI.Path, destination) {
					return fmt.Errorf("portable Python alias destinations %q and %q overlap", binding.CLI.Path, destination)
				}
			}
			claims[binding.CLI.Path] = claim{target: target, identity: identity}
		}
	}
	return nil
}

func validatePortablePythonAliasComponentV1(component pythonprovider.PortableToolPythonComponentV1) error {
	projection := pythonprovider.PortableToolPythonProjectionV1{
		Schema:     pythonprovider.PortableToolPythonProjectionSchemaV1,
		Components: []pythonprovider.PortableToolPythonComponentV1{component},
	}
	if _, err := pythonprovider.CanonicalPortableToolPythonProjectionBytesV1(projection); err != nil {
		return fmt.Errorf("portable Python alias component %q: %w", component.Component, err)
	}
	return nil
}

func portablePythonAliasNodeIDV1(component string) providers.NodeID {
	owner, ok := blueprint.ApplicationContributionOwner(component, blueprint.ContributionProviderPython)
	if ok {
		return providers.NodeID("python/" + blueprint.ApplicationID(owner))
	}
	return providers.NodeID("python/" + component)
}

// portablePythonAliasBindingIdentityV1 is domain separated from other record
// identities and covers the complete canonical binding record. The digest is
// used as the opaque claim value so every provider-owned binding field affects
// destination ownership.
func portablePythonAliasBindingIdentityV1(binding pythonprovider.PortableToolPythonBindingV1) (string, error) {
	identity, err := canonical.Sum(
		"portable-python-binding", "portable-python-binding-v1", binding,
	)
	if err != nil {
		return "", err
	}
	return string(identity), nil
}

func pathsOverlapPortablePythonAliasV1(left, right string) bool {
	left, right = path.Clean(left), path.Clean(right)
	if left == "." || right == "." || left == "" || right == "" {
		return false
	}
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func validatePortablePythonAliasSpecClaimsV1(
	specs []portablePythonAliasSpecV1,
) ([]portablePythonAliasSpecV1, error) {
	type claim struct {
		target, identity string
	}
	claims := make(map[string]claim, len(specs))
	result := make([]portablePythonAliasSpecV1, 0, len(specs))
	for _, spec := range specs {
		previous, found := claims[spec.Destination]
		if found {
			if previous.target != spec.Target || previous.identity != spec.BindingIdentity {
				return nil, fmt.Errorf("portable Python alias destination %q has conflicting targets or binding identities", spec.Destination)
			}
			// Exact target and binding identity is the only supported deduplication.
			continue
		}
		for destination := range claims {
			if pathsOverlapPortablePythonAliasV1(spec.Destination, destination) {
				return nil, fmt.Errorf("portable Python alias destinations %q and %q overlap", spec.Destination, destination)
			}
		}
		claims[spec.Destination] = claim{target: spec.Target, identity: spec.BindingIdentity}
		result = append(result, spec)
	}
	return result, nil
}

// These two extraction helpers mirror the provider DAG's record-local
// filesystem destination rules. They intentionally remain local because the
// provider functions are package-private and this planner must not widen that
// API merely to inspect already validated plan data.
func portablePythonAliasFilesystemDestinationV1(record providers.PortableToolSelectedRecordV1) string {
	for _, field := range []string{"install_directory", "logical_path", "filename"} {
		if value, ok := record.Record.Value[field].(string); ok && value != "" {
			return value
		}
	}
	return record.Reference.ID
}

func portablePythonAliasPayloadFilesystemDestinationV1(
	entry providers.PortableToolPlanEntryV1,
	record providers.PortableToolSelectedRecordV1,
) string {
	destination, ok := record.Record.Value["install_directory"].(string)
	if !ok || destination == "" {
		destination = record.Reference.ID
	}
	if entry.Runtime != nil && !path.IsAbs(destination) {
		return path.Join(entry.Runtime.InstallRoot, destination)
	}
	return destination
}
