package dockerdeploy

import (
	"context"
	"fmt"
	"os"

	"github.com/omry/reploy/internal/deploy"
)

type PackDesiredStateStageInputV1 struct {
	DeploymentDir    string
	Pack             deploy.PackRef
	ExplicitPlatform string
	Create           bool
}

type PackDesiredStateStageResultV1 struct {
	AppID        string
	DesiredState deploy.DesiredStateUpdateResult
}

type LoadedPackDesiredStateStageInputV1 struct {
	DeploymentDir    string
	Pack             deploy.AppPack
	ExplicitPlatform string
	Create           bool
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

	pack, err := deploy.LoadPack(input.Pack)
	if err != nil {
		return PackDesiredStateStageResultV1{}, err
	}
	return StageLoadedPackDesiredStateV1(ctx, LoadedPackDesiredStateStageInputV1{
		DeploymentDir: input.DeploymentDir, Pack: pack, ExplicitPlatform: input.ExplicitPlatform, Create: input.Create,
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
	pack := input.Pack
	if pack.Environment == nil {
		return PackDesiredStateStageResultV1{}, fmt.Errorf("blueprint %q does not use the environment model", pack.AppID)
	}
	if err := prepareDesiredStateStageDirV1(input.DeploymentDir, input.Create); err != nil {
		return PackDesiredStateStageResultV1{}, err
	}
	desired, err := StageDesiredStateV1(ctx, DesiredStateStageInputV1{
		DeploymentDir:    input.DeploymentDir,
		Document:         *pack.Environment,
		ExplicitPlatform: input.ExplicitPlatform,
		Create:           input.Create,
	})
	if err != nil {
		return PackDesiredStateStageResultV1{}, err
	}
	return PackDesiredStateStageResultV1{AppID: pack.AppID, DesiredState: desired}, nil
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
