package dockerdeploy

import (
	"os"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
)

func TestGeneratedImagePrototypeUsesBuildKitReadOnlyMountAndExecArgv(t *testing.T) {
	plan := GeneratedImagePlan{
		BaseImage: "python:3.13-slim", BaseIdentity: "python@sha256:base", Tag: "reploy/demo:staging", BundleDir: t.TempDir(),
		Materialization: providers.Materialization{
			Provider: blueprint.ComponentTypePython, Version: "python-v1", BundleMount: "/reploy-bundle",
			Steps: []providers.MaterializationStep{{Argv: []string{"python", "-m", "venv", "/opt/reploy/python"}}},
		},
		Labels: map[string]string{"io.reploy.environment": "demo", "io.reploy.state": "staging"},
	}
	dockerfile, err := GeneratedImageDockerfile(plan)
	if err != nil {
		t.Fatal(err)
	}
	text := string(dockerfile)
	for _, want := range []string{
		"# syntax=docker/dockerfile:1.7", "FROM ${REPLOY_BASE_IMAGE}",
		"RUN --mount=type=bind,target=/reploy-bundle,readonly [\"python\",\"-m\",\"venv\",\"/opt/reploy/python\"]",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Dockerfile does not contain %q:\n%s", want, text)
		}
	}
	command, err := GeneratedImageBuildCommand(plan, "/tmp/reploy.Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command.Args, " ")
	for _, want := range []string{"build --file /tmp/reploy.Dockerfile", "REPLOY_BASE_IMAGE=python@sha256:base", "io.reploy.environment=demo"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("build command %q does not contain %q", joined, want)
		}
	}
	if len(command.Env) != 1 || command.Env[0] != "DOCKER_BUILDKIT=1" {
		t.Fatalf("build env = %#v", command.Env)
	}
}

func TestBuildGeneratedImageUsesInjectableRunner(t *testing.T) {
	original := runGeneratedImageCommand
	t.Cleanup(func() { runGeneratedImageCommand = original })
	called := false
	runGeneratedImageCommand = func(spec CommandSpec, _ RunOptions) error {
		called = true
		if spec.Name != "docker" || spec.Args[0] != "build" {
			t.Fatalf("spec = %#v", spec)
		}
		dockerfilePath := spec.Args[2]
		content, err := os.ReadFile(dockerfilePath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "--mount=type=bind") {
			t.Fatalf("Dockerfile = %s", content)
		}
		return nil
	}
	plan := GeneratedImagePlan{
		BaseImage: "python:3.13", BaseIdentity: "python@sha256:base", Tag: "reploy/test:staging", BundleDir: t.TempDir(),
		Materialization: providers.Materialization{
			Provider: blueprint.ComponentTypePython, Version: "v1", BundleMount: "/bundle",
			Steps: []providers.MaterializationStep{{Argv: []string{"python", "--version"}}},
		},
	}
	if err := BuildGeneratedImage(plan, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("runner was not called")
	}
}

func TestPrepareGeneratedBuildContextRejectsArtifactDrift(t *testing.T) {
	bundleDir := t.TempDir()
	if err := os.WriteFile(bundleDir+"/demo.whl", []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, cleanup, err := prepareGeneratedBuildContext(bundleDir, []providers.Artifact{{
		Identifier: "demo", Kind: "wheel", Path: "demo.whl", SHA256: strings.Repeat("a", 64),
	}})
	cleanup()
	if err == nil || !strings.Contains(err.Error(), "checksum changed") {
		t.Fatalf("error = %v", err)
	}
}

func TestGeneratedImageDockerfileDoesNotInterpretProviderArgv(t *testing.T) {
	plan := GeneratedImagePlan{
		BaseImage: "python", Materialization: providers.Materialization{
			Provider: blueprint.ComponentTypePython, Version: "v1", BundleMount: "/bundle",
			Steps: []providers.MaterializationStep{{Argv: []string{"tool", "$(touch /tmp/pwned)", ";rm -rf /"}}},
		},
	}
	content, err := GeneratedImageDockerfile(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `["tool","$(touch /tmp/pwned)",";rm -rf /"]`) {
		t.Fatalf("argv was not JSON encoded:\n%s", content)
	}
}
