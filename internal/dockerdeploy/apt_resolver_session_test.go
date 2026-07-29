package dockerdeploy

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
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
		[]byte("sha256sum (GNU coreutils) 9.5\n"),
		[]byte("amd64\n"),
		{},
		{},
		[]byte("  MarkInstall hello:amd64 < none -> 2.10-3 @un puN > FU=1\n    MarkInstall libc6:amd64 < 2.39 @ii pmK > FU=0\n"),
		[]byte("libc6:amd64\t2.39\tamd64\tinstall ok installed\n"),
		{},
	}
	commands, probeInput := stubAPTResolverCommands(t, mustCanonicalProbeResponse(t, response), outputs, nil)

	session, err := OpenAPTResolverSession(context.Background(), descriptor, probeWorkspace, resolverWorkspace, RunOptions{})
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
	if len(validation.Executables) != len(aptprovider.RequiredBaseToolsV1()) {
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
	if err := session.RefreshIndexes(context.Background()); err != nil {
		t.Fatal(err)
	}
	refreshCommandCount := len(*commands)
	if err := session.RefreshIndexes(context.Background()); err != nil || len(*commands) != refreshCommandCount {
		t.Fatalf("repeated refresh err = %v, commands = %d", err, len(*commands))
	}
	downloadRequest := aptResolverTestRequest(t, blueprint.APTPackageRequest{Name: "hello", Exports: map[string]blueprint.ExecutableExport{}})
	plan, err := session.PlanPackages(context.Background(), downloadRequest)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Packages) != 2 || plan.Packages[0].Name != "hello" || plan.Packages[1].Name != "libc6" {
		t.Fatalf("plan = %#v", plan)
	}
	plan.Packages[0].Name = "changed"
	secondPlan, err := session.PlanPackages(context.Background(), downloadRequest)
	if err != nil || secondPlan.Packages[0].Name != "hello" {
		t.Fatalf("cached plan = %#v, err = %v", secondPlan, err)
	}
	differentIdentity := aptResolverTestRequest(t, blueprint.APTPackageRequest{Name: "hello", Exports: map[string]blueprint.ExecutableExport{"hello": {Executable: "/usr/bin/hello"}}})
	commandCount = len(*commands)
	if _, err := session.PlanPackages(context.Background(), differentIdentity); err == nil || !strings.Contains(err.Error(), "different canonical request") || len(*commands) != commandCount {
		t.Fatalf("different request err = %v, commands = %d", err, len(*commands))
	}
	baseState, err := session.ReadBasePackageState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(baseState) != 1 || baseState[0].Name != "libc6" || baseState[0].Status != aptprovider.InstalledPackageStatusV1 {
		t.Fatalf("base state = %#v", baseState)
	}
	baseState[0].Name = "changed"
	secondBaseState, err := session.ReadBasePackageState(context.Background())
	if err != nil || secondBaseState[0].Name != "libc6" {
		t.Fatalf("cached base state = %#v, err = %v", secondBaseState, err)
	}
	if err := session.DownloadPackages(context.Background(), downloadRequest); err != nil {
		t.Fatal(err)
	}
	inventory, err := session.InventoryArchives(context.Background())
	if err != nil || len(inventory) != 0 {
		t.Fatalf("inventory = %#v, err = %v", inventory, err)
	}
	downloadCommandCount := len(*commands)
	if err := session.DownloadPackages(context.Background(), downloadRequest); err != nil || len(*commands) != downloadCommandCount {
		t.Fatalf("repeated download err = %v, commands = %d", err, len(*commands))
	}
	differentRequest := aptResolverTestRequest(t, blueprint.APTPackageRequest{Name: "curl", Exports: map[string]blueprint.ExecutableExport{}})
	if err := session.DownloadPackages(context.Background(), differentRequest); err == nil || !strings.Contains(err.Error(), "different canonical request") || len(*commands) != downloadCommandCount {
		t.Fatalf("different download err = %v, commands = %d", err, len(*commands))
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
		"--entrypoint", ProbeContainerExecutable, string(descriptor.ConfigDigest), "hold",
	}
	if !reflect.DeepEqual((*commands)[0].Args, wantCreate) {
		t.Fatalf("create = %#v, want %#v", (*commands)[0].Args, wantCreate)
	}
	if (*commands)[2].Args[len((*commands)[2].Args)-1] != ProbeContainerExecutable {
		t.Fatalf("first resolver operation was not the fixed probe: %#v", (*commands)[2])
	}
	for _, command := range (*commands)[3 : len(*commands)-5] {
		joined := strings.Join(command.Args, "\x00")
		if !strings.Contains(joined, "/usr/bin/env\x00-i\x00APT_CONFIG=/tmp/reploy-apt-resolve/apt.conf\x00") ||
			!strings.Contains(joined, "\x00/bin/sh\x00-c\x00exec </dev/null; umask \"$1\"; shift; exec \"$@\"\x00apt-resolve-v1\x000022\x00") ||
			strings.Contains(joined, "apt-get\x00update") {
			t.Fatalf("unexpected profile command = %#v", command.Args)
		}
	}
	refresh := (*commands)[len(*commands)-5].Args
	updateArgv := aptprovider.ResolveUpdateArgvV1()
	if len(refresh) < len(updateArgv) || !reflect.DeepEqual(refresh[len(refresh)-len(updateArgv):], updateArgv) {
		t.Fatalf("refresh command = %#v", refresh)
	}
	planCommand := (*commands)[len(*commands)-4].Args
	planArgv := append(aptprovider.ResolvePlanPrefixArgvV1(), "hello")
	if len(planCommand) < len(planArgv) || !reflect.DeepEqual(planCommand[len(planCommand)-len(planArgv):], planArgv) {
		t.Fatalf("plan command = %#v", planCommand)
	}
	baseStateCommand := (*commands)[len(*commands)-3].Args
	baseStateArgv := append(aptprovider.ResolveBaseStatePrefixArgvV1(), "libc6")
	if len(baseStateCommand) < len(baseStateArgv) || !reflect.DeepEqual(baseStateCommand[len(baseStateCommand)-len(baseStateArgv):], baseStateArgv) {
		t.Fatalf("base state command = %#v", baseStateCommand)
	}
	download := (*commands)[len(*commands)-2].Args
	downloadArgv := append(aptprovider.ResolveDownloadPrefixArgvV1(), "hello")
	if len(download) < len(downloadArgv) || !reflect.DeepEqual(download[len(download)-len(downloadArgv):], downloadArgv) {
		t.Fatalf("download command = %#v", download)
	}
	request := aptBaseProbeRequest()
	if !bytes.Equal(*probeInput, mustCanonicalProbeRequest(t, request)) {
		t.Fatalf("probe input = %q", *probeInput)
	}
}

