package providers

import (
	"bytes"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func TestBuildPortableToolProviderDAGV1ProjectsResponsibilitiesAndDependencies(t *testing.T) {
	dag, err := BuildPortableToolProviderDAGV1(
		portableToolProviderPlanFixtureV1(),
		representativePortableToolPlanV1(),
		[]PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application")},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePortableToolProviderDAGV1(dag); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dag.ProviderPlan, portableToolProviderPlanFixtureV1()) {
		t.Fatal("provider plan was not carried unchanged")
	}
	counts := map[string]int{}
	for _, operation := range dag.Operations {
		counts[operation.Kind]++
	}
	wantCounts := map[string]int{
		PortableToolOperationBindingContractV1:                1,
		PortableToolOperationBindingArtifactAcquisitionV1:     1,
		PortableToolOperationBindingArtifactMaterializationV1: 1,
		PortableToolOperationPayloadAcquisitionV1:             1,
		PortableToolOperationPayloadMaterializationV1:         1,
		PortableToolOperationNativePackageSetV1:               1,
		PortableToolOperationRuntimeInstallRootV1:             1,
		PortableToolOperationRuntimeEnvironmentV1:             2,
		PortableToolOperationExportV1:                         2,
		PortableToolOperationCapabilityV1:                     2,
		PortableToolOperationAcquisitionBarrierV1:             1,
	}
	if !reflect.DeepEqual(counts, wantCounts) {
		t.Fatalf("operation kinds = %#v, want %#v", counts, wantCounts)
	}
	barrier := portableToolProviderOperationByKindV1(dag.Operations, PortableToolOperationAcquisitionBarrierV1)
	if barrier == nil {
		t.Fatal("acquisition barrier operation is missing")
	}
	acquisitions := make([]string, 0)
	materializations := make([]string, 0)
	for _, operation := range dag.Operations {
		if strings.HasSuffix(operation.Kind, "-acquisition") {
			acquisitions = append(acquisitions, operation.ID)
		}
		if strings.HasSuffix(operation.Kind, "-materialization") {
			materializations = append(materializations, operation.ID)
		}
	}
	if len(dag.Dependencies) != len(acquisitions)+len(materializations) {
		t.Fatalf("dependencies = %d, want linear acquisition barrier %d", len(dag.Dependencies), len(acquisitions)+len(materializations))
	}
	dependencySet := make(map[string]struct{}, len(dag.Dependencies))
	for _, dependency := range dag.Dependencies {
		dependencySet[dependency.Prerequisite+"\x00"+dependency.Dependent] = struct{}{}
		prerequisite := portableToolProviderOperationByIDV1(dag.Operations, dependency.Prerequisite)
		dependent := portableToolProviderOperationByIDV1(dag.Operations, dependency.Dependent)
		if prerequisite == nil || dependent == nil {
			t.Fatalf("dependency = %#v, operations = %#v -> %#v", dependency, prerequisite, dependent)
		}
		if prerequisite.Kind == PortableToolOperationAcquisitionBarrierV1 {
			if dependent.Kind != PortableToolOperationBindingArtifactMaterializationV1 && dependent.Kind != PortableToolOperationPayloadMaterializationV1 {
				t.Fatalf("barrier dependency has non-materialization dependent: %#v", dependency)
			}
		} else if dependent.Kind != PortableToolOperationAcquisitionBarrierV1 ||
			(prerequisite.Kind != PortableToolOperationBindingArtifactAcquisitionV1 && prerequisite.Kind != PortableToolOperationPayloadAcquisitionV1) {
			t.Fatalf("dependency does not use acquisition barrier: %#v", dependency)
		}
	}
	for _, acquisitionID := range acquisitions {
		if _, found := dependencySet[acquisitionID+"\x00"+barrier.ID]; !found {
			t.Fatalf("acquisition barrier is missing %s -> %s", acquisitionID, barrier.ID)
		}
	}
	for _, materializationID := range materializations {
		if _, found := dependencySet[barrier.ID+"\x00"+materializationID]; !found {
			t.Fatalf("acquisition barrier is missing %s -> %s", barrier.ID, materializationID)
		}
	}
	for _, acquisitionID := range acquisitions {
		for _, materializationID := range materializations {
			if !portableToolProviderDependencyReachableV1(dag.Dependencies, acquisitionID, materializationID) {
				t.Fatalf("acquisition %s does not transitively precede materialization %s", acquisitionID, materializationID)
			}
		}
	}
	encoded, err := CanonicalPortableToolProviderDAGBytesV1(dag)
	if err != nil || !bytes.Contains(encoded, []byte(`"portable_tool_plan"`)) {
		t.Fatalf("canonical DAG = %s, error = %v", encoded, err)
	}
}

func TestBuildPortableToolProviderDAGV1SortsDomainsAndClonesInputs(t *testing.T) {
	portablePlan := portableToolProviderTwoScopePlanV1()
	providerPlan := portableToolProviderPlanFixtureV1()
	portableBefore, err := canonical.Marshal(portablePlan)
	if err != nil {
		t.Fatal(err)
	}
	providerBefore, err := canonical.Marshal(providerPlan)
	if err != nil {
		t.Fatal(err)
	}
	domains := []PortableToolProviderDomainSetV1{
		portableToolProviderDomainV1("other"),
		portableToolProviderDomainV1("application"),
	}
	dag, err := BuildPortableToolProviderDAGV1(providerPlan, portablePlan, domains)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{dag.Domains[0].Scope, dag.Domains[1].Scope}; !reflect.DeepEqual(got, []string{"application", "other"}) {
		t.Fatalf("domain order = %#v", got)
	}
	portableAfter, _ := canonical.Marshal(portablePlan)
	providerAfter, _ := canonical.Marshal(providerPlan)
	if !bytes.Equal(portableBefore, portableAfter) || !bytes.Equal(providerBefore, providerAfter) {
		t.Fatal("builder mutated caller-owned plan input")
	}
	// The composite result owns its provider data and nested portable maps.
	dag.PortableToolPlan.Tools[0].Exports[0].Path = "/changed/export"
	dag.ProviderPlan.Nodes[0].Request.Value["changed"] = "result-only"
	if portablePlan.Tools[0].Exports[0].Path == "/changed/export" || providerPlan.Nodes[0].Request.Value["changed"] == "result-only" {
		t.Fatal("builder result aliases caller-owned plan data")
	}
}

func TestBuildPortableToolProviderDAGV1MapsOneDomainPerDistinctScope(t *testing.T) {
	plan := portableToolProviderTwoToolsOneScopePlanV1()
	dag, err := BuildPortableToolProviderDAGV1(
		portableToolProviderPlanFixtureV1(), plan,
		[]PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application")},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(dag.Domains) != 1 || dag.Domains[0].Scope != "application" {
		t.Fatalf("domains = %#v", dag.Domains)
	}
	if len(dag.PortableToolPlan.Tools) != 2 || dag.PortableToolPlan.Tools[0].Provenance.Tool == dag.PortableToolPlan.Tools[1].Provenance.Tool {
		t.Fatalf("selected tools = %#v", dag.PortableToolPlan.Tools)
	}
}

func TestBuildPortableToolProviderDAGV1ValidatesDomainOwnersAndProjectsThem(t *testing.T) {
	basePlan := portableToolProviderPlanFixtureV1()
	validDomains := []PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application")}
	tests := []struct {
		name   string
		mutate func([]PortableToolProviderDomainSetV1)
		want   string
	}{
		{name: "missing owner", mutate: func(domains []PortableToolProviderDomainSetV1) {
			domains[0].Capabilities.Owner = ""
		}, want: "owner is required"},
		{name: "unknown owner", mutate: func(domains []PortableToolProviderDomainSetV1) {
			domains[0].Capabilities.Owner = "provider/missing"
		}, want: "owner \"provider/missing\" is unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			domains := append([]PortableToolProviderDomainSetV1{}, validDomains...)
			test.mutate(domains)
			if _, err := BuildPortableToolProviderDAGV1(basePlan, representativePortableToolPlanV1(), domains); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}

	twoScopePlan := portableToolProviderTwoScopePlanV1()
	twoDomains := []PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application"), portableToolProviderDomainV1("other")}
	twoDomains[1].Capabilities.ID = twoDomains[0].Capabilities.ID
	twoDomains[1].Capabilities.Owner = "base"
	if _, err := BuildPortableToolProviderDAGV1(basePlan, twoScopePlan, twoDomains); err == nil || !strings.Contains(err.Error(), "conflicting owners") {
		t.Fatalf("shared domain owner error = %v", err)
	}

	dag, err := BuildPortableToolProviderDAGV1(basePlan, representativePortableToolPlanV1(), validDomains)
	if err != nil {
		t.Fatal(err)
	}
	owners := map[string]NodeID{}
	for _, domain := range dag.Domains {
		for _, authority := range []PortableToolDomainAuthorityV1{
			domain.PackageManager, domain.Binding, domain.Filesystem, domain.Environment, domain.Exports, domain.Capabilities,
		} {
			owners[authority.ID] = authority.Owner
		}
	}
	for _, operation := range dag.Operations {
		if operation.Kind == PortableToolOperationAcquisitionBarrierV1 {
			if operation.Owner != "" {
				t.Fatalf("acquisition barrier owner = %q, want empty", operation.Owner)
			}
			continue
		}
		if owners[operation.Domain] != operation.Owner {
			t.Fatalf("operation %q owner = %q, want %q", operation.ID, operation.Owner, owners[operation.Domain])
		}
	}
}

func TestBuildPortableToolProviderDAGV1OmitsBarrierWithoutAcquisitionWork(t *testing.T) {
	plan := representativePortableToolPlanV1()
	plan.Tools[0].Responsibilities.BindingArtifacts = []PortableToolSelectedRecordV1{}
	plan.Tools[0].Responsibilities.Payloads = []PortableToolSelectedRecordV1{}
	dag, err := BuildPortableToolProviderDAGV1(
		portableToolProviderPlanFixtureV1(), plan,
		[]PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application")},
	)
	if err != nil {
		t.Fatal(err)
	}
	if barrier := portableToolProviderOperationByKindV1(dag.Operations, PortableToolOperationAcquisitionBarrierV1); barrier != nil {
		t.Fatalf("unexpected acquisition barrier operation: %#v", barrier)
	}
	if len(dag.Dependencies) != 0 {
		t.Fatalf("dependencies = %#v, want none", dag.Dependencies)
	}
	if err := ValidatePortableToolProviderDAGV1(dag); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePortableToolProviderDAGV1InvokesExistingProviderValidators(t *testing.T) {
	providerPlan := portableToolProviderPlanFixtureV1()
	providerPlan.Schema = "provider-plan-v2"
	if _, err := BuildPortableToolProviderDAGV1(providerPlan, representativePortableToolPlanV1(), []PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application")}); err == nil || !strings.Contains(err.Error(), "provider plan") {
		t.Fatalf("invalid provider plan error = %v", err)
	}
	portablePlan := representativePortableToolPlanV1()
	portablePlan.Schema = "portable-tool-plan-v2"
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), portablePlan, []PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application")}); err == nil || !strings.Contains(err.Error(), "portable tool plan") {
		t.Fatalf("invalid portable plan error = %v", err)
	}
}

func TestValidatePortableToolProviderDAGV1RejectsDomainMappingErrors(t *testing.T) {
	valid := portableToolProviderTwoScopeDAGFixtureV1(t)
	tests := []struct {
		name   string
		mutate func(*PortableToolProviderDAGV1)
		want   string
	}{
		{name: "missing scope", mutate: func(dag *PortableToolProviderDAGV1) { dag.Domains = dag.Domains[:0] }, want: "map every selected scope"},
		{name: "extra scope", mutate: func(dag *PortableToolProviderDAGV1) {
			domain := dag.Domains[0]
			domain.Scope = "unselected"
			dag.Domains = append(dag.Domains, domain)
		}, want: "map every selected scope|not selected"},
		{name: "empty authority", mutate: func(dag *PortableToolProviderDAGV1) { dag.Domains[0].Capabilities.ID = "" }, want: "nonempty"},
		{name: "duplicate scope", mutate: func(dag *PortableToolProviderDAGV1) { dag.Domains[0].Scope = dag.Domains[1].Scope }, want: "unique"},
		{name: "unsorted domains", mutate: func(dag *PortableToolProviderDAGV1) { dag.Domains[0], dag.Domains[1] = dag.Domains[1], dag.Domains[0] }, want: "sorted"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := clonePortableToolProviderDAGV1(valid)
			test.mutate(&candidate)
			if err := ValidatePortableToolProviderDAGV1(candidate); err == nil || !portableToolErrorContainsAnyV1(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidatePortableToolProviderDAGV1RejectsMalformedDependenciesAndCycles(t *testing.T) {
	valid := portableToolProviderDAGFixtureV1(t)
	tests := []struct {
		name   string
		mutate func(*PortableToolProviderDAGV1)
		want   string
	}{
		{name: "missing", mutate: func(dag *PortableToolProviderDAGV1) { dag.Dependencies = dag.Dependencies[:1] }, want: "exactly"},
		{name: "unknown", mutate: func(dag *PortableToolProviderDAGV1) { dag.Dependencies[0].Prerequisite = "unknown-operation" }, want: "unknown"},
		{name: "reversed", mutate: func(dag *PortableToolProviderDAGV1) {
			dag.Dependencies[0].Prerequisite, dag.Dependencies[0].Dependent = dag.Dependencies[0].Dependent, dag.Dependencies[0].Prerequisite
			sort.Slice(dag.Dependencies, func(left, right int) bool {
				return portableToolDependencyCompareV1(dag.Dependencies[left], dag.Dependencies[right]) < 0
			})
		}, want: "missing|reversed"},
		{name: "cycle", mutate: func(dag *PortableToolProviderDAGV1) {
			edge := dag.Dependencies[0]
			dag.Dependencies = append(dag.Dependencies, PortableToolProviderDependencyV1{Prerequisite: edge.Dependent, Dependent: edge.Prerequisite})
			sort.Slice(dag.Dependencies, func(left, right int) bool {
				return portableToolDependencyCompareV1(dag.Dependencies[left], dag.Dependencies[right]) < 0
			})
		}, want: "exactly"},
		{name: "operation order", mutate: func(dag *PortableToolProviderDAGV1) {
			dag.Operations[0], dag.Operations[1] = dag.Operations[1], dag.Operations[0]
		}, want: "sorted|incorrectly ordered"},
		{name: "wrong materialization policy", mutate: func(dag *PortableToolProviderDAGV1) {
			for index := range dag.Operations {
				if strings.HasSuffix(dag.Operations[index].Kind, "-materialization") {
					dag.Operations[index].Network = "network"
					break
				}
			}
		}, want: "does not match"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := clonePortableToolProviderDAGV1(valid)
			test.mutate(&candidate)
			if err := ValidatePortableToolProviderDAGV1(candidate); err == nil || !portableToolErrorContainsAnyV1(err.Error(), test.want) {
				t.Fatalf("error = %v, want one of %q", err, test.want)
			}
		})
	}
}

func TestRejectPortableToolProviderOperationCyclesV1Defensively(t *testing.T) {
	operations := []PortableToolProviderOperationV1{{ID: "operation-a"}, {ID: "operation-b"}}
	dependencies := []PortableToolProviderDependencyV1{
		{Prerequisite: "operation-a", Dependent: "operation-b"},
		{Prerequisite: "operation-b", Dependent: "operation-a"},
	}
	if err := rejectPortableToolProviderOperationCyclesV1(operations, dependencies); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle error = %v", err)
	}
}

func TestBuildPortableToolProviderDAGV1RejectsSharedDomainConflicts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(PortableToolPlanV1)
		want   string
	}{
		{name: "environment", mutate: func(plan PortableToolPlanV1) {
			plan.Tools[1].Runtime.Environment[0].Value = "different"
		}, want: "shared-domain conflict"},
		{name: "export", mutate: func(plan PortableToolPlanV1) {
			plan.Tools[1].Exports[0].Path = "/other/export"
		}, want: "shared-domain conflict"},
		{name: "selected record digest", mutate: func(plan PortableToolPlanV1) {
			selected := &plan.Tools[1].Responsibilities.Payloads[0]
			selected.Record.Value["name"] = "different"
			refreshPortableToolTestRecordDigest(&selected.Reference, selected.Record)
		}, want: "shared-domain conflict"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := portableToolProviderTwoScopePlanV1()
			test.mutate(plan)
			_, err := BuildPortableToolProviderDAGV1(
				portableToolProviderPlanFixtureV1(), plan,
				[]PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application"), portableToolProviderDomainV1("other")},
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
	// Isolated authority domains do not conflict merely because scopes share a
	// name or selected value.
	plan := portableToolProviderTwoScopePlanV1()
	isolated := []PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application"), portableToolProviderDomainV1("other")}
	isolated[1].Environment.ID = "other-environment"
	isolated[1].Exports.ID = "other-exports"
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), plan, isolated); err != nil {
		t.Fatalf("isolated shared claims rejected: %v", err)
	}

	capabilityConflictPlan := portableToolProviderTwoScopePlanV1()
	capabilityConflictPlan.Tools[1].Exports[0].Path = "/other/export"
	capabilityConflictDomains := []PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application"), portableToolProviderDomainV1("other")}
	capabilityConflictDomains[1].Exports.ID = "other-exports"
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), capabilityConflictPlan, capabilityConflictDomains); err == nil || !strings.Contains(err.Error(), "shared-domain conflict") {
		t.Fatalf("shared capability conflict was not rejected: %v", err)
	}

	fullyIsolatedPlan := portableToolProviderTwoScopePlanV1()
	fullyIsolatedPlan.Tools[1].Exports[0].Path = "/other/export"
	fullyIsolatedDomains := []PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application"), portableToolProviderDomainV1("other")}
	fullyIsolatedDomains[1].Exports = PortableToolDomainAuthorityV1{ID: "other-exports", Owner: "base"}
	fullyIsolatedDomains[1].Capabilities = PortableToolDomainAuthorityV1{ID: "other-capabilities", Owner: "base"}
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), fullyIsolatedPlan, fullyIsolatedDomains); err != nil {
		t.Fatalf("split export and capability domains rejected: %v", err)
	}
}

