package dockerdeploy

import (
	"fmt"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/controlledsession"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/runtimeidentity"
)

const (
	ControlledSessionExecutionPlanSchemaV1 = "controlled-session-execution-plan-v1"
	ControlledSessionContainerPlanSchemaV1 = "controlled-session-container-plan-v1"

	controlledSessionNetworkModeV1           = "none"
	controlledSessionOrdinaryNetworkModeV1   = "bridge"
	controlledSessionControllerAliasV1       = "controller"
	controlledSessionWorkloadAliasV1         = controlledsession.WorkloadEndpointHostV1
	controlledSessionChannelRootV1           = "/run/reploy/session"
	controlledSessionChannelSocketNameV1     = controlledsession.PrivateChannelSocketNameV1
	controlledSessionNetworkPolicyFileNameV1 = "network-prefixes"
	controlledSessionNetworkPolicyPathV1     = "/run/reploy/network-prefixes"
)

type ControlledSessionRoleV1 string

const (
	ControlledSessionRoleControllerV1 ControlledSessionRoleV1 = "controller"
	ControlledSessionRoleWorkloadV1   ControlledSessionRoleV1 = "workload"
)

type ControlledSessionPlanInputV1 struct {
	Handle                       string
	LiveRunID                    string
	ControllerCurrent            CurrentBuild
	ControllerRuntime            CurrentRuntimePlanV1
	ControllerCommand            string
	ControllerForwardedArguments []string
	controllerOutput             *transientOutputMount
	WorkloadCurrent              CurrentBuild
	WorkloadRuntime              CurrentRuntimePlanV1
	EndpointIDs                  []string
	InitialColumns               uint32
	InitialRows                  uint32
}

type controlledSessionPlanBackendV1 struct {
	requireReady   func(CurrentBuild, CurrentRuntimePlanV1, string, *transientOutputMount) error
	resolveCommand func(blueprint.Document, CurrentBuild, DockerExecutionPlan, string, []string) (ResolvedEnvironmentCommand, error)
}

type ControlledSessionExecutionPlanV1 struct {
	Schema        string                            `json:"schema"`
	LiveRunID     string                            `json:"live_run_id"`
	Channel       ControlledSessionChannelPlanV1    `json:"channel"`
	Controller    ControlledSessionContainerPlanV1  `json:"controller"`
	Workload      ControlledSessionContainerPlanV1  `json:"workload"`
	Authorization controlledsession.AuthorizationV1 `json:"authorization"`
}

type ControlledSessionChannelPlanV1 struct {
	HostDirectory      string `json:"host_directory"`
	ContainerDirectory string `json:"container_directory"`
	ContainerSocket    string `json:"container_socket"`
}

type ControlledSessionContainerPlanV1 struct {
	Schema              string                              `json:"schema"`
	Role                ControlledSessionRoleV1             `json:"role"`
	LiveRunID           string                              `json:"live_run_id"`
	DeploymentID        string                              `json:"deployment_id"`
	DeploymentDirectory string                              `json:"deployment_directory"`
	GenerationReference string                              `json:"generation_reference"`
	BuildIdentity       canonical.Digest                    `json:"build_identity"`
	Image               string                              `json:"image"`
	Container           string                              `json:"container"`
	RuntimeIdentity     controlledsession.RuntimeIdentityV1 `json:"runtime_identity"`
	Network             string                              `json:"network"`
	NetworkPolicy       ApplicationNetworkPolicyV1          `json:"network_policy"`
	SessionNetwork      ControlledSessionNetworkPlanV1      `json:"session_network"`
	ReadOnlyRoot        bool                                `json:"read_only_root"`
	TemporaryHome       string                              `json:"temporary_home"`
	StartupVerifier     string                              `json:"startup_verifier"`
	SetupCapabilities   []string                            `json:"setup_capabilities"`
	SecurityOptions     []string                            `json:"security_options"`
	Environment         []string                            `json:"environment"`
	Labels              []ControlledSessionLabelV1          `json:"labels"`
	Mounts              []ControlledSessionMountV1          `json:"mounts"`
	Masks               []ControlledSessionMaskV1           `json:"masks"`
	Command             []string                            `json:"command"`
	TTY                 bool                                `json:"tty"`
	OpenStdin           bool                                `json:"open_stdin"`
	InitialColumns      string                              `json:"initial_columns"`
	InitialRows         string                              `json:"initial_rows"`
	Create              ControlledSessionDockerCommandV1    `json:"create"`
	Start               ControlledSessionDockerCommandV1    `json:"start"`
	Cleanup             ControlledSessionDockerCommandV1    `json:"cleanup"`
}

type ControlledSessionNetworkPlanV1 struct {
	Enabled   bool                              `json:"enabled"`
	Name      string                            `json:"name"`
	Internal  bool                              `json:"internal"`
	Alias     string                            `json:"alias"`
	PeerAlias string                            `json:"peer_alias"`
	Endpoints []ControlledSessionEndpointPlanV1 `json:"endpoints"`
}

type ControlledSessionEndpointPlanV1 struct {
	ID     string `json:"id"`
	Scheme string `json:"scheme"`
	Host   string `json:"host"`
	Port   string `json:"port"`
}

type ControlledSessionLabelV1 struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type ControlledSessionMountV1 struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Source     string `json:"source"`
	SourceKind string `json:"source_kind"`
	Target     string `json:"target"`
	ReadOnly   bool   `json:"read_only"`
}

type ControlledSessionMaskV1 struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

type ControlledSessionDockerCommandV1 struct {
	Name string   `json:"name"`
	Args []string `json:"args"`
}

func cloneControlledSessionContainerPlanV1(plan ControlledSessionContainerPlanV1) ControlledSessionContainerPlanV1 {
	clone := plan
	clone.SessionNetwork.Endpoints = slices.Clone(plan.SessionNetwork.Endpoints)
	clone.SetupCapabilities = slices.Clone(plan.SetupCapabilities)
	clone.SecurityOptions = slices.Clone(plan.SecurityOptions)
	clone.Environment = slices.Clone(plan.Environment)
	clone.Labels = slices.Clone(plan.Labels)
	clone.Mounts = slices.Clone(plan.Mounts)
	clone.Masks = slices.Clone(plan.Masks)
	clone.Command = slices.Clone(plan.Command)
	clone.Create.Args = slices.Clone(plan.Create.Args)
	clone.Start.Args = slices.Clone(plan.Start.Args)
	clone.Cleanup.Args = slices.Clone(plan.Cleanup.Args)
	return clone
}

// PlanControlledSessionV1 derives the complete immutable Docker authority for
// one prospective controller/workload run before admission. It is read-only
// and creates no host or Docker resources.
func PlanControlledSessionV1(input ControlledSessionPlanInputV1) (ControlledSessionExecutionPlanV1, error) {
	return planControlledSessionV1(input, controlledSessionPlanBackendV1{
		requireReady: requireControlledSessionRuntimeReadyV1,
		resolveCommand: func(document blueprint.Document, current CurrentBuild, dockerPlan DockerExecutionPlan, name string, arguments []string) (ResolvedEnvironmentCommand, error) {
			return resolveLockedEnvironmentCommandForPlanV1(document, current.Lock.Catalog, dockerPlan, name, arguments)
		},
	})
}

