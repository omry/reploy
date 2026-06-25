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
		Dir:     deployDir,
		Target:  target,
		Service: "arbiter-test",
		Start:   true,
		DryRun:  true,
		Stdout:  &stdout,
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"would install deployment:",
		"target: " + target,
		"service: arbiter-test",
		"would write systemd unit: /etc/systemd/system/arbiter-test.service",
		"would run: systemctl daemon-reload",
		"would run: systemctl enable arbiter-test.service",
		"would run: systemctl restart arbiter-test.service",
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

func TestSystemdUnitIncludesComposeOverrideWhenPresent(t *testing.T) {
	unit := systemdUnit(installPlan{
		TargetDir:       "/srv/arbiter",
		Service:         "arbiter-prod",
		ComposeOverride: true,
	}, "/usr/bin/docker", true)
	for _, want := range []string{
		"Requires=docker.service",
		"After=docker.service",
		"WorkingDirectory=/srv/arbiter",
		"ExecStart=/usr/bin/docker compose --env-file /srv/arbiter/.reploy/docker.env --project-directory /srv/arbiter -f /srv/arbiter/.reploy/compose.yaml -f /srv/arbiter/.reploy/compose.override.yaml up",
		"ExecStop=/usr/bin/docker compose --env-file /srv/arbiter/.reploy/docker.env --project-directory /srv/arbiter -f /srv/arbiter/.reploy/compose.yaml -f /srv/arbiter/.reploy/compose.override.yaml down",
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

	if err := writeInstalledState(deployDir); err != nil {
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
	commands := []string{}
	installRunCommand = func(name string, args ...string) error {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return nil
	}

	if err := Install(InstallOptions{
		Dir:     deployDir,
		Target:  target,
		Service: "arbiter-apply",
		Start:   true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, ComposeFileName)); err != nil {
		t.Fatalf("missing copied compose: %v", err)
	}
	state, err := loadState(target)
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != deploy.PhaseInstalled {
		t.Fatalf("phase = %s, want %s", state.Phase, deploy.PhaseInstalled)
	}
	unit, err := os.ReadFile(filepath.Join(unitDir, "arbiter-apply.service"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), "ExecStart=/usr/bin/docker compose --env-file "+filepath.Join(target, DockerEnvFileName)) {
		t.Fatalf("unit does not point at target docker.env:\n%s", unit)
	}
	for _, want := range []string{
		"/bin/systemctl cat docker.service",
		"/bin/systemctl daemon-reload",
		"/bin/systemctl enable arbiter-apply.service",
		"/bin/systemctl restart arbiter-apply.service",
		filepath.Join(target, "reploy") + " test",
		filepath.Join(target, "reploy") + " app config check --live",
	} {
		if !containsString(commands, want) {
			t.Fatalf("commands missing %q: %#v", want, commands)
		}
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
