package dockerdeploy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
)

func TestApplicationNetworkPolicyDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	image, _ := buildApplicationStartupVerifierIntegrationImage(t, ctx)
	helper := buildNetworkPolicyIntegrationHelper(t, ctx)
	localNetwork, publicNetwork, publicExceptionNetwork, ambiguousNetwork, localAddresses, publicAddresses, ambiguousAddress, publicExceptionAddress, dnsName := createNetworkPolicyIntegrationPeers(t, ctx, image, helper)
	directResolver := createNetworkPolicyDirectResolver(t, ctx, image, helper)

	for _, test := range []struct {
		name   string
		public blueprint.NetworkAccess
		local  blueprint.NetworkAccess
		want   bool
	}{
		{name: "deny both", public: blueprint.NetworkAccessDeny, local: blueprint.NetworkAccessDeny},
		{name: "public only", public: blueprint.NetworkAccessAllow, local: blueprint.NetworkAccessDeny, want: true},
		{name: "local only", public: blueprint.NetworkAccessDeny, local: blueprint.NetworkAccessAllow, want: true},
		{name: "allow both", public: blueprint.NetworkAccessAllow, local: blueprint.NetworkAccessAllow, want: true},
	} {
		for _, transport := range []string{"udp", "tcp"} {
			t.Run("engine-selected DNS "+transport+" "+test.name, func(t *testing.T) {
				plan := networkPolicyIntegrationPlan(t, image, helper, test.public, test.local, blueprint.AmbiguousNetworkAccessRequireBoth)
				command := ResolvedEnvironmentCommand{Argv: []string{"/network-test", "dns", "reploy.test", strconv.FormatBool(test.want), transport}}
				execution, err := PlanTransientContainerExecutionV1(plan, command, nil, "run-0000000000000001", false, false)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = exec.CommandContext(context.Background(), "docker", execution.Cleanup.Args...).Run() })
				runDockerIntegration(t, ctx, dockerCreateWithDNSV1(t, execution.Create.Args, net.JoinHostPort(directResolver, "53"))...)
				output := runDockerIntegration(t, ctx, execution.Start.Args...)
				if !strings.Contains(output, "DNS_PASS") {
					t.Fatalf("direct local DNS output = %q", output)
				}
			})
		}
	}

	t.Run("public-only default DNS profile", func(t *testing.T) {
		plan := networkPolicyIntegrationPlan(t, image, helper, blueprint.NetworkAccessAllow, blueprint.NetworkAccessDeny, blueprint.AmbiguousNetworkAccessRequireBoth)
		command := ResolvedEnvironmentCommand{Argv: []string{"/network-test", "dns", "example.com", "true", "udp"}}
		execution, err := PlanTransientContainerExecutionV1(plan, command, nil, "run-0000000000000001", false, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = exec.CommandContext(context.Background(), "docker", execution.Cleanup.Args...).Run() })
		runDockerIntegration(t, ctx, execution.Create.Args...)
		output := runDockerIntegration(t, ctx, execution.Start.Args...)
		if !strings.Contains(output, "DNS_PASS") {
			t.Fatalf("public-only default DNS output = %q", output)
		}
	})

	for _, test := range []struct {
		name          string
		public        blueprint.NetworkAccess
		local         blueprint.NetworkAccess
		ambiguous     blueprint.AmbiguousNetworkAccess
		wantPublic    bool
		wantLocal     bool
		wantAmbiguous bool
	}{
		{name: "deny-deny", public: blueprint.NetworkAccessDeny, local: blueprint.NetworkAccessDeny, ambiguous: blueprint.AmbiguousNetworkAccessRequireBoth},
		{name: "allow-deny", public: blueprint.NetworkAccessAllow, local: blueprint.NetworkAccessDeny, ambiguous: blueprint.AmbiguousNetworkAccessRequireBoth, wantPublic: true},
		{name: "deny-allow", public: blueprint.NetworkAccessDeny, local: blueprint.NetworkAccessAllow, ambiguous: blueprint.AmbiguousNetworkAccessRequireBoth, wantLocal: true},
		{name: "ambiguous-escape-hatch", public: blueprint.NetworkAccessDeny, local: blueprint.NetworkAccessDeny, ambiguous: blueprint.AmbiguousNetworkAccessAllow, wantAmbiguous: true},
		{name: "allow-allow", public: blueprint.NetworkAccessAllow, local: blueprint.NetworkAccessAllow, ambiguous: blueprint.AmbiguousNetworkAccessRequireBoth, wantPublic: true, wantLocal: true, wantAmbiguous: true},
	} {
		t.Run("transient "+test.name, func(t *testing.T) {
			plan := networkPolicyIntegrationPlan(t, image, helper, test.public, test.local, test.ambiguous)
			command := ResolvedEnvironmentCommand{Argv: networkPolicyIntegrationWorkloadArgv(
				plan.Sandbox.RuntimeUser.UID, localAddresses, publicAddresses, ambiguousAddress, publicExceptionAddress,
				test.wantLocal, test.wantPublic, test.wantAmbiguous, test.wantPublic, false, dnsName,
			)}
			execution, err := PlanTransientContainerExecutionV1(plan, command, nil, "run-0000000000000001", false, false)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = exec.CommandContext(context.Background(), "docker", execution.Cleanup.Args...).Run() })
			runDockerIntegration(t, ctx, dockerCreateWithDNSV1(t, execution.Create.Args, localAddresses[0])...)
			runDockerIntegration(t, ctx, "network", "connect", localNetwork, execution.Container)
			runDockerIntegration(t, ctx, "network", "connect", publicNetwork, execution.Container)
			runDockerIntegration(t, ctx, "network", "connect", publicExceptionNetwork, execution.Container)
			runDockerIntegration(t, ctx, "network", "connect", ambiguousNetwork, execution.Container)
			output := runDockerIntegration(t, ctx, execution.Start.Args...)
			if !strings.Contains(output, "NETWORK_POLICY_PASS") {
				t.Fatalf("transient network-policy output = %q", output)
			}
		})
	}

	t.Run("transient root deny-deny", func(t *testing.T) {
		plan := networkPolicyIntegrationPlan(t, image, helper, blueprint.NetworkAccessDeny, blueprint.NetworkAccessDeny, blueprint.AmbiguousNetworkAccessRequireBoth)
		plan.Sandbox = newApplicationSandboxPlanWithNetworkV1(
			RuntimeUserPlan{UID: 0, GID: 0, DockerUser: "0:0"},
			blueprint.RuntimeNetwork{Public: blueprint.NetworkAccessDeny, Local: blueprint.NetworkAccessDeny, Ambiguous: blueprint.AmbiguousNetworkAccessRequireBoth},
		)
		command := ResolvedEnvironmentCommand{Argv: networkPolicyIntegrationWorkloadArgv(
			0, localAddresses, publicAddresses, ambiguousAddress, publicExceptionAddress, false, false, false, false, false, dnsName,
		)}
		execution, err := PlanTransientContainerExecutionV1(plan, command, nil, "run-0000000000000001", false, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = exec.CommandContext(context.Background(), "docker", execution.Cleanup.Args...).Run() })
		runDockerIntegration(t, ctx, dockerCreateWithDNSV1(t, execution.Create.Args, localAddresses[0])...)
		runDockerIntegration(t, ctx, "network", "connect", localNetwork, execution.Container)
		runDockerIntegration(t, ctx, "network", "connect", publicNetwork, execution.Container)
		runDockerIntegration(t, ctx, "network", "connect", publicExceptionNetwork, execution.Container)
		runDockerIntegration(t, ctx, "network", "connect", ambiguousNetwork, execution.Container)
		output := runDockerIntegration(t, ctx, execution.Start.Args...)
		if !strings.Contains(output, "NETWORK_POLICY_PASS") {
			t.Fatalf("root network-policy output = %q", output)
		}
	})

	for _, test := range []struct {
		name       string
		public     blueprint.NetworkAccess
		local      blueprint.NetworkAccess
		wantPublic bool
		wantLocal  bool
	}{
		{name: "deny-deny", public: blueprint.NetworkAccessDeny, local: blueprint.NetworkAccessDeny},
		{name: "allow-deny", public: blueprint.NetworkAccessAllow, local: blueprint.NetworkAccessDeny, wantPublic: true},
		{name: "deny-allow", public: blueprint.NetworkAccessDeny, local: blueprint.NetworkAccessAllow, wantLocal: true},
		{name: "allow-allow", public: blueprint.NetworkAccessAllow, local: blueprint.NetworkAccessAllow, wantPublic: true, wantLocal: true},
	} {
		t.Run("persistent "+test.name, func(t *testing.T) {
			plan := networkPolicyIntegrationPlan(t, image, helper, test.public, test.local, blueprint.AmbiguousNetworkAccessRequireBoth)
			plan.Workload = &WorkloadExecutionPlan{
				Argv: networkPolicyIntegrationWorkloadArgv(
					plan.Sandbox.RuntimeUser.UID, localAddresses, publicAddresses, ambiguousAddress, publicExceptionAddress,
					test.wantLocal, test.wantPublic, test.wantLocal && test.wantPublic, test.wantPublic, false, dnsName,
				),
				Endpoints: map[string]EndpointExecutionPlan{},
			}
			rendered, err := RenderDockerInputs(plan, "network-policy")
			if err != nil {
				t.Fatal(err)
			}
			composePath := filepath.Join(t.TempDir(), "compose.yaml")
			if err := os.WriteFile(composePath, rendered.Compose, 0o600); err != nil {
				t.Fatal(err)
			}
			composeArgs := []string{"compose", "--project-name", plan.NetworkName, "-f", composePath}
			t.Cleanup(func() {
				args := append(append([]string(nil), composeArgs...), "down", "--remove-orphans")
				_ = exec.CommandContext(context.Background(), "docker", args...).Run()
			})
			runDockerIntegration(t, ctx, append(composeArgs, "create", "--pull", "never")...)
			runDockerIntegration(t, ctx, "network", "connect", localNetwork, plan.ContainerName)
			runDockerIntegration(t, ctx, "network", "connect", publicNetwork, plan.ContainerName)
			runDockerIntegration(t, ctx, "network", "connect", publicExceptionNetwork, plan.ContainerName)
			runDockerIntegration(t, ctx, "network", "connect", ambiguousNetwork, plan.ContainerName)
			runDockerIntegration(t, ctx, append(composeArgs, "start")...)
			runDockerIntegration(t, ctx, "wait", plan.ContainerName)
			logs := runDockerIntegration(t, ctx, append(composeArgs, "logs")...)
			if !strings.Contains(logs, "NETWORK_POLICY_PASS") {
				t.Fatalf("persistent network-policy logs = %q", logs)
			}
		})
	}

	t.Run("persistent endpoint survives default denial", func(t *testing.T) {
		plan := networkPolicyIntegrationPlan(t, image, helper, blueprint.NetworkAccessDeny, blueprint.NetworkAccessDeny, blueprint.AmbiguousNetworkAccessRequireBoth)
		plan.Workload = &WorkloadExecutionPlan{
			Argv: networkPolicyIntegrationWorkloadArgv(plan.Sandbox.RuntimeUser.UID, localAddresses, publicAddresses, ambiguousAddress, publicExceptionAddress, false, false, false, false, true, dnsName),
			Endpoints: map[string]EndpointExecutionPlan{
				"http": {Scheme: "http", PublishAddress: "127.0.0.1", PublishedPort: reserveNetworkPolicyIntegrationPort(t), ContainerPort: 8080},
			},
		}
		rendered, err := RenderDockerInputs(plan, "network-policy")
		if err != nil {
			t.Fatal(err)
		}
		composePath := filepath.Join(t.TempDir(), "compose.yaml")
		if err := os.WriteFile(composePath, rendered.Compose, 0o600); err != nil {
			t.Fatal(err)
		}
		composeArgs := []string{"compose", "--project-name", plan.NetworkName, "-f", composePath}
		t.Cleanup(func() {
			args := append(append([]string(nil), composeArgs...), "down", "--remove-orphans")
			_ = exec.CommandContext(context.Background(), "docker", args...).Run()
		})
		runDockerIntegration(t, ctx, append(composeArgs, "create", "--pull", "never")...)
		runDockerIntegration(t, ctx, "network", "connect", localNetwork, plan.ContainerName)
		runDockerIntegration(t, ctx, "network", "connect", publicNetwork, plan.ContainerName)
		runDockerIntegration(t, ctx, "network", "connect", publicExceptionNetwork, plan.ContainerName)
		runDockerIntegration(t, ctx, "network", "connect", ambiguousNetwork, plan.ContainerName)
		runDockerIntegration(t, ctx, append(composeArgs, "start")...)
		url := fmt.Sprintf("http://127.0.0.1:%d/", plan.Workload.Endpoints["http"].PublishedPort)
		waitNetworkPolicyIntegrationEndpoint(t, url, func() string { return runDockerIntegration(t, ctx, append(composeArgs, "logs")...) })
		runDockerIntegration(t, ctx, "restart", plan.ContainerName)
		waitNetworkPolicyIntegrationEndpoint(t, url, func() string { return runDockerIntegration(t, ctx, append(composeArgs, "logs")...) })
		logs := runDockerIntegration(t, ctx, append(composeArgs, "logs")...)
		if !strings.Contains(logs, "NETWORK_POLICY_PASS") {
			t.Fatalf("persistent network-policy logs = %q", logs)
		}
	})

	t.Run("persistent unrestricted egress still limits inbound", func(t *testing.T) {
		plan := networkPolicyIntegrationPlan(t, image, helper, blueprint.NetworkAccessAllow, blueprint.NetworkAccessAllow, blueprint.AmbiguousNetworkAccessRequireBoth)
		plan.Workload = &WorkloadExecutionPlan{
			Argv: networkPolicyIntegrationWorkloadArgv(plan.Sandbox.RuntimeUser.UID, localAddresses, publicAddresses, ambiguousAddress, publicExceptionAddress, true, true, true, true, true, dnsName),
			Endpoints: map[string]EndpointExecutionPlan{
				"http": {Scheme: "http", PublishAddress: "127.0.0.1", PublishedPort: reserveNetworkPolicyIntegrationPort(t), ContainerPort: 8080},
			},
		}
		rendered, err := RenderDockerInputs(plan, "network-policy")
		if err != nil {
			t.Fatal(err)
		}
		composePath := filepath.Join(t.TempDir(), "compose.yaml")
		if err := os.WriteFile(composePath, rendered.Compose, 0o600); err != nil {
			t.Fatal(err)
		}
		composeArgs := []string{"compose", "--project-name", plan.NetworkName, "-f", composePath}
		t.Cleanup(func() {
			args := append(append([]string(nil), composeArgs...), "down", "--remove-orphans")
			_ = exec.CommandContext(context.Background(), "docker", args...).Run()
		})
		runDockerIntegration(t, ctx, append(composeArgs, "create", "--pull", "never")...)
		runDockerIntegration(t, ctx, "network", "connect", localNetwork, plan.ContainerName)
		runDockerIntegration(t, ctx, "network", "connect", publicNetwork, plan.ContainerName)
		runDockerIntegration(t, ctx, "network", "connect", publicExceptionNetwork, plan.ContainerName)
		runDockerIntegration(t, ctx, "network", "connect", ambiguousNetwork, plan.ContainerName)
		runDockerIntegration(t, ctx, append(composeArgs, "start")...)
		url := fmt.Sprintf("http://127.0.0.1:%d/", plan.Workload.Endpoints["http"].PublishedPort)
		waitNetworkPolicyIntegrationEndpoint(t, url, func() string { return runDockerIntegration(t, ctx, append(composeArgs, "logs")...) })
		runDockerIntegration(t, ctx, "network", "connect", plan.NetworkName, dnsName)
		t.Cleanup(func() {
			_ = exec.CommandContext(context.Background(), "docker", "network", "disconnect", "--force", plan.NetworkName, dnsName).Run()
		})
		runDockerIntegration(t, ctx, "exec", dnsName, "/network-test", "dial", plan.ContainerName+":8080", "true")
		runDockerIntegration(t, ctx, "exec", dnsName, "/network-test", "dial", plan.ContainerName+":8081", "false")
	})
}

