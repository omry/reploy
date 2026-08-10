package dockerdeploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

const dockerSessionNetworkTestIDV1 = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

type fakeDockerSessionNetworkEngineV1 struct {
	t          *testing.T
	plan       ControlledSessionExecutionPlanV1
	network    dockerSessionNetworkInspectionV1
	containers map[string]dockerSessionContainerInspectionV1
	exists     bool

	commands   []CommandSpec
	failBefore map[string]error
	failAfter  map[string]error
}

func newFakeDockerSessionNetworkEngineV1(t *testing.T, plan ControlledSessionExecutionPlanV1) *fakeDockerSessionNetworkEngineV1 {
	t.Helper()
	engine := &fakeDockerSessionNetworkEngineV1{
		t:    t,
		plan: plan,
		network: dockerSessionNetworkInspectionV1{
			ID:       dockerSessionNetworkTestIDV1,
			Name:     plan.Controller.SessionNetwork.Name,
			Scope:    "local",
			Driver:   "bridge",
			Internal: true,
			IPAM: dockerSessionNetworkIPAMV1{Config: []dockerSessionNetworkIPAMConfigV1{
				{Subnet: "172.31.0.0/24"},
			}},
			Containers: map[string]dockerSessionNetworkContainerV1{},
			Labels:     controlledSessionNetworkLabelMapV1(plan.LiveRunID),
		},
		failBefore: map[string]error{},
		failAfter:  map[string]error{},
	}
	engine.containers = map[string]dockerSessionContainerInspectionV1{
		dockerControllerTestContainerIDV1: dockerSessionContainerInspectionFixtureV1(plan.Controller, dockerControllerTestContainerIDV1),
		dockerWorkloadTestContainerIDV1:   dockerSessionContainerInspectionFixtureV1(plan.Workload, dockerWorkloadTestContainerIDV1),
	}
	return engine
}

