package dockerdeploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
)

type PackDesiredStateStageInputV1 struct {
	DeploymentDir      string
	Pack               deploy.PackRef
	ExplicitPlatform   string
	Create             bool
	SkipControlSurface bool
}

type PackDesiredStateStageResultV1 struct {
	AppID        string
	DesiredState deploy.DesiredStateUpdateResult
}

type LoadedPackDesiredStateStageInputV1 struct {
	DeploymentDir      string
	Blueprint          deploy.LoadedBlueprint
	ExplicitPlatform   string
	Create             bool
	Force              bool
	RunOptions         RunOptions
	SkipControlSurface bool
}

// StagePackDesiredStateV1 resolves one blueprint reference, records the
// deployment's desired state, and materializes the user-facing staged control
// surface. It does not prepare providers or an image.
func StagePackDesiredStateV1(ctx context.Context, input PackDesiredStateStageInputV1) (PackDesiredStateStageResultV1, error) {
	if ctx == nil {
		return PackDesiredStateStageResultV1{}, fmt.Errorf("stage blueprint requires a context")
	}
	if err := ctx.Err(); err != nil {
		return PackDesiredStateStageResultV1{}, err
	}
	if input.DeploymentDir == "" {
		return PackDesiredStateStageResultV1{}, fmt.Errorf("stage blueprint requires a deployment directory")
	}
	if input.Pack.Raw == "" {
		return PackDesiredStateStageResultV1{}, fmt.Errorf("stage blueprint requires a blueprint reference")
	}

	loaded, err := deploy.LoadBlueprint(input.Pack)
	if err != nil {
		return PackDesiredStateStageResultV1{}, err
	}
	return StageLoadedPackDesiredStateV1(ctx, LoadedPackDesiredStateStageInputV1{
		DeploymentDir: input.DeploymentDir, Blueprint: loaded, ExplicitPlatform: input.ExplicitPlatform,
		Create: input.Create, SkipControlSurface: input.SkipControlSurface,
	})
}

// StageLoadedPackDesiredStateV1 stages a pack that the caller has already
// resolved, avoiding a second remote blueprint fetch at command boundaries.
func StageLoadedPackDesiredStateV1(ctx context.Context, input LoadedPackDesiredStateStageInputV1) (PackDesiredStateStageResultV1, error) {
	if ctx == nil {
		return PackDesiredStateStageResultV1{}, fmt.Errorf("stage blueprint requires a context")
	}
	if err := ctx.Err(); err != nil {
		return PackDesiredStateStageResultV1{}, err
	}
	if input.DeploymentDir == "" {
		return PackDesiredStateStageResultV1{}, fmt.Errorf("stage blueprint requires a deployment directory")
	}
	loaded := input.Blueprint
	if err := prepareDesiredStateStageDirV1(input.DeploymentDir, input.Create); err != nil {
		return PackDesiredStateStageResultV1{}, err
	}
	if !input.SkipControlSurface {
		_, err := planStagedControlSurfaceV1(input.DeploymentDir, loaded.Document)
		if err != nil {
			return PackDesiredStateStageResultV1{}, err
		}
	}
	initialOverrides, err := localBlueprintInitialPackageOverridesV1(loaded, input.DeploymentDir, input.Create)
	if err != nil {
		return PackDesiredStateStageResultV1{}, err
	}
	stageInput := DesiredStateStageInputV1{
		DeploymentDir:    input.DeploymentDir,
		Document:         loaded.Document,
		ExplicitPlatform: input.ExplicitPlatform,
		BlueprintSource:  loaded.BlueprintSource,
		InitialOverrides: initialOverrides,
		Create:           input.Create,
	}
	var desired deploy.DesiredStateUpdateResult
	if input.Force {
		desired, err = ForceReplaceStagedDesiredStateV1(ctx, ForceReplaceStagedDesiredStateInputV1{
			DesiredState: stageInput,
			RunOptions:   input.RunOptions,
		})
	} else {
		desired, err = StageDesiredStateV1(ctx, stageInput)
	}
	if err != nil {
		return PackDesiredStateStageResultV1{}, err
	}
	if !input.SkipControlSurface {
		controlChanged, err := syncCurrentStagedControlSurfaceV1(ctx, input.DeploymentDir)
		if err != nil {
			return PackDesiredStateStageResultV1{}, fmt.Errorf(
				"staging desired state was recorded, but its control surface could not be generated; run `reploy stage --update`: %w",
				err,
			)
		}
		desired.Changed = desired.Changed || controlChanged
	}
	return PackDesiredStateStageResultV1{AppID: loaded.Document.Environment.ID, DesiredState: desired}, nil
}

