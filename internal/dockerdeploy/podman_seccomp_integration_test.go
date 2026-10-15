package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
)

// Explicit disposable fixtures only. The launcher records the exact engine
// commit and supplies a Reploy-built image containing its real startup verifier.
func TestPodmanSeccompCompatibilityIntegration(t *testing.T) {
	endpoint, image := os.Getenv("REPLOY_HF8_PODMAN_ENDPOINT"), os.Getenv("REPLOY_HF8_PODMAN_IMAGE")
	if runtime.GOOS != "linux" || endpoint == "" || image == "" {
		t.Skip("requires an explicit disposable local Podman endpoint and Reploy runtime image")
	}
	privateSeccompCache(t)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	client, err := identifyPodmanHealthcheckClient(ctx, endpoint)
	if err != nil || client == nil {
		t.Fatalf("fixture is not Podman: %v", err)
	}
	defer client.http.CloseIdleConnections()
	raw := func(t *testing.T, args ...string) string {
		t.Helper()
		command := exec.CommandContext(ctx, "docker", args...)
		command.Env = append(os.Environ(), "DOCKER_HOST="+endpoint, "DOCKER_CONTEXT=")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture command %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	run := func(t *testing.T, spec CommandSpec) string {
		t.Helper()
		var output, diagnostic strings.Builder
		if err := runCommandWithoutDockerPreflight(pinDockerEndpointV1(spec, endpoint), RunOptions{Context: ctx, Stdout: &output, Stderr: &diagnostic}); err != nil {
			t.Fatalf("runtime command %v: %v: %s", spec.Args, err, diagnostic.String())
		}
		return strings.TrimSpace(output.String())
	}
	assertPolicy := func(t *testing.T, id string) {
		t.Helper()
		output, err := client.request(ctx, "GET", "/v1.44/containers/"+id+"/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		var inspection struct {
			HostConfig struct {
				SecurityOpt    []string
				ReadonlyRootfs bool
			}
		}
		if err := json.Unmarshal(output, &inspection); err != nil {
			t.Fatal(err)
		}
		if !inspection.HostConfig.ReadonlyRootfs {
			t.Fatal("writable root filesystem")
		}
		var actual []byte
		nnp := false
		for _, option := range inspection.HostConfig.SecurityOpt {
			if strings.HasPrefix(option, "seccomp=") {
				actual = []byte(strings.TrimPrefix(option, "seccomp="))
			}
			if option == "no-new-privileges" || option == "no-new-privileges=true" || option == "no-new-privileges:true" {
				nnp = true
			}
		}
		var want, got any
		if !nnp || json.Unmarshal(podmanDockerSeccompProfile, &want) != nil || json.Unmarshal(actual, &got) != nil {
			t.Fatalf("missing explicit policy/NNP: %q", inspection.HostConfig.SecurityOpt)
		}
		w, _ := json.Marshal(want)
		g, _ := json.Marshal(got)
		if !bytes.Equal(w, g) {
			t.Fatal("engine did not retain the exact pinned profile")
		}
	}
	// A negative syscall test plus an unconfined positive control proves actual
	// filtering, rather than treating Seccomp:2 alone as policy evidence.
	dir := t.TempDir()
	source := filepath.Join(dir, "check.go")
	helper := filepath.Join(dir, "check")
	code := `package main
import("fmt";"os";"syscall")
func main(){ _,_,e:=syscall.RawSyscall(syscall.SYS_PERSONALITY,0x40000,0,0); if os.Args[1]=="deny" {if e!=syscall.EPERM{fmt.Println(e);os.Exit(1)}}else if e!=0 {fmt.Println(e);os.Exit(2)};fmt.Println("syscall-"+os.Args[1]+"-pass") }
`
	if err := os.WriteFile(source, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	build := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", helper, source)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build syscall fixture: %v: %s", err, output)
	}
	if output := run(t, CommandSpec{Name: "docker", Args: []string{"run", "--rm", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt", "no-new-privileges=true", "--security-opt", "seccomp=unconfined", "--mount", "type=bind,source=" + helper + ",target=/hf8-check,readonly", "--entrypoint", "/hf8-check", image, "allow"}}); output != "syscall-allow-pass" {
		t.Fatalf("no independent syscall control: %q", output)
	}
	identity := RuntimeUserPlan{UID: 1000, GID: 1000, DockerUser: "1000:1000"}
	plan := DockerExecutionPlan{EnvironmentID: "hf8", DeploymentDir: t.TempDir(), Phase: blueprint.PhaseStaged, Image: image, ContainerName: uniqueDockerIntegrationName("reploy-hf8"), NetworkName: uniqueDockerIntegrationName("reploy-hf8-net"), Sandbox: newApplicationSandboxPlanV1(identity), Mounts: []MountExecutionPlan{{Name: "syscall-check", Mode: blueprint.MountBind, Source: helper, SourceKind: deploy.RuntimeMountSourceFile, Target: "/hf8-check", ReadOnly: true}}}
	check := `test "$(awk '/^Seccomp:/ {print $2}' /proc/self/status)" = 2
test "$(awk '/^NoNewPrivs:/ {print $2}' /proc/self/status)" = 1
test "$(id -u):$(id -g)" = 1000:1000
test "$(stat -c %u:%g:%a /mnt/reploy-home)" = 1000:1000:700
test "$HOME" = /mnt/reploy-home
touch "$HOME/private-write"
for name in CapEff CapPrm CapBnd CapAmb; do test "$(awk -v n="$name:" '$1==n {print $2}' /proc/self/status)" = 0000000000000000; done
if touch /hf8-root-write 2>/dev/null; then exit 44; fi
/hf8-check deny
echo kernel-policy-pass`
	proof := filepath.Join(dir, "proof.sh")
	if err := os.WriteFile(proof, []byte(check), 0o644); err != nil {
		t.Fatal(err)
	}
	plan.Mounts = append(plan.Mounts, MountExecutionPlan{Name: "policy-proof", Mode: blueprint.MountBind, Source: proof, SourceKind: deploy.RuntimeMountSourceFile, Target: "/hf8-proof.sh", ReadOnly: true})
	t.Run("unsafe private home fails before app exec", func(t *testing.T) {
		profile, err := materializePodmanDockerSeccomp()
		if err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct{ name, mount string }{
			{"no dedicated mount", ""},
			{"public permissions", "/mnt/reploy-home:rw,noexec,nosuid,nodev,size=64m,mode=0777"},
			{"executable home", "/mnt/reploy-home:rw,exec,nosuid,nodev,size=64m,mode=0700"},
			{"wrong size", "/mnt/reploy-home:rw,noexec,nosuid,nodev,size=128m,mode=0700"},
		} {
			t.Run(test.name, func(t *testing.T) {
				args := []string{"run", "--rm", "--network=none", "--read-only", "--user", "0:0", "--cap-drop=ALL", "--cap-add", "CHOWN", "--cap-add", "SETGID", "--cap-add", "SETUID", "--cap-add", "SETPCAP", "--security-opt", "no-new-privileges=true", "--security-opt", "seccomp=" + profile}
				if test.mount != "" {
					args = append(args, "--tmpfs", test.mount)
				}
				args = append(args, "--entrypoint", "/reploy-probe", image, "restricted-exec", "--prepare-temporary-home", "--uid", "1000", "--gid", "1000", "--", "/bin/echo", "unsafe-app-ran")
				var output bytes.Buffer
				err := runCommandWithoutDockerPreflight(pinDockerEndpointV1(CommandSpec{Name: "docker", Args: args}, endpoint), RunOptions{Context: ctx, Stdout: &output, Stderr: &output})
				if err == nil || !bytes.Contains(output.Bytes(), []byte("prepare private temporary home")) || bytes.Contains(output.Bytes(), []byte("unsafe-app-ran")) {
					t.Fatalf("unsafe home did not stop at trusted setup: %v: %s", err, output.String())
				}
			})
		}
	})
	t.Run("transient actual sandbox", func(t *testing.T) {
		execution, err := PlanTransientContainerExecutionV1(plan, ResolvedEnvironmentCommand{Argv: []string{"/bin/sh", "-eu", "/hf8-proof.sh"}}, nil, "run-0000000000000001", false, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			command := exec.Command("docker", "rm", "--force", execution.Container)
			command.Env = append(os.Environ(), "DOCKER_HOST="+endpoint, "DOCKER_CONTEXT=")
			_ = command.Run()
		})
		id := run(t, execution.Create)
		assertPolicy(t, id)
		if output := run(t, execution.Start); !strings.Contains(output, "kernel-policy-pass") || !strings.Contains(output, "syscall-deny-pass") {
			t.Fatalf("kernel proof missing: %q", output)
		}
	})
	t.Run("controlled session create boundary and recreation", func(t *testing.T) {
		for iteration := 0; iteration < 2; iteration++ {
			t.Run(fmt.Sprint(iteration), func(t *testing.T) {
				current := CurrentBuild{Generation: deploy.EnvironmentGenerationState{Reference: image, BuildLockDigest: canonical.Digest("sha256:" + strings.Repeat("1", 64))}}
				container, err := controlledSessionContainerPlanV1(ControlledSessionRoleWorkloadV1, "run-0000000000000002", current, plan, []string{"/bin/sh"}, ControlledSessionChannelPlanV1{}, []string{plan.DeploymentDir}, disabledControlledSessionNetworkPlanV1(), nil, 80, 24, nil)
				if err != nil {
					t.Fatal(err)
				}
				workload, capture := prepareControlledSessionWorkloadIntegrationV1(t, ctx, container)
				id := raw(t, "inspect", "--format", "{{.Id}}", container.Container)
				assertPolicy(t, id)
				if err := workload.Start(ctx); err != nil {
					t.Fatal(err)
				}
				writeWorkloadPTYIntegrationV1(t, ctx, workload, "stty -echo; /bin/sh -eu /hf8-proof.sh; exit $?\n")
				status, err := workload.Wait(ctx)
				if err != nil || status.Code == nil || *status.Code != 0 {
					t.Fatalf("sandbox shell proof: %#v, %v", status, err)
				}
				capture.waitFor(t, []byte("kernel-policy-pass"), 10*time.Second)
			})
		}
	})
	t.Run("compose actual sandbox stable up and recreation", func(t *testing.T) {
		persistent := plan
		persistent.Workload = &WorkloadExecutionPlan{Argv: []string{"/bin/sh", "-eu", "-c", "/bin/sh -eu /hf8-proof.sh\nexec sleep infinity"}, Endpoints: map[string]EndpointExecutionPlan{}}
		rendered, err := RenderDockerInputs(persistent, "hf8")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(plan.DeploymentDir, ComposeFileName)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(plan.DeploymentDir, ComposeFileName), rendered.Compose, 0o600); err != nil {
			t.Fatal(err)
		}
		environment, err := renderProviderInstallEnvironmentV1(rendered.Environment)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(plan.DeploymentDir, DockerEnvFileName), environment, 0o600); err != nil {
			t.Fatal(err)
		}
		project := uniqueDockerIntegrationName("reploy-hf8-compose")
		t.Cleanup(func() { run(t, composeCommandWithProject(plan.DeploymentDir, project, "down", "--remove-orphans")) })
		var previous string
		for _, force := range []bool{false, false, true} {
			args := []string{"up", "--pull", "never", "-d"}
			if force {
				args = append(args, "--force-recreate")
			}
			run(t, composeCommandWithProject(plan.DeploymentDir, project, args...))
			id := raw(t, "inspect", "--format", "{{.Id}}", plan.ContainerName)
			assertPolicy(t, id)
			if previous != "" && ((force && id == previous) || (!force && id != previous)) {
				t.Fatalf("unexpected recreation force=%t: %s -> %s", force, previous, id)
			}
			previous = id
			deadline := time.Now().Add(10 * time.Second)
			for !strings.Contains(raw(t, "logs", id), "kernel-policy-pass") {
				if time.Now().After(deadline) {
					t.Fatal("persistent kernel proof missing")
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
	})
	if !t.Failed() {
		fmt.Fprintln(os.Stdout, "HF8 pinned profile, negative syscall, kernel baseline and recreation evidence passed on Podman", client.version)
	}
}
