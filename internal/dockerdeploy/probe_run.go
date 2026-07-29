package dockerdeploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probe"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
)

type ImageValidationSession struct {
	descriptor    deploy.ImageDescriptor
	workspace     PreparedProbeWorkspace
	aptWorkspace  *PreparedAPTResolverWorkspace
	containerName string
	aptBase       *APTBaseValidation
	closed        bool
}

var runImageValidationOpenCommand = runCommand
var runImageValidationFollowupCommand = runCommandWithoutDockerPreflight

// OpenImageValidationSession starts one held, networkless container for full
// final-image or additive layer validation. Its public operations remain
// closed: callers may invoke the fixed filesystem probe and close the session,
// but cannot execute arbitrary commands.
func OpenImageValidationSession(ctx context.Context, descriptor deploy.ImageDescriptor, workspace PreparedProbeWorkspace) (*ImageValidationSession, error) {
	return openImageValidationSession(ctx, descriptor, workspace, nil)
}

// OpenAPTImageValidationSession starts the same networkless validation
// container with the private APT scratch mount required by apt-resolve-v1's
// fixed profile commands. It does not grant package-network access.
func OpenAPTImageValidationSession(
	ctx context.Context,
	descriptor deploy.ImageDescriptor,
	workspace PreparedProbeWorkspace,
	aptWorkspace PreparedAPTResolverWorkspace,
) (*ImageValidationSession, error) {
	return openImageValidationSession(ctx, descriptor, workspace, &aptWorkspace)
}

