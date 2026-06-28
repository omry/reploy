package dockerdeploy

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func TestRuntimeCommandActions(t *testing.T) {
	dir, projectName := makeRuntimeDeployment(t)
	cases := []struct {
		action string
		suffix []string
	}{
		{action: "up", suffix: []string{"up", "-d"}},
		{action: "restart", suffix: []string{"up", "-d", "--force-recreate"}},
		{action: "down", suffix: []string{"down", "--remove-orphans"}},
		{action: "ps", suffix: []string{"ps"}},
		{action: "status", suffix: []string{"ps"}},
		{action: "logs", suffix: []string{"logs", "--timestamps"}},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			spec, err := RuntimeCommand(dir, tc.action)
			if err != nil {
				t.Fatal(err)
			}
			if spec.Name != "docker" {
				t.Fatalf("name = %q", spec.Name)
			}
			if !containsAdjacent(spec.Args, "--project-name", projectName) {
				t.Fatalf("args did not include staging compose project: %#v", spec.Args)
			}
			if !reflect.DeepEqual(spec.Args[len(spec.Args)-len(tc.suffix):], tc.suffix) {
				t.Fatalf("suffix = %#v, want %#v", spec.Args[len(spec.Args)-len(tc.suffix):], tc.suffix)
			}
		})
	}
}

func TestRuntimeCommandCanFollowLogs(t *testing.T) {
	dir, projectName := makeRuntimeDeployment(t)
	spec, err := RuntimeCommandWithOptions(dir, "logs", RuntimeCommandOptions{Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	if !containsAdjacent(spec.Args, "--project-name", projectName) {
		t.Fatalf("args did not include staging compose project: %#v", spec.Args)
	}
	suffix := []string{"logs", "--timestamps", "-f"}
	if !reflect.DeepEqual(spec.Args[len(spec.Args)-len(suffix):], suffix) {
		t.Fatalf("suffix = %#v, want %#v", spec.Args[len(spec.Args)-len(suffix):], suffix)
	}
}

func TestRuntimeUpAutomaticallyPreparesBundle(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: dir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	commands := []string{}
	restoreBundle := stubSuccessfulBundlePrepare(t, &commands)
	defer restoreBundle()
	restoreRuntime := stubRuntimeRunner(func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, strings.Join(spec.Args[len(spec.Args)-2:], " "))
		return nil
	})
	defer restoreRuntime()

	if err := Runtime(RuntimeOptions{Dir: dir, Action: "up"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"build", "check", "up -d"}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func TestRuntimeUpVerboseStreamsBundlePrepareOutput(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: dir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	commands := []string{}
	restoreBundle := stubBundleRunner(func(spec CommandSpec, options RunOptions) error {
		if options.Stdout != &stdout || options.Stderr != &stderr {
			t.Fatalf("bundle prepare should stream verbose output: %#v", options)
		}
		switch {
		case containsInOrder(spec.Args, []string{"wheel", "--no-cache-dir"}):
			commands = append(commands, "build")
			wheelhouse := hostPathForContainerMount(t, spec.Args, "/wheelhouse")
			return os.WriteFile(filepath.Join(wheelhouse, "demo_suite-1.2.3-py3-none-any.whl"), []byte("suite\n"), 0o644)
		case containsInOrder(spec.Args, []string{"install", "--no-cache-dir", "--target"}):
			commands = append(commands, "check")
			return nil
		default:
			t.Fatalf("unexpected bundle command: %#v", spec.Args)
			return nil
		}
	})
	defer restoreBundle()
	restoreRuntime := stubRuntimeRunner(func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, strings.Join(spec.Args[len(spec.Args)-2:], " "))
		return nil
	})
	defer restoreRuntime()

	if err := Runtime(RuntimeOptions{Dir: dir, Action: "up", Verbose: true, Stdout: &stdout, Stderr: &stderr}); err != nil {
		t.Fatal(err)
	}
	want := []string{"build", "check", "up -d"}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func TestRuntimeStatusDoesNotPrepareBundle(t *testing.T) {
	dir, _ := makeRuntimeDeployment(t)
	commands := []string{}
	restoreBundle := stubBundleRunner(func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, "bundle")
		return nil
	})
	defer restoreBundle()
	restoreRuntime := stubRuntimeRunner(func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, spec.Args[len(spec.Args)-1])
		return nil
	})
	defer restoreRuntime()

	if err := Runtime(RuntimeOptions{Dir: dir, Action: "status"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"ps"}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func stubRuntimeRunner(run func(CommandSpec, RunOptions) error) func() {
	previous := runRuntimeCommand
	runRuntimeCommand = run
	return func() {
		runRuntimeCommand = previous
	}
}

func makeRuntimeDeployment(t *testing.T) (string, string) {
	t.Helper()
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: dir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	values, err := readDockerEnv(dir)
	if err != nil {
		t.Fatal(err)
	}
	projectName := envValue(values, "REPLOY_CONTAINER_NAME", "")
	if projectName == "" {
		t.Fatal("missing REPLOY_CONTAINER_NAME")
	}
	if err := os.RemoveAll(packDir); err != nil {
		t.Fatal(err)
	}
	return dir, projectName
}

func TestRuntimeCommandUsesInstalledComposeProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ReployInternalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	state := deploy.DeploymentState{
		SchemaVersion: 1,
		Phase:         deploy.PhaseInstalled,
		Install: &deploy.InstallState{
			ComposeProject: "demo-12345678",
		},
	}
	content, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateFileName), append(content, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	spec, err := RuntimeCommandWithOptions(dir, "ps", RuntimeCommandOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsAdjacent(spec.Args, "--project-name", "demo-12345678") {
		t.Fatalf("args did not include installed compose project: %#v", spec.Args)
	}
}

func TestRuntimeCommandRejectsUnknownAction(t *testing.T) {
	dir, _ := makeRuntimeDeployment(t)
	_, err := RuntimeCommand(dir, "explode")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateRuntimeInputsDoesNotRequireAppEnvFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ReployInternalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DockerEnvFileName), []byte("REPLOY_CONFIG_DIR=./conf\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := validateRuntimeInputs(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
