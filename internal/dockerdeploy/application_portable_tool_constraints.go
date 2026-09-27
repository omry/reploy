package dockerdeploy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/toolcatalog"
)

// applicationOrdinaryProviderConstraintsV1 snapshots active ordinary roots
// and executable claims from canonical requests, not the unselected blueprint
// option declarations. The selected portable contributions are added by the
// catalog solver and the provider-owned projections after selection.
func applicationOrdinaryProviderConstraintsV1(
	scopes []string,
	components []providers.ResolvedComponentRequestV1,
	plan providers.ProviderPlanV1,
	config providers.ImageConfigPolicy,
) (toolcatalog.ActiveProviderConstraintsV1, error) {
	active := toolcatalog.ActiveProviderConstraintsV1{
		Schema:  toolcatalog.ActiveProviderConstraintsSchemaV1,
		Sources: []toolcatalog.ActiveProviderConstraintSourceV1{},
	}
	ordinaryExportsByScope := make(map[string]map[string]toolcatalog.ToolExportV1)
	ordinaryOwnedPathsByScope := make(map[string]map[string]struct{})
	for _, scope := range scopes {
		name := strings.TrimPrefix(scope, "application:")
		exportsByName := ordinaryExportsByScope[scope]
		if exportsByName == nil {
			exportsByName = make(map[string]toolcatalog.ToolExportV1)
			ordinaryExportsByScope[scope] = exportsByName
		}
		ownedPaths := ordinaryOwnedPathsByScope[scope]
		if ownedPaths == nil {
			ownedPaths = make(map[string]struct{})
			ordinaryOwnedPathsByScope[scope] = ownedPaths
		}
		imageSource := emptyApplicationOrdinaryConstraintV1(scope, "final-image")
		imageSource.Source.Kind = "image-config"
		for _, variable := range config.Environment {
			imageSource.Environment = append(imageSource.Environment, toolcatalog.RecordEnvironmentVariableV1{
				Name: variable.Name, Value: variable.Value,
			})
		}
		if len(imageSource.Environment) != 0 {
			active.Sources = append(active.Sources, imageSource)
		}
		for _, component := range components {
			if !applicationOrdinaryComponentAppliesV1(name, component.Component) {
				continue
			}
			source := emptyApplicationOrdinaryConstraintV1(scope, "component/"+component.Component)
			switch component.Provider {
			case blueprint.ComponentTypeAPT:
				if err := appendOrdinaryAPTClaimsV1(&source, component); err != nil {
					return active, fmt.Errorf("ordinary APT contribution %q: %w", component.Component, err)
				}
			case blueprint.ComponentTypePython:
				if err := appendOrdinaryPythonClaimsV1(&source, component); err != nil {
					return active, fmt.Errorf("ordinary Python contribution %q: %w", component.Component, err)
				}
			case blueprint.ComponentTypeBase:
				// The base node's concrete executable claims are projected below.
			default:
				return active, fmt.Errorf("ordinary contribution %q uses unsupported provider %q", component.Component, component.Provider)
			}
			if len(source.NativePackages)+len(source.PythonBindings)+len(source.InstallRoots)+len(source.Exports)+len(source.Capabilities) != 0 {
				active.Sources = append(active.Sources, source)
			}
		}
		for _, node := range plan.Nodes {
			source := emptyApplicationOrdinaryConstraintV1(scope, "node/"+string(node.ID))
			for _, output := range node.OutputDeclarations {
				if !applicationOrdinaryComponentAppliesV1(name, output.SupplierComponent) || output.Kind != providers.OutputKindExecutable {
					continue
				}
				if previous, found := exportsByName[output.Name]; found {
					if previous.Path != output.CandidatePath {
						return active, fmt.Errorf("ordinary executable export %q has conflicting paths %q and %q", output.Name, previous.Path, output.CandidatePath)
					}
					// Merged provider nodes can repeat one canonical executable
					// declaration. Keep one deterministic claim for the shared image.
					continue
				}
				claim := toolcatalog.ToolExportV1{Name: output.Name, Path: output.CandidatePath}
				exportsByName[output.Name] = claim
				source.Exports = append(source.Exports, claim)
				source.Capabilities = append(source.Capabilities, claim)
				if _, found := ownedPaths[claim.Path]; !found {
					digest, err := ordinaryExecutablePathDigestV1(claim)
					if err != nil {
						return active, fmt.Errorf("ordinary executable %q provenance: %w", output.Name, err)
					}
					source.OwnedPaths = append(source.OwnedPaths, toolcatalog.ActiveFilesystemConstraintV1{
						Path: claim.Path, Digest: digest,
					})
					ownedPaths[claim.Path] = struct{}{}
				}
			}
			if len(source.Exports) != 0 {
				sort.Slice(source.Exports, func(i, j int) bool { return source.Exports[i].Name < source.Exports[j].Name })
				sort.Slice(source.Capabilities, func(i, j int) bool { return source.Capabilities[i].Name < source.Capabilities[j].Name })
				sort.Slice(source.OwnedPaths, func(i, j int) bool {
					leftBytes, _ := canonical.Marshal(source.OwnedPaths[i])
					rightBytes, _ := canonical.Marshal(source.OwnedPaths[j])
					return string(leftBytes) < string(rightBytes)
				})
				active.Sources = append(active.Sources, source)
			}
		}
	}
	sort.Slice(active.Sources, func(i, j int) bool {
		left, right := active.Sources[i], active.Sources[j]
		if left.Scope != right.Scope {
			return left.Scope < right.Scope
		}
		if left.Source.Kind != right.Source.Kind {
			return left.Source.Kind < right.Source.Kind
		}
		return left.Source.ID < right.Source.ID
	})
	return active, nil
}

