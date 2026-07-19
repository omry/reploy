package dockerdeploy

import (
	"fmt"
	"os"
	"path/filepath"

	aptprovider "github.com/omry/reploy/internal/providers/apt"
	"github.com/omry/reploy/internal/providerstore"
)

// PreparedAPTResolverWorkspace is one initially empty, deployment-local
// resolver scratch directory. It is removed after the containing operation.
type PreparedAPTResolverWorkspace struct {
	HostDir      string
	ContainerDir string
}

func PrepareAPTResolverWorkspace(store providerstore.Store) (PreparedAPTResolverWorkspace, func(), error) {
	workspace, err := store.NewWorkspace("apt-resolve-*")
	if err != nil {
		return PreparedAPTResolverWorkspace{}, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(workspace) }
	prepared := PreparedAPTResolverWorkspace{HostDir: workspace, ContainerDir: aptprovider.ResolverScratchDirectory}
	if err := validatePreparedAPTResolverWorkspace(prepared); err != nil {
		cleanup()
		return PreparedAPTResolverWorkspace{}, func() {}, err
	}
	return prepared, cleanup, nil
}

func validatePreparedAPTResolverWorkspace(prepared PreparedAPTResolverWorkspace) error {
	if prepared.HostDir == "" || !filepath.IsAbs(prepared.HostDir) || filepath.Clean(prepared.HostDir) != prepared.HostDir {
		return fmt.Errorf("APT resolver workspace host path must be absolute and clean")
	}
	info, err := os.Lstat(prepared.HostDir)
	if err != nil {
		return fmt.Errorf("inspect APT resolver workspace: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("APT resolver workspace must be a private real directory")
	}
	if prepared.ContainerDir != aptprovider.ResolverScratchDirectory {
		return fmt.Errorf("APT resolver workspace must use container path %q", aptprovider.ResolverScratchDirectory)
	}
	entries, err := os.ReadDir(prepared.HostDir)
	if err != nil {
		return fmt.Errorf("read APT resolver workspace: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("APT resolver workspace must be initially empty")
	}
	return nil
}
