//go:build linux

package dockerdeploy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/identitymapping"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
)

// This effect-owner fixture records an unused private intent. It does not
// implement a production lifecycle controller or accept/recover a mapping.
type mappedCreateFixtureEvidenceV1 struct{}

func (mappedCreateFixtureEvidenceV1) Plan(context.Context, deploy.StateV1, identitymapping.Geometry) ([]string, error) {
	return nil, nil
}
func (mappedCreateFixtureEvidenceV1) Unused(context.Context, deploy.StateV1) error {
	return errors.New("fixture does not replace candidates")
}
func (mappedCreateFixtureEvidenceV1) Verify(context.Context, deploy.StateV1) error {
	return errors.New("fixture never grants lifecycle admission")
}

func mappedCreateIntegrationSpecV1(t *testing.T, name, image, nonce, endpoint string) CommandSpec {
	t.Helper()
	profile, err := materializePodmanDockerSeccomp()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"create", "--name", name, "--user", "0:0", "--pull", "never", "--read-only", "--cap-drop", "ALL", "--security-opt", "seccomp=" + profile, "--security-opt", "no-new-privileges=true", "--network", "none", "--tmpfs", "/run:rw,noexec,nosuid,nodev,size=4m,mode=1777", "--label", "io.reploy.c1-fixture=" + nonce, "--entrypoint", "/reploy-probe", image, "admission-wait", nonce, "cap-report"}
	return CommandSpec{Name: "docker", Args: args, Env: []string{"DOCKER_HOST=" + endpoint, "DOCKER_CONTEXT="}, builtinSeccompIndex: slices.Index(args, "seccomp="+profile)}
}

func mappedCreateIntegrationStateV1(t *testing.T, image, endpoint string) (deploy.IdentityMappingOperationsV1, deploy.StateV1) {
	t.Helper()
	dir, _ := stageProviderBuildRunState(t, false)
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	state, found, err := operation.ReadStateV1()
	if err != nil || !found {
		t.Fatal("fixture state missing", err)
	}
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	state.BlueprintSource = "private C image-binding fixture; not a qualified application build"
	client, err := identifyPodmanClientWithDialV1(t.Context(), endpoint, dialMappedPodmanV1, true)
	if err != nil || client == nil {
		t.Fatal("fixture image endpoint", err)
	}
	defer client.http.CloseIdleConnections()
	data, err := client.request(t.Context(), http.MethodGet, "/v1.44/images/"+image+"/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := parseDockerImageInspectionDetails(image, state.Platform, append(append([]byte{'['}, data...), ']'))
	if err != nil {
		t.Fatal(err)
	}
	rootFS, err := deploy.RootFSSubject(inspection.Descriptor.RootFSDiffIDs)
	if err != nil {
		t.Fatal(err)
	}
	store, lock := publicationLockFixture(t, dir, "4", "5", "6")
	lock.FinalImage = providers.RealizedImageV1{Digest: canonical.Digest(image), ConfigDigest: canonical.Digest(image), RootFSSubject: rootFS}
	lock.RuntimeLayer.Result = lock.FinalImage
	generation := ownedReferenceGenerationFixtureV1(t, dir, "demo", lock)
	lock.ValidationRecord, err = deploy.PublishPrefixValidation(t.Context(), store, deploy.PrefixValidationV1{Schema: deploy.PrefixValidationSchemaV1, SubjectRootFS: rootFS, Profiles: []providers.ValidationEvidence{}, RuntimePolicy: generation.RuntimePolicyDigest, ExposedOutputs: []providers.ExecutableEvidence{}})
	if err != nil {
		t.Fatal(err)
	}
	// Validation evidence is part of the lock digest, so derive the generation
	// only after the complete metadata fixture has been constructed.
	generation = ownedReferenceGenerationFixtureV1(t, dir, "demo", lock)
	if _, err := operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	colon := strings.LastIndexByte(generation.Reference, ':')
	if _, err := client.request(t.Context(), http.MethodPost, "/v1.44/images/"+image+"/tag?repo="+url.QueryEscape(generation.Reference[:colon])+"&tag="+url.QueryEscape(generation.Reference[colon+1:]), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		path := "/v1.44/images/" + url.PathEscape(generation.Reference)
		data, err := client.request(ctx, http.MethodGet, path+"/json", nil)
		var object struct{ ID string }
		if err != nil || json.Unmarshal(data, &object) != nil || object.ID != image {
			t.Error("refuse cleanup of unproved fixture tag", err)
			return
		}
		if _, err := client.request(ctx, http.MethodDelete, path+"?force=true", nil); err != nil {
			t.Error("owned fixture tag cleanup", err)
		}
	})
	state.Current = &generation
	if err := operation.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	return deploy.IdentityMappingOperationsV1{Operation: operation, Evidence: mappedCreateFixtureEvidenceV1{}}, state
}

