package dockerdeploy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func TestInstallDryRunPrintsPlan(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "installed")

	var stdout strings.Builder
	if err := Install(InstallOptions{
		Dir:           deployDir,
		Target:        target,
		Service:       "demo-test",
		PortOverrides: []PortOverride{{HostPort: "18082"}},
		Start:         true,
		DryRun:        true,
		Stdout:        &stdout,
	}); err != nil {
		t.Fatal(err)
	}
	instanceID, err := installedInstanceID("demo-test", target)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"would install deployment:",
		"target: " + target,
		"service: demo-test",
		"instance id: " + instanceID,
		"compose project: " + instanceID,
		"container: " + instanceID,
		"network: " + instanceID,
		"port default: 127.0.0.1:18082 -> 8080",
		"would write systemd unit: /etc/systemd/system/demo-test.service",
		"would run: systemctl daemon-reload",
		"would run: systemctl enable demo-test.service",
		"would run: systemctl restart demo-test.service",
		"would run: " + filepath.Join(target, "reploy") + " test",
		"would run: " + filepath.Join(target, "reploy") + " app config check --live",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestInstallRequiresAbsoluteTarget(t *testing.T) {
	err := Install(InstallOptions{Dir: "deployment", Target: "relative", DryRun: true})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--to must be an absolute path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInstallRejectsInvalidServiceName(t *testing.T) {
	err := Install(InstallOptions{Dir: "deployment", Target: filepath.Join(t.TempDir(), "installed"), Service: "bad/name", DryRun: true})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--service contains unsupported characters") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInstallRejectsOverlappingTarget(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	deployDir := filepath.Join(parent, "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	targetLink := filepath.Join(parent, "deployment-link")
	if err := os.Symlink(deployDir, targetLink); err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{
		deployDir,
		filepath.Join(deployDir, "installed"),
		parent,
		targetLink,
	} {
		t.Run(target, func(t *testing.T) {
			err := Install(InstallOptions{Dir: deployDir, Target: target, Service: "demo-test", DryRun: true})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "--to must not overlap deployment directory") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestCopyDeploymentTreeProtectedCopiesRegularFiles(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(filepath.Join(source, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "conf", "config.yaml"), []byte("config\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := copyDeploymentTreeProtected(source, target); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(target, "conf", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "config\n" {
		t.Fatalf("copied content = %q", content)
	}
	info, err := os.Stat(filepath.Join(target, "conf", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}

func TestCopyDeploymentTreeProtectedRejectsSymlinks(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(source, "passwd-link")); err != nil {
		t.Fatal(err)
	}

	err := copyDeploymentTreeProtected(source, target)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "refusing to copy symlink") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCopyDeploymentTreeProtectedSkipsRuntimeDirectory(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(filepath.Join(source, RuntimeDirName, "python-venv", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, BundleDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr/bin/python3", filepath.Join(source, RuntimeDirName, "python-venv", "bin", "python")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, BundleDirName, "demo-1.0.0-py3-none-any.whl"), []byte("wheel\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyDeploymentTreeProtected(source, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, RuntimeDirName)); !os.IsNotExist(err) {
		t.Fatalf("runtime dir copied: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(target, BundleDirName, "demo-1.0.0-py3-none-any.whl")); err != nil {
		t.Fatalf("bundle file was not copied: %v", err)
	}
}

func TestSystemdUnitIncludesComposeOverrideWhenPresent(t *testing.T) {
	unit := systemdUnit(installPlan{
		TargetDir:       "/srv/demo",
		Service:         "demo-prod",
		ComposeOverride: true,
	}, "/usr/bin/docker", true)
	for _, want := range []string{
		"Requires=docker.service",
		"After=docker.service",
		"WorkingDirectory=/srv/demo",
		"ExecStart=/usr/bin/docker compose --env-file /srv/demo/.reploy/docker.env --project-directory /srv/demo -f /srv/demo/.reploy/compose.yaml -f /srv/demo/.reploy/compose.override.yaml up",
		"ExecStop=/usr/bin/docker compose --env-file /srv/demo/.reploy/docker.env --project-directory /srv/demo -f /srv/demo/.reploy/compose.yaml -f /srv/demo/.reploy/compose.override.yaml down",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit missing %q:\n%s", want, unit)
		}
	}
}

func TestWriteInstalledStateMarksDeploymentInstalled(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	plan := installPlan{
		TargetDir:      deployDir,
		Service:        "demo",
		UnitPath:       "/etc/systemd/system/demo.service",
		InstanceID:     "demo-12345678",
		ComposeProject: "demo-12345678",
		ContainerName:  "demo-12345678",
		NetworkName:    "demo-12345678",
		Ports: []dockerPortBinding{{
			Name:          "default",
			HostBind:      "127.0.0.1",
			HostPort:      "18080",
			ContainerPort: "8080",
		}},
	}
	if err := writeInstalledState(plan); err != nil {
		t.Fatal(err)
	}
	state, err := loadState(deployDir)
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != deploy.PhaseInstalled {
		t.Fatalf("phase = %s, want %s", state.Phase, deploy.PhaseInstalled)
	}
	if len(state.Bundle.Roots) == 0 {
		t.Fatal("bundle roots were not preserved")
	}
	if state.Install == nil || state.Install.InstanceID != "demo-12345678" {
		t.Fatalf("install state = %#v", state.Install)
	}
}

func TestInstallApplyCopiesDeploymentWritesUnitAndRunsSystemctl(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(deployDir, RuntimeDirName, "python-venv", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr/bin/python3", filepath.Join(deployDir, RuntimeDirName, "python-venv", "bin", "python")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "installed")
	if err := os.MkdirAll(filepath.Join(target, RuntimeDirName, "python-venv"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, RuntimeDirName, "python-venv", "stale"), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unitDir := t.TempDir()

	oldGeteuid := installGeteuid
	oldLookPath := installLookPath
	oldRunCommand := installRunCommand
	oldRunCommandOutput := installRunCommandOutput
	oldSystemdUnitDir := installSystemdUnitDir
	t.Cleanup(func() {
		installGeteuid = oldGeteuid
		installLookPath = oldLookPath
		installRunCommand = oldRunCommand
		installRunCommandOutput = oldRunCommandOutput
		installSystemdUnitDir = oldSystemdUnitDir
	})

	installGeteuid = func() int { return 0 }
	installSystemdUnitDir = unitDir
	installLookPath = func(name string) (string, error) {
		switch name {
		case "docker":
			return "/usr/bin/docker", nil
		case "systemctl":
			return "/bin/systemctl", nil
		default:
			return "", errors.New("not found")
		}
	}
	commands := []string{}
	installRunCommand = func(name string, args ...string) error {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return nil
	}
	installRunCommandOutput = func(name string, args ...string) ([]byte, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return nil, nil
	}

	if err := Install(InstallOptions{
		Dir:           deployDir,
		Target:        target,
		Service:       "demo-apply",
		PortOverrides: []PortOverride{{HostPort: "18082"}},
		Start:         true,
	}); err != nil {
		t.Fatal(err)
	}
	instanceID, err := installedInstanceID("demo-apply", target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, ComposeFileName)); err != nil {
		t.Fatalf("missing copied compose: %v", err)
	}
	if info, err := os.Stat(filepath.Join(target, RuntimeDirName)); err != nil || !info.IsDir() {
		t.Fatalf("missing fresh runtime dir: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(filepath.Join(target, RuntimeDirName, "python-venv")); !os.IsNotExist(err) {
		t.Fatalf("source runtime venv copied: err=%v", err)
	}
	dockerEnv := readFile(t, filepath.Join(target, DockerEnvFileName))
	for _, want := range []string{
		"REPLOY_CONTAINER_NAME=" + instanceID,
		"REPLOY_DOCKER_NETWORK_NAME=" + instanceID,
		"REPLOY_HOST_PORT=18082",
	} {
		if !strings.Contains(dockerEnv, want) {
			t.Fatalf("installed docker.env missing %q:\n%s", want, dockerEnv)
		}
	}
	state, err := loadState(target)
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != deploy.PhaseInstalled {
		t.Fatalf("phase = %s, want %s", state.Phase, deploy.PhaseInstalled)
	}
	if state.Install == nil {
		t.Fatal("missing install state")
	}
	if state.Install.InstanceID != instanceID || state.Install.ComposeProject != instanceID || state.Install.ContainerName != instanceID || state.Install.NetworkName != instanceID {
		t.Fatalf("install state = %#v, want instance %s", state.Install, instanceID)
	}
	if state.Install.Ports["default"].HostPort != "18082" {
		t.Fatalf("install ports = %#v", state.Install.Ports)
	}
	unit, err := os.ReadFile(filepath.Join(unitDir, "demo-apply.service"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), "ExecStart=/usr/bin/docker compose --env-file "+filepath.Join(target, DockerEnvFileName)) {
		t.Fatalf("unit does not point at target docker.env:\n%s", unit)
	}
	if !strings.Contains(string(unit), "--project-name "+instanceID) {
		t.Fatalf("unit does not use install compose project:\n%s", unit)
	}
	for _, want := range []string{
		"/bin/systemctl cat docker.service",
		"/bin/systemctl daemon-reload",
		"/bin/systemctl enable demo-apply.service",
		"/bin/systemctl restart demo-apply.service",
		filepath.Join(target, "reploy") + " test",
		filepath.Join(target, "reploy") + " app config check --live",
	} {
		if !containsString(commands, want) {
			t.Fatalf("commands missing %q: %#v", want, commands)
		}
	}
}

func TestInstalledPostStartCheckIncludesCommandOutput(t *testing.T) {
	oldRunCommandOutput := installRunCommandOutput
	t.Cleanup(func() {
		installRunCommandOutput = oldRunCommandOutput
	})

	installRunCommandOutput = func(name string, args ...string) ([]byte, error) {
		return []byte("docker compose ps failed\npermission denied\n"), errors.New("exit status 1")
	}

	err := runInstalledPostStartChecks(installPlan{TargetDir: "/srv/demo"})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{
		"installed server test: exit status 1",
		"docker compose ps failed",
		"permission denied",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q:\n%v", want, err)
		}
	}
}

func TestInstallAppliesNamedPortOverrides(t *testing.T) {
	packDir := makeTestPackWithManifest(t, `blueprint:
  schema: 1
  version: 0.1.0
  requires_reploy: ">=0.1.0"

app:
  id: demo
  provider:
    type: python
    identifier: demo-server

docker:
  deployment_dirs:
    config: conf
    bundle: .reploy/bundle
    data: data
  ports:
    http:
      host_bind: 127.0.0.1
      host_port: "18080"
      container_port: "8080"
    metrics:
      host_bind: 127.0.0.1
      host_port: "19090"
      container_port: "9090"
  health:
    scheme_env: REPLOY_PUBLIC_SCHEME
    host_env: REPLOY_PORT_HTTP_HOST_BIND
    port_env: REPLOY_PORT_HTTP_HOST_PORT
    default_scheme: http
    default_host: 127.0.0.1
    default_port: "18080"
    path: /health
  default_command: serve
  commands:
    serve:
      container:
        argv:
          - demo-server
          - serve
    config_check:
      trigger:
        - config
        - check
      container:
        argv:
          - demo-server
          - config
          - check
`)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "installed")
	unitDir := t.TempDir()

	oldGeteuid := installGeteuid
	oldLookPath := installLookPath
	oldRunCommand := installRunCommand
	oldSystemdUnitDir := installSystemdUnitDir
	t.Cleanup(func() {
		installGeteuid = oldGeteuid
		installLookPath = oldLookPath
		installRunCommand = oldRunCommand
		installSystemdUnitDir = oldSystemdUnitDir
	})
	installGeteuid = func() int { return 0 }
	installSystemdUnitDir = unitDir
	installLookPath = func(name string) (string, error) {
		switch name {
		case "docker":
			return "/usr/bin/docker", nil
		case "systemctl":
			return "/bin/systemctl", nil
		default:
			return "", errors.New("not found")
		}
	}
	installRunCommand = func(name string, args ...string) error { return nil }

	if err := Install(InstallOptions{
		Dir:     deployDir,
		Target:  target,
		Service: "demo2",
		PortOverrides: []PortOverride{
			{Name: "http", HostPort: "18082"},
			{Name: "metrics", HostPort: "19092"},
		},
		Start: false,
	}); err != nil {
		t.Fatal(err)
	}
	dockerEnv := readFile(t, filepath.Join(target, DockerEnvFileName))
	for _, want := range []string{
		"REPLOY_HOST_PORT=18082",
		"REPLOY_PORT_HTTP_HOST_PORT=18082",
		"REPLOY_PORT_METRICS_HOST_PORT=19092",
	} {
		if !strings.Contains(dockerEnv, want) {
			t.Fatalf("installed docker.env missing %q:\n%s", want, dockerEnv)
		}
	}
	state, err := loadState(target)
	if err != nil {
		t.Fatal(err)
	}
	if state.Install.Ports["http"].HostPort != "18082" || state.Install.Ports["metrics"].HostPort != "19092" {
		t.Fatalf("install ports = %#v", state.Install.Ports)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