func dockerCreateWithDNSV1(t *testing.T, args []string, resolver string) []string {
	t.Helper()
	host, _, err := net.SplitHostPort(resolver)
	if err != nil {
		t.Fatalf("split integration resolver %q: %v", resolver, err)
	}
	entrypoint := -1
	for index, argument := range args {
		if argument == "--entrypoint" {
			entrypoint = index
			break
		}
	}
	if entrypoint < 0 {
		t.Fatal("transient Docker create command has no entrypoint boundary")
	}
	result := make([]string, 0, len(args)+2)
	for index := 0; index < entrypoint; index++ {
		if args[index] == "--dns" {
			if index+1 >= entrypoint {
				t.Fatal("transient Docker create command has an incomplete DNS option")
			}
			index++
			continue
		}
		result = append(result, args[index])
	}
	result = append(result, "--dns", host)
	return append(result, args[entrypoint:]...)
}

func waitNetworkPolicyIntegrationEndpoint(t *testing.T, url string, logs func() string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var requestErr error
	for {
		response, err := http.Get(url)
		requestErr = err
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("persistent endpoint did not become ready: %v\n%s", requestErr, logs())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func networkPolicyIntegrationPlan(t *testing.T, image string, helper string, public blueprint.NetworkAccess, local blueprint.NetworkAccess, ambiguous blueprint.AmbiguousNetworkAccess) DockerExecutionPlan {
	t.Helper()
	return DockerExecutionPlan{
		EnvironmentID: "network-policy", DeploymentDir: t.TempDir(), Phase: blueprint.PhaseStaged,
		Image: image, ContainerName: uniqueDockerIntegrationName("reploy-network-policy"),
		NetworkName: uniqueDockerIntegrationName("reploy-network-policy-network"),
		Sandbox: newApplicationSandboxPlanWithNetworkV1(
			RuntimeUserPlan{UID: 12345, GID: 23456, DockerUser: "12345:23456"},
			blueprint.RuntimeNetwork{Public: public, Local: local, Ambiguous: ambiguous},
		),
		Mounts: []MountExecutionPlan{{
			Name: "network-test", Mode: blueprint.MountBind, Source: helper,
			SourceKind: "file", Target: "/network-test", ReadOnly: true,
		}},
	}
}

func networkPolicyIntegrationWorkloadArgv(uid uint32, local []string, public []string, ambiguous string, publicException string, wantLocal bool, wantPublic bool, wantAmbiguous bool, wantPublicException bool, serve bool, dnsName string) []string {
	mode := "exit"
	if serve {
		mode = "serve"
	}
	return []string{
		"/network-test", "workload", runtimeIDStringV1(uid),
		local[0], local[1], public[0], public[1], ambiguous, publicException,
		strconv.FormatBool(wantLocal), strconv.FormatBool(wantPublic), strconv.FormatBool(wantAmbiguous), strconv.FormatBool(wantPublicException), mode,
		dnsName, strconv.FormatBool(wantPublic || wantLocal),
	}
}

func buildNetworkPolicyIntegrationHelper(t *testing.T, ctx context.Context) string {
	t.Helper()
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("network-policy Docker integration does not build a helper for %s", runtime.GOARCH)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dockerIntegrationSharedTempDir(t), "network-policy-helper")
	command := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", output, "./internal/dockerdeploy/testdata/network_policy_helper")
	command.Dir = root
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH, "GOCACHE="+filepath.Join(t.TempDir(), "go-cache"))
	if content, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build network-policy integration helper: %v\n%s", err, content)
	}
	return output
}