func planControlledSessionV1(input ControlledSessionPlanInputV1, backend controlledSessionPlanBackendV1) (ControlledSessionExecutionPlanV1, error) {
	if err := deploy.ValidateLiveRunIDV1(input.LiveRunID); err != nil {
		return ControlledSessionExecutionPlanV1{}, fmt.Errorf("plan controlled session: %w", err)
	}
	if backend.requireReady == nil || backend.resolveCommand == nil {
		return ControlledSessionExecutionPlanV1{}, fmt.Errorf("plan controlled session requires a complete backend")
	}
	if input.InitialColumns == 0 || input.InitialColumns > 65535 || input.InitialRows == 0 || input.InitialRows > 65535 {
		return ControlledSessionExecutionPlanV1{}, fmt.Errorf("plan controlled session requires initial terminal dimensions between 1 and 65535")
	}
	if input.ControllerRuntime.Docker.PrivateEnvironment || input.WorkloadRuntime.Docker.PrivateEnvironment {
		return ControlledSessionExecutionPlanV1{}, fmt.Errorf("plan controlled session does not yet support private environment injection")
	}
	controllerRoot, err := canonicalPathAllowMissingV1(input.ControllerRuntime.Docker.DeploymentDir)
	if err != nil {
		return ControlledSessionExecutionPlanV1{}, fmt.Errorf("plan controlled session controller deployment root: %w", err)
	}
	workloadRoot, err := canonicalPathAllowMissingV1(input.WorkloadRuntime.Docker.DeploymentDir)
	if err != nil {
		return ControlledSessionExecutionPlanV1{}, fmt.Errorf("plan controlled session workload deployment root: %w", err)
	}
	if controllerRoot == workloadRoot {
		return ControlledSessionExecutionPlanV1{}, fmt.Errorf("plan controlled session requires distinct controller and workload deployment roots")
	}
	commandDefinition, found := input.ControllerRuntime.Document.Environment.Commands[input.ControllerCommand]
	if !found || !commandDefinition.NativeCommand {
		return ControlledSessionExecutionPlanV1{}, fmt.Errorf("plan controlled session controller command %q is not a declared native command", input.ControllerCommand)
	}
	if err := validateControlledSessionCurrentRuntimeV1(
		"controller", input.ControllerCurrent, input.ControllerRuntime,
		runtimeCommandPlanID(input.ControllerCommand, input.controllerOutput != nil), input.controllerOutput, backend.requireReady,
	); err != nil {
		return ControlledSessionExecutionPlanV1{}, err
	}
	if err := validateControlledSessionCurrentRuntimeV1(
		"workload", input.WorkloadCurrent, input.WorkloadRuntime,
		runtimeShellPlanID, nil, backend.requireReady,
	); err != nil {
		return ControlledSessionExecutionPlanV1{}, err
	}
	controllerCommand, err := backend.resolveCommand(
		input.ControllerRuntime.Document,
		input.ControllerCurrent,
		input.ControllerRuntime.Docker,
		input.ControllerCommand,
		input.ControllerForwardedArguments,
	)
	if err != nil {
		return ControlledSessionExecutionPlanV1{}, fmt.Errorf("plan controlled session controller command: %w", err)
	}

	channel := ControlledSessionChannelPlanV1{
		HostDirectory:      filepath.Join(input.WorkloadRuntime.Docker.DeploymentDir, privateRuntimeMetadataDirectoryName, "sessions", input.LiveRunID),
		ContainerDirectory: controlledSessionChannelRootV1,
		ContainerSocket:    path.Join(controlledSessionChannelRootV1, controlledSessionChannelSocketNameV1),
	}
	endpoints, err := controlledSessionEndpointPlansV1(
		input.EndpointIDs,
		input.WorkloadRuntime.Document,
		input.WorkloadRuntime.Docker,
	)
	if err != nil {
		return ControlledSessionExecutionPlanV1{}, fmt.Errorf("plan controlled session endpoints: %w", err)
	}
	controllerNetwork, workloadNetwork := controlledSessionNetworkPlansV1(
		input.WorkloadRuntime.Docker.NetworkName,
		input.LiveRunID,
		endpoints,
	)
	controller, err := controlledSessionContainerPlanV1(
		ControlledSessionRoleControllerV1,
		input.LiveRunID,
		input.ControllerCurrent,
		input.ControllerRuntime.Docker,
		controllerCommand.Argv,
		channel,
		[]string{input.ControllerRuntime.Docker.DeploymentDir, input.WorkloadRuntime.Docker.DeploymentDir},
		controllerNetwork,
		input.controllerOutput,
		0,
		0,
	)
	if err != nil {
		return ControlledSessionExecutionPlanV1{}, fmt.Errorf("plan controlled session controller: %w", err)
	}
	workload, err := controlledSessionContainerPlanV1(
		ControlledSessionRoleWorkloadV1,
		input.LiveRunID,
		input.WorkloadCurrent,
		input.WorkloadRuntime.Docker,
		[]string{"/bin/sh"},
		channel,
		[]string{input.ControllerRuntime.Docker.DeploymentDir, input.WorkloadRuntime.Docker.DeploymentDir},
		workloadNetwork,
		nil,
		input.InitialColumns,
		input.InitialRows,
	)
	if err != nil {
		return ControlledSessionExecutionPlanV1{}, fmt.Errorf("plan controlled session workload: %w", err)
	}
	controllerDigest, err := ControlledSessionContainerPlanDigestV1(controller)
	if err != nil {
		return ControlledSessionExecutionPlanV1{}, err
	}
	workloadDigest, err := ControlledSessionContainerPlanDigestV1(workload)
	if err != nil {
		return ControlledSessionExecutionPlanV1{}, err
	}
	authorization := controlledsession.AuthorizationV1{
		Schema:     controlledsession.AuthorizationSchemaV1,
		Handle:     input.Handle,
		LiveRunID:  input.LiveRunID,
		Controller: environmentAuthorizationForContainerV1(controller, controllerDigest),
		Workload:   environmentAuthorizationForContainerV1(workload, workloadDigest),
		Operations: []controlledsession.OperationV1{
			controlledsession.OperationCompleteV1,
			controlledsession.OperationInputV1,
			controlledsession.OperationResizeV1,
			controlledsession.OperationTerminateV1,
		},
		EndpointIDs: controlledSessionEndpointIDsV1(endpoints),
	}
	plan := ControlledSessionExecutionPlanV1{
		Schema: ControlledSessionExecutionPlanSchemaV1, LiveRunID: input.LiveRunID,
		Channel: channel, Controller: controller, Workload: workload, Authorization: authorization,
	}
	if err := ValidateControlledSessionExecutionPlanV1(plan); err != nil {
		return ControlledSessionExecutionPlanV1{}, fmt.Errorf("validate planned controlled session: %w", err)
	}
	return plan, nil
}

func ControlledSessionContainerPlanDigestV1(plan ControlledSessionContainerPlanV1) (canonical.Digest, error) {
	if err := ValidateControlledSessionContainerPlanV1(plan); err != nil {
		return "", err
	}
	return canonical.Sum("controlled-session-container-plan", ControlledSessionContainerPlanSchemaV1, plan)
}

