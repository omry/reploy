package dockerdeploy

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/identitymapping"
	"github.com/omry/reploy/internal/mappedlaunch"
	"github.com/omry/reploy/internal/providers/registry"
)

// These are actual private operations, not caller assertions of exec safety.
type mappedEntryKindV1 uint8

const (
	mappedWorkloadStartV1 mappedEntryKindV1 = iota + 1
	mappedApplicationCommandV1
	mappedRuntimeTestV1
	mappedShellV1
	mappedControlledSessionV1
	mappedLifecycleHookV1
	mappedHostReplacementV1
	mappedDeployedCommandV1
	mappedHostNativeCommandV1
)

type mappedEntryInputV1 struct {
	Current                CurrentBuild
	Runtime                DockerExecutionPlan
	Kind                   mappedEntryKindV1
	Command                string
	Forwarded, Environment []string
	Stdin, TTY             bool
	PrivateEnvironment     bool
	ObservedExit           bool
	KernelLastCapability   int
}

// The payload is frozen canonical JSON. Neither input aliases nor returned
// decoded slices can change it after Binding.Plan is computed. This is not an
// admission token; the held-lock owner must still run the E1.2 writer and F gate.
type mappedEntryPlanV1 struct {
	binding mappedlaunch.Binding
	payload string
}

type mappedEntryPayloadV1 struct {
	Installation, Operation, Run, Engine string
	Generation, Image                    canonical.Digest
	Revision                             string
	Kind, Mode                           string
	Command, Executable, Role            string
	UID, GID                             string
	Groups                               []string
	Argv, Environment, Startup           []string
	Active                               deploy.IdentityMappingGeometryV1
	Kernel                               mappedKernelFactsV1
	RuntimePolicy, NetworkPolicy         canonical.Digest
	RequiredCode                         []string
	RuntimePlan                          deploy.RuntimePlanV1
	Mounts                               []MountExecutionPlan
	Stdin                                bool
}

func mappedEntryModeForKindV1(kind mappedEntryKindV1) (mappedEntryModeV1, error) {
	switch kind {
	case mappedWorkloadStartV1, mappedApplicationCommandV1, mappedRuntimeTestV1, mappedLifecycleHookV1, mappedHostReplacementV1:
		return mappedEntryFreshInstanceV1, nil
	case mappedShellV1, mappedControlledSessionV1, mappedDeployedCommandV1:
		return mappedEntryOccupiedExecV1, nil
	case mappedHostNativeCommandV1:
		return mappedEntryHostNativeV1, nil
	default:
		return 0, fmt.Errorf("unknown mapped controlled entry kind")
	}
}

