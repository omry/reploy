package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

type providerInstallRunInputV1 struct {
	SourceDeploymentDir      string
	DestinationDeploymentDir string
	Runtime                  StagedProviderBuildRuntimeV1
	ControlMode              ControlAdmissionModeV1
	Install                  providerInstallOptionsV1
	RunOptions               RunOptions
	result                   *ProviderInstallResultV1
}

// ProviderInstallResultV1 describes what a completed install actually did.
// It is derived from the locked install plan and prior destination state.
type ProviderInstallResultV1 struct {
	State         deploy.StateV1
	Environment   string
	TargetDir     string
	ControlScript string
	Service       string
	Updated       bool
	ImageReused   bool
	Started       bool
	PathUpdates   []PathUpdateAction
}

type providerInstallOptionsV1 struct {
	Scope         InstallScope
	Service       string
	PortOverrides []PortOverride
	Replace       []string
	Clean         bool
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
	Installation  deploy.InstallationStateV1
	ControlScript string
	Docker        DockerExecutionPlan
	Rendered      DockerRenderedInputs
	PathUpdates   []PathUpdateAction
	PreservePaths []string
	AfterInstall  LifecyclePlan
	Start         LifecyclePlan
	Backend       installBackend
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
	acquire              func(context.Context, string) (*deploy.OperationLock, error)
	release              func(*deploy.OperationLock) error
	readState            func(*deploy.OperationLock) (deploy.StateV1, bool, error)
	admit                func(context.Context, string, *deploy.OperationLock, ControlAdmissionInputV1) (AdmittedControlV1, error)
	complete             func(*deploy.OperationLock, string, *deploy.ControlLeaseV1) error
	newStore             func(string) (providerstore.Store, error)
	recoverDestination   func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (bool, error)
	buildSource          func(context.Context, LockedProviderBuildRunInputV1) (LockedProviderBuildExecutionResultV1, error)
	prepareAccount       func(context.Context, blueprint.RunAs, providerstore.Store, CurrentBuild, providerInstallRunInputV1) (providerInstallRunInputV1, error)
	newReferences        func(string, string) (EnvironmentImageReferences, error)
	planInstallation     func(context.Context, providerInstallPlanningV1) (providerInstallationPlanV1, error)
	inspectHostTools     func(context.Context, installBackend) (providerInstallHostToolsV1, error)
	preflightDestination func(providerstore.Store, CurrentBuild, string) error
	ensureDestination    func(string) (bool, error)
	cleanupDestination   func(string) error
	prepareDestination   func(context.Context, lockedProviderInstallV1) (preparedProviderInstallFilesV1, error)
	stopDestination      func(context.Context, lockedProviderInstallV1, deploy.StateV1) error
	publish              func(context.Context, *deploy.OperationLock, *deploy.OperationLock, providerstore.Store, providerstore.Store, InstalledBuildPublicationInputV1) (deploy.StateV1, error)
	publishFiles         func(preparedProviderInstallFilesV1) error
	activateDestination  func(context.Context, lockedProviderInstallV1, deploy.StateV1) error
	markReady            func(*deploy.OperationLock, deploy.InstallationStateV1) (deploy.StateV1, bool, error)
	startDestination     func(context.Context, lockedProviderInstallV1, deploy.StateV1) error
}

