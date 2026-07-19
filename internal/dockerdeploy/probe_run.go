package dockerdeploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probe"
)

type ImageValidationSession struct {
	descriptor    deploy.ImageDescriptor
	workspace     PreparedProbeWorkspace
	containerName string
	closed        bool
}

var runImageValidationOpenCommand = runCommand
var runImageValidationFollowupCommand = runCommandWithoutDockerPreflight

// OpenImageValidationSession starts one held, networkless container for full
// final-image or additive layer validation. Its public operations remain
// closed: callers may invoke the fixed filesystem probe and close the session,
// but cannot execute arbitrary commands.
func OpenImageValidationSession(ctx context.Context, descriptor deploy.ImageDescriptor, workspace PreparedProbeWorkspace) (*ImageValidationSession, error) {
	if ctx == nil {
		return nil, fmt.Errorf("image validation session context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open image validation session: %w", err)
	}
	if err := descriptor.Validate(); err != nil {
		return nil, fmt.Errorf("image validation descriptor: %w", err)
	}
	if descriptor.Platform.OS != "linux" {
		return nil, fmt.Errorf("image validation requires a Linux image")
	}
	spec, containerName, err := imageValidationCreateCommandSpec(descriptor, workspace)
	if err != nil {
		return nil, err
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runImageValidationOpenCommand(spec, RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr}); err != nil {
		return nil, imageValidationCommandError("create", descriptor.Platform.Canonical, stderr.String(), err)
	}
	stderr.Reset()
	if err := runImageValidationFollowupCommand(
		CommandSpec{Name: "docker", Args: []string{"start", containerName}},
		RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr},
	); err != nil {
		startErr := imageValidationCommandError("start", descriptor.Platform.Canonical, stderr.String(), err)
		cleanupErr := removeImageValidationContainer(context.WithoutCancel(ctx), containerName)
		return nil, errors.Join(startErr, cleanupErr)
	}
	return &ImageValidationSession{descriptor: descriptor, workspace: workspace, containerName: containerName}, nil
}

