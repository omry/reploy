package dockerdeploy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/deploy"
)

type UpdateOptions struct {
	Dir   string
	Pack  deploy.PackRef
	Force bool
}

type UpdateResult struct {
	Path      string
	Status    deploy.UpdateStatus
	Ownership string
	Reason    string
}

type generatedUpdate struct {
	RelativePath string
	Content      []byte
	Executable   bool
}

func Update(options UpdateOptions) ([]UpdateResult, error) {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	ref := options.Pack
	var state deploy.DeploymentState
	if ref.Raw == "" {
		loadedState, err := loadState(options.Dir)
		if err != nil {
			return nil, err
		}
		state = loadedState
		ref = state.Blueprint
	} else if loadedState, err := loadState(options.Dir); err == nil {
		state = loadedState
	}
	if ref.Raw == "" {
		return nil, fmt.Errorf("blueprint reference is required")
	}
	var pack deploy.AppPack
	var err error
	if options.Pack.Raw == "" {
		pack, err = deploy.LoadResolvedPack(ref, state.RequestedBlueprintRef, state.ResolvedArtifact)
	} else {
		pack, err = deploy.LoadPack(ref)
	}
	if err != nil {
		return nil, err
	}
	bundle := state.Bundle
	if len(bundle.Roots) == 0 {
		bundle, err = inferBundleState(options.Dir, pack)
		if err != nil {
			return nil, err
		}
	}
	bundle.PreparedFingerprint = ""

	manifest, err := loadManifestOrNew(options.Dir)
	if err != nil {
		return nil, err
	}
	dockerIdentity, err := deploymentDockerIdentity(pack, state, options.Dir)
	if err != nil {
		return nil, err
	}
	deployedCommands := pack.Docker.DeployedCommands()
	if err := validateDeployedControlCommands(deployedCommands); err != nil {
		return nil, err
	}
	generatedUpdates := []generatedUpdate{
		{RelativePath: controlScriptName(pack.AppID), Content: []byte(stagingControlScriptContent(pack, deployedCommands)), Executable: true},
	}
	currentGenerated := map[string]bool{}
	for _, generated := range generatedUpdates {
		currentGenerated[filepath.ToSlash(generated.RelativePath)] = true
	}
	if !options.Force {
		conflicts, err := locallyModifiedGeneratedFiles(options.Dir, generatedUpdates, manifest)
		if err != nil {
			return nil, err
		}
		removedConflicts, err := locallyModifiedRemovedGeneratedFiles(options.Dir, currentGenerated, manifest)
		if err != nil {
			return nil, err
		}
		conflicts = append(conflicts, removedConflicts...)
		if len(conflicts) > 0 {
			return nil, fmt.Errorf("refusing to overwrite locally modified generated files: %s; rerun with --force to overwrite", strings.Join(conflicts, ", "))
		}
	}
	results := []UpdateResult{}
	updateGenerated := func(relativePath string, content []byte, executable bool) error {
		currentGenerated[filepath.ToSlash(relativePath)] = true
		status, err := deploy.UpdateGeneratedFile(options.Dir, relativePath, content, executable, &manifest, options.Force)
		if err != nil {
			return err
		}
		results = append(results, UpdateResult{Path: filepath.Join(options.Dir, relativePath), Status: status, Ownership: "generated", Reason: "synced from blueprint and reploy templates"})
		return nil
	}
	for _, generated := range generatedUpdates {
		if err := updateGenerated(generated.RelativePath, generated.Content, generated.Executable); err != nil {
			return nil, err
		}
	}
	if err := pruneRemovedGeneratedFiles(options.Dir, currentGenerated, &manifest, options.Force, &results); err != nil {
		return nil, err
	}
	if err := updateDockerEnvFile(options.Dir, pack, dockerIdentity, &results); err != nil {
		return nil, err
	}
	requirements, err := runtimeRequirementsContent(pack, bundle.Roots)
	if err != nil {
		return nil, err
	}
	requirementsStatus, err := deploy.WriteFileIfChanged(filepath.Join(options.Dir, RequirementsFileName), ensureTrailingNewline(requirements), 0o644)
	if err != nil {
		return nil, err
	}
	results = append(results, UpdateResult{Path: filepath.Join(options.Dir, RequirementsFileName), Status: requirementsStatus, Ownership: "local", Reason: "projected selected bundle roots for Docker runtime"})
	if err := ensureDeploymentDirs(options.Dir, pack.Docker.DeploymentDirs, &results); err != nil {
		return nil, err
	}
	if err := ensureLocalDir(options.Dir, RuntimeDirName, &results); err != nil {
		return nil, err
	}
	composeResult, err := writeRuntimeCompose(options.Dir, pack, bundle.Roots, dockerIdentity)
	if err != nil {
		return nil, err
	}
	results = append(results, composeResult)
	stateStatus, err := writeUpdatedStateIfChanged(options.Dir, pack, bundle, state)
	if err != nil {
		return nil, err
	}
	results = append(results, UpdateResult{Path: filepath.Join(options.Dir, StateFileName), Status: stateStatus, Ownership: "state", Reason: "recorded resolved deployment state"})
	manifestStatus, err := deploy.WriteDeploymentManifestIfChanged(filepath.Join(options.Dir, ManifestFileName), manifest)
	if err != nil {
		return nil, err
	}
	results = append(results, UpdateResult{Path: filepath.Join(options.Dir, ManifestFileName), Status: manifestStatus, Ownership: "state", Reason: "recorded generated file hashes"})
	return results, nil
}

