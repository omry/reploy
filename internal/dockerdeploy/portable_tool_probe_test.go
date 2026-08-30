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

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
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
		evidence.Policy.TimeoutMillis != "120000" || evidence.Policy.CleanupTimeoutMillis != "5000" || evidence.Policy.OutputLimitBytes != "65536" ||
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
	statusExecCount := 0
	ownershipLabels := []string{}
	for _, command := range *commands {
		if len(command.Args) > 0 && command.Args[0] == "create" {
			createCount++
			label := portableToolCommandValue(t, command.Args, "--label")
			prefix := portableToolProbeOwnershipLabelV1 + "="
			if !strings.HasPrefix(label, prefix) || !canonicalPortableToolProbeOwnershipTokenV1(strings.TrimPrefix(label, prefix)) {
				t.Fatalf("portable probe create ownership label = %q, want %s=<64 lowercase hex>", label, prefix)
			}
			ownershipLabels = append(ownershipLabels, strings.TrimPrefix(label, prefix))
		}
		if len(command.Args) == 0 || command.Args[0] != "exec" {
			continue
		}
		if portableToolIsExitStatusExec(command.Args) {
			statusExecCount++
			continue
		}
		if !portableToolIsPrimaryExec(command.Args) {
			t.Fatalf("unexpected portable probe exec command: %#v", command.Args)
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
	if statusExecCount != len(profile.Probes) {
		t.Fatalf("portable probe trusted-status exec count = %d, want %d", statusExecCount, len(profile.Probes))
	}
	if createCount != len(profile.Probes) {
		t.Fatalf("portable probe create count = %d, want one fresh container for each of %d probes", createCount, len(profile.Probes))
	}
	if len(ownershipLabels) != len(profile.Probes) || ownershipLabels[0] == ownershipLabels[1] {
		t.Fatalf("portable probe ownership labels = %#v, want one distinct per attempt", ownershipLabels)
	}
}

func TestPortableToolValidationCreateCPUsDerivesFromCPULimitMillis(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	spec, _, err := portableToolValidationCreateCommandSpec(descriptor, workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := portableToolCommandValue(t, spec.Args, "--cpus")
	want := portableToolExpectedCPULimitDockerValue()
	if got != want {
		t.Fatalf("Docker CPU limit = %q, want %q derived from %d ms", got, want, portableToolProbeCPULimitMillis)
	}
}

func TestRunPortableToolValidationProfileSetupUsesLifecycleDeadline(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--version"}})
	previousContext := newPortableToolProbeContext
	t.Cleanup(func() { newPortableToolProbeContext = previousContext })
	newPortableToolProbeContext = func(parent context.Context) (context.Context, context.CancelFunc) {
		return context.WithTimeout(parent, time.Second)
	}
	var bindDeadline time.Time
	var bindHasDeadline bool
	var createDeadline time.Time
	var createHasDeadline bool
	ownershipToken := ""
	previousBind := bindImageValidationCommandRunner
	t.Cleanup(func() { bindImageValidationCommandRunner = previousBind })
	bindImageValidationCommandRunner = func(bindContext context.Context, _ CommandSpec, _ time.Duration) (commandRunner, error) {
		bindDeadline, bindHasDeadline = bindContext.Deadline()
		return func(spec CommandSpec, options RunOptions) error {
			if len(spec.Args) > 0 && spec.Args[0] == "create" {
				ownershipToken = portableToolCreateOwnershipToken(t, spec.Args)
				createDeadline, createHasDeadline = options.Context.Deadline()
			}
			if len(spec.Args) > 0 && spec.Args[0] == "start" {
				createDeadline, createHasDeadline = options.Context.Deadline()
			}
			if len(spec.Args) > 0 && spec.Args[0] == "exec" && portableToolIsPrimaryExec(spec.Args) && options.Stdout != nil {
				_, _ = options.Stdout.Write([]byte("demo 1.2.3\n"))
			}
			if len(spec.Args) > 0 && spec.Args[0] == "exec" && portableToolIsExitStatusExec(spec.Args) && options.Stdout != nil {
				_, _ = options.Stdout.Write([]byte("0\n"))
			}
			if len(spec.Args) > 1 && spec.Args[0] == "container" && spec.Args[1] == "inspect" && options.Stdout != nil {
				_, _ = options.Stdout.Write([]byte(ownershipToken + "\n"))
			}
			return nil
		}, nil
	}
	if _, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile); err != nil {
		t.Fatal(err)
	}
	if !bindHasDeadline || time.Until(bindDeadline) <= 0 || time.Until(bindDeadline) > time.Second {
		t.Fatalf("Docker binder deadline = %v, has deadline = %v; want bounded lifecycle deadline", bindDeadline, bindHasDeadline)
	}
	if !createHasDeadline || time.Until(createDeadline) <= 0 || time.Until(createDeadline) > time.Second {
		t.Fatalf("Docker setup deadline = %v, has deadline = %v; want bounded lifecycle deadline", createDeadline, createHasDeadline)
	}
}

func TestOpenPortableToolValidationSessionCleansCanceledStartWithFreshBound(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cleanupCalled := false
	cleanupSucceeded := false
	cleanupHasDeadline := false
	var cleanupDeadline time.Time
	previousBind := bindImageValidationCommandRunner
	t.Cleanup(func() { bindImageValidationCommandRunner = previousBind })
	bindImageValidationCommandRunner = func(context.Context, CommandSpec, time.Duration) (commandRunner, error) {
		return func(spec CommandSpec, options RunOptions) error {
			if len(spec.Args) == 0 {
				return errors.New("empty Docker command")
			}
			switch spec.Args[0] {
			case "create":
				return nil
			case "start":
				cancel()
				return options.Context.Err()
			case "rm":
				cleanupCalled = true
				cleanupDeadline, cleanupHasDeadline = options.Context.Deadline()
				if err := options.Context.Err(); err != nil {
					return err
				}
				cleanupSucceeded = true
				return nil
			default:
				return fmt.Errorf("unexpected Docker command: %#v", spec.Args)
			}
		}, nil
	}
	_, err := openImageValidationSessionWithCreateBounded(
		ctx, descriptor, workspace, nil, portableToolValidationCreateCommandSpec,
		newPortableToolProbeCleanupContext,
	)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled start error = %v, want context cancellation", err)
	}
	if !cleanupCalled {
		t.Fatal("canceled start did not attempt container cleanup")
	}
	if !cleanupSucceeded {
		t.Fatal("container cleanup received an already-canceled context")
	}
	if !cleanupHasDeadline {
		t.Fatal("container cleanup context has no explicit bound")
	}
	if remaining := time.Until(cleanupDeadline); remaining <= 0 || remaining > portableToolProbeCleanupTimeout {
		t.Fatalf("container cleanup deadline remaining = %v, want (0, %v]", remaining, portableToolProbeCleanupTimeout)
	}
}