func TestBuildPortableToolProviderDAGV1ComparesNativePackageSemantics(t *testing.T) {
	plan := portableToolProviderTwoScopePlanV1()
	firstPackage := &plan.Tools[0].Responsibilities.NativePackageSets[0]
	secondPackage := &plan.Tools[1].Responsibilities.NativePackageSets[0]
	setPortableToolPackageSemanticsV1(firstPackage, []string{"foo=1"}, []string{"main"})
	setPortableToolTestRecordID(&secondPackage.Reference, &secondPackage.Record, "tool:demo/releases/1.2.3/package-sets/other")
	setPortableToolPackageSemanticsV1(secondPackage, []string{"foo=1"}, []string{"main"})
	domains := []PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application"), portableToolProviderDomainV1("other")}
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), plan, domains); err != nil {
		t.Fatalf("identical package requirements rejected: %v", err)
	}

	canonicalArrays := clonePortableToolPlanForTest(plan)
	canonicalPackage := &canonicalArrays.Tools[1].Responsibilities.NativePackageSets[0]
	canonicalPackage.Record.Value["requirements"] = []any{"foo=1"}
	canonicalPackage.Record.Value["repositories"] = []any{"main"}
	refreshPortableToolTestRecordDigest(&canonicalPackage.Reference, canonicalPackage.Record)
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), canonicalArrays, domains); err != nil {
		t.Fatalf("canonical package arrays rejected: %v", err)
	}

	conflicting := clonePortableToolPlanForTest(plan)
	setPortableToolPackageSemanticsV1(&conflicting.Tools[1].Responsibilities.NativePackageSets[0], []string{"foo=2"}, []string{"main"})
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), conflicting, domains); err == nil || !strings.Contains(err.Error(), "shared-domain conflict") {
		t.Fatalf("conflicting package requirements were not rejected: %v", err)
	}

	independent := clonePortableToolPlanForTest(plan)
	setPortableToolPackageSemanticsV1(&independent.Tools[1].Responsibilities.NativePackageSets[0], []string{"bar=2"}, []string{"vendor"})
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), independent, domains); err != nil {
		t.Fatalf("independent package requirements rejected: %v", err)
	}
}

