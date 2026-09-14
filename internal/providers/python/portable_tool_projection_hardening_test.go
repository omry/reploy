package python

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/portabletool"
	"github.com/omry/reploy/internal/providers"
)

const portableToolProjectionTestDigest = canonical.Digest("sha256:1111111111111111111111111111111111111111111111111111111111111111")

func TestProjectPortableToolPythonBindingsV1CreatesAndMergesApplicationComponents(t *testing.T) {
	plan := portableToolPythonPlanForTest(t, "application:demo", "one", "demo", "demo>=1,<2")
	components, projection, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	if len(components) != 1 || components[0].Component != "application/demo/python" {
		t.Fatalf("components = %#v", components)
	}
	request, err := decodeCanonicalProviderRequestV1(components[0].Request)
	if err != nil {
		t.Fatal(err)
	}
	if request.Interpreter.Command != "python" || len(request.Requirements) != 1 {
		t.Fatalf("created request = %#v", request)
	}
	if len(projection.Components) != 1 || !reflect.DeepEqual(projection.Components[0].TestedTags, []string{"py3-none-manylinux1_x86_64"}) {
		t.Fatalf("projection = %#v", projection)
	}
	if projection.Components[0].Bindings[0].SelectedClosureDigest != plan.Tools[0].SelectedClosureDigest {
		t.Fatal("projection omitted the selected-closure identity")
	}

	existingRequirement, err := CanonicalPackageRequestV1("existing==1")
	if err != nil {
		t.Fatal(err)
	}
	existingRequest, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component:    "application/demo/python",
		Interpreter:  blueprint.CommandRequirement{Command: "python313", Version: ">=3.13,<3.14", Supplier: "base"},
		Requirements: []providers.CanonicalPackageRequest{existingRequirement},
		Overrides:    []PythonPackageOverrideV1{},
	})
	if err != nil {
		t.Fatal(err)
	}
	originalComponents, err := canonical.Marshal([]providers.ResolvedComponentRequestV1{{
		Component: "application/demo/python", Provider: blueprint.ComponentTypePython, Request: existingRequest,
	}})
	if err != nil {
		t.Fatal(err)
	}
	input := []providers.ResolvedComponentRequestV1{{
		Component: "application/demo/python", Provider: blueprint.ComponentTypePython, Request: existingRequest,
	}}
	components, _, err = ProjectPortableToolPythonBindingsV1(plan, input)
	if err != nil {
		t.Fatal(err)
	}
	request, err = decodeCanonicalProviderRequestV1(components[0].Request)
	if err != nil {
		t.Fatal(err)
	}
	if request.Interpreter.Command != "python313" || request.Interpreter.Version != ">=3.13,<3.14" || request.Interpreter.Supplier != "base" || len(request.Requirements) != 2 {
		t.Fatalf("merged request = %#v", request)
	}
	afterComponents, err := canonical.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(originalComponents, afterComponents) {
		t.Fatal("projection mutated caller-owned component requests")
	}
}

func TestProjectPortableToolPythonBindingsV1PreservesGeneralExistingRequirements(t *testing.T) {
	plan := portableToolPythonPlanForTest(t, "application:demo", "one", "demo", "demo>=1,<2")
	existingValues := []string{
		"demo[http]>=1.0",
		"direct @ https://example.com/direct-1.0.0-py3-none-any.whl",
		"marked>=1; platform_release == '5.15/vendor'",
		"other>=1rc1,<2",
	}
	existing := make([]providers.CanonicalPackageRequest, 0, len(existingValues))
	for _, value := range existingValues {
		requirement, err := CanonicalPackageRequestV1(value)
		if err != nil {
			t.Fatal(err)
		}
		existing = append(existing, requirement)
	}
	request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: "application/demo/python", Interpreter: blueprint.CommandRequirement{Command: "python"},
		Requirements: existing, Overrides: []PythonPackageOverrideV1{},
	})
	if err != nil {
		t.Fatal(err)
	}
	components, _, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{{
		Component: "application/demo/python", Provider: blueprint.ComponentTypePython, Request: request,
	}})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := decodeCanonicalProviderRequestV1(components[0].Request)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]struct{}, len(merged.Requirements))
	for _, requirement := range merged.Requirements {
		value, ok := requirement.Value["requirement"].(string)
		if !ok {
			t.Fatalf("merged requirement is not a string: %#v", requirement)
		}
		got[value] = struct{}{}
	}
	for _, value := range append(existingValues, "demo>=1,<2") {
		if _, ok := got[value]; !ok {
			t.Errorf("merged request omitted %q: %#v", value, merged.Requirements)
		}
	}
	if len(got) != len(existingValues)+1 {
		t.Fatalf("merged requirements = %#v", merged.Requirements)
	}
}

