package dockerdeploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
)

const aptOSReleaseProbeScriptV1 = `set -eu
. /etc/os-release
if [ "${ID+x}" = x ]; then printf 'ID\000%s\000' "$ID"; fi
if [ "${ID_LIKE+x}" = x ]; then printf 'ID_LIKE\000%s\000' "$ID_LIKE"; fi
if [ "${VERSION_ID+x}" = x ]; then printf 'VERSION_ID\000%s\000' "$VERSION_ID"; fi
`

type APTBaseValidation struct {
	Profile     aptprovider.BaseProfileEvidenceV1
	Executables []providers.ValidatedExecutableInput
}

type APTResolverSession struct {
	descriptor    deploy.ImageDescriptor
	probe         PreparedProbeWorkspace
	resolver      PreparedAPTResolverWorkspace
	containerName string
	observations  map[string]probe.ExecutableObservationV1
	base          *APTBaseValidation
	closed        bool
}

var runAPTResolverOpenCommand = runCommand
var runAPTResolverFollowupCommand = runCommandWithoutDockerPreflight

// OpenAPTResolverSession starts the one held container that will validate the
// exact prefix and, in later typed operations, resolve its APT transaction.
func OpenAPTResolverSession(
	ctx context.Context,
	descriptor deploy.ImageDescriptor,
	probeWorkspace PreparedProbeWorkspace,
	resolverWorkspace PreparedAPTResolverWorkspace,
) (*APTResolverSession, error) {
	if ctx == nil {
		return nil, fmt.Errorf("APT resolver session context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := descriptor.Validate(); err != nil {
		return nil, fmt.Errorf("APT resolver descriptor: %w", err)
	}
	if descriptor.Platform.OS != "linux" {
		return nil, fmt.Errorf("APT resolver requires a Linux image")
	}
	if err := validatePreparedProbeWorkspace(descriptor, probeWorkspace); err != nil {
		return nil, err
	}
	if err := validatePreparedAPTResolverWorkspace(resolverWorkspace); err != nil {
		return nil, err
	}
	probeMount, err := dockerMountArgument("type=bind", "source="+probeWorkspace.HostDir, "target="+probeWorkspace.ContainerDir, "readonly")
	if err != nil {
		return nil, err
	}
	resolverMount, err := dockerMountArgument("type=bind", "source="+resolverWorkspace.HostDir, "target="+resolverWorkspace.ContainerDir)
	if err != nil {
		return nil, err
	}
	containerName := aptResolverContainerName(resolverWorkspace.HostDir)
	spec := CommandSpec{Name: "docker", Args: []string{
		"create", "--name", containerName,
		"--platform", descriptor.Platform.Canonical, "--pull", "never",
		"--user", "0:0", "--workdir", "/", "--read-only",
		"--network", "default",
		"--mount", probeMount, "--mount", resolverMount,
		"--entrypoint", probeWorkspace.ContainerExecutable,
		descriptor.ImmutableReference, "hold",
	}}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runAPTResolverOpenCommand(spec, RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr}); err != nil {
		return nil, aptResolverCommandError("create", descriptor.Platform.Canonical, stderr.String(), err)
	}
	stderr.Reset()
	if err := runAPTResolverFollowupCommand(CommandSpec{Name: "docker", Args: []string{"start", containerName}}, RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr}); err != nil {
		startErr := aptResolverCommandError("start", descriptor.Platform.Canonical, stderr.String(), err)
		cleanupErr := removeAPTResolverContainer(context.WithoutCancel(ctx), containerName)
		return nil, errors.Join(startErr, cleanupErr)
	}
	return &APTResolverSession{
		descriptor: descriptor, probe: probeWorkspace, resolver: resolverWorkspace,
		containerName: containerName, observations: map[string]probe.ExecutableObservationV1{},
	}, nil
}