func setPortableToolPackageSemanticsV1(selected *PortableToolSelectedRecordV1, requirements, repositories []string) {
	selected.Record.Value["manager"] = "apt"
	selected.Record.Value["requirements"] = append([]string{}, requirements...)
	selected.Record.Value["repositories"] = append([]string{}, repositories...)
	refreshPortableToolTestRecordDigest(&selected.Reference, selected.Record)
}

func TestBuildPortableToolProviderDAGV1AllowsAndRejectsFilesystemPathClaims(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(PortableToolPlanV1)
		wantError string
	}{
		{name: "distinct roots", mutate: func(plan PortableToolPlanV1) {
			plan.Tools[1].Runtime.InstallRoot = "/opt/other"
		}},
		{name: "identical roots deduplicate", mutate: func(plan PortableToolPlanV1) {
			plan.Tools[1].Runtime.InstallRoot = "/opt/demo"
		}},
		{name: "overlapping roots", mutate: func(plan PortableToolPlanV1) {
			plan.Tools[1].Runtime.InstallRoot = "/opt/demo/sub"
		}, wantError: "shared filesystem-domain conflict"},
		{name: "distinct destinations", mutate: func(plan PortableToolPlanV1) {
			for index := range plan.Tools {
				payload := &plan.Tools[index].Responsibilities.Payloads[0]
				payload.Record.Value["install_directory"] = "payload-" + plan.Tools[index].Scope
				refreshPortableToolTestRecordDigest(&payload.Reference, payload.Record)
			}
		}},
		{name: "overlapping destinations", mutate: func(plan PortableToolPlanV1) {
			for index := range plan.Tools {
				payload := &plan.Tools[index].Responsibilities.Payloads[0]
				if index == 0 {
					payload.Record.Value["install_directory"] = "payload"
				} else {
					payload.Record.Value["install_directory"] = "payload/sub"
				}
				refreshPortableToolTestRecordDigest(&payload.Reference, payload.Record)
			}
		}, wantError: "shared filesystem-domain conflict"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := portableToolProviderTwoScopePlanV1()
			secondPayload := &plan.Tools[1].Responsibilities.Payloads[0]
			setPortableToolTestRecordID(&secondPayload.Reference, &secondPayload.Record, "tool:demo/releases/1.2.3/payloads/other")
			test.mutate(plan)
			_, err := BuildPortableToolProviderDAGV1(
				portableToolProviderPlanFixtureV1(), plan,
				[]PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application"), portableToolProviderDomainV1("other")},
			)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestBuildPortableToolProviderDAGV1ResolvesRelativePayloadDestinations(t *testing.T) {
	plan := portableToolProviderTwoScopePlanV1()
	plan.Tools[0].Runtime.InstallRoot = "/opt/first"
	plan.Tools[1].Runtime.InstallRoot = "/opt/second"
	firstPayload := &plan.Tools[0].Responsibilities.Payloads[0]
	secondPayload := &plan.Tools[1].Responsibilities.Payloads[0]
	setPortableToolPayloadPathsV1(firstPayload, "browser", "payload/demo")
	setPortableToolTestRecordID(&secondPayload.Reference, &secondPayload.Record, "tool:demo/releases/1.2.3/payloads/other")
	setPortableToolPayloadPathsV1(secondPayload, "browser", "payload/other")
	domains := []PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application"), portableToolProviderDomainV1("other")}
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), plan, domains); err != nil {
		t.Fatalf("identical relative destinations under separate roots rejected: %v", err)
	}

	overlapping := clonePortableToolPlanForTest(plan)
	overlapping.Tools[1].Runtime.InstallRoot = "/opt/first"
	setPortableToolPayloadPathsV1(&overlapping.Tools[1].Responsibilities.Payloads[0], "browser/cache", "payload/other")
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), overlapping, domains); err == nil || !strings.Contains(err.Error(), "shared filesystem-domain conflict") {
		t.Fatalf("nested relative destination overlap was not rejected: %v", err)
	}
}

