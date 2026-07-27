package dockerdeploy

import (
	"context"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
)

func TestValidatePythonConsumerReusesOneSessionProbe(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	response := probe.ResponseV1{Schema: probe.ResponseSchemaV1, Observations: []probe.ExecutableObservationV1{
		pythonConsumerObservation(pythonCarrierRequirementID, pythonCarrierPath),
		pythonConsumerObservation(pythonLauncherRequirementID, pythonLauncherPath),
	}}
	commands, _ := stubPythonResolverCommands(t, mustCanonicalProbeResponse(t, response), nil, nil)
	session, err := OpenPythonResolverSession(context.Background(), descriptor, workspace, testPreparedPythonResolverArtifacts(t))
	if err != nil {
		t.Fatal(err)
	}
	config := pythonConsumerTestImageConfig()
	first, err := ValidatePythonConsumer(context.Background(), session, config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ValidatePythonConsumer(context.Background(), session, config)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("first = %#v; second = %#v", first, second)
	}
	if first.Carrier.Evidence.InvocationPath != pythonCarrierPath || first.EnvironmentLauncher.Evidence.InvocationPath != pythonLauncherPath || !reflect.DeepEqual(first.FinalImageConfig, config) {
		t.Fatalf("validation = %#v", first)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(*commands) != 4 {
		t.Fatalf("consumer validation used another exec: %#v", *commands)
	}
}

func pythonConsumerObservation(id string, path string) probe.ExecutableObservationV1 {
	_, response := pythonResolverProbeExchange()
	observation := response.Observations[0]
	observation.ID = id
	observation.InvocationPath = path
	observation.Terminal.Path = path
	observation.Access[len(observation.Access)-1].Path = path
	if path == "/bin/sh" {
		observation.Access = []probe.AccessObservationV1{
			{Path: "/", Kind: "directory", Mode: "0755", UID: "0", GID: "0"},
			{Path: "/bin", Kind: "directory", Mode: "0755", UID: "0", GID: "0"},
			{Path: path, Kind: "regular", Mode: "0755", UID: "0", GID: "0"},
		}
	}
	return observation
}

func pythonConsumerTestImageConfig() providers.ImageConfigPolicy {
	return providers.ImageConfigPolicy{
		User: "0:0", WorkingDir: "/", Environment: []providers.EnvironmentVariable{},
		Entrypoint: []string{}, Command: []string{}, Healthcheck: providers.ImageHealthcheckNone,
		StopSignal: "SIGTERM", Labels: []providers.ImageLabel{},
	}
}
