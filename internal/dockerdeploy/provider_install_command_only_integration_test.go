package dockerdeploy

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Exercise the public installed update path with a real Docker engine and a
// packaged CLI (tools/build_reploy), including operator-owned invalid config.
func TestProviderCommandOnlyUpdateDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 and REPLOY_TEST_BINARY to a packaged CLI")
	}
	if runtime.GOOS != "linux" {
		t.Skip("this regression qualifies the Linux Docker-managed user installation")
	}
	binary, err := filepath.Abs(os.Getenv("REPLOY_TEST_BINARY"))
	if err != nil || os.Getenv("REPLOY_TEST_BINARY") == "" {
		t.Fatal("REPLOY_TEST_BINARY must name a packaged Reploy CLI")
	}
	dir := t.TempDir()
	stage, installed := filepath.Join(dir, "stage"), filepath.Join(dir, "installed")
	blueprintPath := filepath.Join(dir, "command-only.blueprint.yaml")
	blueprint := fmt.Sprintf(`
blueprint:
  schema: 1
  version: 0.1.0
  compatibility:
    platforms: [linux/amd64, linux/arm64]
environment:
  id: command-update-%d
  base:
    image: debian:12-slim
  applications:
    shell:
      packages:
        os:
          - package: bash
            exports:
              shell: {executable: /bin/bash}
      executables:
        check: {source: os, binary: shell}
  mounts:
    config: {target: /conf, writable: false, update_policy: preserve}
  commands:
    check:
      executable: shell.check
      trigger: [check]
      native_command: true
      deployed_command: true
      argv: [-c, 'test "$(cat /conf/value)" = edited']
docker:
  mounts:
    config: {extends: environment.mounts.config, mode: managed-bind, source: config}
`, time.Now().UnixNano())
	if err := os.WriteFile(blueprintPath, []byte(blueprint), 0o600); err != nil {
		t.Fatal(err)
	}
	// Any service-manager access fails and leaves an observable marker.
	trapDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(trapDir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "systemctl-called")
	trap := "#!/bin/sh\nprintf called > '" + marker + "'\nexit 99\n"
	if err := os.WriteFile(filepath.Join(trapDir, "systemctl"), []byte(trap), 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(wantSuccess bool, args ...string) string {
		t.Helper()
		executable := binary
		if args[0] == "check" {
			executable = filepath.Join(installed, "appctl")
		}
		command := exec.CommandContext(t.Context(), executable, args...)
		command.Env = append(os.Environ(), "PATH="+trapDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		output, err := command.CombinedOutput()
		if (err == nil) != wantSuccess {
			t.Fatalf("reploy %v: error=%v\n%s", args, err, output)
		}
		t.Logf("reploy %v: success=%v\n%s", args, err == nil, output)
		return string(output)
	}
	run(true, "stage", blueprintPath, "--dir", stage)
	if err := os.MkdirAll(filepath.Join(stage, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "config", "value"), []byte("seed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(true, "build", "--dir", stage)
	installArgs := []string{"install", "--dir", stage, "--to", installed, "--scope", "user", "--no-start"}
	run(true, installArgs...)
	config := filepath.Join(installed, "config", "value")
	for _, content := range [][]byte{[]byte("edited\n"), []byte("invalid operator config\x00\n")} {
		if err := os.WriteFile(config, content, 0o600); err != nil {
			t.Fatal(err)
		}
		valid := bytes.Equal(content, []byte("edited\n"))
		run(valid, "check")
		output := run(true, installArgs...)
		if !strings.Contains(output, "updating existing installation") {
			t.Fatal("normal update did not select the existing installation")
		}
		actual, err := os.ReadFile(config)
		if err != nil || !bytes.Equal(actual, content) {
			t.Fatalf("operator config changed: %q error=%v", actual, err)
		}
		run(valid, "check")
	}
	output := run(false, "down", "--dir", stage)
	if !strings.Contains(output, "no workload") {
		t.Fatalf("standalone down no longer rejects the absent workload: %s", output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("command-only install/update accessed systemctl: %v", err)
	}
}