func setPortableToolPayloadPathsV1(selected *PortableToolSelectedRecordV1, installDirectory, logicalPath string) {
	selected.Record.Value["install_directory"] = installDirectory
	selected.Record.Value["logical_path"] = logicalPath
	refreshPortableToolTestRecordDigest(&selected.Reference, selected.Record)
}

func portableToolProviderDAGFixtureV1(t *testing.T) PortableToolProviderDAGV1 {
	t.Helper()
	dag, err := BuildPortableToolProviderDAGV1(
		portableToolProviderPlanFixtureV1(),
		representativePortableToolPlanV1(),
		[]PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application")},
	)
	if err != nil {
		t.Fatal(err)
	}
	return dag
}

func portableToolProviderTwoScopeDAGFixtureV1(t *testing.T) PortableToolProviderDAGV1 {
	t.Helper()
	dag, err := BuildPortableToolProviderDAGV1(
		portableToolProviderPlanFixtureV1(),
		portableToolProviderTwoScopePlanV1(),
		[]PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application"), portableToolProviderDomainV1("other")},
	)
	if err != nil {
		t.Fatal(err)
	}
	return dag
}

func portableToolProviderPlanFixtureV1() ProviderPlanV1 {
	return ProviderPlanV1{
		Schema: ProviderPlanSchemaV1,
		Nodes: []NodeSpec{
			aptPlanNode("system"),
			basePlanNode(),
			pythonPlanNode("application", ExecutableRequirement{}),
		},
		Edges: []ProviderEdgeV1{},
	}
}

