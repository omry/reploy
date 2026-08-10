package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

const controlledSessionNetworkRoleV1 = deploy.ControlledSessionNetworkRoleV1

type dockerSessionNetworkBackendV1 struct {
	bind                   func(context.Context, CommandSpec, time.Duration) (CommandSpec, commandRunner, error)
	run                    commandRunner
	recordNetworkID        func(string) error
	recordRollbackVerified func()
}

type dockerSessionNetworkInspectionV1 struct {
	ID         string                                     `json:"Id"`
	Name       string                                     `json:"Name"`
	Scope      string                                     `json:"Scope"`
	Driver     string                                     `json:"Driver"`
	Internal   bool                                       `json:"Internal"`
	Attachable bool                                       `json:"Attachable"`
	Ingress    bool                                       `json:"Ingress"`
	IPAM       dockerSessionNetworkIPAMV1                 `json:"IPAM"`
	Containers map[string]dockerSessionNetworkContainerV1 `json:"Containers"`
	Labels     map[string]string                          `json:"Labels"`
}

type dockerSessionNetworkIPAMV1 struct {
	Config []dockerSessionNetworkIPAMConfigV1 `json:"Config"`
}

type dockerSessionNetworkIPAMConfigV1 struct {
	Subnet string `json:"Subnet"`
}

type dockerSessionNetworkContainerV1 struct {
	Name string `json:"Name"`
}

type dockerSessionContainerInspectionV1 struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Running bool `json:"Running"`
	} `json:"State"`
}

// DockerSessionNetworkV1 owns one exact engine-internal Docker network. The
// adapter accepts only the two exact container IDs selected at attachment time
// and refuses cleanup if inspection finds a different member or ownership
// label.
type DockerSessionNetworkV1 struct {
	plan      ControlledSessionExecutionPlanV1
	network   ControlledSessionNetworkPlanV1
	docker    CommandSpec
	networkID string
	subnets   []string
	backend   dockerSessionNetworkBackendV1

	mu           sync.Mutex
	attachTried  bool
	controllerID string
	workloadID   string
	cleaned      bool
}

func (network *DockerSessionNetworkV1) ID() string {
	return network.networkID
}

func (network *DockerSessionNetworkV1) Name() string {
	return network.network.Name
}

func (network *DockerSessionNetworkV1) Subnets() []string {
	network.mu.Lock()
	defer network.mu.Unlock()
	return slices.Clone(network.subnets)
}

// PrepareDockerSessionNetworkV1 creates and verifies the exact planned
// engine-internal network without attaching either inert container.
func PrepareDockerSessionNetworkV1(
	ctx context.Context,
	plan ControlledSessionExecutionPlanV1,
) (*DockerSessionNetworkV1, error) {
	return prepareDockerSessionNetworkWithIDV1(ctx, plan, nil, nil)
}

func prepareDockerSessionNetworkWithIDV1(
	ctx context.Context,
	plan ControlledSessionExecutionPlanV1,
	recordNetworkID func(string) error,
	recordRollbackVerified func(),
) (*DockerSessionNetworkV1, error) {
	return prepareDockerSessionNetworkV1(ctx, plan, dockerSessionNetworkBackendV1{
		bind:                   bindPinnedDockerCommandRunnerV1,
		recordNetworkID:        recordNetworkID,
		recordRollbackVerified: recordRollbackVerified,
	})
}

