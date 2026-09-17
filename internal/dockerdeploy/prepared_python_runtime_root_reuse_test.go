package dockerdeploy

import (
	"path"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

func TestPlannedAPTExclusiveRootsUsesBoundedPythonRuntimeRoot(t *testing.T) {
	application := strings.Repeat("long-application-", 10) + "x"
	component := blueprint.ApplicationContributionID(application, blueprint.ContributionProviderPython)
	plan := providers.ProviderPlanV1{Nodes: []providers.NodeSpec{{
		ID: providers.NodeID("python/application/" + application), Provider: blueprint.ComponentTypePython,
		Components: []string{component},
	}}}

	got, err := plannedAPTExclusiveRoots(plan)
	if err != nil {
		t.Fatal(err)
	}
	want, err := pythonprovider.RuntimeRootV1(component)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("planned Python runtime roots = %#v, want %q", got, want)
	}
	if strings.Contains(got[0], application) {
		t.Fatalf("bounded Python runtime root retained the long application name: %q", got[0])
	}
	if gotPath := path.Join(got[0], "bin", "python"); len(gotPath)+3 > 127 {
		t.Fatalf("bounded Python interpreter path length = %d: %q", len(gotPath)+3, gotPath)
	}
}