// ProbeBaseProfile is the fixed first resolver operation. It observes all
// required executable paths together, then invokes only their fixed read-only
// identity and architecture interfaces through the validated clean launcher.
func (session *APTResolverSession) ProbeBaseProfile(ctx context.Context) (APTBaseValidation, error) {
	if session == nil || session.closed {
		return APTBaseValidation{}, fmt.Errorf("APT resolver session is not open")
	}
	if ctx == nil {
		return APTBaseValidation{}, fmt.Errorf("APT base probe context is required")
	}
	if err := ctx.Err(); err != nil {
		return APTBaseValidation{}, err
	}
	if session.base != nil {
		return cloneAPTBaseValidation(*session.base), nil
	}

	requiredTools := aptprovider.RequiredBaseToolsV1()
	inspections := make([]probe.ExecutableInspectionV1, 0, len(requiredTools))
	for _, tool := range requiredTools {
		inspections = append(inspections, probe.ExecutableInspectionV1{ID: tool.Name, InvocationPath: tool.Path})
	}
	request := probe.RequestV1{Schema: probe.RequestSchemaV1, Inspections: inspections}
	response, err := session.runProbe(ctx, request)
	if err != nil {
		return APTBaseValidation{}, fmt.Errorf("validate APT base executables: %w", err)
	}
	for _, observation := range response.Observations {
		session.observations[observation.ID] = observation
	}

	osReleaseOutput, err := session.runProfileCommand(ctx, "/bin/sh", "-c", aptOSReleaseProbeScriptV1)
	if err != nil {
		return APTBaseValidation{}, err
	}
	osRelease, err := parseAPTOSReleaseOutputV1(osReleaseOutput)
	if err != nil {
		return APTBaseValidation{}, err
	}
	versionCommands := map[string][]string{
		"apt_get":    {"/usr/bin/apt-get", "--version"},
		"dpkg":       {"/usr/bin/dpkg", "--version"},
		"dpkg_deb":   {"/usr/bin/dpkg-deb", "--version"},
		"dpkg_query": {"/usr/bin/dpkg-query", "--version"},
	}
	versions := map[string]string{}
	for _, tool := range requiredTools {
		command, exists := versionCommands[tool.Name]
		if !exists {
			continue
		}
		output, err := session.runProfileCommand(ctx, command[0], command[1:]...)
		if err != nil {
			return APTBaseValidation{}, err
		}
		version, err := firstAPTOutputLine(tool.Name+" version", output)
		if err != nil {
			return APTBaseValidation{}, err
		}
		versions[tool.Name] = version
	}
	nativeOutput, err := session.runProfileCommand(ctx, "/usr/bin/dpkg", "--print-architecture")
	if err != nil {
		return APTBaseValidation{}, err
	}
	native, err := singleAPTOutputLine("native architecture", nativeOutput)
	if err != nil {
		return APTBaseValidation{}, err
	}
	foreignOutput, err := session.runProfileCommand(ctx, "/usr/bin/dpkg", "--print-foreign-architectures")
	if err != nil {
		return APTBaseValidation{}, err
	}
	foreign, err := aptOutputLines("foreign architectures", foreignOutput)
	if err != nil {
		return APTBaseValidation{}, err
	}
	for index := range requiredTools {
		requiredTools[index].Version = versions[requiredTools[index].Name]
	}
	profile, err := aptprovider.NewBaseProfileEvidenceV1(session.descriptor.Platform, osRelease, requiredTools, native, foreign)
	if err != nil {
		return APTBaseValidation{}, err
	}
	executables, err := session.bindBaseExecutables(profile.Tools)
	if err != nil {
		return APTBaseValidation{}, err
	}
	result := APTBaseValidation{Profile: profile, Executables: executables}
	session.base = &result
	return cloneAPTBaseValidation(result), nil
}