func TestProjectPortableToolPythonBindingsV1RejectsConflictingOverridesAndDirectURLs(t *testing.T) {
	plan := portableToolPythonPlanForTest(t, "application:demo", "one", "demo", "demo==1")
	existing, err := CanonicalPackageRequestV1("existing==1")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		overrides []PythonPackageOverrideV1
		wantError string
	}{
		{name: "conflicting version", overrides: []PythonPackageOverrideV1{{Distribution: "demo", Kind: "version", Version: "2"}}, wantError: "conflicting with exact wheel version"},
		{name: "matching semantic version", overrides: []PythonPackageOverrideV1{{Distribution: "demo", Kind: "version", Version: "1"}}},
		{name: "matching local source", overrides: []PythonPackageOverrideV1{{Distribution: "demo", Kind: "local"}}, wantError: "local Python package override"},
		{name: "unrelated override", overrides: []PythonPackageOverrideV1{{Distribution: "other", Kind: "version", Version: "2"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
				Component: "application/demo/python", Interpreter: blueprint.CommandRequirement{Command: "python"},
				Requirements: []providers.CanonicalPackageRequest{existing}, Overrides: test.overrides,
			})
			if err != nil {
				t.Fatal(err)
			}
			components, _, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{{
				Component: "application/demo/python", Provider: blueprint.ComponentTypePython, Request: request,
			}})
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("override error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := decodeCanonicalProviderRequestV1(components[0].Request)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded.Overrides, test.overrides) {
				t.Fatalf("preserved overrides = %#v, want %#v", decoded.Overrides, test.overrides)
			}
		})
	}

	for _, test := range []struct {
		name        string
		requirement string
		wantError   bool
		wantMessage string
		wantAbsent  string
	}{
		{name: "same distribution direct URL", requirement: "demo @ https://example.invalid/demo-1.0.0.whl", wantError: true},
		{name: "same distribution marker-qualified direct URL", requirement: "demo @ https://example.invalid/demo-1.0.0.whl ; python_version >= '3.0'", wantError: true},
		{name: "same distribution compact marker-qualified direct URL", requirement: "demo @ https://example.invalid/demo-1.0.0.whl;python_version >= '3.0'", wantError: true},
		{name: "same distribution direct URL unknown bytes", requirement: "demo @ https://example.invalid/other.whl", wantError: true, wantMessage: "invalid wheel filename"},
		{name: "same distribution unnamed wheel URL", requirement: "file:///tmp/demo-1.0.0-py3-none-any.whl", wantError: true},
		{name: "same distribution unnamed VCS URL", requirement: "git+https://example.invalid/demo.git#egg=demo", wantError: true, wantMessage: "cannot be proven distinct"},
		{name: "named wheel URL mismatched distribution", requirement: "other @ https://example.invalid/demo-1.0.0-py3-none-any.whl", wantError: true, wantMessage: "does not match wheel distribution"},
		{name: "same distribution local wheel", requirement: "demo-1.0.0-py3-none-any.whl", wantError: true, wantMessage: "local path"},
		{name: "same distribution marker-qualified local wheel", requirement: "demo-1.0.0-py3-none-any.whl ; python_version >= '3.0'", wantError: true, wantMessage: "local path"},
		{name: "same distribution relative local wheel", requirement: "vendor/demo-1.0.0-py3-none-any.whl", wantError: true, wantMessage: "local path"},
		{name: "unrelated direct URL", requirement: "other @ https://example.invalid/other-1.0.0-py3-none-any.whl"},
		{name: "unrelated marker-qualified direct URL", requirement: "other @ https://example.invalid/other-1.0.0-py3-none-any.whl ; python_version < '3.0'"},
		{name: "unrelated compact marker-qualified direct URL", requirement: "other @ https://example.invalid/other-1.0.0-py3-none-any.whl;python_version < '3.0'"},
		{name: "unrelated unnamed wheel URL", requirement: "https://example.invalid/other-1.0.0-py3-none-any.whl"},
		{name: "unrelated unnamed VCS URL", requirement: "git+https://example.invalid/other.git#egg=other", wantError: true, wantMessage: "cannot be proven distinct"},
		{name: "credential-bearing VCS URL", requirement: "git+https://user:SENTINEL@example.invalid/private.git#egg=private", wantError: true, wantMessage: "cannot be proven distinct", wantAbsent: "SENTINEL"},
		{name: "malformed URL escape", requirement: "%ZZSENTINEL", wantError: true, wantMessage: "invalid syntax", wantAbsent: "SENTINEL"},
		{name: "malformed requirement", requirement: "@SENTINEL", wantError: true, wantMessage: "invalid syntax", wantAbsent: "SENTINEL"},
		{name: "unrelated named source archive", requirement: "other @ https://example.invalid/other-1.0.0.tar.gz", wantError: true, wantMessage: "cannot be proven distinct"},
		{name: "unrelated local wheel", requirement: "vendor/other-1.0.0-py3-none-any.whl"},
		{name: "opaque unnamed direct URL", requirement: "https://example.invalid/archive.tar.gz", wantError: true},
		{name: "opaque local archive", requirement: "vendor/archive.tar.gz", wantError: true, wantMessage: "local path"},
		{name: "bare tar archive", requirement: "demo.tar", wantError: true, wantMessage: "local path"},
		{name: "marker-qualified bare tar archive", requirement: "demo.tar ; python_version < '3.0'", wantError: true, wantMessage: "local path"},
		{name: "bare tbz archive", requirement: "demo.tbz", wantError: true, wantMessage: "local path"},
		{name: "bare txz archive", requirement: "demo.txz", wantError: true, wantMessage: "local path"},
		{name: "bare tlz archive", requirement: "demo.tlz", wantError: true, wantMessage: "local path"},
		{name: "bare tar lz archive", requirement: "demo.tar.lz", wantError: true, wantMessage: "local path"},
		{name: "bare tar lzma archive", requirement: "demo.tar.lzma", wantError: true, wantMessage: "local path"},
		{name: "same distribution extras", requirement: "demo[http]>=1"},
		{name: "conflicting same distribution extras", requirement: "demo[http]==2", wantError: true, wantMessage: "requirements for \"demo\" conflict"},
		{name: "compatible same distribution marker", requirement: "demo==1; python_version >= '3.12'"},
		{name: "conflicting same distribution marker", requirement: "demo==2; python_version >= '3.12'", wantError: true, wantMessage: "requirements for \"demo\" conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			requirement, err := CanonicalPackageRequestV1(test.requirement)
			if err != nil {
				t.Fatal(err)
			}
			request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
				Component: "application/demo/python", Interpreter: blueprint.CommandRequirement{Command: "python"},
				Requirements: []providers.CanonicalPackageRequest{requirement}, Overrides: []PythonPackageOverrideV1{},
			})
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{{
				Component: "application/demo/python", Provider: blueprint.ComponentTypePython, Request: request,
			}})
			if test.wantError {
				wantMessage := test.wantMessage
				if wantMessage == "" {
					wantMessage = "direct URL"
				}
				if err == nil || !strings.Contains(err.Error(), wantMessage) {
					t.Fatalf("package source conflict error = %v, want %q", err, wantMessage)
				}
				if test.wantAbsent != "" && strings.Contains(err.Error(), test.wantAbsent) {
					t.Fatalf("package source conflict error leaked %q: %v", test.wantAbsent, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProjectPortableToolPythonBindingsV1ChecksDependencyVersionOverrides(t *testing.T) {
	plan := portableToolPythonPlanForTest(t, "application:demo", "demo", "demo", "demo==1")
	contract := &plan.Tools[0].Responsibilities.BindingContracts[0]
	contract.Record.Value["requirements"] = []any{"demo==1", "shared<2"}
	portableToolProjectionRebindRecordForTest(t, contract)
	artifact := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
	artifact.Record.Value["contract"] = map[string]any{
		"id": contract.Reference.ID, "digest": string(contract.Reference.Digest),
	}
	portableToolProjectionRebindRecordForTest(t, artifact)

	for _, test := range []struct {
		name        string
		version     string
		wantMessage string
	}{
		{name: "compatible", version: "1"},
		{name: "conflicting", version: "3", wantMessage: "version Python package override for portable dependency \"shared\" conflicts with binding requirements"},
	} {
		t.Run(test.name, func(t *testing.T) {
			requirement, err := CanonicalPackageRequestV1("existing==1")
			if err != nil {
				t.Fatal(err)
			}
			request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
				Component: "application/demo/python", Interpreter: blueprint.CommandRequirement{Command: "python"},
				Requirements: []providers.CanonicalPackageRequest{requirement},
				Overrides:    []PythonPackageOverrideV1{{Distribution: "shared", Kind: "version", Version: test.version}},
			})
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{{
				Component: "application/demo/python", Provider: blueprint.ComponentTypePython, Request: request,
			}})
			if test.wantMessage == "" {
				if err != nil {
					t.Fatalf("compatible dependency override failed: %v", err)
				}
				return
			}
			if err == nil || err.Error() != test.wantMessage {
				t.Fatalf("dependency override error = %v, want %q", err, test.wantMessage)
			}
		})
	}
}

func TestProjectPortableToolPythonBindingsV1ChecksExistingDependencyRootsWithExtrasAndMarkers(t *testing.T) {
	plan := portableToolPythonPlanForTest(t, "application:demo", "demo", "demo", "demo==1")
	contract := &plan.Tools[0].Responsibilities.BindingContracts[0]
	contract.Record.Value["requirements"] = []any{"demo==1", "shared<2"}
	portableToolProjectionRebindRecordForTest(t, contract)
	artifact := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
	artifact.Record.Value["contract"] = map[string]any{
		"id": contract.Reference.ID, "digest": string(contract.Reference.Digest),
	}
	portableToolProjectionRebindRecordForTest(t, artifact)

	for _, test := range []struct {
		name        string
		requirement string
		wantMessage string
	}{
		{name: "compatible extras", requirement: "shared[feature]>=1,<2"},
		{name: "conflicting extras", requirement: "shared[feature]==3", wantMessage: "portable Python requirements for \"shared\" conflict"},
		{name: "compatible extras and marker", requirement: "shared[feature]>=1,<2; python_version >= '3.12'"},
		{name: "conflicting extras and marker", requirement: "shared[feature]==3; python_version >= '3.12'", wantMessage: "portable Python requirements for \"shared\" conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			requirement, err := CanonicalPackageRequestV1(test.requirement)
			if err != nil {
				t.Fatal(err)
			}
			request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
				Component: "application/demo/python", Interpreter: blueprint.CommandRequirement{Command: "python"},
				Requirements: []providers.CanonicalPackageRequest{requirement}, Overrides: []PythonPackageOverrideV1{},
			})
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{{
				Component: "application/demo/python", Provider: blueprint.ComponentTypePython, Request: request,
			}})
			if test.wantMessage == "" {
				if err != nil {
					t.Fatalf("compatible existing dependency root failed: %v", err)
				}
				return
			}
			if err == nil || err.Error() != test.wantMessage {
				t.Fatalf("existing dependency root error = %v, want %q", err, test.wantMessage)
			}
		})
	}
}

