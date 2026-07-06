package dockerdeploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "_control" {
		os.Exit(runFakeEmbeddedReployControl(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "app" {
		os.Exit(runFakeEmbeddedReployApp(os.Args[2:]))
	}
	detectHostPlatform = func() hostPlatform {
		return hostPlatform{GOOS: "linux"}
	}
	os.Exit(m.Run())
}

func runFakeEmbeddedReployControl(args []string) int {
	dir := "."
	scriptName := "democtl"
	commandArgs := []string{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--dir":
			index++
			if index < len(args) {
				dir = args[index]
			}
		case "--script-name":
			index++
			if index < len(args) {
				scriptName = args[index]
			}
		default:
			commandArgs = append(commandArgs, args[index:]...)
			index = len(args)
		}
	}
	if len(commandArgs) == 0 || commandArgs[0] == "--help" || commandArgs[0] == "help" {
		fmt.Printf("usage: %s COMMAND [ARGS...]\ncommands:\n  config check\n", scriptName)
		return 0
	}
	switch commandArgs[0] {
	case "status", "ps":
		if path := os.Getenv("DOCKER_ARGS_FILE"); path != "" {
			_ = os.WriteFile(path, []byte(strings.Join([]string{
				"compose",
				"--project-name",
				"demo-project",
				"--project-directory",
				dir,
				"ps",
			}, "\n")+"\n"), 0o644)
		}
		fmt.Printf("%s docker output\n", fakeEmbeddedOutputPrefix(dir))
		return 0
	case "up":
		return runFakeEmbeddedReployControlUp(dir)
	}
	if len(commandArgs) >= 2 && commandArgs[0] == "bootstrap" && commandArgs[1] == "plugin" {
		ensureFakeManagedFile(dir)
	}
	if len(commandArgs) >= 2 && commandArgs[0] == "config" && commandArgs[1] == "check" || len(commandArgs) >= 2 && commandArgs[0] == "bootstrap" && commandArgs[1] == "plugin" {
		return runFakeEmbeddedReployApp(append([]string{"--deployed-only", "--dir", dir}, commandArgs...))
	}
	fmt.Fprintf(os.Stderr, "unknown command: %s\n", commandArgs[0])
	return 2
}

func runFakeEmbeddedReployControlUp(dir string) int {
	if path := os.Getenv("DOCKER_ARGS_FILE"); path != "" {
		_ = os.WriteFile(path, []byte(strings.Join([]string{
			"---",
			"compose",
			"up",
			"---",
			"compose",
			"down",
			"--remove-orphans",
			"---",
			"compose",
			"up",
		}, "\n")+"\n"), 0o644)
	}
	fmt.Fprintln(os.Stderr, "Error response from daemon: failed to set up container networking: network b2f601ad24f6dbb403c8f25b418d314854c35d7fc33ac351355b45d12937cbb3 not found")
	fmt.Printf("%s detected stale Docker network state; running down --remove-orphans and retrying up\n", fakeEmbeddedOutputPrefix(dir))
	fmt.Println("docker output")
	return 0
}

func ensureFakeManagedFile(dir string) {
	path := filepath.Join(dir, ".arbiter.env")
	if _, err := os.Stat(path); err != nil && os.IsNotExist(err) {
		_ = os.WriteFile(path, []byte{}, 0o600)
	}
	if os.Getenv("CHOWN_ARGS_FILE") == "" {
		return
	}
	containerUser := ""
	if content, err := os.ReadFile(filepath.Join(dir, DockerEnvFileName)); err == nil {
		for _, line := range strings.Split(string(content), "\n") {
			if strings.HasPrefix(line, "REPLOY_CONTAINER_USER=") {
				containerUser = strings.TrimPrefix(line, "REPLOY_CONTAINER_USER=")
			}
		}
	}
	if containerUser != "" {
		_ = os.WriteFile(os.Getenv("CHOWN_ARGS_FILE"), []byte(containerUser+"\n"+path+"\n"), 0o644)
	}
}

func runFakeEmbeddedReployApp(args []string) int {
	dir := "."
	commandArgs := []string{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--deployed-only":
		case "--dir":
			index++
			if index < len(args) {
				dir = args[index]
			}
		default:
			if strings.HasPrefix(arg, "--dir=") {
				dir = strings.TrimPrefix(arg, "--dir=")
				continue
			}
			commandArgs = append(commandArgs, arg)
		}
	}
	if path := os.Getenv("DOCKER_ARGS_FILE"); path != "" {
		_ = os.WriteFile(path, []byte(strings.Join(fakeEmbeddedDockerArgs(dir, commandArgs), "\n")+"\n"), 0o644)
	}
	fmt.Printf("%s docker output\n", fakeEmbeddedOutputPrefix(dir))
	return 0
}

