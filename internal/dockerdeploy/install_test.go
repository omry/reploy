package dockerdeploy

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": "1000:1000"}); err != nil {
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
		"container user: 1000:1000",
		"install owner: 1000:1000 (1000:1000)",
		"installed container user: 1000:1000",
		"port default: 127.0.0.1:18082 -> 8080",
		"would set installed deployment ownership",
		"would write systemd unit: /etc/systemd/system/demo-test.service",
		"would run: systemctl daemon-reload",
		"would run: systemctl enable demo-test.service",
		"would run: systemctl restart demo-test.service",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestInstallPrintsPreinstallFailures(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": "missing-user"}); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	err = Install(InstallOptions{
		Dir:    deployDir,
		Target: filepath.Join(t.TempDir(), "installed"),
		DryRun: true,
		Stdout: &stdout,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "preinstall doctor failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), `fail: install owner must resolve to a non-root uid:gid: resolve REPLOY_INSTALL_OWNER user "missing-user"`) {
		t.Fatalf("stdout missing preinstall failure:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "ok:") {
		t.Fatalf("stdout should only show failing findings during install:\n%s", stdout.String())
	}
}

func TestInstallRequiresExplicitInstallOwner(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	err = Install(InstallOptions{
		Dir:    deployDir,
		Target: filepath.Join(t.TempDir(), "installed"),
		DryRun: true,
		Stdout: &stdout,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "preinstall doctor failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "fail: install owner must resolve to a non-root uid:gid: REPLOY_INSTALL_OWNER is required for install") {
		t.Fatalf("stdout missing install owner failure:\n%s", stdout.String())
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

func TestCopyDeploymentTreeProtectedSkipsToolBinary(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(filepath.Join(source, filepath.Dir(ToolBinaryFileName)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr/bin/reploy", filepath.Join(source, ToolBinaryFileName)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "reploy"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := copyDeploymentTreeProtected(source, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(target, ToolBinaryFileName)); !os.IsNotExist(err) {
		t.Fatalf("tool binary copied: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "reploy")); err != nil {
		t.Fatalf("helper was not copied: %v", err)
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
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": "appuser:appgroup"}); err != nil {
		t.Fatal(err)
	}
	manifest, err := deploy.LoadDeploymentManifest(filepath.Join(deployDir, ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	oldSourceToolBinary := []byte("old source reploy\n")
	if err := deploy.WriteGeneratedFile(deployDir, ToolBinaryFileName, oldSourceToolBinary, true, &manifest); err != nil {
		t.Fatal(err)
	}
	if err := deploy.WriteDeploymentManifest(filepath.Join(deployDir, ManifestFileName), manifest); err != nil {
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
	if err := os.MkdirAll(filepath.Join(target, filepath.Dir(ToolBinaryFileName)), 0o755); err != nil {
		t.Fatal(err)
	}
	staleTargetBinary := filepath.Join(t.TempDir(), "stale-reploy")
	if err := os.WriteFile(staleTargetBinary, []byte("stale target reploy\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(staleTargetBinary, filepath.Join(target, ToolBinaryFileName)); err != nil {
		t.Fatal(err)
	}
	unitDir := t.TempDir()

	oldGeteuid := installGeteuid
	oldLookPath := installLookPath
	oldRunCommand := installRunCommand
	oldRunCommandOutput := installRunCommandOutput
	oldToolBinaryContent := installToolBinaryContent
	oldChown := installChown
	oldLookupUser := installLookupUser
	oldLookupGroup := installLookupGroup
	oldRunTestCommandOutput := runTestCommandOutput
	oldServiceStartTimeout := installServiceStartTimeout
	oldServicePollInterval := installServicePollInterval
	oldSystemdUnitDir := installSystemdUnitDir
	t.Cleanup(func() {
		installGeteuid = oldGeteuid
		installLookPath = oldLookPath
		installRunCommand = oldRunCommand
		installRunCommandOutput = oldRunCommandOutput
		installToolBinaryContent = oldToolBinaryContent
		installChown = oldChown
		installLookupUser = oldLookupUser
		installLookupGroup = oldLookupGroup
		runTestCommandOutput = oldRunTestCommandOutput
		installServiceStartTimeout = oldServiceStartTimeout
		installServicePollInterval = oldServicePollInterval
		installSystemdUnitDir = oldSystemdUnitDir
	})

	installServiceStartTimeout = time.Second
	installServicePollInterval = time.Millisecond
	currentToolBinary := []byte("current installed reploy\n")
	installToolBinaryContent = func() ([]byte, error) {
		return currentToolBinary, nil
	}
	chownedPaths := map[string][2]int{}
	installChown = func(path string, uid int, gid int) error {
		chownedPaths[path] = [2]int{uid, gid}
		return nil
	}
	installLookupUser = func(name string) (*user.User, error) {
		if name != "appuser" {
			return nil, errors.New("missing user")
		}
		return &user.User{Username: "appuser", Uid: "997", Gid: "988"}, nil
	}
	installLookupGroup = func(name string) (*user.Group, error) {
		if name != "appgroup" {
			return nil, errors.New("missing group")
		}
		return &user.Group{Name: "appgroup", Gid: "988"}, nil
	}
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
	runTestCommandOutput = func(spec CommandSpec) ([]byte, error) {
		if !containsString(spec.Args, "--project-name") {
			t.Fatalf("service probe args did not use install project: %#v", spec.Args)
		}
		return []byte(`[{"State":"running"}]`), nil
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
	targetToolBinary := filepath.Join(target, ToolBinaryFileName)
	installedToolBinary, err := os.ReadFile(targetToolBinary)
	if err != nil {
		t.Fatal(err)
	}
	if string(installedToolBinary) != string(currentToolBinary) {
		t.Fatalf("installed reploy binary = %q, want %q", installedToolBinary, currentToolBinary)
	}
	info, err := os.Lstat(targetToolBinary)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("installed reploy binary is a symlink: %s", targetToolBinary)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("installed reploy binary is not regular: %s mode=%s", targetToolBinary, info.Mode())
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed reploy binary is not executable: mode=%s", info.Mode())
	}
	targetManifest, err := deploy.LoadDeploymentManifest(filepath.Join(target, ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	targetBinaryManifestEntry, ok := targetManifest.Files[filepath.ToSlash(ToolBinaryFileName)]
	if !ok {
		t.Fatalf("installed manifest missing %s", ToolBinaryFileName)
	}
	if targetBinaryManifestEntry.SHA256 != deploy.HashBytes(currentToolBinary) {
		t.Fatalf("installed manifest binary hash = %s, want %s", targetBinaryManifestEntry.SHA256, deploy.HashBytes(currentToolBinary))
	}
	for _, path := range []string{
		target,
		filepath.Join(target, "conf"),
		filepath.Join(target, "data"),
		filepath.Join(target, BundleDirName),
		filepath.Join(target, RuntimeDirName),
		filepath.Join(target, RequirementsFileName),
		filepath.Join(target, ToolBinaryFileName),
		filepath.Join(target, StateFileName),
	} {
		owner, ok := chownedPaths[path]
		if !ok {
			t.Fatalf("path was not chowned: %s", path)
		}
		if owner != [2]int{997, 988} {
			t.Fatalf("owner for %s = %#v, want 997:988", path, owner)
		}
	}
	sourceToolBinary, err := os.ReadFile(filepath.Join(deployDir, ToolBinaryFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(sourceToolBinary) != string(oldSourceToolBinary) {
		t.Fatalf("source reploy binary changed: %q", sourceToolBinary)
	}
	currentToolBinary = []byte("newer installed reploy\n")
	if err := Install(InstallOptions{
		Dir:           deployDir,
		Target:        target,
		Service:       "demo-apply",
		PortOverrides: []PortOverride{{HostPort: "18082"}},
		Start:         true,
	}); err != nil {
		t.Fatal(err)
	}
	installedToolBinary, err = os.ReadFile(targetToolBinary)
	if err != nil {
		t.Fatal(err)
	}
	if string(installedToolBinary) != string(currentToolBinary) {
		t.Fatalf("reinstalled reploy binary = %q, want %q", installedToolBinary, currentToolBinary)
	}
	targetManifest, err = deploy.LoadDeploymentManifest(filepath.Join(target, ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	targetBinaryManifestEntry = targetManifest.Files[filepath.ToSlash(ToolBinaryFileName)]
	if targetBinaryManifestEntry.SHA256 != deploy.HashBytes(currentToolBinary) {
		t.Fatalf("reinstalled manifest binary hash = %s, want %s", targetBinaryManifestEntry.SHA256, deploy.HashBytes(currentToolBinary))
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
		"REPLOY_CONTAINER_USER=997:988",
		"REPLOY_INSTALL_OWNER=appuser:appgroup",
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
	} {
		if !containsString(commands, want) {
			t.Fatalf("commands missing %q: %#v", want, commands)
		}
	}
}

func TestInstallRunsConfiguredHooksAroundServiceStart(t *testing.T) {
	manifest := strings.Replace(testPackManifest(), "  health:\n", `  install:
    hooks:
      before_start:
        - app: [config, check]
      after_start:
        - health_check:
            wait: true
        - app: [config, check, --live]
  health:
`, 1)
	packDir := makeTestPackWithManifest(t, manifest)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": "1000:1000"}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "installed")
	unitDir := t.TempDir()

	var dryRun strings.Builder
	if err := Install(InstallOptions{
		Dir:     deployDir,
		Target:  filepath.Join(t.TempDir(), "dry-run-target"),
		Service: "demo-hooks",
		Start:   true,
		DryRun:  true,
		Stdout:  &dryRun,
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"would run before start hook: app config check",
		"would run after start hook: health check --wait",
		"would run after start hook: app config check --live",
	} {
		if !strings.Contains(dryRun.String(), want) {
			t.Fatalf("dry-run missing %q:\n%s", want, dryRun.String())
		}
	}

	oldGeteuid := installGeteuid
	oldLookPath := installLookPath
	oldRunCommand := installRunCommand
	oldRunCommandOutput := installRunCommandOutput
	oldToolBinaryContent := installToolBinaryContent
	oldChown := installChown
	oldRunTestCommandOutput := runTestCommandOutput
	oldServicePollInterval := installServicePollInterval
	oldSystemdUnitDir := installSystemdUnitDir
	t.Cleanup(func() {
		installGeteuid = oldGeteuid
		installLookPath = oldLookPath
		installRunCommand = oldRunCommand
		installRunCommandOutput = oldRunCommandOutput
		installToolBinaryContent = oldToolBinaryContent
		installChown = oldChown
		runTestCommandOutput = oldRunTestCommandOutput
		installServicePollInterval = oldServicePollInterval
		installSystemdUnitDir = oldSystemdUnitDir
	})

	installGeteuid = func() int { return 0 }
	installSystemdUnitDir = unitDir
	installToolBinaryContent = func() ([]byte, error) { return []byte("current reploy\n"), nil }
	installChown = func(path string, uid int, gid int) error { return nil }
	installServicePollInterval = time.Millisecond
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
	runTestCommandOutput = func(spec CommandSpec) ([]byte, error) {
		return []byte(`[{"State":"running"}]`), nil
	}

	if err := Install(InstallOptions{
		Dir:     deployDir,
		Target:  target,
		Service: "demo-hooks",
		Start:   true,
	}); err != nil {
		t.Fatal(err)
	}

	wantOrder := []string{
		filepath.Join(target, "reploy") + " app config check",
		"/bin/systemctl restart demo-hooks.service",
		filepath.Join(target, "reploy") + " test",
		filepath.Join(target, "reploy") + " app config check --live",
	}
	lastIndex := -1
	for _, want := range wantOrder {
		index := indexOfString(commands, want)
		if index == -1 {
			t.Fatalf("commands missing %q: %#v", want, commands)
		}
		if index <= lastIndex {
			t.Fatalf("command %q ran out of order: %#v", want, commands)
		}
		lastIndex = index
	}
}

func TestParseInstallOwner(t *testing.T) {
	oldLookupUser := installLookupUser
	oldLookupGroup := installLookupGroup
	t.Cleanup(func() {
		installLookupUser = oldLookupUser
		installLookupGroup = oldLookupGroup
	})

	installLookupUser = func(name string) (*user.User, error) {
		switch name {
		case "appuser":
			return &user.User{Username: "appuser", Uid: "997", Gid: "988"}, nil
		case "baduser":
			return &user.User{Username: "baduser", Uid: "not-a-uid", Gid: "988"}, nil
		default:
			return nil, errors.New("missing user")
		}
	}
	installLookupGroup = func(name string) (*user.Group, error) {
		switch name {
		case "appgroup":
			return &user.Group{Name: "appgroup", Gid: "989"}, nil
		case "badgroup":
			return &user.Group{Name: "badgroup", Gid: "not-a-gid"}, nil
		default:
			return nil, errors.New("missing group")
		}
	}

	cases := []struct {
		value string
		uid   int
		gid   int
	}{
		{value: "997:988", uid: 997, gid: 988},
		{value: "997", uid: 997, gid: 997},
		{value: "appuser", uid: 997, gid: 988},
		{value: "appuser:appgroup", uid: 997, gid: 989},
		{value: "appuser:988", uid: 997, gid: 988},
		{value: "997:appgroup", uid: 997, gid: 989},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			uid, gid, err := parseInstallOwner(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			if uid != tc.uid || gid != tc.gid {
				t.Fatalf("owner = %d:%d, want %d:%d", uid, gid, tc.uid, tc.gid)
			}
		})
	}
}

func TestParseInstallOwnerRejectsUnsupportedValues(t *testing.T) {
	oldLookupUser := installLookupUser
	oldLookupGroup := installLookupGroup
	t.Cleanup(func() {
		installLookupUser = oldLookupUser
		installLookupGroup = oldLookupGroup
	})

	installLookupUser = func(name string) (*user.User, error) {
		switch name {
		case "rootish":
			return &user.User{Username: "rootish", Uid: "0", Gid: "0"}, nil
		case "baduser":
			return &user.User{Username: "baduser", Uid: "not-a-uid", Gid: "988"}, nil
		default:
			return nil, errors.New("missing user")
		}
	}
	installLookupGroup = func(name string) (*user.Group, error) {
		if name == "badgroup" {
			return &user.Group{Name: "badgroup", Gid: "not-a-gid"}, nil
		}
		return nil, errors.New("missing group")
	}

	for _, value := range []string{"", ":", "rootish", "0:1000", "1000:0", "-1:1000", "missing-user", "1000:missing-group", "baduser", "1000:badgroup"} {
		t.Run(value, func(t *testing.T) {
			_, _, err := parseInstallOwner(value)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestWaitInstalledServiceRunningWaitsForServiceRows(t *testing.T) {
	dir := makeWaitInstallDeployment(t)
	oldRunTestCommandOutput := runTestCommandOutput
	oldServicePollInterval := installServicePollInterval
	t.Cleanup(func() {
		runTestCommandOutput = oldRunTestCommandOutput
		installServicePollInterval = oldServicePollInterval
	})

	installServicePollInterval = time.Millisecond
	outputs := [][]byte{
		[]byte(`[]`),
		[]byte(`[{"State":"created"}]`),
		[]byte(`[{"State":"running"}]`),
	}
	index := 0
	runTestCommandOutput = func(spec CommandSpec) ([]byte, error) {
		if index >= len(outputs) {
			return outputs[len(outputs)-1], nil
		}
		output := outputs[index]
		index++
		return output, nil
	}

	var stdout strings.Builder
	if err := waitInstalledServiceRunning(dir, time.Second, &stdout); err != nil {
		t.Fatal(err)
	}
	if index != len(outputs) {
		t.Fatalf("probes = %d, want %d", index, len(outputs))
	}
	for _, want := range []string{
		"waiting for installed service to start",
		"installed service state: not created yet",
		"installed service state: created",
		"installed service state: running",
		"installed service is running",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestWaitInstalledServiceRunningToleratesTransientExitedService(t *testing.T) {
	dir := makeWaitInstallDeployment(t)
	oldRunTestCommandOutput := runTestCommandOutput
	oldServicePollInterval := installServicePollInterval
	oldTerminalStateGrace := installServiceTerminalStateGrace
	t.Cleanup(func() {
		runTestCommandOutput = oldRunTestCommandOutput
		installServicePollInterval = oldServicePollInterval
		installServiceTerminalStateGrace = oldTerminalStateGrace
	})

	installServicePollInterval = time.Millisecond
	installServiceTerminalStateGrace = time.Second
	outputs := [][]byte{
		[]byte(`[{"State":"exited"}]`),
		[]byte(`[{"State":"running"}]`),
	}
	index := 0
	runTestCommandOutput = func(spec CommandSpec) ([]byte, error) {
		if index >= len(outputs) {
			return outputs[len(outputs)-1], nil
		}
		output := outputs[index]
		index++
		return output, nil
	}

	var stdout strings.Builder
	if err := waitInstalledServiceRunning(dir, time.Second, &stdout); err != nil {
		t.Fatal(err)
	}
	if index != len(outputs) {
		t.Fatalf("probes = %d, want %d", index, len(outputs))
	}
	for _, want := range []string{
		"installed service state: exited",
		"installed service state: running",
		"installed service is running",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestWaitInstalledServiceRunningFailsForExitedService(t *testing.T) {
	dir := makeWaitInstallDeployment(t)
	oldServicePollInterval := installServicePollInterval
	oldTerminalStateGrace := installServiceTerminalStateGrace
	restoreCommandOutput := stubTestCommandOutput([]byte(`[{"State":"exited"}]`), nil)
	t.Cleanup(func() {
		restoreCommandOutput()
		installServicePollInterval = oldServicePollInterval
		installServiceTerminalStateGrace = oldTerminalStateGrace
	})

	installServicePollInterval = time.Millisecond
	installServiceTerminalStateGrace = time.Millisecond

	err := waitInstalledServiceRunning(dir, time.Second, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "service is not running; current state: exited") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func makeWaitInstallDeployment(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ReployInternalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DockerEnvFileName), []byte("REPLOY_CONTAINER_NAME=demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestInstallAppHookIncludesCommandOutput(t *testing.T) {
	oldRunCommandOutput := installRunCommandOutput
	t.Cleanup(func() {
		installRunCommandOutput = oldRunCommandOutput
	})

	dir := makeWaitInstallDeployment(t)
	installRunCommandOutput = func(name string, args ...string) ([]byte, error) {
		return []byte("docker compose ps failed\npermission denied\n"), errors.New("exit status 1")
	}

	err := runInstallHooks(installPlan{TargetDir: dir}, "before start", []deploy.DockerInstallHookConfig{{App: []string{"config", "check"}}})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{
		"install hook before start app config check",
		"installed app hook: exit status 1",
		"docker compose ps failed",
		"permission denied",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q:\n%v", want, err)
		}
	}
}

func TestInstallHealthHookIncludesLogsWhenServiceDoesNotStart(t *testing.T) {
	oldRunCommandOutput := installRunCommandOutput
	oldRunTestCommandOutput := runTestCommandOutput
	oldTerminalStateGrace := installServiceTerminalStateGrace
	t.Cleanup(func() {
		installRunCommandOutput = oldRunCommandOutput
		runTestCommandOutput = oldRunTestCommandOutput
		installServiceTerminalStateGrace = oldTerminalStateGrace
	})

	installServiceTerminalStateGrace = time.Millisecond
	dir := makeWaitInstallDeployment(t)
	runTestCommandOutput = func(spec CommandSpec) ([]byte, error) {
		return []byte(`[{"State":"exited"}]`), nil
	}
	installRunCommandOutput = func(name string, args ...string) ([]byte, error) {
		if len(args) != 1 || args[0] != "logs" {
			t.Fatalf("unexpected command: %s %#v", name, args)
		}
		return []byte("app failed during startup\nconfiguration check failed\n"), nil
	}

	err := runInstallHooks(installPlan{TargetDir: dir}, "after start", []deploy.DockerInstallHookConfig{{HealthCheck: &deploy.DockerInstallHealthCheckConfig{Wait: true}}})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{
		"install hook after start health check --wait",
		"installed service start: service is not running; current state: exited",
		"installed service logs:",
		"app failed during startup",
		"configuration check failed",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q:\n%v", want, err)
		}
	}
}

func TestInstallRebuildsLocalSourceBundleInTarget(t *testing.T) {
	manifest := strings.Replace(testPackManifest(), "    identifier: demo-suite\n", "    identifier: demo-suite\n    local_sources:\n      demo-server: local/demo-server\n", 1)
	packDir := makeTestPackWithManifest(t, manifest)
	sourceDir := filepath.Join(packDir, "local", "demo-server")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "pyproject.toml"), []byte("[build-system]\nrequires = [\"setuptools>=68\", \"wheel\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref, Requirements: []string{"demo-server==1.2.3"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": "1000:1000"}); err != nil {
		t.Fatal(err)
	}
	sourceWheel := filepath.Join(deployDir, BundleDirName, "demo_server-1.2.3-py3-none-any.whl")
	if err := os.MkdirAll(filepath.Dir(sourceWheel), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourceWheel, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var dryRun strings.Builder
	if err := Install(InstallOptions{
		Dir:     deployDir,
		Target:  filepath.Join(t.TempDir(), "dry-run-target"),
		Service: "demo-install",
		DryRun:  true,
		Stdout:  &dryRun,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dryRun.String(), "would rebuild local source bundle: demo-server") {
		t.Fatalf("dry-run missing local source rebuild:\n%s", dryRun.String())
	}

	target := filepath.Join(t.TempDir(), "installed")
	unitDir := t.TempDir()
	oldGeteuid := installGeteuid
	oldLookPath := installLookPath
	oldRunCommand := installRunCommand
	oldToolBinaryContent := installToolBinaryContent
	oldChown := installChown
	oldSystemdUnitDir := installSystemdUnitDir
	oldRunBundleCommand := runBundleCommand
	t.Cleanup(func() {
		installGeteuid = oldGeteuid
		installLookPath = oldLookPath
		installRunCommand = oldRunCommand
		installToolBinaryContent = oldToolBinaryContent
		installChown = oldChown
		installSystemdUnitDir = oldSystemdUnitDir
		runBundleCommand = oldRunBundleCommand
	})

	installGeteuid = func() int { return 0 }
	installSystemdUnitDir = unitDir
	installToolBinaryContent = func() ([]byte, error) { return []byte("current reploy\n"), nil }
	installChown = func(path string, uid int, gid int) error { return nil }
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

	var specs []CommandSpec
	runBundleCommand = func(spec CommandSpec, options RunOptions) error {
		specs = append(specs, spec)
		if spec.Dir != target {
			t.Fatalf("bundle rebuild ran in %q, want target %q", spec.Dir, target)
		}
		switch {
		case containsInOrder(spec.Args, []string{"sh", "-c"}):
			if mount := hostPathForContainerMount(t, spec.Args, "/source/demo-server"); mount != sourceDir {
				t.Fatalf("source mount = %q, want %q", mount, sourceDir)
			}
			wheelhouse := hostPathForContainerMount(t, spec.Args, "/wheelhouse")
			return os.WriteFile(filepath.Join(wheelhouse, "demo_server-1.2.3-py3-none-any.whl"), []byte("fresh\n"), 0o644)
		case containsInOrder(spec.Args, []string{"install", "--no-cache-dir", "--target"}):
			return nil
		default:
			t.Fatalf("unexpected bundle command: %#v", spec.Args)
			return nil
		}
	}

	if err := Install(InstallOptions{
		Dir:     deployDir,
		Target:  target,
		Service: "demo-install",
		Start:   false,
	}); err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 {
		t.Fatalf("bundle commands = %d, want build and check", len(specs))
	}
	targetWheel := filepath.Join(target, BundleDirName, "demo_server-1.2.3-py3-none-any.whl")
	if got := readFile(t, targetWheel); got != "fresh\n" {
		t.Fatalf("installed wheel = %q, want fresh", got)
	}
	if got := readFile(t, sourceWheel); got != "stale\n" {
		t.Fatalf("source deployment wheel was mutated: %q", got)
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
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": "1000:1000"}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "installed")
	unitDir := t.TempDir()

	oldGeteuid := installGeteuid
	oldLookPath := installLookPath
	oldRunCommand := installRunCommand
	oldChown := installChown
	oldSystemdUnitDir := installSystemdUnitDir
	t.Cleanup(func() {
		installGeteuid = oldGeteuid
		installLookPath = oldLookPath
		installRunCommand = oldRunCommand
		installChown = oldChown
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
	installChown = func(path string, uid int, gid int) error { return nil }

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

func indexOfString(values []string, want string) int {
	for index, value := range values {
		if value == want {
			return index
		}
	}
	return -1
}
