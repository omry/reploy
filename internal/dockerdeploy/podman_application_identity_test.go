package dockerdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"gopkg.in/yaml.v3"
)

func podmanSameIDInfo(uid, gid int) []byte {
	return []byte(fmt.Sprintf(`{"host":{"security":{"rootless":true},"idMappings":{"uidmap":[{"container_id":0,"host_id":%d,"size":1},{"container_id":1,"host_id":100000,"size":65536}],"gidmap":[{"container_id":0,"host_id":%d,"size":1},{"container_id":1,"host_id":100000,"size":65536}]}}}`, uid, gid))
}

func TestPodmanSameIDRequiresExplicitEngineAndAccountEvidence(t *testing.T) {
	info := podmanSameIDInfo(1000, 1001)
	got, err := podmanSameIDNamespace(info, 1000, 1001, 1000, 1001)
	if err != nil || got != "keep-id:uid=1000,gid=1001" {
		t.Fatalf("%q %v", got, err)
	}
	for _, test := range []struct {
		name                 string
		data                 []byte
		uid, gid             uint32
		callerUID, callerGID int
	}{
		{"missing mode", []byte(`{"host":{}}`), 1000, 1001, 1000, 1001},
		{"invalid", []byte(`not-json`), 1000, 1001, 1000, 1001},
		{"missing maps", []byte(`{"host":{"security":{"rootless":true}}}`), 1000, 1001, 1000, 1001},
		{"foreign engine", podmanSameIDInfo(2000, 1001), 1000, 1001, 1000, 1001},
		{"foreign group", podmanSameIDInfo(1000, 2001), 1000, 1001, 1000, 1001},
		{"different service user", info, 2000, 1001, 1000, 1001},
		{"different service group", info, 1000, 2001, 1000, 1001},
		{"root caller", podmanSameIDInfo(0, 0), 0, 0, 0, 0},
		{"invalid native IDs", info, 1000, 1001, -1, -1},
		{"ambiguous root", []byte(strings.Replace(string(info), `"uidmap":[`, `"uidmap":[{"container_id":0,"host_id":1000,"size":1},`, 1)), 1000, 1001, 1000, 1001},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := podmanSameIDNamespace(test.data, test.uid, test.gid, test.callerUID, test.callerGID); err == nil {
				t.Fatal("accepted invalid identity evidence")
			}
		})
	}
	got, err = podmanSameIDNamespace([]byte(`{"host":{"security":{"rootless":false}}}`), 2000, 2001, 1000, 1001)
	if err != nil || got != "" {
		t.Fatalf("changed rootful identity policy: %q %v", got, err)
	}
}

func sameIDApplicationSpec(t *testing.T) CommandSpec {
	return sameIDApplicationSpecWithTranslation(t, true)
}

func sameIDApplicationSpecWithTranslation(t *testing.T, translate bool) CommandSpec {
	return sameIDApplicationSpecWithIdentity(t, translate, 1000, 1000)
}

