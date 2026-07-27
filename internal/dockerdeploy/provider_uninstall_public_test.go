package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestUninstallProviderV1CompletesWhenExplicitTargetIsAlreadyAbsent(t *testing.T) {
	var output bytes.Buffer
	target := filepath.Join(t.TempDir(), "removed-installation")
	runtimeCalled := false
	uninstallCalled := false

	result, err := uninstallProviderV1(UninstallOptions{
		From: target, RemoveDir: true, Stdout: &output,
	}, providerUninstallPublicBackendV1{
		targetAbsent: func(dir string) (bool, error) {
			if dir != target {
				t.Fatalf("target directory = %q, want %q", dir, target)
			}
			return true, nil
		},
		runtime: func() (StagedProviderBuildRuntimeV1, error) {
			runtimeCalled = true
			return StagedProviderBuildRuntimeV1{}, nil
		},
		uninstall: func(context.Context, ProviderUninstallInputV1) error {
			uninstallCalled = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.AlreadyAbsent || result.DeploymentDir != target {
		t.Fatalf("uninstall result = %#v", result)
	}
	if runtimeCalled || uninstallCalled {
		t.Fatalf("already-absent uninstall called runtime=%t uninstall=%t", runtimeCalled, uninstallCalled)
	}
	if output.Len() != 0 {
		t.Fatalf("provider emitted presentation output before CLI progress completed: %q", output.String())
	}
}

func TestUninstallProviderV1DoesNotTreatTargetInspectionFailureAsAbsent(t *testing.T) {
	want := errors.New("permission denied")
	result, err := uninstallProviderV1(UninstallOptions{From: "/opt/unreadable"}, providerUninstallPublicBackendV1{
		targetAbsent: func(string) (bool, error) {
			return false, want
		},
		runtime: func() (StagedProviderBuildRuntimeV1, error) {
			t.Fatal("target inspection failure reached runtime")
			return StagedProviderBuildRuntimeV1{}, nil
		},
		uninstall: func(context.Context, ProviderUninstallInputV1) error {
			t.Fatal("target inspection failure reached uninstall")
			return nil
		},
	})
	if !errors.Is(err, want) {
		t.Fatalf("uninstall error = %v, want %v", err, want)
	}
	if result != (ProviderUninstallResultV1{}) {
		t.Fatalf("uninstall result = %#v, want empty", result)
	}
}

func TestUninstallProviderV1MapsPublicOptions(t *testing.T) {
	runtime := StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1000, GID: 1000}
	timeout := 7 * time.Second
	var progress bytes.Buffer
	captured := ProviderUninstallInputV1{}
	_, err := uninstallProviderV1(UninstallOptions{
		From: "/opt/demo", ServiceName: "demo-service", RemoveDir: true,
		ControlMode: ControlAdmissionDrainV1, Progress: &progress, DockerPreflightTimeout: timeout,
	}, providerUninstallPublicBackendV1{
		runtime: func() (StagedProviderBuildRuntimeV1, error) { return runtime, nil },
		readState: func(string) (deploy.StateV1, bool, error) {
			return deploy.StateV1{}, true, nil
		},
		uninstall: func(_ context.Context, input ProviderUninstallInputV1) error {
			captured = input
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured.DeploymentDir != "/opt/demo" || captured.Runtime != runtime || captured.Service != "demo-service" ||
		!captured.RemoveDir || captured.ControlMode != ControlAdmissionDrainV1 ||
		captured.RunOptions.Progress != &progress || captured.RunOptions.DockerPreflightTimeout != timeout {
		t.Fatalf("provider uninstall input = %#v", captured)
	}
}

func TestUninstallProviderV1RecoversMissingSystemDeploymentByService(t *testing.T) {
	runtime := StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux}
	recovered := ProviderUninstallRecoveryInputV1{}
	_, err := uninstallProviderV1(UninstallOptions{
		ServiceName: "demo", RemoveDir: true, ControlMode: ControlAdmissionForceV1,
	}, providerUninstallPublicBackendV1{
		runtime: func() (StagedProviderBuildRuntimeV1, error) { return runtime, nil },
		readState: func(dir string) (deploy.StateV1, bool, error) {
			if dir != "." {
				t.Fatalf("state dir = %q", dir)
			}
			return deploy.StateV1{}, false, nil
		},
		uninstall: func(context.Context, ProviderUninstallInputV1) error {
			t.Fatal("missing state reached normal uninstall")
			return nil
		},
		recover: func(_ context.Context, input ProviderUninstallRecoveryInputV1) error {
			recovered = input
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Runtime != runtime || recovered.RequestedDir != "" || recovered.Service != "demo" || !recovered.RemoveDir || recovered.ControlMode != ControlAdmissionForceV1 {
		t.Fatalf("recovery input = %#v", recovered)
	}
}

func TestUninstallProviderV1DoesNotRecoverCorruptState(t *testing.T) {
	want := errors.New("corrupt state")
	recovered := false
	_, err := uninstallProviderV1(UninstallOptions{ServiceName: "demo"}, providerUninstallPublicBackendV1{
		runtime: func() (StagedProviderBuildRuntimeV1, error) {
			return StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux}, nil
		},
		readState: func(string) (deploy.StateV1, bool, error) { return deploy.StateV1{}, false, want },
		uninstall: func(context.Context, ProviderUninstallInputV1) error {
			return errors.New("unexpected uninstall")
		},
		recover: func(context.Context, ProviderUninstallRecoveryInputV1) error {
			recovered = true
			return nil
		},
	})
	if !errors.Is(err, want) || recovered {
		t.Fatalf("corrupt-state recovery = %v, recovered=%t", err, recovered)
	}
}

func TestUninstallProviderV1UsesCurrentDirectory(t *testing.T) {
	captured := ""
	backend := providerUninstallPublicBackendV1{
		runtime: func() (StagedProviderBuildRuntimeV1, error) {
			return StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux}, nil
		},
		uninstall: func(_ context.Context, input ProviderUninstallInputV1) error {
			captured = input.DeploymentDir
			return nil
		},
	}
	if _, err := uninstallProviderV1(UninstallOptions{}, backend); err != nil {
		t.Fatal(err)
	}
	if captured != "." {
		t.Fatalf("default deployment directory = %q", captured)
	}
}

func TestUninstallProviderNeedsRootUsesRecordedScope(t *testing.T) {
	for _, test := range []struct {
		name  string
		host  blueprint.HostOS
		scope string
		found bool
		err   error
		want  bool
	}{
		{name: "linux system", host: blueprint.HostLinux, scope: string(InstallScopeSystem), found: true, want: true},
		{name: "linux user", host: blueprint.HostLinux, scope: string(InstallScopeUser), found: true, want: false},
		{name: "mac user", host: blueprint.HostMacOS, found: false, want: false},
		{name: "missing linux state", host: blueprint.HostLinux, found: false, want: true},
		{name: "corrupt linux state", host: blueprint.HostLinux, err: errors.New("corrupt"), want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := uninstallProviderNeedsRootV1(UninstallOptions{From: "/opt/demo"}, providerUninstallPublicBackendV1{
				runtime: func() (StagedProviderBuildRuntimeV1, error) {
					return StagedProviderBuildRuntimeV1{Host: test.host}, nil
				},
				targetAbsent: func(string) (bool, error) {
					return false, nil
				},
				readState: func(dir string) (deploy.StateV1, bool, error) {
					if dir != "/opt/demo" {
						t.Fatalf("read state directory = %q", dir)
					}
					state := deploy.StateV1{}
					if test.found {
						state.Deployment = &deploy.DeploymentStateV1{Installation: deploy.InstallationStateV1{Scope: test.scope}}
					}
					return state, test.found, test.err
				},
			})
			if got != test.want {
				t.Fatalf("needs root = %v, want %v", got, test.want)
			}
		})
	}
}

func TestUninstallProviderNeedsRootDoesNotElevateExplicitAbsentTarget(t *testing.T) {
	readStateCalled := false
	got := uninstallProviderNeedsRootV1(UninstallOptions{From: "/opt/removed"}, providerUninstallPublicBackendV1{
		runtime: func() (StagedProviderBuildRuntimeV1, error) {
			return StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux}, nil
		},
		targetAbsent: func(dir string) (bool, error) {
			if dir != "/opt/removed" {
				t.Fatalf("target directory = %q", dir)
			}
			return true, nil
		},
		readState: func(string) (deploy.StateV1, bool, error) {
			readStateCalled = true
			return deploy.StateV1{}, false, nil
		},
	})
	if got {
		t.Fatal("explicit absent user target requires root")
	}
	if readStateCalled {
		t.Fatal("already-absent target attempted to read installation state")
	}
}
