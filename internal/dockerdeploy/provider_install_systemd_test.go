package dockerdeploy

import (
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
		"WorkingDirectory=\"" + destinationDir + "\"",
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

func TestSystemdInstallArgumentV1EscapesSpecifiersAndQuotes(t *testing.T) {
	got, err := systemdInstallArgumentV1(`/opt/100%/a "quoted" path`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `"/opt/100%%/a \"quoted\" path"` {
		t.Fatalf("quoted argument = %q", got)
	}
	if _, err := systemdInstallArgumentV1("line\nbreak"); err == nil {
		t.Fatal("expected control-character rejection")
	}
}