func materializeRuntimeCompose(dir string) (UpdateResult, error) {
	state, err := loadState(dir)
	if err != nil {
		return UpdateResult{}, err
	}
	state, err = withInferredBundleState(dir, state)
	if err != nil {
		return UpdateResult{}, err
	}
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return UpdateResult{}, err
	}
	dockerIdentity, err := deploymentDockerIdentity(pack, state, dir)
	if err != nil {
		return UpdateResult{}, err
	}
	return writeRuntimeCompose(dir, pack, state.Bundle.Roots, dockerIdentity)
}

func ensureRuntimeCompose(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, ComposeFileName)); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	_, err := materializeRuntimeCompose(dir)
	return err
}

func writeRuntimeCompose(dir string, pack deploy.AppPack, roots []deploy.ArtifactRoot, dockerIdentity string) (UpdateResult, error) {
	compose, err := renderComposeTemplate(pack, roots, dockerIdentity)
	if err != nil {
		return UpdateResult{}, err
	}
	path := filepath.Join(dir, ComposeFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return UpdateResult{}, err
	}
	status, err := deploy.WriteFileIfChanged(path, []byte(compose), 0o644)
	if err != nil {
		return UpdateResult{}, err
	}
	return UpdateResult{Path: path, Status: status, Ownership: "runtime", Reason: "materialized Docker Compose runtime file"}, nil
}

func locallyModifiedGeneratedFiles(dir string, updates []generatedUpdate, manifest deploy.DeploymentManifest) ([]string, error) {
	var conflicts []string
	for _, update := range updates {
		relativePath := filepath.ToSlash(update.RelativePath)
		path := filepath.Join(dir, filepath.FromSlash(relativePath))
		currentHash, err := deploy.HashFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if currentHash == deploy.HashBytes(update.Content) {
			continue
		}
		previous, ok := manifest.Files[relativePath]
		if !ok || previous.SHA256 != currentHash {
			conflicts = append(conflicts, path)
		}
	}
	return conflicts, nil
}

func locallyModifiedRemovedGeneratedFiles(dir string, current map[string]bool, manifest deploy.DeploymentManifest) ([]string, error) {
	var conflicts []string
	for relativePath, entry := range manifest.Files {
		if current[relativePath] {
			continue
		}
		path := filepath.Join(dir, filepath.FromSlash(relativePath))
		currentHash, err := deploy.HashFile(path)
		switch {
		case err == nil && currentHash != entry.SHA256:
			conflicts = append(conflicts, path)
		case err == nil:
		case os.IsNotExist(err):
		default:
			return nil, err
		}
	}
	return conflicts, nil
}

