package python

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/portabletool"
	"github.com/omry/reploy/internal/providers"
)

func TestValidatePortableToolPythonBindingsV1UsesObservedPipMembership(t *testing.T) {
	tests := []struct {
		name           string
		tag            string
		platform       string
		implementation string
		abi            string
		member         bool
		want           bool
	}{
		{name: "older generic tag", tag: "py39-none-any", platform: "linux/amd64", implementation: "cpython", abi: "cp313", member: true, want: true},
		{name: "CPython exact ABI", tag: "cp313-cp313-linux_x86_64", platform: "linux/amd64", implementation: "cpython", abi: "cp313", member: true, want: true},
		{name: "stable ABI", tag: "cp32-abi3-manylinux_2_17_x86_64", platform: "linux/amd64", implementation: "cpython", abi: "cp313", member: true, want: true},
		{name: "debug CPython stable ABI", tag: "cp32-abi3-manylinux_2_17_x86_64", platform: "linux/amd64", implementation: "cpython", abi: "cp313d", member: true, want: true},
		{name: "free threaded CPython rejects stable ABI", tag: "cp32-abi3-manylinux_2_17_x86_64", platform: "linux/amd64", implementation: "cpython", abi: "cp313t", member: false, want: false},
		{name: "nonmatching implementation", tag: "cp313-cp313-linux_x86_64", platform: "linux/amd64", implementation: "pypy", abi: "pypy313", member: false, want: false},
		{name: "implementation diagnostic does not override membership", tag: "cp313-cp313-linux_x86_64", platform: "linux/amd64", implementation: "pypy", abi: "pypy313", member: true, want: true},
		{name: "nonmatching ABI", tag: "cp313-cp313-linux_x86_64", platform: "linux/amd64", implementation: "cpython", abi: "cp313d", member: false, want: false},
		{name: "ABI diagnostic does not override membership", tag: "cp313-cp313-linux_x86_64", platform: "linux/amd64", implementation: "cpython", abi: "cp313d", member: true, want: true},
		{name: "generic any", tag: "py3-none-any", platform: "linux/amd64", implementation: "cpython", abi: "cp313", member: true, want: true},
		{name: "CPython any", tag: "cp313-none-any", platform: "linux/amd64", implementation: "cpython", abi: "cp313", member: true, want: true},
		{name: "native Linux", tag: "py3-none-linux_x86_64", platform: "linux/amd64", implementation: "cpython", abi: "cp313", member: true, want: true},
		{name: "manylinux1", tag: "py3-none-manylinux1_x86_64", platform: "linux/amd64", implementation: "cpython", abi: "cp313", member: true, want: true},
		{name: "manylinux2010", tag: "py3-none-manylinux2010_x86_64", platform: "linux/amd64", implementation: "cpython", abi: "cp313", member: true, want: true},
		{name: "manylinux2014", tag: "py3-none-manylinux2014_x86_64", platform: "linux/amd64", implementation: "cpython", abi: "cp313", member: true, want: true},
		{name: "versioned manylinux below runtime", tag: "py3-none-manylinux_2_17_x86_64", platform: "linux/amd64", implementation: "cpython", abi: "cp313", member: true, want: true},
		{name: "versioned manylinux equal runtime", tag: "py3-none-manylinux_2_35_x86_64", platform: "linux/amd64", implementation: "cpython", abi: "cp313", member: true, want: true},
		{name: "versioned manylinux above runtime", tag: "py3-none-manylinux_2_36_x86_64", platform: "linux/amd64", implementation: "cpython", abi: "cp313", member: false, want: false},
		{name: "glibc minor diagnostic does not override membership", tag: "py3-none-manylinux_2_36_x86_64", platform: "linux/amd64", implementation: "cpython", abi: "cp313", member: true, want: true},
		{name: "ARM64 manylinux floor", tag: "py3-none-manylinux_2_17_aarch64", platform: "linux/arm64", implementation: "cpython", abi: "cp313", member: true, want: true},
		{name: "ARM64 manylinux2014", tag: "py3-none-manylinux2014_aarch64", platform: "linux/arm64", implementation: "cpython", abi: "cp313", member: true, want: true},
		{name: "pip policy hook rejection", tag: "py3-none-manylinux_2_17_x86_64", platform: "linux/amd64", implementation: "cpython", abi: "cp313", member: false, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			component := portablePythonEligibilityComponentForTest(test.tag, test.platform)
			facts := portablePythonEligibilityFactsForTest(component.TestedTags, test.implementation, test.abi)
			if test.member {
				facts.CompatibleTags = append([]string{}, component.TestedTags...)
			}
			platform, err := blueprint.ParsePlatform(test.platform)
			if err != nil {
				t.Fatal(err)
			}
			err = ValidatePortableToolPythonBindingsV1(
				component,
				providers.ExecutableEvidence{Facts: CanonicalInterpreterFactsV2(facts)},
				platform,
			)
			if (err == nil) != test.want {
				t.Fatalf("eligibility error = %v, want eligible %v", err, test.want)
			}
		})
	}
}

