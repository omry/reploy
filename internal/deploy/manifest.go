package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type UpdateStatus string

const (
	UpdateStatusUpdated  UpdateStatus = "updated"
	UpdateStatusUpToDate UpdateStatus = "up_to_date"
	UpdateStatusSkipped  UpdateStatus = "skipped"
	UpdateStatusRemoved  UpdateStatus = "removed"
)

func NewDeploymentManifest(generator string) DeploymentManifest {
	return DeploymentManifest{
		SchemaVersion: 1,
		Generator:     generator,
		Files:         map[string]GeneratedFile{},
	}
}

func HashBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func HashFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return HashBytes(content), nil
}

func LoadDeploymentManifest(path string) (DeploymentManifest, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return DeploymentManifest{}, err
	}
	var manifest DeploymentManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return DeploymentManifest{}, err
	}
	if manifest.Files == nil {
		manifest.Files = map[string]GeneratedFile{}
	}
	return manifest, nil
}

func WriteDeploymentManifest(path string, manifest DeploymentManifest) error {
	content, err := MarshalDeploymentManifest(manifest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

func WriteDeploymentManifestIfChanged(path string, manifest DeploymentManifest) (UpdateStatus, error) {
	content, err := MarshalDeploymentManifest(manifest)
	if err != nil {
		return "", err
	}
	return WriteFileIfChanged(path, content, 0o644)
}

func MarshalDeploymentManifest(manifest DeploymentManifest) ([]byte, error) {
	content, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}

func WriteFileIfChanged(path string, content []byte, mode os.FileMode) (UpdateStatus, error) {
	current, err := os.ReadFile(path)
	if err == nil && string(current) == string(content) {
		return UpdateStatusUpToDate, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		return "", err
	}
	return UpdateStatusUpdated, nil
}

func WriteGeneratedFile(root string, relativePath string, content []byte, executable bool, manifest *DeploymentManifest) error {
	path := filepath.Join(root, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if executable {
		mode = 0o755
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		return err
	}
	manifest.Files[filepath.ToSlash(relativePath)] = GeneratedFile{
		Kind:   "template",
		SHA256: HashBytes(content),
	}
	return nil
}

func UpdateGeneratedFile(root string, relativePath string, content []byte, executable bool, manifest *DeploymentManifest, force bool) (UpdateStatus, error) {
	relativePath = filepath.ToSlash(relativePath)
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	desiredHash := HashBytes(content)

	currentHash, err := HashFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return UpdateStatusUpdated, WriteGeneratedFile(root, relativePath, content, executable, manifest)
		}
		return "", err
	}
	if currentHash == desiredHash {
		manifest.Files[relativePath] = GeneratedFile{Kind: "template", SHA256: currentHash}
		if executable {
			if err := ensureExecutable(path); err != nil {
				return "", err
			}
		}
		return UpdateStatusUpToDate, nil
	}

	previous, ok := manifest.Files[relativePath]
	if !ok {
		if !force {
			return UpdateStatusSkipped, nil
		}
		return UpdateStatusUpdated, WriteGeneratedFile(root, relativePath, content, executable, manifest)
	}
	if previous.SHA256 != currentHash {
		if !force {
			return UpdateStatusSkipped, nil
		}
		return UpdateStatusUpdated, WriteGeneratedFile(root, relativePath, content, executable, manifest)
	}
	return UpdateStatusUpdated, WriteGeneratedFile(root, relativePath, content, executable, manifest)
}

func ensureExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := info.Mode()
	if mode&0o111 != 0 {
		return nil
	}
	if err := os.Chmod(path, mode|0o755); err != nil {
		return fmt.Errorf("make executable: %w", err)
	}
	return nil
}
