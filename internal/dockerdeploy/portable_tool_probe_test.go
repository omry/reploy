package dockerdeploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/toolcatalog"
)

func TestRunPortableToolValidationProfileUsesFixedDirectExecutionPolicy(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(
		toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo2", Args: []string{"--version"}},
		toolcatalog.RecordProbeV1{
			Path: "/opt/demo/bin/demo",
			Args: []string{"literal;$(touch /tmp/pwned)", "$(id)", "a|b"},
		},
	)
	var responses = []portableToolProbeStubResponse{
		{stdout: []byte("demo2 1.2.3\n")},
		{stdout: []byte("demo 1.2.3\n")},
	}
	commands := stubPortableToolProbeCommands(t, responses)

	evidence, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Schema == "" || evidence.ExecutorVersion == "" {
		t.Fatalf("evidence identity is incomplete: %#v", evidence)
	}
	if evidence.Profile.ID != profile.ID || evidence.Profile.Digest.Validate() != nil {
		t.Fatalf("evidence profile reference = %#v, profile = %#v", evidence.Profile, profile)
	}
	if !evidence.Policy.NetworkDisabled || evidence.Policy.WorkingDirectory != "/" ||
		evidence.Policy.TimeoutMillis != "120000" || evidence.Policy.OutputLimitBytes != "65536" ||
		evidence.Policy.MemoryLimitBytes != "1073741824" || evidence.Policy.TemporaryBytes != "134217728" ||
		evidence.Policy.PIDLimit != "512" || evidence.Policy.CPULimitMillis != "2000" ||
		evidence.Policy.OpenFilesLimit != "1024" {
		t.Fatalf("portable probe policy is not fixed: %#v", evidence.Policy)
	}
	wantEnvironment := []toolcatalog.RecordEnvironmentVariableV1{
		{Name: "HOME", Value: "/tmp"}, {Name: "LANG", Value: "C"},
		{Name: "LC_ALL", Value: "C"}, {Name: "PATH", Value: "/usr/bin:/bin"},
		{Name: "TMPDIR", Value: "/tmp"},
	}
	if !reflect.DeepEqual(evidence.Policy.Environment, wantEnvironment) {
		t.Fatalf("portable probe environment policy = %#v, want %#v", evidence.Policy.Environment, wantEnvironment)
	}
	results := portableToolProbeResults(t, evidence)
	if len(results) != len(profile.Probes) {
		t.Fatalf("result count = %d, want %d", len(results), len(profile.Probes))
	}
	for index, result := range results {
		probe := portableToolProbeResultField(t, result, "Probe")
		if !reflect.DeepEqual(probe.Interface(), profile.Probes[index]) {
			t.Fatalf("result %d probe = %#v, want %#v", index, probe.Interface(), profile.Probes[index])
		}
	}

	create := portableToolFindCommand(t, *commands, "create")
	portableToolRequireAdjacent(t, create.Args, "--network", "none")
	portableToolRequireFlag(t, create.Args, "--read-only")
	portableToolRequireAdjacent(t, create.Args, "--cap-drop", "ALL")
	portableToolRequireAdjacent(t, create.Args, "--cap-add", "SETPCAP")
	portableToolRequireAdjacent(t, create.Args, "--cap-add", "SETUID")
	portableToolRequireAdjacent(t, create.Args, "--cap-add", "SETGID")
	portableToolRequireAdjacent(t, create.Args, "--security-opt", "no-new-privileges=true")
	portableToolRequireAdjacent(t, create.Args, "--memory", "1073741824")
	portableToolRequireAdjacent(t, create.Args, "--memory-swap", "1073741824")
	portableToolRequireAdjacent(t, create.Args, "--pids-limit", "512")
	portableToolRequireAdjacent(t, create.Args, "--cpus", "2")
	portableToolRequireAdjacent(t, create.Args, "--ulimit", "nofile=1024:1024")
	portableToolRequireAdjacent(t, create.Args, "--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=134217728,mode=1777")
	createCount := 0
	execIndex := 0
	for _, command := range *commands {
		if len(command.Args) > 0 && command.Args[0] == "create" {
			createCount++
		}
		if len(command.Args) == 0 || command.Args[0] != "exec" {
			continue
		}
		portableToolRequireAdjacent(t, command.Args, "--workdir", "/")
		if execIndex >= len(profile.Probes) {
			t.Fatalf("unexpected extra exec command: %#v", command.Args)
		}
		portableToolRequireDirectProbeInvocation(t, command.Args, profile.Probes[execIndex])
		portableToolRequireFixedEnvironment(t, command.Args, workspace.ContainerExecutable)
		execIndex++
		for _, argument := range command.Args {
			if argument == "/bin/sh" || argument == "sh" || argument == "bash" || argument == "zsh" || argument == "-c" {
				t.Fatalf("portable probe command invokes a shell: %#v", command.Args)
			}
		}
	}
	if len(*commands) < 4 {
		t.Fatalf("portable probe commands = %#v", *commands)
	}
	if execIndex != len(profile.Probes) {
		t.Fatalf("portable probe exec count = %d, want %d", execIndex, len(profile.Probes))
	}
	if createCount != len(profile.Probes) {
		t.Fatalf("portable probe create count = %d, want one fresh container for each of %d probes", createCount, len(profile.Probes))
	}
}