func (session *APTResolverSession) runProbe(ctx context.Context, request probe.RequestV1) (probe.ResponseV1, error) {
	encoded, err := canonical.Marshal(request)
	if err != nil {
		return probe.ResponseV1{}, err
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	spec := CommandSpec{Name: "docker", Args: []string{
		"exec", "--interactive", "--user", "0:0", "--workdir", "/",
		session.containerName, session.probe.ContainerExecutable,
	}}
	if err := runAPTResolverFollowupCommand(spec, RunOptions{Context: ctx, Stdin: bytes.NewReader(encoded), Stdout: &stdout, Stderr: &stderr}); err != nil {
		return probe.ResponseV1{}, aptResolverCommandError("probe", session.descriptor.Platform.Canonical, stderr.String(), err)
	}
	response, err := probe.DecodeResponseV1(request, stdout.Bytes())
	if err != nil {
		return probe.ResponseV1{}, fmt.Errorf("decode APT resolver probe response: %w", err)
	}
	return response, nil
}

func (session *APTResolverSession) runProfileCommand(ctx context.Context, executable string, arguments ...string) ([]byte, error) {
	if _, found := session.observationForPath("/usr/bin/env"); !found {
		return nil, fmt.Errorf("APT resolver clean-environment launcher was not probed in this container")
	}
	observation, found := session.observationForPath(executable)
	if !found {
		return nil, fmt.Errorf("APT resolver executable %s was not probed in this container", executable)
	}
	if observation.InvocationPath != executable {
		return nil, fmt.Errorf("APT resolver executable evidence does not match %s", executable)
	}
	args := []string{
		"exec", "--user", "0:0", "--workdir", "/", session.containerName,
		"/usr/bin/env", "-i",
		"HOME=/root", "LANG=C", "LC_ALL=C", "PATH=/usr/sbin:/usr/bin:/sbin:/bin",
		"TMPDIR=" + session.resolver.ContainerDir, "DEBIAN_FRONTEND=noninteractive",
		executable,
	}
	args = append(args, arguments...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runAPTResolverFollowupCommand(CommandSpec{Name: "docker", Args: args}, RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr}); err != nil {
		return nil, aptResolverCommandError("inspect "+executable, session.descriptor.Platform.Canonical, stderr.String(), err)
	}
	return stdout.Bytes(), nil
}

func (session *APTResolverSession) observationForPath(path string) (probe.ExecutableObservationV1, bool) {
	for _, observation := range session.observations {
		if observation.InvocationPath == path {
			return observation, true
		}
	}
	return probe.ExecutableObservationV1{}, false
}

func (session *APTResolverSession) bindBaseExecutables(tools []aptprovider.RequiredToolEvidenceV1) ([]providers.ValidatedExecutableInput, error) {
	result := make([]providers.ValidatedExecutableInput, 0, len(tools))
	for _, tool := range tools {
		observation, found := session.observations[tool.Name]
		if !found {
			return nil, fmt.Errorf("APT base tool %q was not probed in this container", tool.Name)
		}
		role := providers.ExecutableRoleProviderPrerequisite
		component := "apt"
		if tool.Name == "sh" {
			role, component = providers.ExecutableRoleCarrier, "backend"
		} else if tool.Name == "env" {
			role, component = providers.ExecutableRoleEnvironmentLauncher, "backend"
		}
		requirement := providers.ExecutableRequirement{
			ID: tool.Name, Command: tool.Name, Supplier: component,
			ValidationPolicy: providers.ValidationPolicyCompatible,
		}
		evidence, err := ExecutableEvidenceFromProbe(observation, ProbeExecutableBinding{
			Requirement: &requirement,
			Output:      providers.QualifiedOutput{Component: component, Name: tool.Name},
			Facts: providers.CanonicalProviderData{Schema: "apt-required-tool-v1", Value: canonical.Object{
				"interface": tool.Interface, "version": tool.Version,
			}},
		})
		if err != nil {
			return nil, err
		}
		input := providers.ValidatedExecutableInput{ID: tool.Name, Role: role, Policy: providers.ValidationPolicyCompatible, Evidence: evidence}
		if err := providers.ValidateValidatedExecutableInput(input); err != nil {
			return nil, err
		}
		result = append(result, input)
	}
	sort.Slice(result, func(left int, right int) bool { return result[left].ID < result[right].ID })
	return result, nil
}

func (session *APTResolverSession) Close(ctx context.Context) error {
	if session == nil || session.closed {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("close APT resolver session context is required")
	}
	if err := removeAPTResolverContainer(ctx, session.containerName); err != nil {
		return err
	}
	session.closed = true
	return nil
}

func parseAPTOSReleaseOutputV1(output []byte) (map[string]string, error) {
	parts := bytes.Split(output, []byte{0})
	if len(parts) < 3 || len(parts)%2 == 0 || len(parts[len(parts)-1]) != 0 {
		return nil, fmt.Errorf("APT OS release probe returned malformed output")
	}
	result := map[string]string{}
	for index := 0; index+1 < len(parts); index += 2 {
		name, value := string(parts[index]), string(parts[index+1])
		if name != "ID" && name != "ID_LIKE" && name != "VERSION_ID" {
			return nil, fmt.Errorf("APT OS release probe returned unexpected field %q", name)
		}
		if _, exists := result[name]; exists {
			return nil, fmt.Errorf("APT OS release probe repeated field %q", name)
		}
		result[name] = value
	}
	return result, nil
}

func firstAPTOutputLine(subject string, output []byte) (string, error) {
	newline := bytes.IndexByte(output, '\n')
	if newline <= 0 {
		return "", fmt.Errorf("APT %s probe returned no value", subject)
	}
	line := string(output[:newline])
	if strings.ContainsAny(line, "\x00\r") || strings.TrimSpace(line) != line {
		return "", fmt.Errorf("APT %s probe returned malformed output", subject)
	}
	return line, nil
}

func singleAPTOutputLine(subject string, output []byte) (string, error) {
	lines, err := aptOutputLines(subject, output)
	if err != nil {
		return "", err
	}
	if len(lines) != 1 {
		return "", fmt.Errorf("APT %s probe must return exactly one value", subject)
	}
	return lines[0], nil
}

func aptOutputLines(subject string, output []byte) ([]string, error) {
	text := strings.TrimSuffix(string(output), "\n")
	if text == "" {
		return []string{}, nil
	}
	if strings.ContainsAny(text, "\x00\r") || !strings.HasSuffix(string(output), "\n") {
		return nil, fmt.Errorf("APT %s probe returned malformed output", subject)
	}
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if line == "" || strings.TrimSpace(line) != line {
			return nil, fmt.Errorf("APT %s probe returned malformed output", subject)
		}
	}
	return lines, nil
}

