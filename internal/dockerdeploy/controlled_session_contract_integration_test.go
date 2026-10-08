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

type reployContractFixtureV1 struct {
	Schema            string                    `json:"schema"`
	PlaywrightVersion string                    `json:"playwright_version"`
	ControllerImage   string                    `json:"controller_image"`
	Packages          []reployContractPackageV1 `json:"packages"`
}

type reployContractPackageV1 struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type reployHostResultV1 struct {
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

type reployControllerProofV1 struct {
	Schema                   string `json:"schema"`
	Scenario                 string `json:"scenario"`
	Terminal                 string `json:"terminal"`
	TerminalBytes            int64  `json:"terminal_bytes"`
	BrowserScreenshot        string `json:"browser_screenshot,omitempty"`
	TerminalMarkersVerified  bool   `json:"terminal_markers_verified,omitempty"`
	AttachmentFailed         bool   `json:"attachment_failed,omitempty"`
	OutputFinalizationStatus string `json:"output_finalization_status"`
	OutputFinalizationReason string `json:"output_finalization_reason,omitempty"`
}

func TestReployControlledSessionContractDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("Reploy contract requires a supported Linux host, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	assets := reployContractAssetsV1(t)
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
	defer cancel()

	repositoryRoot := repositoryRootForControllerPackageTestV1(t)
	host := buildReployContractHostV1(t, ctx, repositoryRoot)
	controllerImage := buildReployContractControllerImageV1(t, ctx, repositoryRoot, assets)
	controllerDir, workloadDir := prepareReployContractDeploymentsV1(t, ctx, host, controllerImage)

	t.Run("terminal and browser handoff", func(t *testing.T) {
		outputDir := filepath.Join(t.TempDir(), "output")
		result, stderr, exitCode := runReployContractSessionV1(t, ctx, host, controllerDir, workloadDir, outputDir, "success")
		if exitCode != 0 || !result.OK || result.Error != nil {
			t.Fatalf("public success result = %#v, exit=%d, stderr=%s, controller=%s", result, exitCode, stderr, readReployControllerErrorV1(outputDir))
		}
		assertReployHostResultV1(t, result, "drained", outputDir)
		proof := readReployControllerProofV1(t, outputDir, "success")
		if !proof.TerminalMarkersVerified || proof.BrowserScreenshot == "" || proof.TerminalBytes == 0 {
			t.Fatalf("success controller proof = %#v", proof)
		}
		png, err := os.ReadFile(filepath.Join(outputDir, proof.BrowserScreenshot))
		if err != nil || !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) {
			t.Fatalf("browser screenshot = %d bytes, %v", len(png), err)
		}
		for _, artifact := range []string{proof.Terminal, "terminal.txt", proof.BrowserScreenshot} {
			if info, err := os.Stat(filepath.Join(outputDir, artifact)); err != nil || info.Size() == 0 {
				t.Fatalf("retained success artifact %q = %#v, %v", artifact, info, err)
			}
		}
	})

	t.Run("failed output finalization", func(t *testing.T) {
		outputDir := filepath.Join(t.TempDir(), "output")
		result, stderr, exitCode := runReployContractSessionV1(t, ctx, host, controllerDir, workloadDir, outputDir, "failed-output-finalization")
		if exitCode != 1 || result.OK || result.Error == nil {
			t.Fatalf("public failure result = %#v, exit=%d, stderr=%s, controller=%s", result, exitCode, stderr, readReployControllerErrorV1(outputDir))
		}
		assertReployHostResultV1(t, result, "failed", outputDir)
		if result.SessionResult.WorkloadOutputFinalizationStatus.Reason != "workload PTY output finalization timed out" ||
			!strings.Contains(stderr, "context deadline exceeded") {
			t.Fatalf("failure diagnostics = result %#v stderr %q", result.SessionResult.WorkloadOutputFinalizationStatus, stderr)
		}
		proof := readReployControllerProofV1(t, outputDir, "failed-output-finalization")
		if !proof.AttachmentFailed || proof.TerminalBytes == 0 || proof.OutputFinalizationStatus != "failed" || proof.OutputFinalizationReason == "" {
			t.Fatalf("failed-finalization controller proof = %#v", proof)
		}
		payload, err := os.ReadFile(filepath.Join(outputDir, proof.Terminal))
		if err != nil || len(payload) == 0 || payload[len(payload)-1] != '\n' {
			t.Fatalf("retained partial terminal artifact = %d bytes, %v", len(payload), err)
		}
	})
}