// Probe performs one fixed canonical filesystem-observation exchange in the
// already-running validation container.
func (session *ImageValidationSession) Probe(ctx context.Context, request probe.RequestV1) (probe.ResponseV1, error) {
	if session == nil || session.closed {
		return probe.ResponseV1{}, fmt.Errorf("image validation session is not open")
	}
	if ctx == nil {
		return probe.ResponseV1{}, fmt.Errorf("image validation probe context is required")
	}
	if err := ctx.Err(); err != nil {
		return probe.ResponseV1{}, fmt.Errorf("run image validation probe: %w", err)
	}
	if err := probe.ValidateRequestV1(request); err != nil {
		return probe.ResponseV1{}, err
	}
	encoded, err := canonical.Marshal(request)
	if err != nil {
		return probe.ResponseV1{}, fmt.Errorf("encode image probe request: %w", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	spec := CommandSpec{Name: "docker", Args: []string{
		"exec", "--interactive", "--user", "0:0", "--workdir", "/",
		session.containerName, session.workspace.ContainerExecutable,
	}}
	if err := runImageValidationFollowupCommand(spec, RunOptions{
		Context: ctx, Stdin: bytes.NewReader(encoded), Stdout: &stdout, Stderr: &stderr,
	}); err != nil {
		return probe.ResponseV1{}, imageValidationCommandError("probe", session.descriptor.Platform.Canonical, stderr.String(), err)
	}
	response, err := probe.DecodeResponseV1(request, stdout.Bytes())
	if err != nil {
		return probe.ResponseV1{}, fmt.Errorf("decode image probe response from %s: %w", session.descriptor.ImmutableReference, err)
	}
	return response, nil
}

// Close force-removes exactly this validation container. It is idempotent
// after successful removal.
func (session *ImageValidationSession) Close(ctx context.Context) error {
	if session == nil || session.closed {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("close image validation session context is required")
	}
	if err := removeImageValidationContainer(ctx, session.containerName); err != nil {
		return err
	}
	session.closed = true
	return nil
}

// RunImageProbe is the one-exchange convenience boundary used when the full
// validation plan currently needs only filesystem evidence. Higher-level full
// validation can open a session and perform several fixed checks before close.
func RunImageProbe(ctx context.Context, descriptor deploy.ImageDescriptor, workspace PreparedProbeWorkspace, request probe.RequestV1) (probe.ResponseV1, error) {
	session, err := OpenImageValidationSession(ctx, descriptor, workspace)
	if err != nil {
		return probe.ResponseV1{}, err
	}
	response, probeErr := session.Probe(ctx, request)
	closeErr := session.Close(context.WithoutCancel(ctx))
	if err := errors.Join(probeErr, closeErr); err != nil {
		return probe.ResponseV1{}, err
	}
	return response, nil
}

func imageValidationCreateCommandSpec(descriptor deploy.ImageDescriptor, workspace PreparedProbeWorkspace) (CommandSpec, string, error) {
	if err := validatePreparedProbeWorkspace(descriptor, workspace); err != nil {
		return CommandSpec{}, "", err
	}
	mount, err := dockerMountArgument(
		"type=bind",
		"source="+workspace.HostDir,
		"target="+workspace.ContainerDir,
		"readonly",
	)
	if err != nil {
		return CommandSpec{}, "", err
	}
	containerName := imageProbeContainerName(workspace.HostDir)
	args := []string{
		"create", "--name", containerName,
		"--platform", descriptor.Platform.Canonical,
		"--pull", "never", "--user", "0:0", "--workdir", "/",
		"--read-only", "--network", "none",
		"--mount", mount,
		"--entrypoint", workspace.ContainerExecutable,
		descriptor.ImmutableReference, "hold",
	}
	return CommandSpec{Name: "docker", Args: args}, containerName, nil
}

func validatePreparedProbeWorkspace(descriptor deploy.ImageDescriptor, workspace PreparedProbeWorkspace) error {
	if err := workspace.Platform.Validate(); err != nil {
		return fmt.Errorf("probe helper platform: %w", err)
	}
	if workspace.Platform.Canonical != descriptor.Platform.Canonical {
		return fmt.Errorf("probe helper platform %s does not match image platform %s", workspace.Platform.Canonical, descriptor.Platform.Canonical)
	}
	if workspace.ContainerDir != ProbeContainerRoot || workspace.ContainerExecutable != ProbeContainerExecutable || !workspace.ReadOnly {
		return fmt.Errorf("probe workspace does not describe the fixed read-only container mount")
	}
	if workspace.HostDir == "" || !filepath.IsAbs(workspace.HostDir) || filepath.Clean(workspace.HostDir) != workspace.HostDir {
		return fmt.Errorf("probe workspace host directory must be an absolute clean path")
	}
	if workspace.HostExecutable != filepath.Join(workspace.HostDir, filepath.Base(ProbeContainerExecutable)) {
		return fmt.Errorf("probe workspace executable is outside its fixed location")
	}
	if err := workspace.SHA256.Validate(); err != nil {
		return fmt.Errorf("probe workspace digest: %w", err)
	}
	return nil
}

func removeImageValidationContainer(ctx context.Context, containerName string) error {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runImageValidationFollowupCommand(
		CommandSpec{Name: "docker", Args: []string{"rm", "--force", containerName}},
		RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr},
	); err != nil {
		output := trimmedCommandOutput(stderr.String())
		if output != "" {
			return fmt.Errorf("remove image validation container %s: %w\ncommand output:\n%s", containerName, err, output)
		}
		return fmt.Errorf("remove image validation container %s: %w", containerName, err)
	}
	return nil
}

func dockerMountArgument(fields ...string) (string, error) {
	var output strings.Builder
	writer := csv.NewWriter(&output)
	if err := writer.Write(fields); err != nil {
		return "", fmt.Errorf("render Docker probe mount: %w", err)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", fmt.Errorf("render Docker probe mount: %w", err)
	}
	return strings.TrimSuffix(output.String(), "\n"), nil
}

func imageProbeContainerName(workspace string) string {
	digest := sha256.Sum256([]byte(workspace))
	return fmt.Sprintf("reploy-probe-%x", digest[:12])
}

func imageValidationCommandError(operation string, platform string, stderr string, err error) error {
	output := trimmedCommandOutput(stderr)
	if strings.Contains(strings.ToLower(output), "exec format error") {
		return fmt.Errorf("Docker cannot execute the %s Reploy probe; enable binfmt/QEMU emulation for that platform or run the build on a compatible host: %w", platform, err)
	}
	if output != "" {
		return fmt.Errorf("%s image validation container for %s: %w\ncommand output:\n%s", operation, platform, err, output)
	}
	return fmt.Errorf("%s image validation container for %s: %w", operation, platform, err)
}
