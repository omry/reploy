package dockerdeploy

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func publishedAddressDocument() blueprint.Document {
	return blueprint.Document{Environment: blueprint.Environment{ID: "demo", Workload: &blueprint.Workload{Command: "server"}}, Docker: blueprint.Docker{Workload: &blueprint.DockerWorkload{Endpoints: map[string]blueprint.DockerEndpoint{
		"https":   {Bind: blueprint.Bind{Address: "0.0.0.0"}, Publish: blueprint.Publication{Address: "127.0.0.1", Staging: 18075, Deployed: 8075}, Endpoint: blueprint.Endpoint{Scheme: "https", Port: 8075}},
		"metrics": {Publish: blueprint.Publication{Address: "127.0.0.2", Staging: 19090, Deployed: 9090}, Endpoint: blueprint.Endpoint{Scheme: "http", Port: 9090}},
	}}}}
}

func TestPublishedAddressesShareStagedInstalledRenderingAndStatus(t *testing.T) {
	document := publishedAddressDocument()
	for _, phase := range []blueprint.Phase{blueprint.PhaseStaged, blueprint.PhaseInstalled} {
		t.Run(string(phase), func(t *testing.T) {
			scope := blueprint.InstallScopeUser
			context := DockerPlanContext{DeploymentDir: t.TempDir(), GeneratedImage: "image", Phase: phase, UID: 1000, GID: 1000, PublishedAddresses: map[string]string{"https": "0.0.0.0"}}
			port := 18075
			if phase == blueprint.PhaseInstalled {
				context.Scope = &scope
				context.InstallTarget = t.TempDir()
				port = 8075
			}
			plan, err := PlanDockerExecution(document, context)
			if err != nil {
				t.Fatal(err)
			}
			endpoint := plan.Workload.Endpoints["https"]
			if endpoint.PublishAddress != "0.0.0.0" || endpoint.ProbeHost != "127.0.0.1" || endpoint.PublishedPort != port || endpoint.ContainerPort != 8075 || endpoint.BindAddress != "0.0.0.0" || plan.Workload.Endpoints["metrics"].PublishAddress != "127.0.0.2" {
				t.Fatalf("plan=%#v", plan)
			}
			plan.Workload.Argv = []string{"server"}
			rendered, err := RenderDockerInputs(plan, "appctl")
			if err != nil {
				t.Fatal(err)
			}
			if rendered.Environment["REPLOY_ENDPOINT_HTTPS_HOST"] != "0.0.0.0" || !strings.Contains(string(rendered.Compose), "0.0.0.0:") {
				t.Fatalf("rendered=%#v", rendered)
			}
			var status bytes.Buffer
			if err := WriteRuntimeStatusV1(&status, RuntimeStatusV1{Status: "up"}, plan); err != nil || !strings.Contains(status.String(), "https://0.0.0.0:") {
				t.Fatalf("status=%s %v", status.String(), err)
			}
			ports := installationPortsForDockerPlanV1(plan)
			if ports[0].HostBind != "0.0.0.0" {
				t.Fatalf("installation bindings=%#v", ports)
			}
			plain := context
			plain.PublishedAddresses = nil
			original, err := PlanDockerExecution(document, plain)
			if err != nil {
				t.Fatal(err)
			}
			context.PublishedAddresses = map[string]string{"https": "127.0.0.1"}
			equal, err := PlanDockerExecution(document, context)
			if err != nil || !reflect.DeepEqual(equal, original) {
				t.Fatalf("explicit default differs: %v", err)
			}
		})
	}
}

func TestPublishedAddressesCurrentRuntimeIgnoresSidecarUntilFreshLaunch(t *testing.T) {
	current, _ := runtimeCurrentBuildFixture(t)
	dir := t.TempDir()
	input := CurrentRuntimePlanInputV1{DeploymentDir: dir, Current: current, Runtime: blueprintRuntimeFixtureV1()}
	recorded, err := PlanCurrentRuntimeV1(input)
	if err != nil {
		t.Fatal(err)
	}
	raw := deploy.EmptyPackageOverridesV1("demo")
	raw.Docker.Workload.Endpoints = map[string]deploy.DockerEndpointOverrideV1{"missing": {Publish: deploy.DockerPublicationOverrideV1{Address: "0.0.0.0"}}}
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.CommitPackageOverridesV1(raw); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	observed, err := PlanCurrentRuntimeV1(input)
	if err != nil || !reflect.DeepEqual(observed, recorded) {
		t.Fatalf("sidecar edit changed the recorded runtime: %v", err)
	}
	input.RequireCurrentPublishedAddresses = true
	if _, err := PlanCurrentRuntimeV1(input); err == nil {
		t.Fatal("fresh launch accepted an unknown overridden endpoint")
	}
}

func TestPublishedAddressesInvalidateBuildReuse(t *testing.T) {
	current, input := currentBuildReuseFixture(t)
	input.PackageOverrides.PublishedAddresses = map[string]string{"https": "0.0.0.0"}
	matches, err := CurrentBuildMatches(current, input)
	if err != nil || matches {
		t.Fatalf("changed address reused old generation: matches=%v err=%v", matches, err)
	}
}

func TestPublishedAddressesIPv6StatusAndEnvironment(t *testing.T) {
	for _, address := range []string{"::", "[::]", "::1", "[::1]"} {
		document := publishedAddressDocument()
		plan, err := PlanDockerExecution(document, DockerPlanContext{DeploymentDir: t.TempDir(), Phase: blueprint.PhaseStaged, GeneratedImage: "image", UID: 1000, GID: 1000, PublishedAddresses: map[string]string{"https": address}})
		if err != nil {
			t.Fatal(err)
		}
		plan.Workload.Argv = []string{"server"}
		rendered, err := RenderDockerInputs(plan, "appctl")
		if err != nil {
			t.Fatal(err)
		}
		if rendered.Environment["REPLOY_ENDPOINT_HTTPS_HOST"] != address || strings.Contains(strings.Join(rendered.Status.Endpoints, " "), "[[") {
			t.Fatalf("IPv6 render=%#v", rendered)
		}
		var status bytes.Buffer
		if err := WriteRuntimeStatusV1(&status, RuntimeStatusV1{Status: "up"}, plan); err != nil || strings.Contains(status.String(), "[[") || !strings.Contains(status.String(), "]:18075") {
			t.Fatalf("IPv6 status=%s %v", status.String(), err)
		}
		if plan.Workload.Endpoints["https"].ProbeHost != "::1" {
			t.Fatal("IPv6 probe normalization changed")
		}
	}
}
