package dockerdeploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"gopkg.in/yaml.v3"
)

func TestPrivateWorkloadEnvironmentDockerIntegrationMasksFilesAndInjectsValues(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	image, _ := buildApplicationStartupVerifierIntegrationImage(t, ctx)
	testPrivateWorkloadEnvironmentRuntime(t, ctx, image, RuntimeUserPlan{UID: 12345, GID: 23456, SupplementaryGIDs: []uint32{34567, 45678}, DockerUser: "12345:23456"}, false)
	testNonPrivateWorkloadLaunchRuntime(t, ctx, image, RuntimeUserPlan{UID: 12345, GID: 23456, DockerUser: "12345:23456"}, false)
}

// This opt-in qualification uses an empty, disposable Podman endpoint. The image
// is built on Docker and streamed to that endpoint, without a retained archive.
func TestPrivateWorkloadEnvironmentPodmanIntegrationMasksFilesAndInjectsValues(t *testing.T) {
	endpoint := os.Getenv("REPLOY_HF13_PODMAN_ENDPOINT")
	if runtime.GOOS != "linux" || !strings.HasPrefix(endpoint, "unix://") {
		t.Skip("set REPLOY_HF13_PODMAN_ENDPOINT to a disposable local Podman socket")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	target := func(args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "docker", append([]string{"--host", endpoint}, args...)...)
	}
	version, err := target("version", "--format", "{{json .Server}}").CombinedOutput()
	if err != nil || !strings.Contains(strings.ToLower(string(version)), "podman") {
		t.Fatalf("disposable endpoint must be Podman: %v %s", err, version)
	}
	for _, kind := range []string{"container", "image"} {
		output, err := target(kind, "ls", "--all", "--quiet").CombinedOutput()
		if err != nil || strings.TrimSpace(string(output)) != "" {
			t.Fatalf("Podman qualification requires an empty %s store: %v %s", kind, err, output)
		}
	}
	account := RuntimeUserPlan{UID: uint32(os.Getuid()), GID: uint32(os.Getgid()), DockerUser: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())}
	image, _ := buildApplicationStartupVerifierIntegrationImageForAccount(t, ctx, "debian:bookworm-slim", deploy.ApplicationLocalAccountV1{
		Schema: deploy.ApplicationLocalAccountSchemaV1, Name: "reploy", UID: strconv.Itoa(os.Getuid()), GID: strconv.Itoa(os.Getgid()), Home: environmentTemporaryHome,
	})
	save := exec.CommandContext(ctx, "docker", "image", "save", image)
	stream, err := save.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	save.Stderr = &diagnostics
	load := target("image", "load")
	load.Stdin = stream
	if err := save.Start(); err != nil {
		t.Fatal(err)
	}
	output, loadErr := load.CombinedOutput()
	saveErr := save.Wait()
	if loadErr != nil || saveErr != nil {
		t.Fatalf("transfer qualification image: load=%v save=%v %s %s", loadErr, saveErr, output, diagnostics.String())
	}
	t.Setenv("DOCKER_HOST", endpoint)
	t.Setenv("DOCKER_CONTEXT", "")
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if output, err := exec.CommandContext(cleanupCtx, "docker", "--host", endpoint, "image", "rm", image).CombinedOutput(); err != nil {
			t.Errorf("remove owned Podman qualification image: %v %s", err, output)
		}
	})
	t.Logf("qualified Podman server: %s", version)
	testPrivateWorkloadEnvironmentRuntime(t, ctx, image, account, true)
	testNonPrivateWorkloadLaunchRuntime(t, ctx, image, account, true)
}

