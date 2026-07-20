package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

type providerInstallRunInputV1 struct {
	SourceDeploymentDir      string
	DestinationDeploymentDir string
	Runtime                  StagedProviderBuildRuntimeV1
	Install                  providerInstallOptionsV1
	RunOptions               RunOptions
}

type providerInstallOptionsV1 struct {
	Scope         InstallScope
	Service       string
	PortOverrides []PortOverride
	Start         bool
	SystemUser    string
	SystemGroup   string
	SystemUID     int
	SystemGID     int
}

type providerInstallPlanningV1 struct {
	SourceBuild CurrentBuild
	References  EnvironmentImageReferences
	Input       providerInstallRunInputV1
}

type providerInstallationPlanV1 struct {
	Installation deploy.InstallationStateV1
	Docker       DockerExecutionPlan
	Rendered     DockerRenderedInputs
	Backend      installBackend
}

type lockedProviderInstallV1 struct {
	SourceOperation      *deploy.OperationLock
	DestinationOperation *deploy.OperationLock
	SourceStore          providerstore.Store
	DestinationStore     providerstore.Store
	SourceBuild          CurrentBuild
	Plan                 providerInstallationPlanV1
	References           EnvironmentImageReferences
	HostTools            providerInstallHostToolsV1
	Input                providerInstallRunInputV1
}

type providerInstallRunBackend struct {
	acquire             func(context.Context, string) (*deploy.OperationLock, error)
	release             func(*deploy.OperationLock) error
	newStore            func(string) (providerstore.Store, error)
	recoverDestination  func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (bool, error)
	buildSource         func(context.Context, LockedProviderBuildRunInputV1) (LockedProviderBuildExecutionResultV1, error)
	prepareAccount      func(context.Context, blueprint.RunAs, providerstore.Store, CurrentBuild, providerInstallRunInputV1) (providerInstallRunInputV1, error)
	newReferences       func(string, string) (EnvironmentImageReferences, error)
	planInstallation    func(context.Context, providerInstallPlanningV1) (providerInstallationPlanV1, error)
	inspectHostTools    func(context.Context, installBackend) (providerInstallHostToolsV1, error)
	prepareDestination  func(context.Context, lockedProviderInstallV1) (preparedProviderInstallFilesV1, error)
	publish             func(context.Context, *deploy.OperationLock, *deploy.OperationLock, providerstore.Store, providerstore.Store, InstalledBuildPublicationInputV1) (deploy.StateV1, error)
	publishFiles        func(preparedProviderInstallFilesV1) error
	activateDestination func(context.Context, lockedProviderInstallV1, deploy.StateV1) error
	markReady           func(*deploy.OperationLock, deploy.InstallationStateV1) (deploy.StateV1, bool, error)
	startDestination    func(context.Context, lockedProviderInstallV1, deploy.StateV1) error
}