func TestValidatePortableToolPythonBindingsV1MatchesInstalledPipTagDecisions(t *testing.T) {
	program := `import json
from pip._vendor.packaging import tags
as_set=lambda values:set(map(str,values))
result={
 "generic older": "py39-none-any" in as_set(tags.compatible_tags((3,13),"cp313",["any"])),
 "CPython any": "cp313-none-any" in as_set(tags.compatible_tags((3,13),"cp313",["any"])),
 "CPython exact": "cp313-cp313-linux_x86_64" in as_set(tags.cpython_tags((3,13),["cp313"],["linux_x86_64"])),
 "stable ABI": "cp32-abi3-manylinux_2_17_x86_64" in as_set(tags.cpython_tags((3,13),["cp313"],["manylinux_2_17_x86_64"])),
 "nonmatching implementation": "cp313-cp313-linux_x86_64" in as_set(tags.generic_tags("pp313",["pypy313"],["linux_x86_64"])),
 "nonmatching platform": "cp313-cp313-linux_aarch64" in as_set(tags.cpython_tags((3,13),["cp313"],["linux_x86_64"])),
}
print(json.dumps(result,sort_keys=True,separators=(",",":")))`
	output, err := exec.Command(isolatedPythonForTest(t), "-I", "-c", program).CombinedOutput()
	if err != nil {
		t.Fatalf("generate installed-pip decisions: %v: %s", err, output)
	}
	decisions := map[string]bool{}
	if err := json.Unmarshal(output, &decisions); err != nil {
		t.Fatalf("decode installed-pip decisions: %v: %s", err, output)
	}
	cases := map[string]struct {
		tag      string
		platform string
	}{
		"generic older":              {tag: "py39-none-any", platform: "linux/amd64"},
		"CPython any":                {tag: "cp313-none-any", platform: "linux/amd64"},
		"CPython exact":              {tag: "cp313-cp313-linux_x86_64", platform: "linux/amd64"},
		"stable ABI":                 {tag: "cp32-abi3-manylinux_2_17_x86_64", platform: "linux/amd64"},
		"nonmatching implementation": {tag: "cp313-cp313-linux_x86_64", platform: "linux/amd64"},
		"nonmatching platform":       {tag: "cp313-cp313-linux_aarch64", platform: "linux/arm64"},
	}
	if len(decisions) != len(cases) {
		t.Fatalf("installed-pip decisions = %#v", decisions)
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			member, found := decisions[name]
			if !found {
				t.Fatalf("installed-pip decision %q is missing", name)
			}
			component := portablePythonEligibilityComponentForTest(test.tag, test.platform)
			facts := portablePythonEligibilityFactsForTest(component.TestedTags, "cpython", "cp313")
			if member {
				facts.CompatibleTags = append([]string{}, component.TestedTags...)
			}
			platform, _ := blueprint.ParsePlatform(test.platform)
			err := ValidatePortableToolPythonBindingsV1(component, providers.ExecutableEvidence{Facts: CanonicalInterpreterFactsV2(facts)}, platform)
			if (err == nil) != member {
				t.Fatalf("eligibility error = %v, installed pip member %v", err, member)
			}
		})
	}
}