func TestOpenPortableToolValidationSessionCleansCanceledCreateWithFreshBound(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	created := false
	cleanupCalled := false
	cleanupSucceeded := false
	cleanupHasDeadline := false
	var cleanupDeadline time.Time
	previousBind := bindImageValidationCommandRunner
	t.Cleanup(func() { bindImageValidationCommandRunner = previousBind })
	bindImageValidationCommandRunner = func(context.Context, CommandSpec, time.Duration) (commandRunner, error) {
		return func(spec CommandSpec, options RunOptions) error {
			if len(spec.Args) == 0 {
				return errors.New("empty Docker command")
			}
			switch spec.Args[0] {
			case "create":
				// Model Docker having created the container immediately before the
				// lifecycle cancellation is observed by the command runner.
				created = true
				cancel()
				return options.Context.Err()
			case "rm":
				cleanupCalled = true
				cleanupDeadline, cleanupHasDeadline = options.Context.Deadline()
				if err := options.Context.Err(); err != nil {
					return err
				}
				cleanupSucceeded = true
				return nil
			default:
				return fmt.Errorf("unexpected Docker command: %#v", spec.Args)
			}
		}, nil
	}
	_, err := openImageValidationSessionWithCreateBounded(
		ctx, descriptor, workspace, nil, portableToolValidationCreateCommandSpec,
		newPortableToolProbeCleanupContext,
	)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled create error = %v, want context cancellation", err)
	}
	if !created {
		t.Fatal("canceled create did not reach the container-creation boundary")
	}
	if !cleanupCalled {
		t.Fatal("canceled create did not attempt container cleanup")
	}
	if !cleanupSucceeded {
		t.Fatal("container cleanup received an already-canceled context")
	}
	if !cleanupHasDeadline {
		t.Fatal("container cleanup context has no explicit bound")
	}
	if remaining := time.Until(cleanupDeadline); remaining <= 0 || remaining > portableToolProbeCleanupTimeout {
		t.Fatalf("container cleanup deadline remaining = %v, want (0, %v]", remaining, portableToolProbeCleanupTimeout)
	}
}

