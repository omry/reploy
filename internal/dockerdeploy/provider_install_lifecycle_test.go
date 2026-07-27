package dockerdeploy

import (
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
)

func TestLockedLifecycleV1PlansStartStopAndRestartFromPublishedCatalog(t *testing.T) {
	document, dockerPlan, catalog := lockedLifecycleFixtureV1()
	start, err := planLockedStartLifecycleV1(document, dockerPlan, catalog)
	if err != nil {
		t.Fatal(err)
	}
	stop, err := planLockedStopLifecycleV1(document, dockerPlan, catalog)
	if err != nil {
		t.Fatal(err)
	}
	restart, err := planLockedRestartLifecycleV1(document, dockerPlan, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if got := lifecycleKindsV1(start); !reflect.DeepEqual(got, []LifecycleOperationKind{LifecycleCommand, LifecycleStart, LifecycleCommand}) {
		t.Fatalf("start lifecycle = %v", got)
	}
	if got := lifecycleKindsV1(stop); !reflect.DeepEqual(got, []LifecycleOperationKind{LifecycleCommand, LifecycleStop, LifecycleCommand}) {
		t.Fatalf("stop lifecycle = %v", got)
	}
	wantRestart := append(lifecycleKindsV1(stop), lifecycleKindsV1(start)...)
	if got := lifecycleKindsV1(restart); !reflect.DeepEqual(got, wantRestart) {
		t.Fatalf("restart lifecycle = %v, want %v", got, wantRestart)
	}
	for _, plan := range []LifecyclePlan{start, stop, restart} {
		for _, operation := range plan.Operations {
			if operation.Kind == LifecycleCommand && !reflect.DeepEqual(operation.Command.Argv, []string{"/opt/demo", "--prefix", "serve", "--suffix"}) {
				t.Fatalf("locked lifecycle argv = %#v", operation.Command.Argv)
			}
		}
	}
}

func TestLockedLifecycleV1RejectsMissingPublishedOutput(t *testing.T) {
	document, dockerPlan, _ := lockedLifecycleFixtureV1()
	if _, err := planLockedStartLifecycleV1(document, dockerPlan, []providers.RealizedOutput{}); err == nil {
		t.Fatal("start lifecycle accepted a missing locked output")
	}
	if _, err := planLockedStopLifecycleV1(document, dockerPlan, []providers.RealizedOutput{}); err == nil {
		t.Fatal("stop lifecycle accepted a missing locked output")
	}
}

func lockedLifecycleFixtureV1() (blueprint.Document, DockerExecutionPlan, []providers.RealizedOutput) {
	steps := []blueprint.Step{{Actions: []blueprint.Action{{Environment: []string{"serve"}}}}}
	document := commandTestDocument()
	document.Environment.Workload = &blueprint.Workload{
		Command: "serve",
		Runtime: blueprint.RuntimeEvents{
			BeforeStart: steps, AfterStart: steps,
			BeforeStop: steps, AfterStop: steps,
		},
	}
	plan := DockerExecutionPlan{Phase: blueprint.PhaseStaged, Workload: &WorkloadExecutionPlan{Command: "serve"}}
	catalog := []providers.RealizedOutput{{
		SupplierComponent: "application/application/python", SupplierNode: "python/application/application", Name: "demo",
		Candidate: providers.ExecutableCandidate{InvocationPath: "/opt/demo"},
		Evidence:  providers.ExecutableEvidence{InvocationPath: "/opt/demo"},
	}}
	return document, plan, catalog
}

func lifecycleKindsV1(plan LifecyclePlan) []LifecycleOperationKind {
	result := make([]LifecycleOperationKind, len(plan.Operations))
	for index, operation := range plan.Operations {
		result[index] = operation.Kind
	}
	return result
}
