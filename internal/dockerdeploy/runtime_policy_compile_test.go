package dockerdeploy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

func TestCompileRuntimePolicyCanonicalizesPlans(t *testing.T) {
	document := runtimePolicyDocument(t)
	plans := []deploy.RuntimePlanV1{
		{ID: "workload", Mounts: []deploy.RuntimeMountV1{
			{Destination: "/mnt/config", SourceKind: deploy.RuntimeMountSourceFile, ReadOnly: true},
			{Destination: "/data", SourceKind: deploy.RuntimeMountSourceDirectory},
		}, Executables: []providers.QualifiedOutput{}},
		{ID: "shell", Mounts: []deploy.RuntimeMountV1{}, Executables: []providers.QualifiedOutput{}},
	}
	policy, err := CompileRuntimePolicyV1(document, emptyRuntimePolicyGraph(), plans)
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.ProtectedPaths) != 2 || policy.ProtectedPaths[0].Path != deploy.ReployImageRoot || policy.ProtectedPaths[1].Path != deploy.ReployProviderRoot {
		t.Fatalf("protected paths = %#v", policy.ProtectedPaths)
	}
	if len(policy.Plans) != 2 || policy.Plans[0].ID != "shell" || policy.Plans[1].Mounts[0].Destination != "/data" {
		t.Fatalf("canonical plans = %#v", policy.Plans)
	}
}