func TestPortableToolPythonRequirementIdentityV1ClassifiesMarkerQualifiedArchives(t *testing.T) {
	for _, filename := range []string{
		"archive.zip", "archive-1.0-py3-none-any.whl", "archive.tar.bz2", "archive.tbz",
		"archive.tar.gz", "archive.tgz", "archive.tar", "archive.tar.xz", "archive.txz",
		"archive.tlz", "archive.tar.lz", "archive.tar.lzma",
	} {
		requirement := filename + " ; python_version >= '3.0'"
		_, sourceKind, err := portableToolPythonRequirementIdentityV1(requirement)
		if err != nil {
			t.Errorf("identity %q: %v", requirement, err)
			continue
		}
		if sourceKind != "local path" {
			t.Errorf("identity %q source kind = %q, want local path", requirement, sourceKind)
		}
	}
	distribution, sourceKind, err := portableToolPythonRequirementIdentityV1("demo==1; platform_release == '5.15/vendor;still-marker'")
	if err != nil || distribution != "demo" || sourceKind != "" {
		t.Fatalf("marker-literal identity = %q, %q, %v", distribution, sourceKind, err)
	}
}

func TestProjectPortableToolPythonBindingsV1IsolatesApplicationsAndInputs(t *testing.T) {
	plan := portableToolPythonPlanForTest(t, "application:alpha", "shared", "shared-pkg", "shared-pkg==1")
	second := plan.Tools[0]
	second.Scope = "application:beta"
	plan.Tools = append(plan.Tools, second)
	original, err := canonical.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	components, projection, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	if len(components) != 2 || components[0].Component != "application/alpha/python" || components[1].Component != "application/beta/python" {
		t.Fatalf("components = %#v", components)
	}
	if len(projection.Components) != 2 || projection.Components[0].Component != "application/alpha/python" || projection.Components[1].Component != "application/beta/python" {
		t.Fatalf("projection = %#v", projection)
	}
	alphaBinding := projection.Components[0].Bindings[0]
	betaBinding := projection.Components[1].Bindings[0]
	if alphaBinding.Scope != "application:alpha" || betaBinding.Scope != "application:beta" ||
		alphaBinding.Component != "application/alpha/python" || betaBinding.Component != "application/beta/python" ||
		alphaBinding.Distribution != "shared-pkg" || betaBinding.Distribution != "shared-pkg" {
		t.Fatalf("isolated application sidecars = %#v", projection.Components)
	}
	if alphaBinding.Contract != betaBinding.Contract || alphaBinding.Artifact != betaBinding.Artifact {
		t.Fatalf("isolated applications did not retain the same records: %#v", projection.Components)
	}
	after, err := canonical.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, after) {
		t.Fatal("projection mutated the portable-tool plan")
	}
}

func TestProjectPortableToolPythonBindingsV1CombinesNeutralToolsAndIsOrderIndependent(t *testing.T) {
	first := portableToolPythonPlanForTest(t, "application:demo", "alpha", "alpha-pkg", "alpha-pkg==1")
	second := portableToolPythonPlanForTest(t, "application:demo", "beta", "beta-pkg", "beta-pkg==1")
	first.Tools = append(first.Tools, second.Tools...)

	unrelated := func(component string) providers.ResolvedComponentRequestV1 {
		requirement, err := CanonicalPackageRequestV1("existing==1")
		if err != nil {
			t.Fatal(err)
		}
		request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
			Component: component, Interpreter: blueprint.CommandRequirement{Command: "python"},
			Requirements: []providers.CanonicalPackageRequest{requirement}, Overrides: []PythonPackageOverrideV1{},
		})
		if err != nil {
			t.Fatal(err)
		}
		return providers.ResolvedComponentRequestV1{Component: component, Provider: blueprint.ComponentTypePython, Request: request}
	}
	forward := []providers.ResolvedComponentRequestV1{unrelated("application/one/python"), unrelated("application/two/python")}
	reverse := []providers.ResolvedComponentRequestV1{forward[1], forward[0]}
	forwardComponents, forwardProjection, err := ProjectPortableToolPythonBindingsV1(first, forward)
	if err != nil {
		t.Fatal(err)
	}
	reverseComponents, reverseProjection, err := ProjectPortableToolPythonBindingsV1(first, reverse)
	if err != nil {
		t.Fatal(err)
	}
	forwardBytes, err := canonical.Marshal(struct {
		Components []providers.ResolvedComponentRequestV1 `json:"components"`
		Projection PortableToolPythonProjectionV1         `json:"projection"`
	}{forwardComponents, forwardProjection})
	if err != nil {
		t.Fatal(err)
	}
	reverseBytes, err := canonical.Marshal(struct {
		Components []providers.ResolvedComponentRequestV1 `json:"components"`
		Projection PortableToolPythonProjectionV1         `json:"projection"`
	}{reverseComponents, reverseProjection})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(forwardBytes, reverseBytes) {
		t.Fatal("component input order changed canonical projection bytes")
	}
	if len(forwardProjection.Components) != 1 || len(forwardProjection.Components[0].Bindings) != 2 {
		t.Fatalf("combined projection = %#v", forwardProjection)
	}
}

func TestProjectPortableToolPythonBindingsV1DeduplicatesRootsAndRejectsConflicts(t *testing.T) {
	plan := portableToolPythonPlanForTest(t, "application:demo", "alpha", "demo-pkg", "demo-pkg==1")
	requirement, err := CanonicalPackageRequestV1("demo-pkg==1")
	if err != nil {
		t.Fatal(err)
	}
	request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: "application/demo/python", Interpreter: blueprint.CommandRequirement{Command: "python"},
		Requirements: []providers.CanonicalPackageRequest{requirement}, Overrides: []PythonPackageOverrideV1{},
	})
	if err != nil {
		t.Fatal(err)
	}
	components, _, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{{
		Component: "application/demo/python", Provider: blueprint.ComponentTypePython, Request: request,
	}})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeCanonicalProviderRequestV1(components[0].Request)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Requirements) != 1 {
		t.Fatalf("duplicate root was not collapsed: %#v", decoded.Requirements)
	}
	conflictingRequirement, err := CanonicalPackageRequestV1("demo-pkg==2")
	if err != nil {
		t.Fatal(err)
	}
	conflictingRequest, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: "application/demo/python", Interpreter: blueprint.CommandRequirement{Command: "python"},
		Requirements: []providers.CanonicalPackageRequest{conflictingRequirement}, Overrides: []PythonPackageOverrideV1{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{{
		Component: "application/demo/python", Provider: blueprint.ComponentTypePython, Request: conflictingRequest,
	}}); err == nil || !strings.Contains(err.Error(), "requirements for \"demo-pkg\" conflict") {
		t.Fatalf("root conflict error = %v", err)
	}

	conflicting := portableToolPythonPlanForTest(t, "application:demo", "beta", "demo-pkg", "demo-pkg==2")
	conflictingArtifact := &conflicting.Tools[0].Responsibilities.BindingArtifacts[0]
	conflictingArtifact.Record.Value["ecosystem_version"] = "2.0.0"
	conflictingArtifact.Record.Value["filename"] = "demo_pkg-2.0.0-py3-none-manylinux1_x86_64.whl"
	portableToolProjectionRebindRecordForTest(t, conflictingArtifact)
	plan.Tools = append(plan.Tools, conflicting.Tools...)
	if _, _, err := ProjectPortableToolPythonBindingsV1(plan, nil); err == nil || !strings.Contains(err.Error(), "must use an array") {
		t.Fatalf("nil component input error = %v", err)
	}
	if _, _, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{}); err == nil || !strings.Contains(err.Error(), "conflicts across selected tools") {
		t.Fatalf("semantic binding conflict error = %v", err)
	}
}

