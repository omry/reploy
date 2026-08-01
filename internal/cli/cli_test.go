package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	reploy "github.com/omry/reploy"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/buildprofile"
	"github.com/omry/reploy/internal/buildprogress"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/dockerdeploy"
	"github.com/omry/reploy/internal/overrideui"
	"github.com/omry/reploy/internal/providerstore"
)

func runCLI(args ...string) (int, string, string) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Main(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func requireLinuxHost(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Linux/systemd-specific CLI behavior is covered by Linux CI")
	}
}

func newCLITestHTTPServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listener unavailable in this environment: %v", err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.Start()
	return server
}

func setCLITestPackIndex(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "reploy-blueprint-index.json")
	content := `{
  "schema_version": 1,
  "blueprints": {
    "demo-server": {
      "ref": "pypi://demo-server/demo_server/reploy/demo-server.blueprint.yaml"
    },
    "demo-suite": {
      "ref": "pypi://demo-suite/demo_suite/reploy/demo-suite.blueprint.yaml"
    }
  }
}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(packIndexURLEnv, "file:"+path)
}

func TestHelp(t *testing.T) {
	code, stdout, stderr := runCLI("--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Usage: reploy [--docker-timeout DURATION] COMMAND") {
		t.Fatalf("stdout did not contain usage:\n%s", stdout)
	}
	if strings.Contains(stdout, "--docker ") || strings.Contains(stdout, "[--docker]") {
		t.Fatalf("stdout retained removed Docker backend selector:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--docker-timeout DURATION") {
		t.Fatalf("stdout did not contain Docker timeout option:\n%s", stdout)
	}
	if !strings.Contains(stdout, "index        Manage the cached blueprint shorthand index") {
		t.Fatalf("stdout did not contain index command:\n%s", stdout)
	}
	if strings.Contains(stdout, "blueprint-index") {
		t.Fatalf("stdout contained removed blueprint-index alias:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestParseGlobalDeploymentOptionsDockerTimeout(t *testing.T) {
	options, args, err := parseGlobalDeploymentOptions([]string{"--docker-timeout", "12s", "build"})
	if err != nil {
		t.Fatal(err)
	}
	if !options.DockerTimeoutSet || options.DockerTimeout != 12*time.Second {
		t.Fatalf("docker timeout = %s set=%v, want 12s set", options.DockerTimeout, options.DockerTimeoutSet)
	}
	if strings.Join(args, " ") != "build" {
		t.Fatalf("args = %#v", args)
	}

	options, args, err = parseGlobalDeploymentOptions([]string{"--docker-timeout=250ms", "status"})
	if err != nil {
		t.Fatal(err)
	}
	if !options.DockerTimeoutSet || options.DockerTimeout != 250*time.Millisecond {
		t.Fatalf("docker timeout = %s set=%v, want 250ms set", options.DockerTimeout, options.DockerTimeoutSet)
	}
	if strings.Join(args, " ") != "status" {
		t.Fatalf("args = %#v", args)
	}
}

func TestParseDockerBundleCommandOptions(t *testing.T) {
	options, err := parseDockerBundleCommandOptions([]string{"--verbose", "--dir", "stage"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !options.Verbose || options.Dir != "stage" {
		t.Fatalf("options = %#v", options)
	}
	for _, args := range [][]string{{"--verbose"}, {"--dry-run"}, {"--wheelhouse-backend", "pip"}} {
		if _, err := parseDockerBundleCommandOptions(args, false); err == nil || !strings.Contains(err.Error(), "unknown option") {
			t.Fatalf("args/error = %#v/%v", args, err)
		}
	}
}

func TestParseGlobalDeploymentOptionsRejectsInvalidDockerTimeout(t *testing.T) {
	for _, args := range [][]string{
		{"--docker-timeout"},
		{"--docker-timeout", "nope"},
		{"--docker-timeout", "0"},
		{"--docker-timeout", "-1s"},
	} {
		if _, _, err := parseGlobalDeploymentOptions(args); err == nil {
			t.Fatalf("parseGlobalDeploymentOptions(%#v) err = nil, want error", args)
		}
	}
}

func TestParseDockerBuildOptions(t *testing.T) {
	options, err := parseDockerBuildOptions([]string{"--dir", "stage", "--verify", "--profile", "--verbose"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Dir != "stage" || !options.DirExplicit || options.NoCache || !options.Verify || !options.Profile || !options.Verbose {
		t.Fatalf("options = %#v", options)
	}
	if _, err := parseDockerBuildOptions([]string{"--validate-layers"}); err == nil {
		t.Fatal("removed --validate-layers option was accepted")
	}
	if _, err := parseDockerBuildOptions([]string{"--no-cache", "--verify"}); err == nil ||
		!strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("conflicting build verification options error = %v", err)
	}
}

func TestBuildVerifyReportsRecoveryFromInvalidCachedBuild(t *testing.T) {
	t.Setenv("TERM", "dumb")
	originalBuild := dockerProviderBuild
	originalRuntime := dockerProviderBuildRuntime
	t.Cleanup(func() {
		dockerProviderBuild = originalBuild
		dockerProviderBuildRuntime = originalRuntime
	})
	dockerProviderBuildRuntime = func() (dockerdeploy.StagedProviderBuildRuntimeV1, error) {
		return dockerdeploy.StagedProviderBuildRuntimeV1{UID: 501, GID: 20}, nil
	}
	stageDir := filepath.Join(t.TempDir(), "provider-stage")
	writeCLITestStagedState(t, stageDir, "demo")
	dockerProviderBuild = func(
		_ context.Context,
		input dockerdeploy.ProviderBuildRunInputV1,
	) (dockerdeploy.LockedProviderBuildExecutionResultV1, error) {
		if !input.Verify || input.NoCache {
			t.Fatalf("verified build input = %#v", input)
		}
		result := cliTestProviderBuildResult(t, stageDir, false)
		result.VerificationFailure = "provider artifact digest changed"
		return result, nil
	}
	code, stdout, stderr := runCLI("build", "--dir", stageDir, "--verify")
	if code != 0 ||
		!strings.Contains(stdout, "built demo") ||
		!strings.Contains(stderr, "Cached build verification failed") ||
		!strings.Contains(stderr, "rebuilt the environment instead") ||
		!strings.Contains(stderr, "provider artifact digest changed") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestBuildProfilePrintsInstrumentedTimings(t *testing.T) {
	t.Setenv("TERM", "dumb")
	originalBuild := dockerProviderBuild
	originalRuntime := dockerProviderBuildRuntime
	t.Cleanup(func() {
		dockerProviderBuild = originalBuild
		dockerProviderBuildRuntime = originalRuntime
	})
	dockerProviderBuildRuntime = func() (dockerdeploy.StagedProviderBuildRuntimeV1, error) {
		return dockerdeploy.StagedProviderBuildRuntimeV1{UID: 501, GID: 20}, nil
	}
	stageDir := filepath.Join(t.TempDir(), "provider-stage")
	writeCLITestStagedState(t, stageDir, "demo")
	dockerProviderBuild = func(
		ctx context.Context,
		_ dockerdeploy.ProviderBuildRunInputV1,
	) (dockerdeploy.LockedProviderBuildExecutionResultV1, error) {
		_, end := buildprofile.Start(ctx, "Profile test phase")
		end(nil)
		return cliTestProviderBuildResult(t, stageDir, true), nil
	}
	code, stdout, stderr := runCLI("build", "--dir", stageDir, "--profile")
	if code != 0 ||
		!strings.Contains(stdout, "demo is already up to date") ||
		!strings.Contains(stdout, "Build profile:") ||
		!strings.Contains(stdout, "Profile test phase") ||
		strings.Contains(stdout, stageDir) ||
		!strings.Contains(stderr, "building environment") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestBuildProfilePrintsInstrumentedTimingsAfterFailure(t *testing.T) {
	t.Setenv("TERM", "dumb")
	originalBuild := dockerProviderBuild
	originalRuntime := dockerProviderBuildRuntime
	t.Cleanup(func() {
		dockerProviderBuild = originalBuild
		dockerProviderBuildRuntime = originalRuntime
	})
	dockerProviderBuildRuntime = func() (dockerdeploy.StagedProviderBuildRuntimeV1, error) {
		return dockerdeploy.StagedProviderBuildRuntimeV1{UID: 501, GID: 20}, nil
	}
	stageDir := filepath.Join(t.TempDir(), "provider-stage")
	writeCLITestStagedState(t, stageDir, "demo")
	buildErr := errors.New("profiled build failed")
	dockerProviderBuild = func(
		ctx context.Context,
		_ dockerdeploy.ProviderBuildRunInputV1,
	) (dockerdeploy.LockedProviderBuildExecutionResultV1, error) {
		_, end := buildprofile.Start(ctx, "Profile failure phase")
		end(buildErr)
		return dockerdeploy.LockedProviderBuildExecutionResultV1{}, buildErr
	}
	code, stdout, stderr := runCLI("build", "--dir", stageDir, "--profile")
	if code != 1 ||
		!strings.Contains(stdout, "Build profile:") ||
		!strings.Contains(stdout, "Profile failure phase") ||
		!strings.Contains(stdout, "(failed)") ||
		strings.Contains(stdout, stageDir) ||
		!strings.Contains(stderr, buildErr.Error()) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestVerifyCommandReportsReadOnlyAuditResult(t *testing.T) {
	originalVerify := dockerVerifyCurrentBuild
	originalRuntime := dockerProviderBuildRuntime
	t.Cleanup(func() {
		dockerVerifyCurrentBuild = originalVerify
		dockerProviderBuildRuntime = originalRuntime
	})
	stageDir := filepath.Join(t.TempDir(), "provider-stage")
	writeCLITestStagedState(t, stageDir, "demo")
	dockerProviderBuildRuntime = func() (dockerdeploy.StagedProviderBuildRuntimeV1, error) {
		return dockerdeploy.StagedProviderBuildRuntimeV1{UID: 501, GID: 20}, nil
	}
	called := false
	dockerVerifyCurrentBuild = func(
		ctx context.Context,
		input dockerdeploy.VerifyCurrentBuildInputV1,
	) (dockerdeploy.VerifyCurrentBuildResultV1, error) {
		called = true
		if ctx == nil ||
			input.DeploymentDir != stageDir ||
			input.Runtime.UID != 501 ||
			input.Runtime.GID != 20 {
			t.Fatalf("verify input = %#v", input)
		}
		return dockerdeploy.VerifyCurrentBuildResultV1{
			Environment: "demo",
			Reference:   "reploy/env/demo-deadbeef:g-current",
			Details: dockerdeploy.CurrentBuildVerificationResultV1{
				StoreObjects: 8, Images: 4, Commands: 3,
			},
		}, nil
	}
	code, stdout, stderr := runCLI("verify", "--dir", stageDir)
	if code != 0 || !called || stderr != "" {
		t.Fatalf("code=%d called=%t stdout=%q stderr=%q", code, called, stdout, stderr)
	}
	want := "" +
		"verified current build: demo\n" +
		"image: reploy/env/demo-deadbeef:g-current\n" +
		"provider-store objects: 8\n" +
		"images: 4\n" +
		"commands: 3\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestVerifyCommandSuggestsRebuildForMissingCachedImage(t *testing.T) {
	originalVerify := dockerVerifyCurrentBuild
	originalRuntime := dockerProviderBuildRuntime
	t.Cleanup(func() {
		dockerVerifyCurrentBuild = originalVerify
		dockerProviderBuildRuntime = originalRuntime
	})
	stageDir := filepath.Join(t.TempDir(), "provider-stage")
	writeCLITestStagedState(t, stageDir, "demo")
	dockerProviderBuildRuntime = func() (dockerdeploy.StagedProviderBuildRuntimeV1, error) {
		return dockerdeploy.StagedProviderBuildRuntimeV1{UID: 501, GID: 20}, nil
	}
	const imageID = "sha256:d5bc82357ed038ef1f77c03b1425c21deffa956d75c80651d07cf560a3d0b562"
	dockerVerifyCurrentBuild = func(
		context.Context,
		dockerdeploy.VerifyCurrentBuildInputV1,
	) (dockerdeploy.VerifyCurrentBuildResultV1, error) {
		return dockerdeploy.VerifyCurrentBuildResultV1{}, &dockerdeploy.CurrentBuildImageMissingErrorV1{
			Subject: "cached Python layer image",
			ImageID: imageID,
		}
	}
	code, stdout, stderr := runCLI("verify", "--dir", stageDir)
	if code != 1 || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, want := range []string{
		"reploy verify error: cached Python layer image " + imageID + " is missing from Docker",
		"complete build lineage cannot be verified",
		"next: run `reploy build --verify`",
		"rebuild instead of reusing this incomplete cache",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("missing image diagnostic lacks %q:\n%s", want, stderr)
		}
	}
	for _, unwanted := range []string{
		"application/application",
		"inspect materialization candidate",
		"Error response from daemon",
		"[]",
	} {
		if strings.Contains(stderr, unwanted) {
			t.Fatalf("missing image diagnostic exposes %q:\n%s", unwanted, stderr)
		}
	}
}

func TestVerifyHelpDescribesReadOnlyComprehensiveAudit(t *testing.T) {
	code, stdout, stderr := runCLI("verify", "--help")
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, want := range []string{
		"Usage: reploy verify [OPTIONS]",
		"without changing it",
		"fully hashes",
		"network-disabled",
		"does not resolve packages",
		"execute application commands",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("verify help missing %q:\n%s", want, stdout)
		}
	}
}

func TestBuildOverrideUIAllowsOnlyInteractiveNondumbTerminals(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("TERM", "xterm-256color")
	if !buildOverrideUIAllowed(true, true) {
		t.Fatal("interactive terminal did not enable the build screen")
	}
	t.Setenv("TERM", "dumb")
	if buildOverrideUIAllowed(true, true) {
		t.Fatal("dumb terminal enabled the interactive build screen")
	}
}

func TestBuildCommandUsesReployProgressAndHidesBackendOutput(t *testing.T) {
	t.Setenv("TERM", "dumb")
	originalBuild := dockerProviderBuild
	originalRuntime := dockerProviderBuildRuntime
	t.Cleanup(func() {
		dockerProviderBuild = originalBuild
		dockerProviderBuildRuntime = originalRuntime
	})
	dockerProviderBuildRuntime = func() (dockerdeploy.StagedProviderBuildRuntimeV1, error) {
		return dockerdeploy.StagedProviderBuildRuntimeV1{UID: 501, GID: 20}, nil
	}
	stageDir := filepath.Join(t.TempDir(), "provider-stage")
	writeCLITestStagedState(t, stageDir, "demo")
	called := false
	dockerProviderBuild = func(ctx context.Context, input dockerdeploy.ProviderBuildRunInputV1) (dockerdeploy.LockedProviderBuildExecutionResultV1, error) {
		called = true
		if ctx == nil || input.DeploymentDir != stageDir || input.Automatic || !input.NoCache {
			t.Fatalf("input = %#v", input)
		}
		if input.Runtime.UID != 501 || input.Runtime.GID != 20 || input.RunOptions.DockerPreflightTimeout != 12*time.Second || input.RunOptions.Stdout == nil || input.RunOptions.Stderr == nil {
			t.Fatalf("runtime options = %#v", input)
		}
		fmt.Fprintln(input.Progress, "resolving Python packages for component application")
		fmt.Fprintln(input.RunOptions.Stdout, "[+] Building 5.6s (10/10) FINISHED")
		fmt.Fprintln(input.RunOptions.Stderr, "1 warning found (use docker --debug to expand):")
		fmt.Fprintln(input.RunOptions.Stderr, `- SecretsUsedInArgOrEnv: Do not use ARG or ENV instructions for sensitive data (ENV "SECRET")`)
		fmt.Fprintln(input.RunOptions.Stderr, `- SecretsUsedInArgOrEnv: Do not use ARG or ENV instructions for sensitive data (ENV "SECRET")`)
		fmt.Fprintln(input.RunOptions.Stderr, `- UndefinedVar: Usage of undefined variable '$MISSING'`)
		fmt.Fprintln(input.RunOptions.Stderr, `- UndefinedVar: Usage of undefined variable '$MISSING'`)
		return cliTestProviderBuildResult(t, stageDir, false), nil
	}
	code, stdout, stderr := runCLI("--docker-timeout", "12s", "build", "--dir", stageDir, "--no-cache")
	if code != 0 || !called {
		t.Fatalf("code/called/stdout/stderr = %d/%v/%q/%q", code, called, stdout, stderr)
	}
	for _, want := range []string{
		"building environment: preparing staged environment",
		"building environment: resolving Python packages for component application",
		"building environment: done",
		"reploy warning: Docker check UndefinedVar:",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
	for _, unwanted := range []string{"[+] Building", "warning found", "docker --debug", "SecretsUsedInArgOrEnv", `"SECRET"`} {
		if strings.Contains(stdout+stderr, unwanted) {
			t.Fatalf("normal build output leaked %q:\nstdout:\n%s\nstderr:\n%s", unwanted, stdout, stderr)
		}
	}
	if strings.Count(stderr, "reploy warning:") != 1 {
		t.Fatalf("build warnings were not deduplicated:\n%s", stderr)
	}
	if want := "image: reploy/env/demo:g-test\nbuilt demo\n"; stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestInteractiveBuildUsesOneBuildTransaction(t *testing.T) {
	originalEnabled := buildOverrideUIEnabled
	originalProgress := runBuildProgress
	originalBuild := dockerProviderBuild
	originalRuntime := dockerProviderBuildRuntime
	originalDelay := interactiveBuildDisplayDelay
	t.Cleanup(func() {
		buildOverrideUIEnabled = originalEnabled
		runBuildProgress = originalProgress
		dockerProviderBuild = originalBuild
		dockerProviderBuildRuntime = originalRuntime
		interactiveBuildDisplayDelay = originalDelay
	})
	buildOverrideUIEnabled = func(io.Reader, io.Writer) bool { return true }
	interactiveBuildDisplayDelay = time.Millisecond
	dockerProviderBuildRuntime = func() (dockerdeploy.StagedProviderBuildRuntimeV1, error) {
		return dockerdeploy.StagedProviderBuildRuntimeV1{}, nil
	}
	stageDir := filepath.Join(t.TempDir(), "provider-stage")
	writeCLITestStagedState(t, stageDir, "demo")
	var progressConfig overrideui.BuildProgressConfig
	var buildOutcome *overrideui.BuildOutcome
	runBuildProgress = func(config overrideui.BuildProgressConfig) (overrideui.BuildProgressResult, error) {
		progressConfig = config
		result, err := config.Run(t.Context(), io.Discard, nil)
		if err != nil {
			return overrideui.BuildProgressResult{BuildError: err}, nil
		}
		buildOutcome = result.Build
		fmt.Fprintln(config.Output, "progress UI released")
		return overrideui.BuildProgressResult{Validation: result}, nil
	}
	buildCalls := 0
	dockerProviderBuild = func(_ context.Context, input dockerdeploy.ProviderBuildRunInputV1) (dockerdeploy.LockedProviderBuildExecutionResultV1, error) {
		buildCalls++
		if input.ValidateChoices || !input.NoCache {
			t.Fatalf("interactive build input = %#v", input)
		}
		fmt.Fprintln(input.RunOptions.Stderr, `- UndefinedVar: Usage of undefined variable '$MISSING'`)
		fmt.Fprintln(input.RunOptions.Stderr, `- UndefinedVar: Usage of undefined variable '$MISSING'`)
		fmt.Fprintln(input.RunOptions.Stderr, `- SecretsUsedInArgOrEnv: Do not use ARG or ENV instructions for sensitive data (ENV "TOKEN")`)
		time.Sleep(10 * time.Millisecond)
		return cliTestProviderBuildResult(t, stageDir, false), nil
	}

	code, stdout, stderr := runCLI("build", "--dir", stageDir, "--no-cache")
	if code != 0 || buildCalls != 1 {
		t.Fatalf("code/buildCalls/stdout/stderr = %d/%d/%q/%q", code, buildCalls, stdout, stderr)
	}
	if progressConfig.Context == nil || progressConfig.Run == nil {
		t.Fatalf("interactive build progress config = %#v", progressConfig)
	}
	if progressConfig.Input == nil || progressConfig.Output == nil {
		t.Fatalf("interactive build terminal streams = %#v", progressConfig)
	}
	if buildOutcome == nil || buildOutcome.ImageReference != "reploy/env/demo:g-test" || buildOutcome.Environment != "demo" {
		t.Fatalf("interactive build outcome = %#v", buildOutcome)
	}
	if stdout != "image: reploy/env/demo:g-test\nbuilt demo\n" {
		t.Fatalf("completed build stdout/stderr = %q/%q", stdout, stderr)
	}
	uiReleased := strings.Index(stderr, "progress UI released")
	warningPrinted := strings.Index(stderr, "reploy warning:")
	if uiReleased < 0 ||
		warningPrinted < 0 ||
		uiReleased > warningPrinted ||
		strings.Count(stderr, "reploy warning: Docker check UndefinedVar:") != 1 ||
		strings.Contains(stderr, "SecretsUsedInArgOrEnv") ||
		strings.Contains(stderr, `"TOKEN"`) {
		t.Fatalf("interactive build warning output = %q", stderr)
	}
}

func TestInteractiveBuildProgressRunsWhileDeploymentLockIsHeld(t *testing.T) {
	originalEnabled := buildOverrideUIEnabled
	originalProgress := runBuildProgress
	originalBuild := dockerProviderBuild
	originalRuntime := dockerProviderBuildRuntime
	originalDelay := interactiveBuildDisplayDelay
	t.Cleanup(func() {
		buildOverrideUIEnabled = originalEnabled
		runBuildProgress = originalProgress
		dockerProviderBuild = originalBuild
		dockerProviderBuildRuntime = originalRuntime
		interactiveBuildDisplayDelay = originalDelay
	})
	buildOverrideUIEnabled = func(io.Reader, io.Writer) bool { return true }
	interactiveBuildDisplayDelay = time.Millisecond
	dockerProviderBuildRuntime = func() (dockerdeploy.StagedProviderBuildRuntimeV1, error) {
		return dockerdeploy.StagedProviderBuildRuntimeV1{}, nil
	}
	stageDir := filepath.Join(t.TempDir(), "provider-stage")
	writeCLITestStagedState(t, stageDir, "demo")
	dockerProviderBuild = func(ctx context.Context, _ dockerdeploy.ProviderBuildRunInputV1) (dockerdeploy.LockedProviderBuildExecutionResultV1, error) {
		operation, err := deploy.AcquireOperationLock(ctx, stageDir)
		if err != nil {
			return dockerdeploy.LockedProviderBuildExecutionResultV1{}, err
		}
		defer operation.Unlock()
		time.Sleep(20 * time.Millisecond)
		return cliTestProviderBuildResult(t, stageDir, false), nil
	}
	runBuildProgress = func(config overrideui.BuildProgressConfig) (overrideui.BuildProgressResult, error) {
		config.Input = strings.NewReader("")
		config.Output = &bytes.Buffer{}
		return overrideui.RunBuildProgress(config)
	}

	code, stdout, stderr := runCLI("build", "--dir", stageDir)
	if code != 0 || !strings.Contains(stdout, "built demo") || stderr != "" {
		t.Fatalf("code/stdout/stderr = %d/%q/%q", code, stdout, stderr)
	}
}

func TestFastInteractiveBuildPrintsConciseOutcomeWithoutBuildScreen(t *testing.T) {
	originalEnabled := buildOverrideUIEnabled
	originalProgress := runBuildProgress
	originalBuild := dockerProviderBuild
	originalRuntime := dockerProviderBuildRuntime
	t.Cleanup(func() {
		buildOverrideUIEnabled = originalEnabled
		runBuildProgress = originalProgress
		dockerProviderBuild = originalBuild
		dockerProviderBuildRuntime = originalRuntime
	})
	buildOverrideUIEnabled = func(io.Reader, io.Writer) bool { return true }
	dockerProviderBuildRuntime = func() (dockerdeploy.StagedProviderBuildRuntimeV1, error) {
		return dockerdeploy.StagedProviderBuildRuntimeV1{}, nil
	}
	stageDir := filepath.Join(t.TempDir(), "provider-stage")
	writeCLITestStagedState(t, stageDir, "demo")
	runBuildProgress = func(overrideui.BuildProgressConfig) (overrideui.BuildProgressResult, error) {
		t.Fatal("fast successful build opened the build screen")
		return overrideui.BuildProgressResult{}, nil
	}
	dockerProviderBuild = func(_ context.Context, input dockerdeploy.ProviderBuildRunInputV1) (dockerdeploy.LockedProviderBuildExecutionResultV1, error) {
		fmt.Fprintln(input.RunOptions.Stderr, `- UndefinedVar: Usage of undefined variable '$MISSING'`)
		fmt.Fprintln(input.RunOptions.Stderr, `- UndefinedVar: Usage of undefined variable '$MISSING'`)
		fmt.Fprintln(input.RunOptions.Stderr, `W: Could not open lock file /var/lib/dpkg/lock-frontend - open (13: Permission denied)`)
		fmt.Fprintln(input.RunOptions.Stderr, `- SecretsUsedInArgOrEnv: Do not use ARG or ENV instructions for sensitive data (ENV "TOKEN")`)
		return cliTestProviderBuildResult(t, stageDir, true), nil
	}

	code, stdout, stderr := runCLI("build", "--dir", stageDir)
	if code != 0 {
		t.Fatalf("code/stdout/stderr = %d/%q/%q", code, stdout, stderr)
	}
	if stdout != "image: reploy/env/demo:g-test\ndemo is already up to date\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if strings.Count(stderr, "reploy warning: Docker check UndefinedVar:") != 1 ||
		strings.Contains(stderr, "SecretsUsedInArgOrEnv") ||
		strings.Contains(stderr, "Could not open lock file") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestInteractiveBuildCancellationCancelsPrestartedBackendBuild(t *testing.T) {
	originalEnabled := buildOverrideUIEnabled
	originalProgress := runBuildProgress
	originalBuild := dockerProviderBuild
	originalRuntime := dockerProviderBuildRuntime
	originalDelay := interactiveBuildDisplayDelay
	t.Cleanup(func() {
		buildOverrideUIEnabled = originalEnabled
		runBuildProgress = originalProgress
		dockerProviderBuild = originalBuild
		dockerProviderBuildRuntime = originalRuntime
		interactiveBuildDisplayDelay = originalDelay
	})
	buildOverrideUIEnabled = func(io.Reader, io.Writer) bool { return true }
	interactiveBuildDisplayDelay = time.Millisecond
	dockerProviderBuildRuntime = func() (dockerdeploy.StagedProviderBuildRuntimeV1, error) {
		return dockerdeploy.StagedProviderBuildRuntimeV1{}, nil
	}
	stageDir := filepath.Join(t.TempDir(), "provider-stage")
	writeCLITestStagedState(t, stageDir, "demo")
	runBuildProgress = func(config overrideui.BuildProgressConfig) (overrideui.BuildProgressResult, error) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, _ = config.Run(ctx, io.Discard, nil)
		return overrideui.BuildProgressResult{Canceled: true}, nil
	}
	buildCalls := 0
	dockerProviderBuild = func(ctx context.Context, _ dockerdeploy.ProviderBuildRunInputV1) (dockerdeploy.LockedProviderBuildExecutionResultV1, error) {
		buildCalls++
		<-ctx.Done()
		return dockerdeploy.LockedProviderBuildExecutionResultV1{}, ctx.Err()
	}

	code, stdout, stderr := runCLI("build", "--dir", stageDir)
	if code != 130 || buildCalls != 1 || stdout != "" || !strings.Contains(stderr, "reploy build canceled") {
		t.Fatalf("code/buildCalls/stdout/stderr = %d/%d/%q/%q", code, buildCalls, stdout, stderr)
	}
}

func TestInteractiveBuildFailureClosesProgressAndPrintsDiagnostic(t *testing.T) {
	originalEnabled := buildOverrideUIEnabled
	originalProgress := runBuildProgress
	originalBuild := dockerProviderBuild
	originalRuntime := dockerProviderBuildRuntime
	originalDelay := interactiveBuildDisplayDelay
	t.Cleanup(func() {
		buildOverrideUIEnabled = originalEnabled
		runBuildProgress = originalProgress
		dockerProviderBuild = originalBuild
		dockerProviderBuildRuntime = originalRuntime
		interactiveBuildDisplayDelay = originalDelay
	})
	buildOverrideUIEnabled = func(io.Reader, io.Writer) bool { return true }
	interactiveBuildDisplayDelay = time.Millisecond
	dockerProviderBuildRuntime = func() (dockerdeploy.StagedProviderBuildRuntimeV1, error) {
		return dockerdeploy.StagedProviderBuildRuntimeV1{}, nil
	}
	stageDir := filepath.Join(t.TempDir(), "provider-stage")
	writeCLITestStagedState(t, stageDir, "demo")
	dockerProviderBuild = func(context.Context, dockerdeploy.ProviderBuildRunInputV1) (dockerdeploy.LockedProviderBuildExecutionResultV1, error) {
		time.Sleep(10 * time.Millisecond)
		return dockerdeploy.LockedProviderBuildExecutionResultV1{}, errors.New("dependency conflict")
	}
	runBuildProgress = func(config overrideui.BuildProgressConfig) (overrideui.BuildProgressResult, error) {
		_, err := config.Run(t.Context(), io.Discard, nil)
		if err == nil {
			t.Fatal("failed build returned no validation error")
		}
		return overrideui.BuildProgressResult{BuildError: err}, nil
	}

	code, stdout, stderr := runCLI("build", "--dir", stageDir)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "dependency conflict") ||
		strings.Contains(stderr, "stopped before package choices") {
		t.Fatalf("code/stdout/stderr = %d/%q/%q", code, stdout, stderr)
	}
}

func TestInteractiveBuildSessionReplaysProgress(t *testing.T) {
	runner := func(
		_ context.Context,
		progress io.Writer,
		report buildprogress.Reporter,
	) (overrideui.ValidationResult, error) {
		report(buildprogress.Event{
			Phase: buildprogress.PhaseInspect, Environment: "demo",
			Detail: "Inspecting build",
		})
		report(buildprogress.Event{Phase: buildprogress.PhasePrepare, Detail: "Preparing build"})
		fmt.Fprintln(progress, "inspecting build")
		fmt.Fprintln(progress, "preparing build")
		return overrideui.ValidationResult{}, errors.New("build failed")
	}
	session := startInteractiveBuildSession(runner)
	<-session.done
	validate := session.validationRunner()

	var progress bytes.Buffer
	var event buildprogress.Event
	if _, err := validate(t.Context(), &progress, func(got buildprogress.Event) {
		event = got
	}); err == nil ||
		!strings.Contains(err.Error(), "build failed") {
		t.Fatalf("validation error = %v", err)
	}
	if progress.String() != "preparing build\n" {
		t.Fatalf("replayed progress = %q", progress.String())
	}
	if event.Phase != buildprogress.PhasePrepare || event.Environment != "demo" ||
		event.Detail != "Preparing build" {
		t.Fatalf("replayed build progress event = %#v", event)
	}
}

func TestBuildWarningsSuppressExpectedSuccessfulWarnings(t *testing.T) {
	output := strings.Join([]string{
		`- SecretsUsedInArgOrEnv: Do not use ARG or ENV instructions for sensitive data (ENV "FIRST_TOKEN")`,
		`- SecretsUsedInArgOrEnv: Do not use ARG or ENV instructions for sensitive data (ARG "SECOND_TOKEN")`,
		`W: Download is performed unsandboxed as root as file '/tmp/reploy-apt-resolve/lists/partial/example' couldn't be accessed by user '_apt'. - pkgAcquire::Run (13: Permission denied)`,
		`W: Could not open lock file /var/lib/dpkg/lock-frontend - open (13: Permission denied)`,
		`W: Repository metadata will expire soon`,
		`- UndefinedVar: Usage of undefined variable '$MISSING'`,
	}, "\n")
	warnings := buildWarnings(output, true)
	if len(warnings) != 2 {
		t.Fatalf("warnings = %#v", warnings)
	}
	for _, want := range []string{"Docker check UndefinedVar", "APT: Repository metadata will expire soon"} {
		if !slices.ContainsFunc(warnings, func(warning string) bool { return strings.Contains(warning, want) }) {
			t.Fatalf("unrelated warning %q was lost: %#v", want, warnings)
		}
	}
	failedWarnings := buildWarnings(output, false)
	if !slices.ContainsFunc(failedWarnings, func(warning string) bool {
		return strings.Contains(warning, "unsandboxed as root")
	}) {
		t.Fatalf("failed-build resolver warning was lost: %#v", failedWarnings)
	}
}