func ValidateControlledSessionExecutionPlanV1(plan ControlledSessionExecutionPlanV1) error {
	if plan.Schema != ControlledSessionExecutionPlanSchemaV1 {
		return fmt.Errorf("controlled-session execution plan schema must be %q", ControlledSessionExecutionPlanSchemaV1)
	}
	if err := deploy.ValidateLiveRunIDV1(plan.LiveRunID); err != nil {
		return fmt.Errorf("controlled-session execution plan: %w", err)
	}
	if plan.Controller.Role != ControlledSessionRoleControllerV1 || plan.Workload.Role != ControlledSessionRoleWorkloadV1 {
		return fmt.Errorf("controlled-session execution plan must contain controller and workload roles")
	}
	if plan.Controller.LiveRunID != plan.LiveRunID || plan.Workload.LiveRunID != plan.LiveRunID {
		return fmt.Errorf("controlled-session container plans must name the execution plan's live run")
	}
	controllerRoot, err := canonicalPathAllowMissingV1(plan.Controller.DeploymentDirectory)
	if err != nil {
		return fmt.Errorf("controlled-session controller deployment root: %w", err)
	}
	workloadRoot, err := canonicalPathAllowMissingV1(plan.Workload.DeploymentDirectory)
	if err != nil {
		return fmt.Errorf("controlled-session workload deployment root: %w", err)
	}
	if controllerRoot == workloadRoot {
		return fmt.Errorf("controlled-session execution plan requires distinct controller and workload deployment roots")
	}
	if err := validateControlledSessionChannelV1(plan.Channel, plan.Workload.DeploymentDirectory, plan.LiveRunID); err != nil {
		return err
	}
	if controlledSessionContainerExposesChannelV1(plan.Workload, plan.Channel) {
		return fmt.Errorf("controlled-session workload plan must not expose the private channel")
	}
	if err := ValidateControlledSessionContainerPlanV1(plan.Controller); err != nil {
		return fmt.Errorf("controlled-session controller plan: %w", err)
	}
	if err := ValidateControlledSessionContainerPlanV1(plan.Workload); err != nil {
		return fmt.Errorf("controlled-session workload plan: %w", err)
	}
	if plan.Controller.Container == plan.Workload.Container {
		return fmt.Errorf("controlled-session controller and workload containers must be distinct")
	}
	if err := validateControlledSessionNetworkPairV1(
		plan.Controller.SessionNetwork,
		plan.Workload.SessionNetwork,
		plan.Workload.Container,
		plan.LiveRunID,
	); err != nil {
		return err
	}
	if !controlledSessionControllerCarriesChannelV1(plan.Controller, plan.Channel) {
		return fmt.Errorf("controlled-session controller plan does not carry the exact private channel")
	}
	for _, container := range []ControlledSessionContainerPlanV1{plan.Controller, plan.Workload} {
		if !controlledSessionContainerCarriesNetworkPolicyV1(container, plan.Channel) {
			return fmt.Errorf("controlled-session %s plan does not carry the exact session-network policy input", container.Role)
		}
	}
	protectedRoots := []string{controllerRoot, workloadRoot}
	if err := validateControlledSessionMasksForRootsV1(plan.Controller, plan.Channel, protectedRoots); err != nil {
		return fmt.Errorf("controlled-session controller plan: %w", err)
	}
	if err := validateControlledSessionMasksForRootsV1(plan.Workload, plan.Channel, protectedRoots); err != nil {
		return fmt.Errorf("controlled-session workload plan: %w", err)
	}
	controllerDigest, err := ControlledSessionContainerPlanDigestV1(plan.Controller)
	if err != nil {
		return err
	}
	workloadDigest, err := ControlledSessionContainerPlanDigestV1(plan.Workload)
	if err != nil {
		return err
	}
	wantAuthorization := controlledsession.AuthorizationV1{
		Schema: controlledsession.AuthorizationSchemaV1, Handle: plan.Authorization.Handle, LiveRunID: plan.LiveRunID,
		Controller: environmentAuthorizationForContainerV1(plan.Controller, controllerDigest),
		Workload:   environmentAuthorizationForContainerV1(plan.Workload, workloadDigest),
		Operations: []controlledsession.OperationV1{
			controlledsession.OperationCompleteV1,
			controlledsession.OperationInputV1,
			controlledsession.OperationResizeV1,
			controlledsession.OperationTerminateV1,
		},
		EndpointIDs: controlledSessionEndpointIDsV1(plan.Controller.SessionNetwork.Endpoints),
	}
	if !reflect.DeepEqual(plan.Authorization, wantAuthorization) {
		return fmt.Errorf("controlled-session authorization does not match the immutable controller and workload plans")
	}
	return controlledsession.ValidateAuthorizationV1(plan.Authorization)
}

