package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/dockerdeploy"
)

func incompatibleStagingFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".reploy"), 0700); err != nil {
		t.Fatal(err)
	}
	// A future state field reproduces a controller unable to read newer state.
	if err := os.WriteFile(filepath.Join(dir, dockerdeploy.StateFileName), []byte(`{"schema":"state-v1","futureField":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestIncompatibleStagingGuidanceAndUsableControlHelp(t *testing.T) {
	dir := incompatibleStagingFixture(t)
	for _, args := range [][]string{
		{"_control", "--dir", dir, "--script-name", "democtl", "restart"},
		{"restart", "--dir", dir},
		{"restart", "--verbose", "--dir", dir},
	} {
		code, stdout, stderr := runCLI(args...)
		if code != 1 || stdout != "" || !strings.Contains(stderr, "unknown field") || !strings.Contains(stderr, "reploy stage --update") || !strings.Contains(stderr, dir) {
			t.Fatalf("args=%v code=%d out=%q err=%q", args, code, stdout, stderr)
		}
	}
	code, stdout, stderr := runCLI("_control", "--dir", dir, "--script-name", "democtl", "--help")
	if code != 0 || !strings.Contains(stdout, "usage: democtl COMMAND") || !strings.Contains(stderr, "reploy stage --update") {
		t.Fatalf("help code=%d out=%q err=%q", code, stdout, stderr)
	}
}

func TestStagingHintDoesNotPrescribeRefreshForOtherFailures(t *testing.T) {
	dir := incompatibleStagingFixture(t)
	for _, message := range []string{"permission denied", "missing image", "Docker i/o timeout"} {
		var out strings.Builder
		printStagingCompatibilityHint(&out, dir, errors.New(message), true)
		if out.Len() != 0 {
			t.Fatalf("unrelated hint: %q", out.String())
		}
	}
	if err := os.WriteFile(filepath.Join(dir, dockerdeploy.StateFileName), []byte(`{"schema":"state-v1","deployment":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	printStagingCompatibilityHint(&out, dir, errors.New("unknown field"), true)
	if out.Len() != 0 {
		t.Fatalf("installed deployment restaging hint: %q", out.String())
	}
}

func TestUnsupportedStagingSchemaOffersRefreshAndControlHelp(t *testing.T) {
	dir := incompatibleStagingFixture(t)
	if err := os.WriteFile(filepath.Join(dir, dockerdeploy.StateFileName), []byte(`{"schema":"state-v2"}`), 0600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCLI("_control", "--dir", dir, "restart")
	if code != 1 || !strings.Contains(stderr, "state-v2") || !strings.Contains(stderr, "reploy stage --update") {
		t.Fatalf("code=%d err=%q", code, stderr)
	}
	code, stdout, stderr := runCLI("_control", "--dir", dir, "--help")
	if code != 0 || !strings.Contains(stdout, "commands:") || !strings.Contains(stderr, "reploy stage --update") {
		t.Fatalf("help code=%d out=%q err=%q", code, stdout, stderr)
	}
}

func TestStagingRefreshHintPreservesLiteralShellArgument(t *testing.T) {
	base := incompatibleStagingFixture(t)
	dir := filepath.Join(base, "stage $HOME $(printf expanded) `printf expanded` 'quoted'")
	if err := os.MkdirAll(filepath.Join(dir, ".reploy"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, dockerdeploy.StateFileName), []byte(`{"schema":"state-v1","futureField":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	var hint strings.Builder
	printStagingCompatibilityHint(&hint, dir, errors.New(`json: unknown field "futureField"`), false)
	var command string
	for _, line := range strings.Split(hint.String(), "\n") {
		if strings.HasPrefix(line, "Try ") {
			command = strings.TrimSuffix(strings.TrimPrefix(line, "Try "), " using the current installed Reploy CLI to refresh them.")
		}
	}
	if command == "" {
		t.Fatal("missing refresh command")
	}
	// Execute the suggested command with a harmless shell-local reploy stub.
	// Its fourth argument must be the literal directory, without expansion.
	shell, args := "sh", []string{"-c", `reploy() { printf '%s' "$4"; }; ` + command}
	if runtime.GOOS == "windows" {
		var err error
		shell, err = exec.LookPath("pwsh")
		if err != nil {
			shell, err = exec.LookPath("powershell")
		}
		if err != nil {
			t.Skip("PowerShell is unavailable")
		}
		args = []string{"-NoProfile", "-NonInteractive", "-Command", `function reploy { [Console]::Write($args[3]) }; ` + command}
	}
	output, err := exec.Command(shell, args...).CombinedOutput()
	if err != nil || string(output) != dir {
		t.Fatalf("refresh command changed literal directory: output=%q want=%q err=%v", output, dir, err)
	}
}
