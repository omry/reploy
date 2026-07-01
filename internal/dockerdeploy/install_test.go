package dockerdeploy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	markTestBundlePrepared(t, deployDir)
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
		"port https: 127.0.0.1:18082 -> 8075",
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

func TestInstallDryRunPrintsMissingOwnerCreation(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	markTestBundlePrepared(t, deployDir)
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{
		"REPLOY_INSTALL_OWNER":            "appuser:appgroup",
		"REPLOY_INSTALL_OWNER_ON_MISSING": "create",
	}); err != nil {
		t.Fatal(err)
	}
	oldLookupUser := installLookupUser
	oldLookupGroup := installLookupGroup
	t.Cleanup(func() {
		installLookupUser = oldLookupUser
		installLookupGroup = oldLookupGroup
	})
	installLookupUser = func(name string) (*user.User, error) {
		return nil, user.UnknownUserError(name)
	}
	installLookupGroup = func(name string) (*user.Group, error) {
		return nil, user.UnknownGroupError(name)
	}

	var stdout strings.Builder
	if err := Install(InstallOptions{
		Dir:    deployDir,
		Target: filepath.Join(t.TempDir(), "installed"),
		DryRun: true,
		Stdout: &stdout,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "install owner: appuser:appgroup (will create system user/group)") {
		t.Fatalf("stdout missing owner creation plan:\n%s", stdout.String())
	}
}

