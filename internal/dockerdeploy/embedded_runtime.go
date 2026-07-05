package dockerdeploy

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/omry/reploy/internal/deploy"
)

var embeddedRuntimeExecutable = os.Executable

func embeddedRuntimeFileName() string {
	if currentHostPlatform().GOOS == "windows" {
		return ToolBinaryFileName + ".exe"
	}
	return ToolBinaryFileName
}

func writeEmbeddedRuntime(dir string, manifest *deploy.DeploymentManifest) (deploy.RuntimeState, error) {
	sourcePath, err := embeddedRuntimeExecutable()
	if err != nil {
		return deploy.RuntimeState{}, fmt.Errorf("locate reploy runtime: %w", err)
	}
	content, err := os.ReadFile(sourcePath)
	if err != nil {
		return deploy.RuntimeState{}, fmt.Errorf("read reploy runtime: %w", err)
	}
	relativePath := embeddedRuntimeFileName()
	if err := deploy.WriteGeneratedFile(dir, relativePath, content, true, manifest); err != nil {
		return deploy.RuntimeState{}, err
	}
	entry := manifest.Files[filepath.ToSlash(relativePath)]
	entry.Kind = "runtime"
	manifest.Files[filepath.ToSlash(relativePath)] = entry
	return deploy.RuntimeState{
		Path:        filepath.ToSlash(relativePath),
		ToolVersion: deploy.ToolVersion,
		SHA256:      entry.SHA256,
	}, nil
}

func updateEmbeddedRuntime(dir string, manifest *deploy.DeploymentManifest, force bool) (deploy.RuntimeState, deploy.UpdateStatus, error) {
	sourcePath, err := embeddedRuntimeExecutable()
	if err != nil {
		return deploy.RuntimeState{}, "", fmt.Errorf("locate reploy runtime: %w", err)
	}
	content, err := os.ReadFile(sourcePath)
	if err != nil {
		return deploy.RuntimeState{}, "", fmt.Errorf("read reploy runtime: %w", err)
	}
	relativePath := embeddedRuntimeFileName()
	status, err := deploy.UpdateGeneratedFile(dir, relativePath, content, true, manifest, force)
	if err != nil {
		return deploy.RuntimeState{}, "", err
	}
	if status == deploy.UpdateStatusSkipped {
		return deploy.RuntimeState{}, "", fmt.Errorf("refusing to overwrite locally modified generated files: %s; rerun with --force to overwrite", relativePath)
	}
	entry := manifest.Files[filepath.ToSlash(relativePath)]
	entry.Kind = "runtime"
	manifest.Files[filepath.ToSlash(relativePath)] = entry
	return deploy.RuntimeState{
		Path:        filepath.ToSlash(relativePath),
		ToolVersion: deploy.ToolVersion,
		SHA256:      entry.SHA256,
	}, status, nil
}

func writeInstalledEmbeddedRuntime(plan installPlan) (deploy.RuntimeState, error) {
	sourcePath, err := embeddedRuntimeExecutable()
	if err != nil {
		return deploy.RuntimeState{}, fmt.Errorf("locate reploy runtime: %w", err)
	}
	content, err := os.ReadFile(sourcePath)
	if err != nil {
		return deploy.RuntimeState{}, fmt.Errorf("read reploy runtime: %w", err)
	}
	relativePath := embeddedRuntimeFileName()
	targetPath := filepath.Join(plan.TargetDir, filepath.FromSlash(relativePath))
	if err := writeInstallFileNoFollow(targetPath, content, 0o755); err != nil {
		return deploy.RuntimeState{}, err
	}
	manifest, err := loadManifestOrNew(plan.TargetDir)
	if err != nil {
		return deploy.RuntimeState{}, err
	}
	hash := deploy.HashBytes(content)
	manifest.Files[filepath.ToSlash(relativePath)] = deploy.GeneratedFile{
		Kind:   "runtime",
		SHA256: hash,
	}
	if err := deploy.WriteDeploymentManifest(filepath.Join(plan.TargetDir, ManifestFileName), manifest); err != nil {
		return deploy.RuntimeState{}, err
	}
	return deploy.RuntimeState{
		Path:        filepath.ToSlash(relativePath),
		ToolVersion: deploy.ToolVersion,
		SHA256:      hash,
	}, nil
}

func embeddedRuntimeStateForDir(dir string) (*deploy.RuntimeState, error) {
	relativePath := embeddedRuntimeFileName()
	hash, err := deploy.HashFile(filepath.Join(dir, filepath.FromSlash(relativePath)))
	if err != nil {
		return nil, err
	}
	return &deploy.RuntimeState{
		Path:        filepath.ToSlash(relativePath),
		ToolVersion: deploy.ToolVersion,
		SHA256:      hash,
	}, nil
}

func validateEmbeddedRuntimeForControl(dir string) error {
	if os.Getenv("REPLOY_SYSTEM_RUNTIME") == "1" {
		return nil
	}
	state, err := loadState(dir)
	if err != nil {
		return err
	}
	if state.Runtime == nil || state.Runtime.Path == "" || state.Runtime.SHA256 == "" {
		return fmt.Errorf("embedded Reploy runtime metadata is missing; repair with `reploy stage --update --dir %s --force` or reinstall the app", dir)
	}
	hash, err := deploy.HashFile(filepath.Join(dir, filepath.FromSlash(state.Runtime.Path)))
	if err != nil {
		return fmt.Errorf("embedded Reploy runtime is missing or unreadable: %w; repair with `reploy stage --update --dir %s --force` or reinstall the app", err, dir)
	}
	if hash != state.Runtime.SHA256 {
		return fmt.Errorf("embedded Reploy runtime is invalid; repair with `reploy stage --update --dir %s --force` or reinstall the app", dir)
	}
	return nil
}