// constructMappedEntryPlanV1 accepts snapshots from the existing held-lock
// readers. All operations here are pure; it neither authenticates a public
// caller nor reads a store, acquires a lock, changes state or contacts an engine.
func constructMappedEntryPlanV1(input mappedEntryInputV1) (mappedEntryPlanV1, error) {
	fail := func(err error) (mappedEntryPlanV1, error) {
		return mappedEntryPlanV1{}, fmt.Errorf("mapped entry plan: %w", err)
	}
	mode, err := mappedEntryModeForKindV1(input.Kind)
	if err != nil {
		return fail(err)
	}
	c := input.Current
	document, err := runtimeDocumentForEntryModeV1(c.State, mode)
	if err != nil {
		return fail(err)
	}
	if input.TTY || input.PrivateEnvironment || input.Runtime.PrivateEnvironment || input.ObservedExit {
		return fail(fmt.Errorf("mapped TTY, private environment and observed-exit transport remain unqualified"))
	}
	if err := deploy.ValidateStateV1(c.State); err != nil {
		return fail(err)
	}
	if c.State.Current == nil || !reflect.DeepEqual(*c.State.Current, c.Generation) || c.State.Platform != c.Generation.Platform {
		return fail(fmt.Errorf("selected state and generation differ"))
	}
	if c.State.TerminalRemoval || c.State.Staging != nil && (input.Runtime.Phase != blueprint.PhaseStaged || input.Runtime.Scope != nil) ||
		c.State.Deployment != nil && (input.Runtime.Phase != blueprint.PhaseInstalled || input.Runtime.Scope == nil || string(*input.Runtime.Scope) != c.State.Deployment.Installation.Scope) {
		return fail(fmt.Errorf("runtime phase or scope differs from selected owner"))
	}
	if err := validateGenerationBuildLock(c.Generation, c.Lock, registry.ValidateRequirementProfileV1); err != nil {
		return fail(err)
	}
	if err := validateSelectedIdentityPolicyV1(c.State.Blueprint, c.Lock); err != nil {
		return fail(err)
	}
	m := c.State.IdentityMapping
	if m == nil || m.Schema != deploy.IdentityMappingStateSchemaV2 || m.Revoked || c.Lock.IdentityPolicy == nil || m.PendingOperation == nil {
		return fail(fmt.Errorf("requires native pending operation and locked identity policy"))
	}
	p := m.PendingOperation
	if p.Kind != "native-execute" || p.Native == nil || !p.Inhibited || (p.Phase != "intent" && p.Phase != "effects-pending") || p.ExpectedRevision != m.Revision || !reflect.DeepEqual(p.ExpectedGeneration, c.State.Current) {
		return fail(fmt.Errorf("native attempt generation, revision or ownership changed"))
	}
	plans, err := RuntimePlansV1(document, input.Runtime)
	if err != nil || !reflect.DeepEqual(plans, c.Lock.RuntimePolicy.Plans) || input.Runtime.Image != c.Generation.Reference {
		return fail(fmt.Errorf("runtime shape differs from selected locked policy: %v", err))
	}
	planID := runtimeCommandPlanID(input.Command, false)
	entryRuntime := input.Runtime
	if input.Kind == mappedWorkloadStartV1 || input.Kind == mappedHostReplacementV1 {
		if document.Environment.Workload == nil || document.Environment.Workload.Command != input.Command {
			return fail(fmt.Errorf("entry does not select the locked workload command"))
		}
		planID = runtimeWorkloadPlanID
	} else {
		entryRuntime, err = effectiveCommandDockerPlanV1(document, input.Runtime, input.Command)
		if err != nil {
			return fail(err)
		}
	}
	var runtimePlan *deploy.RuntimePlanV1
	for i := range plans {
		if plans[i].ID == planID {
			runtimePlan = &plans[i]
		}
	}
	if runtimePlan == nil {
		return fail(fmt.Errorf("controlled entry is absent from the locked runtime inventory"))
	}
	forwarded, err := validateForwardedArguments(input.Command, document.Environment.Commands[input.Command].ForwardFlags, input.Forwarded)
	if err != nil {
		return fail(err)
	}
	command, err := resolveLockedEnvironmentCommandForPlanV1(document, c.Lock.Catalog, input.Runtime, input.Command, forwarded)
	if err != nil {
		return fail(err)
	}
	_, executable, found := document.Environment.ResolveExecutableProfile(document.Environment.Commands[input.Command].Executable)
	role, roleFound := c.Lock.IdentityPolicy.Policy.Roles[executable.Role]
	if !found || !roleFound || executable.Role == "" || len(command.Argv) == 0 || !mappedCodeAbsoluteV1(command.Argv[0]) {
		return fail(fmt.Errorf("entry requires an image-locked executable and explicit role"))
	}
	policy := c.Lock.IdentityPolicy.Policy
	user := policy.Users[role.User]
	uid, _ := strconv.ParseUint(user.UID, 10, 32)
	gid, _ := strconv.ParseUint(policy.Groups[user.PrimaryGroup], 10, 32)
	groups := make([]uint32, len(user.SupplementaryGroups))
	for i, name := range user.SupplementaryGroups {
		id, _ := strconv.ParseUint(policy.Groups[name], 10, 32)
		groups[i] = uint32(id)
	}
	groups, err = normalizeSupplementaryGIDsV1(uint32(gid), groups)
	if err != nil {
		return fail(err)
	}
	var reserved, accepted *identitymapping.Geometry
	if len(m.UIDAssignments) != 0 {
		g := mappedStateGeometryV1(deploy.IdentityMappingGeometryV1{Profile: m.Profile, UIDs: m.UIDAssignments, GIDs: m.GIDAssignments})
		reserved = &g
	}
	if len(*m.AcceptedUIDs) != 0 {
		g := mappedStateGeometryV1(deploy.IdentityMappingGeometryV1{Profile: m.Profile, UIDs: *m.AcceptedUIDs, GIDs: *m.AcceptedGIDs})
		accepted = &g
	}
	maps, err := deploy.PlanIdentityExecutionV1(deploy.IdentityExecutionInputV1{
		Generation: c.Generation.BuildLockDigest, Policy: c.Lock.IdentityPolicy,
		Candidate: mappedStateGeometryV1(p.Candidate), Reservations: reserved, Accepted: accepted,
		RuntimeUsers: p.Native.RuntimeUsers, RuntimeGroups: p.Native.RuntimeGroups,
	})
	if err != nil || !reflect.DeepEqual(maps.Active, mappedStateGeometryV1(p.Native.Active)) {
		return fail(fmt.Errorf("pending active maps differ from current closure: %v", err))
	}
	final := uint64(0)
	for _, name := range role.Capabilities {
		bit := slices.Index(mappedCapabilityNamesV1, strings.TrimPrefix(name, "CAP_"))
		if bit < 0 {
			return fail(fmt.Errorf("unknown locked role capability"))
		}
		final |= uint64(1) << bit
	}
	sandbox := newApplicationSandboxPlanWithNetworkV1(RuntimeUserPlan{UID: uint32(uid), GID: uint32(gid), SupplementaryGIDs: groups}, c.Lock.RuntimePolicy.Network)
	// The supported Podman renderer prepares the fixed private home before
	// dropping setup authority. Bind that existing step before hashing the plan.
	setup := final | uint64(1) // CHOWN, used only by temporary-home setup.
	for _, name := range applicationSetupCapabilitiesV1(sandbox) {
		setup |= uint64(1) << slices.Index(mappedCapabilityNamesV1, name)
	}
	kernel, err := compileMappedSeccompV1(podmanDockerSeccompProfile, c.Generation.Platform.Architecture, final, setup, input.KernelLastCapability)
	if err != nil {
		return fail(err)
	}
	ports := make([]int, len(runtimePlan.InboundTCP))
	for i, port := range runtimePlan.InboundTCP {
		ports[i], _ = strconv.Atoi(port)
	}
	startup := sandboxApplicationArgvV1(DockerExecutionPlan{Sandbox: sandbox}, command.Argv, true, ports)
	startup = slices.Insert(startup, 1, fmt.Sprintf("--mapped-capabilities=%016x", final), podmanPrepareTemporaryHome)
	if _, err := mappedStartupCommandV1(startup, kernel); err != nil {
		return fail(err)
	}
	environment := append([]string{}, input.Environment...)
	slices.Sort(environment)
	keys := map[string]bool{}
	for _, value := range environment {
		key, _, ok := strings.Cut(value, "=")
		if !ok || key == "" || keys[key] || strings.ContainsAny(value, "\x00") || !utf8.ValidString(value) {
			return fail(fmt.Errorf("invalid or duplicate mapped environment entry"))
		}
		keys[key] = true
	}
	for _, arg := range command.Argv {
		if !utf8.ValidString(arg) || strings.ContainsRune(arg, 0) {
			return fail(fmt.Errorf("invalid mapped argument"))
		}
	}
	required := []string{deploy.ApplicationStartupVerifierPathV1, command.Argv[0]}
	for _, code := range c.Lock.RuntimePolicy.ProtectedPaths {
		required = append(required, code.Path)
	}
	slices.Sort(required)
	network, _ := canonical.Sum("mapped-network", "v1", c.Lock.RuntimePolicy.Network)
	groupIDs := make([]string, len(groups))
	for i, group := range groups {
		groupIDs[i] = runtimeIDStringV1(group)
	}
	payload := mappedEntryPayloadV1{
		Installation: m.InstallationID, Operation: p.OperationID, Run: p.Native.Process, Engine: p.Native.Engine,
		Generation: c.Generation.BuildLockDigest, Image: c.Lock.FinalImage.ConfigDigest, Revision: m.Revision,
		Kind: strconv.Itoa(int(input.Kind)), Mode: strconv.Itoa(int(mode)), Command: input.Command, Executable: document.Environment.Commands[input.Command].Executable, Role: executable.Role,
		UID: user.UID, GID: policy.Groups[user.PrimaryGroup], Groups: groupIDs, Argv: command.Argv, Environment: environment, Startup: startup,
		Active: p.Native.Active, Kernel: kernel.facts(), RuntimePolicy: c.Generation.RuntimePolicyDigest, NetworkPolicy: network,
		RequiredCode: slices.Compact(required), RuntimePlan: *runtimePlan, Mounts: entryRuntime.Mounts, Stdin: input.Stdin,
	}
	digest, err := canonical.Sum("mapped-entry", "v1", payload)
	if err != nil {
		return fail(err)
	}
	binding := mappedlaunch.Binding{Schema: "mapped-release-v1", Installation: payload.Installation, Operation: payload.Operation,
		Generation: payload.Generation, Revision: payload.Revision, Image: payload.Image, Role: payload.Role, Plan: digest}
	if err := binding.Validate(); err != nil {
		return fail(err)
	}
	encoded, err := canonical.Marshal(payload)
	if err != nil {
		return fail(err)
	}
	return mappedEntryPlanV1{binding: binding, payload: string(encoded)}, nil
}

