package dockerdeploy

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
)

func TestGeneratedImageBuildKitIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err := ProbeBuildKitCapabilities(ctx); err != nil {
		t.Fatal(err)
	}
	base := "alpine:3.21"
	runDockerIntegration(t, ctx, "pull", base)
	digest := strings.TrimSpace(runDockerIntegration(t, ctx, "image", "inspect", "--format", "{{index .RepoDigests 0}}", base))
	if !strings.Contains(digest, "@sha256:") {
		t.Fatalf("immutable base digest = %q", digest)
	}
	tag := fmt.Sprintf("reploy/buildkit-integration-%d:staging", os.Getpid())
	t.Cleanup(func() {
		command := exec.Command("docker", "image", "rm", "--force", tag)
		_ = command.Run()
	})
	bundleDir := t.TempDir()
	evidence := []byte("BuildKit mount evidence\n")
	if err := os.WriteFile(filepath.Join(bundleDir, "evidence.txt"), evidence, 0o644); err != nil {
		t.Fatal(err)
	}
	digestSum := sha256.Sum256(evidence)
	plan := GeneratedImagePlan{
		BaseImage: base, BaseIdentity: digest, Tag: tag, BundleDir: bundleDir,
		Materialization: providers.Materialization{
			Provider: blueprint.ComponentTypePython, Version: "integration-v1", BundleMount: "/reploy-bundle",
			Artifacts: []providers.Artifact{{Identifier: "evidence", Kind: "fixture", Path: "evidence.txt", SHA256: fmt.Sprintf("%x", digestSum[:])}},
			Steps: []providers.MaterializationStep{{Argv: []string{
				"sh", "-c", "test -f /reploy-bundle/evidence.txt && cp /reploy-bundle/evidence.txt /reploy-evidence.txt",
			}}},
		},
		Labels: map[string]string{generatedImageOwnerLabel: "reploy", generatedImageDirectoryLabel: "integration"},
	}
	if err := BuildGeneratedImage(plan, RunOptions{Context: ctx}); err != nil {
		t.Fatal(err)
	}
	runDockerIntegration(t, ctx, "run", "--rm", tag, "test", "-f", "/reploy-evidence.txt")
}

func runDockerIntegration(t *testing.T, ctx context.Context, args ...string) string {
	t.Helper()
	command := exec.CommandContext(ctx, "docker", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
