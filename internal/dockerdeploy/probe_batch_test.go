package dockerdeploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestCollectFullImageExecutableEvidenceUsesOneWorkspaceAndContainer(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	checks := []FullImageExecutableProbe{
		testFullImageExecutableCheck("pip", "pip3"),
		testFullImageExecutableCheck("interpreter", "python3"),
	}
	previousPrepare := prepareImageProbeWorkspace
	previousRun := runPreparedImageProbe
	t.Cleanup(func() {
		prepareImageProbeWorkspace = previousPrepare
		runPreparedImageProbe = previousRun
	})
	prepareCalls := 0
	cleanupCalls := 0
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	prepareImageProbeWorkspace = func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		prepareCalls++
		return workspace, func() error { cleanupCalls++; return nil }, nil
	}
	runCalls := 0
	runPreparedImageProbe = func(_ context.Context, gotDescriptor deploy.ImageDescriptor, gotWorkspace PreparedProbeWorkspace, request probe.RequestV1) (probe.ResponseV1, error) {
		runCalls++
		if gotDescriptor.ImmutableReference != descriptor.ImmutableReference || gotWorkspace.HostDir != workspace.HostDir {
			t.Fatalf("probe run target = %#v, %#v", gotDescriptor, gotWorkspace)
		}
		if len(request.Inspections) != 2 || request.Inspections[0].ID != "interpreter" || request.Inspections[1].ID != "pip" {
			t.Fatalf("sorted probe request = %#v", request)
		}
		observations := make([]probe.ExecutableObservationV1, 0, len(request.Inspections))
		for _, inspection := range request.Inspections {
			observation := testExecutableObservation()
			observation.ID = inspection.ID
			observation.InvocationPath = inspection.InvocationPath
			observation.Links[0].Path = inspection.InvocationPath
			observations = append(observations, observation)
		}
		return probe.ResponseV1{Schema: probe.ResponseSchemaV1, Observations: observations}, nil
	}
	evidence, err := CollectFullImageExecutableEvidence(context.Background(), providerstore.Store{}, descriptor, checks)
	if err != nil {
		t.Fatal(err)
	}
	if prepareCalls != 1 || runCalls != 1 || cleanupCalls != 1 {
		t.Fatalf("calls prepare=%d run=%d cleanup=%d", prepareCalls, runCalls, cleanupCalls)
	}
	if len(evidence) != 2 || evidence[0].RequirementID != "" || evidence[1].RequirementID != "" {
		t.Fatalf("evidence = %#v", evidence)
	}
}

func TestCollectFullImageExecutableEvidenceSkipsEmptyBatch(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	previous := prepareImageProbeWorkspace
	t.Cleanup(func() { prepareImageProbeWorkspace = previous })
	prepareImageProbeWorkspace = func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		t.Fatal("empty batch prepared a helper")
		return PreparedProbeWorkspace{}, nil, nil
	}
	evidence, err := CollectFullImageExecutableEvidence(context.Background(), providerstore.Store{}, descriptor, []FullImageExecutableProbe{})
	if err != nil {
		t.Fatal(err)
	}
	if evidence == nil || len(evidence) != 0 {
		t.Fatalf("empty evidence = %#v", evidence)
	}
}

func TestCollectFullImageExecutableEvidenceRejectsInvalidDescriptorBeforeWorkspace(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	descriptor.ImmutableReference = "mutable:latest"
	previous := prepareImageProbeWorkspace
	t.Cleanup(func() { prepareImageProbeWorkspace = previous })
	prepareImageProbeWorkspace = func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		t.Fatal("invalid descriptor prepared a helper")
		return PreparedProbeWorkspace{}, nil, nil
	}
	_, err := CollectFullImageExecutableEvidence(context.Background(), providerstore.Store{}, descriptor, []FullImageExecutableProbe{testFullImageExecutableCheck("interpreter", "python3")})
	if err == nil || !strings.Contains(err.Error(), "descriptor") {
		t.Fatalf("descriptor error = %v", err)
	}
}

func TestCollectFullImageExecutableEvidenceCleansWorkspaceAfterRunFailure(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	previousPrepare := prepareImageProbeWorkspace
	previousRun := runPreparedImageProbe
	t.Cleanup(func() {
		prepareImageProbeWorkspace = previousPrepare
		runPreparedImageProbe = previousRun
	})
	cleaned := false
	prepareImageProbeWorkspace = func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		return testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir()), func() error { cleaned = true; return nil }, nil
	}
	runPreparedImageProbe = func(context.Context, deploy.ImageDescriptor, PreparedProbeWorkspace, probe.RequestV1) (probe.ResponseV1, error) {
		return probe.ResponseV1{}, errors.New("Docker failed")
	}
	_, err := CollectFullImageExecutableEvidence(context.Background(), providerstore.Store{}, descriptor, []FullImageExecutableProbe{testFullImageExecutableCheck("interpreter", "python3")})
	if err == nil || !strings.Contains(err.Error(), "Docker failed") || !cleaned {
		t.Fatalf("run error = %v, cleaned=%t", err, cleaned)
	}
}

func TestCollectFullImageExecutableEvidenceRejectsConsumerRequirement(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	check := testFullImageExecutableCheck("interpreter", "python3")
	check.Binding.Requirement = &providers.ExecutableRequirement{
		ID: "interpreter", Command: "python3", Supplier: "runtime", ValidationPolicy: providers.ValidationPolicyCompatible,
	}
	previous := prepareImageProbeWorkspace
	t.Cleanup(func() { prepareImageProbeWorkspace = previous })
	prepareImageProbeWorkspace = func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		t.Fatal("consumer requirement prepared a standalone helper")
		return PreparedProbeWorkspace{}, nil, nil
	}
	_, err := CollectFullImageExecutableEvidence(context.Background(), providerstore.Store{}, descriptor, []FullImageExecutableProbe{check})
	if err == nil || !strings.Contains(err.Error(), "must not carry a consumer requirement") {
		t.Fatalf("consumer requirement error = %v", err)
	}
}

func testFullImageExecutableCheck(id string, command string) FullImageExecutableProbe {
	return FullImageExecutableProbe{
		ID: id, InvocationPath: "/usr/bin/python3",
		Binding: ProbeExecutableBinding{
			Output: providers.QualifiedOutput{Component: "runtime", Name: command},
			Facts:  providers.CanonicalProviderData{Schema: "test-executable-facts-v1", Value: canonical.Object{}},
		},
	}
}
