package dockerdeploy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"os/exec"
	"path"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	reployprobe "github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/toolcatalog"
)

const (
	PortableToolProbeEvidenceSchemaV1 = "portable-tool-probe-evidence-v1"
	PortableToolProbeExecutorV1       = "portable-tool-probe-executor-v1"

	PortableToolProbeOutcomePassV1        = "pass"
	PortableToolProbeOutcomeExitV1        = "exit-failure"
	PortableToolProbeOutcomeTimeoutV1     = "timeout"
	PortableToolProbeOutcomeOutputLimitV1 = "output-limit"

	portableToolProbeWorkdir        = "/"
	portableToolProbeTimeout        = 2 * time.Minute
	portableToolProbeCleanupTimeout = 5 * time.Second
	portableToolProbeOutputLimit    = 64 << 10
	portableToolProbeMemoryLimit    = 1 << 30
	portableToolProbeTemporaryLimit = 128 << 20
	portableToolProbePIDLimit       = 512
	portableToolProbeCPULimitMillis = 2000
	portableToolProbeOpenFilesLimit = 1024
	portableToolProbeUnprivilegedID = 65532

	portableToolProbeOwnershipLabelV1 = "io.reploy.portable-tool-probe.ownership-v1"
)

var errPortableToolProbeOutputLimit = errors.New("portable-tool probe output limit exceeded")

var newPortableToolProbeContext = func(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, portableToolProbeTimeout)
}

var newPortableToolProbeCleanupContext = func(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), portableToolProbeCleanupTimeout)
}

type PortableToolProbePolicyV1 struct {
	NetworkDisabled      bool                                      `json:"network_disabled"`
	WorkingDirectory     string                                    `json:"working_directory"`
	Environment          []toolcatalog.RecordEnvironmentVariableV1 `json:"environment"`
	TimeoutMillis        string                                    `json:"timeout_milliseconds"`
	CleanupTimeoutMillis string                                    `json:"cleanup_timeout_milliseconds"`
	OutputLimitBytes     string                                    `json:"output_limit_bytes"`
	MemoryLimitBytes     string                                    `json:"memory_limit_bytes"`
	TemporaryBytes       string                                    `json:"temporary_bytes"`
	PIDLimit             string                                    `json:"pid_limit"`
	CPULimitMillis       string                                    `json:"cpu_limit_millis"`
	OpenFilesLimit       string                                    `json:"open_files_limit"`
}

type PortableToolProbeOutputV1 struct {
	Size      string           `json:"size"`
	SHA256    canonical.Digest `json:"sha256"`
	Content   string           `json:"content_base64"`
	Truncated bool             `json:"truncated"`
}

type PortableToolProbeResultV1 struct {
	Probe    toolcatalog.RecordProbeV1 `json:"probe"`
	Outcome  string                    `json:"outcome"`
	ExitCode *string                   `json:"exit_code"`
	Stdout   PortableToolProbeOutputV1 `json:"stdout"`
	Stderr   PortableToolProbeOutputV1 `json:"stderr"`
}

type PortableToolProbeEvidenceV1 struct {
	Schema          string                        `json:"schema"`
	ExecutorVersion string                        `json:"executor_version"`
	Profile         toolcatalog.RecordReferenceV1 `json:"profile"`
	Policy          PortableToolProbePolicyV1     `json:"policy"`
	Results         []PortableToolProbeResultV1   `json:"results"`
}

// RunPortableToolValidationProfile executes every exact profile probe through
// one fixed Reploy-owned policy. Each probe receives a fresh read-only,
// networkless container so timeout cleanup cannot leak work into another
// observation. Process outcomes are evidence; Docker or cleanup failures are
// infrastructure errors.
func RunPortableToolValidationProfile(
	ctx context.Context,
	descriptor deploy.ImageDescriptor,
	workspace PreparedProbeWorkspace,
	profile toolcatalog.ValidationProfileRecordV1,
) (PortableToolProbeEvidenceV1, error) {
	if ctx == nil {
		return PortableToolProbeEvidenceV1{}, fmt.Errorf("portable-tool probe context is required")
	}
	if err := ctx.Err(); err != nil {
		return PortableToolProbeEvidenceV1{}, err
	}
	profileDigest, err := toolcatalog.ValidationProfileDigestV1(profile)
	if err != nil {
		return PortableToolProbeEvidenceV1{}, err
	}
	evidence := PortableToolProbeEvidenceV1{
		Schema:          PortableToolProbeEvidenceSchemaV1,
		ExecutorVersion: PortableToolProbeExecutorV1,
		Profile:         toolcatalog.RecordReferenceV1{ID: profile.ID, Digest: profileDigest},
		Policy:          fixedPortableToolProbePolicyV1(),
		Results:         make([]PortableToolProbeResultV1, 0, len(profile.Probes)),
	}
	for _, declared := range profile.Probes {
		result, err := runPortableToolProbe(ctx, descriptor, workspace, declared)
		if err != nil {
			return PortableToolProbeEvidenceV1{}, err
		}
		evidence.Results = append(evidence.Results, result)
	}
	if _, err := PortableToolProbeEvidenceDigestV1(evidence); err != nil {
		return PortableToolProbeEvidenceV1{}, err
	}
	return evidence, nil
}

