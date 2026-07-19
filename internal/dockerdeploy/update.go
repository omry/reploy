package dockerdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

type UpdateOptions struct {
	Dir                    string
	Pack                   deploy.PackRef
	Force                  bool
	MaterializeEnvironment bool
	Verbose                bool
	Stdout                 io.Writer
	Stderr                 io.Writer
	Progress               io.Writer
	DockerPreflightTimeout time.Duration
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

var materializeStagedEnvironmentForStage = materializeStagedEnvironment

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
	state.Materialization = nil
	if state.Images != nil {
		state.Images.Staging = nil
	}

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
		{RelativePath: controlScriptNameForPack(pack), Content: []byte(stagingControlScriptContent(pack, deployedCommands)), Executable: true},
	}
	currentGenerated := map[string]bool{}
	for _, generated := range generatedUpdates {
		currentGenerated[filepath.ToSlash(generated.RelativePath)] = true
	}
	currentGenerated[filepath.ToSlash(embeddedRuntimeFileName())] = true
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
	runtimeState, runtimeStatus, err := updateEmbeddedRuntime(options.Dir, &manifest, options.Force)
	if err != nil {
		return nil, err
	}
	state.Runtime = &runtimeState
	results = append(results, UpdateResult{Path: filepath.Join(options.Dir, runtimeState.Path), Status: runtimeStatus, Ownership: "runtime", Reason: "embedded Reploy runtime for generated control scripts"})
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
	if err := ensureInstallDirs(options.Dir, pack.Docker.DeploymentDirs, pack.Install.ManagedPaths, &results); err != nil {
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
	if options.MaterializeEnvironment && pack.Environment != nil {
		materialized, err := materializeStagedEnvironmentForStage(
			context.Background(), options.Dir, pack, options.Verbose, options.Stdout, options.Stderr, options.Progress, options.DockerPreflightTimeout,
		)
		if err != nil {
			return nil, err
		}
		results = append(results, materialized...)
	}
	return results, nil
}