func ValidateControlledSessionContainerPlanV1(plan ControlledSessionContainerPlanV1) error {
	if plan.Schema != ControlledSessionContainerPlanSchemaV1 {
		return fmt.Errorf("container plan schema must be %q", ControlledSessionContainerPlanSchemaV1)
	}
	if plan.Role != ControlledSessionRoleControllerV1 && plan.Role != ControlledSessionRoleWorkloadV1 {
		return fmt.Errorf("container role %q is unsupported", plan.Role)
	}
	if err := deploy.ValidateLiveRunIDV1(plan.LiveRunID); err != nil {
		return err
	}
	if strings.TrimSpace(plan.DeploymentID) == "" || strings.TrimSpace(plan.DeploymentDirectory) == "" {
		return fmt.Errorf("container plan requires deployment identity and directory")
	}
	if !filepath.IsAbs(plan.DeploymentDirectory) || filepath.Clean(plan.DeploymentDirectory) != plan.DeploymentDirectory {
		return fmt.Errorf("container deployment directory must be an absolute clean path")
	}
	if strings.TrimSpace(plan.GenerationReference) == "" || plan.Image != plan.GenerationReference {
		return fmt.Errorf("container image must be the exact generation reference")
	}
	if err := plan.BuildIdentity.Validate(); err != nil {
		return fmt.Errorf("container build identity: %w", err)
	}
	if strings.TrimSpace(plan.Container) == "" || !strings.HasSuffix(plan.Container, "-"+string(plan.Role)+"-"+plan.LiveRunID) {
		return fmt.Errorf("container name must bind its role and live run")
	}
	if err := runtimeidentity.ValidateIdentityV1(plan.RuntimeIdentity); err != nil {
		return fmt.Errorf("container runtime identity: %w", err)
	}
	wantNetwork := controlledSessionDockerNetworkModeV1(plan.NetworkPolicy)
	if plan.Network != wantNetwork {
		return fmt.Errorf("container network must be %q for its ordinary network grants", wantNetwork)
	}
	if err := validateControlledSessionNetworkPlanV1(plan.SessionNetwork); err != nil {
		return fmt.Errorf("container session network: %w", err)
	}
	if err := validateApplicationNetworkAccessV1("public", plan.NetworkPolicy.Public); err != nil {
		return fmt.Errorf("container network policy: %w", err)
	}
	if err := validateApplicationNetworkAccessV1("local", plan.NetworkPolicy.Local); err != nil {
		return fmt.Errorf("container network policy: %w", err)
	}
	if err := validateApplicationAmbiguousNetworkAccessV1(plan.NetworkPolicy.Ambiguous); err != nil {
		return fmt.Errorf("container network policy: %w", err)
	}
	if !plan.ReadOnlyRoot || plan.TemporaryHome != environmentTemporaryHome || plan.StartupVerifier != deploy.ApplicationStartupVerifierPathV1 {
		return fmt.Errorf("container plan does not preserve the application sandbox root and startup verifier")
	}
	if !slices.Equal(plan.SetupCapabilities, controlledSessionSetupCapabilitiesV1(
		plan.RuntimeIdentity.UID,
		controlledSessionInstallsNetworkPolicyV1(plan.NetworkPolicy, plan.SessionNetwork.Enabled),
	)) {
		return fmt.Errorf("container setup capabilities do not match the session sandbox contract")
	}
	if !slices.Equal(plan.SecurityOptions, []string{"no-new-privileges=true", "seccomp=" + applicationSeccompProfileBuiltinV1}) {
		return fmt.Errorf("container security options do not match the application sandbox")
	}
	if plan.Environment == nil || plan.Labels == nil || plan.Mounts == nil || plan.Masks == nil || plan.Command == nil {
		return fmt.Errorf("container plan collections must use arrays")
	}
	wantEnvironment := []string{"HOME=" + plan.TemporaryHome, "TMPDIR=" + plan.TemporaryHome}
	if plan.Role == ControlledSessionRoleControllerV1 {
		wantEnvironment = append(wantEnvironment, "REPLOY_SESSION_SOCKET="+path.Join(controlledSessionChannelRootV1, controlledSessionChannelSocketNameV1))
	}
	outputEnvironment, err := validateControlledSessionOutputV1(plan)
	if err != nil {
		return err
	}
	if outputEnvironment != "" {
		wantEnvironment = append(wantEnvironment, outputEnvironment)
	}
	if !slices.Equal(plan.Environment, wantEnvironment) {
		return fmt.Errorf("container environment does not match the fixed session contract")
	}
	if len(plan.Command) == 0 || !path.IsAbs(plan.Command[0]) {
		return fmt.Errorf("container command must begin with an absolute executable")
	}
	if plan.Role == ControlledSessionRoleWorkloadV1 {
		if !slices.Equal(plan.Command, []string{"/bin/sh"}) || !plan.TTY || !plan.OpenStdin || !validControlledSessionDimensionsV1(plan.InitialColumns, plan.InitialRows) {
			return fmt.Errorf("workload plan must use the persistent PTY shell and valid initial dimensions")
		}
	} else if plan.TTY || plan.OpenStdin || plan.InitialColumns != "" || plan.InitialRows != "" {
		return fmt.Errorf("controller plan must not request a workload PTY")
	}
	if err := validateControlledSessionLabelsV1(plan.Labels, plan); err != nil {
		return err
	}
	if err := validateControlledSessionMountsV1(plan.Mounts); err != nil {
		return err
	}
	if err := validateControlledSessionNetworkPolicyMountV1(plan); err != nil {
		return err
	}
	if err := validateControlledSessionMasksV1(plan.Masks); err != nil {
		return err
	}
	expectedCreate, err := renderControlledSessionCreateV1(plan)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(plan.Create, expectedCreate) {
		return fmt.Errorf("container create command does not reflect the immutable plan")
	}
	wantStart := ControlledSessionDockerCommandV1{Name: "docker", Args: []string{"start", plan.Container}}
	wantCleanup := ControlledSessionDockerCommandV1{Name: "docker", Args: []string{"container", "rm", "--force", plan.Container}}
	if !reflect.DeepEqual(plan.Start, wantStart) || !reflect.DeepEqual(plan.Cleanup, wantCleanup) {
		return fmt.Errorf("container lifecycle commands do not reflect the immutable plan")
	}
	return nil
}

func validateControlledSessionOutputV1(plan ControlledSessionContainerPlanV1) (string, error) {
	var output *ControlledSessionMountV1
	for index := range plan.Mounts {
		mount := &plan.Mounts[index]
		if mount.Name != "controller-output" && mount.Target != runtimeOutputRoot {
			continue
		}
		if output != nil {
			return "", fmt.Errorf("container plan contains multiple controller output mounts")
		}
		output = mount
	}
	if output == nil {
		return "", nil
	}
	if plan.Role != ControlledSessionRoleControllerV1 {
		return "", fmt.Errorf("workload plan must not expose the controller output")
	}
	want := ControlledSessionMountV1{
		Name: "controller-output", Type: "bind", Source: output.Source,
		SourceKind: deploy.RuntimeMountSourceDirectory, Target: runtimeOutputRoot,
	}
	if !reflect.DeepEqual(*output, want) {
		return "", fmt.Errorf("controller output mount does not match the fixed session contract")
	}
	for _, value := range plan.Environment {
		switch value {
		case runtimeOutputDirectoryVariable + "=" + runtimeOutputRoot:
			return value, nil
		case runtimeOutputFileVariable + "=" + path.Join(runtimeOutputRoot, runtimeOutputFileName):
			return value, nil
		}
	}
	return "", fmt.Errorf("controller output mount requires exactly one supported output environment coordinate")
}

func controlledSessionEndpointPlansV1(
	endpointIDs []string,
	document blueprint.Document,
	workload DockerExecutionPlan,
) ([]ControlledSessionEndpointPlanV1, error) {
	ids := append([]string{}, endpointIDs...)
	slices.Sort(ids)
	result := make([]ControlledSessionEndpointPlanV1, 0, len(ids))
	for index, id := range ids {
		if index > 0 && ids[index-1] == id {
			return nil, fmt.Errorf("endpoint ID %q is duplicated", id)
		}
		if document.Environment.Workload == nil || workload.Workload == nil {
			return nil, fmt.Errorf("endpoint ID %q requires a declared workload", id)
		}
		resolved, found := document.Environment.Workload.Endpoints[id]
		if !found {
			return nil, fmt.Errorf("endpoint ID %q is not declared by the exact workload generation", id)
		}
		planned, found := workload.Workload.Endpoints[id]
		if !found {
			return nil, fmt.Errorf("endpoint ID %q is not declared by the exact workload generation", id)
		}
		if planned.Scheme != resolved.Scheme || planned.ContainerPort != resolved.Port {
			return nil, fmt.Errorf("endpoint ID %q does not match the resolved workload document", id)
		}
		endpoint := ControlledSessionEndpointPlanV1{
			ID: id, Scheme: resolved.Scheme, Host: controlledSessionWorkloadAliasV1, Port: strconv.Itoa(resolved.Port),
		}
		if err := validateControlledSessionEndpointPlanV1(endpoint); err != nil {
			return nil, err
		}
		result = append(result, endpoint)
	}
	return result, nil
}

func controlledSessionNetworkPlansV1(
	workloadNetworkName string,
	liveRunID string,
	endpoints []ControlledSessionEndpointPlanV1,
) (ControlledSessionNetworkPlanV1, ControlledSessionNetworkPlanV1) {
	if len(endpoints) == 0 {
		disabled := disabledControlledSessionNetworkPlanV1()
		return disabled, disabled
	}
	name := workloadNetworkName + "-session-" + liveRunID
	controller := ControlledSessionNetworkPlanV1{
		Enabled: true, Name: name, Internal: true,
		Alias: controlledSessionControllerAliasV1, PeerAlias: controlledSessionWorkloadAliasV1,
		Endpoints: slices.Clone(endpoints),
	}
	workload := ControlledSessionNetworkPlanV1{
		Enabled: true, Name: name, Internal: true,
		Alias: controlledSessionWorkloadAliasV1, PeerAlias: controlledSessionControllerAliasV1,
		Endpoints: slices.Clone(endpoints),
	}
	return controller, workload
}

