package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

type providerInstallPrepareDestinationBackendV1 struct {
	files            func(providerInstallationPlanV1, string, bool) ([]providerInstallFileCandidateV1, error)
	diskRequirements func(providerstore.Store, providerstore.Store, InstalledBuildPublicationInputV1, *deploy.EnvironmentGenerationState, []providerInstallFileCandidateV1) ([]providerInstallDiskRequirementV1, error)
	preflight        func([]providerInstallDiskRequirementV1) error
	prepare          func([]providerInstallFileCandidateV1) (preparedProviderInstallFilesV1, error)
}

func prepareProviderInstallDestinationV1(
	ctx context.Context,
	locked lockedProviderInstallV1,
	dockerPath string,
	includeDockerUnit bool,
) (preparedProviderInstallFilesV1, error) {
	return prepareProviderInstallDestinationWithV1(ctx, locked, dockerPath, includeDockerUnit, providerInstallPrepareDestinationBackendV1{
		files:            providerInstallFilesV1,
		diskRequirements: providerInstallDiskRequirementsV1,
		preflight:        preflightProviderInstallDiskSpaceV1,
		prepare:          prepareProviderInstallFileCandidatesV1,
	})
}

func prepareProviderInstallDestinationWithV1(
	ctx context.Context,
	locked lockedProviderInstallV1,
	dockerPath string,
	includeDockerUnit bool,
	backend providerInstallPrepareDestinationBackendV1,
) (preparedProviderInstallFilesV1, error) {
	if ctx == nil {
		return preparedProviderInstallFilesV1{}, fmt.Errorf("prepare provider install destination requires a context")
	}
	if err := ctx.Err(); err != nil {
		return preparedProviderInstallFilesV1{}, err
	}
	if locked.SourceOperation == nil || locked.DestinationOperation == nil || locked.SourceOperation == locked.DestinationOperation {
		return preparedProviderInstallFilesV1{}, fmt.Errorf("prepare provider install destination requires distinct operation locks")
	}
	if err := locked.SourceOperation.RequireHeld(); err != nil {
		return preparedProviderInstallFilesV1{}, err
	}
	if err := locked.DestinationOperation.RequireHeld(); err != nil {
		return preparedProviderInstallFilesV1{}, err
	}
	if backend.files == nil || backend.diskRequirements == nil || backend.preflight == nil || backend.prepare == nil {
		return preparedProviderInstallFilesV1{}, fmt.Errorf("prepare provider install destination requires a complete backend")
	}

	candidates, err := backend.files(locked.Plan, dockerPath, includeDockerUnit)
	if err != nil {
		return preparedProviderInstallFilesV1{}, err
	}
	destinationState, found, err := locked.DestinationOperation.ReadStateV1()
	if err != nil {
		return preparedProviderInstallFilesV1{}, err
	}
	var old *deploy.EnvironmentGenerationState
	if found && destinationState.Current != nil {
		generation := *destinationState.Current
		old = &generation
	}
	document, err := blueprint.DecodeResolvedDocumentV1(locked.SourceBuild.State.Blueprint)
	if err != nil {
		return preparedProviderInstallFilesV1{}, fmt.Errorf("prepare provider install source blueprint: %w", err)
	}
	configuring := locked.Plan.Installation
	configuring.Status = deploy.InstallationStatusConfiguring
	publication := InstalledBuildPublicationInputV1{
		Environment: document.Environment.ID, SourceDeploymentDir: locked.Input.SourceDeploymentDir,
		DestinationDeploymentDir: locked.Input.DestinationDeploymentDir, Source: locked.SourceBuild,
		Installation: configuring, References: locked.References,
	}
	requirements, err := backend.diskRequirements(locked.SourceStore, locked.DestinationStore, publication, old, candidates)
	if err != nil {
		return preparedProviderInstallFilesV1{}, err
	}
	if err := ctx.Err(); err != nil {
		return preparedProviderInstallFilesV1{}, err
	}
	if err := backend.preflight(requirements); err != nil {
		return preparedProviderInstallFilesV1{}, err
	}
	if err := ctx.Err(); err != nil {
		return preparedProviderInstallFilesV1{}, err
	}
	prepared, err := backend.prepare(candidates)
	if err != nil {
		return preparedProviderInstallFilesV1{}, err
	}
	return prepared, nil
}