func runPortableToolProbe(
	ctx context.Context,
	descriptor deploy.ImageDescriptor,
	workspace PreparedProbeWorkspace,
	declared toolcatalog.RecordProbeV1,
) (result PortableToolProbeResultV1, resultErr error) {
	probeCtx, cancel := newPortableToolProbeContext(ctx)
	defer cancel()
	ownershipToken, err := newPortableToolProbeOwnershipTokenV1()
	if err != nil {
		return PortableToolProbeResultV1{}, err
	}
	session, err := openImageValidationSessionWithCreateOwnedBounded(
		probeCtx, descriptor, workspace, nil,
		portableToolValidationCreateCommandSpecWithOwnershipV1(ownershipToken),
		newPortableToolProbeCleanupContext,
		portableToolProbeCleanupOwnershipVerifierV1(ownershipToken),
	)
	if err != nil {
		return PortableToolProbeResultV1{}, err
	}
	defer func() {
		cleanupCtx, cancelCleanup := newPortableToolProbeCleanupContext(ctx)
		closeErr := session.Close(cleanupCtx)
		cancelCleanup()
		if closeErr != nil {
			result = PortableToolProbeResultV1{}
			resultErr = errors.Join(resultErr, closeErr)
		}
	}()
	executionCtx, cancelExecution := context.WithCancelCause(probeCtx)
	defer cancelExecution(nil)
	cancelForOutputLimit := func() { cancelExecution(errPortableToolProbeOutputLimit) }
	stdout := newBoundedPortableToolProbeOutput(portableToolProbeOutputLimit, cancelForOutputLimit)
	stderr := newBoundedPortableToolProbeOutput(portableToolProbeOutputLimit, cancelForOutputLimit)
	args := []string{
		"exec", "--user", "0:0", "--workdir", portableToolProbeWorkdir,
		session.containerName, session.workspace.ContainerExecutable, "restricted-exec",
		"--uid", strconv.Itoa(portableToolProbeUnprivilegedID),
		"--gid", strconv.Itoa(portableToolProbeUnprivilegedID),
		"--groups", "",
		"--environment-profile", reployprobe.PortableToolEnvironmentProfileV1,
		"--record-exit-status",
		"--", declared.Path,
	}
	args = append(args, declared.Args...)
	runErr := session.runDockerCommand(
		CommandSpec{Name: "docker", Args: args},
		RunOptions{Context: executionCtx, Stdout: stdout, Stderr: stderr},
	)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return PortableToolProbeResultV1{}, ctxErr
	}
	result = PortableToolProbeResultV1{
		Probe: clonePortableToolProbeDeclarationV1(declared), Stdout: stdout.Evidence(), Stderr: stderr.Evidence(),
	}
	exitCode := 0
	exitCodeRead := false
	var statusErr error
	if runErr == nil && executionCtx.Err() == nil {
		exitCode, statusErr = readPortableToolProbeExitStatusV1(executionCtx, session)
		exitCodeRead = statusErr == nil
	}
	var exitErr *exec.ExitError
	switch {
	case errors.Is(probeCtx.Err(), context.DeadlineExceeded):
		result.Outcome = PortableToolProbeOutcomeTimeoutV1
	case errors.Is(context.Cause(executionCtx), errPortableToolProbeOutputLimit):
		if runErr != nil {
			if !errors.As(runErr, &exitErr) || exitErr.ExitCode() >= 0 {
				return PortableToolProbeResultV1{}, fmt.Errorf("execute portable-tool probe: %w", runErr)
			}
		}
		result.Outcome = PortableToolProbeOutcomeOutputLimitV1
	case runErr != nil:
		return PortableToolProbeResultV1{}, fmt.Errorf("execute portable-tool probe: %w", runErr)
	case statusErr != nil:
		return PortableToolProbeResultV1{}, statusErr
	case !exitCodeRead:
		return PortableToolProbeResultV1{}, fmt.Errorf("portable-tool probe produced no trusted exit status")
	case exitCode != 0:
		value := strconv.Itoa(exitCode)
		result.ExitCode = &value
		result.Outcome = PortableToolProbeOutcomeExitV1
	default:
		zero := "0"
		result.ExitCode = &zero
		result.Outcome = PortableToolProbeOutcomePassV1
	}
	return result, nil
}

