package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestSystemInstallRejectsActualNonRootBeforeSourceWork(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires an actual non-root process")
	}
	runtime := blueprintRuntimeFixtureV1()
	runtime.UID, runtime.GID = 0, 0
	_, publicErr := RunProviderInstallV1(t.Context(), ProviderInstallInputV1{
		SourceDeploymentDir: filepath.Join(t.TempDir(), "not-staged"), Runtime: runtime, Scope: InstallScopeSystem,
	})
	if publicErr == nil || !strings.Contains(publicErr.Error(), "requires root privileges") {
		t.Fatalf("public staged install error=%v", publicErr)
	}
	_, publicErr = RunDirectProviderInstallV1(t.Context(), DirectProviderInstallInputV1{
		Pack: deploy.PackRef{Raw: "file:/not-a-pack"}, Runtime: runtime, Scope: InstallScopeSystem,
	})
	if publicErr == nil || !strings.Contains(publicErr.Error(), "requires root privileges") {
		t.Fatalf("public direct install error=%v", publicErr)
	}
	called := false
	_, err := runProviderInstallEntryV1(t.Context(), ProviderInstallInputV1{
		Runtime: runtime, Scope: InstallScopeSystem,
	}, providerInstallEntryBackendV1{
		run: func(context.Context, providerInstallRunInputV1, providerInstallRunBackend) (deploy.StateV1, error) {
			called = true
			return deploy.StateV1{}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "requires root privileges") || called {
		t.Fatalf("staged error=%v source work=%v", err, called)
	}
	_, err = runDirectProviderInstallEntryV1(t.Context(), DirectProviderInstallInputV1{
		Pack: deploy.PackRef{Raw: "https://example.invalid/never-fetch"}, Runtime: runtime, Scope: InstallScopeSystem,
	}, directProviderInstallEntryBackendV1{
		withSource: func(context.Context, directProviderInstallSourceInputV1, func(context.Context, string) error) error {
			called = true
			return nil
		},
		readState: func(context.Context, string) (deploy.StateV1, error) { return deploy.StateV1{}, nil },
		install:   func(context.Context, ProviderInstallInputV1) (deploy.StateV1, error) { return deploy.StateV1{}, nil },
		roots:     func(string) (installTargetRootsV1, error) { return installTargetRootsV1{}, nil },
	})
	if err == nil || !strings.Contains(err.Error(), "requires root privileges") || called {
		t.Fatalf("direct error=%v fetching=%v", err, called)
	}
}

func TestInstallEntryPrivilegeAndScopeValidation(t *testing.T) {
	for _, tt := range []struct {
		scope   InstallScope
		uid     int
		service string
		want    string
	}{
		{InstallScopeSystem, 0, "", ""},
		{InstallScopeSystem, 1000, "", "requires root privileges"},
		{InstallScopeUser, 1000, "", ""},
		{InstallScopeSystem, 1000, "bad/name", "unsupported characters"},
		{"invalid", 0, "", "--scope must be"},
	} {
		_, err := validateProviderInstallEntryV1(tt.scope, "linux", tt.service, func() int { return tt.uid })
		if tt.want == "" && err != nil || tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
			t.Fatalf("scope=%s uid=%d service=%s error=%v", tt.scope, tt.uid, tt.service, err)
		}
	}
}

