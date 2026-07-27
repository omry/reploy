package dockerdeploy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
)

func TestPlanCurrentAppCommandV1MatchesForwardsAndUsesLockedOutput(t *testing.T) {
	document := commandTestDocument()
	application := document.Environment.Applications["application"]
	application.Executables["server"] = blueprint.Executable{
		Source: "python", Binary: "demo", ArgvPrefix: []string{"--prefix"},
		ArgvSuffix: []string{"phase={{ reploy.phase }}"},
	}
	document.Environment.Applications["application"] = application
	plan := CurrentRuntimePlanV1{
		Document: document,
		Docker:   DockerExecutionPlan{Phase: blueprint.PhaseInstalled},
	}
	catalog := []providers.RealizedOutput{{
		SupplierComponent: "application/application/python", Name: "demo",
		Candidate: providers.ExecutableCandidate{InvocationPath: "/opt/demo"},
		Evidence:  providers.ExecutableEvidence{InvocationPath: "/opt/demo"},
	}}

	command, err := PlanCurrentAppCommandV1(plan, catalog, []string{"config", "show", "--", "value"}, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/demo", "show", "phase=installed", "value"}
	if !reflect.DeepEqual(command.Argv, want) || command.Name != "special" {
		t.Fatalf("command = %#v, want argv %#v", command, want)
	}
}

func TestPlanCurrentAppCommandV1EnforcesDeploymentExposureAndLockedCatalog(t *testing.T) {
	plan := CurrentRuntimePlanV1{Document: commandTestDocument(), Docker: DockerExecutionPlan{Phase: blueprint.PhaseInstalled}}
	if _, err := PlanCurrentAppCommandV1(plan, nil, []string{"config", "show"}, true); err == nil || !strings.Contains(err.Error(), "unknown environment command") {
		t.Fatalf("undeployed command error = %v", err)
	}
	if _, err := PlanCurrentAppCommandV1(plan, nil, []string{"serve"}, true); err == nil || !strings.Contains(err.Error(), "locked output catalog") {
		t.Fatalf("missing locked output error = %v", err)
	}
}