func disabledControlledSessionNetworkPlanV1() ControlledSessionNetworkPlanV1 {
	return ControlledSessionNetworkPlanV1{Endpoints: []ControlledSessionEndpointPlanV1{}}
}

func controlledSessionEndpointIDsV1(endpoints []ControlledSessionEndpointPlanV1) []string {
	result := make([]string, len(endpoints))
	for index, endpoint := range endpoints {
		result[index] = endpoint.ID
	}
	return result
}

func validateControlledSessionNetworkPlanV1(plan ControlledSessionNetworkPlanV1) error {
	if plan.Endpoints == nil {
		return fmt.Errorf("endpoints must use an array")
	}
	if !plan.Enabled {
		if plan.Name != "" || plan.Internal || plan.Alias != "" || plan.PeerAlias != "" || len(plan.Endpoints) != 0 {
			return fmt.Errorf("disabled grant must not carry network authority")
		}
		return nil
	}
	if !plan.Internal {
		return fmt.Errorf("enabled grant must use an engine-internal network")
	}
	if !validControlledSessionDockerNameV1(plan.Name) {
		return fmt.Errorf("network name must use a safe Docker resource name")
	}
	if plan.Alias == plan.PeerAlias ||
		(plan.Alias != controlledSessionControllerAliasV1 && plan.Alias != controlledSessionWorkloadAliasV1) ||
		(plan.PeerAlias != controlledSessionControllerAliasV1 && plan.PeerAlias != controlledSessionWorkloadAliasV1) {
		return fmt.Errorf("network aliases must identify the fixed controller and workload peers")
	}
	if len(plan.Endpoints) == 0 {
		return fmt.Errorf("enabled grant requires at least one endpoint")
	}
	for index, endpoint := range plan.Endpoints {
		if err := validateControlledSessionEndpointPlanV1(endpoint); err != nil {
			return err
		}
		if index > 0 && plan.Endpoints[index-1].ID >= endpoint.ID {
			return fmt.Errorf("endpoints must be unique and sorted by ID")
		}
	}
	return nil
}

func validateControlledSessionEndpointPlanV1(endpoint ControlledSessionEndpointPlanV1) error {
	port, err := strconv.ParseUint(endpoint.Port, 10, 16)
	if err != nil || port == 0 || strconv.FormatUint(port, 10) != endpoint.Port {
		return fmt.Errorf("controlled-session endpoint %q port must be a canonical decimal between 1 and 65535", endpoint.ID)
	}
	return controlledsession.ValidateEndpointV1(controlledsession.EndpointV1{
		ID: endpoint.ID, Scheme: endpoint.Scheme, Host: endpoint.Host, Port: uint32(port),
	})
}

func controlledSessionOpenedEndpointsV1(endpoints []ControlledSessionEndpointPlanV1) []controlledsession.EndpointV1 {
	result := make([]controlledsession.EndpointV1, len(endpoints))
	for index, endpoint := range endpoints {
		port, _ := strconv.ParseUint(endpoint.Port, 10, 16)
		result[index] = controlledsession.EndpointV1{
			ID: endpoint.ID, Scheme: endpoint.Scheme, Host: endpoint.Host, Port: uint32(port),
		}
	}
	return result
}

func validateControlledSessionNetworkPairV1(
	controller ControlledSessionNetworkPlanV1,
	workload ControlledSessionNetworkPlanV1,
	workloadContainer string,
	liveRunID string,
) error {
	if controller.Enabled != workload.Enabled {
		return fmt.Errorf("controlled-session controller and workload network grants must be enabled together")
	}
	if !controller.Enabled {
		if !reflect.DeepEqual(controller, workload) {
			return fmt.Errorf("controlled-session disabled network grants must match")
		}
		return nil
	}
	wantBase := strings.TrimSuffix(workloadContainer, "-"+string(ControlledSessionRoleWorkloadV1)+"-"+liveRunID)
	wantName := wantBase + "-session-" + liveRunID
	if controller.Name != wantName || workload.Name != wantName ||
		controller.Alias != controlledSessionControllerAliasV1 || controller.PeerAlias != controlledSessionWorkloadAliasV1 ||
		workload.Alias != controlledSessionWorkloadAliasV1 || workload.PeerAlias != controlledSessionControllerAliasV1 ||
		!controller.Internal || !workload.Internal || !reflect.DeepEqual(controller.Endpoints, workload.Endpoints) {
		return fmt.Errorf("controlled-session network grants do not match the exact lease-local network and peers")
	}
	return nil
}

func validControlledSessionDockerNameV1(value string) bool {
	if value == "" || len(value) > 255 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		alphanumeric := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9'
		if !alphanumeric && (index == 0 || character != '_' && character != '.' && character != '-') {
			return false
		}
	}
	return true
}

func validateControlledSessionCurrentRuntimeV1(
	role string,
	current CurrentBuild,
	runtime CurrentRuntimePlanV1,
	planID string,
	output *transientOutputMount,
	requireReady func(CurrentBuild, CurrentRuntimePlanV1, string, *transientOutputMount) error,
) error {
	if err := requireReady(current, runtime, planID, output); err != nil {
		return fmt.Errorf("plan controlled session %s current build: %w", role, err)
	}
	document, err := blueprint.DecodeResolvedDocumentV1(current.State.Blueprint)
	if err != nil {
		return fmt.Errorf("plan controlled session %s blueprint: %w", role, err)
	}
	if !reflect.DeepEqual(document, runtime.Document) {
		return fmt.Errorf("plan controlled session %s runtime document does not match its current build", role)
	}
	if runtime.Docker.EnvironmentID != document.Environment.ID || runtime.Docker.Image != current.Generation.Reference {
		return fmt.Errorf("plan controlled session %s runtime does not name its exact environment generation", role)
	}
	return nil
}

func requireControlledSessionRuntimeReadyV1(current CurrentBuild, runtime CurrentRuntimePlanV1, planID string, output *transientOutputMount) error {
	sources, err := RuntimeHostSourcesV1(runtime.Docker, output)
	if err != nil {
		return err
	}
	return RequireRuntimeReady(RuntimeReadinessInput{
		Current: current, DockerPlan: runtime.Docker, PlanID: planID, Sources: sources,
	})
}