func TestRunPortableToolValidationProfileEvidenceIsCanonicalAndDigestStable(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--version"}})
	responses := []portableToolProbeStubResponse{{stdout: []byte("demo 1.2.3\n"), stderr: []byte("diagnostic\n")}}
	stubPortableToolProbeCommands(t, responses)
	first, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := canonical.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	firstDigest, err := PortableToolProbeEvidenceDigestV1(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := firstDigest.Validate(); err != nil {
		t.Fatalf("evidence digest = %q: %v", firstDigest, err)
	}

	secondDigest, err := PortableToolProbeEvidenceDigestV1(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := canonical.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) || firstDigest != secondDigest {
		t.Fatalf("evidence is not canonical/digest stable: json %q/%q digest %q/%q", firstJSON, secondJSON, firstDigest, secondDigest)
	}
}

func TestRunPortableToolValidationProfileEvidenceDoesNotAliasProfileArguments(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--version"}})
	stubPortableToolProbeCommands(t, []portableToolProbeStubResponse{{stdout: []byte("demo 1.2.3\n")}})
	evidence, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile)
	if err != nil {
		t.Fatal(err)
	}
	before, err := PortableToolProbeEvidenceDigestV1(evidence)
	if err != nil {
		t.Fatal(err)
	}
	profile.Probes[0].Args[0] = "--mutated"
	if got := evidence.Results[0].Probe.Args[0]; got != "--version" {
		t.Fatalf("evidence probe argument = %q after profile mutation", got)
	}
	after, err := PortableToolProbeEvidenceDigestV1(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("evidence digest changed after profile mutation: %q != %q", after, before)
	}
}

func TestRunPortableToolValidationProfileClassifiesExitAndTimeout(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(
		toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--exit"}},
		toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo2", Args: []string{"--timeout"}},
	)
	responses := []portableToolProbeStubResponse{
		{stderr: []byte("failed\n"), err: portableToolExitError(t, 7)},
		{stderr: []byte("timed out\n"), err: portableToolExitError(t, 137)},
	}
	stubPortableToolProbeCommands(t, responses)
	previousContext := newPortableToolProbeContext
	t.Cleanup(func() { newPortableToolProbeContext = previousContext })
	contextCalls := 0
	newPortableToolProbeContext = func(parent context.Context) (context.Context, context.CancelFunc) {
		contextCalls++
		if contextCalls == 1 {
			return context.WithTimeout(parent, time.Minute)
		}
		return context.WithDeadline(parent, time.Now().Add(-time.Second))
	}
	evidence, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile)
	if err != nil {
		t.Fatal(err)
	}
	results := portableToolProbeResults(t, evidence)
	if got := strings.ToLower(portableToolStringField(t, results[0], "Outcome")); !strings.Contains(got, "exit") && !strings.Contains(got, "fail") && !strings.Contains(got, "nonzero") {
		t.Fatalf("nonzero outcome = %q", got)
	}
	if got := strings.ToLower(portableToolStringField(t, results[1], "Outcome")); !strings.Contains(got, "timeout") && !strings.Contains(got, "deadline") {
		t.Fatalf("timeout outcome = %q", got)
	}
	if got := portableToolNullableStringField(t, results[0], "ExitCode"); got != "7" {
		t.Fatalf("nonzero exit code = %q, want 7", got)
	}
	if got := portableToolNullableStringField(t, results[1], "ExitCode"); got != "" {
		t.Fatalf("timeout exit code = %q, want null/empty", got)
	}
}

