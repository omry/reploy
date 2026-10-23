package dockerdeploy

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProviderInstallServiceUnitRetainsBuildEngineAcrossAmbientContextDrift(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("systemd installation rendering is Linux-specific")
	}
	tests := []struct {
		name              string
		endpoint          string
		engine            string
		includeDockerUnit bool
		wantDockerUnit    bool
	}{
		{name: "default Docker at var-run", endpoint: "unix:///var/run/docker.sock", engine: "Docker", includeDockerUnit: true, wantDockerUnit: true},
		{name: "default Docker at run", endpoint: "unix:///run/docker.sock", engine: "Docker", includeDockerUnit: true, wantDockerUnit: true},
		{name: "default Docker without host unit", endpoint: "unix:///run/docker.sock", engine: "Docker", wantDockerUnit: false},
		{name: "custom Docker with host unit", endpoint: "unix:///run/selected-engine.sock", engine: "Docker", includeDockerUnit: true},
		{name: "custom Docker without host unit", endpoint: "unix:///run/selected-engine.sock", engine: "Docker"},
		{name: "custom Podman with host unit", endpoint: "unix:///run/selected-engine.sock", engine: "Podman", includeDockerUnit: true},
		{name: "custom Podman without host unit", endpoint: "unix:///run/selected-engine.sock", engine: "Podman"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			locked := providerInstallPrepareDestinationFixture(t)
			locked.Plan.Backend = installBackendLinuxSystemd
			locked.Plan.Installation.Scope = "system"
			locked.Plan.Installation.UnitPath = filepath.Join(t.TempDir(), "demo.service")
			locked.Plan.ServiceEngineEndpoint = "" // The production backend must supply the selection.
			locked.HostTools = providerInstallHostToolsV1{DockerPath: "/usr/bin/docker", IncludeDockerUnit: test.includeDockerUnit}
			selected := &generatedImageBuilder{endpoint: test.endpoint, engine: test.engine}
			ctx, err := bindMaterializationBuilderContext(t.Context(), selected)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("DOCKER_HOST", "unix:///run/other-engine.sock")
			t.Setenv("DOCKER_CONTEXT", "unrelated-context")
			prepared, err := newProviderInstallRunBackendV1().prepareDestination(ctx, locked)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = prepared.Cleanup() })
			var unit string
			for _, file := range prepared.Files {
				if file.FinalPath == locked.Plan.Installation.UnitPath {
					content, err := os.ReadFile(file.TemporaryPath)
					if err != nil {
						t.Fatal(err)
					}
					unit = string(content)
				}
			}
			if !strings.Contains(unit, `Environment="DOCKER_HOST=`+test.endpoint+`"`) ||
				!strings.Contains(unit, `Environment="DOCKER_CONTEXT="`) || strings.Contains(unit, "other-engine") {
				t.Fatalf("service lost its selected engine:\n%s", unit)
			}
			if strings.Contains(unit, "Requires=docker.service") != test.wantDockerUnit {
				t.Fatalf("service requires the wrong engine:\n%s", unit)
			}
			if !strings.Contains(unit, "ExecStart=") || !strings.Contains(unit, "ExecStop=") {
				t.Fatal("start and stop must share the unit's pinned environment")
			}
		})
	}
}

func TestProviderInstallServiceEngineSelectionFailureCreatesNoCandidates(t *testing.T) {
	locked := providerInstallPrepareDestinationFixture(t)
	locked.Plan.Backend = installBackendLinuxSystemd
	old := probeGeneratedImageBuilder
	probeGeneratedImageBuilder = func(context.Context, RunOptions) (*generatedImageBuilder, error) {
		return nil, os.ErrPermission
	}
	t.Cleanup(func() { probeGeneratedImageBuilder = old })
	_, err := newProviderInstallRunBackendV1().prepareDestination(withGeneratedBuilderSession(t.Context()), locked)
	if err == nil || !strings.Contains(err.Error(), "select installed service engine") {
		t.Fatalf("selection error = %v", err)
	}
	entries, err := os.ReadDir(locked.Input.DestinationDeploymentDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".reploy-install-") {
			t.Fatal("engine selection failure prepared destination files")
		}
	}
}

func TestSystemdServiceEngineEnvironmentEscapesSpecifiersWithoutExpandingDollars(t *testing.T) {
	got, err := systemdInstallEnvironmentV1(`DOCKER_HOST=unix:///run/%n/$X/a "b"\c`)
	if err != nil || got != `"DOCKER_HOST=unix:///run/%%n/$X/a \"b\"\\c"` {
		t.Fatalf("environment = %q error=%v", got, err)
	}
	if _, err := systemdInstallEnvironmentV1("DOCKER_HOST=unix:///run/a\nExecStart=other"); err == nil {
		t.Fatal("service environment accepted a newline")
	}
	plan := providerInstallRunPlanFixture(t.TempDir(), fixedPublicationReferences(t, t.TempDir(), 0xb8))
	for _, endpoint := range []string{"", "tcp://remote:2375", "ssh://remote"} {
		plan.ServiceEngineEndpoint = endpoint
		if _, err := providerInstallSystemdFileV1(plan, "/usr/bin/docker", false); err == nil {
			t.Fatalf("service accepted unverified endpoint %q", endpoint)
		}
	}
}