func createNetworkPolicyIntegrationPeers(t *testing.T, ctx context.Context, image string, helper string) (string, string, string, string, []string, []string, string, string, string) {
	t.Helper()
	seed := int(time.Now().UnixNano()%180) + 40
	localNetwork := uniqueDockerIntegrationName("reploy-network-local")
	publicNetwork := uniqueDockerIntegrationName("reploy-network-public")
	publicExceptionNetwork := uniqueDockerIntegrationName("reploy-network-public-exception")
	ambiguousNetwork := uniqueDockerIntegrationName("reploy-network-ambiguous")
	localV4 := fmt.Sprintf("172.30.%d.3", seed)
	localV6 := fmt.Sprintf("fd30:%x::3", seed)
	publicV4 := fmt.Sprintf("11.%d.0.3", seed)
	publicV6 := fmt.Sprintf("2001:4860:%x::3", seed)
	publicExceptionV6 := fmt.Sprintf("2001:20:%x::3", seed)
	ambiguousV4 := fmt.Sprintf("172.29.%d.3", seed)
	ambiguousV6 := fmt.Sprintf("64:ff9b:1:%x::3", seed)
	runDockerIntegration(t, ctx, "network", "create", "--ipv6", "--subnet", fmt.Sprintf("172.30.%d.0/24", seed), "--subnet", fmt.Sprintf("fd30:%x::/64", seed), localNetwork)
	runDockerIntegration(t, ctx, "network", "create", "--ipv6", "--subnet", fmt.Sprintf("11.%d.0.0/24", seed), "--subnet", fmt.Sprintf("2001:4860:%x::/64", seed), publicNetwork)
	runDockerIntegration(t, ctx, "network", "create", "--ipv6", "--subnet", fmt.Sprintf("172.28.%d.0/24", seed), "--subnet", fmt.Sprintf("2001:20:%x::/64", seed), publicExceptionNetwork)
	runDockerIntegration(t, ctx, "network", "create", "--ipv6", "--subnet", fmt.Sprintf("172.29.%d.0/24", seed), "--subnet", fmt.Sprintf("64:ff9b:1:%x::/64", seed), ambiguousNetwork)
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "docker", "network", "rm", localNetwork, publicNetwork, publicExceptionNetwork, ambiguousNetwork).Run()
	})
	publicPeerName := ""
	for _, peer := range []struct {
		name    string
		network string
		ipv4    string
		ipv6    string
	}{
		{name: uniqueDockerIntegrationName("reploy-network-local-peer"), network: localNetwork, ipv4: localV4, ipv6: localV6},
		{name: uniqueDockerIntegrationName("reploy-network-public-peer"), network: publicNetwork, ipv4: publicV4, ipv6: publicV6},
		{name: uniqueDockerIntegrationName("reploy-network-public-exception-peer"), network: publicExceptionNetwork, ipv6: publicExceptionV6},
		{name: uniqueDockerIntegrationName("reploy-network-ambiguous-peer"), network: ambiguousNetwork, ipv4: ambiguousV4, ipv6: ambiguousV6},
	} {
		if peer.network == publicNetwork && publicPeerName == "" {
			publicPeerName = peer.name
		}
		args := []string{
			"run", "--detach", "--pull", "never", "--name", peer.name,
			"--network", peer.network,
		}
		if peer.ipv4 != "" {
			args = append(args, "--ip", peer.ipv4)
		}
		args = append(args,
			"--ip6", peer.ipv6,
			"--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true",
			"--mount", "type=bind,source="+helper+",target=/network-test,readonly",
			"--entrypoint", "/network-test", image, "peer",
		)
		runDockerIntegration(t, ctx, args...)
		t.Cleanup(func() {
			_ = exec.CommandContext(context.Background(), "docker", "container", "rm", "--force", peer.name).Run()
		})
	}
	time.Sleep(250 * time.Millisecond)
	return localNetwork, publicNetwork, publicExceptionNetwork, ambiguousNetwork,
		[]string{localV4 + ":9090", "[" + localV6 + "]:9090"},
		[]string{publicV4 + ":9090", "[" + publicV6 + "]:9090"}, "[" + ambiguousV6 + "]:9090", "[" + publicExceptionV6 + "]:9090", publicPeerName
}

func createNetworkPolicyDirectResolver(t *testing.T, ctx context.Context, image string, helper string) string {
	t.Helper()
	name := uniqueDockerIntegrationName("reploy-network-direct-dns")
	runDockerIntegration(t, ctx,
		"run", "--detach", "--name", name, "--network", "bridge",
		"--mount", "type=bind,source="+helper+",target=/network-test,readonly",
		"--entrypoint", "/network-test", image, "peer",
	)
	t.Cleanup(func() { _ = exec.CommandContext(context.Background(), "docker", "rm", "--force", name).Run() })
	address := strings.TrimSpace(runDockerIntegration(t, ctx, "inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", name))
	if net.ParseIP(address) == nil {
		t.Fatalf("direct DNS peer address = %q", address)
	}
	return address
}

func reserveNetworkPolicyIntegrationPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
