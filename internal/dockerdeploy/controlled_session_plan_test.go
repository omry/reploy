package dockerdeploy

import (
	"errors"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/controlledsession"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

func TestPlanControlledSessionV1FreezesExactTwoEnvironmentAuthority(t *testing.T) {
	input, backend := controlledSessionPlanFixtureV1(t)
	plan, err := planControlledSessionV1(input, backend)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Controller.GenerationReference == plan.Workload.GenerationReference ||
		plan.Authorization.Controller.GenerationReference != plan.Controller.GenerationReference ||
		plan.Authorization.Workload.GenerationReference != plan.Workload.GenerationReference {
		t.Fatalf("generation binding = controller %q/%q workload %q/%q",
			plan.Controller.GenerationReference, plan.Authorization.Controller.GenerationReference,
			plan.Workload.GenerationReference, plan.Authorization.Workload.GenerationReference,
		)
	}
	if !slices.Equal(plan.Controller.Command, []string{"/opt/controller", "inspect", "target"}) {
		t.Fatalf("controller command = %#v", plan.Controller.Command)
	}
	if !slices.Equal(plan.Workload.Command, []string{"/bin/sh"}) || !plan.Workload.TTY || !plan.Workload.OpenStdin {
		t.Fatalf("workload shell = command %#v tty=%t stdin=%t", plan.Workload.Command, plan.Workload.TTY, plan.Workload.OpenStdin)
	}
	if plan.Controller.ControllerPackage == nil || plan.Controller.Image != string(input.ControllerPackage.Image.Digest) ||
		plan.Controller.Image == plan.Controller.GenerationReference {
		t.Fatalf("controller package authority = %#v image %q", plan.Controller.ControllerPackage, plan.Controller.Image)
	}
	if plan.Workload.ControllerPackage != nil || plan.Workload.Image != plan.Workload.GenerationReference {
		t.Fatalf("workload received controller package authority: package %#v image %q", plan.Workload.ControllerPackage, plan.Workload.Image)
	}
	if plan.Controller.Network != "none" || plan.Workload.Network != "none" {
		t.Fatalf("session networks = %q / %q", plan.Controller.Network, plan.Workload.Network)
	}
	if !containsInOrder(plan.Controller.Create.Args, []string{"--network", "none"}) ||
		!containsInOrder(plan.Workload.Create.Args, []string{"--network", "none", "--user", "0:0"}) ||
		containsString(plan.Controller.Create.Args, "--tty") || !containsString(plan.Workload.Create.Args, "--tty") ||
		containsString(plan.Controller.Create.Args, "--rm") || containsString(plan.Workload.Create.Args, "--rm") {
		t.Fatalf("Docker create commands =\ncontroller %#v\nworkload %#v", plan.Controller.Create.Args, plan.Workload.Create.Args)
	}
	if !controlledSessionControllerCarriesChannelV1(plan.Controller, plan.Channel) || controlledSessionContainerExposesChannelV1(plan.Workload, plan.Channel) {
		t.Fatalf("private channel exposure = controller %#v workload %#v", plan.Controller.Mounts, plan.Workload.Mounts)
	}
	if !reflect.DeepEqual(plan.Authorization.Operations, []controlledsession.OperationV1{
		controlledsession.OperationCompleteV1,
		controlledsession.OperationInputV1,
		controlledsession.OperationResizeV1,
		controlledsession.OperationTerminateV1,
	}) || len(plan.Authorization.EndpointIDs) != 0 {
		t.Fatalf("initial capability set = %#v / %#v", plan.Authorization.Operations, plan.Authorization.EndpointIDs)
	}
	if err := ValidateControlledSessionExecutionPlanV1(plan); err != nil {
		t.Fatalf("ValidateControlledSessionExecutionPlanV1() error = %v", err)
	}
}

func TestPlanControlledSessionV1PreservesExplicitOrdinaryNetworkGrantsWithEndpoints(t *testing.T) {
	input, backend := controlledSessionPlanFixtureV1(t)
	input.EndpointIDs = []string{"browser"}
	input.ControllerRuntime.Docker.Sandbox.Network = ApplicationNetworkPolicyV1{
		Public: blueprint.NetworkAccessAllow, Local: blueprint.NetworkAccessDeny,
		Ambiguous: blueprint.AmbiguousNetworkAccessRequireBoth,
	}
	input.WorkloadRuntime.Docker.Sandbox.Network = ApplicationNetworkPolicyV1{
		Public: blueprint.NetworkAccessDeny, Local: blueprint.NetworkAccessAllow,
		Ambiguous: blueprint.AmbiguousNetworkAccessRequireBoth,
	}
	plan, err := planControlledSessionV1(input, backend)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Controller.Network != controlledSessionOrdinaryNetworkModeV1 || plan.Workload.Network != controlledSessionOrdinaryNetworkModeV1 {
		t.Fatalf("ordinary network modes = controller %q workload %q", plan.Controller.Network, plan.Workload.Network)
	}
	if !containsInOrder(plan.Controller.Create.Args, []string{
		"--network", "bridge", "--user", "0:0", "--cap-drop", "ALL", "--cap-add", "NET_ADMIN",
	}) || !containsInOrder(plan.Controller.Create.Args, []string{
		"--dns", applicationGooglePublicDNSPrimaryV1, "--dns", applicationGooglePublicDNSSecondaryV1,
	}) {
		t.Fatalf("public-only controller create = %#v", plan.Controller.Create.Args)
	}
	if !containsInOrder(plan.Workload.Create.Args, []string{"--network", "bridge"}) ||
		containsString(plan.Workload.Create.Args, "--dns") {
		t.Fatalf("local-only workload create = %#v", plan.Workload.Create.Args)
	}
	for _, container := range []ControlledSessionContainerPlanV1{plan.Controller, plan.Workload} {
		if !containsInOrder(container.Create.Args, []string{
			"sandbox-exec", "--uid", container.RuntimeIdentity.UID,
			"--gid", container.RuntimeIdentity.GID,
			"--public", string(container.NetworkPolicy.Public),
			"--local", string(container.NetworkPolicy.Local),
			"--ambiguous", string(container.NetworkPolicy.Ambiguous),
			"--session-network-prefixes", controlledSessionNetworkPolicyPathV1,
			"--session-network-peer", container.SessionNetwork.PeerAlias,
		}) {
			t.Fatalf("%s sandbox create = %#v", container.Role, container.Create.Args)
		}
	}
}

func TestControlledSessionOrdinaryNetworkModeIncludesAmbiguousEscapeHatch(t *testing.T) {
	policy := ApplicationNetworkPolicyV1{
		Public: blueprint.NetworkAccessDeny, Local: blueprint.NetworkAccessDeny,
		Ambiguous: blueprint.AmbiguousNetworkAccessAllow,
	}
	if got := controlledSessionDockerNetworkModeV1(policy); got != controlledSessionOrdinaryNetworkModeV1 {
		t.Fatalf("ambiguous escape-hatch network mode = %q", got)
	}
	if !controlledSessionInstallsNetworkPolicyV1(policy, false) {
		t.Fatal("ambiguous escape hatch did not install the application firewall")
	}
}

func TestPlanControlledSessionV1BindsEveryCoveredInputToPlanDigests(t *testing.T) {
	baseInput, backend := controlledSessionPlanFixtureV1(t)
	base, err := planControlledSessionV1(baseInput, backend)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name              string
		mutate            func(*ControlledSessionPlanInputV1)
		controllerChanged bool
		workloadChanged   bool
	}{
		{name: "controller arguments", mutate: func(input *ControlledSessionPlanInputV1) {
			input.ControllerForwardedArguments = []string{"other"}
		}, controllerChanged: true},
		{name: "controller identity", mutate: func(input *ControlledSessionPlanInputV1) {
			input.ControllerRuntime.Docker.Sandbox = testApplicationSandboxPlanV1(2000, 2000)
		}, controllerChanged: true},
		{name: "controller network policy", mutate: func(input *ControlledSessionPlanInputV1) {
			input.ControllerRuntime.Docker.Sandbox.Network.Public = blueprint.NetworkAccessAllow
		}, controllerChanged: true},
		{name: "controller mount", mutate: func(input *ControlledSessionPlanInputV1) {
			input.ControllerRuntime.Docker.Mounts = []MountExecutionPlan{{Name: "cache", Mode: blueprint.MountVolume, Source: "controller-cache", Target: "/cache"}}
		}, controllerChanged: true},
		{name: "controller generation", mutate: func(input *ControlledSessionPlanInputV1) {
			input.ControllerCurrent.Generation.Reference = "reploy/env/controller:g-other"
			input.ControllerCurrent.State.Current = &input.ControllerCurrent.Generation
			input.ControllerRuntime.Docker.Image = input.ControllerCurrent.Generation.Reference
		}, controllerChanged: true},
		{name: "workload dimensions", mutate: func(input *ControlledSessionPlanInputV1) {
			input.InitialColumns = 132
		}, workloadChanged: true},
		{name: "workload identity", mutate: func(input *ControlledSessionPlanInputV1) {
			input.WorkloadRuntime.Docker.Sandbox = testApplicationSandboxPlanV1(3000, 3000)
		}, workloadChanged: true},
		{name: "workload network policy", mutate: func(input *ControlledSessionPlanInputV1) {
			input.WorkloadRuntime.Docker.Sandbox.Network.Local = blueprint.NetworkAccessAllow
		}, workloadChanged: true},
		{name: "workload mount", mutate: func(input *ControlledSessionPlanInputV1) {
			input.WorkloadRuntime.Docker.Mounts = []MountExecutionPlan{{Name: "data", Mode: blueprint.MountVolume, Source: "workload-data", Target: "/data"}}
		}, workloadChanged: true},
		{name: "endpoint grant", mutate: func(input *ControlledSessionPlanInputV1) {
			input.EndpointIDs = []string{"browser"}
		}, controllerChanged: true, workloadChanged: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := baseInput
			input.ControllerForwardedArguments = append([]string{}, baseInput.ControllerForwardedArguments...)
			test.mutate(&input)
			changed, err := planControlledSessionV1(input, backend)
			if err != nil {
				t.Fatal(err)
			}
			controllerChanged := changed.Authorization.Controller.PlanDigest != base.Authorization.Controller.PlanDigest
			workloadChanged := changed.Authorization.Workload.PlanDigest != base.Authorization.Workload.PlanDigest
			if controllerChanged != test.controllerChanged || workloadChanged != test.workloadChanged {
				t.Fatalf("digest changes = controller %t workload %t, want %t/%t", controllerChanged, workloadChanged, test.controllerChanged, test.workloadChanged)
			}
		})
	}
}