func TestOpenPortableToolValidationSessionOwnershipGatesCanceledCreateCleanup(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	owner := strings.Repeat("a", 64)
	for _, test := range []struct {
		name         string
		inspectValue string
		inspectErr   error
		wantRemove   bool
	}{
		{name: "matching label", inspectValue: owner, wantRemove: true},
		{name: "mismatched label", inspectValue: strings.Repeat("b", 64)},
		{name: "inspect failure", inspectErr: errors.New("inspect unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			created := false
			inspectCalled := false
			removeCalled := false
			cleanupLive := false
			cleanupHasDeadline := false
			var cleanupDeadline time.Time
			previousBind := bindImageValidationCommandRunner
			t.Cleanup(func() { bindImageValidationCommandRunner = previousBind })
			bindImageValidationCommandRunner = func(context.Context, CommandSpec, time.Duration) (commandRunner, error) {
				return func(spec CommandSpec, options RunOptions) error {
					if len(spec.Args) == 0 {
						return errors.New("empty Docker command")
					}
					switch spec.Args[0] {
					case "create":
						created = true
						cancel()
						return options.Context.Err()
					case "container":
						inspectCalled = true
						cleanupLive = options.Context.Err() == nil
						cleanupDeadline, cleanupHasDeadline = options.Context.Deadline()
						if test.inspectErr != nil {
							return test.inspectErr
						}
						if options.Stdout != nil {
							_, _ = options.Stdout.Write([]byte(test.inspectValue + "\n"))
						}
						return nil
					case "rm":
						removeCalled = true
						return nil
					default:
						return fmt.Errorf("unexpected Docker command: %#v", spec.Args)
					}
				}, nil
			}
			_, err := openImageValidationSessionWithCreateOwnedBounded(
				ctx, descriptor, workspace, nil, portableToolValidationCreateCommandSpecWithOwnershipV1(owner),
				newPortableToolProbeCleanupContext, portableToolProbeCleanupOwnershipVerifierV1(owner),
			)
			if err == nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled create error = %v, want context cancellation", err)
			}
			if test.inspectErr != nil && !errors.Is(err, test.inspectErr) {
				t.Fatalf("inspect failure error = %v, want %v", err, test.inspectErr)
			}
			if test.inspectErr == nil && test.inspectValue != owner && !strings.Contains(err.Error(), "not owned") {
				t.Fatalf("mismatched ownership error = %v, want ownership refusal", err)
			}
			if !created || !inspectCalled {
				t.Fatalf("setup cleanup calls: created=%t inspect=%t", created, inspectCalled)
			}
			if removeCalled != test.wantRemove {
				t.Fatalf("rm called = %t, want %t", removeCalled, test.wantRemove)
			}
			if !cleanupLive {
				t.Fatal("ownership inspection received an already-canceled context")
			}
			if !cleanupHasDeadline {
				t.Fatal("ownership inspection context has no explicit bound")
			}
			if remaining := time.Until(cleanupDeadline); remaining <= 0 || remaining > portableToolProbeCleanupTimeout {
				t.Fatalf("ownership inspection deadline remaining = %v, want (0, %v]", remaining, portableToolProbeCleanupTimeout)
			}
		})
	}
}

func TestRunPortableToolValidationProfileStartsFreshCleanupAfterProbeTimeout(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--timeout"}})
	previousContext := newPortableToolProbeContext
	t.Cleanup(func() { newPortableToolProbeContext = previousContext })
	newPortableToolProbeContext = func(parent context.Context) (context.Context, context.CancelFunc) {
		return context.WithTimeout(parent, 20*time.Millisecond)
	}
	cleanupStarted := make(chan struct{})
	var cleanupContextErr error
	var cleanupHasDeadline bool
	var probeTimedOut bool
	ownershipToken := ""
	previousBind := bindImageValidationCommandRunner
	t.Cleanup(func() { bindImageValidationCommandRunner = previousBind })
	bindImageValidationCommandRunner = func(context.Context, CommandSpec, time.Duration) (commandRunner, error) {
		return func(spec CommandSpec, options RunOptions) error {
			if len(spec.Args) == 0 {
				return nil
			}
			switch spec.Args[0] {
			case "create":
				ownershipToken = portableToolCreateOwnershipToken(t, spec.Args)
			case "exec":
				if portableToolIsPrimaryExec(spec.Args) {
					<-options.Context.Done()
					probeTimedOut = errors.Is(options.Context.Err(), context.DeadlineExceeded)
					return options.Context.Err()
				}
			case "container":
				if len(spec.Args) > 1 && spec.Args[1] == "inspect" && options.Stdout != nil {
					_, _ = options.Stdout.Write([]byte(ownershipToken + "\n"))
				}
			case "rm":
				cleanupContextErr = options.Context.Err()
				_, cleanupHasDeadline = options.Context.Deadline()
				close(cleanupStarted)
				return nil
			}
			return nil
		}, nil
	}
	started := time.Now()
	evidence, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("portable probe cleanup exceeded bounded lifecycle: %v", elapsed)
	}
	if got := evidence.Results[0].Outcome; got != PortableToolProbeOutcomeTimeoutV1 {
		t.Fatalf("timeout outcome = %q", got)
	}
	select {
	case <-cleanupStarted:
	default:
		t.Fatal("bounded cleanup was not started")
	}
	if !probeTimedOut {
		t.Fatal("probe did not reach its lifecycle deadline")
	}
	if cleanupContextErr != nil {
		t.Fatalf("fresh cleanup context was already canceled: %v", cleanupContextErr)
	}
	if !cleanupHasDeadline {
		t.Fatal("fresh cleanup context has no explicit deadline")
	}
	if got, want := evidence.Policy.CleanupTimeoutMillis, strconv.FormatInt(portableToolProbeCleanupTimeout.Milliseconds(), 10); got != want {
		t.Fatalf("cleanup policy bound = %q, want %q", got, want)
	}
}

