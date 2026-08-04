package dockerdeploy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func TestTransientCommandDockerIntegrationInitializesPrivateHomeAndDropsPrivileges(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx := context.Background()
	helperDir := t.TempDir()
	helper := filepath.Join(helperDir, "reploy-probe")
	build := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", helper, "github.com/omry/reploy/cmd/reploy-probe")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build transient helper: %v\n%s", err, output)
	}
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	workspace := testPreparedProbeWorkspace(t, platform, helperDir)
	plan := DockerExecutionPlan{
		DeploymentDir: t.TempDir(), Image: "debian:bookworm-slim", ContainerName: "reploy-transient-home-integration",
		Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 12345, GID: 23456, DockerUser: "12345:23456"}),
	}
	command := ResolvedEnvironmentCommand{Argv: []string{
		"/bin/sh", "-eu", "-c",
		`test "$(id -u):$(id -g)" = "12345:23456"
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
		plan, command, workspace, nil, "run-0000000000000001", false, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "docker", execution.Cleanup.Args...).Run()
	})
	runDockerIntegration(t, ctx, execution.Create.Args...)
	volume := transientHomeVolumeName(t, ctx, execution.Container)
	output := runDockerIntegration(t, ctx, execution.Start.Args...)
	if strings.TrimSpace(output) != "transient-home-pass" {
		t.Fatalf("transient command output = %q", output)
	}
	requireDockerObjectMissing(t, ctx, "volume", volume)
	requireDockerObjectMissing(t, ctx, "container", execution.Container)

	forced, err := PlanTransientContainerExecutionV1(
		plan,
		ResolvedEnvironmentCommand{Argv: []string{"/bin/sleep", "300"}},
		workspace, nil, "run-0000000000000002", false, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "docker", forced.Cleanup.Args...).Run()
	})
	runDockerIntegration(t, ctx, forced.Create.Args...)
	forcedVolume := transientHomeVolumeName(t, ctx, forced.Container)
	runDockerIntegration(t, ctx, "start", forced.Container)
	runDockerIntegration(t, ctx, forced.Cleanup.Args...)
	requireDockerObjectMissing(t, ctx, "volume", forcedVolume)
	requireDockerObjectMissing(t, ctx, "container", forced.Container)
}

func transientHomeVolumeName(t *testing.T, ctx context.Context, container string) string {
	t.Helper()
	value := runDockerIntegration(
		t, ctx, "container", "inspect",
		"--format", `{{range .Mounts}}{{if eq .Destination "/mnt/reploy-home"}}{{.Name}}{{end}}{{end}}`,
		container,
	)
	value = strings.TrimSpace(value)
	if value == "" {
		t.Fatalf("container %s has no anonymous transient-home volume", container)
	}
	return value
}

func requireDockerObjectMissing(t *testing.T, ctx context.Context, kind string, name string) {
	t.Helper()
	if err := exec.CommandContext(ctx, "docker", kind, "inspect", name).Run(); err == nil {
		t.Fatalf("Docker %s %s still exists", kind, name)
	}
}
