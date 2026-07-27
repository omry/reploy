package dockerdeploy

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestPreparePreparedPythonGraphBackendUsesDeploymentStoreAndOneHelper(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	request := preparedPythonResolveRequest(t, descriptor)
	packed := packedProbeExecutable(t)
	previousLocate := locateProbeArchiveExecutable
	locateProbeArchiveExecutable = func() (string, error) { return packed, nil }
	t.Cleanup(func() { locateProbeArchiveExecutable = previousLocate })
	deploymentRoot := t.TempDir()
	store, err := providerstore.NewStore(deploymentRoot)
	if err != nil {
		t.Fatal(err)
	}
	workspaceSources := sourceWheelTestWorkspaceSources(t, "demo-pkg")
	backend, cleanup, err := PreparePreparedPythonGraphBackend(
		context.Background(),
		store,
		request.Plan,
		descriptor,
		pythonConsumerTestImageConfig(),
		map[providers.NodeID]PreparedPythonNodeConfig{
			request.NodeID: {WorkspaceSources: workspaceSources},
		},
		map[providers.NodeID]PreparedAPTNodeConfig{},
		RunOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	operation, found := backend.Operations[request.NodeID]
	if !found || operation.Store.Root() != store.Root() || operation.Validators.Profile == nil || operation.Validators.Bundle == nil {
		t.Fatalf("operation = %#v, found = %v", operation, found)
	}
	if got := operation.FinalImageConfig; got.User != "0:0" || got.WorkingDir != "/" {
		t.Fatalf("operation final image config = %#v", got)
	}
	if filepath.Dir(operation.Artifacts.HostDir) != filepath.Join(store.Root(), "tmp") {
		t.Fatalf("operation resolver artifacts = %#v", operation.Artifacts)
	}
	if len(operation.SourceSnapshots) != 1 || operation.SourceSnapshots[0].Distribution != "demo-pkg" ||
		operation.SourceSnapshots[0].SourceManifestDigest != workspaceSources[0].SourceManifestDigest {
		t.Fatalf("operation source snapshots = %#v", operation.SourceSnapshots)
	}
	if backend.Workspace.HostDir == "" || !strings.HasPrefix(backend.Workspace.HostDir, store.Root()+string(os.PathSeparator)) {
		t.Fatalf("workspace = %#v, store = %s", backend.Workspace, store.Root())
	}
	if backend.Materializer.Store.Root() != store.Root() || backend.Materializer.RunEvidence == nil || backend.Materializer.Platform != descriptor.Platform {
		t.Fatalf("materializer = %#v", backend.Materializer)
	}
	operation.verifiedArtifacts[rendererDigest("d")] = "/tmp/verified-wheel"
	if backend.Materializer.verifiedArtifacts[request.NodeID][rendererDigest("d")] != "/tmp/verified-wheel" {
		t.Fatal("resolver and materializer do not share verified artifact paths")
	}
	workspace := backend.Workspace.HostDir
	artifactWorkspace := operation.Artifacts.HostDir
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("workspace still exists: %v", err)
	}
	if _, err := os.Stat(artifactWorkspace); !os.IsNotExist(err) {
		t.Fatalf("artifact workspace still exists: %v", err)
	}
}

func TestPreparePreparedPythonGraphBackendConfiguresAPTNode(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	pythonRequest := preparedPythonResolveRequest(t, descriptor)
	aptNode := aptResolverTestNode(t, aptResolverTestRequest(t, blueprint.APTPackageRequest{Name: "hello"}))
	plan := providers.ProviderPlanV1{
		Schema: providers.ProviderPlanSchemaV1,
		Nodes:  []providers.NodeSpec{aptNode, pythonRequest.Plan.Nodes[0]},
		Edges:  []providers.ProviderEdgeV1{},
	}
	if err := providers.ValidateProviderPlanV1(plan); err != nil {
		t.Fatal(err)
	}
	packed := packedProbeExecutable(t)
	previousLocate := locateProbeArchiveExecutable
	locateProbeArchiveExecutable = func() (string, error) { return packed, nil }
	t.Cleanup(func() { locateProbeArchiveExecutable = previousLocate })
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runOptions := RunOptions{Stdout: &stdout, Stderr: &stderr, DockerPreflightTimeout: 17}
	backend, cleanup, err := PreparePreparedPythonGraphBackend(
		context.Background(), store, plan, descriptor, pythonConsumerTestImageConfig(),
		map[providers.NodeID]PreparedPythonNodeConfig{},
		map[providers.NodeID]PreparedAPTNodeConfig{"apt": {ExclusiveRoots: []string{"/usr"}}},
		runOptions,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cleanup() })
	operation, found := backend.APTOperations["apt"]
	if !found || operation.Store.Root() != store.Root() || operation.Validators.Profile == nil || operation.Validators.Bundle == nil {
		t.Fatalf("APT operation = %#v, found = %v", operation, found)
	}
	if len(operation.ExclusiveRoots) != 1 || operation.ExclusiveRoots[0] != "/usr" || backend.Materializer.verifiedArtifacts["apt"] != nil {
		t.Fatalf("APT operation = %#v, verified = %#v", operation, backend.Materializer.verifiedArtifacts["apt"])
	}
	if operation.RunOptions.Stdout != &stdout || operation.RunOptions.Stderr != &stderr || operation.RunOptions.DockerPreflightTimeout != runOptions.DockerPreflightTimeout {
		t.Fatalf("APT run options = %#v, want %#v", operation.RunOptions, runOptions)
	}
}

func TestPreparePreparedPythonGraphBackendRejectsConfigOutsidePlan(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	request := preparedPythonResolveRequest(t, descriptor)
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, cleanup, err := PreparePreparedPythonGraphBackend(
		context.Background(),
		store,
		request.Plan,
		descriptor,
		pythonConsumerTestImageConfig(),
		map[providers.NodeID]PreparedPythonNodeConfig{
			"python/other": {},
		},
		map[providers.NodeID]PreparedAPTNodeConfig{},
		RunOptions{},
	)
	if err == nil || !strings.Contains(err.Error(), "unsupported node") {
		t.Fatalf("error = %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestPreparePreparedPythonGraphBackendRejectsInvalidSharedImageConfigBeforeScratch(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	request := preparedPythonResolveRequest(t, descriptor)
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	invalid := pythonConsumerTestImageConfig()
	invalid.WorkingDir = "relative"
	_, cleanup, err := PreparePreparedPythonGraphBackend(
		context.Background(), store, request.Plan, descriptor, invalid,
		map[providers.NodeID]PreparedPythonNodeConfig{request.NodeID: {}},
		map[providers.NodeID]PreparedAPTNodeConfig{},
		RunOptions{},
	)
	if err == nil || !strings.Contains(err.Error(), "final image config") {
		t.Fatalf("error = %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
}
