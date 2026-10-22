package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func TestConfigureProviderInstallHostV1RunsOnlyPlannedConfiguration(t *testing.T) {
	commands := providerInstallHostCommandsV1{
		Configure: []CommandSpec{
			{Name: "/usr/bin/systemctl", Args: []string{"daemon-reload"}},
			{Name: "/usr/bin/systemctl", Args: []string{"enable", "demo.service"}},
		},
		Start: CommandSpec{Name: "/usr/bin/systemctl", Args: []string{"restart", "demo.service"}},
	}
	var ran []CommandSpec
	err := configureProviderInstallHostWithV1(t.Context(), commands, RunOptions{}, func(spec CommandSpec, options RunOptions) error {
		if options.Context != t.Context() || options.DockerPreflightTimeout != 0 {
			t.Fatalf("run options = %#v", options)
		}
		ran = append(ran, spec)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ran, commands.Configure) {
		t.Fatalf("commands = %#v, want %#v", ran, commands.Configure)
	}
}

func TestStartProviderInstallHostV1RunsOneCommandWithoutPreflight(t *testing.T) {
	commands := providerInstallHostCommandsV1{
		Configure: []CommandSpec{},
		Start:     CommandSpec{Name: "/usr/bin/docker", Args: []string{"compose", "up", "-d"}},
	}
	called := 0
	err := startProviderInstallHostWithV1(t.Context(), commands, RunOptions{}, func(spec CommandSpec, options RunOptions) error {
		called++
		if options.Context != t.Context() || !reflect.DeepEqual(spec, commands.Start) {
			t.Fatalf("spec=%#v options=%#v", spec, options)
		}
		return nil
	})
	if err != nil || called != 1 {
		t.Fatalf("called=%d error=%v", called, err)
	}
}

func TestStartProviderInstallHostV1RequiresLifecycleAuthority(t *testing.T) {
	dir := t.TempDir()
	plan := providerInstallRunPlanFixture(dir, fixedPublicationReferences(t, dir, 0xd4))
	plan.Backend = installBackendDockerManaged
	plan.Installation.Scope, plan.Installation.UnitPath = "user", ""
	plan.Docker.DeploymentDir = dir
	err := startProviderInstallHostV1(t.Context(), plan, providerInstallHostToolsV1{DockerPath: filepath.Join(dir, "docker")}, RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "controller-owned lifecycle evidence") {
		t.Fatal(err)
	}
}

func TestStartProviderInstallHostV1RetainsFailedPrivateStartup(t *testing.T) {
	fixture := newWorkloadLifecycleFixtureV1(t)
	dir := filepath.Dir(filepath.Dir(fixture.operation.Path()))
	if err := os.WriteFile(filepath.Join(dir, PrivateWorkloadEnvironmentFileName), []byte("TOKEN=synthetic\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := providerInstallRunPlanFixture(dir, fixedPublicationReferences(t, dir, 0xd5))
	plan.Backend = installBackendDockerManaged
	plan.Installation.Scope, plan.Installation.UnitPath = "user", ""
	plan.Docker.DeploymentDir = dir
	plan.Docker.PrivateEnvironment = true
	plan.Docker.ContainerName = "demo"
	plan.Docker.Workload = &WorkloadExecutionPlan{Argv: []string{"/bin/true"}}
	rendered, err := RenderDockerInputs(plan.Docker, plan.ControlScript)
	if err != nil {
		t.Fatal(err)
	}
	plan.Rendered = rendered
	run := func(spec CommandSpec, options RunOptions) error {
		if commandEnvironmentValueV1(spec, "DOCKER_HOST") != "unix:///fixture.sock" && len(spec.Args) > 0 && spec.Args[0] != "exec" {
			t.Fatal("lost selected endpoint")
		}
		if len(spec.Args) > 0 && spec.Args[0] == "exec" {
			fixture.injections++
			return errors.New("relay failed")
		}
		if len(spec.Args) > 0 && spec.Args[0] == "compose" {
			spec.Args = []string{"compose", "up"}
		}
		return fixture.run(spec, options)
	}
	lifecycle := &workloadLifecycleV1{command: fixture.command, containerName: "demo", run: run, operation: func(_ context.Context, action func(*deploy.OperationLock) error) error {
		return action(fixture.operation)
	}}
	if _, _, err := lifecycle.initialize(t.Context(), fixture.environment.RuntimeBinding, true); err != nil {
		t.Fatal(err)
	}
	err = startProviderInstallHostV1(t.Context(), plan, providerInstallHostToolsV1{DockerPath: filepath.Join(dir, "docker")}, RunOptions{workloadLifecycle: lifecycle})
	if err == nil || !strings.Contains(err.Error(), "relay failed") || fixture.stops != 1 || fixture.removals != 0 || fixture.state.ID == "" || fixture.state.Running {
		t.Fatal("failed private install startup not retained", err)
	}
}