func prepareDockerSessionNetworkV1(
	ctx context.Context,
	plan ControlledSessionExecutionPlanV1,
	backend dockerSessionNetworkBackendV1,
) (*DockerSessionNetworkV1, error) {
	plan = cloneControlledSessionExecutionPlanV1(plan)
	if err := ValidateControlledSessionExecutionPlanV1(plan); err != nil {
		return nil, fmt.Errorf("prepare controlled-session network: %w", err)
	}
	if !plan.Controller.SessionNetwork.Enabled {
		return nil, fmt.Errorf("prepare controlled-session network: execution plan does not grant a session network")
	}
	if backend.bind == nil && backend.run == nil {
		return nil, fmt.Errorf("prepare controlled-session network: backend is incomplete")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	docker := controlledSessionCommandSpecV1(plan.Controller.Create)
	if backend.bind != nil {
		var err error
		docker, backend.run, err = backend.bind(ctx, docker, defaultDockerPreflightTimeout)
		if err != nil {
			return nil, fmt.Errorf("bind controlled-session network Docker endpoint: %w", err)
		}
		if backend.run == nil {
			return nil, fmt.Errorf("prepare controlled-session network: Docker endpoint binder returned no command runner")
		}
	}
	networkPlan := plan.Controller.SessionNetwork
	labels := controlledSessionNetworkLabelsV1(plan.LiveRunID)
	create := docker
	create.Args = []string{"network", "create", "--driver", "bridge", "--internal"}
	for _, label := range labels {
		create.Args = append(create.Args, "--label", label.Name+"="+label.Value)
	}
	create.Args = append(create.Args, networkPlan.Name)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := backend.run(create, RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr}); err != nil {
		if output := trimmedCommandOutput(stderr.String()); output != "" {
			err = fmt.Errorf("%w\ncommand output:\n%s", err, output)
		}
		return nil, fmt.Errorf("create controlled-session network %q: %w; refusing cleanup because the creating attempt did not return an exact network ID", networkPlan.Name, err)
	}
	networkID, err := parseDockerNetworkIDV1(stdout.String())
	if err != nil {
		return nil, fmt.Errorf("create controlled-session network %q: %w; refusing name-based cleanup because the created network identity is unknown", networkPlan.Name, err)
	}
	network := &DockerSessionNetworkV1{
		plan: plan, network: networkPlan, docker: docker, networkID: networkID, backend: backend,
	}
	if backend.recordNetworkID != nil {
		if err := backend.recordNetworkID(networkID); err != nil {
			return nil, network.rollbackAfterPreparationFailureV1(
				fmt.Errorf("record controlled-session network %q exact ID: %w", networkPlan.Name, err),
				true,
			)
		}
	}
	inspection, found, err := network.inspectV1(ctx)
	if err != nil {
		return nil, network.rollbackAfterPreparationFailureV1(
			fmt.Errorf("inspect created controlled-session network %q: %w", networkPlan.Name, err),
			false,
		)
	}
	if !found {
		return nil, network.rollbackAfterPreparationFailureV1(
			fmt.Errorf("created controlled-session network %q disappeared before verification", networkPlan.Name),
			false,
		)
	}
	subnets, err := validateDockerSessionNetworkInspectionV1(
		inspection,
		networkID,
		networkPlan.Name,
		controlledSessionNetworkLabelMapV1(plan.LiveRunID),
		map[string]string{},
	)
	if err != nil {
		return nil, network.rollbackAfterPreparationFailureV1(
			fmt.Errorf("verify created controlled-session network %q: %w", networkPlan.Name, err),
			false,
		)
	}
	network.subnets = subnets
	return network, nil
}

// Attach connects exactly the controller and workload inert containers using
// their fixed aliases, then verifies that the network contains only those two
// exact full container IDs.
func (network *DockerSessionNetworkV1) Attach(ctx context.Context, controllerID string, workloadID string) error {
	network.mu.Lock()
	defer network.mu.Unlock()
	if network.cleaned {
		return fmt.Errorf("controlled-session network %q is already cleaned", network.network.Name)
	}
	if network.attachTried {
		return fmt.Errorf("controlled-session network %q attachment was already attempted", network.network.Name)
	}
	if err := validateDockerNetworkContainerIDsV1(controllerID, workloadID); err != nil {
		return fmt.Errorf("attach controlled-session network %q: %w", network.network.Name, err)
	}
	for _, participant := range []struct {
		id   string
		plan ControlledSessionContainerPlanV1
	}{
		{id: controllerID, plan: network.plan.Controller},
		{id: workloadID, plan: network.plan.Workload},
	} {
		if err := network.verifyInertContainerV1(ctx, participant.id, participant.plan); err != nil {
			return fmt.Errorf("attach controlled-session network %q: %w", network.network.Name, err)
		}
	}
	network.attachTried = true
	network.controllerID = controllerID
	network.workloadID = workloadID
	if ctx == nil {
		ctx = context.Background()
	}
	for _, attachment := range []struct {
		alias       string
		containerID string
	}{
		{alias: network.plan.Controller.SessionNetwork.Alias, containerID: controllerID},
		{alias: network.plan.Workload.SessionNetwork.Alias, containerID: workloadID},
	} {
		command := network.commandV1("network", "connect", "--alias", attachment.alias, network.networkID, attachment.containerID)
		if err := network.backend.run(command, RunOptions{Context: ctx}); err != nil {
			return fmt.Errorf("attach controlled-session %s container %q to network %q: %w", attachment.alias, attachment.containerID, network.network.Name, err)
		}
	}
	return network.verifyLockedV1(ctx)
}

