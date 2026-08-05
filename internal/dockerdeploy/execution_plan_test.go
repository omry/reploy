package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestPlanDockerExecutionBaseIdentity(t *testing.T) {
	stagingDir := t.TempDir()
	document := blueprint.Document{
		Environment: blueprint.Environment{ID: "demo", Mounts: map[string]blueprint.EnvironmentMount{}},
		Docker:      blueprint.Docker{Image: "python:3.13", Mounts: map[string]blueprint.DockerMount{}},
	}
	plan, err := PlanDockerExecution(document, DockerPlanContext{
		DeploymentDir: stagingDir, Phase: blueprint.PhaseStaged,
		GeneratedImage: "reploy/demo:staging", Host: blueprint.HostMacOS, UID: 501, GID: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.EnvironmentID != "demo" || plan.Image != "reploy/demo:staging" || plan.Phase != blueprint.PhaseStaged {
		t.Fatalf("plan = %#v", plan)
	}
	if plan.Scope != nil || plan.Sandbox.RuntimeUser.UID != 501 {
		t.Fatalf("scope/user = %#v / %#v", plan.Scope, plan.Sandbox.RuntimeUser)
	}
	stagingHash, err := pathIdentityHash(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	if want := "demo-staging-" + stagingHash; plan.ContainerName != want || plan.NetworkName != want {
		t.Fatalf("staging names = %q / %q, want %q", plan.ContainerName, plan.NetworkName, want)
	}

	installTarget := t.TempDir()
	scope := blueprint.InstallScopeUser
	plan, err = PlanDockerExecution(document, DockerPlanContext{
		DeploymentDir: stagingDir, InstallTarget: installTarget, Phase: blueprint.PhaseInstalled, Scope: &scope,
		GeneratedImage: "reploy/demo:generation", Host: blueprint.HostMacOS, UID: 501, GID: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	installedHash, err := pathIdentityHash(installTarget)
	if err != nil {
		t.Fatal(err)
	}
	if want := "demo-" + installedHash; plan.ContainerName != want || plan.NetworkName != want {
		t.Fatalf("installed names = %q / %q, want %q", plan.ContainerName, plan.NetworkName, want)
	}
}

func TestPlanDockerExecutionMountModes(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.conf")
	if err := os.WriteFile(external, []byte("value=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	document := blueprint.Document{
		Environment: blueprint.Environment{ID: "demo"},
		Docker: blueprint.Docker{Mounts: map[string]blueprint.DockerMount{
			"config":   {Mode: blueprint.MountManagedBind, Source: "conf", Contract: blueprint.EnvironmentMount{Target: "/config", UpdatePolicy: blueprint.UpdatePreserve}},
			"data":     {Mode: blueprint.MountVolume, Name: "data", Contract: blueprint.EnvironmentMount{Target: "/data", Writable: true, UpdatePolicy: blueprint.UpdateReplace}},
			"external": {Mode: blueprint.MountBind, Source: external, Contract: blueprint.EnvironmentMount{Target: "/external", UpdatePolicy: blueprint.UpdateUnmanaged}},
			"scratch":  {Mode: blueprint.MountTmpfs, Contract: blueprint.EnvironmentMount{Target: "/scratch", Writable: true, UpdatePolicy: blueprint.UpdatePreserve}},
		}},
	}
	plan, err := PlanDockerExecution(document, DockerPlanContext{DeploymentDir: root, Phase: blueprint.PhaseStaged, GeneratedImage: "image", UID: 501, GID: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Mounts) != 4 || plan.Mounts[0].Source != filepath.Join(root, "conf") || plan.Mounts[0].ReadOnly != true {
		t.Fatalf("mounts = %#v", plan.Mounts)
	}
	if plan.Mounts[1].Mode != blueprint.MountVolume || plan.Mounts[1].Source == "data" {
		t.Fatalf("volume was not directory-scoped: %#v", plan.Mounts[1])
	}
	if plan.Mounts[0].SourceKind != deploy.RuntimeMountSourceDirectory || plan.Mounts[1].SourceKind != deploy.RuntimeMountSourceGenerated || plan.Mounts[2].SourceKind != deploy.RuntimeMountSourceFile || plan.Mounts[3].SourceKind != deploy.RuntimeMountSourceGenerated {
		t.Fatalf("mount source kinds = %#v", plan.Mounts)
	}
}

func TestPlanDockerExecutionRejectsSystemStaging(t *testing.T) {
	scope := blueprint.InstallScopeSystem
	_, err := PlanDockerExecution(blueprint.Document{Environment: blueprint.Environment{ID: "demo"}}, DockerPlanContext{
		DeploymentDir: t.TempDir(), Phase: blueprint.PhaseStaged, Scope: &scope, GeneratedImage: "image",
	})
	if err == nil {
		t.Fatal("expected staged scope rejection")
	}
}

func TestPlanDockerExecutionPhasePortsAndRetainedOverrides(t *testing.T) {
	document := blueprint.Document{
		Environment: blueprint.Environment{ID: "demo", Workload: &blueprint.Workload{Command: "server"}},
		Docker: blueprint.Docker{Workload: &blueprint.DockerWorkload{Endpoints: map[string]blueprint.DockerEndpoint{
			"http": {Endpoint: blueprint.Endpoint{Scheme: "http", Port: 8080}, Bind: blueprint.Bind{Address: "0.0.0.0"}, Publish: blueprint.Publication{Address: "127.0.0.1", Staging: 18080, Deployed: 8080}},
		}}},
	}
	staged, err := PlanDockerExecution(document, DockerPlanContext{DeploymentDir: t.TempDir(), Phase: blueprint.PhaseStaged, GeneratedImage: "image", UID: 1, GID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if staged.Workload.Endpoints["http"].PublishedPort != 18080 {
		t.Fatalf("staging endpoint = %#v", staged.Workload.Endpoints["http"])
	}
	scope := blueprint.InstallScopeUser
	installed, err := PlanDockerExecution(document, DockerPlanContext{
		DeploymentDir: t.TempDir(), InstallTarget: t.TempDir(), Phase: blueprint.PhaseInstalled, Scope: &scope,
		GeneratedImage: "image", UID: 1, GID: 1, PortOverrideArgs: []PortOverride{{HostPort: "9090"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if installed.Workload.Endpoints["http"].PublishedPort != 9090 || installed.Workload.Endpoints["http"].ContainerPort != 8080 {
		t.Fatalf("installed endpoint = %#v", installed.Workload.Endpoints["http"])
	}
}

func TestNormalizeProbeHostUsesLoopbackForWildcards(t *testing.T) {
	tests := map[string]string{"0.0.0.0": "127.0.0.1", "*": "127.0.0.1", "::": "::1", "[::]": "::1", "127.0.0.2": "127.0.0.2"}
	for input, want := range tests {
		if got := normalizeProbeHost(input); got != want {
			t.Fatalf("normalizeProbeHost(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestPlanRuntimeUserScopePolicy(t *testing.T) {
	scope := blueprint.InstallScopeUser
	document := blueprint.Document{Environment: blueprint.Environment{
		Runtime: blueprint.EnvironmentRuntime{User: "omegaflow"},
		Install: blueprint.Install{System: blueprint.SystemInstall{Account: blueprint.SystemAccount{User: "service", Group: "service"}}},
	}}
	plan, err := planRuntimeUser(document, DockerPlanContext{Phase: blueprint.PhaseInstalled, Scope: &scope, Host: blueprint.HostMacOS, UID: 501, GID: 20})
	if err != nil {
		t.Fatal(err)
	}
	if plan.DockerUser != "501:20" || plan.LocalUser != "omegaflow" || len(plan.Warnings) != 3 {
		t.Fatalf("user plan = %#v", plan)
	}
	if !strings.Contains(plan.Warnings[0], `local account "omegaflow"`) || !strings.Contains(plan.Warnings[0], "501:20") {
		t.Fatalf("user identity warning = %q", plan.Warnings[0])
	}
	scope = blueprint.InstallScopeSystem
	plan, err = planRuntimeUser(document, DockerPlanContext{
		Phase: blueprint.PhaseInstalled, Scope: &scope, Host: blueprint.HostLinux,
		SystemUser: "service", SystemGroup: "service", UID: 991, GID: 991,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.DockerUser != "991:991" || plan.LocalUser != "omegaflow" || len(plan.Warnings) != 0 {
		t.Fatalf("system plan = %#v", plan)
	}
	root, err := planRuntimeUser(document, DockerPlanContext{
		Phase: blueprint.PhaseInstalled, Scope: &scope, Host: blueprint.HostLinux,
		SystemUser: "root", SystemGroup: "root", UID: 0, GID: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if root.LocalUser != "root" || root.DockerUser != "0:0" {
		t.Fatalf("root plan = %#v", root)
	}
	if len(root.Warnings) != 0 {
		t.Fatalf("root warnings = %#v", root.Warnings)
	}
	scope = blueprint.InstallScopeUser
	root, err = planRuntimeUser(document, DockerPlanContext{
		Phase: blueprint.PhaseInstalled, Scope: &scope, Host: blueprint.HostLinux, UID: 0, GID: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Warnings) != 2 || strings.Contains(strings.Join(root.Warnings, "\n"), "non-root identity") ||
		strings.Contains(strings.Join(root.Warnings, "\n"), "run as root inside its container") {
		t.Fatalf("root current-user warnings = %#v", root.Warnings)
	}
}

func TestDockerPlanCrossPlatformUserPaths(t *testing.T) {
	tests := []struct {
		host blueprint.HostOS
		root string
		want string
	}{
		{host: blueprint.HostLinux, root: "/home/demo/stage", want: "/home/demo/stage/conf"},
		{host: blueprint.HostMacOS, root: "/Users/demo/stage", want: "/Users/demo/stage/conf"},
		{host: blueprint.HostWindows, root: `C:\Users\demo\stage`, want: `C:\Users\demo\stage\conf`},
	}
	for _, tt := range tests {
		document := blueprint.Document{Environment: blueprint.Environment{ID: "demo"}, Docker: blueprint.Docker{Mounts: map[string]blueprint.DockerMount{
			"config": {Mode: blueprint.MountManagedBind, Source: "conf", Contract: blueprint.EnvironmentMount{Target: "/config", UpdatePolicy: blueprint.UpdatePreserve}},
		}}}
		plan, err := PlanDockerExecution(document, DockerPlanContext{DeploymentDir: tt.root, Phase: blueprint.PhaseStaged, Host: tt.host, GeneratedImage: "image", UID: 1000, GID: 1000})
		if err != nil {
			t.Fatal(err)
		}
		if plan.Mounts[0].Source != tt.want {
			t.Fatalf("%s source = %q, want %q", tt.host, plan.Mounts[0].Source, tt.want)
		}
	}
}