func TestProjectPortableToolPythonBindingsV1RejectsSiblingRootsOutsideExactWheel(t *testing.T) {
	for _, test := range []struct {
		name               string
		selectedVersion    string
		siblingRequirement string
	}{
		{name: "prerelease", selectedVersion: "2rc1", siblingRequirement: "beta==1rc1"},
		{name: "prerelease range", selectedVersion: "2rc1", siblingRequirement: "beta<2rc1"},
		{name: "local version", selectedVersion: "2+vendor", siblingRequirement: "beta==2+other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := portableToolPythonPlanForTest(t, "application:demo", "alpha", "alpha", "alpha==1.0.0")
			alphaContract := &plan.Tools[0].Responsibilities.BindingContracts[0]
			alphaContract.Record.Value["requirements"] = []any{"alpha==1.0.0", test.siblingRequirement}
			portableToolProjectionRebindRecordForTest(t, alphaContract)
			alphaArtifact := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
			alphaArtifact.Record.Value["contract"] = map[string]any{
				"id": alphaContract.Reference.ID, "digest": string(alphaContract.Reference.Digest),
			}
			portableToolProjectionRebindRecordForTest(t, alphaArtifact)

			beta := portableToolPythonPlanForTest(t, "application:demo", "beta", "beta", "beta=="+test.selectedVersion)
			betaArtifact := &beta.Tools[0].Responsibilities.BindingArtifacts[0]
			betaArtifact.Record.Value["ecosystem_version"] = test.selectedVersion
			betaArtifact.Record.Value["filename"] = "beta-" + test.selectedVersion + "-py3-none-manylinux1_x86_64.whl"
			portableToolProjectionRebindRecordForTest(t, betaArtifact)
			plan.Tools = append(plan.Tools, beta.Tools...)

			if _, _, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{}); err == nil || !strings.Contains(err.Error(), "does not satisfy contract requirement") {
				t.Fatalf("sibling exact-wheel conflict error = %v", err)
			}
		})
	}
}

func TestProjectPortableToolPythonBindingsV1AcceptsMatchingComplexExactVersions(t *testing.T) {
	for _, version := range []string{"2rc1", "2+vendor"} {
		t.Run(version, func(t *testing.T) {
			plan := portableToolPythonPlanForTest(t, "application:demo", "one", "demo", "demo=="+version)
			artifact := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
			artifact.Record.Value["ecosystem_version"] = version
			artifact.Record.Value["filename"] = "demo-" + version + "-py3-none-manylinux1_x86_64.whl"
			portableToolProjectionRebindRecordForTest(t, artifact)

			requirement, err := CanonicalPackageRequestV1("demo==" + version)
			if err != nil {
				t.Fatal(err)
			}
			request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
				Component: "application/demo/python", Interpreter: blueprint.CommandRequirement{Command: "python"},
				Requirements: []providers.CanonicalPackageRequest{requirement}, Overrides: []PythonPackageOverrideV1{},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{{
				Component: "application/demo/python", Provider: blueprint.ComponentTypePython, Request: request,
			}}); err != nil {
				t.Fatalf("matching exact version was rejected: %v", err)
			}
		})
	}
}

func TestProjectPortableToolPythonBindingsV1AcceptsExactPrereleaseRangeWitness(t *testing.T) {
	plan := portableToolPythonPlanForTest(t, "application:demo", "one", "demo", "demo>=1rc1,<2")
	artifact := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
	artifact.Record.Value["ecosystem_version"] = "1rc1"
	artifact.Record.Value["filename"] = "demo-1rc1-py3-none-manylinux1_x86_64.whl"
	portableToolProjectionRebindRecordForTest(t, artifact)

	_, projection, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatalf("matching prerelease range was rejected: %v", err)
	}
	if _, err := CanonicalPortableToolPythonProjectionBytesV1(projection); err != nil {
		t.Fatalf("canonical projection rejected exact prerelease witness: %v", err)
	}
}

func TestProjectPortableToolPythonBindingsV1AcceptsDependencyOnlyComplexRanges(t *testing.T) {
	for _, test := range []struct {
		name        string
		requirement string
		duplicate   bool
	}{
		{name: "single prerelease range", requirement: "shared>=1rc1,<2"},
		{name: "duplicate prerelease range", requirement: "shared>=1rc1,<2", duplicate: true},
		{name: "single epoch range", requirement: "shared>=1!2"},
		{name: "duplicate epoch range", requirement: "shared>=1!2", duplicate: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := portableToolPythonPlanForTest(t, "application:demo", "alpha", "alpha", "alpha==1.0.0")
			alphaContract := &plan.Tools[0].Responsibilities.BindingContracts[0]
			alphaContract.Record.Value["requirements"] = append(alphaContract.Record.Value["requirements"].([]any), test.requirement)
			portableToolProjectionRebindRecordForTest(t, alphaContract)
			alphaArtifact := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
			alphaArtifact.Record.Value["contract"] = map[string]any{
				"id": alphaContract.Reference.ID, "digest": string(alphaContract.Reference.Digest),
			}
			portableToolProjectionRebindRecordForTest(t, alphaArtifact)

			if test.duplicate {
				beta := portableToolPythonPlanForTest(t, "application:demo", "beta", "beta", "beta==1.0.0")
				betaContract := &beta.Tools[0].Responsibilities.BindingContracts[0]
				betaContract.Record.Value["requirements"] = append(betaContract.Record.Value["requirements"].([]any), test.requirement)
				portableToolProjectionRebindRecordForTest(t, betaContract)
				betaArtifact := &beta.Tools[0].Responsibilities.BindingArtifacts[0]
				betaArtifact.Record.Value["contract"] = map[string]any{
					"id": betaContract.Reference.ID, "digest": string(betaContract.Reference.Digest),
				}
				portableToolProjectionRebindRecordForTest(t, betaArtifact)
				plan.Tools = append(plan.Tools, beta.Tools...)
			}

			components, projection, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{})
			if err != nil {
				t.Fatalf("dependency-only complex range was rejected: %v", err)
			}
			if _, err := CanonicalPortableToolPythonProjectionBytesV1(projection); err != nil {
				t.Fatalf("canonical projection rejected dependency-only complex range: %v", err)
			}
			decoded, err := decodeCanonicalProviderRequestV1(components[0].Request)
			if err != nil {
				t.Fatal(err)
			}
			matches := 0
			for _, requirement := range decoded.Requirements {
				if requirement.Value["requirement"] == test.requirement {
					matches++
				}
			}
			if matches != 1 {
				t.Fatalf("projected dependency requirement count = %d, want 1", matches)
			}
		})
	}
}

