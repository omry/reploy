package cli

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/dockerdeploy"
)

func TestVerifyJSONOptionsAndOutput(t *testing.T) {
	originalVerify, originalRuntime := dockerVerifyCurrentBuild, dockerProviderBuildRuntime
	t.Cleanup(func() { dockerVerifyCurrentBuild = originalVerify; dockerProviderBuildRuntime = originalRuntime })
	dir := filepath.Join(t.TempDir(), "stage")
	writeCLITestStagedState(t, dir, "demo")
	dockerProviderBuildRuntime = func() (dockerdeploy.StagedProviderBuildRuntimeV1, error) {
		return dockerdeploy.StagedProviderBuildRuntimeV1{}, nil
	}
	called := 0
	dockerVerifyCurrentBuild = func(_ context.Context, input dockerdeploy.VerifyCurrentBuildInputV1) (dockerdeploy.VerifyCurrentBuildResultV1, error) {
		called++
		if !input.ReportJSON || input.DeploymentDir != dir {
			t.Fatalf("input: %+v", input)
		}
		return dockerdeploy.VerifyCurrentBuildResultV1{JSON: []byte("{\"schema_version\":1}\n")}, nil
	}
	for _, format := range [][]string{{"--format", "json"}, {"--format=json"}} {
		args := append([]string{"verify", "--dir", dir}, format...)
		code, stdout, stderr := runCLI(args...)
		if code != 0 || stdout != "{\"schema_version\":1}\n" || stderr != "" {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	}
	if called != 2 {
		t.Fatal(called)
	}
	for _, options := range [][]string{{"--format"}, {"--format="}, {"--format", "text"}, {"--format=json", "--format", "json"}, {"--format", "json", "--format=json"}, {"--dir", "--format=json"}} {
		before := called
		code, stdout, _ := runCLI(append([]string{"verify"}, options...)...)
		if code != 2 || stdout != "" || called != before {
			t.Fatalf("usage accepted: %v code=%d stdout=%q", options, code, stdout)
		}
	}
	code, _, _ := runCLI("info", "--format=json")
	if code != 2 {
		t.Fatal("verify format leaked to info")
	}
	for _, failure := range []error{errors.New("audit failure"), context.Canceled} {
		dockerVerifyCurrentBuild = func(context.Context, dockerdeploy.VerifyCurrentBuildInputV1) (dockerdeploy.VerifyCurrentBuildResultV1, error) {
			return dockerdeploy.VerifyCurrentBuildResultV1{JSON: []byte("must not publish")}, failure
		}
		code, stdout, _ := runCLI("verify", "--dir", dir, "--format=json")
		want := 1
		if errors.Is(failure, context.Canceled) {
			want = 130
		}
		if code != want || stdout != "" {
			t.Fatalf("failure code=%d output=%q", code, stdout)
		}
	}
	dockerVerifyCurrentBuild = func(context.Context, dockerdeploy.VerifyCurrentBuildInputV1) (dockerdeploy.VerifyCurrentBuildResultV1, error) {
		return dockerdeploy.VerifyCurrentBuildResultV1{JSON: []byte("{}\n")}, nil
	}
	for _, writer := range []io.Writer{verifyJSONFailWriter{err: io.ErrClosedPipe}, verifyJSONFailWriter{}} {
		var stderr strings.Builder
		if code := runDockerVerify([]string{"--dir", dir, "--format=json"}, writer, &stderr); code != 1 || !strings.Contains(stderr.String(), "write JSON report") {
			t.Fatalf("write failure code=%d stderr=%s", code, stderr.String())
		}
	}
}

type verifyJSONFailWriter struct{ err error }

func (w verifyJSONFailWriter) Write([]byte) (int, error) { return 0, w.err }
