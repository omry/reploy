package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestDoctorReportsUnbuiltStateV1(t *testing.T) {
	disableDoctorColor(t)
	dir := t.TempDir()
	state := doctorStateV1Fixture(t)
	state.Current = nil
	writeDoctorStateV1(t, dir, state)

	var stdout strings.Builder
	if code := Doctor(DoctorOptions{Dir: dir, Stdout: &stdout}); code != 0 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	for _, want := range []string{
		"ok: state-v1 deployment is readable:",
		"warn: environment has not been built",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestDoctorUsesStateReadUnderOperationLock(t *testing.T) {
	disableDoctorColor(t)
	dir, operation, _, _, state := currentBuildFixture(t, true)
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	document.Environment.ID = "demo"
	state.Blueprint, err = blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.CommitStateV1(state.Current, state); err != nil {
		t.Fatal(err)
	}

	preflightRead := make(chan struct{})
	result := make(chan []DoctorFinding)
	go func() {
		result <- doctorFindingsWithV1(dir, false, "", 0, doctorFindingsBackendV1{
			readFile: func(path string) ([]byte, error) {
				content, err := os.ReadFile(path)
				close(preflightRead)
				return content, err
			},
			acquire: deploy.AcquireExistingOperationLock,
		})
	}()
	<-preflightRead

	replacement := state
	replacement.Current = nil
	if err := operation.CommitStateV1(state.Current, replacement); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}

	findings := <-result
	var unbuilt bool
	for _, finding := range findings {
		if finding.Status == "fail" {
			t.Fatalf("doctor reported a mixed-state failure: %#v", findings)
		}
		if finding.Status == "warn" && strings.Contains(finding.Message, "has not been built") {
			unbuilt = true
		}
	}
	if !unbuilt {
		t.Fatalf("doctor findings = %#v, want locked replacement state", findings)
	}
}

func TestDoctorChecksCurrentRuntimeFiles(t *testing.T) {
	disableDoctorColor(t)
	dir, operation, _, lock, state := currentBuildFixture(t, true)
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	document.Environment.ID = "demo"
	document.Environment.ControlScript = "demo-control"
	state.Blueprint, err = blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.CommitStateV1(state.Current, state); err != nil {
		t.Fatal(err)
	}
	runtime, err := CurrentStagedProviderBuildRuntimeV1()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanCurrentRuntimeV1(CurrentRuntimePlanInputV1{
		DeploymentDir: dir,
		Current:       CurrentBuild{State: state, Generation: *state.Current, Lock: lock},
		Runtime:       runtime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PublishCurrentRuntimeInputsV1(operation, dir, plan); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	doctorDir, err := filepath.Rel(workingDir, dir)
	if err != nil {
		t.Fatal(err)
	}

	var current strings.Builder
	if code := Doctor(DoctorOptions{Dir: doctorDir, Stdout: &current}); code != 0 {
		t.Fatalf("doctor exit = %d\n%s", code, current.String())
	}
	if !strings.Contains(current.String(), "ok: current runtime files match the recorded build") {
		t.Fatalf("stdout missing runtime-file success:\n%s", current.String())
	}

	if err := os.WriteFile(filepath.Join(dir, ComposeFileName), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stale strings.Builder
	if code := Doctor(DoctorOptions{Dir: doctorDir, Stdout: &stale}); code != 1 {
		t.Fatalf("doctor exit = %d\n%s", code, stale.String())
	}
	if !strings.Contains(stale.String(), "fail: current runtime files do not match the recorded build") {
		t.Fatalf("stdout missing runtime-file drift:\n%s", stale.String())
	}
}

func TestDoctorRejectsMissingAndLegacyState(t *testing.T) {
	disableDoctorColor(t)
	for _, test := range []struct {
		name    string
		content []byte
		want    string
	}{
		{name: "missing", want: "fail: cannot read state:"},
		{name: "legacy", content: []byte(`{"phase":"installed"}`), want: "state.legacy_unsupported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if test.content != nil {
				path := filepath.Join(dir, StateFileName)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, test.content, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var stdout strings.Builder
			if code := Doctor(DoctorOptions{Dir: dir, Stdout: &stdout}); code != 1 {
				t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
			}
			if !strings.Contains(stdout.String(), test.want) {
				t.Fatalf("stdout missing %q:\n%s", test.want, stdout.String())
			}
		})
	}
}

func TestDoctorPreinstallChecksDockerRuntime(t *testing.T) {
	disableDoctorColor(t)
	dir := t.TempDir()
	state := doctorStateV1Fixture(t)
	state.Current = nil
	writeDoctorStateV1(t, dir, state)

	previous := detectDockerRuntimeForDoctor
	previousTools := doctorInspectHostTools
	t.Cleanup(func() {
		detectDockerRuntimeForDoctor = previous
		doctorInspectHostTools = previousTools
	})
	doctorInspectHostTools = func(context.Context, installBackend) (providerInstallHostToolsV1, error) {
		return providerInstallHostToolsV1{DockerPath: "/usr/bin/docker"}, nil
	}
	detectDockerRuntimeForDoctor = func(_ context.Context, command CommandSpec, timeout time.Duration) (dockerRuntimeInfo, error) {
		if command.Name != "/usr/bin/docker" || command.Dir != dir {
			t.Fatalf("docker command = %#v", command)
		}
		if timeout != 2*time.Second {
			t.Fatalf("docker timeout = %s", timeout)
		}
		return dockerRuntimeInfo{OperatingSystem: "Docker Desktop"}, nil
	}

	var stdout strings.Builder
	if code := Doctor(DoctorOptions{Dir: dir, Preinstall: true, Scope: InstallScopeUser, DockerPreflightTimeout: 2 * time.Second, Stdout: &stdout}); code != 0 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok: Docker runtime detected: Docker Desktop") {
		t.Fatalf("stdout missing Docker runtime success:\n%s", stdout.String())
	}

	detectDockerRuntimeForDoctor = func(context.Context, CommandSpec, time.Duration) (dockerRuntimeInfo, error) {
		return dockerRuntimeInfo{}, errors.New("docker is not running")
	}
	stdout.Reset()
	if code := Doctor(DoctorOptions{Dir: dir, Preinstall: true, Scope: InstallScopeUser, Stdout: &stdout}); code != 1 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "fail: Docker runtime is required for install: docker is not running") {
		t.Fatalf("stdout missing Docker runtime failure:\n%s", stdout.String())
	}
}

func TestDoctorPreinstallChecksSystemScopePrivilegesAndAccount(t *testing.T) {
	disableDoctorColor(t)
	dir := t.TempDir()
	state := doctorStateV1Fixture(t)
	state.Current = nil
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	document.Environment.Install.System.Account = blueprint.SystemAccount{User: "demo", Group: "demo", OnMissing: "create"}
	state.Blueprint, err = blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	writeDoctorStateV1(t, dir, state)

	previousPlatform := detectHostPlatform
	previousRuntime := detectDockerRuntimeForDoctor
	previousTools := doctorInspectHostTools
	previousAccount := doctorInspectAccount
	previousGeteuid := doctorGeteuid
	t.Cleanup(func() {
		detectHostPlatform = previousPlatform
		detectDockerRuntimeForDoctor = previousRuntime
		doctorInspectHostTools = previousTools
		doctorInspectAccount = previousAccount
		doctorGeteuid = previousGeteuid
	})
	detectHostPlatform = func() hostPlatform { return hostPlatform{GOOS: "linux"} }
	doctorInspectHostTools = func(_ context.Context, backend installBackend) (providerInstallHostToolsV1, error) {
		if backend != installBackendLinuxSystemd {
			t.Fatalf("backend = %q", backend)
		}
		return providerInstallHostToolsV1{DockerPath: "/usr/bin/docker", SystemctlPath: "/usr/bin/systemctl"}, nil
	}
	detectDockerRuntimeForDoctor = func(context.Context, CommandSpec, time.Duration) (dockerRuntimeInfo, error) {
		return dockerRuntimeInfo{OperatingSystem: "Linux"}, nil
	}
	doctorGeteuid = func() int { return 0 }
	doctorInspectAccount = func(scope InstallScope, account blueprint.SystemAccount) (providerInstallAccountInspectionV1, error) {
		if scope != InstallScopeSystem || account.User != "demo" || account.Group != "demo" {
			t.Fatalf("account input = %q/%#v", scope, account)
		}
		return providerInstallAccountInspectionV1{User: "demo", Group: "demo", WillCreate: true}, nil
	}

	var stdout strings.Builder
	if code := Doctor(DoctorOptions{Dir: dir, Preinstall: true, Scope: InstallScopeSystem, Stdout: &stdout}); code != 0 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	for _, want := range []string{
		"ok: install host tools are ready for system scope",
		"ok: system install is running with root privileges",
		"ok: system install account demo:demo can be created",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}

	doctorGeteuid = func() int { return 1000 }
	stdout.Reset()
	if code := Doctor(DoctorOptions{Dir: dir, Preinstall: true, Scope: InstallScopeSystem, Stdout: &stdout}); code != 1 {
		t.Fatalf("non-root doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "fail: system install requires root privileges") {
		t.Fatalf("stdout missing root failure:\n%s", stdout.String())
	}
}

func TestDoctorOutputFiltersAndColors(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "always")
	colors := doctorStatusColors(&strings.Builder{})
	for status, want := range map[string]string{
		"ok":   "\x1b[32mok\x1b[0m",
		"warn": "\x1b[33mwarn\x1b[0m",
		"fail": "\x1b[31mfail\x1b[0m",
	} {
		if got := colors.status(status); got != want {
			t.Fatalf("status %q = %q, want %q", status, got, want)
		}
	}
	if shouldPrintDoctorFinding(DoctorFinding{Status: "ok"}, DoctorOptions{Quiet: true}) {
		t.Fatal("quiet doctor printed an ok finding")
	}
	if shouldPrintDoctorFinding(DoctorFinding{Status: "warn"}, DoctorOptions{SuppressWarnings: true}) {
		t.Fatal("suppressed doctor printed a warning")
	}
	if !shouldPrintDoctorFinding(DoctorFinding{Status: "fail"}, DoctorOptions{Quiet: true, SuppressWarnings: true}) {
		t.Fatal("doctor suppressed a failure")
	}
}

func doctorStateV1Fixture(t *testing.T) deploy.StateV1 {
	t.Helper()
	current, _ := runtimeCurrentBuildFixture(t)
	return current.State
}

func writeDoctorStateV1(t *testing.T, dir string, state deploy.StateV1) {
	t.Helper()
	content, err := deploy.EncodeStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, StateFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func disableDoctorColor(t *testing.T) {
	t.Helper()
	t.Setenv("REPLOY_COLOR", "never")
}
