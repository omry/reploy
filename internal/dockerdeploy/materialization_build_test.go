package dockerdeploy

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func TestMaterializationBuildCommandUsesOrdinaryDockerBuild(t *testing.T) {
	t.Setenv("DOCKER_BUILDKIT", "")
	platform, err := blueprint.ParsePlatform("linux/arm/v7")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	plan := MaterializationBuildPlan{
		BaseReference:   "debian@" + string(rendererDigest("a")),
		OutputReference: temporaryBuildReferencePrefix + "12345678:build-output",
		Platform:        platform,
		DockerfilePath:  filepath.Join(root, "Dockerfile"), ContextDir: filepath.Join(root, "context"), IIDFile: filepath.Join(root, "result.iid"),
	}
	command, err := MaterializationBuildCommand(plan)
	if err != nil {
		t.Fatal(err)
	}
	want := CommandSpec{
		Name: "docker",
		Args: []string{
			"build", "--file", plan.DockerfilePath,
			"--platform", "linux/arm/v7",
			"--build-arg", "REPLOY_BASE_IMAGE=" + plan.BaseReference,
			"--tag", plan.OutputReference,
			"--iidfile", plan.IIDFile,
			plan.ContextDir,
		},
		Env: []string{"DOCKER_BUILDKIT=1"},
	}
	if !reflect.DeepEqual(command, want) {
		t.Fatalf("command = %#v, want %#v", command, want)
	}
	joined := strings.Join(command.Args, " ")
	if strings.Contains(joined, "buildx") {
		t.Fatalf("command introduced Buildx: %s", joined)
	}
}

func TestMaterializationBuildCommandUsesSelectedEngineRegardlessOfAmbientVariable(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	plan := MaterializationBuildPlan{BaseReference: "sha256:" + strings.Repeat("b", 64), OutputReference: temporaryBuildReferencePrefix + "12345678:build-output", Platform: platform, DockerfilePath: filepath.Join(root, "Dockerfile"), ContextDir: root, IIDFile: filepath.Join(root, "result.iid"), NoCache: true}
	for _, mode := range []generatedBuilderMode{generatedBuildKit, generatedPodmanAPI} {
		plan.builder = &generatedImageBuilder{mode: mode, endpoint: "unix:///private/selected.sock"}
		want := "1"
		if mode == generatedPodmanAPI {
			want = "0"
		}
		for _, value := range []string{"", "0", "1", "false", "invalid"} {
			t.Setenv("DOCKER_BUILDKIT", value)
			t.Setenv("DOCKER_HOST", "unix:///private/other.sock")
			t.Setenv("DOCKER_CONTEXT", "other-context")
			spec, err := MaterializationBuildCommand(plan)
			if err != nil {
				t.Fatal(err)
			}
			if commandEnvironmentValueV1(spec, "DOCKER_BUILDKIT") != want || commandEnvironmentValueV1(spec, "DOCKER_HOST") != plan.builder.endpoint || commandEnvironmentValueV1(spec, "DOCKER_CONTEXT") != "" || !reflect.DeepEqual(spec.Args[:2], []string{"build", "--no-cache"}) {
				t.Fatalf("selection escaped pinned engine: %#v", spec)
			}
		}
	}
	plan.builder.mode = generatedBuilderMode(99)
	if _, err := MaterializationBuildCommand(plan); err == nil {
		t.Fatal("unknown builder mode accepted")
	}
}

func TestMaterializationBuildCommandBypassesDockerCacheWhenRequested(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	plan := MaterializationBuildPlan{
		BaseReference:   "sha256:" + strings.Repeat("b", 64),
		OutputReference: temporaryBuildReferencePrefix + "12345678:build-output",
		Platform:        platform,
		DockerfilePath:  filepath.Join(root, "Dockerfile"), ContextDir: filepath.Join(root, "context"), IIDFile: filepath.Join(root, "result.iid"),
		NoCache: true,
	}
	command, err := MaterializationBuildCommand(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(command.Args[:2], []string{"build", "--no-cache"}) {
		t.Fatalf("command args = %#v", command.Args)
	}
}

func TestMaterializationBuildCommandRejectsMutableOrIncompleteInputs(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	valid := MaterializationBuildPlan{
		BaseReference:   "sha256:" + strings.Repeat("b", 64),
		OutputReference: temporaryBuildReferencePrefix + "12345678:build-output",
		Platform:        platform,
		DockerfilePath:  filepath.Join(root, "Dockerfile"), ContextDir: filepath.Join(root, "context"), IIDFile: filepath.Join(root, "result.iid"),
	}
	tests := []struct {
		name   string
		mutate func(*MaterializationBuildPlan)
		want   string
	}{
		{name: "mutable base", mutate: func(value *MaterializationBuildPlan) { value.BaseReference = "debian:13" }, want: "immutable"},
		{name: "unowned output", mutate: func(value *MaterializationBuildPlan) { value.OutputReference = "example/output:latest" }, want: "Reploy-owned"},
		{name: "relative Dockerfile", mutate: func(value *MaterializationBuildPlan) { value.DockerfilePath = "Dockerfile" }, want: "absolute"},
		{name: "relative context", mutate: func(value *MaterializationBuildPlan) { value.ContextDir = "context" }, want: "absolute"},
		{name: "missing iid", mutate: func(value *MaterializationBuildPlan) { value.IIDFile = "" }, want: "absolute"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if _, err := MaterializationBuildCommand(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}