func testPrivateWorkloadEnvironmentRuntime(t *testing.T, ctx context.Context, image string, account RuntimeUserPlan, noNetwork bool) {
	t.Helper()
	hostRoot := dockerIntegrationSharedTempDir(t)
	if err := os.Chmod(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	deploymentDir := filepath.Join(hostRoot, "deployment")
	if err := os.Mkdir(deploymentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	privateDir := filepath.Join(deploymentDir, privateRuntimeMetadataDirectoryName)
	if err := os.MkdirAll(privateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	environmentPath := filepath.Join(deploymentDir, PrivateWorkloadEnvironmentFileName)
	if created, err := publishPrivateWorkloadEnvironmentFileV1(
		environmentPath,
		[]byte("TOKEN=initial-private-value\n"),
		false,
	); err != nil || !created {
		t.Fatalf("create private environment = %t, %v", created, err)
	}
	if err := os.WriteFile(filepath.Join(privateDir, "before"), []byte("private-metadata\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Empty shadow masks must not copy host metadata into the 4 KiB tmpfs.
	if err := os.WriteFile(filepath.Join(privateDir, "large"), bytes.Repeat([]byte("synthetic-private-metadata"), 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	environment, err := preparePrivateWorkloadEnvironmentV1(deploymentDir)
	if err != nil {
		t.Fatal(err)
	}
	tokenDigest := sha256.Sum256([]byte("initial-private-value"))
	expectedTokenDigest := hex.EncodeToString(tokenDigest[:])

	unique := fmt.Sprintf("reploy-private-mask-%d-%d", os.Getpid(), time.Now().UnixNano())
	container := unique + "-container"
	groups := append([]uint32{account.GID}, account.SupplementaryGIDs...)
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })
	groupNames := make([]string, len(groups))
	for i, group := range groups {
		groupNames[i] = strconv.FormatUint(uint64(group), 10)
	}
	workloadScript := fmt.Sprintf(`if [ "${TOKEN+x}" != x ]; then echo token-missing; exit 31; fi
if [ "$(printf '%%s' "$TOKEN" | sha256sum | cut -d' ' -f1)" != %s ]; then echo token-mismatch; exit 32; fi
test "$(id -u):$(id -g)" = "%s"
test "$(id -G)" = "%s"
test "$(awk '/^CapEff:/ {print $2}' /proc/self/status)" = "0000000000000000"
test "$(awk '/^CapPrm:/ {print $2}' /proc/self/status)" = "0000000000000000"
test "$(awk '/^CapBnd:/ {print $2}' /proc/self/status)" = "0000000000000000"
test "$(awk '/^NoNewPrivs:/ {print $2}' /proc/self/status)" = "1"
test "$(awk '/^Seccomp:/ {print $2}' /proc/self/status)" = "2"
rm -f "$HOME/.reploy-private-environment" "$HOME/.reploy-private-launch"
printf 'private-home-cleaned\n'
while [ ! -e /host/deployment/ready ]; do sleep 0.05; done
if [ -s /host/deployment/.env ]; then echo env-readable; exit 41; fi
if cat /host/deployment/.reploy/before >/dev/null 2>&1; then echo metadata-readable; exit 42; fi
if cat /host/deployment/.reploy/late >/dev/null 2>&1; then echo late-metadata-readable; exit 43; fi
printf 'private-mask-pass\n'`, expectedTokenDigest, account.DockerUser, strings.Join(groupNames, " "))
	workloadScript = strings.ReplaceAll(workloadScript, "$", "$$")
	plan := DockerExecutionPlan{
		EnvironmentID: "private-mask", DeploymentDir: deploymentDir, Phase: blueprint.PhaseStaged,
		Image: image, ContainerName: container, NetworkName: unique,
		Sandbox:            newApplicationSandboxPlanV1(account),
		PrivateEnvironment: true,
		Workload: &WorkloadExecutionPlan{Argv: []string{
			"/bin/sh", "-eu", "-c",
			workloadScript,
		}},
		Mounts: []MountExecutionPlan{{
			Name: "host", Mode: blueprint.MountBind, Source: hostRoot,
			SourceKind: deploy.RuntimeMountSourceDirectory, Target: "/host", ReadOnly: true,
		}},
	}
	rendered, err := RenderDockerInputs(plan, "private-mask")
	if err != nil {
		t.Fatal(err)
	}
	rendered.Compose = privateEnvironmentQualificationCompose(t, rendered.Compose, noNetwork)
	if bytes.Contains(rendered.Compose, []byte("initial-private-value")) ||
		bytes.Contains(rendered.Compose, []byte("rotated-private-value")) {
		t.Fatal("rendered Compose contains private environment material")
	}
	composePath := filepath.Join(t.TempDir(), "compose.yaml")
	if err := os.WriteFile(composePath, rendered.Compose, 0o600); err != nil {
		t.Fatal(err)
	}
	start := CommandSpec{
		Name:               "docker",
		Args:               []string{"compose", "--project-name", unique, "-f", composePath, "up", "--pull", "never", "-d"},
		applicationCompose: true,
	}
	cleanup := CommandSpec{
		Name: "docker",
		Args: []string{"compose", "--project-name", unique, "-f", composePath, "down", "--remove-orphans"},
	}
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), cleanup.Name, cleanup.Args...).Run()
	})
	operation, err := deploy.AcquireOperationLock(ctx, deploymentDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	binding, err := workloadRuntimeBindingV1(plan)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := dockerPreflight(ctx, start, effectiveDockerPreflightTimeout(0))
	if err != nil {
		t.Fatal(err)
	}
	start = pinDockerEndpointV1(start, endpoint)
	environment.Operation, environment.RuntimeBinding = operation, binding
	relays := 0
	run := func(spec CommandSpec, options RunOptions) error {
		if len(spec.Args) > 0 && spec.Args[0] == "exec" && options.Stdin != nil {
			relays++
		}
		return runDockerCommand(pinDockerEndpointV1(spec, endpoint), options)
	}
	startAndComplete := func(command CommandSpec, environment privateWorkloadEnvironmentV1) error {
		if err := startAndInjectPrivateWorkloadEnvironmentV1(ctx, command, container, plan.Sandbox, environment, RunOptions{}, run); err != nil {
			return err
		}
		lifecycle := &workloadLifecycleV1{command: command, containerName: container, run: run, operation: func(_ context.Context, action func(*deploy.OperationLock) error) error { return action(operation) }}
		if _, _, err := lifecycle.initialize(ctx, binding, true); err != nil {
			return err
		}
		return lifecycle.complete(ctx)
	}
	// Deterministically reproduce a killed controller: Compose created the
	// private launcher, but its first secret relay never ran.
	if err := run(start, RunOptions{Context: ctx}); err != nil {
		t.Fatal(err)
	}
	waitingIdentity := runDockerIntegration(t, ctx, "container", "inspect", "--format", "{{.Id}} {{.State.StartedAt}}", container)
	if err := startAndInjectPrivateWorkloadEnvironmentV1(ctx, start, container, plan.Sandbox, environment, RunOptions{}, run); err == nil || !strings.Contains(err.Error(), "reploy restart") {
		t.Fatalf("uninitialized private workload did not request explicit recovery: %v", err)
	}
	if relays != 0 || runDockerIntegration(t, ctx, "container", "inspect", "--format", "{{.Id}} {{.State.StartedAt}}", container) != waitingIdentity {
		t.Fatal("interrupted-start observation injected secrets or replaced the existing process")
	}
	runDockerIntegration(t, ctx, "container", "rm", "--force", strings.Fields(waitingIdentity)[0])
	if err := startAndComplete(start, environment); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(runDockerIntegration(t, ctx, "logs", container), "private-home-cleaned") {
		if time.Now().After(deadline) {
			t.Fatal("application did not clean its private HOME")
		}
		time.Sleep(10 * time.Millisecond)
	}
	identity := runDockerIntegration(t, ctx, "container", "inspect", "--format", "{{.Id}} {{.State.StartedAt}}", container)
	if err := startAndInjectPrivateWorkloadEnvironmentV1(ctx, start, container, plan.Sandbox, environment, RunOptions{}, run); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(environmentPath, []byte("TOKEN=rotated-private-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rotated, err := loadPrivateWorkloadEnvironmentV1(deploymentDir)
	if err != nil {
		t.Fatal(err)
	}
	rotated.Operation, rotated.RuntimeBinding = operation, binding
	if err := startAndInjectPrivateWorkloadEnvironmentV1(ctx, start, container, plan.Sandbox, rotated, RunOptions{}, run); err != nil {
		t.Fatal(err)
	}
	if relays != 1 || runDockerIntegration(t, ctx, "container", "inspect", "--format", "{{.Id}} {{.State.StartedAt}}", container) != identity {
		t.Fatal("repeated up reinjected private values or replaced the running process")
	}
	for _, transition := range []struct {
		name    string
		content []byte
		missing bool
	}{
		{name: "private-to-empty", content: []byte{}},
		{name: "empty-to-private", content: []byte("TOKEN=changed-private-value\n")},
		{name: "private-to-missing", missing: true},
		{name: "missing-to-private", content: []byte("TOKEN=restored-private-value\n")},
	} {
		t.Run(transition.name, func(t *testing.T) {
			if transition.missing {
				if err := os.Remove(environmentPath); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(environmentPath, transition.content, 0o600); err != nil {
				t.Fatal(err)
			}
			currentEnvironment, err := loadPrivateWorkloadEnvironmentV1(deploymentDir)
			if err != nil {
				t.Fatal(err)
			}
			currentEnvironment.Operation, currentEnvironment.RuntimeBinding = operation, binding
			lifecycle := &workloadLifecycleV1{command: start, containerName: container, run: run, operation: func(_ context.Context, action func(*deploy.OperationLock) error) error { return action(operation) }}
			reused, changed, err := lifecycle.initialize(ctx, binding, currentEnvironment.Present)
			if err != nil || !reused || changed {
				t.Fatalf("presence transition lifecycle reuse: reused=%t changed=%t err=%v", reused, changed, err)
			}
			if err := startAndInjectPrivateWorkloadEnvironmentV1(ctx, start, container, plan.Sandbox, currentEnvironment, RunOptions{}, run); err != nil {
				t.Fatal(err)
			}
			if relays != 1 || runDockerIntegration(t, ctx, "container", "inspect", "--format", "{{.Id}} {{.State.StartedAt}}", container) != identity {
				t.Fatalf("presence transition replaced or reinjected the process: relays=%d", relays)
			}
		})
	}
	// A stopped process and an explicit recreation each get one new relay.
	if err := os.WriteFile(environmentPath, []byte("TOKEN=initial-private-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, recreate := range []bool{false, true} {
		previous := runDockerIntegration(t, ctx, "container", "inspect", "--format", "{{.Id}} {{.State.StartedAt}}", container)
		restart := start
		runDockerIntegration(t, ctx, "stop", "--time", "1", container)
		if recreate {
			restart.Args = append(append([]string(nil), start.Args...), "--force-recreate")
		}
		if err := startAndComplete(restart, environment); err != nil {
			t.Fatal(err)
		}
		if current := runDockerIntegration(t, ctx, "container", "inspect", "--format", "{{.Id}} {{.State.StartedAt}}", container); current == previous {
			t.Fatal("restart did not replace the running process")
		}
	}
	if relays != 3 {
		t.Fatalf("startup, restart and recreation used %d relays; want 3", relays)
	}
	if err := os.WriteFile(filepath.Join(privateDir, "late"), []byte("late-private-metadata\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deploymentDir, "ready"), []byte("ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exitCode := strings.TrimSpace(runDockerIntegration(t, ctx, "wait", container))
	logs := strings.TrimSpace(runDockerIntegration(t, ctx, "logs", container))
	if exitCode != "0" || logs != "private-home-cleaned\nprivate-mask-pass" {
		state := strings.TrimSpace(runDockerIntegration(
			t,
			ctx,
			"container",
			"inspect",
			"--format",
			`{{json .State}}`,
			container,
		))
		t.Fatalf("container exit=%q logs=%q state=%s", exitCode, logs, state)
	}
	testPrivateWorkloadEnvironmentRuntimeFailures(t, ctx, image, account, environment, noNetwork)

}

// Podman's opt-in fixture qualifies private environment transport without host
// network helpers. Docker retains the normal rendered Compose network.
func privateEnvironmentQualificationCompose(t *testing.T, content []byte, noNetwork bool) []byte {
	t.Helper()
	if !noNetwork {
		return content
	}
	var document map[string]any
	if err := yaml.Unmarshal(content, &document); err != nil {
		t.Fatal(err)
	}
	services := document["services"].(map[string]any)
	service := services[composeWorkloadService].(map[string]any)
	service["network_mode"] = "none"
	delete(document, "networks")
	result, err := yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// Exercise actual relay termination and exact-container cleanup, including the
// launcher failing before it creates its FIFO. Payloads stay on stdin.
func testPrivateWorkloadEnvironmentRuntimeFailures(t *testing.T, ctx context.Context, image string, account RuntimeUserPlan, environment privateWorkloadEnvironmentV1, noNetwork bool) {
	t.Helper()
	for _, test := range []struct {
		name, script string
		cancelRelay  bool
		private      bool
	}{
		{name: "missing-fifo-deadline", script: "while true; do sleep 1; done"},
		{name: "caller-cancellation", script: "while true; do sleep 1; done", cancelRelay: true},
		{name: "launcher-exit", script: "sleep 1; exit 31"},
		{name: "application-exec-failure", private: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			name := uniqueDockerIntegrationName("reploy-private-failure")
			plan := DockerExecutionPlan{EnvironmentID: "private-failure", DeploymentDir: t.TempDir(), Phase: blueprint.PhaseStaged,
				Image: image, ContainerName: name, NetworkName: name,
				Sandbox:  newApplicationSandboxPlanV1(account),
				Workload: &WorkloadExecutionPlan{Argv: []string{"/bin/sh", "-eu", "-c", test.script}},
			}
			if test.private {
				plan.PrivateEnvironment = true
				plan.Workload.Argv = []string{"/reploy-no-such-executable"}
			}
			rendered, err := RenderDockerInputs(plan, "private-failure")
			if err != nil {
				t.Fatal(err)
			}
			rendered.Compose = privateEnvironmentQualificationCompose(t, rendered.Compose, noNetwork)
			path := filepath.Join(t.TempDir(), "compose.yaml")
			if err := os.WriteFile(path, rendered.Compose, 0o600); err != nil {
				t.Fatal(err)
			}
			start := CommandSpec{Name: "docker", Args: []string{"compose", "--project-name", name, "-f", path, "up", "--pull", "never", "-d"}, applicationCompose: true}
			t.Cleanup(func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if output, err := exec.CommandContext(cleanupCtx, "docker", "compose", "--project-name", name, "-f", path, "down", "--remove-orphans").CombinedOutput(); err != nil {
					t.Errorf("cleanup qualification project: %v %s", err, output)
				}
			})
			operation, err := deploy.AcquireOperationLock(ctx, plan.DeploymentDir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = operation.Unlock() })
			binding, err := workloadRuntimeBindingV1(plan)
			if err != nil {
				t.Fatal(err)
			}
			endpoint, err := dockerPreflight(ctx, start, effectiveDockerPreflightTimeout(0))
			if err != nil {
				t.Fatal(err)
			}
			start = pinDockerEndpointV1(start, endpoint)
			environment := environment
			environment.Operation, environment.RuntimeBinding = operation, binding
			relayCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			relays := 0
			run := func(spec CommandSpec, options RunOptions) error {
				if len(spec.Args) > 0 && spec.Args[0] == "exec" && options.Stdin != nil {
					relays++
					if test.cancelRelay {
						timer := time.AfterFunc(300*time.Millisecond, cancel)
						defer timer.Stop()
					}
				}
				return runDockerCommand(pinDockerEndpointV1(spec, endpoint), options)
			}
			started := time.Now()
			result, err := startAndInjectPrivateWorkloadEnvironmentWithV1(relayCtx, start, name, plan.Sandbox, environment, RunOptions{}, run, injectPrivateWorkloadEnvironmentV1)
			if err == nil || !result.Owned || result.ContainerID == "" || relays != 1 {
				t.Fatalf("relay failure = %#v, %v, relays=%d", result, err, relays)
			}
			if test.cancelRelay && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			if elapsed := time.Since(started); elapsed > 60*time.Second {
				t.Fatalf("relay cleanup exceeded bound: %s", elapsed)
			}
			if output := strings.TrimSpace(runDockerIntegration(t, ctx, "container", "inspect", "--format", "{{.State.Running}}", result.ContainerID)); output != "false" {
				t.Fatal("failed startup did not retain its stopped container", output)
			}
			_ = runDockerIntegration(t, ctx, "logs", result.ContainerID)
			record, found, err := operation.ReadWorkloadLifecycleV1()
			if err != nil || !found || record.State != "down" {
				t.Fatal("failed startup state was not confirmed down", record, err)
			}
			t.Logf("bounded failure and owned cleanup: %s", time.Since(started))
		})
	}
}

func TestPrivateRuntimeMasksDockerIntegrationProtectTransientContainer(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	image, _ := buildApplicationStartupVerifierIntegrationImage(t, ctx)
	deploymentDir := dockerIntegrationSharedTempDir(t)
	if err := os.Chmod(deploymentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	privateDir := filepath.Join(deploymentDir, privateRuntimeMetadataDirectoryName)
	if err := os.MkdirAll(privateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	environmentPath := filepath.Join(deploymentDir, PrivateWorkloadEnvironmentFileName)
	if created, err := publishPrivateWorkloadEnvironmentFileV1(
		environmentPath,
		[]byte("TOKEN=initial-private-value\n"),
		false,
	); err != nil || !created {
		t.Fatalf("create private environment = %t, %v", created, err)
	}
	if err := os.WriteFile(filepath.Join(privateDir, "before"), []byte("private-metadata\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	unique := fmt.Sprintf("reploy-transient-private-mask-%d-%d", os.Getpid(), time.Now().UnixNano())
	plan := DockerExecutionPlan{
		DeploymentDir: deploymentDir, Image: image, ContainerName: unique,
		Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 12345, GID: 23456, DockerUser: "12345:23456"}),
		Mounts: []MountExecutionPlan{{
			Name: "deployment", Mode: blueprint.MountBind, Source: deploymentDir,
			SourceKind: deploy.RuntimeMountSourceDirectory, Target: "/deployment", ReadOnly: true,
		}},
	}
	command := ResolvedEnvironmentCommand{Argv: []string{
		"/bin/sh", "-eu", "-c",
		`while [ ! -e /deployment/ready ]; do sleep 0.05; done
if [ -s /deployment/.env ]; then echo env-readable; exit 41; fi
if cat /deployment/.reploy/before >/dev/null 2>&1; then echo metadata-readable; exit 42; fi
if cat /deployment/.reploy/late >/dev/null 2>&1; then echo late-metadata-readable; exit 43; fi
printf 'transient-private-mask-pass\n'`,
	}}
	execution, err := PlanTransientContainerExecutionV1(
		plan,
		command,
		nil,
		"run-0000000000000001",
		false,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	// Production transient containers use --rm. Keep this integration
	// container until inspection so its exit status and logs remain available.
	createArgs := make([]string, 0, len(execution.Create.Args)-1)
	for _, argument := range execution.Create.Args {
		if argument != "--rm" {
			createArgs = append(createArgs, argument)
		}
	}
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "docker", "container", "rm", "--force", "--volumes", execution.Container).Run()
	})
	runDockerIntegration(t, ctx, createArgs...)
	runDockerIntegration(t, ctx, "start", execution.Container)

	if err := os.WriteFile(environmentPath, []byte("TOKEN=rotated-private-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(privateDir, "late"), []byte("late-private-metadata\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deploymentDir, "ready"), []byte("ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exitCode := strings.TrimSpace(runDockerIntegration(t, ctx, "wait", execution.Container))
	logs := strings.TrimSpace(runDockerIntegration(t, ctx, "logs", execution.Container))
	if exitCode != "0" || logs != "transient-private-mask-pass" {
		t.Fatalf("container exit=%q logs=%q", exitCode, logs)
	}
}

// The no-secret path must prove direct exec after the real credential drop,
// without a private relay or a controller being present on engine restart.
func testNonPrivateWorkloadLaunchRuntime(t *testing.T, ctx context.Context, image string, account RuntimeUserPlan, noNetwork bool) {
	t.Helper()
	for _, emptyFile := range []bool{false, true} {
		t.Run(fmt.Sprintf("no-secret-empty-file-%t", emptyFile), func(t *testing.T) {
			dir := t.TempDir()
			if emptyFile {
				if err := os.WriteFile(filepath.Join(dir, ".env"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			environment, err := preparePrivateWorkloadEnvironmentV1(dir)
			if err != nil || environment.Present {
				t.Fatal("no-secret fixture selected private injection", err)
			}
			operation, err := deploy.AcquireOperationLock(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = operation.Unlock() })
			name := fmt.Sprintf("reploy-launch-%d-%d", os.Getpid(), time.Now().UnixNano())
			plan := DockerExecutionPlan{EnvironmentID: "launch-proof", DeploymentDir: dir, Phase: blueprint.PhaseStaged, Image: image, ContainerName: name, NetworkName: name,
				Sandbox: newApplicationSandboxPlanV1(account), Workload: &WorkloadExecutionPlan{Argv: []string{"/bin/sh", "-eu", "-c", "printf 'application-launch-pass\n'; exec sleep 300"}}, Restart: "on-failure:1"}
			rendered, err := RenderDockerInputs(plan, "launch-proof")
			if err != nil {
				t.Fatal(err)
			}
			rendered.Compose = privateEnvironmentQualificationCompose(t, rendered.Compose, noNetwork)
			composePath := filepath.Join(dir, "compose.yaml")
			if err := os.WriteFile(composePath, rendered.Compose, 0600); err != nil {
				t.Fatal(err)
			}
			command := CommandSpec{Name: "docker", Args: []string{"compose", "--project-name", name, "-f", composePath, "up", "--pull", "never", "-d"}, applicationCompose: true}
			endpoint, err := dockerPreflight(ctx, command, effectiveDockerPreflightTimeout(0))
			if err != nil {
				t.Fatal(err)
			}
			command = pinDockerEndpointV1(command, endpoint)
			t.Cleanup(func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				_ = exec.CommandContext(cleanupCtx, "docker", "--host", endpoint, "compose", "--project-name", name, "-f", composePath, "down", "--remove-orphans").Run()
			})
			run := func(spec CommandSpec, options RunOptions) error {
				if options.Stdin != nil {
					t.Fatal("no-secret startup sent a payload")
				}
				return runDockerCommand(pinDockerEndpointV1(spec, endpoint), options)
			}
			binding, err := workloadRuntimeBindingV1(plan)
			if err != nil {
				t.Fatal(err)
			}
			lifecycle := &workloadLifecycleV1{command: command, containerName: name, run: run, operation: func(_ context.Context, action func(*deploy.OperationLock) error) error { return action(operation) }}
			if _, _, err := lifecycle.initialize(ctx, binding, false); err != nil {
				t.Fatal(err)
			}
			environment.Operation, environment.RuntimeBinding = operation, binding
			started, err := startAndInjectPrivateWorkloadEnvironmentWithV1(ctx, command, name, plan.Sandbox, environment, RunOptions{}, run, func(context.Context, string, string, ApplicationSandboxPlanV1, privateWorkloadEnvironmentV1, RunOptions, commandRunner) error {
				t.Fatal("private relay called without secrets")
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			lifecycle.started = started
			if err := lifecycle.complete(ctx); err != nil {
				t.Fatal(err)
			}
			if reused, changed, err := lifecycle.initialize(ctx, binding, false); err != nil || !reused || changed {
				t.Fatal("completed no-secret process not reused", reused, changed, err)
			}
			// Podman omits Docker's HostConfig.Init field. Prove the actual
			// process boundary rather than infer it from compatible metadata.
			if result := runDockerIntegration(t, ctx, "exec", name, "cat", "/proc/1/comm"); strings.TrimSpace(result) != "sleep" {
				t.Fatal("PID 1 is not the directly executed application", result)
			}
			// A new process must launch without waiting for a controller/secret relay.
			runDockerIntegration(t, ctx, "restart", name)
			deadline := time.Now().Add(5 * time.Second)
			for strings.Count(runDockerIntegration(t, ctx, "logs", name), "application-launch-pass") < 2 {
				if time.Now().After(deadline) {
					t.Fatal("no-secret engine restart waited for a controller")
				}
				time.Sleep(20 * time.Millisecond)
			}
			if reused, _, err := lifecycle.initialize(ctx, binding, false); err != nil || reused {
				t.Fatal("old record certified replacement process", reused, err)
			}
			t.Log("no-secret launch, completed-process reuse and controller-free restart passed")
		})
	}
}
