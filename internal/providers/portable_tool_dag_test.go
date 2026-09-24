package providers

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

type portableToolProviderGraphViewV1 struct {
	Operations   []PortableToolProviderOperationV1
	Dependencies []PortableToolProviderDependencyV1
}

func portableToolProviderGraphViewForTestV1(t *testing.T, dag PortableToolProviderDAGV1) portableToolProviderGraphViewV1 {
	t.Helper()
	operations, dependencies, err := DerivePortableToolProviderGraphV1(dag)
	if err != nil {
		t.Fatal(err)
	}
	return portableToolProviderGraphViewV1{Operations: operations, Dependencies: dependencies}
}

func TestBuildPortableToolProviderDAGV1ProjectsResponsibilitiesAndDependencies(t *testing.T) {
	dag, err := BuildPortableToolProviderDAGV1(
		portableToolProviderPlanFixtureV1(),
		representativePortableToolPlanV1(),
		[]PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application:demo")},
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
	view := portableToolProviderGraphViewForTestV1(t, dag)
	counts := map[string]int{}
	for _, operation := range view.Operations {
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
	barrier := portableToolProviderOperationByKindV1(view.Operations, PortableToolOperationAcquisitionBarrierV1)
	if barrier == nil {
		t.Fatal("acquisition barrier operation is missing")
	}
	acquisitions := make([]string, 0)
	materializations := make([]string, 0)
	for _, operation := range view.Operations {
		if strings.HasSuffix(operation.Kind, "-acquisition") {
			acquisitions = append(acquisitions, operation.ID)
		}
		if strings.HasSuffix(operation.Kind, "-materialization") {
			materializations = append(materializations, operation.ID)
		}
	}
	if len(view.Dependencies) != len(acquisitions)+len(materializations)+len(dag.PortableToolPlan.Tools[0].Exports)+1 {
		t.Fatalf("dependencies = %d, want barrier plus binding export ordering", len(view.Dependencies))
	}
	dependencySet := make(map[string]struct{}, len(view.Dependencies))
	for _, dependency := range view.Dependencies {
		dependencySet[dependency.Prerequisite+"\x00"+dependency.Dependent] = struct{}{}
		prerequisite := portableToolProviderOperationByIDV1(view.Operations, dependency.Prerequisite)
		dependent := portableToolProviderOperationByIDV1(view.Operations, dependency.Dependent)
		if prerequisite == nil || dependent == nil {
			t.Fatalf("dependency = %#v, operations = %#v -> %#v", dependency, prerequisite, dependent)
		}
		if prerequisite.Kind == PortableToolOperationAcquisitionBarrierV1 {
			if dependent.Kind != PortableToolOperationBindingArtifactMaterializationV1 && dependent.Kind != PortableToolOperationPayloadMaterializationV1 {
				t.Fatalf("barrier dependency has non-materialization dependent: %#v", dependency)
			}
		} else if dependent.Kind == PortableToolOperationAcquisitionBarrierV1 {
			if prerequisite.Kind != PortableToolOperationBindingArtifactAcquisitionV1 && prerequisite.Kind != PortableToolOperationPayloadAcquisitionV1 {
				t.Fatalf("dependency does not use acquisition barrier: %#v", dependency)
			}
		} else if prerequisite.Kind != PortableToolOperationBindingArtifactMaterializationV1 &&
			(prerequisite.Kind != PortableToolOperationExportV1 || dependent.Kind != PortableToolOperationCapabilityV1) {
			t.Fatalf("dependency is not a binding materialization or export ordering edge: %#v", dependency)
		} else if prerequisite.Kind == PortableToolOperationBindingArtifactMaterializationV1 && dependent.Kind != PortableToolOperationExportV1 {
			t.Fatalf("binding materialization edge does not target export: %#v", dependency)
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
	for _, operation := range view.Operations {
		if operation.Kind != PortableToolOperationExportV1 {
			continue
		}
		capabilityID := portableToolOperationIDV1(operation.Scope, operation.Tool, PortableToolOperationCapabilityV1, operation.Export.Name)
		if _, found := dependencySet[operation.ID+"\x00"+capabilityID]; !found {
			t.Fatalf("export %s does not precede capability %s", operation.ID, capabilityID)
		}
	}
	materialization := portableToolProviderOperationByKindV1(view.Operations, PortableToolOperationBindingArtifactMaterializationV1)
	export := portableToolProviderOperationByIDV1(view.Operations, portableToolOperationIDV1("application:demo", "demo", PortableToolOperationExportV1, "demo"))
	if materialization == nil || export == nil || !portableToolProviderDependencyReachableV1(view.Dependencies, materialization.ID, export.ID) {
		t.Fatalf("binding materialization does not precede its export: %#v -> %#v", materialization, export)
	}
	for _, acquisitionID := range acquisitions {
		for _, materializationID := range materializations {
			if !portableToolProviderDependencyReachableV1(view.Dependencies, acquisitionID, materializationID) {
				t.Fatalf("acquisition %s does not transitively precede materialization %s", acquisitionID, materializationID)
			}
		}
	}
	encoded, err := CanonicalPortableToolProviderDAGBytesV1(dag)
	if err != nil || !bytes.Contains(encoded, []byte(`"portable_tool_plan"`)) {
		t.Fatalf("canonical DAG = %s, error = %v", encoded, err)
	}
}

func TestBuildPortableToolProviderDAGV1OrdersBindingExportAndCapability(t *testing.T) {
	dag, _, _ := portableToolLockFixtureV1(t)
	view := portableToolProviderGraphViewForTestV1(t, dag)
	materializationID := portableToolOperationIDV1("application:demo", "demo", PortableToolOperationBindingArtifactMaterializationV1,
		"tool:demo/releases/1.2.3/bindings/demo/artifacts/linux-amd64")
	exportID := portableToolOperationIDV1("application:demo", "demo", PortableToolOperationExportV1, "demo")
	capabilityID := portableToolOperationIDV1("application:demo", "demo", PortableToolOperationCapabilityV1, "demo")
	dependencies := make(map[string]struct{}, len(view.Dependencies))
	for _, dependency := range view.Dependencies {
		dependencies[dependency.Prerequisite+"\x00"+dependency.Dependent] = struct{}{}
	}
	for _, edge := range [][2]string{{materializationID, exportID}, {exportID, capabilityID}} {
		if _, found := dependencies[edge[0]+"\x00"+edge[1]]; !found {
			t.Fatalf("missing binding ordering edge %s -> %s", edge[0], edge[1])
		}
	}
	if _, found := dependencies[materializationID+"\x00"+capabilityID]; found {
		t.Fatal("binding materialization has a direct capability edge")
	}
	if !portableToolProviderDependencyReachableV1(view.Dependencies, materializationID, capabilityID) {
		t.Fatal("binding materialization does not transitively precede capability")
	}
}

func TestBuildPortableToolProviderDAGV1RejectsBindingCLIJoinDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PortableToolPlanV1)
		want   string
	}{
		{name: "missing CLI", mutate: func(plan *PortableToolPlanV1) {
			contract := &plan.Tools[0].Responsibilities.BindingContracts[0]
			delete(contract.Record.Value, "cli")
			refreshPortableToolTestRecordDigest(&contract.Reference, contract.Record)
		}, want: "CLI is required"},
		{name: "CLI path mismatch", mutate: func(plan *PortableToolPlanV1) {
			contract := &plan.Tools[0].Responsibilities.BindingContracts[0]
			contract.Record.Value["cli"].(canonical.Object)["path"] = "/opt/demo/bin/other"
			refreshPortableToolTestRecordDigest(&contract.Reference, contract.Record)
			artifact := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
			artifact.Record.Value["contract"].(canonical.Object)["digest"] = string(contract.Reference.Digest)
			refreshPortableToolTestRecordDigest(&artifact.Reference, artifact.Record)
		}, want: "exactly one export and capability"},
		{name: "unknown artifact contract", mutate: func(plan *PortableToolPlanV1) {
			artifact := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
			artifact.Record.Value["contract"].(canonical.Object)["id"] = "tool:demo/releases/1.2.3/bindings/other/contract"
			refreshPortableToolTestRecordDigest(&artifact.Reference, artifact.Record)
		}, want: "unknown binding contract"},
		{name: "identity-only contract", mutate: func(plan *PortableToolPlanV1) {
			contract := &plan.Tools[0].Responsibilities.BindingContracts[0]
			contract.Record.Value = canonical.Object{
				"schema": contract.Record.Schema, "id": contract.Reference.ID, "name": "demo",
			}
			refreshPortableToolTestRecordDigest(&contract.Reference, contract.Record)
			artifact := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
			artifact.Record.Value["contract"].(canonical.Object)["digest"] = string(contract.Reference.Digest)
			refreshPortableToolTestRecordDigest(&artifact.Reference, artifact.Record)
		}, want: "CLI is required"},
		{name: "missing artifact contract", mutate: func(plan *PortableToolPlanV1) {
			artifact := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
			delete(artifact.Record.Value, "contract")
			refreshPortableToolTestRecordDigest(&artifact.Reference, artifact.Record)
		}, want: "contract reference is required"},
		{name: "extra CLI field", mutate: func(plan *PortableToolPlanV1) {
			contract := &plan.Tools[0].Responsibilities.BindingContracts[0]
			contract.Record.Value["cli"].(canonical.Object)["unexpected"] = "value"
			refreshPortableToolTestRecordDigest(&contract.Reference, contract.Record)
		}, want: "exactly canonical name and path"},
		{name: "extra artifact contract field", mutate: func(plan *PortableToolPlanV1) {
			artifact := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
			artifact.Record.Value["contract"].(canonical.Object)["unexpected"] = "value"
			refreshPortableToolTestRecordDigest(&artifact.Reference, artifact.Record)
		}, want: "exactly canonical id and digest"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dag, _, _ := portableToolLockFixtureV1(t)
			plan := clonePortableToolPlanForTest(dag.PortableToolPlan)
			test.mutate(&plan)
			if _, err := BuildPortableToolProviderDAGV1(dag.ProviderPlan, plan, dag.Domains); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestBuildPortableToolProviderDAGV1RejectsMultipleArtifactsForOneBindingContract(t *testing.T) {
	plan := representativePortableToolPlanV1()
	duplicate := clonePortableToolRecordsForTest(plan.Tools[0].Responsibilities.BindingArtifacts)[0]
	setPortableToolTestRecordID(
		&duplicate.Reference,
		&duplicate.Record,
		"tool:demo/releases/1.2.3/bindings/demo/artifacts/linux-arm64",
	)
	plan.Tools[0].Responsibilities.BindingArtifacts = append(
		plan.Tools[0].Responsibilities.BindingArtifacts,
		duplicate,
	)
	if _, err := BuildPortableToolProviderDAGV1(
		portableToolProviderPlanFixtureV1(), plan,
		[]PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application:demo")},
	); err == nil || !strings.Contains(err.Error(), "exactly one selected binding artifact") {
		t.Fatalf("multiple binding artifacts were accepted: %v", err)
	}
}

func TestBuildPortableToolProviderDAGV1RejectsMultipleBindingContractsForOneExport(t *testing.T) {
	plan := representativePortableToolPlanV1()
	entry := &plan.Tools[0]
	contract := clonePortableToolRecordsForTest(entry.Responsibilities.BindingContracts)[0]
	setPortableToolTestRecordID(
		&contract.Reference,
		&contract.Record,
		"tool:demo/releases/1.2.3/bindings/other/contract",
	)
	artifact := clonePortableToolRecordsForTest(entry.Responsibilities.BindingArtifacts)[0]
	setPortableToolTestRecordID(
		&artifact.Reference,
		&artifact.Record,
		"tool:demo/releases/1.2.3/bindings/other/artifacts/linux-amd64",
	)
	artifact.Record.Value["binding"] = "other"
	artifact.Record.Value["filename"] = "other-1.2.3-py3-none-any.whl"
	artifact.Record.Value["contract"] = canonical.Object{
		"id": contract.Reference.ID, "digest": string(contract.Reference.Digest),
	}
	refreshPortableToolTestRecordDigest(&artifact.Reference, artifact.Record)
	entry.Responsibilities.BindingContracts = append(entry.Responsibilities.BindingContracts, contract)
	entry.Responsibilities.BindingArtifacts = append(entry.Responsibilities.BindingArtifacts, artifact)

	if _, err := BuildPortableToolProviderDAGV1(
		portableToolProviderPlanFixtureV1(), plan,
		[]PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application:demo")},
	); err == nil || !strings.Contains(err.Error(), "export-path") {
		t.Fatalf("multiple binding contracts sharing one export were accepted: %v", err)
	}
}

func TestBuildPortableToolProviderDAGV1RejectsSharedExportDestinationAcrossRuntimes(t *testing.T) {
	plan := portableToolProviderTwoScopePlanV1()
	plan.Tools[1].Runtime.InstallRoot = "/opt/other"
	plan.Tools[1].Exports[0].Path = plan.Tools[0].Exports[0].Path
	setPortableToolBindingCLIPathV1(&plan.Tools[1], plan.Tools[1].Exports[0].Path)
	_, err := BuildPortableToolProviderDAGV1(
		portableToolProviderPlanFixtureV1(), plan,
		[]PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application:demo"), portableToolProviderDomainV1("application:other")},
	)
	if err == nil || !strings.Contains(err.Error(), "export-path") {
		t.Fatalf("shared export destination was accepted: %v", err)
	}
}

func TestBuildPortableToolProviderDAGV1RejectsExportFilesystemOverlap(t *testing.T) {
	plan := representativePortableToolPlanV1()
	plan.Tools[0].Exports[0].Path = "/opt/demo/bin/demo-alias"
	setPortableToolBindingCLIPathV1(&plan.Tools[0], "/opt/demo/bin/demo-alias")
	_, err := BuildPortableToolProviderDAGV1(
		portableToolProviderPlanFixtureV1(), plan,
		[]PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application:demo")},
	)
	if err == nil || !strings.Contains(err.Error(), "filesystem-domain conflict") {
		t.Fatalf("export overlapping runtime filesystem claim was accepted: %v", err)
	}
}

func TestBuildPortableToolProviderDAGV1RejectsBindingAliasAcrossExportDomains(t *testing.T) {
	_, err := BuildPortableToolProviderDAGV1(
		portableToolProviderPlanFixtureV1(), portableToolProviderTwoScopePlanV1(),
		portableToolProviderDistinctAliasDomainsV1(),
	)
	if err == nil || !strings.Contains(err.Error(), "export-path") {
		t.Fatalf("binding aliases in separate export domains were accepted: %v", err)
	}
}

func TestBuildPortableToolProviderDAGV1RejectsBindingAliasAgainstOrdinaryExportAcrossDomains(t *testing.T) {
	plan := portableToolProviderTwoScopePlanWithDistinctAliasesV1()
	ordinary := &plan.Tools[1]
	ordinary.Responsibilities.BindingContracts = []PortableToolSelectedRecordV1{}
	ordinary.Responsibilities.BindingArtifacts = []PortableToolSelectedRecordV1{}
	ordinary.Exports[0].Name = "ordinary"
	ordinary.Exports[0].Path = plan.Tools[0].Exports[0].Path
	sort.Slice(ordinary.Exports, func(left, right int) bool { return ordinary.Exports[left].Name < ordinary.Exports[right].Name })
	if _, err := BuildPortableToolProviderDAGV1(
		portableToolProviderPlanFixtureV1(), plan,
		portableToolProviderDistinctAliasDomainsV1(),
	); err == nil || !strings.Contains(err.Error(), "export-path") {
		t.Fatalf("binding alias and ordinary export sharing a path were accepted: %v", err)
	}
}

func TestBuildPortableToolProviderDAGV1AllowsDistinctOrdinaryExportsSharingPath(t *testing.T) {
	plan := portableToolProviderTwoScopePlanV1()
	for index := range plan.Tools {
		plan.Tools[index].Responsibilities.BindingContracts = []PortableToolSelectedRecordV1{}
		plan.Tools[index].Responsibilities.BindingArtifacts = []PortableToolSelectedRecordV1{}
	}
	plan.Tools[1].Exports[0].Name = "ordinary"
	plan.Tools[1].Exports[0].Path = plan.Tools[0].Exports[0].Path
	sort.Slice(plan.Tools[1].Exports, func(left, right int) bool {
		return plan.Tools[1].Exports[left].Name < plan.Tools[1].Exports[right].Name
	})
	if _, err := BuildPortableToolProviderDAGV1(
		portableToolProviderPlanFixtureV1(), plan,
		[]PortableToolProviderDomainSetV1{
			portableToolProviderDomainV1("application:demo"),
			portableToolProviderDomainV1("application:other"),
		},
	); err != nil {
		t.Fatalf("distinct ordinary exports sharing a path were rejected: %v", err)
	}
}

func TestBuildPortableToolProviderDAGV1AllowsOrdinaryExportInsideRuntime(t *testing.T) {
	plan := representativePortableToolPlanV1()
	plan.Tools[0].Responsibilities.BindingContracts = []PortableToolSelectedRecordV1{}
	plan.Tools[0].Responsibilities.BindingArtifacts = []PortableToolSelectedRecordV1{}
	plan.Tools[0].Exports[0].Path = "/opt/demo/bin/demo"
	if _, err := BuildPortableToolProviderDAGV1(
		portableToolProviderPlanFixtureV1(), plan,
		[]PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application:demo")},
	); err != nil {
		t.Fatalf("ordinary export inside runtime rejected: %v", err)
	}
}

func TestBuildPortableToolProviderDAGV1RejectsAliasInsidePythonRuntime(t *testing.T) {
	plan := representativePortableToolPlanV1()
	pythonPath := "/opt/reploy/providers/python/application/demo/bin/demo"
	plan.Tools[0].Exports[0].Path = pythonPath
	setPortableToolBindingCLIPathV1(&plan.Tools[0], pythonPath)
	if _, err := BuildPortableToolProviderDAGV1(
		portableToolProviderPlanFixtureV1(), plan,
		[]PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application:demo")},
	); err == nil || !strings.Contains(err.Error(), "filesystem-domain conflict") {
		t.Fatalf("alias inside Python runtime was accepted: %v", err)
	}
}

func TestPortableToolPythonRuntimeRootV1BoundsLongApplication(t *testing.T) {
	root, err := portableToolPythonRuntimeRootV1("application:" + strings.Repeat("long-application-name-", 20) + "4")
	if err != nil {
		t.Fatal(err)
	}
	if len(root+"/bin/python")+3 > 127 || !strings.Contains(root, "/application/_") {
		t.Fatalf("long application runtime root = %q", root)
	}
}

func TestBuildPortableToolProviderDAGV1RejectsConflictingExportAliasClaims(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(PortableToolPlanV1)
	}{
		{name: "different alias target", mutate: func(plan PortableToolPlanV1) {
			entry := &plan.Tools[1]
			entry.Exports[0].Name = "other"
			entry.Exports[0].Path = "/opt/demo/bin/demo"
			setPortableToolBindingCLINameAndPathV1(entry, "other", "/opt/demo/bin/demo")
			sort.Slice(entry.Exports, func(left, right int) bool { return entry.Exports[left].Name < entry.Exports[right].Name })
		}},
		{name: "overlapping destinations", mutate: func(plan PortableToolPlanV1) {
			plan.Tools[1].Exports[0].Path = "/opt/demo/bin/demo/sub"
			setPortableToolBindingCLINameAndPathV1(&plan.Tools[1], "demo", "/opt/demo/bin/demo/sub")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := portableToolProviderTwoScopePlanV1()
			test.mutate(plan)
			domains := []PortableToolProviderDomainSetV1{
				portableToolProviderDomainV1("application:demo"),
				portableToolProviderDomainV1("application:other"),
			}
			domains[1].Binding.ID = "binding-other"
			domains[1].Filesystem.ID = "filesystem-other"
			domains[1].Capabilities.ID = "capabilities-other"
			if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), plan, domains); err == nil || !strings.Contains(err.Error(), "shared") {
				t.Fatalf("conflicting export alias claim was accepted: %v", err)
			}
		})
	}
}

func setPortableToolBindingCLINameAndPathV1(entry *PortableToolPlanEntryV1, name, cliPath string) {
	contract := &entry.Responsibilities.BindingContracts[0]
	cli := contract.Record.Value["cli"].(canonical.Object)
	cli["name"] = name
	cli["path"] = cliPath
	refreshPortableToolTestRecordDigest(&contract.Reference, contract.Record)
	artifact := &entry.Responsibilities.BindingArtifacts[0]
	artifact.Record.Value["contract"].(canonical.Object)["digest"] = string(contract.Reference.Digest)
	refreshPortableToolTestRecordDigest(&artifact.Reference, artifact.Record)
	refreshPortableToolBindingArtifactContractsForTest(entry)
}

func TestBuildPortableToolProviderDAGV1SortsDomainsAndClonesInputs(t *testing.T) {
	portablePlan := portableToolProviderTwoScopePlanWithDistinctAliasesV1()
	providerPlan := portableToolProviderPlanFixtureV1()
	portableBefore, err := canonical.Marshal(portablePlan)
	if err != nil {
		t.Fatal(err)
	}
	providerBefore, err := canonical.Marshal(providerPlan)
	if err != nil {
		t.Fatal(err)
	}
	domains := portableToolProviderDistinctAliasDomainsV1()
	domains[0], domains[1] = domains[1], domains[0]
	dag, err := BuildPortableToolProviderDAGV1(providerPlan, portablePlan, domains)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{dag.Domains[0].Scope, dag.Domains[1].Scope}; !reflect.DeepEqual(got, []string{"application:demo", "application:other"}) {
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
	setPortableToolBindingCLINameAndPathV1(&plan.Tools[1], "other", "/usr/local/bin/other")
	plan.Tools[1].Exports[0].Name = "other"
	plan.Tools[1].Exports[0].Path = "/usr/local/bin/other"
	sort.Slice(plan.Tools[1].Exports, func(left, right int) bool {
		return plan.Tools[1].Exports[left].Name < plan.Tools[1].Exports[right].Name
	})
	dag, err := BuildPortableToolProviderDAGV1(
		portableToolProviderPlanFixtureV1(), plan,
		[]PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application:demo")},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(dag.Domains) != 1 || dag.Domains[0].Scope != "application:demo" {
		t.Fatalf("domains = %#v", dag.Domains)
	}
	if len(dag.PortableToolPlan.Tools) != 2 || dag.PortableToolPlan.Tools[0].Provenance.Tool == dag.PortableToolPlan.Tools[1].Provenance.Tool {
		t.Fatalf("selected tools = %#v", dag.PortableToolPlan.Tools)
	}
}

func TestBuildPortableToolProviderDAGV1ValidatesDomainOwnersAndProjectsThem(t *testing.T) {
	basePlan := portableToolProviderPlanFixtureV1()
	validDomains := []PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application:demo")}
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
	twoDomains := []PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application:demo"), portableToolProviderDomainV1("application:other")}
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
	view := portableToolProviderGraphViewForTestV1(t, dag)
	for _, operation := range view.Operations {
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
	plan.Tools[0].Responsibilities.BindingContracts = []PortableToolSelectedRecordV1{}
	plan.Tools[0].Responsibilities.BindingArtifacts = []PortableToolSelectedRecordV1{}
	plan.Tools[0].Responsibilities.Payloads = []PortableToolSelectedRecordV1{}
	dag, err := BuildPortableToolProviderDAGV1(
		portableToolProviderPlanFixtureV1(), plan,
		[]PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application:demo")},
	)
	if err != nil {
		t.Fatal(err)
	}
	view := portableToolProviderGraphViewForTestV1(t, dag)
	if barrier := portableToolProviderOperationByKindV1(view.Operations, PortableToolOperationAcquisitionBarrierV1); barrier != nil {
		t.Fatalf("unexpected acquisition barrier operation: %#v", barrier)
	}
	if len(view.Dependencies) != len(plan.Tools[0].Exports) {
		t.Fatalf("dependencies = %#v, want one export -> capability edge per export", view.Dependencies)
	}
	if err := ValidatePortableToolProviderDAGV1(dag); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePortableToolProviderDAGV1InvokesExistingProviderValidators(t *testing.T) {
	providerPlan := portableToolProviderPlanFixtureV1()
	providerPlan.Schema = "provider-plan-v2"
	if _, err := BuildPortableToolProviderDAGV1(providerPlan, representativePortableToolPlanV1(), []PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application:demo")}); err == nil || !strings.Contains(err.Error(), "provider plan") {
		t.Fatalf("invalid provider plan error = %v", err)
	}
	portablePlan := representativePortableToolPlanV1()
	portablePlan.Schema = "portable-tool-plan-v2"
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), portablePlan, []PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application:demo")}); err == nil || !strings.Contains(err.Error(), "portable tool plan") {
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

func TestPortableToolProviderDAGV1DerivesOrderingWithoutPersistedOperations(t *testing.T) {
	dag := portableToolProviderDAGFixtureV1(t)
	view := portableToolProviderGraphViewForTestV1(t, dag)
	if len(view.Operations) == 0 || len(view.Dependencies) == 0 {
		t.Fatal("selected inputs did not derive a provider graph")
	}
	encoded, err := CanonicalPortableToolProviderDAGBytesV1(dag)
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &shape); err != nil {
		t.Fatal(err)
	}
	if _, exists := shape["operations"]; exists {
		t.Fatal("derived operations were persisted as lock authority")
	}
	if _, exists := shape["dependencies"]; exists {
		t.Fatal("derived dependencies were persisted as lock authority")
	}
	previousShape := struct {
		Schema           string                             `json:"schema"`
		ProviderPlan     ProviderPlanV1                     `json:"provider_plan"`
		PortableToolPlan PortableToolPlanV1                 `json:"portable_tool_plan"`
		Domains          []PortableToolProviderDomainSetV1  `json:"domains"`
		Operations       []PortableToolProviderOperationV1  `json:"operations"`
		Dependencies     []PortableToolProviderDependencyV1 `json:"dependencies"`
	}{dag.Schema, dag.ProviderPlan, dag.PortableToolPlan, dag.Domains, view.Operations, view.Dependencies}
	previousBytes, err := canonical.Marshal(previousShape)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) >= len(previousBytes) {
		t.Fatalf("derived lock shape did not shrink: current=%d previous=%d", len(encoded), len(previousBytes))
	}
	decoder := json.NewDecoder(bytes.NewReader(previousBytes))
	decoder.DisallowUnknownFields()
	var decoded PortableToolProviderDAGV1
	if err := decoder.Decode(&decoded); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("persisted redundant operation fields were accepted: %v", err)
	}
	t.Logf("provider DAG canonical bytes: %d -> %d (-%d)", len(previousBytes), len(encoded), len(previousBytes)-len(encoded))
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
			setPortableToolBindingCLIPathV1(&plan.Tools[1], "/other/export")
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
			domains := []PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application:demo"), portableToolProviderDomainV1("application:other")}
			if test.name != "export" {
				domains[1].Exports.ID = "exports-other"
			}
			_, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), plan, domains)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
	// Isolated authority domains do not conflict merely because scopes share a
	// name or selected value.
	plan := portableToolProviderTwoScopePlanWithDistinctAliasesV1()
	isolated := portableToolProviderDistinctAliasDomainsV1()
	isolated[1].Environment.ID = "other-environment"
	isolated[1].Exports.ID = "other-exports"
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), plan, isolated); err != nil {
		t.Fatalf("isolated shared claims rejected: %v", err)
	}

	capabilityConflictPlan := portableToolProviderTwoScopePlanV1()
	capabilityConflictPlan.Tools[1].Exports[0].Path = "/other/export"
	setPortableToolBindingCLIPathV1(&capabilityConflictPlan.Tools[1], "/other/export")
	capabilityConflictDomains := []PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application:demo"), portableToolProviderDomainV1("application:other")}
	capabilityConflictDomains[1].Exports.ID = "other-exports"
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), capabilityConflictPlan, capabilityConflictDomains); err == nil || !strings.Contains(err.Error(), "shared-domain conflict") {
		t.Fatalf("shared capability conflict was not rejected: %v", err)
	}

	fullyIsolatedPlan := portableToolProviderTwoScopePlanV1()
	fullyIsolatedPlan.Tools[1].Exports[0].Path = "/other/export"
	setPortableToolBindingCLIPathV1(&fullyIsolatedPlan.Tools[1], "/other/export")
	fullyIsolatedDomains := []PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application:demo"), portableToolProviderDomainV1("application:other")}
	fullyIsolatedDomains[1].Exports = PortableToolDomainAuthorityV1{ID: "other-exports", Owner: "base"}
	fullyIsolatedDomains[1].Capabilities = PortableToolDomainAuthorityV1{ID: "other-capabilities", Owner: "base"}
	fullyIsolatedDomains[1].Binding = PortableToolDomainAuthorityV1{ID: "other-binding", Owner: "python/application"}
	fullyIsolatedDomains[1].Filesystem = PortableToolDomainAuthorityV1{ID: "other-filesystem", Owner: "python/application"}
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), fullyIsolatedPlan, fullyIsolatedDomains); err != nil {
		t.Fatalf("split export and capability domains rejected: %v", err)
	}
}

