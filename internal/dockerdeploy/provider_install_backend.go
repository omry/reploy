package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

func newProviderInstallRunBackendV1() providerInstallRunBackend {
	return providerInstallRunBackend{
		acquire:            deploy.AcquireOperationLock,
		release:            func(lock *deploy.OperationLock) error { return lock.Unlock() },
		newStore:           providerstore.NewStore,
		recoverDestination: recoverProviderInstallDestinationV1,
		buildSource:        RunLockedProviderBuildV1,
		prepareAccount:     prepareProviderInstallAccountV1,
		newReferences:      NewEnvironmentImageReferences,
		planInstallation:   planProviderInstallationV1,
		inspectHostTools:   inspectProviderInstallHostToolsV1,
		prepareDestination: func(ctx context.Context, locked lockedProviderInstallV1) (preparedProviderInstallFilesV1, error) {
			return prepareProviderInstallDestinationV1(ctx, locked, locked.HostTools.DockerPath, locked.HostTools.IncludeDockerUnit)
		},
		publish: PublishInstalledBuildV1,
		publishFiles: func(prepared preparedProviderInstallFilesV1) error {
			return prepared.Publish()
		},
		activateDestination: func(ctx context.Context, locked lockedProviderInstallV1, _ deploy.StateV1) error {
			return configureProviderInstallHostV1(ctx, locked.Plan, locked.HostTools, locked.Input.RunOptions)
		},
		markReady: func(lock *deploy.OperationLock, installation deploy.InstallationStateV1) (deploy.StateV1, bool, error) {
			return lock.MarkInstallationReadyV1(installation)
		},
		startDestination: func(ctx context.Context, locked lockedProviderInstallV1, _ deploy.StateV1) error {
			return startProviderInstallHostV1(ctx, locked.Plan, locked.HostTools, locked.Input.RunOptions)
		},
	}
}

func recoverProviderInstallDestinationV1(
	ctx context.Context,
	operation *deploy.OperationLock,
	store providerstore.Store,
	environment string,
	deploymentDir string,
) (bool, error) {
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return false, fmt.Errorf("read install destination state: %w", err)
	}
	var current *deploy.EnvironmentGenerationState
	if found {
		current = state.Current
	}
	return RecoverPendingPublication(
		ctx, operation, store, current, environment, deploymentDir,
		registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1,
	)
}
