package dockerdeploy

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

type DoctorOptions struct {
	Dir                    string
	Preinstall             bool
	Scope                  InstallScope
	Quiet                  bool
	SuppressWarnings       bool
	Stdout                 io.Writer
	DockerPreflightTimeout time.Duration
}

type DoctorFinding struct {
	Status  string
	Message string
}

func Doctor(options DoctorOptions) int {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	colors := doctorStatusColors(options.Stdout)
	if stdout, _, err := DeploymentOutputWriters(options.Dir, options.Stdout, nil); err == nil {
		options.Stdout = stdout
	}
	findings := doctorFindings(options.Dir, options.Preinstall, options.Scope, options.DockerPreflightTimeout)
	exitCode := 0
	for _, finding := range findings {
		if finding.Status == "fail" {
			exitCode = 1
		}
		if options.Stdout != nil && shouldPrintDoctorFinding(finding, options) {
			fmt.Fprintf(options.Stdout, "%s: %s\n", colors.status(finding.Status), finding.Message)
		}
	}
	return exitCode
}

func shouldPrintDoctorFinding(finding DoctorFinding, options DoctorOptions) bool {
	if options.Quiet && finding.Status == "ok" {
		return false
	}
	if options.SuppressWarnings && finding.Status == "warn" {
		return false
	}
	return true
}

type doctorColors struct{ enabled bool }

func doctorStatusColors(output io.Writer) doctorColors {
	return doctorColors{enabled: outputColorEnabled(output)}
}

func (colors doctorColors) status(status string) string {
	if !colors.enabled {
		return status
	}
	switch status {
	case "ok":
		return "\x1b[32mok\x1b[0m"
	case "fail":
		return "\x1b[31mfail\x1b[0m"
	case "warn":
		return "\x1b[33mwarn\x1b[0m"
	default:
		return status
	}
}

func outputColorEnabled(output io.Writer) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("REPLOY_COLOR"))) {
	case "always":
		return true
	case "never":
		return false
	}
	if os.Getenv("NO_COLOR") != "" || !terminalLooksColorCapable() {
		return false
	}
	return writerLooksTerminal(output)
}

type doctorFindingsBackendV1 struct {
	readFile func(string) ([]byte, error)
	acquire  func(context.Context, string) (*deploy.OperationLock, error)
}

func doctorFindings(dir string, preinstall bool, scope InstallScope, dockerPreflightTimeout time.Duration) []DoctorFinding {
	return doctorFindingsWithV1(dir, preinstall, scope, dockerPreflightTimeout, doctorFindingsBackendV1{
		readFile: os.ReadFile,
		acquire:  deploy.AcquireExistingOperationLock,
	})
}

func doctorFindingsWithV1(
	dir string,
	preinstall bool,
	scope InstallScope,
	dockerPreflightTimeout time.Duration,
	backend doctorFindingsBackendV1,
) (findings []DoctorFinding) {
	if backend.readFile == nil || backend.acquire == nil {
		return []DoctorFinding{{Status: "fail", Message: "doctor requires a complete inspection backend"}}
	}
	absoluteDir, err := filepath.Abs(dir)
	if err != nil {
		return []DoctorFinding{{Status: "fail", Message: fmt.Sprintf("cannot resolve deployment directory: %v", err)}}
	}
	dir = absoluteDir
	statePath := filepath.Join(dir, StateFileName)
	content, err := backend.readFile(statePath)
	if err != nil {
		return []DoctorFinding{{Status: "fail", Message: fmt.Sprintf("cannot read state: %v", err)}}
	}
	if _, err := deploy.DecodeStateV1(content); err != nil {
		return []DoctorFinding{{Status: "fail", Message: fmt.Sprintf("cannot decode state-v1: %v", err)}}
	}
	operation, err := backend.acquire(context.Background(), dir)
	if err != nil {
		return []DoctorFinding{{Status: "fail", Message: fmt.Sprintf("cannot lock deployment for inspection: %v", err)}}
	}
	defer func() {
		if err := operation.Unlock(); err != nil {
			findings = append(findings, DoctorFinding{Status: "fail", Message: fmt.Sprintf("cannot unlock deployment after inspection: %v", err)})
		}
	}()
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return []DoctorFinding{{Status: "fail", Message: fmt.Sprintf("cannot decode state-v1: %v", err)}}
	}
	if !found {
		return []DoctorFinding{{Status: "fail", Message: "deployment state disappeared while waiting for the operation lock"}}
	}
	findings = []DoctorFinding{{Status: "ok", Message: "state-v1 deployment is readable: " + statePath}}
	if state.Current == nil {
		findings = append(findings, DoctorFinding{Status: "warn", Message: "environment has not been built"})
	} else {
		findings = append(findings, doctorCurrentRuntimeFileFindings(dir, operation, state)...)
	}
	if preinstall {
		findings = append(findings, providerPreinstallFindings(dir, scope, state, dockerPreflightTimeout)...)
	}
	return findings
}

