package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
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

func TestStartProviderInstallHostV1PreflightsDockerBackend(t *testing.T) {
	destinationDir := t.TempDir()
	dockerPath := writeFakeCommand(
		t,
		destinationDir,
		"docker",
		"#!/bin/sh\nexit 0\n",
		"@exit /b 0\r\n",
	)
	references := fixedPublicationReferences(t, destinationDir, 0xd4)
	plan := providerInstallRunPlanFixture(destinationDir, references)
	plan.Backend = installBackendDockerManaged
	plan.Installation.Scope = "user"
	plan.Installation.UnitPath = ""
	plan.Docker.DeploymentDir = destinationDir

	previousPreflight := dockerPreflight
	t.Cleanup(func() { dockerPreflight = previousPreflight })
	preflights := 0
	dockerPreflight = func(_ context.Context, spec CommandSpec, _ time.Duration) (string, error) {
		preflights++
		if spec.Name != dockerPath {
			t.Fatalf("preflight command = %#v", spec)
		}
		return "unix:///var/run/docker.sock", nil
	}

	if err := startProviderInstallHostV1(
		t.Context(),
		plan,
		providerInstallHostToolsV1{DockerPath: dockerPath},
		RunOptions{},
	); err != nil {
		t.Fatal(err)
	}
	if preflights != 1 {
		t.Fatalf("Docker preflights = %d, want 1", preflights)
	}
}

func TestStartProviderInstallHostV1BindsPrivateEnvironmentStartupOnce(t *testing.T) {
	destinationDir := t.TempDir()
	dockerPath := filepath.Join(destinationDir, "docker")
	environmentPath := filepath.Join(destinationDir, PrivateWorkloadEnvironmentFileName)
	if created, err := publishPrivateWorkloadEnvironmentFileV1(
		environmentPath,
		[]byte("PRIVATE_NAME=private-value\n"),
		false,
	); err != nil || !created {
		t.Fatal(err)
	}
	references := fixedPublicationReferences(t, destinationDir, 0xd5)
	plan := providerInstallRunPlanFixture(destinationDir, references)
	plan.Backend = installBackendDockerManaged
	plan.Installation.Scope = "user"
	plan.Installation.UnitPath = ""
	plan.Docker.DeploymentDir = destinationDir
	plan.Docker.PrivateEnvironment = true
	plan.Docker.Workload = &WorkloadExecutionPlan{Argv: []string{"/bin/true"}}
	rendered, err := RenderDockerInputs(plan.Docker, plan.ControlScript)
	if err != nil {
		t.Fatal(err)
	}
	plan.Rendered = rendered

	previousBind := bindProviderInstallHostCommandRunner
	t.Cleanup(func() { bindProviderInstallHostCommandRunner = previousBind })
	binds := 0
	var operations []string
	bindProviderInstallHostCommandRunner = func(bindCtx context.Context, spec CommandSpec, timeout time.Duration) (commandRunner, error) {
		binds++
		if bindCtx != t.Context() || spec.Name != dockerPath || timeout != 3*time.Second {
			t.Fatalf("bind context=%v spec=%#v timeout=%v", bindCtx, spec, timeout)
		}
		return func(spec CommandSpec, _ RunOptions) error {
			operations = append(operations, strings.Join(spec.Args, " "))
			if len(spec.Args) > 0 && spec.Args[0] == "exec" {
				return errors.New("relay failed")
			}
			return nil
		}, nil
	}

	err = startProviderInstallHostV1(
		t.Context(),
		plan,
		providerInstallHostToolsV1{DockerPath: dockerPath},
		RunOptions{DockerPreflightTimeout: 3 * time.Second},
	)
	if err == nil || !strings.Contains(err.Error(), "relay failed") {
		t.Fatalf("error = %v", err)
	}
	if binds != 1 {
		t.Fatalf("Docker runner binds = %d, want 1", binds)
	}
	if len(operations) != 3 || !strings.HasPrefix(operations[0], "compose ") || !strings.HasPrefix(operations[1], "exec -i ") || !strings.HasPrefix(operations[2], "compose ") {
		t.Fatalf("operations = %#v", operations)
	}
}

func TestStartProviderInstallHostV1RejectsChangedPrivateRuntimeMasks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ordinary Windows users cannot create the test symlink")
	}
	destinationDir := t.TempDir()
	environmentPath := filepath.Join(destinationDir, PrivateWorkloadEnvironmentFileName)
	if created, err := publishPrivateWorkloadEnvironmentFileV1(
		environmentPath,
		[]byte("PRIVATE_NAME=private-value\n"),
		false,
	); err != nil || !created {
		t.Fatal(err)
	}
	originalSource := t.TempDir()
	link := filepath.Join(t.TempDir(), "deployment-link")
	if err := os.Symlink(originalSource, link); err != nil {
		t.Fatal(err)
	}
	references := fixedPublicationReferences(t, destinationDir, 0xd3)
	plan := providerInstallRunPlanFixture(destinationDir, references)
	plan.Backend = installBackendDockerManaged
	plan.Installation.Scope = "user"
	plan.Installation.UnitPath = ""
	plan.Docker.DeploymentDir = destinationDir
	plan.Docker.PrivateEnvironment = true
	plan.Docker.Workload = &WorkloadExecutionPlan{Argv: []string{"/bin/true"}}
	plan.Docker.Mounts = []MountExecutionPlan{{
		Name: "deployment", Mode: blueprint.MountBind, Source: link,
		SourceKind: deploy.RuntimeMountSourceDirectory, Target: "/deployment",
	}}
	rendered, err := RenderDockerInputs(plan.Docker, plan.ControlScript)
	if err != nil {
		t.Fatal(err)
	}
	plan.Rendered = rendered
	if len(plan.Rendered.privateRuntimeMasks) != 0 {
		t.Fatalf("initial masks = %#v", plan.Rendered.privateRuntimeMasks)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(destinationDir, link); err != nil {
		t.Fatal(err)
	}
	err = startProviderInstallHostV1(
		t.Context(),
		plan,
		providerInstallHostToolsV1{DockerPath: "/usr/bin/docker"},
		RunOptions{},
	)
	if err == nil || !strings.Contains(err.Error(), "runtime bind sources changed after runtime inputs were rendered") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "PRIVATE_NAME") || strings.Contains(err.Error(), "private-value") {
		t.Fatalf("error exposes private environment material: %v", err)
	}
}

func TestConfigureProviderInstallHostV1StopsAtFirstFailure(t *testing.T) {
	want := errors.New("enable failed")
	commands := providerInstallHostCommandsV1{Configure: []CommandSpec{{Name: "one"}, {Name: "two"}, {Name: "three"}}}
	called := 0
	err := configureProviderInstallHostWithV1(t.Context(), commands, RunOptions{}, func(CommandSpec, RunOptions) error {
		called++
		if called == 2 {
			return want
		}
		return nil
	})
	if !errors.Is(err, want) || called != 2 {
		t.Fatalf("called=%d error=%v", called, err)
	}
}