func controlledSessionContainerPlanV1(
	role ControlledSessionRoleV1,
	liveRunID string,
	current CurrentBuild,
	dockerPlan DockerExecutionPlan,
	command []string,
	channel ControlledSessionChannelPlanV1,
	protectedDeploymentRoots []string,
	sessionNetwork ControlledSessionNetworkPlanV1,
	output *transientOutputMount,
	columns uint32,
	rows uint32,
) (ControlledSessionContainerPlanV1, error) {
	if err := ValidateApplicationSandboxPlanV1(dockerPlan.Sandbox); err != nil {
		return ControlledSessionContainerPlanV1{}, err
	}
	mounts := make([]ControlledSessionMountV1, 0, len(dockerPlan.Mounts)+1)
	for _, mount := range dockerPlan.Mounts {
		sourceKind, err := runtimeMountSourceKindV1(mount)
		if err != nil {
			return ControlledSessionContainerPlanV1{}, fmt.Errorf("runtime mount %q: %w", mount.Name, err)
		}
		mounts = append(mounts, ControlledSessionMountV1{
			Name: mount.Name, Type: renderDockerMountType(mount.Mode), Source: mount.Source,
			SourceKind: sourceKind, Target: mount.Target, ReadOnly: mount.ReadOnly,
		})
	}
	if role == ControlledSessionRoleControllerV1 {
		mounts = append(mounts, ControlledSessionMountV1{
			Name: "session-channel", Type: "bind", Source: channel.HostDirectory,
			SourceKind: deploy.RuntimeMountSourceDirectory, Target: channel.ContainerDirectory, ReadOnly: true,
		})
	}
	if output != nil {
		if role != ControlledSessionRoleControllerV1 {
			return ControlledSessionContainerPlanV1{}, fmt.Errorf("controlled-session output may be exposed only to the controller")
		}
		if !filepath.IsAbs(output.HostDirectory) || filepath.Clean(output.HostDirectory) != output.HostDirectory ||
			(output.Variable != runtimeOutputDirectoryVariable && output.Variable != runtimeOutputFileVariable) ||
			output.ContainerPath == "" {
			return ControlledSessionContainerPlanV1{}, fmt.Errorf("controlled-session controller output mount is incomplete")
		}
		mounts = append(mounts, ControlledSessionMountV1{
			Name: "controller-output", Type: "bind", Source: output.HostDirectory,
			SourceKind: deploy.RuntimeMountSourceDirectory, Target: runtimeOutputRoot,
		})
	}
	if sessionNetwork.Enabled {
		mounts = append(mounts, ControlledSessionMountV1{
			Name: "session-network-prefixes", Type: "bind",
			Source:     filepath.Join(channel.HostDirectory, controlledSessionNetworkPolicyFileNameV1),
			SourceKind: deploy.RuntimeMountSourceFile, Target: controlledSessionNetworkPolicyPathV1, ReadOnly: true,
		})
	}
	sort.Slice(mounts, func(left int, right int) bool {
		if mounts[left].Target != mounts[right].Target {
			return mounts[left].Target < mounts[right].Target
		}
		return mounts[left].Name < mounts[right].Name
	})
	maskPlan := dockerPlan
	maskPlan.Mounts = append([]MountExecutionPlan{}, dockerPlan.Mounts...)
	if output != nil {
		maskPlan.Mounts = append(maskPlan.Mounts, MountExecutionPlan{
			Name: "controller-output", Mode: blueprint.MountBind, Source: output.HostDirectory,
			SourceKind: deploy.RuntimeMountSourceDirectory, Target: runtimeOutputRoot,
		})
	}
	masks, err := privateRuntimeMasksForDeploymentRootsV1(maskPlan, protectedDeploymentRoots)
	if err != nil {
		return ControlledSessionContainerPlanV1{}, err
	}
	normalizedMasks := make([]ControlledSessionMaskV1, len(masks))
	for index, mask := range masks {
		normalizedMasks[index] = ControlledSessionMaskV1{Kind: string(mask.Kind), Target: mask.Target}
	}
	identity := controlledSessionRuntimeIdentityV1(dockerPlan.Sandbox.RuntimeUser)
	environment := []string{"HOME=" + dockerPlan.Sandbox.TemporaryHome, "TMPDIR=" + dockerPlan.Sandbox.TemporaryHome}
	if role == ControlledSessionRoleControllerV1 {
		environment = append(environment, "REPLOY_SESSION_SOCKET="+channel.ContainerSocket)
		if output != nil {
			environment = append(environment, output.Variable+"="+output.ContainerPath)
		}
	}
	labels := []ControlledSessionLabelV1{
		{Name: "io.reploy.session.build", Value: string(current.Generation.BuildLockDigest)},
		{Name: "io.reploy.session.environment", Value: dockerPlan.EnvironmentID},
		{Name: "io.reploy.session.generation", Value: current.Generation.Reference},
		{Name: "io.reploy.session.live-run", Value: liveRunID},
		{Name: "io.reploy.session.role", Value: string(role)},
	}
	plan := ControlledSessionContainerPlanV1{
		Schema: ControlledSessionContainerPlanSchemaV1, Role: role, LiveRunID: liveRunID,
		DeploymentID: dockerPlan.EnvironmentID, DeploymentDirectory: dockerPlan.DeploymentDir,
		GenerationReference: current.Generation.Reference, BuildIdentity: current.Generation.BuildLockDigest,
		Image: dockerPlan.Image, Container: dockerPlan.ContainerName + "-" + string(role) + "-" + liveRunID,
		RuntimeIdentity: identity, Network: controlledSessionDockerNetworkModeV1(dockerPlan.Sandbox.Network),
		NetworkPolicy: dockerPlan.Sandbox.Network, SessionNetwork: sessionNetwork,
		ReadOnlyRoot: dockerPlan.Sandbox.ReadOnlyRoot, TemporaryHome: dockerPlan.Sandbox.TemporaryHome,
		StartupVerifier: dockerPlan.Sandbox.StartupVerifier.Path,
		SetupCapabilities: controlledSessionSetupCapabilitiesV1(
			identity.UID,
			controlledSessionInstallsNetworkPolicyV1(dockerPlan.Sandbox.Network, sessionNetwork.Enabled),
		),
		SecurityOptions: []string{"no-new-privileges=true", "seccomp=" + dockerPlan.Sandbox.Kernel.SeccompProfile},
		Environment:     environment, Labels: labels, Mounts: mounts, Masks: normalizedMasks,
		Command: append([]string{}, command...), TTY: role == ControlledSessionRoleWorkloadV1,
		OpenStdin: role == ControlledSessionRoleWorkloadV1,
	}
	if role == ControlledSessionRoleWorkloadV1 {
		plan.InitialColumns = strconv.FormatUint(uint64(columns), 10)
		plan.InitialRows = strconv.FormatUint(uint64(rows), 10)
	}
	plan.Create, err = renderControlledSessionCreateV1(plan)
	if err != nil {
		return ControlledSessionContainerPlanV1{}, err
	}
	plan.Start = ControlledSessionDockerCommandV1{Name: "docker", Args: []string{"start", plan.Container}}
	plan.Cleanup = ControlledSessionDockerCommandV1{Name: "docker", Args: []string{"container", "rm", "--force", plan.Container}}
	if err := ValidateControlledSessionContainerPlanV1(plan); err != nil {
		return ControlledSessionContainerPlanV1{}, err
	}
	return plan, nil
}

