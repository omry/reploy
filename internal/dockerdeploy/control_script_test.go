package dockerdeploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func writeFakeEmbeddedReploy(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, embeddedRuntimeFileName())
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `#!/bin/sh
if [ -n "${REPLOY_ARGS_FILE:-}" ]; then
  printf '%s\n' "$@" > "$REPLOY_ARGS_FILE"
fi
if [ -n "${REPLOY_FAKE_OUTPUT:-}" ]; then
  printf '%s' "$REPLOY_FAKE_OUTPUT"
fi
if [ -n "${REPLOY_FAKE_EXIT:-}" ]; then
  exit "$REPLOY_FAKE_EXIT"
fi
`
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDeployedControlScriptTreatsTargetPathAsData(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX control script")
	}
	targetDir := filepath.Join(t.TempDir(), "installed-$(printf injected)-'quote")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeEmbeddedReploy(t, targetDir)
	scriptPath := filepath.Join(targetDir, "democtl")
	script := renderControlScript(controlScriptSpec{
		Mode: controlScriptModeDeployed, TargetDir: targetDir, ControlScript: "democtl",
	})
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	argsPath := filepath.Join(t.TempDir(), "args")
	command := exec.Command("sh", scriptPath, "status")
	command.Env = append(os.Environ(), "REPLOY_ARGS_FILE="+argsPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run generated control script: %v\n%s\nscript:\n%s", err, output, script)
	}
	content, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(content)), "\n")
	want := []string{"_control", "--dir", targetDir, "--script-name", "democtl", "status"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %#v, want %#v", got, want)
	}
}