func TestProjectPortableToolPythonBindingsV1RejectsComplexCrossBindingDependencyConflicts(t *testing.T) {
	maxComponent := fmt.Sprintf("%d", int(^uint(0)>>1))
	for _, test := range []struct {
		name        string
		left        string
		right       string
		wantMessage string
	}{
		{name: "prerelease", left: "shared==1rc1", right: "shared==2rc1"},
		{name: "local version", left: "shared==1+alpha", right: "shared==1+beta"},
		{name: "arbitrary equality", left: "shared===1+alpha", right: "shared===1+beta"},
		{name: "epoch range", left: "shared>=1!2", right: "shared<1!1", wantMessage: "compatibility cannot be proven"},
		{name: "prerelease range", left: "shared>=2rc1", right: "shared<1rc1", wantMessage: "compatibility cannot be proven"},
		{name: "maximum wildcard", left: "shared==" + maxComponent + ".*", right: "shared!=" + maxComponent + ".*", wantMessage: "compatibility cannot be proven"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := portableToolPythonPlanForTest(t, "application:demo", "alpha", "alpha", "alpha==1.0.0")
			beta := portableToolPythonPlanForTest(t, "application:demo", "beta", "beta", "beta==1.0.0")
			for _, fixture := range []struct {
				tool        *providers.PortableToolPlanEntryV1
				requirement string
			}{
				{tool: &plan.Tools[0], requirement: test.left},
				{tool: &beta.Tools[0], requirement: test.right},
			} {
				contract := &fixture.tool.Responsibilities.BindingContracts[0]
				contract.Record.Value["requirements"] = append(contract.Record.Value["requirements"].([]any), fixture.requirement)
				portableToolProjectionRebindRecordForTest(t, contract)
				artifact := &fixture.tool.Responsibilities.BindingArtifacts[0]
				artifact.Record.Value["contract"] = map[string]any{
					"id": contract.Reference.ID, "digest": string(contract.Reference.Digest),
				}
				portableToolProjectionRebindRecordForTest(t, artifact)
			}
			plan.Tools = append(plan.Tools, beta.Tools...)

			wantMessage := test.wantMessage
			if wantMessage == "" {
				wantMessage = "requirements for \"shared\" conflict"
			}
			if _, _, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{}); err == nil || !strings.Contains(err.Error(), wantMessage) {
				t.Fatalf("complex cross-binding dependency conflict error = %v", err)
			}
		})
	}
}

func TestCanonicalPortableToolPythonProjectionBytesV1RejectsSiblingRootDrift(t *testing.T) {
	plan := portableToolPythonPlanForTest(t, "application:demo", "alpha", "alpha", "alpha==1.0.0")
	beta := portableToolPythonPlanForTest(t, "application:demo", "beta", "beta", "beta==2rc1")
	betaArtifact := &beta.Tools[0].Responsibilities.BindingArtifacts[0]
	betaArtifact.Record.Value["ecosystem_version"] = "2rc1"
	betaArtifact.Record.Value["filename"] = "beta-2rc1-py3-none-manylinux1_x86_64.whl"
	portableToolProjectionRebindRecordForTest(t, betaArtifact)
	plan.Tools = append(plan.Tools, beta.Tools...)

	_, projection, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	projection.Components[0].Bindings[0].Requirements = []string{"alpha==1.0.0", "beta==1rc1"}
	if _, err := CanonicalPortableToolPythonProjectionBytesV1(projection); err == nil || !strings.Contains(err.Error(), "does not satisfy contract requirement") {
		t.Fatalf("sibling sidecar drift error = %v", err)
	}
}

func TestCanonicalPortableToolPythonProjectionBytesV1RejectsCrossBindingDependencyConflict(t *testing.T) {
	maxComponent := fmt.Sprintf("%d", int(^uint(0)>>1))
	for _, test := range []struct {
		name        string
		left        string
		right       string
		wantMessage string
	}{
		{name: "release", left: "shared==1", right: "shared==2"},
		{name: "prerelease", left: "shared==1rc1", right: "shared==2rc1"},
		{name: "local version", left: "shared==1+alpha", right: "shared==1+beta"},
		{name: "arbitrary equality", left: "shared===1+alpha", right: "shared===1+beta"},
		{name: "epoch range", left: "shared>=1!2", right: "shared<1!1", wantMessage: "compatibility cannot be proven"},
		{name: "prerelease range", left: "shared>=2rc1", right: "shared<1rc1", wantMessage: "compatibility cannot be proven"},
		{name: "maximum wildcard", left: "shared==" + maxComponent + ".*", right: "shared!=" + maxComponent + ".*", wantMessage: "compatibility cannot be proven"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := portableToolPythonPlanForTest(t, "application:demo", "alpha", "alpha", "alpha==1.0.0")
			beta := portableToolPythonPlanForTest(t, "application:demo", "beta", "beta", "beta==1.0.0")
			plan.Tools = append(plan.Tools, beta.Tools...)

			_, projection, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{})
			if err != nil {
				t.Fatal(err)
			}
			projection.Components[0].Bindings[0].Requirements = []string{"alpha==1.0.0", test.left}
			projection.Components[0].Bindings[1].Requirements = []string{"beta==1.0.0", test.right}
			wantMessage := test.wantMessage
			if wantMessage == "" {
				wantMessage = "requirements for \"shared\" conflict"
			}
			if _, err := CanonicalPortableToolPythonProjectionBytesV1(projection); err == nil || !strings.Contains(err.Error(), wantMessage) {
				t.Fatalf("cross-binding dependency conflict error = %v", err)
			}
		})
	}
}

func TestProjectPortableToolPythonBindingsV1RejectsNonPEPRootOperators(t *testing.T) {
	for _, requirement := range []string{"demo=1", "demo>=2||==1", "demo>=2||=1,==1"} {
		t.Run(requirement, func(t *testing.T) {
			plan := portableToolPythonPlanForTest(t, "application:demo", "demo", "demo", "demo==1")
			contract := &plan.Tools[0].Responsibilities.BindingContracts[0]
			contract.Record.Value["requirements"] = []any{requirement}
			portableToolProjectionRebindRecordForTest(t, contract)
			artifact := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
			artifact.Record.Value["contract"] = map[string]any{
				"id": contract.Reference.ID, "digest": string(contract.Reference.Digest),
			}
			portableToolProjectionRebindRecordForTest(t, artifact)

			if _, _, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{}); err == nil || !strings.Contains(err.Error(), "invalid Python package root requirement") {
				t.Fatalf("non-PEP root %q error = %v", requirement, err)
			}
		})
	}
}

func TestProjectPortableToolPythonBindingsV1RejectsScopeOwnerAndCLIConflicts(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*providers.PortableToolPlanV1)
		want   string
	}{
		{name: "scope", mutate: func(plan *providers.PortableToolPlanV1) { plan.Tools[0].Scope = "source-builder:demo" }, want: "application:<owner>"},
		{name: "cli", mutate: func(plan *providers.PortableToolPlanV1) { plan.Tools[0].Exports[0].Path = "/opt/other" }, want: "selected export"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := portableToolPythonPlanForTest(t, "application:demo", "one", "demo", "demo==1")
			test.mutate(&plan)
			if _, _, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{}); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}

	plan := portableToolPythonPlanForTest(t, "application:demo", "one", "demo", "demo==1")
	request, err := CanonicalPackageRequestV1("occupied==1")
	if err != nil {
		t.Fatal(err)
	}
	occupied, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: "application/demo/python", Interpreter: blueprint.CommandRequirement{Command: "python"},
		Requirements: []providers.CanonicalPackageRequest{request}, Overrides: []PythonPackageOverrideV1{},
	})
	if err != nil {
		t.Fatal(err)
	}
	occupied.Provider = blueprint.ComponentTypeAPT
	if _, _, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{{
		Component: "application/demo/python", Provider: blueprint.ComponentTypeAPT, Request: occupied,
	}}); err == nil || !strings.Contains(err.Error(), "owned by provider") {
		t.Fatalf("provider owner conflict error = %v", err)
	}
}