func doctorCurrentRuntimeFileFindings(dir string, operation *deploy.OperationLock, state deploy.StateV1) []DoctorFinding {
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return []DoctorFinding{{Status: "fail", Message: fmt.Sprintf("cannot decode current runtime blueprint: %v", err)}}
	}
	store, err := providerstore.NewStore(dir)
	if err != nil {
		return []DoctorFinding{{Status: "fail", Message: fmt.Sprintf("cannot open provider store for runtime-file verification: %v", err)}}
	}
	current, found, err := LoadRecordedCurrentBuildV1(context.Background(), operation, store, document.Environment.ID, dir)
	if err != nil {
		return []DoctorFinding{{Status: "fail", Message: fmt.Sprintf("cannot load current build for runtime-file verification: %v", err)}}
	}
	if !found {
		return []DoctorFinding{{Status: "fail", Message: "current state names a build but its build record is missing"}}
	}
	runtime, err := CurrentStagedProviderBuildRuntimeV1()
	if err != nil {
		return []DoctorFinding{{Status: "fail", Message: fmt.Sprintf("cannot inspect host runtime identity: %v", err)}}
	}
	plan, err := PlanCurrentRuntimeV1(CurrentRuntimePlanInputV1{DeploymentDir: dir, Current: current, Runtime: runtime})
	if err != nil {
		return []DoctorFinding{{Status: "fail", Message: fmt.Sprintf("cannot reconstruct current runtime files: %v", err)}}
	}
	if err := RequireCurrentRuntimeInputsV1(operation, dir, plan); err != nil {
		return []DoctorFinding{{Status: "fail", Message: fmt.Sprintf("current runtime files do not match the recorded build: %v", err)}}
	}
	return []DoctorFinding{{Status: "ok", Message: "current runtime files match the recorded build"}}
}

var doctorInspectHostTools = inspectProviderInstallHostToolsV1
var doctorInspectAccount = inspectProviderInstallAccountV1
var doctorGeteuid = os.Geteuid

func providerPreinstallFindings(dir string, scope InstallScope, state deploy.StateV1, dockerPreflightTimeout time.Duration) []DoctorFinding {
	parsedScope, err := ParseInstallScope(string(scope))
	if err != nil {
		return []DoctorFinding{{Status: "fail", Message: fmt.Sprintf("install scope readiness: %v", err)}}
	}
	platform := currentHostPlatform()
	backend := platform.installBackendForScope(parsedScope)
	if err := validateInstallScopeForBackend(parsedScope, backend, platform); err != nil {
		return []DoctorFinding{{Status: "fail", Message: fmt.Sprintf("install scope readiness: %v", err)}}
	}
	tools, err := doctorInspectHostTools(context.Background(), backend)
	if err != nil {
		return []DoctorFinding{{Status: "fail", Message: fmt.Sprintf("install host tools are not ready for %s scope: %v", parsedScope, err)}}
	}
	findings := []DoctorFinding{{Status: "ok", Message: fmt.Sprintf("install host tools are ready for %s scope", parsedScope)}}
	runtimeInfo, err := detectDockerRuntimeForDoctor(context.Background(), CommandSpec{Name: tools.DockerPath, Dir: dir}, dockerPreflightTimeout)
	if err != nil {
		return append(findings, DoctorFinding{Status: "fail", Message: fmt.Sprintf("Docker runtime is required for install: %v", err)})
	}
	findings = append(findings, DoctorFinding{Status: "ok", Message: "Docker runtime detected: " + runtimeInfo.OperatingSystem})
	if parsedScope != InstallScopeSystem {
		return findings
	}
	if doctorGeteuid() != 0 {
		return append(findings, DoctorFinding{Status: "fail", Message: "system install requires root privileges"})
	}
	findings = append(findings, DoctorFinding{Status: "ok", Message: "system install is running with root privileges"})
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return append(findings, DoctorFinding{Status: "fail", Message: fmt.Sprintf("cannot decode system install account: %v", err)})
	}
	account, err := doctorInspectAccount(parsedScope, document.Environment.Install.System.RunAs)
	if err != nil {
		return append(findings, DoctorFinding{Status: "fail", Message: fmt.Sprintf("system install account is not ready: %v", err)})
	}
	if account.WillCreate {
		findings = append(findings, DoctorFinding{Status: "ok", Message: fmt.Sprintf("system install account %s:%s can be created", account.User, account.Group)})
	} else {
		findings = append(findings, DoctorFinding{Status: "ok", Message: fmt.Sprintf("system install account %s:%s exists", account.User, account.Group)})
	}
	return findings
}
