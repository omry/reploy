package dockerdeploy

import (
	"path/filepath"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func TestPlanDockerExecutionBaseIdentity(t *testing.T) {
	document := blueprint.Document{
		Environment: blueprint.Environment{ID: "demo", Paths: map[string]blueprint.Path{}},
		Docker:      blueprint.Docker{Image: "python:3.13", Mounts: map[string]blueprint.DockerMount{}},
	}
	plan, err := PlanDockerExecution(document, DockerPlanContext{
		DeploymentDir: t.TempDir(), Phase: blueprint.PhaseStaged,
		GeneratedImage: "reploy/demo:staging", Host: blueprint.HostMacOS, UID: 501, GID: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.EnvironmentID != "demo" || plan.Image != "reploy/demo:staging" || plan.Phase != blueprint.PhaseStaged {
		t.Fatalf("plan = %#v", plan)
	}
	if plan.Scope != nil || plan.RuntimeUser.UID != 501 {
		t.Fatalf("scope/user = %#v / %#v", plan.Scope, plan.RuntimeUser)
	}
}

func TestPlanDockerExecutionMountModes(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	document := blueprint.Document{
		Environment: blueprint.Environment{ID: "demo"},
		Docker: blueprint.Docker{Mounts: map[string]blueprint.DockerMount{
			"config":   {Mode: blueprint.MountManagedBind, Source: "conf", Path: blueprint.Path{Container: "/config", Update: blueprint.UpdatePreserve}},
			"data":     {Mode: blueprint.MountVolume, Name: "data", Path: blueprint.Path{Container: "/data", Writable: true, Update: blueprint.UpdateReplace}},
			"external": {Mode: blueprint.MountBind, Source: external, Path: blueprint.Path{Container: "/external", Update: blueprint.UpdateUnmanaged}},
			"scratch":  {Mode: blueprint.MountTmpfs, Path: blueprint.Path{Container: "/scratch", Writable: true, Update: blueprint.UpdatePreserve}},
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
	document := blueprint.Document{Environment: blueprint.Environment{Install: blueprint.Install{System: blueprint.SystemInstall{RunAs: blueprint.RunAs{User: "service", Group: "service"}}}}}
	plan, err := planRuntimeUser(document, DockerPlanContext{Phase: blueprint.PhaseInstalled, Scope: &scope, Host: blueprint.HostMacOS, UID: 501, GID: 20})
	if err != nil {
		t.Fatal(err)
	}
	if plan.DockerUser != "501:20" || len(plan.Warnings) != 3 {
		t.Fatalf("user plan = %#v", plan)
	}
	scope = blueprint.InstallScopeSystem
	plan, err = planRuntimeUser(document, DockerPlanContext{
		Phase: blueprint.PhaseInstalled, Scope: &scope, Host: blueprint.HostLinux,
		SystemUser: "service", SystemGroup: "service", UID: 991, GID: 991,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.DockerUser != "991:991" || len(plan.Warnings) != 0 {
		t.Fatalf("system plan = %#v", plan)
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
			"config": {Mode: blueprint.MountManagedBind, Source: "conf", Path: blueprint.Path{Container: "/config", Update: blueprint.UpdatePreserve}},
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