// runProviderInstallV1 owns the two-lock install sequence.
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
	if input.SourceDeploymentDir == "" {
		return deploy.StateV1{}, fmt.Errorf("run provider install requires a source deployment directory")
	}
	if input.ControlMode == "" {
		input.ControlMode = ControlAdmissionImmediateV1
	}
	if !validControlAdmissionModeV1(input.ControlMode) {
		return deploy.StateV1{}, fmt.Errorf("install control admission mode must be immediate, wait, drain, or force")
	}
	sourceDir, err := filepath.Abs(input.SourceDeploymentDir)
	if err != nil {
		return deploy.StateV1{}, fmt.Errorf("resolve provider install source: %w", err)
	}
	destinationDir := ""
	if input.DestinationDeploymentDir != "" {
		destinationDir, err = filepath.Abs(input.DestinationDeploymentDir)
		if err != nil {
			return deploy.StateV1{}, fmt.Errorf("resolve provider install destination: %w", err)
		}
		if installPathsOverlap(sourceDir, destinationDir) {
			return deploy.StateV1{}, fmt.Errorf("provider install source and destination must not overlap")
		}
	}
	if backend.acquire == nil || backend.release == nil || backend.readState == nil || backend.admit == nil || backend.complete == nil || backend.newStore == nil || backend.recoverDestination == nil || backend.buildSource == nil || backend.prepareAccount == nil || backend.newReferences == nil || backend.planInstallation == nil || backend.inspectHostTools == nil || backend.preflightDestination == nil || backend.ensureDestination == nil || backend.cleanupDestination == nil || backend.prepareDestination == nil || backend.stopDestination == nil || backend.publish == nil || backend.publishFiles == nil || backend.activateDestination == nil || backend.markReady == nil || backend.startDestination == nil {
		return deploy.StateV1{}, fmt.Errorf("run provider install requires a complete backend")
	}
	input.SourceDeploymentDir = sourceDir
	if destinationDir != "" {
		if err := preflightProviderInstallDestinationRoleV1(ctx, destinationDir, backend.acquire, backend.release); err != nil {
			return deploy.StateV1{}, err
		}
	}

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
	writeProviderBuildProgress(input.RunOptions.Progress, "preparing current staged environment")
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
	if destinationDir == "" {
		destinationDir, err = resolveProviderInstallDestinationV1(document, input)
		if err != nil {
			return deploy.StateV1{}, fmt.Errorf("resolve provider install destination: %w", err)
		}
		if installPathsOverlap(sourceDir, destinationDir) {
			return deploy.StateV1{}, fmt.Errorf("provider install source and destination must not overlap")
		}
	}
	input.DestinationDeploymentDir = destinationDir
	service, err := providerInstallServiceV1(document.Environment.ID, input.Install.Service)
	if err != nil {
		return deploy.StateV1{}, err
	}

	var destinationOperation *deploy.OperationLock
	var destinationStore providerstore.Store
	markerID := ""
	var controlLease *deploy.ControlLeaseV1
	destinationCleanupSafe := true
	releaseDestination := func() {
		if destinationOperation == nil {
			return
		}
		var releaseErr error
		if markerID == "" {
			releaseErr = backend.release(destinationOperation)
		} else {
			releaseErr = backend.complete(destinationOperation, markerID, controlLease)
		}
		if releaseErr != nil {
			err = errors.Join(err, releaseErr)
		} else {
			destinationCleanupSafe = true
		}
	}
	destinationExists, err := providerInstallDestinationExistsV1(destinationDir)
	if err != nil {
		return deploy.StateV1{}, err
	}
	if destinationExists {
		destinationOperation, err = backend.acquire(ctx, destinationDir)
		if err != nil {
			return deploy.StateV1{}, err
		}
		defer releaseDestination()
		destinationCleanupSafe = false
		destinationStore, err = backend.newStore(destinationDir)
		if err != nil {
			return deploy.StateV1{}, err
		}
		if err := validateProviderInstallDestinationServiceV1(destinationOperation, service); err != nil {
			return deploy.StateV1{}, err
		}
		if _, err := backend.recoverDestination(ctx, destinationOperation, destinationStore, document.Environment.ID, destinationDir); err != nil {
			return deploy.StateV1{}, fmt.Errorf("recover provider install destination: %w", err)
		}
		if err := validateProviderInstallDestinationServiceV1(destinationOperation, service); err != nil {
			return deploy.StateV1{}, err
		}
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
	if err := backend.preflightDestination(sourceStore, sourceBuild, destinationDir); err != nil {
		return deploy.StateV1{}, fmt.Errorf("check install disk space before creating destination: %w", err)
	}
	destinationCreated, err := backend.ensureDestination(destinationDir)
	if err != nil {
		return deploy.StateV1{}, err
	}
	destinationPublished := false
	defer func() {
		if err != nil && destinationCreated && !destinationPublished && destinationCleanupSafe {
			err = errors.Join(err, backend.cleanupDestination(destinationDir))
		}
	}()
	configuring := plan.Installation
	configuring.Status = deploy.InstallationStatusConfiguring
	publicationInput := InstalledBuildPublicationInputV1{
		Environment: document.Environment.ID, SourceDeploymentDir: sourceDir,
		DestinationDeploymentDir: destinationDir, Source: sourceBuild, Installation: configuring, References: references,
	}
	if err := validateInstalledBuildSource(publicationInput); err != nil {
		return deploy.StateV1{}, err
	}

	if destinationOperation == nil {
		destinationOperation, err = backend.acquire(ctx, destinationDir)
		if err != nil {
			return deploy.StateV1{}, err
		}
		defer releaseDestination()
		destinationCleanupSafe = false
		destinationStore, err = backend.newStore(destinationDir)
		if err != nil {
			return deploy.StateV1{}, err
		}
		if err := validateProviderInstallDestinationServiceV1(destinationOperation, service); err != nil {
			return deploy.StateV1{}, err
		}
		if _, err := backend.recoverDestination(ctx, destinationOperation, destinationStore, document.Environment.ID, destinationDir); err != nil {
			return deploy.StateV1{}, fmt.Errorf("recover provider install destination: %w", err)
		}
	}
	if err := validateProviderInstallDestinationV1(destinationOperation, plan.Installation); err != nil {
		return deploy.StateV1{}, err
	}
	destinationState, destinationFound, err := backend.readState(destinationOperation)
	if err != nil {
		return deploy.StateV1{}, err
	}
	updated := destinationFound && destinationState.Deployment != nil
	if updated {
		writeProviderBuildProgress(input.RunOptions.Progress, "updating existing installation")
	} else {
		writeProviderBuildProgress(input.RunOptions.Progress, "planning new installation")
	}
	if input.result != nil {
		*input.result = ProviderInstallResultV1{
			Environment: document.Environment.ID, TargetDir: destinationDir,
			ControlScript: plan.ControlScript, Service: plan.Installation.Service,
			Updated: updated, Started: input.Install.Start,
			PathUpdates: append([]PathUpdateAction(nil), plan.PathUpdates...),
		}
		if updated && destinationState.Current != nil {
			input.result.ImageReused = destinationState.Current.ImageDigest == sourceBuild.Generation.ImageDigest
		}
	}
	destinationGeneration := providerInstallDestinationGenerationV1(destinationState, destinationFound, references.Generation)
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
	admitted, err := backend.admit(ctx, destinationDir, destinationOperation, ControlAdmissionInputV1{
		Operation:              deploy.ControlOperationInstallV1,
		GenerationReference:    destinationGeneration,
		Mode:                   input.ControlMode,
		DockerPreflightTimeout: input.RunOptions.DockerPreflightTimeout,
		Notice:                 controlWaitNoticeWriterV1(input.RunOptions),
	})
	if err != nil {
		if errors.Is(err, deploy.ErrLiveRunConflict) {
			return deploy.StateV1{}, fmt.Errorf("%w; rerun with --wait to queue this install", err)
		}
		return deploy.StateV1{}, err
	}
	previousDestinationOperation := destinationOperation
	destinationOperation = admitted.Operation
	locked.DestinationOperation = destinationOperation
	markerID = admitted.Marker.ID
	controlLease = admitted.Lease
	if destinationOperation != previousDestinationOperation {
		waitingState, waitingFound, readErr := backend.readState(destinationOperation)
		if readErr != nil {
			return deploy.StateV1{}, readErr
		}
		if providerInstallDestinationGenerationV1(waitingState, waitingFound, references.Generation) != destinationGeneration {
			return deploy.StateV1{}, fmt.Errorf("destination generation changed while install was waiting; retry the command")
		}
		if waitingFound != destinationFound || !reflect.DeepEqual(waitingState, destinationState) {
			return deploy.StateV1{}, fmt.Errorf("destination state changed while install was waiting; retry the command")
		}
		if err := validateProviderInstallDestinationV1(destinationOperation, plan.Installation); err != nil {
			return deploy.StateV1{}, err
		}
	}
	if destinationFound && destinationState.Deployment != nil {
		writeProviderBuildProgress(input.RunOptions.Progress, "stopping existing service")
		if err := backend.stopDestination(ctx, locked, destinationState); err != nil {
			return deploy.StateV1{}, fmt.Errorf("stop existing installed workload before cutover: %w", err)
		}
	}
	writeProviderBuildProgress(input.RunOptions.Progress, "installing environment generation")
	published, err := backend.publish(ctx, sourceOperation, destinationOperation, sourceStore, destinationStore, publicationInput)
	if err != nil {
		return deploy.StateV1{}, err
	}
	destinationPublished = true
	writeProviderBuildProgress(input.RunOptions.Progress, "configuring installed environment")
	if err := backend.publishFiles(prepared); err != nil {
		return published, fmt.Errorf("installation was committed as configuring but installation configuration failed: %w; resolve the cause and rerun reploy install", err)
	}
	if err := backend.activateDestination(ctx, locked, published); err != nil {
		return published, fmt.Errorf("installation was committed as configuring but installation configuration failed: %w; resolve the cause and rerun reploy install", err)
	}
	ready, _, err := backend.markReady(destinationOperation, plan.Installation)
	if err != nil {
		return published, fmt.Errorf("host configuration succeeded but installation could not be marked ready: %w; rerun reploy install", err)
	}
	if input.Install.Start {
		writeProviderBuildProgress(input.RunOptions.Progress, "starting installed service")
		if err := backend.startDestination(ctx, locked, ready); err != nil {
			return ready, fmt.Errorf("installation is ready but startup failed: %w; the installation remains in place for inspection", err)
		}
	}
	if input.result != nil {
		input.result.State = ready
	}
	return ready, nil
}