// Verify checks exact network ownership, isolation mode, subnets, and member
// identities without changing Docker state.
func (network *DockerSessionNetworkV1) Verify(ctx context.Context) error {
	network.mu.Lock()
	defer network.mu.Unlock()
	if network.cleaned {
		return fmt.Errorf("controlled-session network %q is already cleaned", network.network.Name)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return network.verifyLockedV1(ctx)
}

func (network *DockerSessionNetworkV1) verifyLockedV1(ctx context.Context) error {
	inspection, found, err := network.inspectV1(ctx)
	if err != nil {
		return fmt.Errorf("inspect controlled-session network %q: %w", network.network.Name, err)
	}
	if !found {
		return fmt.Errorf("controlled-session network %q does not exist", network.network.Name)
	}
	wantContainers := map[string]string{}
	if network.attachTried {
		wantContainers[network.controllerID] = network.plan.Controller.Container
		wantContainers[network.workloadID] = network.plan.Workload.Container
	}
	subnets, err := validateDockerSessionNetworkInspectionV1(
		inspection,
		network.networkID,
		network.network.Name,
		controlledSessionNetworkLabelMapV1(network.plan.LiveRunID),
		wantContainers,
	)
	if err != nil {
		return fmt.Errorf("verify controlled-session network %q: %w", network.network.Name, err)
	}
	if len(network.subnets) != 0 && !slices.Equal(network.subnets, subnets) {
		return fmt.Errorf("verify controlled-session network %q: engine-assigned subnets changed", network.network.Name)
	}
	network.subnets = subnets
	return nil
}

// Cleanup disconnects only the exact selected containers, removes the exact
// full network ID, and independently verifies absence. Ownership-label or
// unexpected-member mismatches fail closed without mutating the network.
func (network *DockerSessionNetworkV1) Cleanup(ctx context.Context) error {
	network.mu.Lock()
	defer network.mu.Unlock()
	if network.cleaned {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	inspection, found, err := network.inspectV1(ctx)
	if err != nil {
		return fmt.Errorf("inspect controlled-session network %q before cleanup: %w", network.network.Name, err)
	}
	if !found {
		network.cleaned = true
		return nil
	}
	allowedContainers := map[string]string{}
	if network.attachTried {
		allowedContainers[network.controllerID] = network.plan.Controller.Container
		allowedContainers[network.workloadID] = network.plan.Workload.Container
	}
	actualContainers := make(map[string]string, len(inspection.Containers))
	for containerID, member := range inspection.Containers {
		wantName, allowed := allowedContainers[containerID]
		if !allowed || member.Name != wantName {
			return fmt.Errorf("refuse controlled-session network %q cleanup: unexpected network member %q", network.network.Name, containerID)
		}
		actualContainers[containerID] = wantName
	}
	if _, err := validateDockerSessionNetworkInspectionV1(
		inspection,
		network.networkID,
		network.network.Name,
		controlledSessionNetworkLabelMapV1(network.plan.LiveRunID),
		actualContainers,
	); err != nil {
		return fmt.Errorf("refuse controlled-session network %q cleanup: %w", network.network.Name, err)
	}
	memberIDs := make([]string, 0, len(inspection.Containers))
	for containerID := range inspection.Containers {
		memberIDs = append(memberIDs, containerID)
	}
	sort.Strings(memberIDs)
	for _, containerID := range memberIDs {
		command := network.commandV1("network", "disconnect", "--force", network.networkID, containerID)
		if err := network.backend.run(command, RunOptions{Context: ctx}); err != nil && !isMissingDockerNetworkErrorV1(err) {
			return fmt.Errorf("disconnect controlled-session container %q from network %q: %w", containerID, network.network.Name, err)
		}
	}
	remove := network.commandV1("network", "rm", network.networkID)
	if err := network.backend.run(remove, RunOptions{Context: ctx}); err != nil && !isMissingDockerNetworkErrorV1(err) {
		return fmt.Errorf("remove controlled-session network %q: %w", network.network.Name, err)
	}
	_, found, err = network.inspectV1(ctx)
	if err != nil {
		return fmt.Errorf("verify controlled-session network %q removal: %w", network.network.Name, err)
	}
	if found {
		return fmt.Errorf("controlled-session network %q still exists after removal", network.network.Name)
	}
	network.cleaned = true
	return nil
}

func (network *DockerSessionNetworkV1) rollbackAfterPreparationFailureV1(cause error, retryRecordOnFailure bool) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), defaultDockerPreflightTimeout)
	defer cancel()
	cleanupErr := network.removeExactCreatedNetworkV1(cleanupCtx)
	if cleanupErr == nil {
		if network.backend.recordRollbackVerified != nil {
			network.backend.recordRollbackVerified()
		}
		return cause
	}
	var retryErr error
	if retryRecordOnFailure && network.backend.recordNetworkID != nil {
		retryErr = network.backend.recordNetworkID(network.networkID)
		if retryErr != nil {
			retryErr = fmt.Errorf("retry controlled-session network %q exact ID after rollback failure: %w", network.network.Name, retryErr)
		}
	}
	return errors.Join(cause, fmt.Errorf("remove inert controlled-session network %q after preparation failure: %w", network.network.Name, cleanupErr), retryErr)
}

