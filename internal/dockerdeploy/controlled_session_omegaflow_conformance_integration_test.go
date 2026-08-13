//go:build linux

package dockerdeploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

type omegaFlowConformanceFixtureV1 struct {
	Schema            string                          `json:"schema"`
	PlaywrightVersion string                          `json:"playwright_version"`
	ControllerImage   string                          `json:"controller_image"`
	Packages          []omegaFlowConformancePackageV1 `json:"packages"`
}

type omegaFlowConformancePackageV1 struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type omegaFlowHostResultV1 struct {
	Schema             string  `json:"schema"`
	OK                 bool    `json:"ok"`
	Error              *string `json:"error"`
	ResultDelivered    *bool   `json:"result_delivered"`
	ResultAcknowledged *bool   `json:"result_acknowledged"`
	SessionResult      *struct {
		WorkloadOutputFinalizationStatus struct {
			Kind   string `json:"kind"`
			Reason string `json:"reason,omitempty"`
		} `json:"workload_output_finalization_status"`
		ControllerFinalizationStatus struct {
			Kind string `json:"kind"`
		} `json:"controller_finalization_status"`
		CleanupStatus struct {
			Kind string `json:"kind"`
		} `json:"cleanup_status"`
	} `json:"session_result"`
	ControllerOutput *struct {
		Kind string `json:"kind"`
	} `json:"controller_output"`
}

type omegaFlowControllerProofV1 struct {
	Schema                   string `json:"schema"`
	Scenario                 string `json:"scenario"`
	Cast                     string `json:"cast"`
	CastBytes                int64  `json:"cast_bytes"`
	BrowserScreenshot        string `json:"browser_screenshot,omitempty"`
	TerminalMarkersVerified  bool   `json:"terminal_markers_verified,omitempty"`
	RecorderFailed           bool   `json:"recorder_failed,omitempty"`
	OutputFinalizationStatus string `json:"output_finalization_status"`
	OutputFinalizationReason string `json:"output_finalization_reason,omitempty"`
}

func TestOmegaFlowControlledSessionConformanceDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("OmegaFlow conformance requires a supported Linux host, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	assets := omegaFlowConformanceAssetsV1(t)
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
	defer cancel()

	repositoryRoot := repositoryRootForControllerPackageTestV1(t)
	host := buildOmegaFlowConformanceHostV1(t, ctx, repositoryRoot)
	controllerImage := buildOmegaFlowConformanceControllerImageV1(t, ctx, repositoryRoot, assets)
	controllerDir, workloadDir := prepareOmegaFlowConformanceDeploymentsV1(t, ctx, host, controllerImage)

	t.Run("terminal and browser handoff", func(t *testing.T) {
		outputDir := filepath.Join(t.TempDir(), "output")
		result, stderr, exitCode := runOmegaFlowConformanceSessionV1(t, ctx, host, controllerDir, workloadDir, outputDir, "success")
		if exitCode != 0 || !result.OK || result.Error != nil {
			t.Fatalf("public success result = %#v, exit=%d, stderr=%s, controller=%s", result, exitCode, stderr, readOmegaFlowControllerErrorV1(outputDir))
		}
		assertOmegaFlowHostResultV1(t, result, "drained", outputDir)
		proof := readOmegaFlowControllerProofV1(t, outputDir, "success")
		if !proof.TerminalMarkersVerified || proof.BrowserScreenshot == "" || proof.CastBytes == 0 {
			t.Fatalf("success controller proof = %#v", proof)
		}
		png, err := os.ReadFile(filepath.Join(outputDir, proof.BrowserScreenshot))
		if err != nil || !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) {
			t.Fatalf("browser screenshot = %d bytes, %v", len(png), err)
		}
		for _, artifact := range []string{proof.Cast, "terminal.txt", proof.BrowserScreenshot} {
			if info, err := os.Stat(filepath.Join(outputDir, artifact)); err != nil || info.Size() == 0 {
				t.Fatalf("retained success artifact %q = %#v, %v", artifact, info, err)
			}
		}
	})

	t.Run("failed output finalization", func(t *testing.T) {
		outputDir := filepath.Join(t.TempDir(), "output")
		result, stderr, exitCode := runOmegaFlowConformanceSessionV1(t, ctx, host, controllerDir, workloadDir, outputDir, "failed-output-finalization")
		if exitCode != 1 || result.OK || result.Error == nil {
			t.Fatalf("public failure result = %#v, exit=%d, stderr=%s, controller=%s", result, exitCode, stderr, readOmegaFlowControllerErrorV1(outputDir))
		}
		assertOmegaFlowHostResultV1(t, result, "failed", outputDir)
		if result.SessionResult.WorkloadOutputFinalizationStatus.Reason != "workload PTY output finalization timed out" ||
			!strings.Contains(stderr, "context deadline exceeded") {
			t.Fatalf("failure diagnostics = result %#v stderr %q", result.SessionResult.WorkloadOutputFinalizationStatus, stderr)
		}
		proof := readOmegaFlowControllerProofV1(t, outputDir, "failed-output-finalization")
		if !proof.RecorderFailed || proof.CastBytes == 0 || proof.OutputFinalizationStatus != "failed" || proof.OutputFinalizationReason == "" {
			t.Fatalf("failed-finalization controller proof = %#v", proof)
		}
		payload, err := os.ReadFile(filepath.Join(outputDir, proof.Cast))
		if err != nil || len(payload) == 0 || payload[len(payload)-1] != '\n' {
			t.Fatalf("retained partial cast = %d bytes, %v", len(payload), err)
		}
	})
}