func sameIDApplicationSpecWithIdentity(t *testing.T, translate bool, uid, gid int) CommandSpec {
	t.Helper()
	privateSeccompCache(t)
	plan := DockerExecutionPlan{ContainerName: "hf11-app", Image: "sha256:" + strings.Repeat("a", 64), DeploymentDir: t.TempDir(), Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{UID: uint32(uid), GID: uint32(gid), DockerUser: fmt.Sprintf("%d:%d", uid, gid)}), Mounts: []MountExecutionPlan{{Name: "conf", Mode: blueprint.MountManagedBind, Source: filepath.Join(t.TempDir(), "comma,quote\"$path"), SourceKind: deploy.RuntimeMountSourceDirectory, Target: "/conf", ReadOnly: false}}}
	spec, err := TransientCommandSpec(plan, ResolvedEnvironmentCommand{Argv: []string{"/bin/echo", "--userns", "host", "--mount", "payload"}}, nil, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !translate {
		return spec
	}
	spec, err = preparePodmanSeccompCommand(spec)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestPodmanSameIDCreatePreservesGeneratedAuthorityAndArguments(t *testing.T) {
	spec := sameIDApplicationSpec(t)
	original := slices.Clone(spec.Args)
	body, name, err := podmanApplicationCreateRequest(spec, "keep-id:uid=1000,gid=1000")
	if err != nil {
		t.Fatal(err)
	}
	entry := slices.Index(spec.Args, "--entrypoint")
	if name != spec.Args[slices.Index(spec.Args, "--name")+1] || body.Image != spec.Args[entry+2] || !reflect.DeepEqual(body.Cmd, spec.Args[entry+3:]) || !reflect.DeepEqual(spec.Args, original) {
		t.Fatal("changed image, argv, name or canonical input")
	}
	if !body.OpenStdin || !body.StdinOnce || !body.AttachStdin || !body.AttachStdout || !body.AttachStderr || !body.Tty || !body.HostConfig.AutoRemove || !body.HostConfig.ReadonlyRootfs || body.User != "0:0" {
		t.Fatalf("lost lifecycle/sandbox properties: %#v", body)
	}
	if !slices.Equal(body.HostConfig.CapDrop, []string{"ALL"}) || !slices.Contains(body.HostConfig.CapAdd, "CHOWN") || !slices.Contains(body.HostConfig.SecurityOpt, "no-new-privileges=true") || !slices.Contains(body.HostConfig.SecurityOpt, "seccomp="+string(podmanDockerSeccompProfile)) {
		t.Fatal("lost pinned policy or setup capabilities")
	}
	if len(body.HostConfig.Mounts) != 1 || !strings.Contains(body.HostConfig.Mounts[0].Source, "comma,quote\"$path") || body.HostConfig.Mounts[0].Target != "/conf" || body.HostConfig.Mounts[0].ReadOnly {
		t.Fatalf("mount encoding changed: %#v", body.HostConfig.Mounts)
	}
	command, err := podmanApplicationCommand(spec)
	if err != nil {
		t.Fatal(err)
	}
	uid, gid, err := podmanApplicationIdentity(command)
	if err != nil || uid != 1000 || gid != 1000 {
		t.Fatalf("forwarded argv changed identity: %d:%d %v", uid, gid, err)
	}
}

func TestPodmanSameIDRejectsNetworkedSessionsBeforeCreate(t *testing.T) {
	privateSeccompCache(t)
	plan := controlledSessionNetworkPlanFixtureV1(t)
	for _, container := range []ControlledSessionContainerPlanV1{plan.Controller, plan.Workload} {
		t.Run(string(container.Role), func(t *testing.T) {
			create, err := renderControlledSessionCreateV1(container)
			if err != nil {
				t.Fatal(err)
			}
			spec, err := controlledSessionCreateWithPeerHostsV1(controlledSessionCommandSpecV1(create), container, []string{"172.31.0.3"})
			if err != nil {
				t.Fatal(err)
			}
			spec, err = preparePodmanSeccompCommand(spec)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			client := newPodmanApplicationClient(t, func(http.ResponseWriter, *http.Request) { calls++ })
			_, err = client.createSameIDApplication(t.Context(), spec, "keep-id:uid=1000,gid=1000", nil)
			if err == nil || !strings.Contains(err.Error(), "network-enabled controlled sessions are not supported") || calls != 0 {
				t.Fatalf("unsupported session reached create: calls=%d, err=%v", calls, err)
			}
		})
	}
}

func TestPodmanSameIDRejectsUnrecognisedCreationBeforeMutation(t *testing.T) {
	spec := sameIDApplicationSpec(t)
	for _, test := range []struct {
		name   string
		change func(*CommandSpec)
	}{
		{"no renderer authority", func(s *CommandSpec) { s.builtinSeccompIndex = 0 }},
		{"explicit mapping", func(s *CommandSpec) { s.Args = slices.Insert(s.Args, 1, "--userns", "private") }},
		{"unknown option", func(s *CommandSpec) { s.Args = slices.Insert(s.Args, 1, "--privileged", "true") }},
		{"duplicate name", func(s *CommandSpec) { s.Args = slices.Insert(s.Args, 1, "--name", "other") }},
		{"mutable pull", func(s *CommandSpec) { s.Args[slices.Index(s.Args, "--pull")+1] = "always" }},
		{"different user", func(s *CommandSpec) { s.Args[slices.Index(s.Args, "--user")+1] = "1000:1000" }},
		{"missing root restriction", func(s *CommandSpec) {
			i := slices.Index(s.Args, "--read-only")
			s.Args = append(s.Args[:i], s.Args[i+1:]...)
		}},
		{"different profile", func(s *CommandSpec) { s.Args[s.builtinSeccompIndex] = "seccomp=unconfined" }},
		{"different entrypoint", func(s *CommandSpec) { s.Args[slices.Index(s.Args, "--entrypoint")+1] = "/bin/sh" }},
		{"mount duplication", func(s *CommandSpec) {
			s.Args[slices.Index(s.Args, "--mount")+1] = "type=bind,source=/a,target=/conf,source=/b"
		}},
		{"reserved attempt label", func(s *CommandSpec) {
			s.Args = slices.Insert(s.Args, 1, "--label", podmanApplicationAttemptLabel+"=foreign")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			bad := spec
			bad.Args = slices.Clone(spec.Args)
			test.change(&bad)
			if _, _, err := podmanApplicationCreateRequest(bad, "keep-id"); err == nil {
				t.Fatal("accepted noncanonical request")
			}
		})
	}
}

type podmanApplicationTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (transport podmanApplicationTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	u := *r.URL
	r.URL = &u
	r.URL.Scheme, r.URL.Host = transport.target.Scheme, transport.target.Host
	return transport.base.RoundTrip(r)
}
func newPodmanApplicationClient(t *testing.T, handler http.HandlerFunc) *podmanHealthcheckClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	return &podmanHealthcheckClient{version: "6.1.4", endpoint: "unix:///private.sock", http: &http.Client{Transport: podmanApplicationTransport{target: target, base: server.Client().Transport}}}
}

