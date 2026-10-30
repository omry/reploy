package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/identitymapping"
	"github.com/omry/reploy/internal/mappedlaunch"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
)

type waiterEffectFixtureV1 struct {
	fail           bool
	calls          int
	sandboxFailure error
	sandboxChange  func(*mappedSandboxProofV1)
}

func (e *waiterEffectFixtureV1) MappedSandbox(_ context.Context, _ deploy.StateV1, request mappedSandboxRequestV1) (mappedSandboxProofV1, error) {
	digest, err := canonical.Sum("mapped-sandbox", "v1", request)
	if err != nil {
		return mappedSandboxProofV1{}, err
	}
	code := make([]mappedCodePathV1, len(request.RequiredCode))
	for i, name := range request.RequiredCode {
		code[i] = mappedCodePathV1{name, name}
	}
	proof := mappedSandboxProofV1{Request: digest, NetworkPolicy: request.NetworkPolicy, ReadOnlyRoot: true,
		KernelPolicy: request.KernelPolicy, KernelFacts: request.KernelFacts,
		Code: code, ObservedCode: slices.Clone(code), Mounts: []mappedSandboxMountV1{}}
	if e.sandboxChange != nil {
		e.sandboxChange(&proof)
	}
	return proof, e.sandboxFailure
}

func (*waiterEffectFixtureV1) Plan(context.Context, deploy.StateV1, identitymapping.Geometry) ([]string, error) {
	return nil, nil
}
func (*waiterEffectFixtureV1) Unused(context.Context, deploy.StateV1) error {
	return errors.New("not an unused-intent proof")
}
func (e *waiterEffectFixtureV1) Verify(context.Context, deploy.StateV1) error {
	e.calls++
	if e.fail {
		return errors.New("F storage/network proof missing")
	}
	return nil
}

type waiterProcessFixtureV1 struct {
	fail           bool
	checks, closed int
}

func (p *waiterProcessFixtureV1) fresh() error {
	p.checks++
	if p.fail {
		return errors.New("kernel process fence changed")
	}
	return nil
}
func (p *waiterProcessFixtureV1) close() { p.closed++ }

// Only orchestration is injected. The test retains real image/selected-state
// verification and operation lock. E1.2 separately tests the actual atomic
// admission writer; these injected successes grant no runtime/F qualification.
type waiterFixtureV1 struct {
	prepared                                                           *preparedMappedPodmanCreateV1
	binding                                                            mappedlaunch.Binding
	backend                                                            mappedWaiterBackendV1
	process                                                            *waiterProcessFixtureV1
	effect                                                             *waiterEffectFixtureV1
	client                                                             *podmanHealthcheckClient
	ctx                                                                context.Context
	cancelAdmission                                                    context.CancelFunc
	options                                                            RunOptions
	created, removed, admissions                                       int
	mu                                                                 sync.Mutex
	body                                                               podmanApplicationCreate
	id                                                                 string
	pid                                                                int
	objectChange                                                       func(map[string]any)
	inspectFailure, stopped, createFailure, cleanupFailure, pinFailure bool
	observeFailure                                                     bool
	observes                                                           int
	application                                                        atomic.Int32
	expectedInput                                                      string
	frame                                                              chan mappedlaunch.Request
	startErr                                                           chan error
}

type trackedWaiterInputV1 struct {
	f *waiterFixtureV1
	io.Reader
}

func (r trackedWaiterInputV1) Read(b []byte) (int, error) {
	r.f.application.Add(1)
	return r.Reader.Read(b)
}

