package python

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/legacyprovider"
	providerapi "github.com/omry/reploy/internal/providers"
)

const (
	RecipeVersion                = "python-v1"
	MaterializationRecipeVersion = "python-materialize-v1"
	InstallRoot                  = "/opt/reploy/providers/python"
	BundleMount                  = "/reploy-bundle"
)

// BundleResolver performs the network/source-aware phase and returns only
// closed artifacts plus console-script metadata. Image materialization never
// calls it.
type BundleResolver interface {
	ResolvePython(context.Context, LegacyResolveRequest) (ResolvedSet, error)
}

type ResolvedSet struct {
	BaseIdentity   string
	Artifacts      []legacyprovider.Artifact
	ConsoleScripts map[string]string // script name -> normalized distribution
}

type ComponentProvider struct {
	Resolver BundleResolver
}

type LegacyBaseProbe struct {
	Name      string
	ProbeArgv []string
}

func (ComponentProvider) Type() blueprint.ComponentType { return blueprint.ComponentTypePython }

func (ComponentProvider) RecipeVersion() string { return RecipeVersion }

func (ComponentProvider) Plan(input providerapi.PlanInput) ([]providerapi.NodeSpec, error) {
	if err := input.BlueprintDigest.Validate(); err != nil {
		return nil, fmt.Errorf("Python plan blueprint digest: %w", err)
	}
	if err := input.Platform.Validate(); err != nil {
		return nil, fmt.Errorf("Python plan platform: %w", err)
	}
	components := append([]providerapi.ResolvedComponentRequestV1{}, input.Components...)
	sort.Slice(components, func(left int, right int) bool { return components[left].Component < components[right].Component })
	nodes := []providerapi.NodeSpec{}
	for _, component := range components {
		if component.Provider != blueprint.ComponentTypePython {
			continue
		}
		request, err := decodeCanonicalProviderRequestV1(component.Request)
		if err != nil {
			return nil, fmt.Errorf("plan Python component %q: %w", component.Component, err)
		}
		if request.Component != component.Component {
			return nil, fmt.Errorf("Python request component %q does not match resolved component %q", request.Component, component.Component)
		}
		node := providerapi.NodeSpec{
			ID:                 providerapi.NodeID("python/" + component.Component),
			Provider:           blueprint.ComponentTypePython,
			Components:         []string{component.Component},
			Request:            component.Request,
			OutputDeclarations: []providerapi.OutputDeclaration{},
			Requirements: providerapi.RequirementDeclaration{
				Executables: []providerapi.ExecutableRequirement{{
					ID: "interpreter", Command: request.Interpreter.Command, VersionConstraint: request.Interpreter.Version,
					Supplier: request.Interpreter.Supplier, ValidationPolicy: providerapi.ValidationPolicyCompatible,
				}},
				Files: []providerapi.FileRequirement{},
				ProviderData: providerapi.CanonicalProviderData{
					Schema: component.Request.Schema, Value: component.Request.Value,
				},
			},
		}
		if err := providerapi.ValidateNodeSpec(node); err != nil {
			return nil, fmt.Errorf("plan Python component %q: %w", component.Component, err)
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func (ComponentProvider) LegacyBaseProbes() []LegacyBaseProbe {
	return []LegacyBaseProbe{
		{Name: "python", ProbeArgv: []string{"python", "--version"}},
		{Name: "python-venv", ProbeArgv: []string{"python", "-m", "venv", "--help"}},
	}
}

func (provider ComponentProvider) Resolve(ctx context.Context, request LegacyResolveRequest) (legacyprovider.Bundle, error) {
	if provider.Resolver == nil {
		return legacyprovider.Bundle{}, fmt.Errorf("Python provider has no bundle resolver")
	}
	request = normalizeLegacyResolveRequest(request)
	resolved, err := provider.Resolver.ResolvePython(ctx, request)
	if err != nil {
		return legacyprovider.Bundle{}, fmt.Errorf("resolve Python bundle: %w", err)
	}
	artifacts := append([]legacyprovider.Artifact(nil), resolved.Artifacts...)
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	bundle := legacyprovider.Bundle{
		Provider: blueprint.ComponentTypePython, RecipeVersion: RecipeVersion,
		Platform: request.Platform, BaseIdentity: resolved.BaseIdentity,
		Artifacts: artifacts, Executables: map[string]legacyprovider.ExecutableOutput{},
	}
	for _, executable := range request.Executables {
		if path.Base(executable.Binary) != executable.Binary || executable.Binary == "." || executable.Binary == ".." {
			return legacyprovider.Bundle{}, fmt.Errorf("Python executable %q has invalid console script %q", executable.Name, executable.Binary)
		}
		if _, exists := resolved.ConsoleScripts[executable.Binary]; !exists {
			return legacyprovider.Bundle{}, fmt.Errorf("Python component %q does not provide console script %q for executable %q", executable.Component, executable.Binary, executable.Name)
		}
		bundle.Executables[executable.Name] = legacyprovider.ExecutableOutput{
			Component: executable.Component, Binary: executable.Binary,
			ImagePath: path.Join(InstallRoot, "bin", executable.Binary),
		}
	}
	if err := legacyprovider.ValidateBundle(bundle); err != nil {
		return legacyprovider.Bundle{}, fmt.Errorf("invalid Python bundle: %w", err)
	}
	return bundle, nil
}

func (ComponentProvider) LegacyMaterialize(request legacyprovider.MaterializeRequest) (legacyprovider.Materialization, error) {
	bundle := request.Bundle
	if bundle.Provider != blueprint.ComponentTypePython {
		return legacyprovider.Materialization{}, fmt.Errorf("Python provider cannot materialize %q bundle", bundle.Provider)
	}
	if bundle.RecipeVersion != RecipeVersion {
		return legacyprovider.Materialization{}, fmt.Errorf("unsupported Python recipe version %q", bundle.RecipeVersion)
	}
	if err := legacyprovider.ValidateBundle(bundle); err != nil {
		return legacyprovider.Materialization{}, fmt.Errorf("invalid Python bundle: %w", err)
	}
	wheels := []string{}
	for _, artifact := range bundle.Artifacts {
		if artifact.Kind != "wheel" {
			continue
		}
		wheels = append(wheels, path.Join(BundleMount, artifact.Path))
	}
	if len(wheels) == 0 {
		return legacyprovider.Materialization{}, fmt.Errorf("Python bundle contains no wheels")
	}
	sort.Strings(wheels)
	install := []string{
		path.Join(InstallRoot, "bin", "python"), "-m", "pip",
		"--disable-pip-version-check", "install", "--no-index", "--no-deps", "--no-cache-dir",
	}
	install = append(install, wheels...)
	return legacyprovider.Materialization{
		Provider: blueprint.ComponentTypePython, Version: RecipeVersion, BundleMount: BundleMount,
		Artifacts: append([]legacyprovider.Artifact(nil), bundle.Artifacts...),
		Steps: []legacyprovider.MaterializationStep{
			{Argv: []string{"python", "-m", "venv", InstallRoot}},
			{Argv: install},
		},
		Executables: cloneExecutableOutputs(bundle.Executables),
	}, nil
}

func ValidatePythonVersion(output string) error {
	value := strings.TrimSpace(output)
	if !strings.HasPrefix(value, "Python 3.") {
		return fmt.Errorf("Python provider requires Python 3 in the selected base image; probe returned %q", value)
	}
	return nil
}

func cloneExecutableOutputs(source map[string]legacyprovider.ExecutableOutput) map[string]legacyprovider.ExecutableOutput {
	result := make(map[string]legacyprovider.ExecutableOutput, len(source))
	for name, output := range source {
		result[name] = output
	}
	return result
}
