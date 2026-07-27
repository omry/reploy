package dockerdeploy

import (
	"context"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
)

func TestPlanFullImageValidationProbeCombinesAndDeduplicatesPaths(t *testing.T) {
	completion, operation, _ := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	input := completion.Validation.Final
	if len(input.Profiles) != 1 || len(input.Outputs) == 0 {
		t.Fatalf("fixture profiles/outputs = %d/%d", len(input.Profiles), len(input.Outputs))
	}
	input.Profiles = append([]providers.RequirementProfile{testAPTFullValidationProfile(t, input)}, input.Profiles...)
	duplicate := input.Outputs[0]
	duplicate.SupplierComponent = "duplicate"
	duplicate.Name = "same_path"
	duplicate.Evidence.Output = providers.QualifiedOutput{Component: duplicate.SupplierComponent, Name: duplicate.Name}
	input.Outputs = append(input.Outputs, duplicate)

	plan, err := planFullImageValidationProbe(input)
	if err != nil {
		t.Fatal(err)
	}
	if plan.APT == nil || plan.APT.ProfileIndex != 0 || len(plan.PythonProfiles) != 1 || len(plan.Outputs) != len(input.Outputs) {
		t.Fatalf("probe plan = %#v", plan)
	}
	pathByID := map[string]string{}
	seenPaths := map[string]bool{}
	for _, inspection := range plan.Request.Inspections {
		if seenPaths[inspection.InvocationPath] {
			t.Fatalf("path %q was scheduled more than once", inspection.InvocationPath)
		}
		seenPaths[inspection.InvocationPath] = true
		pathByID[inspection.ID] = inspection.InvocationPath
	}
	for _, tool := range aptprovider.RequiredBaseToolsV1() {
		id := plan.APT.ToolInspection[tool.Name]
		if pathByID[id] != tool.Path {
			t.Fatalf("APT tool %q maps to %q, want %q", tool.Name, pathByID[id], tool.Path)
		}
	}
	python := plan.PythonProfiles[0]
	if python.LauncherInspection != plan.LauncherInspection || python.LauncherInspection != plan.APT.ToolInspection["env"] || pathByID[python.InterpreterInspection] != input.Profiles[python.ProfileIndex].SelectedExecutables[0].InvocationPath {
		t.Fatalf("Python probe mapping = %#v", python)
	}
	if pathByID[plan.CarrierInspection] != pythonCarrierPath || pathByID[plan.LauncherInspection] != pythonLauncherPath {
		t.Fatalf("backend baseline mappings = %#v", plan)
	}
	if plan.Outputs[0].Binding.Output.Component > plan.Outputs[1].Binding.Output.Component {
		t.Fatalf("output checks are not sorted: %#v", plan.Outputs)
	}
	firstPath := input.Outputs[0].Candidate.InvocationPath
	shared := []string{}
	for _, output := range plan.Outputs {
		if pathByID[output.InspectionID] == firstPath {
			shared = append(shared, output.InspectionID)
		}
	}
	if len(shared) != 2 || shared[0] != shared[1] {
		t.Fatalf("duplicate output path mappings = %#v", shared)
	}
}

func TestPlanFullImageValidationProbeKeepsBackendBaselineForBaseOnlyImage(t *testing.T) {
	input := fullValidationInput(t, "7")
	plan, err := planFullImageValidationProbe(input)
	if err != nil {
		t.Fatal(err)
	}
	if plan.APT != nil || len(plan.Request.Inspections) != 2 || plan.CarrierInspection == "" || plan.LauncherInspection == "" || !reflect.DeepEqual(plan.PythonProfiles, []plannedPythonProfileProbe{}) || !reflect.DeepEqual(plan.Outputs, []plannedOutputProbe{}) {
		t.Fatalf("base-only probe plan = %#v", plan)
	}
}

func testAPTFullValidationProfile(t *testing.T, input FullImageValidationInput) providers.RequirementProfile {
	t.Helper()
	outputs := map[string][]byte{
		"/bin/sh\x00-c\x00" + aptOSReleaseProbeScriptV1:  []byte("ID\x00debian\x00VERSION_ID\x0013\x00"),
		"/usr/bin/apt-get\x00--version":                  []byte("apt 3.0.3 (amd64)\n"),
		"/usr/bin/dpkg\x00--version":                     []byte("Debian 'dpkg' package management program version 1.22.21 (amd64).\n"),
		"/usr/bin/dpkg-deb\x00--version":                 []byte("Debian 'dpkg-deb' package archive backend version 1.22.21 (amd64).\n"),
		"/usr/bin/dpkg-query\x00--version":               []byte("Debian dpkg-query package management program query tool version 1.22.21 (amd64).\n"),
		"/usr/bin/sha256sum\x00--version":                []byte("sha256sum (GNU coreutils) 9.5\n"),
		"/usr/bin/dpkg\x00--print-architecture":          []byte("amd64\n"),
		"/usr/bin/dpkg\x00--print-foreign-architectures": {},
	}
	base, err := observeAPTBaseProfile(
		context.Background(), input.Image.Descriptor.Platform,
		func(context.Context, probe.RequestV1) (probe.ResponseV1, error) { return aptBaseProbeResponse(), nil },
		func(_ context.Context, executable string, arguments ...string) ([]byte, error) {
			key := executable
			for _, argument := range arguments {
				key += "\x00" + argument
			}
			return outputs[key], nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := aptprovider.CanonicalProfileFactsV1(base.Profile, base.Executables)
	if err != nil {
		t.Fatal(err)
	}
	node := aptResolverTestNode(t, aptResolverTestRequest(t, blueprint.APTPackageRequest{Name: "hello", Exports: map[string]blueprint.ExecutableExport{}}))
	profile := providers.RequirementProfile{
		Schema: providers.RequirementProfileSchemaV1, Provider: blueprint.ComponentTypeAPT, Declaration: node.Requirements,
		SelectedExecutables: []providers.ExecutableEvidence{}, SelectedFiles: []providers.FileEvidence{},
		Platform: input.Image.Descriptor.Platform, Facts: facts,
	}
	if err := aptprovider.ValidateRequirementProfileV1(profile); err != nil {
		t.Fatal(err)
	}
	return profile
}