func portableToolProviderDomainV1(scope string) PortableToolProviderDomainSetV1 {
	return PortableToolProviderDomainSetV1{
		Scope:          scope,
		PackageManager: PortableToolDomainAuthorityV1{ID: "pm-shared", Owner: "apt"},
		Binding:        PortableToolDomainAuthorityV1{ID: "binding-shared", Owner: "python/application"},
		Filesystem:     PortableToolDomainAuthorityV1{ID: "filesystem-shared", Owner: "python/application"},
		Environment:    PortableToolDomainAuthorityV1{ID: "environment-shared", Owner: "python/application"},
		Exports:        PortableToolDomainAuthorityV1{ID: "exports-shared", Owner: "python/application"},
		Capabilities:   PortableToolDomainAuthorityV1{ID: "capabilities-shared", Owner: "python/application"},
	}
}

func portableToolProviderTwoScopePlanV1() PortableToolPlanV1 {
	plan := clonePortableToolPlanForTest(representativePortableToolPlanV1())
	second := clonePortableToolPlanForTest(plan).Tools[0]
	second.Scope = "other"
	second.SelectedClosureDigest = canonical.Digest("sha256:abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd")
	plan.Tools = append(plan.Tools, second)
	sort.Slice(plan.Tools, func(left, right int) bool { return plan.Tools[left].Scope < plan.Tools[right].Scope })
	return plan
}