func localBlueprintInitialPackageOverridesV1(
	loaded deploy.LoadedBlueprint,
	deploymentDir string,
	create bool,
) (*deploy.PackageOverridesV1, error) {
	if !create || loaded.RequestedRef.Scheme != "file" {
		return nil, nil
	}
	sourceDir := filepath.Clean(filepath.Dir(loaded.ManifestPath))
	overrides, found, err := deploy.ReadPackageOverridesV1(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("load local blueprint package overrides: %w", err)
	}
	if !found {
		return nil, nil
	}
	if overrides.Environment.ID != loaded.Document.Environment.ID {
		return nil, fmt.Errorf(
			"local blueprint %s targets environment %q but its %s targets %q",
			loaded.ManifestPath,
			loaded.Document.Environment.ID,
			deploy.PackageOverridesFilename,
			overrides.Environment.ID,
		)
	}
	workspace, hasWorkspace, err := localBlueprintOverridesWorkspaceV1(&overrides, sourceDir)
	if err != nil {
		return nil, fmt.Errorf("resolve local blueprint package overrides workspace: %w", err)
	}
	resolved, err := deploy.ResolvePackageOverridesV1(
		overrides, sourceDir, registry.NormalizePackageOverrideV1,
	)
	if err != nil {
		return nil, fmt.Errorf("resolve local blueprint package overrides: %w", err)
	}
	absoluteDeploymentDir, err := filepath.Abs(deploymentDir)
	if err != nil {
		return nil, fmt.Errorf("resolve staging directory: %w", err)
	}
	if filepath.Clean(absoluteDeploymentDir) == sourceDir {
		return nil, nil
	}
	staged := deploy.EmptyPackageOverridesV1(resolved.EnvironmentID)
	if overrides.Environment.Base != nil {
		base := *overrides.Environment.Base
		staged.Environment.Base = &base
	}
	if hasWorkspace {
		staged.Environment.Vars["workspace_root"] = workspace
	}
	if len(resolved.Additions) != 0 {
		staged.Environment.PackageAdditions = map[string][]string{}
	}
	for provider, requirements := range resolved.Additions {
		staged.Environment.PackageAdditions[provider] = append([]string{}, requirements...)
	}
	for provider, packages := range resolved.Providers {
		stagedPackages := make(map[string]deploy.PackageOverrideChoiceV1, len(packages))
		for packageID, choice := range packages {
			path := choice.Path
			if hasWorkspace {
				path = stagedPackageOverridePathV1(workspace, path)
			}
			stagedPackages[packageID] = deploy.PackageOverrideChoiceV1{
				Path: path, Version: choice.Version,
			}
		}
		staged.Environment.PackageOverrides[provider] = stagedPackages
	}
	return &staged, nil
}

func localBlueprintOverridesWorkspaceV1(
	overrides *deploy.PackageOverridesV1,
	sourceDir string,
) (string, bool, error) {
	if _, declared := overrides.Environment.Vars["workspace_root"]; !declared {
		return "", false, nil
	}
	variables, err := blueprint.ResolveEnvironmentVariables(overrides.Environment.Vars)
	if err != nil {
		return "", false, err
	}
	value, ok := variables["workspace_root"].(string)
	if !ok {
		return "", false, fmt.Errorf("workspace_root must resolve to a string")
	}
	if value == "" || strings.TrimSpace(value) != value {
		return "", false, fmt.Errorf("workspace_root must resolve to a non-empty path without surrounding whitespace")
	}
	workspace := ""
	if filepath.IsAbs(value) || value == "~" || strings.HasPrefix(value, "~/") {
		workspace, err = deploy.ResolvePackageOverrideWorkspaceRootV1(value)
		if err != nil {
			return "", false, err
		}
	} else {
		workspace = filepath.Clean(filepath.Join(sourceDir, filepath.FromSlash(value)))
	}
	source := make(map[string]any, len(overrides.Environment.Vars))
	for name, variable := range overrides.Environment.Vars {
		source[name] = variable
	}
	source["workspace_root"] = workspace
	overrides.Environment.Vars = source
	return workspace, true, nil
}

func stagedPackageOverridePathV1(workspace string, path string) string {
	if path == "" {
		return ""
	}
	relative, err := filepath.Rel(workspace, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return path
	}
	if relative == "." {
		return "{{ workspace_root }}"
	}
	return "{{ workspace_root }}/" + filepath.ToSlash(relative)
}

func prepareDesiredStateStageDirV1(dir string, create bool) error {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) && create {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create staging directory: %w", err)
		}
		info, err = os.Lstat(dir)
	}
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("staging directory does not exist: %s", dir)
		}
		return fmt.Errorf("inspect staging directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("staging path must be a real directory: %s", dir)
	}
	return nil
}
