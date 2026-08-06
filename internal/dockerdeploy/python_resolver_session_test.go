package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

func TestPythonResolverSessionProbesAndInspectsInOneContainer(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, filepath.Join(t.TempDir(), "with,comma"))
	artifacts := testPreparedPythonResolverArtifacts(t)
	request, responseRecord := pythonResolverProbeExchange()
	response := mustCanonicalProbeResponse(t, responseRecord)
	commands, probeInput := stubPythonResolverCommands(t, response, []byte("3.13.2\n"), nil)

	session, err := OpenPythonResolverSession(context.Background(), descriptor, workspace, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Probe(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	launcher := pythonResolverSessionInput(t, session, responseRecord.Observations[0], providers.ExecutableRoleEnvironmentLauncher)
	interpreter := pythonResolverSessionInput(t, session, responseRecord.Observations[1], providers.ExecutableRoleSelectedOutput)
	version, err := session.InspectInterpreter(context.Background(), launcher, interpreter)
	if err != nil {
		t.Fatal(err)
	}
	if version != "3.13.2" {
		t.Fatalf("version = %q", version)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	name := pythonResolverContainerName(workspace.HostDir)
	wantCreate := []string{
		"create", "--name", name,
		"--platform", "linux/amd64", "--pull", "never",
		"--user", "0:0", "--workdir", "/", "--read-only",
		"--network", "default", "--tmpfs", "/tmp:rw,exec,nosuid,nodev,mode=1777",
		"--mount", "type=bind,\"source=" + workspace.HostDir + "\",target=/.reploy-validation,readonly",
		"--mount", "type=bind,source=" + artifacts.InputHostDir + ",target=" + pythonResolverInputContainerDir + ",readonly",
		"--mount", "type=bind,source=" + artifacts.OutputHostDir + ",target=" + pythonResolverOutputContainerDir,
		"--entrypoint", ProbeContainerExecutable, string(descriptor.ConfigDigest), "hold",
	}
	wantInspect := []string{
		"exec", "--user", "0:0", "--workdir", "/", name,
		"/usr/bin/env", "-i", "HOME=/tmp", "LANG=C", "LC_ALL=C",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TMPDIR=/tmp",
		"/usr/bin/python3", "-I", "-S", "-c", `import sys; print(".".join(map(str, sys.version_info[:3])))`,
	}
	want := [][]string{
		wantCreate,
		{"start", name},
		{"exec", "--interactive", "--user", "0:0", "--workdir", "/", name, ProbeContainerExecutable},
		wantInspect,
		{"rm", "--force", name},
	}
	if len(*commands) != len(want) {
		t.Fatalf("commands = %#v", *commands)
	}
	for index := range want {
		if !reflect.DeepEqual((*commands)[index].Args, want[index]) {
			t.Fatalf("command %d = %#v, want %#v", index, (*commands)[index].Args, want[index])
		}
	}
	if !bytes.Equal(*probeInput, mustCanonicalProbeRequest(t, request)) {
		t.Fatalf("probe input = %q", *probeInput)
	}
}

func TestPythonResolverSessionRejectsWrongTypedRolesBeforeExec(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	commands, _ := stubPythonResolverCommands(t, nil, nil, nil)
	session, err := OpenPythonResolverSession(context.Background(), descriptor, workspace, testPreparedPythonResolverArtifacts(t))
	if err != nil {
		t.Fatal(err)
	}
	launcher := rendererExecutable("cleanenv", providers.ExecutableRoleCarrier, "/usr/bin/env")
	interpreter := rendererExecutable("interpreter", providers.ExecutableRoleSelectedOutput, "/usr/bin/python3")
	if _, err := session.InspectInterpreter(context.Background(), launcher, interpreter); err == nil || !strings.Contains(err.Error(), "environment-launcher") {
		t.Fatalf("wrong launcher error = %v", err)
	}
	if len(*commands) != 2 {
		t.Fatalf("invalid typed input reached Docker: %#v", *commands)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPythonResolverSessionRequiresSameContainerProbeBeforeInspection(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	commands, _ := stubPythonResolverCommands(t, nil, nil, nil)
	session, err := OpenPythonResolverSession(context.Background(), descriptor, workspace, testPreparedPythonResolverArtifacts(t))
	if err != nil {
		t.Fatal(err)
	}
	launcher := rendererExecutable("cleanenv", providers.ExecutableRoleEnvironmentLauncher, "/usr/bin/env")
	interpreter := rendererExecutable("interpreter", providers.ExecutableRoleSelectedOutput, "/usr/bin/python3")
	if _, err := session.InspectInterpreter(context.Background(), launcher, interpreter); err == nil || !strings.Contains(err.Error(), "was not probed in this container") {
		t.Fatalf("unprobed inspection error = %v", err)
	}
	if len(*commands) != 2 {
		t.Fatalf("unprobed inspection reached Docker: %#v", *commands)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPythonResolverSessionRejectsBindingUnprobedExecutable(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	commands, _ := stubPythonResolverCommands(t, nil, nil, nil)
	session, err := OpenPythonResolverSession(context.Background(), descriptor, workspace, testPreparedPythonResolverArtifacts(t))
	if err != nil {
		t.Fatal(err)
	}
	requirement := providers.ExecutableRequirement{
		ID: "interpreter", Command: "python", Supplier: "base", ValidationPolicy: providers.ValidationPolicyCompatible,
	}
	_, err = session.ValidatedExecutableInput(
		providers.ExecutableRoleSelectedOutput,
		requirement,
		providers.QualifiedOutput{Component: "base", Name: "python"},
		providers.CanonicalProviderData{Schema: "resolver-session-test-v1", Value: canonical.Object{}},
	)
	if err == nil || !strings.Contains(err.Error(), "was not probed in this container") {
		t.Fatalf("unprobed binding error = %v", err)
	}
	if len(*commands) != 2 {
		t.Fatalf("unprobed binding reached Docker: %#v", *commands)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOpenPythonResolverSessionCleansFailedStart(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/arm64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	commands, _ := stubPythonResolverCommands(t, nil, nil, errors.New("start failed"))
	_, err := OpenPythonResolverSession(context.Background(), descriptor, workspace, testPreparedPythonResolverArtifacts(t))
	if err == nil || !strings.Contains(err.Error(), "binfmt/QEMU") {
		t.Fatalf("start error = %v", err)
	}
	if len(*commands) != 3 || (*commands)[2].Args[0] != "rm" {
		t.Fatalf("failed start commands = %#v", *commands)
	}
}

func TestOpenPythonResolverSessionFinishesCreateBeforeHonoringCancellation(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	artifacts := testPreparedPythonResolverArtifacts(t)
	previousBind := bindPythonResolverCommandRunner
	t.Cleanup(func() { bindPythonResolverCommandRunner = previousBind })
	ctx, cancel := context.WithCancel(context.Background())
	removed := false
	bindPythonResolverCommandRunner = func(bindCtx context.Context, _ CommandSpec, _ time.Duration) (commandRunner, error) {
		if err := bindCtx.Err(); err != nil {
			t.Fatalf("Docker endpoint binding inherited cancellation: %v", err)
		}
		return func(spec CommandSpec, options RunOptions) error {
			if len(spec.Args) != 0 && spec.Args[0] == "create" {
				cancel()
				if err := options.Context.Err(); err != nil {
					t.Fatalf("Docker create inherited cancellation: %v", err)
				}
				return nil
			}
			if len(spec.Args) == 3 && spec.Args[0] == "rm" && spec.Args[1] == "--force" {
				removed = true
				return nil
			}
			t.Fatalf("unexpected follow-up command: %#v", spec)
			return nil
		}, nil
	}
	if _, err := OpenPythonResolverSession(ctx, descriptor, workspace, artifacts); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if !removed {
		t.Fatal("created container was not removed after cancellation")
	}
}

func TestOpenPythonResolverSessionRejectsNonemptyOutputBeforeDocker(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	artifacts := testPreparedPythonResolverArtifacts(t)
	if err := os.WriteFile(filepath.Join(artifacts.OutputHostDir, "unexpected.whl"), []byte("unexpected"), 0o600); err != nil {
		t.Fatal(err)
	}
	commands, _ := stubPythonResolverCommands(t, nil, nil, nil)
	_, err := OpenPythonResolverSession(context.Background(), descriptor, workspace, artifacts)
	if err == nil || !strings.Contains(err.Error(), "initially empty") {
		t.Fatalf("error = %v", err)
	}
	if len(*commands) != 0 {
		t.Fatalf("nonempty resolver output reached Docker: %#v", *commands)
	}
}

func TestPythonResolverSessionBuildsSdistThenWheelWithSelectedInterpreterAndPinnedUV(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	artifacts := testPreparedPythonResolverArtifacts(t)
	request, responseRecord := pythonResolverProbeExchange()
	commands, _ := stubPythonResolverCommands(t, mustCanonicalProbeResponse(t, responseRecord), []byte("3.13.2\n"), nil)
	session, err := OpenPythonResolverSession(context.Background(), descriptor, workspace, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Probe(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	launcher := pythonResolverSessionInput(t, session, responseRecord.Observations[0], providers.ExecutableRoleEnvironmentLauncher)
	requirement := providers.ExecutableRequirement{
		ID: "interpreter", Command: "interpreter", Supplier: "base", ValidationPolicy: providers.ValidationPolicyCompatible,
	}
	interpreter, _, err := session.InspectAndBindInterpreter(
		context.Background(), launcher, requirement, providers.QualifiedOutput{Component: "base", Name: "interpreter"},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshots := stageSourceWheelTestSnapshot(t, artifacts, "demo-pkg")
	if err := session.BuildSourceDistributions(context.Background(), launcher, requirement, interpreter.Evidence, snapshots); err != nil {
		t.Fatal(err)
	}
	successfulFollowup := session.runDocker
	session.runDocker = func(spec CommandSpec, options RunOptions) error {
		if containsInOrder(spec.Args, []string{"-m", "uv", "build", "--no-progress", "--sdist"}) {
			_, _ = options.Stderr.Write([]byte("backend requires repository metadata\n"))
			return errors.New("exit status 1")
		}
		return successfulFollowup(spec, options)
	}
	if err := session.BuildSourceDistributions(
		context.Background(), launcher, requirement, interpreter.Evidence, snapshots,
	); err == nil || !strings.Contains(err.Error(), "ordinary source tree without VCS metadata") {
		t.Fatalf("source distribution failure = %v", err)
	}
	session.runDocker = successfulFollowup
	buildEnvironmentDigest, err := session.SourceBuildEnvironmentDigest(interpreter.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	if err := buildEnvironmentDigest.Validate(); err != nil {
		t.Fatal(err)
	}
	changedUpstream := *session
	changedUpstream.descriptor.ConfigDigest = canonical.Digest("sha256:" + strings.Repeat("f", 64))
	changedUpstream.descriptor.ImmutableReference = string(changedUpstream.descriptor.ConfigDigest)
	changedUpstreamDigest, err := changedUpstream.SourceBuildEnvironmentDigest(interpreter.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	if changedUpstreamDigest == buildEnvironmentDigest {
		t.Fatal("upstream image change did not change the Python source build environment digest")
	}
	changedInterpreter := interpreter.Evidence
	changedInterpreter.Terminal.SHA256 = canonical.Digest("sha256:" + strings.Repeat("e", 64))
	changedInterpreterDigest, err := session.SourceBuildEnvironmentDigest(changedInterpreter)
	if err != nil {
		t.Fatal(err)
	}
	if changedInterpreterDigest == buildEnvironmentDigest {
		t.Fatal("interpreter evidence change did not change the Python source build environment digest")
	}
	distributions := stageSourceWheelTestDistributionFromSnapshot(t, artifacts, snapshots[0], "1.0")
	distributions[0].BuildEnvironmentDigest = buildEnvironmentDigest
	if err := session.BuildSourceWheels(context.Background(), launcher, requirement, interpreter.Evidence, distributions); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	var sourceCopyCommand, distributionCopyCommand, bootstrapCommand, sdistCommand, wheelCommand, cleanupCommand []string
	for _, command := range *commands {
		switch {
		case containsInOrder(command.Args, []string{"cp", "-a", snapshots[0].ContainerDir + "/.", pythonSourceBuildRoot + "/demo-pkg"}):
			sourceCopyCommand = command.Args
		case containsInOrder(command.Args, []string{"cp", "-a", distributions[0].ContainerDir + "/.", pythonSourceBuildRoot + "/demo-pkg"}):
			distributionCopyCommand = command.Args
		case containsInOrder(command.Args, []string{"-m", "pip", "--disable-pip-version-check", "install"}):
			bootstrapCommand = command.Args
		case containsInOrder(command.Args, []string{"-m", "uv", "build", "--no-progress", "--sdist"}):
			sdistCommand = command.Args
		case containsInOrder(command.Args, []string{"-m", "uv", "build", "--no-progress", "--wheel"}):
			wheelCommand = command.Args
		case containsInOrder(command.Args, []string{"rm", "-f", artifacts.OutputContainerDir + "/.gitignore"}):
			cleanupCommand = command.Args
		}
	}
	if sourceCopyCommand == nil || distributionCopyCommand == nil {
		t.Fatalf("source snapshot was not copied: %#v", *commands)
	}
	if !containsInOrder(bootstrapCommand, []string{
		"/usr/bin/python3", "-m", "pip", "--disable-pip-version-check", "install",
	}) || !containsInOrder(bootstrapCommand, []string{
		"--find-links", artifacts.InputContainerDir, "--target", pythonSourceBuilderRoot, pythonprovider.SourceBuilderRequirementV1,
	}) {
		t.Fatalf("uv bootstrap command = %#v", bootstrapCommand)
	}
	if !containsInOrder(sdistCommand, []string{
		"/usr/bin/python3", "-m", "uv", "build", "--no-progress", "--sdist", "--no-sources",
	}) {
		t.Fatalf("sdist build command = %#v", sdistCommand)
	}
	if !containsInOrder(wheelCommand, []string{
		"PYTHONPATH=" + pythonSourceBuilderRoot, "UV_CACHE_DIR=" + pythonSourceUVCacheRoot,
		"UV_PYTHON=/usr/bin/python3", "UV_PYTHON_DOWNLOADS=never",
		"/usr/bin/python3", "-m", "uv", "build", "--no-progress", "--wheel", "--no-sources",
		"--python", "/usr/bin/python3", "--no-python-downloads",
		"--find-links", artifacts.InputContainerDir, "--out-dir", artifacts.OutputContainerDir,
		pythonSourceBuildRoot + "/demo-pkg/demo-pkg-1.0",
	}) {
		t.Fatalf("wheel build command = %#v", wheelCommand)
	}
	if cleanupCommand == nil {
		t.Fatalf("source-builder metadata was not cleared: %#v", *commands)
	}
}

func testPreparedPythonResolverArtifacts(t *testing.T) PreparedPythonResolverArtifacts {
	t.Helper()
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PreparePythonResolverArtifacts(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return prepared
}

func stubPythonResolverCommands(
	t *testing.T,
	probeResponse []byte,
	inspectionResponse []byte,
	startErr error,
) (*[]CommandSpec, *[]byte) {
	t.Helper()
	previousBind := bindPythonResolverCommandRunner
	commands := []CommandSpec{}
	probeInput := []byte(nil)
	t.Cleanup(func() { bindPythonResolverCommandRunner = previousBind })
	bindPythonResolverCommandRunner = func(context.Context, CommandSpec, time.Duration) (commandRunner, error) {
		return func(spec CommandSpec, options RunOptions) error {
			commands = append(commands, spec)
			if len(spec.Args) == 0 {
				return errors.New("empty Docker command")
			}
			if spec.Args[0] == "start" && startErr != nil {
				_, _ = options.Stderr.Write([]byte("exec format error\n"))
				return startErr
			}
			if spec.Args[0] != "exec" {
				return nil
			}
			if spec.Args[len(spec.Args)-1] == ProbeContainerExecutable {
				input, err := io.ReadAll(options.Stdin)
				if err != nil {
					return err
				}
				probeInput = input
				_, _ = options.Stdout.Write(probeResponse)
				return nil
			}
			_, _ = options.Stdout.Write(inspectionResponse)
			return nil
		}, nil
	}
	return &commands, &probeInput
}

func pythonResolverProbeExchange() (probe.RequestV1, probe.ResponseV1) {
	digest := canonical.Digest("sha256:" + strings.Repeat("d", 64))
	request := probe.RequestV1{Schema: probe.RequestSchemaV1, Inspections: []probe.ExecutableInspectionV1{
		{ID: "cleanenv", InvocationPath: "/usr/bin/env"},
		{ID: "interpreter", InvocationPath: "/usr/bin/python3"},
	}}
	environment := probe.ExecutableObservationV1{
		ID: "cleanenv", InvocationPath: "/usr/bin/env", Links: []probe.LinkObservationV1{},
		Terminal: probe.FileObservationV1{Path: "/usr/bin/env", Kind: "regular", Mode: "0755", Size: "48536", SHA256: digest, UID: "0", GID: "0"},
		Access: []probe.AccessObservationV1{
			{Path: "/", Kind: "directory", Mode: "0755", UID: "0", GID: "0"},
			{Path: "/usr", Kind: "directory", Mode: "0755", UID: "0", GID: "0"},
			{Path: "/usr/bin", Kind: "directory", Mode: "0755", UID: "0", GID: "0"},
			{Path: "/usr/bin/env", Kind: "regular", Mode: "0755", UID: "0", GID: "0"},
		},
	}
	interpreter := testExecutableObservation()
	return request, probe.ResponseV1{
		Schema: probe.ResponseSchemaV1, Observations: []probe.ExecutableObservationV1{environment, interpreter},
	}
}

func pythonResolverSessionInput(t *testing.T, session *PythonResolverSession, observation probe.ExecutableObservationV1, role string) providers.ValidatedExecutableInput {
	t.Helper()
	output := providers.QualifiedOutput{Component: "base", Name: observation.ID}
	facts := providers.CanonicalProviderData{Schema: "resolver-session-test-v1", Value: canonical.Object{}}
	requirement := providers.ExecutableRequirement{
		ID: observation.ID, Command: observation.ID, Supplier: "base", ValidationPolicy: providers.ValidationPolicyCompatible,
	}
	input, err := session.ValidatedExecutableInput(role, requirement, output, facts)
	if err != nil {
		t.Fatal(err)
	}
	return input
}
