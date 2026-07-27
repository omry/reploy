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

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

func TestPythonResolverWheelIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx := context.Background()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	const base = "python:3.13-slim"
	inspection, err := runDockerOutput(ctx, "image", "inspect", base)
	if err != nil {
		t.Skipf("local %s image is required for resolver integration: %v", base, err)
	}
	descriptor, _, err := parseDockerImageInspection(base, platform, []byte(inspection))
	if err != nil {
		t.Fatal(err)
	}
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
	artifacts, cleanupArtifacts, err := PreparePythonResolverArtifacts(store, []providerstore.ArtifactDescriptor{wheel})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupArtifacts)
	probeWorkspace := buildIntegrationProbeWorkspace(t, platform)
	session, err := OpenPythonResolverSession(ctx, descriptor, probeWorkspace, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	consumer, err := ValidatePythonConsumer(ctx, session, pythonConsumerTestImageConfig())
	if err != nil {
		t.Fatal(err)
	}
	requirement := providers.ExecutableRequirement{
		ID: "interpreter", Command: "python", Supplier: "base", VersionConstraint: ">=3.11",
		ValidationPolicy: providers.ValidationPolicyCompatible,
	}
	interpreter, err := SelectPythonInterpreter(ctx, session, consumer.EnvironmentLauncher, requirement, []providers.RealizedOutput{{
		SupplierNode: "base", SupplierComponent: "base", Name: "python",
		Candidate: providers.ExecutableCandidate{InvocationPath: "/usr/local/bin/python3"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	packageRequest, err := pythonprovider.CanonicalPackageRequestV1("demo-server==1.0")
	if err != nil {
		t.Fatal(err)
	}
	request, err := pythonprovider.CanonicalProviderRequestV1(pythonprovider.PythonProviderRequestV1{
		Component: "application", Interpreter: blueprint.CommandRequirement{Command: "python", Supplier: "base"},
		Requirements: []providers.CanonicalPackageRequest{packageRequest},
	})
	if err != nil {
		t.Fatal(err)
	}
	source := testPythonResolvedSource(
		"application", "demo-server", "1.0",
		canonical.Digest(fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("demo source")))), wheel.SHA256,
	)
	if err := session.ResolveWheels(ctx, consumer.EnvironmentLauncher, requirement, interpreter, request, []providers.ResolvedSourceInput{source}, []providerstore.ArtifactDescriptor{wheel}); err != nil {
		t.Fatal(err)
	}
	if err := session.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(artifacts.OutputHostDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(wheelPath) {
		t.Fatalf("resolver output = %#v", entries)
	}
	content, err := os.ReadFile(filepath.Join(artifacts.OutputHostDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	digest := canonical.Digest(fmt.Sprintf("sha256:%x", sha256.Sum256(content)))
	if digest != wheel.SHA256 {
		t.Fatalf("resolver copied digest = %s, want %s", digest, wheel.SHA256)
	}
}

func TestPythonLocalSourceResolverIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx := context.Background()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	const base = "python:3.13-slim"
	inspection, err := runDockerOutput(ctx, "image", "inspect", base)
	if err != nil {
		t.Skipf("local %s image is required for source resolver integration: %v", base, err)
	}
	descriptor, _, err := parseDockerImageInspection(base, platform, []byte(inspection))
	if err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	artifacts, cleanupArtifacts, err := PreparePythonResolverArtifacts(store, []providerstore.ArtifactDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupArtifacts)
	workspace := t.TempDir()
	sourceDir := filepath.Join(workspace, "demo")
	if err := os.Mkdir(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pyproject := `[build-system]
requires = ["setuptools>=77"]
build-backend = "setuptools.build_meta"

[project]
name = "demo-server"
version = "1.0"

[project.scripts]
demo-server = "demo_server:main"
`
	if err := os.WriteFile(filepath.Join(sourceDir, "pyproject.toml"), []byte(pyproject), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "demo_server.py"), []byte("def main():\n    print('hello from workspace source')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, digest, err := ObservePythonSourceManifest(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	localSources := []PythonLocalSource{{
		Distribution: "demo-server", HostDir: sourceDir,
		Manifest: manifest, SourceManifestDigest: digest,
	}}
	snapshots, err := StagePythonLocalSourceSnapshots(artifacts, localSources)
	if err != nil {
		t.Fatal(err)
	}
	probeWorkspace := buildIntegrationProbeWorkspace(t, platform)
	session, err := OpenPythonResolverSession(ctx, descriptor, probeWorkspace, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	consumer, err := ValidatePythonConsumer(ctx, session, pythonConsumerTestImageConfig())
	if err != nil {
		t.Fatal(err)
	}
	requirement := providers.ExecutableRequirement{
		ID: "interpreter", Command: "python", Supplier: "base", VersionConstraint: ">=3.11",
		ValidationPolicy: providers.ValidationPolicyCompatible,
	}
	interpreter, err := SelectPythonInterpreter(ctx, session, consumer.EnvironmentLauncher, requirement, []providers.RealizedOutput{{
		SupplierNode: "base", SupplierComponent: "base", Name: "python",
		Candidate: providers.ExecutableCandidate{InvocationPath: "/usr/local/bin/python3"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.BuildSourceWheels(ctx, consumer.EnvironmentLauncher, requirement, interpreter, snapshots); err != nil {
		t.Fatal(err)
	}
	sources, wheels, err := PublishBuiltPythonSourceWheels(ctx, store, artifacts, "application", snapshots, []providerstore.ArtifactDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	packageRequest, err := pythonprovider.CanonicalPackageRequestV1("demo-server==1.0")
	if err != nil {
		t.Fatal(err)
	}
	request, err := pythonprovider.CanonicalProviderRequestV1(pythonprovider.PythonProviderRequestV1{
		Component: "application", Interpreter: blueprint.CommandRequirement{Command: "python", Supplier: "base"},
		Requirements: []providers.CanonicalPackageRequest{packageRequest},
		Overrides:    []pythonprovider.PythonPackageOverrideV1{{Distribution: "demo-server", Kind: "local"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.ResolveWheels(ctx, consumer.EnvironmentLauncher, requirement, interpreter, request, sources, wheels); err != nil {
		t.Fatal(err)
	}
	if err := session.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(artifacts.OutputHostDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].SourceManifestDigest != localSources[0].SourceManifestDigest ||
		len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "demo_server-1.0-") || !strings.HasSuffix(entries[0].Name(), ".whl") {
		t.Fatalf("source resolver result = sources %#v, entries %#v", sources, entries)
	}
}

func buildIntegrationProbeWorkspace(t *testing.T, platform blueprint.Platform) PreparedProbeWorkspace {
	t.Helper()
	dir := t.TempDir()
	executable := filepath.Join(dir, filepath.Base(ProbeContainerExecutable))
	command := exec.Command("go", "build", "-o", executable, "./cmd/reploy-probe")
	command.Dir = filepath.Clean(filepath.Join("..", ".."))
	command.Env = append(
		os.Environ(),
		"CGO_ENABLED=0",
		"GOOS="+platform.OS,
		"GOARCH="+platform.Architecture,
		"GOCACHE=/tmp/reploy-go-cache",
		"GOFLAGS=-buildvcs=false",
	)
	if platform.Architecture == "arm" && strings.HasPrefix(platform.Variant, "v") {
		command.Env = append(command.Env, "GOARM="+strings.TrimPrefix(platform.Variant, "v"))
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build integration probe: %v\n%s", err, strings.TrimSpace(string(output)))
	}
	content, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	digest := canonical.Digest(fmt.Sprintf("sha256:%x", sha256.Sum256(content)))
	return PreparedProbeWorkspace{
		HostDir: dir, HostExecutable: executable,
		ContainerDir: ProbeContainerRoot, ContainerExecutable: ProbeContainerExecutable,
		ReadOnly: true, Platform: platform, SHA256: digest,
	}
}