func preflightProviderInstallDestinationRoleV1(
	ctx context.Context,
	destinationDir string,
	acquire func(context.Context, string) (*deploy.OperationLock, error),
	release func(*deploy.OperationLock) error,
) (err error) {
	if ctx == nil {
		return fmt.Errorf("preflight provider install destination requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if destinationDir == "" || acquire == nil || release == nil {
		return fmt.Errorf("preflight provider install destination requires a directory and lock backend")
	}
	if _, err := os.Lstat(filepath.Join(destinationDir, StateFileName)); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect provider install destination state: %w", err)
	}
	operation, err := acquire(ctx, destinationDir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release(operation)) }()
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return err
	}
	if found && state.Deployment == nil {
		return fmt.Errorf("destination contains a staging deployment; choose a different install target")
	}
	return nil
}

func providerInstallDestinationExistsV1(destinationDir string) (bool, error) {
	info, err := os.Lstat(destinationDir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect provider install destination: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("install destination must be a real directory: %s", destinationDir)
	}
	return true, nil
}

func resolveProviderInstallDestinationV1(document blueprint.Document, input providerInstallRunInputV1) (string, error) {
	scope, err := ParseInstallScope(string(input.Install.Scope))
	if err != nil {
		return "", err
	}
	platform, err := installHostPlatformV1(input.Runtime.Host)
	if err != nil {
		return "", err
	}
	roots, err := installTargetRoots(platform.GOOS)
	if err != nil {
		return "", err
	}
	target, err := blueprint.ResolveInstallTarget(document.Environment.Install.Target, document.Environment.ID, blueprint.InstallTargetContext{
		Host: input.Runtime.Host, Scope: blueprint.InstallScope(scope),
		Paths: blueprint.HostPaths{
			Home: roots.UserHome, UserData: roots.UserData, LocalData: roots.UserLocalData,
			SystemData: roots.SystemData,
		},
		Variables: document.Environment.Vars,
	})
	if err != nil {
		return "", err
	}
	return filepath.Abs(target)
}

func providerInstallDestinationGenerationV1(state deploy.StateV1, found bool, incoming string) string {
	if found && state.Current != nil {
		return state.Current.Reference
	}
	return incoming
}

func validateProviderInstallDestinationV1(operation *deploy.OperationLock, installation deploy.InstallationStateV1) error {
	return validateProviderInstallDestinationServiceV1(operation, installation.Service)
}

func validateProviderInstallDestinationServiceV1(operation *deploy.OperationLock, service string) error {
	if operation == nil {
		return fmt.Errorf("validate provider install destination requires an operation lock")
	}
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if state.Deployment == nil {
		return fmt.Errorf("destination contains a staging deployment; choose a different install target")
	}
	existing := state.Deployment.Installation.Service
	if existing != service {
		return fmt.Errorf("destination is already installed as service %q; uninstall it before installing as service %q", existing, service)
	}
	return nil
}