func portableToolProviderTwoToolsOneScopePlanV1() PortableToolPlanV1 {
	plan := clonePortableToolPlanForTest(representativePortableToolPlanV1())
	second := clonePortableToolPlanForTest(plan).Tools[0]
	retargetPortableToolTestEntry(&second, "demo", "other")
	plan.Tools = append(plan.Tools, second)
	sort.Slice(plan.Tools, func(left, right int) bool {
		if plan.Tools[left].Scope != plan.Tools[right].Scope {
			return plan.Tools[left].Scope < plan.Tools[right].Scope
		}
		return plan.Tools[left].Provenance.Tool < plan.Tools[right].Provenance.Tool
	})
	return plan
}

func portableToolProviderOperationByIDV1(operations []PortableToolProviderOperationV1, id string) *PortableToolProviderOperationV1 {
	for index := range operations {
		if operations[index].ID == id {
			return &operations[index]
		}
	}
	return nil
}

func portableToolProviderOperationByKindV1(operations []PortableToolProviderOperationV1, kind string) *PortableToolProviderOperationV1 {
	for index := range operations {
		if operations[index].Kind == kind {
			return &operations[index]
		}
	}
	return nil
}

func portableToolProviderDependencyReachableV1(dependencies []PortableToolProviderDependencyV1, from, to string) bool {
	adjacency := make(map[string][]string)
	for _, dependency := range dependencies {
		adjacency[dependency.Prerequisite] = append(adjacency[dependency.Prerequisite], dependency.Dependent)
	}
	seen := map[string]struct{}{from: {}}
	queue := []string{from}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range adjacency[current] {
			if next == to {
				return true
			}
			if _, found := seen[next]; found {
				continue
			}
			seen[next] = struct{}{}
			queue = append(queue, next)
		}
	}
	return from == to
}

func clonePortableToolProviderDAGV1(dag PortableToolProviderDAGV1) PortableToolProviderDAGV1 {
	result := dag
	result.Domains = append([]PortableToolProviderDomainSetV1{}, dag.Domains...)
	result.Operations = append([]PortableToolProviderOperationV1{}, dag.Operations...)
	for index, operation := range result.Operations {
		if operation.Record != nil {
			record := *operation.Record
			result.Operations[index].Record = &record
		}
		if operation.Environment != nil {
			environment := *operation.Environment
			result.Operations[index].Environment = &environment
		}
		if operation.Export != nil {
			exported := *operation.Export
			result.Operations[index].Export = &exported
		}
	}
	result.Dependencies = append([]PortableToolProviderDependencyV1{}, dag.Dependencies...)
	result.ProviderPlan = cloneProviderPlanForPortableToolDAGV1(dag.ProviderPlan)
	result.PortableToolPlan = clonePortableToolPlanForPortableToolDAGV1(dag.PortableToolPlan)
	return result
}

func portableToolErrorContainsAnyV1(value string, alternatives string) bool {
	for _, alternative := range strings.Split(alternatives, "|") {
		if strings.Contains(value, alternative) {
			return true
		}
	}
	return false
}
