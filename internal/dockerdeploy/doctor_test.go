package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

func TestDoctorPassesForInitializedDeployment(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": ""}); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Stdout: &stdout})
	if code != 0 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok: generated file matches manifest:") {
		t.Fatalf("stdout missing manifest check:\n%s", stdout.String())
	}
}

func TestDoctorFailsForEditedGeneratedFile(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, "democtl"), []byte("local edit\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Stdout: &stdout})
	if code != 1 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "fail: generated file has local edits:") {
		t.Fatalf("stdout missing local edit failure:\n%s", stdout.String())
	}
}

func TestDoctorPreinstallReportsGeneratedControlScriptDrift(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": "1000:1000"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, "democtl"), []byte("#!/bin/sh\necho edited\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var normalStdout strings.Builder
	normalCode := Doctor(DoctorOptions{Dir: deployDir, Stdout: &normalStdout})
	if normalCode != 1 {
		t.Fatalf("normal doctor exit = %d\n%s", normalCode, normalStdout.String())
	}
	if !strings.Contains(normalStdout.String(), "fail: generated file has local edits:") {
		t.Fatalf("normal doctor stdout missing local edit failure:\n%s", normalStdout.String())
	}

	var preinstallStdout strings.Builder
	preinstallCode := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Stdout: &preinstallStdout})
	if preinstallCode != 1 {
		t.Fatalf("preinstall doctor exit = %d\n%s", preinstallCode, preinstallStdout.String())
	}
	if !strings.Contains(preinstallStdout.String(), "fail: generated file has local edits:") {
		t.Fatalf("preinstall doctor stdout missing local edit failure:\n%s", preinstallStdout.String())
	}
}

func TestDoctorPreinstallStillFailsForEditedGeneratedFile(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": "1000:1000"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, "democtl"), []byte("local edit\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Stdout: &stdout})
	if code != 1 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "fail: generated file has local edits:") {
		t.Fatalf("stdout missing local edit failure:\n%s", stdout.String())
	}
}

func TestDoctorPreinstallPassesForInitializedDeployment(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": "1000:1000"}); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Stdout: &stdout})
	if code != 0 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok: preinstall checks passed") {
		t.Fatalf("stdout missing preinstall success:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok: install owner resolves to 1000:1000 (1000:1000)") {
		t.Fatalf("stdout missing install owner success:\n%s", stdout.String())
	}
}

func TestDoctorPreinstallOnDarwinUserScopeAcceptsDockerRuntime(t *testing.T) {
	disableDoctorColor(t)
	restorePlatform := stubHostPlatform(t, hostPlatform{GOOS: "darwin"})
	defer restorePlatform()
	previousDetector := detectDockerRuntimeForDoctor
	t.Cleanup(func() {
		detectDockerRuntimeForDoctor = previousDetector
	})
	detectDockerRuntimeForDoctor = func(_ context.Context, _ CommandSpec, timeout time.Duration) (dockerRuntimeInfo, error) {
		if timeout != 2*time.Second {
			t.Fatalf("docker timeout = %s, want 2s", timeout)
		}
		return dockerRuntimeInfo{Runtime: dockerRuntimeLinuxEngine, OperatingSystem: "Colima", ServerVersion: "29.5.3"}, nil
	}

	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Scope: InstallScopeUser, Stdout: &stdout, DockerPreflightTimeout: 2 * time.Second})
	if code != 0 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	for _, want := range []string{
		"ok: Docker runtime detected: Colima",
		"ok: preinstall checks passed",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "warn:") {
		t.Fatalf("darwin doctor should not print Docker Desktop advisory warnings:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "install owner resolves") {
		t.Fatalf("darwin doctor should not require Linux install owner:\n%s", stdout.String())
	}
}

func TestDoctorPreinstallOnDarwinFailsWhenDockerRuntimeUnavailable(t *testing.T) {
	disableDoctorColor(t)
	restorePlatform := stubHostPlatform(t, hostPlatform{GOOS: "darwin"})
	defer restorePlatform()
	previousDetector := detectDockerRuntimeForDoctor
	t.Cleanup(func() {
		detectDockerRuntimeForDoctor = previousDetector
	})
	detectDockerRuntimeForDoctor = func(context.Context, CommandSpec, time.Duration) (dockerRuntimeInfo, error) {
		return dockerRuntimeInfo{}, errors.New("docker is not running")
	}

	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Scope: InstallScopeUser, Stdout: &stdout})
	if code != 1 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "fail: Docker runtime is required for Docker-managed permanent install: docker is not running") {
		t.Fatalf("stdout missing Docker runtime failure:\n%s", stdout.String())
	}
}

