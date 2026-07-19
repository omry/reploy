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

	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	"github.com/omry/reploy/internal/providerstore"
)

func TestAPTResolverSessionCollectsBaseEvidenceBeforeNetworkWork(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	probeWorkspace := testPreparedProbeWorkspace(t, descriptor.Platform, filepath.Join(t.TempDir(), "probe,workspace"))
	resolverWorkspace := testPreparedAPTResolverWorkspace(t)
	response := aptBaseProbeResponse()
	outputs := [][]byte{
		[]byte("ID\x00debian\x00VERSION_ID\x0013\x00"),
		[]byte("apt 3.0.3 (amd64)\nSupported modules:\n* standard .deb\n"),
		[]byte("Debian 'dpkg' package management program version 1.22.21 (amd64).\n"),
		[]byte("Debian 'dpkg-deb' package archive backend version 1.22.21 (amd64).\n"),
		[]byte("Debian dpkg-query package management program query tool version 1.22.21 (amd64).\n"),
		[]byte("amd64\n"),
		{},
	}
	commands, probeInput := stubAPTResolverCommands(t, mustCanonicalProbeResponse(t, response), outputs, nil)

	session, err := OpenAPTResolverSession(context.Background(), descriptor, probeWorkspace, resolverWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	validation, err := session.ProbeBaseProfile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if validation.Profile.MatchedBy != "id" || validation.Profile.NativeArchitecture != "amd64" || len(validation.Profile.ForeignArchitectures) != 0 {
		t.Fatalf("profile = %#v", validation.Profile)
	}
	if len(validation.Executables) != 6 {
		t.Fatalf("executables = %#v", validation.Executables)
	}
	roles := map[string]string{}
	for _, executable := range validation.Executables {
		roles[executable.ID] = executable.Role
	}
	if roles["sh"] != providers.ExecutableRoleCarrier || roles["env"] != providers.ExecutableRoleEnvironmentLauncher || roles["apt_get"] != providers.ExecutableRoleProviderPrerequisite {
		t.Fatalf("roles = %#v", roles)
	}
	commandCount := len(*commands)
	validation.Executables[0].Evidence.Access.Paths[0].Mode = "0000"
	second, err := session.ProbeBaseProfile(context.Background())
	if err != nil || second.Executables[0].Evidence.Access.Paths[0].Mode != "0755" || len(*commands) != commandCount {
		t.Fatalf("cached validation = %#v, err = %v, commands = %d", second, err, len(*commands))
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	name := aptResolverContainerName(resolverWorkspace.HostDir)
	wantCreate := []string{
		"create", "--name", name,
		"--platform", "linux/amd64", "--pull", "never",
		"--user", "0:0", "--workdir", "/", "--read-only", "--network", "default",
		"--mount", "type=bind,\"source=" + probeWorkspace.HostDir + "\",target=/.reploy-validation,readonly",
		"--mount", "type=bind,source=" + resolverWorkspace.HostDir + ",target=" + aptprovider.ResolverScratchDirectory,
		"--entrypoint", ProbeContainerExecutable, descriptor.ImmutableReference, "hold",
	}
	if !reflect.DeepEqual((*commands)[0].Args, wantCreate) {
		t.Fatalf("create = %#v, want %#v", (*commands)[0].Args, wantCreate)
	}
	if (*commands)[2].Args[len((*commands)[2].Args)-1] != ProbeContainerExecutable {
		t.Fatalf("first resolver operation was not the fixed probe: %#v", (*commands)[2])
	}
	for _, command := range (*commands)[3 : len(*commands)-1] {
		joined := strings.Join(command.Args, "\x00")
		if !strings.Contains(joined, "/usr/bin/env\x00-i\x00") || strings.Contains(joined, "apt-get\x00update") {
			t.Fatalf("unexpected profile command = %#v", command.Args)
		}
	}
	request := aptBaseProbeRequest()
	if !bytes.Equal(*probeInput, mustCanonicalProbeRequest(t, request)) {
		t.Fatalf("probe input = %q", *probeInput)
	}
}

func TestAPTResolverSessionRejectsForeignArchitecture(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	probeWorkspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	resolverWorkspace := testPreparedAPTResolverWorkspace(t)
	outputs := [][]byte{
		[]byte("ID\x00ubuntu\x00VERSION_ID\x0024.04\x00"),
		[]byte("apt 2.7.14 (amd64)\n"), []byte("dpkg 1\n"), []byte("dpkg-deb 1\n"), []byte("dpkg-query 1\n"),
		[]byte("amd64\n"), []byte("i386\n"),
	}
	commands, _ := stubAPTResolverCommands(t, mustCanonicalProbeResponse(t, aptBaseProbeResponse()), outputs, nil)
	session, err := OpenAPTResolverSession(context.Background(), descriptor, probeWorkspace, resolverWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.ProbeBaseProfile(context.Background()); err == nil || !strings.Contains(err.Error(), "unsupported foreign") {
		t.Fatalf("err = %v", err)
	}
	if len(*commands) != 10 {
		t.Fatalf("commands = %#v", *commands)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAPTResolverSessionRejectsAmbiguousNativeArchitecture(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	probeWorkspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	resolverWorkspace := testPreparedAPTResolverWorkspace(t)
	outputs := [][]byte{
		[]byte("ID\x00debian\x00VERSION_ID\x0013\x00"),
		[]byte("apt 3.0.3 (amd64)\n"), []byte("dpkg 1\n"), []byte("dpkg-deb 1\n"), []byte("dpkg-query 1\n"),
		[]byte("amd64\narm64\n"),
	}
	stubAPTResolverCommands(t, mustCanonicalProbeResponse(t, aptBaseProbeResponse()), outputs, nil)
	session, err := OpenAPTResolverSession(context.Background(), descriptor, probeWorkspace, resolverWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.ProbeBaseProfile(context.Background()); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("err = %v", err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAPTResolverSessionCleansFailedStart(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/arm64")
	probeWorkspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	resolverWorkspace := testPreparedAPTResolverWorkspace(t)
	commands, _ := stubAPTResolverCommands(t, nil, nil, errors.New("exec format error"))
	_, err := OpenAPTResolverSession(context.Background(), descriptor, probeWorkspace, resolverWorkspace)
	if err == nil || !strings.Contains(err.Error(), "binfmt/QEMU") {
		t.Fatalf("err = %v", err)
	}
	if len(*commands) != 3 || (*commands)[2].Args[0] != "rm" {
		t.Fatalf("commands = %#v", *commands)
	}
}

func TestOpenAPTResolverSessionRejectsNonemptyScratchBeforeDocker(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	probeWorkspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	resolverWorkspace := testPreparedAPTResolverWorkspace(t)
	if err := os.WriteFile(filepath.Join(resolverWorkspace.HostDir, "unexpected"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	commands, _ := stubAPTResolverCommands(t, nil, nil, nil)
	_, err := OpenAPTResolverSession(context.Background(), descriptor, probeWorkspace, resolverWorkspace)
	if err == nil || !strings.Contains(err.Error(), "initially empty") {
		t.Fatalf("err = %v", err)
	}
	if len(*commands) != 0 {
		t.Fatalf("invalid scratch reached Docker: %#v", *commands)
	}
}

func testPreparedAPTResolverWorkspace(t *testing.T) PreparedAPTResolverWorkspace {
	t.Helper()
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PrepareAPTResolverWorkspace(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return prepared
}

func aptBaseProbeRequest() probe.RequestV1 {
	tools := aptprovider.RequiredBaseToolsV1()
	inspections := make([]probe.ExecutableInspectionV1, 0, len(tools))
	for _, tool := range tools {
		inspections = append(inspections, probe.ExecutableInspectionV1{ID: tool.Name, InvocationPath: tool.Path})
	}
	return probe.RequestV1{Schema: probe.RequestSchemaV1, Inspections: inspections}
}

func aptBaseProbeResponse() probe.ResponseV1 {
	request := aptBaseProbeRequest()
	observations := make([]probe.ExecutableObservationV1, 0, len(request.Inspections))
	for _, inspection := range request.Inspections {
		observations = append(observations, directExecutableObservation(inspection.ID, inspection.InvocationPath))
	}
	return probe.ResponseV1{Schema: probe.ResponseSchemaV1, Observations: observations}
}

func stubAPTResolverCommands(
	t *testing.T,
	probeResponse []byte,
	profileOutputs [][]byte,
	startErr error,
) (*[]CommandSpec, *[]byte) {
	t.Helper()
	previousOpen := runAPTResolverOpenCommand
	previousFollowup := runAPTResolverFollowupCommand
	commands := []CommandSpec{}
	probeInput := []byte(nil)
	profileIndex := 0
	t.Cleanup(func() {
		runAPTResolverOpenCommand = previousOpen
		runAPTResolverFollowupCommand = previousFollowup
	})
	runAPTResolverOpenCommand = func(spec CommandSpec, _ RunOptions) error {
		commands = append(commands, spec)
		return nil
	}
	runAPTResolverFollowupCommand = func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, spec)
		if len(spec.Args) != 0 && spec.Args[0] == "start" && startErr != nil {
			if options.Stderr != nil {
				_, _ = options.Stderr.Write([]byte(startErr.Error()))
			}
			return startErr
		}
		if len(spec.Args) != 0 && spec.Args[len(spec.Args)-1] == ProbeContainerExecutable {
			input, err := io.ReadAll(options.Stdin)
			if err != nil {
				return err
			}
			probeInput = input
			_, _ = options.Stdout.Write(probeResponse)
			return nil
		}
		if len(spec.Args) != 0 && spec.Args[0] == "exec" {
			if profileIndex >= len(profileOutputs) {
				return errors.New("unexpected APT profile command")
			}
			_, _ = options.Stdout.Write(profileOutputs[profileIndex])
			profileIndex++
		}
		return nil
	}
	return &commands, &probeInput
}