func TestProjectPortableToolPythonBindingsV1RejectsRecordJoinAndIdentityConflicts(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*providers.PortableToolPlanV1)
		want   string
	}{
		{
			name: "missing target artifact",
			mutate: func(plan *providers.PortableToolPlanV1) {
				plan.Tools[0].Responsibilities.BindingArtifacts = []providers.PortableToolSelectedRecordV1{}
			},
			want: "exactly one selected target artifact",
		},
		{
			name: "inexact contract join",
			mutate: func(plan *providers.PortableToolPlanV1) {
				selected := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
				contract, ok := asCanonicalObject(selected.Record.Value["contract"])
				if !ok {
					t.Fatal("artifact contract is not an object")
				}
				contract["digest"] = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
				portableToolProjectionRebindRecordForTest(t, selected)
			},
			want: "does not join an exact selected contract reference",
		},
		{
			name: "binding identity",
			mutate: func(plan *providers.PortableToolPlanV1) {
				selected := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
				selected.Record.Value["id"] = "tool:one/releases/1.0.0/bindings/other/artifacts/linux-amd64"
				selected.Record.Value["binding"] = "other"
				selected.Reference.ID = "tool:one/releases/1.0.0/bindings/other/artifacts/linux-amd64"
				portableToolProjectionRebindRecordForTest(t, selected)
			},
			want: "binding artifact contract reference",
		},
		{
			name: "package identity",
			mutate: func(plan *providers.PortableToolPlanV1) {
				selected := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
				selected.Record.Value["name"] = "other"
				selected.Record.Value["filename"] = "other-1.0.0-py3-none-manylinux1_x86_64.whl"
				portableToolProjectionRebindRecordForTest(t, selected)
			},
			want: "does not match contract package",
		},
		{
			name: "artifact version",
			mutate: func(plan *providers.PortableToolPlanV1) {
				selected := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
				selected.Record.Value["ecosystem_version"] = "2.0.0"
				selected.Record.Value["filename"] = "demo-2.0.0-py3-none-manylinux1_x86_64.whl"
				portableToolProjectionRebindRecordForTest(t, selected)
			},
			want: "does not satisfy contract requirement",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := portableToolPythonPlanForTest(t, "application:demo", "one", "demo", "demo==1")
			test.mutate(&plan)
			if _, _, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{}); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("record conflict error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestProjectPortableToolPythonBindingsV1AcceptsArtifactVersionWithinContractRoot(t *testing.T) {
	plan := portableToolPythonPlanForTest(t, "application:demo", "one", "demo", "demo>=1,<2")
	selected := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
	selected.Record.Value["ecosystem_version"] = "1.5.0"
	selected.Record.Value["filename"] = "demo-1.5.0-py3-none-manylinux1_x86_64.whl"
	portableToolProjectionRebindRecordForTest(t, selected)

	if _, _, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{}); err != nil {
		t.Fatalf("compatible artifact version was rejected: %v", err)
	}
}

func TestProjectPortableToolPythonBindingsV1ProjectsSelectedPlaywrightRecords(t *testing.T) {
	contractRecord := portableToolProjectionDefinitionRecordForTest(t, "../../toolcatalog/definitions/playwright/releases/1.61.0/bindings/python/contract.json")
	artifactRecord := portableToolProjectionDefinitionRecordForTest(t, "../../toolcatalog/definitions/playwright/releases/1.61.0/bindings/python/linux-amd64.json")
	contractReference := portableToolProjectionReferenceForTest(t, contractRecord.Value["id"].(string), contractRecord)
	artifactReference := portableToolProjectionReferenceForTest(t, artifactRecord.Value["id"].(string), artifactRecord)
	plan := providers.PortableToolPlanV1{
		Schema: providers.PortableToolPlanSchemaV1,
		Tools: []providers.PortableToolPlanEntryV1{{
			Scope: "application:browser", SelectedClosureDigest: portableToolProjectionTestDigest,
			Provenance: providers.PortableToolReleaseProvenanceV1{
				Tool: "playwright", Version: "1.61.0", Revision: "1", ManifestDigest: portableToolProjectionTestDigest,
			},
			Responsibilities: providers.PortableToolResponsibilitiesV1{
				BindingContracts: []providers.PortableToolSelectedRecordV1{{Reference: contractReference, Record: contractRecord}},
				BindingArtifacts: []providers.PortableToolSelectedRecordV1{{Reference: artifactReference, Record: artifactRecord}},
				Payloads:         []providers.PortableToolSelectedRecordV1{}, NativePackageSets: []providers.PortableToolSelectedRecordV1{},
			},
			Exports:            []providers.PortableToolExportV1{{Name: "playwright", Path: "/opt/reploy/tools/playwright/bin/playwright"}},
			ValidationProfiles: []providers.PortableToolValidationProfileV1{},
		}},
	}
	components, projection, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	if len(components) != 1 || components[0].Component != "application/browser/python" || len(projection.Components) != 1 {
		t.Fatalf("Playwright projection components = %#v; %#v", components, projection.Components)
	}
	binding := projection.Components[0].Bindings[0]
	if binding.Distribution != "playwright" || binding.Wheel.Filename != "playwright-1.61.0-py3-none-manylinux1_x86_64.whl" ||
		binding.Contract != contractReference || binding.Artifact != artifactReference || len(binding.Requirements) != 3 {
		t.Fatalf("Playwright binding projection = %#v", binding)
	}
}

func TestValidatePortableToolWheelTagEnvelopeV1(t *testing.T) {
	valid := []struct {
		tag      string
		platform string
	}{
		{"py3-none-any", "linux/amd64"},
		{"py313-none-any", "linux/amd64"},
		{"cp313-none-any", "linux/amd64"},
		{"py3-cp313d-linux_x86_64", "linux/amd64"},
		{"cp32-abi3-manylinux1_x86_64", "linux/amd64"},
		{"cp313-cp313t-manylinux_2_17_aarch64", "linux/arm64"},
	}
	for _, test := range valid {
		if err := ValidatePortableToolWheelTagEnvelopeV1(test.tag, test.platform); err != nil {
			t.Errorf("valid envelope %q/%q: %v", test.tag, test.platform, err)
		}
	}
	invalid := []struct {
		tag      string
		platform string
	}{
		{"pp313-none-any", "linux/amd64"},
		{"cp31-abi3-linux_x86_64", "linux/amd64"},
		{"cp313-abi3-any", "linux/amd64"},
		{"cp313-cp313-any", "linux/amd64"},
		{"py3-none-musllinux_1_2_x86_64", "linux/amd64"},
		{"py3-none-linux_aarch64", "linux/amd64"},
		{"py3-none-manylinux1_aarch64", "linux/arm64"},
		{"py3-none-manylinux_2_16_aarch64", "linux/arm64"},
		{"py3-none-manylinux_3_17_aarch64", "linux/arm64"},
	}
	for _, test := range invalid {
		if err := ValidatePortableToolWheelTagEnvelopeV1(test.tag, test.platform); err == nil {
			t.Errorf("unsupported envelope %q/%q was accepted", test.tag, test.platform)
		}
	}
}

func TestCanonicalPortableToolPythonProjectionBytesV1RejectsTestedTagDrift(t *testing.T) {
	plan := portableToolPythonPlanForTest(t, "application:demo", "one", "demo", "demo==1")
	_, projection, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	projection.Components[0].TestedTags = []string{"py3-none-any", "py3-none-manylinux1_x86_64"}
	if _, err := CanonicalPortableToolPythonProjectionBytesV1(projection); err == nil || !strings.Contains(err.Error(), "tag union") {
		t.Fatalf("tested-tag drift error = %v", err)
	}
}

