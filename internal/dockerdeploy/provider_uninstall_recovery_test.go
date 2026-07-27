package dockerdeploy

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
)

func TestRecoverMissingProviderUninstallUsesManagedUnitIdentity(t *testing.T) {
	unitDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "deleted")
	unitPath := filepath.Join(unitDir, "demo.service")
	content := "# Managed-By: reploy\n# Reploy-Service: demo\n# Reploy-Target: " + target + "\n# Reploy-Compose-Project: demo-deadbeef\n"
	if err := os.WriteFile(unitPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var got providerUninstallRecoveryPlanV1
	err := recoverMissingProviderUninstallV1(t.Context(), ProviderUninstallRecoveryInputV1{
		RequestedDir: target, Service: "demo", Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux},
		RunOptions: RunOptions{DockerPreflightTimeout: 8 * time.Second},
	}, providerUninstallRecoveryBackendV1{
		unitDir: unitDir,
		apply: func(_ context.Context, plan providerUninstallRecoveryPlanV1, _ RunOptions) error {
			got = plan
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := providerUninstallRecoveryPlanV1{
		TargetDir: target, Service: "demo", UnitPath: unitPath, ComposeProject: "demo-deadbeef",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recovery plan = %#v, want %#v", got, want)
	}
}

func TestRecoverMissingProviderUninstallRejectsUnmanagedOrExistingTarget(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		target  func(*testing.T) string
		want    string
	}{
		{
			name: "unmanaged", content: "[Service]\nWorkingDirectory=/opt/demo\n",
			target: func(*testing.T) string { return "/opt/demo" }, want: "not managed by Reploy",
		},
		{
			name:    "existing target",
			content: "# Managed-By: reploy\n# Reploy-Service: demo\n# Reploy-Compose-Project: demo-deadbeef\n",
			target:  func(t *testing.T) string { return t.TempDir() }, want: "still exists",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			unitDir := t.TempDir()
			target := test.target(t)
			content := test.content
			if !strings.Contains(content, "# Reploy-Target:") {
				content += "# Reploy-Target: " + target + "\n"
			}
			if err := os.WriteFile(filepath.Join(unitDir, "demo.service"), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			err := recoverMissingProviderUninstallV1(t.Context(), ProviderUninstallRecoveryInputV1{
				Service: "demo", Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux},
			}, providerUninstallRecoveryBackendV1{unitDir: unitDir, apply: func(context.Context, providerUninstallRecoveryPlanV1, RunOptions) error {
				t.Fatal("invalid recovery reached host mutation")
				return nil
			}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("recovery error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestApplyProviderUninstallRecoveryRunsCleanupInRecoverableOrder(t *testing.T) {
	unitPath := filepath.Join(t.TempDir(), "demo.service")
	if err := os.WriteFile(unitPath, []byte("unit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	order := []string{}
	err := applyProviderUninstallRecoveryV1(t.Context(), providerUninstallRecoveryPlanV1{
		TargetDir: "/opt/deleted", Service: "demo", UnitPath: unitPath, ComposeProject: "demo-deadbeef",
	}, RunOptions{Stdout: io.Discard, DockerPreflightTimeout: 9 * time.Second}, providerUninstallRecoveryApplyBackendV1{
		lookPath: func(name string) (string, error) {
			if name != "systemctl" {
				t.Fatalf("lookup = %q", name)
			}
			return "/bin/systemctl", nil
		},
		runHost: func(name string, args ...string) error {
			order = append(order, strings.Join(append([]string{name}, args...), " "))
			return nil
		},
		removeDockerProject: func(_ context.Context, project string, timeout time.Duration) error {
			if project != "demo-deadbeef" || timeout != 9*time.Second {
				t.Fatalf("Docker cleanup = %q/%s", project, timeout)
			}
			order = append(order, "docker-clean "+project)
			return nil
		},
		remove: func(path string) error {
			order = append(order, "remove "+path)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/bin/systemctl stop demo.service", "docker-clean demo-deadbeef",
		"/bin/systemctl disable demo.service", "remove " + unitPath,
		"/bin/systemctl daemon-reload",
	}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("recovery order = %#v, want %#v", order, want)
	}
}

func TestRemoveProviderUninstallDockerProjectUsesExactComposeLabels(t *testing.T) {
	outputs := [][]byte{[]byte("container-1\ncontainer-2\n"), []byte("network-1\n")}
	got := []CommandSpec{}
	err := removeProviderUninstallDockerProjectWithV1(t.Context(), "demo-deadbeef", 7*time.Second, providerUninstallDockerProjectBackendV1{
		output: func(spec CommandSpec, options RunOptions) ([]byte, error) {
			if options.Context != t.Context() || options.DockerPreflightTimeout != 7*time.Second {
				t.Fatalf("output options = %#v", options)
			}
			got = append(got, spec)
			output := outputs[0]
			outputs = outputs[1:]
			return output, nil
		},
		run: func(spec CommandSpec, options RunOptions) error {
			if options.Context != t.Context() || options.DockerPreflightTimeout != 7*time.Second {
				t.Fatalf("run options = %#v", options)
			}
			got = append(got, spec)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []CommandSpec{
		{Name: "docker", Args: []string{"ps", "-a", "--filter", "label=com.docker.compose.project=demo-deadbeef", "--format", "{{.ID}}"}},
		{Name: "docker", Args: []string{"rm", "-f", "container-1", "container-2"}},
		{Name: "docker", Args: []string{"network", "ls", "--filter", "label=com.docker.compose.project=demo-deadbeef", "--format", "{{.ID}}"}},
		{Name: "docker", Args: []string{"network", "rm", "network-1"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Docker recovery commands = %#v, want %#v", got, want)
	}
}