func fakeEmbeddedDockerArgs(dir string, commandArgs []string) []string {
	commandName := "config_check"
	forwarded := []string{}
	if len(commandArgs) >= 2 && commandArgs[0] == "bootstrap" && commandArgs[1] == "plugin" {
		commandName = "bootstrap_plugin"
		if len(commandArgs) > 2 {
			forwarded = commandArgs[2:]
		}
	} else if len(commandArgs) >= 2 && commandArgs[0] == "config" && commandArgs[1] == "check" {
		forwarded = commandArgs[2:]
	}
	result := []string{
		"compose",
		"--project-name",
		"demo-project",
		"--project-directory",
		dir,
		"run",
		"--rm",
		"--no-deps",
		"REPLOY_CONTAINER_COMMAND=" + commandName,
		fmt.Sprintf("REPLOY_FORWARDED_ARGC=%d", len(forwarded)),
	}
	for index, arg := range forwarded {
		result = append(result, fmt.Sprintf("REPLOY_FORWARDED_ARG_%d=%s", index, arg))
	}
	result = append(result,
		"REPLOY_CONFIG_CONTAINER_DIR=/conf",
		"REPLOY_CONFIG_MOUNT=rw",
		"REPLOY_APP_COMMAND_PREFIX="+defaultString(os.Getenv("REPLOY_APP_COMMAND_PREFIX"), "reploy app"),
	)
	if color := fakeEmbeddedDemoColor(); color != "" {
		result = append(result, "DEMO_COLOR="+color)
	}
	if columns := os.Getenv("COLUMNS"); columns != "" {
		result = append(result, "COLUMNS="+columns)
	}
	return append(result, "app")
}

func fakeEmbeddedDemoColor() string {
	if value, ok := os.LookupEnv("DEMO_COLOR"); ok {
		return value
	}
	switch strings.ToLower(os.Getenv("REPLOY_COLOR")) {
	case "always":
		return "always"
	case "never":
		return "never"
	default:
		return ""
	}
}

func fakeEmbeddedOutputPrefix(dir string) string {
	label := "[DEPLOYED : demo]"
	if content, err := os.ReadFile(filepath.Join(dir, StateFileName)); err == nil && strings.Contains(string(content), `"phase": "staged"`) {
		label = "[STAGING : demo]"
	}
	if strings.EqualFold(os.Getenv("REPLOY_COLOR"), "always") {
		return "\x1b[38;5;117m" + label + "\x1b[0m"
	}
	return label
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
	dockerPath := writeFakeCommand(
		t,
		dir,
		"docker",
		"#!/bin/sh\nprintf '{\"OperatingSystem\":\"Docker Desktop\",\"ServerVersion\":\"29.5.3\"}\\n'\n",
		"@echo off\r\necho {\"OperatingSystem\":\"Docker Desktop\",\"ServerVersion\":\"29.5.3\"}\r\n",
	)
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
	dockerPath := writeFakeCommand(
		t,
		dir,
		"docker",
		"#!/bin/sh\nprintf '{\"OperatingSystem\":\"Ubuntu 24.04\",\"ServerVersion\":\"29.5.3\"}\\n'\n",
		"@echo off\r\necho {\"OperatingSystem\":\"Ubuntu 24.04\",\"ServerVersion\":\"29.5.3\"}\r\n",
	)
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
