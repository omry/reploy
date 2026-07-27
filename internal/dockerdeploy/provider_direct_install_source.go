package dockerdeploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/omry/reploy/internal/deploy"
)

type directProviderInstallSourceInputV1 struct {
	Pack             deploy.PackRef
	ExplicitPlatform string
}

type directProviderInstallSourceBackendV1 struct {
	mkdirTemp func(string, string) (string, error)
	removeAll func(string) error
	stage     func(context.Context, PackDesiredStateStageInputV1) (PackDesiredStateStageResultV1, error)
}

// withDirectProviderInstallSourceV1 creates the state-v1 source workspace used
// by a direct install. The callback owns the build and install transaction
// while the workspace exists; cleanup always follows callback completion.
func withDirectProviderInstallSourceV1(
	ctx context.Context,
	input directProviderInstallSourceInputV1,
	run func(context.Context, string) error,
) error {
	return withDirectProviderInstallSourceBackendV1(ctx, input, run, directProviderInstallSourceBackendV1{
		mkdirTemp: os.MkdirTemp,
		removeAll: os.RemoveAll,
		stage:     StagePackDesiredStateV1,
	})
}

func withDirectProviderInstallSourceBackendV1(
	ctx context.Context,
	input directProviderInstallSourceInputV1,
	run func(context.Context, string) error,
	backend directProviderInstallSourceBackendV1,
) (err error) {
	if ctx == nil {
		return fmt.Errorf("direct provider install source requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if input.Pack.Raw == "" {
		return fmt.Errorf("direct provider install source requires a blueprint reference")
	}
	if run == nil {
		return fmt.Errorf("direct provider install source requires an install callback")
	}
	if backend.mkdirTemp == nil || backend.removeAll == nil || backend.stage == nil {
		return fmt.Errorf("direct provider install source requires a complete backend")
	}
	root, err := backend.mkdirTemp("", "reploy-direct-install-*")
	if err != nil {
		return fmt.Errorf("create direct provider install workspace: %w", err)
	}
	defer func() {
		if cleanupErr := backend.removeAll(root); err == nil && cleanupErr != nil {
			err = fmt.Errorf("remove direct provider install workspace: %w", cleanupErr)
		}
	}()
	deploymentDir := filepath.Join(root, "staging")
	if _, err := backend.stage(ctx, PackDesiredStateStageInputV1{
		DeploymentDir: deploymentDir, Pack: input.Pack, ExplicitPlatform: input.ExplicitPlatform,
		Create: true, SkipControlSurface: true,
	}); err != nil {
		return fmt.Errorf("stage direct provider install source: %w", err)
	}
	return run(ctx, deploymentDir)
}
