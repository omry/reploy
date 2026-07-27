package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
)

func TestLoadPrivateWorkloadEnvironmentV1OpensOwnerOnlyRealFile(t *testing.T) {
	dir := t.TempDir()
	content := []byte("# deployment-local\nTOKEN='private value'\nEMPTY=\nPORT=8080\n")
	path := filepath.Join(dir, PrivateWorkloadEnvironmentFileName)
	if created, err := publishPrivateWorkloadEnvironmentFileV1(path, content, false); err != nil || !created {
		t.Fatal(err)
	}
	environment, err := loadPrivateWorkloadEnvironmentV1(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !environment.Present || !bytes.Equal(environment.Raw, content) {
		t.Fatalf("loaded environment = %#v", environment)
	}
	if got, want := string(environment.Payload), "EMPTY=\nPORT=8080\nTOKEN=private value\n\n"; got != want {
		t.Fatalf("payload = %q, want %q", got, want)
	}
}

func TestLoadPrivateWorkloadEnvironmentV1RejectsExposureAndLinksWithoutLeakingValues(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, string)
		want  string
	}{
		{
			name: "group readable",
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("TOKEN=do-not-report\n"), 0o640); err != nil {
					t.Fatal(err)
				}
			},
			want: "permissions",
		},
		{
			name: "symlink",
			setup: func(t *testing.T, path string) {
				t.Helper()
				target := filepath.Join(filepath.Dir(path), "actual")
				if err := os.WriteFile(target, []byte("TOKEN=do-not-report\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			},
			want: "without following links",
		},
		{
			name: "hard link",
			setup: func(t *testing.T, path string) {
				t.Helper()
				target := filepath.Join(filepath.Dir(path), "actual")
				if err := os.WriteFile(target, []byte("TOKEN=do-not-report\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(target, path); err != nil {
					t.Fatal(err)
				}
			},
			want: "hard links",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if runtime.GOOS == "windows" && (test.name == "symlink" || test.name == "hard link") {
				t.Skip("ordinary Windows users cannot create the test link")
			}
			dir := t.TempDir()
			test.setup(t, filepath.Join(dir, PrivateWorkloadEnvironmentFileName))
			_, err := loadPrivateWorkloadEnvironmentV1(dir)
			want := test.want
			if runtime.GOOS == "windows" && test.name == "group readable" {
				want = "ACL"
			}
			if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "do-not-report") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestParsePrivateWorkloadEnvironmentV1RejectsAmbiguousInput(t *testing.T) {
	tests := []struct {
		content string
		want    string
	}{
		{content: "TOKEN\n", want: "NAME=value"},
		{content: "BAD-NAME=value\n", want: "invalid variable name"},
		{content: "TOKEN=first\nTOKEN=second\n", want: "repeats variable"},
		{content: "TOKEN='unterminated\n", want: "not terminated"},
		{content: "TOKEN=\"line\\nfeed\"\n", want: "control character"},
	}
	for _, test := range tests {
		if _, err := parsePrivateWorkloadEnvironmentV1([]byte(test.content)); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("parse %q error = %v, want %q", test.content, err, test.want)
		}
	}
	_, err := parsePrivateWorkloadEnvironmentV1([]byte("PRIVATE_NAME=private-value\nPRIVATE_NAME=other\n"))
	if err == nil || strings.Contains(err.Error(), "PRIVATE_NAME") || strings.Contains(err.Error(), "private-value") {
		t.Fatalf("parse diagnostic exposed private material: %v", err)
	}
}

func TestValidatePrivateWorkloadEnvironmentIsolationV1RejectsAncestorMountAndRestart(t *testing.T) {
	dir := t.TempDir()
	plan := DockerExecutionPlan{Mounts: []MountExecutionPlan{{
		Name: "deployment", Mode: blueprint.MountBind, Source: dir,
	}}}
	if err := validatePrivateWorkloadEnvironmentIsolationV1(dir, plan); err == nil || !strings.Contains(err.Error(), "exposes .env") {
		t.Fatalf("ancestor mount error = %v", err)
	}
	plan = DockerExecutionPlan{Restart: "unless-stopped"}
	if err := validatePrivateWorkloadEnvironmentIsolationV1(dir, plan); err == nil || !strings.Contains(err.Error(), "Reploy-managed restart") {
		t.Fatalf("restart error = %v", err)
	}
}

func TestRenderDockerInputsUsesSecretFreePrivateLauncher(t *testing.T) {
	plan := DockerExecutionPlan{
		EnvironmentID: "demo", Phase: blueprint.PhaseStaged,
		Image: "sha256:image", ContainerName: "demo", NetworkName: "demo",
		RuntimeUser:        RuntimeUserPlan{DockerUser: "1000:1000"},
		PrivateEnvironment: true,
		Workload:           &WorkloadExecutionPlan{Argv: []string{"/opt/demo", "serve"}},
	}
	rendered, err := RenderDockerInputs(plan, "democtl")
	if err != nil {
		t.Fatal(err)
	}
	compose := string(rendered.Compose)
	for _, want := range []string{"stdin_open: true", "reploy_private_environment_ready", "/opt/demo", "serve"} {
		if !strings.Contains(compose, want) {
			t.Fatalf("Compose missing %q:\n%s", want, compose)
		}
	}
	if !strings.Contains(compose, "$$reploy_private_environment_line") ||
		!strings.Contains(compose, "$$@") ||
		!strings.Contains(compose, "</dev/null") {
		t.Fatalf("Compose launcher does not escape variable references from Compose interpolation:\n%s", compose)
	}
	for _, unwanted := range []string{"env_file:", "TOKEN=", "private value"} {
		if strings.Contains(compose, unwanted) {
			t.Fatalf("Compose contains private environment material %q:\n%s", unwanted, compose)
		}
	}
}

func TestInjectPrivateWorkloadEnvironmentV1UsesOnlyStdin(t *testing.T) {
	environment := privateWorkloadEnvironmentV1{Present: true, Payload: []byte("TOKEN=private value\n\n")}
	var gotSpec CommandSpec
	var gotInput []byte
	err := injectPrivateWorkloadEnvironmentV1(t.Context(), "/usr/bin/docker", "demo", environment, RunOptions{}, func(spec CommandSpec, options RunOptions) error {
		gotSpec = spec
		var err error
		gotInput, err = readAllForPrivateEnvironmentTest(options)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	command := strings.Join(append([]string{gotSpec.Name}, gotSpec.Args...), " ")
	if strings.Contains(command, "private value") || strings.Contains(command, "TOKEN") {
		t.Fatalf("command contains secret: %#v", gotSpec)
	}
	if !reflect.DeepEqual(gotSpec.Args, []string{"exec", "-i", "demo", "/bin/sh", "-c", privateWorkloadEnvironmentRelayV1}) {
		t.Fatalf("relay command = %#v", gotSpec)
	}
	want := []byte("TOKEN=private value\n\n")
	if !bytes.Equal(gotInput, want) {
		t.Fatalf("relay input = %q, want %q", gotInput, want)
	}
}

func readAllForPrivateEnvironmentTest(options RunOptions) ([]byte, error) {
	if options.Stdin == nil || options.Stdout != nil || options.Stderr != nil {
		return nil, errors.New("private injection must use isolated stdin and captured output")
	}
	var output bytes.Buffer
	_, err := output.ReadFrom(options.Stdin)
	return output.Bytes(), err
}

func TestStartAndInjectPrivateWorkloadEnvironmentV1CleansFailedContainer(t *testing.T) {
	order := []string{}
	environment := privateWorkloadEnvironmentV1{Present: true, Payload: []byte("TOKEN=value\n\n")}
	err := startAndInjectPrivateWorkloadEnvironmentV1(
		t.Context(),
		CommandSpec{Name: "docker", Args: []string{"compose", "up"}},
		CommandSpec{Name: "docker", Args: []string{"compose", "down"}},
		"demo",
		environment,
		RunOptions{},
		func(spec CommandSpec, _ RunOptions) error {
			order = append(order, strings.Join(spec.Args, " "))
			if len(spec.Args) > 0 && spec.Args[0] == "exec" {
				return errors.New("relay failed")
			}
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "one-shot stdin relay") {
		t.Fatalf("error = %v", err)
	}
	if len(order) != 3 || order[0] != "compose up" || !strings.HasPrefix(order[1], "exec -i demo /bin/sh -c ") || order[2] != "compose down" {
		t.Fatalf("order = %#v", order)
	}
}

func TestPlanAndApplyPrivateEnvironmentInstallPreservesThenReplaces(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "staging")
	target := filepath.Join(root, "installed")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(source, PrivateWorkloadEnvironmentFileName)
	if created, err := publishPrivateWorkloadEnvironmentFileV1(sourcePath, []byte("TOKEN=first\n"), false); err != nil || !created {
		t.Fatal(err)
	}
	document := blueprint.Document{Environment: blueprint.Environment{ID: "demo"}}
	actions, preserve, err := planEnvironmentInstallPathUpdates(document, source, target, InstallScopeUser, nil, false, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0].Kind != PathPreservePrivateEnv || !reflect.DeepEqual(preserve, []string{PrivateWorkloadEnvironmentFileName}) {
		t.Fatalf("initial actions=%#v preserve=%#v", actions, preserve)
	}
	locked := providerInstallPathUpdateFixture(target, actions[0])
	locked.Input.SourceDeploymentDir = source
	if err := applyProviderInstallPathUpdatesWithV1(t.Context(), locked, providerInstallPathUpdateBackendV1{
		volumeExists: func(context.Context, string) (bool, error) { return false, nil },
		run:          func(CommandSpec, RunOptions) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(target, PrivateWorkloadEnvironmentFileName)
	if got, err := os.ReadFile(targetPath); err != nil || string(got) != "TOKEN=first\n" {
		t.Fatalf("initial installed environment = %q, %v", got, err)
	}
	if err := os.WriteFile(sourcePath, []byte("TOKEN=second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	actions, _, err = planEnvironmentInstallPathUpdates(document, source, target, InstallScopeUser, nil, false, "linux")
	if err != nil {
		t.Fatal(err)
	}
	locked.Plan.PathUpdates = actions
	if err := applyProviderInstallPathUpdatesWithV1(t.Context(), locked, providerInstallPathUpdateBackendV1{
		volumeExists: func(context.Context, string) (bool, error) { return false, nil },
		run:          func(CommandSpec, RunOptions) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(targetPath); string(got) != "TOKEN=first\n" {
		t.Fatalf("preserved environment = %q", got)
	}
	actions, _, err = planEnvironmentInstallPathUpdates(document, source, target, InstallScopeUser, []string{PrivateWorkloadEnvironmentFileName}, false, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0].Kind != PathReplacePrivateEnv {
		t.Fatalf("replace actions = %#v", actions)
	}
	locked.Plan.PathUpdates = actions
	if err := applyProviderInstallPathUpdatesWithV1(t.Context(), locked, providerInstallPathUpdateBackendV1{
		volumeExists: func(context.Context, string) (bool, error) { return false, nil },
		run:          func(CommandSpec, RunOptions) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(targetPath); string(got) != "TOKEN=second\n" {
		t.Fatalf("replaced environment = %q", got)
	}
}

func TestPrivateWorkloadEnvironmentRealDockerIsolation(t *testing.T) {
	if os.Getenv("REPLOY_TEST_PRIVATE_ENV_DOCKER") != "1" {
		t.Skip("set REPLOY_TEST_PRIVATE_ENV_DOCKER=1 to run the real-Docker private environment test")
	}
	container := "reploy-private-env-test-" + strconv.Itoa(os.Getpid())
	run := func(spec CommandSpec, options RunOptions) error {
		options.Context = t.Context()
		return runCommandWithoutDockerPreflight(spec, options)
	}
	_ = run(CommandSpec{Name: "docker", Args: []string{"rm", "--force", container}}, RunOptions{})
	t.Cleanup(func() {
		_ = run(CommandSpec{Name: "docker", Args: []string{"rm", "--force", container}}, RunOptions{})
	})
	script := privateWorkloadEnvironmentLauncherV1
	create := CommandSpec{Name: "docker", Args: []string{
		"create", "--name", container, "-i", "--user", "65532:65532", "--entrypoint", "/bin/sh", "python:3.11-slim",
		"-c", script, "reploy-private-environment",
		"python", "-c", `import os,time; name="REPLOY_"+"PRIVATE_"+"TEST"; value=os.environ.get(name); data=os.read(0, 1); print("ENV_PRESENT", value is not None, "LENGTH", len(value or ""), "STDIN_EOF", data == b"", flush=True); time.sleep(20)`,
	}}
	if err := run(create, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := run(CommandSpec{Name: "docker", Args: []string{"start", container}}, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	secret := "private-docker-test-value"
	environment := privateWorkloadEnvironmentV1{Present: true, Payload: []byte("REPLOY_PRIVATE_TEST=" + secret + "\n\n")}
	started := time.Now()
	if err := injectPrivateWorkloadEnvironmentV1(t.Context(), "docker", container, environment, RunOptions{}, run); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("private injection did not detach promptly: %s", elapsed)
	}
	var inspect bytes.Buffer
	if err := run(CommandSpec{Name: "docker", Args: []string{"inspect", container}}, RunOptions{Stdout: &inspect}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(inspect.String(), secret) || strings.Contains(inspect.String(), "REPLOY_PRIVATE_TEST") {
		t.Fatal("Docker metadata contains private workload environment material")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var logs bytes.Buffer
		if err := run(CommandSpec{Name: "docker", Args: []string{"logs", container}}, RunOptions{Stdout: &logs}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(logs.String(), "ENV_PRESENT True LENGTH 25 STDIN_EOF True") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("workload did not observe injected environment:\n%s", logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}
