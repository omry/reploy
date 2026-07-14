package dockerdeploy

import (
	"archive/zip"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

func TestPythonGeneratedImageIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	base := "python:3.13-slim"
	runDockerIntegration(t, ctx, "pull", base)
	baseDigest := strings.TrimSpace(runDockerIntegration(t, ctx, "image", "inspect", "--format", "{{index .RepoDigests 0}}", base))
	bundleDir := t.TempDir()
	writePythonIntegrationWheel(t, filepath.Join(bundleDir, "demo_server-1.0-py3-none-any.whl"))
	request := providers.ResolveRequest{
		Platform: "linux/amd64", BaseImage: base,
		Components:  []providers.Component{{Name: "application", Requirements: []string{"demo-server==1.0"}}},
		Executables: []providers.ExecutableRequest{{Name: "server", Component: "application", Binary: "demo-server"}},
	}
	provider := pythonprovider.ComponentProvider{Resolver: pythonprovider.PreparedBundleResolver{Dir: bundleDir, BaseIdentity: baseDigest}}
	bundle, err := provider.Resolve(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	materialization, err := provider.Materialize(providers.MaterializeRequest{Bundle: bundle})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := generatedImageIdentity("python-integration", t.TempDir(), GeneratedImageStaging, []providers.Bundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		command := exec.Command("docker", "image", "rm", "--force", identity.Reference)
		_ = command.Run()
	})
	if err := BuildGeneratedImage(GeneratedImagePlan{
		BaseImage: base, BaseIdentity: baseDigest, Tag: identity.Reference, BundleDir: bundleDir,
		Materialization: materialization, Labels: identity.Labels,
	}, RunOptions{Context: ctx}); err != nil {
		t.Fatal(err)
	}
	output := runDockerIntegration(t, ctx, "run", "--rm", identity.Reference, bundle.Executables["server"].ImagePath)
	if strings.TrimSpace(output) != "hello from generated Python image" {
		t.Fatalf("console script output = %q", output)
	}
}

func writePythonIntegrationWheel(t *testing.T, filename string) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	files := map[string]string{
		"demo_server.py":                             "def main():\n    print('hello from generated Python image')\n",
		"demo_server-1.0.dist-info/METADATA":         "Metadata-Version: 2.1\nName: demo-server\nVersion: 1.0\n\n",
		"demo_server-1.0.dist-info/WHEEL":            "Wheel-Version: 1.0\nGenerator: reploy-integration\nRoot-Is-Purelib: true\nTag: py3-none-any\n",
		"demo_server-1.0.dist-info/entry_points.txt": "[console_scripts]\ndemo-server = demo_server:main\n",
		"demo_server-1.0.dist-info/RECORD":           "",
	}
	for name, content := range files {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
