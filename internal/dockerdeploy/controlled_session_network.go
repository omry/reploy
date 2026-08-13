package dockerdeploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

const controlledSessionNetworkRoleV1 = deploy.ControlledSessionNetworkRoleV1

const (
	controlledSessionNetworkSubnetBitsV1         = 29
	controlledSessionNetworkAllocationAttemptsV1 = 64
)

var controlledSessionDockerBuiltinAddressPoolsV1 = []dockerDefaultAddressPoolV1{
	{Base: "172.17.0.0/16", Size: 16},
	{Base: "172.18.0.0/16", Size: 16},
	{Base: "172.19.0.0/16", Size: 16},
	{Base: "172.20.0.0/14", Size: 16},
	{Base: "172.24.0.0/14", Size: 16},
	{Base: "172.28.0.0/14", Size: 16},
	{Base: "192.168.0.0/16", Size: 20},
}

var errControlledSessionNetworkActivationPendingV1 = errors.New("controlled-session network activation is not fully visible yet")

type dockerSessionNetworkBackendV1 struct {
	bind                   func(context.Context, CommandSpec, time.Duration) (CommandSpec, commandRunner, error)
	run                    commandRunner
	recordNetworkID        func(string) error
	recordRollbackVerified func()
}

type dockerDefaultAddressPoolV1 struct {
	Base string `json:"Base"`
	Size int    `json:"Size"`
}

type controlledSessionNetworkSubnetPoolV1 struct {
	Prefix         netip.Prefix
	AllocationBits int
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
	Subnet  string `json:"Subnet"`
	Gateway string `json:"Gateway"`
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
	NetworkSettings struct {
		Networks map[string]dockerSessionContainerNetworkV1 `json:"Networks"`
	} `json:"NetworkSettings"`
}

type dockerSessionContainerNetworkV1 struct {
	Aliases           []string                            `json:"Aliases"`
	IPAMConfig        *dockerSessionContainerIPAMConfigV1 `json:"IPAMConfig"`
	IPAddress         string                              `json:"IPAddress"`
	GlobalIPv6Address string                              `json:"GlobalIPv6Address"`
}

type dockerSessionContainerIPAMConfigV1 struct {
	IPv4Address string `json:"IPv4Address"`
	IPv6Address string `json:"IPv6Address"`
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
	realized  controlledSessionNetworkRealizationV1
	backend   dockerSessionNetworkBackendV1

	mu           sync.Mutex
	attachTried  bool
	controllerID string
	workloadID   string
	cleaned      bool
}

type controlledSessionNetworkRealizationV1 struct {
	Subnets             []string
	ControllerAddresses []string
	WorkloadAddresses   []string
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

func (network *DockerSessionNetworkV1) Realization() controlledSessionNetworkRealizationV1 {
	network.mu.Lock()
	defer network.mu.Unlock()
	return cloneControlledSessionNetworkRealizationV1(network.realized)
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
	subnetPools, err := discoverControlledSessionNetworkSubnetPoolsV1(ctx, docker, backend.run)
	if err != nil {
		return nil, fmt.Errorf("discover controlled-session Docker address pools: %w", err)
	}
	var networkID string
	var selectedSubnet string
	for attempt := 0; attempt < controlledSessionNetworkAllocationAttemptsV1; attempt++ {
		subnet, err := controlledSessionNetworkSubnetCandidateFromPoolsV1(plan.LiveRunID, subnetPools, attempt)
		if err != nil {
			return nil, fmt.Errorf("select controlled-session network subnet: %w", err)
		}
		create := docker
		create.Args = []string{
			"network", "create", "--driver", "bridge", "--internal", "--ipv6=false",
			"--subnet", subnet.String(), "--gateway", subnet.Addr().Next().String(),
		}
		for _, label := range labels {
			create.Args = append(create.Args, "--label", label.Name+"="+label.Value)
		}
		create.Args = append(create.Args, networkPlan.Name)
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if err := backend.run(create, RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr}); err != nil {
			output := trimmedCommandOutput(stderr.String())
			if isDockerNetworkSubnetOverlapV1(output) {
				if attempt+1 < controlledSessionNetworkAllocationAttemptsV1 {
					continue
				}
				return nil, fmt.Errorf(
					"allocate controlled-session network %q: Docker rejected %d subnet candidates as overlapping",
					networkPlan.Name, controlledSessionNetworkAllocationAttemptsV1,
				)
			}
			if output != "" {
				err = fmt.Errorf("%w\ncommand output:\n%s", err, output)
			}
			return nil, fmt.Errorf("create controlled-session network %q: %w; refusing cleanup because the creating attempt did not return an exact network ID", networkPlan.Name, err)
		}
		networkID, err = parseDockerNetworkIDV1(stdout.String())
		if err != nil {
			return nil, fmt.Errorf("create controlled-session network %q: %w; refusing name-based cleanup because the created network identity is unknown", networkPlan.Name, err)
		}
		selectedSubnet = subnet.String()
		break
	}
	network := &DockerSessionNetworkV1{
		plan: plan, network: networkPlan, docker: docker, networkID: networkID,
		subnets: []string{selectedSubnet}, backend: backend,
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
		network.subnets,
	)
	if err != nil {
		return nil, network.rollbackAfterPreparationFailureV1(
			fmt.Errorf("verify created controlled-session network %q: %w", networkPlan.Name, err),
			false,
		)
	}
	realized, err := deriveControlledSessionNetworkRealizationV1(subnets)
	if err != nil {
		return nil, network.rollbackAfterPreparationFailureV1(
			fmt.Errorf("derive controlled-session network addresses: %w", err),
			false,
		)
	}
	network.realized = realized
	return network, nil
}