func pruneRemovedGeneratedFiles(dir string, current map[string]bool, manifest *deploy.DeploymentManifest, force bool, results *[]UpdateResult) error {
	for relativePath, entry := range manifest.Files {
		if current[relativePath] {
			continue
		}
		path := filepath.Join(dir, filepath.FromSlash(relativePath))
		currentHash, err := deploy.HashFile(path)
		switch {
		case err == nil && currentHash == entry.SHA256:
			if err := os.Remove(path); err != nil {
				return err
			}
			*results = append(*results, UpdateResult{Path: path, Status: deploy.UpdateStatusRemoved, Ownership: "generated", Reason: "removed file no longer generated by current blueprint"})
		case err == nil:
			if !force {
				return fmt.Errorf("refusing to remove locally modified generated files: %s; rerun with --force to remove", path)
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			*results = append(*results, UpdateResult{Path: path, Status: deploy.UpdateStatusRemoved, Ownership: "generated", Reason: "removed locally edited file no longer generated by current blueprint"})
		case os.IsNotExist(err):
		default:
			return err
		}
		delete(manifest.Files, relativePath)
	}
	return nil
}

func loadState(dir string) (deploy.DeploymentState, error) {
	content, err := os.ReadFile(filepath.Join(dir, StateFileName))
	if err != nil {
		return deploy.DeploymentState{}, fmt.Errorf("read deployment state: %w", err)
	}
	var state deploy.DeploymentState
	if err := json.Unmarshal(content, &state); err != nil {
		return deploy.DeploymentState{}, fmt.Errorf("parse deployment state: %w", err)
	}
	return state, nil
}

func RequireStagingDeployment(dir string) error {
	state, err := loadState(dir)
	if err != nil {
		return err
	}
	switch state.Phase {
	case deploy.PhaseStaged:
		return nil
	case deploy.PhaseInstalled:
		return fmt.Errorf("%s is an installed deployment; use the generated app control script for deployed operation", dir)
	case "":
		return fmt.Errorf("%s is missing deployment phase; expected staged", dir)
	default:
		return fmt.Errorf("%s has unsupported deployment phase %q; expected staged", dir, state.Phase)
	}
}

func loadManifestOrNew(dir string) (deploy.DeploymentManifest, error) {
	manifest, err := deploy.LoadDeploymentManifest(filepath.Join(dir, ManifestFileName))
	if err == nil {
		return manifest, nil
	}
	if os.IsNotExist(err) {
		return deploy.NewDeploymentManifest("reploy stage --update"), nil
	}
	return deploy.DeploymentManifest{}, err
}

func writeMissingLocalFile(dir string, relativePath string, content []byte, results *[]UpdateResult) error {
	return writeMissingLocalFileMode(dir, relativePath, content, 0o644, results)
}

func writeMissingLocalFileMode(dir string, relativePath string, content []byte, mode os.FileMode, results *[]UpdateResult) error {
	path := filepath.Join(dir, relativePath)
	if _, err := os.Stat(path); err == nil {
		*results = append(*results, UpdateResult{Path: path, Status: deploy.UpdateStatusUpToDate, Ownership: "local", Reason: "operator-owned file already exists"})
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		return err
	}
	*results = append(*results, UpdateResult{Path: path, Status: deploy.UpdateStatusUpdated, Ownership: "local", Reason: "created missing operator-owned file"})
	return nil
}

func updateDockerEnvFile(dir string, pack deploy.AppPack, dockerIdentity string, results *[]UpdateResult) error {
	path := filepath.Join(dir, DockerEnvFileName)
	dockerEnv, err := defaultDockerEnv(pack, dockerIdentity)
	if err != nil {
		return err
	}
	desired := []byte(dockerEnv)
	current, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.WriteFile(path, desired, 0o644); err != nil {
			return err
		}
		*results = append(*results, UpdateResult{Path: path, Status: deploy.UpdateStatusUpdated, Ownership: "local", Reason: "created missing Docker environment file"})
		return nil
	}
	if string(current) == string(desired) {
		*results = append(*results, UpdateResult{Path: path, Status: deploy.UpdateStatusUpToDate, Ownership: "local", Reason: "Docker environment already matches current defaults"})
		return nil
	}
	service := dockerServiceDefaults(pack, dockerIdentity)
	updates := map[string]string{
		"REPLOY_CONTAINER_NAME":      service.ContainerName,
		"REPLOY_DOCKER_NETWORK_NAME": service.NetworkName,
	}
	changed, err := updateExistingDockerEnvValues(dir, updates)
	if err != nil {
		return err
	}
	if changed {
		*results = append(*results, UpdateResult{Path: path, Status: deploy.UpdateStatusUpdated, Ownership: "local", Reason: "updated Reploy-managed Docker identity"})
		return nil
	}
	*results = append(*results, UpdateResult{Path: path, Status: deploy.UpdateStatusUpToDate, Ownership: "local", Reason: "preserved operator-edited Docker environment"})
	return nil
}

func ensureDeploymentDirs(dir string, deploymentDirs deploy.DockerDeploymentDirs, results *[]UpdateResult) error {
	for _, relativeDir := range deploymentDirs.All() {
		if err := ensureLocalDir(dir, relativeDir, results); err != nil {
			return err
		}
	}
	return nil
}

func ensureLocalDir(dir string, relativeDir string, results *[]UpdateResult) error {
	path := filepath.Join(dir, relativeDir)
	if _, err := os.Stat(path); err == nil {
		*results = append(*results, UpdateResult{Path: path, Status: deploy.UpdateStatusUpToDate, Ownership: "directory", Reason: "deployment directory already exists"})
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	*results = append(*results, UpdateResult{Path: path, Status: deploy.UpdateStatusUpdated, Ownership: "directory", Reason: "created deployment directory"})
	return nil
}
