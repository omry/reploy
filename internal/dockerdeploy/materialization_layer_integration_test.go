package dockerdeploy

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestTypedMaterializationLayerDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx := context.Background()
	base := "debian:bookworm-slim"
	if command := exec.CommandContext(ctx, "docker", "image", "inspect", base); command.Run() != nil {
		runDockerIntegration(t, ctx, "pull", base)
	}
	baseID := canonical.Digest(strings.TrimSpace(runDockerIntegration(t, ctx, "image", "inspect", "--format", "{{.Id}}", base)))
	if err := baseID.Validate(); err != nil {
		t.Fatalf("base image ID: %v", err)
	}
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	deploymentRoot := t.TempDir()
	store, err := providerstore.NewStore(deploymentRoot)
	if err != nil {
		t.Fatal(err)
	}
	scriptContent := []byte("#!/bin/sh\nset -eu\nmkdir -p /opt/reploy-smoke\nprintf 'typed materialization\\n' > /opt/reploy-smoke/evidence\n")
	script, err := store.Publish(ctx, "scripts/smoke.sh", "script", bytes.NewReader(scriptContent))
	if err != nil {
		t.Fatal(err)
	}
	transaction := rendererTransaction()
	transaction.NodeID = "python/smoke"
	transaction.RecipeVersion = "synthetic-materialize-v1"
	transaction.Upstream = providers.RealizedImageV1{Digest: baseID, ConfigDigest: baseID, RootFSSubject: rendererDigest("3")}
	transaction.Prerequisites = []providers.ValidatedExecutableInput{}
	transaction.Script = script
	transaction.Argv = []providers.TypedArgument{
		{Kind: providers.TypedArgumentValidatedExecutable, ExecutableID: "carrier"},
		{Kind: providers.TypedArgumentLiteral, Literal: "-eu"},
		{Kind: providers.TypedArgumentMountedArtifact, MountID: "script", RelativePath: "smoke.sh"},
	}
	transaction.Mounts = []providers.BuildMount{{
		ID: "script", SourceKind: providers.BuildMountSourceScript, SourceDigest: script.SHA256,
		Destination: "/.reploy-build/script", ReadOnly: true, ExpectedKind: "directory",
	}}
	transaction.GeneratedExecutables = []providers.GeneratedExecutableDeclaration{}
	transaction.FinalImageConfig = providers.ImageConfigPolicy{
		User: "0:0", WorkingDir: "/", Environment: []providers.EnvironmentVariable{},
		Entrypoint: []string{}, Command: []string{}, Healthcheck: providers.ImageHealthcheckNone,
		StopSignal: "SIGTERM", Labels: []providers.ImageLabel{},
	}
	request := MaterializationLayerRequest{
		Transaction: transaction,
		MountInputs: []MaterializationMountInput{{
			ID: "script", SourceDigest: script.SHA256,
			Files: []MaterializationMountFile{{RelativePath: "smoke.sh", Artifact: script}},
		}},
		Platform: platform,
	}
	built, err := BuildMaterializationLayer(store, request, RunOptions{Context: ctx, Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		command := exec.Command("docker", "image", "rm", "--force", string(built.Built.ImageID))
		_ = command.Run()
	})
	inspected, err := InspectMaterializationLayerCandidate(ctx, built, request)
	if err != nil {
		t.Fatal(err)
	}
	if inspected.Image.Image.Digest != built.Built.ImageID {
		t.Fatalf("inspected image = %s, built = %s", inspected.Image.Image.Digest, built.Built.ImageID)
	}
	output := runDockerIntegration(
		t, ctx, "run", "--rm", "--entrypoint", "/bin/cat",
		string(built.Built.ImageID), "/opt/reploy-smoke/evidence",
	)
	if strings.TrimSpace(output) != "typed materialization" {
		t.Fatalf("materialized evidence = %q", output)
	}
}