func TestValidatePortableToolPythonBindingsV1MatchesClaimsAndRequiresPython(t *testing.T) {
	tag := "py3-none-any"
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	tests := []struct {
		name            string
		version         string
		supportedPython []string
		requiresPython  string
		want            bool
	}{
		{name: "minor series", version: "3.13.2", supportedPython: []string{"3.13"}, requiresPython: ">=3.13,<3.14", want: true},
		{name: "exact release", version: "3.13.2", supportedPython: []string{"3.13.2"}, requiresPython: "==3.13.2", want: true},
		{name: "unsupported minor", version: "3.14.0", supportedPython: []string{"3.13"}, requiresPython: ">=3.13,<3.14", want: false},
		{name: "nonmatching exact release", version: "3.13.3", supportedPython: []string{"3.13.2"}, requiresPython: "==3.13.2", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			component := portablePythonEligibilityComponentForTest(tag, platform.Canonical)
			component.Bindings[0].SupportedPython = test.supportedPython
			component.Bindings[0].Wheel.RequiresPython = test.requiresPython
			facts := portablePythonEligibilityFactsForTest(component.TestedTags, "cpython", "cp313")
			facts.Version = test.version
			facts.CompatibleTags = append([]string{}, component.TestedTags...)
			err := ValidatePortableToolPythonBindingsV1(component, providers.ExecutableEvidence{Facts: CanonicalInterpreterFactsV2(facts)}, platform)
			if (err == nil) != test.want {
				t.Fatalf("eligibility error = %v, want eligible %v", err, test.want)
			}
		})
	}
}

func TestValidatePortableToolPythonBindingsV1AppliesOnlyBoundedRuntimeGuard(t *testing.T) {
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	for _, test := range []struct {
		name      string
		tag       string
		libc      string
		libcMajor string
		want      bool
	}{
		{name: "manylinux glibc two", tag: "py3-none-manylinux_2_17_x86_64", libc: "glibc", libcMajor: "2", want: true},
		{name: "manylinux unknown libc", tag: "py3-none-manylinux_2_17_x86_64", libc: "unknown", libcMajor: "0", want: false},
		{name: "manylinux non glibc", tag: "py3-none-manylinux_2_17_x86_64", libc: "musl", libcMajor: "1", want: false},
		{name: "manylinux other glibc major", tag: "py3-none-manylinux_2_17_x86_64", libc: "glibc", libcMajor: "3", want: false},
		{name: "any ignores libc", tag: "py3-none-any", libc: "musl", libcMajor: "1", want: true},
		{name: "native Linux ignores libc", tag: "py3-none-linux_x86_64", libc: "unknown", libcMajor: "0", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			component := portablePythonEligibilityComponentForTest(test.tag, platform.Canonical)
			facts := portablePythonEligibilityFactsForTest(component.TestedTags, "cpython", "cp313")
			facts.Libc, facts.LibcMajor = test.libc, test.libcMajor
			facts.CompatibleTags = append([]string{}, component.TestedTags...)
			err := ValidatePortableToolPythonBindingsV1(component, providers.ExecutableEvidence{Facts: CanonicalInterpreterFactsV2(facts)}, platform)
			if (err == nil) != test.want {
				t.Fatalf("eligibility error = %v, want eligible %v", err, test.want)
			}
		})
	}
}

