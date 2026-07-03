package dockerdeploy

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/deploy"
)

type configArtifactFileMount struct {
	HostRelative  string
	ContainerPath string
}

type configMountLayout struct {
	ContainerConfigDir string
	FileMounts         []configArtifactFileMount
}

func configMountLayoutForPack(pack deploy.AppPack) configMountLayout {
	fileMounts := configArtifactFileMounts(pack)
	containerConfigDir := "/config"
	if len(fileMounts) > 0 {
		containerConfigDir = "/config/" + cleanManifestPath(pack.Docker.DeploymentDirs.Config)
	}
	return configMountLayout{ContainerConfigDir: containerConfigDir, FileMounts: fileMounts}
}

func configArtifactFileMounts(pack deploy.AppPack) []configArtifactFileMount {
	artifact, ok := pack.Install.Upgrade.Artifacts["config"]
	if !ok {
		return nil
	}
	configDir := cleanManifestPath(pack.Docker.DeploymentDirs.Config)
	mounts := []configArtifactFileMount{}
	for _, rawPath := range artifact.Paths {
		trimmed := strings.TrimSpace(rawPath)
		if strings.HasSuffix(filepath.ToSlash(trimmed), "/") {
			continue
		}
		relativePath := cleanManifestPath(trimmed)
		if relativePath == configDir || strings.HasPrefix(relativePath, configDir+"/") {
			continue
		}
		mounts = append(mounts, configArtifactFileMount{
			HostRelative:  relativePath,
			ContainerPath: "/config/" + relativePath,
		})
	}
	sort.Slice(mounts, func(i int, j int) bool {
		return mounts[i].HostRelative < mounts[j].HostRelative
	})
	return mounts
}

func configArtifactFilePaths(mounts []configArtifactFileMount) []string {
	paths := make([]string, 0, len(mounts))
	for _, mount := range mounts {
		paths = append(paths, mount.HostRelative)
	}
	return paths
}

func cleanManifestPath(path string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(path))))
}

func ensureConfigArtifactFileMounts(dir string, pack deploy.AppPack) error {
	return ensureConfigArtifactFiles(dir, configArtifactFilePaths(configArtifactFileMounts(pack)))
}

func ensureConfigArtifactFileMountPlaceholders(dir string, pack deploy.AppPack) error {
	return ensureConfigArtifactFilePlaceholders(dir, configArtifactFilePaths(configArtifactFileMounts(pack)))
}

func ensureConfigArtifactFiles(dir string, relativePaths []string) error {
	for _, relativePath := range relativePaths {
		path := filepath.Join(dir, filepath.FromSlash(relativePath))
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			return fmt.Errorf("config artifact file is missing: %s", path)
		}
		if err != nil {
			return fmt.Errorf("inspect config artifact file %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("config artifact path must be a file: %s", path)
		}
	}
	return nil
}

func ensureConfigArtifactFilePlaceholders(dir string, relativePaths []string) error {
	for _, relativePath := range relativePaths {
		path := filepath.Join(dir, filepath.FromSlash(relativePath))
		info, err := os.Stat(path)
		if err == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("config artifact path must be a file: %s", path)
			}
			continue
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("inspect config artifact file %s: %w", path, err)
		}
		if _, lstatErr := os.Lstat(path); lstatErr == nil {
			return fmt.Errorf("config artifact path must be a file: %s", path)
		} else if !os.IsNotExist(lstatErr) {
			return fmt.Errorf("inspect config artifact file %s: %w", path, lstatErr)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("create config artifact file parent %s: %w", filepath.Dir(path), err)
		}
		file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if os.IsExist(err) {
				info, statErr := os.Stat(path)
				if statErr != nil {
					return fmt.Errorf("inspect config artifact file %s: %w", path, statErr)
				}
				if !info.Mode().IsRegular() {
					return fmt.Errorf("config artifact path must be a file: %s", path)
				}
				continue
			}
			return fmt.Errorf("create config artifact file placeholder %s: %w", path, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("create config artifact file placeholder %s: %w", path, err)
		}
	}
	return nil
}
