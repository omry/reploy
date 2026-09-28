package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

func sealSyntheticApplicationPortablePythonSelectionForTest(
	t *testing.T,
	selected *ApplicationPortableToolPlanV1,
	base deploy.ImageDescriptor,
	config providers.ImageConfigPolicy,
) {
	t.Helper()
	selected.Target = sourceBuilderJavaTarget("debian", "12")
	var err error
	selected.sealed, err = sealApplicationPortablePythonSelectionV1(selected, base, config)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSealApplicationPortablePythonSelectionRejectsDomainSubstitution(t *testing.T) {
	input := applicationPortableInputForTest(t, "[{tool: playwright, version: 1.61.0, binding: python, select: {browser: [chromium]}}]")
	calls := 0
	stubApplicationPortableTargetForTest(t, &calls)
	selected, err := PlanApplicationPortableToolsV1(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.DAG.Domains) != 1 {
		t.Fatalf("domains = %#v", selected.DAG.Domains)
	}
	tests := []struct {
		name   string
		change func(*providers.PortableToolProviderDomainSetV1)
	}{
		{"binding authority ID", func(domain *providers.PortableToolProviderDomainSetV1) {
			domain.Binding.ID = "provider/python/web/other-binding"
		}},
		{"binding authority owner", func(domain *providers.PortableToolProviderDomainSetV1) {
			domain.Binding.Owner = "base"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tampered := *selected
			tampered.DAG = selected.DAG
			tampered.DAG.Domains = append([]providers.PortableToolProviderDomainSetV1{}, selected.DAG.Domains...)
			test.change(&tampered.DAG.Domains[0])
			if err := providers.ValidatePortableToolProviderDAGV1(tampered.DAG); err != nil {
				t.Fatalf("substituted domain must remain structurally valid: %v", err)
			}
			if _, err := sealApplicationPortablePythonSelectionV1(&tampered, input.Base, input.FinalImageConfig); err == nil || !strings.Contains(err.Error(), "DAG domains do not match") {
				t.Fatalf("canonical domain rejection = %v", err)
			}
		})
	}
}

func TestExecuteApplicationPortablePythonGraphRejectsUnjoinedInputsBeforeAcquisition(t *testing.T) {
	input := applicationPortableInputForTest(t, "[{tool: playwright, version: 1.61.0, binding: python, select: {browser: [chromium]}}]")
	calls := 0
	stubApplicationPortableTargetForTest(t, &calls)
	selected, err := PlanApplicationPortableToolsV1(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("target observations = %d", calls)
	}
	ordinary, err := registry.Plan(providers.PlanInput{Components: input.Components, Platform: input.Platform})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		change func(*ApplicationPortableToolPlanV1, *PreparedPythonGraphExecutionInput)
		want   string
	}{
		{"missing seal", func(plan *ApplicationPortableToolPlanV1, _ *PreparedPythonGraphExecutionInput) {
			plan.sealed = nil
		}, "sealed preflight selection"},
		{"ordinary graph substituted", func(_ *ApplicationPortableToolPlanV1, graph *PreparedPythonGraphExecutionInput) {
			graph.Plan = ordinary
		}, "provider graph differs"},
		{"caller binding override", func(_ *ApplicationPortableToolPlanV1, graph *PreparedPythonGraphExecutionInput) {
			graph.PortablePython = &PortableToolPythonFreshPlanV1{}
		}, "may not override"},
		{"caller selection override", func(_ *ApplicationPortableToolPlanV1, graph *PreparedPythonGraphExecutionInput) {
			graph.DesiredPortableToolPlan = &providers.PortableToolPlanV1{}
		}, "may not override"},
		{"target view drift", func(plan *ApplicationPortableToolPlanV1, _ *PreparedPythonGraphExecutionInput) {
			plan.Target.VersionID = "13"
		}, "target or operation snapshot differs"},
		{"snapshot view drift", func(plan *ApplicationPortableToolPlanV1, _ *PreparedPythonGraphExecutionInput) {
			plan.Snapshot.CanonicalJSON = "{}"
		}, "target or operation snapshot differs"},
		{"same-platform base drift", func(_ *ApplicationPortableToolPlanV1, graph *PreparedPythonGraphExecutionInput) {
			graph.BaseDescriptor.AuthorReference = "docker.io/library/ubuntu:24.04"
		}, "execution base differs"},
		{"image config drift", func(_ *ApplicationPortableToolPlanV1, graph *PreparedPythonGraphExecutionInput) {
			graph.FinalImageConfig.Environment = append(graph.FinalImageConfig.Environment,
				providers.EnvironmentVariable{Name: "DRIFT", Value: "yes"})
		}, "execution image config differs"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := *selected
			graph := PreparedPythonGraphExecutionInput{
				Plan: selected.DAG.ProviderPlan, BaseDescriptor: input.Base,
				FinalImageConfig: input.FinalImageConfig,
			}
			test.change(&plan, &graph)
			_, err := ExecuteApplicationPortablePythonGraphV1(context.Background(), &plan, graph)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ExecuteApplicationPortablePythonGraphV1(cancelled, selected, PreparedPythonGraphExecutionInput{
		Plan: selected.DAG.ProviderPlan, BaseDescriptor: input.Base,
		FinalImageConfig: input.FinalImageConfig,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("valid selected plan did not reach the graph boundary: %v", err)
	}
}

func TestSealApplicationPortablePythonSelectionRejectsLockedClosureMismatchBeforeGraph(t *testing.T) {
	tests := []struct {
		name   string
		change func(*ApplicationPortableToolPlanV1)
		want   string
	}{
		{name: "nil closures", change: func(plan *ApplicationPortableToolPlanV1) {
			plan.Closures = nil
		}, want: "closures"},
		{name: "conflicting closure", change: func(plan *ApplicationPortableToolPlanV1) {
			plan.Closures[0].Identity = canonical.Digest("sha256:" + strings.Repeat("0", 64))
		}, want: "do not match selected plan"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reuse := newPreparedPythonGraphReuseFixture(t)
			locked := newPortableToolPythonLockedTestFixture(t)
			domains, err := applicationPortableProviderDomainsV1(locked.fresh.Plan, reuse.request.Plan)
			if err != nil {
				t.Fatal(err)
			}
			dag, err := providers.BuildPortableToolProviderDAGV1(reuse.request.Plan, locked.fresh.Plan, domains)
			if err != nil {
				t.Fatal(err)
			}
			_, projection, err := pythonprovider.ProjectPortableToolPythonBindingsV1(locked.fresh.Plan, []providers.ResolvedComponentRequestV1{})
			if err != nil {
				t.Fatal(err)
			}
			selected := &ApplicationPortableToolPlanV1{
				Plan: locked.fresh.Plan, DAG: dag, Closures: locked.fresh.Closures, PythonProjection: projection,
			}
			selected.Target = sourceBuilderJavaTarget("debian", "12")
			matches, err := portableToolPythonSelectionsMatchCurrentBuildV1(&locked.lock, &selected.Plan)
			if err != nil {
				t.Fatal(err)
			}
			if !matches {
				t.Fatal("test lock does not match selected plan")
			}
			test.change(selected)

			_, err = sealApplicationPortablePythonSelectionV1(selected, reuse.lock.Base, pythonConsumerTestImageConfig())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPortablePythonAcquisitionCollectorRejectsIncompleteAndDuplicate(t *testing.T) {
	fresh := portableToolPythonFreshPlaywrightFixtureV1(t, "application:application")
	collector := &portablePythonAcquisitionCollectorV1{}
	if _, err := collector.snapshot(); err == nil || !strings.Contains(err.Error(), "no binding acquisitions") {
		t.Fatalf("missing acquisition error = %v", err)
	}
	binding := fresh.Plan.Tools[0].Responsibilities.BindingArtifacts[0]
	collector.inputs = []providers.PortableToolArtifactAcquisitionInputV1{
		{Scope: fresh.Plan.Tools[0].Scope, Tool: fresh.Plan.Tools[0].Provenance.Tool, Artifact: binding.Reference},
		{Scope: fresh.Plan.Tools[0].Scope, Tool: fresh.Plan.Tools[0].Provenance.Tool, Artifact: binding.Reference},
	}
	if _, err := collector.snapshot(); err == nil || !strings.Contains(err.Error(), "duplicate binding acquisition") {
		t.Fatalf("duplicate acquisition error = %v", err)
	}
	collector.inputs = collector.inputs[:1]
	got, err := collector.snapshot()
	if err != nil || len(got) != 1 || got[0].Artifact != binding.Reference {
		t.Fatalf("transport snapshot = %#v, error = %v", got, err)
	}
}

func TestExecuteApplicationPortablePythonGraphAcceptsReboundNeutralSelection(t *testing.T) {
	fresh, records := portableToolPythonFreshTwoNeutralFixtureV1(t)
	component := portableToolPythonFreshComponentForTestV1(t, &fresh)
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	descriptors := map[string]providerstore.ArtifactDescriptor{}
	for _, binding := range component.Bindings {
		content := preparedPTD2337NeutralBindingWheelV1(t,
			strings.ReplaceAll(binding.Distribution, "-", "_"), binding.Distribution,
			binding.CLI.Name, "neutral acceptance")
		descriptor, err := store.Publish(context.Background(), "wheels/"+binding.Wheel.Filename, "wheel", bytes.NewReader(content))
		if err != nil {
			t.Fatal(err)
		}
		descriptors[binding.Distribution] = descriptor
	}
	rebindPortableToolPythonFreshTwoNeutralRecordsV1(t, &fresh, &records, descriptors)
	plan := preparedPythonResolveRequest(t, testProbeImageDescriptor(t, "linux/amd64")).Plan
	domains, err := applicationPortableProviderDomainsV1(fresh.Plan, plan)
	if err != nil {
		t.Fatal(err)
	}
	dag, err := providers.BuildPortableToolProviderDAGV1(plan, fresh.Plan, domains)
	if err != nil {
		t.Fatal(err)
	}
	_, projection, err := pythonprovider.ProjectPortableToolPythonBindingsV1(fresh.Plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	selected := &ApplicationPortableToolPlanV1{Plan: fresh.Plan, DAG: dag, Closures: fresh.Closures, PythonProjection: projection}
	base := testProbeImageDescriptor(t, "linux/amd64")
	config := pythonConsumerTestImageConfig()
	sealSyntheticApplicationPortablePythonSelectionForTest(t, selected, base, config)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ExecuteApplicationPortablePythonGraphV1(cancelled, selected, PreparedPythonGraphExecutionInput{
		Plan: plan, BaseDescriptor: base, FinalImageConfig: config,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("rebound selected closure did not reach graph boundary: %v", err)
	}
}
