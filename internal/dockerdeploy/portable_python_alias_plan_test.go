package dockerdeploy

import (
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

func portablePythonAliasPlanFixtureV1(
	t *testing.T,
	scope string,
) (pythonprovider.PortableToolPythonComponentV1, providers.MaterializationTransaction, providers.PortableToolPlanV1) {
	t.Helper()
	fresh := portableToolPythonFreshPlaywrightFixtureV1(t, scope)
	component := fresh.Projection.Components[0]
	transaction := rendererTransaction()
	transaction.NodeID = portablePythonAliasNodeIDV1(component.Component)
	transaction.RecipeVersion = pythonprovider.MaterializationRecipeVersion
	runtimeRoot, err := pythonprovider.RuntimeRootV1(component.Component)
	if err != nil {
		t.Fatal(err)
	}
	name := component.Bindings[0].CLI.Name
	transaction.GeneratedExecutables = []providers.GeneratedExecutableDeclaration{
		{
			ID: "output_" + name, Path: path.Join(runtimeRoot, "bin", name),
			ExclusiveRoot: runtimeRoot, ValidationPolicy: providers.ValidationPolicyCompatible,
		},
		{
			ID: "venv_python", Path: path.Join(runtimeRoot, "bin", "python"),
			ExclusiveRoot: runtimeRoot, ValidationPolicy: providers.ValidationPolicyCompatible,
		},
	}
	return component, transaction, fresh.Plan
}

func TestPlanPortablePythonAliasesV1JoinsExactGeneratedScript(t *testing.T) {
	component, transaction, _ := portablePythonAliasPlanFixtureV1(t, "application:alias-plan")
	aliases, err := planPortablePythonAliasesV1(&component, transaction)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 1 {
		t.Fatalf("aliases = %#v", aliases)
	}
	alias := aliases[0]
	runtimeRoot, err := pythonprovider.RuntimeRootV1(component.Component)
	if err != nil {
		t.Fatal(err)
	}
	wantTarget := path.Join(runtimeRoot, "bin", component.Bindings[0].CLI.Name)
	if alias.Name != component.Bindings[0].CLI.Name || alias.Component != component.Component || alias.Destination != component.Bindings[0].CLI.Path ||
		alias.Target != wantTarget || alias.Output != (providers.QualifiedOutput{Component: component.Component, Name: alias.Name}) {
		t.Fatalf("alias = %#v, want target %q", alias, wantTarget)
	}
	if !strings.HasPrefix(alias.BindingIdentity, "sha256:") || alias.Facts.Schema != "portable-python-alias-v1" ||
		alias.Facts.Value["binding_identity"] != alias.BindingIdentity || alias.Facts.Value["target"] != alias.Target {
		t.Fatalf("alias claim = %#v", alias)
	}
}

func TestPlanPortablePythonAliasesV1RejectsMissingOrWrongGeneratedScript(t *testing.T) {
	component, transaction, _ := portablePythonAliasPlanFixtureV1(t, "application:alias-plan-errors")
	name := component.Bindings[0].CLI.Name
	transaction.GeneratedExecutables[0].Path = "/opt/reploy/providers/python/other/bin/" + name
	if _, err := planPortablePythonAliasesV1(&component, transaction); err == nil || !strings.Contains(err.Error(), "generated executable path") {
		t.Fatalf("wrong generated path error = %v", err)
	}
	component, transaction, _ = portablePythonAliasPlanFixtureV1(t, "application:alias-plan-missing")
	transaction.GeneratedExecutables = transaction.GeneratedExecutables[1:]
	if _, err := planPortablePythonAliasesV1(&component, transaction); err == nil || !strings.Contains(err.Error(), "has no generated executable declaration") {
		t.Fatalf("missing generated path error = %v", err)
	}
}

func TestPortablePythonAliasBindingIdentityV1CoversCanonicalBinding(t *testing.T) {
	component, _, _ := portablePythonAliasPlanFixtureV1(t, "application:alias-identity")
	binding := component.Bindings[0]
	first, err := portablePythonAliasBindingIdentityV1(binding)
	if err != nil {
		t.Fatal(err)
	}
	changed := binding
	changed.Requirements = append([]string{}, binding.Requirements...)
	changed.Requirements[0] = changed.Requirements[0] + ",!=0"
	second, err := portablePythonAliasBindingIdentityV1(changed)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("binding identity did not change: %q", first)
	}
	if _, err := canonical.ParseDigest(first); err != nil {
		t.Fatalf("binding identity is not a canonical digest: %v", err)
	}
}

func TestValidatePortablePythonAliasSelectionClaimsV1RejectsRuntimeOverlap(t *testing.T) {
	component, _, plan := portablePythonAliasPlanFixtureV1(t, "application:alias-runtime")
	runtimeRoot, err := pythonprovider.RuntimeRootV1(component.Component)
	if err != nil {
		t.Fatal(err)
	}
	component.Bindings[0].CLI.Path = path.Join(runtimeRoot, "bin", component.Bindings[0].CLI.Name)
	components := map[string]*pythonprovider.PortableToolPythonComponentV1{component.Component: &component}
	if err := validatePortablePythonAliasSelectionClaimsV1(plan, components); err == nil || !strings.Contains(err.Error(), "runtime root") {
		t.Fatalf("runtime overlap error = %v", err)
	}
}

func TestValidatePortablePythonAliasSelectionClaimsV1AcceptsSelectedFilesystemLayout(t *testing.T) {
	component, _, plan := portablePythonAliasPlanFixtureV1(t, "application:alias-layout")
	components := map[string]*pythonprovider.PortableToolPythonComponentV1{component.Component: &component}
	if err := validatePortablePythonAliasSelectionClaimsV1(plan, components); err != nil {
		t.Fatalf("valid selected filesystem layout rejected: %v", err)
	}
}

func TestValidatePortablePythonAliasSelectionClaimsV1RejectsCrossComponentExportCollision(t *testing.T) {
	first, _, firstPlan := portablePythonAliasPlanFixtureV1(t, "application:alias-first")
	second, _, secondPlan := portablePythonAliasPlanFixtureV1(t, "application:alias-second")
	plan := firstPlan
	plan.Tools = append(plan.Tools, secondPlan.Tools...)
	sort.Slice(plan.Tools, func(left, right int) bool {
		if plan.Tools[left].Scope != plan.Tools[right].Scope {
			return plan.Tools[left].Scope < plan.Tools[right].Scope
		}
		return plan.Tools[left].Provenance.Tool < plan.Tools[right].Provenance.Tool
	})
	components := map[string]*pythonprovider.PortableToolPythonComponentV1{
		first.Component: &first, second.Component: &second,
	}
	if err := validatePortablePythonAliasSelectionClaimsV1(plan, components); err == nil || !strings.Contains(err.Error(), "overlaps selected export") {
		t.Fatalf("cross component collision error = %v", err)
	}
}
