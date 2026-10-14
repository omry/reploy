package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A disposable, unmodified engine is supplied by the test launcher. Never use
// the ambient/default endpoint for these tests or install a replacement engine.
func TestPodmanHealthcheckCompatibilityIntegration(t *testing.T) {
	endpoint, image := os.Getenv("REPLOY_HF7_PODMAN_ENDPOINT"), os.Getenv("REPLOY_HF7_PODMAN_IMAGE")
	if runtime.GOOS != "linux" || endpoint == "" || image == "" {
		t.Skip("requires an explicit disposable Linux Podman endpoint and NONE image")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	client, err := identifyPodmanHealthcheckClient(ctx, endpoint)
	if err != nil || client == nil {
		t.Fatalf("fixture is not Podman: %v", err)
	}
	defer client.http.CloseIdleConnections()
	rawOutput := func(args ...string) string {
		t.Helper()
		command := exec.CommandContext(ctx, "docker", args...)
		command.Env = append(os.Environ(), "DOCKER_HOST="+endpoint, "DOCKER_CONTEXT=", "DOCKER_BUILDKIT=1")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture command %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	assertNoTimer := func(id string) {
		t.Helper()
		if !validPodmanHealthcheckContainerID(id) {
			t.Fatalf("invalid owned ID: %q", id)
		}
		output, err := exec.CommandContext(ctx, "systemctl", "--user", "list-units", "--all", "--quiet",
			id+"*.timer", id+"*.service", "libpod-healthcheck-"+id+"*.timer", "libpod-healthcheck-"+id+"*.service").CombinedOutput()
		if err != nil || len(bytes.TrimSpace(output)) != 0 {
			t.Fatalf("owned timer/service remains: %v: %s", err, output)
		}
	}
	pinned := func(args ...string) CommandSpec {
		return pinDockerEndpointV1(CommandSpec{Name: "docker", Args: args}, endpoint)
	}
	t.Run("create probe and repeated lifecycle", func(t *testing.T) {
		name := fmt.Sprintf("reploy-hf7-create-%d", time.Now().UnixNano())
		t.Cleanup(func() { rawOutput("container", "rm", "--force", name) })
		var output strings.Builder
		err := runCommandWithoutDockerPreflight(pinned("create", "--name", name, "--network=none", "--read-only", "--cap-drop=ALL", image, "sh", "-c", "while :; do sleep 1; done"), RunOptions{Context: ctx, Stdout: &output})
		if err != nil {
			t.Fatal(err)
		}
		id := strings.TrimSpace(output.String())
		inspection, err := client.inspect(ctx, id)
		if err != nil || !podmanNoneHealthcheck(inspection.Config.Healthcheck) || inspection.Config.Healthcheck.Interval != 0 {
			t.Fatalf("not prepared before initialization: %#v / %v", inspection, err)
		}
		assertNoTimer(id)
		rawOutput("start", id)
		assertNoTimer(id)
		rawOutput("restart", "--time=0", id)
		assertNoTimer(id)
		rawOutput("stop", "--time=0", id)
		assertNoTimer(id)
	})
	t.Run("run stdin and nonzero exit", func(t *testing.T) {
		name := fmt.Sprintf("reploy-hf7-run-%d", time.Now().UnixNano())
		var stdout, stderr strings.Builder
		err := runCommandWithoutDockerPreflight(pinned("run", "--rm", "--interactive", "--name", name,
			"--network=none", "--read-only", "--cap-drop=ALL", image, "sh", "-c", "cat; echo hf7-error >&2; exit 7"),
			RunOptions{Context: ctx, Stdin: strings.NewReader("hf7-input\n"), Stdout: &stdout, Stderr: &stderr})
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 7 || stdout.String() != "hf7-input\n" || !strings.Contains(stderr.String(), "hf7-error") {
			t.Fatalf("exit/output changed: %v / %q / %q", err, stdout.String(), stderr.String())
		}
	})
	t.Run("output runner creates before copy", func(t *testing.T) {
		name := fmt.Sprintf("reploy-hf7-output-%d", time.Now().UnixNano())
		t.Cleanup(func() { rawOutput("container", "rm", "--force", name) })
		id, err := executeDockerOutputAtEndpoint(ctx, endpoint, "container", "create", "--name", name, "--network=none", image, "true")
		if err != nil {
			t.Fatal(err)
		}
		inspection, err := client.inspect(ctx, strings.TrimSpace(id))
		if err != nil || inspection.Config.Healthcheck.Interval != 0 {
			t.Fatalf("output runner missed preparation: %#v / %v", inspection, err)
		}
		assertNoTimer(strings.TrimSpace(id))
	})
	t.Run("compose startup and recreation", func(t *testing.T) {
		project := fmt.Sprintf("reploy-hf7-compose-%d", time.Now().UnixNano())
		file := filepath.Join(t.TempDir(), "compose.yaml")
		content := fmt.Sprintf("services:\n  app:\n    image: %s\n    pull_policy: never\n    network_mode: none\n    read_only: true\n    cap_drop: [ALL]\n    command: [sh, -c, 'while :; do sleep 1; done']\n", image)
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		prefix := []string{"compose", "--project-name", project, "-f", file}
		t.Cleanup(func() { rawOutput(append(append([]string{}, prefix...), "down", "--remove-orphans")...) })
		// Reproduce an old deployment made without the workaround. The adapter
		// must remove it while its positive interval still permits timer cleanup.
		rawOutput(append(append([]string{}, prefix...), "up", "--pull", "never", "-d")...)
		previous := rawOutput(append(append([]string{}, prefix...), "ps", "--all", "--quiet")...)
		old, err := client.inspect(ctx, previous)
		if err != nil || !podmanNoneHealthcheck(old.Config.Healthcheck) || old.Config.Healthcheck.Interval == 0 {
			t.Fatalf("old unpatched deployment did not reproduce inherited NONE: %#v / %v", old, err)
		}
		for _, force := range []bool{false, true} {
			args := append(append([]string{}, prefix...), "up", "--pull", "never", "-d")
			if force {
				args = append(args, "--force-recreate")
			}
			if err := runCommandWithoutDockerPreflight(pinned(args...), RunOptions{Context: ctx}); err != nil {
				t.Fatal(err)
			}
			id := rawOutput(append(append([]string{}, prefix...), "ps", "--all", "--quiet")...)
			if id == previous {
				t.Fatal("Compose compatibility/recreation reused the old container")
			}
			assertNoTimer(previous)
			assertNoTimer(id)
			previous = id
		}
	})
}