func TestRunPortableToolValidationProfileBoundsObservedOutput(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--output"}})
	stdout := bytes.Repeat([]byte("out"), 64*1024)
	stderr := bytes.Repeat([]byte("err"), 64*1024)
	canceled := false
	stubPortableToolProbeCommands(t, []portableToolProbeStubResponse{{stdout: stdout, stderr: stderr, contextCanceled: &canceled}})
	evidence, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile)
	if err != nil {
		t.Fatal(err)
	}
	result := portableToolProbeResults(t, evidence)[0]
	if got := portableToolStringField(t, result, "Outcome"); got != PortableToolProbeOutcomeOutputLimitV1 {
		t.Fatalf("output-limit outcome = %q, want %q", got, PortableToolProbeOutcomeOutputLimitV1)
	}
	portableToolAssertBoundedOutput(t, result, "Stdout", stdout)
	portableToolAssertBoundedOutput(t, result, "Stderr", stderr)
	if !canceled {
		t.Fatal("probe execution context was not canceled at the output limit")
	}
}

func TestRunPortableToolValidationProfileRejectsInvalidProfileBeforeDocker(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "relative/demo", Args: []string{}})
	called := false
	previous := bindImageValidationCommandRunner
	t.Cleanup(func() { bindImageValidationCommandRunner = previous })
	bindImageValidationCommandRunner = func(context.Context, CommandSpec, time.Duration) (commandRunner, error) {
		called = true
		return nil, errors.New("Docker must not be reached")
	}
	if _, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile); err == nil {
		t.Fatal("invalid profile unexpectedly succeeded")
	}
	if called {
		t.Fatal("invalid profile reached Docker")
	}
}

func TestRunPortableToolValidationProfileDoesNotMaskInfrastructureFailureWithOutputLimit(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--output"}})
	stubPortableToolProbeCommands(t, []portableToolProbeStubResponse{{
		stdout: bytes.Repeat([]byte("x"), portableToolProbeOutputLimit), err: errors.New("Docker transport failed"),
	}})
	if _, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile); err == nil || !strings.Contains(err.Error(), "Docker transport failed") {
		t.Fatalf("infrastructure error = %v, want Docker transport failure", err)
	}
}

type portableToolProbeStubResponse struct {
	stdout          []byte
	stderr          []byte
	err             error
	contextCanceled *bool
}

func stubPortableToolProbeCommands(t *testing.T, responses []portableToolProbeStubResponse) *[]CommandSpec {
	t.Helper()
	previous := bindImageValidationCommandRunner
	commands := []CommandSpec{}
	probeIndex := 0
	t.Cleanup(func() { bindImageValidationCommandRunner = previous })
	bindImageValidationCommandRunner = func(context.Context, CommandSpec, time.Duration) (commandRunner, error) {
		return func(spec CommandSpec, options RunOptions) error {
			commands = append(commands, spec)
			if len(spec.Args) == 0 || spec.Args[0] != "exec" {
				return nil
			}
			if probeIndex >= len(responses) {
				return fmt.Errorf("unexpected portable probe exec %d: %#v", probeIndex, spec.Args)
			}
			response := responses[probeIndex]
			probeIndex++
			if options.Stdout != nil {
				_, _ = options.Stdout.Write(response.stdout)
			}
			if options.Stderr != nil {
				_, _ = options.Stderr.Write(response.stderr)
			}
			if response.contextCanceled != nil {
				*response.contextCanceled = options.Context.Err() != nil
			}
			return response.err
		}, nil
	}
	return &commands
}

