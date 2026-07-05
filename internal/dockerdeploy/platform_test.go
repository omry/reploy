package dockerdeploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	detectHostPlatform = func() hostPlatform {
		return hostPlatform{GOOS: "linux"}
	}
	os.Exit(m.Run())
}

func TestHostPlatformInstallBackend(t *testing.T) {
	tests := []struct {
		goos string
		want installBackend
	}{
		{goos: "linux", want: installBackendLinuxSystemd},
		{goos: "darwin", want: installBackendDockerDesktop},
		{goos: "windows", want: installBackendDockerDesktop},
		{goos: "plan9", want: installBackendUnsupported},
	}
	for _, test := range tests {
		if got := (hostPlatform{GOOS: test.goos}).installBackend(); got != test.want {
			t.Fatalf("install backend for %s = %s, want %s", test.goos, got, test.want)
		}
	}
}

func TestUnsupportedPersistentInstallError(t *testing.T) {
	err := (hostPlatform{GOOS: "windows"}).unsupportedPersistentInstallError("uninstall")
	if err == nil || !strings.Contains(err.Error(), "Windows persistent uninstall is planned as a Docker-managed permanent install but is not supported by this build") {
		t.Fatalf("unexpected windows uninstall error: %v", err)
	}
	err = (hostPlatform{GOOS: "plan9"}).unsupportedPersistentInstallError("install")
	if err == nil || !strings.Contains(err.Error(), "install is not supported on plan9") {
		t.Fatalf("unexpected plan9 install error: %v", err)
	}
}

func TestWindowsCommandSupport(t *testing.T) {
	platform := hostPlatform{GOOS: "windows"}
	tests := []struct {
		name                string
		command             platformCommand
		wantStatus          platformSupportStatus
		wantDockerDesktop   bool
		wantReasonSubstring string
	}{
		{
			name:       "metadata commands are native",
			command:    platformCommandStage,
			wantStatus: platformSupportSupported,
		},
		{
			name:              "docker bundle requires Docker Desktop",
			command:           platformCommandBundleDocker,
			wantStatus:        platformSupportSupported,
			wantDockerDesktop: true,
		},
		{
			name:              "preinstall doctor requires Docker Desktop",
			command:           platformCommandDoctorPreinstall,
			wantStatus:        platformSupportSupported,
			wantDockerDesktop: true,
		},
		{
			name:              "install requires Docker Desktop",
			command:           platformCommandInstall,
			wantStatus:        platformSupportSupported,
			wantDockerDesktop: true,
		},
		{
			name:              "uninstall from install directory requires Docker Desktop",
			command:           platformCommandUninstallFrom,
			wantStatus:        platformSupportSupported,
			wantDockerDesktop: true,
		},
		{
			name:              "PowerShell control requires Docker Desktop",
			command:           platformCommandInstalledPowerShell,
			wantStatus:        platformSupportSupported,
			wantDockerDesktop: true,
		},
		{
			name:                "POSIX control is deferred",
			command:             platformCommandInstalledPOSIX,
			wantStatus:          platformSupportDeferred,
			wantDockerDesktop:   true,
			wantReasonSubstring: "optional WSL/Linux-like access",
		},
		{
			name:                "service name uninstall is deferred",
			command:             platformCommandUninstallServiceName,
			wantStatus:          platformSupportDeferred,
			wantReasonSubstring: "must not imply Windows Service semantics",
		},
		{
			name:                "service list uninstall stays unsupported",
			command:             platformCommandUninstallList,
			wantStatus:          platformSupportUnsupported,
			wantReasonSubstring: "Linux/systemd service discovery",
		},
		{
			name:                "Windows Service install stays unsupported",
			command:             platformCommandWindowsService,
			wantStatus:          platformSupportUnsupported,
			wantReasonSubstring: "future design topic",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := platform.commandSupport(test.command)
			if got.Status != test.wantStatus {
				t.Fatalf("status = %s, want %s", got.Status, test.wantStatus)
			}
			if got.RequiresDockerDesktop != test.wantDockerDesktop {
				t.Fatalf("requires Docker Desktop = %v, want %v", got.RequiresDockerDesktop, test.wantDockerDesktop)
			}
			if test.wantReasonSubstring != "" && !strings.Contains(got.Reason, test.wantReasonSubstring) {
				t.Fatalf("reason = %q, want substring %q", got.Reason, test.wantReasonSubstring)
			}
		})
	}
}