func newWaiterFixtureV1(t *testing.T) *waiterFixtureV1 {
	t.Helper()
	dir, operation, _, lock, state := currentBuildFixture(t, true)
	t.Cleanup(func() { _ = operation.Unlock() })
	f := &waiterFixtureV1{id: strings.Repeat("b", 64), pid: 1234, expectedInput: "application-input\n", effect: &waiterEffectFixtureV1{}, process: &waiterProcessFixtureV1{}, frame: make(chan mappedlaunch.Request, 1), startErr: make(chan error, 1)}
	f.client = newPodmanApplicationClient(t, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/v1.44/images/") {
			_ = json.NewEncoder(w).Encode(mappedImageInspectionFixtureV1(string(lock.FinalImage.ConfigDigest)))
			return
		}
		if f.inspectFailure {
			w.WriteHeader(500)
			return
		}
		object := map[string]any{"Id": f.id, "Image": f.binding.Image, "RestartCount": 0, "Config": map[string]any{"Labels": f.body.Labels, "Entrypoint": f.body.Entrypoint, "Cmd": f.body.Cmd, "Tty": f.body.Tty}, "State": map[string]any{"Running": !f.stopped, "Pid": f.pid}, "HostConfig": map[string]any{"RestartPolicy": map[string]any{"Name": "no", "MaximumRetryCount": 0}, "AutoRemove": false, "CapAdd": f.body.HostConfig.CapAdd, "CapDrop": f.body.HostConfig.CapDrop, "SecurityOpt": f.body.HostConfig.SecurityOpt}}
		if f.objectChange != nil {
			f.objectChange(object)
		}
		_ = json.NewEncoder(w).Encode(object)
	})
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	state.BlueprintSource = "private E1.3 orchestration fixture; not qualified runtime evidence"
	spec := sameIDApplicationSpec(t)
	spec.Args = slices.DeleteFunc(spec.Args, func(s string) bool { return s == "--tty" })
	entry := slices.Index(spec.Args, "--entrypoint")
	// Independent trusted startup input, checked against the compiled policy.
	spec.Args = slices.Insert(spec.Args, entry+4, "--mapped-capabilities=0000000000000000")
	spec.Args[entry+2] = string(lock.FinalImage.ConfigDigest)
	name := spec.Args[slices.Index(spec.Args, "--name")+1]
	empty := []deploy.IdentityMappingIntervalV1{}
	geometry := deploy.IdentityMappingGeometryV1{Profile: "exact", UIDs: []deploy.IdentityMappingIntervalV1{{ContainerStart: "0", HostStart: "100000", Count: "1"}}, GIDs: []deploy.IdentityMappingIntervalV1{{ContainerStart: "0", HostStart: "100000", Count: "1"}}}
	state.IdentityMapping = &deploy.IdentityMappingStateV1{Schema: deploy.IdentityMappingStateSchemaV2, ProcessModel: deploy.IdentityMappingNativeProcessModelV1, InstallationID: "installation", OwnerDir: dir, Revision: "1", OperatorUID: "1000", OperatorGID: "1000", Profile: "exact", AcceptedUIDs: &empty, AcceptedGIDs: &empty, RetainedStorage: []string{}, PendingOperation: &deploy.IdentityMappingOperationV1{OperationID: "operation", Kind: "native-execute", ExpectedRevision: "1", ExpectedGeneration: state.Current, Candidate: geometry, OwnedObjects: []string{}, Phase: "effects-pending", Inhibited: true, Native: &deploy.NativeIdentityOperationV1{Active: geometry, Engine: f.client.endpoint, Process: name, RuntimeUsers: []string{}, RuntimeGroups: []string{}}}}
	encoded, err := deploy.EncodeStateV1(state)
	if err != nil {
		t.Fatal("native fixture", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(operation.Path()), "state.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	f.prepared = &preparedMappedPodmanCreateV1{ops: deploy.IdentityMappingOperationsV1{Operation: operation, Evidence: f.effect, ValidateProfileOwner: registry.ValidateRequirementProfileV1}, state: state, active: identitymapping.Geometry{Profile: "exact", UIDs: []identitymapping.Interval{{HostStart: 100000, Count: 1}}, GIDs: []identitymapping.Interval{{HostStart: 100000, Count: 1}}}, operation: "operation", spec: spec, endpoint: f.client.endpoint, version: f.client.version, namespace: "fixture-namespace"}
	f.prepared.policy = mappedPolicyForSpecFixtureV1(t, spec, state.Platform.Architecture, 0)
	f.binding = mappedlaunch.Binding{Schema: "mapped-release-v1", Installation: "installation", Operation: "operation", Generation: state.Current.BuildLockDigest, Revision: "1", Image: lock.FinalImage.ConfigDigest, Role: "app", Plan: rendererDigest("c")}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	t.Cleanup(cancel)
	f.ctx = ctx
	f.cancelAdmission = cancel
	f.options = RunOptions{Context: t.Context(), Stdin: trackedWaiterInputV1{f, strings.NewReader("application-input\n")}}
	f.backend = mappedWaiterBackendV1{
		observe: func(ctx context.Context) (*podmanHealthcheckClient, string, error) {
			f.observes++
			if f.observeFailure {
				return nil, "", errors.New("engine observation changed")
			}
			return f.client, f.prepared.namespace, ctx.Err()
		},
		create: func(p *preparedMappedPodmanCreateV1, ctx context.Context, _ commandRunner) (string, error) {
			f.created++
			if f.createFailure {
				return "", errors.New("uncertain create; journal retained")
			}
			request, err := p.request()
			if err != nil {
				return "", err
			}
			f.mu.Lock()
			f.body = request.body
			f.mu.Unlock()
			return f.id, ctx.Err()
		},
		pin: func(pid int, active identitymapping.Geometry, argv []string) (mappedWaiterProcessV1, error) {
			if f.pinFailure {
				return nil, errors.New("kernel observation unproved")
			}
			if pid != f.pid || !slices.Equal(argv[1:], f.body.Cmd) || argv[0] != "/reploy-probe" || !slices.Equal(active.UIDs, f.prepared.active.UIDs) {
				return nil, errors.New("wrong trusted process plan")
			}
			return f.process, nil
		},
		admit: func(ops deploy.IdentityMappingOperationsV1, ctx context.Context, s deploy.StateV1, id string) (deploy.StateV1, error) {
			if id != "operation" || f.application.Load() != 0 {
				return deploy.StateV1{}, errors.New("wrong operation or early application stdin")
			}
			if err := ops.Operation.RequireHeld(); err != nil {
				return deploy.StateV1{}, err
			}
			if err := ops.Evidence.Verify(ctx, s); err != nil {
				return deploy.StateV1{}, err
			}
			f.admissions++
			return s, nil
		},
		run: func(spec CommandSpec, options RunOptions) error {
			if slices.Equal(spec.Args, []string{"container", "rm", "--force", f.id}) {
				f.removed++
				if options.Context.Err() != nil {
					return errors.New("cleanup lost independent context")
				}
				if f.cleanupFailure {
					return errors.New("owned cleanup failed")
				}
				return nil
			}
			if !slices.Equal(spec.Args, []string{"start", "--attach", "--interactive", f.id}) {
				return fmt.Errorf("unexpected command %v", spec.Args)
			}
			request, err := mappedlaunch.Read(options.Stdin, f.binding)
			if err == nil {
				f.frame <- request
				var data []byte
				data, err = io.ReadAll(options.Stdin)
				if err == nil && !bytes.Equal(data, []byte(f.expectedInput)) {
					err = fmt.Errorf("application stdin changed: %q", data)
				}
			}
			f.startErr <- err
			return err
		},
	}
	return f
}

func (f *waiterFixtureV1) start(t *testing.T) *ownedMappedWaiterV1 {
	t.Helper()
	a, err := startMappedWaiterWithBackendV1(f.ctx, f.prepared, f.binding, f.options, f.backend)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.close() })
	return a
}

