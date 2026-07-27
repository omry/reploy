package dockerdeploy

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
)

func commandTestDocument() blueprint.Document {
	return blueprint.Document{Environment: blueprint.Environment{
		Applications: map[string]blueprint.Application{"application": {
			Executables: map[string]blueprint.Executable{"server": {
				Source: "python", Binary: "demo", ArgvPrefix: []string{"--prefix"}, ArgvSuffix: []string{"--suffix"},
			}},
		}},
		Commands: map[string]blueprint.Command{
			"serve":   {Executable: "application.server", Trigger: []string{"serve"}, NativeCommand: true, DeployedCommand: true, ForwardFlags: []string{"--verbose"}, Argv: []string{"serve"}, Order: blueprint.DefaultArgumentOrder},
			"special": {Executable: "application.server", Trigger: []string{"config", "show"}, NativeCommand: true, Argv: []string{"show"}, Order: []blueprint.ArgumentSegment{blueprint.ArgumentBinary, blueprint.ArgumentCommand, blueprint.ArgumentSuffix, blueprint.ArgumentForwarded}},
		},
	}}
}

func TestResolveLockedEnvironmentCommandV1UsesQualifiedCatalogOutput(t *testing.T) {
	resolved, err := resolveLockedEnvironmentCommandV1(commandTestDocument(), []providers.RealizedOutput{
		{
			SupplierComponent: "other", Name: "demo",
			Candidate: providers.ExecutableCandidate{InvocationPath: "/opt/other"},
			Evidence:  providers.ExecutableEvidence{InvocationPath: "/opt/other"},
		},
		{
			SupplierComponent: "application/application/python", Name: "demo",
			Candidate: providers.ExecutableCandidate{InvocationPath: "/opt/demo"},
			Evidence:  providers.ExecutableEvidence{InvocationPath: "/opt/demo"},
		},
	}, "special", []string{"value"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resolved.Argv, []string{"/opt/demo", "show", "--suffix", "value"}) {
		t.Fatalf("argv = %#v", resolved.Argv)
	}
}

func TestResolveLockedEnvironmentCommandV1RejectsMissingOrDriftingOutput(t *testing.T) {
	document := commandTestDocument()
	if _, err := resolveLockedEnvironmentCommandV1(document, []providers.RealizedOutput{}, "serve", nil); err == nil || !strings.Contains(err.Error(), "locked output catalog") {
		t.Fatalf("missing output error = %v", err)
	}
	_, err := resolveLockedEnvironmentCommandV1(document, []providers.RealizedOutput{{
		SupplierComponent: "application/application/python", Name: "demo",
		Candidate: providers.ExecutableCandidate{InvocationPath: "/opt/demo"},
		Evidence:  providers.ExecutableEvidence{InvocationPath: "/opt/other"},
	}}, "serve", nil)
	if err == nil || !strings.Contains(err.Error(), "locked evidence") {
		t.Fatalf("drifting output error = %v", err)
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
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	probeDir := t.TempDir()
	workspace := testPreparedProbeWorkspace(t, platform, probeDir)
	mountDir := t.TempDir()
	outputDir := t.TempDir()
	plan := DockerExecutionPlan{Image: "reploy/demo:staging", ContainerName: "demo", RuntimeUser: RuntimeUserPlan{UID: 501, GID: 20, DockerUser: "501:20"}, Mounts: []MountExecutionPlan{{Mode: blueprint.MountManagedBind, Source: mountDir, Target: "/conf", ReadOnly: true}}}
	output := &transientOutputMount{HostDirectory: outputDir, Variable: runtimeOutputFileVariable, ContainerPath: runtimeOutputRoot + "/output"}
	spec, err := TransientCommandSpec(plan, ResolvedEnvironmentCommand{Argv: []string{"/opt/demo", ";rm", "$(touch pwned)"}}, workspace, output, true, false)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(spec.Args, "|")
	if !strings.Contains(joined, "/opt/demo|;rm|$(touch pwned)") || strings.Contains(joined, "sh|-c") {
		t.Fatalf("spec = %#v", spec)
	}
	if !containsInOrder(spec.Args, []string{"--mount", "type=bind,source=" + outputDir + ",target=" + runtimeOutputRoot, "--env", runtimeOutputFileVariable + "=" + runtimeOutputRoot + "/output"}) {
		t.Fatalf("spec lacks explicit output mount: %#v", spec.Args)
	}
	if !containsAdjacent(spec.Args, "--pull", "never") {
		t.Fatalf("transient command permits image pulls: %#v", spec.Args)
	}
	if containsAdjacent(spec.Args, "--user", plan.RuntimeUser.DockerUser) {
		t.Fatalf("transient container starts as the runtime user before its anonymous home is initialized: %#v", spec.Args)
	}
	if !containsInOrder(spec.Args, []string{"--user", "0:0"}) ||
		!containsInOrder(spec.Args, []string{"--mount", "type=bind,source=" + probeDir + ",target=" + ProbeContainerRoot + ",readonly"}) ||
		!containsInOrder(spec.Args, []string{
			"--entrypoint", ProbeContainerExecutable,
			plan.Image, "run-transient", "501", "20",
			"/opt/demo", ";rm", "$(touch pwned)",
		}) {
		t.Fatalf("transient command does not initialize its anonymous home and drop to the runtime user: %#v", spec.Args)
	}
	shell := ShellCommandSpec(plan, workspace, true, true)
	if !strings.Contains(strings.Join(shell.Args, " "), "--interactive --tty") || shell.Args[len(shell.Args)-1] != "/bin/sh" {
		t.Fatalf("shell = %#v", shell)
	}
	if !containsInOrder(shell.Args, []string{"--read-only", "--mount", transientHomeMountForPlan(plan)}) ||
		!containsInOrder(shell.Args, []string{
			"--env", "HOME=" + environmentTemporaryHome,
			"--env", "TMPDIR=" + environmentTemporaryHome,
		}) {
		t.Fatalf("shell lacks a read-only root and anonymous temporary home: %#v", shell.Args)
	}
	if strings.Contains(strings.Join(shell.Args, " "), "--tmpfs") {
		t.Fatalf("transient home unexpectedly uses tmpfs: %#v", shell.Args)
	}
}

func TestTransientCommandSpecQuotesCommaContainingMountFields(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	hostRoot := t.TempDir()
	probeDir := filepath.Join(hostRoot, "probe,workspace")
	mountDir := filepath.Join(hostRoot, "deployment,preview", "conf")
	outputDir := filepath.Join(hostRoot, "output,preview")
	workspace := testPreparedProbeWorkspace(t, platform, probeDir)
	plan := DockerExecutionPlan{
		Image: "reploy/demo:staging", ContainerName: "demo",
		RuntimeUser: RuntimeUserPlan{UID: 501, GID: 20, DockerUser: "501:20"},
		Mounts: []MountExecutionPlan{{
			Mode: blueprint.MountManagedBind, Source: mountDir,
			Target: "/conf,preview", ReadOnly: true,
		}},
	}
	output := &transientOutputMount{
		HostDirectory: outputDir, Variable: runtimeOutputDirectoryVariable,
		ContainerPath: runtimeOutputRoot,
	}
	spec, err := TransientCommandSpec(plan, ResolvedEnvironmentCommand{Argv: []string{"/opt/demo"}}, workspace, output, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !containsInOrder(spec.Args, []string{
		"--mount", `type=bind,"source=` + probeDir + `",target=` + ProbeContainerRoot + ",readonly",
	}) || !containsInOrder(spec.Args, []string{
		"--mount", `type=bind,"target=/conf,preview","source=` + mountDir + `",readonly`,
		"--mount", `type=bind,"source=` + outputDir + `",target=` + runtimeOutputRoot,
	}) {
		t.Fatalf("comma-containing mount fields were not CSV-quoted: %#v", spec.Args)
	}
}

func TestPlanTransientContainerExecutionV1SeparatesCreateStartAndCleanup(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	workspace := testPreparedProbeWorkspace(t, platform, t.TempDir())
	plan := DockerExecutionPlan{
		Image: "reploy/demo:staging", ContainerName: "demo-staging-abcd",
		RuntimeUser: RuntimeUserPlan{UID: 501, GID: 20, DockerUser: "501:20"},
		Mounts:      []MountExecutionPlan{{Mode: blueprint.MountManagedBind, Source: t.TempDir(), Target: "/conf", ReadOnly: true}},
	}
	output := &transientOutputMount{
		HostDirectory: t.TempDir(), Variable: runtimeOutputFileVariable,
		ContainerPath: runtimeOutputRoot + "/output",
	}
	execution, err := PlanTransientContainerExecutionV1(
		plan,
		ResolvedEnvironmentCommand{Argv: []string{"/opt/demo", "export"}},
		workspace,
		output,
		"run-00010203040506ff",
		true,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantContainer := "demo-staging-abcd-run-00010203040506ff"
	if execution.Container != wantContainer {
		t.Fatalf("container = %q", execution.Container)
	}
	if !reflect.DeepEqual(execution.Create.Args[:7], []string{"create", "--pull", "never", "--rm", "--name", wantContainer, "--user"}) {
		t.Fatalf("create prefix = %#v", execution.Create.Args)
	}
	if !containsInOrder(execution.Create.Args, []string{"--interactive", "--tty"}) ||
		!reflect.DeepEqual(execution.Create.Args[len(execution.Create.Args)-6:], []string{plan.Image, "run-transient", "501", "20", "/opt/demo", "export"}) {
		t.Fatalf("create args = %#v", execution.Create.Args)
	}
	if !reflect.DeepEqual(execution.Start.Args, []string{"start", "--attach", "--interactive", wantContainer}) {
		t.Fatalf("start args = %#v", execution.Start.Args)
	}
	if !reflect.DeepEqual(execution.Cleanup, TemporaryContainerCleanupCommand(wantContainer)) {
		t.Fatalf("cleanup = %#v", execution.Cleanup)
	}
	if strings.Contains(strings.Join(execution.Create.Args, " "), " run ") {
		t.Fatalf("create unexpectedly runs container: %#v", execution.Create.Args)
	}
}

func TestPlanTransientContainerExecutionV1RejectsInvalidIdentity(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	workspace := testPreparedProbeWorkspace(t, platform, t.TempDir())
	command := ResolvedEnvironmentCommand{Argv: []string{"/bin/true"}}
	if _, err := PlanTransientContainerExecutionV1(DockerExecutionPlan{ContainerName: "demo"}, command, workspace, nil, "invalid", false, false); err == nil || !strings.Contains(err.Error(), "run ID") {
		t.Fatalf("invalid run ID error = %v", err)
	}
	if _, err := PlanTransientContainerExecutionV1(DockerExecutionPlan{}, command, workspace, nil, "run-0000000000000001", false, false); err == nil || !strings.Contains(err.Error(), "base container name") {
		t.Fatalf("missing base name error = %v", err)
	}
}