func readReployControllerErrorV1(outputDir string) string {
	payload, err := os.ReadFile(filepath.Join(outputDir, "controller-error.txt"))
	if err != nil {
		return err.Error()
	}
	return string(payload)
}

type reployContractAssets struct {
	fixture        reployContractFixtureV1
	playwright     string
	playwrightCore string
}

func reployContractAssetsV1(t *testing.T) reployContractAssets {
	t.Helper()
	playwright := os.Getenv("REPLOY_PLAYWRIGHT_FIXTURE")
	playwrightCore := os.Getenv("REPLOY_PLAYWRIGHT_CORE_FIXTURE")
	if playwright == "" || playwrightCore == "" {
		t.Fatal("REPLOY_PLAYWRIGHT_FIXTURE and REPLOY_PLAYWRIGHT_CORE_FIXTURE are required pinned fixture files for direct Docker proof")
	}
	root := repositoryRootForControllerPackageTestV1(t)
	payload, err := os.ReadFile(filepath.Join(root, "testdata", "controlled-session", "controlled-session-contract-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture reployContractFixtureV1
	if err := json.Unmarshal(payload, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Schema != "reploy-controlled-session-contract-fixture-v1" || fixture.PlaywrightVersion == "" || fixture.ControllerImage == "" || len(fixture.Packages) != 2 {
		t.Fatalf("invalid Reploy contract fixture = %#v", fixture)
	}
	paths := map[string]string{"playwright": playwright, "playwright-core": playwrightCore}
	for _, item := range fixture.Packages {
		path, ok := paths[item.Name]
		if !ok || item.URL == "" {
			t.Fatalf("unexpected Playwright fixture package = %#v", item)
		}
		assertReployFixtureDigestV1(t, path, item.SHA256)
	}
	return reployContractAssets{fixture: fixture, playwright: playwright, playwrightCore: playwrightCore}
}

func assertReployFixtureDigestV1(t *testing.T, path string, want string) {
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

func buildReployContractHostV1(t *testing.T, ctx context.Context, root string) string {
	t.Helper()
	outdir := t.TempDir()
	command := exec.CommandContext(ctx, filepath.Join(root, "tools", "build_reploy"), "--target", "linux-"+runtime.GOARCH, "--outdir", outdir)
	command.Dir = root
	command.Env = append(os.Environ(), "GOCACHE="+filepath.Join(t.TempDir(), "go-cache"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build packaged contract host: %v\n%s", err, output)
	}
	return filepath.Join(outdir, "linux-"+runtime.GOARCH, "reploy")
}

func buildReployContractControllerImageV1(t *testing.T, ctx context.Context, root string, assets reployContractAssets) string {
	t.Helper()
	if output, err := exec.CommandContext(ctx, "docker", "pull", assets.fixture.ControllerImage).CombinedOutput(); err != nil {
		t.Fatalf("pull pinned Playwright controller image: %v\n%s", err, output)
	}
	workspace := t.TempDir()
	helper := filepath.Join(workspace, "reploy-contract-controller")
	build := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", helper, "./internal/dockerdeploy/testdata/controlled_session_contract_controller")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH, "GOCACHE="+filepath.Join(workspace, "go-cache"))
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build direct Reploy controller: %v\n%s", err, output)
	}
	for source, name := range map[string]string{
		assets.playwright: "playwright.tgz", assets.playwrightCore: "playwright-core.tgz",
	} {
		payload, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, name), payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	browserProof, err := os.ReadFile(filepath.Join(root, "internal", "dockerdeploy", "testdata", "controlled_session_contract_controller", "browser-proof.js"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "browser-proof.js"), browserProof, 0o600); err != nil {
		t.Fatal(err)
	}
	dockerfile := "FROM " + assets.fixture.ControllerImage + "\n" +
		"USER 0:0\n" +
		"COPY --chmod=0555 reploy-contract-controller /usr/local/bin/reploy-contract-controller\n" +
		"COPY playwright.tgz playwright-core.tgz /tmp/\n" +
		"RUN mkdir -p /opt/reploy-contract/node_modules/playwright /opt/reploy-contract/node_modules/playwright-core && " +
		"tar -xzf /tmp/playwright.tgz --strip-components=1 -C /opt/reploy-contract/node_modules/playwright && " +
		"tar -xzf /tmp/playwright-core.tgz --strip-components=1 -C /opt/reploy-contract/node_modules/playwright-core && " +
		"rm /tmp/playwright.tgz /tmp/playwright-core.tgz\n" +
		"COPY --chmod=0444 browser-proof.js /opt/reploy-contract/browser-proof.js\n"
	if err := os.WriteFile(filepath.Join(workspace, "Dockerfile"), []byte(dockerfile), 0o600); err != nil {
		t.Fatal(err)
	}
	image := uniqueDockerIntegrationName("reploy-reploy-contract-controller")
	command := exec.CommandContext(ctx, "docker", "build", "--pull=false", "--tag", image, workspace)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build Reploy controller fixture image: %v\n%s", err, output)
	}
	imageID := strings.TrimSpace(runDockerIntegration(t, ctx, "image", "inspect", "--format", "{{.Id}}", image))
	t.Cleanup(func() {
		if output, err := exec.CommandContext(context.Background(), "docker", "image", "rm", image).CombinedOutput(); err != nil {
			t.Errorf("remove Reploy controller fixture image: %v\n%s", err, output)
		}
	})
	return imageID
}

func prepareReployContractDeploymentsV1(t *testing.T, ctx context.Context, host string, controllerImage string) (string, string) {
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
  id: reploy-contract-controller
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
    contract:
      executable: controller.shell
      trigger: [contract]
      native_command: true
      argv: [-c, 'exec /usr/local/bin/reploy-contract-controller "$@"', contract]
docker: {}
`, platform, controllerImage)
	workload := fmt.Sprintf(`blueprint:
  schema: 1
  version: 0.1.0
  compatibility:
    platforms: [%s]
environment:
  id: reploy-contract-workload
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
		runReployHostCommandV1(t, ctx, host, "stage", "--dir", deployment.dir, "--platform", platform, "file:"+deployment.blueprint)
		runReployHostCommandV1(t, ctx, host, "build", "--dir", deployment.dir)
	}
	return controllerDir, workloadDir
}

func runReployHostCommandV1(t *testing.T, ctx context.Context, host string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(ctx, host, args...)
	command.Env = append(os.Environ(), "NO_UPDATE_NOTIFIER=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run %s %s: %v\n%s", host, strings.Join(args, " "), err, output)
	}
	return string(output)
}

func runReployContractSessionV1(
	t *testing.T,
	ctx context.Context,
	host string,
	controllerDir string,
	workloadDir string,
	outputDir string,
	scenario string,
) (reployHostResultV1, string, int) {
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
		"--", "contract", scenario,
	)
	command.Env = append(os.Environ(), "NO_UPDATE_NOTIFIER=1")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	channelRoot := filepath.Join(workloadDir, privateRuntimeMetadataDirectoryName, "sessions")
	unrelated := filepath.Join(channelRoot, "unrelated-proof")
	if err := os.MkdirAll(unrelated, 0o700); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(unrelated)
	if err := command.Start(); err != nil {
		t.Fatalf("start public controlled-session command: %v", err)
	}
	defer func() {
		if command.ProcessState == nil {
			_ = command.Process.Signal(syscall.SIGCONT)
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	liveRunID := waitForReployOwnedChannelV1(t, ctx, channelRoot)
	if scenario == "failed-output-finalization" {
		waitForReployControllerLogV1(t, ctx, liveRunID, "REPLOY-CONTRACT-TERMINATING", 30*time.Second)
		if signalErr := command.Process.Signal(syscall.SIGSTOP); signalErr != nil {
			t.Fatalf("suspend public controlled-session host: %v", signalErr)
		}
		time.Sleep(31 * time.Second)
		if signalErr := command.Process.Signal(syscall.SIGCONT); signalErr != nil {
			t.Fatalf("resume public controlled-session host: %v", signalErr)
		}
	}
	err := command.Wait()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run public controlled-session command: %v\n%s", err, stderr.String())
		}
		exitCode = exitErr.ExitCode()
	}
	var result reployHostResultV1
	if decodeErr := json.Unmarshal(stdout.Bytes(), &result); decodeErr != nil {
		t.Fatalf("decode public controlled-session result: %v\nstdout=%s\nstderr=%s", decodeErr, stdout.String(), stderr.String())
	}
	if result.Schema != "reploy-controlled-session-run-result-v1" {
		t.Fatalf("public result schema = %q", result.Schema)
	}
	for _, args := range [][]string{
		{"ps", "-a", "--filter", "label=io.reploy.session.live-run=" + liveRunID, "--format", "{{.ID}}"},
		{"network", "ls", "--filter", "label=io.reploy.session.live-run=" + liveRunID, "--format", "{{.ID}}"},
	} {
		if resources := strings.TrimSpace(runDockerIntegration(t, ctx, args...)); resources != "" {
			t.Fatalf("owned resources remain for %s: %s", liveRunID, resources)
		}
	}
	if _, err := os.Stat(filepath.Join(channelRoot, liveRunID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned session channel remains: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("cleanup removed unrelated session directory: %v", err)
	}
	return result, stderr.String(), exitCode
}

// Observe this test's private deployment rather than selecting another live
// controller on a shared Docker engine. The directory is created before launch.
func waitForReployOwnedChannelV1(t *testing.T, ctx context.Context, root string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() && entry.Name() != "unrelated-proof" {
				return entry.Name()
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out observing owned session channel")
	return ""
}

func reployControllerContainersV1(t *testing.T, ctx context.Context, liveRunID string) []string {
	t.Helper()
	command := exec.CommandContext(ctx, "docker", "ps", "--filter", "label=io.reploy.session.role=controller", "--filter", "label=io.reploy.session.live-run="+liveRunID, "--format", "{{.ID}}")
	payload, err := command.Output()
	if err != nil {
		t.Fatalf("list controlled-session controller containers: %v", err)
	}
	return strings.Fields(string(payload))
}

func waitForReployControllerLogV1(t *testing.T, ctx context.Context, liveRunID string, marker string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, id := range reployControllerContainersV1(t, ctx, liveRunID) {
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

func assertReployHostResultV1(t *testing.T, result reployHostResultV1, outputStatus string, outputDir string) {
	t.Helper()
	if result.SessionResult == nil || result.ResultDelivered == nil || !*result.ResultDelivered ||
		result.ResultAcknowledged == nil || !*result.ResultAcknowledged || result.ControllerOutput == nil ||
		result.ControllerOutput.Kind != "directory-retained" ||
		result.SessionResult.WorkloadOutputFinalizationStatus.Kind != outputStatus ||
		result.SessionResult.ControllerFinalizationStatus.Kind != "completed" ||
		result.SessionResult.CleanupStatus.Kind != "succeeded" {
		t.Fatalf("public controlled-session result invariants = %#v, controller=%s", result, readReployControllerErrorV1(outputDir))
	}
}

func readReployControllerProofV1(t *testing.T, outputDir string, scenario string) reployControllerProofV1 {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(outputDir, scenario+"-proof.json"))
	if err != nil {
		t.Fatal(err)
	}
	var proof reployControllerProofV1
	if err := json.Unmarshal(payload, &proof); err != nil {
		t.Fatal(err)
	}
	if proof.Schema != "reploy-controlled-session-contract-proof-v1" || proof.Scenario != scenario {
		t.Fatalf("controller proof identity = %#v", proof)
	}
	return proof
}
