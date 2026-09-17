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
	got, err := WheelResolverArgv("/usr/bin/python3", request, []providers.ResolvedSourceInput{source}, []providerstore.ArtifactDescriptor{wheel}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/usr/bin/python3", "-I", "-m", "pip", "--disable-pip-version-check", "wheel", "--no-cache-dir",
		"--progress-bar", "off", "--find-links", ResolverInputDirectory,
		"--wheel-dir", ResolverOutputDirectory, "--constraint", ResolverSourceConstraintsPath, "demo>=1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
	constraints, err := WheelResolverSourceConstraints(request, []providers.ResolvedSourceInput{source}, []providerstore.ArtifactDescriptor{wheel}, nil)
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
	got, err := WheelResolverArgv("/usr/bin/python3", request, []providers.ResolvedSourceInput{}, nil, nil)
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
	argv, err := WheelResolverArgv("/usr/bin/python3", request, []providers.ResolvedSourceInput{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := argv[len(argv)-1]; got != "demo" {
		t.Fatalf("last resolver argument = %q, version override became a direct requirement", got)
	}
	constraints, err := WheelResolverSourceConstraints(request, []providers.ResolvedSourceInput{}, nil, nil)
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
	if _, err := WheelResolverArgv("/usr/bin/python3", request, []providers.ResolvedSourceInput{source}, nil, nil); err == nil || !strings.Contains(err.Error(), "exactly one reusable wheel") {
		t.Fatalf("error = %v", err)
	}
}

func TestWheelResolverRecipePinsSelectedWheelsAndRootsDeterministically(t *testing.T) {
	requirement, err := CanonicalPackageRequestV1("ordinary>=1")
	if err != nil {
		t.Fatal(err)
	}
	request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: "application", Interpreter: blueprint.CommandRequirement{Command: "python"},
		Requirements: []providers.CanonicalPackageRequest{requirement},
		Overrides:    []PythonPackageOverrideV1{},
	})
	if err != nil {
		t.Fatal(err)
	}
	first := testSelectedPortableWheelV1("z-tool", "z_tool-1.0.0-py3-none-any.whl", "1.0.0", "a")
	second := testSelectedPortableWheelV1("a-tool", "a_tool-2.0.0-py3-none-any.whl", "2.0.0", "b")

	got, err := WheelResolverArgv("/usr/bin/python3", request, []providers.ResolvedSourceInput{}, nil, []PortableToolVerifiedWheelInputV1{first, second})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/usr/bin/python3", "-I", "-m", "pip", "--disable-pip-version-check", "wheel", "--no-cache-dir",
		"--progress-bar", "off", "--find-links", ResolverInputDirectory,
		"--wheel-dir", ResolverOutputDirectory, "--constraint", ResolverSourceConstraintsPath,
		"ordinary>=1", "a-tool", "z-tool",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}

	constraints, err := WheelResolverSourceConstraints(request, []providers.ResolvedSourceInput{}, nil, []PortableToolVerifiedWheelInputV1{first, second})
	if err != nil {
		t.Fatal(err)
	}
	wantConstraints := "a-tool @ file:///.reploy-resolver/input/a_tool-2.0.0-py3-none-any.whl\nz-tool @ file:///.reploy-resolver/input/z_tool-1.0.0-py3-none-any.whl\n"
	if string(constraints) != wantConstraints {
		t.Fatalf("constraints = %q, want %q", constraints, wantConstraints)
	}

	// The selected input order is not part of resolver identity.
	reversed, err := WheelResolverSourceConstraints(request, []providers.ResolvedSourceInput{}, nil, []PortableToolVerifiedWheelInputV1{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(constraints, reversed) {
		t.Fatalf("reversed selected input changed constraints: %q vs %q", reversed, constraints)
	}
}

func TestWheelResolverRecipeAlwaysIncludesSelectedRoot(t *testing.T) {
	requirement, err := CanonicalPackageRequestV1("demo>=1")
	if err != nil {
		t.Fatal(err)
	}
	request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: "application", Interpreter: blueprint.CommandRequirement{Command: "python"},
		Requirements: []providers.CanonicalPackageRequest{requirement}, Overrides: []PythonPackageOverrideV1{},
	})
	if err != nil {
		t.Fatal(err)
	}
	selected := testSelectedPortableWheelV1("demo", "demo-1.0.0-py3-none-any.whl", "1.0.0", "c")
	argv, err := WheelResolverArgv("/usr/bin/python3", request, []providers.ResolvedSourceInput{}, nil, []PortableToolVerifiedWheelInputV1{selected})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, argument := range argv {
		if argument == "demo" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("selected root count = %d, want one unconditional selected root: %#v", count, argv)
	}
}

func TestWheelResolverRecipeRejectsSelectedWheelCollisions(t *testing.T) {
	base := testSelectedPortableWheelV1("demo", "demo-1.0.0-py3-none-any.whl", "1.0.0", "d")
	requirement, err := CanonicalPackageRequestV1("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: "application", Interpreter: blueprint.CommandRequirement{Command: "python"},
		Requirements: []providers.CanonicalPackageRequest{requirement}, Overrides: []PythonPackageOverrideV1{},
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicate := base
	duplicate.Descriptor.LogicalPath = "wheels/demo-other-1.0.0-py3-none-any.whl"
	duplicate.Inspection.Filename = duplicate.Descriptor.LogicalPath[len("wheels/"):]
	duplicate.Inspection.Artifact = duplicate.Descriptor
	for _, test := range []struct {
		name     string
		request  providers.CanonicalProviderRequest
		sources  []providers.ResolvedSourceInput
		selected []PortableToolVerifiedWheelInputV1
		want     string
	}{
		{name: "duplicate selected distribution", request: request, selected: []PortableToolVerifiedWheelInputV1{base, base}, want: "duplicate distribution"},
		{name: "conflicting selected distribution", request: request, selected: []PortableToolVerifiedWheelInputV1{base, duplicate}, want: "duplicate distribution"},
		{
			name: "local override",
			request: func() providers.CanonicalProviderRequest {
				value, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
					Component: "application", Interpreter: blueprint.CommandRequirement{Command: "python"},
					Requirements: []providers.CanonicalPackageRequest{requirement},
					Overrides:    []PythonPackageOverrideV1{{Distribution: "demo", Kind: "local"}},
				})
				if err != nil {
					t.Fatal(err)
				}
				return value
			}(),
			selected: []PortableToolVerifiedWheelInputV1{base}, want: "local package override",
		},
		{
			name:    "source",
			request: request,
			sources: []providers.ResolvedSourceInput{testPythonSourceInput(
				"application", "demo", "1.0.0", canonical.Digest("sha256:"+strings.Repeat("f", 64)), base.Descriptor.SHA256,
			)},
			selected: []PortableToolVerifiedWheelInputV1{base}, want: "conflicts with source",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := WheelResolverSourceConstraints(test.request, test.sources, nil, test.selected); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func testSelectedPortableWheelV1(distribution, filename, version, digestLetter string) PortableToolVerifiedWheelInputV1 {
	digest := canonical.Digest("sha256:" + strings.Repeat(digestLetter, 64))
	descriptor := providerstore.ArtifactDescriptor{LogicalPath: "wheels/" + filename, Kind: "wheel", Size: "10", SHA256: digest}
	return PortableToolVerifiedWheelInputV1{
		Descriptor: descriptor,
		Inspection: WheelInspectionV1{Artifact: descriptor, Distribution: distribution, Version: version, Filename: filename},
	}
}