func openImageValidationSession(
	ctx context.Context,
	descriptor deploy.ImageDescriptor,
	workspace PreparedProbeWorkspace,
	aptWorkspace *PreparedAPTResolverWorkspace,
) (*ImageValidationSession, error) {
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
	spec, containerName, err := imageValidationCreateCommandSpecWithAPT(descriptor, workspace, aptWorkspace)
	if err != nil {
		return nil, err
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runImageValidationOpenCommand(spec, RunOptions{Context: context.WithoutCancel(ctx), Stdout: &stdout, Stderr: &stderr}); err != nil {
		return nil, imageValidationCommandError("create", descriptor.Platform.Canonical, stderr.String(), err)
	}
	if err := ctx.Err(); err != nil {
		cleanupErr := removeImageValidationContainer(context.WithoutCancel(ctx), containerName)
		return nil, errors.Join(fmt.Errorf("open image validation session: %w", err), cleanupErr)
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
	return &ImageValidationSession{
		descriptor: descriptor, workspace: workspace, aptWorkspace: aptWorkspace, containerName: containerName,
	}, nil
}

// ProbeAPTBaseProfile reproduces the same canonical APT base facts used by
// resolution inside this already-held networkless validation container.
func (session *ImageValidationSession) ProbeAPTBaseProfile(ctx context.Context) (APTBaseValidation, error) {
	if session == nil || session.closed {
		return APTBaseValidation{}, fmt.Errorf("image validation session is not open")
	}
	if session.aptWorkspace == nil {
		return APTBaseValidation{}, fmt.Errorf("image validation session has no APT profile workspace")
	}
	if session.aptBase != nil {
		return cloneAPTBaseValidation(*session.aptBase), nil
	}
	result, err := observeAPTBaseProfile(ctx, session.descriptor.Platform, session.Probe, session.runAPTProfileCommand)
	if err != nil {
		return APTBaseValidation{}, err
	}
	session.aptBase = &result
	return cloneAPTBaseValidation(result), nil
}

func (session *ImageValidationSession) runAPTProfileCommand(ctx context.Context, executable string, arguments ...string) ([]byte, error) {
	if session == nil || session.closed || session.aptWorkspace == nil {
		return nil, fmt.Errorf("image validation APT profile session is not open")
	}
	if ctx == nil {
		return nil, fmt.Errorf("image validation APT profile context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("run image validation APT profile: %w", err)
	}
	profile := aptprovider.ResolveChildEnvironmentV1()
	args := []string{
		"exec", "--user", "0:0", "--workdir", "/", session.containerName,
		"/usr/bin/env", "-i",
	}
	for _, variable := range profile.Variables {
		args = append(args, variable.Name+"="+variable.Value)
	}
	args = append(args,
		"/bin/sh", "-c", `exec </dev/null; umask "$1"; shift; exec "$@"`,
		profile.Name, profile.Umask, executable,
	)
	args = append(args, arguments...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runImageValidationFollowupCommand(CommandSpec{Name: "docker", Args: args}, RunOptions{
		Context: ctx, Stdout: &stdout, Stderr: &stderr,
	}); err != nil {
		return nil, imageValidationCommandError("APT profile", session.descriptor.Platform.Canonical, stderr.String(), err)
	}
	return stdout.Bytes(), nil
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

// QueryDPKGOwners performs the one fixed read-only ownership operation used
// for already-observed APT output paths. It does not enumerate packages or
// files and cannot execute a caller-selected command.
func (session *ImageValidationSession) QueryDPKGOwners(ctx context.Context, paths []string) ([]byte, error) {
	if session == nil || session.closed {
		return nil, fmt.Errorf("image validation session is not open")
	}
	if ctx == nil {
		return nil, fmt.Errorf("image validation dpkg owner context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("query image dpkg owners: %w", err)
	}
	if len(paths) == 0 {
		return []byte{}, nil
	}
	paths = append([]string{}, paths...)
	sort.Strings(paths)
	for index, path := range paths {
		if path == "" || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\x00\r\n") {
			return nil, fmt.Errorf("image validation dpkg owner path %d is invalid", index)
		}
		if index > 0 && paths[index-1] == path {
			return nil, fmt.Errorf("image validation dpkg owner paths must be unique")
		}
	}
	args := []string{
		"exec", "--user", "0:0", "--workdir", "/",
		session.containerName, "/usr/bin/dpkg-query", "--search",
	}
	args = append(args, paths...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runImageValidationFollowupCommand(CommandSpec{Name: "docker", Args: args}, RunOptions{
		Context: ctx, Stdout: &stdout, Stderr: &stderr,
	}); err != nil {
		output := trimmedCommandOutput(stderr.String())
		if output != "" {
			return nil, fmt.Errorf("query image dpkg owners: %w\ncommand output:\n%s", err, output)
		}
		return nil, fmt.Errorf("query image dpkg owners: %w", err)
	}
	return stdout.Bytes(), nil
}

// QueryDPKGPackageState performs one fixed read-only exact-state query for
// already-known owner package names. It never enumerates installed packages.
func (session *ImageValidationSession) QueryDPKGPackageState(ctx context.Context, names []string) ([]byte, error) {
	if session == nil || session.closed {
		return nil, fmt.Errorf("image validation session is not open")
	}
	if ctx == nil {
		return nil, fmt.Errorf("image validation dpkg package-state context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("query image dpkg package state: %w", err)
	}
	if len(names) == 0 {
		return []byte{}, nil
	}
	names = append([]string{}, names...)
	sort.Strings(names)
	for index, name := range names {
		if !validImageDPKGPackageName(name) {
			return nil, fmt.Errorf("image validation dpkg package name %d is invalid", index)
		}
		if index > 0 && names[index-1] == name {
			return nil, fmt.Errorf("image validation dpkg package names must be unique")
		}
	}
	args := []string{
		"exec", "--user", "0:0", "--workdir", "/", session.containerName,
		"/usr/bin/dpkg-query", "--show",
		"--showformat=${binary:Package}\t${Version}\t${Architecture}\t${Status}\n",
	}
	args = append(args, names...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runImageValidationFollowupCommand(CommandSpec{Name: "docker", Args: args}, RunOptions{
		Context: ctx, Stdout: &stdout, Stderr: &stderr,
	}); err != nil {
		output := trimmedCommandOutput(stderr.String())
		if output != "" {
			return nil, fmt.Errorf("query image dpkg package state: %w\ncommand output:\n%s", err, output)
		}
		return nil, fmt.Errorf("query image dpkg package state: %w", err)
	}
	return stdout.Bytes(), nil
}

// QueryAlternative performs one fixed read-only query for a link-group name
// derived from an already-observed /etc/alternatives path.
func (session *ImageValidationSession) QueryAlternative(ctx context.Context, group string) ([]byte, error) {
	if session == nil || session.closed {
		return nil, fmt.Errorf("image validation session is not open")
	}
	if ctx == nil {
		return nil, fmt.Errorf("image validation alternatives context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("query image alternative: %w", err)
	}
	if !validImageAlternativeGroup(group) {
		return nil, fmt.Errorf("image validation alternative group is invalid")
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	spec := CommandSpec{Name: "docker", Args: []string{
		"exec", "--user", "0:0", "--workdir", "/", session.containerName,
		"/usr/bin/update-alternatives", "--query", group,
	}}
	if err := runImageValidationFollowupCommand(spec, RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr}); err != nil {
		output := trimmedCommandOutput(stderr.String())
		if output != "" {
			return nil, fmt.Errorf("query image alternative %q: %w\ncommand output:\n%s", group, err, output)
		}
		return nil, fmt.Errorf("query image alternative %q: %w", group, err)
	}
	return stdout.Bytes(), nil
}

// ValidateBuildScratchAbsent performs the one fixed absence check required
// before provider mount roots can be trusted. It cannot inspect a
// caller-selected path.
func (session *ImageValidationSession) ValidateBuildScratchAbsent(ctx context.Context) error {
	if session == nil || session.closed {
		return fmt.Errorf("image validation session is not open")
	}
	if ctx == nil {
		return fmt.Errorf("image validation build-scratch context is required")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("validate image build-scratch absence: %w", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	spec := CommandSpec{Name: "docker", Args: []string{
		"exec", "--user", "0:0", "--workdir", "/", session.containerName,
		"/bin/sh", "-c", `test ! -e "$1"`, "reploy-validation", "/.reploy-build",
	}}
	if err := runImageValidationFollowupCommand(spec, RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr}); err != nil {
		return fmt.Errorf("image validation requires /.reploy-build to be absent: %w", imageValidationCommandError("build-scratch absence", session.descriptor.Platform.Canonical, stderr.String(), err))
	}
	return nil
}

func validImageAlternativeGroup(value string) bool {
	if value == "" || value[0] == '-' {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("+_.-", char) {
			continue
		}
		return false
	}
	return true
}

func validImageDPKGPackageName(value string) bool {
	if len(value) < 2 {
		return false
	}
	for index, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || index > 0 && strings.ContainsRune("+.-", char) {
			continue
		}
		return false
	}
	return true
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
	return imageValidationCreateCommandSpecWithAPT(descriptor, workspace, nil)
}

func imageValidationCreateCommandSpecWithAPT(
	descriptor deploy.ImageDescriptor,
	workspace PreparedProbeWorkspace,
	aptWorkspace *PreparedAPTResolverWorkspace,
) (CommandSpec, string, error) {
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
	}
	if aptWorkspace != nil {
		if err := validatePreparedAPTResolverWorkspace(*aptWorkspace); err != nil {
			return CommandSpec{}, "", err
		}
		aptMount, err := dockerMountArgument(
			"type=bind", "source="+aptWorkspace.HostDir,
			"target="+aptWorkspace.ContainerDir,
		)
		if err != nil {
			return CommandSpec{}, "", err
		}
		args = append(args, "--mount", aptMount)
	}
	args = append(args, "--entrypoint", workspace.ContainerExecutable, string(descriptor.ConfigDigest), "hold")
	return CommandSpec{Name: "docker", Args: args}, containerName, nil
}

func validatePreparedProbeWorkspace(descriptor deploy.ImageDescriptor, workspace PreparedProbeWorkspace) error {
	if err := validatePreparedProbeWorkspaceShape(workspace); err != nil {
		return err
	}
	if workspace.Platform.Canonical != descriptor.Platform.Canonical {
		return fmt.Errorf("probe helper platform %s does not match image platform %s", workspace.Platform.Canonical, descriptor.Platform.Canonical)
	}
	return nil
}

func validatePreparedProbeWorkspaceShape(workspace PreparedProbeWorkspace) error {
	if err := workspace.Platform.Validate(); err != nil {
		return fmt.Errorf("probe helper platform: %w", err)
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
			return markProviderHelperCleanupError(fmt.Errorf("remove image validation container %s: %w\ncommand output:\n%s", containerName, err, output))
		}
		return markProviderHelperCleanupError(fmt.Errorf("remove image validation container %s: %w", containerName, err))
	}
	return nil
}

func dockerMountArgument(fields ...string) (string, error) {
	var output strings.Builder
	writer := csv.NewWriter(&output)
	if err := writer.Write(fields); err != nil {
		return "", fmt.Errorf("render Docker mount: %w", err)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", fmt.Errorf("render Docker mount: %w", err)
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
