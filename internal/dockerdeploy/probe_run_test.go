package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probe"
)

func TestRunImageProbeUsesHeldClosedDockerBoundary(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/arm/v7")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, filepath.Join(t.TempDir(), "with,comma"))
	request := probe.RequestV1{Schema: probe.RequestSchemaV1, Inspections: []probe.ExecutableInspectionV1{}}
	responseBytes := mustCanonicalProbeResponse(t, probe.ResponseV1{Schema: probe.ResponseSchemaV1, Observations: []probe.ExecutableObservationV1{}})
	restore := stubImageValidationCommands(t, responseBytes, nil)
	defer restore()
	response, err := RunImageProbe(context.Background(), descriptor, workspace, request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Schema != probe.ResponseSchemaV1 || response.Observations == nil {
		t.Fatalf("probe response = %#v", response)
	}
	wantCreate := []string{
		"create", "--name", imageProbeContainerName(workspace.HostDir),
		"--platform", "linux/arm/v7", "--pull", "never",
		"--user", "0:0", "--workdir", "/", "--read-only", "--network", "none",
		"--mount", "type=bind,\"source=" + workspace.HostDir + "\",target=/.reploy-validation,readonly",
		"--entrypoint", ProbeContainerExecutable, descriptor.ImmutableReference, "hold",
	}
	if !reflect.DeepEqual(recordedImageValidationCommands[0].Args, wantCreate) {
		t.Fatalf("create command = %#v", recordedImageValidationCommands[0])
	}
	wantFollowups := [][]string{
		{"start", imageProbeContainerName(workspace.HostDir)},
		{"exec", "--interactive", "--user", "0:0", "--workdir", "/", imageProbeContainerName(workspace.HostDir), ProbeContainerExecutable},
		{"rm", "--force", imageProbeContainerName(workspace.HostDir)},
	}
	if len(recordedImageValidationCommands) != 4 {
		t.Fatalf("validation commands = %#v", recordedImageValidationCommands)
	}
	for index, want := range wantFollowups {
		if !reflect.DeepEqual(recordedImageValidationCommands[index+1].Args, want) {
			t.Fatalf("follow-up %d = %#v", index, recordedImageValidationCommands[index+1])
		}
	}
	if !bytes.Equal(recordedImageValidationStdin, mustCanonicalProbeRequest(t, request)) {
		t.Fatalf("probe stdin = %q", recordedImageValidationStdin)
	}
}

func TestImageValidationSessionSupportsSeveralFixedProbes(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	responseBytes := mustCanonicalProbeResponse(t, probe.ResponseV1{Schema: probe.ResponseSchemaV1, Observations: []probe.ExecutableObservationV1{}})
	restore := stubImageValidationCommands(t, responseBytes, nil)
	defer restore()
	session, err := OpenImageValidationSession(context.Background(), descriptor, workspace)
	if err != nil {
		t.Fatal(err)
	}
	request := probe.RequestV1{Schema: probe.RequestSchemaV1, Inspections: []probe.ExecutableInspectionV1{}}
	for range 2 {
		if _, err := session.Probe(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("repeat close: %v", err)
	}
	if len(recordedImageValidationCommands) != 5 {
		t.Fatalf("commands = %#v", recordedImageValidationCommands)
	}
	execCount := 0
	removeCount := 0
	for _, command := range recordedImageValidationCommands {
		if len(command.Args) > 0 && command.Args[0] == "exec" {
			execCount++
		}
		if len(command.Args) > 0 && command.Args[0] == "rm" {
			removeCount++
		}
	}
	if execCount != 2 || removeCount != 1 {
		t.Fatalf("exec=%d remove=%d commands=%#v", execCount, removeCount, recordedImageValidationCommands)
	}
}

func TestAPTImageValidationSessionAddsScratchMountWithoutNetwork(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	probeWorkspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	aptWorkspace := testPreparedAPTResolverWorkspace(t)
	spec, _, err := imageValidationCreateCommandSpecWithAPT(descriptor, probeWorkspace, &aptWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	wantProbeMount := "type=bind,source=" + probeWorkspace.HostDir + ",target=" + probeWorkspace.ContainerDir + ",readonly"
	wantAPTMount := "type=bind,source=" + aptWorkspace.HostDir + ",target=" + aptWorkspace.ContainerDir
	joined := strings.Join(spec.Args, "\n")
	if !strings.Contains(joined, wantProbeMount) || !strings.Contains(joined, wantAPTMount) {
		t.Fatalf("create args do not contain both mounts: %#v", spec.Args)
	}
	if !containsAdjacentArguments(spec.Args, "--network", "none") {
		t.Fatalf("APT validation container is not networkless: %#v", spec.Args)
	}
}

func TestAPTImageValidationSessionReusesHeldContainerAndCachesProfile(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	probeWorkspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	aptWorkspace := testPreparedAPTResolverWorkspace(t)
	profileOutputs := [][]byte{
		[]byte("ID\x00debian\x00VERSION_ID\x0013\x00"),
		[]byte("apt 3.0.3 (amd64)\n"),
		[]byte("Debian 'dpkg' package management program version 1.22.21 (amd64).\n"),
		[]byte("Debian 'dpkg-deb' package archive backend version 1.22.21 (amd64).\n"),
		[]byte("Debian dpkg-query package management program query tool version 1.22.21 (amd64).\n"),
		[]byte("sha256sum (GNU coreutils) 9.5\n"),
		[]byte("amd64\n"),
		{},
	}
	previousOpen := runImageValidationOpenCommand
	previousFollowup := runImageValidationFollowupCommand
	t.Cleanup(func() {
		runImageValidationOpenCommand = previousOpen
		runImageValidationFollowupCommand = previousFollowup
	})
	commands := []CommandSpec{}
	profileIndex := 0
	runImageValidationOpenCommand = func(spec CommandSpec, _ RunOptions) error {
		commands = append(commands, spec)
		return nil
	}
	runImageValidationFollowupCommand = func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, spec)
		if len(spec.Args) == 0 || spec.Args[0] != "exec" {
			return nil
		}
		if spec.Args[len(spec.Args)-1] == ProbeContainerExecutable {
			if _, err := io.ReadAll(options.Stdin); err != nil {
				return err
			}
			_, _ = options.Stdout.Write(mustCanonicalProbeResponse(t, aptBaseProbeResponse()))
			return nil
		}
		if profileIndex >= len(profileOutputs) {
			t.Fatalf("unexpected profile command: %#v", spec.Args)
		}
		_, _ = options.Stdout.Write(profileOutputs[profileIndex])
		profileIndex++
		return nil
	}
	session, err := OpenAPTImageValidationSession(context.Background(), descriptor, probeWorkspace, aptWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	first, err := session.ProbeAPTBaseProfile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	commandCount := len(commands)
	first.Executables[0].Evidence.Access.Paths[0].Mode = "0000"
	second, err := session.ProbeAPTBaseProfile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Executables[0].Evidence.Access.Paths[0].Mode != "0755" || len(commands) != commandCount {
		t.Fatalf("cached profile = %#v, commands = %#v", second, commands)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if profileIndex != len(profileOutputs) || len(commands) != 12 {
		t.Fatalf("profile commands=%d all commands=%#v", profileIndex, commands)
	}
}

func containsAdjacentArguments(arguments []string, first string, second string) bool {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == first && arguments[index+1] == second {
			return true
		}
	}
	return false
}

func TestImageValidationSessionDPKGOwnerQueryIsFixedAndLiteral(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	restore := stubImageValidationCommands(t, []byte("dash: /bin/sh\n"), nil)
	defer restore()
	session, err := OpenImageValidationSession(context.Background(), descriptor, workspace)
	if err != nil {
		t.Fatal(err)
	}
	output, err := session.QueryDPKGOwners(context.Background(), []string{"/bin/sh"})
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "dash: /bin/sh\n" {
		t.Fatalf("owner output = %q", output)
	}
	want := []string{
		"exec", "--user", "0:0", "--workdir", "/", imageProbeContainerName(workspace.HostDir),
		"/usr/bin/dpkg-query", "--search", "/bin/sh",
	}
	if !reflect.DeepEqual(recordedImageValidationCommands[2].Args, want) {
		t.Fatalf("dpkg owner command = %#v", recordedImageValidationCommands[2])
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestImageValidationSessionDPKGPackageStateQueryIsFixedAndLiteral(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	restore := stubImageValidationCommands(t, []byte("libc6:amd64\t2.39\tamd64\tinstall ok installed\n"), nil)
	defer restore()
	session, err := OpenImageValidationSession(context.Background(), descriptor, workspace)
	if err != nil {
		t.Fatal(err)
	}
	output, err := session.QueryDPKGPackageState(context.Background(), []string{"libc6"})
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "libc6:amd64\t2.39\tamd64\tinstall ok installed\n" {
		t.Fatalf("package-state output = %q", output)
	}
	want := []string{
		"exec", "--user", "0:0", "--workdir", "/", imageProbeContainerName(workspace.HostDir),
		"/usr/bin/dpkg-query", "--show", "--showformat=${binary:Package}\t${Version}\t${Architecture}\t${Status}\n", "libc6",
	}
	if !reflect.DeepEqual(recordedImageValidationCommands[2].Args, want) {
		t.Fatalf("dpkg package-state command = %#v", recordedImageValidationCommands[2])
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestImageValidationSessionAlternativeQueryIsFixedAndReadOnly(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	restore := stubImageValidationCommands(t, []byte("Name: java\nLink: /usr/bin/java\nValue: /usr/lib/jvm/java/bin/java\n"), nil)
	defer restore()
	session, err := OpenImageValidationSession(context.Background(), descriptor, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.QueryAlternative(context.Background(), "java"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"exec", "--user", "0:0", "--workdir", "/", imageProbeContainerName(workspace.HostDir),
		"/usr/bin/update-alternatives", "--query", "java",
	}
	if !reflect.DeepEqual(recordedImageValidationCommands[2].Args, want) {
		t.Fatalf("alternatives command = %#v", recordedImageValidationCommands[2])
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestImageValidationSessionBuildScratchAbsenceCheckIsFixed(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	restore := stubImageValidationCommands(t, nil, nil)
	defer restore()
	session, err := OpenImageValidationSession(context.Background(), descriptor, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.ValidateBuildScratchAbsent(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"exec", "--user", "0:0", "--workdir", "/", imageProbeContainerName(workspace.HostDir),
		"/bin/sh", "-c", `test ! -e "$1"`, "reploy-validation", "/.reploy-build",
	}
	if !reflect.DeepEqual(recordedImageValidationCommands[2].Args, want) {
		t.Fatalf("build-scratch command = %#v", recordedImageValidationCommands[2])
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOpenImageValidationSessionCleansFailedStartAndExplainsEmulation(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/arm64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	startErr := errors.New("exit status 1")
	restore := stubImageValidationCommands(t, nil, startErr)
	defer restore()
	_, err := OpenImageValidationSession(context.Background(), descriptor, workspace)
	if err == nil || !strings.Contains(err.Error(), "binfmt/QEMU") || !strings.Contains(err.Error(), "linux/arm64") {
		t.Fatalf("open error = %v", err)
	}
	if len(recordedImageValidationCommands) != 3 || recordedImageValidationCommands[2].Args[0] != "rm" {
		t.Fatalf("failed start cleanup commands = %#v", recordedImageValidationCommands)
	}
}

func TestRunImageProbeRejectsMismatchedPlatformBeforeDocker(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	other, err := blueprint.ParsePlatform("linux/arm64")
	if err != nil {
		t.Fatal(err)
	}
	workspace := testPreparedProbeWorkspace(t, other, t.TempDir())
	previous := runImageValidationOpenCommand
	t.Cleanup(func() { runImageValidationOpenCommand = previous })
	runImageValidationOpenCommand = func(CommandSpec, RunOptions) error {
		t.Fatal("mismatched platform reached Docker")
		return nil
	}
	request := probe.RequestV1{Schema: probe.RequestSchemaV1, Inspections: []probe.ExecutableInspectionV1{}}
	if _, err := RunImageProbe(context.Background(), descriptor, workspace, request); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("platform mismatch error = %v", err)
	}
}

var recordedImageValidationCommands []CommandSpec
var recordedImageValidationStdin []byte

func stubImageValidationCommands(t *testing.T, response []byte, startErr error) func() {
	t.Helper()
	previousOpen := runImageValidationOpenCommand
	previousFollowup := runImageValidationFollowupCommand
	recordedImageValidationCommands = nil
	recordedImageValidationStdin = nil
	runImageValidationOpenCommand = func(spec CommandSpec, _ RunOptions) error {
		recordedImageValidationCommands = append(recordedImageValidationCommands, spec)
		return nil
	}
	runImageValidationFollowupCommand = func(spec CommandSpec, options RunOptions) error {
		recordedImageValidationCommands = append(recordedImageValidationCommands, spec)
		if len(spec.Args) == 0 {
			return errors.New("empty Docker command")
		}
		switch spec.Args[0] {
		case "start":
			if startErr != nil {
				_, _ = options.Stderr.Write([]byte("exec /.reploy-validation/reploy-probe: exec format error\n"))
				return startErr
			}
		case "exec":
			if options.Stdin != nil {
				var input bytes.Buffer
				if _, err := input.ReadFrom(options.Stdin); err != nil {
					return err
				}
				recordedImageValidationStdin = append(recordedImageValidationStdin, input.Bytes()...)
			}
			if _, err := options.Stdout.Write(response); err != nil {
				return err
			}
		}
		return nil
	}
	return func() {
		runImageValidationOpenCommand = previousOpen
		runImageValidationFollowupCommand = previousFollowup
		recordedImageValidationCommands = nil
		recordedImageValidationStdin = nil
	}
}

func testProbeImageDescriptor(t *testing.T, platformValue string) deploy.ImageDescriptor {
	t.Helper()
	platform, err := blueprint.ParsePlatform(platformValue)
	if err != nil {
		t.Fatal(err)
	}
	digest := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	return deploy.ImageDescriptor{
		Schema: deploy.ImageDescriptorSchemaV1, Platform: platform,
		AuthorReference: string(digest), ImmutableReference: string(digest), ConfigDigest: digest,
		RootFSDiffIDs: []canonical.Digest{canonical.Digest("sha256:" + strings.Repeat("b", 64))},
	}
}

func testPreparedProbeWorkspace(t *testing.T, platform blueprint.Platform, hostDir string) PreparedProbeWorkspace {
	t.Helper()
	return PreparedProbeWorkspace{
		HostDir: hostDir, HostExecutable: filepath.Join(hostDir, "reploy-probe"),
		ContainerDir: ProbeContainerRoot, ContainerExecutable: ProbeContainerExecutable,
		ReadOnly: true, Platform: platform, SHA256: canonical.Digest("sha256:" + strings.Repeat("c", 64)),
	}
}

func mustCanonicalProbeRequest(t *testing.T, request probe.RequestV1) []byte {
	t.Helper()
	encoded, err := canonical.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func mustCanonicalProbeResponse(t *testing.T, response probe.ResponseV1) []byte {
	t.Helper()
	encoded, err := canonical.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