func cloneAPTBaseValidation(input APTBaseValidation) APTBaseValidation {
	result := input
	result.Profile.OSRelease = append([]aptprovider.OSReleaseFieldV1{}, input.Profile.OSRelease...)
	result.Profile.Tools = append([]aptprovider.RequiredToolEvidenceV1{}, input.Profile.Tools...)
	result.Profile.ForeignArchitectures = append([]string{}, input.Profile.ForeignArchitectures...)
	result.Executables = make([]providers.ValidatedExecutableInput, len(input.Executables))
	for index, executable := range input.Executables {
		result.Executables[index] = executable
		result.Executables[index].Evidence.LinkChain = append([]providers.LinkEvidence{}, executable.Evidence.LinkChain...)
		result.Executables[index].Evidence.Access.Paths = append([]providers.AccessPathEvidence{}, executable.Evidence.Access.Paths...)
	}
	return result
}

func aptResolverContainerName(workspace string) string {
	digest := sha256.Sum256([]byte(workspace))
	return fmt.Sprintf("reploy-apt-resolve-%x", digest[:12])
}

func removeAPTResolverContainer(ctx context.Context, containerName string) error {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runAPTResolverFollowupCommand(CommandSpec{Name: "docker", Args: []string{"rm", "--force", containerName}}, RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr}); err != nil {
		return aptResolverCommandError("remove", "container", stderr.String(), err)
	}
	return nil
}

func aptResolverCommandError(operation string, platform string, stderr string, err error) error {
	output := trimmedCommandOutput(stderr)
	if strings.Contains(strings.ToLower(output), "exec format error") {
		return fmt.Errorf("Docker cannot execute the %s APT resolver; enable binfmt/QEMU emulation for that platform or run the build on a compatible host: %w", platform, err)
	}
	if output != "" {
		return fmt.Errorf("%s APT resolver for %s: %w\ncommand output:\n%s", operation, platform, err, output)
	}
	return fmt.Errorf("%s APT resolver for %s: %w", operation, platform, err)
}