func TestCanonicalPortableToolPythonProjectionBytesV1RequiresArtifactInterpreterCoverage(t *testing.T) {
	for _, test := range []struct {
		name            string
		supportedPython []string
		requiresPython  string
		wantError       bool
	}{
		{name: "complete minor series", supportedPython: []string{"3.12"}, requiresPython: ">=3.12,<3.13"},
		{name: "finite minor prefix", supportedPython: []string{"3.12"}, requiresPython: ">=3.12,<3.12.1", wantError: true},
		{name: "excluded minor release", supportedPython: []string{"3.12"}, requiresPython: ">=3.12,<3.13,!=3.12.7", wantError: true},
		{name: "disjoint artifact", supportedPython: []string{"3.12"}, requiresPython: ">=3.13", wantError: true},
		{name: "exact patch", supportedPython: []string{"3.12.0"}, requiresPython: ">=3.12,<3.12.1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := portableToolPythonPlanForTest(t, "application:demo", "one", "demo", "demo==1")
			_, projection, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{})
			if err != nil {
				t.Fatal(err)
			}
			binding := &projection.Components[0].Bindings[0]
			binding.SupportedPython = test.supportedPython
			binding.Wheel.RequiresPython = test.requiresPython
			_, err = CanonicalPortableToolPythonProjectionBytesV1(projection)
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), "does not cover supported Python claim") {
					t.Fatalf("coverage error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("valid coverage rejected: %v", err)
			}
		})
	}
}

func TestProjectPortableToolPythonBindingsV1PreservesExactTagsOutsideTestedEnvelope(t *testing.T) {
	plan := portableToolPythonPlanForTest(t, "application:demo", "one", "demo", "demo==1")
	selected := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
	selected.Record.Value["filename"] = "demo-1.0.0-py3-none.cp313-manylinux1_x86_64.whl"
	selected.Record.Value["tags"] = []any{
		"py3-cp313-manylinux1_x86_64",
		"py3-none-manylinux1_x86_64",
	}
	portableToolProjectionRebindRecordForTest(t, selected)

	_, projection, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	binding := projection.Components[0].Bindings[0]
	if !reflect.DeepEqual(binding.Wheel.Tags, []string{
		"py3-cp313-manylinux1_x86_64",
		"py3-none-manylinux1_x86_64",
	}) {
		t.Fatalf("exact wheel tags = %v", binding.Wheel.Tags)
	}
	if !reflect.DeepEqual(binding.SupportedTags, []string{"py3-none-manylinux1_x86_64"}) ||
		!reflect.DeepEqual(projection.Components[0].TestedTags, []string{"py3-none-manylinux1_x86_64"}) {
		t.Fatalf("eligible tag projection = %#v", projection.Components[0])
	}
	if _, err := CanonicalPortableToolPythonProjectionBytesV1(projection); err != nil {
		t.Fatalf("canonical projection rejected exact metadata outside the tested envelope: %v", err)
	}
}

func TestProjectPortableToolPythonBindingsV1RejectsBundledComponentDrift(t *testing.T) {
	for _, test := range []struct {
		name      string
		component []any
	}{
		{name: "missing", component: []any{}},
		{name: "version mismatch", component: []any{map[string]any{"name": "runtime", "version": "2.0.0", "path": "demo/runtime"}}},
		{name: "path mismatch", component: []any{map[string]any{"name": "runtime", "version": "1.0.0", "path": "other/runtime"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := portableToolPythonPlanForTest(t, "application:demo", "one", "demo", "demo==1")
			contract := &plan.Tools[0].Responsibilities.BindingContracts[0]
			contract.Record.Value["bundled_components"] = []any{map[string]any{
				"name": "runtime", "version": "1.0.0", "path": "demo/runtime",
			}}
			portableToolProjectionRebindRecordForTest(t, contract)
			artifact := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
			artifact.Record.Value["contract"] = map[string]any{
				"id": contract.Reference.ID, "digest": string(contract.Reference.Digest),
			}
			artifact.Record.Value["bundled_components"] = test.component
			portableToolProjectionRebindRecordForTest(t, artifact)
			if _, _, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{}); err == nil || !strings.Contains(err.Error(), "bundled component") {
				t.Fatalf("bundled-component drift error = %v", err)
			}
		})
	}
}

func TestCanonicalPortableToolPythonProjectionBytesV1RejectsBindingDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*PortableToolPythonProjectionV1)
		want   string
	}{
		{
			name: "malformed CLI name",
			mutate: func(projection *PortableToolPythonProjectionV1) {
				projection.Components[0].Bindings[0].CLI.Name = "../escape"
			},
			want: "binding CLI must use a canonical name and absolute path",
		},
		{
			name: "root CLI path",
			mutate: func(projection *PortableToolPythonProjectionV1) {
				projection.Components[0].Bindings[0].CLI.Path = "/"
			},
			want: "binding CLI must use a canonical name and absolute path",
		},
		{
			name: "backslash CLI path",
			mutate: func(projection *PortableToolPythonProjectionV1) {
				projection.Components[0].Bindings[0].CLI.Path = `/opt\demo`
			},
			want: "binding CLI must use a canonical name and absolute path",
		},
		{
			name: "noncanonical requires Python whitespace",
			mutate: func(projection *PortableToolPythonProjectionV1) {
				projection.Components[0].Bindings[0].Wheel.RequiresPython = " >=3.12,<3.13 "
			},
			want: "requires_python must be a canonical PEP 440 specifier set",
		},
		{
			name: "malformed contract reference ID",
			mutate: func(projection *PortableToolPythonProjectionV1) {
				projection.Components[0].Bindings[0].Contract.ID = "contract"
			},
			want: "exact record references are invalid",
		},
		{
			name: "swapped record reference categories",
			mutate: func(projection *PortableToolPythonProjectionV1) {
				binding := &projection.Components[0].Bindings[0]
				binding.Contract.ID, binding.Artifact.ID = binding.Artifact.ID, binding.Contract.ID
			},
			want: "exact record references are invalid",
		},
		{
			name: "unjoined record references",
			mutate: func(projection *PortableToolPythonProjectionV1) {
				projection.Components[0].Bindings[0].Artifact.ID = "tool:other/releases/1.0.0/bindings/python/artifacts/linux-amd64"
			},
			want: "exact record references are invalid",
		},
		{
			name: "artifact reference platform mismatch",
			mutate: func(projection *PortableToolPythonProjectionV1) {
				projection.Components[0].Bindings[0].Artifact.ID = "tool:one/releases/1.0.0/bindings/python/artifacts/linux-arm64"
			},
			want: "does not match platform",
		},
		{
			name: "empty bindings",
			mutate: func(projection *PortableToolPythonProjectionV1) {
				projection.Components[0].Bindings = []PortableToolPythonBindingV1{}
			},
			want: "at least one binding",
		},
		{
			name: "missing package root",
			mutate: func(projection *PortableToolPythonProjectionV1) {
				projection.Components[0].Bindings[0].Requirements = []string{"dependency==1"}
			},
			want: "no matching package root",
		},
		{
			name: "artifact version outside contract root",
			mutate: func(projection *PortableToolPythonProjectionV1) {
				projection.Components[0].Bindings[0].Wheel.EcosystemVersion = "2.0.0"
			},
			want: "does not satisfy contract requirement",
		},
		{
			name: "artifact version outside one of several roots",
			mutate: func(projection *PortableToolPythonProjectionV1) {
				projection.Components[0].Bindings[0].Requirements = []string{"demo==1", "demo==2"}
			},
			want: "same distribution",
		},
		{
			name: "incompatible exact wheel tag platform",
			mutate: func(projection *PortableToolPythonProjectionV1) {
				binding := &projection.Components[0].Bindings[0]
				binding.Wheel.Filename = "demo-1.0.0-py3-none-manylinux1_x86_64.manylinux_2_17_aarch64.whl"
				binding.Wheel.Tags = []string{"py3-none-manylinux1_x86_64", "py3-none-manylinux_2_17_aarch64"}
			},
			want: "incompatible with platform",
		},
		{
			name: "malformed wheel filename",
			mutate: func(projection *PortableToolPythonProjectionV1) {
				projection.Components[0].Bindings[0].Wheel.Filename = "other.whl"
			},
			want: "exact wheel filename",
		},
		{
			name: "wheel filename distribution mismatch",
			mutate: func(projection *PortableToolPythonProjectionV1) {
				projection.Components[0].Bindings[0].Wheel.Filename = "other-1.0.0-py3-none-manylinux1_x86_64.whl"
			},
			want: "does not match its distribution, ecosystem version, and tags",
		},
		{
			name: "wheel filename version mismatch",
			mutate: func(projection *PortableToolPythonProjectionV1) {
				projection.Components[0].Bindings[0].Wheel.Filename = "demo-2.0.0-py3-none-manylinux1_x86_64.whl"
			},
			want: "does not match its distribution, ecosystem version, and tags",
		},
		{
			name: "wheel filename tag mismatch",
			mutate: func(projection *PortableToolPythonProjectionV1) {
				projection.Components[0].Bindings[0].Wheel.Filename = "demo-1.0.0-py2-none-manylinux1_x86_64.whl"
			},
			want: "does not match its distribution, ecosystem version, and tags",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := portableToolPythonPlanForTest(t, "application:demo", "one", "demo", "demo==1")
			_, projection, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{})
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(&projection)
			if _, err := CanonicalPortableToolPythonProjectionBytesV1(projection); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("drift error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCanonicalPortableToolPythonProjectionBytesV1RejectsOversizedRecordCollections(t *testing.T) {
	oversized := make([]string, portabletool.RecordArrayMaxEntriesV1+1)
	for index := range oversized {
		oversized[index] = fmt.Sprintf("x%04d", index)
	}
	for _, test := range []struct {
		name   string
		mutate func(*PortableToolPythonProjectionV1)
	}{
		{name: "tested tags", mutate: func(projection *PortableToolPythonProjectionV1) {
			projection.Components[0].TestedTags = append([]string{}, oversized...)
		}},
		{name: "requirements", mutate: func(projection *PortableToolPythonProjectionV1) {
			projection.Components[0].Bindings[0].Requirements = append([]string{}, oversized...)
		}},
		{name: "supported Python", mutate: func(projection *PortableToolPythonProjectionV1) {
			projection.Components[0].Bindings[0].SupportedPython = append([]string{}, oversized...)
		}},
		{name: "supported tags", mutate: func(projection *PortableToolPythonProjectionV1) {
			projection.Components[0].Bindings[0].SupportedTags = append([]string{}, oversized...)
		}},
		{name: "wheel tags", mutate: func(projection *PortableToolPythonProjectionV1) {
			projection.Components[0].Bindings[0].Wheel.Tags = append([]string{}, oversized...)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := portableToolPythonPlanForTest(t, "application:demo", "one", "demo", "demo==1")
			_, projection, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{})
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(&projection)
			if _, err := CanonicalPortableToolPythonProjectionBytesV1(projection); err == nil || !strings.Contains(err.Error(), "record limit") {
				t.Fatalf("oversized collection error = %v", err)
			}
		})
	}
}

