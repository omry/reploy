package dockerdeploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/omry/reploy/internal/deploy"
)

type PackDesiredStateStageInputV1 struct {
	DeploymentDir    string
	Pack             deploy.PackRef
	ExplicitPlatform string
	WorkspaceRoot    string
	Create           bool
}

type PackDesiredStateStageResultV1 struct {
	AppID        string
	DesiredState deploy.DesiredStateUpdateResult
}

type LoadedPackDesiredStateStageInputV1 struct {
	DeploymentDir    string
	Blueprint        deploy.LoadedBlueprint
	ExplicitPlatform string
	WorkspaceRoot    string
	Create           bool
	Force            bool
	RunOptions       RunOptions
}

// StagePackDesiredStateV1 resolves one blueprint reference and records only
// the deployment's desired state. It does not prepare providers or an image.
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
		WorkspaceRoot: input.WorkspaceRoot, Create: input.Create,
	})
}

// StageLoadedPackDesiredStateV1 records a pack that the caller has already
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
	workspaceRoot := loaded.WorkspaceRoot
	if input.WorkspaceRoot != "" {
		resolvedWorkspaceRoot, err := filepath.Abs(input.WorkspaceRoot)
		if err != nil {
			return PackDesiredStateStageResultV1{}, fmt.Errorf("resolve staging workspace root: %w", err)
		}
		workspaceRoot = filepath.Clean(resolvedWorkspaceRoot)
	}
	stageInput := DesiredStateStageInputV1{
		DeploymentDir:    input.DeploymentDir,
		Document:         loaded.Document,
		ExplicitPlatform: input.ExplicitPlatform,
		BlueprintSource:  loaded.BlueprintSource,
		WorkspaceRoot:    workspaceRoot,
		Create:           input.Create,
	}
	var desired deploy.DesiredStateUpdateResult
	var err error
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
	return PackDesiredStateStageResultV1{AppID: loaded.Document.Environment.ID, DesiredState: desired}, nil
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
