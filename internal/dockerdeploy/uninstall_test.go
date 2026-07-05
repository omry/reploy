package dockerdeploy

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

func TestUninstallDryRunFromInstalledTarget(t *testing.T) {
	target := makeInstalledDeploymentForUninstall(t, "demo-test", "demo-test-abcd")

	var stdout strings.Builder
	if err := Uninstall(UninstallOptions{From: target, DryRun: true, Stdout: &stdout}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"would uninstall service: demo-test",
		"target: " + target,
		"unit: " + filepath.Join(target, "demo-test.service"),
		"compose project: demo-test-abcd",
		"container: demo-test-abcd",
		"network: demo-test-abcd",
		"would run: systemctl stop demo-test.service",
		"would run: docker compose --project-name demo-test-abcd",
		"down --remove-orphans",
		"would run: systemctl disable demo-test.service",
		"target directory: kept",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestUninstallDryRunOnDarwinPrintsDockerDesktopPlan(t *testing.T) {
	restorePlatform := stubHostPlatform(t, hostPlatform{GOOS: "darwin"})
	defer restorePlatform()
	target := makeInstalledDeploymentForUninstall(t, "demo-test", "demo-test-abcd")

	var stdout strings.Builder
	if err := Uninstall(UninstallOptions{From: target, DryRun: true, Stdout: &stdout}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"would uninstall service: demo-test",
		"target: " + target,
		"permanent install backend: Docker-managed Compose",
		"compose project: demo-test-abcd",
		"container: demo-test-abcd",
		"network: demo-test-abcd",
		"would run: docker compose --project-name demo-test-abcd",
		"down --remove-orphans",
		"target directory: kept",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "systemctl") || strings.Contains(stdout.String(), "unit:") {
		t.Fatalf("darwin uninstall dry-run should not mention systemd:\n%s", stdout.String())
	}
}

func TestUninstallDryRunOnWindowsPrintsDockerDesktopPlan(t *testing.T) {
	restorePlatform := stubHostPlatform(t, hostPlatform{GOOS: "windows"})
	defer restorePlatform()
	target := makeInstalledDeploymentForUninstall(t, "demo-test", "demo-test-abcd")

	var stdout strings.Builder
	if err := Uninstall(UninstallOptions{From: target, DryRun: true, Stdout: &stdout}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"would uninstall service: demo-test",
		"target: " + target,
		"permanent install backend: Docker-managed Compose",
		"compose project: demo-test-abcd",
		"would run: docker compose --project-name demo-test-abcd",
		"down --remove-orphans",
		"target directory: kept",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "systemctl") || strings.Contains(stdout.String(), "unit:") {
		t.Fatalf("windows uninstall dry-run should not mention systemd:\n%s", stdout.String())
	}
}

func TestListReploySystemdServices(t *testing.T) {
	unitDir := t.TempDir()
	oldSystemdUnitDir := uninstallSystemdUnitDir
	t.Cleanup(func() {
		uninstallSystemdUnitDir = oldSystemdUnitDir
	})
	uninstallSystemdUnitDir = unitDir

	newUnit := systemdUnit(installPlan{
		TargetDir:      "/opt/demo2",
		Service:        "demo2",
		ComposeProject: "demo2-abcd",
	}, "/usr/bin/docker", false)
	if err := os.WriteFile(filepath.Join(unitDir, "demo2.service"), []byte(newUnit), 0o644); err != nil {
		t.Fatal(err)
	}
	legacyUnit := strings.ReplaceAll(systemdUnit(installPlan{
		TargetDir:      "/opt/demo1",
		Service:        "demo1",
		ComposeProject: "demo1-abcd",
	}, "/usr/bin/docker", false), "# Managed-By: reploy\n# Reploy-Service: demo1\n# Reploy-Target: /opt/demo1\n# Reploy-Compose-Project: demo1-abcd\n", "")
	if err := os.WriteFile(filepath.Join(unitDir, "demo1.service"), []byte(legacyUnit), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unitDir, "other.service"), []byte("[Unit]\nDescription=Other\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	services, err := ListReploySystemdServices()
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 2 {
		t.Fatalf("services = %#v", services)
	}
	if services[0].ServiceName != "demo1" || services[0].TargetDir != "/opt/demo1" || services[0].ComposeProject != "demo1-abcd" {
		t.Fatalf("legacy service = %#v", services[0])
	}
	if services[1].ServiceName != "demo2" || services[1].TargetDir != "/opt/demo2" || services[1].ComposeProject != "demo2-abcd" {
		t.Fatalf("new service = %#v", services[1])
	}

	var stdout strings.Builder
	if err := PrintReploySystemdServices(&stdout); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SERVICE", "demo1", "/opt/demo1", "demo1-abcd", "demo2", "/opt/demo2", "demo2-abcd"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestPrintReploySystemdServicesRejectsDockerManagedPlatforms(t *testing.T) {
	restorePlatform := stubHostPlatform(t, hostPlatform{GOOS: "windows"})
	defer restorePlatform()
	var stdout strings.Builder
	err := PrintReploySystemdServices(&stdout)
	if err == nil || !strings.Contains(err.Error(), "Linux/systemd-only") || !strings.Contains(err.Error(), "--from") {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout should be empty: %q", stdout.String())
	}
}

func TestUninstallFromInstalledTargetRunsCleanupInOrder(t *testing.T) {
	target := makeInstalledDeploymentForUninstall(t, "demo-test", "demo-test-abcd")
	restore := stubUninstallHost(t)
	defer restore()

	commands := []string{}
	uninstallRunCommand = func(name string, args ...string) error {
		commands = append(commands, formatCommand(name, args...))
		return nil
	}

	var stdout strings.Builder
	if err := Uninstall(UninstallOptions{From: target, Stdout: &stdout}); err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{
		"/bin/systemctl stop demo-test.service",
		"docker compose --project-name demo-test-abcd",
		"/bin/systemctl disable demo-test.service",
		"/bin/systemctl daemon-reload",
	}
	lastIndex := -1
	for _, want := range wantOrder {
		index := indexOfCommandContaining(commands, want)
		if index == -1 {
			t.Fatalf("commands missing %q: %#v", want, commands)
		}
		if index <= lastIndex {
			t.Fatalf("command %q ran out of order: %#v", want, commands)
		}
		lastIndex = index
	}
	if _, err := os.Stat(filepath.Join(target, "demo-test.service")); !os.IsNotExist(err) {
		t.Fatalf("unit was not removed: %v", err)
	}
	if !strings.Contains(stdout.String(), "uninstalled service: demo-test") {
		t.Fatalf("stdout missing success:\n%s", stdout.String())
	}
}

func TestUninstallFromInstalledTargetOnDarwinUsesDockerDesktopCleanup(t *testing.T) {
	restorePlatform := stubHostPlatform(t, hostPlatform{GOOS: "darwin"})
	defer restorePlatform()
	target := makeInstalledDeploymentForUninstall(t, "demo-test", "demo-test-abcd")
	restoreHost := stubUninstallHost(t)
	defer restoreHost()

	commands := []string{}
	uninstallRunDockerCommand = func(spec CommandSpec, dockerPreflightTimeout time.Duration) error {
		commands = append(commands, formatCommand(spec.Name, spec.Args...))
		return nil
	}
	uninstallRunCommand = func(name string, args ...string) error {
		commands = append(commands, formatCommand(name, args...))
		return nil
	}

	var stdout strings.Builder
	if err := Uninstall(UninstallOptions{From: target, RemoveDir: true, Stdout: &stdout}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"docker compose --project-name demo-test-abcd",
		"down --remove-orphans",
	} {
		if !containsCommand(commands, want) {
			t.Fatalf("commands missing %q: %#v", want, commands)
		}
	}
	for _, forbidden := range []string{"systemctl stop", "systemctl disable", "systemctl daemon-reload"} {
		if containsCommand(commands, forbidden) {
			t.Fatalf("darwin uninstall should not run %q: %#v", forbidden, commands)
		}
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target was not removed: %v", err)
	}
	if !strings.Contains(stdout.String(), "uninstalled service: demo-test") {
		t.Fatalf("stdout missing success:\n%s", stdout.String())
	}
}

func TestUninstallFromInstalledTargetOnWindowsUsesDockerDesktopCleanup(t *testing.T) {
	restorePlatform := stubHostPlatform(t, hostPlatform{GOOS: "windows"})
	defer restorePlatform()
	target := makeInstalledDeploymentForUninstall(t, "demo-test", "demo-test-abcd")
	restoreHost := stubUninstallHost(t)
	defer restoreHost()

	commands := []string{}
	uninstallRunDockerCommand = func(spec CommandSpec, dockerPreflightTimeout time.Duration) error {
		commands = append(commands, formatCommand(spec.Name, spec.Args...))
		return nil
	}
	uninstallRunCommand = func(name string, args ...string) error {
		commands = append(commands, formatCommand(name, args...))
		return nil
	}

	var stdout strings.Builder
	if err := Uninstall(UninstallOptions{From: target, RemoveDir: true, Stdout: &stdout}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"docker compose --project-name demo-test-abcd",
		"down --remove-orphans",
	} {
		if !containsCommand(commands, want) {
			t.Fatalf("commands missing %q: %#v", want, commands)
		}
	}
	for _, forbidden := range []string{"systemctl stop", "systemctl disable", "systemctl daemon-reload"} {
		if containsCommand(commands, forbidden) {
			t.Fatalf("windows uninstall should not run %q: %#v", forbidden, commands)
		}
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target was not removed: %v", err)
	}
	if !strings.Contains(stdout.String(), "uninstalled service: demo-test") {
		t.Fatalf("stdout missing success:\n%s", stdout.String())
	}
}

func TestUninstallPassesDockerPreflightTimeout(t *testing.T) {
	target := makeInstalledDeploymentForUninstall(t, "demo-test", "demo-test-abcd")
	restore := stubUninstallHost(t)
	defer restore()

	timeout := 1200 * time.Millisecond
	var gotTimeout time.Duration
	uninstallRunDockerCommand = func(spec CommandSpec, dockerPreflightTimeout time.Duration) error {
		gotTimeout = dockerPreflightTimeout
		return nil
	}

	if err := Uninstall(UninstallOptions{From: target, DockerPreflightTimeout: timeout}); err != nil {
		t.Fatal(err)
	}
	if gotTimeout != timeout {
		t.Fatalf("docker preflight timeout = %s, want %s", gotTimeout, timeout)
	}
}

func TestUninstallMissingTargetRecoversDockerCleanupFromUnit(t *testing.T) {
	unitDir := t.TempDir()
	service := "demo-missing"
	target := filepath.Join(t.TempDir(), "missing")
	project := "demo-missing-abcd"
	unitPath := filepath.Join(unitDir, service+".service")
	unit := systemdUnit(installPlan{
		TargetDir:      target,
		Service:        service,
		ComposeProject: project,
	}, "/usr/bin/docker", false)
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := stubUninstallHost(t)
	defer restore()
	uninstallSystemdUnitDir = unitDir

	commands := []string{}
	uninstallRunCommand = func(name string, args ...string) error {
		command := formatCommand(name, args...)
		commands = append(commands, command)
		if command == "/bin/systemctl stop "+service+".service" {
			return errors.New("exit status 1")
		}
		return nil
	}
	uninstallRunCommandOutput = func(name string, args ...string) ([]byte, error) {
		command := formatCommand(name, args...)
		commands = append(commands, command)
		switch {
		case strings.Contains(command, "ps -a"):
			return []byte("container1\ncontainer2\n"), nil
		case strings.Contains(command, "network ls"):
			return []byte("network1\n"), nil
		default:
			return nil, nil
		}
	}

	var stdout strings.Builder
	if err := Uninstall(UninstallOptions{From: target, ServiceName: service, Stdout: &stdout}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"warning: systemctl stop demo-missing.service failed",
		"uninstalled service: demo-missing",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
	for _, want := range []string{
		"docker ps -a --filter label=com.docker.compose.project=demo-missing-abcd --format {{.ID}}",
		"docker rm -f container1 container2",
		"docker network ls --filter label=com.docker.compose.project=demo-missing-abcd --format {{.ID}}",
		"docker network rm network1",
		"/bin/systemctl disable demo-missing.service",
		"/bin/systemctl daemon-reload",
	} {
		if !containsCommand(commands, want) {
			t.Fatalf("commands missing %q: %#v", want, commands)
		}
	}
}

func TestUninstallServiceOnlyDoesNotRemoveUnitDerivedTargetDir(t *testing.T) {
	unitDir := t.TempDir()
	service := "demo-service-only"
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	unitPath := filepath.Join(unitDir, service+".service")
	unit := systemdUnit(installPlan{
		TargetDir:      target,
		Service:        service,
		ComposeProject: "demo-service-only-abcd",
	}, "/usr/bin/docker", false)
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := stubUninstallHost(t)
	defer restore()
	uninstallSystemdUnitDir = unitDir

	var stdout strings.Builder
	if err := Uninstall(UninstallOptions{ServiceName: service, RemoveDir: true, DryRun: true, Stdout: &stdout}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "would remove target directory") {
		t.Fatalf("dry-run should not remove unit-derived target:\n%s", stdout.String())
	}

	uninstallRunCommand = func(name string, args ...string) error { return nil }
	if err := Uninstall(UninstallOptions{ServiceName: service, RemoveDir: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target should still exist: %v", err)
	}
}

func TestUninstallServiceOnlyRequiresExistingUnit(t *testing.T) {
	unitDir := t.TempDir()
	oldSystemdUnitDir := uninstallSystemdUnitDir
	t.Cleanup(func() {
		uninstallSystemdUnitDir = oldSystemdUnitDir
	})
	uninstallSystemdUnitDir = unitDir

	err := Uninstall(UninstallOptions{ServiceName: "missing", DryRun: true})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{
		"service unit not found",
		"missing.service",
		"reploy services list",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
}

func TestUninstallServiceOnlyOnWindowsRequiresFrom(t *testing.T) {
	restorePlatform := stubHostPlatform(t, hostPlatform{GOOS: "windows"})
	defer restorePlatform()
	err := Uninstall(UninstallOptions{ServiceName: "demo", DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "--from is required for Docker-managed uninstall") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUninstallRejectsServiceNameMismatch(t *testing.T) {
	target := makeInstalledDeploymentForUninstall(t, "demo-test", "demo-test-abcd")
	err := Uninstall(UninstallOptions{From: target, ServiceName: "other", DryRun: true})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `--service-name "other" does not match installed service "demo-test"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func makeInstalledDeploymentForUninstall(t *testing.T, service string, project string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(StateFileName)), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, relativePath := range []string{DockerEnvFileName, ComposeFileName} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, relativePath)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, relativePath), []byte("generated\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	unitPath := filepath.Join(dir, service+".service")
	if err := os.WriteFile(unitPath, []byte("unit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := deploy.DeploymentState{
		SchemaVersion: 1,
		Phase:         deploy.PhaseInstalled,
		Install: &deploy.InstallState{
			TargetDir:      dir,
			Service:        service,
			UnitPath:       unitPath,
			ComposeProject: project,
			ContainerName:  project,
			NetworkName:    project,
		},
	}
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateFileName), append(content, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func stubUninstallHost(t *testing.T) func() {
	t.Helper()
	oldGeteuid := uninstallGeteuid
	oldLookPath := uninstallLookPath
	oldRunCommand := uninstallRunCommand
	oldRunCommandOutput := uninstallRunCommandOutput
	oldRunDockerCommand := uninstallRunDockerCommand
	oldRunDockerCommandOutput := uninstallRunDockerCommandOutput
	oldRemove := uninstallRemove
	oldRemoveAll := uninstallRemoveAll
	oldSystemdUnitDir := uninstallSystemdUnitDir
	uninstallGeteuid = func() int { return 0 }
	uninstallLookPath = func(name string) (string, error) {
		if name == "systemctl" {
			return "/bin/systemctl", nil
		}
		return name, nil
	}
	uninstallRunCommandOutput = func(name string, args ...string) ([]byte, error) {
		return nil, nil
	}
	uninstallRunDockerCommand = func(spec CommandSpec, dockerPreflightTimeout time.Duration) error {
		return uninstallRunCommand(spec.Name, spec.Args...)
	}
	uninstallRunDockerCommandOutput = func(spec CommandSpec, dockerPreflightTimeout time.Duration) ([]byte, error) {
		return uninstallRunCommandOutput(spec.Name, spec.Args...)
	}
	return func() {
		uninstallGeteuid = oldGeteuid
		uninstallLookPath = oldLookPath
		uninstallRunCommand = oldRunCommand
		uninstallRunCommandOutput = oldRunCommandOutput
		uninstallRunDockerCommand = oldRunDockerCommand
		uninstallRunDockerCommandOutput = oldRunDockerCommandOutput
		uninstallRemove = oldRemove
		uninstallRemoveAll = oldRemoveAll
		uninstallSystemdUnitDir = oldSystemdUnitDir
	}
}

func containsCommand(commands []string, want string) bool {
	return indexOfCommandContaining(commands, want) != -1
}

func indexOfCommandContaining(commands []string, want string) int {
	for index, command := range commands {
		if strings.Contains(command, want) {
			return index
		}
	}
	return -1
}
