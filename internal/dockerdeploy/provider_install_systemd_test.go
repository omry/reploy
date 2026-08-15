package dockerdeploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProviderInstallSystemdFileV1RendersRestartWithoutTimingPolicy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("systemd installation rendering is Linux-specific")
	}
	destinationDir := t.TempDir()
	references := fixedPublicationReferences(t, destinationDir, 0xb1)
	plan := providerInstallRunPlanFixture(destinationDir, references)

	files, err := providerInstallSystemdFileV1(plan, "/usr/bin/docker", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != plan.Installation.UnitPath || files[0].Mode != 0o644 {
		t.Fatalf("systemd files = %#v", files)
	}
	unit := string(files[0].Content)
	for _, want := range []string{
		"Requires=docker.service\nAfter=docker.service\n",
		"Type=notify\nNotifyAccess=main\n",
		"WorkingDirectory=" + destinationDir + "\n",
		"ExecStart=\"" + systemdPath(destinationDir, embeddedRuntimeFileName()) + "\" \"_service-container\"",
		"\"--dir\" \"" + systemdPath(destinationDir) + "\"",
		"\"--docker\" \"/usr/bin/docker\" \"run\"",
		"ExecStop=\"/usr/bin/docker\" \"compose\"",
		"\"--env-file\" \"" + systemdPath(destinationDir, DockerEnvFileName) + "\"",
		"\"down\" \"--remove-orphans\"\nRestart=on-failure\n",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit missing %q:\n%s", want, unit)
		}
	}
	for _, unwanted := range []string{"RestartSec=", "TimeoutStartSec=", "TimeoutStopSec="} {
		if strings.Contains(unit, unwanted) {
			t.Fatalf("unit contains timing policy %q:\n%s", unwanted, unit)
		}
	}
}

func TestProviderInstallSystemdFileV1PassesSystemdAnalyzeVerify(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd unit validation is Linux-specific")
	}
	systemdAnalyze, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Skip("systemd-analyze is unavailable")
	}
	destinationDir := filepath.Join(t.TempDir(), "work %n path")
	if err := os.Mkdir(destinationDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(destinationDir, embeddedRuntimeFileName())
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimePath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	references := fixedPublicationReferences(t, destinationDir, 0xb3)
	plan := providerInstallRunPlanFixture(destinationDir, references)

	files, err := providerInstallSystemdFileV1(plan, "/usr/bin/true", false)
	if err != nil {
		t.Fatal(err)
	}
	unit := string(files[0].Content)
	wantWorkingDirectory := "WorkingDirectory=" + strings.ReplaceAll(systemdPath(destinationDir), "%", "%%") + "\n"
	if !strings.Contains(unit, wantWorkingDirectory) {
		t.Fatalf("unit missing %q:\n%s", wantWorkingDirectory, unit)
	}
	if !strings.Contains(unit, `work %%n path`) {
		t.Fatalf("unit does not escape command specifiers:\n%s", unit)
	}
	unitPath := filepath.Join(t.TempDir(), "reploy-systemd-verify.service")
	if err := os.WriteFile(unitPath, files[0].Content, 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(systemdAnalyze, "verify", unitPath).CombinedOutput(); err != nil {
		t.Fatalf("systemd-analyze verify: %v\n%s", err, output)
	}
}

func TestProviderInstallSystemdFileV1EscapesCommandEnvironmentVariables(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("systemd installation rendering is Linux-specific")
	}
	destinationDir := filepath.Join(t.TempDir(), "work $USER path")
	references := fixedPublicationReferences(t, destinationDir, 0xb5)
	plan := providerInstallRunPlanFixture(destinationDir, references)

	files, err := providerInstallSystemdFileV1(plan, "/opt/$DOCKER/docker", false)
	if err != nil {
		t.Fatal(err)
	}
	unit := string(files[0].Content)
	for _, want := range []string{
		"WorkingDirectory=" + systemdPath(destinationDir) + "\n",
		`ExecStart="` + strings.ReplaceAll(systemdPath(destinationDir, embeddedRuntimeFileName()), "$", "$$") + `"`,
		`"--dir" "` + strings.ReplaceAll(systemdPath(destinationDir), "$", "$$") + `"`,
		`"--docker" "/opt/$$DOCKER/docker"`,
		`ExecStop="/opt/$$DOCKER/docker"`,
		`"--project-directory" "` + strings.ReplaceAll(systemdPath(destinationDir), "$", "$$") + `"`,
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit missing %q:\n%s", want, unit)
		}
	}
}

