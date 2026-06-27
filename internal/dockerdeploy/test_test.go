package dockerdeploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

func TestServerURLFromDockerEnv(t *testing.T) {
	dir := makeTestDeploymentWithDockerEnv(t, "REPLOY_PUBLIC_SCHEME=https\nREPLOY_HOST_BIND=0.0.0.0\nREPLOY_HOST_PORT=18075\n")
	serverURL, err := ServerURL(dir)
	if err != nil {
		t.Fatal(err)
	}
	if serverURL.String() != "https://127.0.0.1:18075/_health_" {
		t.Fatalf("server URL = %q", serverURL)
	}
}

func TestInstallSuccessLinesResolveAppVars(t *testing.T) {
	manifest := strings.Replace(testPackManifest(), "  health:\n", `  install:
    success:
      vars:
        server_url:
          app: [config, show, --resolve, --package, demo.server.public.base_url, --value]
      lines:
        - "server url: ${server_url}"
        - "client command: demo-client --url=${server_url} info"
  health:
`, 1)
	packDir := makeTestPackWithManifest(t, manifest)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	oldRunInstallAppCommand := runInstallAppCommand
	t.Cleanup(func() {
		runInstallAppCommand = oldRunInstallAppCommand
	})
	runInstallAppCommand = func(dir string, args []string, stdout io.Writer, stderr io.Writer) error {
		if dir != deployDir {
			t.Fatalf("dir = %q", dir)
		}
		if got := strings.Join(args, " "); got != "config show --resolve --package demo.server.public.base_url --value" {
			t.Fatalf("args = %q", got)
		}
		fmt.Fprintln(stdout, "https://arbiter.example.com")
		return nil
	}

	lines, err := InstallSuccessLines(deployDir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"server url: https://arbiter.example.com",
		"client command: demo-client --url=https://arbiter.example.com info",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("success lines = %#v, want %#v", lines, want)
	}
}

func TestInstallSuccessLinesResolveServerURLVar(t *testing.T) {
	manifest := strings.Replace(testPackManifest(), "  health:\n", `  install:
    success:
      vars:
        server_url:
          server_url: true
      lines:
        - "server url: ${server_url}"
        - "client command: demo-client --url=${server_url} info"
  health:
`, 1)
	packDir := makeTestPackWithManifest(t, manifest)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{
		"REPLOY_PUBLIC_SCHEME": "https",
		"REPLOY_HOST_BIND":     "127.0.0.1",
		"REPLOY_HOST_PORT":     "8083",
	}); err != nil {
		t.Fatal(err)
	}

	lines, err := InstallSuccessLines(deployDir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"server url: https://127.0.0.1:8083",
		"client command: demo-client --url=https://127.0.0.1:8083 info",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("success lines = %#v, want %#v", lines, want)
	}
}

func TestInstallSuccessLinesReportsAppVarFailures(t *testing.T) {
	manifest := strings.Replace(testPackManifest(), "  health:\n", `  install:
    success:
      vars:
        server_url:
          app: [config, show, --value]
      lines:
        - "server url: ${server_url}"
  health:
`, 1)
	packDir := makeTestPackWithManifest(t, manifest)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	oldRunInstallAppCommand := runInstallAppCommand
	t.Cleanup(func() {
		runInstallAppCommand = oldRunInstallAppCommand
	})
	runInstallAppCommand = func(dir string, args []string, stdout io.Writer, stderr io.Writer) error {
		fmt.Fprintln(stderr, "config failed")
		return errors.New("exit status 1")
	}

	_, err = InstallSuccessLines(deployDir)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{
		"success variable server_url",
		"installed success app output: exit status 1",
		"config failed",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q:\n%v", want, err)
		}
	}
}

func TestInstallSuccessLinesRejectsMultilineAppVarOutput(t *testing.T) {
	manifest := strings.Replace(testPackManifest(), "  health:\n", `  install:
    success:
      vars:
        server_url:
          app: [config, show, --value]
      lines:
        - "server url: ${server_url}"
  health:
`, 1)
	packDir := makeTestPackWithManifest(t, manifest)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	oldRunInstallAppCommand := runInstallAppCommand
	t.Cleanup(func() {
		runInstallAppCommand = oldRunInstallAppCommand
	})
	runInstallAppCommand = func(dir string, args []string, stdout io.Writer, stderr io.Writer) error {
		fmt.Fprintln(stdout, "https://arbiter.example.com")
		fmt.Fprintln(stdout, "extra output")
		return nil
	}

	_, err = InstallSuccessLines(deployDir)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "success variable server_url: app output must be a single line") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServerURLRequiresPackHealthProbe(t *testing.T) {
	packDir := makeTestPackWithManifest(t, strings.ReplaceAll(testPackManifest(), "  health:\n    scheme_env: REPLOY_PUBLIC_SCHEME\n    host_env: REPLOY_HOST_BIND\n    port_env: REPLOY_HOST_PORT\n    default_scheme: https\n    default_host: 127.0.0.1\n    default_port: \"18075\"\n    path: /_health_\n", ""))
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	_, err = ServerURL(deployDir)
	if err == nil || !strings.Contains(err.Error(), "blueprint does not declare docker.health.path") {
		t.Fatalf("error = %v", err)
	}
}

