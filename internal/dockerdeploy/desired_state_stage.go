package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
)

type DesiredStateStageInputV1 struct {
	DeploymentDir    string
	Document         blueprint.Document
	ExplicitPlatform string
	Create           bool
}

type desiredStateStageBackendV1 struct {
	probeNative func(context.Context) (blueprint.Platform, error)
	setState    func(context.Context, string, blueprint.Document, blueprint.Platform, bool) (deploy.DesiredStateUpdateResult, error)
}

// StageDesiredStateV1 records the resolved blueprint and one selected target.
// It never resolves providers or builds an image.
func StageDesiredStateV1(ctx context.Context, input DesiredStateStageInputV1) (deploy.DesiredStateUpdateResult, error) {
	return stageDesiredStateV1(ctx, input, desiredStateStageBackendV1{
		probeNative: ProbeDockerNativePlatform,
		setState: func(ctx context.Context, dir string, document blueprint.Document, platform blueprint.Platform, create bool) (deploy.DesiredStateUpdateResult, error) {
			if create {
				return deploy.CreateDesiredStateV1(ctx, dir, document, platform, registry.ValidatePackageRequest)
			}
			return deploy.SetDesiredStateV1(ctx, dir, document, platform, registry.ValidatePackageRequest)
		},
	})
}

func stageDesiredStateV1(ctx context.Context, input DesiredStateStageInputV1, backend desiredStateStageBackendV1) (deploy.DesiredStateUpdateResult, error) {
	if ctx == nil {
		return deploy.DesiredStateUpdateResult{}, fmt.Errorf("stage desired state requires a context")
	}
	if err := ctx.Err(); err != nil {
		return deploy.DesiredStateUpdateResult{}, err
	}
	if input.DeploymentDir == "" {
		return deploy.DesiredStateUpdateResult{}, fmt.Errorf("stage desired state requires a deployment directory")
	}
	if backend.setState == nil {
		return deploy.DesiredStateUpdateResult{}, fmt.Errorf("stage desired state requires a state writer")
	}

	var native *blueprint.Platform
	if input.ExplicitPlatform == "" && len(input.Document.Blueprint.Compatibility.Platforms) > 1 {
		if backend.probeNative == nil {
			return deploy.DesiredStateUpdateResult{}, fmt.Errorf("stage desired state requires a Docker native-platform probe")
		}
		observed, err := backend.probeNative(ctx)
		if err != nil {
			return deploy.DesiredStateUpdateResult{}, err
		}
		native = &observed
	}
	selected, err := SelectDockerTargetPlatform(input.Document, input.ExplicitPlatform, native)
	if err != nil {
		return deploy.DesiredStateUpdateResult{}, err
	}
	return backend.setState(ctx, input.DeploymentDir, input.Document, selected, input.Create)
}

// RestageCurrentDesiredPlatformV1 reselects only the target platform from the
// resolved blueprint already stored in state-v1.
func RestageCurrentDesiredPlatformV1(ctx context.Context, deploymentDir string, explicitPlatform string) (deploy.DesiredStateUpdateResult, error) {
	return restageCurrentDesiredPlatformV1(ctx, deploymentDir, explicitPlatform, ProbeDockerNativePlatform)
}

func restageCurrentDesiredPlatformV1(
	ctx context.Context,
	deploymentDir string,
	explicitPlatform string,
	probeNative func(context.Context) (blueprint.Platform, error),
) (deploy.DesiredStateUpdateResult, error) {
	if deploymentDir == "" {
		return deploy.DesiredStateUpdateResult{}, fmt.Errorf("restage desired platform requires a deployment directory")
	}
	return deploy.SelectDesiredPlatformV1(ctx, deploymentDir, func(document blueprint.Document) (blueprint.Platform, error) {
		var native *blueprint.Platform
		if explicitPlatform == "" && len(document.Blueprint.Compatibility.Platforms) > 1 {
			if probeNative == nil {
				return blueprint.Platform{}, fmt.Errorf("restage desired platform requires a Docker native-platform probe")
			}
			observed, err := probeNative(ctx)
			if err != nil {
				return blueprint.Platform{}, err
			}
			native = &observed
		}
		return SelectDockerTargetPlatform(document, explicitPlatform, native)
	}, registry.ValidatePackageRequest)
}