func emptyApplicationOrdinaryConstraintV1(scope, id string) toolcatalog.ActiveProviderConstraintSourceV1 {
	return toolcatalog.ActiveProviderConstraintSourceV1{
		Scope: scope, Source: toolcatalog.ConstraintSourceV1{Kind: "provider-plan", ID: id},
		NativePackages: []toolcatalog.ActiveNativePackageConstraintV1{},
		PythonBindings: []toolcatalog.ActivePythonBindingConstraintV1{},
		InstallRoots:   []string{}, OwnedPaths: []toolcatalog.ActiveFilesystemConstraintV1{},
		Artifacts:   []toolcatalog.ActiveFilesystemConstraintV1{},
		Environment: []toolcatalog.RecordEnvironmentVariableV1{},
		Exports:     []toolcatalog.ToolExportV1{}, Capabilities: []toolcatalog.ToolExportV1{},
	}
}

func applicationOrdinaryComponentAppliesV1(application, component string) bool {
	return component == "base" || strings.HasPrefix(component, "environment/") ||
		strings.HasPrefix(component, blueprint.ApplicationID(application)+"/")
}

func appendOrdinaryAPTClaimsV1(source *toolcatalog.ActiveProviderConstraintSourceV1, component providers.ResolvedComponentRequestV1) error {
	if err := aptprovider.ValidateCanonicalProviderRequestForComponentV1(component.Component, component.Request); err != nil {
		return err
	}
	items, ok := component.Request.Value["components"].([]any)
	if !ok || len(items) != 1 {
		return fmt.Errorf("canonical APT request must contain one component")
	}
	item, ok := ordinaryCanonicalObjectV1(items[0])
	if !ok {
		return fmt.Errorf("canonical APT component is not an object")
	}
	packages, ok := item["packages"].([]any)
	if !ok {
		return fmt.Errorf("canonical APT packages must use an array")
	}
	requirements := []string{}
	for _, raw := range packages {
		entry, ok := ordinaryCanonicalObjectV1(raw)
		if !ok {
			return fmt.Errorf("canonical APT package is not an object")
		}
		value, ok := ordinaryCanonicalObjectV1(entry["value"])
		if !ok {
			return fmt.Errorf("canonical APT package value is not an object")
		}
		pkg, err := aptprovider.DecodeCanonicalPackageRequestV1(providers.CanonicalPackageRequest{Schema: aptprovider.PackageRequestSchemaV1, Value: value})
		if err != nil {
			return err
		}
		requirement := pkg.Name
		if pkg.Version != "" {
			requirement += "=" + pkg.Version
		}
		requirements = append(requirements, requirement)
	}
	sort.Strings(requirements)
	source.NativePackages = append(source.NativePackages, toolcatalog.ActiveNativePackageConstraintV1{
		Manager: "apt", Requirements: requirements, Repositories: []string{},
	})
	return nil
}

