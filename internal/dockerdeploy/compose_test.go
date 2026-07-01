package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunCommandForwardsStdin(t *testing.T) {
	var stdout strings.Builder
	err := runCommand(
		CommandSpec{Name: "sh", Args: []string{"-c", "cat"}},
		RunOptions{Stdin: strings.NewReader("hello\n"), Stdout: &stdout},
	)
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "hello\n" {
		t.Fatalf("stdout = %q, want stdin echoed", stdout.String())
	}
}

func TestRunCommandCapturesSuppressedOutputOnFailure(t *testing.T) {
	err := runCommand(
		CommandSpec{Name: "sh", Args: []string{"-c", "echo useful failure; exit 1"}},
		RunOptions{},
	)
	if err == nil {
		t.Fatal("err = nil, want failure")
	}
	for _, want := range []string{
		"sh failed: exit status 1",
		"command output:",
		"useful failure",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q:\n%s", want, err)
		}
	}
}

func TestRunCommandSkipsDockerPreflightForNonDockerCommand(t *testing.T) {
	restore := stubDockerPreflight(t, func(context.Context, CommandSpec, time.Duration) error {
		t.Fatal("docker preflight should not run for non-docker commands")
		return nil
	})
	defer restore()

	var stdout strings.Builder
	err := runCommand(
		CommandSpec{Name: "sh", Args: []string{"-c", "printf ok"}},
		RunOptions{Stdout: &stdout},
	)
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "ok" {
		t.Fatalf("stdout = %q, want ok", stdout.String())
	}
}

func TestRunCommandChecksDockerBeforeDockerCommand(t *testing.T) {
	preflightErr := errors.New("daemon stuck")
	called := false
	restore := stubDockerPreflight(t, func(context.Context, CommandSpec, time.Duration) error {
		called = true
		return preflightErr
	})
	defer restore()

	err := runCommand(CommandSpec{Name: "docker", Args: []string{"run", "should-not-run"}}, RunOptions{})
	if !called {
		t.Fatal("docker preflight was not called")
	}
	if !errors.Is(err, preflightErr) {
		t.Fatalf("err = %v, want %v", err, preflightErr)
	}
}

func TestRunCommandPassesDockerPreflightTimeout(t *testing.T) {
	preflightErr := errors.New("stop after preflight")
	var gotTimeout time.Duration
	restore := stubDockerPreflight(t, func(_ context.Context, _ CommandSpec, timeout time.Duration) error {
		gotTimeout = timeout
		return preflightErr
	})
	defer restore()

	err := runCommand(CommandSpec{Name: "docker", Args: []string{"version"}}, RunOptions{DockerPreflightTimeout: time.Second})
	if !errors.Is(err, preflightErr) {
		t.Fatalf("err = %v, want %v", err, preflightErr)
	}
	if gotTimeout != time.Second {
		t.Fatalf("docker preflight timeout = %s, want 1s", gotTimeout)
	}
}

func TestCheckDockerResponsiveUsesServerVersion(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "argv.log")
	dockerPath := filepath.Join(dir, "docker")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$DOCKER_ARGV_LOG\"\nprintf '29.5.3\\n'\n"
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	err := checkDockerResponsive(
		context.Background(),
		CommandSpec{Name: dockerPath, Env: []string{"DOCKER_ARGV_LOG=" + logPath}},
		defaultDockerPreflightTimeout,
	)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(content)) != "version --format {{.Server.Version}}" {
		t.Fatalf("docker preflight argv = %q", strings.TrimSpace(string(content)))
	}
}

func stubDockerPreflight(t *testing.T, preflight func(context.Context, CommandSpec, time.Duration) error) func() {
	t.Helper()
	previous := dockerPreflight
	dockerPreflight = preflight
	return func() {
		dockerPreflight = previous
	}
}