func TestDestinationPreflightRejectsBeforeBuildAndPreservesMetadata(t *testing.T) {
	for _, defaultTarget := range []bool{false, true} {
		for _, problem := range []string{"staging", "service", "legacy", "blueprint"} {
			t.Run(problem+map[bool]string{false: "-explicit", true: "-default"}[defaultTarget], func(t *testing.T) {
				source, destination := t.TempDir(), t.TempDir()
				document, platform := testSelectedPlatformDocumentV1(t)
				document.Environment.Install.Target.DefaultPath = destination
				if _, err := deploy.SetDesiredStateV1(t.Context(), source, document, platform, nil); err != nil {
					t.Fatal(err)
				}
				if problem == "staging" {
					if _, err := deploy.SetDesiredStateV1(t.Context(), destination, document, platform, nil); err != nil {
						t.Fatal(err)
					}
				} else {
					op, _, _ := installedBuildPublicationSourceFixtureAtDir(t, destination)
					installation := installedBuildPublicationInstallation(destination)
					installation.Service = "old-service"
					installation.UnitPath = filepath.Join(destination, "old-service.service")
					if _, _, err := op.SetInstallationStateV1(installation); err != nil {
						t.Fatal(err)
					}
					if err := op.Unlock(); err != nil {
						t.Fatal(err)
					}
				}
				statePath := filepath.Join(destination, StateFileName)
				if problem == "legacy" {
					if err := os.WriteFile(statePath, []byte(`{"schema":"state-v0"}`), 0o600); err != nil {
						t.Fatal(err)
					}
				} else if problem == "blueprint" {
					content, err := os.ReadFile(statePath)
					if err != nil {
						t.Fatal(err)
					}
					// Whitespace inside the encoded canonical blueprint is incompatible.
					content = []byte(strings.Replace(string(content), `"blueprint":"{`, `"blueprint":"{ `, 1))
					if err := os.WriteFile(statePath, content, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				before, err := os.ReadFile(statePath)
				if err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(statePath)
				if err != nil {
					t.Fatal(err)
				}
				lockPath := filepath.Join(destination, ".reploy", "operation.lock")
				// The existing fixture lock is removed so preflight must not recreate it.
				if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				builds, destinationLocks := 0, 0
				backend := newProviderInstallRunBackendV1()
				backend.acquire = func(ctx context.Context, dir string) (*deploy.OperationLock, error) {
					if dir == destination {
						destinationLocks++
					}
					return deploy.AcquireOperationLock(ctx, dir)
				}
				backend.buildSource = func(context.Context, LockedProviderBuildRunInputV1) (LockedProviderBuildExecutionResultV1, error) {
					builds++
					return LockedProviderBuildExecutionResultV1{}, errors.New("unexpected source build")
				}
				buildRuntime := blueprintRuntimeFixtureV1()
				installScope := InstallScopeSystem
				if runtime.GOOS == "windows" {
					// Resolve Windows temporary paths using the supported user scope.
					buildRuntime.Host = blueprint.HostWindows
					installScope = InstallScopeUser
				}
				input := providerInstallRunInputV1{SourceDeploymentDir: source, Runtime: buildRuntime, Install: providerInstallOptionsV1{Scope: installScope, Service: "new-service"}}
				if !defaultTarget {
					input.DestinationDeploymentDir = destination
				}
				_, err = runProviderInstallV1(t.Context(), input, backend)
				if err == nil || !strings.Contains(err.Error(), statePath) || builds != 0 || destinationLocks != 0 {
					t.Fatalf("error=%v builds=%d destination locks=%d", err, builds, destinationLocks)
				}
				after, readErr := os.ReadFile(statePath)
				if readErr != nil {
					t.Fatal(readErr)
				}
				final, statErr := os.Stat(statePath)
				if statErr != nil {
					t.Fatal(statErr)
				}
				if !reflect.DeepEqual(before, after) || !os.SameFile(info, final) || info.Mode() != final.Mode() {
					t.Fatal("preflight mutated destination state")
				}
				if _, err := os.Lstat(lockPath); !os.IsNotExist(err) {
					t.Fatalf("preflight created destination lock: %v", err)
				}
			})
		}
	}
}

func TestDestinationPreflightAcceptsCompatibleStateWithoutLock(t *testing.T) {
	destination := t.TempDir()
	op, _, _ := installedBuildPublicationSourceFixtureAtDir(t, destination)
	installation := installedBuildPublicationInstallation(destination)
	if _, _, err := op.SetInstallationStateV1(installation); err != nil {
		t.Fatal(err)
	}
	if err := op.Unlock(); err != nil {
		t.Fatal(err)
	}
	if err := preflightProviderInstallDestinationV1(destination, installation.Service); err != nil {
		t.Fatal(err)
	}
}

func TestDestinationPreflightRejectsLinkedMetadata(t *testing.T) {
	for _, linkedDirectory := range []bool{true, false} {
		destination, outside := t.TempDir(), t.TempDir()
		if linkedDirectory {
			if err := os.Symlink(outside, filepath.Join(destination, ".reploy")); err != nil {
				t.Skip(err)
			}
		} else {
			if err := os.Mkdir(filepath.Join(destination, ".reploy"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(outside, "state.json"), []byte("private"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(outside, "state.json"), filepath.Join(destination, StateFileName)); err != nil {
				t.Skip(err)
			}
		}
		if err := preflightProviderInstallDestinationV1(destination, ""); err == nil {
			t.Fatal("linked metadata accepted")
		}
	}
}
