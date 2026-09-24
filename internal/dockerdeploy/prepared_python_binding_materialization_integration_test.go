package dockerdeploy

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

// TestPreparedPythonGraphDockerIntegrationPortableNeutralBinding exercises the
// selected binding path through resolver staging and the production Python
// materialization transaction. The selected records are rebound to a local
// wheel so the real verified-wheel producer can run without external traffic.
func TestPreparedPythonGraphDockerIntegrationPortableNeutralBinding(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx := context.Background()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	const base = "python:3.12-slim"
	imagePlatformOutput, err := runDockerOutput(ctx, "image", "inspect", "--platform", platform.Canonical, "--format", "{{.Os}}/{{.Architecture}}", base)
	if err != nil {
		t.Skipf("local %s image for %s is required for portable binding integration: %v", base, platform.Canonical, err)
	}
	imagePlatform := strings.TrimSpace(imagePlatformOutput)
	if imagePlatform != platform.Canonical {
		t.Skipf("local %s image platform %q does not match selected platform %s", base, imagePlatform, platform.Canonical)
	}
	packedProbe := packIntegrationProbe(t, platform)
	previousLocate := locateProbeArchiveExecutable
	locateProbeArchiveExecutable = func() (string, error) { return packedProbe, nil }
	t.Cleanup(func() { locateProbeArchiveExecutable = previousLocate })

	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wheelFilename := "neutral_binding-1.0.0-py3-none-any.whl"
	wheelContent := preparedPortableNeutralBindingWheelV1(t)
	wheel, err := store.Publish(ctx, "wheels/"+wheelFilename, "wheel", bytes.NewReader(wheelContent))
	if err != nil {
		t.Fatal(err)
	}
	fresh, records := portableToolPythonFreshNeutralFixtureV1(t)
	rebindPortableToolPythonFreshNeutralWheelV1(t, &fresh, &records, wheel)
	component := portableToolPythonFreshComponentForTestV1(t, &fresh)
	binding := component.Bindings[0]
	if component.Component != "application/neutral/python" || binding.Distribution != "neutral-binding" {
		t.Fatalf("neutral portable projection = %#v", component)
	}
	previousRecords := portableToolPythonFreshLockRecordsV1
	t.Cleanup(func() {
		portableToolPythonFreshLockRecordsV1 = previousRecords
	})
	portableToolPythonFreshLockRecordsV1 = func([]toolcatalog.SelectedClosureV1) (toolcatalog.EmbeddedPortableToolLockRecordSetV1, error) {
		return records, nil
	}

	components, projection, err := pythonprovider.ProjectPortableToolPythonBindingsV1(
		fresh.Plan, []providers.ResolvedComponentRequestV1{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(components) != 1 || projection.Components[0].Component != component.Component {
		t.Fatalf("projected portable Python components = %#v, projection = %#v", components, projection)
	}
	baseRequest, err := providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{
		Image: base,
		Exports: map[string]blueprint.BaseExecutableExport{
			"python": {Executable: "/usr/local/bin/python3"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	components = append(components, providers.ResolvedComponentRequestV1{
		Component: "base", Provider: blueprint.ComponentTypeBase, Request: baseRequest,
	})
	sort.Slice(components, func(left, right int) bool { return components[left].Component < components[right].Component })
	request := providers.ResolvedRequestV1{
		Schema: providers.ResolvedRequestSchemaV1, OverlayDigest: reuseTestDigest("a"),
		Platform: platform, Components: components, Sources: []providers.ResolvedSourceInput{},
	}
	if err := providers.ValidateResolvedRequestV1(request, registry.ValidateResolvedRequestOwnersV1); err != nil {
		t.Fatal(err)
	}
	preparedBase, err := PrepareProviderBase(ctx, store, request)
	if err != nil {
		t.Fatal(err)
	}
	finalImageConfig, err := ProviderFinalImageConfigV1(preparedBase.Config)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ExecutePreparedPythonGraph(ctx, PreparedPythonGraphExecutionInput{
		Store: store, Plan: preparedBase.Plan, BaseDescriptor: preparedBase.Descriptor,
		BaseCatalog: preparedBase.Catalog, Sources: request.Sources,
		SourceWheels: []providerstore.ArtifactDescriptor{},
		PortablePython: &PortableToolPythonFreshPlanV1{
			Plan: fresh.Plan, Closures: fresh.Closures,
		},
		DesiredPortableToolPlan: &fresh.Plan,
		FinalImageConfig:        finalImageConfig,
		RunOptions:              RunOptions{Stdout: os.Stdout, Stderr: os.Stderr},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Materializations) != 1 || len(result.Bundles) != 1 {
		t.Fatalf("portable binding graph result = %#v", result)
	}
	image := result.Materializations[0].Image.Digest
	t.Cleanup(func() {
		_, _ = runDockerOutput(context.Background(), "image", "rm", "--force", string(image))
	})
	bundle, err := pythonprovider.DecodeCanonicalBundleDataV1(component.Component, result.Bundles[0].Payload.ProviderPayload)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Wheels) != 1 || bundle.Wheels[0].Distribution != binding.Distribution || bundle.Wheels[0].Artifact != wheel {
		t.Fatalf("portable binding bundle wheels = %#v, want %v", bundle.Wheels, wheel)
	}
	if len(bundle.Outputs) != 1 || bundle.Outputs[0].Name != binding.CLI.Name {
		t.Fatalf("portable binding bundle outputs = %#v", bundle.Outputs)
	}
	wantExecutable := bundle.Outputs[0].Path
	if !strings.HasPrefix(wantExecutable, pythonprovider.InstallRoot+"/") || !strings.HasSuffix(wantExecutable, "/bin/"+binding.CLI.Name) {
		t.Fatalf("portable binding output path = %q", wantExecutable)
	}
	var executable string
	for _, output := range result.Catalog {
		if output.SupplierComponent == component.Component && output.Name == binding.CLI.Name {
			executable = output.Candidate.InvocationPath
		}
	}
	if executable != wantExecutable {
		t.Fatalf("portable binding catalog executable = %q, want %q", executable, wantExecutable)
	}
	output := runDockerIntegration(t, ctx, "run", "--rm", "--entrypoint", executable, string(image))
	if strings.TrimSpace(output) != "hello from neutral portable binding" {
		t.Fatalf("portable binding console script output = %q", output)
	}
}

func preparedPortableNeutralBindingWheelV1(t *testing.T) []byte {
	t.Helper()
	var content bytes.Buffer
	archive := zip.NewWriter(&content)
	files := map[string]string{
		"neutral_binding/__main__.py":                      "def main():\n    print('hello from neutral portable binding')\n",
		"neutral_binding-1.0.0.dist-info/METADATA":         "Metadata-Version: 2.1\nName: neutral-binding\nVersion: 1.0.0\nRequires-Python: >=3.12,<3.13\n\n",
		"neutral_binding-1.0.0.dist-info/WHEEL":            "Wheel-Version: 1.0\nGenerator: reploy-integration\nRoot-Is-Purelib: true\nTag: py3-none-any\n",
		"neutral_binding-1.0.0.dist-info/entry_points.txt": "[console_scripts]\nneutral-cli = neutral_binding.__main__:main\n",
		"neutral_binding-1.0.0.dist-info/RECORD":           "",
	}
	for name, body := range files {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return content.Bytes()
}
