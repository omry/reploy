package dockerdeploy

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestInstallProviderV1MapsPublicOptions(t *testing.T) {
	runtime := StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 12, GID: 34}
	var progress bytes.Buffer
	var captured ProviderInstallInputV1
	err := installProviderV1(InstallOptions{
		Dir: "/staging", Target: "/installed", ControlMode: ControlAdmissionDrainV1,
		Scope: InstallScopeSystem, Service: "demo", PortOverrides: []PortOverride{{Name: "web", HostPort: "8080"}},
		Replace: []string{"config"}, Clean: true, Start: true, Stdout: io.Discard,
		Progress: &progress, DockerPreflightTimeout: 7 * time.Second,
	}, providerInstallPublicBackendV1{
		runtime: func() (StagedProviderBuildRuntimeV1, error) { return runtime, nil },
		install: func(ctx context.Context, input ProviderInstallInputV1) (deploy.StateV1, error) {
			if ctx == nil {
				t.Fatal("install context is nil")
			}
			captured = input
			return deploy.StateV1{Schema: deploy.StateSchemaV1}, nil
		},
	})
	if err != nil {
		t.Fatalf("install provider: %v", err)
	}
	if captured.SourceDeploymentDir != "/staging" || captured.DestinationDeploymentDir != "/installed" {
		t.Fatalf("install directories = %q -> %q", captured.SourceDeploymentDir, captured.DestinationDeploymentDir)
	}
	if captured.Runtime != runtime || captured.ControlMode != ControlAdmissionDrainV1 || captured.Scope != InstallScopeSystem || captured.Service != "demo" {
		t.Fatalf("install identity options = %#v", captured)
	}
	if !reflect.DeepEqual(captured.PortOverrides, []PortOverride{{Name: "web", HostPort: "8080"}}) || !reflect.DeepEqual(captured.Replace, []string{"config"}) || !captured.Clean || !captured.Start {
		t.Fatalf("install content options = %#v", captured)
	}
	if captured.RunOptions.Stdout != io.Discard || captured.RunOptions.Stderr != io.Discard ||
		captured.RunOptions.Progress != &progress || captured.RunOptions.DockerPreflightTimeout != 7*time.Second {
		t.Fatalf("install run options = %#v", captured.RunOptions)
	}
}

func TestDirectInstallProviderV1MapsPublicOptionsAndReturnsTarget(t *testing.T) {
	runtime := StagedProviderBuildRuntimeV1{Host: blueprint.HostMacOS, UID: 56, GID: 78}
	pack := deploy.PackRef{Raw: "demo"}
	var captured DirectProviderInstallInputV1
	target, err := directInstallProviderV1(DirectInstallOptions{
		Pack: pack, Target: "/installed", ControlMode: ControlAdmissionForceV1,
		Scope: InstallScopeUser, Service: "demo", Start: true, Stdout: io.Discard,
	}, providerInstallPublicBackendV1{
		runtime: func() (StagedProviderBuildRuntimeV1, error) { return runtime, nil },
		direct: func(ctx context.Context, input DirectProviderInstallInputV1) (DirectProviderInstallResultV1, error) {
			if ctx == nil {
				t.Fatal("direct install context is nil")
			}
			captured = input
			return DirectProviderInstallResultV1{Target: "/resolved"}, nil
		},
	})
	if err != nil {
		t.Fatalf("direct install provider: %v", err)
	}
	if target != "/resolved" {
		t.Fatalf("target = %q, want /resolved", target)
	}
	if !reflect.DeepEqual(captured.Pack, pack) || captured.Target != "/installed" || captured.Runtime != runtime || captured.ControlMode != ControlAdmissionForceV1 || captured.Scope != InstallScopeUser || captured.Service != "demo" || !captured.Start {
		t.Fatalf("direct install options = %#v", captured)
	}
}

func TestProviderInstallPublicAdaptersRejectDryRunBeforeBackend(t *testing.T) {
	if err := installProviderV1(InstallOptions{DryRun: true}, providerInstallPublicBackendV1{}); err == nil {
		t.Fatal("staged dry-run unexpectedly succeeded")
	}
	if _, err := directInstallProviderV1(DirectInstallOptions{DryRun: true}, providerInstallPublicBackendV1{}); err == nil {
		t.Fatal("direct dry-run unexpectedly succeeded")
	}
}