func discoverControlledSessionNetworkSubnetPoolsV1(
	ctx context.Context,
	docker CommandSpec,
	run commandRunner,
) ([]controlledSessionNetworkSubnetPoolV1, error) {
	command := docker
	command.Args = []string{"info", "--format", "{{json .DefaultAddressPools}}"}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(command, RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr}); err != nil {
		if output := trimmedCommandOutput(stderr.String()); output != "" {
			err = fmt.Errorf("%w\ncommand output:\n%s", err, output)
		}
		return nil, fmt.Errorf("inspect Docker default address pools: %w", err)
	}
	return parseControlledSessionNetworkSubnetPoolsV1(stdout.Bytes())
}

func parseControlledSessionNetworkSubnetPoolsV1(content []byte) ([]controlledSessionNetworkSubnetPoolV1, error) {
	var configured []dockerDefaultAddressPoolV1
	if err := decodeOneDockerInspectionV1(content, &configured); err != nil {
		return nil, fmt.Errorf("decode Docker default address pools: %w", err)
	}
	if len(configured) == 0 {
		configured = controlledSessionDockerBuiltinAddressPoolsV1
	}
	return controlledSessionNetworkSubnetPoolsFromConfigV1(configured)
}

func controlledSessionNetworkSubnetPoolsFromConfigV1(configured []dockerDefaultAddressPoolV1) ([]controlledSessionNetworkSubnetPoolV1, error) {
	pools := make([]controlledSessionNetworkSubnetPoolV1, 0, len(configured))
	for index, configuredPool := range configured {
		prefix, err := netip.ParsePrefix(configuredPool.Base)
		if err != nil {
			return nil, fmt.Errorf("address pool %d base %q is invalid: %w", index, configuredPool.Base, err)
		}
		if prefix != prefix.Masked() {
			return nil, fmt.Errorf("address pool %d base %q is not canonical", index, configuredPool.Base)
		}
		if configuredPool.Size < prefix.Bits() || configuredPool.Size > prefix.Addr().BitLen() {
			return nil, fmt.Errorf("address pool %d size %d is invalid for %s", index, configuredPool.Size, prefix)
		}
		if !prefix.Addr().Is4() || prefix.Bits() > controlledSessionNetworkSubnetBitsV1 || configuredPool.Size > controlledSessionNetworkSubnetBitsV1 {
			continue
		}
		for _, existing := range pools {
			if existing.Prefix.Contains(prefix.Addr()) || prefix.Contains(existing.Prefix.Addr()) {
				return nil, fmt.Errorf("address pool %d %s overlaps %s", index, prefix, existing.Prefix)
			}
		}
		pools = append(pools, controlledSessionNetworkSubnetPoolV1{Prefix: prefix, AllocationBits: configuredPool.Size})
	}
	if len(pools) == 0 {
		return nil, fmt.Errorf("Docker exposes no IPv4 default address pool that can contain a /%d session subnet", controlledSessionNetworkSubnetBitsV1)
	}
	return pools, nil
}

