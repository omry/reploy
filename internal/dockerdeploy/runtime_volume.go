package dockerdeploy

import (
	"fmt"
	"strings"
	"time"
)

var runRuntimeVolumeInitCommand = runCommand

func dockerRuntimeVolumeName(dockerIdentity string) string {
	dockerIdentity = strings.TrimSpace(dockerIdentity)
	if dockerIdentity == "" {
		return "reploy-runtime"
	}
	return dockerIdentity + "-runtime"
}

func ensureRuntimeNamedVolumeWritable(dir string, projectName string, dockerPreflightTimeout time.Duration) error {
	volumeName, _, ok, err := runtimeNamedVolumeConfig(dir)
	if err != nil || !ok {
		return err
	}
	spec, ok, err := RuntimeVolumeInitCommand(dir, projectName)
	if err != nil || !ok {
		return err
	}
	if err := runRuntimeVolumeInitCommand(DockerVolumeCreateCommand(volumeName), RunOptions{DockerPreflightTimeout: dockerPreflightTimeout}); err != nil {
		return fmt.Errorf("create runtime volume: %w", err)
	}
	if err := runRuntimeVolumeInitCommand(spec, RunOptions{DockerPreflightTimeout: dockerPreflightTimeout}); err != nil {
		return fmt.Errorf("prepare runtime volume: %w", err)
	}
	return nil
}

func RuntimeVolumeInitCommand(dir string, projectName string) (CommandSpec, bool, error) {
	_, containerUser, ok, err := runtimeNamedVolumeConfig(dir)
	if err != nil || !ok {
		return CommandSpec{}, ok, err
	}
	args := []string{
		"run",
		"--rm",
		"--no-deps",
		"--user",
		"0",
		"--entrypoint",
		"sh",
		"-e",
		"REPLOY_RUNTIME_OWNER=" + containerUser,
		"app",
		"-c",
		`mkdir -p /reploy-runtime && chown "$REPLOY_RUNTIME_OWNER" /reploy-runtime`,
	}
	return quietComposeCommand(composeCommandWithProject(dir, projectName, args...)), true, nil
}

func runtimeNamedVolumeConfig(dir string) (string, string, bool, error) {
	values, err := readDockerEnv(dir)
	if err != nil {
		return "", "", false, err
	}
	runtimeDir := strings.TrimSpace(values["REPLOY_RUNTIME_DIR"])
	if !isDockerNamedVolumeReference(runtimeDir) {
		return "", "", false, nil
	}
	containerUser := envValue(values, "REPLOY_CONTAINER_USER", defaultContainerUser())
	return runtimeDir, containerUser, true, nil
}

func DockerVolumeCreateCommand(name string) CommandSpec {
	return CommandSpec{Name: "docker", Args: []string{"volume", "create", name}}
}

func DockerVolumeRemoveCommand(name string) CommandSpec {
	return CommandSpec{Name: "docker", Args: []string{"volume", "rm", "-f", name}}
}
