package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"reflect"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/buildprofile"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

type PythonResolverSession struct {
	descriptor    deploy.ImageDescriptor
	workspace     PreparedProbeWorkspace
	artifacts     PreparedPythonResolverArtifacts
	containerName string
	runDocker     commandRunner
	observations  map[string]probe.ExecutableObservationV1
	inspected     map[string]string
	buildTools    []PortableBuildToolEvidenceV1
	stopped       bool
	closed        bool
}

var bindPythonResolverCommandRunner = bindDockerCommandRunnerV1
var runPythonResolverFollowupCommand = runDockerCommand

type pythonSourceBuildCommand struct {
	operation string
	profile   string
	argv      []string
}

const (
	pythonSourceBuildRoot                = "/tmp/reploy-source-build"
	pythonSourceBuilderRoot              = "/tmp/reploy-source-builder"
	pythonSourceUVCacheRoot              = "/tmp/reploy-uv-cache"
	pythonSourceBuildEnvironmentSchemaV1 = "python-source-build-environment-v1"
)

type pythonSourceBuildEnvironmentV1 struct {
	Schema      string                        `json:"schema"`
	Platform    blueprint.Platform            `json:"platform"`
	Upstream    providers.RealizedImageV1     `json:"upstream"`
	Interpreter providers.ExecutableEvidence  `json:"interpreter"`
	BuildTools  []PortableBuildToolEvidenceV1 `json:"build_tools,omitempty"`
}

// OpenPythonResolverSession starts the one disposable consumer container used
// for Python prerequisite validation and, later, wheel resolution. Its public
// methods expose only fixed provider-owned operations.
func OpenPythonResolverSession(
	ctx context.Context,
	descriptor deploy.ImageDescriptor,
	workspace PreparedProbeWorkspace,
	artifacts PreparedPythonResolverArtifacts,
) (*PythonResolverSession, error) {
	if ctx == nil {
		return nil, fmt.Errorf("Python resolver session context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open Python resolver session: %w", err)
	}
	if err := descriptor.Validate(); err != nil {
		return nil, fmt.Errorf("Python resolver descriptor: %w", err)
	}
	if descriptor.Platform.OS != "linux" {
		return nil, fmt.Errorf("Python resolver requires a Linux image")
	}
	if err := validatePreparedProbeWorkspace(descriptor, workspace); err != nil {
		return nil, err
	}
	if err := validatePreparedPythonResolverArtifacts(artifacts); err != nil {
		return nil, err
	}
	probeMount, err := dockerMountArgument("type=bind", "source="+workspace.HostDir, "target="+workspace.ContainerDir, "readonly")
	if err != nil {
		return nil, err
	}
	inputMount, err := dockerMountArgument("type=bind", "source="+artifacts.InputHostDir, "target="+artifacts.InputContainerDir, "readonly")
	if err != nil {
		return nil, err
	}
	outputMount, err := dockerMountArgument("type=bind", "source="+artifacts.OutputHostDir, "target="+artifacts.OutputContainerDir)
	if err != nil {
		return nil, err
	}
	containerName := pythonResolverContainerName(workspace.HostDir)
	spec := CommandSpec{Name: "docker", Args: []string{
		"create", "--name", containerName,
		"--platform", descriptor.Platform.Canonical, "--pull", "never",
		"--user", "0:0", "--workdir", "/", "--read-only",
		"--network", "default", "--tmpfs", "/tmp:rw,exec,nosuid,nodev,mode=1777",
		"--mount", probeMount,
		"--mount", inputMount,
		"--mount", outputMount,
		"--entrypoint", workspace.ContainerExecutable,
		string(descriptor.ConfigDigest), "hold",
	}}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runDocker, err := bindPythonResolverCommandRunner(context.WithoutCancel(ctx), spec, 0)
	if err != nil {
		return nil, pythonResolverCommandError("create", descriptor.Platform.Canonical, stderr.String(), err)
	}
	if err := runDocker(spec, RunOptions{Context: context.WithoutCancel(ctx), Stdout: &stdout, Stderr: &stderr}); err != nil {
		return nil, pythonResolverCommandError("create", descriptor.Platform.Canonical, stderr.String(), err)
	}
	if err := ctx.Err(); err != nil {
		cleanupErr := removePythonResolverContainer(context.WithoutCancel(ctx), containerName, runDocker)
		return nil, errors.Join(fmt.Errorf("open Python resolver session: %w", err), cleanupErr)
	}
	stderr.Reset()
	if err := runDocker(CommandSpec{Name: "docker", Args: []string{"start", containerName}}, RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr}); err != nil {
		startErr := pythonResolverCommandError("start", descriptor.Platform.Canonical, stderr.String(), err)
		cleanupErr := removePythonResolverContainer(context.WithoutCancel(ctx), containerName, runDocker)
		return nil, errors.Join(startErr, cleanupErr)
	}
	return &PythonResolverSession{
		descriptor: descriptor, workspace: workspace, artifacts: artifacts, containerName: containerName, runDocker: runDocker,
		observations: map[string]probe.ExecutableObservationV1{},
		inspected:    map[string]string{},
	}, nil
}