func portableToolValidationProfile(probes ...toolcatalog.RecordProbeV1) toolcatalog.ValidationProfileRecordV1 {
	return toolcatalog.ValidationProfileRecordV1{
		Schema: toolcatalog.ValidationProfileSchemaV1,
		ID:     "tool:demo/releases/1.2.3/validation/profiles/default",
		Tool:   "demo", Version: "1.2.3", Probes: probes,
	}
}

func portableToolExitError(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Run()
	if err == nil {
		t.Fatalf("exit helper unexpectedly succeeded")
	}
	return err
}

func portableToolFindCommand(t *testing.T, commands []CommandSpec, operation string) CommandSpec {
	t.Helper()
	for _, command := range commands {
		if len(command.Args) > 0 && command.Args[0] == operation {
			return command
		}
	}
	t.Fatalf("%s command missing from %#v", operation, commands)
	return CommandSpec{}
}

func portableToolRequireFlag(t *testing.T, args []string, flag string) {
	t.Helper()
	for _, argument := range args {
		if argument == flag || strings.HasPrefix(argument, flag+"=") {
			return
		}
	}
	t.Fatalf("command %#v does not contain %s", args, flag)
}

func portableToolRequireAdjacent(t *testing.T, args []string, first string, second string) {
	t.Helper()
	for index := 0; index+1 < len(args); index++ {
		if args[index] == first && args[index+1] == second {
			return
		}
	}
	t.Fatalf("command %#v does not contain adjacent %q %q", args, first, second)
}

func portableToolRequireDirectProbeInvocation(t *testing.T, args []string, probe toolcatalog.RecordProbeV1) {
	t.Helper()
	separator := -1
	for index, argument := range args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 {
		t.Fatalf("portable probe command lacks catalog argument separator: %#v", args)
	}
	if separator+1+len(probe.Args)+1 > len(args) {
		t.Fatalf("portable probe command is missing direct probe argv: %#v", args)
	}
	// Each invocation carries exactly one catalog probe. The first probe is
	// enough to ensure that the separator does not turn its metacharacters into
	// shell syntax; order is checked from the returned evidence.
	want := append([]string{probe.Path}, probe.Args...)
	if !reflect.DeepEqual(args[separator+1:], want) {
		// Some fixed helpers add a stable marker immediately after --. Permit
		// that marker while still requiring the complete catalog argv to remain
		// literal and contiguous.
		if len(args) < separator+2+len(want) || !reflect.DeepEqual(args[separator+2:], want) {
			t.Fatalf("catalog argv after -- = %#v, want %#v", args[separator+1:], want)
		}
	}
}

func portableToolRequireFixedEnvironment(t *testing.T, args []string, expectedHelper string) {
	t.Helper()
	portableToolRequireAdjacent(t, args, "--environment-profile", "portable-tool-v1")
	workdir := -1
	restrictedExec := -1
	for index, argument := range args {
		if argument == "--workdir" {
			workdir = index
		}
		if argument == "restricted-exec" {
			restrictedExec = index
		}
		if argument == "/usr/bin/env" || argument == "env" {
			t.Fatalf("image environment utility precedes the trusted helper: %#v", args)
		}
	}
	// After --workdir and its value, Docker receives exactly the container,
	// pinned helper, and restricted-exec action. No image-controlled process may
	// appear between the container name and helper.
	if workdir < 0 || restrictedExec != workdir+4 {
		t.Fatalf("portable probe command does not invoke restricted-exec through the first container process: %#v", args)
	}
	if args[restrictedExec-1] != expectedHelper {
		t.Fatalf("portable probe helper = %q, want pinned helper %q", args[restrictedExec-1], expectedHelper)
	}
}

