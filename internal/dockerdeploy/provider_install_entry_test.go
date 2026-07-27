package dockerdeploy

import (
	"context"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func TestProviderInstallEntryMapsPublicOptionsWithoutAccountState(t *testing.T) {
	want := ProviderInstallInputV1{
		SourceDeploymentDir:      "/stage",
		DestinationDeploymentDir: "/installed",
		Runtime:                  blueprintRuntimeFixtureV1(),
		ControlMode:              ControlAdmissionDrainV1,
		Scope:                    InstallScopeSystem,
		Service:                  "demo",
		PortOverrides:            []PortOverride{{Name: "http", HostPort: "19090"}},
		Replace:                  []string{"conf"},
		Clean:                    true,
		Start:                    true,
	}
	called := false
	_, err := runProviderInstallEntryV1(t.Context(), want, providerInstallEntryBackendV1{
		run: func(_ context.Context, got providerInstallRunInputV1, backend providerInstallRunBackend) (deploy.StateV1, error) {
			called = true
			if got.SourceDeploymentDir != want.SourceDeploymentDir || got.DestinationDeploymentDir != want.DestinationDeploymentDir || got.Runtime != want.Runtime || got.ControlMode != want.ControlMode {
				t.Fatalf("provider install input = %#v", got)
			}
			if got.Install.Scope != want.Scope || got.Install.Service != want.Service || !reflect.DeepEqual(got.Install.PortOverrides, want.PortOverrides) || !reflect.DeepEqual(got.Install.Replace, want.Replace) || got.Install.Clean != want.Clean || got.Install.Start != want.Start {
				t.Fatalf("provider install options = %#v", got.Install)
			}
			if got.Install.SystemUser != "" || got.Install.SystemGroup != "" || got.Install.SystemUID != 0 || got.Install.SystemGID != 0 {
				t.Fatalf("public entry supplied internal account state: %#v", got.Install)
			}
			if backend.acquire == nil || backend.buildSource == nil || backend.prepareDestination == nil {
				t.Fatal("public entry did not supply the production install backend")
			}
			return deploy.StateV1{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("provider install backend was not called")
	}
}

func TestProviderInstallEntryRejectsInvalidServiceBeforeBuild(t *testing.T) {
	called := false
	_, err := runProviderInstallEntryV1(t.Context(), ProviderInstallInputV1{
		Runtime: blueprintRuntimeFixtureV1(), Scope: InstallScopeSystem, Service: "bad/name",
	}, providerInstallEntryBackendV1{
		run: func(context.Context, providerInstallRunInputV1, providerInstallRunBackend) (deploy.StateV1, error) {
			called = true
			return deploy.StateV1{}, nil
		},
	})
	if err == nil || called {
		t.Fatalf("error=%v backend called=%v", err, called)
	}
}
