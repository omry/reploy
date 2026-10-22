//go:build linux

package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

// The opt-in binary is the actual product CLI. Docker is the only process
// replaced: this regression must exercise the generated wrapper and metadata
// admission as the operator, without contacting the host's Docker daemon.
func TestPrivilegedInstallLeavesRealControllerUsable(t *testing.T) {
	cli := os.Getenv("REPLOY_HF_TEST_CLI")
	if os.Geteuid() != 0 || cli == "" {
		t.Skip("requires mapped root and REPLOY_HF_TEST_CLI product binary")
	}
	for _, outcome := range []string{"success", "failure", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			base, err := os.MkdirTemp("", "reploy-hf-control-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(base)
			if err := os.Chmod(base, 0o755); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(base, "staging")
			if err := os.Mkdir(source, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(source, 1000, 1000); err != nil {
				t.Fatal(err)
			}
			operation, _, current := installedBuildPublicationSourceFixtureAtDir(t, source)
			operator := StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1000, GID: 1000, SupplementaryGIDs: []uint32{}}
			planned, err := PlanCurrentRuntimeV1(CurrentRuntimePlanInputV1{DeploymentDir: source, Current: current, Runtime: operator})
			if err != nil {
				t.Fatal(err)
			}
			plans, err := RuntimePlansV1(planned.Document, planned.Docker)
			if err != nil {
				t.Fatal(err)
			}
			current.Lock.RuntimePolicy, err = CompileRuntimePolicyFromLockV1(planned.Document, current.Lock, plans)
			if err != nil {
				t.Fatal(err)
			}
			current.Generation.RuntimePolicyDigest, err = deploy.RuntimePolicyDigestV1(current.Lock.RuntimePolicy)
			if err != nil {
				t.Fatal(err)
			}
			current.Generation.BuildLockDigest, err = operation.PublishBuildLock(current.Lock, registry.ValidateRequirementProfileV1)
			if err != nil {
				t.Fatal(err)
			}
			old := *current.State.Current
			current.State.Current = &current.Generation
			if err := operation.CommitStateV1(&old, current.State); err != nil {
				t.Fatal(err)
			}
			if _, err := PublishCurrentRuntimeInputsV1(operation, source, planned); err != nil {
				t.Fatal(err)
			}
			oldExecutable := embeddedRuntimeExecutable
			embeddedRuntimeExecutable = func() (string, error) { return cli, nil }
			_, syncErr := syncStagedControlSurfaceV1(source, planned.Document)
			embeddedRuntimeExecutable = oldExecutable
			if syncErr != nil {
				t.Fatal(syncErr)
			}
			if err := operation.Unlock(); err != nil {
				t.Fatal(err)
			}
			backend := newProviderInstallRunBackendV1()
			var scratch string
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			sentinel := errors.New("source preparation failed")
			backend.buildSource = func(_ context.Context, input LockedProviderBuildRunInputV1) (LockedProviderBuildExecutionResultV1, error) {
				if input.Runtime.UID != 1000 || input.Runtime.GID != 1000 {
					t.Fatalf("sudo changed staged runtime account: %#v", input.Runtime)
				}
				var err error
				scratch, err = input.Store.NewWorkspace("failed-build-*")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(scratch, "diagnostic"), []byte("retained"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := input.Operation.CommitStateV1(current.State.Current, current.State); err != nil {
					t.Fatal(err)
				}
				if outcome == "cancelled" {
					cancel()
					return LockedProviderBuildExecutionResultV1{}, ctx.Err()
				}
				if outcome == "success" {
					return LockedProviderBuildExecutionResultV1{State: current.State, Lock: current.Lock, Reused: true}, nil
				}
				return LockedProviderBuildExecutionResultV1{}, sentinel
			}
			destination := filepath.Join(base, "installed")
			if outcome == "success" {
				backend = providerInstallAdmissionTestBackend(backend, nil)
				backend.prepareAccount = providerInstallRunPrepareAccountFixture
				backend.buildInstallRuntime = func(_ context.Context, _ providerstore.Store, source CurrentBuild, _ DockerExecutionPlan, _ RunOptions) (installedRuntimeIdentityBuildV1, error) {
					return installedRuntimeIdentityBuildV1{Lock: source.Lock}, nil
				}
				backend.planInstallation = func(_ context.Context, input providerInstallPlanningV1) (providerInstallationPlanV1, error) {
					return providerInstallRunPlanFixture(destination, input.References), nil
				}
				backend.inspectHostTools = func(context.Context, installBackend) (providerInstallHostToolsV1, error) {
					return providerInstallHostToolsV1{DockerPath: "/disposable/docker"}, nil
				}
				backend.preflightDestination = func(providerstore.Store, CurrentBuild, string) error { return nil }
				backend.prepareDestination = func(context.Context, lockedProviderInstallV1) (preparedProviderInstallFilesV1, error) {
					return preparedProviderInstallFilesV1{Files: []preparedProviderInstallFileV1{}}, nil
				}
				backend.publish = func(_ context.Context, _ *deploy.OperationLock, target *deploy.OperationLock, _ providerstore.Store, _ providerstore.Store, input InstalledBuildPublicationInputV1) (deploy.StateV1, error) {
					state := input.Source.State
					generation := input.Source.Generation
					generation.Reference = input.References.Generation
					var err error
					generation.BuildLockDigest, err = target.PublishBuildLock(input.Build, registry.ValidateRequirementProfileV1)
					if err != nil {
						return deploy.StateV1{}, err
					}
					state.Current = &generation
					state.Deployment = &deploy.DeploymentStateV1{Schema: deploy.DeploymentStateSchemaV1, Installation: input.Installation}
					return state, target.CommitStateV1(nil, state)
				}
				backend.activateDestination = func(context.Context, lockedProviderInstallV1, deploy.StateV1) error { return nil }
			}
			_, err = runProviderInstallV1(ctx, providerInstallRunInputV1{SourceDeploymentDir: source, DestinationDeploymentDir: destination, Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux}, Install: providerInstallOptionsV1{Scope: InstallScopeSystem}}, backend)
			wantErr := sentinel
			if outcome == "success" {
				wantErr = nil
			}
			if outcome == "cancelled" {
				wantErr = context.Canceled
			}
			if !errors.Is(err, wantErr) {
				t.Fatalf("install failure lost: %v", err)
			}
			if outcome == "success" {
				info, err := os.Stat(filepath.Join(destination, ".reploy", "state.json"))
				if err != nil {
					t.Fatal(err)
				}
				if info.Sys().(*syscall.Stat_t).Uid != 0 || info.Mode().Perm() != 0o600 {
					t.Fatal("installed metadata lost root ownership or restrictive mode")
				}
			}
			info, err := os.Stat(filepath.Join(scratch, "diagnostic"))
			if err != nil {
				t.Fatal(err)
			}
			if info.Sys().(*syscall.Stat_t).Uid != 1000 || info.Mode().Perm() != 0o600 {
				t.Fatal("retained scratch inaccessible to operator")
			}
			docker := filepath.Join(base, "docker")
			stub := "#!/bin/sh\ncase \"$1 $2\" in\n'image inspect') printf '%s\\n' '" + string(current.Lock.FinalImage.ConfigDigest) + "';;\n'version --format') echo '24.0.0';;\n'inspect --format') echo '{\"Running\":false,\"Status\":\"exited\"}';;\n*) echo \"unexpected Docker command: $*\" >&2; exit 99;;\nesac\n"
			if err := os.WriteFile(docker, []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(filepath.Join(source, planned.Document.Environment.ControlScript), "status")
			cmd.Env = append(os.Environ(), "PATH="+base+":/usr/bin:/bin", "DOCKER_HOST=unix:///disposable-test-no-daemon.sock", "DOCKER_CONTEXT=")
			cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 1000, Gid: 1000, Groups: []uint32{1000}}}
			output, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(output), "status: down") {
				t.Fatalf("actual operator controller status: %v\n%s", err, output)
			}
		})
	}
}