func renderControlledSessionCreateV1(plan ControlledSessionContainerPlanV1) (ControlledSessionDockerCommandV1, error) {
	createNetwork := plan.Network
	if plan.SessionNetwork.Enabled && plan.Network == controlledSessionNetworkModeV1 {
		// Docker refuses to connect a container created in none mode to a later
		// network. The stopped container is staged on bridge; the session-network
		// adapter removes that attachment before either process starts.
		createNetwork = controlledSessionOrdinaryNetworkModeV1
	}
	args := []string{
		"create", "--pull", "never", "--name", plan.Container,
		"--network", createNetwork,
		"--user", "0:0",
		"--cap-drop", "ALL",
	}
	for _, capability := range plan.SetupCapabilities {
		args = append(args, "--cap-add", capability)
	}
	for _, resolver := range applicationDockerDNSResolversV1(plan.NetworkPolicy) {
		args = append(args, "--dns", resolver)
	}
	for _, option := range plan.SecurityOptions {
		args = append(args, "--security-opt", option)
	}
	if plan.ReadOnlyRoot {
		args = append(args, "--read-only")
	}
	args = append(args, "--tmpfs", transientHomeMountForSessionPlanV1(plan))
	for _, value := range plan.Environment {
		args = append(args, "--env", value)
	}
	if plan.OpenStdin {
		args = append(args, "--interactive")
	}
	if plan.TTY {
		args = append(args, "--tty")
	}
	for _, label := range plan.Labels {
		args = append(args, "--label", label.Name+"="+label.Value)
	}
	for _, mount := range plan.Mounts {
		fields := []string{"type=" + mount.Type, "target=" + mount.Target}
		if mount.Source != "" {
			fields = append(fields, "source="+mount.Source)
		}
		if mount.ReadOnly {
			fields = append(fields, "readonly")
		}
		value, err := dockerMountArgument(fields...)
		if err != nil {
			return ControlledSessionDockerCommandV1{}, err
		}
		args = append(args, "--mount", value)
	}
	for _, mask := range plan.Masks {
		switch privateRuntimeMaskKindV1(mask.Kind) {
		case privateRuntimeMaskDirectoryV1:
			args = append(args, "--tmpfs", mask.Target+":"+privateRuntimeDirectoryMaskOptionsV1)
		case privateRuntimeMaskFileV1:
			value, err := dockerMountArgument("type=bind", "source=/dev/null", "target="+mask.Target, "readonly")
			if err != nil {
				return ControlledSessionDockerCommandV1{}, err
			}
			args = append(args, "--mount", value)
		default:
			return ControlledSessionDockerCommandV1{}, fmt.Errorf("unsupported controlled-session mask kind %q", mask.Kind)
		}
	}
	args = append(args, "--entrypoint", plan.StartupVerifier, plan.Image)
	args = append(args, controlledSessionRestrictedArgvV1(plan)...)
	return ControlledSessionDockerCommandV1{Name: "docker", Args: args}, nil
}

func controlledSessionRestrictedArgvV1(plan ControlledSessionContainerPlanV1) []string {
	identity := plan.RuntimeIdentity
	mode := "restricted-exec"
	if controlledSessionInstallsNetworkPolicyV1(plan.NetworkPolicy, plan.SessionNetwork.Enabled) {
		mode = "sandbox-exec"
	}
	result := []string{mode, "--uid", identity.UID, "--gid", identity.GID}
	if len(identity.SupplementaryGIDs) != 0 {
		result = append(result, "--groups", strings.Join(identity.SupplementaryGIDs, ","))
	}
	if mode == "sandbox-exec" {
		result = append(result,
			"--public", string(plan.NetworkPolicy.Public),
			"--local", string(plan.NetworkPolicy.Local),
			"--ambiguous", string(plan.NetworkPolicy.Ambiguous),
		)
		if plan.SessionNetwork.Enabled {
			result = append(result,
				"--session-network-prefixes", controlledSessionNetworkPolicyPathV1,
				"--session-network-peer", plan.SessionNetwork.PeerAlias,
			)
		}
	}
	result = append(result, "--")
	return append(result, plan.Command...)
}

func controlledSessionDockerNetworkModeV1(policy ApplicationNetworkPolicyV1) string {
	if controlledSessionNeedsOrdinaryNetworkV1(policy) {
		return controlledSessionOrdinaryNetworkModeV1
	}
	return controlledSessionNetworkModeV1
}

func controlledSessionNeedsOrdinaryNetworkV1(policy ApplicationNetworkPolicyV1) bool {
	return policy.Public == blueprint.NetworkAccessAllow ||
		policy.Local == blueprint.NetworkAccessAllow ||
		policy.Ambiguous == blueprint.AmbiguousNetworkAccessAllow
}

func controlledSessionInstallsNetworkPolicyV1(policy ApplicationNetworkPolicyV1, sessionNetwork bool) bool {
	return sessionNetwork || controlledSessionNeedsOrdinaryNetworkV1(policy)
}

func controlledSessionRuntimeIdentityV1(user RuntimeUserPlan) controlledsession.RuntimeIdentityV1 {
	groups := make([]string, len(user.SupplementaryGIDs))
	for index, group := range user.SupplementaryGIDs {
		groups[index] = runtimeIDStringV1(group)
	}
	return controlledsession.RuntimeIdentityV1{
		Username: user.LocalUser, UID: runtimeIDStringV1(user.UID), GID: runtimeIDStringV1(user.GID), SupplementaryGIDs: groups,
	}
}

func controlledSessionSetupCapabilitiesV1(uid string, installNetworkPolicy bool) []string {
	result := []string{"SETGID", "SETPCAP"}
	if installNetworkPolicy {
		result = append(result, "NET_ADMIN")
	}
	if uid != "0" {
		result = append(result, "SETUID")
	}
	sort.Strings(result)
	return result
}

func environmentAuthorizationForContainerV1(plan ControlledSessionContainerPlanV1, digest canonical.Digest) controlledsession.EnvironmentAuthorizationV1 {
	return controlledsession.EnvironmentAuthorizationV1{
		DeploymentID: plan.DeploymentID, GenerationReference: plan.GenerationReference,
		BuildIdentity: plan.BuildIdentity, PlanDigest: digest, RuntimeIdentity: plan.RuntimeIdentity,
	}
}

func validateControlledSessionChannelV1(channel ControlledSessionChannelPlanV1, workloadDirectory string, liveRunID string) error {
	wantHost := filepath.Join(workloadDirectory, privateRuntimeMetadataDirectoryName, "sessions", liveRunID)
	if channel.HostDirectory != wantHost || !filepath.IsAbs(channel.HostDirectory) || filepath.Clean(channel.HostDirectory) != channel.HostDirectory {
		return fmt.Errorf("controlled-session channel host directory must be private to the workload deployment and live run")
	}
	if channel.ContainerDirectory != controlledSessionChannelRootV1 || channel.ContainerSocket != path.Join(channel.ContainerDirectory, controlledSessionChannelSocketNameV1) {
		return fmt.Errorf("controlled-session channel container paths must use the fixed session contract")
	}
	return nil
}

func validateControlledSessionLabelsV1(labels []ControlledSessionLabelV1, plan ControlledSessionContainerPlanV1) error {
	want := []ControlledSessionLabelV1{
		{Name: "io.reploy.session.build", Value: string(plan.BuildIdentity)},
		{Name: "io.reploy.session.environment", Value: plan.DeploymentID},
		{Name: "io.reploy.session.generation", Value: plan.GenerationReference},
		{Name: "io.reploy.session.live-run", Value: plan.LiveRunID},
		{Name: "io.reploy.session.role", Value: string(plan.Role)},
	}
	if !reflect.DeepEqual(labels, want) {
		return fmt.Errorf("container labels must bind the live run and role")
	}
	return nil
}

