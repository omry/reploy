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

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probearchive"
	"github.com/omry/reploy/internal/providerstore"
)

func TestApplicationStartupVerifierDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	image, platform := buildApplicationStartupVerifierIntegrationImage(t, ctx)
	plan := DockerExecutionPlan{
		EnvironmentID: "verifier-integration", DeploymentDir: t.TempDir(), Phase: blueprint.PhaseStaged,
		Image: image, ContainerName: uniqueDockerIntegrationName("reploy-verifier"),
		NetworkName: uniqueDockerIntegrationName("reploy-verifier-network"),
		Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{
			UID: 12345, GID: 23456, DockerUser: "12345:23456",
		}),
		Workload: &WorkloadExecutionPlan{Argv: []string{
			"/bin/sh", "-eu", "-c", `test "$1" = 'literal $(not-shell)'; printf 'persistent-verifier-pass\n'`, "reploy-test", "literal $(not-shell)",
		}},
	}

	t.Run("persistent", func(t *testing.T) {
		rendered, err := RenderDockerInputs(plan, "verifier-integration")
		if err != nil {
			t.Fatal(err)
		}
		composePath := filepath.Join(t.TempDir(), "compose.yaml")
		if err := os.WriteFile(composePath, rendered.Compose, 0o600); err != nil {
			t.Fatal(err)
		}
		cleanup := exec.CommandContext(context.Background(), "docker", "compose", "--project-name", plan.NetworkName, "-f", composePath, "down", "--remove-orphans")
		t.Cleanup(func() { _ = cleanup.Run() })
		output := runDockerIntegration(
			t, ctx, "compose", "--project-name", plan.NetworkName, "-f", composePath,
			"up", "--pull", "never", "--abort-on-container-exit", "--exit-code-from", "environment",
		)
		if !strings.Contains(output, "persistent-verifier-pass") {
			t.Fatalf("persistent workload output = %q", output)
		}
	})

	t.Run("transient", func(t *testing.T) {
		transientPlan := plan
		transientPlan.ContainerName = uniqueDockerIntegrationName("reploy-verifier-transient")
		command := ResolvedEnvironmentCommand{Argv: []string{
			"/bin/sh", "-eu", "-c", `test "$1" = 'literal $(not-shell)'; printf 'transient-verifier-pass\n'`, "reploy-test", "literal $(not-shell)",
		}}
		spec, err := TransientCommandSpec(transientPlan, command, nil, false, false)
		if err != nil {
			t.Fatal(err)
		}
		output := runDockerIntegration(t, ctx, spec.Args...)
		if strings.TrimSpace(output) != "transient-verifier-pass" {
			t.Fatalf("transient workload output = %q", output)
		}
	})

	t.Run("preserves application exit code", func(t *testing.T) {
		transientPlan := plan
		transientPlan.ContainerName = uniqueDockerIntegrationName("reploy-verifier-exit")
		command := ResolvedEnvironmentCommand{Argv: []string{
			"/bin/sh", "-c", "printf 'exit-preserved\\n'; exit 42",
		}}
		spec, err := TransientCommandSpec(transientPlan, command, nil, false, false)
		if err != nil {
			t.Fatal(err)
		}
		output, err := exec.CommandContext(ctx, "docker", spec.Args...).CombinedOutput()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 42 {
			t.Fatalf("transient exit error = %v, output = %q", err, output)
		}
		if strings.TrimSpace(string(output)) != "exit-preserved" {
			t.Fatalf("transient exit output = %q", output)
		}
	})

	// A container can inherit an outer seccomp filter even when Docker is asked
	// for seccomp=unconfined, so this integration test cannot portably produce
	// Seccomp: 0. The parser's fail-closed Seccomp cases are covered by unit
	// tests; Docker evidence here proves the controls the daemon can omit.
	for _, test := range []struct {
		name       string
		dockerArgs []string
		want       string
	}{
		{name: "missing no-new-privileges", dockerArgs: []string{"--cap-drop", "ALL"}, want: "NoNewPrivs is 0, want 1"},
		{name: "retained capability bounding set", dockerArgs: []string{"--security-opt", "no-new-privileges=true"}, want: "CapBnd is"},
	} {
		t.Run("rejects "+test.name, func(t *testing.T) {
			args := []string{"run", "--rm", "--pull", "never", "--user", "12345:23456", "--read-only"}
			args = append(args, test.dockerArgs...)
			args = append(args,
				"--entrypoint", deploy.ApplicationStartupVerifierPathV1, image,
				"verify-exec", "--", "/bin/sh", "-c", "printf 'untrusted-application-ran\\n'",
			)
			command := exec.CommandContext(ctx, "docker", args...)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("incomplete sandbox unexpectedly succeeded: %s", output)
			}
			if bytes.Contains(output, []byte("untrusted-application-ran")) || !bytes.Contains(output, []byte(test.want)) {
				t.Fatalf("incomplete sandbox output = %q, want diagnostic %q and no application output", output, test.want)
			}
		})
	}

	if platform.Canonical == "" {
		t.Fatal("integration helper returned an empty platform")
	}
}