func TestPlanControlledSessionV1FreezesGrantedEndpointCoordinatesAndLeaseNetwork(t *testing.T) {
	input, backend := controlledSessionPlanFixtureV1(t)
	input.EndpointIDs = []string{"socket", "browser"}
	plan, err := planControlledSessionV1(input, backend)
	if err != nil {
		t.Fatal(err)
	}
	wantEndpoints := []ControlledSessionEndpointPlanV1{
		{ID: "browser", Scheme: "http", Host: controlledsession.WorkloadEndpointHostV1, Port: "8080"},
		{ID: "socket", Scheme: "ws", Host: controlledsession.WorkloadEndpointHostV1, Port: "9090"},
	}
	wantName := input.WorkloadRuntime.Docker.NetworkName + "-session-" + input.LiveRunID
	if !reflect.DeepEqual(plan.Controller.SessionNetwork.Endpoints, wantEndpoints) ||
		!reflect.DeepEqual(plan.Workload.SessionNetwork.Endpoints, wantEndpoints) ||
		!slices.Equal(plan.Authorization.EndpointIDs, []string{"browser", "socket"}) {
		t.Fatalf("endpoint grants = controller %#v workload %#v authorization %#v",
			plan.Controller.SessionNetwork.Endpoints, plan.Workload.SessionNetwork.Endpoints, plan.Authorization.EndpointIDs)
	}
	if plan.Controller.SessionNetwork.Name != wantName || plan.Workload.SessionNetwork.Name != wantName ||
		!plan.Controller.SessionNetwork.Internal || !plan.Workload.SessionNetwork.Internal ||
		plan.Controller.SessionNetwork.Alias != controlledSessionControllerAliasV1 ||
		plan.Controller.SessionNetwork.PeerAlias != controlledSessionWorkloadAliasV1 ||
		plan.Workload.SessionNetwork.Alias != controlledSessionWorkloadAliasV1 ||
		plan.Workload.SessionNetwork.PeerAlias != controlledSessionControllerAliasV1 {
		t.Fatalf("session network grants = controller %#v workload %#v", plan.Controller.SessionNetwork, plan.Workload.SessionNetwork)
	}
	if plan.Controller.Network != controlledSessionNetworkModeV1 || plan.Workload.Network != controlledSessionNetworkModeV1 ||
		!containsInOrder(plan.Controller.Create.Args, []string{
			"--network", controlledSessionOrdinaryNetworkModeV1,
			"--user", "0:0", "--cap-drop", "ALL",
		}) ||
		!containsInOrder(plan.Workload.Create.Args, []string{
			"--network", controlledSessionOrdinaryNetworkModeV1,
			"--user", "0:0", "--cap-drop", "ALL",
		}) {
		t.Fatalf("inert Docker network modes = %q/%q", plan.Controller.Network, plan.Workload.Network)
	}
	wantOpenedEndpoints := []controlledsession.EndpointV1{
		{ID: "browser", Scheme: "http", Host: controlledsession.WorkloadEndpointHostV1, Port: 8080},
		{ID: "socket", Scheme: "ws", Host: controlledsession.WorkloadEndpointHostV1, Port: 9090},
	}
	if got := controlledSessionOpenedEndpointsV1(plan.Controller.SessionNetwork.Endpoints); !reflect.DeepEqual(got, wantOpenedEndpoints) {
		t.Fatalf("planned opened endpoints = %#v, want %#v", got, wantOpenedEndpoints)
	}
	config, err := controlledSessionPrivateChannelConfigV1(plan)
	if err != nil || !reflect.DeepEqual(config.Opened.Endpoints, wantOpenedEndpoints) {
		t.Fatalf("controlledSessionPrivateChannelConfigV1 endpoints = %#v, error = %v", config.Opened.Endpoints, err)
	}
	for _, container := range []ControlledSessionContainerPlanV1{plan.Controller, plan.Workload} {
		if !slices.Contains(container.SetupCapabilities, "NET_ADMIN") ||
			!containsInOrder(container.Create.Args, []string{"sandbox-exec", "--uid", container.RuntimeIdentity.UID}) ||
			!containsInOrder(container.Create.Args, []string{
				"--public", string(container.NetworkPolicy.Public),
				"--local", string(container.NetworkPolicy.Local),
				"--ambiguous", string(container.NetworkPolicy.Ambiguous),
				"--session-network-prefixes", controlledSessionNetworkPolicyPathV1,
				"--session-network-peer", container.SessionNetwork.PeerAlias,
			}) || !controlledSessionContainerCarriesNetworkPolicyV1(container, plan.Channel) {
			t.Fatalf("%s container does not carry the immutable session-network firewall contract: %#v", container.Role, container)
		}
	}
}

