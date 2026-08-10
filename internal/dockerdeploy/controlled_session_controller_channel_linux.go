//go:build linux

package dockerdeploy

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/omry/reploy/internal/deploy"
)

func requirePreparedControlledSessionControllerChannelV1(plan ControlledSessionContainerPlanV1) error {
	var source string
	for _, mount := range plan.Mounts {
		if mount.Name == "session-channel" {
			if mount.Type != "bind" || mount.SourceKind != deploy.RuntimeMountSourceDirectory || mount.Target != controlledSessionChannelRootV1 || !mount.ReadOnly {
				return fmt.Errorf("controller session-channel mount does not match the private read-only bind contract")
			}
			source = mount.Source
			break
		}
	}
	if source == "" {
		return fmt.Errorf("controller plan does not contain the private session-channel mount")
	}
	directory, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect private channel directory %q: %w", source, err)
	}
	if !directory.IsDir() || directory.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private channel source %q is not a real directory", source)
	}
	socket := filepath.Join(source, controlledSessionChannelSocketNameV1)
	info, err := os.Lstat(socket)
	if err != nil {
		return fmt.Errorf("inspect private channel socket %q: %w", socket, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("private channel path %q is not a Unix socket", socket)
	}
	return nil
}
