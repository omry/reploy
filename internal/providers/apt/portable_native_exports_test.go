package apt

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/portabletool"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/toolcatalog"
)

func nativeExportPlanForTest(t *testing.T, context string) providers.PortableToolPlanV1 {
	t.Helper()
	cases, err := toolcatalog.EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.Manifest.Tool != "bash" || c.Manifest.Version != "5.2.15" || c.Support.Context != context || c.Target.Target.OCIArchitecture != "amd64" {
			continue
		}
		scope := "application:case"
		if context == "build" {
			scope = "source-builder:case"
		}
		closure, err := toolcatalog.EmbeddedSelectedClosureForIntegrationCaseV1(c, scope)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := toolcatalog.CompilePortableToolPlanV1([]toolcatalog.SelectedClosureV1{closure})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	t.Fatal("missing native export fixture")
	return providers.PortableToolPlanV1{}
}

func TestPortableNativeExportUsesOrdinaryExactAPTDeclaration(t *testing.T) {
	for _, context := range []string{"build", "runtime"} {
		t.Run(context, func(t *testing.T) {
			plan := nativeExportPlanForTest(t, context)
			var components []providers.ResolvedComponentRequestV1
			var err error
			if context == "build" {
				components, err = ProjectSourceBuilderPortableToolAPTRootsV1(plan)
			} else {
				components, err = ProjectPortableToolAPTRootsV1(plan, []providers.ResolvedComponentRequestV1{})
			}
			if err != nil {
				t.Fatal(err)
			}
			request, err := decodeCanonicalProviderRequestV1(components[0].Request)
			if err != nil {
				t.Fatal(err)
			}
			pkg := request.Components[0].Packages[0]
			if len(request.Components[0].Packages) != 1 || pkg.Name != "bash" || pkg.Version != "5.2.15-2+b13" || !reflect.DeepEqual(pkg.Exports, map[string]blueprint.ExecutableExport{"bash": {Executable: "/bin/bash"}}) {
				t.Fatalf("wrong exact package/export declaration: %#v", pkg)
			}
			want := "application/case/os"
			if context == "build" {
				want = "source-builder"
			}
			if components[0].Component != want {
				t.Fatal("builder roots leaked into application contribution")
			}
		})
	}
}

func TestPortableNativeExportRejectsAmbiguousUnpinnedAndConflictingRoots(t *testing.T) {
	for name, requirements := range map[string][]any{"ambiguous": {"bash=5.2.15-2+b13", "coreutils"}, "unpinned": {"bash"}} {
		t.Run(name, func(t *testing.T) {
			plan := nativeExportPlanForTest(t, "runtime")
			selected := &plan.Tools[0].Responsibilities.NativePackageSets[0]
			selected.Record.Value["requirements"] = requirements
			digest, err := canonical.Sum("portable-tool-record", portabletool.RecordIdentitySchemaV1, selected.Record.Value)
			if err != nil {
				t.Fatal(err)
			}
			selected.Reference.Digest = digest
			if _, err := ProjectPortableToolAPTRootsV1(plan, []providers.ResolvedComponentRequestV1{}); err == nil {
				t.Fatal("ambiguous or unpinned native export accepted")
			}
		})
	}
	plan := nativeExportPlanForTest(t, "runtime")
	ordinary := portableAPTComponentForTest(t, "application/case/os", "bash=5.2.15-2+b13")
	request, _ := decodeCanonicalProviderRequestV1(ordinary.Request)
	request.Components[0].Packages[0].Exports["bash"] = blueprint.ExecutableExport{Executable: "/unowned/bash"}
	ordinary.Request, _ = CanonicalProviderRequestV1(request)
	if _, err := ProjectPortableToolAPTRootsV1(plan, []providers.ResolvedComponentRequestV1{ordinary}); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("conflicting ordinary export accepted: %v", err)
	}
	ordinary = portableAPTComponentForTest(t, "application/case/os", "bash=99")
	if _, err := ProjectPortableToolAPTRootsV1(plan, []providers.ResolvedComponentRequestV1{ordinary}); err == nil {
		t.Fatal("conflicting package revision accepted")
	}
}

func TestPortableNativeExportMergesAndDeduplicatesWithoutChangingCaller(t *testing.T) {
	plan := nativeExportPlanForTest(t, "runtime")
	ordinary := []providers.ResolvedComponentRequestV1{portableAPTComponentForTest(t, "application/case/os", "bash=5.2.15-2+b13")}
	before, _ := canonical.Marshal(ordinary)
	first, err := ProjectPortableToolAPTRootsV1(plan, ordinary)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ProjectPortableToolAPTRootsV1(plan, first)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, again) {
		t.Fatal("native export projection is not idempotent")
	}
	after, _ := canonical.Marshal(ordinary)
	if string(before) != string(after) {
		t.Fatal("native projection mutated its caller")
	}
	build := nativeExportPlanForTest(t, "build")
	second := build.Tools[0]
	second.Scope = "source-builder:other"
	build.Tools = append(build.Tools, second)
	components, err := ProjectSourceBuilderPortableToolAPTRootsV1(build)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := decodeCanonicalProviderRequestV1(components[0].Request)
	if len(components) != 1 || len(request.Components[0].Packages) != 1 {
		t.Fatal("identical shared builder roots were duplicated")
	}
}
