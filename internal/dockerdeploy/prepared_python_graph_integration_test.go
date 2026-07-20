package dockerdeploy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/probearchive"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

func TestPreparedPythonGraphDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx := context.Background()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	const base = "python:3.13-slim"
	if _, err := runDockerOutput(ctx, "image", "inspect", base); err != nil {
		t.Skipf("local %s image is required for graph integration: %v", base, err)
	}
	packedProbe := packIntegrationProbe(t, platform)
	previousLocate := locateProbeArchiveExecutable
	locateProbeArchiveExecutable = func() (string, error) { return packedProbe, nil }
	t.Cleanup(func() { locateProbeArchiveExecutable = previousLocate })

	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wheelPath := filepath.Join(t.TempDir(), "demo_server-1.0-py3-none-any.whl")
	writePythonIntegrationWheel(t, wheelPath)
	wheelFile, err := os.Open(wheelPath)
	if err != nil {
		t.Fatal(err)
	}
	wheel, publishErr := store.Publish(ctx, "wheels/"+filepath.Base(wheelPath), "wheel", wheelFile)
	closeErr := wheelFile.Close()
	if publishErr != nil {
		t.Fatal(publishErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	source := providers.ResolvedSourceInput{
		Schema:               providers.ResolvedSourceInputSchemaV1,
		Component:            "application",
		LogicalPackage:       "demo-server",
		SourceManifestDigest: wheel.SHA256,
		BuilderProfile:       "uv-wheel-v1",
		BuildSettings:        providers.CanonicalProviderData{Schema: "python-build-settings-v1", Value: canonical.Object{}},
		EcosystemMetadata:    providers.CanonicalProviderData{Schema: "python-source-metadata-v1", Value: canonical.Object{}},
		ArtifactDigest:       wheel.SHA256,
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
	packageRequest, err := pythonprovider.CanonicalPackageRequestV1("demo-server==1.0")
	if err != nil {
		t.Fatal(err)
	}
	pythonRequest, err := pythonprovider.CanonicalProviderRequestV1(pythonprovider.PythonProviderRequestV1{
		Component: "application",
		Interpreter: blueprint.CommandRequirement{
			Command: "python", Version: ">=3.11", Supplier: "base",
		},
		Requirements: []providers.CanonicalPackageRequest{packageRequest},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := providers.ResolvedRequestV1{
		Schema: providers.ResolvedRequestSchemaV1, OverlayDigest: reuseTestDigest("c"),
		Platform: platform,
		Components: []providers.ResolvedComponentRequestV1{
			{Component: "application", Provider: blueprint.ComponentTypePython, Request: pythonRequest},
			{Component: "base", Provider: blueprint.ComponentTypeBase, Request: baseRequest},
		},
		Sources: []providers.ResolvedSourceInput{source},
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
		SourceWheels:     []providerstore.ArtifactDescriptor{wheel},
		FinalImageConfig: finalImageConfig,
		RunOptions:       RunOptions{Stdout: os.Stdout, Stderr: os.Stderr},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Materializations) != 1 || len(result.Bundles) != 1 || len(result.Bundles[0].Payload.Outputs) != 1 {
		t.Fatalf("graph result = %#v", result)
	}
	image := result.Materializations[0].Image.Digest
	t.Cleanup(func() {
		_, _ = runDockerOutput(context.Background(), "image", "rm", "--force", string(image))
	})
	var executable string
	for _, output := range result.Catalog {
		if output.SupplierComponent == "application" && output.Name == "demo-server" {
			executable = output.Candidate.InvocationPath
		}
	}
	if executable == "" {
		t.Fatalf("graph catalog has no demo-server output: %#v", result.Catalog)
	}
	output := runDockerIntegration(t, ctx, "run", "--rm", "--entrypoint", executable, string(image))
	if strings.TrimSpace(output) != "hello from generated Python image" {
		t.Fatalf("generated console script output = %q", output)
	}
}

func TestPreparedAPTPythonGraphDockerIntegration(t *testing.T) {
	runPreparedAPTPythonGraphDockerIntegration(
		t, "debian:bookworm-slim", map[string]blueprint.BaseExecutableExport{},
		[]blueprint.APTPackageRequest{{Name: "python3"}, {Name: "python3-pip"}, {Name: "python3-venv"}},
		"system", "/usr/bin/python3",
	)
}

func TestPreparedAPTPythonTwoInterpreterDockerIntegration(t *testing.T) {
	runPreparedAPTPythonGraphDockerIntegration(
		t, "python:3.13-slim",
		map[string]blueprint.BaseExecutableExport{"python": {Executable: "/usr/local/bin/python3"}},
		[]blueprint.APTPackageRequest{{Name: "python3"}, {Name: "python3-pip"}, {Name: "python3-venv"}},
		"system", "/usr/bin/python3",
	)
}

func TestPreparedAPTPythonNativeLibraryDockerIntegration(t *testing.T) {
	runPreparedAPTPythonGraphDockerIntegration(
		t, "python:3.13-slim",
		map[string]blueprint.BaseExecutableExport{"python": {Executable: "/usr/local/bin/python3"}},
		[]blueprint.APTPackageRequest{{Name: "libpq5"}},
		"base", "/usr/local/bin/python3",
	)
}

func runPreparedAPTPythonGraphDockerIntegration(
	t *testing.T,
	base string,
	baseExports map[string]blueprint.BaseExecutableExport,
	aptPackages []blueprint.APTPackageRequest,
	pythonSupplier string,
	wantInterpreter string,
) {
	t.Helper()
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx := context.Background()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runDockerOutput(ctx, "image", "inspect", base); err != nil {
		t.Skipf("local %s image is required for APT/Python graph integration: %v", base, err)
	}
	packedProbe := packIntegrationProbe(t, platform)
	previousLocate := locateProbeArchiveExecutable
	locateProbeArchiveExecutable = func() (string, error) { return packedProbe, nil }
	t.Cleanup(func() { locateProbeArchiveExecutable = previousLocate })

	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wheelPath := filepath.Join(t.TempDir(), "demo_server-1.0-py3-none-any.whl")
	writePythonIntegrationWheel(t, wheelPath)
	wheelFile, err := os.Open(wheelPath)
	if err != nil {
		t.Fatal(err)
	}
	wheel, publishErr := store.Publish(ctx, "wheels/"+filepath.Base(wheelPath), "wheel", wheelFile)
	closeErr := wheelFile.Close()
	if publishErr != nil {
		t.Fatal(publishErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	source := providers.ResolvedSourceInput{
		Schema:               providers.ResolvedSourceInputSchemaV1,
		Component:            "application",
		LogicalPackage:       "demo-server",
		SourceManifestDigest: wheel.SHA256,
		BuilderProfile:       "uv-wheel-v1",
		BuildSettings:        providers.CanonicalProviderData{Schema: "python-build-settings-v1", Value: canonical.Object{}},
		EcosystemMetadata:    providers.CanonicalProviderData{Schema: "python-source-metadata-v1", Value: canonical.Object{}},
		ArtifactDigest:       wheel.SHA256,
	}
	baseRequest, err := providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{
		Image: base, Exports: baseExports,
	})
	if err != nil {
		t.Fatal(err)
	}
	aptRequest, err := aptprovider.CanonicalProviderRequestV1(aptprovider.APTProviderRequestV1{
		Components: []aptprovider.APTComponentRequestV1{{
			Component: "system", Packages: aptPackages,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	packageRequest, err := pythonprovider.CanonicalPackageRequestV1("demo-server==1.0")
	if err != nil {
		t.Fatal(err)
	}
	pythonRequest, err := pythonprovider.CanonicalProviderRequestV1(pythonprovider.PythonProviderRequestV1{
		Component: "application",
		Interpreter: blueprint.CommandRequirement{
			Command: "python", Version: ">=3.11", Supplier: pythonSupplier,
		},
		Requirements: []providers.CanonicalPackageRequest{packageRequest},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := providers.ResolvedRequestV1{
		Schema: providers.ResolvedRequestSchemaV1, OverlayDigest: reuseTestDigest("e"),
		Platform: platform,
		Components: []providers.ResolvedComponentRequestV1{
			{Component: "application", Provider: blueprint.ComponentTypePython, Request: pythonRequest},
			{Component: "base", Provider: blueprint.ComponentTypeBase, Request: baseRequest},
			{Component: "system", Provider: blueprint.ComponentTypeAPT, Request: aptRequest},
		},
		Sources: []providers.ResolvedSourceInput{source},
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
		SourceWheels:     []providerstore.ArtifactDescriptor{wheel},
		FinalImageConfig: finalImageConfig,
		RunOptions:       RunOptions{Stdout: os.Stdout, Stderr: os.Stderr},
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := len(result.Materializations) - 1; index >= 0; index-- {
		image := result.Materializations[index].Image.Digest
		t.Cleanup(func() { _, _ = runDockerOutput(context.Background(), "image", "rm", "--force", string(image)) })
	}
	if len(result.Materializations) != 2 || len(result.Bundles) != 2 || result.Bundles[0].Payload.Provider != blueprint.ComponentTypeAPT || result.Bundles[1].Payload.Provider != blueprint.ComponentTypePython {
		t.Fatalf("APT/Python graph result = %#v", result)
	}
	if len(result.Profiles) != 2 || len(result.Profiles[1].SelectedExecutables) != 1 || result.Profiles[1].SelectedExecutables[0].InvocationPath != wantInterpreter {
		t.Fatalf("selected Python interpreter = %#v, want %q", result.Profiles, wantInterpreter)
	}
	var executable string
	for _, output := range result.Catalog {
		if output.SupplierComponent == "application" && output.Name == "demo-server" {
			executable = output.Candidate.InvocationPath
		}
	}
	if executable == "" {
		t.Fatalf("graph catalog has no demo-server output: %#v", result.Catalog)
	}
	finalImage := result.Materializations[len(result.Materializations)-1].Image.Digest
	output := runDockerIntegration(t, ctx, "run", "--rm", "--entrypoint", executable, string(finalImage))
	if strings.TrimSpace(output) != "hello from generated Python image" {
		t.Fatalf("generated console script output = %q", output)
	}
}

func packIntegrationProbe(t *testing.T, _ blueprint.Platform) string {
	t.Helper()
	inputs := make([]probearchive.HelperInput, 0, 3)
	for _, value := range []string{"linux/amd64", "linux/arm64", "linux/arm/v7"} {
		target, err := blueprint.ParsePlatform(value)
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, probearchive.HelperInput{Platform: value, Path: buildIntegrationProbeBinary(t, target)})
	}
	packed := filepath.Join(t.TempDir(), "reploy-with-probe")
	if err := os.WriteFile(packed, []byte("reploy integration probe archive\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := probearchive.Append(packed, inputs); err != nil {
		t.Fatal(err)
	}
	return packed
}

func buildIntegrationProbeBinary(t *testing.T, platform blueprint.Platform) string {
	t.Helper()
	output := filepath.Join(t.TempDir(), "reploy-probe-"+strings.NewReplacer("/", "-", "v", "").Replace(platform.Canonical))
	command := exec.Command("go", "build", "-o", output, "./cmd/reploy-probe")
	command.Dir = filepath.Clean(filepath.Join("..", ".."))
	command.Env = append(os.Environ(),
		"GOOS="+platform.OS,
		"GOARCH="+platform.Architecture,
		"GOCACHE=/tmp/reploy-go-cache",
		"GOFLAGS=-buildvcs=false",
	)
	if platform.Architecture == "arm" {
		command.Env = append(command.Env, "GOARM=7")
	}
	if outputBytes, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s integration probe: %v\n%s", platform.Canonical, err, strings.TrimSpace(string(outputBytes)))
	}
	return output
}