func TestPlatformCommandSupportSelectedNonWindowsContracts(t *testing.T) {
	tests := []struct {
		name                string
		platform            hostPlatform
		command             platformCommand
		wantStatus          platformSupportStatus
		wantDockerDesktop   bool
		wantReasonSubstring string
	}{
		{
			name:       "linux install remains systemd supported",
			platform:   hostPlatform{GOOS: "linux"},
			command:    platformCommandInstall,
			wantStatus: platformSupportSupported,
		},
		{
			name:       "linux POSIX installed control is supported",
			platform:   hostPlatform{GOOS: "linux"},
			command:    platformCommandInstalledPOSIX,
			wantStatus: platformSupportSupported,
		},
		{
			name:                "linux PowerShell control is unsupported",
			platform:            hostPlatform{GOOS: "linux"},
			command:             platformCommandInstalledPowerShell,
			wantStatus:          platformSupportUnsupported,
			wantReasonSubstring: "Linux support path",
		},
		{
			name:              "macOS install requires Docker Desktop",
			platform:          hostPlatform{GOOS: "darwin"},
			command:           platformCommandInstall,
			wantStatus:        platformSupportSupported,
			wantDockerDesktop: true,
		},
		{
			name:              "macOS runtime commands require Docker Desktop",
			platform:          hostPlatform{GOOS: "darwin"},
			command:           platformCommandStagingRuntime,
			wantStatus:        platformSupportSupported,
			wantDockerDesktop: true,
		},
		{
			name:                "macOS list uninstall is unsupported",
			platform:            hostPlatform{GOOS: "darwin"},
			command:             platformCommandUninstallList,
			wantStatus:          platformSupportUnsupported,
			wantReasonSubstring: "Linux/systemd service discovery",
		},
		{
			name:                "unknown platforms are unsupported",
			platform:            hostPlatform{GOOS: "plan9"},
			command:             platformCommandStage,
			wantStatus:          platformSupportUnsupported,
			wantReasonSubstring: "unsupported host platform",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.platform.commandSupport(test.command)
			if got.Status != test.wantStatus {
				t.Fatalf("status = %s, want %s", got.Status, test.wantStatus)
			}
			if got.RequiresDockerDesktop != test.wantDockerDesktop {
				t.Fatalf("requires Docker Desktop = %v, want %v", got.RequiresDockerDesktop, test.wantDockerDesktop)
			}
			if test.wantReasonSubstring != "" && !strings.Contains(got.Reason, test.wantReasonSubstring) {
				t.Fatalf("reason = %q, want substring %q", got.Reason, test.wantReasonSubstring)
			}
		})
	}
}

func TestDetectDockerRuntimeDetectsDockerDesktop(t *testing.T) {
	dir := t.TempDir()
	dockerPath := filepath.Join(dir, "docker")
	script := "#!/bin/sh\nprintf '{\"OperatingSystem\":\"Docker Desktop\",\"ServerVersion\":\"29.5.3\"}\\n'\n"
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := detectDockerRuntime(context.Background(), CommandSpec{Name: dockerPath}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if info.Runtime != dockerRuntimeDockerDesktop {
		t.Fatalf("runtime = %s, want %s", info.Runtime, dockerRuntimeDockerDesktop)
	}
	if info.ServerVersion != "29.5.3" {
		t.Fatalf("server version = %q", info.ServerVersion)
	}
}

func TestDetectDockerRuntimeDetectsLinuxEngine(t *testing.T) {
	dir := t.TempDir()
	dockerPath := filepath.Join(dir, "docker")
	script := "#!/bin/sh\nprintf '{\"OperatingSystem\":\"Ubuntu 24.04\",\"ServerVersion\":\"29.5.3\"}\\n'\n"
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := detectDockerRuntime(context.Background(), CommandSpec{Name: dockerPath}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if info.Runtime != dockerRuntimeLinuxEngine {
		t.Fatalf("runtime = %s, want %s", info.Runtime, dockerRuntimeLinuxEngine)
	}
}

func TestUninstallNeedsRootDependsOnPlatform(t *testing.T) {
	restore := stubHostPlatform(t, hostPlatform{GOOS: "darwin"})
	defer restore()
	if UninstallNeedsRoot(UninstallOptions{}) {
		t.Fatal("darwin Docker Desktop uninstall should not require root")
	}
	restore()

	restore = stubHostPlatform(t, hostPlatform{GOOS: "linux"})
	defer restore()
	if !UninstallNeedsRoot(UninstallOptions{}) {
		t.Fatal("linux systemd uninstall should require root")
	}
}

func stubHostPlatform(t *testing.T, platform hostPlatform) func() {
	t.Helper()
	previous := detectHostPlatform
	detectHostPlatform = func() hostPlatform {
		return platform
	}
	return func() {
		detectHostPlatform = previous
	}
}
