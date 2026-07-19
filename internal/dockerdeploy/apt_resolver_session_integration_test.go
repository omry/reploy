package dockerdeploy

import (
	"context"
	"os"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providerstore"
)

func TestAPTResolverBaseProfileDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx := context.Background()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	const base = "debian:bookworm-slim"
	inspection, err := runDockerOutput(ctx, "image", "inspect", base)
	if err != nil {
		t.Skipf("local %s image is required for APT resolver integration: %v", base, err)
	}
	descriptor, _, err := parseDockerImageInspection(base, platform, []byte(inspection))
	if err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	probeWorkspace := buildIntegrationProbeWorkspace(t, platform)
	resolverWorkspace, cleanupResolver, err := PrepareAPTResolverWorkspace(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupResolver)
	session, err := OpenAPTResolverSession(ctx, descriptor, probeWorkspace, resolverWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	validation, err := session.ProbeBaseProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if validation.Profile.NativeArchitecture != "amd64" || len(validation.Profile.ForeignArchitectures) != 0 || len(validation.Executables) != 6 {
		t.Fatalf("validation = %#v", validation)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
