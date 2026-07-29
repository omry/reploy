package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/providerstore"
)

var runAbandonedProviderContainerCleanupCommand = runCommand

// removeAbandonedProviderContainers removes only helper containers whose
// deterministic names can be reconstructed from deployment-owned scratch.
// Callers hold the deployment operation lock, so none of these workspaces can
// belong to a live Reploy operation.
func removeAbandonedProviderContainers(ctx context.Context, store providerstore.Store) error {
	if ctx == nil {
		return fmt.Errorf("abandoned provider container cleanup requires a context")
	}
	temporaryRoot := filepath.Join(store.Root(), "tmp")
	entries, err := os.ReadDir(temporaryRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read provider store temporary directory for container recovery: %w", err)
	}

	names := []string{}
	for _, entry := range entries {
		switch {
		case temporaryWorkspaceName(entry.Name(), "probe-"):
			workspace := filepath.Join(temporaryRoot, entry.Name())
			names = append(names, imageProbeContainerName(workspace), pythonResolverContainerName(workspace))
		case temporaryWorkspaceName(entry.Name(), "apt-resolve-"):
			workspace := filepath.Join(temporaryRoot, entry.Name())
			names = append(names, aptResolverContainerName(workspace))
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if err := removeAbandonedProviderContainer(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

func temporaryWorkspaceName(name string, prefix string) bool {
	suffix := strings.TrimPrefix(name, prefix)
	if suffix == name || suffix == "" {
		return false
	}
	_, err := strconv.ParseUint(suffix, 10, 64)
	return err == nil
}

func removeAbandonedProviderContainer(ctx context.Context, name string) error {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runAbandonedProviderContainerCleanupCommand(
		CommandSpec{Name: "docker", Args: []string{"rm", "--force", name}},
		RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr},
	)
	if err == nil || strings.Contains(strings.ToLower(stderr.String()), "no such container") {
		return nil
	}
	output := trimmedCommandOutput(stderr.String())
	if output != "" {
		return fmt.Errorf("remove abandoned provider helper container %q: %w\ncommand output:\n%s", name, err, output)
	}
	return fmt.Errorf("remove abandoned provider helper container %q: %w", name, err)
}