func TestValidatePortableToolPythonBindingsV1AppliesManylinuxGuardPerCandidate(t *testing.T) {
	tags := []string{"py3-none-any", "py3-none-manylinux_2_17_x86_64"}
	component := portablePythonEligibilityComponentForTest(tags[0], "linux/amd64")
	component.TestedTags = append([]string{}, tags...)
	component.Bindings[0].SupportedTags = append([]string{}, tags...)
	component.Bindings[0].Wheel.Tags = append([]string{}, tags...)
	component.Bindings[0].Wheel.Filename = "demo-1-py3-none-any.manylinux_2_17_x86_64.whl"
	facts := portablePythonEligibilityFactsForTest(tags, "cpython", "cp313")
	facts.Libc, facts.LibcMajor = "musl", "1"
	facts.CompatibleTags = []string{"py3-none-any"}
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	if err := ValidatePortableToolPythonBindingsV1(component, providers.ExecutableEvidence{Facts: CanonicalInterpreterFactsV2(facts)}, platform); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePortableToolPythonBindingsV1FailsClosedAtItsBoundaries(t *testing.T) {
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	for _, test := range []struct {
		name string
		tag  string
	}{
		{name: "pre abi3 floor", tag: "cp31-abi3-linux_x86_64"},
		{name: "exact ABI any", tag: "cp313-cp313-any"},
		{name: "stable ABI any", tag: "cp313-abi3-any"},
		{name: "unsupported policy", tag: "py3-none-musllinux_1_2_x86_64"},
		{name: "below x86 floor", tag: "py3-none-manylinux_2_4_x86_64"},
		{name: "unsupported manylinux major", tag: "py3-none-manylinux_3_17_x86_64"},
	} {
		t.Run(test.name, func(t *testing.T) {
			component := portablePythonEligibilityComponentForTest(test.tag, platform.Canonical)
			facts := portablePythonEligibilityFactsForTest(component.TestedTags, "cpython", "cp313")
			facts.CompatibleTags = append([]string{}, component.TestedTags...)
			if err := ValidatePortableToolPythonBindingsV1(component, providers.ExecutableEvidence{Facts: CanonicalInterpreterFactsV2(facts)}, platform); err == nil {
				t.Fatal("unsupported tag was eligible")
			}
		})
	}

	component := portablePythonEligibilityComponentForTest("py3-none-any", platform.Canonical)
	facts := portablePythonEligibilityFactsForTest([]string{"py2-none-any"}, "cpython", "cp313")
	if err := ValidatePortableToolPythonBindingsV1(component, providers.ExecutableEvidence{Facts: CanonicalInterpreterFactsV2(facts)}, platform); err == nil || !strings.Contains(err.Error(), "tested tags") {
		t.Fatalf("tested-tag mismatch error = %v", err)
	}

	facts = portablePythonEligibilityFactsForTest(component.TestedTags, "cpython", "cp313")
	facts.CompatibleTags = append([]string{}, component.TestedTags...)
	arm64, _ := blueprint.ParsePlatform("linux/arm64")
	if err := ValidatePortableToolPythonBindingsV1(component, providers.ExecutableEvidence{Facts: CanonicalInterpreterFactsV2(facts)}, arm64); err == nil || !strings.Contains(err.Error(), "not \"linux/arm64\"") {
		t.Fatalf("target mismatch error = %v", err)
	}
}

func portablePythonEligibilityComponentForTest(tag, platform string) PortableToolPythonComponentV1 {
	digest := canonical.Digest("sha256:2222222222222222222222222222222222222222222222222222222222222222")
	component := "application/demo/python"
	return PortableToolPythonComponentV1{
		Component:  component,
		TestedTags: []string{tag},
		Bindings: []PortableToolPythonBindingV1{{
			Scope: "application:demo", Component: component, SelectedClosureDigest: digest, Distribution: "demo",
			Contract: providers.PortableToolRecordReferenceV1{ID: "tool:demo/releases/1/bindings/python/contract", Digest: digest},
			Artifact: providers.PortableToolRecordReferenceV1{
				ID: "tool:demo/releases/1/bindings/python/artifacts/" + strings.ReplaceAll(platform, "/", "-"), Digest: digest,
			},
			Requirements: []string{"demo==1"}, SupportedPython: []string{"3.13"}, SupportedTags: []string{tag},
			CLI: portabletool.ToolExportV1{Name: "demo", Path: "/opt/demo/bin/demo"},
			Wheel: PortableToolExactWheelConstraintV1{
				Platform: platform, Filename: fmt.Sprintf("demo-1-%s.whl", tag), Distribution: "demo",
				EcosystemVersion: "1", Tags: []string{tag}, Size: "1", SHA256: digest, RequiresPython: ">=3.13,<3.14",
			},
		}},
	}
}

func portablePythonEligibilityFactsForTest(testedTags []string, implementation, abi string) InterpreterInspectionFactsV2 {
	return InterpreterInspectionFactsV2{
		Version: "3.13.2", Implementation: implementation, ABI: abi,
		Libc: "glibc", LibcMajor: "2", LibcMinor: "35",
		TestedTags: append([]string{}, testedTags...), CompatibleTags: []string{},
	}
}