func appendOrdinaryPythonClaimsV1(source *toolcatalog.ActiveProviderConstraintSourceV1, component providers.ResolvedComponentRequestV1) error {
	if err := pythonprovider.ValidateCanonicalProviderRequestForComponentV1(component.Component, component.Request); err != nil {
		return err
	}
	runtimeRoot, err := pythonprovider.RuntimeRootV1(component.Component)
	if err != nil {
		return err
	}
	source.InstallRoots = append(source.InstallRoots, runtimeRoot)
	items, ok := component.Request.Value["requirements"].([]any)
	if !ok {
		return fmt.Errorf("canonical Python requirements must use an array")
	}
	requirements := []string{}
	for _, raw := range items {
		entry, ok := ordinaryCanonicalObjectV1(raw)
		if !ok {
			return fmt.Errorf("canonical Python package is not an object")
		}
		value, ok := ordinaryCanonicalObjectV1(entry["value"])
		if !ok {
			return fmt.Errorf("canonical Python package value is not an object")
		}
		requirement, ok := value["requirement"].(string)
		if !ok {
			return fmt.Errorf("canonical Python requirement is not text")
		}
		requirements = append(requirements, requirement)
	}
	sort.Strings(requirements)
	// One ordinary request can contain multiple compatible constraints on the
	// same distribution. Keep each root as its own claim so the solver checks
	// their intersection without treating them as a duplicate declaration.
	for _, requirement := range requirements {
		source.PythonBindings = append(source.PythonBindings, toolcatalog.ActivePythonBindingConstraintV1{
			Name: "python", Requirements: []string{requirement}, SupportedPython: []string{},
		})
	}
	interpreter, ok := ordinaryCanonicalObjectV1(component.Request.Value["interpreter"])
	if !ok {
		return fmt.Errorf("canonical Python interpreter is not an object")
	}
	if version, ok := interpreter["version"].(string); ok && version != "" {
		if len(source.PythonBindings) == 0 {
			source.PythonBindings = append(source.PythonBindings, toolcatalog.ActivePythonBindingConstraintV1{
				Name: "python", Requirements: []string{}, SupportedPython: []string{},
			})
		}
		source.PythonBindings[0].InterpreterConstraints = []string{version}
	}
	overrides, ok := component.Request.Value["overrides"].([]any)
	if !ok {
		return fmt.Errorf("canonical Python overrides must use an array")
	}
	if len(overrides) != 0 {
		if len(source.PythonBindings) == 0 {
			source.PythonBindings = append(source.PythonBindings, toolcatalog.ActivePythonBindingConstraintV1{
				Name: "python", Requirements: []string{}, SupportedPython: []string{},
			})
		}
		for index, raw := range overrides {
			entry, ok := ordinaryCanonicalObjectV1(raw)
			if !ok {
				return fmt.Errorf("canonical Python override %d is not an object", index)
			}
			distribution, _ := entry["distribution"].(string)
			kind, _ := entry["kind"].(string)
			version, _ := entry["version"].(string)
			source.PythonBindings[0].Overrides = append(source.PythonBindings[0].Overrides,
				toolcatalog.ActivePythonOverrideConstraintV1{Distribution: distribution, Kind: kind, Version: version})
		}
	}
	if len(source.PythonBindings) > 1 {
		sort.Slice(source.PythonBindings, func(left, right int) bool {
			leftBytes, _ := canonical.Marshal(source.PythonBindings[left])
			rightBytes, _ := canonical.Marshal(source.PythonBindings[right])
			return string(leftBytes) < string(rightBytes)
		})
	}
	return nil
}

func ordinaryExecutablePathDigestV1(export toolcatalog.ToolExportV1) (canonical.Digest, error) {
	return canonical.Sum("portable-tool-ordinary-executable", "portable-tool-ordinary-executable-v1", struct {
		Path string `json:"path"`
	}{Path: export.Path})
}

func ordinaryCanonicalObjectV1(value any) (canonical.Object, bool) {
	switch object := value.(type) {
	case canonical.Object:
		return object, true
	case map[string]any:
		return canonical.Object(object), true
	default:
		return nil, false
	}
}
