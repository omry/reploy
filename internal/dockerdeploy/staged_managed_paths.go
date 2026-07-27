package dockerdeploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
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
	if err := requireRealManagedPathDirectory(root, false); err != nil {
		return fmt.Errorf("staged managed path root: %w", err)
	}
	for _, mount := range plan.Mounts {
		if mount.Mode != blueprint.MountManagedBind {
			continue
		}
		relative, err := filepath.Rel(root, mount.Source)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return fmt.Errorf("staged managed path %q escapes deployment root", mount.Name)
		}
		current := root
		for _, part := range strings.Split(relative, string(filepath.Separator)) {
			if part == "" || part == "." {
				continue
			}
			current = filepath.Join(current, part)
			if err := requireRealManagedPathDirectory(current, true); err != nil {
				return fmt.Errorf("prepare staged managed path %q: %w", mount.Name, err)
			}
		}
	}
	return nil
}

func requireRealManagedPathDirectory(path string, create bool) error {
	for {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) && create {
			if err := os.Mkdir(path, 0o755); err != nil {
				if os.IsExist(err) {
					continue
				}
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("%s is not a real directory", path)
		}
		return nil
	}
}