func TestBuildFailureDiagnosticTranslatesProviderFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		output string
		err    error
		want   string
	}{
		{
			name: "apt", output: "E: The repository 'https://example.invalid stable Release' does not have a Release file.",
			want: "APT package operation failed: The repository",
		},
		{
			name: "network", output: "Temporary failure resolving 'deb.debian.org'",
			want: "network access failed: Temporary failure resolving",
		},
		{
			name: "python resolution", output: "ERROR: Could not find a version that satisfies the requirement demo==99",
			want: "Python package resolution failed: Could not find a version",
		},
		{
			name: "python resolution retained only in error chain",
			err: errors.New(
				"execute provider graph: prepare provider node \"python/application\": fresh Python resolution failed: " +
					"resolve wheels Python resolver container for linux/amd64: docker failed: exit status 1\n" +
					"command output:\nERROR: No matching distribution found for omegaconf-inspector",
			),
			want: "Python package resolution failed: No matching distribution found for omegaconf-inspector",
		},
		{
			name: "unclassified Python build backend failure retains operation and output",
			err: errors.New(
				"execute provider graph: prepare provider node \"python/application\": fresh Python resolution failed: " +
					"local Python source sdist build failed: build source distribution Python resolver container for linux/amd64: " +
					"\x1b[31mdocker failed: exit status 1\x1b[0m\rcommand output:\r" +
					"\x1b[1m× Failed to build `/reploy/source-build/omegaconf`\x1b[0m\a\b\x00\x7f\u009b\u202e\r" +
					"╰─▶ FileNotFoundError: [Errno 2] No such file or directory: 'java'",
			),
			want: "build source distribution Python resolver container for linux/amd64",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			buildErr := test.err
			if buildErr == nil {
				buildErr = errors.New("docker failed: exit status 1")
			}
			got := buildFailureDiagnostic(buildErr, test.output)
			if !strings.Contains(got, test.want) {
				t.Fatalf("diagnostic = %q, want substring %q", got, test.want)
			}
			if strings.Contains(got, "docker failed") {
				t.Fatalf("diagnostic leaked backend process failure: %q", got)
			}
			if strings.IndexFunc(got, func(char rune) bool {
				return char != '\n' && (unicode.IsControl(char) || unicode.In(char, unicode.Cf))
			}) >= 0 {
				t.Fatalf("diagnostic retained terminal controls: %q", got)
			}
			if test.name == "unclassified Python build backend failure retains operation and output" &&
				!strings.Contains(got, "FileNotFoundError") {
				t.Fatalf("diagnostic lost backend output: %q", got)
			}
		})
	}
}

func TestBuildCommandReportsExactReuse(t *testing.T) {
	originalBuild := dockerProviderBuild
	originalRuntime := dockerProviderBuildRuntime
	t.Cleanup(func() {
		dockerProviderBuild = originalBuild
		dockerProviderBuildRuntime = originalRuntime
	})
	dockerProviderBuildRuntime = func() (dockerdeploy.StagedProviderBuildRuntimeV1, error) {
		return dockerdeploy.StagedProviderBuildRuntimeV1{}, nil
	}
	stageDir := filepath.Join(t.TempDir(), "provider-stage")
	writeCLITestStagedState(t, stageDir, "demo")
	dockerProviderBuild = func(context.Context, dockerdeploy.ProviderBuildRunInputV1) (dockerdeploy.LockedProviderBuildExecutionResultV1, error) {
		return cliTestProviderBuildResult(t, stageDir, true), nil
	}

	code, stdout, stderr := runCLI("build", "--dir", stageDir)
	if code != 0 {
		t.Fatalf("code = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if want := "image: reploy/env/demo:g-test\ndemo is already up to date\n"; stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if !strings.Contains(stderr, "building environment: done") {
		t.Fatalf("stderr missing successful completion:\n%s", stderr)
	}
}

func TestBuildCommandReportsRuntimeConfigurationUpdate(t *testing.T) {
	originalBuild := dockerProviderBuild
	originalRuntime := dockerProviderBuildRuntime
	t.Cleanup(func() {
		dockerProviderBuild = originalBuild
		dockerProviderBuildRuntime = originalRuntime
	})
	dockerProviderBuildRuntime = func() (dockerdeploy.StagedProviderBuildRuntimeV1, error) {
		return dockerdeploy.StagedProviderBuildRuntimeV1{}, nil
	}
	stageDir := filepath.Join(t.TempDir(), "provider-stage")
	writeCLITestStagedState(t, stageDir, "demo")
	dockerProviderBuild = func(context.Context, dockerdeploy.ProviderBuildRunInputV1) (dockerdeploy.LockedProviderBuildExecutionResultV1, error) {
		result := cliTestProviderBuildResult(t, stageDir, true)
		result.Republished = true
		return result, nil
	}

	code, stdout, stderr := runCLI("build", "--dir", stageDir)
	if code != 0 {
		t.Fatalf("code = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if want := "image: reploy/env/demo:g-test\nupdated demo\n"; stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if !strings.Contains(stderr, "building environment: done") {
		t.Fatalf("stderr missing successful completion:\n%s", stderr)
	}
}

func TestBuildCommandFailureRetainsStepWithoutRawBackendTranscript(t *testing.T) {
	originalBuild := dockerProviderBuild
	originalRuntime := dockerProviderBuildRuntime
	t.Cleanup(func() {
		dockerProviderBuild = originalBuild
		dockerProviderBuildRuntime = originalRuntime
	})
	dockerProviderBuildRuntime = func() (dockerdeploy.StagedProviderBuildRuntimeV1, error) {
		return dockerdeploy.StagedProviderBuildRuntimeV1{}, nil
	}
	stageDir := filepath.Join(t.TempDir(), "provider-stage")
	writeCLITestStagedState(t, stageDir, "demo")
	dockerProviderBuild = func(_ context.Context, input dockerdeploy.ProviderBuildRunInputV1) (dockerdeploy.LockedProviderBuildExecutionResultV1, error) {
		fmt.Fprintln(input.Progress, "assembling environment image")
		fmt.Fprintln(input.RunOptions.Stderr, "ERROR: failed to solve: selected base image was not found")
		return dockerdeploy.LockedProviderBuildExecutionResultV1{}, errors.New("execute provider graph: docker failed: exit status 1")
	}

	code, stdout, stderr := runCLI("build", "--dir", stageDir)
	if code != 1 || stdout != "" {
		t.Fatalf("code/stdout = %d/%q\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"building environment: assembling environment image: failed",
		"reploy build error: environment image construction failed: selected base image was not found",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
	for _, unwanted := range []string{"failed to solve", "docker failed", "exit status 1"} {
		if strings.Contains(stderr, unwanted) {
			t.Fatalf("failure output leaked %q:\n%s", unwanted, stderr)
		}
	}
}

func TestBuildHelpDescribesLayerValidationInvariant(t *testing.T) {
	code, stdout, stderr := runCLI("build", "--help")
	if code != 0 || stderr != "" {
		t.Fatalf("code/stderr = %d/%q", code, stderr)
	}
	if !strings.Contains(stdout, "Every newly created component layer is validated") ||
		!strings.Contains(stdout, "fully validated before publication") ||
		!strings.Contains(stdout, "without installing") || !strings.Contains(stdout, "--verbose") ||
		!strings.Contains(stdout, "without backend transcripts") ||
		!strings.Contains(stdout, "inline") || !strings.Contains(stdout, "exits automatically") ||
		strings.Contains(stdout, "retain progress") {
		t.Fatalf("build help = %q", stdout)
	}
}

func TestVersion(t *testing.T) {
	code, stdout, stderr := runCLI("--version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stdout != "reploy "+reploy.DisplayVersion()+"\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestWindowsWSLBoundaryError(t *testing.T) {
	tests := []struct {
		name string
		goos string
		env  map[string]string
		cwd  string
		want bool
	}{
		{
			name: "windows process launched from WSL distro",
			goos: "windows",
			env:  map[string]string{"WSL_DISTRO_NAME": "Ubuntu"},
			want: true,
		},
		{
			name: "windows process launched with WSL interop marker",
			goos: "windows",
			env:  map[string]string{"WSL_INTEROP": "/run/WSL/1_interop"},
			want: true,
		},
		{
			name: "windows process in WSL localhost filesystem",
			goos: "windows",
			cwd:  `\\wsl.localhost\Ubuntu\home\omry\dev\reploy`,
			want: true,
		},
		{
			name: "windows process in legacy WSL filesystem",
			goos: "windows",
			cwd:  `\\wsl$\Ubuntu\home\omry\dev\reploy`,
			want: true,
		},
		{
			name: "windows process in extended WSL filesystem path",
			goos: "windows",
			cwd:  `\\?\UNC\wsl.localhost\Ubuntu\home\omry\dev\reploy`,
			want: true,
		},
		{
			name: "native windows shell with WSLENV only in windows filesystem",
			goos: "windows",
			env:  map[string]string{"WSLENV": "REPLOY_HOME/p"},
			cwd:  `C:\Users\omry\dev\reploy`,
			want: false,
		},
		{
			name: "linux host is not native windows",
			goos: "linux",
			env:  map[string]string{"WSL_DISTRO_NAME": "Ubuntu"},
			cwd:  "/home/omry/dev/reploy",
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := windowsWSLBoundaryError(
				test.goos,
				func(name string) (string, bool) {
					value, ok := test.env[name]
					return value, ok
				},
				func() (string, error) {
					return test.cwd, nil
				},
			)
			if (got != "") != test.want {
				t.Fatalf("windowsWSLBoundaryError() = %q, want present=%v", got, test.want)
			}
			if got != "" && !strings.Contains(got, "use the Linux reploy binary inside WSL") {
				t.Fatalf("error does not explain WSL Linux binary path: %q", got)
			}
		})
	}
}

func TestNoArgsShowsVersionAndNextSteps(t *testing.T) {
	t.Chdir(t.TempDir())

	code, stdout, stderr := runCLI()
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"reploy " + reploy.Version,
		"Usage: reploy COMMAND",
		"Next steps:",
		"reploy stage APP_REF",
		"reploy install APP_REF --scope user|system",
		"reploy index search QUERY",
		"Run 'reploy --help' for all commands.",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestUsageErrorsDoNotShowGlobalOnboarding(t *testing.T) {
	t.Chdir(t.TempDir())

	tests := []struct {
		name        string
		args        []string
		wantError   string
		wantUsage   string
		forbidExtra []string
	}{
		{
			name:      "unknown top-level short option",
			args:      []string{"-fd"},
			wantError: "reploy usage error: unknown option: -fd",
			wantUsage: "Usage: reploy [--docker-timeout DURATION] COMMAND",
			forbidExtra: []string{
				"Next steps:",
			},
		},
		{
			name:      "unknown top-level long option",
			args:      []string{"--wat"},
			wantError: "reploy usage error: unknown option: --wat",
			wantUsage: "Usage: reploy [--docker-timeout DURATION] COMMAND",
			forbidExtra: []string{
				"Next steps:",
			},
		},
		{
			name:      "removed global target",
			args:      []string{"--docker"},
			wantError: "reploy usage error: unknown option: --docker",
			wantUsage: "Usage: reploy [--docker-timeout DURATION] COMMAND",
			forbidExtra: []string{
				"Next steps:",
			},
		},
		{
			name:      "global timeout without command",
			args:      []string{"--docker-timeout", "5s"},
			wantError: "reploy usage error: expected command",
			wantUsage: "Usage: reploy [--docker-timeout DURATION] COMMAND",
			forbidExtra: []string{
				"Next steps:",
			},
		},
		{
			name:      "global timeout missing value",
			args:      []string{"--docker-timeout"},
			wantError: "reploy usage error: --docker-timeout requires a value",
			wantUsage: "Usage: reploy [--docker-timeout DURATION] COMMAND",
			forbidExtra: []string{
				"Next steps:",
			},
		},
		{
			name:      "unknown top-level command",
			args:      []string{"wat"},
			wantError: "reploy usage error: unknown command: wat",
			wantUsage: "Usage: reploy [--docker-timeout DURATION] COMMAND",
			forbidExtra: []string{
				"Next steps:",
			},
		},
		{
			name:      "index command slot option",
			args:      []string{"index", "-fd"},
			wantError: "reploy index usage error: unknown option: -fd",
			wantUsage: "Usage: reploy index COMMAND",
		},
		{
			name:      "bundle command slot option",
			args:      []string{"bundle", "-fd"},
			wantError: "reploy usage error: unknown option: -fd",
			wantUsage: "Usage: reploy [--docker-timeout DURATION] bundle COMMAND",
		},
		{
			name:      "bundle action short option",
			args:      []string{"bundle", "clean", "-fd"},
			wantError: "reploy usage error: unknown option: -fd",
			wantUsage: "Usage: reploy [--docker-timeout DURATION] bundle COMMAND",
		},
		{
			name:      "app command slot option",
			args:      []string{"app", "-fd"},
			wantError: "reploy usage error: unknown option: -fd",
			wantUsage: "Usage: reploy [--docker-timeout DURATION] app COMMAND",
		},
		{
			name:      "app option after explicit dir before command",
			args:      []string{"app", "--dir", "deployment", "-fd"},
			wantError: "reploy usage error: unknown option: -fd",
			wantUsage: "Usage: reploy [--docker-timeout DURATION] app COMMAND",
		},
	}

	globalOnboarding := []string{
		"reploy " + reploy.Version,
		"Usage: reploy COMMAND",
		"reploy stage APP_REF",
		"reploy install APP_REF --scope user|system",
		"Run 'reploy --help' for all commands.",
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(tc.args...)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			if stdout != "" {
				t.Fatalf("stdout = %q, want empty", stdout)
			}
			for _, want := range []string{tc.wantError, tc.wantUsage} {
				if !strings.Contains(stderr, want) {
					t.Fatalf("stderr missing %q:\n%s", want, stderr)
				}
			}
			for _, unexpected := range append(globalOnboarding, tc.forbidExtra...) {
				if strings.Contains(stderr, unexpected) {
					t.Fatalf("stderr contained onboarding text %q:\n%s", unexpected, stderr)
				}
			}
		})
	}
}

func TestPackIndexRefreshLoadsFileIndex(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "reploy-blueprint-index.json")
	if err := os.WriteFile(indexPath, []byte(`{"schema_version":1,"blueprints":{"demo":{"ref":"pypi://demo-pkg/demo_pkg/reploy/demo.blueprint.yaml"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLI("index", "update", "--url", "file:"+indexPath)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "updated blueprint index\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if strings.Contains(stdout, indexPath) || strings.Contains(stdout, "blueprint-index") || strings.Contains(stdout, "shorthands") {
		t.Fatalf("stdout leaked cache details:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestRemovedBlueprintIndexAliasIsUnknown(t *testing.T) {
	code, stdout, stderr := runCLI("blueprint-index", "refresh")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "unknown command: blueprint-index") {
		t.Fatalf("stderr did not reject removed alias:\n%s", stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
}

func TestPackIndexNoArgsShowsNextSteps(t *testing.T) {
	code, stdout, stderr := runCLI("index")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	for _, want := range []string{
		"reploy index usage error: expected command",
		"Usage: reploy index COMMAND",
		"Next steps:",
		"reploy index update",
		"reploy index search QUERY",
		"reploy index show NAME[==PIN]",
		"Run 'reploy index --help' for blueprint index help.",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestPackIndexSearchAndShow(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "reploy-blueprint-index.json")
	if err := os.WriteFile(indexPath, []byte(`{"schema_version":1,"blueprints":{"arbiter-server":{"ref":"pypi://arbiter-server/arbiter_server/reploy/arbiter.blueprint.yaml"},"demo":{"ref":"pypi://demo-pkg/demo_pkg/reploy/demo.blueprint.yaml"},"github-demo":{"ref":"github://acme/demo/demo_pkg/reploy/demo.blueprint.yaml"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(packIndexURLEnv, "file:"+indexPath)

	code, stdout, stderr := runCLI("index", "search", "arbiter")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "arbiter-server\tpypi://arbiter-server/arbiter_server/reploy/arbiter.blueprint.yaml\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	code, stdout, stderr = runCLI("index", "show", "arbiter-server")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{"name: arbiter-server", "ref: pypi://arbiter-server/arbiter_server/reploy/arbiter.blueprint.yaml"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "resolved ref:") {
		t.Fatalf("unpinned show should not print a resolved ref:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	code, stdout, stderr = runCLI("index", "show", "arbiter-server==1.2.3")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"name: arbiter-server",
		"ref: pypi://arbiter-server/arbiter_server/reploy/arbiter.blueprint.yaml",
		"resolved ref: pypi://arbiter-server/arbiter_server/reploy/arbiter.blueprint.yaml?version=1.2.3",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	code, stdout, stderr = runCLI("index", "show", "github-demo==feature/demo")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"name: github-demo",
		"ref: github://acme/demo/demo_pkg/reploy/demo.blueprint.yaml",
		"resolved ref: github://acme/demo/demo_pkg/reploy/demo.blueprint.yaml?ref=feature%2Fdemo",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestPackIndexRefreshDownloadsAndCachesHTTPIndex(t *testing.T) {
	indexContent := `{"schema_version":1,"blueprints":{"demo":{"ref":"pypi://demo-pkg/demo_pkg/reploy/demo.blueprint.yaml"}}}`
	server := newCLITestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, indexContent)
	}))
	defer server.Close()
	cacheDir := filepath.Join(t.TempDir(), "cache")
	t.Setenv("REPLOY_CACHE_DIR", cacheDir)

	code, stdout, stderr := runCLI("index", "update", "--url", server.URL+"/index.json")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	cachePath := packIndexCachePath(server.URL + "/index.json")
	expectedCacheDir := filepath.Dir(cachePath)
	if stdout != "updated blueprint index: "+expectedCacheDir+"\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if strings.Contains(stdout, server.URL) || strings.Contains(stdout, "shorthands") {
		t.Fatalf("stdout leaked cache details:\n%s", stdout)
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("missing cached index: %v", err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestWritePackIndexCachePathPreservesExistingCacheOnPreparationFailure(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*os.File, []byte) error
		want    string
	}{
		{
			name: "partial write",
			prepare: func(temporary *os.File, _ []byte) error {
				if _, err := temporary.Write([]byte(`{"schema_version":1`)); err != nil {
					return err
				}
				return errors.New("injected temporary-file write failure")
			},
			want: "injected temporary-file write failure",
		},
		{
			name: "sync",
			prepare: func(temporary *os.File, content []byte) error {
				if _, err := temporary.Write(content); err != nil {
					return err
				}
				return errors.New("injected temporary-file sync failure")
			},
			want: "injected temporary-file sync failure",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache", "index.json")
			original := []byte(`{"schema_version":1,"blueprints":{"old":{"ref":"file:old"}}}`)
			if err := writePackIndexCachePath(path, original); err != nil {
				t.Fatal(err)
			}

			originalPrepare := preparePackIndexCacheTemporary
			t.Cleanup(func() {
				preparePackIndexCacheTemporary = originalPrepare
			})
			preparePackIndexCacheTemporary = test.prepare

			if err := writePackIndexCachePath(path, []byte(`{"schema_version":1,"blueprints":{}}`)); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("write error = %v", err)
			}
			if content, err := os.ReadFile(path); err != nil {
				t.Fatal(err)
			} else if !bytes.Equal(content, original) {
				t.Fatalf("cache content = %q, want original %q", content, original)
			}
			if matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".blueprint-index-*.tmp")); err != nil {
				t.Fatal(err)
			} else if len(matches) != 0 {
				t.Fatalf("temporary cache files remain: %v", matches)
			}
		})
	}
}

func TestWritePackIndexCachePathPreservesExistingCacheOnReplaceFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache", "index.json")
	original := []byte(`{"schema_version":1,"blueprints":{"old":{"ref":"file:old"}}}`)
	if err := writePackIndexCachePath(path, original); err != nil {
		t.Fatal(err)
	}

	originalReplace := replacePackIndexCacheFile
	t.Cleanup(func() {
		replacePackIndexCacheFile = originalReplace
	})
	replacePackIndexCacheFile = func(string, string) error {
		return errors.New("injected replace failure")
	}

	if err := writePackIndexCachePath(path, []byte(`{"schema_version":1,"blueprints":{}}`)); err == nil ||
		!strings.Contains(err.Error(), "injected replace failure") {
		t.Fatalf("write error = %v", err)
	}
	if content, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	} else if !bytes.Equal(content, original) {
		t.Fatalf("cache content = %q, want original %q", content, original)
	}
}

