package dockerdeploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providerstore"
)

func TestProviderInstallVolumeCopyHelperDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx := context.Background()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	workspace := buildIntegrationProbeWorkspace(t, platform)

	base := "debian:12-slim"
	runDockerIntegration(t, ctx, "pull", "--platform", platform.Canonical, base)
	unique := fmt.Sprintf("reploy-volume-copy-%d-%d", os.Getpid(), time.Now().UnixNano())
	sourceVolume := unique + "-source"
	targetVolume := unique + "-target"
	for _, volume := range []string{sourceVolume, targetVolume} {
		runDockerIntegration(t, ctx, "volume", "create", "--name", volume)
		name := volume
		t.Cleanup(func() {
			command := exec.Command("docker", "volume", "rm", "--force", name)
			_ = command.Run()
		})
	}
	runDockerIntegration(
		t, ctx, "run", "--rm", "--platform", platform.Canonical,
		"--mount", "type=volume,source="+sourceVolume+",target=/seed",
		"--entrypoint", "/bin/sh", base, "-c",
		"mkdir -p /seed/nested && printf payload > /seed/nested/value && ln /seed/nested/value /seed/nested/hardlink && ln -s value /seed/nested/current",
	)

	contextDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(contextDir, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	imageTag := unique + ":scratch"
	runDockerIntegration(t, ctx, "build", "--platform", platform.Canonical, "--tag", imageTag, contextDir)
	t.Cleanup(func() {
		command := exec.Command("docker", "image", "rm", "--force", imageTag)
		_ = command.Run()
	})
	imageID := canonical.Digest(strings.TrimSpace(runDockerIntegration(t, ctx, "image", "inspect", "--format", "{{.Id}}", imageTag)))
	if err := imageID.Validate(); err != nil {
		t.Fatal(err)
	}
	action := PathUpdateAction{Name: "data", Kind: PathReplaceVolume, Source: sourceVolume, Target: targetVolume}
	locked := providerInstallPathUpdateFixture(t.TempDir(), action)
	locked.HostTools.DockerPath = "docker"
	locked.SourceBuild.Lock.Platform = platform
	locked.SourceBuild.Lock.FinalImage.ConfigDigest = imageID
	cleaned := false
	if err := applyProviderInstallPathUpdatesWithV1(ctx, locked, providerInstallPathUpdateBackendV1{
		volumeExists: func(ctx context.Context, name string) (bool, error) {
			return providerInstallVolumeExistsV1(ctx, "docker", name, RunOptions{})
		},
		prepareProbeWorkspace: func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
			return workspace, func() error { cleaned = true; return nil }, nil
		},
		run: runCommandWithoutDockerPreflight,
	}); err != nil {
		t.Fatal(err)
	}
	if !cleaned {
		t.Fatal("volume copy helper workspace was not cleaned")
	}
	result := runDockerIntegration(
		t, ctx, "run", "--rm", "--platform", platform.Canonical,
		"--mount", "type=volume,source="+targetVolume+",target=/result,readonly",
		"--entrypoint", "/bin/sh", base, "-c",
		"test \"$(cat /result/nested/current)\" = payload && test /result/nested/value -ef /result/nested/hardlink && readlink /result/nested/current",
	)
	if strings.TrimSpace(result) != "value" {
		t.Fatalf("copied volume result = %q", result)
	}
}
