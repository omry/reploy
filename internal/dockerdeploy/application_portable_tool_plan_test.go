package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

func applicationPortableDocumentForTest(t *testing.T, tools string) blueprint.Document {
	t.Helper()
	source, err := blueprint.Decode([]byte(`
blueprint:
  schema: 1
  version: test
  compatibility: {platforms: [linux/amd64]}
environment:
  id: demo
  base: {image: docker.io/library/debian:12-slim}
  applications:
    web:
      packages:
        os: [curl]
        tools: ` + tools + `
docker: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	document, err := blueprint.Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func applicationPortableInputForTest(t *testing.T, tools string) PlanApplicationPortableToolsInputV1 {
	t.Helper()
	base := testProbeImageDescriptor(t, "linux/amd64")
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	baseRequest, err := providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{
		Image: "docker.io/library/debian:12-slim", Exports: map[string]blueprint.BaseExecutableExport{
			"python": {Executable: "/usr/bin/python3"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	aptComponent := blueprint.ApplicationContributionID("web", blueprint.ContributionProviderOS)
	aptRequest, err := aptprovider.CanonicalProviderRequestV1(aptprovider.APTProviderRequestV1{
		Components: []aptprovider.APTComponentRequestV1{{
			Component: aptComponent, Packages: []blueprint.APTPackageRequest{{Name: "curl"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return PlanApplicationPortableToolsInputV1{
		Document: applicationPortableDocumentForTest(t, tools),
		Components: []providers.ResolvedComponentRequestV1{
			{Component: "base", Provider: blueprint.ComponentTypeBase, Request: baseRequest},
			{Component: aptComponent, Provider: blueprint.ComponentTypeAPT, Request: aptRequest},
		},
		Platform: base.Platform, Store: store, Base: base, ReployVersion: "0.7.0.dev1",
		FinalImageConfig: pythonConsumerTestImageConfig(),
	}
}

func stubApplicationPortableTargetForTest(t *testing.T, calls *int) {
	t.Helper()
	previous := observeApplicationPortableTargetV1
	t.Cleanup(func() { observeApplicationPortableTargetV1 = previous })
	observeApplicationPortableTargetV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor) (toolcatalog.TargetIdentityV1, error) {
		*calls++
		return sourceBuilderJavaTarget("debian", "12"), nil
	}
}

func TestPlanApplicationPortableToolsV1PreservesNoToolBuild(t *testing.T) {
	input := applicationPortableInputForTest(t, "[]")
	input.Components = nil
	input.Base = deploy.ImageDescriptor{}
	calls := 0
	stubApplicationPortableTargetForTest(t, &calls)
	plan, err := PlanApplicationPortableToolsV1(context.Background(), input)
	if err != nil || plan != nil || calls != 0 {
		t.Fatalf("plan = %#v, err = %v, observations = %d", plan, err, calls)
	}
}

func TestPlanApplicationPortableToolsV1RejectsInvalidPlanningInputs(t *testing.T) {
	input := applicationPortableInputForTest(t, "[{tool: playwright, version: 1.61.0, binding: python, select: {browser: [chromium]}}]")
	if _, err := PlanApplicationPortableToolsV1(nil, input); err == nil || !strings.Contains(err.Error(), "context") {
		t.Fatalf("nil context error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PlanApplicationPortableToolsV1(cancelled, input); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context error = %v", err)
	}
	bad := input
	bad.Components = nil
	if _, err := PlanApplicationPortableToolsV1(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "components must use an array") {
		t.Fatalf("missing components error = %v", err)
	}
	bad = input
	bad.Base = deploy.ImageDescriptor{}
	if _, err := PlanApplicationPortableToolsV1(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "base") {
		t.Fatalf("invalid base error = %v", err)
	}
	bad = input
	bad.Platform.Architecture = "arm64"
	if _, err := PlanApplicationPortableToolsV1(context.Background(), bad); err == nil {
		t.Fatal("platform mismatch passed")
	}
	bad = input
	bad.FinalImageConfig.Environment = nil
	if _, err := PlanApplicationPortableToolsV1(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "final image config") {
		t.Fatalf("invalid image config error = %v", err)
	}
	bad = input
	app := bad.Document.Environment.Applications["web"]
	app.Packages.Tools[0].Scope = "application:other"
	bad.Document.Environment.Applications["web"] = app
	if _, err := PlanApplicationPortableToolsV1(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "foreign") {
		t.Fatalf("foreign scope error = %v", err)
	}
}

func TestPlanApplicationPortableToolsV1RejectsBuildOnlyJavaBeforeAcquisition(t *testing.T) {
	input := applicationPortableInputForTest(t, "[tool:java==21]")
	calls := 0
	stubApplicationPortableTargetForTest(t, &calls)
	_, err := PlanApplicationPortableToolsV1(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), `context "runtime" is not supported`) || calls != 1 {
		t.Fatalf("Java runtime selection error = %v, observations = %d", err, calls)
	}
}

func TestPlanApplicationPortableToolsV1RejectsUnsupportedSelectionBeforeAcquisition(t *testing.T) {
	input := applicationPortableInputForTest(t, "[{tool: playwright, version: 1.61.0, binding: python, select: {browser: [unknown]}}]")
	calls := 0
	stubApplicationPortableTargetForTest(t, &calls)
	_, err := PlanApplicationPortableToolsV1(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), "selection") || calls != 1 {
		t.Fatalf("unsupported selection error = %v, observations = %d", err, calls)
	}
}

func TestPlanApplicationPortableToolsV1SealsExecutionAuthority(t *testing.T) {
	input := applicationPortableInputForTest(t, "[{tool: playwright, version: 1.61.0, binding: python, select: {browser: [chromium]}}]")
	calls := 0
	stubApplicationPortableTargetForTest(t, &calls)
	selected, err := PlanApplicationPortableToolsV1(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if selected.sealed == nil || selected.Snapshot.Digest == "" || selected.Target.VersionID != "12" {
		t.Fatalf("missing complete preflight seal: %#v", selected)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name   string
		change func(*ApplicationPortableToolPlanV1, *PreparedPythonGraphExecutionInput)
		want   string
	}{
		{"unchanged preflight", func(*ApplicationPortableToolPlanV1, *PreparedPythonGraphExecutionInput) {}, ""},
		{"mutable public views cannot replace the seal", func(plan *ApplicationPortableToolPlanV1, _ *PreparedPythonGraphExecutionInput) {
			plan.Plan.Schema = "forged-plan"
			plan.DAG.Schema = "forged-dag"
			plan.Closures = nil
			plan.ProjectedComponents = nil
			plan.PythonProjection.Schema = "forged-projection"
		}, ""},
		{"missing construction seal", func(plan *ApplicationPortableToolPlanV1, _ *PreparedPythonGraphExecutionInput) {
			plan.sealed = nil
		}, "sealed preflight selection"},
		{"same-platform base replaced", func(_ *ApplicationPortableToolPlanV1, graph *PreparedPythonGraphExecutionInput) {
			graph.BaseDescriptor.AuthorReference = "docker.io/library/ubuntu:24.04"
		}, "execution base differs"},
		{"image configuration changed", func(_ *ApplicationPortableToolPlanV1, graph *PreparedPythonGraphExecutionInput) {
			graph.FinalImageConfig.Environment = append(graph.FinalImageConfig.Environment,
				providers.EnvironmentVariable{Name: "ADDED_AFTER_PREFLIGHT", Value: "yes"})
		}, "execution image config differs"},
		{"target view changed", func(plan *ApplicationPortableToolPlanV1, _ *PreparedPythonGraphExecutionInput) {
			plan.Target.VersionID = "13"
		}, "target or operation snapshot differs"},
		{"operation snapshot changed", func(plan *ApplicationPortableToolPlanV1, _ *PreparedPythonGraphExecutionInput) {
			plan.Snapshot.CanonicalJSON = "{}"
		}, "target or operation snapshot differs"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := *selected
			graph := PreparedPythonGraphExecutionInput{
				Plan: selected.DAG.ProviderPlan, BaseDescriptor: input.Base,
				FinalImageConfig: input.FinalImageConfig,
			}
			test.change(&plan, &graph)
			_, err := ExecuteApplicationPortablePythonGraphV1(cancelled, &plan, graph)
			if test.want == "" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("unchanged sealed preflight did not reach graph boundary: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("preflight drift error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPlanApplicationPortableToolsV1SelectsRuntimeAndProjectsOrdinaryClaims(t *testing.T) {
	input := applicationPortableInputForTest(t, "[{tool: playwright, version: 1.61.0, binding: python, select: {browser: [chromium]}}]")
	component := blueprint.ApplicationContributionID("web", blueprint.ContributionProviderPython)
	root, err := pythonprovider.CanonicalPackageRequestV1("ordinary==1.0")
	if err != nil {
		t.Fatal(err)
	}
	request, err := pythonprovider.CanonicalProviderRequestV1(pythonprovider.PythonProviderRequestV1{
		Component: component, Interpreter: blueprint.CommandRequirement{Command: "python", Supplier: "base"},
		Requirements: []providers.CanonicalPackageRequest{root}, Overrides: []pythonprovider.PythonPackageOverrideV1{},
	})
	if err != nil {
		t.Fatal(err)
	}
	input.Components = append(input.Components, providers.ResolvedComponentRequestV1{
		Component: component, Provider: blueprint.ComponentTypePython, Request: request,
	})
	input.FinalImageConfig.Environment = []providers.EnvironmentVariable{{Name: "ORDINARY_SETTING", Value: "active"}}
	calls := 0
	stubApplicationPortableTargetForTest(t, &calls)
	plan, err := PlanApplicationPortableToolsV1(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil || len(plan.Plan.Tools) != 1 || plan.Plan.Tools[0].Scope != "application:web" ||
		len(plan.Closures) != 1 || calls != 1 {
		t.Fatalf("selected plan = %#v, observations = %d", plan, calls)
	}
	if !strings.Contains(plan.Snapshot.CanonicalJSON, `"ordinary==1.0"`) ||
		!strings.Contains(plan.Snapshot.CanonicalJSON, `"provider-plan"`) ||
		!strings.Contains(plan.Snapshot.CanonicalJSON, `"ORDINARY_SETTING"`) {
		t.Fatalf("ordinary contribution missing from immutable operation: %s", plan.Snapshot.CanonicalJSON)
	}
	runtimeRoot, err := pythonprovider.RuntimeRootV1(component)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Snapshot.CanonicalJSON, `"install_roots":["`+runtimeRoot+`"]`) {
		t.Fatalf("ordinary Python runtime root missing from immutable operation: %s", plan.Snapshot.CanonicalJSON)
	}
	if err := providers.ValidatePortableToolProviderDAGV1(plan.DAG); err != nil {
		t.Fatal(err)
	}
}

func TestPlanApplicationPortableToolsV1KeepsScopesAndOperationDeterministic(t *testing.T) {
	input := applicationPortableInputForTest(t, "[{tool: playwright, version: 1.61.0, binding: python, select: {browser: [chromium]}}]")
	calls := 0
	stubApplicationPortableTargetForTest(t, &calls)
	first, err := PlanApplicationPortableToolsV1(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PlanApplicationPortableToolsV1(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Plan.Tools) != 1 || first.Plan.Tools[0].Scope != "application:web" ||
		first.Snapshot.Digest != second.Snapshot.Digest || !reflect.DeepEqual(first.DAG, second.DAG) || calls != 2 {
		t.Fatalf("scope or deterministic operation mismatch: first %#v, second %#v, calls %d", first, second, calls)
	}
}

func TestApplicationActiveBindingsByScopeV1DoesNotBorrowAnotherApplication(t *testing.T) {
	scopes := []string{"application:api", "application:web"}
	components := []providers.ResolvedComponentRequestV1{{
		Component: blueprint.ApplicationContributionID("web", blueprint.ContributionProviderPython),
		Provider:  blueprint.ComponentTypePython,
	}}
	got := applicationActiveBindingsByScopeV1(scopes, components)
	if !reflect.DeepEqual(got["application:web"], []string{blueprint.ContributionProviderPython}) ||
		!reflect.DeepEqual(got["application:api"], []string{}) {
		t.Fatalf("application-scoped active bindings = %#v", got)
	}
}

func TestPlanApplicationPortableToolsV1RejectsConflictingOrdinaryAPTBeforeAcquisition(t *testing.T) {
	input := applicationPortableInputForTest(t, "[{tool: playwright, version: 1.61.0, binding: python, select: {browser: [chromium]}}]")
	component := blueprint.ApplicationContributionID("web", blueprint.ContributionProviderOS)
	request, err := aptprovider.CanonicalProviderRequestV1(aptprovider.APTProviderRequestV1{
		Components: []aptprovider.APTComponentRequestV1{{
			Component: component, Packages: []blueprint.APTPackageRequest{{Name: "libnss3", Version: "9999"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	input.Components[1] = providers.ResolvedComponentRequestV1{
		Component: component, Provider: blueprint.ComponentTypeAPT, Request: request,
	}
	calls := 0
	stubApplicationPortableTargetForTest(t, &calls)
	if _, err := PlanApplicationPortableToolsV1(context.Background(), input); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("conflicting ordinary APT claim error = %v", err)
	}
}

func TestPlanApplicationPortableToolsV1RejectsConflictingOrdinaryPythonBeforeAcquisition(t *testing.T) {
	input := applicationPortableInputForTest(t, "[{tool: playwright, version: 1.61.0, binding: python, select: {browser: [chromium]}}]")
	component := blueprint.ApplicationContributionID("web", blueprint.ContributionProviderPython)
	root, err := pythonprovider.CanonicalPackageRequestV1("playwright==9999")
	if err != nil {
		t.Fatal(err)
	}
	request, err := pythonprovider.CanonicalProviderRequestV1(pythonprovider.PythonProviderRequestV1{
		Component: component, Interpreter: blueprint.CommandRequirement{Command: "python", Supplier: "base"},
		Requirements: []providers.CanonicalPackageRequest{root}, Overrides: []pythonprovider.PythonPackageOverrideV1{},
	})
	if err != nil {
		t.Fatal(err)
	}
	input.Components = append(input.Components, providers.ResolvedComponentRequestV1{
		Component: component, Provider: blueprint.ComponentTypePython, Request: request,
	})
	calls := 0
	stubApplicationPortableTargetForTest(t, &calls)
	if _, err := PlanApplicationPortableToolsV1(context.Background(), input); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("conflicting ordinary Python claim error = %v", err)
	}
}

func TestPlanApplicationPortableToolsV1PreflightsOrdinaryPythonSourceForms(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		requirement string
		wantError   string
	}{
		{"extras and marker", "ordinary[extra]>=1; python_version < '4'", ""},
		{"unrelated direct wheel", "ordinary @ https://example.invalid/ordinary-1.5-py3-none-any.whl", ""},
		{"unrelated direct archive", "ordinary @ https://example.invalid/ordinary.tar.gz", ""},
		{"unrelated local wheel", "./ordinary-1.5-py3-none-any.whl", ""},
		{"selected direct wheel", "playwright @ https://example.invalid/playwright-1.61.0-py3-none-any.whl", "Python source requirement conflict"},
		{"selected local wheel", "./playwright-1.61.0-py3-none-any.whl", "Python source requirement conflict"},
		{"unverifiable direct source", "https://example.invalid/unknown.tar.gz", "unverifiable"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			input := applicationPortableInputForTest(t, "[{tool: playwright, version: 1.61.0, binding: python, select: {browser: [chromium]}}]")
			component := blueprint.ApplicationContributionID("web", blueprint.ContributionProviderPython)
			root, err := pythonprovider.CanonicalPackageRequestV1(testCase.requirement)
			if err != nil {
				t.Fatal(err)
			}
			request, err := pythonprovider.CanonicalProviderRequestV1(pythonprovider.PythonProviderRequestV1{
				Component: component, Interpreter: blueprint.CommandRequirement{Command: "python", Supplier: "base"},
				Requirements: []providers.CanonicalPackageRequest{root}, Overrides: []pythonprovider.PythonPackageOverrideV1{},
			})
			if err != nil {
				t.Fatal(err)
			}
			input.Components = append(input.Components, providers.ResolvedComponentRequestV1{
				Component: component, Provider: blueprint.ComponentTypePython, Request: request,
			})
			calls := 0
			stubApplicationPortableTargetForTest(t, &calls)
			plan, err := PlanApplicationPortableToolsV1(context.Background(), input)
			if testCase.wantError == "" {
				if err != nil || plan == nil {
					t.Fatalf("planning ordinary Python requirement %q: plan %v, error %v", testCase.requirement, plan, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), testCase.wantError) || plan != nil {
				t.Fatalf("planning ordinary Python requirement %q: plan %v, error %v; want %q", testCase.requirement, plan, err, testCase.wantError)
			}
			if calls != 1 {
				t.Fatalf("target observations = %d, want one", calls)
			}
		})
	}
}

func TestPlanApplicationPortableToolsV1RejectsOrdinaryExportConflict(t *testing.T) {
	input := applicationPortableInputForTest(t, "[{tool: playwright, version: 1.61.0, binding: python, select: {browser: [chromium]}}]")
	baseRequest, err := providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{
		Image: "docker.io/library/debian:12-slim", Exports: map[string]blueprint.BaseExecutableExport{
			"python":     {Executable: "/usr/bin/python3"},
			"playwright": {Executable: "/usr/bin/unrelated-playwright"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	input.Components[0].Request = baseRequest
	calls := 0
	stubApplicationPortableTargetForTest(t, &calls)
	if _, err := PlanApplicationPortableToolsV1(context.Background(), input); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("ordinary export conflict error = %v", err)
	}
}

func TestPlanApplicationPortableToolsV1RejectsFinalImageEnvironmentConflict(t *testing.T) {
	input := applicationPortableInputForTest(t, "[{tool: playwright, version: 1.61.0, binding: python, select: {browser: [chromium]}}]")
	input.FinalImageConfig.Environment = []providers.EnvironmentVariable{{
		Name: "PLAYWRIGHT_BROWSERS_PATH", Value: "/opt/unrelated-browsers",
	}}
	calls := 0
	stubApplicationPortableTargetForTest(t, &calls)
	if _, err := PlanApplicationPortableToolsV1(context.Background(), input); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("ordinary environment conflict error = %v", err)
	}
}

func TestPlanApplicationPortableToolsV1RejectsIncompatibleOrdinaryPythonInterpreter(t *testing.T) {
	for _, test := range []struct {
		version string
		reject  bool
	}{
		{version: ">=3.15", reject: true},
		{version: "<3.14.0", reject: false},
		{version: ">=3.14.1,<3.15", reject: false},
		{version: ">=3.15,<3.16", reject: true},
	} {
		t.Run(test.version, func(t *testing.T) {
			input := applicationPortableInputForTest(t, "[{tool: playwright, version: 1.61.0, binding: python, select: {browser: [chromium]}}]")
			component := blueprint.ApplicationContributionID("web", blueprint.ContributionProviderPython)
			root, err := pythonprovider.CanonicalPackageRequestV1("ordinary==1.0")
			if err != nil {
				t.Fatal(err)
			}
			request, err := pythonprovider.CanonicalProviderRequestV1(pythonprovider.PythonProviderRequestV1{
				Component: component, Interpreter: blueprint.CommandRequirement{
					Command: "python", Version: test.version, Supplier: "base",
				},
				Requirements: []providers.CanonicalPackageRequest{root}, Overrides: []pythonprovider.PythonPackageOverrideV1{},
			})
			if err != nil {
				t.Fatal(err)
			}
			input.Components = append(input.Components, providers.ResolvedComponentRequestV1{
				Component: component, Provider: blueprint.ComponentTypePython, Request: request,
			})
			calls := 0
			stubApplicationPortableTargetForTest(t, &calls)
			_, err = PlanApplicationPortableToolsV1(context.Background(), input)
			if test.reject && (err == nil || !strings.Contains(err.Error(), "interpreter")) {
				t.Fatalf("incompatible ordinary interpreter error = %v", err)
			}
			if !test.reject && err != nil {
				t.Fatalf("compatible ordinary interpreter error = %v", err)
			}
		})
	}
}

func TestApplicationOrdinaryProviderConstraintsDedupeAndRejectExecutablePathClaimsV1(t *testing.T) {
	declaration := providers.OutputDeclaration{
		SupplierComponent: blueprint.ApplicationContributionID("web", blueprint.ContributionProviderOS),
		Name:              "demo",
		Kind:              providers.OutputKindExecutable,
		CandidatePath:     "/opt/demo/bin/demo",
	}
	plan := providers.ProviderPlanV1{
		Schema: providers.ProviderPlanSchemaV1,
		Nodes: []providers.NodeSpec{
			{ID: "node-a", OutputDeclarations: []providers.OutputDeclaration{declaration}},
			{ID: "node-b", OutputDeclarations: []providers.OutputDeclaration{{
				SupplierComponent: declaration.SupplierComponent, Name: "demo-alias",
				Kind: declaration.Kind, CandidatePath: declaration.CandidatePath,
			}}},
		},
		Edges: []providers.ProviderEdgeV1{},
	}
	active, err := applicationOrdinaryProviderConstraintsV1(
		[]string{"application:web"}, nil, plan, providers.ImageConfigPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	exports, ownedPaths := 0, 0
	for _, source := range active.Sources {
		exports += len(source.Exports)
		ownedPaths += len(source.OwnedPaths)
	}
	if exports != 2 || ownedPaths != 1 {
		t.Fatalf("deduped executable claims = %#v, want two exports and one owned path", active.Sources)
	}

	conflicting := declaration
	conflicting.CandidatePath = "/opt/other/bin/demo"
	plan.Nodes[1].OutputDeclarations = []providers.OutputDeclaration{conflicting}
	if _, err := applicationOrdinaryProviderConstraintsV1(
		[]string{"application:web"}, nil, plan, providers.ImageConfigPolicy{}); err == nil ||
		!strings.Contains(err.Error(), `ordinary executable export "demo" has conflicting paths`) {
		t.Fatalf("same-name executable path conflict = %v", err)
	}
}

func TestPlanApplicationPortableToolsV1AllowsDistinctOrdinaryExportAliasesV1(t *testing.T) {
	input := applicationPortableInputForTest(t, "[{tool: playwright, version: 1.61.0, binding: python, select: {browser: [chromium]}}]")
	baseRequest, err := providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{
		Image: "docker.io/library/debian:12-slim", Exports: map[string]blueprint.BaseExecutableExport{
			"python":  {Executable: "/usr/bin/python3"},
			"python3": {Executable: "/usr/bin/python3"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	input.Components[0].Request = baseRequest
	calls := 0
	stubApplicationPortableTargetForTest(t, &calls)
	plan, err := PlanApplicationPortableToolsV1(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil || calls != 1 {
		t.Fatalf("alias plan = %#v, observations = %d", plan, calls)
	}
	if !strings.Contains(plan.Snapshot.CanonicalJSON, `"name":"python"`) ||
		!strings.Contains(plan.Snapshot.CanonicalJSON, `"name":"python3"`) {
		t.Fatalf("snapshot omitted one of the semantic export names: %s", plan.Snapshot.CanonicalJSON)
	}
	if strings.Count(plan.Snapshot.CanonicalJSON, `"owned_paths":[{"digest"`) != 1 {
		t.Fatalf("snapshot should retain one canonical filesystem claim for aliases: %s", plan.Snapshot.CanonicalJSON)
	}
}