func buildApplicationStartupVerifierIntegrationImage(t *testing.T, ctx context.Context) (string, blueprint.Platform) {
	t.Helper()
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("startup verifier Docker integration does not build a helper for %s", runtime.GOARCH)
	}
	platform, err := blueprint.ParsePlatform("linux/" + runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate integration test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	workspace := t.TempDir()
	helperPath := filepath.Join(workspace, "reploy-probe")
	build := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", helperPath, "./cmd/reploy-probe")
	build.Dir = repositoryRoot
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH, "GOCACHE="+filepath.Join(workspace, "go-cache"))
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build integration reploy-probe: %v\n%s", err, output)
	}
	carrierPath := filepath.Join(workspace, "reploy-carrier")
	if err := os.WriteFile(carrierPath, []byte("reploy integration archive carrier\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	inputs := []probearchive.HelperInput{
		{Platform: "linux/amd64", Path: helperPath},
		{Platform: "linux/arm/v7", Path: helperPath},
		{Platform: "linux/arm64", Path: helperPath},
	}
	if err := probearchive.Append(carrierPath, inputs); err != nil {
		t.Fatal(err)
	}
	previousLocator := locateApplicationRuntimeExecutable
	locateApplicationRuntimeExecutable = func() (string, error) { return carrierPath, nil }
	t.Cleanup(func() { locateApplicationRuntimeExecutable = previousLocator })

	const base = "debian:bookworm-slim"
	if command := exec.CommandContext(ctx, "docker", "image", "inspect", base); command.Run() != nil {
		runDockerIntegration(t, ctx, "pull", base)
	}
	baseID := canonical.Digest(strings.TrimSpace(runDockerIntegration(t, ctx, "image", "inspect", "--format", "{{.Id}}", base)))
	if err := baseID.Validate(); err != nil {
		t.Fatalf("base image ID: %v", err)
	}
	source, err := InspectBuiltImageCandidate(ctx, BuiltImageCandidate{ImageID: baseID}, platform)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := LoadApplicationStartupVerifierV1(platform)
	if err != nil {
		t.Fatal(err)
	}
	deploymentRoot := filepath.Join(workspace, "deployment")
	if err := os.Mkdir(deploymentRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(deploymentRoot)
	if err != nil {
		t.Fatal(err)
	}
	built, err := BuildApplicationRuntimeLayerCandidate(store, ApplicationRuntimeLayerBuildRequest{
		Source: source, Verifier: verifier, Platform: platform,
	}, RunOptions{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := RemoveBuiltImageCandidate(context.Background(), built); err != nil {
			t.Errorf("remove startup verifier integration image: %v", err)
		}
	})
	if _, err := InspectApplicationRuntimeLayerCandidate(ctx, built, ApplicationRuntimeLayerBuildRequest{
		Source: source, Verifier: verifier, Platform: platform,
	}); err != nil {
		t.Fatal(err)
	}
	return string(built.ImageID), platform
}

func uniqueDockerIntegrationName(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, os.Getpid(), time.Now().UnixNano())
}
