package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/identitymapping"
	"github.com/omry/reploy/internal/mappedlaunch"
)

type mappedWaiterProcessV1 interface {
	fresh() error
	close()
}

type mappedWaiterBackendV1 struct {
	run     commandRunner
	pin     func(int, identitymapping.Geometry, []string) (mappedWaiterProcessV1, error)
	observe func(context.Context) (*podmanHealthcheckClient, string, error)
	create  func(*preparedMappedPodmanCreateV1, context.Context, commandRunner) (string, error)
	admit   func(deploy.IdentityMappingOperationsV1, context.Context, deploy.StateV1, string) (deploy.StateV1, error)
}

// ownedMappedWaiterV1 owns an invocation-local attempt, not an admission token.
// Its caller retains the lock through release, then owns lifetime/cleanup (E3).
type ownedMappedWaiterV1 struct {
	prepared  *preparedMappedPodmanCreateV1
	admission context.Context
	binding   mappedlaunch.Binding
	container string
	pid       int
	process   mappedWaiterProcessV1
	input     *io.PipeWriter
	reader    *io.PipeReader
	cancel    context.CancelFunc
	done      chan error
	backend   mappedWaiterBackendV1
	mu        sync.Mutex
	consumed  bool
	closed    bool
}

// startOwnedMappedWaiterV1 is internal plumbing. Existing public guards remain;
// no production F verifier or E2 transition is supplied by this constructor.
func startOwnedMappedWaiterV1(ctx context.Context, p *preparedMappedPodmanCreateV1, binding mappedlaunch.Binding, options RunOptions) (*ownedMappedWaiterV1, error) {
	return startMappedWaiterWithBackendV1(ctx, p, binding, options, mappedWaiterBackendV1{
		run: runCommand, pin: pinMappedProcessV1, observe: p.observe,
		create: (*preparedMappedPodmanCreateV1).create,
		admit:  deploy.IdentityMappingOperationsV1.AdmitNativeExecution,
	})
}