func TestTestServerHealth(t *testing.T) {
	restore := stubTestCommandOutput([]byte(`[{"State":"running"}]`), nil)
	defer restore()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listener unavailable in this environment: %v", err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/_health_" {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write([]byte("ok"))
	}))
	server.Listener = listener
	server.StartTLS()
	defer server.Close()

	host, port := splitHostPortForTest(t, server.Listener.Addr().String())
	env := "REPLOY_PUBLIC_SCHEME=https\nREPLOY_HOST_BIND=" + host + "\nREPLOY_HOST_PORT=" + port + "\n"
	dir := makeTestDeploymentWithDockerEnv(t, env)

	var stdout strings.Builder
	if err := TestServer(TestOptions{Dir: dir, Timeout: time.Second, Stdout: &stdout}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "ok: https://") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestTestServerWaitsWhenServiceIsRunning(t *testing.T) {
	restore := stubTestCommandOutput([]byte(`[{"State":"running"}]`), nil)
	defer restore()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listener unavailable in this environment: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	host, port := splitHostPortForTest(t, address)
	env := "REPLOY_PUBLIC_SCHEME=https\nREPLOY_HOST_BIND=" + host + "\nREPLOY_HOST_PORT=" + port + "\n"
	dir := makeTestDeploymentWithDockerEnv(t, env)

	start := time.Now()
	err = TestServer(TestOptions{Dir: dir, Timeout: 1200 * time.Millisecond})
	if err == nil {
		t.Fatal("expected connection failure")
	}
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Fatalf("test did not wait: elapsed=%s err=%v", elapsed, err)
	}
}

func TestTestServerWaitFailsImmediatelyWhenServiceIsDown(t *testing.T) {
	restore := stubTestCommandOutput([]byte(`[{"State":"exited"}]`), nil)
	defer restore()

	dir := makeTestDeploymentWithDockerEnv(t, "REPLOY_PUBLIC_SCHEME=https\nREPLOY_HOST_BIND=127.0.0.1\nREPLOY_HOST_PORT=1\n")
	start := time.Now()
	err := TestServer(TestOptions{Dir: dir, Timeout: time.Minute})
	if err == nil {
		t.Fatal("expected service state failure")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("test did not fail fast: elapsed=%s err=%v", elapsed, err)
	}
	if !strings.Contains(err.Error(), "service is not running") {
		t.Fatalf("error = %v", err)
	}
}

func TestTestServerWaitFailsImmediatelyWhenServiceIsRestarting(t *testing.T) {
	restore := stubTestCommandOutput([]byte(`[{"State":"restarting"}]`), nil)
	defer restore()

	dir := makeTestDeploymentWithDockerEnv(t, "REPLOY_PUBLIC_SCHEME=https\nREPLOY_HOST_BIND=127.0.0.1\nREPLOY_HOST_PORT=1\n")
	start := time.Now()
	err := TestServer(TestOptions{Dir: dir, Timeout: time.Minute})
	if err == nil {
		t.Fatal("expected service state failure")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("test did not fail fast: elapsed=%s err=%v", elapsed, err)
	}
	if !strings.Contains(err.Error(), "service is restarting") || !strings.Contains(err.Error(), "reploy logs") {
		t.Fatalf("error = %v", err)
	}
}

func TestTestServerStopsWaitingWhenServiceLeavesRunningState(t *testing.T) {
	restore := stubTestCommandOutputSequence(t, [][]byte{
		[]byte(`[{"State":"running"}]`),
		[]byte(`[{"State":"restarting"}]`),
	})
	defer restore()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listener unavailable in this environment: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	host, port := splitHostPortForTest(t, address)
	env := "REPLOY_PUBLIC_SCHEME=https\nREPLOY_HOST_BIND=" + host + "\nREPLOY_HOST_PORT=" + port + "\n"
	dir := makeTestDeploymentWithDockerEnv(t, env)

	start := time.Now()
	err = TestServer(TestOptions{Dir: dir, Timeout: time.Minute})
	if err == nil {
		t.Fatal("expected service state failure")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("test did not stop soon after service state changed: elapsed=%s err=%v", elapsed, err)
	}
	if !strings.Contains(err.Error(), "service is restarting") {
		t.Fatalf("error = %v", err)
	}
}

