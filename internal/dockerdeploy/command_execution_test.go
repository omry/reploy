package dockerdeploy

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
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
	mountDir := t.TempDir()
	outputDir := t.TempDir()
	plan := DockerExecutionPlan{DeploymentDir: t.TempDir(), Image: "reploy/demo:staging", ContainerName: "demo", Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 501, GID: 20, SupplementaryGIDs: []uint32{33, 44}, DockerUser: "501:20"}), Mounts: []MountExecutionPlan{{Mode: blueprint.MountManagedBind, Source: mountDir, Target: "/conf", ReadOnly: true}}}
	output := &transientOutputMount{HostDirectory: outputDir, Variable: runtimeOutputFileVariable, ContainerPath: runtimeOutputRoot + "/output"}
	spec, err := TransientCommandSpec(plan, ResolvedEnvironmentCommand{Argv: []string{"/opt/demo", ";rm", "$(touch pwned)"}}, output, true, false)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(spec.Args, "|")
	if strings.Contains(joined, "sh|-c") || !containsInOrder(spec.Args, []string{"--entrypoint", plan.Sandbox.StartupVerifier.Path, plan.Image, "sandbox-exec", "--uid", "501", "--gid", "20", "--groups", "33,44", "--public", "deny", "--local", "deny", "--ambiguous", "require-both", "--", "/opt/demo", ";rm", "$(touch pwned)"}) {
		t.Fatalf("spec = %#v", spec)
	}
	if !containsInOrder(spec.Args, []string{"--mount", "type=bind,source=" + outputDir + ",target=" + runtimeOutputRoot, "--env", runtimeOutputFileVariable + "=" + runtimeOutputRoot + "/output"}) {
		t.Fatalf("spec lacks explicit output mount: %#v", spec.Args)
	}
	if !containsAdjacent(spec.Args, "--pull", "never") {
		t.Fatalf("transient command permits image pulls: %#v", spec.Args)
	}
	if !containsInOrder(spec.Args, []string{"--user", "0:0", "--cap-drop", "ALL"}) ||
		!containsInOrder(spec.Args, []string{"--cap-add", "NET_ADMIN", "--cap-add", "SETGID", "--cap-add", "SETPCAP", "--cap-add", "SETUID"}) ||
		!containsInOrder(spec.Args, []string{"--entrypoint", plan.Sandbox.StartupVerifier.Path, plan.Image, "sandbox-exec", "--uid", "501", "--gid", "20", "--groups", "33,44", "--public", "deny", "--local", "deny", "--ambiguous", "require-both", "--", "/opt/demo", ";rm", "$(touch pwned)"}) {
		t.Fatalf("transient command does not start directly with its final identity and command: %#v", spec.Args)
	}
	shell := ShellCommandSpec(plan, true, true)
	if !strings.Contains(strings.Join(shell.Args, " "), "--interactive --tty") || !containsInOrder(shell.Args, []string{"--entrypoint", plan.Sandbox.StartupVerifier.Path, plan.Image, "sandbox-exec", "--uid", "501", "--gid", "20", "--groups", "33,44", "--public", "deny", "--local", "deny", "--ambiguous", "require-both", "--", "/bin/sh"}) {
		t.Fatalf("shell = %#v", shell)
	}
	if !containsInOrder(shell.Args, []string{"--read-only", "--tmpfs", transientHomeMountForPlan(plan)}) ||
		!containsInOrder(shell.Args, []string{
			"--env", "HOME=" + environmentTemporaryHome,
			"--env", "TMPDIR=" + environmentTemporaryHome,
		}) {
		t.Fatalf("shell lacks a read-only root and private temporary home: %#v", shell.Args)
	}
}