func TestCompileRuntimePolicyAllowsAbsoluteTargetsAndRejectsOverlap(t *testing.T) {
	document := runtimePolicyDocument(t)
	if _, err := CompileRuntimePolicyV1(document, emptyRuntimePolicyGraph(), []deploy.RuntimePlanV1{{
		ID: "workload", Mounts: []deploy.RuntimeMountV1{{Destination: "/data", SourceKind: deploy.RuntimeMountSourceDirectory}}, Executables: []providers.QualifiedOutput{},
	}}); err != nil {
		t.Fatalf("absolute mount target: %v", err)
	}
	for _, test := range []struct {
		name   string
		mounts []deploy.RuntimeMountV1
		want   string
	}{
		{name: "filesystem root", mounts: []deploy.RuntimeMountV1{{Destination: "/", SourceKind: deploy.RuntimeMountSourceDirectory}}, want: "filesystem root"},
		{name: "kernel subtree", mounts: []deploy.RuntimeMountV1{{Destination: "/sys/fs", SourceKind: deploy.RuntimeMountSourceDirectory}}, want: "reserved container path"},
		{name: "overlap", mounts: []deploy.RuntimeMountV1{
			{Destination: "/mnt/data", SourceKind: deploy.RuntimeMountSourceDirectory},
			{Destination: "/mnt/data/cache", SourceKind: deploy.RuntimeMountSourceDirectory},
		}, want: "overlap"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := CompileRuntimePolicyV1(document, emptyRuntimePolicyGraph(), []deploy.RuntimePlanV1{{ID: "workload", Mounts: test.mounts, Executables: []providers.QualifiedOutput{}}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestCompileRuntimePolicyProtectsProviderRootsAndExecutableChains(t *testing.T) {
	transaction := rendererTransaction()
	generated := acceptedGeneratedExecutable(transaction)
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	graph := providers.GraphExecutionResult{
		Bundles: []providers.ResolvedBundle{acceptanceBundle(transaction, platform)},
		Materializations: []providers.GraphNodeMaterializeResult{{
			GeneratedExecutables: []providers.RealizedGeneratedExecutable{generated}, Outputs: []providers.RealizedOutput{},
		}},
		Catalog: []providers.RealizedOutput{},
	}
	document := runtimePolicyDocument(t)
	_, err = CompileRuntimePolicyV1(document, graph, []deploy.RuntimePlanV1{{
		ID: "shell", Mounts: []deploy.RuntimeMountV1{{Destination: "/opt/reploy/providers/python/web", SourceKind: deploy.RuntimeMountSourceDirectory}}, Executables: []providers.QualifiedOutput{},
	}})
	if err == nil || !strings.Contains(err.Error(), "protected") {
		t.Fatalf("protected allowed-root error = %v", err)
	}
	fixture := newPreparedPythonGraphReuseFixture(t)
	document = runtimePolicyDocument(t)
	graph = providers.GraphExecutionResult{
		Bundles: []providers.ResolvedBundle{}, Materializations: []providers.GraphNodeMaterializeResult{},
		Catalog: fixture.request.EarlierCatalog,
	}
	_, err = CompileRuntimePolicyV1(document, graph, []deploy.RuntimePlanV1{{
		ID: "shell", Mounts: []deploy.RuntimeMountV1{{Destination: "/usr/bin/python3", SourceKind: deploy.RuntimeMountSourceFile}},
		Executables: []providers.QualifiedOutput{},
	}})
	if err != nil {
		t.Fatalf("unreferenced executable should not be protected: %v", err)
	}
	_, err = CompileRuntimePolicyV1(document, graph, []deploy.RuntimePlanV1{{
		ID: "command/check", Mounts: []deploy.RuntimeMountV1{{Destination: "/usr/bin/python3", SourceKind: deploy.RuntimeMountSourceFile}},
		Executables: []providers.QualifiedOutput{{Component: "base", Name: "python"}},
	}})
	if err == nil || !strings.Contains(err.Error(), "protected") {
		t.Fatalf("protected executable error = %v", err)
	}
}

func TestCompileRuntimePolicyRejectsExecutableAbsentFromFinalGraph(t *testing.T) {
	_, err := CompileRuntimePolicyV1(runtimePolicyDocument(t), emptyRuntimePolicyGraph(), []deploy.RuntimePlanV1{{
		ID: "workload", Mounts: []deploy.RuntimeMountV1{},
		Executables: []providers.QualifiedOutput{{Component: "application", Name: "serve"}},
	}})
	if err == nil || !strings.Contains(err.Error(), "absent from the final provider graph") {
		t.Fatalf("missing executable error = %v", err)
	}
}

func TestCompileRuntimePolicyFromLockMatchesGraphCompilation(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	bundle, err := providers.LoadResolvedBundleManifest(fixture.store, fixture.lock.Nodes[0].BundleManifest, pythonprovider.ValidateResolvedBundlePayloadV1)
	if err != nil {
		t.Fatal(err)
	}
	document := runtimePolicyDocument(t)
	plans := []deploy.RuntimePlanV1{{
		ID: "shell", Mounts: []deploy.RuntimeMountV1{},
		Executables: []providers.QualifiedOutput{{Component: "base", Name: "python"}},
	}}
	graph := providers.GraphExecutionResult{
		Bundles: []providers.ResolvedBundle{bundle},
		Materializations: []providers.GraphNodeMaterializeResult{{
			GeneratedExecutables: fixture.lock.Nodes[0].GeneratedExecutables,
			Outputs:              fixture.lock.Nodes[0].Outputs,
		}},
		Catalog: append([]providers.RealizedOutput{}, fixture.lock.Catalog...),
	}
	fromGraph, err := CompileRuntimePolicyV1(document, graph, plans)
	if err != nil {
		t.Fatal(err)
	}
	fromLock, err := CompileRuntimePolicyFromLockV1(document, fixture.lock, plans)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromLock, fromGraph) {
		t.Fatalf("lock policy = %#v, graph policy = %#v", fromLock, fromGraph)
	}
}

func runtimePolicyDocument(t *testing.T) blueprint.Document {
	t.Helper()
	document := blueprint.Document{Docker: blueprint.Docker{}}
	return document
}

func emptyRuntimePolicyGraph() providers.GraphExecutionResult {
	return providers.GraphExecutionResult{
		Bundles: []providers.ResolvedBundle{}, Materializations: []providers.GraphNodeMaterializeResult{}, Catalog: []providers.RealizedOutput{},
	}
}
