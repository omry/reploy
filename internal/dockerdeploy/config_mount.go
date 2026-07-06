package dockerdeploy

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/deploy"
)

type managedPathMount struct {
	HostRelative  string
	ContainerPath string
	Mode          string
}

type configMountLayout struct {
	ContainerConfigDir string
	Mounts             []managedPathMount
	FileMounts         []string
}

func configMountLayoutForPack(pack deploy.AppPack) configMountLayout {
	configDir := cleanManifestPath(pack.Docker.DeploymentDirs.Config)
	layout := configMountLayout{ContainerConfigDir: "/config"}
	for _, dir := range pack.Install.ManagedPaths.Dirs {
		mount := strings.TrimSpace(dir.Mount)
		if mount == "" {
			continue
		}
		relativePath := cleanManifestPath(dir.Path)
		if relativePath == configDir {
			layout.ContainerConfigDir = mount
		}
		layout.Mounts = append(layout.Mounts, managedPathMount{
			HostRelative:  relativePath,
			ContainerPath: mount,
			Mode:          managedPathMountMode(dir),
		})
	}
	for _, file := range pack.Install.ManagedPaths.Files {
		relativePath := cleanManifestPath(file.Path)
		layout.FileMounts = append(layout.FileMounts, relativePath)
		mount := strings.TrimSpace(file.Mount)
		if mount == "" {
			continue
		}
		layout.Mounts = append(layout.Mounts, managedPathMount{
			HostRelative:  relativePath,
			ContainerPath: mount,
			Mode:          managedPathMountMode(file),
		})
	}
	sort.Slice(layout.Mounts, func(i int, j int) bool {
		return layout.Mounts[i].HostRelative < layout.Mounts[j].HostRelative
	})
	sort.Strings(layout.FileMounts)
	return layout
}

func managedPathMountMode(entry deploy.InstallManagedPathConfig) string {
	if entry.RuntimeReadonly != nil && !*entry.RuntimeReadonly {
		return "rw"
	}
	return "${REPLOY_CONFIG_MOUNT:-ro}"
}

func managedPathNames(managedPaths deploy.InstallManagedPathsConfig) []string {
	names := make([]string, 0, len(managedPaths.Files)+len(managedPaths.Dirs))
	for _, file := range managedPaths.Files {
		names = append(names, cleanManifestPath(file.Path))
	}
	for _, dir := range managedPaths.Dirs {
		names = append(names, cleanManifestPath(dir.Path))
	}
	sort.Strings(names)
	return names
}

func managedDirPaths(managedPaths deploy.InstallManagedPathsConfig) []string {
	paths := make([]string, 0, len(managedPaths.Dirs))
	for _, dir := range managedPaths.Dirs {
		paths = append(paths, cleanManifestPath(dir.Path))
	}
	sort.Strings(paths)
	return paths
}

func cleanManifestPath(path string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(path))))
}

func ensureManagedFileMountsForPack(dir string, pack deploy.AppPack) error {
	return ensureManagedFiles(dir, configMountLayoutForPack(pack).FileMounts)
}

func ensureManagedFilePlaceholdersForPack(dir string, pack deploy.AppPack) error {
	return ensureManagedFilePlaceholders(dir, configMountLayoutForPack(pack).FileMounts)
}

func ensureManagedFiles(dir string, relativePaths []string) error {
	for _, relativePath := range relativePaths {
		path := filepath.Join(dir, filepath.FromSlash(relativePath))
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			return fmt.Errorf("managed file is missing: %s", path)
		}
		if err != nil {
			return fmt.Errorf("inspect managed file %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("managed path must be a file: %s", path)
		}
	}
	return nil
}

func ensureManagedFilePlaceholders(dir string, relativePaths []string) error {
	for _, relativePath := range relativePaths {
		path := filepath.Join(dir, filepath.FromSlash(relativePath))
		info, err := os.Stat(path)
		if err == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("managed path must be a file: %s", path)
			}
			continue
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("inspect managed file %s: %w", path, err)
		}
		if _, lstatErr := os.Lstat(path); lstatErr == nil {
			return fmt.Errorf("managed path must be a file: %s", path)
		} else if !os.IsNotExist(lstatErr) {
			return fmt.Errorf("inspect managed file %s: %w", path, lstatErr)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("create managed file parent %s: %w", filepath.Dir(path), err)
		}
		file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if os.IsExist(err) {
				info, statErr := os.Stat(path)
				if statErr != nil {
					return fmt.Errorf("inspect managed file %s: %w", path, statErr)
				}
				if !info.Mode().IsRegular() {
					return fmt.Errorf("managed path must be a file: %s", path)
				}
				continue
			}
			return fmt.Errorf("create managed file placeholder %s: %w", path, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("create managed file placeholder %s: %w", path, err)
		}
	}
	return nil
}
