package dockerdeploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestPrivateWorkloadEnvironmentDockerIntegrationMasksFilesAndInjectsValues(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	image, _ := buildApplicationStartupVerifierIntegrationImage(t, ctx)

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
	environment, err := preparePrivateWorkloadEnvironmentV1(deploymentDir)
	if err != nil {
		t.Fatal(err)
	}
	tokenDigest := sha256.Sum256([]byte("initial-private-value"))
	expectedTokenDigest := hex.EncodeToString(tokenDigest[:])

	unique := fmt.Sprintf("reploy-private-mask-%d-%d", os.Getpid(), time.Now().UnixNano())
	container := unique + "-container"
	workloadScript := fmt.Sprintf(`if [ "${TOKEN+x}" != x ]; then echo token-missing; exit 31; fi
if [ "$(printf '%%s' "$TOKEN" | sha256sum | cut -d' ' -f1)" != %s ]; then echo token-mismatch; exit 32; fi
test "$(id -u):$(id -g)" = "12345:23456"
test "$(id -G)" = "23456 34567 45678"
test "$(awk '/^CapEff:/ {print $2}' /proc/self/status)" = "0000000000000000"
test "$(awk '/^CapPrm:/ {print $2}' /proc/self/status)" = "0000000000000000"
test "$(awk '/^CapBnd:/ {print $2}' /proc/self/status)" = "0000000000000000"
test "$(awk '/^NoNewPrivs:/ {print $2}' /proc/self/status)" = "1"
test "$(awk '/^Seccomp:/ {print $2}' /proc/self/status)" = "2"
while [ ! -e /host/deployment/ready ]; do sleep 0.05; done
if [ -s /host/deployment/.env ]; then echo env-readable; exit 41; fi
if cat /host/deployment/.reploy/before >/dev/null 2>&1; then echo metadata-readable; exit 42; fi
if cat /host/deployment/.reploy/late >/dev/null 2>&1; then echo late-metadata-readable; exit 43; fi
printf 'private-mask-pass\n'`, expectedTokenDigest)
	workloadScript = strings.ReplaceAll(workloadScript, "$", "$$")
	plan := DockerExecutionPlan{
		EnvironmentID: "private-mask", DeploymentDir: deploymentDir, Phase: blueprint.PhaseStaged,
		Image: image, ContainerName: container, NetworkName: unique,
		Sandbox:            newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 12345, GID: 23456, SupplementaryGIDs: []int{34567, 45678}, DockerUser: "12345:23456"}),
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
	if bytes.Contains(rendered.Compose, []byte("initial-private-value")) ||
		bytes.Contains(rendered.Compose, []byte("rotated-private-value")) {
		t.Fatal("rendered Compose contains private environment material")
	}
	composePath := filepath.Join(t.TempDir(), "compose.yaml")
	if err := os.WriteFile(composePath, rendered.Compose, 0o600); err != nil {
		t.Fatal(err)
	}
	start := CommandSpec{
		Name: "docker",
		Args: []string{"compose", "--project-name", unique, "-f", composePath, "up", "--pull", "never", "-d"},
	}
	cleanup := CommandSpec{
		Name: "docker",
		Args: []string{"compose", "--project-name", unique, "-f", composePath, "down", "--remove-orphans"},
	}
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), cleanup.Name, cleanup.Args...).Run()
	})
	if err := startAndInjectPrivateWorkloadEnvironmentV1(
		ctx,
		start,
		cleanup,
		container,
		environment,
		RunOptions{},
		runCommandWithoutDockerPreflight,
	); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(environmentPath, []byte("TOKEN=rotated-private-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(privateDir, "late"), []byte("late-private-metadata\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deploymentDir, "ready"), []byte("ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exitCode := strings.TrimSpace(runDockerIntegration(t, ctx, "wait", container))
	logs := strings.TrimSpace(runDockerIntegration(t, ctx, "logs", container))
	if exitCode != "0" || logs != "private-mask-pass" {
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
}

func TestPrivateRuntimeMasksDockerIntegrationProtectTransientContainer(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