func portableToolPythonPlanForTest(t *testing.T, scope, tool, distribution, requirement string) providers.PortableToolPlanV1 {
	t.Helper()
	namespace := "tool:" + tool + "/releases/1.0.0"
	contract := portabletool.BindingContractV1{
		Schema: portabletool.BindingContractSchemaV1, ID: namespace + "/bindings/python/contract",
		Name: "python", Package: distribution, Requirements: []string{requirement},
		SupportedPython: []string{"3.12"}, SupportedTags: []string{"py3-none-manylinux1_x86_64"},
		BundledComponents: []portabletool.BundledComponentV1{},
		CLI:               portabletool.ToolExportV1{Name: "demo", Path: "/opt/demo/bin/demo"},
	}
	contractRecord := portableToolProjectionRecordForTest(t, contract)
	contractReference := portableToolProjectionReferenceForTest(t, contract.ID, contractRecord)
	artifact := portabletool.BindingArtifactRecordV1{
		Schema: portabletool.BindingArtifactSchemaV1, ID: namespace + "/bindings/python/artifacts/linux-amd64",
		Binding: "python", Contract: contractReference, Name: distribution, EcosystemVersion: "1.0.0",
		Platform: "linux/amd64", Filename: strings.ReplaceAll(distribution, "-", "_") + "-1.0.0-py3-none-manylinux1_x86_64.whl",
		Size: "7", SHA256: portableToolProjectionTestDigest, Resolver: "https-sha256",
		Tags: []string{"py3-none-manylinux1_x86_64"}, RequiresPython: ">=3.12,<3.13",
		BundledComponents: []portabletool.BundledComponentV1{},
	}
	artifactRecord := portableToolProjectionRecordForTest(t, artifact)
	artifactReference := portableToolProjectionReferenceForTest(t, artifact.ID, artifactRecord)
	return providers.PortableToolPlanV1{
		Schema: providers.PortableToolPlanSchemaV1,
		Tools: []providers.PortableToolPlanEntryV1{{
			Scope: scope, SelectedClosureDigest: portableToolProjectionTestDigest,
			Provenance: providers.PortableToolReleaseProvenanceV1{Tool: tool, Version: "1.0.0", Revision: "1", ManifestDigest: portableToolProjectionTestDigest},
			Responsibilities: providers.PortableToolResponsibilitiesV1{
				BindingContracts: []providers.PortableToolSelectedRecordV1{{Reference: contractReference, Record: contractRecord}},
				BindingArtifacts: []providers.PortableToolSelectedRecordV1{{Reference: artifactReference, Record: artifactRecord}},
				Payloads:         []providers.PortableToolSelectedRecordV1{}, NativePackageSets: []providers.PortableToolSelectedRecordV1{},
			},
			Exports:            []providers.PortableToolExportV1{{Name: "demo", Path: "/opt/demo/bin/demo"}},
			ValidationProfiles: []providers.PortableToolValidationProfileV1{},
		}},
	}
}

func portableToolProjectionRecordForTest(t *testing.T, value any) providers.CanonicalProviderData {
	t.Helper()
	encoded, err := canonical.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object canonical.Object
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	schema, ok := object["schema"].(string)
	if !ok {
		t.Fatal("record schema is missing")
	}
	return providers.CanonicalProviderData{Schema: schema, Value: object}
}

func portableToolProjectionDefinitionRecordForTest(t *testing.T, filename string) providers.CanonicalProviderData {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var value canonical.Object
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	schema, ok := value["schema"].(string)
	if !ok {
		t.Fatal("definition record schema is missing")
	}
	return providers.CanonicalProviderData{Schema: schema, Value: value}
}

func portableToolProjectionReferenceForTest(t *testing.T, id string, record providers.CanonicalProviderData) providers.PortableToolRecordReferenceV1 {
	t.Helper()
	digest, err := canonical.Sum("portable-tool-record", portabletool.RecordIdentitySchemaV1, record.Value)
	if err != nil {
		t.Fatal(err)
	}
	return providers.PortableToolRecordReferenceV1{ID: id, Digest: digest}
}

func portableToolProjectionRebindRecordForTest(t *testing.T, selected *providers.PortableToolSelectedRecordV1) {
	t.Helper()
	digest, err := canonical.Sum("portable-tool-record", portabletool.RecordIdentitySchemaV1, selected.Record.Value)
	if err != nil {
		t.Fatal(err)
	}
	selected.Reference.Digest = digest
}