func TestAPTResolverPlanRequiresRefreshAndDownloadRequiresPlan(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	probeWorkspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	resolverWorkspace := testPreparedAPTResolverWorkspace(t)
	outputs := [][]byte{
		[]byte("ID\x00debian\x00VERSION_ID\x0013\x00"),
		[]byte("apt 3.0.3 (amd64)\n"), []byte("dpkg 1\n"), []byte("dpkg-deb 1\n"), []byte("dpkg-query 1\n"),
		[]byte("sha256sum 1\n"), []byte("amd64\n"), {}, {},
	}
	commands, _ := stubAPTResolverCommands(t, mustCanonicalProbeResponse(t, aptBaseProbeResponse()), outputs, nil)
	session, err := OpenAPTResolverSession(context.Background(), descriptor, probeWorkspace, resolverWorkspace, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.ProbeBaseProfile(context.Background()); err != nil {
		t.Fatal(err)
	}
	request := aptResolverTestRequest(t, blueprint.APTPackageRequest{Name: "hello", Exports: map[string]blueprint.ExecutableExport{}})
	commandCount := len(*commands)
	if _, err := session.PlanPackages(context.Background(), request); err == nil || !strings.Contains(err.Error(), "successful index refresh") || len(*commands) != commandCount {
		t.Fatalf("plan err = %v, commands = %d", err, len(*commands))
	}
	if err := session.RefreshIndexes(context.Background()); err != nil {
		t.Fatal(err)
	}
	commandCount = len(*commands)
	if err := session.DownloadPackages(context.Background(), request); err == nil || !strings.Contains(err.Error(), "successful resolution plan") || len(*commands) != commandCount {
		t.Fatalf("download err = %v, commands = %d", err, len(*commands))
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAPTResolverDownloadRequiresVerifiedBaseState(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	probeWorkspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	resolverWorkspace := testPreparedAPTResolverWorkspace(t)
	outputs := [][]byte{
		[]byte("ID\x00debian\x00VERSION_ID\x0013\x00"),
		[]byte("apt 3.0.3 (amd64)\n"), []byte("dpkg 1\n"), []byte("dpkg-deb 1\n"), []byte("dpkg-query 1\n"),
		[]byte("sha256sum 1\n"), []byte("amd64\n"), {}, {},
		[]byte("  MarkInstall hello:amd64 < none -> 2.10 @un puN > FU=1\n"),
		{},
	}
	commands, _ := stubAPTResolverCommands(t, mustCanonicalProbeResponse(t, aptBaseProbeResponse()), outputs, nil)
	session, err := OpenAPTResolverSession(context.Background(), descriptor, probeWorkspace, resolverWorkspace, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.ProbeBaseProfile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := session.RefreshIndexes(context.Background()); err != nil {
		t.Fatal(err)
	}
	request := aptResolverTestRequest(t, blueprint.APTPackageRequest{Name: "hello", Exports: map[string]blueprint.ExecutableExport{}})
	if _, err := session.PlanPackages(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	commandCount := len(*commands)
	if err := session.DownloadPackages(context.Background(), request); err == nil || !strings.Contains(err.Error(), "verified base package state") || len(*commands) != commandCount {
		t.Fatalf("download err = %v, commands = %d", err, len(*commands))
	}
	baseState, err := session.ReadBasePackageState(context.Background())
	if err != nil || len(baseState) != 0 || len(*commands) != commandCount {
		t.Fatalf("empty base state = %#v, err = %v, commands = %d", baseState, err, len(*commands))
	}
	if err := session.DownloadPackages(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAPTResolverBaseStateCommandFailureDoesNotRetryOrAcceptPartialOutput(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	probeWorkspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	resolverWorkspace := testPreparedAPTResolverWorkspace(t)
	outputs := [][]byte{
		[]byte("ID\x00debian\x00VERSION_ID\x0013\x00"),
		[]byte("apt 3.0.3 (amd64)\n"), []byte("dpkg 1\n"), []byte("dpkg-deb 1\n"), []byte("dpkg-query 1\n"),
		[]byte("sha256sum 1\n"), []byte("amd64\n"), {}, {},
		[]byte("  MarkInstall hello:amd64 < none -> 2.10 @un puN > FU=1\n    MarkInstall libc6:amd64 < 2.39 @ii pmK > FU=0\n"),
	}
	commands, _ := stubAPTResolverCommands(t, mustCanonicalProbeResponse(t, aptBaseProbeResponse()), outputs, nil)
	session, err := OpenAPTResolverSession(context.Background(), descriptor, probeWorkspace, resolverWorkspace, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.ProbeBaseProfile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := session.RefreshIndexes(context.Background()); err != nil {
		t.Fatal(err)
	}
	request := aptResolverTestRequest(t, blueprint.APTPackageRequest{Name: "hello", Exports: map[string]blueprint.ExecutableExport{}})
	if _, err := session.PlanPackages(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	prior := runAPTResolverFollowupCommand
	runAPTResolverFollowupCommand = func(spec CommandSpec, options RunOptions) error {
		*commands = append(*commands, spec)
		_, _ = options.Stdout.Write([]byte("libc6:amd64\t2.39\tamd64\tinstall ok installed\n"))
		_, _ = options.Stderr.Write([]byte("secret partial dpkg output"))
		return syscall.E2BIG
	}
	t.Cleanup(func() { runAPTResolverFollowupCommand = prior })
	_, err = session.ReadBasePackageState(context.Background())
	if err == nil || !strings.Contains(err.Error(), "apt.resolve.base-state") || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "libc6") {
		t.Fatalf("base state err = %v", err)
	}
	commandCount := len(*commands)
	_, err = session.ReadBasePackageState(context.Background())
	if err == nil || !strings.Contains(err.Error(), "already failed") || len(*commands) != commandCount {
		t.Fatalf("retry err = %v, commands = %d", err, len(*commands))
	}
	runAPTResolverFollowupCommand = prior
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAPTResolverInspectsSelectedArchiveAndIgnoresUnrelatedUnchangedSeed(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	probeWorkspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seed, err := store.Publish(context.Background(), "debs/old_1_all.deb", "deb", strings.NewReader("old seed bytes"))
	if err != nil {
		t.Fatal(err)
	}
	resolverWorkspace, cleanup, err := PrepareAPTResolverWorkspace(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	resolverWorkspace, err = SeedAPTResolverArchives(context.Background(), store, resolverWorkspace, []providerstore.ArtifactDescriptor{seed})
	if err != nil {
		t.Fatal(err)
	}
	outputs := [][]byte{
		[]byte("ID\x00debian\x00VERSION_ID\x0013\x00"),
		[]byte("apt 3.0.3 (amd64)\n"), []byte("dpkg 1\n"), []byte("dpkg-deb 1\n"), []byte("dpkg-query 1\n"),
		[]byte("sha256sum 1\n"), []byte("amd64\n"), {}, {},
		[]byte("  MarkInstall hello:amd64 < none -> 2.10 @un puN > FU=1\n    MarkInstall libc6:amd64 < 2.39 @ii pmK > FU=0\n"),
		[]byte("libc6:amd64\t2.39\tamd64\tinstall ok installed\n"), {},
		aptArchiveInspectionStream(t, "hello", "2.10", "amd64", "./usr/bin/hello"),
		aptArchiveInspectionStream(t, "old", "1", "all", "./usr/share/old"),
	}
	commands, _ := stubAPTResolverCommands(t, mustCanonicalProbeResponse(t, aptBaseProbeResponse()), outputs, nil)
	session, err := OpenAPTResolverSession(context.Background(), descriptor, probeWorkspace, resolverWorkspace, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.ProbeBaseProfile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := session.RefreshIndexes(context.Background()); err != nil {
		t.Fatal(err)
	}
	request := aptResolverTestRequest(t, blueprint.APTPackageRequest{Name: "hello", Exports: map[string]blueprint.ExecutableExport{}})
	if _, err := session.PlanPackages(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := session.ReadBasePackageState(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := session.DownloadPackages(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resolverWorkspace.HostDir, "archives", "hello_2.10_amd64.deb"), []byte("new hello bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := session.InventoryArchives(context.Background()); err != nil {
		t.Fatal(err)
	}
	bundles, err := session.InspectArchives(context.Background(), []string{})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != 1 || bundles[0].Tuple.Name != "hello" || bundles[0].Artifact.LogicalPath != "debs/hello_2.10_amd64.deb" || bundles[0].FileListDigest == "" || bundles[0].BasePredecessor != nil {
		t.Fatalf("bundles = %#v", bundles)
	}
	bundle, err := session.PublishBundleArtifacts(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.BasePackages) != 1 || bundle.BasePackages[0].Tuple.Name != "libc6" || len(bundle.BundlePackages) != 1 || bundle.BundlePackages[0].Tuple.Name != "hello" {
		t.Fatalf("bundle = %#v", bundle)
	}
	if err := store.VerifyArtifact(bundle.BundlePackages[0].Artifact); err != nil {
		t.Fatal(err)
	}
	result, reference, err := session.PublishResolvedBundle(context.Background(), store, aptResolverTestNode(t, request))
	if err != nil {
		t.Fatal(err)
	}
	if reference.Digest != result.Bundle.Identity || reference.Kind != providerstore.BundleManifestKind {
		t.Fatalf("result = %#v, reference = %#v", result, reference)
	}
	loaded, err := providers.LoadResolvedBundleManifest(store, reference, aptprovider.ValidateResolvedBundlePayloadV1)
	if err != nil || loaded.Identity != result.Bundle.Identity {
		t.Fatalf("loaded = %#v, err = %v", loaded, err)
	}
	commandCount := len(*commands)
	bundles[0].Tuple.Name = "changed"
	second, err := session.InspectArchives(context.Background(), []string{})
	if err != nil || second[0].Tuple.Name != "hello" || len(*commands) != commandCount {
		t.Fatalf("cached bundles = %#v, err = %v, commands = %d", second, err, len(*commands))
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAPTResolverPlanRejectsUnsupportedMarkersAndDoesNotRetryOrLeakOutput(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	probeWorkspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	resolverWorkspace := testPreparedAPTResolverWorkspace(t)
	outputs := [][]byte{
		[]byte("ID\x00debian\x00VERSION_ID\x0013\x00"),
		[]byte("apt 3.0.3 (amd64)\n"), []byte("dpkg 1\n"), []byte("dpkg-deb 1\n"), []byte("dpkg-query 1\n"),
		[]byte("sha256sum 1\n"), []byte("amd64\n"), {}, {},
		[]byte("  Unsupported secret-marker https://user:secret@example.invalid\n"),
	}
	commands, _ := stubAPTResolverCommands(t, mustCanonicalProbeResponse(t, aptBaseProbeResponse()), outputs, nil)
	session, err := OpenAPTResolverSession(context.Background(), descriptor, probeWorkspace, resolverWorkspace, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.ProbeBaseProfile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := session.RefreshIndexes(context.Background()); err != nil {
		t.Fatal(err)
	}
	request := aptResolverTestRequest(t, blueprint.APTPackageRequest{Name: "hello", Exports: map[string]blueprint.ExecutableExport{}})
	_, err = session.PlanPackages(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "unsupported output") || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "example.invalid") {
		t.Fatalf("plan err = %v", err)
	}
	commandCount := len(*commands)
	_, err = session.PlanPackages(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "already failed") || len(*commands) != commandCount {
		t.Fatalf("retry err = %v, commands = %d", err, len(*commands))
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAPTResolverRefreshForwardsDiagnosticsAndReturnsStructuredError(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	probeWorkspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	resolverWorkspace := testPreparedAPTResolverWorkspace(t)
	outputs := [][]byte{
		[]byte("ID\x00debian\x00VERSION_ID\x0013\x00"),
		[]byte("apt 3.0.3 (amd64)\n"), []byte("dpkg 1\n"), []byte("dpkg-deb 1\n"), []byte("dpkg-query 1\n"),
		[]byte("sha256sum 1\n"), []byte("amd64\n"), {},
	}
	commands, _ := stubAPTResolverCommands(t, mustCanonicalProbeResponse(t, aptBaseProbeResponse()), outputs, nil)
	var liveStdout bytes.Buffer
	var liveStderr bytes.Buffer
	session, err := OpenAPTResolverSession(
		context.Background(), descriptor, probeWorkspace, resolverWorkspace,
		RunOptions{Stdout: &liveStdout, Stderr: &liveStderr},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.RefreshIndexes(context.Background()); err == nil || !strings.Contains(err.Error(), "requires successful base validation") {
		t.Fatalf("pre-validation refresh err = %v", err)
	}
	if _, err := session.ProbeBaseProfile(context.Background()); err != nil {
		t.Fatal(err)
	}
	prior := runAPTResolverFollowupCommand
	runAPTResolverFollowupCommand = func(spec CommandSpec, options RunOptions) error {
		commandsValue := *commands
		commandsValue = append(commandsValue, spec)
		*commands = commandsValue
		_, _ = options.Stdout.Write([]byte("Reading package lists...\n"))
		_, _ = options.Stderr.Write([]byte("E: https://user:secret@example.invalid/private failed\n"))
		return errors.New("exit status 100: user:secret")
	}
	t.Cleanup(func() { runAPTResolverFollowupCommand = prior })
	err = session.RefreshIndexes(context.Background())
	if err == nil || !strings.Contains(err.Error(), "apt.resolve.update") || !strings.Contains(err.Error(), "apt.update_failed") || !strings.Contains(err.Error(), "select or rebuild a base image") || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "example.invalid") {
		t.Fatalf("refresh err = %v", err)
	}
	var failure *providers.BuildErrorV1
	if !errors.As(err, &failure) || failure.Phase != "resolve" || failure.Code != "apt.update_failed" || failure.CauseKind != "apt.resolve.update" {
		t.Fatalf("structured refresh error = %#v", failure)
	}
	if !strings.Contains(liveStdout.String(), "Reading package lists") || !strings.Contains(liveStderr.String(), "user:secret@example.invalid") {
		t.Fatalf("live output was not forwarded: stdout=%q stderr=%q", liveStdout.String(), liveStderr.String())
	}
	commandCount := len(*commands)
	err = session.RefreshIndexes(context.Background())
	if err == nil || !strings.Contains(err.Error(), "already failed") || len(*commands) != commandCount {
		t.Fatalf("retry err = %v, commands = %d", err, len(*commands))
	}
	runAPTResolverFollowupCommand = prior
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAPTResolverSessionRejectsForeignArchitecture(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	probeWorkspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	resolverWorkspace := testPreparedAPTResolverWorkspace(t)
	outputs := [][]byte{
		[]byte("ID\x00ubuntu\x00VERSION_ID\x0024.04\x00"),
		[]byte("apt 2.7.14 (amd64)\n"), []byte("dpkg 1\n"), []byte("dpkg-deb 1\n"), []byte("dpkg-query 1\n"),
		[]byte("sha256sum 1\n"),
		[]byte("amd64\n"), []byte("i386\n"),
	}
	commands, _ := stubAPTResolverCommands(t, mustCanonicalProbeResponse(t, aptBaseProbeResponse()), outputs, nil)
	session, err := OpenAPTResolverSession(context.Background(), descriptor, probeWorkspace, resolverWorkspace, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.ProbeBaseProfile(context.Background()); err == nil || !strings.Contains(err.Error(), "unsupported foreign") {
		t.Fatalf("err = %v", err)
	}
	if len(*commands) != 11 {
		t.Fatalf("commands = %#v", *commands)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAPTDiagnosticTailKeepsOnlyBoundedSuffix(t *testing.T) {
	tail := &aptDiagnosticTail{limit: 5}
	for _, chunk := range []string{"abc", "def", "ghijkl"} {
		if written, err := tail.Write([]byte(chunk)); err != nil || written != len(chunk) {
			t.Fatalf("write %q = %d, %v", chunk, written, err)
		}
	}
	if got := tail.String(); got != "hijkl" {
		t.Fatalf("tail = %q", got)
	}
}

func TestAPTResolverSessionRejectsAmbiguousNativeArchitecture(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	probeWorkspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	resolverWorkspace := testPreparedAPTResolverWorkspace(t)
	outputs := [][]byte{
		[]byte("ID\x00debian\x00VERSION_ID\x0013\x00"),
		[]byte("apt 3.0.3 (amd64)\n"), []byte("dpkg 1\n"), []byte("dpkg-deb 1\n"), []byte("dpkg-query 1\n"),
		[]byte("sha256sum 1\n"),
		[]byte("amd64\narm64\n"),
	}
	stubAPTResolverCommands(t, mustCanonicalProbeResponse(t, aptBaseProbeResponse()), outputs, nil)
	session, err := OpenAPTResolverSession(context.Background(), descriptor, probeWorkspace, resolverWorkspace, RunOptions{})
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
	_, err := OpenAPTResolverSession(context.Background(), descriptor, probeWorkspace, resolverWorkspace, RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "binfmt/QEMU") {
		t.Fatalf("err = %v", err)
	}
	if len(*commands) != 3 || (*commands)[2].Args[0] != "rm" {
		t.Fatalf("commands = %#v", *commands)
	}
}

func TestOpenAPTResolverSessionFinishesCreateBeforeHonoringCancellation(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	probeWorkspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	resolverWorkspace := testPreparedAPTResolverWorkspace(t)
	previousOpen := runAPTResolverOpenCommand
	previousFollowup := runAPTResolverFollowupCommand
	t.Cleanup(func() {
		runAPTResolverOpenCommand = previousOpen
		runAPTResolverFollowupCommand = previousFollowup
	})
	ctx, cancel := context.WithCancel(context.Background())
	removed := false
	runAPTResolverOpenCommand = func(_ CommandSpec, options RunOptions) error {
		cancel()
		if err := options.Context.Err(); err != nil {
			t.Fatalf("Docker create inherited cancellation: %v", err)
		}
		return nil
	}
	runAPTResolverFollowupCommand = func(spec CommandSpec, _ RunOptions) error {
		if len(spec.Args) == 3 && spec.Args[0] == "rm" && spec.Args[1] == "--force" {
			removed = true
			return nil
		}
		t.Fatalf("unexpected follow-up command: %#v", spec)
		return nil
	}
	if _, err := OpenAPTResolverSession(ctx, descriptor, probeWorkspace, resolverWorkspace, RunOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if !removed {
		t.Fatal("created container was not removed after cancellation")
	}
}

func TestOpenAPTResolverSessionPropagatesDockerPreflightTimeout(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	probeWorkspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	resolverWorkspace := testPreparedAPTResolverWorkspace(t)
	previousOpen := runAPTResolverOpenCommand
	previousFollowup := runAPTResolverFollowupCommand
	t.Cleanup(func() {
		runAPTResolverOpenCommand = previousOpen
		runAPTResolverFollowupCommand = previousFollowup
	})
	var received time.Duration
	runAPTResolverOpenCommand = func(_ CommandSpec, options RunOptions) error {
		received = options.DockerPreflightTimeout
		return nil
	}
	runAPTResolverFollowupCommand = func(_ CommandSpec, _ RunOptions) error { return nil }

	const timeout = 17 * time.Second
	session, err := OpenAPTResolverSession(context.Background(), descriptor, probeWorkspace, resolverWorkspace, RunOptions{
		DockerPreflightTimeout: timeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	if received != timeout {
		t.Fatalf("Docker preflight timeout = %s, want %s", received, timeout)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAPTResolverSessionRejectsNonemptyScratchBeforeDocker(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	probeWorkspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	resolverWorkspace := testPreparedAPTResolverWorkspace(t)
	if err := os.WriteFile(filepath.Join(resolverWorkspace.HostDir, "output", "unexpected"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	commands, _ := stubAPTResolverCommands(t, nil, nil, nil)
	_, err := OpenAPTResolverSession(context.Background(), descriptor, probeWorkspace, resolverWorkspace, RunOptions{})
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

func aptResolverTestRequest(t *testing.T, packages ...blueprint.APTPackageRequest) providers.CanonicalProviderRequest {
	t.Helper()
	request, err := aptprovider.CanonicalProviderRequestV1(aptprovider.APTProviderRequestV1{Components: []aptprovider.APTComponentRequestV1{{
		Component: "system", Packages: packages,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func aptResolverTestNode(t *testing.T, request providers.CanonicalProviderRequest) providers.NodeSpec {
	t.Helper()
	outputs, err := aptprovider.OutputDeclarationsV1(request)
	if err != nil {
		t.Fatal(err)
	}
	node := providers.NodeSpec{
		ID: "apt", Provider: blueprint.ComponentTypeAPT, Components: []string{"system"}, Request: request,
		OutputDeclarations: outputs,
		Requirements: providers.RequirementDeclaration{
			Executables: []providers.ExecutableRequirement{}, Files: []providers.FileRequirement{},
			ProviderData: providers.CanonicalProviderData{Schema: request.Schema, Value: request.Value},
		},
	}
	if err := providers.ValidateNodeSpec(node); err != nil {
		t.Fatal(err)
	}
	return node
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
			joined := strings.Join(spec.Args, "\x00")
			if strings.Contains(joined, "Debug::pkgDepCache::Marker=1") {
				_, _ = options.Stderr.Write(profileOutputs[profileIndex])
			} else {
				_, _ = options.Stdout.Write(profileOutputs[profileIndex])
			}
			profileIndex++
		}
		return nil
	}
	return &commands, &probeInput
}

func aptArchiveInspectionStream(t *testing.T, name string, version string, architecture string, member string) []byte {
	t.Helper()
	var output bytes.Buffer
	_, _ = fmt.Fprintf(&output, "Package: %s\nVersion: %s\nArchitecture: %s\x00", name, version, architecture)
	tarWriter := tar.NewWriter(&output)
	if err := tarWriter.WriteHeader(&tar.Header{Name: member, Typeflag: tar.TypeReg, Mode: 0o755, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