// runProviderInstallV1 owns the two-lock install sequence. It remains internal
// until candidate preparation and host activation implement the complete
// installation contract.
func runProviderInstallV1(
	ctx context.Context,
	input providerInstallRunInputV1,
	backend providerInstallRunBackend,
) (result deploy.StateV1, err error) {
	if ctx == nil {
		return deploy.StateV1{}, fmt.Errorf("run provider install requires a context")
	}
	if err := ctx.Err(); err != nil {
		return deploy.StateV1{}, err
	}
	if input.SourceDeploymentDir == "" || input.DestinationDeploymentDir == "" {
		return deploy.StateV1{}, fmt.Errorf("run provider install requires source and destination deployment directories")
	}
	if backend.acquire == nil || backend.release == nil || backend.newStore == nil || backend.recoverDestination == nil || backend.buildSource == nil || backend.prepareAccount == nil || backend.newReferences == nil || backend.planInstallation == nil || backend.inspectHostTools == nil || backend.prepareDestination == nil || backend.publish == nil || backend.publishFiles == nil || backend.activateDestination == nil || backend.markReady == nil || backend.startDestination == nil {
		return deploy.StateV1{}, fmt.Errorf("run provider install requires a complete backend")
	}
	sourceDir, err := filepath.Abs(input.SourceDeploymentDir)
	if err != nil {
		return deploy.StateV1{}, fmt.Errorf("resolve provider install source: %w", err)
	}
	destinationDir, err := filepath.Abs(input.DestinationDeploymentDir)
	if err != nil {
		return deploy.StateV1{}, fmt.Errorf("resolve provider install destination: %w", err)
	}
	if installPathsOverlap(sourceDir, destinationDir) {
		return deploy.StateV1{}, fmt.Errorf("provider install source and destination must not overlap")
	}
	input.SourceDeploymentDir = sourceDir
	input.DestinationDeploymentDir = destinationDir

	sourceOperation, err := backend.acquire(ctx, sourceDir)
	if err != nil {
		return deploy.StateV1{}, err
	}
	defer func() {
		if releaseErr := backend.release(sourceOperation); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()
	sourceStore, err := backend.newStore(sourceDir)
	if err != nil {
		return deploy.StateV1{}, err
	}
	built, err := backend.buildSource(ctx, LockedProviderBuildRunInputV1{
		Operation: sourceOperation, Store: sourceStore, DeploymentDir: sourceDir,
		Runtime: input.Runtime, RunOptions: input.RunOptions,
	})
	if err != nil {
		return deploy.StateV1{}, err
	}
	if built.State.Current == nil {
		return deploy.StateV1{}, fmt.Errorf("provider install source build did not publish a current generation")
	}
	sourceBuild := CurrentBuild{State: built.State, Generation: *built.State.Current, Lock: built.Lock}
	document, err := blueprint.DecodeResolvedDocumentV1(sourceBuild.State.Blueprint)
	if err != nil {
		return deploy.StateV1{}, fmt.Errorf("provider install source blueprint: %w", err)
	}
	input, err = backend.prepareAccount(ctx, document.Environment.Install.System.RunAs, sourceStore, sourceBuild, input)
	if err != nil {
		return deploy.StateV1{}, fmt.Errorf("prepare provider installation account: %w", err)
	}
	references, err := backend.newReferences(document.Environment.ID, destinationDir)
	if err != nil {
		return deploy.StateV1{}, fmt.Errorf("plan provider installation image reference: %w", err)
	}
	if err := ValidateEnvironmentImageReferences(references, document.Environment.ID, destinationDir); err != nil {
		return deploy.StateV1{}, fmt.Errorf("plan provider installation image reference: %w", err)
	}
	plan, err := backend.planInstallation(ctx, providerInstallPlanningV1{SourceBuild: sourceBuild, References: references, Input: input})
	if err != nil {
		return deploy.StateV1{}, fmt.Errorf("plan provider installation: %w", err)
	}
	if err := validateProviderInstallationPlanV1(plan, references); err != nil {
		return deploy.StateV1{}, fmt.Errorf("plan provider installation: %w", err)
	}
	if plan.Installation.TargetDir != destinationDir {
		return deploy.StateV1{}, fmt.Errorf("provider install record does not name the destination deployment")
	}
	if plan.Installation.Status != deploy.InstallationStatusReady {
		return deploy.StateV1{}, fmt.Errorf("provider installation plan must describe a ready installation")
	}
	hostTools, err := backend.inspectHostTools(ctx, plan.Backend)
	if err != nil {
		return deploy.StateV1{}, fmt.Errorf("inspect provider installation host tools: %w", err)
	}
	configuring := plan.Installation
	configuring.Status = deploy.InstallationStatusConfiguring
	publicationInput := InstalledBuildPublicationInputV1{
		Environment: document.Environment.ID, SourceDeploymentDir: sourceDir,
		DestinationDeploymentDir: destinationDir, Source: sourceBuild, Installation: configuring, References: references,
	}
	if err := validateInstalledBuildSource(publicationInput); err != nil {
		return deploy.StateV1{}, err
	}

	destinationOperation, err := backend.acquire(ctx, destinationDir)
	if err != nil {
		return deploy.StateV1{}, err
	}
	defer func() {
		if releaseErr := backend.release(destinationOperation); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()
	destinationStore, err := backend.newStore(destinationDir)
	if err != nil {
		return deploy.StateV1{}, err
	}
	if _, err := backend.recoverDestination(ctx, destinationOperation, destinationStore, document.Environment.ID, destinationDir); err != nil {
		return deploy.StateV1{}, fmt.Errorf("recover provider install destination: %w", err)
	}
	if err := validateProviderInstallDestinationV1(destinationOperation, plan.Installation); err != nil {
		return deploy.StateV1{}, err
	}
	locked := lockedProviderInstallV1{
		SourceOperation: sourceOperation, DestinationOperation: destinationOperation,
		SourceStore: sourceStore, DestinationStore: destinationStore,
		SourceBuild: sourceBuild, Plan: plan, References: references, HostTools: hostTools, Input: input,
	}
	prepared, err := backend.prepareDestination(ctx, locked)
	if err != nil {
		return deploy.StateV1{}, err
	}
	defer func() { err = errors.Join(err, prepared.Cleanup()) }()
	published, err := backend.publish(ctx, sourceOperation, destinationOperation, sourceStore, destinationStore, publicationInput)
	if err != nil {
		return deploy.StateV1{}, err
	}
	if err := backend.publishFiles(prepared); err != nil {
		return published, fmt.Errorf("installation was committed as configuring but host configuration failed: %w; resolve the cause and rerun reploy install", err)
	}
	if err := backend.activateDestination(ctx, locked, published); err != nil {
		return published, fmt.Errorf("installation was committed as configuring but host configuration failed: %w; resolve the cause and rerun reploy install", err)
	}
	ready, _, err := backend.markReady(destinationOperation, plan.Installation)
	if err != nil {
		return published, fmt.Errorf("host configuration succeeded but installation could not be marked ready: %w; rerun reploy install", err)
	}
	if input.Install.Start {
		if err := backend.startDestination(ctx, locked, ready); err != nil {
			return ready, fmt.Errorf("installation is ready but startup failed: %w; the installation remains in place for inspection", err)
		}
	}
	return ready, nil
}

func validateProviderInstallDestinationV1(operation *deploy.OperationLock, installation deploy.InstallationStateV1) error {
	if operation == nil {
		return fmt.Errorf("validate provider install destination requires an operation lock")
	}
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return err
	}
	if !found || state.Deployment == nil {
		return nil
	}
	existing := state.Deployment.Installation.Service
	if existing != installation.Service {
		return fmt.Errorf("destination is already installed as service %q; uninstall it before installing as service %q", existing, installation.Service)
	}
	return nil
}