func (session *PythonResolverSession) runDockerCommand(spec CommandSpec, options RunOptions) error {
	if session.runDocker != nil {
		return session.runDocker(spec, options)
	}
	return runPythonResolverFollowupCommand(spec, options)
}

// Probe performs the fixed filesystem observation as the first operation in
// this already-running resolver container.
func (session *PythonResolverSession) Probe(ctx context.Context, request probe.RequestV1) (probe.ResponseV1, error) {
	if session == nil || session.closed || session.stopped {
		return probe.ResponseV1{}, fmt.Errorf("Python resolver session is not open")
	}
	if ctx == nil {
		return probe.ResponseV1{}, fmt.Errorf("Python resolver probe context is required")
	}
	if err := ctx.Err(); err != nil {
		return probe.ResponseV1{}, err
	}
	if err := probe.ValidateRequestV1(request); err != nil {
		return probe.ResponseV1{}, err
	}
	encoded, err := canonical.Marshal(request)
	if err != nil {
		return probe.ResponseV1{}, fmt.Errorf("encode Python resolver probe: %w", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	spec := CommandSpec{Name: "docker", Args: []string{
		"exec", "--interactive", "--user", "0:0", "--workdir", "/",
		session.containerName, session.workspace.ContainerExecutable,
	}}
	if err := session.runDockerCommand(spec, RunOptions{Context: ctx, Stdin: bytes.NewReader(encoded), Stdout: &stdout, Stderr: &stderr}); err != nil {
		return probe.ResponseV1{}, pythonResolverCommandError("probe", session.descriptor.Platform.Canonical, stderr.String(), err)
	}
	response, err := probe.DecodeResponseV1(request, stdout.Bytes())
	if err != nil {
		return probe.ResponseV1{}, fmt.Errorf("decode Python resolver probe response: %w", err)
	}
	for _, observation := range response.Observations {
		session.observations[observation.ID] = observation
	}
	return response, nil
}

// ValidatedExecutableInput binds one typed consumer input to filesystem
// evidence observed by this resolver container's preceding probe.
func (session *PythonResolverSession) ValidatedExecutableInput(
	role string,
	requirement providers.ExecutableRequirement,
	output providers.QualifiedOutput,
	facts providers.CanonicalProviderData,
) (providers.ValidatedExecutableInput, error) {
	if session == nil || session.closed || session.stopped {
		return providers.ValidatedExecutableInput{}, fmt.Errorf("Python resolver session is not open")
	}
	observation, found := session.observations[requirement.ID]
	if !found {
		return providers.ValidatedExecutableInput{}, fmt.Errorf("Python resolver executable requirement %q was not probed in this container", requirement.ID)
	}
	evidence, err := ExecutableEvidenceFromProbe(observation, ProbeExecutableBinding{
		Requirement: &requirement,
		Output:      output,
		Facts:       facts,
	})
	if err != nil {
		return providers.ValidatedExecutableInput{}, fmt.Errorf("Python resolver executable requirement %q: %w", requirement.ID, err)
	}
	input := providers.ValidatedExecutableInput{
		ID: requirement.ID, Role: role, Policy: requirement.ValidationPolicy, Evidence: evidence,
	}
	if err := providers.ValidateValidatedExecutableInput(input); err != nil {
		return providers.ValidatedExecutableInput{}, fmt.Errorf("Python resolver executable requirement %q input: %w", requirement.ID, err)
	}
	return input, nil
}

// InspectAndBindInterpreter validates the currently probed candidate, runs the
// fixed Python inspection, and only then returns selected-output evidence with
// observed version facts.
func (session *PythonResolverSession) InspectAndBindInterpreter(
	ctx context.Context,
	launcher providers.ValidatedExecutableInput,
	requirement providers.ExecutableRequirement,
	output providers.QualifiedOutput,
) (providers.ValidatedExecutableInput, string, error) {
	inspectionInput, err := session.ValidatedExecutableInput(
		providers.ExecutableRoleSelectedOutput,
		requirement,
		output,
		providers.CanonicalProviderData{
			Schema: "python-interpreter-inspection-v1",
			Value:  canonical.Object{"consumer_kind": "python"},
		},
	)
	if err != nil {
		return providers.ValidatedExecutableInput{}, "", err
	}
	version, err := session.InspectInterpreter(ctx, launcher, inspectionInput)
	if err != nil {
		return providers.ValidatedExecutableInput{}, "", err
	}
	selected, err := session.ValidatedExecutableInput(
		providers.ExecutableRoleSelectedOutput,
		requirement,
		output,
		pythonprovider.CanonicalInterpreterFactsV1(version),
	)
	if err != nil {
		return providers.ValidatedExecutableInput{}, "", err
	}
	return selected, version, nil
}

// InspectInterpreter runs only Python's fixed isolated inspection through an
// already-validated clean-environment launcher in this resolver container.
func (session *PythonResolverSession) InspectInterpreter(
	ctx context.Context,
	launcher providers.ValidatedExecutableInput,
	interpreter providers.ValidatedExecutableInput,
) (string, error) {
	if session == nil || session.closed || session.stopped {
		return "", fmt.Errorf("Python resolver session is not open")
	}
	if ctx == nil {
		return "", fmt.Errorf("Python interpreter inspection context is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := providers.ValidateValidatedExecutableInput(launcher); err != nil {
		return "", fmt.Errorf("Python resolver environment launcher: %w", err)
	}
	if launcher.Role != providers.ExecutableRoleEnvironmentLauncher {
		return "", fmt.Errorf("Python resolver environment launcher role must be %q", providers.ExecutableRoleEnvironmentLauncher)
	}
	if err := providers.ValidateValidatedExecutableInput(interpreter); err != nil {
		return "", fmt.Errorf("Python resolver interpreter: %w", err)
	}
	if interpreter.Role != providers.ExecutableRoleSelectedOutput {
		return "", fmt.Errorf("Python resolver interpreter role must be %q", providers.ExecutableRoleSelectedOutput)
	}
	for _, required := range []struct {
		name       string
		executable providers.ValidatedExecutableInput
	}{
		{name: "environment launcher", executable: launcher},
		{name: "interpreter", executable: interpreter},
	} {
		observation, found := session.observations[required.executable.ID]
		if !found {
			return "", fmt.Errorf("Python resolver %s %q was not probed in this container", required.name, required.executable.Evidence.InvocationPath)
		}
		requirement := providers.ExecutableRequirement{
			ID: required.executable.ID, Command: required.executable.Evidence.Output.Name,
			Supplier: required.executable.Evidence.Output.Component, ValidationPolicy: required.executable.Policy,
		}
		expected, err := ExecutableEvidenceFromProbe(observation, ProbeExecutableBinding{
			Requirement: &requirement, Output: required.executable.Evidence.Output, Facts: required.executable.Evidence.Facts,
		})
		if err != nil {
			return "", fmt.Errorf("Python resolver %s probe evidence: %w", required.name, err)
		}
		if !reflect.DeepEqual(expected, required.executable.Evidence) {
			return "", fmt.Errorf("Python resolver %s evidence does not match this container's probe", required.name)
		}
	}
	inspection, err := pythonprovider.InterpreterInspectionArgv(interpreter.Evidence.InvocationPath)
	if err != nil {
		return "", err
	}
	args := []string{
		"exec", "--user", "0:0", "--workdir", "/", session.containerName,
		launcher.Evidence.InvocationPath, "-i",
		"HOME=/tmp", "LANG=C", "LC_ALL=C",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TMPDIR=/tmp",
	}
	args = append(args, inspection...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := session.runDockerCommand(CommandSpec{Name: "docker", Args: args}, RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr}); err != nil {
		return "", pythonResolverCommandError("inspect interpreter", session.descriptor.Platform.Canonical, stderr.String(), err)
	}
	version, err := pythonprovider.ParseInterpreterInspectionOutput(stdout.Bytes())
	if err != nil {
		return "", err
	}
	session.inspected[interpreter.Evidence.InvocationPath] = version
	return version, nil
}

// ResolveWheels runs the one provider-owned pip invocation after the selected
// interpreter has been observed in this exact container.
func (session *PythonResolverSession) ResolveWheels(
	ctx context.Context,
	launcher providers.ValidatedExecutableInput,
	requirement providers.ExecutableRequirement,
	interpreter providers.ExecutableEvidence,
	request providers.CanonicalProviderRequest,
	sources []providers.ResolvedSourceInput,
	reusable []providerstore.ArtifactDescriptor,
) error {
	if session == nil || session.closed || session.stopped {
		return fmt.Errorf("Python resolver session is not open")
	}
	if ctx == nil {
		return fmt.Errorf("Python wheel resolution context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := session.validateWheelOperationInputs(launcher, requirement, interpreter); err != nil {
		return err
	}
	if err := StagePythonResolverSourceConstraints(session.artifacts, request, sources, reusable); err != nil {
		return err
	}
	resolverArgv, err := pythonprovider.WheelResolverArgv(interpreter.InvocationPath, request, sources, reusable)
	if err != nil {
		return err
	}
	args := []string{
		"exec", "--user", "0:0", "--workdir", "/", session.containerName,
		launcher.Evidence.InvocationPath, "-i", "HOME=/tmp", "LANG=C", "LC_ALL=C",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TMPDIR=/tmp",
	}
	args = append(args, resolverArgv...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := session.runDockerCommand(CommandSpec{Name: "docker", Args: args}, RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr}); err != nil {
		commandErr := pythonResolverCommandError("resolve wheels", session.descriptor.Platform.Canonical, stderr.String(), err)
		if strings.Contains(strings.ToLower(stderr.String()), "no module named pip") {
			return fmt.Errorf("selected Python interpreter has no pip module; ensure its providing packages include pip support: %w", commandErr)
		}
		return fmt.Errorf("Python wheel resolution failed: %w", commandErr)
	}
	return nil
}

// BuildSourceDistributions copies immutable source inputs into container-local
// scratch and asks each declared backend for exactly one portable sdist.
func (session *PythonResolverSession) BuildSourceDistributions(
	ctx context.Context,
	launcher providers.ValidatedExecutableInput,
	requirement providers.ExecutableRequirement,
	interpreter providers.ExecutableEvidence,
	snapshots []PreparedPythonSourceSnapshot,
) error {
	if session == nil || session.closed || session.stopped {
		return fmt.Errorf("Python resolver session is not open")
	}
	if ctx == nil {
		return fmt.Errorf("Python source build context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshots == nil {
		return fmt.Errorf("Python source snapshots must use an array")
	}
	if err := validatePreparedPythonResolverArtifacts(session.artifacts); err != nil {
		return err
	}
	if err := validatePreparedPythonSourceSnapshots(session.artifacts, snapshots); err != nil {
		return err
	}
	if err := session.validateWheelOperationInputs(launcher, requirement, interpreter); err != nil {
		return err
	}
	if len(snapshots) == 0 {
		return nil
	}

	commands := []pythonSourceBuildCommand{
		{
			operation: "clear source-build scratch",
			argv:      []string{"rm", "-rf", pythonSourceBuildRoot, pythonSourceBuilderRoot, pythonSourceUVCacheRoot},
		},
		{
			operation: "create source-build scratch",
			argv:      []string{"mkdir", "-p", pythonSourceBuildRoot},
		},
	}
	for _, snapshot := range snapshots {
		buildDir := path.Join(pythonSourceBuildRoot, snapshot.Distribution)
		commands = append(commands,
			pythonSourceBuildCommand{
				operation: "create source-build directory",
				profile:   "Prepare source-build directory: " + snapshot.Distribution,
				argv:      []string{"mkdir", "-p", buildDir},
			},
			pythonSourceBuildCommand{
				operation: "copy source snapshot",
				profile:   "Copy source snapshot: " + snapshot.Distribution,
				argv:      []string{"cp", "-a", snapshot.ContainerDir + "/.", buildDir},
			},
		)
	}
	commands = append(commands, pythonSourceBuildCommand{
		operation: "bootstrap pinned uv source builder",
		argv: []string{
			interpreter.InvocationPath, "-m", "pip", "--disable-pip-version-check",
			"install", "--no-cache-dir", "--progress-bar", "off", "--root-user-action", "ignore",
			"--find-links", session.artifacts.InputContainerDir,
			"--target", pythonSourceBuilderRoot, pythonprovider.SourceBuilderRequirementV1,
		},
	})
	for _, snapshot := range snapshots {
		commands = append(commands, pythonSourceBuildCommand{
			operation: "build source distribution",
			profile:   "Build source distribution: " + snapshot.Distribution,
			argv: []string{
				interpreter.InvocationPath, "-m", "uv", "build", "--no-progress", "--sdist", "--no-sources",
				"--python", interpreter.InvocationPath, "--no-python-downloads",
				"--find-links", session.artifacts.InputContainerDir,
				"--out-dir", session.artifacts.OutputContainerDir,
				path.Join(pythonSourceBuildRoot, snapshot.Distribution),
			},
		})
	}
	commands = append(commands, pythonSourceBuildCommand{
		operation: "clear source-builder metadata",
		argv:      []string{"rm", "-f", path.Join(session.artifacts.OutputContainerDir, ".gitignore")},
	})
	for _, command := range commands {
		label := command.profile
		if label == "" {
			label = command.operation
		}
		commandCtx, endCommand := buildprofile.Start(ctx, label)
		err := session.runWheelEnvironmentCommand(commandCtx, launcher, interpreter.InvocationPath, command.operation, command.argv)
		endCommand(err)
		if err != nil {
			if command.operation == "bootstrap pinned uv source builder" && strings.Contains(strings.ToLower(err.Error()), "no module named pip") {
				return fmt.Errorf("selected Python interpreter has no pip module; ensure its providing packages include pip support: %w", err)
			}
			if command.operation == "build source distribution" {
				return fmt.Errorf(
					"local Python source sdist build failed; its backend must produce an sdist "+
						"from an ordinary source tree without VCS metadata: %w",
					err,
				)
			}
			return err
		}
	}
	return nil
}

func (session *PythonResolverSession) SourceBuildEnvironmentDigest(
	interpreter providers.ExecutableEvidence,
) (canonical.Digest, error) {
	if session == nil || session.closed || session.stopped {
		return "", fmt.Errorf("Python resolver session is not open")
	}
	version, inspected := session.inspected[interpreter.InvocationPath]
	if !inspected || interpreter.Facts.Schema != pythonprovider.InterpreterFactsSchemaV1 ||
		interpreter.Facts.Value["version"] != version {
		return "", fmt.Errorf("Python source build environment interpreter was not inspected in this container")
	}
	upstream, err := realizedImageFromDescriptor(session.descriptor)
	if err != nil {
		return "", err
	}
	environment := pythonSourceBuildEnvironmentV1{
		Schema:      pythonSourceBuildEnvironmentSchemaV1,
		Platform:    session.descriptor.Platform,
		Upstream:    upstream,
		Interpreter: interpreter,
		BuildTools:  append([]PortableBuildToolEvidenceV1{}, session.buildTools...),
	}
	return canonical.Sum(
		"python-source-build-environment", pythonSourceBuildEnvironmentSchemaV1, environment,
	)
}

// BuildSourceWheels copies only the securely extracted retained sdists into
// container-local scratch and builds one wheel from each closed artifact.
func (session *PythonResolverSession) BuildSourceWheels(
	ctx context.Context,
	launcher providers.ValidatedExecutableInput,
	requirement providers.ExecutableRequirement,
	interpreter providers.ExecutableEvidence,
	distributions []PreparedPythonSourceDistribution,
) error {
	if session == nil || session.closed || session.stopped {
		return fmt.Errorf("Python resolver session is not open")
	}
	if ctx == nil {
		return fmt.Errorf("Python source wheel build context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if distributions == nil {
		return fmt.Errorf("Python source distributions must use an array")
	}
	if err := validatePreparedPythonResolverArtifacts(session.artifacts); err != nil {
		return err
	}
	if err := validatePreparedPythonSourceDistributions(session.artifacts, distributions); err != nil {
		return err
	}
	if err := session.validateWheelOperationInputs(launcher, requirement, interpreter); err != nil {
		return err
	}
	if len(distributions) == 0 {
		return nil
	}

	commands := []pythonSourceBuildCommand{
		{operation: "clear source-build scratch", argv: []string{"rm", "-rf", pythonSourceBuildRoot}},
		{operation: "create source-build scratch", argv: []string{"mkdir", "-p", pythonSourceBuildRoot}},
	}
	for _, distribution := range distributions {
		buildDir := path.Join(pythonSourceBuildRoot, distribution.Distribution)
		commands = append(commands,
			pythonSourceBuildCommand{
				operation: "create source-build directory",
				profile:   "Prepare wheel-build directory: " + distribution.Distribution,
				argv:      []string{"mkdir", "-p", buildDir},
			},
			pythonSourceBuildCommand{
				operation: "copy retained source distribution",
				profile:   "Copy retained source distribution: " + distribution.Distribution,
				argv:      []string{"cp", "-a", distribution.ContainerDir + "/.", buildDir},
			},
			pythonSourceBuildCommand{
				operation: "build source wheel from retained distribution",
				profile:   "Build wheel: " + distribution.Distribution,
				argv: []string{
					interpreter.InvocationPath, "-m", "uv", "build", "--no-progress", "--wheel", "--no-sources",
					"--python", interpreter.InvocationPath, "--no-python-downloads",
					"--find-links", session.artifacts.InputContainerDir,
					"--out-dir", session.artifacts.OutputContainerDir,
					path.Join(buildDir, distribution.ArchiveRoot),
				},
			},
		)
	}
	commands = append(commands, pythonSourceBuildCommand{
		operation: "clear source-builder metadata",
		argv:      []string{"rm", "-f", path.Join(session.artifacts.OutputContainerDir, ".gitignore")},
	})
	for _, command := range commands {
		label := command.profile
		if label == "" {
			label = command.operation
		}
		commandCtx, endCommand := buildprofile.Start(ctx, label)
		err := session.runWheelEnvironmentCommand(commandCtx, launcher, interpreter.InvocationPath, command.operation, command.argv)
		endCommand(err)
		if err != nil {
			return err
		}
	}
	return nil
}

func (session *PythonResolverSession) validateWheelOperationInputs(
	launcher providers.ValidatedExecutableInput,
	requirement providers.ExecutableRequirement,
	interpreter providers.ExecutableEvidence,
) error {
	if err := providers.ValidateValidatedExecutableInput(launcher); err != nil || launcher.Role != providers.ExecutableRoleEnvironmentLauncher {
		return fmt.Errorf("Python wheel operation requires its validated environment launcher")
	}
	launcherRequirement := providers.ExecutableRequirement{
		ID: launcher.ID, Command: launcher.Evidence.Output.Name,
		Supplier: launcher.Evidence.Output.Component, ValidationPolicy: launcher.Policy,
	}
	expectedLauncher, err := session.ValidatedExecutableInput(
		providers.ExecutableRoleEnvironmentLauncher, launcherRequirement, launcher.Evidence.Output, launcher.Evidence.Facts,
	)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expectedLauncher, launcher) {
		return fmt.Errorf("Python wheel operation environment launcher evidence does not match this container")
	}
	selected := providers.ValidatedExecutableInput{
		ID: requirement.ID, Role: providers.ExecutableRoleSelectedOutput,
		Policy: requirement.ValidationPolicy, Evidence: interpreter,
	}
	if err := providers.ValidateValidatedExecutableInput(selected); err != nil {
		return fmt.Errorf("Python wheel operation interpreter: %w", err)
	}
	expected, err := session.ValidatedExecutableInput(
		providers.ExecutableRoleSelectedOutput, requirement, interpreter.Output, interpreter.Facts,
	)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected.Evidence, interpreter) {
		return fmt.Errorf("Python wheel operation interpreter evidence does not match this container")
	}
	version, inspected := session.inspected[interpreter.InvocationPath]
	if !inspected || interpreter.Facts.Schema != pythonprovider.InterpreterFactsSchemaV1 || interpreter.Facts.Value["version"] != version {
		return fmt.Errorf("Python wheel operation interpreter was not inspected in this container")
	}
	return nil
}

func (session *PythonResolverSession) runWheelEnvironmentCommand(
	ctx context.Context,
	launcher providers.ValidatedExecutableInput,
	interpreter string,
	operation string,
	command []string,
) error {
	args := []string{
		"exec", "--user", "0:0", "--workdir", "/", session.containerName,
		launcher.Evidence.InvocationPath, "-i",
		"HOME=/tmp", "LANG=C", "LC_ALL=C",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TMPDIR=/tmp",
		"PYTHONNOUSERSITE=1", "PYTHONPATH=" + pythonSourceBuilderRoot,
		"UV_CACHE_DIR=" + pythonSourceUVCacheRoot, "UV_PYTHON=" + interpreter, "UV_PYTHON_DOWNLOADS=never",
	}
	args = append(args, command...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := session.runDockerCommand(CommandSpec{Name: "docker", Args: args}, RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr}); err != nil {
		return pythonResolverCommandError(operation, session.descriptor.Platform.Canonical, stderr.String(), err)
	}
	return nil
}

// Stop ends the resolver before host-side output ingestion.
func (session *PythonResolverSession) Stop(ctx context.Context) error {
	if session == nil || session.closed {
		return fmt.Errorf("Python resolver session is not open")
	}
	if session.stopped {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("Python resolver stop context is required")
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := session.runDockerCommand(CommandSpec{Name: "docker", Args: []string{"kill", "--signal", "KILL", session.containerName}}, RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr}); err != nil {
		return pythonResolverCommandError("stop", session.descriptor.Platform.Canonical, stderr.String(), err)
	}
	session.stopped = true
	return nil
}

func (session *PythonResolverSession) Close(ctx context.Context) error {
	if session == nil || session.closed {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("close Python resolver session context is required")
	}
	if err := removePythonResolverContainer(ctx, session.containerName, session.runDockerCommand); err != nil {
		return err
	}
	session.closed = true
	return nil
}

func removePythonResolverContainer(ctx context.Context, containerName string, runDocker commandRunner) error {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runDocker(CommandSpec{Name: "docker", Args: []string{"rm", "--force", containerName}}, RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr}); err != nil {
		return markProviderHelperCleanupError(pythonResolverCommandError("remove", "local", stderr.String(), err))
	}
	return nil
}

func pythonResolverContainerName(workspace string) string {
	return strings.Replace(imageProbeContainerName(workspace), "reploy-probe-", "reploy-python-resolver-", 1)
}

func pythonResolverCommandError(operation string, platform string, stderr string, err error) error {
	output := trimmedCommandOutput(stderr)
	if strings.Contains(strings.ToLower(output), "exec format error") {
		return fmt.Errorf("Docker cannot execute the Python resolver for %s; enable binfmt/QEMU emulation for that platform or run the build on a compatible host: %w", platform, err)
	}
	if output != "" {
		return fmt.Errorf("%s Python resolver container for %s: %w\ncommand output:\n%s", operation, platform, err, output)
	}
	return fmt.Errorf("%s Python resolver container for %s: %w", operation, platform, err)
}
