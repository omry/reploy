package providerstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/omry/reploy/internal/fsowner"
)

type ownedWorkspaces struct {
	mutex sync.Mutex
	roots []*os.Root
}

// WithWorkspaceOwnerRestoration scopes descriptor retention to one privileged
// source operation. Its caller must defer RestoreWorkspaceOwners before work.
func (store Store) WithWorkspaceOwnerRestoration() Store {
	if store.owner.Enabled() && store.workspaces == nil {
		store.workspaces = &ownedWorkspaces{}
	}
	return store
}

func (store Store) ensureDirectory(path string) error {
	parent, _, err := fsowner.OpenDirectory(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer parent.Close()
	child, err := store.owner.EnsureDirectory(parent, filepath.Base(path), 0o755)
	if child != nil {
		_ = child.Close()
	}
	if err != nil {
		return fmt.Errorf("provider store path must be a real directory %s: %w", path, err)
	}
	return nil
}

func (store Store) retainWorkspace(workspace string) error {
	if store.workspaces == nil {
		return nil
	}
	info, err := os.Lstat(workspace)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return err
	}
	opened, err := root.Stat(".")
	if err != nil || !info.IsDir() || !os.SameFile(info, opened) {
		_ = root.Close()
		return fmt.Errorf("private workspace changed before owner retention")
	}
	store.workspaces.mutex.Lock()
	store.workspaces.roots = append(store.workspaces.roots, root)
	store.workspaces.mutex.Unlock()
	return nil
}

// RestoreWorkspaceOwners runs after privileged source preparation on success,
// failure and cancellation, while its operation lock is still held. Only newly
// created scratch roots are visited; existing application data is not walked.
func (store Store) RestoreWorkspaceOwners() error {
	if store.workspaces == nil {
		return nil
	}
	store.workspaces.mutex.Lock()
	roots := store.workspaces.roots
	store.workspaces.roots = nil
	store.workspaces.mutex.Unlock()
	var result error
	for _, root := range roots {
		result = errors.Join(result, store.owner.Tree(root), root.Close())
	}
	return result
}