func TestBuildPortableToolProviderDAGV1ComparesNativePackageSemantics(t *testing.T) {
	plan := portableToolProviderTwoScopePlanWithDistinctAliasesV1()
	firstPackage := &plan.Tools[0].Responsibilities.NativePackageSets[0]
	secondPackage := &plan.Tools[1].Responsibilities.NativePackageSets[0]
	setPortableToolPackageSemanticsV1(firstPackage, []string{"foo=1"}, []string{"main"})
	setPortableToolTestRecordID(&secondPackage.Reference, &secondPackage.Record, "tool:demo/releases/1.2.3/package-sets/other")
	setPortableToolPackageSemanticsV1(secondPackage, []string{"foo=1"}, []string{"main"})
	domains := portableToolProviderDistinctAliasDomainsV1()
	domains[1].Exports.ID = "exports-other"
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

func TestBuildPortableToolProviderDAGV1ComparesBindingPythonSemantics(t *testing.T) {
	plan := portableToolProviderTwoScopePlanWithDistinctAliasesV1()
	firstContract := &plan.Tools[0].Responsibilities.BindingContracts[0]
	secondContract := &plan.Tools[1].Responsibilities.BindingContracts[0]
	setPortableToolBindingPythonSemanticsV1(firstContract, []string{"demo>=1"}, []string{"3.11", "3.12"})
	setPortableToolTestRecordID(&secondContract.Reference, &secondContract.Record, "tool:demo/releases/1.2.3/bindings/other/contract")
	setPortableToolBindingPythonSemanticsV1(secondContract, []string{"demo<3"}, []string{"3.12", "3.13"})
	refreshPortableToolBindingArtifactContractsForTest(&plan.Tools[0])
	refreshPortableToolBindingArtifactContractsForTest(&plan.Tools[1])
	domains := []PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application:demo"), portableToolProviderDomainV1("application:other")}
	domains[1].Binding.ID = "binding-other"
	domains[1].Filesystem.ID = "filesystem-other"
	domains[1].Exports.ID = "exports-other"
	domains[1].Capabilities.ID = "capabilities-other"
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), plan, domains); err != nil {
		t.Fatalf("compatible Python constraints rejected: %v", err)
	}
	for _, requirement := range []string{"demo>=1rc1,<2", "demo>1rc1,<2", "demo>=1!2", "demo===1.*", "demo===v1.0"} {
		complexDuplicate := clonePortableToolPlanForTest(plan)
		setPortableToolBindingPythonSemanticsV1(&complexDuplicate.Tools[0].Responsibilities.BindingContracts[0], []string{requirement}, []string{"3.12"})
		setPortableToolBindingPythonSemanticsV1(&complexDuplicate.Tools[1].Responsibilities.BindingContracts[0], []string{requirement}, []string{"3.12"})
		refreshPortableToolBindingArtifactContractsForTest(&complexDuplicate.Tools[0])
		refreshPortableToolBindingArtifactContractsForTest(&complexDuplicate.Tools[1])
		if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), complexDuplicate, domains); err != nil {
			t.Fatalf("duplicate complex Python requirement %q rejected: %v", requirement, err)
		}
	}
	for _, requirement := range []string{"demo>=3,<3", "demo==1,==2", "demo==1.*,!=1.*", "demo===1.*,==1.*"} {
		contradictory := clonePortableToolPlanForTest(plan)
		setPortableToolBindingPythonSemanticsV1(&contradictory.Tools[0].Responsibilities.BindingContracts[0], []string{requirement}, []string{"3.12"})
		setPortableToolBindingPythonSemanticsV1(&contradictory.Tools[1].Responsibilities.BindingContracts[0], []string{requirement}, []string{"3.12"})
		refreshPortableToolBindingArtifactContractsForTest(&contradictory.Tools[0])
		refreshPortableToolBindingArtifactContractsForTest(&contradictory.Tools[1])
		if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), contradictory, domains); err == nil {
			t.Fatalf("contradictory Python requirement %q was accepted", requirement)
		}
	}
	mixedGranularity := clonePortableToolPlanForTest(plan)
	setPortableToolBindingPythonSemanticsV1(&mixedGranularity.Tools[0].Responsibilities.BindingContracts[0], []string{"demo>=1"}, []string{"3.12"})
	setPortableToolBindingPythonSemanticsV1(&mixedGranularity.Tools[1].Responsibilities.BindingContracts[0], []string{"demo<3"}, []string{"3.12.7"})
	refreshPortableToolBindingArtifactContractsForTest(&mixedGranularity.Tools[0])
	refreshPortableToolBindingArtifactContractsForTest(&mixedGranularity.Tools[1])
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), mixedGranularity, domains); err != nil {
		t.Fatalf("compatible Python series and exact patch rejected: %v", err)
	}

	disjointExactPatches := clonePortableToolPlanForTest(plan)
	setPortableToolBindingPythonSemanticsV1(&disjointExactPatches.Tools[0].Responsibilities.BindingContracts[0], []string{"demo>=1"}, []string{"3.12.6"})
	setPortableToolBindingPythonSemanticsV1(&disjointExactPatches.Tools[1].Responsibilities.BindingContracts[0], []string{"demo<3"}, []string{"3.12.7"})
	refreshPortableToolBindingArtifactContractsForTest(&disjointExactPatches.Tools[0])
	refreshPortableToolBindingArtifactContractsForTest(&disjointExactPatches.Tools[1])
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), disjointExactPatches, domains); err == nil || !strings.Contains(err.Error(), "shared-domain conflict") {
		t.Fatalf("disjoint exact Python patches were not rejected: %v", err)
	}

	canonicalArrays := clonePortableToolPlanForTest(plan)
	canonicalContract := &canonicalArrays.Tools[1].Responsibilities.BindingContracts[0]
	canonicalContract.Record.Value["requirements"] = []any{"demo<3"}
	canonicalContract.Record.Value["supported_python"] = []any{"3.12", "3.13"}
	refreshPortableToolTestRecordDigest(&canonicalContract.Reference, canonicalContract.Record)
	refreshPortableToolBindingArtifactContractsForTest(&canonicalArrays.Tools[1])
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), canonicalArrays, domains); err != nil {
		t.Fatalf("canonical binding arrays rejected: %v", err)
	}

	noCommonRequirements := clonePortableToolPlanForTest(plan)
	setPortableToolBindingPythonSemanticsV1(&noCommonRequirements.Tools[1].Responsibilities.BindingContracts[0], []string{"demo<1"}, []string{"3.12", "3.13"})
	refreshPortableToolBindingArtifactContractsForTest(&noCommonRequirements.Tools[1])
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), noCommonRequirements, domains); err == nil || !strings.Contains(err.Error(), "shared-domain conflict") {
		t.Fatalf("non-overlapping Python requirements were not rejected: %v", err)
	}

	noCommonSupportedPython := clonePortableToolPlanForTest(plan)
	setPortableToolBindingPythonSemanticsV1(&noCommonSupportedPython.Tools[1].Responsibilities.BindingContracts[0], []string{"demo<3"}, []string{"3.13"})
	refreshPortableToolBindingArtifactContractsForTest(&noCommonSupportedPython.Tools[1])
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), noCommonSupportedPython, domains); err == nil || !strings.Contains(err.Error(), "shared-domain conflict") {
		t.Fatalf("non-overlapping supported Python versions were not rejected: %v", err)
	}

	isolatedDomains := append([]PortableToolProviderDomainSetV1{}, domains...)
	isolatedDomains[1].PackageManager = PortableToolDomainAuthorityV1{ID: "pm-other", Owner: "apt"}
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), noCommonRequirements, isolatedDomains); err != nil {
		t.Fatalf("isolated Python constraints rejected: %v", err)
	}
}