func TestRunPortableToolValidationProfilePreCanceledContextDoesNotStartCleanup(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--timeout"}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rmStarted := false
	previousBind := bindImageValidationCommandRunner
	t.Cleanup(func() { bindImageValidationCommandRunner = previousBind })
	bindImageValidationCommandRunner = func(context.Context, CommandSpec, time.Duration) (commandRunner, error) {
		return func(spec CommandSpec, _ RunOptions) error {
			if len(spec.Args) > 0 && spec.Args[0] == "rm" {
				rmStarted = true
			}
			return nil
		}, nil
	}
	if _, err := RunPortableToolValidationProfile(ctx, descriptor, workspace, profile); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled probe error = %v, want context cancellation", err)
	}
	if rmStarted {
		t.Fatal("pre-canceled probe started cleanup")
	}
}

func TestRunPortableToolValidationProfileStalledCleanupReturnsInfrastructureError(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--timeout"}})
	previousContext := newPortableToolProbeContext
	t.Cleanup(func() { newPortableToolProbeContext = previousContext })
	newPortableToolProbeContext = func(parent context.Context) (context.Context, context.CancelFunc) {
		return context.WithTimeout(parent, 20*time.Millisecond)
	}
	previousCleanup := newPortableToolProbeCleanupContext
	t.Cleanup(func() { newPortableToolProbeCleanupContext = previousCleanup })
	newPortableToolProbeCleanupContext = func(context.Context) (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 20*time.Millisecond)
	}
	cleanupStarted := make(chan struct{})
	cleanupFinished := make(chan struct{})
	ownershipToken := ""
	previousBind := bindImageValidationCommandRunner
	t.Cleanup(func() { bindImageValidationCommandRunner = previousBind })
	bindImageValidationCommandRunner = func(context.Context, CommandSpec, time.Duration) (commandRunner, error) {
		return func(spec CommandSpec, options RunOptions) error {
			if len(spec.Args) == 0 {
				return nil
			}
			switch spec.Args[0] {
			case "create":
				ownershipToken = portableToolCreateOwnershipToken(t, spec.Args)
			case "exec":
				if portableToolIsPrimaryExec(spec.Args) {
					<-options.Context.Done()
					return options.Context.Err()
				}
			case "container":
				if len(spec.Args) > 1 && spec.Args[1] == "inspect" && options.Stdout != nil {
					_, _ = options.Stdout.Write([]byte(ownershipToken + "\n"))
				}
			case "rm":
				close(cleanupStarted)
				<-options.Context.Done()
				close(cleanupFinished)
				return options.Context.Err()
			}
			return nil
		}, nil
	}
	started := time.Now()
	if _, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile); err == nil || !strings.Contains(err.Error(), "remove image validation container") {
		t.Fatalf("stalled cleanup error = %v, want cleanup infrastructure error", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("stalled cleanup exceeded explicit bound: %v", elapsed)
	}
	select {
	case <-cleanupStarted:
	default:
		t.Fatal("stalled cleanup was not started")
	}
	select {
	case <-cleanupFinished:
	case <-time.After(time.Second):
		t.Fatal("stalled cleanup did not observe its bound")
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

func TestPortableToolProbeEvidenceDigestBindsExactValidationProfile(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--version"}})
	stubPortableToolProbeCommands(t, []portableToolProbeStubResponse{{stdout: []byte("demo 1.2.3\n")}})
	evidence, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile)
	if err != nil {
		t.Fatal(err)
	}
	wantProfileDigest, err := toolcatalog.ValidationProfileDigestV1(profile)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Profile.ID != profile.ID || evidence.Profile.Digest != wantProfileDigest {
		t.Fatalf("evidence profile reference = %#v, want ID %q and digest %q", evidence.Profile, profile.ID, wantProfileDigest)
	}
	if _, err := PortableToolProbeEvidenceDigestV1(evidence); err != nil {
		t.Fatalf("executor-produced evidence was rejected: %v", err)
	}

	foreignProbe := toolcatalog.RecordProbeV1{Path: "/opt/foreign/bin/tool", Args: []string{"--version"}}
	foreignProfile := profile
	foreignProfile.Probes = []toolcatalog.RecordProbeV1{foreignProbe}
	foreignProfileDigest, err := toolcatalog.ValidationProfileDigestV1(foreignProfile)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*PortableToolProbeEvidenceV1)
	}{
		{
			name: "foreign-result-probe",
			mutate: func(mutated *PortableToolProbeEvidenceV1) {
				mutated.Results = append([]PortableToolProbeResultV1(nil), mutated.Results...)
				mutated.Results[0].Probe = foreignProbe
			},
		},
		{
			name: "foreign-profile-identity",
			mutate: func(mutated *PortableToolProbeEvidenceV1) {
				mutated.Profile.ID = "tool:demo/releases/1.2.3/validation/profiles/other"
			},
		},
		{
			name: "foreign-profile-digest",
			mutate: func(mutated *PortableToolProbeEvidenceV1) {
				mutated.Profile.Digest = foreignProfileDigest
			},
		},
		{
			name: "profile-definition-identity",
			mutate: func(mutated *PortableToolProbeEvidenceV1) {
				mutated.ProfileDefinition.ID = "tool:demo/releases/1.2.3/validation/profiles/other"
			},
		},
		{
			name: "profile-definition-probe",
			mutate: func(mutated *PortableToolProbeEvidenceV1) {
				mutated.ProfileDefinition.Probes = append([]toolcatalog.RecordProbeV1(nil), mutated.ProfileDefinition.Probes...)
				mutated.ProfileDefinition.Probes[0] = foreignProbe
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mutated := evidence
			test.mutate(&mutated)
			if _, err := PortableToolProbeEvidenceDigestV1(mutated); err == nil {
				t.Fatalf("evidence mutation was accepted: %#v", mutated)
			}
		})
	}
}