func readOmegaFlowControllerErrorV1(outputDir string) string {
	payload, err := os.ReadFile(filepath.Join(outputDir, "controller-error.txt"))
	if err != nil {
		return err.Error()
	}
	return string(payload)
}

type omegaFlowConformanceAssets struct {
	fixture        omegaFlowConformanceFixtureV1
	asciinema      string
	playwright     string
	playwrightCore string
}

func omegaFlowConformanceAssetsV1(t *testing.T) omegaFlowConformanceAssets {
	t.Helper()
	asciinema := os.Getenv("REPLOY_ASCIINEMA_FIXTURE")
	playwright := os.Getenv("REPLOY_PLAYWRIGHT_FIXTURE")
	playwrightCore := os.Getenv("REPLOY_PLAYWRIGHT_CORE_FIXTURE")
	if asciinema == "" || playwright == "" || playwrightCore == "" {
		t.Skip("set REPLOY_ASCIINEMA_FIXTURE, REPLOY_PLAYWRIGHT_FIXTURE, and REPLOY_PLAYWRIGHT_CORE_FIXTURE to pinned fixture files")
	}
	root := repositoryRootForControllerPackageTestV1(t)
	payload, err := os.ReadFile(filepath.Join(root, "testdata", "controlled-session", "omegaflow-conformance-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture omegaFlowConformanceFixtureV1
	if err := json.Unmarshal(payload, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Schema != "reploy-omegaflow-conformance-fixture-v1" || fixture.PlaywrightVersion == "" || fixture.ControllerImage == "" || len(fixture.Packages) != 2 {
		t.Fatalf("invalid OmegaFlow conformance fixture = %#v", fixture)
	}
	paths := map[string]string{"playwright": playwright, "playwright-core": playwrightCore}
	for _, item := range fixture.Packages {
		path, ok := paths[item.Name]
		if !ok || item.URL == "" {
			t.Fatalf("unexpected Playwright fixture package = %#v", item)
		}
		assertOmegaFlowFixtureDigestV1(t, path, item.SHA256)
	}
	asciinemaMetadataPath := filepath.Join(root, "testdata", "controlled-session", "asciinema-v3-linux-"+runtime.GOARCH+".json")
	asciinemaMetadata, err := os.ReadFile(asciinemaMetadataPath)
	if err != nil {
		t.Fatal(err)
	}
	var recorder struct {
		Version string `json:"version"`
		SHA256  string `json:"sha256"`
	}
	if err := json.Unmarshal(asciinemaMetadata, &recorder); err != nil {
		t.Fatal(err)
	}
	assertOmegaFlowFixtureDigestV1(t, asciinema, recorder.SHA256)
	version, err := exec.Command(asciinema, "--version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(version)) != "asciinema "+recorder.Version {
		t.Fatalf("asciinema fixture version = %q, %v", version, err)
	}
	return omegaFlowConformanceAssets{fixture: fixture, asciinema: asciinema, playwright: playwright, playwrightCore: playwrightCore}
}

func assertOmegaFlowFixtureDigestV1(t *testing.T, path string, want string) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	if got := hex.EncodeToString(digest[:]); got != want {
		t.Fatalf("fixture %s SHA-256 = %s, want %s", path, got, want)
	}
}

func buildOmegaFlowConformanceHostV1(t *testing.T, ctx context.Context, root string) string {
	t.Helper()
	outdir := t.TempDir()
	command := exec.CommandContext(ctx, filepath.Join(root, "tools", "build_reploy"), "--target", "linux-"+runtime.GOARCH, "--outdir", outdir)
	command.Dir = root
	command.Env = append(os.Environ(), "GOCACHE="+filepath.Join(t.TempDir(), "go-cache"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build packaged conformance host: %v\n%s", err, output)
	}
	return filepath.Join(outdir, "linux-"+runtime.GOARCH, "reploy")
}

func buildOmegaFlowConformanceControllerImageV1(t *testing.T, ctx context.Context, root string, assets omegaFlowConformanceAssets) string {
	t.Helper()
	if output, err := exec.CommandContext(ctx, "docker", "pull", assets.fixture.ControllerImage).CombinedOutput(); err != nil {
		t.Fatalf("pull pinned Playwright controller image: %v\n%s", err, output)
	}
	workspace := t.TempDir()
	helper := filepath.Join(workspace, "omegaflow-conformance-controller")
	build := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", helper, "./internal/dockerdeploy/testdata/omegaflow_conformance_controller")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH, "GOCACHE="+filepath.Join(workspace, "go-cache"))
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build OmegaFlow-shaped controller: %v\n%s", err, output)
	}
	for source, name := range map[string]string{
		assets.asciinema: "asciinema", assets.playwright: "playwright.tgz", assets.playwrightCore: "playwright-core.tgz",
	} {
		payload, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, name), payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	browserProof, err := os.ReadFile(filepath.Join(root, "internal", "dockerdeploy", "testdata", "omegaflow_conformance_controller", "browser-proof.js"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "browser-proof.js"), browserProof, 0o600); err != nil {
		t.Fatal(err)
	}
	dockerfile := "FROM " + assets.fixture.ControllerImage + "\n" +
		"USER 0:0\n" +
		"COPY --chmod=0555 omegaflow-conformance-controller /usr/local/bin/omegaflow-conformance-controller\n" +
		"COPY --chmod=0555 asciinema /usr/local/bin/asciinema\n" +
		"COPY playwright.tgz playwright-core.tgz /tmp/\n" +
		"RUN mkdir -p /opt/omegaflow/node_modules/playwright /opt/omegaflow/node_modules/playwright-core && " +
		"tar -xzf /tmp/playwright.tgz --strip-components=1 -C /opt/omegaflow/node_modules/playwright && " +
		"tar -xzf /tmp/playwright-core.tgz --strip-components=1 -C /opt/omegaflow/node_modules/playwright-core && " +
		"rm /tmp/playwright.tgz /tmp/playwright-core.tgz\n" +
		"COPY --chmod=0444 browser-proof.js /opt/omegaflow/browser-proof.js\n"
	if err := os.WriteFile(filepath.Join(workspace, "Dockerfile"), []byte(dockerfile), 0o600); err != nil {
		t.Fatal(err)
	}
	image := uniqueDockerIntegrationName("reploy-omegaflow-controller")
	command := exec.CommandContext(ctx, "docker", "build", "--pull=false", "--tag", image, workspace)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build OmegaFlow controller fixture image: %v\n%s", err, output)
	}
	imageID := strings.TrimSpace(runDockerIntegration(t, ctx, "image", "inspect", "--format", "{{.Id}}", image))
	t.Cleanup(func() {
		if output, err := exec.CommandContext(context.Background(), "docker", "image", "rm", image).CombinedOutput(); err != nil {
			t.Errorf("remove OmegaFlow controller fixture image: %v\n%s", err, output)
		}
	})
	return imageID
}

func prepareOmegaFlowConformanceDeploymentsV1(t *testing.T, ctx context.Context, host string, controllerImage string) (string, string) {
	t.Helper()
	root := shortControlledSessionChannelTestDirectoryV1(t)
	controllerBlueprint := filepath.Join(root, "controller.blueprint.yaml")
	workloadBlueprint := filepath.Join(root, "workload.blueprint.yaml")
	platform := "linux/" + runtime.GOARCH
	controller := fmt.Sprintf(`blueprint:
  schema: 1
  version: 0.1.0
  compatibility:
    platforms: [%s]
environment:
  id: omegaflow-conformance-controller
  base:
    image: %s
  applications:
    controller:
      packages:
        os:
          - package: bash
            exports:
              shell:
                executable: /usr/bin/bash
          - util-linux
      executables:
        shell:
          source: os
          binary: shell
  commands:
    conformance:
      executable: controller.shell
      trigger: [conformance]
      native_command: true
      argv: [-c, 'exec /usr/local/bin/omegaflow-conformance-controller "$@"', conformance]
docker: {}
`, platform, controllerImage)
	workload := fmt.Sprintf(`blueprint:
  schema: 1
  version: 0.1.0
  compatibility:
    platforms: [%s]
environment:
  id: omegaflow-conformance-workload
  base:
    image: python:3.13-slim
  applications:
    shell:
      packages:
        os:
          - package: bash
            exports:
              shell:
                executable: /usr/bin/bash
      executables:
        shell:
          source: os
          binary: shell
  commands:
    shell:
      executable: shell.shell
  workload:
    command: shell
    endpoints:
      web:
        scheme: http
        port: 8080
docker:
  workload:
    endpoints:
      web:
        extends: environment.workload.endpoints.web
        bind: {address: 0.0.0.0}
        publish: {address: 127.0.0.1, staging: 18080, deployed: 18081}
`, platform)
	if err := os.WriteFile(controllerBlueprint, []byte(controller), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workloadBlueprint, []byte(workload), 0o600); err != nil {
		t.Fatal(err)
	}
	controllerDir := filepath.Join(root, "controller")
	workloadDir := filepath.Join(root, "workload")
	for _, deployment := range []struct {
		dir       string
		blueprint string
	}{
		{dir: controllerDir, blueprint: controllerBlueprint},
		{dir: workloadDir, blueprint: workloadBlueprint},
	} {
		runOmegaFlowHostCommandV1(t, ctx, host, "stage", "--dir", deployment.dir, "--platform", platform, "file:"+deployment.blueprint)
		runOmegaFlowHostCommandV1(t, ctx, host, "build", "--dir", deployment.dir)
	}
	return controllerDir, workloadDir
}

func runOmegaFlowHostCommandV1(t *testing.T, ctx context.Context, host string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(ctx, host, args...)
	command.Env = append(os.Environ(), "NO_UPDATE_NOTIFIER=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run %s %s: %v\n%s", host, strings.Join(args, " "), err, output)
	}
	return string(output)
}

