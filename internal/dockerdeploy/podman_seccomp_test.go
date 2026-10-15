package dockerdeploy

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func privateSeccompCache(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Podman compatibility is local Linux only")
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	return cache
}

func TestPodmanSeccompPinnedDataAndCacheIntegrity(t *testing.T) {
	cache := privateSeccompCache(t)
	if got := fmt.Sprintf("%x", sha256.Sum256(podmanDockerSeccompProfile)); got != podmanDockerSeccompSHA256 {
		t.Fatalf("upstream data changed: %s", got)
	}
	p, err := materializePodmanDockerSeccomp()
	if err != nil {
		t.Fatal(err)
	}
	second, err := materializePodmanDockerSeccomp()
	if err != nil || p != second || !strings.HasPrefix(p, cache+string(os.PathSeparator)) {
		t.Fatalf("unstable/private path: %q %q %v", p, second, err)
	}
	data, err := os.ReadFile(p)
	if err != nil || !bytes.Equal(data, podmanDockerSeccompProfile) {
		t.Fatalf("profile data mismatch: %v", err)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"defaultAction":"SCMP_ACT_ALLOW"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := materializePodmanDockerSeccomp(); err == nil {
		t.Fatal("accepted tampered cache policy")
	}
}

func TestPodmanSeccompCacheRejectsSymlinkAndSharedDirectory(t *testing.T) {
	for _, kind := range []string{"symlink", "shared"} {
		t.Run(kind, func(t *testing.T) {
			cache := privateSeccompCache(t)
			dir := filepath.Join(cache, "reploy")
			if kind == "symlink" {
				if err := os.Symlink(t.TempDir(), dir); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(dir, 0o777); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(dir, 0o777); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := materializePodmanDockerSeccomp(); err == nil {
				t.Fatalf("accepted %s cache", kind)
			}
		})
	}
}

func TestPodmanSeccompUsesRendererAuthorityNotApplicationArgv(t *testing.T) {
	privateSeccompCache(t)
	plan := DockerExecutionPlan{ContainerName: "seccomp", Image: "image", DeploymentDir: t.TempDir(),
		Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 1000, GID: 1000, DockerUser: "1000:1000"})}
	args := []string{"/bin/echo", "--security-opt", "seccomp=builtin"}
	spec, err := TransientCommandSpec(plan, ResolvedEnvironmentCommand{Argv: args}, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	original := append([]string{}, spec.Args...)
	translated, err := preparePodmanSeccompCommand(spec)
	if err != nil || translated.Args[spec.builtinSeccompIndex] == "seccomp=builtin" {
		t.Fatalf("renderer policy not translated: %v", err)
	}
	if !reflect.DeepEqual(spec.Args, original) || !reflect.DeepEqual(translated.Args[len(translated.Args)-len(args):], args) {
		t.Fatal("changed logical plan or forwarded application arguments")
	}
	controlledArgs := append([]string{}, spec.Args...)
	controlledArgs[0] = "create"
	controlled := controlledSessionCommandSpecV1(ControlledSessionDockerCommandV1{Name: "docker", Args: controlledArgs})
	if controlled.builtinSeccompIndex != spec.builtinSeccompIndex {
		t.Fatal("controlled-session renderer lost seccomp authority")
	}
	spec.Args = append([]string{}, spec.Args...)
	spec.Args[spec.builtinSeccompIndex] = "seccomp=unconfined"
	if _, err := preparePodmanSeccompCommand(spec); err == nil {
		t.Fatal("accepted changed canonical policy")
	}
	untagged := CommandSpec{Name: "docker", Args: []string{"create", "image", "echo", "--security-opt", "seccomp=builtin"}}
	got, err := preparePodmanSeccompCommand(untagged)
	if err != nil || !reflect.DeepEqual(got, untagged) {
		t.Fatal("interpreted application payload as an engine security option")
	}
}

func TestPodmanSeccompComposePreservesModelAndStableProfile(t *testing.T) {
	privateSeccompCache(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, ComposeFileName)), 0o700); err != nil {
		t.Fatal(err)
	}
	content := []byte("services:\n  environment:\n    image: pinned\n    read_only: true\n    cap_drop: [ALL]\n    security_opt: [no-new-privileges:true, seccomp=builtin]\n    volumes: [./data:/data:ro]\n  unrelated:\n    image: other\n    security_opt: [seccomp=unconfined]\n")
	if err := os.WriteFile(filepath.Join(dir, ComposeFileName), content, 0o600); err != nil {
		t.Fatal(err)
	}
	spec := composeCommandWithProject(dir, "owned", "create")
	action := len(spec.Args) - 1
	var policy string
	for iteration := 0; iteration < 2; iteration++ {
		translated, cleanup, changed, err := preparePodmanSeccompCompose(spec, action)
		if err != nil || !changed {
			t.Fatalf("translation: %v / %t", err, changed)
		}
		var file string
		for i := 0; i+1 < len(translated.Args); i++ {
			if translated.Args[i] == "-f" {
				file = translated.Args[i+1]
			}
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var before, after map[string]any
		if err := yaml.Unmarshal(content, &before); err != nil {
			t.Fatal(err)
		}
		if err := yaml.Unmarshal(data, &after); err != nil {
			t.Fatal(err)
		}
		service := after["services"].(map[string]any)["environment"].(map[string]any)
		options := service["security_opt"].([]any)
		if iteration == 0 {
			policy = options[1].(string)
		}
		if options[1] != policy || !strings.HasPrefix(policy, "seccomp=") || policy == "seccomp=builtin" {
			t.Fatal("unstable/absent Docker policy")
		}
		service["security_opt"] = before["services"].(map[string]any)["environment"].(map[string]any)["security_opt"]
		if !reflect.DeepEqual(before, after) {
			t.Fatal("changed unrelated Compose authority")
		}
		cleanup()
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatal("temporary Compose file retained")
		}
	}
	data, _ := os.ReadFile(filepath.Join(dir, ComposeFileName))
	if !bytes.Equal(data, content) {
		t.Fatal("changed maintained Compose input")
	}
}

func TestPodmanSeccompRuntimeLeavesDockerUntouchedAndFailsBeforeStart(t *testing.T) {
	privateSeccompCache(t)
	fixture := newPodmanHealthcheckFixture(t)
	plan := DockerExecutionPlan{ContainerName: "seccomp", Image: "image", DeploymentDir: t.TempDir(), Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 1000, GID: 1000, DockerUser: "1000:1000"})}
	spec, err := TransientCommandSpec(plan, ResolvedEnvironmentCommand{Argv: []string{"/bin/true"}}, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	spec = pinDockerEndpointV1(spec, fixture.endpoint)
	for _, docker := range []bool{true, false} {
		fixture.docker = docker
		calls := 0
		handled, err := runPodmanHealthcheckCompatibility(spec, RunOptions{Context: t.Context()}, func(got CommandSpec, _ RunOptions) error {
			calls++
			if docker || got.Args[0] != "create" || got.Args[spec.builtinSeccompIndex] == "seccomp=builtin" {
				t.Fatal("unexpected engine/create translation")
			}
			return fmt.Errorf("inline JSON rejected by engine")
		})
		if docker {
			if handled || err != nil || calls != 0 {
				t.Fatal("intercepted Docker")
			}
		} else if !handled || err == nil || !strings.Contains(err.Error(), "29726") || calls != 1 {
			t.Fatalf("unsafe fallback/start or missing guidance: %v / calls %d", err, calls)
		}
	}
}