func (network *DockerSessionNetworkV1) removeExactCreatedNetworkV1(ctx context.Context) error {
	remove := network.commandV1("network", "rm", network.networkID)
	if err := network.backend.run(remove, RunOptions{Context: ctx}); err != nil && !isMissingDockerNetworkErrorV1(err) {
		return err
	}
	_, found, err := network.inspectV1(ctx)
	if err != nil {
		return err
	}
	if found {
		return fmt.Errorf("network still exists after removal")
	}
	return nil
}

func (network *DockerSessionNetworkV1) inspectV1(ctx context.Context) (dockerSessionNetworkInspectionV1, bool, error) {
	var output bytes.Buffer
	command := network.commandV1("network", "inspect", "--format", "{{json .}}", network.networkID)
	err := network.backend.run(command, RunOptions{Context: ctx, Stdout: &output, Stderr: &output})
	if err != nil {
		message := trimmedCommandOutput(output.String())
		if isMissingDockerNetworkResponseV1(err, message) {
			return dockerSessionNetworkInspectionV1{}, false, nil
		}
		if message != "" {
			return dockerSessionNetworkInspectionV1{}, false, fmt.Errorf("%w: %s", err, message)
		}
		return dockerSessionNetworkInspectionV1{}, false, err
	}
	var inspection dockerSessionNetworkInspectionV1
	if err := decodeOneDockerInspectionV1(output.Bytes(), &inspection); err != nil {
		return dockerSessionNetworkInspectionV1{}, false, fmt.Errorf("decode Docker network inspection: %w", err)
	}
	return inspection, true, nil
}