func TestPodmanSameIDCreateReconcilesOnlyItsAttemptAndHidesResponseSecrets(t *testing.T) {
	for _, test := range []struct {
		name               string
		status             int
		malformed, foreign bool
		wantCleanup        bool
	}{
		{"success", 201, false, false, false}, {"malformed success", 201, true, false, true}, {"engine failure after create", 500, false, false, true}, {"foreign collision", 409, false, true, false}, {"foreign reconciliation", 500, false, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := sameIDApplicationSpec(t)
			id := strings.Repeat("b", 64)
			var submitted podmanApplicationCreate
			lookups, removals := 0, 0
			client := newPodmanApplicationClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					if r.URL.Query().Get("name") != spec.Args[slices.Index(spec.Args, "--name")+1] {
						t.Error("name changed")
					}
					if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
						t.Error(err)
					}
					w.WriteHeader(test.status)
					if test.status == 201 && !test.malformed {
						fmt.Fprintf(w, `{"Id":%q}`, id)
					} else {
						io.WriteString(w, "secret-environment-value")
					}
					return
				}
				lookups++
				labels := submitted.Labels
				if test.foreign {
					labels = map[string]string{podmanApplicationAttemptLabel: "foreign"}
				}
				json.NewEncoder(w).Encode(map[string]any{"Id": id, "Config": map[string]any{"Labels": labels}})
			})
			raw := func(s CommandSpec, _ RunOptions) error {
				removals++
				if !slices.Equal(s.Args, []string{"container", "rm", "--force", id}) {
					t.Errorf("unsafe cleanup: %v", s.Args)
				}
				return nil
			}
			got, err := client.createSameIDApplication(t.Context(), spec, "keep-id:uid=1000,gid=1000", raw)
			if test.name == "success" {
				if err != nil || got != id || lookups != 0 {
					t.Fatalf("%q %v lookups=%d", got, err, lookups)
				}
			} else if err == nil || strings.Contains(err.Error(), "secret-environment-value") {
				t.Fatalf("unsafe failure: %v", err)
			}
			if (removals == 1) != test.wantCleanup {
				t.Fatalf("cleanup=%d want=%t", removals, test.wantCleanup)
			}
			if submitted.Labels[podmanApplicationAttemptLabel] == "" || submitted.HostConfig.UsernsMode != "keep-id:uid=1000,gid=1000" {
				t.Fatal("missing attempt binding or namespace")
			}
			if test.status == 409 && lookups != 0 {
				t.Fatal("inspected a foreign collision")
			}
		})
	}
}

