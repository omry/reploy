package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
)

type DesiredStateStageInputV1 struct {
	DeploymentDir    string
	Document         blueprint.Document
	ExplicitPlatform string
	BlueprintSource  string
	InitialOverrides *deploy.PackageOverridesV1
	Create           bool
}

type desiredStateStageBackendV1 struct {
	probeNative func(context.Context) (blueprint.Platform, error)
	setState    func(context.Context, string, blueprint.Document, blueprint.Platform, string, bool, *deploy.PackageOverridesV1) (deploy.DesiredStateUpdateResult, error)
}

// StageDesiredStateV1 records the resolved blueprint and one selected target.
// It never resolves providers or builds an image.
func StageDesiredStateV1(ctx context.Context, input DesiredStateStageInputV1) (deploy.DesiredStateUpdateResult, error) {
	return stageDesiredStateV1(ctx, input, desiredStateStageBackendV1{
		probeNative: ProbeDockerNativePlatform,
		setState: func(ctx context.Context, dir string, document blueprint.Document, platform blueprint.Platform, source string, create bool, overrides *deploy.PackageOverridesV1) (deploy.DesiredStateUpdateResult, error) {
			if overrides != nil {
				if !create || source == "" {
					return deploy.DesiredStateUpdateResult{}, fmt.Errorf("initial package overrides require a new staged blueprint source")
				}
				return deploy.CreateStagedDesiredStateWithPackageOverridesV1(
					ctx, dir, document, platform, registry.ValidatePackageRequest, source, *overrides,
				)
			}
			if source == "" {
				if create {
					return deploy.CreateDesiredStateV1(ctx, dir, document, platform, registry.ValidatePackageRequest)
				}
				return deploy.SetDesiredStateV1(ctx, dir, document, platform, registry.ValidatePackageRequest)
			}
			return deploy.SetStagedDesiredStateV1(ctx, dir, document, platform, registry.ValidatePackageRequest, source, create)
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

	selected, err := selectDesiredStateTargetV1(ctx, input, backend.probeNative)
	if err != nil {
		return deploy.DesiredStateUpdateResult{}, err
	}
	return backend.setState(
		ctx, input.DeploymentDir, input.Document, selected,
		input.BlueprintSource, input.Create, input.InitialOverrides,
	)
}

func selectDesiredStateTargetV1(ctx context.Context, input DesiredStateStageInputV1, probeNative func(context.Context) (blueprint.Platform, error)) (blueprint.Platform, error) {
	var native *blueprint.Platform
	if input.ExplicitPlatform == "" && len(input.Document.Blueprint.Compatibility.Platforms) > 1 {
		if probeNative == nil {
			return blueprint.Platform{}, fmt.Errorf("stage desired state requires a Docker native-platform probe")
		}
		observed, err := probeNative(ctx)
		if err != nil {
			return blueprint.Platform{}, err
		}
		native = &observed
	}
	selected, err := SelectDockerTargetPlatform(input.Document, input.ExplicitPlatform, native)
	if err != nil {
		return blueprint.Platform{}, err
	}
	return selected, nil
}

// RestageCurrentDesiredPlatformV1 reselects only the target platform from the
// resolved blueprint already stored in state-v1.
func RestageCurrentDesiredPlatformV1(ctx context.Context, deploymentDir string, explicitPlatform string) (deploy.DesiredStateUpdateResult, error) {
	result, err := restageCurrentDesiredPlatformV1(ctx, deploymentDir, explicitPlatform, ProbeDockerNativePlatform)
	if err != nil {
		return result, err
	}
	changed, err := syncCurrentStagedControlSurfaceV1(ctx, deploymentDir)
	if err != nil {
		return result, fmt.Errorf("refresh staged control surface: %w", err)
	}
	result.Changed = result.Changed || changed
	return result, nil
}

// ForceRestageCurrentDesiredPlatformV1 behaves like an ordinary update for
// current state and explicitly recovers the one recognized incompatible
// components-based development staging shape.
func ForceRestageCurrentDesiredPlatformV1(
	ctx context.Context,
	deploymentDir string,
	explicitPlatform string,
	options RunOptions,
) (result deploy.DesiredStateUpdateResult, err error) {
	result, err = RestageCurrentDesiredPlatformV1(ctx, deploymentDir, explicitPlatform)
	if err == nil || !errors.Is(err, deploy.ErrLegacyStateUnsupported) {
		return result, err
	}
	return forceRecoverLegacyComponentsStagingV1(
		ctx,
		deploymentDir,
		explicitPlatform,
		options,
		forceLegacyComponentsStagingBackendV1{
			acquire: deploy.AcquireOperationLock,
			prepare: func(
				operation *deploy.OperationLock,
				selectPlatform deploy.DesiredPlatformSelector,
				preserveSelectedPlatform bool,
			) (deploy.LegacyComponentsStagingRecoveryV1, error) {
				return operation.PrepareLegacyComponentsStagingRecoveryV1(
					selectPlatform,
					registry.ValidatePackageRequest,
					preserveSelectedPlatform,
				)
			},
			admit:     AdmitControlOperationV1,
			complete:  CompleteControlAdmissionV1,
			stopOwned: stopLegacyStagedWorkloadForRecoveryV1,
			commit: func(
				operation *deploy.OperationLock,
				recovery deploy.LegacyComponentsStagingRecoveryV1,
			) error {
				return operation.CommitLegacyComponentsStagingRecoveryV1(recovery)
			},
			validateReference: ValidateEnvironmentGenerationReference,
			removeReference:   removeLegacyEnvironmentGenerationReferenceV1,
			syncControl:       syncCurrentStagedControlSurfaceV1,
		},
	)
}

type forceLegacyComponentsStagingBackendV1 struct {
	acquire           func(context.Context, string) (*deploy.OperationLock, error)
	prepare           func(*deploy.OperationLock, deploy.DesiredPlatformSelector, bool) (deploy.LegacyComponentsStagingRecoveryV1, error)
	admit             func(context.Context, string, *deploy.OperationLock, ControlAdmissionInputV1) (AdmittedControlV1, error)
	complete          func(*deploy.OperationLock, string, *deploy.ControlLeaseV1) error
	stopOwned         func(context.Context, *deploy.OperationLock, string, string, RunOptions) error
	commit            func(*deploy.OperationLock, deploy.LegacyComponentsStagingRecoveryV1) error
	validateReference func(string, string, string) error
	removeReference   func(context.Context, string, string, string) error
	syncControl       func(context.Context, string) (bool, error)
}

func stopLegacyStagedWorkloadForRecoveryV1(
	ctx context.Context,
	operation *deploy.OperationLock,
	environment string,
	deploymentDir string,
	options RunOptions,
) error {
	return stopLegacyStagedWorkloadForRecoveryWithV1(
		ctx,
		operation,
		environment,
		deploymentDir,
		options,
		removeDockerComposeProjectByLabelV1,
	)
}

func stopLegacyStagedWorkloadForRecoveryWithV1(
	ctx context.Context,
	operation *deploy.OperationLock,
	environment string,
	deploymentDir string,
	options RunOptions,
	removeProject func(context.Context, string, time.Duration) error,
) error {
	if err := operation.RequireHeld(); err != nil {
		return err
	}
	if removeProject == nil {
		return fmt.Errorf("stop legacy staged workload requires a Docker project remover")
	}
	project, err := legacyStagedComposeProjectNameV1(environment, deploymentDir)
	if err != nil {
		return err
	}
	return removeProject(ctx, project, options.DockerPreflightTimeout)
}

func legacyStagedComposeProjectNameV1(
	environment string,
	deploymentDir string,
) (string, error) {
	if environment == "" {
		return "", fmt.Errorf("legacy staged workload has no recorded environment")
	}
	hash, err := pathIdentityHash(deploymentDir)
	if err != nil {
		return "", fmt.Errorf("derive legacy staged Docker Compose project: %w", err)
	}
	return dockerNameSlug(environment, "environment") + "-staging-" + hash, nil
}

func forceRecoverLegacyComponentsStagingV1(
	ctx context.Context,
	deploymentDir string,
	explicitPlatform string,
	options RunOptions,
	backend forceLegacyComponentsStagingBackendV1,
) (result deploy.DesiredStateUpdateResult, err error) {
	if ctx == nil {
		return result, fmt.Errorf("recover legacy staging requires a context")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if deploymentDir == "" {
		return result, fmt.Errorf("recover legacy staging requires a deployment directory")
	}
	if backend.acquire == nil || backend.prepare == nil || backend.admit == nil ||
		backend.complete == nil || backend.stopOwned == nil || backend.commit == nil ||
		backend.validateReference == nil || backend.removeReference == nil ||
		backend.syncControl == nil {
		return result, fmt.Errorf("recover legacy staging requires a complete backend")
	}
	operation, err := backend.acquire(ctx, deploymentDir)
	if err != nil {
		return result, err
	}
	markerID := ""
	var lease *deploy.ControlLeaseV1
	defer func() {
		if operation == nil {
			return
		}
		if markerID == "" {
			err = errors.Join(err, operation.Unlock())
			return
		}
		err = errors.Join(err, backend.complete(operation, markerID, lease))
	}()
	recovery, err := backend.prepare(
		operation,
		desiredPlatformSelectorV1(ctx, explicitPlatform, ProbeDockerNativePlatform),
		explicitPlatform == "",
	)
	if err != nil {
		return result, err
	}

	generationReference := "staged/" + recovery.PreviousEnvironment
	if recovery.PreviousCurrent != nil {
		generationReference = recovery.PreviousCurrent.Reference
		if err := backend.validateReference(
			recovery.PreviousCurrent.Reference,
			recovery.PreviousEnvironment,
			deploymentDir,
		); err != nil {
			return result, fmt.Errorf(
				"validate legacy staged image reference for recovery: %w",
				err,
			)
		}
	}
	admissionOperation := operation
	operation = nil
	admitted, err := backend.admit(ctx, deploymentDir, admissionOperation, ControlAdmissionInputV1{
		Operation:              deploy.ControlOperationStageV1,
		GenerationReference:    generationReference,
		Mode:                   ControlAdmissionForceV1,
		DockerPreflightTimeout: options.DockerPreflightTimeout,
	})
	if err != nil {
		return result, err
	}
	operation = admitted.Operation
	markerID = admitted.Marker.ID
	lease = admitted.Lease

	if recovery.PreviousCurrent != nil {
		if err := backend.stopOwned(
			ctx,
			operation,
			recovery.PreviousEnvironment,
			deploymentDir,
			options,
		); err != nil {
			return result, fmt.Errorf("stop legacy staged workload before recovery: %w", err)
		}
		if err := backend.removeReference(
			context.WithoutCancel(ctx),
			recovery.PreviousCurrent.Reference,
			recovery.PreviousEnvironment,
			deploymentDir,
		); err != nil {
			return result, fmt.Errorf("remove legacy staged image before recovery: %w", err)
		}
	}
	if err := backend.commit(operation, recovery); err != nil {
		return result, fmt.Errorf(
			"publish recovered staging state after stopping the old workload; rerun `reploy stage --update --force`: %w",
			err,
		)
	}
	result = deploy.DesiredStateUpdateResult{State: recovery.State, Changed: true}

	if completeErr := backend.complete(operation, markerID, lease); completeErr != nil {
		operation = nil
		return result, completeErr
	}
	operation = nil
	markerID = ""
	lease = nil
	changed, err := backend.syncControl(ctx, deploymentDir)
	if err != nil {
		return result, fmt.Errorf("refresh recovered staged control surface: %w", err)
	}
	result.Changed = result.Changed || changed
	return result, nil
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
	return deploy.SelectDesiredPlatformV1(
		ctx,
		deploymentDir,
		desiredPlatformSelectorV1(ctx, explicitPlatform, probeNative),
		registry.ValidatePackageRequest,
	)
}

func desiredPlatformSelectorV1(
	ctx context.Context,
	explicitPlatform string,
	probeNative func(context.Context) (blueprint.Platform, error),
) deploy.DesiredPlatformSelector {
	return func(document blueprint.Document) (blueprint.Platform, error) {
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
	}
}