func runOmegaFlowConformanceSessionV1(
	t *testing.T,
	ctx context.Context,
	host string,
	controllerDir string,
	workloadDir string,
	outputDir string,
	scenario string,
) (omegaFlowHostResultV1, string, int) {
	t.Helper()
	command := exec.CommandContext(ctx, host,
		"controlled-session", "run",
		"--controller-dir", controllerDir,
		"--workload-dir", workloadDir,
		"--endpoint", "web",
		"--columns", "80", "--rows", "24",
		"--output-dir", outputDir,
		"--controller-finalization-timeout", "1m",
		"--result-acknowledgement-timeout", "15s",
		"--cleanup-timeout", "30s",
		"--", "conformance", scenario,
	)
	command.Env = append(os.Environ(), "NO_UPDATE_NOTIFIER=1")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	var err error
	if scenario == "failed-output-finalization" {
		before := omegaFlowControllerContainersV1(t, ctx)
		if err = command.Start(); err == nil {
			resumed := false
			defer func() {
				if !resumed && command.Process != nil {
					_ = command.Process.Signal(syscall.SIGCONT)
				}
			}()
			waitForOmegaFlowControllerLogV1(t, ctx, before, "OMEGAFLOW-CONFORMANCE-TERMINATING", 30*time.Second)
			if signalErr := command.Process.Signal(syscall.SIGSTOP); signalErr != nil {
				t.Fatalf("suspend public controlled-session host: %v", signalErr)
			}
			time.Sleep(31 * time.Second)
			if signalErr := command.Process.Signal(syscall.SIGCONT); signalErr != nil {
				t.Fatalf("resume public controlled-session host: %v", signalErr)
			}
			resumed = true
			err = command.Wait()
		}
	} else {
		err = command.Run()
	}
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run public controlled-session command: %v\n%s", err, stderr.String())
		}
		exitCode = exitErr.ExitCode()
	}
	var result omegaFlowHostResultV1
	if decodeErr := json.Unmarshal(stdout.Bytes(), &result); decodeErr != nil {
		t.Fatalf("decode public controlled-session result: %v\nstdout=%s\nstderr=%s", decodeErr, stdout.String(), stderr.String())
	}
	if result.Schema != "reploy-controlled-session-run-result-v1" {
		t.Fatalf("public result schema = %q", result.Schema)
	}
	return result, stderr.String(), exitCode
}