func (engine *fakeDockerSessionNetworkEngineV1) run(spec CommandSpec, options RunOptions) error {
	engine.commands = append(engine.commands, spec)
	key := strings.Join(spec.Args, " ")
	if err := engine.failBefore[key]; err != nil {
		return err
	}
	switch {
	case slices.Equal(spec.Args, controlledSessionNetworkCreateArgsV1(engine.plan)):
		engine.exists = true
		writeStringV1(engine.t, options.Stdout, dockerSessionNetworkTestIDV1+"\n")
	case slices.Equal(spec.Args, []string{"network", "inspect", "--format", "{{json .}}", dockerSessionNetworkTestIDV1}):
		if !engine.exists {
			writeStringV1(engine.t, options.Stderr, "Error: No such network: "+dockerSessionNetworkTestIDV1)
			return errors.New("No such network")
		}
		if options.Stdout == nil {
			engine.t.Fatal("network inspect received no stdout")
		}
		if err := json.NewEncoder(options.Stdout).Encode(engine.network); err != nil {
			engine.t.Fatal(err)
		}
	case len(spec.Args) == 5 && slices.Equal(spec.Args[:4], []string{"container", "inspect", "--format", "{{json .}}"}):
		inspection, found := engine.containers[spec.Args[4]]
		if !found {
			writeStringV1(engine.t, options.Stderr, "Error: No such container: "+spec.Args[4])
			return errors.New("No such container")
		}
		if options.Stdout == nil {
			engine.t.Fatal("container inspect received no stdout")
		}
		if err := json.NewEncoder(options.Stdout).Encode(inspection); err != nil {
			engine.t.Fatal(err)
		}
	case len(spec.Args) >= 8 && slices.Equal(spec.Args[:3], []string{"network", "connect", "--alias"}):
		alias := spec.Args[3]
		networkID, containerID := spec.Args[len(spec.Args)-2], spec.Args[len(spec.Args)-1]
		if networkID != dockerSessionNetworkTestIDV1 {
			engine.t.Fatalf("network connect ID = %q", networkID)
		}
		var name string
		switch alias {
		case engine.plan.Controller.SessionNetwork.Alias:
			name = engine.plan.Controller.Container
		case engine.plan.Workload.SessionNetwork.Alias:
			name = engine.plan.Workload.Container
		default:
			engine.t.Fatalf("network connect alias = %q", alias)
		}
		realized, err := deriveControlledSessionNetworkRealizationV1([]string{"172.31.0.0/24"})
		if err != nil {
			engine.t.Fatal(err)
		}
		wantAddress := realized.ControllerAddresses[0]
		if alias == engine.plan.Workload.SessionNetwork.Alias {
			wantAddress = realized.WorkloadAddresses[0]
		}
		if !slices.Equal(spec.Args[4:len(spec.Args)-2], []string{"--ip", wantAddress}) {
			engine.t.Fatalf("network connect address arguments = %#v, want %q", spec.Args, wantAddress)
		}
		engine.network.Containers[containerID] = dockerSessionNetworkContainerV1{Name: name}
		inspection := engine.containers[containerID]
		inspection.NetworkSettings.Networks[engine.plan.Controller.SessionNetwork.Name] = dockerSessionContainerNetworkV1{
			Aliases: []string{alias}, IPAMConfig: &dockerSessionContainerIPAMConfigV1{IPv4Address: wantAddress},
		}
		engine.containers[containerID] = inspection
	case len(spec.Args) == 5 && slices.Equal(spec.Args[:3], []string{"network", "disconnect", "--force"}):
		if spec.Args[3] == controlledSessionOrdinaryNetworkModeV1 {
			inspection := engine.containers[spec.Args[4]]
			delete(inspection.NetworkSettings.Networks, controlledSessionOrdinaryNetworkModeV1)
			engine.containers[spec.Args[4]] = inspection
		} else if spec.Args[3] == dockerSessionNetworkTestIDV1 {
			inspection := engine.containers[spec.Args[4]]
			delete(inspection.NetworkSettings.Networks, engine.plan.Controller.SessionNetwork.Name)
			engine.containers[spec.Args[4]] = inspection
			delete(engine.network.Containers, spec.Args[4])
		} else {
			engine.t.Fatalf("network disconnect ID = %q", spec.Args[3])
		}
	case slices.Equal(spec.Args, []string{"network", "rm", dockerSessionNetworkTestIDV1}):
		engine.exists = false
	default:
		engine.t.Fatalf("unexpected Docker network command: %#v", spec)
	}
	return engine.failAfter[key]
}