func readMappedKernelIntervalsV1(t *testing.T, pid int, kind string) []identitymapping.Interval {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/%s_map", pid, kind))
	if err != nil {
		t.Fatal(err)
	}
	var values []identitymapping.Interval
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var c, h, n uint32
		if count, err := fmt.Sscanf(line, "%d %d %d", &c, &h, &n); err != nil || count != 3 {
			t.Fatalf("invalid live map %q", line)
		}
		values = append(values, identitymapping.Interval{ContainerStart: c, HostStart: h, Count: n})
	}
	return values
}

// Opt in only on a disposable eligible host. The image contains the existing
// conformance probe at /probe and /reploy-probe, with no host data mounted.
func TestMappedPodmanCreateLiveV1(t *testing.T) {
	endpoint, image := os.Getenv("REPLOY_C1_TEST_ENDPOINT"), os.Getenv("REPLOY_C1_TEST_IMAGE")
	if endpoint == "" && image == "" {
		t.Skip("requires explicit disposable eligible C1 endpoint and fixture image")
	}
	if !strings.HasPrefix(endpoint, "unix://") || !isDockerLocalImageID(image) || os.Geteuid() == 0 {
		t.Fatal("invalid explicit C1 fixture inputs")
	}
	privateSeccompCache(t)
	for _, profile := range []string{"exact", "bounded"} {
		t.Run(profile, func(t *testing.T) {
			ops, original := mappedCreateIntegrationStateV1(t, image, endpoint)
			active := mappedGeometryFixtureV1(profile)
			var random [16]byte
			if _, err := rand.Read(random[:]); err != nil {
				t.Fatal(err)
			}
			nonce := hex.EncodeToString(random[:])
			operationID := "c1-" + nonce
			intent, err := ops.Configure(t.Context(), original, active, operationID)
			if err != nil {
				t.Fatalf("real B Configure: %v", err)
			}
			firstSpec := mappedCreateIntegrationSpecV1(t, "reploy-c1-"+nonce, image, nonce, endpoint)
			if _, err := prepareMappedPodmanCreateV1(t.Context(), ops, intent, active, operationID, firstSpec); err == nil {
				t.Fatal("intent granted a dispatchable preparation")
			}
			pending, err := ops.BeginEffects(t.Context(), intent, operationID)
			if err != nil {
				t.Fatalf("real B BeginEffects: %v", err)
			}
			if profile == "exact" {
				checkMappedEngineRefusalsV1(t, ops, pending, active, operationID, firstSpec)
			}
			if _, err := prepareMappedPodmanCreateV1(t.Context(), ops, pending, active, "foreign-operation", firstSpec); err == nil {
				t.Fatal("foreign operation admitted")
			}
			if _, err := prepareMappedPodmanCreateV1(t.Context(), ops, intent, active, operationID, firstSpec); err == nil {
				t.Fatal("stale revision admitted")
			}
			foreign := firstSpec
			foreign.Args = slices.Clone(firstSpec.Args)
			foreign.Args[slices.Index(foreign.Args, "--entrypoint")+2] = string(rendererDigest("a"))
			if _, err := prepareMappedPodmanCreateV1(t.Context(), ops, pending, active, operationID, foreign); err == nil || !strings.Contains(err.Error(), "selected generation") {
				t.Fatal("foreign image was not rejected by current-generation binding", err)
			}
			for repeat := 0; repeat < 2; repeat++ {
				t.Run(strconv.Itoa(repeat), func(t *testing.T) {
					spec := mappedCreateIntegrationSpecV1(t, fmt.Sprintf("reploy-c1-%s-%d", nonce, repeat), image, nonce, endpoint)
					if repeat == 1 {
						spec.Args[slices.Index(spec.Args, "--entrypoint")+2] = pending.Current.Reference
					}
					prepared, err := prepareMappedPodmanCreateV1(t.Context(), ops, pending, active, operationID, spec)
					if err != nil {
						t.Fatal("production C1 preparation", err)
					}
					client, err := identifyPodmanClientWithDialV1(t.Context(), endpoint, dialMappedPodmanV1, true)
					if err != nil || client == nil {
						t.Fatal("fixture endpoint", err)
					}
					defer client.http.CloseIdleConnections()
					var ownedID string
					remove := func(ctx context.Context, id string) error {
						data, err := client.request(ctx, http.MethodGet, "/v1.44/containers/"+id+"/json", nil)
						var object struct {
							ID, Name string
							Config   struct{ Labels map[string]string }
						}
						if err != nil || json.Unmarshal(data, &object) != nil || object.ID != id || object.Config.Labels["io.reploy.c1-fixture"] != nonce {
							return errors.New("refuse cleanup of an unproved fixture object")
						}
						_, err = client.request(ctx, http.MethodDelete, "/v1.44/containers/"+id+"?force=true", nil)
						return err
					}
					raw := func(s CommandSpec, o RunOptions) error {
						if len(s.Args) != 4 || !slices.Equal(s.Args[:3], []string{"container", "rm", "--force"}) || commandEnvironmentValueV1(s, "DOCKER_HOST") != endpoint {
							return errors.New("unsafe fixture cleanup request")
						}
						return remove(o.Context, s.Args[3])
					}
					ownedID, err = prepared.create(t.Context(), raw)
					if err != nil {
						t.Fatal("production C1 compatible create", err)
					}
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
						defer cancel()
						if err := remove(ctx, ownedID); err != nil {
							t.Error("exact fixture cleanup", err)
						}
					}()
					// Private test starts only the trusted waiter. No product payload
					// admission or public mapped execution is exercised or claimed.
					if _, err := client.request(t.Context(), http.MethodPost, "/v1.44/containers/"+ownedID+"/start", nil); err != nil {
						t.Fatal(err)
					}
					var pid int
					deadline := time.Now().Add(20 * time.Second)
					for time.Now().Before(deadline) {
						data, err := client.request(t.Context(), http.MethodGet, "/v1.44/containers/"+ownedID+"/json", nil)
						var object struct {
							State struct {
								Running bool
								Pid     int
							}
						}
						if err != nil || json.Unmarshal(data, &object) != nil {
							t.Fatal("inspect waiter", err)
						}
						if object.State.Running && object.State.Pid > 0 {
							pid = object.State.Pid
							break
						}
						time.Sleep(20 * time.Millisecond)
					}
					if pid == 0 {
						t.Fatal("trusted fixture waiter did not start")
					}
					uids, gids := readMappedKernelIntervalsV1(t, pid, "uid"), readMappedKernelIntervalsV1(t, pid, "gid")
					if !slices.Equal(uids, active.UIDs) || !slices.Equal(gids, active.GIDs) {
						t.Fatalf("complete kernel maps differ before release: uid=%v gid=%v", uids, gids)
					}
					if slices.Equal(uids, append(slices.Clone(active.UIDs), identitymapping.Interval{ContainerStart: 70000, HostStart: 900000, Count: 1})) {
						t.Fatal("surplus mapping comparator accepted")
					}
					t.Logf("C1 exact-source create: profile=%s repeat=%d engine=%s uid=%v gid=%v", profile, repeat, prepared.version, uids, gids)
					// The waiter remains closed; cleanup proves the mapped create
					// boundary without starting any application or accepting effects.
					state, found, err := ops.Operation.ReadStateV1()
					if err != nil || !found || state.IdentityMapping.PendingOperation == nil || state.IdentityMapping.PendingOperation.Phase != "effects-pending" || !state.IdentityMapping.PendingOperation.Inhibited || state.IdentityMapping.AdmittedGeneration != nil {
						t.Fatal("C1 altered or accepted mapping authority", err)
					}
					if _, err := unmappedRuntimeDocumentV1(state); err == nil {
						t.Fatal("public mapped-execution guard was bypassed")
					}
				})
			}
		})
	}
}

