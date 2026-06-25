package dockerdeploy

import (
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