func TestTransientCommandSpecQuotesCommaContainingMountFields(t *testing.T) {
	hostRoot := t.TempDir()
	mountDir := filepath.Join(hostRoot, "deployment,preview", "conf")
	outputDir := filepath.Join(hostRoot, "output,preview")
	plan := DockerExecutionPlan{
		DeploymentDir: t.TempDir(), Image: "reploy/demo:staging", ContainerName: "demo",
		Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 501, GID: 20, DockerUser: "501:20"}),
		Mounts: []MountExecutionPlan{{
			Mode: blueprint.MountManagedBind, Source: mountDir,
			Target: "/conf,preview", ReadOnly: true,
		}},
	}
	output := &transientOutputMount{
		HostDirectory: outputDir, Variable: runtimeOutputDirectoryVariable,
		ContainerPath: runtimeOutputRoot,
	}
	spec, err := TransientCommandSpec(plan, ResolvedEnvironmentCommand{Argv: []string{"/opt/demo"}}, output, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !containsInOrder(spec.Args, []string{
		"--mount", `type=bind,"target=/conf,preview","source=` + mountDir + `",readonly`,
		"--mount", `type=bind,"source=` + outputDir + `",target=` + runtimeOutputRoot,
	}) {
		t.Fatalf("comma-containing mount fields were not CSV-quoted: %#v", spec.Args)
	}
}

func TestTransientCommandSpecMasksDeploymentPrivatePaths(t *testing.T) {
	deploymentDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(deploymentDir, privateRuntimeMetadataDirectoryName), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := preparePrivateWorkloadEnvironmentV1(deploymentDir); err != nil {
		t.Fatal(err)
	}
	plan := DockerExecutionPlan{
		DeploymentDir: deploymentDir, Image: "reploy/demo:staging", ContainerName: "demo",
		Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 501, GID: 20, DockerUser: "501:20"}),
		Mounts: []MountExecutionPlan{{
			Name: "deployment", Mode: blueprint.MountBind, Source: deploymentDir,
			SourceKind: deploy.RuntimeMountSourceDirectory, Target: "/deployment",
		}},
	}
	spec, err := TransientCommandSpec(
		plan,
		ResolvedEnvironmentCommand{Argv: []string{"/bin/true"}},
		nil,
		false,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !containsInOrder(spec.Args, []string{
		"--tmpfs", "/deployment/.reploy:" + privateRuntimeDirectoryMaskOptionsV1,
	}) || !containsInOrder(spec.Args, []string{
		"--mount", "type=bind,source=/dev/null,target=/deployment/.env,readonly",
	}) {
		t.Fatalf("transient private runtime masks = %#v", spec.Args)
	}
}

func TestPlanTransientContainerExecutionV1SeparatesCreateStartAndCleanup(t *testing.T) {
	plan := DockerExecutionPlan{
		DeploymentDir: t.TempDir(), Image: "reploy/demo:staging", ContainerName: "demo-staging-abcd",
		Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 501, GID: 20, DockerUser: "501:20"}),
		Mounts:  []MountExecutionPlan{{Mode: blueprint.MountManagedBind, Source: t.TempDir(), Target: "/conf", ReadOnly: true}},
	}
	output := &transientOutputMount{
		HostDirectory: t.TempDir(), Variable: runtimeOutputFileVariable,
		ContainerPath: runtimeOutputRoot + "/output",
	}
	execution, err := PlanTransientContainerExecutionV1(
		plan,
		ResolvedEnvironmentCommand{Argv: []string{"/opt/demo", "export"}},
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
		!containsInOrder(execution.Create.Args, []string{"--entrypoint", plan.Sandbox.StartupVerifier.Path, plan.Image, "sandbox-exec", "--uid", "501", "--gid", "20", "--public", "deny", "--local", "deny", "--ambiguous", "require-both", "--", "/opt/demo", "export"}) {
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
	command := ResolvedEnvironmentCommand{Argv: []string{"/bin/true"}}
	if _, err := PlanTransientContainerExecutionV1(DockerExecutionPlan{ContainerName: "demo"}, command, nil, "invalid", false, false); err == nil || !strings.Contains(err.Error(), "run ID") {
		t.Fatalf("invalid run ID error = %v", err)
	}
	if _, err := PlanTransientContainerExecutionV1(DockerExecutionPlan{}, command, nil, "run-0000000000000001", false, false); err == nil || !strings.Contains(err.Error(), "base container name") {
		t.Fatalf("missing base name error = %v", err)
	}
}
