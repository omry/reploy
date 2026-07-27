package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecuteProviderUninstallKeepsInstalledStateWhenHostCleanupFails(t *testing.T) {
	dir := t.TempDir()
	operation, _, _ := installedBuildPublicationSourceFixtureAtDir(t, dir)
	installation := installedBuildPublicationInstallation(dir)
	if _, _, err := operation.SetInstallationStateV1(installation); err != nil {
		t.Fatal(err)
	}
	want := errors.New("docker unavailable")
	err := executeProviderUninstallWithV1(t.Context(), operation, providerUninstallPlanV1{
		Installation: installation,
	}, RunOptions{}, func(context.Context, providerUninstallPlanV1, RunOptions) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("cleanup failure = %v, want %v", err, want)
	}
	state, found, readErr := operation.ReadStateV1()
	if readErr != nil || !found || state.Deployment == nil {
		t.Fatalf("failed uninstall state: found=%v err=%v state=%#v", found, readErr, state)
	}
}

func TestExecuteProviderUninstallRetainsBuildAsStagingAndReportsSuccess(t *testing.T) {
	dir := t.TempDir()
	operation, _, current := installedBuildPublicationSourceFixtureAtDir(t, dir)
	installation := installedBuildPublicationInstallation(dir)
	if _, _, err := operation.SetInstallationStateV1(installation); err != nil {
		t.Fatal(err)
	}
	called := false
	var stdout bytes.Buffer
	err := executeProviderUninstallWithV1(t.Context(), operation, providerUninstallPlanV1{
		State: current.State, Installation: installation, Environment: "demo",
		GenerationReference: current.Generation.Reference, Backend: installBackendLinuxSystemd,
	}, RunOptions{Stdout: &stdout}, func(_ context.Context, _ providerUninstallPlanV1, _ RunOptions) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("execute uninstall: %v", err)
	}
	if !called {
		t.Fatal("host cleanup was not called")
	}
	if !strings.Contains(stdout.String(), "uninstalled service: demo") {
		t.Fatalf("success output = %q", stdout.String())
	}
	state, found, err := operation.ReadStateV1()
	if err != nil || !found || state.Deployment != nil || state.Current == nil {
		t.Fatalf("retained state: found=%v err=%v state=%#v", found, err, state)
	}
	for _, path := range []string{
		filepath.Join(dir, "demo"),
		filepath.Join(dir, filepath.FromSlash(embeddedRuntimeFileName())),
		filepath.Join(dir, filepath.FromSlash(stagedControlManifestPathV1)),
	} {
		if info, statErr := os.Stat(path); statErr != nil || !info.Mode().IsRegular() {
			t.Fatalf("retained staged control file %q: info=%v err=%v", path, info, statErr)
		}
	}
}

func TestExecuteProviderUninstallDefersRemoveDirFinalization(t *testing.T) {
	dir := t.TempDir()
	operation, _, _ := installedBuildPublicationSourceFixtureAtDir(t, dir)
	installation := installedBuildPublicationInstallation(dir)
	if _, _, err := operation.SetInstallationStateV1(installation); err != nil {
		t.Fatal(err)
	}
	called := false
	var stdout bytes.Buffer
	err := executeProviderUninstallWithV1(t.Context(), operation, providerUninstallPlanV1{Installation: installation, RemoveDir: true}, RunOptions{Stdout: &stdout}, func(context.Context, providerUninstallPlanV1, RunOptions) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("prepare remove-dir: %v", err)
	}
	if !called {
		t.Fatal("remove-dir did not clean host before finalization")
	}
	if stdout.Len() != 0 {
		t.Fatalf("remove-dir reported success before finalization: %q", stdout.String())
	}
	state, found, readErr := operation.ReadStateV1()
	if readErr != nil || !found || state.Deployment == nil {
		t.Fatalf("remove-dir preparation must retain retryable installed state: found=%v err=%v state=%#v", found, readErr, state)
	}
}
