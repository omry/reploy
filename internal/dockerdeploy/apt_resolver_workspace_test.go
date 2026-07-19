package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	aptprovider "github.com/omry/reploy/internal/providers/apt"
	"github.com/omry/reploy/internal/providerstore"
)

func TestPrepareAPTResolverWorkspaceUsesDeploymentStoreAndCleansUp(t *testing.T) {
	deployment := t.TempDir()
	store, err := providerstore.NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PrepareAPTResolverWorkspace(store)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(prepared.HostDir, filepath.Join(deployment, ".reploy", providerstore.StoreDirName, "tmp")+string(os.PathSeparator)) {
		t.Fatalf("workspace = %s", prepared.HostDir)
	}
	if prepared.ContainerDir != aptprovider.ResolverScratchDirectory {
		t.Fatalf("container directory = %s", prepared.ContainerDir)
	}
	if err := os.WriteFile(filepath.Join(prepared.HostDir, "scratch"), []byte("temporary"), 0o600); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, err := os.Lstat(prepared.HostDir); !os.IsNotExist(err) {
		t.Fatalf("workspace survived cleanup: %v", err)
	}
}

func TestAPTResolverWorkspaceRejectsNonemptyDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unexpected"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := validatePreparedAPTResolverWorkspace(PreparedAPTResolverWorkspace{HostDir: dir, ContainerDir: aptprovider.ResolverScratchDirectory})
	if err == nil || !strings.Contains(err.Error(), "initially empty") {
		t.Fatalf("err = %v", err)
	}
}