func setPortableToolPackageSemanticsV1(selected *PortableToolSelectedRecordV1, requirements, repositories []string) {
	selected.Record.Value["manager"] = "apt"
	selected.Record.Value["requirements"] = append([]string{}, requirements...)
	selected.Record.Value["repositories"] = append([]string{}, repositories...)
	refreshPortableToolTestRecordDigest(&selected.Reference, selected.Record)
}

func setPortableToolBindingPythonSemanticsV1(selected *PortableToolSelectedRecordV1, requirements, supported []string) {
	selected.Record.Value["requirements"] = append([]string{}, requirements...)
	selected.Record.Value["supported_python"] = append([]string{}, supported...)
	refreshPortableToolTestRecordDigest(&selected.Reference, selected.Record)
}

func setPortableToolBindingCLIPathV1(entry *PortableToolPlanEntryV1, cliPath string) {
	contract := &entry.Responsibilities.BindingContracts[0]
	contract.Record.Value["cli"].(canonical.Object)["path"] = cliPath
	refreshPortableToolTestRecordDigest(&contract.Reference, contract.Record)
	refreshPortableToolBindingArtifactContractsForTest(entry)
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
			plan := portableToolProviderTwoScopePlanWithDistinctAliasesV1()
			secondPayload := &plan.Tools[1].Responsibilities.Payloads[0]
			setPortableToolTestRecordID(&secondPayload.Reference, &secondPayload.Record, "tool:demo/releases/1.2.3/payloads/other")
			test.mutate(plan)
			domains := portableToolProviderDistinctAliasSharedFilesystemDomainsV1()
			domains[1].Exports.ID = "exports-other"
			// Keep the filesystem cases independent from PTD-23.3.6's shared
			// export destination claim.
			_, err := BuildPortableToolProviderDAGV1(
				portableToolProviderPlanFixtureV1(), plan,
				domains,
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
	plan := portableToolProviderTwoScopePlanWithDistinctAliasesV1()
	plan.Tools[0].Runtime.InstallRoot = "/opt/first"
	plan.Tools[1].Runtime.InstallRoot = "/opt/second"
	firstPayload := &plan.Tools[0].Responsibilities.Payloads[0]
	secondPayload := &plan.Tools[1].Responsibilities.Payloads[0]
	setPortableToolPayloadPathsV1(firstPayload, "browser", "payload/demo")
	setPortableToolTestRecordID(&secondPayload.Reference, &secondPayload.Record, "tool:demo/releases/1.2.3/payloads/other")
	setPortableToolPayloadPathsV1(secondPayload, "browser", "payload/other")
	domains := portableToolProviderDistinctAliasSharedFilesystemDomainsV1()
	domains[1].Exports.ID = "exports-other"
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

func TestBuildPortableToolProviderDAGV1ClaimsPayloadLogicalPathByDigest(t *testing.T) {
	plan := portableToolProviderTwoScopePlanWithDistinctAliasesV1()
	plan.Tools[0].Runtime.InstallRoot = "/opt/first"
	plan.Tools[1].Runtime.InstallRoot = "/opt/second"
	setPortableToolPayloadPathsV1(&plan.Tools[0].Responsibilities.Payloads[0], "browser", "payload/shared")
	setPortableToolPayloadPathsV1(&plan.Tools[1].Responsibilities.Payloads[0], "browser", "payload/shared")
	domains := portableToolProviderDistinctAliasSharedFilesystemDomainsV1()
	domains[1].Exports.ID = "exports-other"
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), plan, domains); err != nil {
		t.Fatalf("equal logical path and digest rejected: %v", err)
	}

	conflicting := clonePortableToolPlanForTest(plan)
	setPortableToolTestRecordID(&conflicting.Tools[1].Responsibilities.Payloads[0].Reference,
		&conflicting.Tools[1].Responsibilities.Payloads[0].Record, "tool:demo/releases/1.2.3/payloads/other")
	conflicting.Tools[1].Responsibilities.Payloads[0].Record.Value["name"] = "different"
	setPortableToolPayloadPathsV1(&conflicting.Tools[1].Responsibilities.Payloads[0], "other-browser", "payload/shared")
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), conflicting, domains); err == nil || !strings.Contains(err.Error(), "shared-domain conflict") {
		t.Fatalf("equal logical path with different digest was not rejected: %v", err)
	}

	isolatedDomains := append([]PortableToolProviderDomainSetV1{}, domains...)
	isolatedDomains[1].Filesystem = PortableToolDomainAuthorityV1{ID: "filesystem-other", Owner: "python/application"}
	if _, err := BuildPortableToolProviderDAGV1(portableToolProviderPlanFixtureV1(), conflicting, isolatedDomains); err != nil {
		t.Fatalf("isolated logical path claims rejected: %v", err)
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
		[]PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application:demo")},
	)
	if err != nil {
		t.Fatal(err)
	}
	return dag
}

