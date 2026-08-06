package dockerdeploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProbeBuildKitCapabilitiesLinuxAndDesktop(t *testing.T) {
	tests := []struct {
		name string
		info string
		ctx  string
		kind DockerEngineKind
	}{
		{name: "minimum engine", info: "24.0.0\tlinux\tDebian GNU/Linux 12\n", ctx: "default\n", kind: DockerEngineLinux},
		{name: "linux engine", info: "27.5.1\tlinux\tUbuntu 24.04 LTS\n", ctx: "default\n", kind: DockerEngineLinux},
		{name: "desktop", info: "29.6.1\tlinux\tDocker Desktop\n", ctx: "desktop-linux\n", kind: DockerEngineDesktop},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := func(_ context.Context, args ...string) (string, error) {
				if args[0] == "info" {
					return tt.info, nil
				}
				if args[0] == "context" {
					return tt.ctx, nil
				}
				return "", fmt.Errorf("unexpected args: %v", args)
			}
			capabilities, err := probeBuildKitCapabilities(context.Background(), run)
			if err != nil {
				t.Fatal(err)
			}
			if capabilities.Engine != tt.kind || capabilities.ServerOS != "linux" {
				t.Fatalf("capabilities = %#v", capabilities)
			}
		})
	}
}

func TestProbeBuildKitCapabilitiesRejectsUnsupportedDaemon(t *testing.T) {
	tests := []string{"23.0.6\tlinux\tUbuntu", "29.0.0\twindows\tDocker Desktop"}
	for _, info := range tests {
		run := func(_ context.Context, args ...string) (string, error) {
			if args[0] == "info" {
				return info, nil
			}
			return "default", nil
		}
		if _, err := probeBuildKitCapabilities(context.Background(), run); err == nil {
			t.Fatalf("expected %q to fail", info)
		}
	}
}

func TestExecuteDockerOutputPinsVerifiedEndpoint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires a POSIX host")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "command.env")
	writeFakeCommand(
		t,
		dir,
		"docker",
		"#!/bin/sh\nif [ \"$1\" = context ]; then printf 'unix:///verified/docker.sock\\n'; exit 0; fi\nprintf '%s|%s|%s\\n' \"$*\" \"$DOCKER_HOST\" \"$DOCKER_CONTEXT\" > \"$DOCKER_COMMAND_ENV\"\nprintf 'result\\n'\n",
		"@exit /b 1\r\n",
	)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_COMMAND_ENV", logPath)

	output, err := executeDockerOutput(context.Background(), "image", "inspect", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output) != "result" {
		t.Fatalf("output = %q", output)
	}
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(content)); got != "image inspect demo|unix:///verified/docker.sock|" {
		t.Fatalf("Docker environment = %q", got)
	}
}

func TestProbeBuildKitCapabilitiesRetainsVerifiedNamedContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires a POSIX host")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "command.env")
	writeFakeCommand(
		t,
		dir,
		"docker",
		"#!/bin/sh\nif [ \"$1\" = context ] && [ \"$2\" = inspect ]; then printf 'desktop-linux\\tunix:///verified/docker.sock\\n'; exit 0; fi\nif [ \"$1\" = info ]; then printf '%s|%s\\n' \"$DOCKER_HOST\" \"$DOCKER_CONTEXT\" > \"$DOCKER_COMMAND_ENV\"; printf '29.6.1\\tlinux\\tDocker Desktop\\n'; exit 0; fi\nprintf 'unexpected command: %s\\n' \"$*\" >&2\nexit 1\n",
		"@exit /b 1\r\n",
	)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_COMMAND_ENV", logPath)

	capabilities, err := ProbeBuildKitCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.Context != "desktop-linux" || capabilities.Engine != DockerEngineDesktop {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(content)); got != "unix:///verified/docker.sock|" {
		t.Fatalf("Docker environment = %q", got)
	}
}
