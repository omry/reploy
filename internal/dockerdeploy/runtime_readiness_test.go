package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

func TestRequireRuntimeReadyAcceptsMatchingBuildAndGeneratedOnlyPlan(t *testing.T) {
	current, buildInput := runtimeCurrentBuildFixture(t)
	if err := RequireRuntimeReady(RuntimeReadinessInput{
		Current: current, DockerPlan: buildInput.DockerPlan, PlanID: "shell", Sources: []RuntimeHostSourceV1{},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeInvocationV1UsesCompiledPlanIdentities(t *testing.T) {
	root := t.TempDir()
	plan := DockerExecutionPlan{Mounts: []MountExecutionPlan{{
		Name: "config", Mode: blueprint.MountManagedBind, Source: root,
		SourceKind: deploy.RuntimeMountSourceDirectory, Target: "/mnt/config", ReadOnly: true,
	}}}
	workload, err := WorkloadRuntimeInvocationV1(plan)
	if err != nil || workload.PlanID != runtimeWorkloadPlanID || len(workload.Sources) != 1 {
		t.Fatalf("workload invocation = %#v, %v", workload, err)
	}
	shell, err := ShellRuntimeInvocationV1(plan)
	if err != nil || shell.PlanID != runtimeShellPlanID || len(shell.Sources) != 1 {
		t.Fatalf("shell invocation = %#v, %v", shell, err)
	}
	output := &transientOutputMount{HostDirectory: root, Variable: runtimeOutputDirectoryVariable, ContainerPath: runtimeOutputRoot}
	command, err := CommandRuntimeInvocationV1(plan, "check", output)
	if err != nil || command.PlanID != "command/check/output" || len(command.Sources) != 2 {
		t.Fatalf("command invocation = %#v, %v", command, err)
	}
	if _, err := CommandRuntimeInvocationV1(plan, "", nil); err == nil {
		t.Fatal("empty command name unexpectedly accepted")
	}
}

func TestRunPublishedRuntimeContainerV1GatesRunnerAndPassesExactBuild(t *testing.T) {
	current, buildInput := runtimeCurrentBuildFixture(t)
	invocation, err := ShellRuntimeInvocationV1(buildInput.DockerPlan)
	if err != nil {
		t.Fatal(err)
	}
	order := []string{}
	input := PublishedRuntimeContainerInput{
		Environment: "demo", DeploymentDir: "/srv/demo", DockerPlan: buildInput.DockerPlan, Invocation: invocation,
	}
	err = runPublishedRuntimeContainerV1(t.Context(), input, func(
		_ context.Context,
		_ *deploy.OperationLock,
		_ providerstore.Store,
		_ string,
		_ string,
	) (CurrentBuild, bool, error) {
		order = append(order, "load")
		return current, true, nil
	}, func(_ context.Context, got CurrentBuild) error {
		order = append(order, "run")
		if !reflect.DeepEqual(got, current) {
			t.Fatalf("runner build = %#v, want %#v", got, current)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"load", "run"}) {
		t.Fatalf("order = %v", order)
	}
}

func TestRunPublishedRuntimeContainerV1NeverRunsForStaleBuild(t *testing.T) {
	current, buildInput := runtimeCurrentBuildFixture(t)
	invocation, err := ShellRuntimeInvocationV1(buildInput.DockerPlan)
	if err != nil {
		t.Fatal(err)
	}
	current.Lock.Base.AuthorReference = "debian:changed"
	refreshCurrentBuildReuseGeneration(t, &current)
	runs := 0
	err = runPublishedRuntimeContainerV1(t.Context(), PublishedRuntimeContainerInput{
		Environment: "demo", DeploymentDir: "/srv/demo", DockerPlan: buildInput.DockerPlan, Invocation: invocation,
	}, func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
		return current, true, nil
	}, func(context.Context, CurrentBuild) error {
		runs++
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "run `reploy build`") {
		t.Fatalf("error = %v", err)
	}
	if runs != 0 {
		t.Fatalf("runner called %d times", runs)
	}
}

func TestRunPublishedRuntimeContainerV1RejectsMissingBoundaryInputs(t *testing.T) {
	if err := runPublishedRuntimeContainerV1(t.Context(), PublishedRuntimeContainerInput{}, nil, func(context.Context, CurrentBuild) error {
		return nil
	}); err == nil || !strings.Contains(err.Error(), "plan") {
		t.Fatalf("missing plan error = %v", err)
	}
	if err := runPublishedRuntimeContainerV1(t.Context(), PublishedRuntimeContainerInput{
		Invocation: RuntimeInvocationV1{PlanID: runtimeShellPlanID},
	}, nil, nil); err == nil || !strings.Contains(err.Error(), "runner") {
		t.Fatalf("missing runner error = %v", err)
	}
}

func TestRequirePublishedRuntimeReadyLoadsCurrentBuildWithoutMutation(t *testing.T) {
	current, buildInput := runtimeCurrentBuildFixture(t)
	loaded := 0
	result, err := requirePublishedRuntimeReady(t.Context(), PublishedRuntimeReadinessInput{
		Environment: "demo", DeploymentDir: "/deployment", DockerPlan: buildInput.DockerPlan,
		PlanID: "shell", Sources: []RuntimeHostSourceV1{},
	}, func(_ context.Context, _ *deploy.OperationLock, _ providerstore.Store, environment string, dir string) (CurrentBuild, bool, error) {
		loaded++
		if environment != "demo" || dir != "/deployment" {
			t.Fatalf("load identity = %q, %q", environment, dir)
		}
		return current, true, nil
	})
	if err != nil || loaded != 1 || result.Generation != current.Generation {
		t.Fatalf("result=%#v loaded=%d error=%v", result, loaded, err)
	}
}

func TestRequirePublishedRuntimeReadyRejectsMissingAndLoadFailure(t *testing.T) {
	current, buildInput := runtimeCurrentBuildFixture(t)
	input := PublishedRuntimeReadinessInput{DockerPlan: buildInput.DockerPlan, PlanID: "shell", Sources: []RuntimeHostSourceV1{}}
	_, err := requirePublishedRuntimeReady(t.Context(), input, func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
		return CurrentBuild{}, false, nil
	})
	if err == nil || !strings.Contains(err.Error(), "missing") || !strings.Contains(err.Error(), "reploy build") {
		t.Fatalf("missing build error = %v", err)
	}
	want := errors.New("state corrupt")
	_, err = requirePublishedRuntimeReady(t.Context(), input, func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
		return current, false, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("load failure = %v", err)
	}
}

func TestRequirePublishedRuntimeReadyPointsInstalledBuildRecoveryToInstall(t *testing.T) {
	input := PublishedRuntimeReadinessInput{
		DockerPlan: DockerExecutionPlan{Phase: blueprint.PhaseInstalled},
		PlanID:     "shell",
		Sources:    []RuntimeHostSourceV1{},
	}
	_, err := requirePublishedRuntimeReady(t.Context(), input, func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
		return CurrentBuild{}, false, nil
	})
	if err == nil || !strings.Contains(err.Error(), "original `reploy install` command") || strings.Contains(err.Error(), "reploy build") {
		t.Fatalf("installed missing-build recovery = %v", err)
	}
}

func TestRequireRuntimeReadyReportsStaleBuildBeforeHostChecks(t *testing.T) {
	current, buildInput := runtimeCurrentBuildFixture(t)
	changed := buildInput.Document
	changed.Environment.Terminal.ColorEnv = "FORCE_COLOR"
	var err error
	current.State.Blueprint, err = blueprint.EncodeResolvedDocumentV1(changed)
	if err != nil {
		t.Fatal(err)
	}
	err = RequireRuntimeReady(RuntimeReadinessInput{
		Current: current, DockerPlan: buildInput.DockerPlan, PlanID: "missing", Sources: []RuntimeHostSourceV1{},
	})
	if err == nil || !strings.Contains(err.Error(), "run `reploy build`") || strings.Contains(err.Error(), "host-source") {
		t.Fatalf("stale runtime error = %v", err)
	}
}

func TestRequireRuntimeReadyChecksHostSourceAfterExactBuildMatch(t *testing.T) {
	current, buildInput := runtimeCurrentBuildFixture(t)
	root := t.TempDir()
	config := filepath.Join(root, "config")
	if err := os.Mkdir(config, 0o700); err != nil {
		t.Fatal(err)
	}
	buildInput.DockerPlan.Mounts = []MountExecutionPlan{{
		Name: "config", Mode: blueprint.MountManagedBind, Source: config,
		SourceKind: deploy.RuntimeMountSourceDirectory, Target: "/mnt/config", ReadOnly: true,
	}}
	plans, err := RuntimePlansV1(buildInput.Document, buildInput.DockerPlan)
	if err != nil {
		t.Fatal(err)
	}
	current.Lock.RuntimePolicy, err = CompileRuntimePolicyFromLockV1(buildInput.Document, current.Lock, plans)
	if err != nil {
		t.Fatal(err)
	}
	refreshCurrentBuildReuseGeneration(t, &current)
	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}
	err = RequireRuntimeReady(RuntimeReadinessInput{
		Current: current, DockerPlan: buildInput.DockerPlan, PlanID: "shell",
		Sources: []RuntimeHostSourceV1{{
			Destination: "/mnt/config", HostPath: config,
			SourceKind: deploy.RuntimeMountSourceDirectory, ReadOnly: true,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "host-source") || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("missing host source error = %v", err)
	}
}

func TestCurrentBuildMatchesRuntimeV1TreatsChangedStateAsStale(t *testing.T) {
	current, buildInput := runtimeCurrentBuildFixture(t)
	matched, err := CurrentBuildMatchesRuntimeV1(current, buildInput.DockerPlan)
	if err != nil || !matched {
		t.Fatalf("matching runtime build = %v, %v", matched, err)
	}

	changed := buildInput.Document
	changed.Environment.Base.Image = "debian:13"
	if err := changed.Environment.RebuildProviderContributions(); err != nil {
		t.Fatal(err)
	}
	current.State.Blueprint, err = blueprint.EncodeResolvedDocumentV1(changed)
	if err != nil {
		t.Fatal(err)
	}
	matched, err = CurrentBuildMatchesRuntimeV1(current, buildInput.DockerPlan)
	if err != nil || matched {
		t.Fatalf("changed runtime blueprint = %v, %v", matched, err)
	}
}

func TestCurrentBuildMatchesRuntimeV1RejectsMalformedRuntimePlan(t *testing.T) {
	current, _ := runtimeCurrentBuildFixture(t)
	matched, err := CurrentBuildMatchesRuntimeV1(current, DockerExecutionPlan{Workload: &WorkloadExecutionPlan{}})
	if err == nil || matched || !strings.Contains(err.Error(), "runtime plan") {
		t.Fatalf("malformed runtime plan = %v, %v", matched, err)
	}
}

func runtimeCurrentBuildFixture(t *testing.T) (CurrentBuild, CurrentBuildReuseInput) {
	t.Helper()
	current, input := currentBuildReuseFixture(t)
	document := input.Document
	document.Environment.Base = blueprint.BaseComponent{
		Image: current.Lock.Base.AuthorReference, Exports: map[string]blueprint.BaseExecutableExport{},
	}
	if err := document.Environment.RebuildProviderContributions(); err != nil {
		t.Fatal(err)
	}
	resolved, err := blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	current.State.Blueprint = resolved
	current.Lock.BlueprintDigest, err = blueprint.ResolvedDocumentDigestV1(resolved)
	if err != nil {
		t.Fatal(err)
	}
	request, err := BuildResolvedRequestV1(document, current.State.Overlay, current.State.Platform, []providers.ResolvedSourceInput{})
	if err != nil {
		t.Fatal(err)
	}
	current.Lock.ResolvedRequestDigest, err = providers.ResolvedRequestDigest(request, registry.ValidateResolvedRequestOwnersV1)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := RuntimePlansV1(document, input.DockerPlan)
	if err != nil {
		t.Fatal(err)
	}
	current.Lock.RuntimePolicy, err = CompileRuntimePolicyFromLockV1(document, current.Lock, plans)
	if err != nil {
		t.Fatal(err)
	}
	refreshCurrentBuildReuseGeneration(t, &current)
	input.Document = document
	input.ResolvedRequest = request
	input.Base = current.Lock.Base
	return current, input
}
