package dockerdeploy

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providerstore"
)

type providerInstallPathUpdateBackendV1 struct {
	volumeExists          func(context.Context, string) (bool, error)
	prepareProbeWorkspace func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error)
	run                   commandRunner
}

func applyProviderInstallPathUpdatesV1(ctx context.Context, locked lockedProviderInstallV1) error {
	return applyProviderInstallPathUpdatesWithV1(ctx, locked, providerInstallPathUpdateBackendV1{
		volumeExists: func(ctx context.Context, name string) (bool, error) {
			return providerInstallVolumeExistsV1(ctx, locked.HostTools.DockerPath, name, locked.Input.RunOptions)
		},
		prepareProbeWorkspace: PrepareProbeWorkspace,
		run:                   runCommandWithoutDockerPreflight,
	})
}

func applyProviderInstallPathUpdatesWithV1(
	ctx context.Context,
	locked lockedProviderInstallV1,
	backend providerInstallPathUpdateBackendV1,
) error {
	if ctx == nil {
		return fmt.Errorf("apply provider install path updates requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if backend.volumeExists == nil || backend.run == nil {
		return fmt.Errorf("apply provider install path updates requires a complete backend")
	}
	for _, action := range locked.Plan.PathUpdates {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch action.Kind {
		case PathPreserveManagedBind:
			if err := applyProviderInstallManagedBindV1(ctx, action, locked, false); err != nil {
				return err
			}
		case PathReplaceManagedBind:
			if err := applyProviderInstallManagedBindV1(ctx, action, locked, true); err != nil {
				return err
			}
		case PathPreserveVolume:
			if err := applyProviderInstallVolumeV1(ctx, action, locked, false, backend); err != nil {
				return err
			}
		case PathReplaceVolume:
			if err := applyProviderInstallVolumeV1(ctx, action, locked, true, backend); err != nil {
				return err
			}
		case PathPreservePrivateEnv:
			if err := applyProviderInstallPrivateEnvironmentV1(ctx, action, locked, false); err != nil {
				return err
			}
		case PathReplacePrivateEnv:
			if err := applyProviderInstallPrivateEnvironmentV1(ctx, action, locked, true); err != nil {
				return err
			}
		case PathValidateUnmanaged:
			if _, err := os.Stat(action.Target); err != nil {
				return fmt.Errorf("validate unmanaged mount %q: %w", action.Name, err)
			}
		case PathTmpfsNoop:
			// A tmpfs has no durable install-time state.
		default:
			return fmt.Errorf("installed mount %q has unsupported path update %q", action.Name, action.Kind)
		}
	}
	return nil
}

func applyProviderInstallPrivateEnvironmentV1(
	ctx context.Context,
	action PathUpdateAction,
	locked lockedProviderInstallV1,
	replace bool,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if action.Name != PrivateWorkloadEnvironmentFileName {
		return fmt.Errorf("private environment path update has unexpected name %q", action.Name)
	}
	if err := requirePathWithinInstallTarget(action.Target, locked.Input.DestinationDeploymentDir); err != nil {
		return fmt.Errorf("installed %s: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	targetInfo, targetErr := os.Lstat(action.Target)
	if targetErr == nil && (!targetInfo.Mode().IsRegular() || targetInfo.Mode()&os.ModeSymlink != 0) {
		return fmt.Errorf("installed %s must be a real regular file: %s", PrivateWorkloadEnvironmentFileName, action.Target)
	}
	if targetErr == nil && !replace {
		if _, err := loadPrivateWorkloadEnvironmentV1(filepath.Dir(action.Target)); err != nil {
			return fmt.Errorf("validate preserved installed %s: %w", PrivateWorkloadEnvironmentFileName, err)
		}
		return nil
	}
	if targetErr != nil && !os.IsNotExist(targetErr) {
		return fmt.Errorf("inspect installed %s: %w", PrivateWorkloadEnvironmentFileName, targetErr)
	}
	source, err := loadPrivateWorkloadEnvironmentV1(filepath.Dir(action.Source))
	if err != nil {
		return fmt.Errorf("read staging %s for installation: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	if !source.Exists {
		return fmt.Errorf("install %s: staging source is missing", PrivateWorkloadEnvironmentFileName)
	}
	parent := filepath.Dir(action.Target)
	if err := validateProviderInstallDirectoryAncestorsV1(parent); err != nil {
		return fmt.Errorf("validate installed %s destination: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create installed %s parent: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	created, err := publishPrivateWorkloadEnvironmentFileV1(action.Target, source.Raw, replace)
	if err != nil {
		return fmt.Errorf("publish installed %s: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	if !created {
		if _, validateErr := loadPrivateWorkloadEnvironmentV1(parent); validateErr != nil {
			return fmt.Errorf("validate concurrently created installed %s: %w", PrivateWorkloadEnvironmentFileName, validateErr)
		}
		return nil
	}
	if err := syncProviderInstallDirectory(parent); err != nil {
		return fmt.Errorf("sync installed %s directory: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	return nil
}

func applyProviderInstallManagedBindV1(ctx context.Context, action PathUpdateAction, locked lockedProviderInstallV1, replace bool) error {
	if err := requirePathWithinInstallTarget(action.Target, locked.Input.DestinationDeploymentDir); err != nil {
		return fmt.Errorf("managed mount %q: %w", action.Name, err)
	}
	sourceInfo, err := os.Lstat(action.Source)
	if err != nil {
		return fmt.Errorf("inspect staging managed mount %q: %w", action.Name, err)
	}
	if !sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("staging managed mount %q must be a real directory: %s", action.Name, action.Source)
	}
	targetInfo, targetErr := os.Lstat(action.Target)
	switch {
	case targetErr == nil && targetInfo.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("installed managed mount %q must not be a symlink: %s", action.Name, action.Target)
	case targetErr == nil && !targetInfo.IsDir():
		return fmt.Errorf("installed managed mount %q must be a directory: %s", action.Name, action.Target)
	case targetErr != nil && !os.IsNotExist(targetErr):
		return fmt.Errorf("inspect installed managed mount %q: %w", action.Name, targetErr)
	case targetErr == nil && !replace:
		return nil
	}
	parent := filepath.Dir(action.Target)
	if err := validateProviderInstallDirectoryAncestorsV1(parent); err != nil {
		return fmt.Errorf("validate installed managed mount %q: %w", action.Name, err)
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create installed managed mount parent %q: %w", action.Name, err)
	}
	staged, err := os.MkdirTemp(parent, ".reploy-mount-")
	if err != nil {
		return fmt.Errorf("stage installed managed mount %q: %w", action.Name, err)
	}
	keepStaged := false
	defer func() {
		if !keepStaged {
			_ = os.RemoveAll(staged)
		}
	}()
	if err := copyProviderInstallManagedBindV1(ctx, action.Source, staged, locked); err != nil {
		return fmt.Errorf("stage installed managed mount %q: %w", action.Name, err)
	}
	if replace && targetErr == nil {
		if err := os.RemoveAll(action.Target); err != nil {
			return fmt.Errorf("replace installed managed mount %q: %w", action.Name, err)
		}
	} else if !replace {
		if current, err := os.Lstat(action.Target); err == nil {
			if !current.IsDir() || current.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("installed managed mount %q appeared with an invalid type: %s", action.Name, action.Target)
			}
			return nil
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("reinspect installed managed mount %q: %w", action.Name, err)
		}
	}
	if err := os.Rename(staged, action.Target); err != nil {
		return fmt.Errorf("publish installed managed mount %q: %w", action.Name, err)
	}
	keepStaged = true
	return nil
}

func applyProviderInstallVolumeV1(
	ctx context.Context,
	action PathUpdateAction,
	locked lockedProviderInstallV1,
	replace bool,
	backend providerInstallPathUpdateBackendV1,
) error {
	dockerPath := locked.HostTools.DockerPath
	if strings.TrimSpace(dockerPath) == "" {
		return fmt.Errorf("materialize installed volume %q requires the Docker client", action.Name)
	}
	run := func(runCtx context.Context, args ...string) error {
		options := locked.Input.RunOptions
		options.Context = runCtx
		return backend.run(CommandSpec{Name: dockerPath, Args: args}, options)
	}
	containerName := providerInstallVolumeCopyContainerNameV1(action.Target)
	cleanupContainer := func(cleanupCtx context.Context) error {
		err := run(cleanupCtx, "container", "rm", "--force", containerName)
		if isMissingContainerCleanupError(err) {
			return nil
		}
		return err
	}
	if err := cleanupContainer(ctx); err != nil {
		return fmt.Errorf("clean stale installed volume %q copy container: %w", action.Name, err)
	}
	targetExists, err := backend.volumeExists(ctx, action.Target)
	if err != nil {
		return fmt.Errorf("inspect installed volume %q: %w", action.Name, err)
	}
	if targetExists && !replace {
		return nil
	}
	if targetExists {
		if err := run(ctx, "volume", "rm", action.Target); err != nil {
			return fmt.Errorf("replace installed volume %q: %w", action.Name, err)
		}
	}
	if err := run(ctx, "volume", "create", "--name", action.Target); err != nil {
		return fmt.Errorf("create installed volume %q: %w", action.Name, err)
	}
	removeNewTarget := func(cause error) error {
		cleanupErr := run(context.WithoutCancel(ctx), "volume", "rm", action.Target)
		if cleanupErr != nil {
			cleanupErr = fmt.Errorf("remove incomplete installed volume %q: %w", action.Name, cleanupErr)
		}
		return errors.Join(cause, cleanupErr)
	}
	sourceExists, err := backend.volumeExists(ctx, action.Source)
	if err != nil {
		return removeNewTarget(fmt.Errorf("inspect staging volume %q: %w", action.Name, err))
	}
	if !sourceExists {
		return nil
	}
	if backend.prepareProbeWorkspace == nil {
		return removeNewTarget(fmt.Errorf("materialize installed volume %q requires the embedded Reploy helper", action.Name))
	}
	workspace, cleanup, err := backend.prepareProbeWorkspace(ctx, locked.DestinationStore, locked.SourceBuild.Lock.Platform)
	if err != nil {
		return removeNewTarget(fmt.Errorf("prepare installed volume %q copy helper: %w", action.Name, err))
	}
	if cleanup == nil {
		return removeNewTarget(fmt.Errorf("prepare installed volume %q copy helper returned no cleanup", action.Name))
	}
	copyCommand, err := providerInstallVolumeCopyCommandV1(
		dockerPath, containerName, action.Source, action.Target,
		locked.SourceBuild.Lock.FinalImage.ConfigDigest,
		locked.SourceBuild.Lock.Platform,
		workspace,
	)
	if err != nil {
		return removeNewTarget(errors.Join(fmt.Errorf("plan installed volume %q copy: %w", action.Name, err), cleanup()))
	}
	options := locked.Input.RunOptions
	options.Context = ctx
	copyErr := backend.run(copyCommand, options)
	if copyErr != nil {
		cleanupCtx := context.WithoutCancel(ctx)
		containerErr := cleanupContainer(cleanupCtx)
		cleanupErr := cleanup()
		return errors.Join(
			removeNewTarget(fmt.Errorf("copy installed volume %q: %w", action.Name, copyErr)),
			cleanupErr,
			containerErr,
		)
	}
	cleanupErr := cleanup()
	if cleanupErr != nil {
		return fmt.Errorf("clean installed volume %q copy helper: %w", action.Name, cleanupErr)
	}
	return nil
}

func providerInstallVolumeExistsV1(ctx context.Context, dockerPath string, name string, options RunOptions) (bool, error) {
	return providerInstallVolumeExistsWithV1(ctx, dockerPath, name, options, runCommandWithoutDockerPreflight)
}

func providerInstallVolumeExistsWithV1(ctx context.Context, dockerPath string, name string, options RunOptions, run commandRunner) (bool, error) {
	if strings.TrimSpace(dockerPath) == "" {
		return false, fmt.Errorf("inspect installed volume requires the Docker client")
	}
	if run == nil {
		return false, fmt.Errorf("inspect installed volume requires a command runner")
	}
	options.Context = ctx
	err := run(CommandSpec{Name: dockerPath, Args: []string{"volume", "inspect", name}}, options)
	if err == nil {
		return true, nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "no such volume") {
		return false, nil
	}
	return false, err
}

func providerInstallVolumeCopyCommandV1(
	dockerPath string,
	containerName string,
	source string,
	target string,
	imageConfigDigest canonical.Digest,
	platform blueprint.Platform,
	workspace PreparedProbeWorkspace,
) (CommandSpec, error) {
	if strings.TrimSpace(dockerPath) == "" {
		return CommandSpec{}, fmt.Errorf("Docker client path is required")
	}
	if strings.TrimSpace(containerName) == "" {
		return CommandSpec{}, fmt.Errorf("volume copy container name is required")
	}
	if err := imageConfigDigest.Validate(); err != nil {
		return CommandSpec{}, fmt.Errorf("volume copy image config digest: %w", err)
	}
	if err := platform.Validate(); err != nil {
		return CommandSpec{}, fmt.Errorf("volume copy platform: %w", err)
	}
	if workspace.Platform != platform || workspace.ContainerDir != ProbeContainerRoot || workspace.ContainerExecutable != ProbeContainerExecutable || !workspace.ReadOnly {
		return CommandSpec{}, fmt.Errorf("volume copy helper does not match the selected platform and fixed mount")
	}
	if workspace.HostDir == "" || !filepath.IsAbs(workspace.HostDir) || filepath.Clean(workspace.HostDir) != workspace.HostDir {
		return CommandSpec{}, fmt.Errorf("volume copy helper host directory must be an absolute clean path")
	}
	helperMount, err := dockerMountArgument("type=bind", "source="+workspace.HostDir, "target="+workspace.ContainerDir, "readonly")
	if err != nil {
		return CommandSpec{}, err
	}
	sourceMount, err := dockerMountArgument("type=volume", "source="+source, "target="+probe.VolumeCopySourceRoot, "readonly")
	if err != nil {
		return CommandSpec{}, err
	}
	targetMount, err := dockerMountArgument("type=volume", "source="+target, "target="+probe.VolumeCopyTargetRoot)
	if err != nil {
		return CommandSpec{}, err
	}
	return CommandSpec{Name: dockerPath, Args: []string{
		"run", "--rm", "--name", containerName, "--platform", platform.Canonical, "--pull", "never",
		"--user", "0:0", "--workdir", "/", "--read-only", "--network", "none",
		"--mount", helperMount,
		"--mount", sourceMount,
		"--mount", targetMount,
		"--entrypoint", workspace.ContainerExecutable,
		string(imageConfigDigest), "copy-volume-tree",
	}}, nil
}

func providerInstallVolumeCopyContainerNameV1(targetVolume string) string {
	digest := sha256.Sum256([]byte(targetVolume))
	return fmt.Sprintf("reploy-volume-copy-%x", digest[:12])
}