func TestRunPortableToolValidationProfileRecordsDescriptorIdentity(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--version"}})
	stubPortableToolProbeCommands(t, []portableToolProbeStubResponse{{stdout: []byte("demo 1.2.3\n")}})
	evidence, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile)
	if err != nil {
		t.Fatal(err)
	}
	wantSubject, err := deploy.RootFSSubject(descriptor.RootFSDiffIDs)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.SubjectRootFS != wantSubject {
		t.Fatalf("evidence subject rootfs = %q, want descriptor-derived %q", evidence.SubjectRootFS, wantSubject)
	}
	if evidence.Platform != descriptor.Platform {
		t.Fatalf("evidence platform = %#v, want descriptor platform %#v", evidence.Platform, descriptor.Platform)
	}
	if _, err := PortableToolProbeEvidenceDigestV1(evidence); err != nil {
		t.Fatalf("executor-produced descriptor identity evidence was rejected: %v", err)
	}
}

func TestPortableToolProbeEvidenceDigestSeparatesRootFSSubjects(t *testing.T) {
	firstDescriptor := testProbeImageDescriptor(t, "linux/amd64")
	secondDescriptor := firstDescriptor
	secondDescriptor.RootFSDiffIDs = []canonical.Digest{canonical.Digest("sha256:" + strings.Repeat("d", 64))}
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--version"}})
	workspace := testPreparedProbeWorkspace(t, firstDescriptor.Platform, t.TempDir())
	stubPortableToolProbeCommands(t, []portableToolProbeStubResponse{
		{stdout: []byte("demo 1.2.3\n")},
		{stdout: []byte("demo 1.2.3\n")},
	})
	var first, second PortableToolProbeEvidenceV1
	if !t.Run("first-rootfs", func(t *testing.T) {
		var err error
		first, err = RunPortableToolValidationProfile(context.Background(), firstDescriptor, workspace, profile)
		if err != nil {
			t.Fatal(err)
		}
	}) {
		t.FailNow()
	}
	if !t.Run("second-rootfs", func(t *testing.T) {
		var err error
		second, err = RunPortableToolValidationProfile(context.Background(), secondDescriptor, workspace, profile)
		if err != nil {
			t.Fatal(err)
		}
	}) {
		t.FailNow()
	}
	if first.SubjectRootFS == second.SubjectRootFS {
		t.Fatalf("descriptor-derived rootfs subjects are equal: %q", first.SubjectRootFS)
	}
	if !reflect.DeepEqual(first.Results, second.Results) {
		t.Fatalf("identical profile output produced different results: %#v / %#v", first.Results, second.Results)
	}
	firstDigest, err := PortableToolProbeEvidenceDigestV1(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := PortableToolProbeEvidenceDigestV1(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == secondDigest {
		t.Fatalf("evidence digest ignored rootfs subject %q: %q", first.SubjectRootFS, firstDigest)
	}
}

func TestPortableToolProbeEvidenceDigestRejectsMalformedDescriptorIdentity(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--version"}})
	stubPortableToolProbeCommands(t, []portableToolProbeStubResponse{{stdout: []byte("demo 1.2.3\n")}})
	evidence, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PortableToolProbeEvidenceDigestV1(evidence); err != nil {
		t.Fatalf("executor-produced descriptor identity evidence was rejected: %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*PortableToolProbeEvidenceV1)
	}{
		{
			name: "subject-rootfs",
			mutate: func(mutated *PortableToolProbeEvidenceV1) {
				mutated.SubjectRootFS = canonical.Digest("not-a-canonical-rootfs-subject")
			},
		},
		{
			name: "platform",
			mutate: func(mutated *PortableToolProbeEvidenceV1) {
				mutated.Platform = blueprint.Platform{OS: "linux", Architecture: "amd64", Canonical: "linux/arm64"}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mutated := evidence
			test.mutate(&mutated)
			if _, err := PortableToolProbeEvidenceDigestV1(mutated); err == nil {
				t.Fatalf("malformed descriptor identity was accepted: %#v", mutated)
			}
		})
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
		{stderr: []byte("failed\n"), exitStatus: "7"},
		{stderr: []byte("timed out\n"), waitForContext: true},
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
		return context.WithTimeout(parent, 10*time.Millisecond)
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

func TestPortableToolProbeEvidenceDigestValidatesExitCodeRange(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--exit"}})
	for _, test := range []struct {
		name       string
		code       string
		wantReject bool
	}{
		{name: "one", code: "1"},
		{name: "maximum", code: "255"},
		{name: "256", code: "256", wantReject: true},
		{name: "999", code: "999", wantReject: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			stubPortableToolProbeCommands(t, []portableToolProbeStubResponse{{exitStatus: "1"}})
			evidence, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile)
			if err != nil {
				t.Fatal(err)
			}
			evidence.Results[0].ExitCode = &test.code
			_, err = PortableToolProbeEvidenceDigestV1(evidence)
			if test.wantReject {
				if err == nil {
					t.Fatalf("exit code %q was accepted", test.code)
				}
			} else if err != nil {
				t.Fatalf("valid exit code %q was rejected: %v", test.code, err)
			}
		})
	}
}

func TestRunPortableToolValidationProfileTreatsPrimaryExitErrorAsInfrastructure(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--exit"}})
	exitErr := portableToolExitError(t, 23)
	wrappedExitErr := fmt.Errorf("primary Docker exec failed: %w", exitErr)
	stubPortableToolProbeCommands(t, []portableToolProbeStubResponse{{err: wrappedExitErr}})
	evidence, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile)
	if err == nil || !errors.Is(err, exitErr) {
		t.Fatalf("primary exec error = %v, want wrapped Docker exit error", err)
	}
	if !reflect.DeepEqual(evidence, PortableToolProbeEvidenceV1{}) {
		t.Fatalf("primary exec failure returned probe evidence: %#v", evidence)
	}
}

func TestRunPortableToolValidationProfileRejectsMalformedOrMissingTrustedExitStatus(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--status"}})
	for _, test := range []struct {
		name     string
		response portableToolProbeStubResponse
	}{
		{name: "malformed", response: portableToolProbeStubResponse{exitStatusOutput: []byte("07\n")}},
		{name: "missing", response: portableToolProbeStubResponse{omitExitStatus: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			stubPortableToolProbeCommands(t, []portableToolProbeStubResponse{test.response})
			evidence, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile)
			if err == nil || !strings.Contains(err.Error(), "trusted portable-tool exit status") {
				t.Fatalf("trusted status error = %v, want infrastructure error", err)
			}
			if !reflect.DeepEqual(evidence, PortableToolProbeEvidenceV1{}) {
				t.Fatalf("malformed trusted status returned probe evidence: %#v", evidence)
			}
		})
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

func TestPortableToolProbeEvidenceDigestRejectsTruncatedOutputSizeOverflow(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--output"}})
	stubPortableToolProbeCommands(t, []portableToolProbeStubResponse{{
		stdout: bytes.Repeat([]byte("x"), portableToolProbeOutputLimit+1),
	}})
	evidence, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile)
	if err != nil {
		t.Fatal(err)
	}
	if !evidence.Results[0].Stdout.Truncated {
		t.Fatal("fixture did not produce valid truncated output evidence")
	}
	evidence.Results[0].Stdout.Size = "18446744073709551616"
	if _, err := PortableToolProbeEvidenceDigestV1(evidence); err == nil {
		t.Fatal("evidence digest accepted a truncated output size greater than uint64 max")
	}
}

func TestPortableToolProbeEvidenceDigestRequiresExactTruncatedOutputSentinel(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--output"}})
	stubPortableToolProbeCommands(t, []portableToolProbeStubResponse{{
		stdout: bytes.Repeat([]byte("x"), portableToolProbeOutputLimit+1),
	}})
	evidence, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := evidence.Results[0].Stdout.Size, strconv.Itoa(portableToolProbeOutputLimit+1); got != want {
		t.Fatalf("executor observed size = %q, want sentinel %q", got, want)
	}
	if _, err := PortableToolProbeEvidenceDigestV1(evidence); err != nil {
		t.Fatalf("executor-produced truncated sentinel was rejected: %v", err)
	}
	for _, size := range []string{"70000", strconv.Itoa(portableToolProbeOutputLimit + 2)} {
		mutated := evidence
		mutated.Results = append([]PortableToolProbeResultV1(nil), evidence.Results...)
		mutated.Results[0].Stdout.Size = size
		if _, err := PortableToolProbeEvidenceDigestV1(mutated); err == nil {
			t.Fatalf("truncated observed size %q was accepted", size)
		}
	}
}

func TestPortableToolProbeEvidenceDigestRejectsTruncatedOutputDigestTampering(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--output"}})
	stubPortableToolProbeCommands(t, []portableToolProbeStubResponse{{
		stdout: bytes.Repeat([]byte("x"), portableToolProbeOutputLimit+1),
	}})
	evidence, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile)
	if err != nil {
		t.Fatal(err)
	}
	if !evidence.Results[0].Stdout.Truncated {
		t.Fatal("fixture did not produce valid truncated output evidence")
	}
	if _, err := PortableToolProbeEvidenceDigestV1(evidence); err != nil {
		t.Fatalf("executor-produced truncated evidence was rejected: %v", err)
	}
	evidence.Results[0].Stdout.SHA256 = canonical.Digest("sha256:" + strings.Repeat("0", 64))
	if _, err := PortableToolProbeEvidenceDigestV1(evidence); err == nil {
		t.Fatal("truncated evidence with a tampered SHA256 was accepted")
	}
}

func TestPortableToolProbeEvidenceDigestRejectsOutputLimitExitCode(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--output"}})
	stubPortableToolProbeCommands(t, []portableToolProbeStubResponse{{
		stdout: bytes.Repeat([]byte("x"), portableToolProbeOutputLimit+1),
	}})
	evidence, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Results[0].ExitCode != nil {
		t.Fatalf("executor-produced output-limit evidence has exit code %q, want null", *evidence.Results[0].ExitCode)
	}
	if _, err := PortableToolProbeEvidenceDigestV1(evidence); err != nil {
		t.Fatalf("executor-produced output-limit evidence was rejected: %v", err)
	}
	for _, code := range []string{"0", "255", "999"} {
		mutated := evidence
		mutated.Results = append([]PortableToolProbeResultV1(nil), evidence.Results...)
		mutated.Results[0].ExitCode = &code
		if _, err := PortableToolProbeEvidenceDigestV1(mutated); err == nil {
			t.Fatalf("output-limit evidence with exit code %q was accepted", code)
		}
	}
}

func TestRunPortableToolValidationProfileClassifiesOutputLimitWithSignalExit(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--output"}})
	signalErr := portableToolSignalExitError(t)
	wrappedSignalErr := fmt.Errorf("probe terminated by signal: %w", signalErr)
	stubPortableToolProbeCommands(t, []portableToolProbeStubResponse{{
		stdout: bytes.Repeat([]byte("x"), portableToolProbeOutputLimit+1), err: wrappedSignalErr,
	}})
	evidence, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile)
	if err != nil {
		t.Fatalf("signal-terminated output-limit probe error = %v, want evidence", err)
	}
	result := portableToolProbeResults(t, evidence)[0]
	if got := portableToolStringField(t, result, "Outcome"); got != PortableToolProbeOutcomeOutputLimitV1 {
		t.Fatalf("signal-terminated output-limit outcome = %q, want %q", got, PortableToolProbeOutcomeOutputLimitV1)
	}
	if got := portableToolNullableStringField(t, result, "ExitCode"); got != "" {
		t.Fatalf("signal-terminated output-limit exit code = %q, want null/empty", got)
	}
	portableToolAssertBoundedOutput(t, result, "Stdout", bytes.Repeat([]byte("x"), portableToolProbeOutputLimit+1))
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
	infraErr := errors.New("Docker transport failed")
	stubPortableToolProbeCommands(t, []portableToolProbeStubResponse{{
		stdout: bytes.Repeat([]byte("x"), portableToolProbeOutputLimit+1), err: infraErr,
	}})
	evidence, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile)
	if err == nil || !errors.Is(err, infraErr) {
		t.Fatalf("infrastructure error = %v, want Docker transport failure", err)
	}
	if !reflect.DeepEqual(evidence, PortableToolProbeEvidenceV1{}) {
		t.Fatalf("infrastructure failure returned output-limit evidence: %#v", evidence)
	}
}