func validateControlledSessionMountsV1(mounts []ControlledSessionMountV1) error {
	names := make(map[string]bool, len(mounts))
	for index, mount := range mounts {
		if mount.Name == "" || !path.IsAbs(mount.Target) || path.Clean(mount.Target) != mount.Target {
			return fmt.Errorf("container mount %q is incomplete", mount.Name)
		}
		if names[mount.Name] {
			return fmt.Errorf("container mount name %q is duplicated", mount.Name)
		}
		names[mount.Name] = true
		if mount.Type != "bind" && mount.Type != "volume" && mount.Type != "tmpfs" {
			return fmt.Errorf("container mount %q has unsupported type %q", mount.Name, mount.Type)
		}
		switch mount.Type {
		case "bind":
			if !filepath.IsAbs(mount.Source) || filepath.Clean(mount.Source) != mount.Source {
				return fmt.Errorf("container bind mount %q requires an absolute clean source, got %q", mount.Name, mount.Source)
			}
			if mount.SourceKind != deploy.RuntimeMountSourceDirectory && mount.SourceKind != deploy.RuntimeMountSourceFile {
				return fmt.Errorf("container bind mount %q requires a file or directory source kind", mount.Name)
			}
		case "volume":
			if mount.Source == "" || mount.SourceKind != deploy.RuntimeMountSourceGenerated {
				return fmt.Errorf("container volume mount %q requires a generated named source", mount.Name)
			}
		case "tmpfs":
			if mount.Source != "" || mount.SourceKind != deploy.RuntimeMountSourceGenerated {
				return fmt.Errorf("container tmpfs mount %q must use an empty generated source", mount.Name)
			}
		}
		if index > 0 {
			previous := mounts[index-1]
			if previous.Target > mount.Target || previous.Target == mount.Target && previous.Name >= mount.Name {
				return fmt.Errorf("container mounts must be unique and sorted by target and name")
			}
		}
		for _, previous := range mounts[:index] {
			if runtimePolicyPathsOverlap(previous.Target, mount.Target) {
				return fmt.Errorf("container mounts %q and %q overlap", previous.Name, mount.Name)
			}
		}
	}
	return nil
}

func validateControlledSessionNetworkPolicyMountV1(plan ControlledSessionContainerPlanV1) error {
	want := ControlledSessionMountV1{
		Name: "session-network-prefixes", Type: "bind",
		SourceKind: deploy.RuntimeMountSourceFile, Target: controlledSessionNetworkPolicyPathV1, ReadOnly: true,
	}
	found := false
	for _, mount := range plan.Mounts {
		if mount.Name != want.Name {
			continue
		}
		got := mount
		got.Source = ""
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("container session-network policy mount does not match the fixed session contract")
		}
		found = true
	}
	if found != plan.SessionNetwork.Enabled {
		return fmt.Errorf("container session-network policy mount must match its network grant")
	}
	return nil
}

func controlledSessionContainerCarriesNetworkPolicyV1(plan ControlledSessionContainerPlanV1, channel ControlledSessionChannelPlanV1) bool {
	wantSource := filepath.Join(channel.HostDirectory, controlledSessionNetworkPolicyFileNameV1)
	found := false
	for _, mount := range plan.Mounts {
		if mount.Name == "session-network-prefixes" {
			found = mount.Source == wantSource
		}
	}
	return found == plan.SessionNetwork.Enabled
}

func validateControlledSessionMasksV1(masks []ControlledSessionMaskV1) error {
	for index, mask := range masks {
		if privateRuntimeMaskKindV1(mask.Kind) != privateRuntimeMaskDirectoryV1 && privateRuntimeMaskKindV1(mask.Kind) != privateRuntimeMaskFileV1 {
			return fmt.Errorf("container mask kind %q is unsupported", mask.Kind)
		}
		if !path.IsAbs(mask.Target) || path.Clean(mask.Target) != mask.Target {
			return fmt.Errorf("container mask target %q must be absolute and clean", mask.Target)
		}
		if index > 0 && masks[index-1].Target >= mask.Target {
			return fmt.Errorf("container masks must be unique and sorted")
		}
	}
	return nil
}

func validateControlledSessionMasksForRootsV1(
	plan ControlledSessionContainerPlanV1,
	channel ControlledSessionChannelPlanV1,
	protectedRoots []string,
) error {
	mounts := make([]MountExecutionPlan, 0, len(plan.Mounts))
	for _, mount := range plan.Mounts {
		if plan.Role == ControlledSessionRoleControllerV1 &&
			mount.Name == "session-channel" &&
			mount.Type == "bind" &&
			mount.Source == channel.HostDirectory &&
			mount.Target == channel.ContainerDirectory {
			continue
		}
		if plan.SessionNetwork.Enabled &&
			mount.Name == "session-network-prefixes" &&
			mount.Type == "bind" &&
			mount.Source == filepath.Join(channel.HostDirectory, controlledSessionNetworkPolicyFileNameV1) &&
			mount.SourceKind == deploy.RuntimeMountSourceFile &&
			mount.Target == controlledSessionNetworkPolicyPathV1 && mount.ReadOnly {
			continue
		}
		mounts = append(mounts, MountExecutionPlan{
			Name: mount.Name, Mode: blueprint.MountMode(mount.Type), Source: mount.Source,
			SourceKind: mount.SourceKind, Target: mount.Target, ReadOnly: mount.ReadOnly,
		})
	}
	expected, err := privateRuntimeMasksForDeploymentRootsV1(DockerExecutionPlan{
		DeploymentDir: plan.DeploymentDirectory,
		Mounts:        mounts,
	}, protectedRoots)
	if err != nil {
		return fmt.Errorf("derive private runtime masks: %w", err)
	}
	want := make([]ControlledSessionMaskV1, len(expected))
	for index, mask := range expected {
		want[index] = ControlledSessionMaskV1{Kind: string(mask.Kind), Target: mask.Target}
	}
	if !reflect.DeepEqual(plan.Masks, want) {
		return fmt.Errorf("private runtime masks do not match the exact controller and workload deployment roots")
	}
	return nil
}

func controlledSessionControllerCarriesChannelV1(plan ControlledSessionContainerPlanV1, channel ControlledSessionChannelPlanV1) bool {
	wantEnvironment := "REPLOY_SESSION_SOCKET=" + channel.ContainerSocket
	if !slices.Contains(plan.Environment, wantEnvironment) {
		return false
	}
	for _, mount := range plan.Mounts {
		if mount.Name == "session-channel" && mount.Type == "bind" && mount.Source == channel.HostDirectory &&
			mount.SourceKind == deploy.RuntimeMountSourceDirectory && mount.Target == channel.ContainerDirectory && mount.ReadOnly {
			return true
		}
	}
	return false
}

func controlledSessionContainerExposesChannelV1(plan ControlledSessionContainerPlanV1, channel ControlledSessionChannelPlanV1) bool {
	if slices.Contains(plan.Environment, "REPLOY_SESSION_SOCKET="+channel.ContainerSocket) {
		return true
	}
	for _, mount := range plan.Mounts {
		if runtimePolicyPathsOverlap(mount.Target, channel.ContainerDirectory) {
			return true
		}
	}
	return false
}

func validControlledSessionDimensionsV1(columns string, rows string) bool {
	columnValue, columnErr := strconv.ParseUint(columns, 10, 16)
	rowValue, rowErr := strconv.ParseUint(rows, 10, 16)
	return columnErr == nil && rowErr == nil && columnValue != 0 && rowValue != 0 &&
		strconv.FormatUint(columnValue, 10) == columns && strconv.FormatUint(rowValue, 10) == rows
}

func transientHomeMountForSessionPlanV1(plan ControlledSessionContainerPlanV1) string {
	return fmt.Sprintf(
		"%s:rw,noexec,nosuid,nodev,size=64m,mode=0700,uid=%s,gid=%s",
		plan.TemporaryHome,
		plan.RuntimeIdentity.UID,
		plan.RuntimeIdentity.GID,
	)
}