func (network *DockerSessionNetworkV1) verifyInertContainerV1(
	ctx context.Context,
	containerID string,
	plan ControlledSessionContainerPlanV1,
) error {
	var output bytes.Buffer
	command := network.commandV1("container", "inspect", "--format", "{{json .}}", containerID)
	err := network.backend.run(command, RunOptions{Context: ctx, Stdout: &output, Stderr: &output})
	if err != nil {
		message := trimmedCommandOutput(output.String())
		if message != "" {
			return fmt.Errorf("inspect controlled-session %s container %q: %w: %s", plan.Role, containerID, err, message)
		}
		return fmt.Errorf("inspect controlled-session %s container %q: %w", plan.Role, containerID, err)
	}
	var inspection dockerSessionContainerInspectionV1
	if err := decodeOneDockerInspectionV1(output.Bytes(), &inspection); err != nil {
		return fmt.Errorf("decode controlled-session %s container %q inspection: %w", plan.Role, containerID, err)
	}
	if inspection.ID != containerID {
		return fmt.Errorf("controlled-session %s container inspection returned full ID %q instead of %q", plan.Role, inspection.ID, containerID)
	}
	if inspection.Name != "/"+plan.Container {
		return fmt.Errorf("controlled-session %s container name is %q instead of %q", plan.Role, inspection.Name, plan.Container)
	}
	for name, value := range controlledSessionContainerLabelMapV1(plan) {
		if inspection.Config.Labels[name] != value {
			return fmt.Errorf("controlled-session %s container ownership label %q does not match the exact plan", plan.Role, name)
		}
	}
	if inspection.State.Running {
		return fmt.Errorf("controlled-session %s container %q must remain inert before network attachment", plan.Role, plan.Container)
	}
	return nil
}

func decodeOneDockerInspectionV1(content []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailer any
	if err := decoder.Decode(&trailer); err == nil {
		return fmt.Errorf("Docker inspection contains trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode Docker inspection trailer: %w", err)
	}
	return nil
}

func validateDockerSessionNetworkInspectionV1(
	inspection dockerSessionNetworkInspectionV1,
	wantID string,
	wantName string,
	wantLabels map[string]string,
	wantContainers map[string]string,
) ([]string, error) {
	if inspection.ID != wantID {
		return nil, fmt.Errorf("Docker returned full network ID %q instead of %q", inspection.ID, wantID)
	}
	if parsed, err := parseDockerNetworkIDV1(inspection.ID); err != nil || parsed != inspection.ID {
		return nil, fmt.Errorf("Docker returned noncanonical full network ID %q", inspection.ID)
	}
	if inspection.Name != wantName {
		return nil, fmt.Errorf("network name is %q instead of %q", inspection.Name, wantName)
	}
	if inspection.Scope != "local" || inspection.Driver != "bridge" || !inspection.Internal || inspection.Attachable || inspection.Ingress {
		return nil, fmt.Errorf("network must be a local, non-attachable, non-ingress, engine-internal bridge")
	}
	if !reflect.DeepEqual(inspection.Labels, wantLabels) {
		return nil, fmt.Errorf("network ownership labels do not match the exact live run")
	}
	if inspection.Containers == nil {
		inspection.Containers = map[string]dockerSessionNetworkContainerV1{}
	}
	if len(inspection.Containers) != len(wantContainers) {
		return nil, fmt.Errorf("network members do not match the exact controller and workload")
	}
	for containerID, wantName := range wantContainers {
		member, found := inspection.Containers[containerID]
		if !found || member.Name != wantName {
			return nil, fmt.Errorf("network member %q does not match container %q", containerID, wantName)
		}
	}
	subnets := make([]string, 0, len(inspection.IPAM.Config))
	for _, config := range inspection.IPAM.Config {
		if config.Subnet == "" {
			continue
		}
		_, parsed, err := net.ParseCIDR(config.Subnet)
		if err != nil || parsed.String() != config.Subnet {
			return nil, fmt.Errorf("network subnet %q is not canonical CIDR", config.Subnet)
		}
		subnets = append(subnets, config.Subnet)
	}
	sort.Strings(subnets)
	if len(subnets) == 0 {
		return nil, fmt.Errorf("network inspection contains no assigned subnet")
	}
	for index := 1; index < len(subnets); index++ {
		if subnets[index-1] == subnets[index] {
			return nil, fmt.Errorf("network inspection contains duplicate subnet %q", subnets[index])
		}
	}
	return subnets, nil
}