func TestWritePackIndexCachePathKeepsConcurrentReadsComplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache", "index.json")
	oldContent := []byte(`{"schema_version":1,"blueprints":{"old":{"ref":"file:old"}}}`)
	newContent := []byte(`{"schema_version":1,"blueprints":{"new":{"ref":"file:new"}}}`)
	if err := writePackIndexCachePath(path, oldContent); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			content, err := readPackIndexPath(path)
			if err != nil {
				done <- err
				return
			}
			if _, err := parsePackIndex(content); err != nil {
				done <- fmt.Errorf("parse concurrently read cache: %w", err)
				return
			}
		}
	}()
	for index := 0; index < 20; index++ {
		content := oldContent
		if index%2 == 0 {
			content = newContent
		}
		if err := writePackIndexCachePath(path, content); err != nil {
			close(stop)
			<-done
			t.Fatal(err)
		}
	}
	close(stop)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestLoadPackIndexReportsRefreshAndCorruptFallback(t *testing.T) {
	server := newCLITestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	t.Setenv("REPLOY_CACHE_DIR", t.TempDir())
	indexURL := server.URL + "/index.json"
	cachePath := packIndexCachePath(indexURL)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte(`{"schema_version":`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := loadPackIndex(indexURL)
	if err == nil {
		t.Fatal("loadPackIndex() error = nil")
	}
	for _, want := range []string{
		"503 Service Unavailable",
		"cached blueprint index " + cachePath + " is invalid",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("loadPackIndex() error = %q, want %q", err, want)
		}
	}
}