func TestTestServerWaitFailsImmediatelyWhenServiceIsAbsent(t *testing.T) {
	restore := stubTestCommandOutput([]byte(`[]`), nil)
	defer restore()

	dir := makeTestDeploymentWithDockerEnv(t, "REPLOY_PUBLIC_SCHEME=https\nREPLOY_HOST_BIND=127.0.0.1\nREPLOY_HOST_PORT=1\n")
	start := time.Now()
	err := TestServer(TestOptions{Dir: dir, Timeout: time.Minute})
	if err == nil {
		t.Fatal("expected service state failure")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("test did not fail fast: elapsed=%s err=%v", elapsed, err)
	}
	if !strings.Contains(err.Error(), "service is not started") {
		t.Fatalf("error = %v", err)
	}
}

func TestTestServerComposeFailureIncludesCommandOutput(t *testing.T) {
	restore := stubTestCommandOutput([]byte("permission denied while trying to connect to docker\n"), errors.New("exit status 1"))
	defer restore()

	dir := makeTestDeploymentWithDockerEnv(t, "REPLOY_PUBLIC_SCHEME=https\nREPLOY_HOST_BIND=127.0.0.1\nREPLOY_HOST_PORT=1\n")
	err := TestServer(TestOptions{Dir: dir, Timeout: time.Minute})
	if err == nil {
		t.Fatal("expected compose failure")
	}
	for _, want := range []string{
		"docker compose ps: exit status 1",
		"permission denied while trying to connect to docker",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q:\n%v", want, err)
		}
	}
}

func TestCommandOutputKeepsStderrOutOfSuccessfulOutput(t *testing.T) {
	output, err := commandOutput(CommandSpec{
		Name: "sh",
		Args: []string{"-c", "printf stdout; printf stderr >&2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "stdout" {
		t.Fatalf("output = %q", output)
	}
}

func TestCommandOutputIncludesStderrOnFailure(t *testing.T) {
	output, err := commandOutput(CommandSpec{
		Name: "sh",
		Args: []string{"-c", "printf stdout; printf stderr >&2; exit 7"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"stdout", "stderr"} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("output missing %q: %q", want, output)
		}
	}
}

func TestComposeServiceStatesUsesInstalledComposeProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ReployInternalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	state := deploy.DeploymentState{
		SchemaVersion: 1,
		Phase:         deploy.PhaseInstalled,
		Install: &deploy.InstallState{
			ComposeProject: "demo-12345678",
		},
	}
	content, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateFileName), append(content, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	original := runTestCommandOutput
	t.Cleanup(func() {
		runTestCommandOutput = original
	})
	runTestCommandOutput = func(spec CommandSpec) ([]byte, error) {
		if !containsAdjacent(spec.Args, "--project-name", "demo-12345678") {
			t.Fatalf("args did not include installed compose project: %#v", spec.Args)
		}
		if !containsString(spec.Args, "--all") {
			t.Fatalf("args did not include --all: %#v", spec.Args)
		}
		return []byte(`[{"State":"running"}]`), nil
	}

	states, err := composeServiceStates(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(states, ",") != "running" {
		t.Fatalf("states = %#v", states)
	}
}

func TestParseComposeServiceStatesSupportsJSONLines(t *testing.T) {
	states, err := parseComposeServiceStates([]byte("{\"State\":\"running\"}\n{\"State\":\"restarting\"}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(states, ",") != "running,restarting" {
		t.Fatalf("states = %#v", states)
	}
}

func TestHealthTLSVerifyDefaultsToTrue(t *testing.T) {
	if !healthTLSVerify(deploy.DockerHealthConfig{}) {
		t.Fatal("expected TLS verification by default")
	}
	verify := false
	if healthTLSVerify(deploy.DockerHealthConfig{TLSVerify: &verify}) {
		t.Fatal("expected explicit tls_verify false to disable verification")
	}
}

func splitHostPortForTest(t *testing.T, address string) (string, string) {
	t.Helper()
	index := strings.LastIndex(address, ":")
	if index < 0 {
		t.Fatalf("address has no port: %s", address)
	}
	return address[:index], address[index+1:]
}

func makeTestDeploymentWithDockerEnv(t *testing.T, env string) string {
	t.Helper()
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, DockerEnvFileName), []byte(env), 0o644); err != nil {
		t.Fatal(err)
	}
	return deployDir
}

func stubTestCommandOutput(output []byte, err error) func() {
	original := runTestCommandOutput
	runTestCommandOutput = func(spec CommandSpec) ([]byte, error) {
		return output, err
	}
	return func() {
		runTestCommandOutput = original
	}
}

func stubTestCommandOutputSequence(t *testing.T, outputs [][]byte) func() {
	t.Helper()
	original := runTestCommandOutput
	index := 0
	runTestCommandOutput = func(spec CommandSpec) ([]byte, error) {
		if index >= len(outputs) {
			return outputs[len(outputs)-1], nil
		}
		output := outputs[index]
		index++
		return output, nil
	}
	return func() {
		runTestCommandOutput = original
	}
}
