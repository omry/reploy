package dockerdeploy

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestExecuteProviderUninstallHostMapsLockedInstallationWithoutRemovingDirectory(t *testing.T) {
	dir := t.TempDir()
	installation := installedBuildPublicationInstallation(dir)
	var captured providerUninstallHostPlanV1
	stdout := io.Discard
	err := executeProviderUninstallHostWithV1(t.Context(), providerUninstallPlanV1{
		Installation: installation, Backend: installBackendLinuxSystemd, RemoveDir: true,
	}, RunOptions{Stdout: stdout, DockerPreflightTimeout: 9 * time.Second}, providerUninstallHostBackendV1{
		apply: func(plan providerUninstallHostPlanV1, output io.Writer) error {
			captured = plan
			if output != stdout {
				t.Fatalf("stdout was not propagated")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("execute host cleanup: %v", err)
	}
	if captured.TargetDir != dir || captured.ServiceName != installation.Service || captured.UnitPath != installation.UnitPath {
		t.Fatalf("host cleanup identity = %#v", captured)
	}
	if captured.ComposeProject != installation.ComposeProject {
		t.Fatalf("host cleanup Docker identity = %#v", captured)
	}
	if captured.Backend != installBackendLinuxSystemd || captured.DockerPreflightTimeout != 9*time.Second {
		t.Fatalf("host cleanup execution options = %#v", captured)
	}
}

func TestApplyProviderUninstallHostPlanRunsLinuxCleanupInOrder(t *testing.T) {
	oldLookPath := uninstallLookPath
	oldRunCommand := uninstallRunCommand
	oldRunDocker := uninstallRunDockerCommand
	oldRemove := uninstallRemove
	t.Cleanup(func() {
		uninstallLookPath = oldLookPath
		uninstallRunCommand = oldRunCommand
		uninstallRunDockerCommand = oldRunDocker
		uninstallRemove = oldRemove
	})
	order := []string{}
	unitPath := filepath.Join(t.TempDir(), "demo.service")
	if err := os.WriteFile(unitPath, []byte("unit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	uninstallLookPath = func(name string) (string, error) {
		if name != "systemctl" {
			t.Fatalf("look path = %q", name)
		}
		return "/bin/systemctl", nil
	}
	uninstallRunCommand = func(name string, args ...string) error {
		order = append(order, strings.Join(append([]string{name}, args...), " "))
		return nil
	}
	uninstallRunDockerCommand = func(spec CommandSpec, timeout time.Duration) error {
		if timeout != 8*time.Second {
			t.Fatalf("Docker timeout = %s", timeout)
		}
		order = append(order, strings.Join(append([]string{spec.Name}, spec.Args...), " "))
		return nil
	}
	uninstallRemove = func(path string) error {
		order = append(order, "remove "+path)
		return nil
	}

	err := applyProviderUninstallHostPlanV1(providerUninstallHostPlanV1{
		TargetDir: "/opt/demo", ServiceName: "demo", UnitPath: unitPath,
		ComposeProject: "demo-project", Backend: installBackendLinuxSystemd,
		DockerPreflightTimeout: 8 * time.Second,
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/bin/systemctl stop demo.service",
		"docker compose --project-name demo-project --project-directory /opt/demo --env-file /opt/demo/.reploy/docker.env -f /opt/demo/.reploy/runtime/compose.yaml down --remove-orphans",
		"/bin/systemctl disable demo.service",
		"remove " + unitPath,
		"/bin/systemctl daemon-reload",
	}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("cleanup order = %#v, want %#v", order, want)
	}
}

func TestApplyProviderUninstallHostPlanFailsWhenDockerManagedCleanupFails(t *testing.T) {
	oldRunDocker := uninstallRunDockerCommand
	t.Cleanup(func() { uninstallRunDockerCommand = oldRunDocker })
	uninstallRunDockerCommand = func(CommandSpec, time.Duration) error { return errors.New("docker unavailable") }
	err := applyProviderUninstallHostPlanV1(providerUninstallHostPlanV1{
		TargetDir: "/opt/demo", ComposeProject: "demo", Backend: installBackendDockerManaged,
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "Docker Compose cleanup") {
		t.Fatalf("cleanup error = %v", err)
	}
}

func TestApplyProviderUninstallHostPlanRetriesAfterUnitWasAlreadyRemoved(t *testing.T) {
	oldLookPath := uninstallLookPath
	oldRunCommand := uninstallRunCommand
	oldRunDocker := uninstallRunDockerCommand
	t.Cleanup(func() {
		uninstallLookPath = oldLookPath
		uninstallRunCommand = oldRunCommand
		uninstallRunDockerCommand = oldRunDocker
	})
	uninstallLookPath = func(string) (string, error) { return "/bin/systemctl", nil }
	order := []string{}
	uninstallRunDockerCommand = func(CommandSpec, time.Duration) error {
		order = append(order, "compose")
		return nil
	}
	uninstallRunCommand = func(_ string, args ...string) error {
		order = append(order, strings.Join(args, " "))
		return nil
	}
	err := applyProviderUninstallHostPlanV1(providerUninstallHostPlanV1{
		TargetDir: "/opt/demo", ServiceName: "demo", UnitPath: filepath.Join(t.TempDir(), "missing.service"),
		ComposeProject: "demo", Backend: installBackendLinuxSystemd,
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"compose", "daemon-reload"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("retry order = %#v, want %#v", order, want)
	}
}

func TestExecuteProviderUninstallHostHonorsCancellationBeforeCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	called := false
	err := executeProviderUninstallHostWithV1(ctx, providerUninstallPlanV1{}, RunOptions{}, providerUninstallHostBackendV1{
		apply: func(providerUninstallHostPlanV1, io.Writer) error {
			called = true
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
	if called {
		t.Fatal("cancelled cleanup reached host mutation")
	}
}
