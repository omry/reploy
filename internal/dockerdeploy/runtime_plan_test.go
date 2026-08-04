package dockerdeploy

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

func TestRuntimePlansV1CoversWorkloadShellCommandsAndOutputVariant(t *testing.T) {
	document := runtimePlanDocument()
	plan := DockerExecutionPlan{Sandbox: testApplicationSandboxPlanV1(1000, 1000), Workload: &WorkloadExecutionPlan{}, Mounts: []MountExecutionPlan{
		{Name: "data", Mode: blueprint.MountVolume, Target: "/mnt/data", ReadOnly: false},
		{Name: "config", Mode: blueprint.MountManagedBind, Source: filepath.Join(t.TempDir(), "config"), Target: "/mnt/config", ReadOnly: true},
	}}
	plans, err := RuntimePlansV1(document, plan)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"command/check", "command/check/output", "command/prepare", "shell", "workload"}
	gotIDs := make([]string, 0, len(plans))
	for _, item := range plans {
		gotIDs = append(gotIDs, item.ID)
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("runtime plan IDs = %#v, want %#v", gotIDs, wantIDs)
	}
	check := runtimePlanByID(t, plans, "command/check")
	if !reflect.DeepEqual(check.Executables, []providers.QualifiedOutput{{Component: "application/application/python", Name: "demo"}}) {
		t.Fatalf("command executables = %#v", check.Executables)
	}
	if !reflect.DeepEqual(check.Mounts, []deploy.RuntimeMountV1{
		{Destination: "/mnt/config", SourceKind: deploy.RuntimeMountSourceDirectory, ReadOnly: true},
		{Destination: "/mnt/data", SourceKind: deploy.RuntimeMountSourceGenerated},
		{Destination: environmentTemporaryHome, SourceKind: deploy.RuntimeMountSourceGenerated},
	}) {
		t.Fatalf("command mounts = %#v", check.Mounts)
	}
	output := runtimePlanByID(t, plans, "command/check/output")
	if output.Mounts[len(output.Mounts)-1] != (deploy.RuntimeMountV1{Destination: runtimeOutputRoot, SourceKind: deploy.RuntimeMountSourceDirectory}) {
		t.Fatalf("output mounts = %#v", output.Mounts)
	}
	shell := runtimePlanByID(t, plans, "shell")
	if len(shell.Executables) != 0 || len(shell.Mounts) != 3 {
		t.Fatalf("shell plan = %#v", shell)
	}
	workload := runtimePlanByID(t, plans, "workload")
	if !reflect.DeepEqual(workload.Executables, []providers.QualifiedOutput{{Component: "application/application/python", Name: "demo"}}) {
		t.Fatalf("workload executables = %#v", workload.Executables)
	}
}

func TestRuntimePlansV1DetectsExternalBindSourceKind(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	plans, err := RuntimePlansV1(runtimePlanDocument(), DockerExecutionPlan{Sandbox: testApplicationSandboxPlanV1(1000, 1000), Workload: &WorkloadExecutionPlan{}, Mounts: []MountExecutionPlan{{
		Name: "config", Mode: blueprint.MountBind, Source: file, SourceKind: deploy.RuntimeMountSourceFile,
		Target: "/mnt/config", ReadOnly: true,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	shell := runtimePlanByID(t, plans, "shell")
	if shell.Mounts[0].SourceKind != deploy.RuntimeMountSourceFile {
		t.Fatalf("bind source kind = %q", shell.Mounts[0].SourceKind)
	}

	_, err = RuntimePlansV1(runtimePlanDocument(), DockerExecutionPlan{Sandbox: testApplicationSandboxPlanV1(1000, 1000), Workload: &WorkloadExecutionPlan{}, Mounts: []MountExecutionPlan{{
		Name: "missing-kind", Mode: blueprint.MountBind, Source: filepath.Join(t.TempDir(), "source"), Target: "/mnt/missing",
	}}})
	if err == nil || !strings.Contains(err.Error(), "source kind") {
		t.Fatalf("missing bind source-kind error = %v", err)
	}
}

func TestRuntimePlansV1RejectsUnknownCommandExecutable(t *testing.T) {
	document := runtimePlanDocument()
	command := document.Environment.Commands["check"]
	command.Executable = "application.missing"
	document.Environment.Commands["check"] = command
	_, err := RuntimePlansV1(document, DockerExecutionPlan{Sandbox: testApplicationSandboxPlanV1(1000, 1000), Workload: &WorkloadExecutionPlan{}})
	if err == nil || !strings.Contains(err.Error(), "unknown executable") {
		t.Fatalf("unknown executable error = %v", err)
	}
}

func TestRuntimePlansV1RejectsWorkloadPlanMismatch(t *testing.T) {
	_, err := RuntimePlansV1(runtimePlanDocument(), DockerExecutionPlan{})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("workload mismatch error = %v", err)
	}
}

func runtimePlanDocument() blueprint.Document {
	return blueprint.Document{Environment: blueprint.Environment{
		Applications: map[string]blueprint.Application{
			"application": {Executables: map[string]blueprint.Executable{"server": {Source: "python", Binary: "demo"}}},
		},
		Commands: map[string]blueprint.Command{
			"check":   {Executable: "application.server", Trigger: []string{"check"}, NativeCommand: true},
			"prepare": {Executable: "application.server", Trigger: []string{"prepare"}},
			"serve":   {Executable: "application.server"},
			"unused":  {Executable: "application.server"},
		},
		Workload: &blueprint.Workload{
			Command: "serve",
			Runtime: blueprint.RuntimeEvents{AfterStart: []blueprint.Step{{Actions: []blueprint.Action{{Environment: []string{"prepare"}}}}}},
		},
	}}
}

func runtimePlanByID(t *testing.T, plans []deploy.RuntimePlanV1, id string) deploy.RuntimePlanV1 {
	t.Helper()
	for _, plan := range plans {
		if plan.ID == id {
			return plan
		}
	}
	t.Fatalf("runtime plan %q not found in %#v", id, plans)
	return deploy.RuntimePlanV1{}
}