type portableToolProbeStubResponse struct {
	stdout           []byte
	stderr           []byte
	err              error
	exitStatus       string
	exitStatusOutput []byte
	exitStatusStderr []byte
	exitStatusErr    error
	omitExitStatus   bool
	waitForContext   bool
	contextCanceled  *bool
}

func stubPortableToolProbeCommands(t *testing.T, responses []portableToolProbeStubResponse) *[]CommandSpec {
	t.Helper()
	previous := bindImageValidationCommandRunner
	commands := []CommandSpec{}
	probeIndex := 0
	ownershipToken := ""
	t.Cleanup(func() { bindImageValidationCommandRunner = previous })
	bindImageValidationCommandRunner = func(context.Context, CommandSpec, time.Duration) (commandRunner, error) {
		return func(spec CommandSpec, options RunOptions) error {
			commands = append(commands, spec)
			if len(spec.Args) == 0 {
				return fmt.Errorf("empty portable probe command")
			}
			if spec.Args[0] == "create" {
				ownershipToken = portableToolCreateOwnershipToken(t, spec.Args)
				return nil
			}
			if spec.Args[0] == "start" {
				return nil
			}
			if len(spec.Args) > 1 && spec.Args[0] == "container" && spec.Args[1] == "inspect" {
				if options.Stdout != nil {
					_, _ = options.Stdout.Write([]byte(ownershipToken + "\n"))
				}
				return nil
			}
			if spec.Args[0] == "rm" {
				return nil
			}
			if spec.Args[0] != "exec" {
				return fmt.Errorf("unexpected portable probe command: %#v", spec.Args)
			}
			if portableToolIsExitStatusExec(spec.Args) {
				if probeIndex == 0 || probeIndex > len(responses) {
					return fmt.Errorf("unexpected portable probe status exec %d: %#v", probeIndex, spec.Args)
				}
				response := responses[probeIndex-1]
				if response.omitExitStatus {
					return response.exitStatusErr
				}
				statusOutput := response.exitStatusOutput
				if statusOutput == nil {
					exitStatus := response.exitStatus
					if exitStatus == "" {
						exitStatus = "0"
					}
					statusOutput = []byte(exitStatus + "\n")
				}
				if options.Stdout != nil {
					_, _ = options.Stdout.Write(statusOutput)
				}
				if options.Stderr != nil {
					_, _ = options.Stderr.Write(response.exitStatusStderr)
				}
				return response.exitStatusErr
			}
			if !portableToolIsPrimaryExec(spec.Args) {
				return fmt.Errorf("unexpected portable probe exec: %#v", spec.Args)
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
			if response.waitForContext {
				<-options.Context.Done()
				return options.Context.Err()
			}
			return response.err
		}, nil
	}
	return &commands
}

func portableToolIsPrimaryExec(args []string) bool {
	for _, argument := range args {
		if argument == "--record-exit-status" {
			return true
		}
	}
	return false
}

func portableToolIsExitStatusExec(args []string) bool {
	return len(args) > 0 && args[len(args)-1] == "read-portable-tool-exit-status-v1"
}

func portableToolCreateOwnershipToken(t *testing.T, args []string) string {
	t.Helper()
	label := portableToolCommandValue(t, args, "--label")
	prefix := portableToolProbeOwnershipLabelV1 + "="
	if !strings.HasPrefix(label, prefix) {
		t.Fatalf("portable probe create ownership label = %q, want %s=<64 lowercase hex>", label, prefix)
	}
	token := strings.TrimPrefix(label, prefix)
	if !canonicalPortableToolProbeOwnershipTokenV1(token) {
		t.Fatalf("portable probe create ownership token = %q, want 64 lowercase hex characters", token)
	}
	return token
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

func portableToolSignalExitError(t *testing.T) error {
	t.Helper()
	err := exec.Command("sh", "-c", "kill -9 $$").Run()
	if err == nil {
		t.Fatal("signal helper unexpectedly succeeded")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != -1 {
		t.Fatalf("signal helper error = %T/%v, want *exec.ExitError with exit code -1", err, err)
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

func portableToolCommandValue(t *testing.T, args []string, flag string) string {
	t.Helper()
	for index := 0; index+1 < len(args); index++ {
		if args[index] == flag {
			return args[index+1]
		}
	}
	t.Fatalf("command %#v does not contain %s with a value", args, flag)
	return ""
}

func portableToolExpectedCPULimitDockerValue() string {
	whole := portableToolProbeCPULimitMillis / 1000
	fraction := portableToolProbeCPULimitMillis % 1000
	if fraction == 0 {
		return strconv.Itoa(whole)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%d.%03d", whole, fraction), "0"), ".")
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
	wantDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(decoded))
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
