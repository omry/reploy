package python

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestWheelResolverArgvUsesOnePipClosureWithOptionalSourceConstraints(t *testing.T) {
	requirement, err := CanonicalPackageRequestV1("demo>=1")
	if err != nil {
		t.Fatal(err)
	}
	request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: "application", Interpreter: blueprint.CommandRequirement{Command: "python"},
		Requirements: []providers.CanonicalPackageRequest{requirement},
		Overrides:    []PythonPackageOverrideV1{{Distribution: "local-demo", Kind: "local"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	wheel := providerstore.ArtifactDescriptor{LogicalPath: "wheels/local_demo-2-py3-none-any.whl", Kind: "wheel", Size: "10", SHA256: digest}
	source := testPythonSourceInput(
		"application", "local-demo", "2", canonical.Digest("sha256:"+strings.Repeat("b", 64)), digest,
	)
	got, err := WheelResolverArgv("/usr/bin/python3", request, []providers.ResolvedSourceInput{source}, []providerstore.ArtifactDescriptor{wheel})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/usr/bin/python3", "-m", "pip", "--disable-pip-version-check", "wheel", "--no-cache-dir",
		"--progress-bar", "off", "--find-links", ResolverInputDirectory,
		"--wheel-dir", ResolverOutputDirectory, "--constraint", ResolverSourceConstraintsPath, "demo>=1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
	constraints, err := WheelResolverSourceConstraints(request, []providers.ResolvedSourceInput{source}, []providerstore.ArtifactDescriptor{wheel})
	if err != nil {
		t.Fatal(err)
	}
	wantConstraints := "local-demo @ file:///.reploy-resolver/input/local_demo-2-py3-none-any.whl\n"
	if string(constraints) != wantConstraints {
		t.Fatalf("constraints = %q, want %q", constraints, wantConstraints)
	}
}

func TestWheelResolverArgvOmitsEmptySourceConstraints(t *testing.T) {
	requirement, _ := CanonicalPackageRequestV1("demo")
	request, _ := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: "application", Interpreter: blueprint.CommandRequirement{Command: "python"},
		Requirements: []providers.CanonicalPackageRequest{requirement},
		Overrides:    []PythonPackageOverrideV1{{Distribution: "demo", Kind: "local"}},
	})
	got, err := WheelResolverArgv("/usr/bin/python3", request, []providers.ResolvedSourceInput{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(got, " "), ResolverSourceConstraintsPath) {
		t.Fatalf("empty source constraints were added: %#v", got)
	}
}

func TestWheelResolverArgvAddsVersionConstraintWithoutRequestingPackage(t *testing.T) {
	requirement, _ := CanonicalPackageRequestV1("demo")
	request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: "application", Interpreter: blueprint.CommandRequirement{Command: "python"},
		Requirements: []providers.CanonicalPackageRequest{requirement},
		Overrides: []PythonPackageOverrideV1{
			{Distribution: "transitive", Kind: "version", Version: "2.4.0"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	argv, err := WheelResolverArgv("/usr/bin/python3", request, []providers.ResolvedSourceInput{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := argv[len(argv)-1]; got != "demo" {
		t.Fatalf("last resolver argument = %q, version override became a direct requirement", got)
	}
	constraints, err := WheelResolverSourceConstraints(request, []providers.ResolvedSourceInput{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(constraints); got != "transitive==2.4.0\n" {
		t.Fatalf("constraints = %q", got)
	}
}

func TestWheelResolverArgvRequiresSourceWheelInReusableInputs(t *testing.T) {
	requirement, _ := CanonicalPackageRequestV1("demo")
	request, _ := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: "application", Interpreter: blueprint.CommandRequirement{Command: "python"},
		Requirements: []providers.CanonicalPackageRequest{requirement},
		Overrides:    []PythonPackageOverrideV1{{Distribution: "demo", Kind: "local"}},
	})
	digest := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	source := testPythonSourceInput("application", "demo", "1.0", digest, digest)
	if _, err := WheelResolverArgv("/usr/bin/python3", request, []providers.ResolvedSourceInput{source}, nil); err == nil || !strings.Contains(err.Error(), "exactly one reusable wheel") {
		t.Fatalf("error = %v", err)
	}
}