func controlledSessionNetworkLabelsV1(liveRunID string) []ControlledSessionLabelV1 {
	return []ControlledSessionLabelV1{
		{Name: "io.reploy.session.live-run", Value: liveRunID},
		{Name: "io.reploy.session.role", Value: controlledSessionNetworkRoleV1},
	}
}

func controlledSessionNetworkLabelMapV1(liveRunID string) map[string]string {
	labels := controlledSessionNetworkLabelsV1(liveRunID)
	result := make(map[string]string, len(labels))
	for _, label := range labels {
		result[label.Name] = label.Value
	}
	return result
}

func controlledSessionContainerLabelMapV1(plan ControlledSessionContainerPlanV1) map[string]string {
	result := make(map[string]string, len(plan.Labels))
	for _, label := range plan.Labels {
		result[label.Name] = label.Value
	}
	return result
}

func validateDockerNetworkContainerIDsV1(controllerID string, workloadID string) error {
	for _, value := range []struct {
		role string
		id   string
	}{
		{role: "controller", id: controllerID},
		{role: "workload", id: workloadID},
	} {
		role, id := value.role, value.id
		if _, err := parseDockerContainerIDV1(id); err != nil || id != strings.ToLower(id) {
			return fmt.Errorf("%s container ID must be an exact lowercase full Docker ID", role)
		}
	}
	if controllerID == workloadID {
		return fmt.Errorf("controller and workload container IDs must be different")
	}
	return nil
}

func parseDockerNetworkIDV1(output string) (string, error) {
	networkID := strings.TrimSpace(output)
	if len(networkID) != 64 {
		return "", fmt.Errorf("Docker create returned invalid full network ID %q", networkID)
	}
	if _, err := hex.DecodeString(networkID); err != nil || networkID != strings.ToLower(networkID) {
		return "", fmt.Errorf("Docker create returned invalid full network ID %q", networkID)
	}
	return networkID, nil
}

func isMissingDockerNetworkErrorV1(err error) bool {
	return isMissingDockerNetworkResponseV1(err, "")
}

func isMissingDockerNetworkResponseV1(err error, output string) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error() + " " + output)
	return strings.Contains(message, "no such network") || strings.Contains(message, "no such object") ||
		strings.Contains(message, "network") && strings.Contains(message, "not found")
}

func (network *DockerSessionNetworkV1) commandV1(args ...string) CommandSpec {
	command := network.docker
	command.Args = append([]string(nil), args...)
	return command
}

func cloneControlledSessionExecutionPlanV1(plan ControlledSessionExecutionPlanV1) ControlledSessionExecutionPlanV1 {
	clone := plan
	clone.Controller = cloneControlledSessionContainerPlanV1(plan.Controller)
	clone.Workload = cloneControlledSessionContainerPlanV1(plan.Workload)
	clone.Controller.RuntimeIdentity.SupplementaryGIDs = slices.Clone(plan.Controller.RuntimeIdentity.SupplementaryGIDs)
	clone.Workload.RuntimeIdentity.SupplementaryGIDs = slices.Clone(plan.Workload.RuntimeIdentity.SupplementaryGIDs)
	clone.Authorization.Controller.RuntimeIdentity.SupplementaryGIDs = slices.Clone(plan.Authorization.Controller.RuntimeIdentity.SupplementaryGIDs)
	clone.Authorization.Workload.RuntimeIdentity.SupplementaryGIDs = slices.Clone(plan.Authorization.Workload.RuntimeIdentity.SupplementaryGIDs)
	clone.Authorization.Operations = slices.Clone(plan.Authorization.Operations)
	clone.Authorization.EndpointIDs = slices.Clone(plan.Authorization.EndpointIDs)
	return clone
}
