package dockerdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
	"github.com/omry/reploy/internal/toolrequest"
	"gopkg.in/yaml.v3"
)

// Representatives are selected from the authenticated inventory, once per
// execution context. This deliberately does not establish matrix coverage.
func representativePortableToolCasesV1(cases []toolcatalog.IntegrationCaseV1) ([]toolcatalog.IntegrationCaseV1, error) {
	ordered := append([]toolcatalog.IntegrationCaseV1(nil), cases...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	selected := []toolcatalog.IntegrationCaseV1{}
	seen := map[string]bool{}
	for _, caseV1 := range ordered {
		scope, err := portableToolIntegrationScopeV1(caseV1)
		if err != nil {
			return nil, err
		}
		if _, err := toolcatalog.EmbeddedSelectedClosureForIntegrationCaseV1(caseV1, scope); err != nil {
			return nil, err
		}
		if !seen[caseV1.Support.Context] {
			seen[caseV1.Support.Context] = true
			selected = append(selected, caseV1)
		}
	}
	if !seen["build"] || !seen["runtime"] {
		return nil, fmt.Errorf("representative inventory requires build and runtime cases")
	}
	return selected, nil
}

func portableToolIntegrationScopeV1(caseV1 toolcatalog.IntegrationCaseV1) (string, error) {
	switch caseV1.Support.Context {
	case "build":
		return "source-builder:case-fixture", nil
	case "runtime":
		return "application:case", nil
	default:
		return "", fmt.Errorf("unsupported integration context %q", caseV1.Support.Context)
	}
}

func portableToolIntegrationRequestsV1(cases []toolcatalog.IntegrationCaseV1) ([]PortableToolCaseEvidenceRequestV1, error) {
	if len(cases) == 0 {
		return nil, fmt.Errorf("integration requires a nonempty exercised set")
	}
	requests := []PortableToolCaseEvidenceRequestV1{}
	seen := map[canonical.Digest]bool{}
	for _, caseV1 := range cases {
		scope, err := portableToolIntegrationScopeV1(caseV1)
		if err != nil {
			return nil, err
		}
		if _, err := toolcatalog.EmbeddedSelectedClosureForIntegrationCaseV1(caseV1, scope); err != nil {
			return nil, err
		}
		if seen[caseV1.ID] {
			return nil, fmt.Errorf("duplicate exercised case %s", caseV1.ID)
		}
		seen[caseV1.ID] = true
		requests = append(requests, PortableToolCaseEvidenceRequestV1{Case: caseV1, Scope: scope})
	}
	return requests, nil
}

func portableToolIntegrationRequirementV1(caseV1 toolcatalog.IntegrationCaseV1) toolrequest.SyntaxV1 {
	selections := map[string]toolrequest.SetSyntaxV1{}
	for key, values := range caseV1.Support.Selections {
		selections[key] = toolrequest.SetSyntaxV1{Values: append([]string(nil), values...)}
	}
	return toolrequest.SyntaxV1{
		Structured: true, Tool: caseV1.Manifest.Tool,
		HasVersion: true, Version: "==" + caseV1.Manifest.Version,
		HasDefinitionRevision: true, DefinitionRevision: caseV1.Manifest.Revision,
		HasBinding: len(caseV1.Support.Bindings) != 0,
		Binding:    toolrequest.SetSyntaxV1{Values: append([]string(nil), caseV1.Support.Bindings...)}, Selections: selections,
	}
}

func portableToolIntegrationBaseV1(caseV1 toolcatalog.IntegrationCaseV1) string {
	// Preserve the fixture repository; replace its mutable tag with its digest.
	base := strings.Split(caseV1.Fixture.BaseImage, "@")[0]
	if colon := strings.LastIndex(base, ":"); colon > strings.LastIndex(base, "/") {
		base = base[:colon]
	}
	return base + "@" + string(caseV1.Fixture.BaseImageDigest)
}

// The incidental Python package activates an ordinary Python provider node.
// Tool requests, bindings, selections and all probe commands remain catalog owned.
func portableToolIntegrationDocumentV1(caseV1 toolcatalog.IntegrationCaseV1) (blueprint.Document, error) {
	packages := blueprint.ApplicationPackagesSyntax{
		OS: []blueprint.APTPackageRequestSyntax{{Package: "python3", Exports: map[string]blueprint.ExecutableExportSyntax{
			"python": {Executable: "/usr/bin/python3"},
		}}, {Package: "python3-pip"}, {Package: "python3-venv"}},
		Python: &blueprint.PythonPackagesSyntax{
			Interpreter:  &blueprint.CommandRequirementSyntax{Command: "python", Version: ">=3.11", Supplier: "os"},
			Requirements: []string{"packaging==25.0"},
		},
		Tools: []toolrequest.SyntaxV1{},
	}
	if caseV1.Support.Context == "runtime" {
		packages.Tools = []toolrequest.SyntaxV1{portableToolIntegrationRequirementV1(caseV1)}
		packages.ToolsFieldPresent = true
	}
	return blueprint.Resolve(blueprint.Syntax{
		Blueprint: blueprint.MetadataSyntax{Schema: 1, Version: "1.0", Compatibility: blueprint.CompatibilitySyntax{Platforms: []string{caseV1.Fixture.Target.Platform}}},
		Environment: blueprint.EnvironmentSyntax{
			ID: "portable-case", Base: blueprint.BaseSyntax{Image: portableToolIntegrationBaseV1(caseV1)},
			Applications: map[string]blueprint.ApplicationSyntax{"case": {Packages: packages}},
		},
	})
}

func TestPortableToolRepresentativeDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to exercise real derived cases")
	}
	cases, err := toolcatalog.EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := representativePortableToolCasesV1(cases)
	if err != nil {
		t.Fatal(err)
	}
	runPortableToolIntegrationCasesV1(t, selected)
}

