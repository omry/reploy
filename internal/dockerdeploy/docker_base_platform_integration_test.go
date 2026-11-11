package dockerdeploy

import (
	"os"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

// Run explicitly on the store/daemon being qualified. This test pulls the pinned
// index and selected child; it never deletes shared images or changes the store.
func TestDockerBasePlatformIntegration(t *testing.T) {
	if os.Getenv("REPLOY_BASE_PLATFORM_INTEGRATION") != "1" {
		t.Skip("set REPLOY_BASE_PLATFORM_INTEGRATION=1 to qualify base-image inspection with Docker")
	}
	ctx := t.Context()
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	index := "python:3.14-slim-bookworm@sha256:516b51cb9db2ef061e465c803e50f64361b82cefd91e525b889b967b83e83527"
	child := "python@sha256:47d41ea4947601fd5bc7ed6c00a7b9c1fbb936a7684873908a3c3b4e68ce3c86"
	run, _, err := bindDockerOutputV1(ctx)
	if err != nil {
		t.Fatal(err)
	}
	info, err := run(ctx, "info", "--format", "{{.ServerVersion}} {{.Architecture}} {{.Driver}} {{json .DriverStatus}}")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("daemon: %s", info)
	if _, err := run(ctx, "pull", "--platform", platform.Canonical, index); err != nil {
		t.Fatal(err)
	}
	first, firstConfig, err := resolveBase(ctx, index, platform, run)
	if err != nil {
		t.Fatal(err)
	}
	again, againConfig, err := resolveBase(ctx, index, platform, run)
	if err != nil || !reflect.DeepEqual(first, again) || !reflect.DeepEqual(firstConfig, againConfig) {
		t.Fatalf("pre-pulled reuse changed identity/config: %v", err)
	}
	cached, cachedConfig, found, err := inspectCachedBase(ctx, index, platform, run, &first)
	if err != nil || !found || !reflect.DeepEqual(first, cached) || !reflect.DeepEqual(firstConfig, cachedConfig) {
		t.Fatalf("cache reuse found=%v err=%v", found, err)
	}
	selectedChild, childConfig, err := resolveBase(ctx, child, platform, run)
	if err != nil {
		t.Fatal(err)
	}
	if first.ConfigDigest != selectedChild.ConfigDigest || !reflect.DeepEqual(first.RootFSDiffIDs, selectedChild.RootFSDiffIDs) || !reflect.DeepEqual(firstConfig, childConfig) {
		t.Fatal("index inspection returned config/layers different from the selected amd64 child")
	}
	t.Logf("selected platform=%s config=%s layers=%v immutable=%s", first.Platform.Canonical, first.ConfigDigest, first.RootFSDiffIDs, first.ImmutableReference)
}
