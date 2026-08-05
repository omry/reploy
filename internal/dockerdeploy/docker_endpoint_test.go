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