// Exhaustive suites select every advertised case for the requested tool. The
// authenticated catalog owns the tuples, profiles and commands.
func portableToolIntegrationCasesForToolV1(cases []toolcatalog.IntegrationCaseV1, tool string) ([]toolcatalog.IntegrationCaseV1, error) {
	selected := []toolcatalog.IntegrationCaseV1{}
	for _, caseV1 := range cases {
		if caseV1.Manifest.Tool == tool {
			selected = append(selected, caseV1)
		}
	}
	if _, err := portableToolIntegrationRequestsV1(selected); err != nil {
		return nil, err
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].ID < selected[j].ID })
	return selected, nil
}

func TestPortableToolJavaMatrixDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to exercise the complete Java matrix")
	}
	cases, err := toolcatalog.EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := portableToolIntegrationCasesForToolV1(cases, "java")
	if err != nil {
		t.Fatal(err)
	}
	runPortableToolIntegrationCasesV1(t, selected)
}

// The same runner and exact exercised-set gate are used for representative and
// exhaustive suites. No executor, artifact identity or target probe is replaced.
func runPortableToolIntegrationCasesV1(t *testing.T, cases []toolcatalog.IntegrationCaseV1) {
	t.Helper()
	// Reject the whole selected set before creating storage or acquiring anything.
	requests, err := portableToolIntegrationRequestsV1(cases)
	if err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("REPLOY_PORTABLE_TOOL_EVIDENCE_DIR")
	if root == "" {
		root = t.TempDir()
	}
	root, err = filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	evidenceStore, err := providerstore.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	refs := map[canonical.Digest]providerstore.StoreObjectRef{}
	for _, request := range requests {
		caseV1, scope := request.Case, request.Scope
		var observation PortableToolCaseObservationV1
		passed := t.Run(caseV1.Manifest.Tool+"/"+caseV1.Fixture.ID, func(t *testing.T) {
			observation = executePortableToolIntegrationCaseV1(t, caseV1, scope)
		})
		// Subtest cleanup retires all image owners before external publication.
		// A failed callback, profile or cleanup leaves this requested case absent.
		if !passed {
			continue
		}
		ref, err := PersistPortableToolCaseEvidenceV1(t.Context(), evidenceStore, observation)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := MatchPortableToolCaseEvidenceV1(evidenceStore, caseV1, scope, ref); err != nil {
			t.Fatal(err)
		}
		refs[caseV1.ID] = ref
		t.Logf("current external evidence: case=%s scope=%s ref=%s", caseV1.ID, scope, ref.Digest)
	}
	if err := RequireCurrentPortableToolCaseEvidenceV1(evidenceStore, requests, refs); err != nil {
		t.Fatalf("complete exercised-set evidence: %v", err)
	}
	// These checks use the real successful records, not fabricated pass records.
	incomplete := map[canonical.Digest]providerstore.StoreObjectRef{}
	for id, ref := range refs {
		incomplete[id] = ref
	}
	delete(incomplete, requests[0].Case.ID)
	if err := RequireCurrentPortableToolCaseEvidenceV1(evidenceStore, requests, incomplete); err == nil {
		t.Fatal("missing exercised case passed the evidence gate")
	}
	changed := append([]PortableToolCaseEvidenceRequestV1(nil), requests...)
	changed[0].Case.Fixture.BaseImageDigest = rendererDigest("f")
	if err := RequireCurrentPortableToolCaseEvidenceV1(evidenceStore, changed, refs); err == nil {
		t.Fatal("stale fixture passed the evidence gate")
	}
	substituted := map[canonical.Digest]providerstore.StoreObjectRef{}
	for id := range refs {
		substituted[id] = refs[requests[0].Case.ID]
	}
	if len(requests) > 1 {
		if err := RequireCurrentPortableToolCaseEvidenceV1(evidenceStore, requests, substituted); err == nil {
			t.Fatal("another case's record passed the evidence gate")
		}
	}
	// Keep a workflow index next to the immutable records, outside definitions.
	index, err := json.MarshalIndent(refs, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, strings.ReplaceAll(t.Name(), "/", "-")+".json"), append(index, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func executePortableToolIntegrationCaseV1(t *testing.T, caseV1 toolcatalog.IntegrationCaseV1, scope string) PortableToolCaseObservationV1 {
	t.Helper()
	ctx := t.Context()
	platform, err := blueprint.ParsePlatform(caseV1.Fixture.Target.Platform)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runDockerOutput(ctx, "pull", "--platform", platform.Canonical, portableToolIntegrationBaseV1(caseV1)); err != nil {
		t.Fatal(err)
	}
	packed := packIntegrationProbe(t, platform)
	previous := locateProbeArchiveExecutable
	previousRuntime := locateApplicationRuntimeExecutable
	locateProbeArchiveExecutable = func() (string, error) { return packed, nil }
	locateApplicationRuntimeExecutable = func() (string, error) { return packed, nil }
	t.Cleanup(func() {
		locateProbeArchiveExecutable = previous
		locateApplicationRuntimeExecutable = previousRuntime
	})
	document, err := portableToolIntegrationDocumentV1(caseV1)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	options := RunOptions{Stdout: os.Stdout, Stderr: os.Stderr}
	if caseV1.Support.Context == "build" {
		base, _, err := ResolveBase(ctx, document.Environment.Base.Image, platform)
		if err != nil {
			t.Fatal(err)
		}
		request, err := BuildResolvedRequestV1(document, deploy.EmptyRequestOverlayV1(), platform, []providers.ResolvedSourceInput{})
		if err != nil {
			t.Fatal(err)
		}
		providerPlan, err := registry.Plan(providers.PlanInput{Components: request.Components, Platform: request.Platform})
		if err != nil {
			t.Fatal(err)
		}
		requirement := map[string]any{"tool": caseV1.Manifest.Tool, "version": "==" + caseV1.Manifest.Version, "definition_revision": caseV1.Manifest.Revision}
		if len(caseV1.Support.Bindings) != 0 {
			requirement["binding"] = caseV1.Support.Bindings
		}
		if len(caseV1.Support.Selections) != 0 {
			requirement["select"] = caseV1.Support.Selections
		}
		recipeBytes, err := yaml.Marshal(map[string]any{"schema": 1, "project": "case-fixture", "type": "python", "build": "setuptools-legacy", "requires": []any{requirement}})
		if err != nil {
			t.Fatal(err)
		}
		source := t.TempDir()
		for name, content := range map[string][]byte{"setup.py": []byte("from setuptools import setup\n"), "pyproject.toml": []byte("[tool.ruff]\n"), LocalSourceRecipeFilename: recipeBytes} {
			if err := os.WriteFile(filepath.Join(source, name), content, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		recipe, err := ReadPythonLocalSourceRecipeV1(source, "case-fixture")
		if err != nil {
			t.Fatal(err)
		}
		payload, err := blueprint.EncodeResolvedDocumentV1(document)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := blueprint.ResolvedDocumentDigestV1(payload)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := PlanSourceBuilderPortableToolsV1(ctx, PlanSourceBuilderPortableToolsInputV1{
			Store: store, Base: base, ProviderPlan: providerPlan, BlueprintDigest: digest, ReployVersion: deploy.ToolVersion,
			SelectedRecipes: []SourceBuilderSelectedRecipeV1{{Distribution: "case-fixture", Recipe: recipe}},
		})
		if err != nil {
			t.Fatal(err)
		}
		tools, err := MaterializeSourceBuilderPortableToolsV1(ctx, store, plan)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := tools.Cleanup(); err != nil {
				t.Error(err)
			}
		}()
		observation, err := ObserveSourceBuilderBuildCaseV1(ctx, store, tools, base, options, caseV1, scope)
		if err != nil {
			t.Fatal(err)
		}
		return observation
	}
	if _, err := StageDesiredStateV1(ctx, DesiredStateStageInputV1{DeploymentDir: dir, Document: document, ExplicitPlatform: platform.Canonical, BlueprintSource: "catalog-case", Create: true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := RemoveStagedDeploymentV1(context.WithoutCancel(ctx), StagedDeploymentRemoveInputV1{DeploymentDir: dir}); err != nil {
			t.Error(err)
		}
	})
	operation, err := deploy.AcquireOperationLock(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := operation.Unlock(); err != nil {
			t.Error(err)
		}
	}()
	runtime, err := CurrentStagedProviderBuildRuntimeV1()
	if err != nil {
		t.Fatal(err)
	}
	dockerPlan, err := PlanDockerExecution(document, DockerPlanContext{
		DeploymentDir: dir, Phase: blueprint.PhaseStaged, GeneratedImage: providerBuildPlanImage,
		Host: runtime.Host, UID: runtime.UID, GID: runtime.GID, SupplementaryGIDs: runtime.SupplementaryGIDs,
	})
	if err != nil {
		t.Fatal(err)
	}
	preparation, err := PrepareLockedProviderBuildV1(ctx, LockedProviderBuildPreparationInputV1{
		Operation: operation, Store: store, Environment: document.Environment.ID, DeploymentDir: dir,
		PackageOverrides: deploy.EmptyPackageOverrideIntentV1(document.Environment.ID), BaseImage: document.Environment.Base.Image,
		Sources: []providers.ResolvedSourceInput{}, LocalOverrides: []PythonLocalOverrideV1{}, ReployVersion: deploy.ToolVersion,
		DockerPlan: dockerPlan, NoCache: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cleanupFailedProviderBuildV1(context.WithoutCancel(ctx), preparation); err != nil {
			t.Error(err)
		}
	}()
	observation, err := ObserveApplicationBuildCaseV1(ctx, LockedProviderBuildExecutionInputV1{
		Preparation: preparation, SourceWheels: []providerstore.ArtifactDescriptor{}, LocalOverrides: []PythonLocalOverrideV1{},
		RunOptions: options, Progress: os.Stdout,
	}, caseV1, scope)
	if err != nil {
		t.Fatal(err)
	}
	return observation
}
