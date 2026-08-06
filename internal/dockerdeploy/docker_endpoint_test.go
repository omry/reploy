package dockerdeploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalDockerEndpointV1(t *testing.T) {
	for _, test := range []struct {
		endpoint string
		want     bool
	}{
		{endpoint: "unix:///var/run/docker.sock", want: true},
		{endpoint: "unix:///home/user/.docker/desktop/docker.sock", want: true},
		{endpoint: "npipe:////./pipe/docker_engine", want: true},
		{endpoint: "npipe:////./pipe/dockerDesktopLinuxEngine", want: true},
		{endpoint: "ssh://builder.example", want: false},
		{endpoint: "tcp://builder.example:2376", want: false},
		{endpoint: "tcp://127.0.0.1:2375", want: false},
		{endpoint: "https://builder.example", want: false},
		{endpoint: "", want: false},
	} {
		if got := localDockerEndpointV1(test.endpoint); got != test.want {
			t.Fatalf("local Docker endpoint %q = %t, want %t", test.endpoint, got, test.want)
		}
	}
}

func TestRequireLocalDockerEndpointV1RejectsDockerHost(t *testing.T) {
	err := requireLocalDockerEndpointV1(
		context.Background(),
		CommandSpec{Name: "command-must-not-run", Env: []string{"DOCKER_HOST=ssh://builder.example", "DOCKER_CONTEXT="}},
		time.Second,
	)
	if err == nil {
		t.Fatal("remote DOCKER_HOST was accepted")
	}
	for _, want := range []string{"ssh://builder.example", "DOCKER_HOST", "not supported"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
}

func TestRequireLocalDockerEndpointV1RejectsRemoteContext(t *testing.T) {
	dir := t.TempDir()
	dockerPath := writeFakeCommand(
		t,
		dir,
		"docker",
		"#!/bin/sh\nprintf 'ssh://builder.example\\n'\n",
		"@echo off\r\necho ssh://builder.example\r\n",
	)
	err := requireLocalDockerEndpointV1(
		context.Background(),
		CommandSpec{Name: dockerPath, Env: []string{"DOCKER_CONTEXT=remote", "DOCKER_HOST=tcp://ignored.example:2376"}},
		time.Second,
	)
	if err == nil {
		t.Fatal("remote Docker context was accepted")
	}
	if !strings.Contains(err.Error(), `Docker context "remote"`) {
		t.Fatalf("error does not identify context: %v", err)
	}
}

func TestEffectiveDockerEndpointV1InspectsActiveContext(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "argv.log")
	dockerPath := writeFakeCommand(
		t,
		dir,
		"docker",
		"#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$DOCKER_ARGV_LOG\"\nprintf 'unix:///var/run/docker.sock\\n'\n",
		"@echo off\r\necho %* > \"%DOCKER_ARGV_LOG%\"\r\necho npipe:////./pipe/docker_engine\r\n",
	)
	endpoint, source, err := effectiveDockerEndpointV1(
		context.Background(),
		CommandSpec{Name: dockerPath, Env: []string{"DOCKER_HOST=", "DOCKER_CONTEXT=", "DOCKER_ARGV_LOG=" + logPath}},
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !localDockerEndpointV1(endpoint) || source != "the active Docker context" {
		t.Fatalf("endpoint = %q from %q", endpoint, source)
	}
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "context inspect --format " + dockerContextHostFormatV1
	if strings.TrimSpace(string(content)) != want {
		t.Fatalf("Docker context argv = %q, want %q", strings.TrimSpace(string(content)), want)
	}
}

func TestEffectiveDockerEndpointV1IgnoresSuccessfulContextWarnings(t *testing.T) {
	dir := t.TempDir()
	dockerPath := writeFakeCommand(
		t,
		dir,
		"docker",
		"#!/bin/sh\nprintf 'configuration warning\\n' >&2\nprintf 'unix:///var/run/docker.sock\\n'\n",
		"@echo off\r\necho configuration warning 1>&2\r\necho npipe:////./pipe/docker_engine\r\n",
	)
	endpoint, source, err := effectiveDockerEndpointV1(
		context.Background(),
		CommandSpec{Name: dockerPath, Env: []string{"DOCKER_HOST=", "DOCKER_CONTEXT="}},
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !localDockerEndpointV1(endpoint) || source != "the active Docker context" {
		t.Fatalf("endpoint = %q from %q", endpoint, source)
	}
}

func TestRequireDefaultLocalDockerEndpointV1RevalidatesActiveContext(t *testing.T) {
	dir := t.TempDir()
	endpointPath := filepath.Join(dir, "endpoint")
	writeFakeCommand(
		t,
		dir,
		"docker",
		"#!/bin/sh\ncat \"$DOCKER_ENDPOINT_FILE\"\n",
		"@echo off\r\ntype \"%DOCKER_ENDPOINT_FILE%\"\r\n",
	)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_ENDPOINT_FILE", endpointPath)

	if err := os.WriteFile(endpointPath, []byte("unix:///var/run/docker.sock\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requireDefaultLocalDockerEndpointV1(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(endpointPath, []byte("ssh://builder.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := requireDefaultLocalDockerEndpointV1(context.Background())
	if err == nil || !strings.Contains(err.Error(), "remote Docker endpoint") {
		t.Fatalf("changed active context was not rejected: %v", err)
	}
}
