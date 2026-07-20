package dockerdeploy

import (
	"errors"
	"reflect"
	"testing"
)

func TestInspectProviderInstallHostToolsV1FindsSystemdDockerDependency(t *testing.T) {
	var commands []CommandSpec
	tools, err := inspectProviderInstallHostToolsWithV1(t.Context(), installBackendLinuxSystemd, providerInstallHostToolBackendV1{
		lookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		run: func(spec CommandSpec, options RunOptions) error {
			if options.Context != t.Context() {
				t.Fatal("systemctl inspection lost context")
			}
			commands = append(commands, spec)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := providerInstallHostToolsV1{DockerPath: "/usr/bin/docker", SystemctlPath: "/usr/bin/systemctl", IncludeDockerUnit: true}
	if tools != want {
		t.Fatalf("tools = %#v, want %#v", tools, want)
	}
	wantCommands := []CommandSpec{{Name: "/usr/bin/systemctl", Args: []string{"cat", "docker.service"}}}
	if !reflect.DeepEqual(commands, wantCommands) {
		t.Fatalf("commands = %#v, want %#v", commands, wantCommands)
	}
}

func TestInspectProviderInstallHostToolsV1TreatsMissingDockerUnitAsOptional(t *testing.T) {
	tools, err := inspectProviderInstallHostToolsWithV1(t.Context(), installBackendLinuxSystemd, providerInstallHostToolBackendV1{
		lookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		run:      func(CommandSpec, RunOptions) error { return errors.New("unit not found") },
	})
	if err != nil {
		t.Fatal(err)
	}
	if tools.IncludeDockerUnit {
		t.Fatal("missing docker.service was included")
	}
}

func TestInspectProviderInstallHostToolsV1DoesNotRunDockerProbe(t *testing.T) {
	run := false
	tools, err := inspectProviderInstallHostToolsWithV1(t.Context(), installBackendDockerManaged, providerInstallHostToolBackendV1{
		lookPath: func(string) (string, error) { return "/usr/bin/docker", nil },
		run: func(CommandSpec, RunOptions) error {
			run = true
			return nil
		},
	})
	if err != nil || tools.DockerPath != "/usr/bin/docker" || run {
		t.Fatalf("tools=%#v run=%v error=%v", tools, run, err)
	}
}
