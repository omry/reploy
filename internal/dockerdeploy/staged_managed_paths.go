package dockerdeploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/fsowner"
)

// PrepareStagedManagedBindPathsV1 ensures that every Reploy-owned bind source
// exists after a staged image build. Existing directories are preserved; path
// components that are files or symlinks are rejected.
func PrepareStagedManagedBindPathsV1(deploymentDir string, plan DockerExecutionPlan) error {
	if plan.Phase != blueprint.PhaseStaged {
		return fmt.Errorf("prepare staged managed paths requires a staged Docker plan")
	}
	root, err := filepath.Abs(deploymentDir)
	if err != nil {
		return fmt.Errorf("resolve staged managed path root: %w", err)
	}
	root = filepath.Clean(root)
	held, _, err := fsowner.OpenDirectory(root)
	if err != nil {
		return fmt.Errorf("staged managed path root: %w", err)
	}
	defer held.Close()
	for _, mount := range plan.Mounts {
		if mount.Mode != blueprint.MountManagedBind {
			continue
		}
		relative, err := filepath.Rel(root, mount.Source)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return fmt.Errorf("staged managed path %q escapes deployment root", mount.Name)
		}
		if err := prepareManagedPathAt(held, relative); err != nil {
			return fmt.Errorf("prepare staged managed path %q: %w", mount.Name, err)
		}
	}
	return nil
}

func prepareManagedPathAt(root *os.Root, relative string) error {
	current := root
	defer func() {
		if current != root {
			_ = current.Close()
		}
	}()
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		info, err := current.Stat(".")
		if err != nil {
			return err
		}
		child, err := fsowner.FromInfo(info).EnsureDirectory(current, part, 0o755)
		if err != nil {
			return err
		}
		if current != root {
			_ = current.Close()
		}
		current = child
	}
	return nil
}

func requireRealManagedPathDirectory(path string, create bool) error {
	if !create {
		root, _, err := fsowner.OpenDirectory(path)
		if root != nil {
			_ = root.Close()
		}
		return err
	}
	parent, owner, err := fsowner.OpenDirectory(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer parent.Close()
	child, err := owner.EnsureDirectory(parent, filepath.Base(path), 0o755)
	if child != nil {
		_ = child.Close()
	}
	return err
}
