package deploy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func TestPublishedAddressOverridesStrictRoundTrip(t *testing.T) {
	source := `environment:
  id: demo
  vars: {note: retained}
  base: {image: 'python:3.13-slim'}
  package_additions: {os: [curl]}
  package_overrides: {python: {demo: {version: '1.2.3'}}}
docker:
  workload:
    endpoints:
      https:
        publish: {address: 0.0.0.0}
`
	raw, err := DecodePackageOverridesV1([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodePackageOverridesV1(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, err := DecodePackageOverridesV1(encoded)
	if err != nil || !reflect.DeepEqual(raw, again) {
		t.Fatalf("round trip = %#v, %v", again, err)
	}
	resolved, err := ResolvePackageOverridesV1(raw, t.TempDir(), func(_, id string, _ PackageOverrideChoiceV1) (string, error) { return id, nil })
	if err != nil {
		t.Fatal(err)
	}
	intent, err := resolved.Intent()
	if err != nil || intent.PublishedAddresses["https"] != "0.0.0.0" || len(intent.Choices) != 1 {
		t.Fatalf("intent=%#v error=%v", intent, err)
	}
	originalDigest, err := PackageOverridesDigestV1(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw.Docker.Workload.Endpoints["https"] = DockerEndpointOverrideV1{Publish: DockerPublicationOverrideV1{Address: "127.0.0.1"}}
	changedDigest, err := PackageOverridesDigestV1(raw)
	if err != nil || originalDigest == changedDigest {
		t.Fatalf("address edit did not change input digest: %v", err)
	}
	for _, bad := range []string{
		strings.Replace(source, "address: 0.0.0.0", "address: ''", 1),
		strings.Replace(source, "address: 0.0.0.0", "address: null", 1),
		strings.Replace(source, "address: 0.0.0.0", "address: 127.0.0.1:8075", 1),
		strings.Replace(source, "address: 0.0.0.0", "address: http://localhost", 1),
		strings.Replace(source, "address: 0.0.0.0", "address: 0.0.0.0, port: 123", 1),
		strings.Replace(source, "publish: {address: 0.0.0.0}", "bind: {address: 0.0.0.0}", 1),
		"environment: {id: demo}\ndocker: null\n",
		"environment: {id: demo}\ndocker: {workload: {endpoints: {https: null}}}\n",
		"environment: {id: demo, package_overrides: null}\ndocker: {workload: {endpoints: {https: {publish: {address: 0.0.0.0}}}}}\n",
	} {
		if _, err := DecodePackageOverridesV1([]byte(bad)); err == nil {
			t.Fatalf("accepted invalid override:\n%s", bad)
		}
	}
	minimal := []byte("environment: {id: demo}\ndocker: {workload: {endpoints: {https: {publish: {address: 0.0.0.0}}}}}\n")
	if _, err := DecodePackageOverridesV1(minimal); err != nil {
		t.Fatal(err)
	}
	empty, _ := EncodePackageOverridesV1(EmptyPackageOverridesV1("demo"))
	if strings.Contains(string(empty), "docker:") {
		t.Fatalf("absent override changed encoding: %s", empty)
	}
}

func TestPublishedAddressOverridesDeclarationAndPreservation(t *testing.T) {
	document := blueprint.Document{Environment: blueprint.Environment{ID: "demo"}, Docker: blueprint.Docker{Workload: &blueprint.DockerWorkload{Endpoints: map[string]blueprint.DockerEndpoint{
		"https":   {Bind: blueprint.Bind{Address: "0.0.0.0"}, Publish: blueprint.Publication{Address: "127.0.0.1", Staging: 18075, Deployed: 8075}, Endpoint: blueprint.Endpoint{Scheme: "https", Port: 8075}},
		"metrics": {Publish: blueprint.Publication{Address: "127.0.0.2", Staging: 19090, Deployed: 9090}},
	}}}}
	before := document.Docker.Workload.Endpoints["https"]
	for _, address := range []string{"127.0.0.1", "0.0.0.0", "192.0.2.42", "::", "[::]", "::1", "[2001:db8::42]"} {
		effective, err := ResolvePublishedAddressesV1(document, map[string]string{"https": address})
		if err != nil {
			t.Fatal(err)
		}
		if effective["https"] != address || effective["metrics"] != "127.0.0.2" || !reflect.DeepEqual(document.Docker.Workload.Endpoints["https"], before) {
			t.Fatalf("effective=%v document mutated", effective)
		}
	}
	for _, address := range []string{"*", "", " ", "0.0.0.0 ", "https://example.com", "[127.0.0.1]", "::1:port", "[::1]:80", "256.2.3.4", "bad host", "127.0.0.1\n", "0.0.0.0/0"} {
		if ValidatePublishedAddressV1(address) == nil {
			t.Fatalf("accepted %q", address)
		}
	}
	raw := EmptyPackageOverridesV1("demo")
	raw.Docker.Workload.Endpoints = map[string]DockerEndpointOverrideV1{"missing": {Publish: DockerPublicationOverrideV1{Address: "0.0.0.0"}}}
	if err := raw.CheckDeclarationsV1(document); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("missing endpoint: %v", err)
	}
	raw.Environment.ID = "other"
	if err := raw.CheckDeclarationsV1(document); err == nil {
		t.Fatal("accepted mismatched environment")
	}
	effective, err := ResolvePublishedAddressesV1(document, nil)
	if err != nil || effective["https"] != "127.0.0.1" {
		t.Fatalf("reset=%v %v", effective, err)
	}
}
