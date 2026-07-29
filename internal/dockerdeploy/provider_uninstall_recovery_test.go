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
		rename: func(oldPath string, newPath string) error {
			order = append(order, "rename "+oldPath+" -> "+newPath)
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
		"/bin/systemctl disable demo.service",
		"rename " + unitPath + " -> " + unitPath + ".reploy-uninstall-pending",
		"/bin/systemctl daemon-reload",
		"remove " + unitPath + ".reploy-uninstall-pending",
	}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("recovery order = %#v, want %#v", order, want)
	}
}

func TestRecoverMissingProviderUninstallRestoresUnitAfterReloadFailureAndRetries(t *testing.T) {
	unitDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "deleted")
	unitPath := filepath.Join(unitDir, "demo.service")
	content := []byte(
		"# Managed-By: reploy\n" +
			"# Reploy-Service: demo\n" +
			"# Reploy-Target: " + target + "\n" +
			"# Reploy-Compose-Project: demo-deadbeef\n",
	)
	if err := os.WriteFile(unitPath, content, 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(unitPath)
	if err != nil {
		t.Fatal(err)
	}
	reloads := 0
	dockerCleanups := 0
	apply := func(ctx context.Context, plan providerUninstallRecoveryPlanV1, options RunOptions) error {
		return applyProviderUninstallRecoveryV1(ctx, plan, options, providerUninstallRecoveryApplyBackendV1{
			lookPath: func(string) (string, error) { return "/bin/systemctl", nil },
			runHost: func(_ string, args ...string) error {
				if len(args) == 1 && args[0] == "daemon-reload" {
					reloads++
					if reloads == 1 {
						return errors.New("systemd busy")
					}
				}
				return nil
			},
			removeDockerProject: func(context.Context, string, time.Duration) error {
				dockerCleanups++
				return nil
			},
			rename: os.Rename,
			remove: os.Remove,
		})
	}
	input := ProviderUninstallRecoveryInputV1{
		RequestedDir: target,
		Service:      "demo",
		Runtime:      StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux},
	}
	backend := providerUninstallRecoveryBackendV1{unitDir: unitDir, apply: apply}
	err = recoverMissingProviderUninstallV1(t.Context(), input, backend)
	if err == nil || !strings.Contains(err.Error(), "restored verified managed unit at "+unitPath) {
		t.Fatalf("first recovery error = %v", err)
	}
	restored, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("read restored unit: %v", err)
	}
	after, err := os.Stat(unitPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored, content) || after.Mode() != before.Mode() {
		t.Fatalf(
			"restored unit content/mode = %q/%v, want %q/%v",
			restored, after.Mode(), content, before.Mode(),
		)
	}
	if _, err := os.Lstat(unitPath + ".reploy-uninstall-pending"); !os.IsNotExist(err) {
		t.Fatalf("unit tombstone remains after restoration: %v", err)
	}
	if err := recoverMissingProviderUninstallV1(t.Context(), input, backend); err != nil {
		t.Fatalf("retry recovery: %v", err)
	}
	if _, err := os.Lstat(unitPath); !os.IsNotExist(err) {
		t.Fatalf("managed unit remains after retry: %v", err)
	}
	if reloads != 2 || dockerCleanups != 2 {
		t.Fatalf("recovery attempts: reloads=%d Docker cleanups=%d", reloads, dockerCleanups)
	}
}

func TestApplyProviderUninstallRecoveryRejectsPendingUnitBeforeMutation(t *testing.T) {
	unitPath := filepath.Join(t.TempDir(), "demo.service")
	if err := os.WriteFile(unitPath, []byte("unit"), 0o600); err != nil {
		t.Fatal(err)
	}
	tombstone := unitPath + ".reploy-uninstall-pending"
	if err := os.WriteFile(tombstone, []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}
	mutated := false
	err := applyProviderUninstallRecoveryV1(
		t.Context(),
		providerUninstallRecoveryPlanV1{
			Service: "demo", UnitPath: unitPath, ComposeProject: "demo-deadbeef",
		},
		RunOptions{},
		providerUninstallRecoveryApplyBackendV1{
			lookPath: func(string) (string, error) { return "/bin/systemctl", nil },
			runHost: func(string, ...string) error {
				mutated = true
				return nil
			},
			removeDockerProject: func(context.Context, string, time.Duration) error {
				mutated = true
				return nil
			},
			rename: func(string, string) error {
				mutated = true
				return nil
			},
			remove: func(string) error {
				mutated = true
				return nil
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "pending recovered systemd unit removal already exists") {
		t.Fatalf("pending-unit error = %v", err)
	}
	if mutated {
		t.Fatal("pending-unit collision mutated host state")
	}
}

func TestRecoverMissingProviderUninstallUsesVerifiedPendingUnitForCleanupRetry(t *testing.T) {
	unitDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "deleted")
	unitPath := filepath.Join(unitDir, "demo.service")
	content := "# Managed-By: reploy\n# Reploy-Service: demo\n# Reploy-Target: " + target +
		"\n# Reploy-Compose-Project: demo-deadbeef\n"
	if err := os.WriteFile(unitPath, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	removeAttempts := 0
	dockerCleanups := 0
	pendingPlans := 0
	apply := func(ctx context.Context, plan providerUninstallRecoveryPlanV1, options RunOptions) error {
		if plan.PendingUnitRemoval {
			pendingPlans++
		}
		return applyProviderUninstallRecoveryV1(ctx, plan, options, providerUninstallRecoveryApplyBackendV1{
			lookPath: func(string) (string, error) { return "/bin/systemctl", nil },
			runHost:  func(string, ...string) error { return nil },
			removeDockerProject: func(context.Context, string, time.Duration) error {
				dockerCleanups++
				return nil
			},
			rename: os.Rename,
			remove: func(path string) error {
				removeAttempts++
				if removeAttempts == 1 {
					return errors.New("filesystem busy")
				}
				return os.Remove(path)
			},
		})
	}
	input := ProviderUninstallRecoveryInputV1{
		RequestedDir: target,
		Service:      "demo",
		Runtime:      StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux},
	}
	backend := providerUninstallRecoveryBackendV1{unitDir: unitDir, apply: apply}
	err := recoverMissingProviderUninstallV1(t.Context(), input, backend)
	if err == nil || !strings.Contains(err.Error(), "verified managed unit retained at "+unitPath+".reploy-uninstall-pending") {
		t.Fatalf("first recovery error = %v", err)
	}
	if _, err := os.Lstat(unitPath); !os.IsNotExist(err) {
		t.Fatalf("public unit restored after successful reload: %v", err)
	}
	if err := recoverMissingProviderUninstallV1(t.Context(), input, backend); err != nil {
		t.Fatalf("pending-unit retry: %v", err)
	}
	if pendingPlans != 1 || dockerCleanups != 1 || removeAttempts != 2 {
		t.Fatalf(
			"pending retry counts: plans=%d Docker=%d removes=%d",
			pendingPlans, dockerCleanups, removeAttempts,
		)
	}
	if _, err := os.Lstat(unitPath + ".reploy-uninstall-pending"); !os.IsNotExist(err) {
		t.Fatalf("pending unit remains after retry: %v", err)
	}
}

func TestRemoveDockerComposeProjectByLabelUsesExactComposeLabels(t *testing.T) {
	outputs := [][]byte{[]byte("container-1\ncontainer-2\n"), []byte("network-1\n")}
	got := []CommandSpec{}
	err := removeDockerComposeProjectByLabelWithV1(t.Context(), "demo-deadbeef", 7*time.Second, dockerComposeProjectRemovalBackendV1{
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