func TestDoctorPreinstallOnDarwinUserScopeAcceptsNonDockerDesktopRuntime(t *testing.T) {
	disableDoctorColor(t)
	restorePlatform := stubHostPlatform(t, hostPlatform{GOOS: "darwin"})
	defer restorePlatform()
	previousDetector := detectDockerRuntimeForDoctor
	t.Cleanup(func() {
		detectDockerRuntimeForDoctor = previousDetector
	})
	detectDockerRuntimeForDoctor = func(context.Context, CommandSpec, time.Duration) (dockerRuntimeInfo, error) {
		return dockerRuntimeInfo{Runtime: dockerRuntimeLinuxEngine, OperatingSystem: "Ubuntu 24.04", ServerVersion: "29.5.3"}, nil
	}

	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Scope: InstallScopeUser, Stdout: &stdout})
	if code != 0 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok: Docker runtime detected: Ubuntu 24.04") {
		t.Fatalf("stdout missing Docker runtime success:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok: preinstall checks passed") {
		t.Fatalf("stdout missing preinstall success:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "warn:") {
		t.Fatalf("non-Docker-Desktop runtime should pass without advisory warnings:\n%s", stdout.String())
	}
}

func TestDoctorPreinstallOnWindowsUsesDockerDesktopInstallChecks(t *testing.T) {
	disableDoctorColor(t)
	restorePlatform := stubHostPlatform(t, hostPlatform{GOOS: "windows"})
	defer restorePlatform()
	previousDetector := detectDockerRuntimeForDoctor
	t.Cleanup(func() {
		detectDockerRuntimeForDoctor = previousDetector
	})
	detectDockerRuntimeForDoctor = func(_ context.Context, _ CommandSpec, timeout time.Duration) (dockerRuntimeInfo, error) {
		if timeout != 3*time.Second {
			t.Fatalf("docker timeout = %s, want 3s", timeout)
		}
		return dockerRuntimeInfo{Runtime: dockerRuntimeDockerDesktop, OperatingSystem: "Docker Desktop", ServerVersion: "29.5.3"}, nil
	}

	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Stdout: &stdout, DockerPreflightTimeout: 3 * time.Second})
	if code != 0 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	for _, want := range []string{
		"ok: Docker Desktop runtime detected: Docker Desktop",
		"ok: preinstall checks passed",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "warn:") {
		t.Fatalf("windows doctor should not print Docker Desktop advisory warnings:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "install owner resolves") {
		t.Fatalf("windows doctor should not require Linux install owner:\n%s", stdout.String())
	}
}

func TestDoctorPreinstallFailsForMissingBundleWheel(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": "1000:1000"}); err != nil {
		t.Fatal(err)
	}
	sourceWheel := filepath.Join(t.TempDir(), "demo-1.0.0-py3-none-any.whl")
	if err := os.WriteFile(sourceWheel, []byte("wheel\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := BundleAddWheel(BundleRootOptions{Dir: deployDir, Source: sourceWheel}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(deployDir, BundleDirName, filepath.Base(sourceWheel))); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Stdout: &stdout})
	if code != 1 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "fail: wheel root is missing from deployment bundle:") {
		t.Fatalf("stdout missing wheel failure:\n%s", stdout.String())
	}
}

func TestDoctorPreinstallRejectsUnresolvedInstallOwner(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": "appuser"}); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Stdout: &stdout})
	if code != 1 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `fail: install owner must resolve to a non-root uid:gid: resolve REPLOY_INSTALL_OWNER user "appuser"`) {
		t.Fatalf("stdout missing install owner failure:\n%s", stdout.String())
	}
}

func TestDoctorPreinstallAcceptsMissingOwnerWithCreatePolicy(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{
		"REPLOY_INSTALL_OWNER":            "appuser:appgroup",
		"REPLOY_INSTALL_OWNER_ON_MISSING": "create",
	}); err != nil {
		t.Fatal(err)
	}
	oldLookupUser := installLookupUser
	oldLookupGroup := installLookupGroup
	t.Cleanup(func() {
		installLookupUser = oldLookupUser
		installLookupGroup = oldLookupGroup
	})
	installLookupUser = func(name string) (*user.User, error) {
		return nil, user.UnknownUserError(name)
	}
	installLookupGroup = func(name string) (*user.Group, error) {
		return nil, user.UnknownGroupError(name)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Stdout: &stdout})
	if code != 0 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok: install owner will be created if missing: appuser:appgroup") {
		t.Fatalf("stdout missing install owner creation plan:\n%s", stdout.String())
	}
}

func TestDoctorPreinstallRejectsAmbiguousOwnerLookupWithCreatePolicy(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{
		"REPLOY_INSTALL_OWNER":            "appuser:appgroup",
		"REPLOY_INSTALL_OWNER_ON_MISSING": "create",
	}); err != nil {
		t.Fatal(err)
	}
	oldLookupUser := installLookupUser
	oldLookupGroup := installLookupGroup
	t.Cleanup(func() {
		installLookupUser = oldLookupUser
		installLookupGroup = oldLookupGroup
	})
	installLookupUser = func(name string) (*user.User, error) {
		return nil, errors.New("nss backend unavailable")
	}
	installLookupGroup = func(name string) (*user.Group, error) {
		t.Fatalf("group lookup should not run after user lookup failure: %s", name)
		return nil, nil
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Stdout: &stdout})
	if code == 0 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `fail: install owner must resolve to a non-root uid:gid: resolve REPLOY_INSTALL_OWNER user "appuser": nss backend unavailable`) {
		t.Fatalf("stdout missing ambiguous lookup failure:\n%s", stdout.String())
	}
}