func TestDockerSessionNetworkV1CreatesAttachesVerifiesAndRemovesExactNetwork(t *testing.T) {
	plan := controlledSessionNetworkPlanFixtureV1(t)
	engine := newFakeDockerSessionNetworkEngineV1(t, plan)
	network, err := prepareDockerSessionNetworkV1(t.Context(), plan, dockerSessionNetworkBackendV1{run: engine.run})
	if err != nil {
		t.Fatal(err)
	}
	if network.ID() != dockerSessionNetworkTestIDV1 || network.Name() != plan.Controller.SessionNetwork.Name ||
		!reflect.DeepEqual(network.Subnets(), []string{"172.31.0.0/24"}) ||
		!reflect.DeepEqual(network.Realization(), controlledSessionNetworkRealizationV1{
			Subnets: []string{"172.31.0.0/24"}, ControllerAddresses: []string{"172.31.0.2"}, WorkloadAddresses: []string{"172.31.0.3"},
		}) {
		t.Fatalf("prepared network = ID %q name %q realization %#v", network.ID(), network.Name(), network.Realization())
	}
	if err := network.Verify(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := network.Attach(t.Context(), dockerControllerTestContainerIDV1, dockerWorkloadTestContainerIDV1); err != nil {
		t.Fatal(err)
	}
	if err := network.Attach(t.Context(), dockerControllerTestContainerIDV1, dockerWorkloadTestContainerIDV1); err == nil || !strings.Contains(err.Error(), "already attempted") {
		t.Fatalf("second attach error = %v", err)
	}
	if err := network.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := network.Cleanup(t.Context()); err != nil {
		t.Fatalf("idempotent cleanup = %v", err)
	}
	if engine.exists {
		t.Fatal("network still exists after verified cleanup")
	}
	wantCommands := []string{
		strings.Join(controlledSessionNetworkCreateArgsV1(plan), " "),
		"network inspect --format {{json .}} " + dockerSessionNetworkTestIDV1,
		"network inspect --format {{json .}} " + dockerSessionNetworkTestIDV1,
		"container inspect --format {{json .}} " + dockerControllerTestContainerIDV1,
		"container inspect --format {{json .}} " + dockerWorkloadTestContainerIDV1,
		"network connect --alias controller --ip 172.31.0.2 " + dockerSessionNetworkTestIDV1 + " " + dockerControllerTestContainerIDV1,
		"network disconnect --force bridge " + dockerControllerTestContainerIDV1,
		"container inspect --format {{json .}} " + dockerControllerTestContainerIDV1,
		"network connect --alias workload --ip 172.31.0.3 " + dockerSessionNetworkTestIDV1 + " " + dockerWorkloadTestContainerIDV1,
		"network disconnect --force bridge " + dockerWorkloadTestContainerIDV1,
		"container inspect --format {{json .}} " + dockerWorkloadTestContainerIDV1,
		"network inspect --format {{json .}} " + dockerSessionNetworkTestIDV1,
		"network inspect --format {{json .}} " + dockerSessionNetworkTestIDV1,
		"network disconnect --force " + dockerSessionNetworkTestIDV1 + " " + dockerControllerTestContainerIDV1,
		"network disconnect --force " + dockerSessionNetworkTestIDV1 + " " + dockerWorkloadTestContainerIDV1,
		"network rm " + dockerSessionNetworkTestIDV1,
		"network inspect --format {{json .}} " + dockerSessionNetworkTestIDV1,
	}
	if got := commandArgsV1(engine.commands); !reflect.DeepEqual(got, wantCommands) {
		t.Fatalf("Docker commands =\n%#v\nwant\n%#v", got, wantCommands)
	}
}

func TestDockerSessionNetworkV1AttachesInertContainersThatRetainOrdinaryNetworking(t *testing.T) {
	plan := controlledSessionOrdinaryBridgeNetworkPlanFixtureV1(t)
	engine := newFakeDockerSessionNetworkEngineV1(t, plan)
	network, err := prepareDockerSessionNetworkV1(t.Context(), plan, dockerSessionNetworkBackendV1{run: engine.run})
	if err != nil {
		t.Fatal(err)
	}
	if err := network.Attach(t.Context(), dockerControllerTestContainerIDV1, dockerWorkloadTestContainerIDV1); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"network connect --alias controller --ip 172.31.0.2 " + dockerSessionNetworkTestIDV1 + " " + dockerControllerTestContainerIDV1,
		"network connect --alias workload --ip 172.31.0.3 " + dockerSessionNetworkTestIDV1 + " " + dockerWorkloadTestContainerIDV1,
	}
	commands := commandArgsV1(engine.commands)
	for _, expected := range want {
		if !slices.Contains(commands, expected) {
			t.Fatalf("ordinary-network attachment commands = %#v, missing %q", commands, expected)
		}
	}
	if err := network.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestDockerSessionNetworkV1VerifiesStartedParticipantAddresses(t *testing.T) {
	plan := controlledSessionNetworkPlanFixtureV1(t)
	engine := newFakeDockerSessionNetworkEngineV1(t, plan)
	network, err := prepareDockerSessionNetworkV1(t.Context(), plan, dockerSessionNetworkBackendV1{run: engine.run})
	if err != nil {
		t.Fatal(err)
	}
	if err := network.Attach(t.Context(), dockerControllerTestContainerIDV1, dockerWorkloadTestContainerIDV1); err != nil {
		t.Fatal(err)
	}
	for containerID, address := range map[string]string{
		dockerControllerTestContainerIDV1: "172.31.0.2",
		dockerWorkloadTestContainerIDV1:   "172.31.0.3",
	} {
		inspection := engine.containers[containerID]
		inspection.State.Running = true
		connection := inspection.NetworkSettings.Networks[plan.Controller.SessionNetwork.Name]
		connection.IPAddress = address
		inspection.NetworkSettings.Networks[plan.Controller.SessionNetwork.Name] = connection
		engine.containers[containerID] = inspection
	}
	if err := network.VerifyStarted(t.Context()); err != nil {
		t.Fatal(err)
	}
	inspection := engine.containers[dockerWorkloadTestContainerIDV1]
	connection := inspection.NetworkSettings.Networks[plan.Controller.SessionNetwork.Name]
	connection.IPAddress = "172.31.0.4"
	inspection.NetworkSettings.Networks[plan.Controller.SessionNetwork.Name] = connection
	engine.containers[dockerWorkloadTestContainerIDV1] = inspection
	if err := network.VerifyStarted(t.Context()); err == nil || !strings.Contains(err.Error(), `workload container address is "172.31.0.4" instead of "172.31.0.3"`) {
		t.Fatalf("started address mismatch = %v", err)
	}
}

func TestDeriveControlledSessionNetworkRealizationV1(t *testing.T) {
	got, err := deriveControlledSessionNetworkRealizationV1([]string{"fd00:1::/64", "172.31.0.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	want := controlledSessionNetworkRealizationV1{
		Subnets:             []string{"fd00:1::/64", "172.31.0.0/24"},
		ControllerAddresses: []string{"172.31.0.2", "fd00:1::2"},
		WorkloadAddresses:   []string{"172.31.0.3", "fd00:1::3"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("network realization = %#v, want %#v", got, want)
	}
	for _, test := range []struct {
		name    string
		subnets []string
		want    string
	}{
		{name: "empty", want: "no participant addresses"},
		{name: "noncanonical", subnets: []string{"172.31.0.1/24"}, want: "not canonical"},
		{name: "duplicate family", subnets: []string{"172.31.0.0/24", "172.32.0.0/24"}, want: "multiple prefixes"},
		{name: "too small", subnets: []string{"172.31.0.0/30"}, want: "no safe controller and workload addresses"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := deriveControlledSessionNetworkRealizationV1(test.subnets); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("realization error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestControlledSessionCreateWithPeerHostsV1(t *testing.T) {
	plan := controlledSessionNetworkPlanFixtureV1(t).Controller
	got, err := controlledSessionCreateWithPeerHostsV1(
		controlledSessionCommandSpecV1(plan.Create), plan, []string{"172.31.0.3", "fd00:1::3"},
	)
	if err != nil {
		t.Fatal(err)
	}
	entrypoint := slices.Index(got.Args, "--entrypoint")
	if entrypoint < 4 || !slices.Equal(got.Args[entrypoint-4:entrypoint], []string{
		"--add-host", "workload=172.31.0.3", "--add-host", "workload=fd00:1::3",
	}) {
		t.Fatalf("realized create arguments = %#v", got.Args)
	}
	if slices.Contains(plan.Create.Args, "--add-host") {
		t.Fatalf("immutable plan create arguments were mutated: %#v", plan.Create.Args)
	}
	for _, test := range []struct {
		name      string
		addresses []string
		want      string
	}{
		{name: "missing", want: "no peer addresses"},
		{name: "noncanonical", addresses: []string{"172.031.0.3"}, want: "canonical"},
		{name: "unsorted", addresses: []string{"fd00:1::3", "172.31.0.3"}, want: "sorted"},
		{name: "duplicate", addresses: []string{"172.31.0.3", "172.31.0.3"}, want: "unique"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := controlledSessionCreateWithPeerHostsV1(controlledSessionCommandSpecV1(plan.Create), plan, test.addresses); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("peer-host realization error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPrepareDockerSessionNetworkV1RejectsDisabledPlanAndIncompleteBackend(t *testing.T) {
	input, backend := controlledSessionPlanFixtureV1(t)
	plan, err := planControlledSessionV1(input, backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareDockerSessionNetworkV1(t.Context(), plan, dockerSessionNetworkBackendV1{run: func(CommandSpec, RunOptions) error { return nil }}); err == nil || !strings.Contains(err.Error(), "does not grant") {
		t.Fatalf("disabled plan error = %v", err)
	}
	plan = controlledSessionNetworkPlanFixtureV1(t)
	if _, err := prepareDockerSessionNetworkV1(t.Context(), plan, dockerSessionNetworkBackendV1{}); err == nil || !strings.Contains(err.Error(), "backend is incomplete") {
		t.Fatalf("incomplete backend error = %v", err)
	}
}

func TestPrepareDockerSessionNetworkV1DoesNotGuessAfterAmbiguousCreate(t *testing.T) {
	plan := controlledSessionNetworkPlanFixtureV1(t)
	commands := []CommandSpec{}
	_, err := prepareDockerSessionNetworkV1(t.Context(), plan, dockerSessionNetworkBackendV1{
		run: func(spec CommandSpec, options RunOptions) error {
			commands = append(commands, spec)
			writeStringV1(t, options.Stderr, "daemon response was lost")
			return errors.New("create response was lost")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "create response was lost") || !strings.Contains(err.Error(), "daemon response was lost") ||
		!strings.Contains(err.Error(), "did not return an exact network ID") {
		t.Fatalf("ambiguous create error = %v", err)
	}
	if len(commands) != 1 || !slices.Equal(commands[0].Args, controlledSessionNetworkCreateArgsV1(plan)) {
		t.Fatalf("ambiguous create commands = %#v", commands)
	}
}

func TestPrepareDockerSessionNetworkV1RecordsExactIDBeforeInspectionAndRollsBack(t *testing.T) {
	plan := controlledSessionNetworkPlanFixtureV1(t)
	engine := newFakeDockerSessionNetworkEngineV1(t, plan)
	recordErr := errors.New("persist failed")
	recorded := []string{}
	rollbackVerified := false
	_, err := prepareDockerSessionNetworkV1(t.Context(), plan, dockerSessionNetworkBackendV1{
		run: engine.run,
		recordNetworkID: func(id string) error {
			recorded = append(recorded, id)
			return recordErr
		},
		recordRollbackVerified: func() { rollbackVerified = true },
	})
	if !errors.Is(err, recordErr) || !rollbackVerified || engine.exists {
		t.Fatalf("record failure = %v, rollback verified = %t, exists = %t", err, rollbackVerified, engine.exists)
	}
	if !reflect.DeepEqual(recorded, []string{dockerSessionNetworkTestIDV1}) {
		t.Fatalf("recorded IDs = %#v", recorded)
	}
	want := []string{
		strings.Join(controlledSessionNetworkCreateArgsV1(plan), " "),
		"network rm " + dockerSessionNetworkTestIDV1,
		"network inspect --format {{json .}} " + dockerSessionNetworkTestIDV1,
	}
	if got := commandArgsV1(engine.commands); !reflect.DeepEqual(got, want) {
		t.Fatalf("record rollback commands = %#v, want %#v", got, want)
	}
}

func TestPrepareDockerSessionNetworkV1RetriesIDRecordWhenRollbackFails(t *testing.T) {
	plan := controlledSessionNetworkPlanFixtureV1(t)
	engine := newFakeDockerSessionNetworkEngineV1(t, plan)
	removeKey := "network rm " + dockerSessionNetworkTestIDV1
	engine.failBefore[removeKey] = errors.New("Docker unavailable")
	recorded := []string{}
	_, err := prepareDockerSessionNetworkV1(t.Context(), plan, dockerSessionNetworkBackendV1{
		run: engine.run,
		recordNetworkID: func(id string) error {
			recorded = append(recorded, id)
			return errors.New("persist failed")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "remove inert") || !strings.Contains(err.Error(), "retry") {
		t.Fatalf("rollback failure = %v", err)
	}
	if !reflect.DeepEqual(recorded, []string{dockerSessionNetworkTestIDV1, dockerSessionNetworkTestIDV1}) {
		t.Fatalf("recorded IDs = %#v", recorded)
	}
}

func TestPrepareDockerSessionNetworkV1RejectsInspectionMismatchAndRemovesExactID(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*dockerSessionNetworkInspectionV1)
		want   string
	}{
		{name: "ID", mutate: func(value *dockerSessionNetworkInspectionV1) { value.ID = strings.Repeat("d", 64) }, want: "full network ID"},
		{name: "name", mutate: func(value *dockerSessionNetworkInspectionV1) { value.Name = "other" }, want: "network name"},
		{name: "scope", mutate: func(value *dockerSessionNetworkInspectionV1) { value.Scope = "swarm" }, want: "engine-internal bridge"},
		{name: "driver", mutate: func(value *dockerSessionNetworkInspectionV1) { value.Driver = "overlay" }, want: "engine-internal bridge"},
		{name: "external", mutate: func(value *dockerSessionNetworkInspectionV1) { value.Internal = false }, want: "engine-internal bridge"},
		{name: "attachable", mutate: func(value *dockerSessionNetworkInspectionV1) { value.Attachable = true }, want: "non-attachable"},
		{name: "ingress", mutate: func(value *dockerSessionNetworkInspectionV1) { value.Ingress = true }, want: "non-ingress"},
		{name: "labels", mutate: func(value *dockerSessionNetworkInspectionV1) {
			value.Labels["io.reploy.session.live-run"] = "run-other"
		}, want: "ownership labels"},
		{name: "no subnet", mutate: func(value *dockerSessionNetworkInspectionV1) { value.IPAM.Config = nil }, want: "no assigned subnet"},
		{name: "invalid subnet", mutate: func(value *dockerSessionNetworkInspectionV1) { value.IPAM.Config[0].Subnet = "172.31.0.1/24" }, want: "canonical CIDR"},
		{name: "unexpected member", mutate: func(value *dockerSessionNetworkInspectionV1) {
			value.Containers[strings.Repeat("e", 64)] = dockerSessionNetworkContainerV1{Name: "intruder"}
		}, want: "network members"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := controlledSessionNetworkPlanFixtureV1(t)
			engine := newFakeDockerSessionNetworkEngineV1(t, plan)
			test.mutate(&engine.network)
			_, err := prepareDockerSessionNetworkV1(t.Context(), plan, dockerSessionNetworkBackendV1{run: engine.run})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("inspection mismatch error = %v, want %q", err, test.want)
			}
			if engine.exists {
				t.Fatal("mismatched created network was not removed by exact ID")
			}
		})
	}
}

func TestDockerSessionNetworkV1CleansPartialAmbiguousAttachment(t *testing.T) {
	plan := controlledSessionNetworkPlanFixtureV1(t)
	engine := newFakeDockerSessionNetworkEngineV1(t, plan)
	network, err := prepareDockerSessionNetworkV1(t.Context(), plan, dockerSessionNetworkBackendV1{run: engine.run})
	if err != nil {
		t.Fatal(err)
	}
	workloadConnect := "network connect --alias workload --ip 172.31.0.3 " + dockerSessionNetworkTestIDV1 + " " + dockerWorkloadTestContainerIDV1
	engine.failBefore[workloadConnect] = errors.New("connect response lost")
	if err := network.Attach(t.Context(), dockerControllerTestContainerIDV1, dockerWorkloadTestContainerIDV1); err == nil || !strings.Contains(err.Error(), "connect response lost") {
		t.Fatalf("partial attach error = %v", err)
	}
	if err := network.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if engine.exists {
		t.Fatal("partially attached network still exists")
	}
}

func TestDockerSessionNetworkV1VerifiesExactInertContainersBeforeAttachment(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*dockerSessionContainerInspectionV1)
		want   string
	}{
		{name: "ID", mutate: func(value *dockerSessionContainerInspectionV1) { value.ID = strings.Repeat("d", 64) }, want: "returned full ID"},
		{name: "name", mutate: func(value *dockerSessionContainerInspectionV1) { value.Name = "/other" }, want: "container name"},
		{name: "labels", mutate: func(value *dockerSessionContainerInspectionV1) {
			value.Config.Labels["io.reploy.session.live-run"] = "run-other"
		}, want: "ownership label"},
		{name: "running", mutate: func(value *dockerSessionContainerInspectionV1) { value.State.Running = true }, want: "must remain inert"},
		{name: "additional network", mutate: func(value *dockerSessionContainerInspectionV1) {
			value.NetworkSettings.Networks["other"] = dockerSessionContainerNetworkV1{}
		}, want: "only the inert ordinary network staging attachment"},
		{name: "missing bridge", mutate: func(value *dockerSessionContainerInspectionV1) {
			delete(value.NetworkSettings.Networks, controlledSessionOrdinaryNetworkModeV1)
		}, want: "only the inert ordinary network staging attachment"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := controlledSessionNetworkPlanFixtureV1(t)
			engine := newFakeDockerSessionNetworkEngineV1(t, plan)
			inspection := engine.containers[dockerControllerTestContainerIDV1]
			test.mutate(&inspection)
			engine.containers[dockerControllerTestContainerIDV1] = inspection
			network, err := prepareDockerSessionNetworkV1(t.Context(), plan, dockerSessionNetworkBackendV1{run: engine.run})
			if err != nil {
				t.Fatal(err)
			}
			before := len(engine.commands)
			if err := network.Attach(t.Context(), dockerControllerTestContainerIDV1, dockerWorkloadTestContainerIDV1); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("container verification error = %v, want %q", err, test.want)
			}
			if got := commandArgsV1(engine.commands[before:]); len(got) != 1 || !strings.HasPrefix(got[0], "container inspect") {
				t.Fatalf("container mismatch invoked network mutation: %#v", got)
			}
		})
	}
}

func TestDockerSessionNetworkV1RefusesUnexpectedMemberOrLabelDuringCleanup(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*fakeDockerSessionNetworkEngineV1)
		want   string
	}{
		{name: "member", mutate: func(engine *fakeDockerSessionNetworkEngineV1) {
			engine.network.Containers[strings.Repeat("e", 64)] = dockerSessionNetworkContainerV1{Name: "intruder"}
		}, want: "unexpected network member"},
		{name: "label", mutate: func(engine *fakeDockerSessionNetworkEngineV1) {
			engine.network.Labels["io.reploy.session.live-run"] = "run-other"
		}, want: "ownership labels"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := controlledSessionNetworkPlanFixtureV1(t)
			engine := newFakeDockerSessionNetworkEngineV1(t, plan)
			network, err := prepareDockerSessionNetworkV1(t.Context(), plan, dockerSessionNetworkBackendV1{run: engine.run})
			if err != nil {
				t.Fatal(err)
			}
			if err := network.Attach(t.Context(), dockerControllerTestContainerIDV1, dockerWorkloadTestContainerIDV1); err != nil {
				t.Fatal(err)
			}
			test.mutate(engine)
			before := len(engine.commands)
			if err := network.Cleanup(t.Context()); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("cleanup refusal = %v, want %q", err, test.want)
			}
			if !engine.exists || len(engine.commands) != before+1 || !strings.HasPrefix(strings.Join(engine.commands[len(engine.commands)-1].Args, " "), "network inspect") {
				t.Fatalf("cleanup mutated mismatched network: exists=%t commands=%#v", engine.exists, commandArgsV1(engine.commands[before:]))
			}
		})
	}
}

func TestDockerSessionNetworkV1RejectsInvalidContainerIDsBeforeDocker(t *testing.T) {
	plan := controlledSessionNetworkPlanFixtureV1(t)
	engine := newFakeDockerSessionNetworkEngineV1(t, plan)
	network, err := prepareDockerSessionNetworkV1(t.Context(), plan, dockerSessionNetworkBackendV1{run: engine.run})
	if err != nil {
		t.Fatal(err)
	}
	before := len(engine.commands)
	if err := network.Attach(t.Context(), "short", dockerWorkloadTestContainerIDV1); err == nil || !strings.Contains(err.Error(), "controller container ID") {
		t.Fatalf("invalid controller ID error = %v", err)
	}
	if err := network.Attach(t.Context(), dockerControllerTestContainerIDV1, dockerControllerTestContainerIDV1); err == nil || !strings.Contains(err.Error(), "must be different") {
		t.Fatalf("duplicate container ID error = %v", err)
	}
	if len(engine.commands) != before {
		t.Fatalf("invalid IDs invoked Docker: %#v", commandArgsV1(engine.commands[before:]))
	}
}

func TestDockerSessionNetworkV1PinsOneDockerEndpoint(t *testing.T) {
	plan := controlledSessionNetworkPlanFixtureV1(t)
	engine := newFakeDockerSessionNetworkEngineV1(t, plan)
	const endpoint = "unix:///session/network.sock"
	binds := 0
	network, err := prepareDockerSessionNetworkV1(t.Context(), plan, dockerSessionNetworkBackendV1{
		bind: func(_ context.Context, spec CommandSpec, timeout time.Duration) (CommandSpec, commandRunner, error) {
			binds++
			if timeout != defaultDockerPreflightTimeout {
				t.Fatalf("bind timeout = %s", timeout)
			}
			return pinDockerEndpointV1(spec, endpoint), func(command CommandSpec, options RunOptions) error {
				if got := commandEnvironmentValueV1(command, "DOCKER_HOST"); got != endpoint {
					t.Fatalf("Docker endpoint = %q", got)
				}
				return engine.run(command, options)
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := network.Attach(t.Context(), dockerControllerTestContainerIDV1, dockerWorkloadTestContainerIDV1); err != nil {
		t.Fatal(err)
	}
	if err := network.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if binds != 1 {
		t.Fatalf("Docker endpoint binds = %d", binds)
	}
	for _, command := range engine.commands {
		if got := commandEnvironmentValueV1(command, "DOCKER_HOST"); got != endpoint {
			t.Fatalf("command endpoint = %q for %#v", got, command)
		}
	}
}

func controlledSessionNetworkPlanFixtureV1(t *testing.T) ControlledSessionExecutionPlanV1 {
	t.Helper()
	input, backend := controlledSessionPlanFixtureV1(t)
	input.EndpointIDs = []string{"browser"}
	plan, err := planControlledSessionV1(input, backend)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func controlledSessionOrdinaryBridgeNetworkPlanFixtureV1(t *testing.T) ControlledSessionExecutionPlanV1 {
	t.Helper()
	input, backend := controlledSessionPlanFixtureV1(t)
	input.EndpointIDs = []string{"browser"}
	input.ControllerRuntime.Docker.Sandbox.Network.Local = "allow"
	input.WorkloadRuntime.Docker.Sandbox.Network.Local = "allow"
	plan, err := planControlledSessionV1(input, backend)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func controlledSessionNetworkCreateArgsV1(plan ControlledSessionExecutionPlanV1) []string {
	args := []string{"network", "create", "--driver", "bridge", "--internal"}
	for _, label := range controlledSessionNetworkLabelsV1(plan.LiveRunID) {
		args = append(args, "--label", label.Name+"="+label.Value)
	}
	return append(args, plan.Controller.SessionNetwork.Name)
}

func commandArgsV1(commands []CommandSpec) []string {
	result := make([]string, len(commands))
	for index, command := range commands {
		result[index] = strings.Join(command.Args, " ")
	}
	return result
}

func writeStringV1(t *testing.T, writer io.Writer, value string) {
	t.Helper()
	if writer == nil {
		t.Fatal("command received no writer")
	}
	if _, err := fmt.Fprint(writer, value); err != nil {
		t.Fatal(err)
	}
}

func dockerSessionContainerInspectionFixtureV1(
	plan ControlledSessionContainerPlanV1,
	containerID string,
) dockerSessionContainerInspectionV1 {
	inspection := dockerSessionContainerInspectionV1{ID: containerID, Name: "/" + plan.Container}
	inspection.Config.Labels = controlledSessionContainerLabelMapV1(plan)
	inspection.Config.Labels["org.example.inherited"] = "preserved"
	inspection.NetworkSettings.Networks = map[string]dockerSessionContainerNetworkV1{
		controlledSessionOrdinaryNetworkModeV1: {},
	}
	return inspection
}
