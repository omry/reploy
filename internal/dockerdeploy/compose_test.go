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
	"time"
)

func TestRunCommandForwardsStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires a POSIX host")
	}
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
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires a POSIX host")
	}
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
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires a POSIX host")
	}
	restore := stubDockerPreflight(t, func(context.Context, CommandSpec, time.Duration) (string, error) {
		t.Fatal("docker preflight should not run for non-docker commands")
		return "", nil
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
	restore := stubDockerPreflight(t, func(context.Context, CommandSpec, time.Duration) (string, error) {
		called = true
		return "", preflightErr
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
	restore := stubDockerPreflight(t, func(_ context.Context, _ CommandSpec, timeout time.Duration) (string, error) {
		gotTimeout = timeout
		return "", preflightErr
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

func TestCommandRunnerForPinnedDockerEndpointV1PinsEveryCommand(t *testing.T) {
	const endpoint = "unix:///session-engine.sock"
	var commands []CommandSpec
	run, err := commandRunnerForPinnedDockerEndpointV1(endpoint, func(spec CommandSpec, _ RunOptions) error {
		commands = append(commands, spec)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"container", "inspect", "id"}, {"container", "rm", "--force", "id"}} {
		if err := run(CommandSpec{Name: "docker", Args: args}, RunOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, command := range commands {
		if got := commandEnvironmentValueV1(command, "DOCKER_HOST"); got != endpoint {
			t.Fatalf("pinned Docker host = %q", got)
		}
		if contextName, found := commandSpecEnvironmentValueV1(command, "DOCKER_CONTEXT"); !found || contextName != "" {
			t.Fatalf("pinned Docker context = %q, found=%t", contextName, found)
		}
	}
	if _, err := commandRunnerForPinnedDockerEndpointV1("tcp://remote.example:2376", runCommandWithoutDockerPreflight); err == nil {
		t.Fatal("remote endpoint was accepted")
	}
}

func TestRunCommandWithoutDockerPreflightRunsKnownFollowup(t *testing.T) {
	dir := t.TempDir()
	writeFakeCommand(
		t,
		dir,
		"docker",
		"#!/bin/sh\nprintf 'followup:%s\\n' \"$*\"\n",
		"@echo off\r\necho followup:%*\r\n",
	)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	restore := stubDockerPreflight(t, func(context.Context, CommandSpec, time.Duration) (string, error) {
		t.Fatal("known Docker follow-up repeated preflight")
		return "", nil
	})
	defer restore()
	var stdout strings.Builder
	if err := runCommandWithoutDockerPreflight(
		CommandSpec{Name: "docker", Args: []string{"exec", "validation"}, Env: []string{
			"DOCKER_HOST=unix:///var/run/docker.sock",
			"DOCKER_CONTEXT=",
		}},
		RunOptions{Stdout: &stdout},
	); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != "followup:exec validation" {
		t.Fatalf("follow-up output = %q", stdout.String())
	}
}

func TestRunCommandWithoutDockerPreflightRejectsUnpinnedDocker(t *testing.T) {
	err := runCommandWithoutDockerPreflight(
		CommandSpec{Name: "docker", Args: []string{"exec", "validation"}},
		RunOptions{},
	)
	if err == nil || !strings.Contains(err.Error(), "requires a verified pinned local endpoint") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunDockerCommandPreflightsAbsoluteExecutable(t *testing.T) {
	dir := t.TempDir()
	dockerPath := writeFakeCommand(
		t,
		dir,
		"configured-docker",
		"#!/bin/sh\nexit 0\n",
		"@echo off\r\nexit /b 0\r\n",
	)
	preflightCalled := false
	restore := stubDockerPreflight(t, func(_ context.Context, spec CommandSpec, _ time.Duration) (string, error) {
		preflightCalled = true
		if spec.Name != dockerPath {
			t.Fatalf("preflight executable = %q, want %q", spec.Name, dockerPath)
		}
		return "unix:///var/run/docker.sock", nil
	})
	defer restore()

	if err := runDockerCommand(CommandSpec{Name: dockerPath, Args: []string{"version"}}, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if !preflightCalled {
		t.Fatal("absolute Docker executable bypassed preflight")
	}
}

func TestRunDockerCommandPinsVerifiedEndpoint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires a POSIX host")
	}
	dir := t.TempDir()
	versionEnvironmentPath := filepath.Join(dir, "version.env")
	commandEnvironmentPath := filepath.Join(dir, "command.env")
	dockerPath := writeFakeCommand(
		t,
		dir,
		"docker",
		"#!/bin/sh\ncase \"$1\" in\n  context) printf 'unix:///verified/docker.sock\\n' ;;\n  version) printf '%s|%s\\n' \"$DOCKER_HOST\" \"$DOCKER_CONTEXT\" > \"$DOCKER_VERSION_ENV\"; printf '29.5.3\\n' ;;\n  create) printf '%s|%s\\n' \"$DOCKER_HOST\" \"$DOCKER_CONTEXT\" > \"$DOCKER_COMMAND_ENV\" ;;\nesac\n",
		"@exit /b 1\r\n",
	)
	spec := CommandSpec{
		Name: dockerPath,
		Args: []string{"create"},
		Env: []string{
			"DOCKER_HOST=",
			"DOCKER_CONTEXT=",
			"DOCKER_VERSION_ENV=" + versionEnvironmentPath,
			"DOCKER_COMMAND_ENV=" + commandEnvironmentPath,
		},
	}
	if err := runDockerCommand(spec, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{versionEnvironmentPath, commandEnvironmentPath} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(content)); got != "unix:///verified/docker.sock|" {
			t.Fatalf("%s environment = %q", filepath.Base(path), got)
		}
	}
	if spec.Env[0] != "DOCKER_HOST=" || spec.Env[1] != "DOCKER_CONTEXT=" {
		t.Fatalf("caller command environment was mutated: %q", spec.Env)
	}
}

func TestBindDockerCommandRunnerPinsOneEndpointForEveryCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires a POSIX host")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "commands.log")
	dockerPath := writeFakeCommand(
		t,
		dir,
		"docker",
		"#!/bin/sh\nprintf '%s|%s|%s\\n' \"$*\" \"$DOCKER_HOST\" \"$DOCKER_CONTEXT\" >> \"$DOCKER_COMMAND_LOG\"\n",
		"@exit /b 1\r\n",
	)
	preflights := 0
	restore := stubDockerPreflight(t, func(_ context.Context, spec CommandSpec, timeout time.Duration) (string, error) {
		preflights++
		if spec.Name != dockerPath || timeout != 3*time.Second {
			t.Fatalf("preflight = %#v / %s", spec, timeout)
		}
		return "unix:///first/docker.sock", nil
	})
	defer restore()

	run, err := bindDockerCommandRunnerV1(
		context.Background(),
		CommandSpec{Name: dockerPath},
		3*time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"create", "demo"}, {"start", "demo"}, {"rm", "--force", "demo"}} {
		if err := run(
			CommandSpec{Name: dockerPath, Args: args, Env: []string{"DOCKER_COMMAND_LOG=" + logPath}},
			RunOptions{},
		); err != nil {
			t.Fatal(err)
		}
	}
	if preflights != 1 {
		t.Fatalf("Docker preflights = %d, want 1", preflights)
	}
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"create demo|unix:///first/docker.sock|",
		"start demo|unix:///first/docker.sock|",
		"rm --force demo|unix:///first/docker.sock|",
		"",
	}, "\n")
	if string(content) != want {
		t.Fatalf("commands:\n%s\nwant:\n%s", content, want)
	}
	if err := run(CommandSpec{Name: filepath.Join(dir, "other-docker"), Args: []string{"info"}}, RunOptions{}); err == nil ||
		!strings.Contains(err.Error(), "changed executable") {
		t.Fatalf("changed executable error = %v", err)
	}
}

func TestCheckDockerResponsiveUsesServerVersion(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "argv.log")
	dockerPath := writeFakeCommand(
		t,
		dir,
		"docker",
		"#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$DOCKER_ARGV_LOG\"\nprintf '29.5.3\\n'\n",
		"@echo off\r\necho %* > \"%DOCKER_ARGV_LOG%\"\r\necho 29.5.3\r\n",
	)

	_, err := checkDockerResponsive(
		context.Background(),
		CommandSpec{Name: dockerPath, Env: []string{"DOCKER_HOST=unix:///var/run/docker.sock", "DOCKER_CONTEXT=", "DOCKER_ARGV_LOG=" + logPath}},
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

func TestCheckDockerResponsiveSharesOneDeadlineAcrossProbes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell timing fixture requires a POSIX host")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "argv.log")
	dockerPath := writeFakeCommand(
		t,
		dir,
		"docker",
		"#!/bin/sh\nprintf '%s\\n' \"$1\" >> \"$DOCKER_ARGV_LOG\"\nsleep 0.6\nif [ \"$1\" = context ]; then printf 'unix:///var/run/docker.sock\\n'; else printf '29.5.3\\n'; fi\n",
		"@exit /b 1\r\n",
	)

	_, err := checkDockerResponsive(
		context.Background(),
		CommandSpec{Name: dockerPath, Env: []string{"DOCKER_HOST=", "DOCKER_CONTEXT=", "DOCKER_ARGV_LOG=" + logPath}},
		time.Second,
	)
	if err == nil || !strings.Contains(err.Error(), "docker daemon did not respond within 1s") {
		t.Fatalf("error = %v", err)
	}
	content, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if got := strings.Fields(string(content)); !reflect.DeepEqual(got, []string{"context", "version"}) {
		t.Fatalf("Docker probes = %q, want context then version", got)
	}
}

func stubDockerPreflight(t *testing.T, preflight func(context.Context, CommandSpec, time.Duration) (string, error)) func() {
	t.Helper()
	previous := dockerPreflight
	dockerPreflight = preflight
	return func() {
		dockerPreflight = previous
	}
}