func TestInstallDryRunRejectsAmbiguousGroupLookupWithMissingUser(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	markTestBundlePrepared(t, deployDir)
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{
		"REPLOY_INSTALL_OWNER":            "appuser:appgroup",
		"REPLOY_INSTALL_OWNER_ON_MISSING": "create",
	}); err != nil {
		t.Fatal(err)
	}
	oldLookupUser := installLookupUser
	oldLookupGroup := installLookupGroup
	t.Cleanup(func() {
		installLookupUser = oldLookupUser
		installLookupGroup = oldLookupGroup
	})
	installLookupUser = func(name string) (*user.User, error) {
		return nil, user.UnknownUserError(name)
	}
	installLookupGroup = func(name string) (*user.Group, error) {
		return nil, errors.New("nss backend unavailable")
	}

	var stdout strings.Builder
	err = Install(InstallOptions{
		Dir:    deployDir,
		Target: filepath.Join(t.TempDir(), "installed"),
		DryRun: true,
		Stdout: &stdout,
	})
	if err == nil {
		t.Fatal("expected preinstall failure")
	}
	if !strings.Contains(err.Error(), "preinstall doctor failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(stdout.String(), "will create system user/group") {
		t.Fatalf("stdout should not claim owner creation is ready:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `fail: install owner must resolve to a non-root uid:gid: lookup install owner group "appgroup": nss backend unavailable`) {
		t.Fatalf("stdout missing owner lookup failure:\n%s", stdout.String())
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
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": ""}); err != nil {
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

func TestInstallRequiresCurrentStagingBundle(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	oldGeteuid := installGeteuid
	oldRunBundleCommand := runBundleCommand
	t.Cleanup(func() {
		installGeteuid = oldGeteuid
		runBundleCommand = oldRunBundleCommand
	})
	installGeteuid = func() int { return 0 }
	runBundleCommand = func(spec CommandSpec, options RunOptions) error {
		t.Fatalf("install should not build an existing staging bundle: %#v", spec.Args)
		return nil
	}

	err = Install(InstallOptions{
		Dir:    deployDir,
		Target: filepath.Join(t.TempDir(), "installed"),
		Start:  false,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "staging bundle is outdated") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "retest the staging environment") {
		t.Fatalf("error should tell user to retest staging: %v", err)
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

	if err := copyDeploymentTreeProtected(source, target, nil, ""); err != nil {
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

	err := copyDeploymentTreeProtected(source, target, nil, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "refusing to copy symlink") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCopyDeploymentTreeProtectedRejectsTargetSymlinks(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(filepath.Join(source, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "conf", "config.yaml"), []byte("source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("victim\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(target, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(target, "conf", "config.yaml")); err != nil {
		t.Fatal(err)
	}

	err := copyDeploymentTreeProtected(source, target, nil, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite target symlink") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readFile(t, victim); got != "victim\n" {
		t.Fatalf("victim was overwritten: %q", got)
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

	if err := copyDeploymentTreeProtected(source, target, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, RuntimeDirName)); !os.IsNotExist(err) {
		t.Fatalf("runtime dir copied: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(target, BundleDirName, "demo-1.0.0-py3-none-any.whl")); err != nil {
		t.Fatalf("bundle file was not copied: %v", err)
	}
}

func TestCopyDeploymentTreeProtectedSkipsReployEntrypoints(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(source, "democtl"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := copyDeploymentTreeProtected(source, target, nil, "democtl"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(target, ToolBinaryFileName)); !os.IsNotExist(err) {
		t.Fatalf("tool binary copied: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "reploy")); !os.IsNotExist(err) {
		t.Fatalf("helper copied: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "democtl")); !os.IsNotExist(err) {
		t.Fatalf("staging control script copied: err=%v", err)
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
		"ExecStart=/usr/bin/docker compose --env-file /srv/demo/.reploy/docker.env --project-directory /srv/demo -f /srv/demo/.reploy/runtime/compose.yaml -f /srv/demo/.reploy/compose.override.yaml up",
		"ExecStop=/usr/bin/docker compose --env-file /srv/demo/.reploy/docker.env --project-directory /srv/demo -f /srv/demo/.reploy/runtime/compose.yaml -f /srv/demo/.reploy/compose.override.yaml down",
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
	markTestBundlePrepared(t, deployDir)
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
	runTestCommandOutput = func(spec CommandSpec, options RunOptions) ([]byte, error) {
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
	if _, err := os.Lstat(targetToolBinary); !os.IsNotExist(err) {
		t.Fatalf("installed reploy binary should be absent: err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(target, "reploy")); !os.IsNotExist(err) {
		t.Fatalf("installed reploy helper should be absent: err=%v", err)
	}
	controlScript := filepath.Join(target, "democtl")
	installedControlScript, err := os.ReadFile(controlScript)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`service="demo-apply.service"`,
		`up|start)`,
		`disable)`,
		`health)`,
	} {
		if !strings.Contains(string(installedControlScript), want) {
			t.Fatalf("control script missing %q:\n%s", want, installedControlScript)
		}
	}
	info, err := os.Lstat(controlScript)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("installed control script is a symlink: %s", controlScript)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("installed control script is not regular: %s mode=%s", controlScript, info.Mode())
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed control script is not executable: mode=%s", info.Mode())
	}
	targetManifest, err := deploy.LoadDeploymentManifest(filepath.Join(target, ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	targetScriptManifestEntry, ok := targetManifest.Files["democtl"]
	if !ok {
		t.Fatalf("installed manifest missing democtl")
	}
	if targetScriptManifestEntry.SHA256 != deploy.HashBytes(installedControlScript) {
		t.Fatalf("installed manifest script hash = %s, want %s", targetScriptManifestEntry.SHA256, deploy.HashBytes(installedControlScript))
	}
	for _, path := range []string{
		target,
		filepath.Join(target, "conf"),
		filepath.Join(target, "data"),
		filepath.Join(target, BundleDirName),
		filepath.Join(target, RuntimeDirName),
		filepath.Join(target, RequirementsFileName),
		controlScript,
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
	if err := Install(InstallOptions{
		Dir:           deployDir,
		Target:        target,
		Service:       "demo-apply",
		PortOverrides: []PortOverride{{HostPort: "18082"}},
		Start:         true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(targetToolBinary); !os.IsNotExist(err) {
		t.Fatalf("reinstalled reploy binary should be absent: err=%v", err)
	}
	targetManifest, err = deploy.LoadDeploymentManifest(filepath.Join(target, ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := targetManifest.Files[filepath.ToSlash(ToolBinaryFileName)]; ok {
		t.Fatalf("installed manifest still tracks %s", ToolBinaryFileName)
	}
	reinstalledControlScript, err := os.ReadFile(controlScript)
	if err != nil {
		t.Fatal(err)
	}
	if deploy.HashBytes(reinstalledControlScript) != targetManifest.Files["democtl"].SHA256 {
		t.Fatalf("reinstalled manifest script hash mismatch")
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
		"REPLOY_DEPLOYMENT_SCOPE=deployed",
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
	if state.Install.Ports["https"].HostPort != "18082" || state.Install.Ports["https"].ContainerPort != "8075" {
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

func TestWriteInstalledControlScriptRejectsTargetSymlink(t *testing.T) {
	target := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("victim\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(target, "democtl")); err != nil {
		t.Fatal(err)
	}

	err := writeInstalledControlScript(installPlan{
		TargetDir:     target,
		Service:       "demo",
		ControlScript: "democtl",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite target symlink") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readFile(t, victim); got != "victim\n" {
		t.Fatalf("victim was overwritten: %q", got)
	}
}

func TestDirectInstallAppliesViaTemporaryStaging(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "installed")
	unitDir := t.TempDir()

	oldGeteuid := installGeteuid
	oldLookPath := installLookPath
	oldRunCommand := installRunCommand
	oldRunCommandOutput := installRunCommandOutput
	oldRunBundleCommand := runBundleCommand
	oldChown := installChown
	oldSystemdUnitDir := installSystemdUnitDir
	t.Cleanup(func() {
		installGeteuid = oldGeteuid
		installLookPath = oldLookPath
		installRunCommand = oldRunCommand
		installRunCommandOutput = oldRunCommandOutput
		runBundleCommand = oldRunBundleCommand
		installChown = oldChown
		installSystemdUnitDir = oldSystemdUnitDir
	})

	installGeteuid = func() int { return 0 }
	installSystemdUnitDir = unitDir
	installChown = func(path string, uid int, gid int) error { return nil }
	installLookPath = func(name string) (string, error) {
		switch name {
		case "docker":
			return "/usr/bin/docker", nil
		case "systemctl":
			return "/bin/systemctl", nil
		default:
			return "", exec.ErrNotFound
		}
	}
	installRunCommandOutput = func(name string, args ...string) ([]byte, error) {
		return []byte(`[{"State":"running"}]`), nil
	}
	installRunCommand = func(name string, args ...string) error {
		return nil
	}
	runBundleCommand = func(spec CommandSpec, options RunOptions) error {
		if !containsAdjacent(spec.Args, "--user", defaultContainerUser()) {
			t.Fatalf("bundle command did not run container as default user: %#v", spec.Args)
		}
		switch {
		case containsInOrder(spec.Args, []string{"wheel", "--no-cache-dir"}):
			wheelhouse := hostPathForContainerMount(t, spec.Args, "/wheelhouse")
			return os.WriteFile(filepath.Join(wheelhouse, "demo_suite-1.2.3-py3-none-any.whl"), []byte("suite\n"), 0o644)
		case containsInOrder(spec.Args, []string{"install", "--no-cache-dir", "--target"}):
			return nil
		default:
			t.Fatalf("unexpected bundle command: %#v", spec.Args)
			return nil
		}
	}

	installedTarget, err := DirectInstall(DirectInstallOptions{
		Pack:   ref,
		Target: target,
		Start:  false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if installedTarget != target {
		t.Fatalf("installed target = %q, want %q", installedTarget, target)
	}
	if _, err := os.Stat(filepath.Join(target, StateFileName)); err != nil {
		t.Fatalf("missing installed state: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "democtl")); err != nil {
		t.Fatalf("missing installed control script: %v", err)
	}
	state, err := loadState(target)
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != deploy.PhaseInstalled || state.Install == nil {
		t.Fatalf("state was not marked installed: %#v", state)
	}
	if _, err := os.Stat(filepath.Join(unitDir, "demo.service")); err != nil {
		t.Fatalf("missing systemd unit: %v", err)
	}
}

func TestValidateDeployedControlCommandsRejectsConflicts(t *testing.T) {
	err := validateDeployedControlCommands([]deploy.DockerCommandConfig{{
		Name:       "status_check",
		Trigger:    []string{"status", "check"},
		AppCommand: true,
		Deployed:   true,
	}})
	if err == nil {
		t.Fatal("expected conflict")
	}
	if !strings.Contains(err.Error(), "conflicts with built-in control command") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateDeployedControlCommandsRequiresAppCommand(t *testing.T) {
	err := validateDeployedControlCommands([]deploy.DockerCommandConfig{{
		Name:     "config_check",
		Trigger:  []string{"config", "check"},
		Deployed: true,
	}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "must also set app_command: true") {
		t.Fatalf("unexpected error: %v", err)
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
	markTestBundlePrepared(t, deployDir)
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
	oldChown := installChown
	oldRunInstallAppCommand := runInstallAppCommand
	oldRunInstallHealthCheck := runInstallHealthCheck
	oldRunTestCommandOutput := runTestCommandOutput
	oldServicePollInterval := installServicePollInterval
	oldSystemdUnitDir := installSystemdUnitDir
	t.Cleanup(func() {
		installGeteuid = oldGeteuid
		installLookPath = oldLookPath
		installRunCommand = oldRunCommand
		installRunCommandOutput = oldRunCommandOutput
		installChown = oldChown
		runInstallAppCommand = oldRunInstallAppCommand
		runInstallHealthCheck = oldRunInstallHealthCheck
		runTestCommandOutput = oldRunTestCommandOutput
		installServicePollInterval = oldServicePollInterval
		installSystemdUnitDir = oldSystemdUnitDir
	})

	installGeteuid = func() int { return 0 }
	installSystemdUnitDir = unitDir
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
	runTestCommandOutput = func(spec CommandSpec, options RunOptions) ([]byte, error) {
		return []byte(`[{"State":"running"}]`), nil
	}
	runInstallAppCommand = func(dir string, args []string, stdout io.Writer, stderr io.Writer, dockerPreflightTimeout time.Duration) error {
		commands = append(commands, "app "+strings.Join(args, " "))
		return nil
	}
	runInstallHealthCheck = func(dir string, stdout io.Writer, stderr io.Writer, dockerPreflightTimeout time.Duration) error {
		commands = append(commands, "health")
		return nil
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
		"app config check",
		"/bin/systemctl restart demo-hooks.service",
		"health",
		"app config check --live",
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

func TestInstalledControlScriptHealthUsesDeclaredHealthProbe(t *testing.T) {
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, ReployInternalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, DockerEnvFileName), []byte("REPLOY_PUBLIC_SCHEME=https\nREPLOY_HOST_BIND=0.0.0.0\nREPLOY_HOST_PORT=18075\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tlsVerify := false
	script := filepath.Join(target, "democtl")
	content := controlScriptContent(installPlan{
		AppID:          "demo",
		TargetDir:      target,
		Service:        "demo",
		ComposeProject: "demo",
		Health: deploy.DockerHealthConfig{
			SchemeEnv:     "REPLOY_PUBLIC_SCHEME",
			HostEnv:       "REPLOY_HOST_BIND",
			PortEnv:       "REPLOY_HOST_PORT",
			DefaultScheme: "https",
			DefaultHost:   "127.0.0.1",
			DefaultPort:   "18075",
			Path:          "/_health_",
			TLSVerify:     &tlsVerify,
		},
	})
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	curlArgs := filepath.Join(t.TempDir(), "curl.args")
	fakeCurl := filepath.Join(fakeBin, "curl")
	if err := os.WriteFile(fakeCurl, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CURL_ARGS_FILE\"\nprintf 'health ok\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(script, "health")
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CURL_ARGS_FILE="+curlArgs,
		"REPLOY_COLOR=never",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("health command failed: %v\n%s", err, output)
	}
	if string(output) != "[demo] health ok\n" {
		t.Fatalf("health output = %q", output)
	}
	args := readFile(t, curlArgs)
	want := "-fsS\n--insecure\nhttps://127.0.0.1:18075/_health_\n"
	if args != want {
		t.Fatalf("curl args = %q, want %q", args, want)
	}
}

func TestControlScriptOutputLabelUsesAppIDOnly(t *testing.T) {
	if got := controlScriptOutputLabel("demo"); got != "[demo]" {
		t.Fatalf("label = %q", got)
	}
	if got := controlScriptOutputLabel(""); got != "[reploy]" {
		t.Fatalf("fallback label = %q", got)
	}
}

func TestControlScriptUsesSafeStatusFileAndPreservesPartialLines(t *testing.T) {
	content := controlScriptContent(installPlan{
		AppID:         "demo",
		TargetDir:     t.TempDir(),
		Service:       "demo",
		ControlScript: "democtl",
	})
	if !strings.Contains(content, `mktemp "${TMPDIR:-/tmp}/reploy-output-prefix.XXXXXX"`) {
		t.Fatalf("control script does not use mktemp for status handoff:\n%s", content)
	}
	if !strings.Contains(content, `read -r reploy_control_line || [ -n "$reploy_control_line" ]`) {
		t.Fatalf("control script does not preserve final unterminated output line:\n%s", content)
	}
}

func TestInstalledControlScriptRunsDeployedAppCommand(t *testing.T) {
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, ReployInternalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	owner := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	if err := os.WriteFile(filepath.Join(target, DockerEnvFileName), []byte("REPLOY_INSTALL_OWNER="+owner+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(target, "democtl")
	content := controlScriptContent(installPlan{
		AppID:          "demo",
		TargetDir:      target,
		Service:        "demo",
		ComposeProject: "demo-project",
		ControlScript:  "democtl",
		ConfigDir:      "conf",
		DeployedCommands: []deploy.DockerCommandConfig{{
			Name:         "config_check",
			Trigger:      []string{"config", "check"},
			AppCommand:   true,
			Deployed:     true,
			ForwardFlags: []string{"--live"},
		}},
	})
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	helpCommand := exec.Command(script, "--help")
	helpOutput, err := helpCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("help failed: %v\n%s", err, helpOutput)
	}
	if !strings.Contains(string(helpOutput), "config check") {
		t.Fatalf("help output did not include deployed command:\n%s", helpOutput)
	}

	fakeBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	dockerArgs := filepath.Join(t.TempDir(), "docker.args")
	fakeDocker := filepath.Join(fakeBin, "docker")
	if err := os.WriteFile(fakeDocker, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOCKER_ARGS_FILE\"\nprintf 'docker output\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(script, "config", "check", "--live")
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DOCKER_ARGS_FILE="+dockerArgs,
		"REPLOY_COLOR=never",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("deployed command failed: %v\n%s", err, output)
	}
	if string(output) != "[demo] docker output\n" {
		t.Fatalf("deployed command output = %q", output)
	}
	args := readFile(t, dockerArgs)
	for _, want := range []string{
		"compose\n",
		"--project-name\n",
		"demo-project\n",
		"REPLOY_CONTAINER_COMMAND=config_check\n",
		"REPLOY_FORWARDED_ARGC=1\n",
		"REPLOY_FORWARDED_ARG_0=--live\n",
		"REPLOY_APP_COMMAND_PREFIX=democtl\n",
		"app\n",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("docker args missing %q:\n%s", want, args)
		}
	}
}

func TestInstalledControlScriptPrefixesSystemdOutput(t *testing.T) {
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, ReployInternalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, DockerEnvFileName), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(target, "democtl")
	content := controlScriptContent(installPlan{
		AppID:         "demo",
		TargetDir:     target,
		Service:       "demo",
		ControlScript: "democtl",
	})
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	systemctlArgs := filepath.Join(t.TempDir(), "systemctl.args")
	fakeSystemctl := filepath.Join(fakeBin, "systemctl")
	if err := os.WriteFile(fakeSystemctl, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$SYSTEMCTL_ARGS_FILE\"\nprintf 'systemd output\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(script, "status")
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SYSTEMCTL_ARGS_FILE="+systemctlArgs,
		"REPLOY_COLOR=never",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("status failed: %v\n%s", err, output)
	}
	if string(output) != "[demo] systemd output\n" {
		t.Fatalf("status output = %q", output)
	}
	args := readFile(t, systemctlArgs)
	want := "status\ndemo.service\n"
	if args != want {
		t.Fatalf("systemctl args = %q, want %q", args, want)
	}
}

func TestInstalledControlScriptLogsUsesReployOptions(t *testing.T) {
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, ReployInternalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, DockerEnvFileName), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(target, "democtl")
	content := controlScriptContent(installPlan{
		AppID:         "demo",
		TargetDir:     target,
		Service:       "demo",
		ControlScript: "democtl",
	})
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	journalctlArgs := filepath.Join(t.TempDir(), "journalctl.args")
	fakeJournalctl := filepath.Join(fakeBin, "journalctl")
	if err := os.WriteFile(fakeJournalctl, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$JOURNALCTL_ARGS_FILE\"\nprintf 'journal output\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(script, "logs", "--tail=100", "--follow")
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"JOURNALCTL_ARGS_FILE="+journalctlArgs,
		"REPLOY_COLOR=never",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("logs failed: %v\n%s", err, output)
	}
	if string(output) != "[demo] journal output\n" {
		t.Fatalf("logs output = %q", output)
	}
	args := readFile(t, journalctlArgs)
	want := "-u\ndemo.service\n-n\n100\n-f\n"
	if args != want {
		t.Fatalf("journalctl args = %q, want %q", args, want)
	}
}

func TestValidateDeployedControlCommandsRejectsStageBuiltin(t *testing.T) {
	err := validateDeployedControlCommands([]deploy.DockerCommandConfig{{
		Name:       "stage_app",
		Trigger:    []string{"stage"},
		AppCommand: true,
		Deployed:   true,
	}})
	if err == nil {
		t.Fatal("expected stage command conflict")
	}
	if !strings.Contains(err.Error(), `conflicts with built-in control command "stage"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInstalledControlScriptRejectsUndeployedAppCommandFlag(t *testing.T) {
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, ReployInternalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	owner := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	if err := os.WriteFile(filepath.Join(target, DockerEnvFileName), []byte("REPLOY_INSTALL_OWNER="+owner+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(target, "democtl")
	content := controlScriptContent(installPlan{
		AppID:         "demo",
		TargetDir:     target,
		Service:       "demo",
		ControlScript: "democtl",
		ConfigDir:     "conf",
		DeployedCommands: []deploy.DockerCommandConfig{{
			Name:         "config_check",
			Trigger:      []string{"config", "check"},
			AppCommand:   true,
			Deployed:     true,
			ForwardFlags: []string{"--live"},
		}},
	})
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(script, "config", "check", "--unsafe")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(string(output), "unknown forwarded flag: --unsafe") {
		t.Fatalf("unexpected output:\n%s", output)
	}
}

func TestInstalledControlScriptAcceptsForwardedFlagValueWithEquals(t *testing.T) {
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, ReployInternalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	owner := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	if err := os.WriteFile(filepath.Join(target, DockerEnvFileName), []byte("REPLOY_INSTALL_OWNER="+owner+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(target, "democtl")
	content := controlScriptContent(installPlan{
		AppID:         "demo",
		TargetDir:     target,
		Service:       "demo",
		ControlScript: "democtl",
		ConfigDir:     "conf",
		DeployedCommands: []deploy.DockerCommandConfig{{
			Name:         "config_check",
			Trigger:      []string{"config", "check"},
			AppCommand:   true,
			Deployed:     true,
			ForwardFlags: []string{"--set"},
		}},
	})
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	dockerArgs := filepath.Join(t.TempDir(), "docker.args")
	fakeDocker := filepath.Join(fakeBin, "docker")
	if err := os.WriteFile(fakeDocker, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOCKER_ARGS_FILE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(script, "config", "check", "--set=a=b")
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DOCKER_ARGS_FILE="+dockerArgs,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("deployed command failed: %v\n%s", err, output)
	}
	args := readFile(t, dockerArgs)
	if !strings.Contains(args, "REPLOY_FORWARDED_ARG_0=--set=a=b\n") {
		t.Fatalf("docker args missing forwarded flag value:\n%s", args)
	}
}

func TestInstalledControlScriptRejectsForwardedFlagPositionals(t *testing.T) {
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, ReployInternalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	owner := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	if err := os.WriteFile(filepath.Join(target, DockerEnvFileName), []byte("REPLOY_INSTALL_OWNER="+owner+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(target, "democtl")
	content := controlScriptContent(installPlan{
		AppID:         "demo",
		TargetDir:     target,
		Service:       "demo",
		ControlScript: "democtl",
		ConfigDir:     "conf",
		DeployedCommands: []deploy.DockerCommandConfig{{
			Name:         "config_check",
			Trigger:      []string{"config", "check"},
			AppCommand:   true,
			Deployed:     true,
			ForwardFlags: []string{"--live"},
		}},
	})
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(script, "config", "check", "--live", "extra")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(string(output), "unexpected positional argument after app command trigger: extra") {
		t.Fatalf("unexpected output:\n%s", output)
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

func TestEnsureInstallOwnerCreatesMissingSystemOwner(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ReployInternalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DockerEnvFileName), []byte("REPLOY_INSTALL_OWNER=appuser:appgroup\nREPLOY_INSTALL_OWNER_ON_MISSING=create\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldLookupUser := installLookupUser
	oldLookupGroup := installLookupGroup
	oldRunCommandOutput := installRunCommandOutput
	t.Cleanup(func() {
		installLookupUser = oldLookupUser
		installLookupGroup = oldLookupGroup
		installRunCommandOutput = oldRunCommandOutput
	})
	userCreated := false
	groupCreated := false
	commands := []string{}
	installLookupUser = func(name string) (*user.User, error) {
		if name == "appuser" && userCreated {
			return &user.User{Username: "appuser", Uid: "997", Gid: "988"}, nil
		}
		return nil, user.UnknownUserError(name)
	}
	installLookupGroup = func(name string) (*user.Group, error) {
		if name == "appgroup" && groupCreated {
			return &user.Group{Name: "appgroup", Gid: "988"}, nil
		}
		return nil, user.UnknownGroupError(name)
	}
	installRunCommandOutput = func(name string, args ...string) ([]byte, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		switch name {
		case "groupadd":
			groupCreated = true
		case "useradd":
			userCreated = true
		default:
			t.Fatalf("unexpected command: %s %v", name, args)
		}
		return nil, nil
	}

	if err := ensureInstallOwnerForDir(dir); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"groupadd --system appgroup",
		"useradd --system --gid appgroup --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin appuser",
	}
	if strings.Join(commands, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func TestEnsureInstallOwnerDoesNotCreateAfterAmbiguousResolveError(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ReployInternalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DockerEnvFileName), []byte("REPLOY_INSTALL_OWNER=appuser:appgroup\nREPLOY_INSTALL_OWNER_ON_MISSING=create\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldLookupUser := installLookupUser
	oldLookupGroup := installLookupGroup
	oldRunCommandOutput := installRunCommandOutput
	t.Cleanup(func() {
		installLookupUser = oldLookupUser
		installLookupGroup = oldLookupGroup
		installRunCommandOutput = oldRunCommandOutput
	})
	installLookupUser = func(name string) (*user.User, error) {
		return nil, errors.New("nss backend unavailable")
	}
	installLookupGroup = func(name string) (*user.Group, error) {
		t.Fatalf("group lookup should not run after user lookup failure: %s", name)
		return nil, nil
	}
	installRunCommandOutput = func(name string, args ...string) ([]byte, error) {
		t.Fatalf("account creation command should not run after ambiguous owner resolve failure: %s %v", name, args)
		return nil, nil
	}

	err := ensureInstallOwnerForDir(dir)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `resolve REPLOY_INSTALL_OWNER user "appuser": nss backend unavailable`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateMissingInstallOwnerFailsOnAmbiguousLookupError(t *testing.T) {
	for _, tc := range []struct {
		name        string
		lookupUser  func(string) (*user.User, error)
		lookupGroup func(string) (*user.Group, error)
		want        string
	}{
		{
			name: "group lookup",
			lookupUser: func(name string) (*user.User, error) {
				t.Fatalf("user lookup should not run after group lookup failure: %s", name)
				return nil, nil
			},
			lookupGroup: func(name string) (*user.Group, error) {
				return nil, errors.New("nss backend unavailable")
			},
			want: `lookup install owner group "appgroup": nss backend unavailable`,
		},
		{
			name: "user lookup",
			lookupUser: func(name string) (*user.User, error) {
				return nil, errors.New("nss backend unavailable")
			},
			lookupGroup: func(name string) (*user.Group, error) {
				return &user.Group{Name: "appgroup", Gid: "988"}, nil
			},
			want: `lookup install owner user "appuser": nss backend unavailable`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldLookupUser := installLookupUser
			oldLookupGroup := installLookupGroup
			oldRunCommandOutput := installRunCommandOutput
			t.Cleanup(func() {
				installLookupUser = oldLookupUser
				installLookupGroup = oldLookupGroup
				installRunCommandOutput = oldRunCommandOutput
			})
			installLookupUser = tc.lookupUser
			installLookupGroup = tc.lookupGroup
			installRunCommandOutput = func(name string, args ...string) ([]byte, error) {
				t.Fatalf("account creation command should not run after ambiguous lookup failure: %s %v", name, args)
				return nil, nil
			}

			err := createMissingInstallOwner(map[string]string{
				reployInstallOwnerEnv:       "appuser:appgroup",
				reployInstallOwnerOnMissing: "create",
			})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestEnsureInstallOwnerRejectsUnsafeCreateOwner(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
		want string
	}{
		{
			name: "flag user",
			env:  "REPLOY_INSTALL_OWNER=--bad:appgroup\nREPLOY_INSTALL_OWNER_ON_MISSING=create\n",
			want: "REPLOY_INSTALL_OWNER user must be a safe system account name",
		},
		{
			name: "space group",
			env:  "REPLOY_INSTALL_OWNER=appuser:bad group\nREPLOY_INSTALL_OWNER_ON_MISSING=create\n",
			want: "REPLOY_INSTALL_OWNER group must be a safe system account name",
		},
		{
			name: "extra separator",
			env:  "REPLOY_INSTALL_OWNER=appuser:appgroup:extra\nREPLOY_INSTALL_OWNER_ON_MISSING=create\n",
			want: "REPLOY_INSTALL_OWNER must not contain more than one separator",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, ReployInternalDir), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, DockerEnvFileName), []byte(tc.env), 0o644); err != nil {
				t.Fatal(err)
			}
			oldRunCommandOutput := installRunCommandOutput
			t.Cleanup(func() {
				installRunCommandOutput = oldRunCommandOutput
			})
			installRunCommandOutput = func(name string, args ...string) ([]byte, error) {
				t.Fatalf("account creation command should not run for unsafe owner: %s %v", name, args)
				return nil, nil
			}

			err := ensureInstallOwnerForDir(dir)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unexpected error: %v", err)
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
	runTestCommandOutput = func(spec CommandSpec, options RunOptions) ([]byte, error) {
		if index >= len(outputs) {
			return outputs[len(outputs)-1], nil
		}
		output := outputs[index]
		index++
		return output, nil
	}

	var stdout strings.Builder
	if err := waitInstalledServiceRunning(dir, time.Second, &stdout, 0); err != nil {
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
	runTestCommandOutput = func(spec CommandSpec, options RunOptions) ([]byte, error) {
		if index >= len(outputs) {
			return outputs[len(outputs)-1], nil
		}
		output := outputs[index]
		index++
		return output, nil
	}

	var stdout strings.Builder
	if err := waitInstalledServiceRunning(dir, time.Second, &stdout, 0); err != nil {
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

	err := waitInstalledServiceRunning(dir, time.Second, nil, 0)
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
	oldRunInstallAppCommand := runInstallAppCommand
	t.Cleanup(func() {
		runInstallAppCommand = oldRunInstallAppCommand
	})

	dir := makeWaitInstallDeployment(t)
	runInstallAppCommand = func(dir string, args []string, stdout io.Writer, stderr io.Writer, dockerPreflightTimeout time.Duration) error {
		if stderr != nil {
			fmt.Fprintln(stderr, "docker compose ps failed")
			fmt.Fprintln(stderr, "permission denied")
		}
		return errors.New("exit status 1")
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
	runTestCommandOutput = func(spec CommandSpec, options RunOptions) ([]byte, error) {
		return []byte(`[{"State":"exited"}]`), nil
	}
	installRunCommandOutput = func(name string, args ...string) ([]byte, error) {
		if len(args) != 1 || args[0] != "logs" {
			t.Fatalf("unexpected command: %s %#v", name, args)
		}
		return []byte("app failed during startup\nconfiguration check failed\n"), nil
	}

	err := runInstallHooks(installPlan{TargetDir: dir, ControlScript: "democtl"}, "after start", []deploy.DockerInstallHookConfig{{HealthCheck: &deploy.DockerInstallHealthCheckConfig{Wait: true}}})
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
	oldChown := installChown
	oldSystemdUnitDir := installSystemdUnitDir
	oldRunBundleCommand := runBundleCommand
	t.Cleanup(func() {
		installGeteuid = oldGeteuid
		installLookPath = oldLookPath
		installRunCommand = oldRunCommand
		installChown = oldChown
		installSystemdUnitDir = oldSystemdUnitDir
		runBundleCommand = oldRunBundleCommand
	})

	installGeteuid = func() int { return 0 }
	installSystemdUnitDir = unitDir
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
		if !containsAdjacent(spec.Args, "--user", defaultContainerUser()) {
			t.Fatalf("bundle rebuild did not run container as default user: %#v", spec.Args)
		}
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

install:
  owner:
    user: "1000"
    group: "1000"
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 8080
      metrics:
        host_bind: 127.0.0.1
        host_port: 9090
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080
        container_port: 8080
      metrics:
        host_bind: 127.0.0.1
        host_port: 19090
        container_port: 9090

docker:
  deployment_dirs:
    config: conf
    bundle: .reploy/bundle
    data: data
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
	markTestBundlePrepared(t, deployDir)
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

func TestInstallPreservesAppOwnedArtifactsByDefault(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "conf", "app.conf"), []byte("source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, RuntimeDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(target, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "conf", "app.conf"), []byte("installed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyDeploymentTreeProtected(source, target, []string{"conf"}, ""); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(target, "conf", "app.conf")); got != "installed\n" {
		t.Fatalf("preserved file = %q", got)
	}
}

func TestInstallReplaceArtifactOverwritesPreservedPath(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "conf", "app.conf"), []byte("source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(target, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "conf", "app.conf"), []byte("installed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyDeploymentTreeProtected(source, target, nil, ""); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(target, "conf", "app.conf")); got != "source\n" {
		t.Fatalf("replaced file = %q", got)
	}
}

func TestInstallAlwaysReplacesReployOwnedState(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, ".reploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".reploy", "state.json"), []byte("source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(target, ".reploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, ".reploy", "state.json"), []byte("installed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyDeploymentTreeProtected(source, target, []string{".reploy"}, ""); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(target, ".reploy", "state.json")); got != "source\n" {
		t.Fatalf("reploy state = %q", got)
	}
}

func TestInstallPreservePathsHonorReplaceAndClean(t *testing.T) {
	artifacts := map[string]deploy.InstallArtifactPolicyConfig{
		"config": {Default: "preserve", Paths: []string{"conf/"}},
		"env":    {Default: "preserve", Paths: []string{".env"}},
	}
	paths, err := installPreservePaths(artifacts, []string{"config"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(paths, ",") != ".env" {
		t.Fatalf("preserve paths = %#v", paths)
	}
	paths, err = installPreservePaths(artifacts, []string{"all"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("replace all preserve paths = %#v", paths)
	}
	paths, err = installPreservePaths(artifacts, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("clean preserve paths = %#v", paths)
	}
	_, err = installPreservePaths(artifacts, []string{"missing"}, false)
	if err == nil || !strings.Contains(err.Error(), "unknown install artifact") {
		t.Fatalf("expected unknown artifact error, got %v", err)
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
