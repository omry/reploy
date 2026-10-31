package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/identitymapping"
	"github.com/omry/reploy/internal/mappedlaunch"
)

var errMappedMasterExitedV1 = errors.New("required mapped master exited; instance retired")

type mappedLiveOwnedInputV1 struct {
	io.ReadCloser
	stopping atomic.Bool
}

func (input *mappedLiveOwnedInputV1) Read(buffer []byte) (int, error) {
	n, err := input.ReadCloser.Read(buffer)
	if err != nil && input.stopping.Load() {
		return n, io.EOF
	}
	return n, err
}

func (input *mappedLiveOwnedInputV1) Close() error {
	input.stopping.Store(true)
	return input.ReadCloser.Close()
}

type mappedLiveRunBackendV1 struct {
	acquire func(context.Context, string) (*deploy.OperationLock, error)
	start   func(context.Context, deploy.IdentityMappingOperationsV1, deploy.StateV1, identitymapping.Geometry, string, CommandSpec, mappedSeccompPolicyV1, mappedlaunch.Binding, RunOptions) (*ownedMappedWaiterV1, error)
	recover func(deploy.IdentityMappingOperationsV1, context.Context, deploy.StateV1, string) (deploy.StateV1, error)
}

// The caller owns the existing queue-entry lease. This function takes the
// operation lock and retains it through live ownership and the release attempt.
// Enabled application stdin transfers ownership to this private operation: its
// Close must interrupt Read. Borrowed readers are refused before engine effects.
// It is private plumbing: no public mapped guard or F qualification is supplied.
func runMappedLiveInstanceV1(admission context.Context, directory string, ops deploy.IdentityMappingOperationsV1, runID string, plan mappedEntryPlanV1, options RunOptions) error {
	return runMappedLiveInstanceWithBackendV1(admission, directory, ops, runID, plan, options, mappedLiveRunBackendV1{
		acquire: deploy.AcquireOperationLock,
		start: func(ctx context.Context, ops deploy.IdentityMappingOperationsV1, state deploy.StateV1, active identitymapping.Geometry, operation string, spec CommandSpec, policy mappedSeccompPolicyV1, binding mappedlaunch.Binding, options RunOptions) (*ownedMappedWaiterV1, error) {
			p, err := prepareMappedPodmanCreateV1(ctx, ops, state, active, operation, spec, policy)
			if err != nil {
				return nil, err
			}
			return startOwnedMappedWaiterV1(ctx, p, binding, options)
		},
		recover: func(ops deploy.IdentityMappingOperationsV1, ctx context.Context, state deploy.StateV1, operation string) (deploy.StateV1, error) {
			return ops.Recover(ctx, state, operation, false)
		},
	})
}