func startMappedWaiterWithBackendV1(ctx context.Context, p *preparedMappedPodmanCreateV1, b mappedlaunch.Binding, options RunOptions, backend mappedWaiterBackendV1) (*ownedMappedWaiterV1, error) {
	if ctx == nil || options.Context == nil || p == nil || backend.run == nil || backend.pin == nil || backend.observe == nil || backend.create == nil || backend.admit == nil {
		return nil, fmt.Errorf("mapped waiter requires its owned preparation and complete backend")
	}
	deadline, bounded := ctx.Deadline()
	if !bounded || b.Validate() != nil || ctx.Err() != nil || options.Context.Err() != nil {
		return nil, fmt.Errorf("mapped waiter requires a valid binding and admission deadline")
	}
	m := p.state.IdentityMapping
	body, name, err := podmanApplicationCreateRequest(p.spec, p.namespace)
	if err != nil || body.Tty || len(body.Cmd) == 0 || body.Cmd[0] != "sandbox-exec" {
		return nil, fmt.Errorf("mapped waiter requires qualified non-TTY sandbox stdin transport")
	}
	if m == nil || m.PendingOperation == nil || m.PendingOperation.Native == nil || p.state.Current == nil ||
		b.Installation != m.InstallationID || b.Operation != p.operation || b.Generation != p.state.Current.BuildLockDigest || b.Revision != m.Revision ||
		m.PendingOperation.Native.Engine != p.endpoint || m.PendingOperation.Native.Process != name {
		return nil, fmt.Errorf("mapped waiter differs from its exact native journal owner")
	}
	client, namespace, err := backend.observe(ctx)
	if err != nil {
		return nil, err
	}
	defer client.http.CloseIdleConnections()
	image, err := p.verifyImage(ctx, client)
	if err != nil || namespace != p.namespace || client.version != p.version || string(b.Image) != image {
		return nil, fmt.Errorf("mapped waiter image or engine binding changed: %v", err)
	}
	encoded, _ := canonical.Marshal(b) // Binding.Validate permits canonical values only.
	copy := *p
	copy.spec.Args = slices.Clone(p.spec.Args)
	entry := slices.Index(copy.spec.Args, "--entrypoint")
	prefix := slices.Clone(copy.spec.Args[:entry])
	if !body.OpenStdin {
		prefix = append(prefix, "--interactive")
	}
	for key, value := range map[string]string{"io.reploy.native-operation": b.Operation, "io.reploy.native-installation": b.Installation} {
		if _, exists := body.Labels[key]; exists {
			return nil, fmt.Errorf("mapped waiter owner label already supplied")
		}
		prefix = append(prefix, "--label", key+"="+value)
	}
	copy.spec.Args = append(prefix, copy.spec.Args[entry:entry+3]...)
	command := append([]string{mappedlaunch.Command, string(encoded), deadline.UTC().Format(time.RFC3339Nano)}, body.Cmd...)
	copy.spec.Args = append(copy.spec.Args, command...)
	runContext, cancel := context.WithCancel(options.Context)
	r, w := io.Pipe()
	a := &ownedMappedWaiterV1{prepared: &copy, admission: ctx, binding: b, input: w, reader: r, cancel: cancel, backend: backend, done: make(chan error, 1)}
	a.container, err = backend.create(&copy, ctx, backend.run)
	if err != nil {
		cancel()
		_ = r.Close()
		_ = w.Close()
		return nil, err // C2 retains uncertain create ownership; never retry.
	}
	start := copy.spec
	start.Args = []string{"start", "--attach", "--interactive", a.container}
	options.Context = runContext
	if options.Stdin == nil {
		options.Stdin = bytes.NewReader(nil)
	}
	options.Stdin = io.MultiReader(r, options.Stdin)
	go func() {
		err := backend.run(start, options)
		_ = r.CloseWithError(err)
		a.done <- err
	}()
	for {
		pid, err := a.observeContainer(ctx, client)
		if err != nil {
			return nil, errors.Join(err, a.close())
		}
		if pid > 0 {
			a.pid = pid
			a.process, err = backend.pin(pid, p.active, append([]string{"/reploy-probe"}, command...))
			if err != nil {
				return nil, errors.Join(err, a.close())
			}
			return a, nil
		}
		select {
		case err := <-a.done:
			return nil, errors.Join(fmt.Errorf("mapped waiter exited before observation: %v", err), a.close())
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), a.close())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (a *ownedMappedWaiterV1) observeContainer(ctx context.Context, client *podmanHealthcheckClient) (int, error) {
	data, err := client.request(ctx, http.MethodGet, "/v1.44/containers/"+a.container+"/json", nil)
	var object struct {
		ID, Image    string
		RestartCount *int
		Config       struct {
			Labels          map[string]string
			Entrypoint, Cmd []string
			Tty             *bool
		}
		State struct {
			Running bool
			Pid     int
		}
		HostConfig struct{ RestartPolicy *struct{ Name string } }
	}
	entry := slices.Index(a.prepared.spec.Args, "--entrypoint")
	if err != nil || json.Unmarshal(data, &object) != nil || object.ID != a.container || strings.TrimPrefix(object.Image, "sha256:") != strings.TrimPrefix(string(a.binding.Image), "sha256:") || object.Config.Tty == nil || *object.Config.Tty ||
		object.Config.Labels["io.reploy.native-operation"] != a.binding.Operation || object.Config.Labels["io.reploy.native-installation"] != a.binding.Installation ||
		!slices.Equal(object.Config.Entrypoint, []string{"/reploy-probe"}) || !slices.Equal(object.Config.Cmd, a.prepared.spec.Args[entry+3:]) ||
		object.RestartCount == nil || *object.RestartCount != 0 || object.HostConfig.RestartPolicy == nil || object.HostConfig.RestartPolicy.Name != "" && object.HostConfig.RestartPolicy.Name != "no" {
		return 0, fmt.Errorf("mapped waiter runtime identity or inhibited command changed")
	}
	if !object.State.Running {
		return 0, nil
	}
	if object.State.Pid <= 0 {
		return 0, fmt.Errorf("running mapped waiter has no exact host PID")
	}
	return object.State.Pid, nil
}

type mappedWaiterEvidenceV1 struct {
	deploy.IdentityMappingEvidenceV1
	verify func(context.Context) error
}

func (e mappedWaiterEvidenceV1) Verify(ctx context.Context, state deploy.StateV1) error {
	// Run the potentially slow storage/network check first, then refresh the
	// actual inhibited process immediately before the existing atomic writer.
	if err := e.IdentityMappingEvidenceV1.Verify(ctx, state); err != nil {
		return err
	}
	// This is the existing trusted effect-owner/F proof seam, not a boolean
	// qualification flag. No implementation is manufactured by E1.3.
	return e.verify(ctx)
}

func (a *ownedMappedWaiterV1) release() (acceptedState deploy.StateV1, failure error) {
	ctx := a.admission
	a.mu.Lock()
	cleanupOnFailure := !a.consumed && !a.closed
	defer func() {
		a.mu.Unlock()
		if failure != nil && cleanupOnFailure {
			failure = errors.Join(failure, a.close())
		}
	}()
	if ctx == nil || a.consumed || a.closed || a.process == nil || a.prepared.ops.Evidence == nil {
		return deploy.StateV1{}, fmt.Errorf("mapped release requires its unconsumed owned waiter and effect proof")
	}
	a.consumed = true // Even a failed/uncertain admission cannot resend on this attempt.
	p := a.prepared
	client, namespace, err := a.backend.observe(ctx)
	if err != nil {
		return deploy.StateV1{}, err
	}
	defer client.http.CloseIdleConnections()
	image, err := p.verifyImage(ctx, client)
	if err != nil || image != string(a.binding.Image) || namespace != p.namespace || client.version != p.version {
		return deploy.StateV1{}, fmt.Errorf("mapped release image or engine changed: %v", err)
	}
	// Read the selected immutable plan outside the admission writer. Fresh F
	// observations and the final process fence run inside its existing proof.
	sandbox, err := a.sandboxRequest()
	if err != nil {
		return deploy.StateV1{}, err
	}
	verify := func(ctx context.Context) error {
		if err := verifyMappedSandboxV1(ctx, p.state, p.ops.Evidence, sandbox); err != nil {
			return err
		}
		pid, err := a.observeContainer(ctx, client)
		if err != nil || pid != a.pid {
			return fmt.Errorf("mapped waiter is no longer the pinned process: %v", err)
		}
		return a.process.fresh()
	}
	ops := p.ops
	ops.Evidence = mappedWaiterEvidenceV1{ops.Evidence, verify}
	accepted, err := a.backend.admit(ops, ctx, p.state, p.operation)
	if err != nil {
		return deploy.StateV1{}, err
	}
	if err := ctx.Err(); err != nil {
		return accepted, err
	}
	if err := ops.Operation.RequireHeld(); err != nil {
		return accepted, err
	}
	if err := a.process.fresh(); err != nil {
		return accepted, err
	}
	frame, err := mappedlaunch.Frame(mappedlaunch.Request{Binding: a.binding, Container: a.container})
	if err != nil {
		return accepted, err
	}
	stop := context.AfterFunc(ctx, func() { _ = a.input.CloseWithError(ctx.Err()) })
	defer stop()
	if n, err := a.input.Write(frame); err != nil || n != len(frame) {
		return accepted, fmt.Errorf("mapped release is uncertain: %w", err)
	}
	// No control parser remains after this one frame. Later stdin is ordinary
	// application data; copied frames have no control meaning.
	return accepted, a.input.Close()
}

func (a *ownedMappedWaiterV1) close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	a.cancel()
	_ = a.reader.Close()
	_ = a.input.Close()
	if a.process != nil {
		a.process.close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// Only this invocation's exact successful create ID is ever removed. Errors
	// retain the native journal and reservations; absence is not recovery proof.
	return removeCreatedPodmanContainer(ctx, a.prepared.spec, a.container, a.backend.run)
}
