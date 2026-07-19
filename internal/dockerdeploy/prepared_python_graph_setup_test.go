package dockerdeploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	backend, cleanup, err := PreparePreparedPythonGraphBackend(
		context.Background(),
		store,
		request.Plan,
		descriptor,
		pythonConsumerTestImageConfig(),
		map[providers.NodeID]PreparedPythonNodeConfig{
			request.NodeID: {},
		},
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
		RunOptions{},
	)
	if err == nil || !strings.Contains(err.Error(), "final image config") {
		t.Fatalf("error = %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
}