func controlledSessionNetworkSubnetCandidateV1(liveRunID string, attempt int) (netip.Prefix, error) {
	pools, err := controlledSessionNetworkSubnetPoolsFromConfigV1(controlledSessionDockerBuiltinAddressPoolsV1)
	if err != nil {
		return netip.Prefix{}, err
	}
	return controlledSessionNetworkSubnetCandidateFromPoolsV1(liveRunID, pools, attempt)
}

func controlledSessionNetworkSubnetCandidateFromPoolsV1(
	liveRunID string,
	pools []controlledSessionNetworkSubnetPoolV1,
	attempt int,
) (netip.Prefix, error) {
	if err := deploy.ValidateLiveRunIDV1(liveRunID); err != nil {
		return netip.Prefix{}, err
	}
	if attempt < 0 || attempt >= controlledSessionNetworkAllocationAttemptsV1 {
		return netip.Prefix{}, fmt.Errorf("allocation attempt must be in 0..%d", controlledSessionNetworkAllocationAttemptsV1-1)
	}
	if len(pools) == 0 {
		return netip.Prefix{}, fmt.Errorf("no Docker address pools")
	}
	var pool controlledSessionNetworkSubnetPoolV1
	var round uint64
	remaining := attempt
	found := false
	for candidateRound := uint64(0); candidateRound < controlledSessionNetworkAllocationAttemptsV1 && !found; candidateRound++ {
		for _, candidatePool := range pools {
			slots := uint64(1) << uint(controlledSessionNetworkSubnetBitsV1-candidatePool.Prefix.Bits())
			if candidateRound >= slots {
				continue
			}
			if remaining == 0 {
				pool = candidatePool
				round = candidateRound
				found = true
				break
			}
			remaining--
		}
	}
	if !found {
		return netip.Prefix{}, fmt.Errorf("Docker default address pools contain fewer than %d distinct /%d candidates", attempt+1, controlledSessionNetworkSubnetBitsV1)
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", liveRunID, pool.Prefix, pool.AllocationBits)))
	slotBits := controlledSessionNetworkSubnetBitsV1 - pool.Prefix.Bits()
	slotMask := uint64(1<<slotBits) - 1
	start := binary.BigEndian.Uint64(digest[:8]) & slotMask
	stride := (binary.BigEndian.Uint64(digest[8:16]) & slotMask) | 1
	slot := (start + round*stride) & slotMask
	base := pool.Prefix.Addr().As4()
	address := binary.BigEndian.Uint32(base[:]) + uint32(slot<<uint(32-controlledSessionNetworkSubnetBitsV1))
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], address)
	return netip.PrefixFrom(netip.AddrFrom4(encoded), controlledSessionNetworkSubnetBitsV1), nil
}

func isDockerNetworkSubnetOverlapV1(output string) bool {
	return strings.Contains(strings.ToLower(output), "pool overlaps with other one on this address space")
}

// Attach verifies the exact controller and workload inert containers, connects
// participants that retain separately granted ordinary networking, and then
// verifies that the session network contains only those two exact full IDs.
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
	participants := []struct {
		id   string
		plan ControlledSessionContainerPlanV1
	}{
		{id: controllerID, plan: network.plan.Controller},
		{id: workloadID, plan: network.plan.Workload},
	}
	for _, participant := range participants {
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
	for _, participant := range participants {
		args := []string{"network", "connect", "--alias", participant.plan.SessionNetwork.Alias}
		addresses := network.realized.ControllerAddresses
		if participant.plan.Role == ControlledSessionRoleWorkloadV1 {
			addresses = network.realized.WorkloadAddresses
		}
		for _, address := range addresses {
			parsed, err := netip.ParseAddr(address)
			if err != nil {
				return fmt.Errorf("attach controlled-session %s container: invalid frozen address %q", participant.plan.Role, address)
			}
			option := "--ip6"
			if parsed.Is4() {
				option = "--ip"
			}
			args = append(args, option, address)
		}
		args = append(args, network.networkID, participant.id)
		command := network.commandV1(args...)
		if err := network.backend.run(command, RunOptions{Context: ctx}); err != nil {
			return fmt.Errorf("attach controlled-session %s container %q to network %q: %w", participant.plan.SessionNetwork.Alias, participant.id, network.network.Name, err)
		}
		if participant.plan.Network == controlledSessionNetworkModeV1 {
			disconnect := network.commandV1(
				"network", "disconnect", "--force", controlledSessionOrdinaryNetworkModeV1, participant.id,
			)
			if err := network.backend.run(disconnect, RunOptions{Context: ctx}); err != nil {
				return fmt.Errorf("remove inert controlled-session %s container %q ordinary network staging attachment: %w", participant.plan.Role, participant.id, err)
			}
		}
		if err := network.verifyAttachedInertContainerV1(ctx, participant.id, participant.plan); err != nil {
			return fmt.Errorf("verify controlled-session %s container %q network attachment: %w", participant.plan.Role, participant.id, err)
		}
	}
	return network.verifyLockedV1(ctx)
}