func materializeStagedEnvironment(ctx context.Context, dir string, pack deploy.AppPack, verbose bool, stdout io.Writer, stderr io.Writer, progress io.Writer, dockerPreflightTimeout time.Duration) ([]UpdateResult, error) {
	built, err := EnsureBundlePrepared(BundleEnsureOptions{
		Dir: dir, SkipWarmRuntime: true, Verbose: verbose, Stdout: stdout, Stderr: stderr, Progress: progress,
		DockerPreflightTimeout: dockerPreflightTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("prepare staged environment bundle: %w", err)
	}
	state, err := loadState(dir)
	if err != nil {
		return nil, err
	}
	state, err = BuildEnvironmentImage(ctx, dir, pack, state, RunOptions{
		Context: ctx, Stdout: stdout, Stderr: stderr, DockerPreflightTimeout: dockerPreflightTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("materialize staged environment image: %w", err)
	}
	results, err := WriteResolvedRuntimeInputs(dir, pack, state)
	if err != nil {
		return nil, fmt.Errorf("write staged environment runtime: %w", err)
	}
	stateStatus, err := writeUpdatedStateIfChanged(dir, pack, state.Bundle, state)
	if err != nil {
		return nil, err
	}
	results = append(results, UpdateResult{
		Path: filepath.Join(dir, StateFileName), Status: stateStatus, Ownership: "state",
		Reason: "recorded staged environment bundle and generated image",
	})
	if built {
		results = append(results, UpdateResult{
			Path: filepath.Join(dir, BundleDirName), Status: deploy.UpdateStatusUpdated, Ownership: "bundle",
			Reason: "resolved closed environment bundle",
		})
	}
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
	if pack.Environment != nil {
		path := filepath.Join(dir, ComposeFileName)
		if _, err := os.Stat(path); err == nil {
			return UpdateResult{Path: path, Status: deploy.UpdateStatusUpToDate, Ownership: "runtime", Reason: "preserved resolved Docker execution plan"}, nil
		} else if !os.IsNotExist(err) {
			return UpdateResult{}, err
		}
		content := []byte(fmt.Sprintf("name: %s\nservices: {}\n", dockerIdentity))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return UpdateResult{}, err
		}
		status, err := deploy.WriteFileIfChanged(path, content, 0o644)
		if err != nil {
			return UpdateResult{}, err
		}
		return UpdateResult{Path: path, Status: status, Ownership: "runtime", Reason: "created empty runtime plan pending materialization"}, nil
	}
	compose, err := renderComposeTemplate(pack, roots, dockerIdentity)
	if err != nil {
		return UpdateResult{}, err
	}
	compose, err = declareRuntimeVolumeIfNeeded(dir, compose)
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

func declareRuntimeVolumeIfNeeded(dir string, compose string) (string, error) {
	values, err := readDockerEnv(dir)
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(values["REPLOY_RUNTIME_DIR"])
	if !isDockerNamedVolumeReference(name) {
		return compose, nil
	}
	if !strings.Contains(compose, "\nvolumes:\n") {
		compose += "\nvolumes:\n"
	}
	if strings.Contains(compose, "\n  "+name+":\n") {
		return compose, nil
	}
	return compose + fmt.Sprintf("  %s:\n    name: %s\n    external: true\n", name, name), nil
}

func isDockerNamedVolumeReference(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	if strings.HasPrefix(value, ".") || filepath.IsAbs(value) || strings.ContainsAny(value, `/\:`) {
		return false
	}
	for _, r := range value {
		valid := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-'
		if !valid {
			return false
		}
	}
	return true
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

func LoadState(dir string) (deploy.DeploymentState, error) {
	return loadState(dir)
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
	values, err := readDockerEnv(dir)
	if err != nil {
		return err
	}
	runtimeValue, runtimePresent := values["REPLOY_RUNTIME_DIR"]
	if shouldUpdateGeneratedRuntimeDir(runtimeValue) {
		updates["REPLOY_RUNTIME_DIR"] = dockerRuntimeVolumeName(dockerIdentity)
	}
	changed, err := updateExistingDockerEnvValues(dir, updates)
	if err != nil {
		return err
	}
	if !runtimePresent {
		runtimeDir := updates["REPLOY_RUNTIME_DIR"]
		if runtimeDir != "" {
			appended, err := upsertDockerEnvValues(dir, map[string]string{"REPLOY_RUNTIME_DIR": runtimeDir})
			if err != nil {
				return err
			}
			changed = changed || appended
		}
	}
	if changed {
		*results = append(*results, UpdateResult{Path: path, Status: deploy.UpdateStatusUpdated, Ownership: "local", Reason: "updated Reploy-managed Docker identity"})
		return nil
	}
	*results = append(*results, UpdateResult{Path: path, Status: deploy.UpdateStatusUpToDate, Ownership: "local", Reason: "preserved operator-edited Docker environment"})
	return nil
}

func shouldUpdateGeneratedRuntimeDir(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || value == "./"+RuntimeDirName || value == RuntimeDirName
}

func ensureInstallDirs(dir string, deploymentDirs deploy.DockerDeploymentDirs, managedPaths deploy.InstallManagedPathsConfig, results *[]UpdateResult) error {
	seen := map[string]bool{}
	for _, relativeDir := range deploymentDirs.All() {
		if err := ensureLocalDirOnce(dir, relativeDir, results, seen); err != nil {
			return err
		}
	}
	for _, relativeDir := range managedDirPaths(managedPaths) {
		if err := ensureLocalDirOnce(dir, relativeDir, results, seen); err != nil {
			return err
		}
	}
	if err := ensureLocalDirOnce(dir, RuntimeDirName, results, seen); err != nil {
		return err
	}
	return nil
}

func ensureLocalDirOnce(dir string, relativeDir string, results *[]UpdateResult, seen map[string]bool) error {
	relativeDir = cleanManifestPath(relativeDir)
	if seen[relativeDir] {
		return nil
	}
	seen[relativeDir] = true
	if err := ensureLocalDir(dir, relativeDir, results); err != nil {
		return err
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