// ValidateStateV1 has already checked every decimal wire value. Convert by
// interval boundaries, never by iterating the represented ID range.
func mappedStateGeometryV1(wire deploy.IdentityMappingGeometryV1) identitymapping.Geometry {
	convert := func(rows []deploy.IdentityMappingIntervalV1) []identitymapping.Interval {
		out := make([]identitymapping.Interval, len(rows))
		for i, row := range rows {
			start, _ := strconv.ParseUint(row.ContainerStart, 10, 32)
			host, _ := strconv.ParseUint(row.HostStart, 10, 32)
			count, _ := strconv.ParseUint(row.Count, 10, 32)
			out[i] = identitymapping.Interval{ContainerStart: uint32(start), HostStart: uint32(host), Count: uint32(count)}
		}
		return out
	}
	return identitymapping.Geometry{Profile: wire.Profile, UIDs: convert(wire.UIDs), GIDs: convert(wire.GIDs)}
}

func (p mappedEntryPlanV1) facts() (mappedEntryPayloadV1, error) {
	var value mappedEntryPayloadV1
	if err := p.binding.Validate(); err != nil {
		return value, err
	}
	if err := json.Unmarshal([]byte(p.payload), &value); err != nil {
		return mappedEntryPayloadV1{}, err
	}
	digest, err := canonical.Sum("mapped-entry", "v1", value)
	if err != nil || digest != p.binding.Plan ||
		p.binding.Installation != value.Installation || p.binding.Operation != value.Operation || p.binding.Generation != value.Generation ||
		p.binding.Revision != value.Revision || p.binding.Image != value.Image || p.binding.Role != value.Role {
		return mappedEntryPayloadV1{}, fmt.Errorf("mapped entry payload differs from its binding")
	}
	return value, nil
}
