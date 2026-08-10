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
	if err != nil || len(receipts) != 1 || receipts[0] != receipt {
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
	if _, err := recoverLiveRunQueueV1(t.Context(), operation, &notice, containers.run); err != nil {
		t.Fatal(err)
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

type controlledSessionRecoveryContainerFixtureV1 struct {
	id     string
	name   string
	labels map[string]string
}

type controlledSessionRecoveryContainersV1 struct {
	byID     map[string]*controlledSessionRecoveryContainerFixtureV1
	byName   map[string]*controlledSessionRecoveryContainerFixtureV1
	inspects []string
	endpoint string
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
	return fmt.Errorf("unexpected controlled-session recovery command: %#v", spec)
}