func omegaFlowControllerContainersV1(t *testing.T, ctx context.Context) map[string]bool {
	t.Helper()
	command := exec.CommandContext(ctx, "docker", "ps", "--filter", "label=io.reploy.session.role=controller", "--format", "{{.ID}}")
	payload, err := command.Output()
	if err != nil {
		t.Fatalf("list controlled-session controller containers: %v", err)
	}
	result := map[string]bool{}
	for _, id := range strings.Fields(string(payload)) {
		result[id] = true
	}
	return result
}

func waitForOmegaFlowControllerLogV1(t *testing.T, ctx context.Context, before map[string]bool, marker string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for id := range omegaFlowControllerContainersV1(t, ctx) {
			if before[id] {
				continue
			}
			command := exec.CommandContext(ctx, "docker", "logs", id)
			payload, err := command.CombinedOutput()
			if err == nil && bytes.Contains(payload, []byte(marker)) {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for controlled-session controller log marker %q", marker)
}

func assertOmegaFlowHostResultV1(t *testing.T, result omegaFlowHostResultV1, outputStatus string, outputDir string) {
	t.Helper()
	if result.SessionResult == nil || result.ResultDelivered == nil || !*result.ResultDelivered ||
		result.ResultAcknowledged == nil || !*result.ResultAcknowledged || result.ControllerOutput == nil ||
		result.ControllerOutput.Kind != "directory-retained" ||
		result.SessionResult.WorkloadOutputFinalizationStatus.Kind != outputStatus ||
		result.SessionResult.ControllerFinalizationStatus.Kind != "completed" ||
		result.SessionResult.CleanupStatus.Kind != "succeeded" {
		t.Fatalf("public controlled-session result invariants = %#v, controller=%s", result, readOmegaFlowControllerErrorV1(outputDir))
	}
}

func readOmegaFlowControllerProofV1(t *testing.T, outputDir string, scenario string) omegaFlowControllerProofV1 {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(outputDir, scenario+"-proof.json"))
	if err != nil {
		t.Fatal(err)
	}
	var proof omegaFlowControllerProofV1
	if err := json.Unmarshal(payload, &proof); err != nil {
		t.Fatal(err)
	}
	if proof.Schema != "reploy-omegaflow-conformance-proof-v1" || proof.Scenario != scenario {
		t.Fatalf("controller proof identity = %#v", proof)
	}
	return proof
}