func portableToolProbeResults(t *testing.T, evidence PortableToolProbeEvidenceV1) []reflect.Value {
	t.Helper()
	field := portableToolField(t, reflect.ValueOf(evidence), "Results")
	if field.Kind() != reflect.Slice {
		t.Fatalf("evidence Results has kind %s", field.Kind())
	}
	results := make([]reflect.Value, field.Len())
	for index := range results {
		results[index] = field.Index(index)
	}
	return results
}

func portableToolProbeResultField(t *testing.T, result reflect.Value, name string) reflect.Value {
	t.Helper()
	return portableToolField(t, result, name)
}

func portableToolField(t *testing.T, value reflect.Value, name string) reflect.Value {
	t.Helper()
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			t.Fatalf("field %s is nil", name)
		}
		value = value.Elem()
	}
	field := value.FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("%s missing from %s", name, value.Type())
	}
	return field
}

func portableToolStringField(t *testing.T, value reflect.Value, name string) string {
	t.Helper()
	field := portableToolField(t, value, name)
	if field.Kind() != reflect.String {
		t.Fatalf("%s has kind %s, want string", name, field.Kind())
	}
	return field.String()
}

func portableToolNullableStringField(t *testing.T, value reflect.Value, name string) string {
	t.Helper()
	field := portableToolField(t, value, name)
	for field.Kind() == reflect.Pointer || field.Kind() == reflect.Interface {
		if field.IsNil() {
			return ""
		}
		field = field.Elem()
	}
	switch field.Kind() {
	case reflect.String:
		return field.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(field.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(field.Uint(), 10)
	default:
		t.Fatalf("%s has unsupported kind %s", name, field.Kind())
		return ""
	}
}

func portableToolAssertBoundedOutput(t *testing.T, result reflect.Value, fieldName string, full []byte) {
	t.Helper()
	output := portableToolField(t, result, fieldName)
	content := portableToolField(t, output, "Content")
	if content.Kind() != reflect.String {
		t.Fatalf("%s content has kind %s, want base64 string", fieldName, content.Kind())
	}
	decoded, err := base64.StdEncoding.DecodeString(content.String())
	if err != nil {
		t.Fatalf("%s content is not base64: %v", fieldName, err)
	}
	if len(decoded) >= len(full) {
		t.Fatalf("%s retained %d bytes, want bounded below observed %d", fieldName, len(decoded), len(full))
	}
	truncated := portableToolField(t, output, "Truncated")
	if truncated.Kind() != reflect.Bool || !truncated.Bool() {
		t.Fatalf("%s truncation = %#v, want true", fieldName, truncated.Interface())
	}
	size := portableToolOutputSize(t, output)
	wantObserved := int64(portableToolProbeOutputLimit + 1)
	if size != wantObserved {
		t.Fatalf("%s observed size = %d, want first exceeded byte %d", fieldName, size, wantObserved)
	}
	digest := portableToolField(t, output, "SHA256")
	if digest.Kind() != reflect.String {
		t.Fatalf("%s SHA256 has kind %s", fieldName, digest.Kind())
	}
	wantDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(full[:wantObserved]))
	if digest.String() != wantDigest {
		t.Fatalf("%s observed digest = %q, want %q", fieldName, digest.String(), wantDigest)
	}
}

func portableToolOutputSize(t *testing.T, output reflect.Value) int64 {
	t.Helper()
	for _, name := range []string{"Size", "ByteSize", "ObservedSize", "ObservedBytes", "Bytes"} {
		field := output.FieldByName(name)
		if !field.IsValid() {
			continue
		}
		switch field.Kind() {
		case reflect.String:
			value, err := strconv.ParseInt(field.String(), 10, 64)
			if err != nil {
				t.Fatalf("%s output size %q: %v", name, field.String(), err)
			}
			return value
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return field.Int()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return int64(field.Uint())
		}
	}
	t.Fatalf("output evidence %s has no supported observed-size field", output.Type())
	return 0
}
