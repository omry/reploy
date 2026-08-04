package dockerdeploy

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestTransientCommandDockerIntegrationEnforcesIdentityAndKernelBaseline(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx := context.Background()
	plan := DockerExecutionPlan{
		DeploymentDir: t.TempDir(), Image: "debian:bookworm-slim", ContainerName: "reploy-transient-home-integration",
		Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 12345, GID: 23456, SupplementaryGIDs: []int{34567, 45678}, DockerUser: "12345:23456"}),
	}
	command := ResolvedEnvironmentCommand{Argv: []string{
		"/bin/sh", "-eu", "-c",
		`test "$(id -u):$(id -g)" = "12345:23456"
test "$(id -G)" = "23456 34567 45678"
test "$(awk '/^CapEff:/ {print $2}' /proc/self/status)" = "0000000000000000"
test "$(awk '/^CapPrm:/ {print $2}' /proc/self/status)" = "0000000000000000"
test "$(awk '/^CapBnd:/ {print $2}' /proc/self/status)" = "0000000000000000"
test "$(awk '/^NoNewPrivs:/ {print $2}' /proc/self/status)" = "1"
test "$(awk '/^Seccomp:/ {print $2}' /proc/self/status)" = "2"
test "$HOME" = "/mnt/reploy-home"
test "$TMPDIR" = "$HOME"
printf writable > "$HOME/proof"
printf updated >> "$HOME/proof"
test "$(cat "$HOME/proof")" = writableupdated
printf temporary > "$TMPDIR/temporary"
rm "$HOME/proof" "$TMPDIR/temporary"
test ! -e "$HOME/proof"
test ! -e "$TMPDIR/temporary"
test "$(stat -c '%a %u:%g' "$HOME")" = "700 12345:23456"
if touch /reploy-root-proof 2>/dev/null; then exit 41; fi
printf 'transient-home-pass\n'`,
	}}
	execution, err := PlanTransientContainerExecutionV1(
		plan, command, nil, "run-0000000000000001", false, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "docker", execution.Cleanup.Args...).Run()
	})
	runDockerIntegration(t, ctx, execution.Create.Args...)
	output := runDockerIntegration(t, ctx, execution.Start.Args...)
	if strings.TrimSpace(output) != "transient-home-pass" {
		t.Fatalf("transient command output = %q", output)
	}
	requireDockerObjectMissing(t, ctx, "container", execution.Container)

	forced, err := PlanTransientContainerExecutionV1(
		plan,
		ResolvedEnvironmentCommand{Argv: []string{"/bin/sleep", "300"}},
		nil, "run-0000000000000002", false, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "docker", forced.Cleanup.Args...).Run()
	})
	runDockerIntegration(t, ctx, forced.Create.Args...)
	runDockerIntegration(t, ctx, "start", forced.Container)
	runDockerIntegration(t, ctx, forced.Cleanup.Args...)
	requireDockerObjectMissing(t, ctx, "container", forced.Container)
}

func requireDockerObjectMissing(t *testing.T, ctx context.Context, kind string, name string) {
	t.Helper()
	if err := exec.CommandContext(ctx, "docker", kind, "inspect", name).Run(); err == nil {
		t.Fatalf("Docker %s %s still exists", kind, name)
	}
}
