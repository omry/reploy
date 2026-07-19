package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/probearchive"
	"github.com/omry/reploy/internal/providerstore"
)

const (
	ProbeContainerRoot       = "/.reploy-validation"
	ProbeContainerExecutable = ProbeContainerRoot + "/" + probearchive.ExtractedFileName
)

type PreparedProbeWorkspace struct {
	HostDir             string
	HostExecutable      string
	ContainerDir        string
	ContainerExecutable string
	ReadOnly            bool
	Platform            blueprint.Platform
	SHA256              canonical.Digest
}

var locateProbeArchiveExecutable = os.Executable

// PrepareProbeWorkspace extracts one platform helper beneath the deployment's
// private provider store. The returned directory is the complete read-only
// bind-mount source and must be removed with cleanup after the container exits.
func PrepareProbeWorkspace(ctx context.Context, store providerstore.Store, platform blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
	noCleanup := func() error { return nil }
	if ctx == nil {
		return PreparedProbeWorkspace{}, noCleanup, fmt.Errorf("probe workspace context is required")
	}
	if err := ctx.Err(); err != nil {
		return PreparedProbeWorkspace{}, noCleanup, fmt.Errorf("prepare probe workspace: %w", err)
	}
	if err := platform.Validate(); err != nil {
		return PreparedProbeWorkspace{}, noCleanup, fmt.Errorf("probe workspace platform: %w", err)
	}
	if platform.OS != "linux" || !probearchive.Supports(platform.Canonical) {
		return PreparedProbeWorkspace{}, noCleanup, fmt.Errorf("probe workspace does not support platform %q", platform.Canonical)
	}
	executable, err := locateProbeArchiveExecutable()
	if err != nil {
		return PreparedProbeWorkspace{}, noCleanup, fmt.Errorf("locate Reploy probe archive: %w", err)
	}
	workspace, err := store.NewWorkspace("probe-*")
	if err != nil {
		return PreparedProbeWorkspace{}, noCleanup, err
	}
	cleanup := func() error {
		if err := os.RemoveAll(workspace); err != nil {
			return fmt.Errorf("remove probe workspace: %w", err)
		}
		return nil
	}
	extracted, err := probearchive.Extract(ctx, executable, platform.Canonical, workspace)
	if err != nil {
		return PreparedProbeWorkspace{}, noCleanup, errors.Join(err, cleanup())
	}
	if filepath.Dir(extracted.Path) != workspace || filepath.Base(extracted.Path) != probearchive.ExtractedFileName {
		return PreparedProbeWorkspace{}, noCleanup, errors.Join(fmt.Errorf("extracted probe escaped its workspace"), cleanup())
	}
	prepared := PreparedProbeWorkspace{
		HostDir: workspace, HostExecutable: extracted.Path,
		ContainerDir: ProbeContainerRoot, ContainerExecutable: ProbeContainerExecutable,
		ReadOnly: true, Platform: platform, SHA256: extracted.SHA256,
	}
	return prepared, cleanup, nil
}
