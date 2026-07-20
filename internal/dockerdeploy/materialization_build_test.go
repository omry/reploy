package dockerdeploy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func TestMaterializationBuildCommandUsesOrdinaryDockerBuild(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/arm/v7")
	if err != nil {
		t.Fatal(err)
	}
	plan := MaterializationBuildPlan{
		BaseReference: "debian@" + string(rendererDigest("a")), Platform: platform,
		DockerfilePath: "/tmp/reploy/Dockerfile", ContextDir: "/tmp/reploy/context", IIDFile: "/tmp/reploy/result.iid",
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
			"--iidfile", plan.IIDFile,
			plan.ContextDir,
		},
		Env: []string{"DOCKER_BUILDKIT=1"},
	}
	if !reflect.DeepEqual(command, want) {
		t.Fatalf("command = %#v, want %#v", command, want)
	}
	joined := strings.Join(command.Args, " ")
	if strings.Contains(joined, "buildx") || strings.Contains(joined, "--tag") {
		t.Fatalf("command introduced Buildx or a canonical image tag: %s", joined)
	}
}

func TestMaterializationBuildCommandBypassesDockerCacheWhenRequested(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	plan := MaterializationBuildPlan{
		BaseReference: "sha256:" + strings.Repeat("b", 64), Platform: platform,
		DockerfilePath: "/tmp/Dockerfile", ContextDir: "/tmp/context", IIDFile: "/tmp/result.iid",
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
	valid := MaterializationBuildPlan{
		BaseReference: "sha256:" + strings.Repeat("b", 64), Platform: platform,
		DockerfilePath: "/tmp/Dockerfile", ContextDir: "/tmp/context", IIDFile: "/tmp/result.iid",
	}
	tests := []struct {
		name   string
		mutate func(*MaterializationBuildPlan)
		want   string
	}{
		{name: "mutable base", mutate: func(value *MaterializationBuildPlan) { value.BaseReference = "debian:13" }, want: "immutable"},
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