func TestDoctorPreinstallRejectsAmbiguousGroupLookupWithMissingUser(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{
		"REPLOY_INSTALL_OWNER":            "appuser:appgroup",
		"REPLOY_INSTALL_OWNER_ON_MISSING": "create",
	}); err != nil {
		t.Fatal(err)
	}
	oldLookupUser := installLookupUser
	oldLookupGroup := installLookupGroup
	t.Cleanup(func() {
		installLookupUser = oldLookupUser
		installLookupGroup = oldLookupGroup
	})
	installLookupUser = func(name string) (*user.User, error) {
		return nil, user.UnknownUserError(name)
	}
	installLookupGroup = func(name string) (*user.Group, error) {
		return nil, errors.New("nss backend unavailable")
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Stdout: &stdout})
	if code == 0 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `fail: install owner must resolve to a non-root uid:gid: lookup install owner group "appgroup": nss backend unavailable`) {
		t.Fatalf("stdout missing group lookup failure:\n%s", stdout.String())
	}
}

func TestDoctorPreinstallRejectsMissingInstallOwner(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": ""}); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Stdout: &stdout})
	if code != 1 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "fail: install owner must resolve to a non-root uid:gid: REPLOY_INSTALL_OWNER is required for system install") {
		t.Fatalf("stdout missing install owner failure:\n%s", stdout.String())
	}
}

func TestDoctorPreinstallAcceptsNamedInstallOwner(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": "appuser:appgroup"}); err != nil {
		t.Fatal(err)
	}
	oldLookupUser := installLookupUser
	oldLookupGroup := installLookupGroup
	t.Cleanup(func() {
		installLookupUser = oldLookupUser
		installLookupGroup = oldLookupGroup
	})
	installLookupUser = func(name string) (*user.User, error) {
		if name != "appuser" {
			t.Fatalf("unexpected user lookup: %s", name)
		}
		return &user.User{Username: "appuser", Uid: "997", Gid: "988"}, nil
	}
	installLookupGroup = func(name string) (*user.Group, error) {
		if name != "appgroup" {
			t.Fatalf("unexpected group lookup: %s", name)
		}
		return &user.Group{Name: "appgroup", Gid: "988"}, nil
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Stdout: &stdout})
	if code != 0 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok: install owner resolves to appuser:appgroup (997:988)") {
		t.Fatalf("stdout missing install owner success:\n%s", stdout.String())
	}
}

func TestDoctorQuietSuppressesPassingChecks(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Quiet: true, Stdout: &stdout})
	if code != 0 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestDoctorSuppressWarningsKeepsFailures(t *testing.T) {
	disableDoctorColor(t)
	restorePlatform := stubHostPlatform(t, hostPlatform{GOOS: "darwin"})
	previousDetector := detectDockerRuntimeForDoctor
	t.Cleanup(func() {
		restorePlatform()
		detectDockerRuntimeForDoctor = previousDetector
	})
	detectDockerRuntimeForDoctor = func(context.Context, CommandSpec, time.Duration) (dockerRuntimeInfo, error) {
		return dockerRuntimeInfo{}, errors.New("docker unavailable")
	}

	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Quiet: true, SuppressWarnings: true, Stdout: &stdout})
	if code != 1 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if strings.Contains(stdout.String(), "warn:") {
		t.Fatalf("stdout should suppress warnings:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "fail: Docker Desktop runtime is required") {
		t.Fatalf("stdout should keep failures:\n%s", stdout.String())
	}
}

func TestDoctorStatusColors(t *testing.T) {
	colors := doctorColors{enabled: true}
	if got := colors.status("ok"); got != "\x1b[32mok\x1b[0m" {
		t.Fatalf("ok color = %q", got)
	}
	if got := colors.status("fail"); got != "\x1b[31mfail\x1b[0m" {
		t.Fatalf("fail color = %q", got)
	}
	if got := colors.status("unknown"); got != "unknown" {
		t.Fatalf("unknown color = %q", got)
	}
}

func TestDoctorCapturedOutputStaysPlain(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "auto")
	t.Setenv("TERM", "xterm-256color")
	var stdout strings.Builder
	colors := doctorStatusColors(&stdout)
	if got := colors.status("ok"); got != "ok" {
		t.Fatalf("captured ok color = %q", got)
	}
}

func disableDoctorColor(t *testing.T) {
	t.Helper()
	t.Setenv("REPLOY_COLOR", "never")
}