func deriveControlledSessionNetworkRealizationV1(subnets []string) (controlledSessionNetworkRealizationV1, error) {
	realized := controlledSessionNetworkRealizationV1{Subnets: slices.Clone(subnets)}
	families := map[bool]bool{}
	for _, subnet := range subnets {
		prefix, err := netip.ParsePrefix(subnet)
		if err != nil || prefix != prefix.Masked() {
			return controlledSessionNetworkRealizationV1{}, fmt.Errorf("network prefix %q is not canonical", subnet)
		}
		ipv4 := prefix.Addr().Is4()
		if families[ipv4] {
			return controlledSessionNetworkRealizationV1{}, fmt.Errorf("network has multiple prefixes for one address family")
		}
		families[ipv4] = true
		controller := prefix.Addr().Next().Next()
		workload := controller.Next()
		if !controller.IsValid() || !workload.IsValid() || !prefix.Contains(workload.Next()) {
			return controlledSessionNetworkRealizationV1{}, fmt.Errorf("network prefix %q has no safe controller and workload addresses", subnet)
		}
		realized.ControllerAddresses = append(realized.ControllerAddresses, controller.String())
		realized.WorkloadAddresses = append(realized.WorkloadAddresses, workload.String())
	}
	if len(realized.ControllerAddresses) == 0 {
		return controlledSessionNetworkRealizationV1{}, fmt.Errorf("network has no participant addresses")
	}
	slices.Sort(realized.ControllerAddresses)
	slices.Sort(realized.WorkloadAddresses)
	return realized, nil
}

func cloneControlledSessionNetworkRealizationV1(value controlledSessionNetworkRealizationV1) controlledSessionNetworkRealizationV1 {
	return controlledSessionNetworkRealizationV1{
		Subnets:             slices.Clone(value.Subnets),
		ControllerAddresses: slices.Clone(value.ControllerAddresses),
		WorkloadAddresses:   slices.Clone(value.WorkloadAddresses),
	}
}