func TestProviderInstallSystemdFileV1OmitsUnknownDockerUnit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("systemd installation rendering is Linux-specific")
	}
	destinationDir := t.TempDir()
	references := fixedPublicationReferences(t, destinationDir, 0xb2)
	plan := providerInstallRunPlanFixture(destinationDir, references)

	files, err := providerInstallSystemdFileV1(plan, "/opt/docker bin/docker", false)
	if err != nil {
		t.Fatal(err)
	}
	unit := string(files[0].Content)
	if strings.Contains(unit, "docker.service") || !strings.Contains(unit, `"--docker" "/opt/docker bin/docker" "run"`) {
		t.Fatalf("unexpected Docker unit or path rendering:\n%s", unit)
	}
}

func TestProviderInstallSystemdFileV1SkipsNonSystemdBackend(t *testing.T) {
	plan := providerInstallationPlanV1{Backend: installBackendDockerManaged}
	files, err := providerInstallSystemdFileV1(plan, "", false)
	if err != nil || files == nil || len(files) != 0 {
		t.Fatalf("non-systemd files=%#v error=%v", files, err)
	}
}

func TestProviderInstallSystemdFileV1RejectsUnsupportedExecutablePaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("systemd installation rendering is Linux-specific")
	}
	destinationDir := t.TempDir()
	references := fixedPublicationReferences(t, destinationDir, 0xb4)
	plan := providerInstallRunPlanFixture(destinationDir, references)

	quotedTarget := plan
	quotedTarget.Installation.TargetDir = filepath.Join(destinationDir, `quoted"target`)
	if _, err := providerInstallSystemdFileV1(quotedTarget, "/usr/bin/docker", true); err == nil || !strings.Contains(err.Error(), "target directory contains a double quote") {
		t.Fatalf("quoted target error = %v", err)
	}
	backslashTarget := plan
	backslashTarget.Installation.TargetDir = filepath.Join(destinationDir, `backslash\target`)
	if _, err := providerInstallSystemdFileV1(backslashTarget, "/usr/bin/docker", true); err == nil || !strings.Contains(err.Error(), "target directory contains a backslash") {
		t.Fatalf("backslash target error = %v", err)
	}
	if _, err := providerInstallSystemdFileV1(plan, `/opt/docker"bin/docker`, true); err == nil || !strings.Contains(err.Error(), "Docker path contains a double quote") {
		t.Fatalf("quoted Docker path error = %v", err)
	}
}

func TestSystemdInstallArgumentV1EscapesSpecifiersVariablesAndQuotes(t *testing.T) {
	got, err := systemdInstallArgumentV1(`/opt/$HOME/100%/a "quoted" path`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `"/opt/$$HOME/100%%/a \"quoted\" path"` {
		t.Fatalf("quoted argument = %q", got)
	}
	if _, err := systemdInstallArgumentV1("line\nbreak"); err == nil {
		t.Fatal("expected control-character rejection")
	}
}

func TestSystemdInstallWorkingDirectoryV1EscapesSpecifiersWithoutCommandQuoting(t *testing.T) {
	got, err := systemdInstallWorkingDirectoryV1(`/opt/100%/a path`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `/opt/100%%/a path` {
		t.Fatalf("working directory = %q", got)
	}
	if _, err := systemdInstallWorkingDirectoryV1("line\nbreak"); err == nil {
		t.Fatal("expected control-character rejection")
	}
}
