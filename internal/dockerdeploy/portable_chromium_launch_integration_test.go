package dockerdeploy

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

// This opt-in contract fixture is intentionally narrower than PTD-25's
// manifest-derived case runner. It proves one selected Ubuntu 26.04 AMD64 closure
// using the ordinary APT node, the offline payload layer, and a non-root launch.
func TestPortableChromiumUbuntu2604DockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx := context.Background()
	const base = "docker.io/library/ubuntu@sha256:7b202b0e2e0028c6250f5fcf41d04df492d145a1654c6995a6553f0c1f6f1960"
	if _, err := runDockerOutput(ctx, "image", "inspect", base); err != nil {
		t.Fatalf("exact Ubuntu 26.04 AMD64 fixture image is required: %v", err)
	}
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	probe := packIntegrationProbe(t, platform)
	previousLocate := locateProbeArchiveExecutable
	locateProbeArchiveExecutable = func() (string, error) { return probe, nil }
	t.Cleanup(func() { locateProbeArchiveExecutable = previousLocate })

	selected := portableToolPythonFreshPlaywrightFixtureForTargetV1(t, "application:browser", toolcatalog.TargetIdentityV1{
		Platform: "linux/amd64", OSReleaseID: "ubuntu", VersionID: "26.04",
		OCIArchitecture: "amd64", NativeArchitecture: "amd64", PackageManager: "apt",
	})
	components, err := aptprovider.ProjectPortableToolAPTRootsV1(selected.Plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	if len(components) != 1 || components[0].Component != "application/browser/os" {
		t.Fatalf("selected browser APT components = %#v", components)
	}
	baseRequest, err := providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{
		Image: base, Exports: map[string]blueprint.BaseExecutableExport{},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := providers.ResolvedRequestV1{
		Schema: providers.ResolvedRequestSchemaV1, OverlayDigest: reuseTestDigest("f"), Platform: platform,
		Components: append(components, providers.ResolvedComponentRequestV1{
			Component: "base", Provider: blueprint.ComponentTypeBase, Request: baseRequest,
		}),
		Sources: []providers.ResolvedSourceInput{},
	}
	if err := providers.ValidateResolvedRequestV1(request, registry.ValidateResolvedRequestOwnersV1); err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareProviderBase(ctx, store, request)
	if err != nil {
		t.Fatal(err)
	}
	finalConfig, err := ProviderFinalImageConfigV1(prepared.Config)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := ExecutePreparedPythonGraph(ctx, PreparedPythonGraphExecutionInput{
		Store: store, Plan: prepared.Plan, BaseDescriptor: prepared.Descriptor, BaseCatalog: prepared.Catalog,
		Sources: request.Sources, SourceWheels: []providerstore.ArtifactDescriptor{},
		FinalImageConfig: finalConfig, RunOptions: RunOptions{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Materializations) != 1 || len(graph.Bundles) != 1 || graph.Bundles[0].Payload.Provider != blueprint.ComponentTypeAPT {
		t.Fatalf("browser dependencies did not use one APT transaction: %#v", graph.Bundles)
	}
	aptImage := graph.Materializations[0].Image
	t.Cleanup(func() {
		_, _ = runDockerOutput(context.Background(), "image", "rm", "--force", string(aptImage.ConfigDigest))
	})
	upstream, err := ResolveProviderPrefixDescriptor(ctx, prepared.Descriptor, aptImage, platform)
	if err != nil {
		t.Fatal(err)
	}

	var aptNode, baseNode providers.NodeID
	for _, node := range prepared.Plan.Nodes {
		switch node.Provider {
		case blueprint.ComponentTypeAPT:
			aptNode = node.ID
		case blueprint.ComponentTypeBase:
			baseNode = node.ID
		}
	}
	if aptNode == "" || baseNode == "" {
		t.Fatalf("provider plan omitted APT or base node: %#v", prepared.Plan.Nodes)
	}
	packageDomain := providers.PortableToolDomainAuthorityV1{ID: "browser/packages", Owner: aptNode}
	otherDomain := providers.PortableToolDomainAuthorityV1{ID: "browser/runtime", Owner: baseNode}
	dag, err := providers.BuildPortableToolProviderDAGV1(prepared.Plan, selected.Plan, []providers.PortableToolProviderDomainSetV1{{
		Scope: "application:browser", PackageManager: packageDomain, Binding: otherDomain,
		Filesystem: otherDomain, Environment: otherDomain, Exports: otherDomain, Capabilities: otherDomain,
	}})
	if err != nil {
		t.Fatal(err)
	}
	records, err := toolcatalog.EmbeddedPortableToolLockRecordsV1(selected.Closures)
	if err != nil {
		t.Fatal(err)
	}
	other := []providers.PortableToolArtifactAcquisitionInputV1{}
	for _, artifact := range records.Artifacts {
		if artifact.Artifact != selected.Plan.Tools[0].Responsibilities.BindingArtifacts[0].Reference {
			continue
		}
		outcome, err := providerstore.AcquireArtifact(ctx, store, providerstore.AcquisitionRequest{
			Artifact: artifact.Descriptor,
			Source: providerstore.ArtifactSource{
				ID: artifact.Source.Reference.ID, SHA256: artifact.Descriptor.SHA256, Mirrors: artifact.Mirrors,
			},
			Policy: providerstore.DefaultAcquisitionPolicy(),
		})
		if err != nil {
			t.Fatal(err)
		}
		other = append(other, providers.PortableToolArtifactAcquisitionInputV1{
			Scope: artifact.Scope, Tool: artifact.Tool, Artifact: artifact.Artifact,
			Descriptor: outcome.Artifact, Source: artifact.Source, Provenance: outcome.Provenance,
		})
	}
	if len(other) != 1 {
		t.Fatalf("expected one exact selected Python wheel acquisition, got %d", len(other))
	}
	payloads, err := MaterializePortableRuntimePayloadsFreshV1(ctx, store, dag, selected.Closures, other)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = payloads.Cleanup() })
	layer, err := PreparePortableRuntimePayloadLayerV1(ctx, store, payloads, upstream, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = layer.Cleanup(context.Background()) })
	image := string(layer.Image.Descriptor.ConfigDigest)
	const browser = "/opt/reploy/tools/playwright/ms-playwright/chromium-1228/chrome-linux64/chrome"
	output, err := runDockerOutput(ctx, "run", "--rm", "--platform", "linux/amd64", "--network", "none", "--user", "65532:65532",
		"--env", "HOME=/tmp", "--entrypoint", browser, image,
		"--headless", "--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu", "--dump-dom", "data:text/html,<title>reploy-ptd24</title>")
	if err != nil {
		t.Fatalf("non-root selected Chromium launch failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "<title>reploy-ptd24</title>") {
		t.Fatalf("selected Chromium did not render the fixture page: %s", output)
	}
	environment, err := runDockerOutput(ctx, "run", "--rm", "--platform", "linux/amd64", "--network", "none", "--user", "65532:65532",
		"--entrypoint", "/usr/bin/env", image)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{
		"PLAYWRIGHT_BROWSERS_PATH=/opt/reploy/tools/playwright/ms-playwright",
		"PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1", "PLAYWRIGHT_SKIP_BROWSER_GC=1",
	} {
		if !strings.Contains(environment, entry) {
			t.Fatalf("final image environment omitted %q", entry)
		}
	}
}
