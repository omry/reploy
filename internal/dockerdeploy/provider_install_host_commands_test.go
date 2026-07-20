package dockerdeploy

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlanProviderInstallHostCommandsV1SystemdConfiguresThenRestarts(t *testing.T) {
	destinationDir := t.TempDir()
	references := fixedPublicationReferences(t, destinationDir, 0xd1)
	plan := providerInstallRunPlanFixture(destinationDir, references)

	commands, err := planProviderInstallHostCommandsV1(plan, "/usr/bin/docker", "/usr/bin/systemctl")
	if err != nil {
		t.Fatal(err)
	}
	wantConfigure := []CommandSpec{
		{Name: "/usr/bin/systemctl", Args: []string{"daemon-reload"}},
		{Name: "/usr/bin/systemctl", Args: []string{"enable", "demo.service"}},
	}
	if !reflect.DeepEqual(commands.Configure, wantConfigure) {
		t.Fatalf("configure commands = %#v, want %#v", commands.Configure, wantConfigure)
	}
	wantStart := CommandSpec{Name: "/usr/bin/systemctl", Args: []string{"restart", "demo.service"}}
	if !reflect.DeepEqual(commands.Start, wantStart) {
		t.Fatalf("start command = %#v, want %#v", commands.Start, wantStart)
	}
}

func TestPlanProviderInstallHostCommandsV1DockerManagedUsesExactInstalledInputs(t *testing.T) {
	destinationDir := t.TempDir()
	references := fixedPublicationReferences(t, destinationDir, 0xd2)
	plan := providerInstallRunPlanFixture(destinationDir, references)
	plan.Backend = installBackendDockerManaged
	plan.Installation.Scope = "user"
	plan.Installation.UnitPath = ""

	commands, err := planProviderInstallHostCommandsV1(plan, "/opt/docker/bin/docker", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(commands.Configure) != 0 {
		t.Fatalf("managed configure commands = %#v", commands.Configure)
	}
	want := CommandSpec{
		Name: "/opt/docker/bin/docker", Dir: destinationDir,
		Args: []string{
			"compose", "--project-name", "demo", "--project-directory", destinationDir,
			"--env-file", filepath.Join(destinationDir, DockerEnvFileName),
			"-f", filepath.Join(destinationDir, ComposeFileName), "up", "-d",
		},
	}
	if !reflect.DeepEqual(commands.Start, want) {
		t.Fatalf("managed start command = %#v, want %#v", commands.Start, want)
	}
}