func runMappedLiveInstanceWithBackendV1(admission context.Context, directory string, ops deploy.IdentityMappingOperationsV1, runID string, plan mappedEntryPlanV1, options RunOptions, backend mappedLiveRunBackendV1) (failure error) {
	operation := ops.Operation
	if operation == nil {
		return fmt.Errorf("mapped instance requires its operation lock")
	}
	defer func() {
		if operation != nil {
			failure = errors.Join(failure, operation.Unlock())
		}
	}()
	if err := operation.RequireWritable(); err != nil {
		return err
	}
	if admission == nil || options.Context == nil || backend.acquire == nil || backend.start == nil || backend.recover == nil {
		return fmt.Errorf("mapped instance requires admission, lifetime and complete backend")
	}
	if _, bounded := admission.Deadline(); !bounded || admission.Err() != nil || options.Context.Err() != nil {
		return fmt.Errorf("mapped instance requires live lifetime and bounded admission")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil || directory == "" || filepath.Dir(filepath.Dir(operation.Path())) != absolute {
		return fmt.Errorf("mapped instance lock belongs to a different deployment")
	}
	facts, err := plan.facts()
	if err != nil {
		return err
	}
	if !facts.Stdin && options.Stdin != nil {
		return fmt.Errorf("mapped instance stdin is disabled by its entry plan")
	}
	var input io.ReadCloser
	if options.Stdin != nil {
		var owned bool
		input, owned = options.Stdin.(io.ReadCloser)
		if !owned {
			return fmt.Errorf("mapped instance stdin requires owned interruptible input")
		}
		input = &mappedLiveOwnedInputV1{ReadCloser: input}
		options.Stdin = input
	}
	stopInput := func() error {
		if input == nil {
			return nil
		}
		owned := input
		input = nil
		return owned.Close()
	}
	defer func() { failure = errors.Join(failure, stopInput()) }()
	kind, err := strconv.Atoi(facts.Kind)
	if err != nil || facts.Mode != strconv.Itoa(int(mappedEntryFreshInstanceV1)) {
		return fmt.Errorf("mapped instance requires a fresh entry plan")
	}
	master := mappedEntryKindV1(kind) == mappedWorkloadStartV1 || mappedEntryKindV1(kind) == mappedHostReplacementV1
	if !master && mappedEntryKindV1(kind) != mappedApplicationCommandV1 && mappedEntryKindV1(kind) != mappedRuntimeTestV1 && mappedEntryKindV1(kind) != mappedLifecycleHookV1 {
		return fmt.Errorf("mapped instance entry kind is unsupported")
	}
	if err := operation.RequireQueueEntryLeaseHeldV1(runID); err != nil {
		return err
	}
	queue, _, err := operation.ReadLiveRunQueueV1()
	if err != nil {
		return err
	}
	run, found := findLiveRunV1(queue, runID)
	if !found || run.Status != deploy.LiveRunStatusActiveV1 || run.Container != "" || run.Name != facts.Command {
		return fmt.Errorf("mapped instance requires its unstarted active live entry")
	}
	status, err := ops.Status()
	if err != nil {
		return err
	}
	state := status.State
	if state.Current == nil || run.GenerationReference != state.Current.Reference || state.Current.BuildLockDigest != facts.Generation || state.IdentityMapping == nil || state.IdentityMapping.InstallationID != facts.Installation || state.IdentityMapping.Revision != facts.Revision {
		return fmt.Errorf("mapped instance generation or mapping changed")
	}
	active := mappedStateGeometryV1(facts.Active)
	pending := state.IdentityMapping.PendingOperation
	if pending == nil || pending.Kind != "native-execute" || pending.Phase != "effects-pending" || !pending.Inhibited || pending.OperationID != facts.Operation || pending.Native == nil || pending.Native.Engine != facts.Engine || pending.Native.Process != facts.Run || !reflect.DeepEqual(pending.Native.Active, facts.Active) {
		return fmt.Errorf("mapped instance differs from native operation owner")
	}
	spec, policy, err := mappedLiveCreateSpecV1(ops, state, absolute, facts)
	if err != nil {
		return err
	}
	waiter, err := backend.start(admission, ops, state, active, facts.Operation, spec, policy, plan.binding, options)
	if err != nil {
		// Create may already have happened. Keep the existing native intent and
		// queue; only the trusted effect owner can reconcile this uncertainty.
		return fmt.Errorf("mapped create/start requires recovery: %w", err)
	}
	if waiter == nil || waiter.binding != plan.binding || waiter.prepared == nil || waiter.prepared.ops.Operation != operation || waiter.container == "" {
		// Never clean an object returned under a different owner.
		return fmt.Errorf("mapped waiter ownership differs; recovery required")
	}
	if err := operation.RecordLiveRunContainerV1(runID, waiter.container); err != nil {
		return errors.Join(fmt.Errorf("record mapped live ownership: %w", err), waiter.close())
	}
	run.Container = waiter.container
	if _, err := waiter.release(); err != nil {
		// release closes on failure, including uncertain frame writes. Its
		// error is retained; a second idempotent close is not cleanup proof.
		return fmt.Errorf("mapped release requires recovery: %w", err)
	}
	if err := operation.Unlock(); err != nil {
		return errors.Join(fmt.Errorf("unlock mapped attached instance: %w", err), waiter.close())
	}
	operation = nil
	completionContext, cancelCompletion := context.WithCancel(options.Context)
	defer cancelCompletion()
	completion := make(chan error, 1)
	go func() { completion <- waitMappedLiveExitV1(completionContext, waiter) }()
	var runErr error
	attachDone, completionDone := false, false
	select {
	case runErr = <-waiter.done:
		attachDone = true
	case runErr = <-completion:
		completionDone = true
	case <-options.Context.Done():
		runErr = options.Context.Err()
	}
	if attachDone && runErr == nil {
		select {
		case runErr = <-completion:
			completionDone = true
		case <-options.Context.Done():
			runErr = options.Context.Err()
		case <-time.After(5 * time.Second):
			runErr = fmt.Errorf("mapped engine completion is unverified")
		}
	}
	// Cmd.Wait can await its stdin copier after the attach child exits. Stop
	// the owned reader independently, rather than requiring application EOF.
	inputErr := stopInput()
	cancelCompletion()
	if completionDone && !attachDone {
		// Let the exited attach drain output after the owned input becomes EOF.
		// Do not cancel it first and silently lose output or its real errors.
		select {
		case attachErr := <-waiter.done:
			attachDone = true
			runErr = errors.Join(runErr, attachErr)
		case <-time.After(5 * time.Second):
			runErr = errors.Join(runErr, fmt.Errorf("mapped attach drain is unverified"))
		}
	}
	// The owned container is the lifetime boundary, including a lost attach.
	// Stop/remove that exact instance before touching queue or journal state.
	if err := waiter.close(); err != nil {
		return errors.Join(runErr, inputErr, fmt.Errorf("mapped exact cleanup requires recovery: %w", err))
	}
	if !attachDone {
		select {
		case attachErr := <-waiter.done:
			if !completionDone {
				runErr = errors.Join(runErr, attachErr)
			}
		case <-time.After(5 * time.Second):
			return errors.Join(runErr, inputErr, fmt.Errorf("mapped attach did not stop; ownership retained"))
		}
	}
	if !completionDone {
		select {
		case <-completion:
		case <-time.After(5 * time.Second):
			return errors.Join(runErr, inputErr, fmt.Errorf("mapped completion observer did not stop; ownership retained"))
		}
	}
	if inputErr != nil {
		return errors.Join(runErr, fmt.Errorf("mapped input closure unverified; ownership retained: %w", inputErr))
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	operation, err = backend.acquire(cleanup, absolute)
	if err != nil {
		return errors.Join(runErr, fmt.Errorf("reacquire mapped completion lock: %w", err))
	}
	if operation == nil {
		return errors.Join(runErr, fmt.Errorf("mapped completion has no operation lock"))
	}
	queue, _, err = operation.ReadLiveRunQueueV1()
	if err != nil {
		return errors.Join(runErr, err)
	}
	currentRun, found := findLiveRunV1(queue, runID)
	if !found || !reflect.DeepEqual(currentRun, run) {
		return errors.Join(runErr, fmt.Errorf("mapped completion owner changed; recovery required"))
	}
	if err := operation.RequireQueueEntryLeaseHeldV1(runID); err != nil {
		return errors.Join(runErr, err)
	}
	ops.Operation = operation
	status, err = ops.Status()
	if err != nil {
		return errors.Join(runErr, err)
	}
	current := status.State
	m := current.IdentityMapping
	if current.Current == nil || !reflect.DeepEqual(current.Current, state.Current) || m == nil || m.InstallationID != facts.Installation || m.PendingOperation == nil || m.PendingOperation.OperationID != facts.Operation || m.PendingOperation.Phase != "release-attempted" || m.PendingOperation.Native == nil || m.PendingOperation.Native.Engine != facts.Engine || m.PendingOperation.Native.Process != facts.Run {
		return errors.Join(runErr, fmt.Errorf("mapped retirement journal changed; recovery required"))
	}
	// An engine removal result alone cannot clear release-attempted. Recover
	// must independently verify the exact trusted effect-owner evidence.
	retired, err := backend.recover(ops, cleanup, current, facts.Operation)
	if err != nil {
		return errors.Join(runErr, fmt.Errorf("mapped retirement unverified; ownership retained: %w", err))
	}
	if retired.IdentityMapping == nil || retired.IdentityMapping.PendingOperation != nil {
		return errors.Join(runErr, fmt.Errorf("mapped retirement did not complete its journal; ownership retained"))
	}
	if _, removed, err := operation.RemoveLiveRunV1(runID); err != nil || !removed {
		return errors.Join(runErr, fmt.Errorf("complete mapped live entry requires recovery: %v", err))
	}
	if master {
		runErr = errors.Join(runErr, errMappedMasterExitedV1)
	}
	return runErr
}

func waitMappedLiveExitV1(ctx context.Context, waiter *ownedMappedWaiterV1) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	status := newBoundedPortableToolProbeOutput(16, cancel)
	spec := waiter.prepared.spec
	spec.Args = []string{"wait", waiter.container}
	if err := waiter.backend.run(spec, RunOptions{Context: ctx, Stdout: status}); err != nil {
		return err
	}
	value := strings.TrimSpace(status.content.String())
	code, err := strconv.Atoi(value)
	if ctx.Err() != nil || status.Truncated() || err != nil || code < 0 || code > 255 || strconv.Itoa(code) != value {
		return fmt.Errorf("mapped engine completion is unverified")
	}
	if code != 0 {
		return fmt.Errorf("mapped entry exited with status %d", code)
	}
	return nil
}

// Render from frozen plan facts using the existing application renderer. No
// caller-provided raw command can substitute mounts, environment or startup.
func mappedLiveCreateSpecV1(ops deploy.IdentityMappingOperationsV1, state deploy.StateV1, directory string, facts mappedEntryPayloadV1) (CommandSpec, mappedSeccompPolicyV1, error) {
	var empty mappedSeccompPolicyV1
	if ops.ValidateProfileOwner == nil {
		return CommandSpec{}, empty, fmt.Errorf("mapped live plan requires locked build evidence")
	}
	lock, found, err := ops.Operation.ReadBuildLock(state.Current.BuildLockDigest, ops.ValidateProfileOwner)
	if err != nil || !found || lock.FinalImage.ConfigDigest != facts.Image || state.Current.RuntimePolicyDigest != facts.RuntimePolicy {
		return CommandSpec{}, empty, fmt.Errorf("mapped live plan build/image changed: %v", err)
	}
	network, _ := canonical.Sum("mapped-network", "v1", lock.RuntimePolicy.Network)
	if network != facts.NetworkPolicy || facts.Kernel.validate() != nil {
		return CommandSpec{}, empty, fmt.Errorf("mapped live plan policy changed")
	}
	uid, err := strconv.ParseUint(facts.UID, 10, 32)
	if err != nil {
		return CommandSpec{}, empty, err
	}
	gid, err := strconv.ParseUint(facts.GID, 10, 32)
	if err != nil {
		return CommandSpec{}, empty, err
	}
	groups := make([]uint32, len(facts.Groups))
	for i, value := range facts.Groups {
		n, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return CommandSpec{}, empty, err
		}
		groups[i] = uint32(n)
	}
	final, _ := strconv.ParseUint(facts.Kernel.FinalCapabilities, 16, 64)
	setup, _ := strconv.ParseUint(facts.Kernel.SetupCapabilities, 16, 64)
	last, _ := strconv.Atoi(facts.Kernel.KernelLastCapability)
	policy, err := compileMappedSeccompV1(podmanDockerSeccompProfile, facts.Kernel.ABI, final, setup, last)
	if err != nil {
		return CommandSpec{}, empty, err
	}
	sandbox := newApplicationSandboxPlanWithNetworkV1(RuntimeUserPlan{UID: uint32(uid), GID: uint32(gid), DockerUser: facts.UID + ":" + facts.GID, SupplementaryGIDs: groups}, lock.RuntimePolicy.Network)
	runtime := DockerExecutionPlan{DeploymentDir: directory, ContainerName: facts.Run, Image: state.Current.Reference, Mounts: facts.Mounts, Sandbox: sandbox}
	spec, err := transientContainerCommandSpecV1("create", facts.Run, runtime, ResolvedEnvironmentCommand{Argv: facts.Argv}, nil, facts.Stdin, false)
	if err != nil {
		return CommandSpec{}, empty, err
	}
	// Reuse the existing Podman home/seed translation before replacing its
	// ordinary startup with the complete already-bound mapped startup.
	spec, err = preparePodmanSeccompCommand(spec)
	if err != nil {
		return CommandSpec{}, empty, err
	}
	// HOME/TMPDIR defaults from the ordinary renderer must not silently alter
	// a mapped entry's frozen environment. All environment bytes are explicit.
	entry := slices.Index(spec.Args, "--entrypoint")
	args := make([]string, 0, len(spec.Args)+2*len(facts.Environment))
	for i := 0; i < entry; i++ {
		if spec.Args[i] == "--env" {
			i++
			continue
		}
		args = append(args, spec.Args[i])
	}
	for _, value := range facts.Environment {
		args = append(args, "--env", value)
	}
	args = append(args, spec.Args[entry:entry+3]...)
	spec.Args = append(args, facts.Startup...)
	return spec, policy, nil
}
