package dockerdeploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/python"
)

type InfoOptions struct {
	Dir string
}

func Info(options InfoOptions) (string, error) {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	state, err := loadState(options.Dir)
	if err != nil {
		return "", err
	}
	absoluteDir, err := filepath.Abs(options.Dir)
	if err != nil {
		return "", err
	}
	lines := []string{
		fmt.Sprintf("deployment: %s", absoluteDir),
		fmt.Sprintf("target: %s", state.Target),
		fmt.Sprintf("phase: %s", state.Phase),
		fmt.Sprintf("blueprint: %s", state.Blueprint.Raw),
	}
	if state.RequestedBlueprintRef != "" && state.RequestedBlueprintRef != state.Blueprint.Raw {
		lines = append(lines, fmt.Sprintf("requested blueprint: %s", state.RequestedBlueprintRef))
	}
	if state.Install != nil && state.Install.Scope != "" {
		lines = append(lines, fmt.Sprintf("install scope: %s", state.Install.Scope))
	}
	lines = append(lines, "bundle roots:")
	if len(state.Bundle.Roots) == 0 {
		lines = append(lines, "  (none)")
	} else {
		for _, root := range state.Bundle.Roots {
			lines = append(lines, fmt.Sprintf("  - %s", formatArtifactRoot(root)))
		}
	}
	lines = append(lines, "bundle prepared:")
	prepared, ok, err := preparedBundlePackages(options.Dir, state.Bundle.Roots)
	if err != nil {
		return "", err
	}
	if !ok {
		lines = append(lines, "  not built")
	} else {
		for _, resolved := range prepared {
			lines = append(lines, fmt.Sprintf("  - %s %s", resolved.Kind, resolved.Requirement))
		}
	}
	lines = append(lines, fmt.Sprintf("files: %s", filepath.Join(absoluteDir, ReployInternalDir)))
	return strings.Join(lines, "\n") + "\n", nil
}

func formatArtifactRoot(root deploy.ArtifactRoot) string {
	if root.Provider == "" && root.Kind == "" {
		return root.Source
	}
	if root.Provider == "" {
		return root.Kind + " " + root.Source
	}
	if root.Kind == "" {
		return root.Provider + " " + root.Source
	}
	return root.Provider + " " + root.Kind + " " + root.Source
}

func preparedBundlePackages(dir string, roots []deploy.ArtifactRoot) ([]BundleResolvedPackage, bool, error) {
	bundleDir, err := deploymentBundleDir(dir)
	if err != nil {
		return nil, false, err
	}
	entries, err := os.ReadDir(bundleDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	rootRequirements := python.RootRequirements(roots)
	prepared := []BundleResolvedPackage{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".whl") {
			continue
		}
		requirement, ok := python.WheelFilenameRequirement(entry.Name())
		if !ok {
			continue
		}
		kind := "transitive"
		if rootRequirements[requirement] || rootRequirements[strings.Split(requirement, "==")[0]] {
			kind = "root"
		}
		prepared = append(prepared, BundleResolvedPackage{Kind: kind, Requirement: requirement})
	}
	if len(prepared) == 0 {
		return nil, false, nil
	}
	sortBundleResolvedPackages(prepared)
	return prepared, true, nil
}
