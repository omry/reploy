package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

func TestRecoverLiveRunQueueV1DefersAndRetriesExactContainerCleanup(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	run := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	if _, err := operation.AdmitLiveRunV1(run, false); err != nil {
		t.Fatal(err)
	}
	container := "demo-" + run.ID
	if err := operation.RecordLiveRunContainerV1(run.ID, container); err != nil {
		t.Fatal(err)
	}
	want := errors.New("Docker daemon is restarting")
	var notice bytes.Buffer
	recovery, err := recoverLiveRunQueueV1(t.Context(), operation, &notice, func(spec CommandSpec, _ RunOptions) error {
		if !reflect.DeepEqual(spec, TemporaryContainerCleanupCommand(container)) {
			t.Fatalf("cleanup command = %#v", spec)
		}
		return want
	})
	if err != nil || len(recovery.Removed) != 1 {
		t.Fatalf("first recovery = %#v, %v", recovery, err)
	}
	for _, text := range []string{
		"skipped abandoned app command \"export\" (" + run.ID + ")",
		"deferred cleanup of recovered app \"export\" container \"" + container + "\"",
	} {
		if !strings.Contains(notice.String(), text) {
			t.Fatalf("recovery notice missing %q: %q", text, notice.String())
		}
	}
	queue, _, err := operation.ReadLiveRunQueueV1()
	if err != nil || len(queue.Runs) != 0 || len(queue.Cleanup) != 1 || queue.Cleanup[0].Container != container {
		t.Fatalf("deferred cleanup queue = %#v, %v", queue, err)
	}
	calls := 0
	if _, err := recoverLiveRunQueueV1(t.Context(), operation, nil, func(spec CommandSpec, _ RunOptions) error {
		calls++
		if !reflect.DeepEqual(spec, TemporaryContainerCleanupCommand(container)) {
			t.Fatalf("retry command = %#v", spec)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	queue, found, err := operation.ReadLiveRunQueueV1()
	if err != nil || found || calls != 1 || len(queue.Cleanup) != 0 {
		t.Fatalf("cleanup retry = %#v, found=%t calls=%d error=%v", queue, found, calls, err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverLiveRunQueueV1BlocksControllerAdmissionUntilSessionCleanupVerifiesAbsent(t *testing.T) {
	input, planBackend := controlledSessionPlanFixtureV1(t)
	plan, err := planControlledSessionV1(input, planBackend)
	if err != nil {
		t.Fatal(err)
	}
	operations := map[string]*deploy.OperationLock{}
	leases := map[string]*deploy.QueueEntryLeaseV1{}
	for _, participant := range []struct {
		dir        string
		name       string
		generation string
	}{
		{dir: plan.Controller.DeploymentDirectory, name: plan.Controller.DeploymentID, generation: plan.Controller.GenerationReference},
		{dir: plan.Workload.DeploymentDirectory, name: plan.Workload.DeploymentID, generation: plan.Workload.GenerationReference},
	} {
		operation, err := deploy.AcquireOperationLock(t.Context(), participant.dir)
		if err != nil {
			t.Fatal(err)
		}
		operations[participant.dir] = operation
		lease, err := operation.AcquireLiveRunLeaseV1(plan.LiveRunID)
		if err != nil {
			t.Fatal(err)
		}
		leases[participant.dir] = lease
		if status, err := operation.AdmitLiveRunV1(deploy.LiveRunV1{
			ID: plan.LiveRunID, Kind: deploy.LiveRunKindShellV1, Name: participant.name,
			GenerationReference: participant.generation, Exclusive: true,
		}, false); err != nil || status != deploy.LiveRunStatusActiveV1 {
			t.Fatalf("participant admission = %q, %v", status, err)
		}
	}
	ownership := controlledSessionOwnershipFromPlanV1(plan, "unix:///var/run/docker.sock", "", "")
	for _, dir := range []string{plan.Controller.DeploymentDirectory, plan.Workload.DeploymentDirectory} {
		if _, err := operations[dir].RecordControlledSessionOwnershipV1(ownership); err != nil {
			t.Fatalf("record ownership in %q: %v", dir, err)
		}
		if err := operations[dir].Unlock(); err != nil {
			t.Fatal(err)
		}
		if err := leases[dir].Release(); err != nil {
			t.Fatal(err)
		}
	}

	controllerOperation, err := deploy.AcquireOperationLock(t.Context(), plan.Controller.DeploymentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("Docker cleanup failed")
	if _, err := recoverLiveRunQueueWithinV1(
		t.Context(), controllerOperation, nil,
		func(CommandSpec, RunOptions) error { return want },
		time.Second, nil,
	); !errors.Is(err, want) || !strings.Contains(err.Error(), "recovery remains incomplete") {
		t.Fatalf("failed controller recovery error = %v", err)
	}
	queue, _, err := controllerOperation.ReadLiveRunQueueV1()
	if err != nil || len(queue.Runs) != 0 || len(queue.ControlledSessions) != 1 {
		t.Fatalf("retained controller reservation = %#v, %v", queue, err)
	}

	if _, err := recoverLiveRunQueueWithinV1(
		t.Context(), controllerOperation, nil,
		func(spec CommandSpec, options RunOptions) error {
			if len(spec.Args) >= 2 && spec.Args[0] == "container" && spec.Args[1] == "inspect" {
				_, _ = fmt.Fprintln(options.Stderr, "Error: No such container")
				return errors.New("inspect failed")
			}
			return nil
		},
		time.Second, nil,
	); err != nil {
		t.Fatal(err)
	}
	queue, found, err := controllerOperation.ReadLiveRunQueueV1()
	if err != nil || found || len(queue.ControlledSessions) != 0 {
		t.Fatalf("completed controller recovery = %#v, found=%t, %v", queue, found, err)
	}
	if err := controllerOperation.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverLiveRunQueueV1BoundsCleanupAcrossInventory(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	queue := deploy.NewLiveRunQueueV1()
	queue.Cleanup = []deploy.LiveRunContainerCleanupV1{
		{
			Container: "demo-run-0000000000000001", RunID: "run-0000000000000001",
			Kind: deploy.LiveRunKindAppV1, Name: "first", Reason: deploy.LiveRunRecoveryCleanupFailedV1,
		},
		{
			Container: "demo-run-0000000000000002", RunID: "run-0000000000000002",
			Kind: deploy.LiveRunKindAppV1, Name: "second", Reason: deploy.LiveRunRecoveryCleanupFailedV1,
		},
	}
	if err := operation.CommitLiveRunQueueV1(queue); err != nil {
		t.Fatal(err)
	}
	var notice bytes.Buffer
	calls := 0
	started := time.Now()
	_, err = recoverLiveRunQueueWithinV1(
		t.Context(),
		operation,
		&notice,
		func(_ CommandSpec, options RunOptions) error {
			calls++
			<-options.Context.Done()
			return options.Context.Err()
		},
		20*time.Millisecond,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("bounded cleanup took %s", elapsed)
	}
	if calls != 1 || !strings.Contains(notice.String(), "deferred remaining recovered container cleanup") {
		t.Fatalf("bounded cleanup calls=%d notice=%q", calls, notice.String())
	}
	loaded, _, err := operation.ReadLiveRunQueueV1()
	if err != nil || len(loaded.Cleanup) != 2 {
		t.Fatalf("bounded cleanup inventory = %#v, %v", loaded.Cleanup, err)
	}
}

func TestRecoverLiveRunQueueV1ReportsScheduledCleanupWhenDockerIsNotQueried(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	run := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	if _, err := operation.AdmitLiveRunV1(run, false); err != nil {
		t.Fatal(err)
	}
	container := "demo-" + run.ID
	if err := operation.RecordLiveRunContainerV1(run.ID, container); err != nil {
		t.Fatal(err)
	}
	var notice bytes.Buffer
	if _, err := recoverLiveRunQueueWithinV1(
		context.Background(), operation, &notice, nil, time.Second, nil,
	); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notice.String(), "scheduled cleanup for transient container \""+container+"\"") {
		t.Fatalf("scheduled cleanup notice = %q", notice.String())
	}
}

func TestRecoverLiveRunQueueV1PreservesLiveOwnerAcrossDockerInterruption(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	run := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	lease := holdLiveRunLeaseV1(t, operation, run.ID)
	if lease == nil {
		t.Fatal("live-run lease is missing")
	}
	if _, err := operation.AdmitLiveRunV1(run, false); err != nil {
		t.Fatal(err)
	}
	dockerCalls := 0
	recovery, err := recoverLiveRunQueueV1(t.Context(), operation, nil, func(CommandSpec, RunOptions) error {
		dockerCalls++
		return errors.New("Docker daemon unavailable")
	})
	if err != nil || len(recovery.Removed) != 0 || dockerCalls != 0 {
		t.Fatalf("live-owner recovery = %#v, Docker calls=%d, error=%v", recovery, dockerCalls, err)
	}
	queue, _, err := operation.ReadLiveRunQueueV1()
	if err != nil || len(queue.Runs) != 1 || queue.Runs[0].ID != run.ID {
		t.Fatalf("preserved queue = %#v, %v", queue, err)
	}
}

func TestRecoverLiveRunQueueV1CleansPartialControlledSessionByVerifiedName(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	operation, err := deploy.AcquireOperationLock(t.Context(), plan.Workload.DeploymentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	run := liveRunAdmissionFixtureV1(plan.LiveRunID, false)
	run.Kind = deploy.LiveRunKindShellV1
	run.GenerationReference = plan.Workload.GenerationReference
	if _, err := operation.AdmitLiveRunV1(run, false); err != nil {
		t.Fatal(err)
	}
	ownership := controlledSessionOwnershipFromPlanV1(plan, controlledSessionTestDockerEndpointV1, "", "")
	recorded, err := operation.RecordControlledSessionOwnershipV1(ownership)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(recorded.ChannelDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	containers := newControlledSessionRecoveryContainersV1(recorded)
	var notice bytes.Buffer
	recovery, err := recoverLiveRunQueueV1(t.Context(), operation, &notice, containers.run)
	if err != nil || len(recovery.ControlledSessions) != 1 {
		t.Fatalf("controlled-session recovery = %#v, error=%v", recovery, err)
	}
	if len(containers.byID) != 0 {
		t.Fatalf("controlled-session containers remain = %#v", containers.byID)
	}
	if _, err := os.Lstat(recorded.ChannelDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("controlled-session channel remains: %v", err)
	}
	queue, found, err := operation.ReadLiveRunQueueV1()
	if err != nil || found || len(queue.Runs) != 0 || len(queue.ControlledSessions) != 0 {
		t.Fatalf("queue after controlled-session recovery = %#v, found=%t, error=%v", queue, found, err)
	}
	wantInspects := []string{recorded.Workload.Name, dockerWorkloadTestContainerIDV1, recorded.Controller.Name, dockerControllerTestContainerIDV1}
	if !reflect.DeepEqual(containers.inspects, wantInspects) {
		t.Fatalf("controlled-session inspect targets = %#v", containers.inspects)
	}
}

func TestRecoverLiveRunQueueV1CleansLegacyControlledSessionWithoutDockerEndpoint(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	operation, err := deploy.AcquireOperationLock(t.Context(), plan.Workload.DeploymentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	bootSession, err := deploy.CurrentBootSessionIDV1()
	if err != nil {
		t.Fatal(err)
	}
	ownership := controlledSessionOwnershipFromPlanV1(
		plan, controlledSessionTestDockerEndpointV1, dockerControllerTestContainerIDV1, dockerWorkloadTestContainerIDV1,
	)
	ownership.BootSession = bootSession
	ownership.DockerEndpoint = ""
	queue := deploy.NewLiveRunQueueV1()
	queue.ControlledSessions = []deploy.ControlledSessionOwnershipV1{ownership}
	if err := operation.CommitLiveRunQueueV1(queue); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ownership.ChannelDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	containers := newControlledSessionRecoveryContainersV1(ownership)
	containers.endpoint = controlledSessionTestDockerEndpointV1
	resolved := 0
	recovery, err := recoverLiveRunQueueWithinV1(
		t.Context(), operation, nil, containers.run, liveRunRecoveryCleanupTimeoutV1,
		func(context.Context) (string, error) {
			resolved++
			return controlledSessionTestDockerEndpointV1, nil
		},
	)
	if err != nil || len(recovery.ControlledSessions) != 1 {
		t.Fatalf("legacy controlled-session recovery = %#v, error=%v", recovery, err)
	}
	if resolved != 1 {
		t.Fatalf("legacy Docker endpoint resolutions = %d", resolved)
	}
	if len(containers.byID) != 0 {
		t.Fatalf("legacy controlled-session containers remain = %#v", containers.byID)
	}
	if _, found, err := operation.ReadLiveRunQueueV1(); err != nil || found {
		t.Fatalf("legacy queue remains: found=%t, error=%v", found, err)
	}
}

func TestRecoverLiveRunQueueV1PreservesDurableCrashReceiptAfterResourceCleanup(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	operation, err := deploy.AcquireOperationLock(t.Context(), plan.Workload.DeploymentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	run := liveRunAdmissionFixtureV1(plan.LiveRunID, false)
	run.Kind = deploy.LiveRunKindShellV1
	run.GenerationReference = plan.Workload.GenerationReference
	if _, err := operation.AdmitLiveRunV1(run, false); err != nil {
		t.Fatal(err)
	}
	ownership := controlledSessionOwnershipFromPlanV1(plan, controlledSessionTestDockerEndpointV1, dockerControllerTestContainerIDV1, dockerWorkloadTestContainerIDV1)
	recorded, err := operation.RecordControlledSessionOwnershipV1(ownership)
	if err != nil {
		t.Fatal(err)
	}
	target, err := operation.PrepareControlledSessionIncidentReceiptV1(recorded.ChannelDirectory, recorded.LiveRunID)
	if err != nil {
		t.Fatal(err)
	}
	receipt := controlledSessionIncidentRetrievalFixtureV1(recorded.LiveRunID)
	receipt.BootSession = recorded.BootSession
	if err := deploy.WriteControlledSessionIncidentReceiptV1(target.File(), receipt); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	containers := newControlledSessionRecoveryContainersV1(recorded)
	if _, err := recoverLiveRunQueueV1(t.Context(), operation, nil, containers.run); err != nil {
		t.Fatal(err)
	}
	queue, found, err := operation.ReadLiveRunQueueV1()
	if err != nil || found || len(queue.ControlledSessions) != 0 {
		t.Fatalf("queue after recovery = %#v, found=%t, error=%v", queue, found, err)
	}
	receipts, err := operation.ReadControlledSessionIncidentReceiptsV1()
	if err != nil || len(receipts) != 1 || !reflect.DeepEqual(receipts[0], receipt) {
		t.Fatalf("receipt after recovery = %#v, error=%v", receipts, err)
	}
	removed, err := operation.AcknowledgeControlledSessionIncidentReceiptV1(recorded.LiveRunID)
	if err != nil || !removed {
		t.Fatalf("acknowledge recovered receipt = %t, %v", removed, err)
	}
}

func TestRecoverLiveRunQueueV1RetainsControlledSessionAfterLabelMismatchAndRetries(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	operation, err := deploy.AcquireOperationLock(t.Context(), plan.Workload.DeploymentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	run := liveRunAdmissionFixtureV1(plan.LiveRunID, false)
	run.Kind = deploy.LiveRunKindShellV1
	run.GenerationReference = plan.Workload.GenerationReference
	if _, err := operation.AdmitLiveRunV1(run, false); err != nil {
		t.Fatal(err)
	}
	ownership := controlledSessionOwnershipFromPlanV1(plan, controlledSessionTestDockerEndpointV1, dockerControllerTestContainerIDV1, dockerWorkloadTestContainerIDV1)
	recorded, err := operation.RecordControlledSessionOwnershipV1(ownership)
	if err != nil {
		t.Fatal(err)
	}
	containers := newControlledSessionRecoveryContainersV1(recorded)
	containers.byID[dockerWorkloadTestContainerIDV1].labels["io.reploy.session.live-run"] = "run-ffffffffffffffff"
	var notice bytes.Buffer
	if _, err := recoverLiveRunQueueV1(t.Context(), operation, &notice, containers.run); err == nil ||
		!strings.Contains(err.Error(), "recovery remains incomplete") ||
		!strings.Contains(err.Error(), "ownership label") {
		t.Fatalf("label-mismatch recovery error = %v", err)
	}
	queue, found, err := operation.ReadLiveRunQueueV1()
	if err != nil || !found || len(queue.Runs) != 0 || len(queue.ControlledSessions) != 1 || queue.ControlledSessions[0] != recorded {
		t.Fatalf("retained controlled-session recovery = %#v, found=%t, error=%v", queue, found, err)
	}
	if !strings.Contains(notice.String(), "ownership label \"io.reploy.session.live-run\" does not match") {
		t.Fatalf("controlled-session recovery notice = %q", notice.String())
	}
	containers.byID[dockerWorkloadTestContainerIDV1].labels["io.reploy.session.live-run"] = recorded.LiveRunID
	if _, err := recoverLiveRunQueueV1(t.Context(), operation, nil, containers.run); err != nil {
		t.Fatal(err)
	}
	queue, found, err = operation.ReadLiveRunQueueV1()
	if err != nil || found || len(queue.ControlledSessions) != 0 {
		t.Fatalf("retried controlled-session recovery = %#v, found=%t, error=%v", queue, found, err)
	}
}

func TestRecoverLiveRunQueueV1DiscoversAndRemovesOwnedNetworkByFrozenName(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	operation, err := deploy.AcquireOperationLock(t.Context(), plan.Workload.DeploymentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	run := liveRunAdmissionFixtureV1(plan.LiveRunID, false)
	run.Kind = deploy.LiveRunKindShellV1
	run.GenerationReference = plan.Workload.GenerationReference
	if _, err := operation.AdmitLiveRunV1(run, false); err != nil {
		t.Fatal(err)
	}
	ownership := controlledSessionOwnershipFromPlanV1(plan, controlledSessionTestDockerEndpointV1, "", "")
	ownership.NetworkName = "reploy-session-" + plan.LiveRunID
	recorded, err := operation.RecordControlledSessionOwnershipV1(ownership)
	if err != nil {
		t.Fatal(err)
	}
	resources := newControlledSessionRecoveryContainersV1(recorded)
	resources.byID = map[string]*controlledSessionRecoveryContainerFixtureV1{}
	resources.byName = map[string]*controlledSessionRecoveryContainerFixtureV1{}
	if _, err := recoverLiveRunQueueV1(t.Context(), operation, nil, resources.run); err != nil {
		t.Fatal(err)
	}
	if resources.network != nil {
		t.Fatalf("recovered network remains = %#v", resources.network)
	}
	wantNetworkInspects := []string{recorded.NetworkName, strings.Repeat("e", 64)}
	if !reflect.DeepEqual(resources.networkInspects, wantNetworkInspects) {
		t.Fatalf("network inspect targets = %#v, want %#v", resources.networkInspects, wantNetworkInspects)
	}
	if _, found, err := operation.ReadLiveRunQueueV1(); err != nil || found {
		t.Fatalf("recovered network ownership remains: found=%t, error=%v", found, err)
	}
}

func TestRecoverLiveRunQueueV1RetainsLabelMismatchedNetworkAndRetries(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	operation, err := deploy.AcquireOperationLock(t.Context(), plan.Workload.DeploymentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	run := liveRunAdmissionFixtureV1(plan.LiveRunID, false)
	run.Kind = deploy.LiveRunKindShellV1
	run.GenerationReference = plan.Workload.GenerationReference
	if _, err := operation.AdmitLiveRunV1(run, false); err != nil {
		t.Fatal(err)
	}
	ownership := controlledSessionOwnershipFromPlanV1(
		plan, controlledSessionTestDockerEndpointV1, dockerControllerTestContainerIDV1, dockerWorkloadTestContainerIDV1,
	)
	ownership.NetworkID = strings.Repeat("e", 64)
	ownership.NetworkName = "reploy-session-" + plan.LiveRunID
	recorded, err := operation.RecordControlledSessionOwnershipV1(ownership)
	if err != nil {
		t.Fatal(err)
	}
	resources := newControlledSessionRecoveryContainersV1(recorded)
	resources.network.labels["io.reploy.session.live-run"] = "run-ffffffffffffffff"
	var notice bytes.Buffer
	if _, err := recoverLiveRunQueueV1(t.Context(), operation, &notice, resources.run); err == nil ||
		!strings.Contains(err.Error(), "recovery remains incomplete") ||
		!strings.Contains(err.Error(), "ownership labels") {
		t.Fatalf("network-mismatch recovery error = %v", err)
	}
	if resources.network == nil || !strings.Contains(notice.String(), "network") || !strings.Contains(notice.String(), "ownership labels") {
		t.Fatalf("mismatched network was not retained: network=%#v notice=%q", resources.network, notice.String())
	}
	queue, found, err := operation.ReadLiveRunQueueV1()
	if err != nil || !found || len(queue.ControlledSessions) != 1 {
		t.Fatalf("retained network ownership = %#v, found=%t, error=%v", queue.ControlledSessions, found, err)
	}
	resources.network.labels["io.reploy.session.live-run"] = recorded.LiveRunID
	if _, err := recoverLiveRunQueueV1(t.Context(), operation, nil, resources.run); err != nil {
		t.Fatal(err)
	}
	if resources.network != nil {
		t.Fatalf("retried network remains = %#v", resources.network)
	}
}

type controlledSessionRecoveryContainerFixtureV1 struct {
	id     string
	name   string
	labels map[string]string
}

type controlledSessionRecoveryNetworkFixtureV1 struct {
	id      string
	name    string
	labels  map[string]string
	members map[string]dockerSessionNetworkContainerV1
}

type controlledSessionRecoveryContainersV1 struct {
	byID            map[string]*controlledSessionRecoveryContainerFixtureV1
	byName          map[string]*controlledSessionRecoveryContainerFixtureV1
	inspects        []string
	endpoint        string
	network         *controlledSessionRecoveryNetworkFixtureV1
	networkInspects []string
}

func newControlledSessionRecoveryContainersV1(ownership deploy.ControlledSessionOwnershipV1) *controlledSessionRecoveryContainersV1 {
	containers := &controlledSessionRecoveryContainersV1{
		byID:     map[string]*controlledSessionRecoveryContainerFixtureV1{},
		byName:   map[string]*controlledSessionRecoveryContainerFixtureV1{},
		endpoint: ownership.DockerEndpoint,
	}
	for _, input := range []struct {
		ownership deploy.ControlledSessionContainerOwnershipV1
		id        string
	}{
		{ownership: ownership.Controller, id: dockerControllerTestContainerIDV1},
		{ownership: ownership.Workload, id: dockerWorkloadTestContainerIDV1},
	} {
		container := &controlledSessionRecoveryContainerFixtureV1{
			id: input.id, name: input.ownership.Name,
			labels: map[string]string{
				"io.reploy.session.build":       input.ownership.BuildIdentity,
				"io.reploy.session.environment": input.ownership.DeploymentID,
				"io.reploy.session.generation":  input.ownership.GenerationReference,
				"io.reploy.session.live-run":    ownership.LiveRunID,
				"io.reploy.session.role":        input.ownership.Role,
			},
		}
		containers.byID[container.id] = container
		containers.byName[container.name] = container
	}
	if ownership.NetworkName != "" {
		networkID := ownership.NetworkID
		if networkID == "" {
			networkID = strings.Repeat("e", 64)
		}
		containers.network = &controlledSessionRecoveryNetworkFixtureV1{
			id: networkID, name: ownership.NetworkName,
			labels: map[string]string{
				"io.reploy.session.live-run": ownership.LiveRunID,
				"io.reploy.session.role":     deploy.ControlledSessionNetworkRoleV1,
			},
			members: map[string]dockerSessionNetworkContainerV1{},
		}
	}
	return containers
}

func (containers *controlledSessionRecoveryContainersV1) run(spec CommandSpec, options RunOptions) error {
	if host, found := commandSpecEnvironmentValueV1(spec, "DOCKER_HOST"); !found || host != containers.endpoint {
		return fmt.Errorf("controlled-session recovery command used Docker endpoint %q, want %q", host, containers.endpoint)
	}
	if contextName, found := commandSpecEnvironmentValueV1(spec, "DOCKER_CONTEXT"); !found || contextName != "" {
		return fmt.Errorf("controlled-session recovery command retained Docker context %q", contextName)
	}
	if len(spec.Args) >= 2 && spec.Args[0] == "container" && spec.Args[1] == "inspect" {
		target := spec.Args[len(spec.Args)-1]
		containers.inspects = append(containers.inspects, target)
		container := containers.byID[target]
		if container == nil {
			container = containers.byName[target]
		}
		if container == nil {
			return errors.New("No such container")
		}
		labels, err := json.Marshal(container.labels)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(options.Stdout, "%q %s", container.id, labels)
		return err
	}
	if len(spec.Args) >= 2 && spec.Args[0] == "container" && spec.Args[1] == "rm" {
		id := spec.Args[len(spec.Args)-1]
		container := containers.byID[id]
		if container == nil {
			return errors.New("No such container")
		}
		delete(containers.byID, container.id)
		delete(containers.byName, container.name)
		return nil
	}
	if len(spec.Args) >= 2 && spec.Args[0] == "network" && spec.Args[1] == "inspect" {
		target := spec.Args[len(spec.Args)-1]
		containers.networkInspects = append(containers.networkInspects, target)
		network := containers.network
		if network == nil || target != network.id && target != network.name {
			return errors.New("No such network")
		}
		labels, err := json.Marshal(network.labels)
		if err != nil {
			return err
		}
		members, err := json.Marshal(network.members)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(options.Stdout, "%q %q %s %s", network.id, network.name, labels, members)
		return err
	}
	if len(spec.Args) >= 2 && spec.Args[0] == "network" && spec.Args[1] == "rm" {
		id := spec.Args[len(spec.Args)-1]
		if containers.network == nil || containers.network.id != id {
			return errors.New("No such network")
		}
		containers.network = nil
		return nil
	}
	return fmt.Errorf("unexpected controlled-session recovery command: %#v", spec)
}