func TestOwnedMappedWaiterReleasesExactlyOnceAfterProofV1(t *testing.T) {
	f := newWaiterFixtureV1(t)
	a := f.start(t)
	if f.application.Load() != 0 || f.admissions != 0 || f.removed != 0 {
		t.Fatal("application or admission ran before release")
	}
	if _, err := a.release(); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-f.frame:
		if request.Container != f.id || request.Binding != f.binding {
			t.Fatalf("wrong frame %+v", request)
		}
	case <-f.ctx.Done():
		t.Fatal("release not received")
	}
	select {
	case err := <-f.startErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-f.ctx.Done():
		t.Fatal("application stdin not delivered")
	}
	if f.effect.calls != 1 || f.admissions != 1 || f.process.checks != 2 || f.application.Load() == 0 {
		t.Fatal("release bypassed proof or stdin handoff")
	}
	if _, err := a.release(); err == nil {
		t.Fatal("duplicate release permitted")
	}
	if f.removed != 0 {
		t.Fatal("duplicate request cleaned a successfully released instance")
	}
	if err := a.close(); err != nil {
		t.Fatal(err)
	}
	if err := a.close(); err != nil {
		t.Fatal(err)
	}
	if f.created != 1 || f.removed != 1 || f.process.closed != 1 {
		t.Fatal("owned resources not closed exactly once")
	}
}