func TestPlanControlledSessionV1RejectsUnknownDuplicateAndInvalidEndpointGrants(t *testing.T) {
	for _, test := range []struct {
		name   string
		ids    []string
		mutate func(*ControlledSessionPlanInputV1)
		want   string
	}{
		{name: "unknown", ids: []string{"missing"}, want: "not declared"},
		{name: "duplicate", ids: []string{"browser", "browser"}, want: "duplicated"},
		{name: "invalid coordinate", ids: []string{"browser"}, mutate: func(input *ControlledSessionPlanInputV1) {
			input.WorkloadRuntime.Docker.Workload.Endpoints["browser"] = EndpointExecutionPlan{Scheme: "HTTP", ContainerPort: 8080}
		}, want: "does not match the resolved workload document"},
		{name: "mismatched valid scheme", ids: []string{"browser"}, mutate: func(input *ControlledSessionPlanInputV1) {
			input.WorkloadRuntime.Docker.Workload.Endpoints["browser"] = EndpointExecutionPlan{Scheme: "https", ContainerPort: 8080}
		}, want: "does not match the resolved workload document"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input, backend := controlledSessionPlanFixtureV1(t)
			input.EndpointIDs = test.ids
			if test.mutate != nil {
				test.mutate(&input)
			}
			if _, err := planControlledSessionV1(input, backend); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestPlanControlledSessionV1PreservesExactResolvedEndpointScheme(t *testing.T) {
	input, backend := controlledSessionPlanFixtureV1(t)
	scheme := "HTTP" + strings.Repeat("x", 40)
	input.WorkloadRuntime.Document.Environment.Workload.Endpoints["browser"] = blueprint.Endpoint{Scheme: scheme, Port: 8080}
	input.WorkloadCurrent.State.Blueprint = testResolvedBlueprintV1(t, input.WorkloadRuntime.Document)
	input.WorkloadRuntime.Docker.Workload.Endpoints["browser"] = EndpointExecutionPlan{Scheme: scheme, ContainerPort: 8080}
	input.EndpointIDs = []string{"browser"}

	plan, err := planControlledSessionV1(input, backend)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Controller.SessionNetwork.Endpoints[0].Scheme; got != scheme {
		t.Fatalf("planned endpoint scheme = %q, want exact resolved scheme %q", got, scheme)
	}
}

func TestValidateControlledSessionExecutionPlanV1RejectsNetworkGrantExpansion(t *testing.T) {
	input, backend := controlledSessionPlanFixtureV1(t)
	input.EndpointIDs = []string{"browser"}
	base, err := planControlledSessionV1(input, backend)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*ControlledSessionExecutionPlanV1)
		want   string
	}{
		{name: "network name", mutate: func(plan *ControlledSessionExecutionPlanV1) {
			plan.Controller.SessionNetwork.Name = "other"
		}, want: "lease-local network"},
		{name: "external network", mutate: func(plan *ControlledSessionExecutionPlanV1) {
			plan.Workload.SessionNetwork.Internal = false
		}, want: "engine-internal"},
		{name: "peer alias", mutate: func(plan *ControlledSessionExecutionPlanV1) {
			plan.Controller.SessionNetwork.PeerAlias = controlledSessionControllerAliasV1
		}, want: "fixed controller and workload"},
		{name: "endpoint coordinate", mutate: func(plan *ControlledSessionExecutionPlanV1) {
			plan.Controller.SessionNetwork.Endpoints[0].Port = "8081"
		}, want: "lease-local network"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := cloneControlledSessionExecutionPlanForTestV1(base)
			test.mutate(&plan)
			if err := ValidateControlledSessionExecutionPlanV1(plan); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestValidateControlledSessionExecutionPlanV1RejectsAuthorityAndCommandExpansion(t *testing.T) {
	input, backend := controlledSessionPlanFixtureV1(t)
	base, err := planControlledSessionV1(input, backend)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*ControlledSessionExecutionPlanV1)
		want   string
	}{
		{name: "authorization", mutate: func(plan *ControlledSessionExecutionPlanV1) {
			plan.Authorization.EndpointIDs = []string{"browser"}
		}, want: "authorization does not match"},
		{name: "network command", mutate: func(plan *ControlledSessionExecutionPlanV1) {
			index := slices.Index(plan.Workload.Create.Args, "none")
			plan.Workload.Create.Args[index] = "bridge"
		}, want: "create command does not reflect"},
		{name: "workload channel environment", mutate: func(plan *ControlledSessionExecutionPlanV1) {
			plan.Workload.Environment = append(plan.Workload.Environment, "REPLOY_SESSION_SOCKET="+plan.Channel.ContainerSocket)
			plan.Workload.Create, _ = renderControlledSessionCreateV1(plan.Workload)
		}, want: "must not expose"},
		{name: "wrong start container", mutate: func(plan *ControlledSessionExecutionPlanV1) {
			plan.Controller.Start.Args[1] = "other"
		}, want: "lifecycle commands"},
		{name: "relative bind source", mutate: func(plan *ControlledSessionExecutionPlanV1) {
			plan.Workload.Mounts = []ControlledSessionMountV1{{
				Name: "source", Type: "bind", Source: "relative", SourceKind: deploy.RuntimeMountSourceDirectory,
				Target: "/source", ReadOnly: true,
			}}
		}, want: "absolute clean source"},
		{name: "tmpfs source", mutate: func(plan *ControlledSessionExecutionPlanV1) {
			plan.Workload.Mounts = []ControlledSessionMountV1{{
				Name: "scratch", Type: "tmpfs", Source: "unexpected", SourceKind: deploy.RuntimeMountSourceGenerated,
				Target: "/scratch",
			}}
		}, want: "empty generated source"},
		{name: "channel source kind", mutate: func(plan *ControlledSessionExecutionPlanV1) {
			index := slices.IndexFunc(plan.Controller.Mounts, func(mount ControlledSessionMountV1) bool {
				return mount.Name == "session-channel"
			})
			plan.Controller.Mounts[index].SourceKind = deploy.RuntimeMountSourceFile
			plan.Controller.Create, _ = renderControlledSessionCreateV1(plan.Controller)
		}, want: "does not carry the exact private channel"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := cloneControlledSessionExecutionPlanForTestV1(base)
			test.mutate(&plan)
			err := ValidateControlledSessionExecutionPlanV1(plan)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestPlanControlledSessionV1RejectsStaleBuildInvalidDimensionsAndChannelOverlap(t *testing.T) {
	input, backend := controlledSessionPlanFixtureV1(t)
	backend.requireReady = func(CurrentBuild, CurrentRuntimePlanV1, string, *transientOutputMount) error {
		return errors.New("runtime build is missing or stale")
	}
	if _, err := planControlledSessionV1(input, backend); err == nil || !strings.Contains(err.Error(), "missing or stale") {
		t.Fatalf("stale build error = %v", err)
	}

	input, backend = controlledSessionPlanFixtureV1(t)
	input.InitialRows = 0
	if _, err := planControlledSessionV1(input, backend); err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("dimensions error = %v", err)
	}

	input, backend = controlledSessionPlanFixtureV1(t)
	input.ControllerRuntime.Docker.Mounts = []MountExecutionPlan{{
		Name: "reserved", Mode: blueprint.MountVolume, Source: "reserved", Target: controlledSessionChannelRootV1,
	}}
	if _, err := planControlledSessionV1(input, backend); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("channel overlap error = %v", err)
	}
}

func TestPlanControlledSessionV1RejectsSameRootAndPrivateEnvironment(t *testing.T) {
	input, backend := controlledSessionPlanFixtureV1(t)
	input.WorkloadRuntime.Docker.DeploymentDir = input.ControllerRuntime.Docker.DeploymentDir
	if _, err := planControlledSessionV1(input, backend); err == nil || !strings.Contains(err.Error(), "distinct controller and workload deployment roots") {
		t.Fatalf("same-root error = %v", err)
	}

	input, backend = controlledSessionPlanFixtureV1(t)
	input.ControllerRuntime.Docker.PrivateEnvironment = true
	if _, err := planControlledSessionV1(input, backend); err == nil || !strings.Contains(err.Error(), "does not yet support private environment injection") {
		t.Fatalf("private-environment error = %v", err)
	}
}

func TestPlanControlledSessionV1MasksBothDeploymentRootsFromDeclaredBinds(t *testing.T) {
	input, backend := controlledSessionPlanFixtureV1(t)
	input.ControllerRuntime.Docker.Mounts = []MountExecutionPlan{{
		Name: "workload-source", Mode: blueprint.MountBind,
		Source: input.WorkloadRuntime.Docker.DeploymentDir, SourceKind: deploy.RuntimeMountSourceDirectory,
		Target: "/workload", ReadOnly: true,
	}}
	input.WorkloadRuntime.Docker.Mounts = []MountExecutionPlan{{
		Name: "controller-source", Mode: blueprint.MountBind,
		Source: input.ControllerRuntime.Docker.DeploymentDir, SourceKind: deploy.RuntimeMountSourceDirectory,
		Target: "/controller", ReadOnly: true,
	}}
	plan, err := planControlledSessionV1(input, backend)
	if err != nil {
		t.Fatal(err)
	}
	for role, masks := range map[string][]ControlledSessionMaskV1{
		"controller": plan.Controller.Masks,
		"workload":   plan.Workload.Masks,
	} {
		wantRoot := "/workload"
		if role == "workload" {
			wantRoot = "/controller"
		}
		for _, target := range []string{wantRoot + "/.env", wantRoot + "/.reploy"} {
			if !slices.ContainsFunc(masks, func(mask ControlledSessionMaskV1) bool { return mask.Target == target }) {
				t.Fatalf("%s masks %#v do not protect %q", role, masks, target)
			}
		}
	}
}

func TestValidateControlledSessionExecutionPlanV1RederivesCrossEnvironmentMasks(t *testing.T) {
	input, backend := controlledSessionPlanFixtureV1(t)
	input.ControllerRuntime.Docker.Mounts = []MountExecutionPlan{{
		Name: "workload-source", Mode: blueprint.MountBind,
		Source: input.WorkloadRuntime.Docker.DeploymentDir, SourceKind: deploy.RuntimeMountSourceDirectory,
		Target: "/workload", ReadOnly: true,
	}}
	plan, err := planControlledSessionV1(input, backend)
	if err != nil {
		t.Fatal(err)
	}
	plan.Controller.Masks = plan.Controller.Masks[1:]
	plan.Controller.Create, err = renderControlledSessionCreateV1(plan.Controller)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := ControlledSessionContainerPlanDigestV1(plan.Controller)
	if err != nil {
		t.Fatal(err)
	}
	plan.Authorization.Controller = environmentAuthorizationForContainerV1(plan.Controller, digest)
	if err := ValidateControlledSessionExecutionPlanV1(plan); err == nil || !strings.Contains(err.Error(), "masks do not match") {
		t.Fatalf("error = %v, want exact-mask rejection", err)
	}
}

func TestValidateControlledSessionExecutionPlanV1RejectsSameDeploymentRoot(t *testing.T) {
	input, backend := controlledSessionPlanFixtureV1(t)
	plan, err := planControlledSessionV1(input, backend)
	if err != nil {
		t.Fatal(err)
	}
	plan.Workload.DeploymentDirectory = plan.Controller.DeploymentDirectory
	if err := ValidateControlledSessionExecutionPlanV1(plan); err == nil || !strings.Contains(err.Error(), "distinct controller and workload deployment roots") {
		t.Fatalf("error = %v, want distinct-root rejection", err)
	}
}

func TestPlanControlledSessionV1UsesInvocationSpecificReadinessChecks(t *testing.T) {
	input, backend := controlledSessionPlanFixtureV1(t)
	var planIDs []string
	backend.requireReady = func(_ CurrentBuild, _ CurrentRuntimePlanV1, planID string, _ *transientOutputMount) error {
		planIDs = append(planIDs, planID)
		return nil
	}
	if _, err := planControlledSessionV1(input, backend); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(planIDs, []string{runtimeCommandPlanID("inspect", false), runtimeShellPlanID}) {
		t.Fatalf("readiness plan IDs = %#v", planIDs)
	}
}

func TestPlanControlledSessionV1ExposesOutputOnlyToController(t *testing.T) {
	input, backend := controlledSessionPlanFixtureV1(t)
	output := &transientOutputMount{
		HostDirectory: filepath.Join(t.TempDir(), "output"),
		Variable:      runtimeOutputFileVariable,
		ContainerPath: path.Join(runtimeOutputRoot, runtimeOutputFileName),
	}
	input.controllerOutput = output
	var readinessOutputs []*transientOutputMount
	var readinessPlanIDs []string
	backend.requireReady = func(_ CurrentBuild, _ CurrentRuntimePlanV1, planID string, got *transientOutputMount) error {
		readinessPlanIDs = append(readinessPlanIDs, planID)
		readinessOutputs = append(readinessOutputs, got)
		return nil
	}

	plan, err := planControlledSessionV1(input, backend)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(readinessPlanIDs, []string{runtimeCommandPlanID("inspect", true), runtimeShellPlanID}) ||
		len(readinessOutputs) != 2 || readinessOutputs[0] != output || readinessOutputs[1] != nil {
		t.Fatalf("readiness checks = IDs %#v outputs %#v", readinessPlanIDs, readinessOutputs)
	}
	wantEnvironment := runtimeOutputFileVariable + "=" + path.Join(runtimeOutputRoot, runtimeOutputFileName)
	if !slices.Contains(plan.Controller.Environment, wantEnvironment) {
		t.Fatalf("controller environment = %#v", plan.Controller.Environment)
	}
	outputMount := slices.IndexFunc(plan.Controller.Mounts, func(mount ControlledSessionMountV1) bool {
		return mount.Name == "controller-output"
	})
	if outputMount < 0 || plan.Controller.Mounts[outputMount].Source != output.HostDirectory ||
		plan.Controller.Mounts[outputMount].Target != runtimeOutputRoot {
		t.Fatalf("controller mounts = %#v", plan.Controller.Mounts)
	}
	if slices.Contains(plan.Workload.Environment, wantEnvironment) || slices.ContainsFunc(plan.Workload.Mounts, func(mount ControlledSessionMountV1) bool {
		return mount.Name == "controller-output" || mount.Target == runtimeOutputRoot
	}) {
		t.Fatalf("workload received controller output: env %#v mounts %#v", plan.Workload.Environment, plan.Workload.Mounts)
	}

	mutated := cloneControlledSessionExecutionPlanForTestV1(plan)
	mutated.Workload.Mounts = append(mutated.Workload.Mounts, plan.Controller.Mounts[outputMount])
	slices.SortFunc(mutated.Workload.Mounts, func(left ControlledSessionMountV1, right ControlledSessionMountV1) int {
		if left.Target != right.Target {
			return strings.Compare(left.Target, right.Target)
		}
		return strings.Compare(left.Name, right.Name)
	})
	mutated.Workload.Environment = append(mutated.Workload.Environment, wantEnvironment)
	mutated.Workload.Create, _ = renderControlledSessionCreateV1(mutated.Workload)
	if err := ValidateControlledSessionExecutionPlanV1(mutated); err == nil || !strings.Contains(err.Error(), "workload plan must not expose") {
		t.Fatalf("workload output mutation error = %v", err)
	}
}

func TestPlanControlledSessionV1MasksProtectedPathsExposedByControllerOutput(t *testing.T) {
	input, backend := controlledSessionPlanFixtureV1(t)
	input.controllerOutput = &transientOutputMount{
		HostDirectory: input.ControllerRuntime.Docker.DeploymentDir,
		Variable:      runtimeOutputDirectoryVariable,
		ContainerPath: runtimeOutputRoot,
	}

	plan, err := planControlledSessionV1(input, backend)
	if err != nil {
		t.Fatal(err)
	}
	want := []ControlledSessionMaskV1{
		{Kind: string(privateRuntimeMaskFileV1), Target: path.Join(runtimeOutputRoot, PrivateWorkloadEnvironmentFileName)},
		{Kind: string(privateRuntimeMaskDirectoryV1), Target: path.Join(runtimeOutputRoot, privateRuntimeMetadataDirectoryName)},
	}
	if !reflect.DeepEqual(plan.Controller.Masks, want) {
		t.Fatalf("controller masks = %#v, want %#v", plan.Controller.Masks, want)
	}
	if err := ValidateControlledSessionExecutionPlanV1(plan); err != nil {
		t.Fatalf("ValidateControlledSessionExecutionPlanV1() error = %v", err)
	}
}

func controlledSessionPlanFixtureV1(t *testing.T) (ControlledSessionPlanInputV1, controlledSessionPlanBackendV1) {
	t.Helper()
	controllerCurrent, controllerRuntime := controlledSessionEnvironmentFixtureV1(t, "controller", "1", 1000)
	controllerRuntime.Document.Environment.Base.Exports = map[string]blueprint.BaseExecutableExport{
		"python": {Executable: "/usr/bin/python3"},
	}
	controllerRuntime.Document.Environment.Applications = map[string]blueprint.Application{
		"controller": {
			Packages: blueprint.ApplicationPackages{Python: &blueprint.PythonComponent{
				Interpreter:  blueprint.CommandRequirement{Command: "python", Version: ">=3.11", Supplier: "base"},
				Requirements: []string{"controller"},
			}},
			Options:     map[string]blueprint.ApplicationOption{},
			Executables: map[string]blueprint.Executable{"main": {Source: "python", Binary: "controller"}},
		},
	}
	controllerRuntime.Document.Environment.Commands = map[string]blueprint.Command{
		"inspect": {
			Executable: "controller.main", Trigger: []string{"inspect"}, NativeCommand: true,
			Argv: []string{"inspect"}, Order: blueprint.DefaultArgumentOrder,
		},
	}
	if err := controllerRuntime.Document.Environment.RebuildProviderContributions(); err != nil {
		t.Fatal(err)
	}
	controllerCurrent.State.Blueprint = testResolvedBlueprintV1(t, controllerRuntime.Document)

	workloadCurrent, workloadRuntime := controlledSessionEnvironmentFixtureV1(t, "workload", "2", 1001)
	workloadRuntime.Document.Environment.Workload = &blueprint.Workload{
		Command: "serve",
		Endpoints: map[string]blueprint.Endpoint{
			"browser": {Scheme: "http", Port: 8080},
			"socket":  {Scheme: "ws", Port: 9090},
		},
	}
	workloadCurrent.State.Blueprint = testResolvedBlueprintV1(t, workloadRuntime.Document)
	workloadRuntime.Docker.Workload = &WorkloadExecutionPlan{Endpoints: map[string]EndpointExecutionPlan{
		"browser": {Scheme: "http", ContainerPort: 8080},
		"socket":  {Scheme: "ws", ContainerPort: 9090},
	}}
	input := ControlledSessionPlanInputV1{
		Handle: "session-" + strings.Repeat("a", 64), LiveRunID: "run-0000000000000001",
		ControllerCurrent: controllerCurrent, ControllerRuntime: controllerRuntime,
		ControllerPackage: controlledSessionControllerPackageFixtureV1(controllerCurrent),
		ControllerCommand: "inspect", ControllerForwardedArguments: []string{"target"},
		WorkloadCurrent: workloadCurrent, WorkloadRuntime: workloadRuntime,
		InitialColumns: 80, InitialRows: 24,
	}
	backend := controlledSessionPlanBackendV1{
		requireReady: func(CurrentBuild, CurrentRuntimePlanV1, string, *transientOutputMount) error { return nil },
		resolveCommand: func(_ blueprint.Document, _ CurrentBuild, _ DockerExecutionPlan, name string, arguments []string) (ResolvedEnvironmentCommand, error) {
			return ResolvedEnvironmentCommand{Name: name, Native: true, Argv: append([]string{"/opt/controller", name}, arguments...)}, nil
		},
	}
	return input, backend
}

func controlledSessionEnvironmentFixtureV1(t *testing.T, id string, digestCharacter string, uid uint32) (CurrentBuild, CurrentRuntimePlanV1) {
	t.Helper()
	document, platform := testSelectedPlatformDocumentV1(t)
	document.Environment.ID = id
	document.Environment.ControlScript = id
	if err := document.Environment.RebuildProviderContributions(); err != nil {
		t.Fatal(err)
	}
	digest := canonical.Digest("sha256:" + strings.Repeat(digestCharacter, 64))
	generation := deploy.EnvironmentGenerationState{
		Reference: "reploy/env/" + id + ":g-current", ImageDigest: digest, RootFSSubject: digest,
		BuildLockDigest: digest, Platform: platform, RuntimePolicyDigest: digest,
	}
	state := deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, document), Platform: platform,
		Overlay: deploy.EmptyRequestOverlayV1(), Current: &generation,
	}
	directory := t.TempDir()
	dockerPlan := DockerExecutionPlan{
		EnvironmentID: id, DeploymentDir: directory, Phase: blueprint.PhaseStaged,
		Image: generation.Reference, ContainerName: id + "-staging-deadbeef", NetworkName: id + "-staging-deadbeef",
		Sandbox: testApplicationSandboxPlanV1(uid, uid),
	}
	lock := deploy.BuildLockV1{Platform: platform, FinalImage: providers.RealizedImageV1{
		Digest: digest, ConfigDigest: digest, RootFSSubject: digest,
	}}
	return CurrentBuild{State: state, Generation: generation, Lock: lock}, CurrentRuntimePlanV1{Document: document, Docker: dockerPlan}
}

func controlledSessionControllerPackageFixtureV1(current CurrentBuild) ControlledSessionControllerPackageV1 {
	imageDigest := canonical.Digest("sha256:" + strings.Repeat("f", 64))
	return ControlledSessionControllerPackageV1{
		Schema:      ControlledSessionControllerPackageSchemaV1,
		Platform:    current.Lock.Platform,
		Release:     currentControllerReleaseV1(),
		Artifact:    canonical.Digest("sha256:" + strings.Repeat("e", 64)),
		SourceImage: current.Lock.FinalImage,
		Image:       providers.RealizedImageV1{Digest: imageDigest, ConfigDigest: imageDigest, RootFSSubject: imageDigest},
		Executable:  controlledSessionControllerExecutableV1,
	}
}

func controlledSessionControllerPackageFixtureForImageV1(current CurrentBuild, image string) ControlledSessionControllerPackageV1 {
	controllerPackage := controlledSessionControllerPackageFixtureV1(current)
	imageDigest := canonical.Digest(image)
	if imageDigest.Validate() != nil {
		imageDigest = canonical.Digest("sha256:" + strings.Repeat("d", 64))
	}
	controllerPackage.Image = providers.RealizedImageV1{
		Digest: imageDigest, ConfigDigest: imageDigest, RootFSSubject: imageDigest,
	}
	return controllerPackage
}

func cloneControlledSessionExecutionPlanForTestV1(plan ControlledSessionExecutionPlanV1) ControlledSessionExecutionPlanV1 {
	clone := plan
	clone.Controller = cloneControlledSessionContainerPlanForTestV1(plan.Controller)
	clone.Workload = cloneControlledSessionContainerPlanForTestV1(plan.Workload)
	clone.Authorization.Controller.RuntimeIdentity.SupplementaryGIDs = append([]string{}, plan.Authorization.Controller.RuntimeIdentity.SupplementaryGIDs...)
	clone.Authorization.Workload.RuntimeIdentity.SupplementaryGIDs = append([]string{}, plan.Authorization.Workload.RuntimeIdentity.SupplementaryGIDs...)
	clone.Authorization.Operations = append([]controlledsession.OperationV1{}, plan.Authorization.Operations...)
	clone.Authorization.EndpointIDs = append([]string{}, plan.Authorization.EndpointIDs...)
	return clone
}

func cloneControlledSessionContainerPlanForTestV1(plan ControlledSessionContainerPlanV1) ControlledSessionContainerPlanV1 {
	clone := plan
	clone.RuntimeIdentity.SupplementaryGIDs = append([]string{}, plan.RuntimeIdentity.SupplementaryGIDs...)
	clone.SessionNetwork.Endpoints = append([]ControlledSessionEndpointPlanV1{}, plan.SessionNetwork.Endpoints...)
	clone.SetupCapabilities = append([]string{}, plan.SetupCapabilities...)
	clone.SecurityOptions = append([]string{}, plan.SecurityOptions...)
	clone.Environment = append([]string{}, plan.Environment...)
	clone.Labels = append([]ControlledSessionLabelV1{}, plan.Labels...)
	clone.Mounts = append([]ControlledSessionMountV1{}, plan.Mounts...)
	clone.Masks = append([]ControlledSessionMaskV1{}, plan.Masks...)
	clone.Command = append([]string{}, plan.Command...)
	clone.Create.Args = append([]string{}, plan.Create.Args...)
	clone.Start.Args = append([]string{}, plan.Start.Args...)
	clone.Cleanup.Args = append([]string{}, plan.Cleanup.Args...)
	return clone
}