func portableToolProviderTwoScopeDAGFixtureV1(t *testing.T) PortableToolProviderDAGV1 {
	t.Helper()
	domains := portableToolProviderDistinctAliasDomainsV1()
	dag, err := BuildPortableToolProviderDAGV1(
		portableToolProviderPlanFixtureV1(),
		portableToolProviderTwoScopePlanWithDistinctAliasesV1(),
		domains,
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

func portableToolProviderDistinctAliasDomainsV1() []PortableToolProviderDomainSetV1 {
	domains := []PortableToolProviderDomainSetV1{
		portableToolProviderDomainV1("application:demo"),
		portableToolProviderDomainV1("application:other"),
	}
	domains[1].Binding.ID = "binding-other"
	domains[1].Filesystem.ID = "filesystem-other"
	domains[1].Exports.ID = "exports-other"
	domains[1].Capabilities.ID = "capabilities-other"
	return domains
}

func portableToolProviderDistinctAliasSharedFilesystemDomainsV1() []PortableToolProviderDomainSetV1 {
	domains := portableToolProviderDistinctAliasDomainsV1()
	domains[1].Filesystem.ID = domains[0].Filesystem.ID
	return domains
}

func portableToolProviderTwoScopePlanV1() PortableToolPlanV1 {
	plan := clonePortableToolPlanForTest(representativePortableToolPlanV1())
	second := clonePortableToolPlanForTest(plan).Tools[0]
	second.Scope = "application:other"
	second.SelectedClosureDigest = canonical.Digest("sha256:abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd")
	plan.Tools = append(plan.Tools, second)
	sort.Slice(plan.Tools, func(left, right int) bool { return plan.Tools[left].Scope < plan.Tools[right].Scope })
	return plan
}

func portableToolProviderTwoScopePlanWithDistinctAliasesV1() PortableToolPlanV1 {
	plan := portableToolProviderTwoScopePlanV1()
	contract := &plan.Tools[1].Responsibilities.BindingContracts[0]
	setPortableToolTestRecordID(&contract.Reference, &contract.Record, "tool:demo/releases/1.2.3/bindings/other/contract")
	artifact := &plan.Tools[1].Responsibilities.BindingArtifacts[0]
	setPortableToolTestRecordID(&artifact.Reference, &artifact.Record, "tool:demo/releases/1.2.3/bindings/other/artifacts/linux-amd64")
	artifact.Record.Value["name"] = "other"
	artifact.Record.Value["filename"] = "other-1.2.3-py3-none-any.whl"
	refreshPortableToolTestRecordDigest(&artifact.Reference, artifact.Record)
	refreshPortableToolBindingArtifactContractsForTest(&plan.Tools[1])
	plan.Tools[1].Exports[0].Path = "/usr/local/bin/demo-other"
	setPortableToolBindingCLIPathV1(&plan.Tools[1], plan.Tools[1].Exports[0].Path)
	return plan
}

func portableToolProviderTwoToolsOneScopePlanV1() PortableToolPlanV1 {
	plan := clonePortableToolPlanForTest(representativePortableToolPlanV1())
	second := clonePortableToolPlanForTest(plan).Tools[0]
	retargetPortableToolTestEntry(&second, "demo", "other")
	artifact := &second.Responsibilities.BindingArtifacts[0]
	artifact.Record.Value["name"] = "other"
	artifact.Record.Value["filename"] = "other-1.2.3-py3-none-any.whl"
	refreshPortableToolTestRecordDigest(&artifact.Reference, artifact.Record)
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
