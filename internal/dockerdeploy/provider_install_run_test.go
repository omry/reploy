package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

func TestRunProviderInstallV1HoldsSourceBeforeDestinationAndReleasesInReverse(t *testing.T) {
	sourceDir := t.TempDir()
	destinationDir := t.TempDir()
	want, build := providerInstallRunBuildFixture(t, sourceDir)
	order := []string{}
	locks := map[string]*deploy.OperationLock{}
	backend := providerInstallRunBackend{
		acquire: func(ctx context.Context, dir string) (*deploy.OperationLock, error) {
			if dir == sourceDir {
				order = append(order, "acquire-source")
			} else if dir == destinationDir {
				order = append(order, "acquire-destination")
			} else {
				t.Fatalf("unexpected lock directory %q", dir)
			}
			lock, err := deploy.AcquireOperationLock(ctx, dir)
			locks[dir] = lock
			return lock, err
		},
		release: func(lock *deploy.OperationLock) error {
			if lock == locks[destinationDir] {
				order = append(order, "release-destination")
			} else if lock == locks[sourceDir] {
				order = append(order, "release-source")
			} else {
				t.Fatal("released unknown lock")
			}
			return lock.Unlock()
		},
		newStore: providerstore.NewStore,
		recoverDestination: func(_ context.Context, operation *deploy.OperationLock, store providerstore.Store, environment string, dir string) (bool, error) {
			order = append(order, "recover-destination")
			if operation != locks[destinationDir] || store.Root() != filepath.Join(destinationDir, ".reploy", providerstore.StoreDirName) || environment != "demo" || dir != destinationDir {
				t.Fatalf("destination recovery input = %p / %q / %q / %q", operation, store.Root(), environment, dir)
			}
			return true, nil
		},
		newReferences: func(environment string, dir string) (EnvironmentImageReferences, error) {
			if environment != "demo" || dir != destinationDir {
				t.Fatalf("reference input = %q / %q", environment, dir)
			}
			return fixedPublicationReferences(t, destinationDir, 0x81), nil
		},
		buildSource: func(_ context.Context, input LockedProviderBuildRunInputV1) (LockedProviderBuildExecutionResultV1, error) {
			order = append(order, "build-source")
			if err := input.Operation.RequireHeld(); err != nil {
				t.Fatal(err)
			}
			if _, found := locks[destinationDir]; found {
				t.Fatal("destination lock was acquired before the source build")
			}
			return build, nil
		},
		prepareAccount: func(_ context.Context, runAs blueprint.RunAs, store providerstore.Store, gotBuild CurrentBuild, input providerInstallRunInputV1) (providerInstallRunInputV1, error) {
			order = append(order, "prepare-install-account")
			if err := locks[sourceDir].RequireHeld(); err != nil {
				t.Fatal(err)
			}
			if _, found := locks[destinationDir]; found {
				t.Fatal("destination lock was acquired before account preparation")
			}
			if runAs != (blueprint.RunAs{}) || store.Root() != filepath.Join(sourceDir, ".reploy", providerstore.StoreDirName) || !reflect.DeepEqual(gotBuild.State, build.State) {
				t.Fatalf("account preparation input = %#v / %s / %#v", runAs, store.Root(), gotBuild)
			}
			input.Install.SystemUser = "service"
			input.Install.SystemGroup = "service"
			input.Install.SystemUID = 991
			input.Install.SystemGID = 992
			return input, nil
		},
		planInstallation: func(_ context.Context, input providerInstallPlanningV1) (providerInstallationPlanV1, error) {
			order = append(order, "plan-installation")
			if err := locks[sourceDir].RequireHeld(); err != nil {
				t.Fatal(err)
			}
			if _, found := locks[destinationDir]; found {
				t.Fatal("destination lock was acquired before installation planning")
			}
			if !reflect.DeepEqual(input.SourceBuild.State, build.State) {
				t.Fatalf("planned source build = %#v", input.SourceBuild)
			}
			if input.References != fixedPublicationReferences(t, destinationDir, 0x81) {
				t.Fatalf("planned references = %#v", input.References)
			}
			return providerInstallRunPlanFixture(destinationDir, input.References), nil
		},
		inspectHostTools: func(_ context.Context, backend installBackend) (providerInstallHostToolsV1, error) {
			order = append(order, "inspect-host-tools")
			if backend != installBackendLinuxSystemd {
				t.Fatalf("host tool backend = %q", backend)
			}
			return providerInstallHostToolsV1{DockerPath: "/usr/bin/docker", SystemctlPath: "/usr/bin/systemctl", IncludeDockerUnit: true}, nil
		},
		prepareDestination: func(_ context.Context, input lockedProviderInstallV1) (preparedProviderInstallFilesV1, error) {
			order = append(order, "prepare-destination")
			if err := input.SourceOperation.RequireHeld(); err != nil {
				t.Fatal(err)
			}
			if err := input.DestinationOperation.RequireHeld(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(input.Plan.Installation, installedBuildPublicationInstallation(destinationDir)) {
				t.Fatalf("prepared installation = %#v", input.Plan.Installation)
			}
			if input.References != fixedPublicationReferences(t, destinationDir, 0x81) {
				t.Fatalf("prepared references = %#v", input.References)
			}
			if input.HostTools.DockerPath != "/usr/bin/docker" || !input.HostTools.IncludeDockerUnit {
				t.Fatalf("prepared host tools = %#v", input.HostTools)
			}
			temporary := filepath.Join(destinationDir, ".prepared-candidate")
			if err := os.WriteFile(temporary, []byte("candidate"), 0o600); err != nil {
				t.Fatal(err)
			}
			return preparedProviderInstallFilesV1{Files: []preparedProviderInstallFileV1{{TemporaryPath: temporary}}}, nil
		},
		publish: func(_ context.Context, source *deploy.OperationLock, destination *deploy.OperationLock, _ providerstore.Store, _ providerstore.Store, input InstalledBuildPublicationInputV1) (deploy.StateV1, error) {
			order = append(order, "publish")
			if err := source.RequireHeld(); err != nil {
				t.Fatal(err)
			}
			if err := destination.RequireHeld(); err != nil {
				t.Fatal(err)
			}
			configuring := installedBuildPublicationInstallation(destinationDir)
			configuring.Status = deploy.InstallationStatusConfiguring
			if input.Environment != "demo" || !reflect.DeepEqual(input.Source.State, build.State) || input.References != fixedPublicationReferences(t, destinationDir, 0x81) || !reflect.DeepEqual(input.Installation, configuring) {
				t.Fatalf("publication input = %#v", input)
			}
			published := want
			deployment := *want.Deployment
			published.Deployment = &deployment
			published.Deployment.Installation.Status = deploy.InstallationStatusConfiguring
			return published, nil
		},
		publishFiles: func(prepared preparedProviderInstallFilesV1) error {
			order = append(order, "publish-files")
			if len(prepared.Files) != 1 {
				t.Fatalf("prepared files = %#v", prepared.Files)
			}
			return nil
		},
		activateDestination: func(_ context.Context, _ lockedProviderInstallV1, published deploy.StateV1) error {
			order = append(order, "activate-destination")
			if published.Deployment == nil || published.Deployment.Installation.Status != deploy.InstallationStatusConfiguring {
				t.Fatalf("activated state = %#v", published)
			}
			return nil
		},
		markReady: func(_ *deploy.OperationLock, installation deploy.InstallationStateV1) (deploy.StateV1, bool, error) {
			order = append(order, "mark-ready")
			if installation.Status != deploy.InstallationStatusReady {
				t.Fatalf("ready installation = %#v", installation)
			}
			return want, true, nil
		},
		startDestination: func(_ context.Context, _ lockedProviderInstallV1, ready deploy.StateV1) error {
			order = append(order, "start-destination")
			if ready.Deployment == nil || ready.Deployment.Installation.Status != deploy.InstallationStatusReady {
				t.Fatalf("started state = %#v", ready)
			}
			return nil
		},
	}

	result, err := runProviderInstallV1(t.Context(), providerInstallRunInputV1{
		SourceDeploymentDir: sourceDir, DestinationDeploymentDir: destinationDir,
		Install: providerInstallOptionsV1{Start: true},
	}, backend)
	if err != nil || !reflect.DeepEqual(result, want) {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	wantOrder := []string{"acquire-source", "build-source", "prepare-install-account", "plan-installation", "inspect-host-tools", "acquire-destination", "recover-destination", "prepare-destination", "publish", "publish-files", "activate-destination", "mark-ready", "start-destination", "release-destination", "release-source"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("order=%v want=%v", order, wantOrder)
	}
	if _, err := os.Stat(filepath.Join(destinationDir, ".prepared-candidate")); !os.IsNotExist(err) {
		t.Fatalf("prepared candidate was not cleaned: %v", err)
	}
}

func TestRunProviderInstallV1BuildFailureNeverAcquiresDestination(t *testing.T) {
	sourceDir := t.TempDir()
	destinationDir := t.TempDir()
	want := errors.New("source build failed")
	acquiredDestination := false
	releasedSource := false
	backend := providerInstallRunBackend{
		acquire: func(ctx context.Context, dir string) (*deploy.OperationLock, error) {
			if dir == destinationDir {
				acquiredDestination = true
			}
			return deploy.AcquireOperationLock(ctx, dir)
		},
		release: func(lock *deploy.OperationLock) error {
			releasedSource = true
			return lock.Unlock()
		},
		newStore:           providerstore.NewStore,
		recoverDestination: providerInstallRunRecoverDestinationFixture,
		newReferences: func(string, string) (EnvironmentImageReferences, error) {
			t.Fatal("created references after source build failure")
			return EnvironmentImageReferences{}, nil
		},
		buildSource: func(context.Context, LockedProviderBuildRunInputV1) (LockedProviderBuildExecutionResultV1, error) {
			return LockedProviderBuildExecutionResultV1{}, want
		},
		prepareAccount: providerInstallRunPrepareAccountFixture,
		planInstallation: func(context.Context, providerInstallPlanningV1) (providerInstallationPlanV1, error) {
			t.Fatal("planned installation after source build failure")
			return providerInstallationPlanV1{}, nil
		},
		inspectHostTools: func(context.Context, installBackend) (providerInstallHostToolsV1, error) {
			t.Fatal("inspected host tools after source build failure")
			return providerInstallHostToolsV1{}, nil
		},
		prepareDestination: func(context.Context, lockedProviderInstallV1) (preparedProviderInstallFilesV1, error) {
			t.Fatal("prepared destination after source build failure")
			return preparedProviderInstallFilesV1{}, nil
		},
		publish: func(context.Context, *deploy.OperationLock, *deploy.OperationLock, providerstore.Store, providerstore.Store, InstalledBuildPublicationInputV1) (deploy.StateV1, error) {
			t.Fatal("published after source build failure")
			return deploy.StateV1{}, nil
		},
		publishFiles: func(preparedProviderInstallFilesV1) error {
			t.Fatal("published files after source build failure")
			return nil
		},
		activateDestination: func(context.Context, lockedProviderInstallV1, deploy.StateV1) error {
			t.Fatal("activated destination after source build failure")
			return nil
		},
		markReady: func(*deploy.OperationLock, deploy.InstallationStateV1) (deploy.StateV1, bool, error) {
			t.Fatal("marked destination ready after source build failure")
			return deploy.StateV1{}, false, nil
		},
		startDestination: func(context.Context, lockedProviderInstallV1, deploy.StateV1) error {
			t.Fatal("started destination after source build failure")
			return nil
		},
	}

	_, err := runProviderInstallV1(t.Context(), providerInstallRunInputV1{
		SourceDeploymentDir: sourceDir, DestinationDeploymentDir: destinationDir,
	}, backend)
	if !errors.Is(err, want) || acquiredDestination || !releasedSource {
		t.Fatalf("error=%v acquired-destination=%v released-source=%v", err, acquiredDestination, releasedSource)
	}
}

func TestRunProviderInstallV1PlanningFailureNeverAcquiresDestination(t *testing.T) {
	sourceDir := t.TempDir()
	destinationDir := t.TempDir()
	_, build := providerInstallRunBuildFixture(t, sourceDir)
	want := errors.New("installation plan failed")
	acquiredDestination := false
	releasedSource := false
	backend := providerInstallRunBackend{
		acquire: func(ctx context.Context, dir string) (*deploy.OperationLock, error) {
			if dir == destinationDir {
				acquiredDestination = true
			}
			return deploy.AcquireOperationLock(ctx, dir)
		},
		release: func(lock *deploy.OperationLock) error {
			releasedSource = true
			return lock.Unlock()
		},
		newStore:           providerstore.NewStore,
		recoverDestination: providerInstallRunRecoverDestinationFixture,
		newReferences: func(string, string) (EnvironmentImageReferences, error) {
			return fixedPublicationReferences(t, destinationDir, 0x82), nil
		},
		buildSource: func(context.Context, LockedProviderBuildRunInputV1) (LockedProviderBuildExecutionResultV1, error) {
			return build, nil
		},
		prepareAccount: providerInstallRunPrepareAccountFixture,
		planInstallation: func(context.Context, providerInstallPlanningV1) (providerInstallationPlanV1, error) {
			return providerInstallationPlanV1{}, want
		},
		inspectHostTools: func(context.Context, installBackend) (providerInstallHostToolsV1, error) {
			t.Fatal("inspected host tools after installation planning failure")
			return providerInstallHostToolsV1{}, nil
		},
		prepareDestination: func(context.Context, lockedProviderInstallV1) (preparedProviderInstallFilesV1, error) {
			t.Fatal("prepared destination after installation planning failure")
			return preparedProviderInstallFilesV1{}, nil
		},
		publish: func(context.Context, *deploy.OperationLock, *deploy.OperationLock, providerstore.Store, providerstore.Store, InstalledBuildPublicationInputV1) (deploy.StateV1, error) {
			t.Fatal("published after installation planning failure")
			return deploy.StateV1{}, nil
		},
		publishFiles: func(preparedProviderInstallFilesV1) error {
			t.Fatal("published files after installation planning failure")
			return nil
		},
		activateDestination: func(context.Context, lockedProviderInstallV1, deploy.StateV1) error {
			t.Fatal("activated destination after installation planning failure")
			return nil
		},
		markReady: func(*deploy.OperationLock, deploy.InstallationStateV1) (deploy.StateV1, bool, error) {
			t.Fatal("marked destination ready after installation planning failure")
			return deploy.StateV1{}, false, nil
		},
		startDestination: func(context.Context, lockedProviderInstallV1, deploy.StateV1) error {
			t.Fatal("started destination after installation planning failure")
			return nil
		},
	}

	_, err := runProviderInstallV1(t.Context(), providerInstallRunInputV1{
		SourceDeploymentDir: sourceDir, DestinationDeploymentDir: destinationDir,
	}, backend)
	if !errors.Is(err, want) || acquiredDestination || !releasedSource {
		t.Fatalf("error=%v acquired-destination=%v released-source=%v", err, acquiredDestination, releasedSource)
	}
}

func TestRunProviderInstallV1LeavesCommittedConfiguringStateWhenActivationFails(t *testing.T) {
	sourceDir := t.TempDir()
	destinationDir := t.TempDir()
	want, build := providerInstallRunBuildFixture(t, sourceDir)
	wantActivation := errors.New("write systemd unit: permission denied")
	markedReady := false
	backend := providerInstallRunBackend{
		acquire:            deploy.AcquireOperationLock,
		release:            func(lock *deploy.OperationLock) error { return lock.Unlock() },
		newStore:           providerstore.NewStore,
		recoverDestination: providerInstallRunRecoverDestinationFixture,
		buildSource: func(context.Context, LockedProviderBuildRunInputV1) (LockedProviderBuildExecutionResultV1, error) {
			return build, nil
		},
		prepareAccount: providerInstallRunPrepareAccountFixture,
		newReferences: func(string, string) (EnvironmentImageReferences, error) {
			return fixedPublicationReferences(t, destinationDir, 0x84), nil
		},
		planInstallation: func(_ context.Context, input providerInstallPlanningV1) (providerInstallationPlanV1, error) {
			return providerInstallRunPlanFixture(destinationDir, input.References), nil
		},
		inspectHostTools: func(context.Context, installBackend) (providerInstallHostToolsV1, error) {
			return providerInstallHostToolsV1{DockerPath: "/usr/bin/docker", SystemctlPath: "/usr/bin/systemctl"}, nil
		},
		prepareDestination: func(context.Context, lockedProviderInstallV1) (preparedProviderInstallFilesV1, error) {
			return preparedProviderInstallFilesV1{Files: []preparedProviderInstallFileV1{}}, nil
		},
		publish: func(_ context.Context, _ *deploy.OperationLock, _ *deploy.OperationLock, _ providerstore.Store, _ providerstore.Store, input InstalledBuildPublicationInputV1) (deploy.StateV1, error) {
			if input.Installation.Status != deploy.InstallationStatusConfiguring {
				t.Fatalf("published installation status = %q", input.Installation.Status)
			}
			published := want
			deployment := *want.Deployment
			published.Deployment = &deployment
			published.Deployment.Installation = input.Installation
			return published, nil
		},
		publishFiles: func(preparedProviderInstallFilesV1) error { return nil },
		activateDestination: func(context.Context, lockedProviderInstallV1, deploy.StateV1) error {
			return wantActivation
		},
		markReady: func(*deploy.OperationLock, deploy.InstallationStateV1) (deploy.StateV1, bool, error) {
			markedReady = true
			return deploy.StateV1{}, false, nil
		},
		startDestination: func(context.Context, lockedProviderInstallV1, deploy.StateV1) error {
			t.Fatal("started destination after activation failure")
			return nil
		},
	}

	result, err := runProviderInstallV1(t.Context(), providerInstallRunInputV1{
		SourceDeploymentDir: sourceDir, DestinationDeploymentDir: destinationDir,
	}, backend)
	if !errors.Is(err, wantActivation) || !strings.Contains(err.Error(), "committed as configuring") || !strings.Contains(err.Error(), "rerun reploy install") {
		t.Fatalf("activation error = %v", err)
	}
	if markedReady || result.Deployment == nil || result.Deployment.Installation.Status != deploy.InstallationStatusConfiguring {
		t.Fatalf("activation failure result=%#v marked-ready=%v", result.Deployment, markedReady)
	}
}

func TestRunProviderInstallV1NoStartStillMarksInstallationReady(t *testing.T) {
	sourceDir := t.TempDir()
	destinationDir := t.TempDir()
	want, build := providerInstallRunBuildFixture(t, sourceDir)
	started := false
	backend := providerInstallRunBackend{
		acquire:            deploy.AcquireOperationLock,
		release:            func(lock *deploy.OperationLock) error { return lock.Unlock() },
		newStore:           providerstore.NewStore,
		recoverDestination: providerInstallRunRecoverDestinationFixture,
		buildSource: func(context.Context, LockedProviderBuildRunInputV1) (LockedProviderBuildExecutionResultV1, error) {
			return build, nil
		},
		prepareAccount: providerInstallRunPrepareAccountFixture,
		newReferences: func(string, string) (EnvironmentImageReferences, error) {
			return fixedPublicationReferences(t, destinationDir, 0x86), nil
		},
		planInstallation: func(_ context.Context, input providerInstallPlanningV1) (providerInstallationPlanV1, error) {
			return providerInstallRunPlanFixture(destinationDir, input.References), nil
		},
		inspectHostTools: func(context.Context, installBackend) (providerInstallHostToolsV1, error) {
			return providerInstallHostToolsV1{DockerPath: "/usr/bin/docker", SystemctlPath: "/usr/bin/systemctl"}, nil
		},
		prepareDestination: func(context.Context, lockedProviderInstallV1) (preparedProviderInstallFilesV1, error) {
			return preparedProviderInstallFilesV1{Files: []preparedProviderInstallFileV1{}}, nil
		},
		publish: func(_ context.Context, _ *deploy.OperationLock, _ *deploy.OperationLock, _ providerstore.Store, _ providerstore.Store, input InstalledBuildPublicationInputV1) (deploy.StateV1, error) {
			published := want
			deployment := *want.Deployment
			published.Deployment = &deployment
			published.Deployment.Installation = input.Installation
			return published, nil
		},
		publishFiles:        func(preparedProviderInstallFilesV1) error { return nil },
		activateDestination: func(context.Context, lockedProviderInstallV1, deploy.StateV1) error { return nil },
		markReady: func(*deploy.OperationLock, deploy.InstallationStateV1) (deploy.StateV1, bool, error) {
			return want, true, nil
		},
		startDestination: func(context.Context, lockedProviderInstallV1, deploy.StateV1) error {
			started = true
			return nil
		},
	}

	result, err := runProviderInstallV1(t.Context(), providerInstallRunInputV1{
		SourceDeploymentDir: sourceDir, DestinationDeploymentDir: destinationDir,
		Install: providerInstallOptionsV1{Start: false},
	}, backend)
	if err != nil {
		t.Fatal(err)
	}
	if started || result.Deployment == nil || result.Deployment.Installation.Status != deploy.InstallationStatusReady {
		t.Fatalf("no-start result=%#v started=%v", result.Deployment, started)
	}
}

func TestRunProviderInstallV1LeavesReadyStateWhenStartupFails(t *testing.T) {
	sourceDir := t.TempDir()
	destinationDir := t.TempDir()
	want, build := providerInstallRunBuildFixture(t, sourceDir)
	wantStartup := errors.New("service exited")
	backend := providerInstallRunBackend{
		acquire:            deploy.AcquireOperationLock,
		release:            func(lock *deploy.OperationLock) error { return lock.Unlock() },
		newStore:           providerstore.NewStore,
		recoverDestination: providerInstallRunRecoverDestinationFixture,
		buildSource: func(context.Context, LockedProviderBuildRunInputV1) (LockedProviderBuildExecutionResultV1, error) {
			return build, nil
		},
		prepareAccount: providerInstallRunPrepareAccountFixture,
		newReferences: func(string, string) (EnvironmentImageReferences, error) {
			return fixedPublicationReferences(t, destinationDir, 0x85), nil
		},
		planInstallation: func(_ context.Context, input providerInstallPlanningV1) (providerInstallationPlanV1, error) {
			return providerInstallRunPlanFixture(destinationDir, input.References), nil
		},
		inspectHostTools: func(context.Context, installBackend) (providerInstallHostToolsV1, error) {
			return providerInstallHostToolsV1{DockerPath: "/usr/bin/docker", SystemctlPath: "/usr/bin/systemctl"}, nil
		},
		prepareDestination: func(context.Context, lockedProviderInstallV1) (preparedProviderInstallFilesV1, error) {
			return preparedProviderInstallFilesV1{Files: []preparedProviderInstallFileV1{}}, nil
		},
		publish: func(_ context.Context, _ *deploy.OperationLock, _ *deploy.OperationLock, _ providerstore.Store, _ providerstore.Store, input InstalledBuildPublicationInputV1) (deploy.StateV1, error) {
			published := want
			deployment := *want.Deployment
			published.Deployment = &deployment
			published.Deployment.Installation = input.Installation
			return published, nil
		},
		publishFiles:        func(preparedProviderInstallFilesV1) error { return nil },
		activateDestination: func(context.Context, lockedProviderInstallV1, deploy.StateV1) error { return nil },
		markReady: func(*deploy.OperationLock, deploy.InstallationStateV1) (deploy.StateV1, bool, error) {
			return want, true, nil
		},
		startDestination: func(_ context.Context, _ lockedProviderInstallV1, ready deploy.StateV1) error {
			if ready.Deployment == nil || ready.Deployment.Installation.Status != deploy.InstallationStatusReady {
				t.Fatalf("startup state = %#v", ready)
			}
			return wantStartup
		},
	}

	result, err := runProviderInstallV1(t.Context(), providerInstallRunInputV1{
		SourceDeploymentDir: sourceDir, DestinationDeploymentDir: destinationDir,
		Install: providerInstallOptionsV1{Start: true},
	}, backend)
	if !errors.Is(err, wantStartup) || !strings.Contains(err.Error(), "installation is ready") || !strings.Contains(err.Error(), "remains in place") {
		t.Fatalf("startup error = %v", err)
	}
	if result.Deployment == nil || result.Deployment.Installation.Status != deploy.InstallationStatusReady {
		t.Fatalf("startup failure result = %#v", result.Deployment)
	}
}

func TestValidateProviderInstallDestinationAllowsRetryOfConfiguringService(t *testing.T) {
	destinationDir := t.TempDir()
	operation, _, _ := installedBuildPublicationSourceFixtureAtDir(t, destinationDir)
	existing := installedBuildPublicationInstallation(destinationDir)
	existing.Status = deploy.InstallationStatusConfiguring
	if _, _, err := operation.SetInstallationStateV1(existing); err != nil {
		t.Fatal(err)
	}

	retry := installedBuildPublicationInstallation(destinationDir)
	if err := validateProviderInstallDestinationV1(operation, retry); err != nil {
		t.Fatalf("matching configuring retry rejected: %v", err)
	}
}

func TestRunProviderInstallV1RejectsServiceRenameBeforeDestinationPreparation(t *testing.T) {
	sourceDir := t.TempDir()
	destinationDir := t.TempDir()
	_, build := providerInstallRunBuildFixture(t, sourceDir)
	destinationOperation, _, _ := installedBuildPublicationSourceFixtureAtDir(t, destinationDir)
	existing := installedBuildPublicationInstallation(destinationDir)
	existing.Service = "old-service"
	existing.UnitPath = "/etc/systemd/system/old-service.service"
	if _, _, err := destinationOperation.SetInstallationStateV1(existing); err != nil {
		t.Fatal(err)
	}
	if err := destinationOperation.Unlock(); err != nil {
		t.Fatal(err)
	}
	prepared := false
	published := false
	backend := providerInstallRunBackend{
		acquire:            deploy.AcquireOperationLock,
		release:            func(lock *deploy.OperationLock) error { return lock.Unlock() },
		newStore:           providerstore.NewStore,
		recoverDestination: providerInstallRunRecoverDestinationFixture,
		buildSource: func(context.Context, LockedProviderBuildRunInputV1) (LockedProviderBuildExecutionResultV1, error) {
			return build, nil
		},
		prepareAccount: providerInstallRunPrepareAccountFixture,
		newReferences: func(string, string) (EnvironmentImageReferences, error) {
			return fixedPublicationReferences(t, destinationDir, 0x83), nil
		},
		planInstallation: func(_ context.Context, input providerInstallPlanningV1) (providerInstallationPlanV1, error) {
			plan := providerInstallRunPlanFixture(destinationDir, input.References)
			plan.Installation.Service = "new-service"
			plan.Installation.UnitPath = "/etc/systemd/system/new-service.service"
			return plan, nil
		},
		inspectHostTools: func(context.Context, installBackend) (providerInstallHostToolsV1, error) {
			return providerInstallHostToolsV1{DockerPath: "/usr/bin/docker", SystemctlPath: "/usr/bin/systemctl"}, nil
		},
		prepareDestination: func(context.Context, lockedProviderInstallV1) (preparedProviderInstallFilesV1, error) {
			prepared = true
			return preparedProviderInstallFilesV1{}, nil
		},
		publish: func(context.Context, *deploy.OperationLock, *deploy.OperationLock, providerstore.Store, providerstore.Store, InstalledBuildPublicationInputV1) (deploy.StateV1, error) {
			published = true
			return deploy.StateV1{}, nil
		},
		publishFiles: func(preparedProviderInstallFilesV1) error {
			t.Fatal("published files after rejected service rename")
			return nil
		},
		activateDestination: func(context.Context, lockedProviderInstallV1, deploy.StateV1) error {
			t.Fatal("activated destination after rejected service rename")
			return nil
		},
		markReady: func(*deploy.OperationLock, deploy.InstallationStateV1) (deploy.StateV1, bool, error) {
			t.Fatal("marked destination ready after rejected service rename")
			return deploy.StateV1{}, false, nil
		},
		startDestination: func(context.Context, lockedProviderInstallV1, deploy.StateV1) error {
			t.Fatal("started destination after rejected service rename")
			return nil
		},
	}

	_, err := runProviderInstallV1(t.Context(), providerInstallRunInputV1{
		SourceDeploymentDir: sourceDir, DestinationDeploymentDir: destinationDir,
	}, backend)
	if err == nil || !strings.Contains(err.Error(), "already installed as service \"old-service\"") || !strings.Contains(err.Error(), "uninstall") {
		t.Fatalf("service rename error = %v", err)
	}
	if prepared || published {
		t.Fatalf("service rename prepared=%v published=%v", prepared, published)
	}
}

func providerInstallRunBuildFixture(t *testing.T, sourceDir string) (deploy.StateV1, LockedProviderBuildExecutionResultV1) {
	t.Helper()
	operation, _, current := installedBuildPublicationSourceFixtureAtDir(t, sourceDir)
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	result := LockedProviderBuildExecutionResultV1{State: current.State, Lock: current.Lock, Reused: true}
	want := current.State
	want.Deployment = &deploy.DeploymentStateV1{
		Schema: deploy.DeploymentStateSchemaV1,
		Installation: deploy.InstallationStateV1{
			Schema: deploy.InstallationSchemaV1, Status: deploy.InstallationStatusReady,
			TargetDir: filepath.Clean("/tmp/destination"), Scope: "system", Service: "demo",
			UnitPath: "/etc/systemd/system/demo.service", InstanceID: "demo-1", ComposeProject: "demo",
			ContainerName: "demo", NetworkName: "demo", Ports: []deploy.InstallationPortBindingV1{},
		},
	}
	return want, result
}

func providerInstallRunPlanFixture(destinationDir string, references EnvironmentImageReferences) providerInstallationPlanV1 {
	scope := blueprint.InstallScopeSystem
	return providerInstallationPlanV1{
		Installation: installedBuildPublicationInstallation(destinationDir),
		Docker: DockerExecutionPlan{
			Phase: blueprint.PhaseInstalled, Scope: &scope, Image: references.Generation,
			ContainerName: "demo", NetworkName: "demo",
		},
		Rendered: DockerRenderedInputs{Compose: []byte("services: {}\n"), Environment: map[string]string{"REPLOY_IMAGE": references.Generation}},
		Backend:  installBackendLinuxSystemd,
	}
}

func providerInstallRunPrepareAccountFixture(
	_ context.Context,
	_ blueprint.RunAs,
	_ providerstore.Store,
	_ CurrentBuild,
	input providerInstallRunInputV1,
) (providerInstallRunInputV1, error) {
	return input, nil
}

func providerInstallRunRecoverDestinationFixture(
	context.Context,
	*deploy.OperationLock,
	providerstore.Store,
	string,
	string,
) (bool, error) {
	return false, nil
}