func TestDockerHelp(t *testing.T) {
	code, stdout, stderr := runCLI("--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Usage: reploy [--docker-timeout DURATION] COMMAND") {
		t.Fatalf("stdout did not contain deployment usage:\n%s", stdout)
	}
	if strings.Contains(stdout, "  --docker ") || !strings.Contains(stdout, "--docker-timeout DURATION") || !strings.Contains(stdout, "--aws") {
		t.Fatalf("stdout has incorrect runtime options:\n%s", stdout)
	}
	if strings.Contains(stdout, "smoke") {
		t.Fatalf("stdout should not contain premature smoke command:\n%s", stdout)
	}
	if strings.Contains(stdout, "Demo health endpoint") || !strings.Contains(stdout, "staging app health endpoint") {
		t.Fatalf("stdout did not describe generic health probe:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Bundle:") || !strings.Contains(stdout, "add-package") || !strings.Contains(stdout, "clean") || strings.Contains(stdout, "upgrade      Upgrade") {
		t.Fatalf("stdout did not contain bundle command tree:\n%s", stdout)
	}
	if !strings.Contains(stdout, "clean        Remove the deployment-local provider cache") || strings.Contains(stdout, "clean        Remove built installation artifacts") {
		t.Fatalf("stdout misdescribed bundle clean:\n%s", stdout)
	}
	if !strings.Contains(stdout, "validate     Validate blueprint syntax and semantics") {
		t.Fatalf("stdout did not contain blueprint validation command:\n%s", stdout)
	}
	for _, want := range []string{"app          Run a staged app command from the current build", "up           Build if needed", "down         Stop and remove", "start        Alias for up", "stop         Alias for down", "restart      Build if needed"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout did not disclose automatic staged builds with %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stdout, "Staged up and restart may build with Docker and download packages") {
		t.Fatalf("stdout did not disclose automatic build cost:\n%s", stdout)
	}
	if strings.Contains(stdout, "add-wheel") || strings.Contains(stdout, "add-source") {
		t.Fatalf("stdout exposed internal bundle artifact helpers:\n%s", stdout)
	}
	if !strings.Contains(stdout, "options") || !strings.Contains(stdout, "List the current request overlay") || strings.Contains(stdout, "list-options") {
		t.Fatalf("stdout did not contain the state-v1 bundle inspection commands:\n%s", stdout)
	}
	for _, want := range []string{"install      Install or update a deployed host service", "Run 'reploy COMMAND --help' for command-specific options."} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing help text %q:\n%s", want, stdout)
		}
	}
	for _, commandSpecific := range []string{"--preinstall", "--follow", "--wait", "--drain", "--to DIR", "--scope user|system", "--port NAME=PORT", "--replace PATH", "--remove-dir", "--extra ROOT"} {
		if strings.Contains(stdout, commandSpecific) {
			t.Fatalf("stdout mixed command-specific option %q into top-level help:\n%s", commandSpecific, stdout)
		}
	}
	if strings.Contains(stdout, "--in-place") {
		t.Fatalf("stdout retained removed --in-place option:\n%s", stdout)
	}
	if strings.Contains(stdout, "Install or update a deployed host service from staging") {
		t.Fatalf("stdout did not contain install command/options:\n%s", stdout)
	}
	if !strings.Contains(stdout, "app") {
		t.Fatalf("stdout did not contain app command:\n%s", stdout)
	}
	if strings.Contains(stdout, "bootstrap demo") || strings.Contains(stdout, "imap account") {
		t.Fatalf("stdout contained app-specific examples in generic help:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestRemovedDockerTargetOptionIsRejected(t *testing.T) {
	code, stdout, stderr := runCLI("--docker", "--help")
	if code != 2 || stdout != "" {
		t.Fatalf("exit/stdout = %d/%q", code, stdout)
	}
	if !strings.Contains(stderr, "unknown option: --docker") || strings.Contains(stderr, "[--docker]") {
		t.Fatalf("removed option error = %q", stderr)
	}
}

func TestBlueprintValidateHelpDescribesReadOnlyBasicValidation(t *testing.T) {
	code, stdout, stderr := runCLI("validate", "--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, want := range []string{
		"Usage: reploy validate BLUEPRINT_REF",
		"syntax and semantics",
		"does not create staging",
		"state, contact Docker",
		"resolve provider packages",
		"build an image",
		"source cache",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "--dry-run") || stderr != "" {
		t.Fatalf("stdout/stderr = %q/%q", stdout, stderr)
	}
}

func TestBlueprintValidateAcceptsAPTOnlyBlueprintWithoutCreatingStaging(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	manifest := filepath.Join(root, "apt-demo.blueprint.yaml")
	content := `blueprint:
  schema: 1
  version: 0.1.0
  compatibility:
    platforms: [linux/amd64]
environment:
  id: apt-demo
  base:
    image: debian:13
  applications:
    tools:
      packages:
        os:
          - package: curl
docker: {}
`
	if err := os.WriteFile(manifest, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	stagingDir := filepath.Join(root, "reploy-staging")
	code, stdout, stderr := runCLI("validate", manifest)
	if code != 0 {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if stdout != "pass: apt-demo (syntax and semantics)\n" || stderr != "" {
		t.Fatalf("stdout/stderr = %q/%q", stdout, stderr)
	}
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Fatalf("validation created staging state at %s: %v", stagingDir, err)
	}
}

func TestBlueprintValidateReportsSemanticErrors(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "invalid.blueprint.yaml")
	content := `blueprint:
  schema: 1
  compatibility:
    platforms: [linux/amd64]
environment:
  id: invalid
  base:
    image: debian:13
  applications:
    application:
      packages:
        os: [curl]
docker: {}
`
	if err := os.WriteFile(manifest, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCLI("validate", manifest)
	if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "fail: ") || !strings.Contains(stderr, "blueprint.version is required") {
		t.Fatalf("exit/stdout/stderr = %d/%q/%q", code, stdout, stderr)
	}
}

func TestBlueprintValidateColorsOnlyPassAndFailStatus(t *testing.T) {
	valid := filepath.Join(t.TempDir(), "valid.blueprint.yaml")
	if err := os.WriteFile(valid, []byte(cliTestPackManifest()), 0o644); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(t.TempDir(), "invalid.blueprint.yaml")
	if err := os.WriteFile(invalid, []byte("blueprint: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REPLOY_COLOR", "always")
	code, stdout, stderr := runCLI("validate", valid)
	if code != 0 || !strings.HasPrefix(stdout, "\x1b[32mpass\x1b[0m: ") || stderr != "" {
		t.Fatalf("colored pass = %d/%q/%q", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI("validate", invalid)
	if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "\x1b[31mfail\x1b[0m: ") {
		t.Fatalf("colored fail = %d/%q/%q", code, stdout, stderr)
	}
}

func TestBlueprintValidateRejectsMissingDuplicateAndUnknownOption(t *testing.T) {
	for _, args := range [][]string{
		{"validate"},
		{"validate", "one", "two"},
		{"validate", "--level"},
	} {
		code, stdout, stderr := runCLI(args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "reploy validate usage error:") || !strings.Contains(stderr, "Usage: reploy validate BLUEPRINT_REF") {
			t.Fatalf("args/exit/stdout/stderr = %#v/%d/%q/%q", args, code, stdout, stderr)
		}
	}
}

func TestDockerInstallHelpShowsPortOptions(t *testing.T) {
	code, stdout, stderr := runCLI("install", "--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, want := range []string{"--scope user|system", "--port PORT", "--port NAME=PORT", "--replace PATH", "--clean", "may require Docker and package-network access"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout did not contain install option %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "--in-place") {
		t.Fatalf("stdout retained removed --in-place option:\n%s", stdout)
	}
	if strings.Contains(stdout, "--dry-run") {
		t.Fatalf("stdout retained removed install --dry-run option:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerUninstallHelpShowsOnlyImplementedOptions(t *testing.T) {
	code, stdout, stderr := runCLI("uninstall", "--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, want := range []string{"Usage:", "uninstall [OPTIONS]", "--from DIR", "--service-name NAME", "recover a deleted", "--remove-dir", "--wait", "--drain", "--force"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout did not contain uninstall option %q:\n%s", want, stdout)
		}
	}
	for _, removed := range []string{"--dry-run", "--in-place"} {
		if strings.Contains(stdout, removed) {
			t.Fatalf("stdout retained removed option %q:\n%s", removed, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestAppHelp(t *testing.T) {
	code, stdout, stderr := runCLI("app", "--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Usage: reploy [--docker-timeout DURATION] app COMMAND") {
		t.Fatalf("stdout did not contain app usage:\n%s", stdout)
	}
	if !strings.Contains(stdout, "current staged build, not a host executable") {
		t.Fatalf("stdout did not explain staging app runtime:\n%s", stdout)
	}
	if !strings.Contains(stdout, "run reploy build") || !strings.Contains(stdout, "app commands do not resolve packages or rebuild it") {
		t.Fatalf("stdout did not explain the explicit app build boundary:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Show this staging directory's app subcommands") || !strings.Contains(stdout, "reploy app COMMAND") {
		t.Fatalf("stdout did not contain generic app command guidance:\n%s", stdout)
	}
	for _, want := range []string{"--output-dir DIR", "REPLOY_OUTPUT_DIR", "--output-file FILE", "REPLOY_OUTPUT_FILE", "--wait"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout did not document %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "Demo") || strings.Contains(stdout, "bootstrap plugin PLUGIN account NAME") {
		t.Fatalf("stdout contained app-specific help:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestParseDockerAppOutputOptions(t *testing.T) {
	options, err := parseDockerAppOptions([]string{"--wait", "--output-dir", "results", "export"})
	if err != nil {
		t.Fatal(err)
	}
	if options.OutputDir != "results" || options.OutputFile != "" || !options.Wait || !reflect.DeepEqual(options.CommandArgs, []string{"export"}) {
		t.Fatalf("output-dir options = %#v", options)
	}
	options, err = parseDockerAppOptions([]string{"--output-file=report.json", "export"})
	if err != nil {
		t.Fatal(err)
	}
	if options.OutputFile != "report.json" || options.OutputDir != "" || !reflect.DeepEqual(options.CommandArgs, []string{"export"}) {
		t.Fatalf("output-file options = %#v", options)
	}
	for _, args := range [][]string{
		{"--output-dir", "results", "--output-file", "report.json", "export"},
		{"--output-dir=", "export"},
		{"--output-file", "", "export"},
	} {
		if _, err := parseDockerAppOptions(args); err == nil {
			t.Fatalf("parseDockerAppOptions(%#v) unexpectedly succeeded", args)
		}
	}
}

func TestAppWaitRequiresCommand(t *testing.T) {
	for _, args := range [][]string{{"app", "--wait"}, {"app", "--commands", "--wait"}} {
		code, _, stderr := runCLI(args...)
		if code != 2 || !strings.Contains(stderr, "--wait requires an app command") {
			t.Fatalf("args=%v code=%d stderr=%q", args, code, stderr)
		}
	}
}

func TestShellWaitHelpAndParsing(t *testing.T) {
	code, stdout, stderr := runCLI("shell", "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "--wait") || !strings.Contains(stdout, "Usage: reploy") {
		t.Fatalf("shell help: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	options, err := parseDockerShellOptions([]string{"--dir", "deployment", "--wait", "--read-only"})
	if err != nil || options.Dir != "deployment" || !options.Wait || !options.ReadOnly {
		t.Fatalf("shell options = %#v, %v", options, err)
	}
	if !strings.Contains(stdout, "--read-only") {
		t.Fatalf("shell help does not describe read-only mode: %q", stdout)
	}
	runtimeOptions, err := parseDockerRuntimeOptions([]string{"--wait"})
	if err != nil || runtimeOptions.ControlMode != dockerdeploy.ControlAdmissionWaitV1 {
		t.Fatalf("runtime wait options = %#v, %v", runtimeOptions, err)
	}
	if _, err := parseDockerShellOptions([]string{"--drain"}); err == nil || !strings.Contains(err.Error(), "unknown option") {
		t.Fatalf("shell accepted control drain: %v", err)
	}
}

func TestDockerRuntimeControlAdmissionOptions(t *testing.T) {
	for flag, want := range map[string]dockerdeploy.ControlAdmissionModeV1{
		"--wait":  dockerdeploy.ControlAdmissionWaitV1,
		"--drain": dockerdeploy.ControlAdmissionDrainV1,
		"--force": dockerdeploy.ControlAdmissionForceV1,
	} {
		options, err := parseDockerRuntimeOptions([]string{flag})
		if err != nil || options.ControlMode != want {
			t.Fatalf("%s options = %#v, %v", flag, options, err)
		}
	}
	if _, err := parseDockerRuntimeOptions([]string{"--wait", "--force"}); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("conflicting control modes error = %v", err)
	}
}

func TestDockerLifecycleHelpDocumentsControlAdmissionOptions(t *testing.T) {
	for _, command := range []string{"down", "stop", "restart"} {
		code, stdout, stderr := runCLI(command, "--help")
		if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage: reploy "+command+" [OPTIONS]") {
			t.Fatalf("%s help: code=%d stdout=%q stderr=%q", command, code, stdout, stderr)
		}
		for _, want := range []string{"--wait", "active jobs", "three seconds", "Ctrl-C"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("%s help missing %q:\n%s", command, want, stdout)
			}
		}
		for _, removed := range []string{"--drain", "--force"} {
			if strings.Contains(stdout, removed) {
				t.Fatalf("%s help retained %s:\n%s", command, removed, stdout)
			}
		}
	}
	for _, command := range []string{"up", "start"} {
		code, stdout, stderr := runCLI(command, "--help")
		if code != 0 || stderr != "" {
			t.Fatalf("%s help: code=%d stdout=%q stderr=%q", command, code, stdout, stderr)
		}
		for _, flag := range []string{"--wait", "--drain", "--force"} {
			if !strings.Contains(stdout, flag) {
				t.Fatalf("%s help missing %s:\n%s", command, flag, stdout)
			}
		}
	}
}

func TestAppCommandsRejectsOutputOptionsWithoutCommand(t *testing.T) {
	code, _, stderr := runCLI("app", "--commands", "--output-dir", "results")
	if code != 2 || !strings.Contains(stderr, "output options require an app command") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	code, _, stderr = runCLI("app", "--output-file", "report.json")
	if code != 2 || !strings.Contains(stderr, "output options require an app command") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestParseDockerAppOptionsPreservesAppArgs(t *testing.T) {
	options, err := parseDockerAppOptions([]string{"--dir", "deployment", "bootstrap", "plugin", "imap", "account", "primary", "--force"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Dir != "deployment" {
		t.Fatalf("dir = %q", options.Dir)
	}
	if got := strings.Join(options.CommandArgs, " "); got != "bootstrap plugin imap account primary --force" {
		t.Fatalf("command args = %q", got)
	}

	options, err = parseDockerAppOptions([]string{"--dir", "deployment", "config", "check"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Dir != "deployment" {
		t.Fatalf("dir = %q", options.Dir)
	}
	if got := strings.Join(options.CommandArgs, " "); got != "config check" {
		t.Fatalf("command args = %q", got)
	}
}

func TestParseDockerAppOptionsForwardsWaitAfterSeparator(t *testing.T) {
	options, err := parseDockerAppOptions([]string{"export", "--", "--wait"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Wait || !reflect.DeepEqual(options.CommandArgs, []string{"export", "--", "--wait"}) {
		t.Fatalf("app wait forwarding = %#v", options)
	}
}

func TestParseDockerAppOptionsForwardsOutputOptionNamesAfterSeparator(t *testing.T) {
	for _, args := range [][]string{
		{"export", "--", "--output-dir", "results"},
		{"export", "--", "--output-dir="},
		{"export", "--", "--output-file", "report.json"},
		{"export", "--", "--output-file="},
	} {
		options, err := parseDockerAppOptions(args)
		if err != nil {
			t.Fatalf("parseDockerAppOptions(%#v): %v", args, err)
		}
		if options.OutputDir != "" || options.OutputFile != "" || !reflect.DeepEqual(options.CommandArgs, args) {
			t.Fatalf("parseDockerAppOptions(%#v) = %#v", args, options)
		}
	}
}

func expectedDemoAppSummary() string {
	return "[STAGING : demo] app: demo\n" +
		"[STAGING : demo] app subcommands:\n" +
		"[STAGING : demo]   bootstrap plugin\n" +
		"[STAGING : demo]   bootstrap server\n" +
		"[STAGING : demo]   config activate\n" +
		"[STAGING : demo]   config check\n" +
		"[STAGING : demo]   config show\n" +
		"[STAGING : demo]   env bootstrap\n" +
		"[STAGING : demo]   env check\n"
}

func expectedBareDemoStagingSummary(dir string) string {
	return "[STAGING : demo] app: demo\n" +
		"[STAGING : demo] reploy: " + reploy.DisplayVersion() + "\n" +
		"[STAGING : demo] context: staged deployment\n" +
		"[STAGING : demo] directory: " + dir + "\n" +
		"[STAGING : demo] useful commands:\n" +
		"[STAGING : demo]   reploy info\n" +
		"[STAGING : demo]   reploy bundle list\n" +
		"[STAGING : demo]   reploy up|down|status\n" +
		"[STAGING : demo]   reploy logs --tail 50\n" +
		"[STAGING : demo]   reploy install --scope user --to DIR\n" +
		"[STAGING : demo] app command examples:\n" +
		"[STAGING : demo]   reploy app bootstrap plugin\n" +
		"[STAGING : demo]   reploy app bootstrap server\n" +
		"[STAGING : demo]   reploy app config activate\n" +
		"[STAGING : demo]   reploy app ...\n" +
		"[STAGING : demo] Run 'reploy app' for all app commands.\n"
}

func expectedBareDemoInstalledSummary(dir string) string {
	return "[DEPLOYED : demo] app: demo\n" +
		"[DEPLOYED : demo] reploy: " + reploy.DisplayVersion() + "\n" +
		"[DEPLOYED : demo] context: installed deployment\n" +
		"[DEPLOYED : demo] directory: " + dir + "\n" +
		"[DEPLOYED : demo] useful commands:\n" +
		"[DEPLOYED : demo]   reploy up|down|status\n" +
		"[DEPLOYED : demo]   reploy logs --tail 100\n" +
		"[DEPLOYED : demo]   reploy restart\n" +
		"[DEPLOYED : demo]   reploy uninstall --from .\n" +
		"[DEPLOYED : demo] app command examples:\n" +
		"[DEPLOYED : demo]   reploy app --deployed-only config check\n" +
		"[DEPLOYED : demo] Run 'reploy app --deployed-only' for all app commands.\n"
}

func TestAppShowsAppIDAndPackSubcommands(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("app", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("app failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	expected := expectedDemoAppSummary()
	if stdout != expected {
		t.Fatalf("stdout = %q, want %q", stdout, expected)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestAppCommandsDeployedOnlyJSON(t *testing.T) {
	manifest := strings.Replace(
		cliTestPackManifest(),
		"      native_command: true\n      forward_flags: [--live]\n",
		"      native_command: true\n      deployed_command: true\n      forward_flags: [--live]\n",
		1,
	)
	packDir := makeCLITestPackWithManifest(t, manifest)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("app", "--commands", "--deployed-only", "--format", "json", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("app --commands failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "[STAGING") {
		t.Fatalf("json stdout should not be status-prefixed:\n%s", stdout)
	}
	var result dockerdeploy.AppCommandListResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if result.AppID != "demo" {
		t.Fatalf("app id = %q, want demo", result.AppID)
	}
	if len(result.Commands) != 1 {
		t.Fatalf("commands = %#v, want one deployed command", result.Commands)
	}
	if got := strings.Join(result.Commands[0].Trigger, " "); got != "config check" {
		t.Fatalf("deployed trigger = %q, want config check", got)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestEmbeddedControlRunsDeployedAppCommandWithScriptPrefix(t *testing.T) {
	manifest := strings.Replace(
		cliTestPackManifest(),
		"      native_command: true\n      forward_flags: [--live]\n",
		"      native_command: true\n      deployed_command: true\n      forward_flags: [--live]\n",
		1,
	)
	packDir := makeCLITestPackWithManifest(t, manifest)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	markCLITestDeploymentInstalled(t, deployDir)
	appArgs, matched, err := embeddedControlAppArguments(deployDir, []string{"--output-file", "report.json", "--wait", "config", "check"}, true)
	wantAppArgs := []string{"--deployed-only", "--dir", deployDir, "--output-file", "report.json", "--wait", "config", "check"}
	if err != nil || !matched || !reflect.DeepEqual(appArgs, wantAppArgs) {
		t.Fatalf("embedded output app args = %#v, matched=%v, error=%v", appArgs, matched, err)
	}
	helpCode, helpStdout, helpStderr := runCLI("_control", "--dir", deployDir, "--script-name", "democtl", "--help")
	if helpCode != 0 || helpStderr != "" || !strings.Contains(helpStdout, "--output-dir DIR") || !strings.Contains(helpStdout, "--output-file FILE") || !strings.Contains(helpStdout, "--wait") {
		t.Fatalf("embedded control help: code=%d stdout=%q stderr=%q", helpCode, helpStdout, helpStderr)
	}
}

func TestEmbeddedControlForwardsOutputOptionNamesAfterSeparator(t *testing.T) {
	manifest := strings.Replace(
		cliTestPackManifest(),
		"      native_command: true\n      forward_flags: [--live]\n",
		"      native_command: true\n      deployed_command: true\n      forward_flags: [--live]\n",
		1,
	)
	packDir := makeCLITestPackWithManifest(t, manifest)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	markCLITestDeploymentInstalled(t, deployDir)
	for _, commandArgs := range [][]string{
		{"config", "check", "--", "--output-dir", "results"},
		{"config", "check", "--", "--output-file="},
	} {
		appArgs, matched, err := embeddedControlAppArguments(deployDir, commandArgs, true)
		want := append([]string{"--deployed-only", "--dir", deployDir}, commandArgs...)
		if err != nil || !matched || !reflect.DeepEqual(appArgs, want) {
			t.Fatalf("embeddedControlAppArguments(%#v) = %#v, matched=%v, error=%v; want %#v", commandArgs, appArgs, matched, err, want)
		}
	}
}

func TestEmbeddedControlUsesStagedAppCommandsBeforeInstall(t *testing.T) {
	packDir := makeCLITestPackWithManifest(t, cliTestPackManifest())
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	appArgs, matched, err := embeddedControlAppArguments(
		deployDir, []string{"--output-file", "report.json", "--wait", "config", "check"}, false,
	)
	wantAppArgs := []string{"--dir", deployDir, "--output-file", "report.json", "--wait", "config", "check"}
	if err != nil || !matched || !reflect.DeepEqual(appArgs, wantAppArgs) {
		t.Fatalf("staged embedded app args = %#v, matched=%v, error=%v", appArgs, matched, err)
	}
	helpCode, helpStdout, helpStderr := runCLI("_control", "--dir", deployDir, "--script-name", "democtl", "--help")
	if helpCode != 0 || helpStderr != "" || !strings.Contains(helpStdout, "config check") {
		t.Fatalf("staged embedded control help: code=%d stdout=%q stderr=%q", helpCode, helpStdout, helpStderr)
	}
}

func TestEmbeddedControlMissingMetadataUsesNeutralMessage(t *testing.T) {
	code, stdout, stderr := runCLI("_control", "--dir", t.TempDir(), "--script-name", "democtl", "status")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "staged or installed state-v1 deployment metadata is missing") {
		t.Fatalf("embedded control missing metadata: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestEmbeddedControlLogsHelpDescribesStagedAndInstalledWorkloads(t *testing.T) {
	var output strings.Builder
	printEmbeddedControlLogsHelp(&output, embeddedControlUsageContext{ScriptName: "democtl"})
	if !strings.Contains(output.String(), "Show workload logs.") || strings.Contains(output.String(), "deployed service") {
		t.Fatalf("logs help = %q", output.String())
	}
}

func TestAppCommandDelegatesStagedCommand(t *testing.T) {
	packDir := makeCLITestPackWithManifest(t, cliTestPackManifest())
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	original := dockerAppCommand
	t.Cleanup(func() { dockerAppCommand = original })
	var got dockerdeploy.AppCommandOptions
	dockerAppCommand = func(options dockerdeploy.AppCommandOptions) error {
		got = options
		return nil
	}

	code, stdout, stderr = runCLI("app", "--dir", deployDir, "config", "check")
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("staged app command = %d/%q/%q", code, stdout, stderr)
	}
	if got.Dir != deployDir || !reflect.DeepEqual(got.CommandArgs, []string{"config", "check"}) || got.DeployedOnly {
		t.Fatalf("app command options = %#v", got)
	}
}

func TestShellPreservesContainerExitStatus(t *testing.T) {
	packDir := makeCLITestPackWithManifest(t, cliTestPackManifest())
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	previous := dockerShell
	dockerShell = func(dockerdeploy.ShellOptions) error {
		var command *exec.Cmd
		if runtime.GOOS == "windows" {
			command = exec.Command("cmd", "/c", "exit", "42")
		} else {
			command = exec.Command("sh", "-c", "exit 42")
		}
		return fmt.Errorf("wrapped shell failure: %w", command.Run())
	}
	t.Cleanup(func() { dockerShell = previous })

	code, _, stderr = runCLI("shell", "--dir", deployDir)
	if code != 42 {
		t.Fatalf("shell exit code = %d, want 42; stderr:\n%s", code, stderr)
	}
}

func TestShellReportsIntentionalStopWithoutDockerStatus(t *testing.T) {
	packDir := makeCLITestPackWithManifest(t, cliTestPackManifest())
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	previous := dockerShell
	dockerShell = func(options dockerdeploy.ShellOptions) error {
		if options.ReadOnly {
			t.Fatal("default shell unexpectedly became read-only")
		}
		return dockerdeploy.ErrLiveRunStoppedV1
	}
	t.Cleanup(func() { dockerShell = previous })

	code, stdout, stderr = runCLI("shell", "--dir", deployDir)
	if code != 130 {
		t.Fatalf("shell stop exit code = %d, want 130", code)
	}
	if !strings.Contains(stdout, "shell stopped by `reploy runs stop`.") || strings.Contains(stdout+stderr, "137") || stderr != "" {
		t.Fatalf("shell stop output = stdout %q, stderr %q", stdout, stderr)
	}
}

func TestShellCancellationReturnsInterruptionStatusWithoutError(t *testing.T) {
	packDir := makeCLITestPackWithManifest(t, cliTestPackManifest())
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	previous := dockerShell
	dockerShell = func(dockerdeploy.ShellOptions) error { return context.Canceled }
	t.Cleanup(func() { dockerShell = previous })

	code, stdout, stderr = runCLI("shell", "--dir", deployDir)
	if code != 130 || stdout != "" || stderr != "" {
		t.Fatalf("canceled shell = code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func TestEmbeddedControlRuntimeAcceptsInstalledDeploymentDir(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	installDir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, installDir, "demo", "demo-service")
	if err := os.WriteFile(filepath.Join(installDir, dockerdeploy.DockerEnvFileName), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	oldRuntime := dockerRuntime
	t.Cleanup(func() {
		dockerRuntime = oldRuntime
	})
	dockerRuntime = func(options dockerdeploy.RuntimeOptions) error {
		if options.Dir != installDir {
			t.Fatalf("dir = %q, want %q", options.Dir, installDir)
		}
		if options.Action != "status" {
			t.Fatalf("action = %q, want status", options.Action)
		}
		fmt.Fprintln(options.Stdout, "installed status")
		return nil
	}

	code, stdout, stderr := runCLI("_control", "--dir", installDir, "--script-name", "democtl", "status")
	if code != 0 {
		t.Fatalf("_control status failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "installed status\n" {
		t.Fatalf("stdout = %q, want installed status", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestEmbeddedControlSystemdStatusAndLogsUseSharedRuntimeObservation(t *testing.T) {
	installDir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, installDir, "demo", "demo-service")
	markCLITestSystemd(t, installDir, filepath.Join(installDir, "demo-service.service"))

	oldRuntime := dockerRuntime
	t.Cleanup(func() { dockerRuntime = oldRuntime })
	var calls []dockerdeploy.RuntimeOptions
	dockerRuntime = func(options dockerdeploy.RuntimeOptions) error {
		calls = append(calls, options)
		return nil
	}

	for _, command := range [][]string{
		{"_control", "--dir", installDir, "status"},
		{"_control", "--dir", installDir, "logs", "--timestamps", "--tail", "10"},
	} {
		code, stdout, stderr := runCLI(command...)
		if code != 0 || stdout != "" || stderr != "" {
			t.Fatalf("%v: code=%d stdout=%q stderr=%q", command, code, stdout, stderr)
		}
	}
	if len(calls) != 2 || calls[0].Action != "status" || calls[1].Action != "logs" ||
		!calls[1].Timestamps || calls[1].Tail != "10" {
		t.Fatalf("shared runtime observation calls = %#v", calls)
	}
}

func TestEmbeddedControlSystemLifecycleUsesRuntimeAdmission(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	installDir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, installDir, "demo", "demo-service")
	markCLITestSystemd(t, installDir, filepath.Join(installDir, "demo-service.service"))
	helpCode, helpStdout, helpStderr := runCLI("_control", "--dir", installDir, "--script-name", "democtl", "--help")
	if helpCode != 0 || helpStderr != "" || !strings.Contains(helpStdout, "down/restart --wait") ||
		!strings.Contains(helpStdout, "start (alias for up)") || !strings.Contains(helpStdout, "stop (alias for down)") {
		t.Fatalf("system lifecycle help: code=%d stdout=%q stderr=%q", helpCode, helpStdout, helpStderr)
	}

	oldRuntime := dockerRuntime
	t.Cleanup(func() { dockerRuntime = oldRuntime })
	var calls []dockerdeploy.RuntimeOptions
	dockerRuntime = func(options dockerdeploy.RuntimeOptions) error {
		calls = append(calls, options)
		return nil
	}

	for _, test := range []struct {
		command string
		args    []string
		action  string
		mode    dockerdeploy.ControlAdmissionModeV1
	}{
		{command: "start", action: "up"},
		{command: "down", args: []string{"--wait"}, action: "down", mode: dockerdeploy.ControlAdmissionWaitV1},
		{command: "stop", args: []string{"--wait"}, action: "down", mode: dockerdeploy.ControlAdmissionWaitV1},
		{command: "restart", action: "restart"},
	} {
		arguments := []string{"_control", "--dir", installDir, "--script-name", "democtl", test.command}
		arguments = append(arguments, test.args...)
		code, stdout, stderr := runCLI(arguments...)
		if code != 0 || stdout != "" {
			t.Fatalf("%s: code=%d stdout=%q stderr=%q", test.command, code, stdout, stderr)
		}
		if len(calls) == 0 {
			t.Fatalf("%s bypassed runtime admission: stderr=%q", test.command, stderr)
		}
		got := calls[len(calls)-1]
		if got.Dir != installDir || got.Action != test.action || got.ControlMode != test.mode {
			t.Fatalf("%s runtime options = %#v", test.command, got)
		}
	}
	if len(calls) != 4 {
		t.Fatalf("runtime calls = %d, want 4", len(calls))
	}
}

func TestEmbeddedControlSystemdStopRecoversThroughPublicRuntime(t *testing.T) {
	requireLinuxHost(t)
	installDir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, installDir, "demo", "demo-service")
	markCLITestSystemd(t, installDir, filepath.Join(installDir, "demo-service.service"))

	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "systemctl.log")
	systemctl := filepath.Join(binDir, "systemctl")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$REPLOY_TEST_SYSTEMCTL_LOG\"\n"
	if err := os.WriteFile(systemctl, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("REPLOY_TEST_SYSTEMCTL_LOG", logPath)

	code, _, stderr := runCLI("_control", "--dir", installDir, "stop")
	if code != 0 {
		t.Fatalf("systemd stop recovery = %d, stderr=%q", code, stderr)
	}
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(content)); got != "stop demo-service.service" {
		t.Fatalf("systemctl args = %q", got)
	}
}

func TestEmbeddedControlLogsHelp(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	installDir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, installDir, "demo", "demo-service")

	code, stdout, stderr := runCLI("_control", "--dir", installDir, "--script-name", "democtl", "logs", "--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Usage: democtl logs [OPTIONS]") {
		t.Fatalf("stdout did not contain control logs usage:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--tail N") || !strings.Contains(stdout, "default: 100") {
		t.Fatalf("stdout did not contain bounded tail help:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--tail all") || !strings.Contains(stdout, "complete available log") {
		t.Fatalf("stdout did not contain full log help:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--follow, -f") {
		t.Fatalf("stdout did not contain follow help:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestEmbeddedControlLogsDefaultsToBoundedTail(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	installDir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, installDir, "demo", "demo-service")
	if err := os.WriteFile(filepath.Join(installDir, dockerdeploy.DockerEnvFileName), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	oldRuntime := dockerRuntime
	t.Cleanup(func() {
		dockerRuntime = oldRuntime
	})
	dockerRuntime = func(options dockerdeploy.RuntimeOptions) error {
		if options.Action != "logs" {
			t.Fatalf("action = %q, want logs", options.Action)
		}
		if options.Dir != installDir {
			t.Fatalf("dir = %q, want %q", options.Dir, installDir)
		}
		if options.Tail != "100" {
			t.Fatalf("tail = %q, want 100", options.Tail)
		}
		if !options.Follow {
			t.Fatal("follow = false, want true")
		}
		fmt.Fprintln(options.Stdout, "installed logs")
		return nil
	}

	code, stdout, stderr := runCLI("_control", "--dir", installDir, "--script-name", "democtl", "logs", "--follow")
	if code != 0 {
		t.Fatalf("_control logs failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "installed logs\n" {
		t.Fatalf("stdout = %q, want installed logs", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestEmbeddedControlLogsTailAllDisablesBoundedTail(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	installDir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, installDir, "demo", "demo-service")
	if err := os.WriteFile(filepath.Join(installDir, dockerdeploy.DockerEnvFileName), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	oldRuntime := dockerRuntime
	t.Cleanup(func() {
		dockerRuntime = oldRuntime
	})
	dockerRuntime = func(options dockerdeploy.RuntimeOptions) error {
		if options.Tail != "" {
			t.Fatalf("tail = %q, want empty full-log tail", options.Tail)
		}
		if !options.Follow {
			t.Fatal("follow = false, want true")
		}
		return nil
	}

	code, stdout, stderr := runCLI("_control", "--dir", installDir, "--script-name", "democtl", "logs", "--tail", "all", "--follow")
	if code != 0 {
		t.Fatalf("_control logs failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestEmbeddedControlLogsTailAllUsesLastTailValue(t *testing.T) {
	args := embeddedControlLogsArgs([]string{"--tail", "25", "--tail=all", "--follow"})
	if got, want := strings.Join(args, " "), "--follow"; got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}

	args = embeddedControlLogsArgs([]string{"--tail=all", "--tail", "25", "--follow"})
	if got, want := strings.Join(args, " "), "--tail=all --tail 25 --follow"; got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

func TestEmbeddedControlLogsPreservesExplicitTail(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	installDir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, installDir, "demo", "demo-service")
	if err := os.WriteFile(filepath.Join(installDir, dockerdeploy.DockerEnvFileName), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	oldRuntime := dockerRuntime
	t.Cleanup(func() {
		dockerRuntime = oldRuntime
	})
	dockerRuntime = func(options dockerdeploy.RuntimeOptions) error {
		if options.Tail != "25" {
			t.Fatalf("tail = %q, want 25", options.Tail)
		}
		return nil
	}

	code, stdout, stderr := runCLI("_control", "--dir", installDir, "--script-name", "democtl", "logs", "--tail=25")
	if code != 0 {
		t.Fatalf("_control logs failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestEmbeddedControlHealthRejectsUnexpectedArgs(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	markCLITestDeploymentInstalled(t, deployDir)

	code, stdout, stderr = runCLI("_control", "--dir", deployDir, "--script-name", "democtl", "health", "extra")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "health: unexpected argument: extra") {
		t.Fatalf("stderr missing unexpected argument error:\n%s", stderr)
	}
}

func TestAppFormatRequiresCommands(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("app", "--format", "json", "--dir", deployDir)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "--format is only supported with --commands") {
		t.Fatalf("stderr did not explain --format requirement:\n%s", stderr)
	}
}

func TestAppUsesCurrentDeploymentDirByDefault(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	t.Chdir(deployDir)

	code, stdout, stderr = runCLI("app")
	if code != 0 {
		t.Fatalf("app failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	expected := expectedDemoAppSummary()
	if stdout != expected {
		t.Fatalf("stdout = %q, want %q", stdout, expected)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestNoArgsUsesDefaultStagingDirByDefault(t *testing.T) {
	packDir := makeCLITestPack(t)
	workDir := t.TempDir()
	t.Chdir(workDir)

	code, stdout, stderr := runCLI("stage", "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI()
	if code != 0 {
		t.Fatalf("no-args failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	expected := expectedBareDemoStagingSummary(filepath.Join(workDir, "reploy-staging"))
	if stdout != expected {
		t.Fatalf("stdout = %q, want %q", stdout, expected)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestNoArgsUsesCurrentDeploymentDirByDefault(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	t.Chdir(deployDir)

	code, stdout, stderr = runCLI()
	if code != 0 {
		t.Fatalf("no-args failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	expected := expectedBareDemoStagingSummary(deployDir)
	if stdout != expected {
		t.Fatalf("stdout = %q, want %q", stdout, expected)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestNoArgsUsesInstalledDeploymentDirByDefault(t *testing.T) {
	manifest := strings.Replace(
		cliTestPackManifest(),
		"      native_command: true\n      forward_flags: [--live]\n",
		"      native_command: true\n      deployed_command: true\n      forward_flags: [--live]\n",
		1,
	)
	packDir := makeCLITestPackWithManifest(t, manifest)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	markCLITestDeploymentInstalled(t, deployDir)
	t.Chdir(deployDir)

	code, stdout, stderr = runCLI()
	if code != 0 {
		t.Fatalf("no-args failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	expected := expectedBareDemoInstalledSummary(deployDir)
	if stdout != expected {
		t.Fatalf("stdout = %q, want %q", stdout, expected)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestStagingCommandsRejectInstalledDeploymentDir(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	markCLITestDeploymentInstalled(t, deployDir)

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "info", args: []string{"info", "--dir", deployDir}},
		{name: "overrides", args: []string{"overrides", "--dir", deployDir}},
		{name: "verify", args: []string{"verify", "--dir", deployDir}},
		{name: "app", args: []string{"app", "--dir", deployDir}},
		{name: "status", args: []string{"status", "--dir", deployDir}},
		{name: "test", args: []string{"test", "--dir", deployDir}},
		{name: "doctor", args: []string{"doctor", "--dir", deployDir}},
		{name: "install", args: []string{"install", "--dir", deployDir, "--to", filepath.Join(t.TempDir(), "target"), "--scope", "system", "--no-start"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(tc.args...)
			if code != 1 {
				t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			if stdout != "" {
				t.Fatalf("stdout = %q, want empty", stdout)
			}
			if !strings.Contains(stderr, "deployment is installed, not staged") {
				t.Fatalf("stderr did not explain installed deployment rejection:\n%s", stderr)
			}
		})
	}
}

func TestAppExecutionRejectsUnknownCommandBeforeImplicitBuild(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("app", "list", "--dir", deployDir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown environment command: list") {
		t.Fatalf("stderr did not reject unknown command:\n%s", stderr)
	}
}

func TestAppForwardedFlagValidationPrecedesImplicitBuild(t *testing.T) {
	packDir := makeCLITestPack(t)
	workDir := t.TempDir()
	t.Chdir(workDir)
	code, stdout, stderr := runCLI("stage", "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("app", "bootstrap", "server", "--foce")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "does not allow forwarded flag") {
		t.Fatalf("stderr did not reject forwarded flag:\n%s", stderr)
	}
}

func TestAWSTargetOptionIsReserved(t *testing.T) {
	code, stdout, stderr := runCLI("--aws", "up")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "deployment target aws is not supported yet") {
		t.Fatalf("stderr missing unsupported target message:\n%s", stderr)
	}
}

func TestRemovedInitCommandIsUnknown(t *testing.T) {
	code, stdout, stderr := runCLI("init", "demo-suite")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown command: init") {
		t.Fatalf("stderr did not reject removed init command:\n%s", stderr)
	}
}

func TestDockerStageHelp(t *testing.T) {
	code, stdout, stderr := runCLI("stage", "--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, want := range []string{
		"Usage: reploy [--docker-timeout DURATION] stage APP_REF [OPTIONS]",
		"reploy [--docker-timeout DURATION] stage --update [APP_REF] [OPTIONS]",
		"Create a staging directory from an app blueprint reference.",
		"Use --update to refresh an existing staging directory",
		"Stage records desired state and generates the app-named control command without building.",
		"Build explicitly or let staged up/restart build on demand.",
		"Indexed shorthand from the Reploy blueprint index:",
		"arbiter-server==0.4.2",
		"Local filesystem refs:",
		"./PATH",
		"/ABS/PATH",
		"file:PATH",
		"Python provider refs:",
		"pypi://PACKAGE/PATH/APP.blueprint.yaml",
		"pypi://PACKAGE/PATH/APP.blueprint.yaml?version=VERSION",
		"Git provider refs:",
		"github://ORG/REPO/PATH/APP.blueprint.yaml?ref=REF",
		"github://ORG/REPO/PATH/APP.blueprint.yaml?ref=REF&transport=ssh",
		"Local paths without file: must start with . or /.",
		"PyPI paths must point to the blueprint file inside the package.",
		"GitHub paths must point to the blueprint file inside the repository.",
		"--dir DIR",
		"Staging directory to create, update, or remove",
		"default current staging directory or reploy-staging",
		"--update",
		"--remove",
		"--platform OCI",
		"linux/amd64",
		"--force",
		"Replace a staging directory that belongs to another blueprint",
		"--verbose",
		"Show additional staging details",
		"Show stage help",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "init") {
		t.Fatalf("stage help contained old init wording:\n%s", stdout)
	}
	if strings.Contains(stdout, "--requirement") || strings.Contains(stdout, "Python provider options") {
		t.Fatalf("stage help contained removed requirement options:\n%s", stdout)
	}
	for _, hidden := range []string{"git:https://HOST/REPO.git", "git:https://github.com"} {
		if strings.Contains(stdout, hidden) {
			t.Fatalf("stage help exposed hidden git ref %q:\n%s", hidden, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestPackageOverridesHelp(t *testing.T) {
	code, stdout, stderr := runCLI("overrides", "--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, want := range []string{
		"Usage: reploy overrides [OPTIONS]",
		"Existing overrides.yaml content is loaded automatically.",
		"enter an exact image name",
		"workspace root is optional and unset by default",
		"--dir DIR",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestPackageOverridesOpensEditorForStagedDeployment(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	original := runOverrideEditor
	t.Cleanup(func() { runOverrideEditor = original })
	var received overrideui.Config
	runOverrideEditor = func(config overrideui.Config) (overrideui.Result, error) {
		received = config
		return overrideui.Result{}, nil
	}

	code, stdout, stderr = runCLI("overrides", "--dir", deployDir)
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("overrides = %d/%q/%q", code, stdout, stderr)
	}
	if received.DeploymentDir != deployDir {
		t.Fatalf("deployment dir = %q", received.DeploymentDir)
	}
	if received.Document.Environment.ID != "demo" {
		t.Fatalf("environment = %q", received.Document.Environment.ID)
	}
	if !reflect.DeepEqual(received.Overlay, deploy.EmptyRequestOverlayV1()) {
		t.Fatalf("editor overlay = %#v", received.Overlay)
	}
	if received.Input == nil || received.Output == nil {
		t.Fatalf("editor terminal streams were not configured: %#v", received)
	}

	received = overrideui.Config{}
	t.Chdir(deployDir)
	code, stdout, stderr = runCLI("overrides")
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("implicit overrides = %d/%q/%q", code, stdout, stderr)
	}
	if received.DeploymentDir != "." {
		t.Fatalf("implicit deployment dir = %q, want current staging directory", received.DeploymentDir)
	}
}

func TestPackageOverridesValidationRequiresAuthoritativeTrialResult(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	originalEditor := runOverrideEditor
	originalBuild := dockerProviderBuild
	originalRuntime := dockerProviderBuildRuntime
	originalInspect := inspectStagedOverrideValidation
	t.Cleanup(func() {
		runOverrideEditor = originalEditor
		dockerProviderBuild = originalBuild
		dockerProviderBuildRuntime = originalRuntime
		inspectStagedOverrideValidation = originalInspect
	})

	var received overrideui.Config
	runOverrideEditor = func(config overrideui.Config) (overrideui.Result, error) {
		received = config
		return overrideui.Result{}, nil
	}
	dockerProviderBuildRuntime = func() (dockerdeploy.StagedProviderBuildRuntimeV1, error) {
		return dockerdeploy.StagedProviderBuildRuntimeV1{
			Host: blueprint.HostLinux,
			UID:  1000,
			GID:  1000,
		}, nil
	}
	var buildInput dockerdeploy.ProviderBuildRunInputV1
	dockerProviderBuild = func(
		_ context.Context,
		input dockerdeploy.ProviderBuildRunInputV1,
	) (dockerdeploy.LockedProviderBuildExecutionResultV1, error) {
		buildInput = input
		return dockerdeploy.LockedProviderBuildExecutionResultV1{}, nil
	}
	inspectCalls := 0
	inspectStagedOverrideValidation = func(
		context.Context,
		string,
	) (dockerdeploy.StagedOverrideValidationV1, error) {
		inspectCalls++
		return dockerdeploy.StagedOverrideValidationV1{
			Validated: inspectCalls > 1,
			Packages: []dockerdeploy.OverrideDiscoveredPackageV1{{
				Provider: "python",
				Package:  "dependency",
			}},
		}, nil
	}

	code, stdout, stderr = runCLI("overrides", "--dir", deployDir)
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("overrides = %d/%q/%q", code, stdout, stderr)
	}
	if received.Validate == nil {
		t.Fatal("override editor validation action was not configured")
	}
	result, err := received.Validate(t.Context(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !buildInput.ValidateChoices || buildInput.DeploymentDir != deployDir {
		t.Fatalf("trial build input = %#v", buildInput)
	}
	if len(result.Packages) != 1 || result.Packages[0].Package != "dependency" {
		t.Fatalf("validation result = %#v", result)
	}

	inspectStagedOverrideValidation = func(
		context.Context,
		string,
	) (dockerdeploy.StagedOverrideValidationV1, error) {
		return dockerdeploy.StagedOverrideValidationV1{}, nil
	}
	if _, err := received.Validate(t.Context(), io.Discard); err == nil ||
		!strings.Contains(err.Error(), "without a matching validated result") {
		t.Fatalf("missing authoritative result error = %v", err)
	}
}

func TestDockerLogsHelp(t *testing.T) {
	code, stdout, stderr := runCLI("logs", "--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Usage: reploy logs [OPTIONS]") {
		t.Fatalf("stdout did not contain logs usage:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--follow, -f") || !strings.Contains(stdout, "Follow logs instead of exiting after current output") {
		t.Fatalf("stdout did not contain logs follow option:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--tail N") || !strings.Contains(stdout, "Show only the last N log lines") {
		t.Fatalf("stdout did not contain logs tail option:\n%s", stdout)
	}
	if strings.Contains(stdout, "Commands:") || strings.Contains(stdout, "bundle       Manage staging bundle contents") {
		t.Fatalf("stdout showed global help instead of logs help:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerUpdateCommandRemoved(t *testing.T) {
	code, stdout, stderr := runCLI("update")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown command: update") {
		t.Fatalf("stderr did not reject removed update command:\n%s", stderr)
	}
}

func TestDockerDownAndStopMapToInternalDownWithTypedCommandMessages(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	dir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, dir, "demo", "demo-service")
	oldRuntime := dockerRuntime
	t.Cleanup(func() { dockerRuntime = oldRuntime })
	var actions []string
	dockerRuntime = func(options dockerdeploy.RuntimeOptions) error {
		if options.Dir != dir || options.Action != "down" || options.ControlMode != "" {
			t.Fatalf("down runtime options = %#v", options)
		}
		actions = append(actions, options.Action)
		return errors.New("runtime failed")
	}
	for _, command := range []string{"down", "stop"} {
		var stderr bytes.Buffer
		code := runDockerRuntimeControl(command, []string{"--dir", dir}, io.Discard, &stderr, globalDeploymentOptions{})
		if code != 1 || !strings.Contains(stderr.String(), "reploy "+command+" error: runtime failed") {
			t.Fatalf("%s: code=%d stderr=%q", command, code, stderr.String())
		}
	}
	if len(actions) != 2 {
		t.Fatalf("runtime actions = %#v", actions)
	}
}

func TestDockerUpAndStartMapToInternalUpWithTypedCommandMessages(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	dir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, dir, "demo", "demo-service")
	oldRuntime := dockerRuntime
	t.Cleanup(func() { dockerRuntime = oldRuntime })
	var actions []string
	dockerRuntime = func(options dockerdeploy.RuntimeOptions) error {
		if options.Dir != dir || options.Action != "up" || options.ControlMode != "" {
			t.Fatalf("up runtime options = %#v", options)
		}
		actions = append(actions, options.Action)
		return errors.New("runtime failed")
	}
	for _, command := range []string{"up", "start"} {
		var stderr bytes.Buffer
		code := runDockerRuntimeControl(command, []string{"--dir", dir}, io.Discard, &stderr, globalDeploymentOptions{})
		if code != 1 || !strings.Contains(stderr.String(), "reploy "+command+" error: runtime failed") {
			t.Fatalf("%s: code=%d stderr=%q", command, code, stderr.String())
		}
	}
	if len(actions) != 2 {
		t.Fatalf("runtime actions = %#v", actions)
	}
}

func TestDockerStopAndRestartRejectOldDisruptionFlags(t *testing.T) {
	for _, command := range []string{"down", "stop", "restart"} {
		for _, flag := range []string{"--drain", "--force"} {
			code, stdout, stderr := runCLI(command, flag)
			if code != 2 || stdout != "" || !strings.Contains(stderr, flag+" is not supported") || !strings.Contains(stderr, "use --wait") {
				t.Fatalf("%s %s: code=%d stdout=%q stderr=%q", command, flag, code, stdout, stderr)
			}
		}
	}
}

func TestDockerControlAdmissionOptionsRejectedForObservation(t *testing.T) {
	code, stdout, stderr := runCLI("status", "--wait")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "--wait is only supported with up, down, or restart (start and stop are aliases)") {
		t.Fatalf("status wait: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestDockerObservationParserRejectsControlAdmissionOptions(t *testing.T) {
	if _, err := parseDockerObservationOptions([]string{"--wait"}); err == nil || !strings.Contains(err.Error(), "unknown option") {
		t.Fatalf("observation parser wait error = %v", err)
	}
}

func TestDockerLogsOptionsParse(t *testing.T) {
	options, err := parseDockerRuntimeOptions([]string{"--dir", "deployment", "--tail", "100", "--follow", "--timestamps", "--verbose"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Dir != "deployment" {
		t.Fatalf("dir = %q", options.Dir)
	}
	if !options.Follow {
		t.Fatal("follow = false, want true")
	}
	if options.Tail != "100" {
		t.Fatalf("tail = %q, want 100", options.Tail)
	}
	if !options.Verbose {
		t.Fatal("verbose = false, want true")
	}
	if !options.Timestamps {
		t.Fatal("timestamps = false, want true")
	}

	options, err = parseDockerRuntimeOptions([]string{"--tail=25"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Tail != "25" {
		t.Fatalf("tail = %q, want 25", options.Tail)
	}
}

func TestRuntimeLifecycleActionsShowSpinnerWhenNotVerbose(t *testing.T) {
	for _, action := range []string{"up", "start", "restart", "down", "stop"} {
		if !runtimeActionShowsSpinner(action, false) {
			t.Fatalf("%s should show a spinner when not verbose", action)
		}
		if runtimeActionShowsSpinner(action, true) {
			t.Fatalf("%s should not show a spinner when verbose", action)
		}
	}
	for _, action := range []string{"ps", "status", "logs"} {
		if runtimeActionShowsSpinner(action, false) {
			t.Fatalf("%s should stream output instead of showing a spinner", action)
		}
	}
}

func TestRuntimeSpinnerLabelUsesDeploymentPrefix(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	for _, action := range []string{"up", "start", "down", "stop"} {
		label, err := runtimeSpinnerLabel(deployDir, action, &bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		if want := "[STAGING : demo] " + action; label != want {
			t.Fatalf("%s label = %q, want %q", action, label, want)
		}
	}
}

func TestDeploymentSpinnerLabelUsesDeploymentPrefix(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	label, err := deploymentSpinnerLabel(deployDir, "validating installation bundle", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if label != "[STAGING : demo] validating installation bundle" {
		t.Fatalf("label = %q", label)
	}
}

func TestDeploymentSpinnerLabelUsesInstalledPrefix(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	installDir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, installDir, "demo", "demo-service")

	label, err := deploymentSpinnerLabel(installDir, "uninstalling", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if label != "[DEPLOYED : demo] uninstalling" {
		t.Fatalf("label = %q", label)
	}
}

func TestDeploymentStdoutOrFallbackPrefixesInstalledOutput(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	installDir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, installDir, "demo", "demo-service")

	var stdout bytes.Buffer
	writer := deploymentStdoutOrFallback(installDir, &stdout)
	fmt.Fprintln(writer, "server url: https://127.0.0.1:8075")
	if got, want := stdout.String(), "[DEPLOYED : demo] server url: https://127.0.0.1:8075\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestPhaseKnownTestErrorUsesDeploymentPrefix(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	oldTestServer := dockerTestServer
	t.Cleanup(func() {
		dockerTestServer = oldTestServer
	})
	dockerTestServer = func(options dockerdeploy.TestOptions) error {
		if options.Dir != deployDir {
			t.Fatalf("dir = %q, want %q", options.Dir, deployDir)
		}
		return errors.New("service is not started; run reploy up before testing health")
	}

	code, stdout, stderr = runCLI("test", "--dir", deployDir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "[STAGING : demo] reploy test error: service is not started; run reploy up before testing health") {
		t.Fatalf("stderr missing staging-prefixed test error:\n%s", stderr)
	}
}

func TestPhaseKnownRuntimeErrorUsesDeploymentPrefix(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	oldRuntime := dockerRuntime
	t.Cleanup(func() {
		dockerRuntime = oldRuntime
	})
	dockerRuntime = func(options dockerdeploy.RuntimeOptions) error {
		if options.Action != "status" {
			t.Fatalf("action = %q, want status", options.Action)
		}
		return errors.New("runtime exploded")
	}

	code, stdout, stderr = runCLI("status", "--dir", deployDir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "[STAGING : demo] reploy status error: runtime exploded") {
		t.Fatalf("stderr missing staging-prefixed runtime error:\n%s", stderr)
	}
}

func TestPhaseKnownRuntimePrepareHintUsesDeploymentPrefix(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	oldRuntime := dockerRuntime
	t.Cleanup(func() {
		dockerRuntime = oldRuntime
	})
	dockerRuntime = func(options dockerdeploy.RuntimeOptions) error {
		if options.Action != "up" {
			t.Fatalf("action = %q, want up", options.Action)
		}
		return errors.New("prepare installation bundle: docker failed: exit status 1")
	}

	code, stdout, stderr = runCLI("up", "--dir", deployDir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	for _, want := range []string{
		"[STAGING : demo] up",
		"[STAGING : demo] reploy up error: prepare installation bundle: docker failed: exit status 1",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "next step:") {
		t.Fatalf("runtime error retained the removed automatic-build hint:\n%s", stderr)
	}
}

func TestRuntimeUpPrintsServiceURLAfterSuccessfulStart(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	oldRuntime := dockerRuntime
	t.Cleanup(func() {
		dockerRuntime = oldRuntime
	})
	dockerRuntime = func(options dockerdeploy.RuntimeOptions) error {
		if options.Action != "up" {
			t.Fatalf("action = %q, want up", options.Action)
		}
		if options.Dir != deployDir {
			t.Fatalf("dir = %q, want %q", options.Dir, deployDir)
		}
		return nil
	}

	code, stdout, stderr = runCLI("up", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("up failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want no success lines without a recorded build", stdout)
	}
	if !strings.Contains(stderr, "[STAGING : demo] up... done [") {
		t.Fatalf("stderr missing successful spinner:\n%s", stderr)
	}
}

func TestDockerTimeoutAppliesDuringDockerCommand(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	oldRuntime := dockerRuntime
	t.Cleanup(func() {
		dockerRuntime = oldRuntime
	})
	dockerRuntime = func(options dockerdeploy.RuntimeOptions) error {
		if options.Action != "status" {
			t.Fatalf("action = %q, want status", options.Action)
		}
		if got := options.DockerPreflightTimeout; got != 11*time.Second {
			t.Fatalf("DockerPreflightTimeout = %s, want 11s", got)
		}
		return nil
	}

	code, stdout, stderr = runCLI("--docker-timeout", "11s", "status", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("status failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
}

func TestDockerRuntimeRejectsFollowOutsideLogs(t *testing.T) {
	code, stdout, stderr := runCLI("ps", "--follow")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "--follow is only supported with logs") {
		t.Fatalf("stderr missing follow validation message:\n%s", stderr)
	}
}

func TestDockerRuntimeRejectsTailOutsideLogs(t *testing.T) {
	code, stdout, stderr := runCLI("ps", "--tail", "100")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "--tail is only supported with logs") {
		t.Fatalf("stderr missing tail validation message:\n%s", stderr)
	}
}

func TestDockerTestTimeoutOptionParses(t *testing.T) {
	options, err := parseDockerTestOptions([]string{"--dir", "deployment", "--timeout", "2s"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Dir != "deployment" {
		t.Fatalf("dir = %q", options.Dir)
	}
	if options.Timeout != 2*time.Second {
		t.Fatalf("timeout = %s", options.Timeout)
	}
}

func TestDockerTestRejectsWaitOption(t *testing.T) {
	code, stdout, stderr := runCLI("test", "--wait")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown option: --wait") {
		t.Fatalf("stderr missing wait validation message:\n%s", stderr)
	}
}

func TestDockerDoctorScopeOptionsParse(t *testing.T) {
	options, err := parseDockerDoctorOptions([]string{
		"--preinstall",
		"--scope",
		"user",
		"--dir",
		"deployment",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !options.Preinstall || options.Scope != dockerdeploy.InstallScopeUser || options.Dir != "deployment" {
		t.Fatalf("doctor options = %#v", options)
	}

	options, err = parseDockerDoctorOptions([]string{"--preinstall", "--scope=system"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Scope != dockerdeploy.InstallScopeSystem {
		t.Fatalf("scope = %q, want system", options.Scope)
	}
}

func TestDockerDoctorScopeRequiresPreinstall(t *testing.T) {
	if _, err := parseDockerDoctorOptions([]string{"--scope", "user"}); err == nil || !strings.Contains(err.Error(), "--scope requires --preinstall") {
		t.Fatalf("error = %v, want scope/preinstall validation", err)
	}
	if _, err := parseDockerDoctorOptions([]string{"--preinstall"}); err == nil || !strings.Contains(err.Error(), "--preinstall requires --scope") {
		t.Fatalf("error = %v, want preinstall/scope validation", err)
	}
}

func TestDockerInstallPortOptionsParse(t *testing.T) {
	options, err := parseDockerInstallOptions([]string{
		"--dir", "deployment",
		"--to", "/opt/demo2",
		"--scope", "system",
		"--service", "demo2",
		"--port", "http=18082",
		"--port=metrics=19092",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Target != "/opt/demo2" || options.Service != "demo2" {
		t.Fatalf("target/service = %q/%q", options.Target, options.Service)
	}
	if options.Scope != dockerdeploy.InstallScopeSystem {
		t.Fatalf("scope = %q, want system", options.Scope)
	}
	if len(options.PortOverrides) != 2 {
		t.Fatalf("port overrides = %#v", options.PortOverrides)
	}
	if options.PortOverrides[0].Name != "http" || options.PortOverrides[0].HostPort != "18082" {
		t.Fatalf("first override = %#v", options.PortOverrides[0])
	}
	if options.PortOverrides[1].Name != "metrics" || options.PortOverrides[1].HostPort != "19092" {
		t.Fatalf("second override = %#v", options.PortOverrides[1])
	}

	options, err = parseDockerInstallOptions([]string{"--to", "/opt/demo2", "--scope", "system", "--port", "18082"})
	if err != nil {
		t.Fatal(err)
	}
	if len(options.PortOverrides) != 1 || options.PortOverrides[0].Name != "" || options.PortOverrides[0].HostPort != "18082" {
		t.Fatalf("shorthand override = %#v", options.PortOverrides)
	}

	options, err = parseDockerInstallOptions([]string{"pypi:demo-server#demo_server/reploy/demo-server.blueprint.yaml", "--scope=user", "--replace", "conf", "--replace=.env", "--clean"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Pack.Raw != "pypi:demo-server#demo_server/reploy/demo-server.blueprint.yaml" || !options.Clean {
		t.Fatalf("direct install options = %#v", options)
	}
	if options.Scope != dockerdeploy.InstallScopeUser {
		t.Fatalf("scope = %q, want user", options.Scope)
	}
	if strings.Join(options.Replace, ",") != "conf,.env" {
		t.Fatalf("replace = %#v", options.Replace)
	}
	if _, err := parseDockerInstallOptions([]string{"--scope=user", "--in-place"}); err == nil || !strings.Contains(err.Error(), "unknown option: --in-place") {
		t.Fatalf("removed --in-place error = %v", err)
	}
}

func TestDockerInstallScopeIsRequired(t *testing.T) {
	_, err := parseDockerInstallOptions([]string{"--to", "/opt/demo"})
	if err == nil {
		t.Fatal("expected missing scope error")
	}
	if !strings.Contains(err.Error(), "--scope is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDockerInstallRejectsInvalidScope(t *testing.T) {
	_, err := parseDockerInstallOptions([]string{"--to", "/opt/demo", "--scope", "default"})
	if err == nil {
		t.Fatal("expected invalid scope error")
	}
	if !strings.Contains(err.Error(), "--scope must be user or system") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParsePackRefArgumentSupportsPyPIHashBlueprintPath(t *testing.T) {
	ref, err := parsePackRefArgument("pypi:demo-pkg#demo_pkg/reploy/app.blueprint.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Scheme != "pypi" || ref.Source != "demo-pkg" || ref.Subdir != "demo_pkg/reploy/app.blueprint.yaml" {
		t.Fatalf("ref = %#v", ref)
	}
}

func TestParsePackRefArgumentSupportsBareLocalPaths(t *testing.T) {
	absolutePath := filepath.Join(t.TempDir(), "demo.blueprint.yaml")
	for _, value := range []string{"./demo.blueprint.yaml", "../demo", absolutePath} {
		ref, err := parsePackRefArgument(value)
		if err != nil {
			t.Fatalf("parse %q: %v", value, err)
		}
		if ref.Scheme != "file" || ref.Source != value || ref.Raw != value {
			t.Fatalf("ref for %q = %#v", value, ref)
		}
	}
}

func TestParsePackRefArgumentDoesNotGuessPlainPath(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "reploy-blueprint-index.json")
	if err := os.WriteFile(indexPath, []byte(`{"schema_version":1,"blueprints":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(packIndexURLEnv, "file:"+indexPath)

	_, err := parsePackRefArgument("demo/path")
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{
		`"demo/path" looks like a local blueprint path`,
		"./demo/path",
		"file://demo/path",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err missing %q: %v", want, err)
		}
	}
}

func TestStageLikelyLocalPathUsesFocusedDiagnosticEvenWhenIndexMatches(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "reploy-blueprint-index.json")
	index := `{"schema_version":1,"blueprints":{"examples/demo.blueprint.yaml":{"ref":"pypi://demo/demo/reploy/demo.blueprint.yaml"}}}`
	if err := os.WriteFile(indexPath, []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(packIndexURLEnv, "file:"+indexPath)
	code, stdout, stderr := runCLI("stage", "examples/demo.blueprint.yaml")
	if code != 2 || stdout != "" {
		t.Fatalf("stage local-path mistake = %d/%q/%q", code, stdout, stderr)
	}
	for _, want := range []string{"looks like a local blueprint path", "./examples/demo.blueprint.yaml", "file://examples/demo.blueprint.yaml"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("focused diagnostic missing %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "Python provider refs") || strings.Contains(stderr, "Git provider refs") {
		t.Fatalf("focused diagnostic included full reference help:\n%s", stderr)
	}
}

func TestParsePackRefArgumentAcceptsFileDoubleSlashRelativeForm(t *testing.T) {
	ref, err := parsePackRefArgument("file://examples/demo.blueprint.yaml")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if ref.Scheme != "file" || ref.Source != "examples/demo.blueprint.yaml" || ref.Raw != "file://examples/demo.blueprint.yaml" {
		t.Fatalf("file double-slash ref = %#v", ref)
	}
}

func TestParseDockerCommandOptionsWarnsWhenShorthandMatchesLocalPath(t *testing.T) {
	workDir := t.TempDir()
	t.Chdir(workDir)
	if err := os.Mkdir("demo-server", 0o755); err != nil {
		t.Fatal(err)
	}
	setCLITestPackIndex(t)

	options, err := parseDockerCommandOptions([]string{"demo-server"}, true, dockerCommandParseConfig{AllowUpdate: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(options.Warnings) != 1 {
		t.Fatalf("warnings = %#v", options.Warnings)
	}
	for _, want := range []string{`APP_REF "demo-server" also exists as a local path`, "treating it as a blueprint shorthand", "Use ./demo-server or file:demo-server"} {
		if !strings.Contains(options.Warnings[0], want) {
			t.Fatalf("warning missing %q:\n%s", want, options.Warnings[0])
		}
	}
	if options.Pack.Raw != "demo-server" || options.Pack.Scheme != "pypi" {
		t.Fatalf("pack = %#v", options.Pack)
	}
}

func TestParseDockerCommandOptionsParsesNonemptyPlatform(t *testing.T) {
	setCLITestPackIndex(t)
	options, err := parseDockerCommandOptions(
		[]string{"--platform=linux/amd64", "demo-server"},
		true,
		dockerCommandParseConfig{AllowPlatform: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if options.Platform != "linux/amd64" {
		t.Fatalf("platform = %q", options.Platform)
	}
	for _, args := range [][]string{
		{"--platform=", "demo-server"},
		{"--platform", "", "demo-server"},
	} {
		if _, err := parseDockerCommandOptions(args, true, dockerCommandParseConfig{AllowPlatform: true}); err == nil || !strings.Contains(err.Error(), "--platform must not be empty") {
			t.Fatalf("parse %q error = %v", args, err)
		}
	}
}

func TestParseDockerCommandOptionsRejectsRemovedWorkspaceRoot(t *testing.T) {
	setCLITestPackIndex(t)
	_, err := parseDockerCommandOptions(
		[]string{"--workspace-root=../checkout", "demo-server"},
		true,
		dockerCommandParseConfig{},
	)
	if err == nil || !strings.Contains(err.Error(), "unknown option: --workspace-root") {
		t.Fatalf("removed workspace-root error = %v", err)
	}
}

func TestParsePackRefArgumentSupportsGitHTTPSRef(t *testing.T) {
	ref, err := parsePackRefArgument("git:https://github.com/acme/demo.git#demo_pkg/reploy?ref=main")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Scheme != "git" || ref.Source != "https://github.com/acme/demo.git" || ref.Subdir != "demo_pkg/reploy" {
		t.Fatalf("ref = %#v", ref)
	}
	if ref.Query.Get("ref") != "main" {
		t.Fatalf("query = %#v", ref.Query)
	}
}

func TestParsePackRefArgumentSupportsGitHubRef(t *testing.T) {
	raw := "github://acme/demo/demo_pkg/reploy/demo.blueprint.yaml?ref=main"
	ref, err := parsePackRefArgument(raw)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Raw != raw || ref.Scheme != "git" || ref.Source != "https://github.com/acme/demo.git" || ref.Subdir != "demo_pkg/reploy/demo.blueprint.yaml" {
		t.Fatalf("ref = %#v", ref)
	}
	if ref.Query.Get("ref") != "main" {
		t.Fatalf("query = %#v", ref.Query)
	}
}

func TestDockerStageGitHubRefErrorDoesNotExposeInternalGitRef(t *testing.T) {
	code, stdout, stderr := runCLI("stage", "github://acme/demo/demo_pkg/reploy?ref=main")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "github blueprint path must point to a *.blueprint.yaml file") {
		t.Fatalf("stderr did not contain github-facing error:\n%s", stderr)
	}
	for _, leaked := range []string{
		"git:",
		"https://github.com/acme/demo.git",
		"ssh://git@github.com/acme/demo.git",
		"git blueprint",
	} {
		if strings.Contains(stderr, leaked) {
			t.Fatalf("stderr exposed internal git representation %q:\n%s", leaked, stderr)
		}
	}
}

func TestInstallRejectsRemovedDryRunOption(t *testing.T) {
	packDir := makeCLITestPack(t)
	code, stdout, stderr := runCLI("install", "file:"+packDir, "--scope", "system", "--dry-run", "--no-start")
	if code != 2 || !strings.Contains(stderr, "unknown option: --dry-run") {
		t.Fatalf("exit code = %d, want usage error\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
}

func TestDirectInstallRejectsRemovedInPlaceOption(t *testing.T) {
	requireLinuxHost(t)
	packDir := makeCLITestPack(t)
	target := filepath.Join(t.TempDir(), "installed")
	code, stdout, stderr := runCLI("install", "file:"+packDir, "--to", target, "--scope", "system", "--in-place", "--no-start")
	if code != 2 || !strings.Contains(stderr, "unknown option: --in-place") {
		t.Fatalf("exit code = %d, want usage error\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("rejected option created target, stat err=%v", err)
	}
}

func TestDirectInstallPrintsSuccessFromResolvedDefaultTarget(t *testing.T) {
	installTarget := filepath.Join(t.TempDir(), "installed")
	oldDirectInstall := dockerDirectInstall
	oldInstallSuccessLines := dockerInstallSuccessLines
	t.Cleanup(func() {
		dockerDirectInstall = oldDirectInstall
		dockerInstallSuccessLines = oldInstallSuccessLines
	})

	dockerDirectInstall = func(options dockerdeploy.DirectInstallOptions) (dockerdeploy.ProviderInstallResultV1, error) {
		if options.Target != "" {
			t.Fatalf("target option = %q, want empty default target", options.Target)
		}
		if options.Scope != dockerdeploy.InstallScopeSystem {
			t.Fatalf("scope = %q, want system", options.Scope)
		}
		if options.ControlMode != dockerdeploy.ControlAdmissionForceV1 {
			t.Fatalf("control mode = %q, want force", options.ControlMode)
		}
		fmt.Fprintln(options.Stdout, "raw lifecycle success")
		return dockerdeploy.ProviderInstallResultV1{
			Environment: "demo", TargetDir: installTarget, ControlScript: "demo",
			Service: "demo", Started: true,
		}, nil
	}
	dockerInstallSuccessLines = func(dir string, dockerPreflightTimeout time.Duration) ([]string, error) {
		if dir != installTarget {
			t.Fatalf("success dir = %q, want resolved default target", dir)
		}
		if dockerPreflightTimeout != time.Second {
			t.Fatalf("success docker timeout = %s, want 1s", dockerPreflightTimeout)
		}
		return []string{"inspector url: http://127.0.0.1:19076"}, nil
	}

	code, stdout, stderr := runCLI("--docker-timeout", "1s", "install", "file:/does/not/need/to/exist", "--scope", "system", "--force")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"[DEPLOYED : demo] installed successfully",
		"[DEPLOYED : demo] location: " + installTarget,
		"[DEPLOYED : demo] control: " + filepath.Join(installTarget, "demo"),
		"[DEPLOYED : demo] status: running",
		"[DEPLOYED : demo] inspector url: http://127.0.0.1:19076",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "raw lifecycle success") || strings.Contains(stderr, "raw lifecycle success") {
		t.Fatalf("successful lifecycle output leaked:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if strings.Count(stdout, "inspector url:") != 1 {
		t.Fatalf("stdout missing success output:\n%s", stdout)
	}
}

func TestStagedInstallUsesReployProgressAndDeployedResult(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	t.Setenv("TERM", "xterm-256color")
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	oldDockerInstall := dockerInstall
	t.Cleanup(func() {
		dockerInstall = oldDockerInstall
	})
	dockerInstall = func(options dockerdeploy.InstallOptions) (dockerdeploy.ProviderInstallResultV1, error) {
		if options.Dir != deployDir {
			t.Fatalf("install dir = %q, want %q", options.Dir, deployDir)
		}
		if options.Scope != dockerdeploy.InstallScopeSystem {
			t.Fatalf("scope = %q, want system", options.Scope)
		}
		if options.ControlMode != dockerdeploy.ControlAdmissionWaitV1 {
			t.Fatalf("control mode = %q, want wait", options.ControlMode)
		}
		fmt.Fprintln(options.Progress, "running before start hook: app config check")
		return dockerdeploy.ProviderInstallResultV1{
			Environment: "demo", TargetDir: "/opt/demo", ControlScript: "demo",
			Service: "demo", Started: true,
		}, nil
	}
	oldInstallSuccessLines := dockerInstallSuccessLines
	t.Cleanup(func() { dockerInstallSuccessLines = oldInstallSuccessLines })
	dockerInstallSuccessLines = func(string, time.Duration) ([]string, error) { return nil, nil }

	code, stdout, stderr = runCLI("install", "--dir", deployDir, "--scope", "system", "--wait")
	if code != 0 {
		t.Fatalf("install failed: code=%d\nstderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "installing deployment: running before start hook: app config check") {
		t.Fatalf("stderr missing Reploy install progress:\n%s", stderr)
	}
	if !strings.Contains(stdout, "[DEPLOYED : demo] installed successfully") {
		t.Fatalf("stdout missing deployed result:\n%s", stdout)
	}
}

func TestDockerInstallControlOptionsParse(t *testing.T) {
	for flag, want := range map[string]dockerdeploy.ControlAdmissionModeV1{
		"--wait":  dockerdeploy.ControlAdmissionWaitV1,
		"--drain": dockerdeploy.ControlAdmissionDrainV1,
		"--force": dockerdeploy.ControlAdmissionForceV1,
	} {
		options, err := parseDockerInstallOptions([]string{"--scope", "user", flag})
		if err != nil {
			t.Fatalf("parse %s: %v", flag, err)
		}
		if options.ControlMode != want {
			t.Fatalf("%s control mode = %q, want %q", flag, options.ControlMode, want)
		}
	}
	if _, err := parseDockerInstallOptions([]string{"--scope", "user", "--wait", "--force"}); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("conflicting control modes error = %v", err)
	}
	options, err := parseDockerInstallOptions([]string{"--scope", "user", "--verbose"})
	if err != nil || !options.Verbose {
		t.Fatalf("verbose install options = %#v, error=%v", options, err)
	}
}

func TestDockerUninstallOptionsParse(t *testing.T) {
	options, err := parseDockerUninstallOptions([]string{
		"--from", "/opt/demo2",
		"--service-name", "demo2",
		"--remove-dir",
		"--drain",
		"--verbose",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.From != "/opt/demo2" || options.ServiceName != "demo2" {
		t.Fatalf("from/service-name = %q/%q", options.From, options.ServiceName)
	}
	if !options.Verbose {
		t.Fatal("verbose = false, want true")
	}
	if !options.RemoveDir || options.ControlMode != dockerdeploy.ControlAdmissionDrainV1 {
		t.Fatalf("remove-dir/control-mode = %v/%q", options.RemoveDir, options.ControlMode)
	}

	options, err = parseDockerUninstallOptions([]string{"--from=/opt/demo3", "--service-name=demo3"})
	if err != nil {
		t.Fatal(err)
	}
	if options.From != "/opt/demo3" || options.ServiceName != "demo3" {
		t.Fatalf("from/service-name = %q/%q", options.From, options.ServiceName)
	}

	_, err = parseDockerUninstallOptions([]string{"--list-services"})
	if err == nil || !strings.Contains(err.Error(), "unknown option: --list-services") {
		t.Fatalf("expected unknown list-services option, got %v", err)
	}
	if _, err := parseDockerUninstallOptions([]string{"--dry-run"}); err == nil || !strings.Contains(err.Error(), "unknown option") {
		t.Fatalf("removed dry-run error = %v", err)
	}
	if _, err := parseDockerUninstallOptions([]string{"--wait", "--force"}); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("conflicting control modes error = %v", err)
	}
}

func TestServicesListRunsSystemdServiceInventory(t *testing.T) {
	oldPrint := printReploySystemdServices
	t.Cleanup(func() {
		printReploySystemdServices = oldPrint
	})
	printReploySystemdServices = func(stdout io.Writer) error {
		fmt.Fprintln(stdout, "SERVICE\tTARGET")
		fmt.Fprintln(stdout, "demo\t/opt/demo")
		return nil
	}

	code, stdout, stderr := runCLI("services", "list")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "demo\t/opt/demo") {
		t.Fatalf("stdout missing services list:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerUninstallRequiresRootBeforeSpinner(t *testing.T) {
	requireLinuxHost(t)
	if os.Geteuid() == 0 {
		t.Skip("root test environment cannot exercise non-root CLI path")
	}
	code, stdout, stderr := runCLI("uninstall", "--service-name", "demo")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	for _, want := range []string{
		"root privileges are required",
		"rerun with sudo",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "uninstalling deployment") {
		t.Fatalf("stderr should not contain spinner output:\n%s", stderr)
	}
}

func TestDockerUninstallExplicitAbsentTargetIsSuccessfulAfterProgress(t *testing.T) {
	t.Setenv("CI", "1")
	target := filepath.Join(t.TempDir(), "removed-installation")
	var output bytes.Buffer

	code := Main([]string{"uninstall", "--from", target, "--remove-dir"}, &output, &output)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\noutput:\n%s", code, output.String())
	}
	wantProgress := "uninstalling deployment: done"
	wantResult := "No installation found at " + target + "; it may already have been removed."
	if !strings.Contains(output.String(), wantProgress) || !strings.Contains(output.String(), wantResult) {
		t.Fatalf("output missing completed progress or no-installation result:\n%s", output.String())
	}
	if strings.Index(output.String(), wantResult) < strings.Index(output.String(), wantProgress) {
		t.Fatalf("no-installation result appeared before progress completion:\n%s", output.String())
	}
	if strings.Contains(output.String(), "root privileges are required") {
		t.Fatalf("already-absent user target requested root:\n%s", output.String())
	}
}

func TestDockerUninstallHidesSuccessfulBackendOutputAndReportsExactResult(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	t.Setenv("CI", "")
	t.Setenv("TERM", "xterm-256color")
	installDir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, installDir, "demo", "demo-service")

	oldDockerUninstall := dockerUninstall
	oldDockerUninstallNeedsRoot := dockerUninstallNeedsRoot
	t.Cleanup(func() {
		dockerUninstall = oldDockerUninstall
		dockerUninstallNeedsRoot = oldDockerUninstallNeedsRoot
	})
	dockerUninstallNeedsRoot = func(dockerdeploy.UninstallOptions) bool {
		return false
	}
	dockerUninstall = func(options dockerdeploy.UninstallOptions) (dockerdeploy.ProviderUninstallResultV1, error) {
		if options.From != installDir {
			t.Fatalf("from = %q, want %q", options.From, installDir)
		}
		if options.ControlMode != dockerdeploy.ControlAdmissionForceV1 {
			t.Fatalf("control mode = %q", options.ControlMode)
		}
		fmt.Fprintln(options.Stdout, "uninstalled service: demo")
		fmt.Fprintln(options.Progress, "removing runtime resources")
		return dockerdeploy.ProviderUninstallResultV1{
			DeploymentDir: installDir, Environment: "demo", Service: "demo-service",
			RemovedDirectory: true,
		}, nil
	}

	code, stdout, stderr := runCLI("uninstall", "--from", installDir, "--remove-dir", "--force")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "uninstalled service") || strings.Contains(stderr, "uninstalled service") {
		t.Fatalf("successful backend output leaked:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	for _, want := range []string{
		"[DEPLOYED : demo] uninstalled successfully",
		"[DEPLOYED : demo] removed: service demo-service",
		"[DEPLOYED : demo] removed: runtime resources",
		"[DEPLOYED : demo] removed: installation directory " + installDir,
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "uninstalling deployment: removing runtime resources") ||
		!strings.Contains(stderr, "uninstalling deployment: done") {
		t.Fatalf("stderr missing Reploy uninstall progress:\n%q", stderr)
	}
}

func TestDockerInitWritesDeployment(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "created staging directory for demo: "+deployDir) {
		t.Fatalf("stdout did not include staging summary:\n%s", stdout)
	}
	if strings.Contains(stdout, "updated ") {
		t.Fatalf("stdout should not include generated file updates without --verbose:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if _, err := os.Stat(filepath.Join(deployDir, dockerdeploy.StateFileName)); err != nil {
		t.Fatalf("missing state: %v", err)
	}
}

func TestDockerStageEnvironmentBlueprintCreatesAndRestagesCurrentDemo(t *testing.T) {
	blueprintPath := filepath.Join(cliTestRepoRoot(t), "examples", "omegaconf-inspector", "reploy", "omegaconf-inspector.blueprint.yaml")
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "--platform", "linux/amd64", "file:"+blueprintPath)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "created staging directory for omegaconf-inspector: "+deployDir+"\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	assertCLIStateV1Platform(t, deployDir, "linux/amd64")
	content, err := os.ReadFile(filepath.Join(deployDir, dockerdeploy.StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	state, err := deploy.DecodeStateV1(content)
	if err != nil {
		t.Fatal(err)
	}
	wantBlueprint, err := os.ReadFile(blueprintPath)
	if err != nil {
		t.Fatal(err)
	}
	if state.BlueprintSource != string(wantBlueprint) || state.Staging == nil {
		t.Fatalf("retained staging inputs = %#v", state)
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	commandNames := make([]string, 0, len(document.Environment.Commands))
	for name := range document.Environment.Commands {
		commandNames = append(commandNames, name)
	}
	slices.Sort(commandNames)
	wantCommandNames := []string{"config_check", "config_init", "config_show", "serve", "version"}
	if !reflect.DeepEqual(commandNames, wantCommandNames) {
		t.Fatalf("OmegaConf Inspector commands = %q, want %q", commandNames, wantCommandNames)
	}
	if document.Environment.Commands["config_init"].DeployedCommand ||
		!document.Environment.Commands["config_check"].DeployedCommand ||
		!document.Environment.Commands["config_show"].DeployedCommand ||
		!document.Environment.Commands["version"].DeployedCommand {
		t.Fatalf("OmegaConf Inspector command exposure = %#v", document.Environment.Commands)
	}

	entries, err := os.ReadDir(deployDir)
	if err != nil {
		t.Fatal(err)
	}
	wantEntryCount := 3
	if runtime.GOOS == "windows" {
		wantEntryCount = 4
	}
	if len(entries) != wantEntryCount ||
		entries[0].Name() != dockerdeploy.ReployInternalDir ||
		!entries[0].IsDir() ||
		entries[1].Name() != "omegaconf-inspector" ||
		(runtime.GOOS != "windows" && entries[2].Name() != deploy.PackageOverridesFilename) ||
		(runtime.GOOS == "windows" && (entries[2].Name() != "omegaconf-inspector.ps1" || entries[3].Name() != deploy.PackageOverridesFilename)) {
		t.Fatalf("staging entries = %#v", entries)
	}
	overrides, found, err := deploy.ReadPackageOverridesV1(deployDir)
	if err != nil || !found {
		t.Fatalf("read imported package overrides: found=%v err=%v", found, err)
	}
	wantLocalProject := filepath.Clean(filepath.Join(filepath.Dir(blueprintPath), ".."))
	wantWorkspace := filepath.Clean(filepath.Join(wantLocalProject, "..", "..", ".."))
	if got := overrides.Environment.Vars["workspace_root"]; got != wantWorkspace {
		t.Fatalf("imported workspace root = %#v, want %q", got, wantWorkspace)
	}
	if got := overrides.Environment.PackageOverrides["python"]["omegaconf-inspector"].Path; got != "{{ workspace_root }}/reploy/examples/omegaconf-inspector" {
		t.Fatalf("imported local project path = %q", got)
	}
	internalEntries, err := os.ReadDir(filepath.Join(deployDir, dockerdeploy.ReployInternalDir))
	if err != nil {
		t.Fatal(err)
	}
	internalNames := make([]string, len(internalEntries))
	for index, entry := range internalEntries {
		internalNames[index] = entry.Name()
	}
	if !reflect.DeepEqual(internalNames, []string{"bin", "operation.lock", "staged-control.json", "state.json"}) {
		t.Fatalf("internal entries = %q", internalNames)
	}

	code, stdout, stderr = runCLI("stage", "--update", "--dir", deployDir, "--platform", "linux/arm64")
	if code != 0 {
		t.Fatalf("stage --update failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "updated staging directory: "+deployDir+"\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	assertCLIStateV1Platform(t, deployDir, "linux/arm64")
}

func assertCLIStateV1Platform(t *testing.T, dir string, canonical string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dir, dockerdeploy.StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	state, err := deploy.DecodeStateV1(content)
	if err != nil {
		t.Fatal(err)
	}
	if state.Platform.Canonical != canonical || state.Current != nil {
		t.Fatalf("state platform/current = %q/%#v", state.Platform.Canonical, state.Current)
	}
}

func TestDockerInitVerboseReportsGeneratedFiles(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--verbose", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "created staging directory for demo: "+deployDir) {
		t.Fatalf("stdout did not include staging summary:\n%s", stdout)
	}
	if !strings.Contains(stdout, "selected platform: linux/amd64") {
		t.Fatalf("stdout did not include selected platform:\n%s", stdout)
	}
	if strings.Contains(stdout, dockerdeploy.ComposeFileName) {
		t.Fatalf("stage unexpectedly generated runtime files:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerInitUsesDefaultDeploymentDir(t *testing.T) {
	packDir := makeCLITestPack(t)
	workDir := t.TempDir()
	t.Chdir(workDir)

	code, stdout, stderr := runCLI("stage", "file:"+packDir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	deployDir := filepath.Join(workDir, "reploy-staging")
	if _, err := os.Stat(filepath.Join(deployDir, dockerdeploy.StateFileName)); err != nil {
		t.Fatalf("missing state in default deployment dir: %v", err)
	}
	if !strings.Contains(stdout, "created staging directory for demo: reploy-staging") {
		t.Fatalf("stdout did not include default staging summary:\n%s", stdout)
	}
	if strings.Contains(stdout, "updated ") {
		t.Fatalf("stdout should not include generated file updates without --verbose:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerInitAcceptsBareDotRelativePath(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	t.Chdir(packDir)

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, ".")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(deployDir, dockerdeploy.StateFileName)); err != nil {
		t.Fatalf("missing state: %v", err)
	}
	if !strings.Contains(stdout, "created staging directory for demo: "+deployDir) {
		t.Fatalf("stdout did not include staging summary:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerInitWarnsWhenShorthandMatchesLocalPath(t *testing.T) {
	packDir := makeCLITestPack(t)
	workDir := t.TempDir()
	t.Chdir(workDir)
	if err := os.Mkdir("demo", 0o755); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(t.TempDir(), "reploy-blueprint-index.json")
	indexContent := fmt.Sprintf(`{"schema_version":1,"blueprints":{"demo":{"ref":%q}}}`, "file:"+packDir)
	if err := os.WriteFile(indexPath, []byte(indexContent), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(packIndexURLEnv, "file:"+indexPath)

	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "demo")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, `reploy warning: APP_REF "demo" also exists as a local path`) {
		t.Fatalf("stderr missing shorthand/local path warning:\n%s", stderr)
	}
	if !strings.Contains(stdout, "created staging directory for demo: "+deployDir) {
		t.Fatalf("stdout did not include staging summary:\n%s", stdout)
	}
}

func TestDockerInitExistingDefaultDeploymentSuggestsUpdate(t *testing.T) {
	packDir := makeCLITestPack(t)
	workDir := t.TempDir()
	t.Chdir(workDir)

	code, stdout, stderr := runCLI("stage", "file:"+packDir)
	if code != 0 {
		t.Fatalf("initial stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("stage", "file:"+packDir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "staging directory already exists at reploy-staging") {
		t.Fatalf("stderr missing existing deployment message:\n%s", stderr)
	}
	if !strings.Contains(stderr, "use --update to update it") {
		t.Fatalf("stderr missing update hint:\n%s", stderr)
	}
}

func TestDockerInitRequiresPack(t *testing.T) {
	code, stdout, stderr := runCLI("stage")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "APP_REF is required") {
		t.Fatalf("stderr did not contain required blueprint message:\n%s", stderr)
	}
	for _, want := range []string{
		"Usage: reploy [--docker-timeout DURATION] stage APP_REF [OPTIONS]",
		"arbiter-server==VERSION",
		"pypi://PACKAGE/PATH/APP.blueprint.yaml",
		"pypi://PACKAGE/PATH/APP.blueprint.yaml?version=VERSION",
		"github://ORG/REPO/PATH/APP.blueprint.yaml?ref=REF",
		"./PATH",
		"/ABS/PATH",
		"file:PATH",
		"GitHub paths must point to the blueprint file inside the repository.",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
	for _, stale := range []string{"source:PATH"} {
		if strings.Contains(stderr, stale) {
			t.Fatalf("stderr contains stale ref guidance %q:\n%s", stale, stderr)
		}
	}
}

func TestDockerInitValidatesPack(t *testing.T) {
	code, stdout, stderr := runCLI("stage", "oci:example")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unsupported blueprint reference scheme: oci") {
		t.Fatalf("stderr did not contain blueprint validation message:\n%s", stderr)
	}
}

func TestDockerInitRejectsRemovedBlueprintFlag(t *testing.T) {
	code, stdout, stderr := runCLI("stage", "--blueprint", "demo-suite")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown option: --blueprint") {
		t.Fatalf("stderr did not reject removed --blueprint flag:\n%s", stderr)
	}
}

func TestDockerStageRejectsRemovedRequirementOption(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI(
		"stage",
		"--dir",
		deployDir,
		"file:"+packDir,
		"--requirement",
		"demo-server==1.2.3",
		"--requirement=demo-imap==1.2.3",
	)
	if code != 2 || stdout != "" || !strings.Contains(stderr, "unknown option: --requirement") {
		t.Fatalf("removed requirement option = %d/%q/%q", code, stdout, stderr)
	}
}

func TestDockerStageAcceptsAPTAfterProviderCutover(t *testing.T) {
	packDir := makeCLITestPackWithManifest(t, `blueprint:
  schema: 1
  version: 0.1.0
  compatibility:
    platforms: [linux/amd64]
environment:
  id: apt-gate-test
  base:
    image: python:3.13-slim
  applications:
    application:
      packages:
        os: [curl]
        python:
          requirements: [demo]
docker: {}
`)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "created staging directory for apt-gate-test") {
		t.Fatalf("stdout missing staged deployment confirmation:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if _, err := os.Stat(deployDir); err != nil {
		t.Fatalf("staged deployment is missing: %v", err)
	}
}

func TestDockerStageAcceptsGitPackRef(t *testing.T) {
	sourceDir, _ := makeCLITestGitSourcePack(t)
	cacheDir := filepath.Join(t.TempDir(), "cache")
	t.Setenv("REPLOY_CACHE_DIR", cacheDir)
	sourceURL := localFileURL(sourceDir)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "--platform", "linux/amd64", "git:"+sourceURL+"?ref=main")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "created staging directory for git-source-app: "+deployDir+"\n" || stderr != "" {
		t.Fatalf("stdout/stderr = %q/%q", stdout, stderr)
	}
	assertCLIStateV1Platform(t, deployDir, "linux/amd64")
	for _, legacy := range []string{dockerdeploy.ComposeFileName} {
		if _, err := os.Stat(filepath.Join(deployDir, legacy)); !os.IsNotExist(err) {
			t.Fatalf("legacy staging file %s exists or could not be inspected: %v", legacy, err)
		}
	}
}

func TestParseDockerCommandOptionsAcceptsExplicitPyPIPackageRef(t *testing.T) {
	options, err := parseDockerCommandOptions([]string{"pypi:demo-suite==1.2.3#demo_suite/reploy/demo-suite.blueprint.yaml"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if options.Pack.Raw != "pypi:demo-suite==1.2.3#demo_suite/reploy/demo-suite.blueprint.yaml" {
		t.Fatalf("raw = %q", options.Pack.Raw)
	}
	if options.Pack.Scheme != "pypi" {
		t.Fatalf("scheme = %q", options.Pack.Scheme)
	}
	if options.Pack.Source != "demo-suite==1.2.3" {
		t.Fatalf("source = %q", options.Pack.Source)
	}
	if options.Pack.Subdir != "demo_suite/reploy/demo-suite.blueprint.yaml" {
		t.Fatalf("subdir = %q", options.Pack.Subdir)
	}
	if !options.Pack.IsPinned {
		t.Fatal("pinned pypi ref should be pinned")
	}
}

func TestParseDockerCommandOptionsRejectsRemovedFCDOption(t *testing.T) {
	for _, args := range [][]string{
		{"--fcd", "pypi:demo-suite"},
		{"--fcd=pypi:demo-suite"},
	} {
		_, err := parseDockerCommandOptions(args, true)
		if err == nil {
			t.Fatalf("expected error for %v", args)
		}
		if !strings.Contains(err.Error(), "unknown option: --fcd") {
			t.Fatalf("unexpected error for %v: %v", args, err)
		}
	}
}

func TestParseDockerCommandOptionsExpandsDemoSuitePackAlias(t *testing.T) {
	setCLITestPackIndex(t)

	options, err := parseDockerCommandOptions([]string{"demo-suite"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if options.Pack.Raw != "demo-suite" {
		t.Fatalf("raw = %q", options.Pack.Raw)
	}
	if options.Pack.Scheme != "pypi" {
		t.Fatalf("scheme = %q", options.Pack.Scheme)
	}
	if options.Pack.Source != "demo-suite" {
		t.Fatalf("source = %q", options.Pack.Source)
	}
	if options.Pack.Subdir != "demo_suite/reploy/demo-suite.blueprint.yaml" {
		t.Fatalf("subdir = %q", options.Pack.Subdir)
	}
	if options.Pack.IsPinned {
		t.Fatal("latest alias should not be pinned")
	}
}

func TestParseDockerCommandOptionsExpandsPinnedDemoSuitePackAlias(t *testing.T) {
	setCLITestPackIndex(t)

	options, err := parseDockerCommandOptions([]string{"demo-suite==1.2.3"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if options.Pack.Raw != "demo-suite==1.2.3" {
		t.Fatalf("raw = %q", options.Pack.Raw)
	}
	if options.Pack.Source != "demo-suite==1.2.3" {
		t.Fatalf("source = %q", options.Pack.Source)
	}
	if options.Pack.Subdir != "demo_suite/reploy/demo-suite.blueprint.yaml" {
		t.Fatalf("subdir = %q", options.Pack.Subdir)
	}
	if !options.Pack.IsPinned {
		t.Fatal("pinned alias should be pinned")
	}
}

func TestParseDockerCommandOptionsExpandsDemoServerPackAlias(t *testing.T) {
	setCLITestPackIndex(t)

	options, err := parseDockerCommandOptions([]string{"demo-server"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if options.Pack.Raw != "demo-server" {
		t.Fatalf("raw = %q", options.Pack.Raw)
	}
	if options.Pack.Scheme != "pypi" {
		t.Fatalf("scheme = %q", options.Pack.Scheme)
	}
	if options.Pack.Source != "demo-server" {
		t.Fatalf("source = %q", options.Pack.Source)
	}
	if options.Pack.Subdir != "demo_server/reploy/demo-server.blueprint.yaml" {
		t.Fatalf("subdir = %q", options.Pack.Subdir)
	}
	if options.Pack.IsPinned {
		t.Fatal("latest alias should not be pinned")
	}
}

func TestParseDockerCommandOptionsExpandsPinnedDemoServerPackAlias(t *testing.T) {
	setCLITestPackIndex(t)

	options, err := parseDockerCommandOptions([]string{"demo-server==1.2.3"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if options.Pack.Raw != "demo-server==1.2.3" {
		t.Fatalf("raw = %q", options.Pack.Raw)
	}
	if options.Pack.Source != "demo-server==1.2.3" {
		t.Fatalf("source = %q", options.Pack.Source)
	}
	if options.Pack.Subdir != "demo_server/reploy/demo-server.blueprint.yaml" {
		t.Fatalf("subdir = %q", options.Pack.Subdir)
	}
	if !options.Pack.IsPinned {
		t.Fatal("pinned alias should be pinned")
	}
}

func TestParseDockerCommandOptionsPreservesDemoSuitePackAliasQuery(t *testing.T) {
	setCLITestPackIndex(t)

	options, err := parseDockerCommandOptions([]string{"demo-suite?index-url=http://example.test"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if options.Pack.Raw != "demo-suite?index-url=http://example.test" {
		t.Fatalf("raw = %q", options.Pack.Raw)
	}
	if options.Pack.Source != "demo-suite" {
		t.Fatalf("source = %q", options.Pack.Source)
	}
	if options.Pack.Subdir != "demo_suite/reploy/demo-suite.blueprint.yaml" {
		t.Fatalf("subdir = %q", options.Pack.Subdir)
	}
	if options.Pack.Query.Get("index-url") != "http://example.test" {
		t.Fatalf("index-url query = %q", options.Pack.Query.Get("index-url"))
	}
	if options.Pack.IsPinned {
		t.Fatal("latest alias with query should not be pinned")
	}
}

func TestParseDockerCommandOptionsRejectsDuplicatePack(t *testing.T) {
	setCLITestPackIndex(t)

	_, err := parseDockerCommandOptions([]string{"demo-suite", "file:deploy/demo.blueprint.yaml"}, true)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "APP_REF may only be provided once") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseDockerCommandOptionsLoadsPackIndexFromHTTPAndCache(t *testing.T) {
	indexContent := `{"schema_version":1,"blueprints":{"demo":{"ref":"pypi://demo-pkg/demo_pkg/reploy/demo.blueprint.yaml"}}}`
	requests := 0
	server := newCLITestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, indexContent)
	}))
	cacheDir := filepath.Join(t.TempDir(), "cache")
	t.Setenv("REPLOY_CACHE_DIR", cacheDir)
	t.Setenv(packIndexURLEnv, server.URL+"/index.json")

	options, err := parseDockerCommandOptions([]string{"demo==1.2.3"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if options.Pack.Source != "demo-pkg==1.2.3" || options.Pack.Subdir != "demo_pkg/reploy/demo.blueprint.yaml" {
		t.Fatalf("pack = %#v", options.Pack)
	}
	server.Close()

	options, err = parseDockerCommandOptions([]string{"demo"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if options.Pack.Source != "demo-pkg" {
		t.Fatalf("source = %q", options.Pack.Source)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestParseDockerCommandOptionsRejectsPinnedShorthandWhenRefAlreadyHasVersion(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "blueprint-index.json")
	content := `{"schema_version":1,"blueprints":{"demo":{"ref":"pypi://demo-pkg/demo_pkg/reploy/demo.blueprint.yaml?version=1.0.0"}}}`
	if err := os.WriteFile(indexPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(packIndexURLEnv, "file:"+indexPath)

	_, err := parseDockerCommandOptions([]string{"demo==1.2.3"}, true)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "already declares version") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseDockerCommandOptionsRejectsRemovedVersionPlaceholder(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "blueprint-index.json")
	content := `{"schema_version":1,"blueprints":{"demo":{"ref":"pypi://demo-pkg/demo_pkg/reploy/demo.blueprint.yaml?version={version}"}}}`
	if err := os.WriteFile(indexPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(packIndexURLEnv, "file:"+indexPath)

	_, err := parseDockerCommandOptions([]string{"demo==1.2.3"}, true)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "must not use the removed {version} placeholder") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseDockerCommandOptionsAppendsGitRefForPinnedGitHubShorthand(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "blueprint-index.json")
	content := `{"schema_version":1,"blueprints":{"demo":{"ref":"github://acme/demo/demo_pkg/reploy/demo.blueprint.yaml"}}}`
	if err := os.WriteFile(indexPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(packIndexURLEnv, "file:"+indexPath)

	options, err := parseDockerCommandOptions([]string{"demo==feature/demo"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if options.Pack.Scheme != "git" {
		t.Fatalf("scheme = %q", options.Pack.Scheme)
	}
	if options.Pack.Source != "https://github.com/acme/demo.git" {
		t.Fatalf("source = %q", options.Pack.Source)
	}
	if options.Pack.Subdir != "demo_pkg/reploy/demo.blueprint.yaml" {
		t.Fatalf("subdir = %q", options.Pack.Subdir)
	}
	if options.Pack.Query.Get("ref") != "feature/demo" {
		t.Fatalf("ref query = %#v", options.Pack.Query)
	}
}

func TestDockerStageLoadsPyPIPackIntoStateV1(t *testing.T) {
	version := "4.5.6"
	blueprintPath := "demo_pkg/reploy/demo.blueprint.yaml"
	wheel := makeCLITestPackWheel(t, "demo_pkg/reploy", version)
	indexURL := makeCLITestPyPIIndex(t, wheel, version)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	packRef := "pypi:demo-pkg#" + blueprintPath + "?index-url=" + indexURL

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, packRef)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "created staging directory for demo: "+deployDir) {
		t.Fatalf("stdout did not include staging summary:\n%s", stdout)
	}
	if strings.Contains(stdout, "updated ") {
		t.Fatalf("stdout should not include generated file updates without --verbose:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	stateContent, err := os.ReadFile(filepath.Join(deployDir, dockerdeploy.StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	state, err := deploy.DecodeStateV1(stateContent)
	if err != nil {
		t.Fatal(err)
	}
	if state.Schema != deploy.StateSchemaV1 || state.Staging == nil || state.Current != nil {
		t.Fatalf("staged PyPI state = %#v", state)
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	if document.Environment.ID != "demo" || state.BlueprintSource != cliTestPackManifest() {
		t.Fatalf("PyPI blueprint was not retained in state-v1: id=%q source=%q", document.Environment.ID, state.BlueprintSource)
	}
}

func TestDockerStageUpdateRejectsExplicitRequirements(t *testing.T) {
	code, stdout, stderr := runCLI("stage", "--update", "--requirement", "demo-suite")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown option: --requirement") {
		t.Fatalf("stderr did not contain requirement message:\n%s", stderr)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, stdout, stderr := runCLI("wat")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown command: wat") {
		t.Fatalf("stderr did not contain unknown command:\n%s", stderr)
	}
}

func TestBootstrapCommandIsNotPublicSurface(t *testing.T) {
	code, stdout, stderr := runCLI("bootstrap")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown command: bootstrap") {
		t.Fatalf("stderr did not contain unknown command:\n%s", stderr)
	}
}

func TestSmokeCommandIsNotPublicSurface(t *testing.T) {
	code, stdout, stderr := runCLI("smoke")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown command: smoke") {
		t.Fatalf("stderr did not contain unknown command:\n%s", stderr)
	}
}

func TestTopLevelConfigCommandIsNotAppConfigSurface(t *testing.T) {
	code, stdout, stderr := runCLI("config", "check")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown command: config") {
		t.Fatalf("stderr did not contain unknown command:\n%s", stderr)
	}
}

func TestTopLevelAppCommandSuggestsAppPrefix(t *testing.T) {
	packDir := makeCLITestPack(t)
	workDir := t.TempDir()
	t.Chdir(workDir)
	code, stdout, stderr := runCLI("stage", "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("config", "check")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown command: config") || !strings.Contains(stderr, "did you mean `reploy app config check`?") {
		t.Fatalf("stderr did not suggest app prefix:\n%s", stderr)
	}
}

func TestDockerStageUpdateUsesExistingState(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("stage", "--update", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("stage --update failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	want := "staging directory is up to date: " + deployDir + "\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerStageUpdateAcceptsPackRef(t *testing.T) {
	packDir := makeCLITestPack(t)
	updatedPackDir := makeCLITestPackWithManifest(t, strings.ReplaceAll(cliTestPackManifest(), "color_env: DEMO_COLOR", "color_env: UPDATED_COLOR"))
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("stage", "--update", "--dir", deployDir, "file:"+updatedPackDir)
	if code != 0 {
		t.Fatalf("stage --update failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "updated staging directory: "+deployDir) {
		t.Fatalf("stdout missing staging update summary:\n%s", stdout)
	}
	if strings.Contains(stdout, filepath.Join(deployDir, dockerdeploy.StateFileName)) {
		t.Fatalf("stdout should not include generated file updates without --verbose:\n%s", stdout)
	}
	stateContent, err := os.ReadFile(filepath.Join(deployDir, dockerdeploy.StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	state, err := deploy.DecodeStateV1(stateContent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state.BlueprintSource, "color_env: UPDATED_COLOR") {
		t.Fatalf("updated blueprint source was not retained:\n%s", state.BlueprintSource)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerStageUpdateReportsFriendlyDeploymentDirError(t *testing.T) {
	packDir := makeCLITestPack(t)
	invalidPackDir := makeCLITestPackWithManifest(t, strings.Replace(cliTestPackManifest(), "      source: conf\n", "      source: ../outside\n", 1))
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("stage", "--update", "--dir", deployDir, "file:"+invalidPackDir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	for _, want := range []string{"reploy stage --update error: resolve blueprint manifest:", "docker.mounts.config managed-bind requires managed update policy and relative source"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestDockerStageUpdateForceReplacesDifferentEnvironment(t *testing.T) {
	packDir := makeCLITestPack(t)
	replacementDir := makeCLITestPackWithManifest(t, strings.Replace(cliTestPackManifest(), "  id: demo\n", "  id: replacement\n", 1))
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	operation, err := deploy.AcquireOperationLock(t.Context(), deployDir)
	if err != nil {
		t.Fatal(err)
	}
	active := deploy.LiveRunV1{
		ID: "run-0000000000000001", Kind: deploy.LiveRunKindAppV1, Name: "export",
		GenerationReference: "staged/demo", Exclusive: false,
	}
	if _, err := operation.AdmitLiveRunV1(active, false); err != nil {
		t.Fatal(err)
	}
	waiting := active
	waiting.ID = "run-0000000000000002"
	waiting.Exclusive = true
	if _, err := operation.AdmitLiveRunV1(waiting, true); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr = runCLI("stage", "--update", "--dir", deployDir, "file:"+replacementDir)
	if code != 1 || stdout != "" || !strings.Contains(stderr, `staging directory belongs to environment "demo"`) {
		t.Fatalf("unforced replacement = %d/%q/%q", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("stage", "--update", "--force", "--dir", deployDir, "file:"+replacementDir)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "updated staging directory: "+deployDir) {
		t.Fatalf("forced replacement = %d/%q/%q", code, stdout, stderr)
	}
	content, err := os.ReadFile(filepath.Join(deployDir, dockerdeploy.StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	state, err := deploy.DecodeStateV1(content)
	if err != nil {
		t.Fatal(err)
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	if document.Environment.ID != "replacement" || state.Current != nil || !reflect.DeepEqual(state.Overlay, deploy.EmptyRequestOverlayV1()) {
		t.Fatalf("forced replacement state = %#v, environment = %q", state, document.Environment.ID)
	}
	operation, err = deploy.AcquireOperationLock(t.Context(), deployDir)
	if err != nil {
		t.Fatal(err)
	}
	queue, found, err := operation.ReadLiveRunQueueV1()
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	if len(queue.Runs) != 0 {
		t.Fatalf("live-run queue after forced replacement = %#v, found = %t", queue, found)
	}
}

func TestDockerStageForceRequiresUpdateAndAllowsRetainedSourceRecovery(t *testing.T) {
	packDir := makeCLITestPack(t)
	code, stdout, stderr := runCLI("stage", "--force", "file:"+packDir)
	if code != 2 || stdout != "" || !strings.Contains(stderr, "--force requires --update") {
		t.Fatalf("stage force validation = %d/%q/%q", code, stdout, stderr)
	}
	original := dockerForceRestageCurrentDesiredPlatform
	t.Cleanup(func() {
		dockerForceRestageCurrentDesiredPlatform = original
	})
	called := false
	dockerForceRestageCurrentDesiredPlatform = func(
		_ context.Context,
		dir string,
		platform string,
		options dockerdeploy.RunOptions,
	) (deploy.DesiredStateUpdateResult, error) {
		called = true
		if dir == "" ||
			platform != "linux/amd64" ||
			options.DockerPreflightTimeout != 13*time.Second {
			t.Fatalf("forced retained-source update = %q/%q/%#v", dir, platform, options)
		}
		return deploy.DesiredStateUpdateResult{}, nil
	}
	dir := t.TempDir()
	code, stdout, stderr = runCLI(
		"--docker-timeout", "13s",
		"stage", "--update", "--force", "--platform", "linux/amd64", "--dir", dir,
	)
	if code != 0 || stdout != "staging directory is up to date: "+dir+"\n" || stderr != "" || !called {
		t.Fatalf("forced retained-source update = %d/%q/%q, called=%t", code, stdout, stderr, called)
	}
}

func TestDockerStageRemoveRoutesWithoutAppReference(t *testing.T) {
	original := dockerRemoveStagedDeployment
	t.Cleanup(func() {
		dockerRemoveStagedDeployment = original
	})
	dir := t.TempDir()
	called := false
	dockerRemoveStagedDeployment = func(
		_ context.Context,
		input dockerdeploy.StagedDeploymentRemoveInputV1,
	) (dockerdeploy.StagedDeploymentRemoveResultV1, error) {
		called = true
		if input.DeploymentDir != dir ||
			input.ControlMode != dockerdeploy.ControlAdmissionForceV1 {
			t.Fatalf("remove input = %#v", input)
		}
		return dockerdeploy.StagedDeploymentRemoveResultV1{
			DeploymentDir: dir,
			Environment:   "demo",
		}, nil
	}
	code, stdout, stderr := runCLI(
		"stage", "--remove", "--force", "--verbose", "--dir", dir,
	)
	if code != 0 || stderr != "" ||
		stdout != "removed staging directory: "+dir+"\nremoved environment: demo\n" || !called {
		t.Fatalf("stage remove = %d/%q/%q, called=%t", code, stdout, stderr, called)
	}
}

func TestDockerStageRemoveRejectsConflictingInputs(t *testing.T) {
	setCLITestPackIndex(t)
	for _, args := range [][]string{
		{"stage", "--remove", "--update"},
		{"stage", "--remove", "--platform", "linux/amd64"},
		{"stage", "--remove", "demo-server"},
	} {
		code, stdout, stderr := runCLI(args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "--remove") {
			t.Fatalf("stage remove conflict %q = %d/%q/%q", args, code, stdout, stderr)
		}
	}
}

func writeCLIOverlayDeployment(t *testing.T) string {
	t.Helper()
	content := []byte(`blueprint:
  schema: 1
  version: 0.1.0
  compatibility:
    platforms: [linux/amd64]
environment:
  id: overlay-cli
  base:
    image: python:3.13-slim
  applications:
    app:
      packages:
        python:
          requirements: [demo]
      options:
        debug:
          description: Install debug support.
          packages:
            python:
              requirements: [debugpy]
    tools:
      packages:
        os: [curl]
docker: {}
`)
	syntax, err := blueprint.Decode(content)
	if err != nil {
		t.Fatal(err)
	}
	document, err := blueprint.Resolve(syntax)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "deployment")
	if err := os.MkdirAll(filepath.Join(dir, ".reploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	state, err := deploy.EncodeStateV1(deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: resolved,
		Platform: document.Blueprint.Compatibility.Platforms[0], Overlay: deploy.EmptyRequestOverlayV1(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, dockerdeploy.StateFileName), state, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readCLIOverlayState(t *testing.T, dir string) deploy.StateV1 {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dir, dockerdeploy.StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	state, err := deploy.DecodeStateV1(content)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func markCLIOverlayDeploymentInstalled(t *testing.T, dir string) {
	t.Helper()
	state := readCLIOverlayState(t, dir)
	state.Deployment = &deploy.DeploymentStateV1{
		Schema: deploy.DeploymentStateSchemaV1,
		Installation: deploy.InstallationStateV1{
			Schema: deploy.InstallationSchemaV1, Status: deploy.InstallationStatusReady,
			TargetDir: dir, Scope: "system", Service: "overlay-cli", InstanceID: "overlay-cli-1",
			ComposeProject: "overlay-cli", ContainerName: "overlay-cli", NetworkName: "overlay-cli",
			Ports: []deploy.InstallationPortBindingV1{},
		},
	}
	content, err := deploy.EncodeStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, dockerdeploy.StateFileName), content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDockerBundleOverlayMutationsUpdateStateWithoutBuilding(t *testing.T) {
	dir := writeCLIOverlayDeployment(t)
	code, stdout, stderr := runCLI("bundle", "add", "app/debug", "--dir", dir)
	if code != 0 || stdout != "bundle overlay updated\n" || stderr != "" {
		t.Fatalf("add result = %d/%q/%q", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI("bundle", "add", "app/debug", "--dir", dir)
	if code != 0 || stdout != "bundle overlay unchanged\n" || stderr != "" {
		t.Fatalf("duplicate add result = %d/%q/%q", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI("bundle", "add-package", "application/app/python", "rich>=13", "--dir="+dir)
	if code != 0 || stdout != "bundle overlay updated\n" || stderr != "" {
		t.Fatalf("add-package result = %d/%q/%q", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI("bundle", "add-package", "application/tools/os", "jq", "--dir", dir)
	if code != 0 || stdout != "bundle overlay updated\n" || stderr != "" {
		t.Fatalf("APT add-package result = %d/%q/%q", code, stdout, stderr)
	}
	state := readCLIOverlayState(t, dir)
	if len(state.Overlay.SelectedOptions) != 1 || len(state.Overlay.DirectPackages) != 2 {
		t.Fatalf("overlay = %#v", state.Overlay)
	}
	if _, err := os.Stat(filepath.Join(dir, ".reploy", "providers")); !os.IsNotExist(err) {
		t.Fatalf("overlay mutation built provider artifacts: %v", err)
	}
	code, stdout, stderr = runCLI("bundle", "remove-package", "application/app/python", "rich>=13", "--dir", dir)
	if code != 0 || stdout != "bundle overlay updated\n" || stderr != "" {
		t.Fatalf("remove-package result = %d/%q/%q", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI("bundle", "remove", "app/debug", "--dir", dir)
	if code != 0 || stdout != "bundle overlay updated\n" || stderr != "" {
		t.Fatalf("remove result = %d/%q/%q", code, stdout, stderr)
	}
}

func TestDockerBundleOverlayMutationUsage(t *testing.T) {
	for _, args := range [][]string{
		{"bundle", "add"},
		{"bundle", "remove"},
		{"bundle", "add-package", "app"},
		{"bundle", "remove-package", "app"},
		{"bundle", "add", "app/debug", "--extra", "rich"},
	} {
		code, stdout, stderr := runCLI(args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "reploy usage error:") {
			t.Fatalf("args/result = %#v/%d/%q/%q", args, code, stdout, stderr)
		}
	}
}

func TestDockerBundleOptionsAndListInspectStateV1Overlay(t *testing.T) {
	dir := writeCLIOverlayDeployment(t)
	code, stdout, stderr := runCLI("bundle", "options", "--dir", dir)
	if code != 0 || stdout != "app/debug\tInstall debug support.\n" || stderr != "" {
		t.Fatalf("options result = %d/%q/%q", code, stdout, stderr)
	}
	for _, args := range [][]string{
		{"bundle", "add", "app/debug", "--dir", dir},
		{"bundle", "add-package", "application/app/python", "rich>=13", "--dir", dir},
		{"bundle", "add-package", "application/tools/os", "jq=1.7", "--dir", dir},
	} {
		code, stdout, stderr = runCLI(args...)
		if code != 0 || stderr != "" {
			t.Fatalf("mutation args/result = %#v/%d/%q/%q", args, code, stdout, stderr)
		}
	}
	before := readCLIOverlayState(t, dir)
	code, stdout, stderr = runCLI("bundle", "list", "--dir="+dir)
	if code != 0 || stderr != "" {
		t.Fatalf("list result = %d/%q/%q", code, stdout, stderr)
	}
	want := "option\tapp/debug\npackage\tapplication/app/python\trich>=13\npackage\tapplication/tools/os\tjq=1.7\n"
	if stdout != want {
		t.Fatalf("list stdout = %q, want %q", stdout, want)
	}
	after := readCLIOverlayState(t, dir)
	if !reflect.DeepEqual(after, before) {
		t.Fatal("bundle inspection changed state")
	}
}

func TestDockerBundlePrototypeInspectionCommandsAreRemoved(t *testing.T) {
	for _, args := range [][]string{{"bundle", "list-options"}, {"bundle", "list", "all"}} {
		code, stdout, stderr := runCLI(args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "reploy usage error:") {
			t.Fatalf("args/result = %#v/%d/%q/%q", args, code, stdout, stderr)
		}
	}
}

func TestDockerBundleInspectionRejectsInstalledStateV1(t *testing.T) {
	dir := writeCLIOverlayDeployment(t)
	markCLIOverlayDeploymentInstalled(t, dir)
	code, stdout, stderr := runCLI("bundle", "list", "--dir", dir)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "request overlay is only available on a staging deployment") {
		t.Fatalf("result = %d/%q/%q", code, stdout, stderr)
	}
}
func TestDockerBundleArtifactHelpersAreNotPublicSurface(t *testing.T) {
	for _, command := range []string{"add-wheel", "add-source"} {
		code, stdout, stderr := runCLI("bundle", command, "foo")
		if code != 2 {
			t.Fatalf("%s exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", command, code, stdout, stderr)
		}
		if stdout != "" {
			t.Fatalf("%s stdout = %q, want empty", command, stdout)
		}
		if !strings.Contains(stderr, "unknown bundle command: "+command) {
			t.Fatalf("%s stderr missing unknown command:\n%s", command, stderr)
		}
		if strings.Contains(stderr, "state.json") || strings.Contains(stderr, "reploy-staging") {
			t.Fatalf("%s stderr should reject helper before resolving staging:\n%s", command, stderr)
		}
	}

	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	wheel := filepath.Join(t.TempDir(), "demo-1.0.0-py3-none-any.whl")
	if err := os.WriteFile(wheel, []byte("wheel content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr = runCLI("bundle", "add-wheel", wheel, "--dir", deployDir)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown bundle command: add-wheel") {
		t.Fatalf("stderr missing unknown command:\n%s", stderr)
	}

	code, stdout, stderr = runCLI("bundle", "add-source", filepath.Dir(wheel), "--dir", deployDir)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown bundle command: add-source") {
		t.Fatalf("stderr missing unknown command:\n%s", stderr)
	}
}

func TestDockerPrototypeBuildCommandsAreRemoved(t *testing.T) {
	for _, command := range []string{"check", "build", "upgrade", "prepare", "warm-runtime"} {
		code, stdout, stderr := runCLI("bundle", command)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "unknown bundle command: "+command) {
			t.Fatalf("%s result = %d/%q/%q", command, code, stdout, stderr)
		}
		if strings.Contains(stderr, "state.json") || strings.Contains(stderr, "reploy-staging") {
			t.Fatalf("%s resolved staging before rejecting the removed command: %s", command, stderr)
		}
	}
}
func TestDockerBundleWithoutCommandShowsSubcommands(t *testing.T) {
	code, stdout, stderr := runCLI("bundle")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "Usage: reploy [--docker-timeout DURATION] bundle COMMAND") ||
		!strings.Contains(stderr, "clean") ||
		!strings.Contains(stderr, "options") {
		t.Fatalf("stderr missing bundle subcommands:\n%s", stderr)
	}
	for _, removed := range []string{"check", "build", "upgrade", "list-options", "add-wheel", "add-source"} {
		if strings.Contains(stderr, removed) {
			t.Fatalf("stderr exposed removed bundle command %q:\n%s", removed, stderr)
		}
	}
}

func TestDockerBundleHelpShowsSubcommands(t *testing.T) {
	code, stdout, stderr := runCLI("bundle", "--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Usage: reploy [--docker-timeout DURATION] bundle COMMAND") ||
		!strings.Contains(stdout, "clean") ||
		!strings.Contains(stdout, "Deployment directory") ||
		!strings.Contains(stdout, "--verbose") {
		t.Fatalf("stdout missing bundle help:\n%s", stdout)
	}
	for _, removed := range []string{"check", "build", "upgrade", "list-options", "add-wheel", "add-source", "--dry-run", "--pypi-only", "--wheelhouse-backend", "--build-backend"} {
		if strings.Contains(stdout, removed) {
			t.Fatalf("stdout exposed removed bundle surface %q:\n%s", removed, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerBundleCleanRemovesOnlyDeploymentProviderStore(t *testing.T) {
	deployDir := writeCLIOverlayDeployment(t)
	markCLIOverlayDeploymentInstalled(t, deployDir)
	statePath := filepath.Join(deployDir, dockerdeploy.StateFileName)
	stateBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(deployDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(t.Context(), "packages/demo.deb", "deb", strings.NewReader("demo")); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCLI("bundle", "clean", "--dir", deployDir)
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("bundle clean result = %d/%q/%q", code, stdout, stderr)
	}
	if _, err := os.Lstat(store.Root()); !os.IsNotExist(err) {
		t.Fatalf("provider store still exists: %v", err)
	}
	stateAfter, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(stateBefore, stateAfter) {
		t.Fatalf("clean changed deployment state: %v", err)
	}
}

func TestDockerBundleCleanVerboseReportsProviderStoreRemovalAndNoOp(t *testing.T) {
	deployDir := writeCLIOverlayDeployment(t)
	store, err := providerstore.NewStore(deployDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(t.Context(), "packages/demo.deb", "deb", strings.NewReader("demo")); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLI("bundle", "clean", "--verbose", "--dir", deployDir)
	if code != 0 || stderr != "" || stdout != "removed "+store.Root()+"\n" {
		t.Fatalf("first clean result = %d/%q/%q", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI("bundle", "clean", "--verbose", "--dir", deployDir)
	if code != 0 || stderr != "" || stdout != string(deploy.UpdateStatusUpToDate)+"\n" {
		t.Fatalf("repeat clean result = %d/%q/%q", code, stdout, stderr)
	}
}

func forceSpinnerOutputInteractive(t *testing.T) {
	t.Helper()
	original := spinnerOutputIsInteractive
	spinnerOutputIsInteractive = func(io.Writer) bool { return true }
	t.Cleanup(func() {
		spinnerOutputIsInteractive = original
	})
}

func TestStartSpinnerPrintsCompletion(t *testing.T) {
	forceSpinnerOutputInteractive(t)
	t.Setenv("CI", "")
	t.Setenv("TERM", "xterm-256color")
	var stderr bytes.Buffer
	stop := startSpinner(&stderr, "building installation bundle")
	stop(true)
	if !strings.Contains(stderr.String(), "\x1b[?25l") || !strings.Contains(stderr.String(), "\x1b[?25h") {
		t.Fatalf("spinner did not hide and restore cursor:\n%q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "building installation bundle |") {
		t.Fatalf("spinner did not print label before frame:\n%q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "building installation bundle... done") {
		t.Fatalf("spinner did not print completion:\n%q", stderr.String())
	}
}

func TestStartSpinnerUsesPlainProgressForRedirectedOutput(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("TERM", "xterm-256color")
	var stderr bytes.Buffer
	stop := startSpinner(&stderr, "building installation bundle")
	stop(true)
	if got, want := stderr.String(), "building installation bundle...\nbuilding installation bundle... done\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestStartSpinnerUsesPlainProgressInCI(t *testing.T) {
	forceSpinnerOutputInteractive(t)
	t.Setenv("CI", "true")
	t.Setenv("TERM", "xterm-256color")
	var stderr bytes.Buffer
	stop := startSpinner(&stderr, "building installation bundle")
	stop(true)
	if got, want := stderr.String(), "building installation bundle...\nbuilding installation bundle... done\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestStartSpinnerUsesPlainProgressForDumbTerminal(t *testing.T) {
	forceSpinnerOutputInteractive(t)
	t.Setenv("CI", "")
	t.Setenv("TERM", "dumb")
	var stderr bytes.Buffer
	stop := startSpinner(&stderr, "building installation bundle")
	stop(false)
	if got, want := stderr.String(), "building installation bundle...\nbuilding installation bundle... failed\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestStartProgressSpinnerUpdatesAnimatedLabel(t *testing.T) {
	forceSpinnerOutputInteractive(t)
	t.Setenv("CI", "")
	t.Setenv("TERM", "xterm-256color")
	var stderr bytes.Buffer
	stop, progress := startProgressSpinner(&stderr, "installing from staging")
	fmt.Fprintln(progress, "copying staged deployment")
	time.Sleep(20 * time.Millisecond)
	stop(true)
	if !strings.Contains(stderr.String(), "installing from staging: copying staged deployment") {
		t.Fatalf("spinner did not update label:\n%q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "installing from staging... done") {
		t.Fatalf("spinner did not print completion:\n%q", stderr.String())
	}
}

func TestStartProgressSpinnerWithLogsKeepsLogLinesSeparate(t *testing.T) {
	forceSpinnerOutputInteractive(t)
	t.Setenv("CI", "")
	t.Setenv("TERM", "xterm-256color")
	var stderr bytes.Buffer
	stop, progress, logs := startProgressSpinnerWithLogs(&stderr, "installing from staging")
	terminalOutput, ok := logs.(interface{ TerminalOutput() io.Writer })
	if !ok {
		t.Fatalf("spinner log writer does not expose terminal output")
	}
	if terminalOutput.TerminalOutput() != &stderr {
		t.Fatalf("terminal output = %#v, want stderr", terminalOutput.TerminalOutput())
	}
	fmt.Fprintln(progress, "running before start hook: app config check")
	fmt.Fprint(logs, "[STAGING : smoke-app] warn: warning")
	fmt.Fprint(logs, ": Docker-managed install\n")
	time.Sleep(20 * time.Millisecond)
	stop(true)
	got := stderr.String()
	if !strings.Contains(got, "[STAGING : smoke-app] warn: warning: Docker-managed install\n") {
		t.Fatalf("spinner log writer did not keep prefixed log line intact:\n%q", got)
	}
	if strings.Contains(got, "/[STAGING") || strings.Contains(got, "|[STAGING") || strings.Contains(got, "-[STAGING") || strings.Contains(got, "\\[STAGING") {
		t.Fatalf("spinner frame collided with log line:\n%q", got)
	}
	if !strings.Contains(got, "installing from staging: running before start hook: app config check") {
		t.Fatalf("spinner did not keep progress label:\n%q", got)
	}
	if !strings.Contains(got, "installing from staging... done") {
		t.Fatalf("spinner did not print completion:\n%q", got)
	}
}

func TestStartProgressSpinnerUsesPlainProgressInCI(t *testing.T) {
	forceSpinnerOutputInteractive(t)
	t.Setenv("CI", "true")
	t.Setenv("TERM", "xterm-256color")
	var stderr bytes.Buffer
	stop, progress := startProgressSpinner(&stderr, "installing from staging")
	fmt.Fprintln(progress, "copying staged deployment")
	fmt.Fprintln(progress, "starting Docker-managed app")
	stop(true)
	want := strings.Join([]string{
		"installing from staging...",
		"installing from staging: copying staged deployment",
		"installing from staging: starting Docker-managed app",
		"installing from staging... done",
		"",
	}, "\n")
	if got := stderr.String(); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestFormatOperationElapsed(t *testing.T) {
	for _, test := range []struct {
		elapsed time.Duration
		want    string
	}{
		{elapsed: 245 * time.Millisecond, want: "250ms"},
		{elapsed: 15*time.Second + 400*time.Millisecond, want: "15s"},
		{elapsed: 75 * time.Second, want: "1m15s"},
	} {
		if got := formatOperationElapsed(test.elapsed); got != test.want {
			t.Fatalf("formatOperationElapsed(%s) = %q, want %q", test.elapsed, got, test.want)
		}
	}
}

func TestPrintUpdateResultsShowsOnlyActionablePaths(t *testing.T) {
	var stdout bytes.Buffer
	printUpdateResults(&stdout, []dockerdeploy.UpdateResult{
		{Path: "deployment/compose.yaml", Status: deploy.UpdateStatusUpToDate},
		{Path: "deployment/democtl", Status: deploy.UpdateStatusUpdated},
		{Path: "deployment/docker.env", Status: deploy.UpdateStatusSkipped},
	})

	expected := "updated deployment/democtl\nskipped deployment/docker.env\n"
	if stdout.String() != expected {
		t.Fatalf("stdout = %q, want %q", stdout.String(), expected)
	}
}

func TestDockerInfoShowsDeploymentState(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("info", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("info failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{"runtime: docker", "bundle: not built", "image: not built", "phase: staged"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	for _, forbidden := range []string{"target:", "resolved:", "materialized image:"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("stdout retained internal label %q:\n%s", forbidden, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func makeCLITestPack(t *testing.T) string {
	t.Helper()
	return makeCLITestPackWithManifest(t, cliTestPackManifest())
}

func makeCLITestSourcePack(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname = \"demo-suite\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blueprintDir := filepath.Join(dir, "demo_suite", "reploy")
	if err := os.MkdirAll(blueprintDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blueprintDir, "demo.blueprint.yaml"), []byte(cliTestPackManifest()), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func makeCLITestGitSourcePack(t *testing.T) (string, string) {
	t.Helper()
	sourceDir := filepath.Join(t.TempDir(), "git-source-app")
	copyCLITestTree(t, filepath.Join(cliTestRepoRoot(t), "tests", "e2e", "python", "packages", "git-source-app"), sourceDir)
	repository, err := git.PlainInit(sourceDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))); err != nil {
		t.Fatal(err)
	}
	worktree, err := repository.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		relativePath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		_, err = worktree.Add(filepath.ToSlash(relativePath))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	hash, err := worktree.Commit("add git source app fixture", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Reploy Test",
			Email: "test@example.com",
			When:  time.Unix(1, 0),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return sourceDir, hash.String()
}

func localFileURL(path string) string {
	slashed := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && len(slashed) >= 2 && slashed[1] == ':' {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}

func copyCLITestTree(t *testing.T, sourceDir string, targetDir string) {
	t.Helper()
	if err := filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(targetDir, relativePath)
		if entry.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(targetPath, content, info.Mode().Perm())
	}); err != nil {
		t.Fatal(err)
	}
}

func cliTestRepoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate cli test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func makeCLITestPackWithManifest(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.blueprint.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func markCLITestDeploymentInstalled(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, dockerdeploy.StateFileName)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := deploy.DecodeStateV1(content)
	if err != nil {
		t.Fatal(err)
	}
	state.Staging = nil
	state.Deployment = cliTestDeploymentStateV1(dir, "demo", "demo")
	content, err = deploy.EncodeStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func makeCLITestPackWheel(t *testing.T, subdir string, version string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	files := map[string]string{
		subdir + "/demo.blueprint.yaml":                        cliTestPackManifest(),
		fmt.Sprintf("demo_pkg-%s.dist-info/WHEEL", version):    "Wheel-Version: 1.0\nGenerator: reploy-test\nRoot-Is-Purelib: true\nTag: py3-none-any\n",
		fmt.Sprintf("demo_pkg-%s.dist-info/METADATA", version): "Metadata-Version: 2.1\nName: demo-pkg\nVersion: " + version + "\n",
	}
	for path, content := range files {
		file, err := writer.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func writeCLITestInstalledState(t *testing.T, dir string, appID string, service string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, dockerdeploy.ReployInternalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	syntax, err := blueprint.Decode([]byte(fmt.Sprintf(`blueprint:
  schema: 1
  version: 0.1.0
  compatibility:
    platforms: [linux/amd64]
environment:
  id: %s
  base:
    image: alpine:3.20
  applications:
    application:
      packages:
        os: [busybox]
docker: {}
`, appID)))
	if err != nil {
		t.Fatal(err)
	}
	document, err := blueprint.Resolve(syntax)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	state := deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: resolved,
		Platform: document.Blueprint.Compatibility.Platforms[0], Overlay: deploy.EmptyRequestOverlayV1(),
		Deployment: cliTestDeploymentStateV1(dir, appID, service),
	}
	content, err := deploy.EncodeStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, dockerdeploy.StateFileName), content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeCLITestStagedState(t *testing.T, dir string, appID string) {
	t.Helper()
	writeCLITestInstalledState(t, dir, appID, appID+"-service")
	path := filepath.Join(dir, dockerdeploy.StateFileName)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := deploy.DecodeStateV1(content)
	if err != nil {
		t.Fatal(err)
	}
	state.Deployment = nil
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	state.BlueprintSource = "test blueprint"
	content, err = deploy.EncodeStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func cliTestProviderBuildResult(t *testing.T, dir string, reused bool) dockerdeploy.LockedProviderBuildExecutionResultV1 {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dir, dockerdeploy.StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	state, err := deploy.DecodeStateV1(content)
	if err != nil {
		t.Fatal(err)
	}
	state.Current = &deploy.EnvironmentGenerationState{
		Reference: "reploy/env/demo:g-test", ImageDigest: "sha256:demo-image",
	}
	return dockerdeploy.LockedProviderBuildExecutionResultV1{State: state, Reused: reused}
}

func markCLITestSystemd(t *testing.T, dir string, unitPath string) {
	t.Helper()
	path := filepath.Join(dir, dockerdeploy.StateFileName)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := deploy.DecodeStateV1(content)
	if err != nil {
		t.Fatal(err)
	}
	state.Deployment.Installation.UnitPath = unitPath
	content, err = deploy.EncodeStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func cliTestDeploymentStateV1(dir string, appID string, service string) *deploy.DeploymentStateV1 {
	return &deploy.DeploymentStateV1{
		Schema: deploy.DeploymentStateSchemaV1,
		Installation: deploy.InstallationStateV1{
			Schema: deploy.InstallationSchemaV1, Status: deploy.InstallationStatusReady,
			TargetDir: dir, Scope: "system", Service: service, InstanceID: appID + "-test",
			ComposeProject: service, ContainerName: service, NetworkName: service,
			Ports: []deploy.InstallationPortBindingV1{},
		},
	}
}

func makeCLITestPyPIIndex(t *testing.T, wheel []byte, version string) string {
	t.Helper()
	filename := fmt.Sprintf("demo_pkg-%s-py3-none-any.whl", version)
	sha256 := deploy.HashBytes(wheel)
	server := newCLITestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pypi/demo-pkg/json":
			w.Header().Set("Content-Type", "application/json")
			wheelURL := "http://" + r.Host + "/files/" + filename
			response := map[string]any{
				"info": map[string]string{"version": version},
				"releases": map[string]any{
					version: []map[string]any{{
						"filename":    filename,
						"url":         wheelURL,
						"packagetype": "bdist_wheel",
						"digests":     map[string]string{"sha256": sha256},
					}},
				},
				"urls": []any{},
			}
			if err := json.NewEncoder(w).Encode(response); err != nil {
				t.Logf("write pypi response: %v", err)
			}
		case "/files/" + filename:
			w.Header().Set("Content-Type", "application/octet-stream")
			if _, err := w.Write(wheel); err != nil {
				t.Logf("write wheel response: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func cliTestPackManifest() string {
	return `blueprint:
  schema: 1
  version: 0.1.0
  requires_reploy: ">=0.1.0"
  compatibility:
    platforms: [linux/amd64]

environment:
  id: demo
  vars:
    color_env: DEMO_COLOR
  base:
    image: python:3.13-slim
  applications:
    application:
      packages:
        python:
          requirements: [demo-suite]
      executables:
        server:
          source: python
          binary: demo-server
  allow_concurrent: auto
  mounts:
    config:
      target: /conf
      writable: true
      update_policy: preserve
    data:
      target: /data
      writable: true
      update_policy: preserve
  commands:
    serve:
      executable: application.server
      argv: [serve]
    config_check:
      executable: application.server
      trigger: [config, check]
      native_command: true
      forward_flags: [--live]
      argv: [config, check]
    bootstrap_server:
      executable: application.server
      trigger: [bootstrap, server]
      native_command: true
      forward_flags: [--force]
      argv: [bootstrap, demo]
    bootstrap_plugin:
      executable: application.server
      trigger: [bootstrap, plugin]
      native_command: true
      argv: [bootstrap, plugin]
    config_activate:
      executable: application.server
      trigger: [config, activate]
      native_command: true
      argv: [config, activate]
    config_show:
      executable: application.server
      trigger: [config, show]
      native_command: true
      argv: [config, show]
    env_bootstrap:
      executable: application.server
      trigger: [env, bootstrap]
      native_command: true
      argv: [env, bootstrap]
    env_check:
      executable: application.server
      trigger: [env, check]
      native_command: true
      argv: [env, check]
  workload:
    command: serve
    endpoints:
      https:
        scheme: https
        port: 8075
        readiness:
          path: /_health_
          tls_verify: false
  install:
    success:
      lines:
        - "service url: {{ environment.workload.endpoints.https.scheme }}://{{ reploy.workload.endpoints.https.publish.address }}:{{ reploy.workload.endpoints.https.publish.port }}"

docker:
  mounts:
    config:
      extends: environment.mounts.config
      mode: managed-bind
      source: conf
    data:
      extends: environment.mounts.data
      mode: managed-bind
      source: data
  workload:
    endpoints:
      https:
        extends: environment.workload.endpoints.https
        bind:
          address: 0.0.0.0
        publish:
          address: 127.0.0.1
          staging: 18075
          deployed: 8075
`
}