func checkMappedEngineRefusalsV1(t *testing.T, ops deploy.IdentityMappingOperationsV1, pending deploy.StateV1, active identitymapping.Geometry, operationID string, spec CommandSpec) {
	t.Helper()
	baseSpec := spec
	client, err := identifyPodmanClientWithDialV1(t.Context(), commandEnvironmentValueV1(spec, "DOCKER_HOST"), dialMappedPodmanV1, true)
	if err != nil || client == nil {
		t.Fatal("fixture image client", err)
	}
	defer client.http.CloseIdleConnections()
	image := spec.Args[slices.Index(spec.Args, "--entrypoint")+2]
	imageData, err := client.request(t.Context(), http.MethodGet, "/v1.44/images/"+image+"/json", nil)
	if err != nil {
		t.Fatal("fixture image metadata", err)
	}
	for _, kind := range []string{"Docker", "rootful", "missing maps", "wrong parent", "malformed version", "remote", "mutable image", "image unavailable", "host bind", "named volume", "invalid generated policy"} {
		t.Run("reject "+kind, func(t *testing.T) {
			spec := baseSpec
			spec.Args = slices.Clone(spec.Args)
			if kind == "remote" {
				spec.Env = []string{"DOCKER_HOST=tcp://127.0.0.1:1", "DOCKER_CONTEXT="}
				if _, err := prepareMappedPodmanCreateV1(t.Context(), ops, pending, active, operationID, spec); err == nil {
					t.Fatal("remote endpoint admitted")
				}
				return
			}
			dir, err := os.MkdirTemp("", "c1-peer-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			path := filepath.Join(dir, "p.sock")
			listener, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("refused engine reached effectful dispatch")
					w.WriteHeader(500)
					return
				}
				if strings.HasPrefix(r.URL.Path, "/v1.44/images/") {
					if kind == "image unavailable" {
						w.WriteHeader(404)
					} else {
						_, _ = w.Write(imageData)
					}
					return
				}
				if r.URL.Path == "/version" {
					if kind == "malformed version" {
						io.WriteString(w, "{")
						return
					}
					name := "Podman Engine"
					if kind == "Docker" {
						name = "Engine"
					}
					fmt.Fprintf(w, `{"Version":"6.2.0","Os":"linux","Components":[{"Name":%q,"Version":"6.2.0"}]}`, name)
					return
				}
				data := string(podmanSameIDInfo(os.Geteuid(), os.Getegid()))
				if kind == "rootful" {
					data = strings.Replace(data, `"rootless":true`, `"rootless":false`, 1)
				}
				if kind == "missing maps" {
					data = `{"Host":{"Security":{"Rootless":true}}}`
				}
				if kind == "wrong parent" {
					data = string(podmanSameIDInfo(os.Geteuid()+1, os.Getegid()))
				}
				io.WriteString(w, data)
			})}
			go server.Serve(listener)
			defer server.Close()
			spec.Env = []string{"DOCKER_HOST=unix://" + path, "DOCKER_CONTEXT="}
			entry := slices.Index(spec.Args, "--entrypoint")
			switch kind {
			case "mutable image":
				spec.Args[entry+2] = "mutable:latest"
			case "host bind", "named volume":
				mount := "type=bind,source=/unadmitted,target=/data"
				if kind == "named volume" {
					mount = "type=volume,source=unadmitted,target=/data"
				}
				spec.Args = append(append(slices.Clone(spec.Args[:entry]), "--mount", mount), spec.Args[entry:]...)
			case "invalid generated policy":
				spec.Args = slices.Delete(spec.Args, slices.Index(spec.Args, "--read-only"), slices.Index(spec.Args, "--read-only")+1)
			}
			if _, err := prepareMappedPodmanCreateV1(t.Context(), ops, pending, active, operationID, spec); err == nil {
				t.Fatal("incompatible engine admitted")
			}
		})
	}
	if endpoint := os.Getenv("REPLOY_C1_TEST_WRONG_PEER_ENDPOINT"); endpoint != "" {
		connection, err := dialMappedPodmanV1(t.Context(), endpoint)
		if err == nil {
			connection.Close()
			t.Fatal("foreign Unix peer accepted")
		}
		if !strings.Contains(err.Error(), "UID/GID") {
			t.Fatalf("wrong-peer fixture did not exercise credential rejection: %v", err)
		}
		t.Log("actual foreign Unix peer was rejected before HTTP")
	}
	for _, kind := range []string{"changed engine", "changed parent", "metadata unavailable", "image unavailable at create", "changed host authority", "host changes during metadata", "cancelled", "changed generated policy"} {
		t.Run("dispatch rejects "+kind, func(t *testing.T) {
			spec := baseSpec
			if kind == "host changes during metadata" {
				t.Setenv("SUDO_UID", os.Getenv("SUDO_UID"))
			}
			dir, err := os.MkdirTemp("", "c1-change-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			path := filepath.Join(dir, "p.sock")
			listener, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			var changed atomic.Bool
			var posts atomic.Int32
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					posts.Add(1)
					w.WriteHeader(500)
					return
				}
				if strings.HasPrefix(r.URL.Path, "/v1.44/images/") {
					if changed.Load() && kind == "image unavailable at create" {
						w.WriteHeader(404)
						return
					}
					_, _ = w.Write(imageData)
					return
				}
				if r.URL.Path == "/version" {
					version := "6.2.0"
					if changed.Load() && kind == "changed engine" {
						version = "6.3.0"
					}
					fmt.Fprintf(w, `{"Version":%q,"Os":"linux","Components":[{"Name":"Podman Engine","Version":%q}]}`, version, version)
					return
				}
				if changed.Load() && kind == "metadata unavailable" {
					w.WriteHeader(500)
					io.WriteString(w, "private-environment-value")
					return
				}
				data := string(podmanSameIDInfo(os.Geteuid(), os.Getegid()))
				if changed.Load() && kind == "host changes during metadata" {
					if err := os.Setenv("SUDO_UID", "0"); err != nil {
						t.Error(err)
					}
				}
				if changed.Load() && kind == "changed parent" {
					data = strings.ReplaceAll(data, `"container_id":1`, `"container_id":2`)
				}
				io.WriteString(w, data)
			})}
			go server.Serve(listener)
			defer server.Close()
			spec.Env = []string{"DOCKER_HOST=unix://" + path, "DOCKER_CONTEXT="}
			p, err := prepareMappedPodmanCreateV1(t.Context(), ops, pending, active, operationID, spec)
			if err != nil {
				t.Fatal("valid preparation fixture", err)
			}
			if kind == "changed generated policy" {
				i := slices.Index(p.spec.Args, "--read-only")
				p.spec.Args = slices.Delete(p.spec.Args, i, i+1)
			}
			changed.Store(true)
			ctx := t.Context()
			if kind == "changed host authority" {
				t.Setenv("SUDO_UID", "0")
			}
			if kind == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err = p.create(ctx, func(CommandSpec, RunOptions) error { t.Error("rejected dispatch invoked cleanup"); return nil })
			if err == nil || posts.Load() != 0 || strings.Contains(err.Error(), "private-environment-value") {
				t.Fatalf("unsafe dispatch: err=%v POSTs=%d", err, posts.Load())
			}
		})
	}
}

// A selected incompatible host must fail before contacting the endpoint.
func TestMappedPodmanCreateRejectsMissingAuthorityBeforeEngineV1(t *testing.T) {
	if _, err := prepareMappedPodmanCreateV1(t.Context(), deploy.IdentityMappingOperationsV1{}, deploy.StateV1{}, mappedGeometryFixtureV1("exact"), "unowned", CommandSpec{Env: []string{"DOCKER_HOST=unix://" + filepath.Join(t.TempDir(), "absent.sock")}}); err == nil {
		t.Fatal("unowned create preparation succeeded")
	}
}