func TestPodmanSameIDComposeUsesCommonPolicyWithoutChangingMaintainedInput(t *testing.T) {
	privateSeccompCache(t)
	plan := DockerExecutionPlan{DeploymentDir: t.TempDir(), Workload: &WorkloadExecutionPlan{Argv: []string{"/bin/true"}}, ContainerName: "hf11", EnvironmentID: "hf11", NetworkName: "hf11-net", Image: "pinned", Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 1000, GID: 1000, DockerUser: "1000:1000"})}
	rendered, err := RenderDockerInputs(plan, "control")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, ComposeFileName)
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, rendered.Compose, 0600); err != nil {
		t.Fatal(err)
	}
	spec := composeCommandWithProject(dir, "hf11", "create")
	resolve := func(command []string) (string, error) {
		uid, gid, err := podmanApplicationIdentity(command)
		if err != nil {
			return "", err
		}
		return podmanSameIDNamespace(podmanSameIDInfo(1000, 1000), uid, gid, 1000, 1000)
	}
	translated, cleanup, changed, err := preparePodmanSeccompCompose(spec, len(spec.Args)-1, resolve)
	if err != nil || !changed {
		t.Fatalf("%v %t", err, changed)
	}
	defer cleanup()
	index := slices.Index(translated.Args, "-f")
	data, err := os.ReadFile(translated.Args[index+1])
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Services map[string]struct {
			Namespace string `yaml:"userns_mode"`
			User      string
			ReadOnly  bool `yaml:"read_only"`
		}
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	service := document.Services[composeWorkloadService]
	if service.Namespace != "keep-id:uid=1000,gid=1000" || service.User != "0:0" || !service.ReadOnly {
		t.Fatalf("lost common identity or sandbox: %#v", service)
	}
	unchanged, _ := os.ReadFile(file)
	if string(unchanged) != string(rendered.Compose) {
		t.Fatal("rewrote maintained Compose file")
	}
}

func TestPodmanSameIDRuntimeUsesNativeCreateAndExistingStart(t *testing.T) {
	if os.Geteuid() <= 0 || os.Getegid() < 0 {
		t.Skip("same-ID rootless runtime requires a non-root Unix account")
	}
	spec := sameIDApplicationSpecWithIdentity(t, false, os.Geteuid(), os.Getegid())
	spec = pinDockerEndpointV1(spec, "unix:///private.sock")
	id := strings.Repeat("c", 64)
	created, started := false, false
	client := newPodmanApplicationClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/info"):
			w.Write(podmanSameIDInfo(os.Geteuid(), os.Getegid()))
		case r.URL.Path == "/v1.44/containers/create":
			created = true
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"Id":%q}`, id)
		case strings.HasSuffix(r.URL.Path, "/json"):
			json.NewEncoder(w).Encode(fixturePodmanInspection(id, nil))
		default:
			http.NotFound(w, r)
		}
	})
	original := probePodmanHealthcheckClient
	probePodmanHealthcheckClient = func(context.Context, string) (*podmanHealthcheckClient, error) { return client, nil }
	t.Cleanup(func() { probePodmanHealthcheckClient = original })
	raw := func(command CommandSpec, _ RunOptions) error {
		if !created || !slices.Equal(command.Args, []string{"start", "--attach", id}) {
			t.Fatalf("unexpected raw command: %v", command.Args)
		}
		started = true
		return nil
	}
	handled, err := runPodmanHealthcheckCompatibility(spec, RunOptions{Context: t.Context()}, raw)
	if err != nil || !handled || !started {
		t.Fatalf("handled=%t started=%t err=%v", handled, started, err)
	}
}