func controlledSessionCreateWithPeerHostsV1(
	command CommandSpec,
	plan ControlledSessionContainerPlanV1,
	peerAddresses []string,
) (CommandSpec, error) {
	command.Args = slices.Clone(command.Args)
	if !plan.SessionNetwork.Enabled {
		if len(peerAddresses) != 0 {
			return CommandSpec{}, fmt.Errorf("disabled session network received peer addresses")
		}
		return command, nil
	}
	if len(peerAddresses) == 0 {
		return CommandSpec{}, fmt.Errorf("enabled session network received no peer addresses")
	}
	insertAt := slices.Index(command.Args, "--entrypoint")
	if insertAt < 0 {
		return CommandSpec{}, fmt.Errorf("controlled-session create command has no fixed entrypoint boundary")
	}
	options := make([]string, 0, len(peerAddresses)*2)
	previous := ""
	for _, address := range peerAddresses {
		parsed, err := netip.ParseAddr(address)
		if err != nil || parsed.String() != address || previous != "" && previous >= address {
			return CommandSpec{}, fmt.Errorf("peer addresses must be canonical, unique, and sorted")
		}
		options = append(options, "--add-host", plan.SessionNetwork.PeerAlias+"="+address)
		previous = address
	}
	command.Args = slices.Insert(command.Args, insertAt, options...)
	return command, nil
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

// VerifyStarted requires the controller and every still-running workload to
// appear as exact network members after Docker has started them. A workload
// that has already exited retains its verified fixed IPAM request without
// being misclassified as a startup failure.
func (network *DockerSessionNetworkV1) VerifyStarted(ctx context.Context) error {
	network.mu.Lock()
	defer network.mu.Unlock()
	if network.cleaned {
		return fmt.Errorf("controlled-session network %q is already cleaned", network.network.Name)
	}
	if !network.attachTried {
		return fmt.Errorf("controlled-session network %q participants were not selected", network.network.Name)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := network.verifyStartedContainerNetworkV1(ctx, network.controllerID, network.plan.Controller, false)
		if err == nil {
			var workloadRunning bool
			workloadRunning, err = network.verifyStartedContainerNetworkV1(ctx, network.workloadID, network.plan.Workload, true)
			if err == nil {
				required := []string{network.controllerID}
				if workloadRunning {
					required = append(required, network.workloadID)
				}
				err = network.verifyLockedV1(ctx, required...)
			}
		}
		if err == nil {
			return nil
		}
		if !errors.Is(err, errControlledSessionNetworkActivationPendingV1) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("verify controlled-session network %q: %w", network.network.Name, err)
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (network *DockerSessionNetworkV1) verifyStartedContainerNetworkV1(
	ctx context.Context,
	containerID string,
	plan ControlledSessionContainerPlanV1,
	allowStopped bool,
) (bool, error) {
	var output bytes.Buffer
	command := network.commandV1("container", "inspect", "--format", "{{json .}}", containerID)
	if err := network.backend.run(command, RunOptions{Context: ctx, Stdout: &output, Stderr: &output}); err != nil {
		return false, fmt.Errorf("inspect started controlled-session %s container: %w", plan.Role, err)
	}
	var inspection dockerSessionContainerInspectionV1
	if err := decodeOneDockerInspectionV1(output.Bytes(), &inspection); err != nil {
		return false, err
	}
	running := inspection.State.Running
	if !running && !allowStopped {
		return false, fmt.Errorf("controlled-session %s container is not running during exact network verification", plan.Role)
	}
	connection, found := inspection.NetworkSettings.Networks[plan.SessionNetwork.Name]
	if !found {
		if running {
			return true, errControlledSessionNetworkActivationPendingV1
		}
		return false, fmt.Errorf("controlled-session %s container lost the exact session network", plan.Role)
	}
	if !slices.Contains(connection.Aliases, plan.SessionNetwork.Alias) {
		return running, fmt.Errorf("controlled-session %s container lost its exact session-network alias", plan.Role)
	}
	addresses := network.realized.ControllerAddresses
	if plan.Role == ControlledSessionRoleWorkloadV1 {
		addresses = network.realized.WorkloadAddresses
	}
	for _, address := range addresses {
		parsed, _ := netip.ParseAddr(address)
		got := connection.GlobalIPv6Address
		if parsed.Is4() {
			got = connection.IPAddress
		}
		if !running {
			if connection.IPAMConfig == nil {
				return false, fmt.Errorf("stopped controlled-session %s container lost its fixed network address request", plan.Role)
			}
			if parsed.Is4() {
				got = connection.IPAMConfig.IPv4Address
			} else {
				got = connection.IPAMConfig.IPv6Address
			}
		}
		if running && got == "" {
			return true, errControlledSessionNetworkActivationPendingV1
		}
		if got != address {
			return running, fmt.Errorf("started controlled-session %s container address is %q instead of %q", plan.Role, got, address)
		}
	}
	return running, nil
}

func (network *DockerSessionNetworkV1) verifyLockedV1(ctx context.Context, requiredContainerIDs ...string) error {
	inspection, found, err := network.inspectV1(ctx)
	if err != nil {
		return fmt.Errorf("inspect controlled-session network %q: %w", network.network.Name, err)
	}
	if !found {
		return fmt.Errorf("controlled-session network %q does not exist", network.network.Name)
	}
	wantContainers := map[string]string{}
	if network.attachTried {
		selected := map[string]string{
			network.controllerID: network.plan.Controller.Container,
			network.workloadID:   network.plan.Workload.Container,
		}
		for containerID, member := range inspection.Containers {
			wantName, found := selected[containerID]
			if !found || member.Name != wantName {
				return fmt.Errorf("verify controlled-session network %q: unexpected network member %q", network.network.Name, containerID)
			}
			wantContainers[containerID] = wantName
		}
		for _, containerID := range requiredContainerIDs {
			if _, selected := selected[containerID]; !selected {
				return fmt.Errorf("verify controlled-session network %q: required member %q was not selected", network.network.Name, containerID)
			}
			if _, found := wantContainers[containerID]; !found {
				return errControlledSessionNetworkActivationPendingV1
			}
		}
	}
	subnets, err := validateDockerSessionNetworkInspectionV1(
		inspection,
		network.networkID,
		network.network.Name,
		controlledSessionNetworkLabelMapV1(network.plan.LiveRunID),
		wantContainers,
		network.subnets,
	)
	if err != nil {
		return fmt.Errorf("verify controlled-session network %q: %w", network.network.Name, err)
	}
	if !slices.Equal(network.subnets, subnets) {
		return fmt.Errorf("verify controlled-session network %q: selected subnets changed", network.network.Name)
	}
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
		network.subnets,
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
	if _, found := inspection.NetworkSettings.Networks[plan.SessionNetwork.Name]; found ||
		len(inspection.NetworkSettings.Networks) != 1 {
		return fmt.Errorf("controlled-session %s container %q must begin with only the inert ordinary network staging attachment", plan.Role, plan.Container)
	}
	if _, found := inspection.NetworkSettings.Networks[controlledSessionOrdinaryNetworkModeV1]; !found {
		return fmt.Errorf("controlled-session %s container %q is missing its inert ordinary network staging attachment", plan.Role, plan.Container)
	}
	return nil
}

func (network *DockerSessionNetworkV1) verifyAttachedInertContainerV1(
	ctx context.Context,
	containerID string,
	plan ControlledSessionContainerPlanV1,
) error {
	var output bytes.Buffer
	command := network.commandV1("container", "inspect", "--format", "{{json .}}", containerID)
	if err := network.backend.run(command, RunOptions{Context: ctx, Stdout: &output, Stderr: &output}); err != nil {
		return fmt.Errorf("inspect attached controlled-session %s container: %w: %s", plan.Role, err, trimmedCommandOutput(output.String()))
	}
	var inspection dockerSessionContainerInspectionV1
	if err := decodeOneDockerInspectionV1(output.Bytes(), &inspection); err != nil {
		return fmt.Errorf("decode attached controlled-session %s container inspection: %w", plan.Role, err)
	}
	if inspection.State.Running {
		return fmt.Errorf("controlled-session %s container started before network isolation was verified", plan.Role)
	}
	connection, found := inspection.NetworkSettings.Networks[plan.SessionNetwork.Name]
	if !found || !slices.Contains(connection.Aliases, plan.SessionNetwork.Alias) {
		return fmt.Errorf("controlled-session %s container does not carry its exact session network and alias", plan.Role)
	}
	addresses := network.realized.ControllerAddresses
	if plan.Role == ControlledSessionRoleWorkloadV1 {
		addresses = network.realized.WorkloadAddresses
	}
	for _, address := range addresses {
		parsed, _ := netip.ParseAddr(address)
		got := connection.GlobalIPv6Address
		if got == "" && connection.IPAMConfig != nil {
			got = connection.IPAMConfig.IPv6Address
		}
		if parsed.Is4() {
			got = connection.IPAddress
			if got == "" && connection.IPAMConfig != nil {
				got = connection.IPAMConfig.IPv4Address
			}
		}
		if got != address {
			return fmt.Errorf("controlled-session %s container address is %q instead of %q", plan.Role, got, address)
		}
	}
	wantNetworks := 2
	if plan.Network == controlledSessionNetworkModeV1 {
		wantNetworks = 1
	} else if _, found := inspection.NetworkSettings.Networks[controlledSessionOrdinaryNetworkModeV1]; !found {
		return fmt.Errorf("controlled-session %s container lost its explicitly granted ordinary network", plan.Role)
	}
	if len(inspection.NetworkSettings.Networks) != wantNetworks {
		return fmt.Errorf("controlled-session %s container has unexpected network attachments", plan.Role)
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
	wantSubnets []string,
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
	gateways := map[string]string{}
	for _, config := range inspection.IPAM.Config {
		if config.Subnet == "" {
			continue
		}
		_, parsed, err := net.ParseCIDR(config.Subnet)
		if err != nil || parsed.String() != config.Subnet {
			return nil, fmt.Errorf("network subnet %q is not canonical CIDR", config.Subnet)
		}
		subnets = append(subnets, config.Subnet)
		gateways[config.Subnet] = config.Gateway
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
	if !slices.Equal(subnets, wantSubnets) {
		return nil, fmt.Errorf("network subnets %q do not match the selected subnets %q", subnets, wantSubnets)
	}
	for _, subnet := range subnets {
		prefix, _ := netip.ParsePrefix(subnet)
		wantGateway := prefix.Addr().Next().String()
		if gateways[subnet] != wantGateway {
			return nil, fmt.Errorf("network subnet %q gateway is %q instead of %q", subnet, gateways[subnet], wantGateway)
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