func readPortableToolProbeExitStatusV1(ctx context.Context, session *ImageValidationSession) (int, error) {
	stdout := newBoundedPortableToolProbeOutput(16, nil)
	stderr := newBoundedPortableToolProbeOutput(4096, nil)
	spec := CommandSpec{Name: "docker", Args: []string{
		"exec", "--user", "0:0", "--workdir", portableToolProbeWorkdir,
		session.containerName, session.workspace.ContainerExecutable,
		"read-portable-tool-exit-status-v1",
	}}
	if err := session.runDockerCommand(spec, RunOptions{Context: ctx, Stdout: stdout, Stderr: stderr}); err != nil {
		return 0, portableToolProbeHelperCommandErrorV1("read trusted exit status", stderr, err)
	}
	if stdout.Truncated() || stderr.Truncated() {
		return 0, fmt.Errorf("read trusted portable-tool exit status returned oversized output")
	}
	if stderr.content.Len() != 0 {
		return 0, fmt.Errorf("read trusted portable-tool exit status returned unexpected stderr: %s", trimmedCommandOutput(stderr.content.String()))
	}
	content := stdout.content.Bytes()
	if len(content) < 2 || len(content) > 4 || content[len(content)-1] != '\n' {
		return 0, fmt.Errorf("trusted portable-tool exit status is not one canonical line")
	}
	value := string(content[:len(content)-1])
	if !canonicalProbeDecimal(value) {
		return 0, fmt.Errorf("trusted portable-tool exit status is not canonical decimal")
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 || parsed > 255 {
		return 0, fmt.Errorf("trusted portable-tool exit status is outside 0..255")
	}
	return parsed, nil
}

func portableToolProbeHelperCommandErrorV1(operation string, stderr *boundedPortableToolProbeOutput, err error) error {
	message := trimmedCommandOutput(stderr.content.String())
	if stderr.Truncated() {
		message += "\n[truncated]"
	}
	if message == "" {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s: %w\ncommand output:\n%s", operation, err, message)
}

func newPortableToolProbeOwnershipTokenV1() (string, error) {
	content := make([]byte, 32)
	if _, err := rand.Read(content); err != nil {
		return "", fmt.Errorf("generate portable-tool probe ownership token: %w", err)
	}
	return hex.EncodeToString(content), nil
}

func portableToolValidationCreateCommandSpecWithOwnershipV1(token string) imageValidationCreateSpec {
	return func(
		descriptor deploy.ImageDescriptor,
		workspace PreparedProbeWorkspace,
		aptWorkspace *PreparedAPTResolverWorkspace,
	) (CommandSpec, string, error) {
		if !canonicalPortableToolProbeOwnershipTokenV1(token) {
			return CommandSpec{}, "", fmt.Errorf("portable-tool probe ownership token is invalid")
		}
		spec, name, err := portableToolValidationCreateCommandSpec(descriptor, workspace, aptWorkspace)
		if err != nil {
			return CommandSpec{}, "", err
		}
		entrypoint := -1
		for index, argument := range spec.Args {
			if argument == "--entrypoint" {
				entrypoint = index
				break
			}
		}
		if entrypoint < 0 {
			return CommandSpec{}, "", fmt.Errorf("portable-tool probe create command has no fixed entrypoint")
		}
		label := portableToolProbeOwnershipLabelV1 + "=" + token
		args := make([]string, 0, len(spec.Args)+2)
		args = append(args, spec.Args[:entrypoint]...)
		args = append(args, "--label", label)
		args = append(args, spec.Args[entrypoint:]...)
		spec.Args = args
		return spec, name, nil
	}
}

func portableToolProbeCleanupOwnershipVerifierV1(token string) imageValidationCleanupOwnershipVerifier {
	return func(ctx context.Context, containerName string, runDocker commandRunner) error {
		if !canonicalPortableToolProbeOwnershipTokenV1(token) {
			return fmt.Errorf("portable-tool probe ownership token is invalid")
		}
		stdout := newBoundedPortableToolProbeOutput(80, nil)
		stderr := newBoundedPortableToolProbeOutput(4096, nil)
		template := `{{index .Config.Labels "` + portableToolProbeOwnershipLabelV1 + `"}}`
		spec := CommandSpec{Name: "docker", Args: []string{
			"container", "inspect", "--format", template, containerName,
		}}
		if err := runDocker(spec, RunOptions{Context: ctx, Stdout: stdout, Stderr: stderr}); err != nil {
			return portableToolProbeHelperCommandErrorV1("verify portable-tool probe container ownership", stderr, err)
		}
		if stdout.Truncated() || stderr.Truncated() || stderr.content.Len() != 0 {
			return fmt.Errorf("verify portable-tool probe container ownership returned invalid output")
		}
		if stdout.content.String() != token+"\n" {
			return fmt.Errorf("container %s is not owned by this portable-tool probe attempt", containerName)
		}
		return nil
	}
}

func canonicalPortableToolProbeOwnershipTokenV1(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func portableToolValidationCreateCommandSpec(
	descriptor deploy.ImageDescriptor,
	workspace PreparedProbeWorkspace,
	aptWorkspace *PreparedAPTResolverWorkspace,
) (CommandSpec, string, error) {
	if aptWorkspace != nil {
		return CommandSpec{}, "", fmt.Errorf("portable-tool probes do not accept an APT workspace")
	}
	spec, name, err := imageValidationCreateCommandSpec(descriptor, workspace)
	if err != nil {
		return CommandSpec{}, "", err
	}
	resourceArgs := []string{
		"--cap-drop", "ALL",
		"--cap-add", "SETPCAP",
		"--cap-add", "SETUID",
		"--cap-add", "SETGID",
		"--security-opt", "no-new-privileges=true",
		"--memory", strconv.Itoa(portableToolProbeMemoryLimit),
		"--memory-swap", strconv.Itoa(portableToolProbeMemoryLimit),
		"--pids-limit", strconv.Itoa(portableToolProbePIDLimit),
		"--cpus", portableToolProbeCPULimitDockerValueV1(),
		"--ulimit", fmt.Sprintf("nofile=%d:%d", portableToolProbeOpenFilesLimit, portableToolProbeOpenFilesLimit),
		"--tmpfs", fmt.Sprintf("/tmp:rw,noexec,nosuid,nodev,size=%d,mode=1777", portableToolProbeTemporaryLimit),
	}
	entrypoint := -1
	for index, argument := range spec.Args {
		if argument == "--entrypoint" {
			entrypoint = index
			break
		}
	}
	if entrypoint < 0 {
		return CommandSpec{}, "", fmt.Errorf("portable-tool probe create command has no fixed entrypoint")
	}
	args := make([]string, 0, len(spec.Args)+len(resourceArgs))
	args = append(args, spec.Args[:entrypoint]...)
	args = append(args, resourceArgs...)
	args = append(args, spec.Args[entrypoint:]...)
	spec.Args = args
	return spec, name, nil
}

func portableToolProbeCPULimitDockerValueV1() string {
	whole := portableToolProbeCPULimitMillis / 1000
	fraction := portableToolProbeCPULimitMillis % 1000
	if fraction == 0 {
		return strconv.Itoa(whole)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%d.%03d", whole, fraction), "0"), ".")
}

func fixedPortableToolProbePolicyV1() PortableToolProbePolicyV1 {
	return PortableToolProbePolicyV1{
		NetworkDisabled: true, WorkingDirectory: portableToolProbeWorkdir,
		Environment:          fixedPortableToolProbeEnvironmentV1(),
		TimeoutMillis:        strconv.FormatInt(portableToolProbeTimeout.Milliseconds(), 10),
		CleanupTimeoutMillis: strconv.FormatInt(portableToolProbeCleanupTimeout.Milliseconds(), 10),
		OutputLimitBytes:     strconv.Itoa(portableToolProbeOutputLimit),
		MemoryLimitBytes:     strconv.Itoa(portableToolProbeMemoryLimit),
		TemporaryBytes:       strconv.Itoa(portableToolProbeTemporaryLimit),
		PIDLimit:             strconv.Itoa(portableToolProbePIDLimit),
		CPULimitMillis:       strconv.Itoa(portableToolProbeCPULimitMillis),
		OpenFilesLimit:       strconv.Itoa(portableToolProbeOpenFilesLimit),
	}
}

func fixedPortableToolProbeEnvironmentV1() []toolcatalog.RecordEnvironmentVariableV1 {
	variables := reployprobe.PortableToolEnvironmentV1()
	result := make([]toolcatalog.RecordEnvironmentVariableV1, len(variables))
	for index, variable := range variables {
		result[index] = toolcatalog.RecordEnvironmentVariableV1{Name: variable.Name, Value: variable.Value}
	}
	return result
}

func clonePortableToolProbeDeclarationV1(declared toolcatalog.RecordProbeV1) toolcatalog.RecordProbeV1 {
	declared.Args = append([]string(nil), declared.Args...)
	return declared
}

// PortableToolProbeEvidenceDigestV1 validates and identifies canonical probe
// observations. It does not turn them into external support evidence.
func PortableToolProbeEvidenceDigestV1(evidence PortableToolProbeEvidenceV1) (canonical.Digest, error) {
	if err := validatePortableToolProbeEvidenceV1(evidence); err != nil {
		return "", err
	}
	return canonical.Sum("portable-tool-probe-evidence", PortableToolProbeEvidenceSchemaV1, evidence)
}

func validatePortableToolProbeEvidenceV1(evidence PortableToolProbeEvidenceV1) error {
	if evidence.Schema != PortableToolProbeEvidenceSchemaV1 || evidence.ExecutorVersion != PortableToolProbeExecutorV1 {
		return fmt.Errorf("portable-tool probe evidence identity is invalid")
	}
	if evidence.Profile.ID == "" || evidence.Profile.Digest.Validate() != nil {
		return fmt.Errorf("portable-tool probe evidence profile is invalid")
	}
	if !reflect.DeepEqual(evidence.Policy, fixedPortableToolProbePolicyV1()) {
		return fmt.Errorf("portable-tool probe evidence policy is not the fixed executor policy")
	}
	if len(evidence.Results) == 0 {
		return fmt.Errorf("portable-tool probe evidence results must use a nonempty array")
	}
	var previousProbe []byte
	for index, result := range evidence.Results {
		if !validPortableToolProbeDeclarationV1(result.Probe) {
			return fmt.Errorf("portable-tool probe result %d has an invalid declaration", index)
		}
		encodedProbe, err := canonical.Marshal(result.Probe)
		if err != nil {
			return fmt.Errorf("portable-tool probe result %d declaration: %w", index, err)
		}
		if index > 0 && bytes.Compare(previousProbe, encodedProbe) >= 0 {
			return fmt.Errorf("portable-tool probe results must be unique and profile ordered")
		}
		previousProbe = encodedProbe
		if err := validatePortableToolProbeOutputV1(result.Stdout); err != nil {
			return fmt.Errorf("portable-tool probe result %d stdout: %w", index, err)
		}
		if err := validatePortableToolProbeOutputV1(result.Stderr); err != nil {
			return fmt.Errorf("portable-tool probe result %d stderr: %w", index, err)
		}
		switch result.Outcome {
		case PortableToolProbeOutcomePassV1:
			if result.ExitCode == nil || *result.ExitCode != "0" || result.Stdout.Truncated || result.Stderr.Truncated {
				return fmt.Errorf("portable-tool probe result %d has invalid pass evidence", index)
			}
		case PortableToolProbeOutcomeExitV1:
			if result.ExitCode == nil || result.Stdout.Truncated || result.Stderr.Truncated {
				return fmt.Errorf("portable-tool probe result %d has invalid exit evidence", index)
			}
			exitCode, err := strconv.ParseUint(*result.ExitCode, 10, 8)
			if err != nil || exitCode == 0 || !canonicalProbeDecimal(*result.ExitCode) {
				return fmt.Errorf("portable-tool probe result %d has invalid exit evidence", index)
			}
		case PortableToolProbeOutcomeTimeoutV1:
			if result.ExitCode != nil {
				return fmt.Errorf("portable-tool probe result %d has an exit code after timeout", index)
			}
		case PortableToolProbeOutcomeOutputLimitV1:
			if !result.Stdout.Truncated && !result.Stderr.Truncated {
				return fmt.Errorf("portable-tool probe result %d has no exceeded output", index)
			}
			if result.ExitCode != nil && !canonicalProbeDecimal(*result.ExitCode) {
				return fmt.Errorf("portable-tool probe result %d has invalid output-limit exit evidence", index)
			}
		default:
			return fmt.Errorf("portable-tool probe result %d has invalid outcome", index)
		}
	}
	return nil
}

func validPortableToolProbeDeclarationV1(probe toolcatalog.RecordProbeV1) bool {
	if probe.Path == "" || !path.IsAbs(probe.Path) || path.Clean(probe.Path) != probe.Path || strings.Contains(probe.Path, `\`) || !utf8.ValidString(probe.Path) || probe.Args == nil {
		return false
	}
	for _, argument := range probe.Args {
		if !utf8.ValidString(argument) {
			return false
		}
		for _, char := range argument {
			if char < 0x20 || char == 0x7f {
				return false
			}
		}
	}
	return true
}

func validatePortableToolProbeOutputV1(output PortableToolProbeOutputV1) error {
	if !canonicalProbeDecimal(output.Size) || output.SHA256.Validate() != nil {
		return fmt.Errorf("size or digest is invalid")
	}
	size, err := strconv.ParseUint(output.Size, 10, 64)
	if err != nil {
		return fmt.Errorf("size is outside the supported range")
	}
	content, err := base64.StdEncoding.DecodeString(output.Content)
	if err != nil || len(content) > portableToolProbeOutputLimit {
		return fmt.Errorf("content is invalid or exceeds the evidence bound")
	}
	if output.Truncated {
		if len(content) != portableToolProbeOutputLimit || size != uint64(portableToolProbeOutputLimit+1) {
			return fmt.Errorf("truncated content does not match its bound")
		}
		return nil
	}
	if size != uint64(len(content)) || digestPortableToolProbeBytes(content) != output.SHA256 {
		return fmt.Errorf("content does not match its size and digest")
	}
	return nil
}

func canonicalProbeDecimal(value string) bool {
	if value == "0" {
		return true
	}
	if value == "" || value[0] == '0' {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

type boundedPortableToolProbeOutput struct {
	limit     int
	content   bytes.Buffer
	total     uint64
	digest    hash.Hash
	truncated bool
	onLimit   func()
}

func newBoundedPortableToolProbeOutput(limit int, onLimit func()) *boundedPortableToolProbeOutput {
	return &boundedPortableToolProbeOutput{limit: limit, digest: sha256.New(), onLimit: onLimit}
}

func (output *boundedPortableToolProbeOutput) Write(content []byte) (int, error) {
	accepted := content
	maximum := output.limit + 1 - int(output.total)
	if maximum < 0 {
		maximum = 0
	}
	if len(accepted) > maximum {
		accepted = accepted[:maximum]
	}
	_, _ = output.digest.Write(accepted)
	output.total += uint64(len(accepted))
	remaining := output.limit - output.content.Len()
	if remaining > 0 {
		stored := accepted
		if len(stored) > remaining {
			stored = stored[:remaining]
		}
		_, _ = output.content.Write(stored)
	}
	if output.total > uint64(output.limit) && !output.truncated {
		output.truncated = true
		if output.onLimit != nil {
			output.onLimit()
		}
	}
	return len(content), nil
}

func (output *boundedPortableToolProbeOutput) Truncated() bool { return output.truncated }

func (output *boundedPortableToolProbeOutput) Evidence() PortableToolProbeOutputV1 {
	return PortableToolProbeOutputV1{
		Size:      strconv.FormatUint(output.total, 10),
		SHA256:    canonical.Digest("sha256:" + hex.EncodeToString(output.digest.Sum(nil))),
		Content:   base64.StdEncoding.EncodeToString(output.content.Bytes()),
		Truncated: output.truncated,
	}
}

func digestPortableToolProbeBytes(content []byte) canonical.Digest {
	digest := sha256.Sum256(content)
	return canonical.Digest("sha256:" + hex.EncodeToString(digest[:]))
}