func TestOwnedMappedWaiterHeldOperationLockEvidenceV1(t *testing.T) {
	if os.Getenv("REPLOY_MAPPED_WAITER_HELD_LOCK_HELPER") == "1" {
		for _, cancel := range []bool{false, true} {
			t.Run(map[bool]string{false: "completion", true: "cancelled"}[cancel], func(t *testing.T) {
				f := newWaiterFixtureV1(t)
				a := f.start(t)
				_, _, _, heldLock, _ := newPreparedAPTGraphReuseFixture(t)
				var callbackSeen atomic.Int32
				a.backend.admit = func(ops deploy.IdentityMappingOperationsV1, ctx context.Context, state deploy.StateV1, id string) (deploy.StateV1, error) {
					if id != "operation" || state.Current == nil {
						return deploy.StateV1{}, errors.New("held-lock fixture received the wrong admission")
					}
					validate := func(providers.RequirementProfile) error {
						callbackSeen.Add(1)
						if cancel {
							f.cancelAdmission()
						}
						return ops.Evidence.Verify(ctx, state)
					}
					if _, err := ops.Operation.PublishBuildLock(heldLock, validate); err != nil {
						return deploy.StateV1{}, fmt.Errorf("held-lock build validation: %w", err)
					}
					return state, nil
				}
				released := make(chan error, 1)
				go func() { _, err := a.release(); released <- err }()
				select {
				case err := <-released:
					if cancel {
						if err == nil {
							t.Fatal("cancelled held-lock admission succeeded")
						}
						if f.removed != 1 || f.process.closed != 1 || f.application.Load() != 0 {
							t.Fatalf("cancelled admission was not failure-closed: err=%v removed=%d closed=%d application=%d", err, f.removed, f.process.closed, f.application.Load())
						}
						select {
						case <-f.frame:
							t.Fatal("cancelled held-lock admission delivered a frame")
						default:
						}
					} else if err != nil {
						t.Fatal(err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("held-lock evidence callback did not complete boundedly")
				}
				if callbackSeen.Load() == 0 {
					t.Fatal("held-lock build validator did not invoke evidence")
				}
				if !cancel {
					select {
					case <-f.frame:
					case <-time.After(2 * time.Second):
						t.Fatal("successful held-lock admission did not send a frame")
					}
				}
			})
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOwnedMappedWaiterHeldOperationLockEvidenceV1$", "-test.v")
	command.Env = append(os.Environ(), "REPLOY_MAPPED_WAITER_HELD_LOCK_HELPER=1")
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("held-lock regression exceeded its bound: %v\n%s", ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("held-lock regression helper failed: %v\n%s", err, output)
	}
}

func TestOwnedMappedWaiterKeepsOriginalAdmissionContextV1(t *testing.T) {
	f := newWaiterFixtureV1(t)
	deadline, _ := f.ctx.Deadline()
	original := f.backend.admit
	f.backend.admit = func(ops deploy.IdentityMappingOperationsV1, ctx context.Context, state deploy.StateV1, id string) (deploy.StateV1, error) {
		actual, bounded := ctx.Deadline()
		if !bounded || !actual.Equal(deadline) {
			return deploy.StateV1{}, errors.New("original admission deadline lost")
		}
		return original(ops, ctx, state, id)
	}
	a := f.start(t)
	if _, err := a.release(); err != nil {
		t.Fatal(err)
	}
}

func TestOwnedMappedWaiterCancelsBlockedFrameWithOriginalContextV1(t *testing.T) {
	f := newWaiterFixtureV1(t)
	partial := make(chan struct{})
	original := f.backend.run
	f.backend.run = func(spec CommandSpec, options RunOptions) error {
		if spec.Args[0] != "start" {
			return original(spec, options)
		}
		if _, err := io.ReadFull(options.Stdin, make([]byte, 7)); err != nil {
			return err
		}
		close(partial)
		<-options.Context.Done()
		return options.Context.Err()
	}
	a := f.start(t)
	released := make(chan error, 1)
	go func() { _, err := a.release(); released <- err }()
	select {
	case <-partial:
	case <-f.ctx.Done():
		t.Fatal("attach never consumed the partial release frame")
	}
	// The application lifetime context remains live. Only cancelling the
	// original admission context can unblock this incomplete frame write.
	f.cancelAdmission()
	select {
	case err := <-released:
		if err == nil || !strings.Contains(err.Error(), "uncertain") {
			t.Fatalf("partial release cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		_ = a.reader.Close()
		<-released
		t.Fatal("original cancellation did not unblock the release frame")
	}
	if f.admissions != 1 || f.removed != 1 || f.application.Load() != 0 {
		t.Fatal("partial cancelled release lost ownership or sent application stdin")
	}
}

func TestOwnedMappedWaiterRejectsBeforeCreateV1(t *testing.T) {
	for _, mode := range []string{"nil context", "nil preparation", "nil lifetime", "no deadline", "bad binding", "TTY", "not sandbox", "installation", "operation", "generation", "revision", "image", "engine owner", "process owner", "owner label", "missing native", "observe", "engine version", "namespace", "backend", "create"} {
		t.Run(mode, func(t *testing.T) {
			f := newWaiterFixtureV1(t)
			ctx := f.ctx
			p := f.prepared
			b := f.binding
			options := f.options
			backend := f.backend
			switch mode {
			case "nil context":
				ctx = nil
			case "nil preparation":
				p = nil
			case "nil lifetime":
				options.Context = nil
			case "no deadline":
				ctx = t.Context()
			case "bad binding":
				b.Schema = "wrong"
			case "TTY":
				entry := slices.Index(p.spec.Args, "--entrypoint")
				p.spec.Args = append(append(slices.Clone(p.spec.Args[:entry]), "--tty"), p.spec.Args[entry:]...)
			case "not sandbox":
				entry := slices.Index(p.spec.Args, "--entrypoint")
				p.spec.Args[entry+3] = "verify-exec"
			case "installation":
				b.Installation = "other"
			case "operation":
				b.Operation = "other"
			case "generation":
				b.Generation = rendererDigest("d")
			case "revision":
				b.Revision = "2"
			case "image":
				b.Image = rendererDigest("d")
			case "engine owner":
				p.state.IdentityMapping.PendingOperation.Native.Engine = "other"
			case "process owner":
				p.state.IdentityMapping.PendingOperation.Native.Process = "other"
			case "missing native":
				p.state.IdentityMapping.PendingOperation.Native = nil
			case "owner label":
				entry := slices.Index(p.spec.Args, "--entrypoint")
				p.spec.Args = append(append(slices.Clone(p.spec.Args[:entry]), "--label", "io.reploy.native-operation=operation"), p.spec.Args[entry:]...)
			case "observe":
				f.observeFailure = true
			case "engine version":
				p.version = "other"
			case "namespace":
				p.namespace = "other"
				backend.observe = func(context.Context) (*podmanHealthcheckClient, string, error) {
					return f.client, "fixture-namespace", nil
				}
			case "backend":
				backend.pin = nil
			case "create":
				f.createFailure = true
			}
			a, err := startMappedWaiterWithBackendV1(ctx, p, b, options, backend)
			if err == nil {
				_ = a.close()
				t.Fatal("unproved waiter created")
			}
			if mode != "create" && f.created != 0 {
				t.Fatal("rejected plan reached create")
			}
			if f.admissions != 0 || f.application.Load() != 0 {
				t.Fatal("rejected plan released payload")
			}
		})
	}
}

func TestOwnedMappedWaiterReleaseFailuresRetainJournalAndCleanupV1(t *testing.T) {
	for _, mode := range []string{"F missing", "no effect owner", "process changed", "runtime PID changed", "runtime stopped", "image changed", "state changed", "engine unavailable", "admission failed", "released lock", "cancelled", "disconnect", "disconnect after header", "cleanup failed", "cancel after admission", "unlock after admission", "process changes after admission"} {
		t.Run(mode, func(t *testing.T) {
			f := newWaiterFixtureV1(t)
			if mode == "disconnect after header" {
				original := f.backend.run
				f.backend.run = func(s CommandSpec, o RunOptions) error {
					if s.Args[0] == "start" {
						_, err := io.ReadFull(o.Stdin, make([]byte, 7))
						if err != nil {
							return err
						}
						return errors.New("attach disconnected after partial frame")
					}
					return original(s, o)
				}
			}
			a := f.start(t)
			switch mode {
			case "F missing", "cleanup failed":
				f.effect.fail = true
				f.cleanupFailure = mode == "cleanup failed"
			case "no effect owner":
				a.prepared.ops.Evidence = nil
			case "process changed":
				f.process.fail = true
			case "runtime PID changed":
				f.mu.Lock()
				f.pid++
				f.mu.Unlock()
			case "runtime stopped":
				f.mu.Lock()
				f.stopped = true
				f.mu.Unlock()
			case "image changed":
				a.binding.Image = rendererDigest("d")
			case "state changed":
				a.prepared.state.BlueprintSource = "stale"
			case "engine unavailable":
				f.observeFailure = true
			case "admission failed":
				a.backend.admit = func(deploy.IdentityMappingOperationsV1, context.Context, deploy.StateV1, string) (deploy.StateV1, error) {
					return deploy.StateV1{}, errors.New("uncertain atomic write")
				}
			case "released lock":
				_ = f.prepared.ops.Operation.Unlock()
			case "cancelled":
				f.cancelAdmission()
			case "disconnect":
				_ = a.reader.Close()
			case "cancel after admission", "unlock after admission", "process changes after admission":
				original := a.backend.admit
				cancel := f.cancelAdmission
				a.backend.admit = func(ops deploy.IdentityMappingOperationsV1, ctx context.Context, state deploy.StateV1, id string) (deploy.StateV1, error) {
					result, err := original(ops, ctx, state, id)
					if err != nil {
						return result, err
					}
					switch mode {
					case "cancel after admission":
						cancel()
					case "unlock after admission":
						_ = ops.Operation.Unlock()
					case "process changes after admission":
						f.process.fail = true
					}
					return result, nil
				}
			}
			if _, err := a.release(); err == nil {
				t.Fatal("failed fence or uncertain release succeeded")
			}
			if f.removed != 1 || f.process.closed != 1 {
				t.Fatal("failed release did not clean exact owned resources")
			}
			if f.application.Load() != 0 {
				t.Fatal("failed release sent application input")
			}
			select {
			case <-f.frame:
				t.Fatal("failed release delivered frame")
			default:
			}
			if _, err := a.release(); err == nil {
				t.Fatal("failed attempt retried")
			}
		})
	}
}

func TestOwnedMappedWaiterRejectsMissingAndChangedRuntimeEvidenceV1(t *testing.T) {
	for _, mode := range []string{"wrong ID", "wrong image", "missing TTY", "TTY", "wrong label", "wrong install", "wrong entrypoint", "wrong command", "restarted", "missing restart count", "restart policy", "missing restart policy", "missing PID", "inspect failure", "pin failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newWaiterFixtureV1(t)
			f.inspectFailure = mode == "inspect failure"
			f.pinFailure = mode == "pin failure"
			f.objectChange = func(o map[string]any) {
				config := o["Config"].(map[string]any)
				host := o["HostConfig"].(map[string]any)
				switch mode {
				case "wrong ID":
					o["Id"] = strings.Repeat("c", 64)
				case "wrong image":
					o["Image"] = rendererDigest("d")
				case "missing TTY":
					delete(config, "Tty")
				case "TTY":
					config["Tty"] = true
				case "wrong label":
					config["Labels"] = map[string]string{"io.reploy.native-operation": "foreign"}
				case "wrong install":
					config["Labels"] = map[string]string{"io.reploy.native-operation": "operation", "io.reploy.native-installation": "foreign"}
				case "wrong entrypoint":
					config["Entrypoint"] = []string{"/bin/sh"}
				case "wrong command":
					config["Cmd"] = []string{"sandbox-exec"}
				case "restarted":
					o["RestartCount"] = 1
				case "missing restart count":
					delete(o, "RestartCount")
				case "restart policy":
					host["RestartPolicy"] = map[string]string{"Name": "always"}
				case "missing restart policy":
					delete(host, "RestartPolicy")
				case "missing PID":
					o["State"].(map[string]any)["Pid"] = 0
				}
			}
			a, err := startMappedWaiterWithBackendV1(f.ctx, f.prepared, f.binding, f.options, f.backend)
			if err == nil {
				_ = a.close()
				t.Fatal("unproved runtime observation accepted")
			}
			if f.created != 1 || f.removed != 1 || f.admissions != 0 || f.application.Load() != 0 {
				t.Fatal("observation failure lost owned cleanup or released payload")
			}
		})
	}
}

func TestOwnedMappedWaiterWaitsBoundedlyAndSupportsConstructedStdinV1(t *testing.T) {
	for _, mode := range []string{"no original stdin", "no original interactive", "unprefixed image", "not running", "start exited", "admission deadline distinct from lifetime"} {
		t.Run(mode, func(t *testing.T) {
			f := newWaiterFixtureV1(t)
			switch mode {
			case "no original stdin":
				f.options.Stdin = nil
				f.expectedInput = ""
			case "no original interactive":
				f.prepared.spec.Args = slices.DeleteFunc(f.prepared.spec.Args, func(s string) bool { return s == "--interactive" })
			case "unprefixed image":
				f.objectChange = func(o map[string]any) { o["Image"] = strings.TrimPrefix(string(f.binding.Image), "sha256:") }
			case "not running":
				f.stopped = true
				ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
				defer cancel()
				f.ctx = ctx
			case "start exited":
				original := f.backend.run
				f.stopped = true
				f.backend.run = func(s CommandSpec, o RunOptions) error {
					if s.Args[0] == "start" {
						return errors.New("attach disconnected")
					}
					return original(s, o)
				}
			case "admission deadline distinct from lifetime":
				original := f.backend.run
				f.backend.run = func(s CommandSpec, o RunOptions) error {
					if s.Args[0] == "start" {
						if _, bounded := o.Context.Deadline(); bounded {
							return errors.New("admission deadline leaked into app lifetime")
						}
					}
					return original(s, o)
				}
			}
			a, err := startMappedWaiterWithBackendV1(f.ctx, f.prepared, f.binding, f.options, f.backend)
			if mode == "not running" || mode == "start exited" {
				if err == nil {
					_ = a.close()
					t.Fatal("wait failure accepted")
				}
				if f.removed != 1 {
					t.Fatal("wait failure lost cleanup")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer a.close()
			if _, err := a.release(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-f.frame:
			case <-f.ctx.Done():
				t.Fatal("missing frame")
			}
			{
				select {
				case err := <-f.startErr:
					if err != nil {
						t.Fatal(err)
					}
				case <-f.ctx.Done():
					t.Fatal("stream did not complete")
				}
			}
		})
	}
	if _, err := startOwnedMappedWaiterV1(context.Background(), nil, mappedlaunch.Binding{}, RunOptions{}); err == nil {
		t.Fatal("production constructor accepted missing ownership")
	}
}
