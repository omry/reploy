package python

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	providerapi "github.com/omry/reploy/internal/providers"
)

const (
	RecipeVersion = "python-v1"
	InstallRoot   = "/opt/reploy/providers/python"
	BundleMount   = "/reploy-bundle"
)

// BundleResolver performs the network/source-aware phase and returns only
// closed artifacts plus console-script metadata. Image materialization never
// calls it.
type BundleResolver interface {
	ResolvePython(context.Context, providerapi.ResolveRequest) (ResolvedSet, error)
}

type ResolvedSet struct {
	BaseIdentity   string
	Artifacts      []providerapi.Artifact
	ConsoleScripts map[string]string // script name -> normalized distribution
}

type ComponentProvider struct {
	Resolver BundleResolver
}

var _ providerapi.Provider = ComponentProvider{}

func (ComponentProvider) Type() blueprint.ComponentType { return blueprint.ComponentTypePython }

func (ComponentProvider) RecipeVersion() string { return RecipeVersion }

func (ComponentProvider) Prerequisites(providerapi.ResolveRequest) []providerapi.Prerequisite {
	return []providerapi.Prerequisite{
		{
			Name: "python", Constraint: ">=3", Phase: providerapi.PrerequisiteMaterialize,
			Source: providerapi.PrerequisiteBaseImage, ProbeArgv: []string{"python", "--version"},
		},
		{
			Name: "python-venv", Phase: providerapi.PrerequisiteMaterialize,
			Source: providerapi.PrerequisiteBaseImage, ProbeArgv: []string{"python", "-m", "venv", "--help"},
		},
	}
}

func (provider ComponentProvider) Resolve(ctx context.Context, request providerapi.ResolveRequest) (providerapi.Bundle, error) {
	if provider.Resolver == nil {
		return providerapi.Bundle{}, fmt.Errorf("Python provider has no bundle resolver")
	}
	request = providerapi.NormalizeResolveRequest(request)
	resolved, err := provider.Resolver.ResolvePython(ctx, request)
	if err != nil {
		return providerapi.Bundle{}, fmt.Errorf("resolve Python bundle: %w", err)
	}
	artifacts := append([]providerapi.Artifact(nil), resolved.Artifacts...)
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	bundle := providerapi.Bundle{
		Provider: blueprint.ComponentTypePython, RecipeVersion: RecipeVersion,
		Platform: request.Platform, BaseIdentity: resolved.BaseIdentity,
		Artifacts: artifacts, Executables: map[string]providerapi.ExecutableOutput{},
	}
	for _, executable := range request.Executables {
		if path.Base(executable.Binary) != executable.Binary || executable.Binary == "." || executable.Binary == ".." {
			return providerapi.Bundle{}, fmt.Errorf("Python executable %q has invalid console script %q", executable.Name, executable.Binary)
		}
		if _, exists := resolved.ConsoleScripts[executable.Binary]; !exists {
			return providerapi.Bundle{}, fmt.Errorf("Python component %q does not provide console script %q for executable %q", executable.Component, executable.Binary, executable.Name)
		}
		bundle.Executables[executable.Name] = providerapi.ExecutableOutput{
			Component: executable.Component, Binary: executable.Binary,
			ImagePath: path.Join(InstallRoot, "bin", executable.Binary),
		}
	}
	if err := providerapi.ValidateBundle(bundle); err != nil {
		return providerapi.Bundle{}, fmt.Errorf("invalid Python bundle: %w", err)
	}
	return bundle, nil
}

func (ComponentProvider) Materialize(request providerapi.MaterializeRequest) (providerapi.Materialization, error) {
	bundle := request.Bundle
	if bundle.Provider != blueprint.ComponentTypePython {
		return providerapi.Materialization{}, fmt.Errorf("Python provider cannot materialize %q bundle", bundle.Provider)
	}
	if bundle.RecipeVersion != RecipeVersion {
		return providerapi.Materialization{}, fmt.Errorf("unsupported Python recipe version %q", bundle.RecipeVersion)
	}
	if err := providerapi.ValidateBundle(bundle); err != nil {
		return providerapi.Materialization{}, fmt.Errorf("invalid Python bundle: %w", err)
	}
	wheels := []string{}
	for _, artifact := range bundle.Artifacts {
		if artifact.Kind != "wheel" {
			continue
		}
		wheels = append(wheels, path.Join(BundleMount, artifact.Path))
	}
	if len(wheels) == 0 {
		return providerapi.Materialization{}, fmt.Errorf("Python bundle contains no wheels")
	}
	sort.Strings(wheels)
	install := []string{
		path.Join(InstallRoot, "bin", "python"), "-m", "pip",
		"--disable-pip-version-check", "install", "--no-index", "--no-deps", "--no-cache-dir",
	}
	install = append(install, wheels...)
	return providerapi.Materialization{
		Provider: blueprint.ComponentTypePython, Version: RecipeVersion, BundleMount: BundleMount,
		Artifacts: append([]providerapi.Artifact(nil), bundle.Artifacts...),
		Steps: []providerapi.MaterializationStep{
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

func cloneExecutableOutputs(source map[string]providerapi.ExecutableOutput) map[string]providerapi.ExecutableOutput {
	result := make(map[string]providerapi.ExecutableOutput, len(source))
	for name, output := range source {
		result[name] = output
	}
	return result
}
