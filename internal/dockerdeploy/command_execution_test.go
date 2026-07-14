package dockerdeploy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
)

func commandTestDocument() blueprint.Document {
	return blueprint.Document{Environment: blueprint.Environment{
		Components: map[string]blueprint.Component{"application": {Type: blueprint.ComponentTypePython}},
		Executables: map[string]blueprint.Executable{"server": {
			Component: "application", Binary: "demo", ArgvPrefix: []string{"--prefix"}, ArgvSuffix: []string{"--suffix"},
		}},
		Commands: map[string]blueprint.Command{
			"serve":   {Executable: "server", Trigger: []string{"serve"}, NativeCommand: true, DeployedCommand: true, ForwardFlags: []string{"--verbose"}, Argv: []string{"serve"}, Order: blueprint.DefaultArgumentOrder},
			"special": {Executable: "server", Trigger: []string{"config", "show"}, NativeCommand: true, Argv: []string{"show"}, Order: []blueprint.ArgumentSegment{blueprint.ArgumentBinary, blueprint.ArgumentCommand, blueprint.ArgumentSuffix, blueprint.ArgumentForwarded}},
		},
	}}
}

func TestResolveEnvironmentCommandSegmentOrder(t *testing.T) {
	resolved, err := ResolveEnvironmentCommand(commandTestDocument(), map[string]providers.ExecutableOutput{
		"server": {Component: "application", Binary: "demo", ImagePath: "/opt/demo"},
	}, "special", []string{"value"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resolved.Argv, []string{"/opt/demo", "show", "--suffix", "value"}) {
		t.Fatalf("argv = %#v", resolved.Argv)
	}
}

func TestResolveEnvironmentCommandForPlanInterpolatesLateRuntimeValues(t *testing.T) {
	document := commandTestDocument()
	document.Environment.Vars = map[string]any{"config_name": "demo-server"}
	document.Environment.Paths = map[string]blueprint.Path{"data": {Container: "/data", Writable: true, Update: blueprint.UpdatePreserve}}
	document.Environment.Workload = &blueprint.Workload{Endpoints: map[string]blueprint.Endpoint{"http": {Scheme: "http", Port: 8080}}}
	document.Environment.Executables["server"] = blueprint.Executable{
		Component: "application", Binary: "demo", Order: blueprint.DefaultArgumentOrder,
		ArgvPrefix: []string{"--config", "{{ config_name }}"},
		ArgvSuffix: []string{
			"bind={{ reploy.workload.endpoints.http.bind.address }}:{{ reploy.workload.endpoints.http.bind.port }}",
			"publish={{ reploy.workload.endpoints.http.publish.address }}:{{ reploy.workload.endpoints.http.publish.port }}",
			"data={{ environment.paths.data.container }}", "phase={{ reploy.phase }}",
		},
	}
	plan := DockerExecutionPlan{Phase: blueprint.PhaseStaged, Workload: &WorkloadExecutionPlan{Endpoints: map[string]EndpointExecutionPlan{
		"http": {BindAddress: "0.0.0.0", ContainerPort: 8080, PublishAddress: "127.0.0.1", PublishedPort: 18080},
	}}}
	resolved, err := ResolveEnvironmentCommandForPlan(document, map[string]providers.ExecutableOutput{
		"server": {Component: "application", Binary: "demo", ImagePath: "/opt/demo"},
	}, plan, "serve", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/demo", "--config", "demo-server", "serve", "bind=0.0.0.0:8080", "publish=127.0.0.1:18080", "data=/data", "phase=staged"}
	if !reflect.DeepEqual(resolved.Argv, want) {
		t.Fatalf("argv = %#v, want %#v", resolved.Argv, want)
	}
}

func TestMatchEnvironmentCommandLongestTriggerAndForwarding(t *testing.T) {
	name, forwarded, err := MatchEnvironmentCommand(commandTestDocument(), []string{"config", "show", "--", "$(not-shell)"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if name != "special" || !reflect.DeepEqual(forwarded, []string{"$(not-shell)"}) {
		t.Fatalf("match = %q %#v", name, forwarded)
	}
	if _, _, err := MatchEnvironmentCommand(commandTestDocument(), []string{"serve", "--bad"}, false); err == nil {
		t.Fatal("expected unknown forwarded flag rejection")
	}
}

func TestTransientAndShellCommandsUseDockerExecArgv(t *testing.T) {
	plan := DockerExecutionPlan{Image: "reploy/demo:staging", ContainerName: "demo", RuntimeUser: RuntimeUserPlan{DockerUser: "501:20"}, Mounts: []MountExecutionPlan{{Mode: blueprint.MountManagedBind, Source: "/tmp/conf", Target: "/conf", ReadOnly: true}}}
	spec, err := TransientCommandSpec(plan, ResolvedEnvironmentCommand{Argv: []string{"/opt/demo", ";rm", "$(touch pwned)"}}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(spec.Args, "|")
	if !strings.Contains(joined, "/opt/demo|;rm|$(touch pwned)") || strings.Contains(joined, "sh|-c") {
		t.Fatalf("spec = %#v", spec)
	}
	shell := ShellCommandSpec(plan, true, true)
	if !strings.Contains(strings.Join(shell.Args, " "), "--interactive --tty") || shell.Args[len(shell.Args)-1] != "/bin/sh" {
		t.Fatalf("shell = %#v", shell)
	}
	if !containsInOrder(shell.Args, []string{
		"--read-only", "--tmpfs", temporaryHomeMountForPlan(plan),
		"--env", "HOME=" + environmentTemporaryHome,
		"--env", "TMPDIR=" + environmentTemporaryHome,
	}) {
		t.Fatalf("shell lacks a read-only root and temporary home: %#v", shell.Args)
	}
}
