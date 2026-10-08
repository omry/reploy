package dockerdeploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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

func portableToolNativeConsumerPathV1(tool, exportPath string) string {
	if tool == "bash" && exportPath == "/usr/bin/bash" {
		return "/bin/bash"
	}
	return exportPath
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
	nativeCases := []toolcatalog.IntegrationCaseV1{}
	for _, caseV1 := range cases {
		if caseV1.Fixture.Target.Platform == "linux/"+runtime.GOARCH {
			nativeCases = append(nativeCases, caseV1)
		}
	}
	selected, err := representativePortableToolCasesV1(nativeCases)
	if err != nil {
		t.Fatal(err)
	}
	runPortableToolIntegrationCasesV1(t, selected)
}

// This PTD-30 smoke covers the new native APT boundary without establishing
// exhaustive tuple support. PTD-31/PTD-32 own that proof.
func TestPortableToolNativeBoundaryDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to exercise native boundaries")
	}
	cases, err := toolcatalog.EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	selected := []toolcatalog.IntegrationCaseV1{}
	for _, caseV1 := range cases {
		if caseV1.Manifest.Tool == "bash" && caseV1.Manifest.Version == "5.2.15" &&
			caseV1.Fixture.Target.Platform == "linux/"+runtime.GOARCH {
			selected = append(selected, caseV1)
		}
	}
	if len(selected) != 2 {
		t.Fatal("native boundary smoke requires both exact native contexts")
	}
	runPortableToolIntegrationCasesV1(t, selected)
}

func TestPortableToolNativeConsumerPathMappingV1(t *testing.T) {
	cases, err := toolcatalog.EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	wantExports := map[string]string{
		"debian/12":    "/bin/bash",
		"debian/13":    "/usr/bin/bash",
		"ubuntu/26.04": "/usr/bin/bash",
	}
	seen := map[string]bool{}
	for _, caseV1 := range cases {
		if caseV1.Manifest.Tool != "bash" || caseV1.Support.Context != "runtime" {
			continue
		}
		key := caseV1.Target.Target.OSReleaseID + "/" + caseV1.Target.Target.VersionID
		wantExport, ok := wantExports[key]
		if !ok {
			t.Fatalf("unexpected derived Bash target %s", key)
		}
		if len(caseV1.Target.Exports) != 1 || caseV1.Target.Exports[0].Name != "bash" || caseV1.Target.Exports[0].Path != wantExport {
			t.Fatalf("derived Bash target export = %#v, want %s", caseV1.Target.Exports, wantExport)
		}
		scope, err := portableToolIntegrationScopeV1(caseV1)
		if err != nil {
			t.Fatal(err)
		}
		closure, err := toolcatalog.EmbeddedSelectedClosureForIntegrationCaseV1(caseV1, scope)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := toolcatalog.CompilePortableToolPlanV1([]toolcatalog.SelectedClosureV1{closure})
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Tools) != 1 || len(plan.Tools[0].Exports) != 1 {
			t.Fatalf("derived Bash plan = %#v", plan.Tools)
		}
		export := plan.Tools[0].Exports[0]
		if export.Name != "bash" || export.Path != wantExport {
			t.Fatalf("compiled Bash export = %#v, want %s", export, wantExport)
		}
		if got := portableToolNativeConsumerPathV1(caseV1.Manifest.Tool, export.Path); got != "/bin/bash" {
			t.Fatalf("consumer path for %s export %s = %s, want /bin/bash", key, export.Path, got)
		}
		seen[key+"/"+caseV1.Target.Target.OCIArchitecture] = true
	}
	if len(seen) != 6 {
		t.Fatalf("derived runtime Bash cases = %d, want six target/architecture cases", len(seen))
	}
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

func TestPortableToolPlaywrightMatrixDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to exercise the complete Playwright matrix")
	}
	cases, err := toolcatalog.EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := portableToolIntegrationCasesForToolV1(cases, "playwright")
	if err != nil {
		t.Fatal(err)
	}
	runPortableToolIntegrationCasesV1(t, selected)
}

func portableToolIntegrationCasesForPlatformV1(cases []toolcatalog.IntegrationCaseV1, tool, platform string) ([]toolcatalog.IntegrationCaseV1, error) {
	all, err := portableToolIntegrationCasesForToolV1(cases, tool)
	if err != nil {
		return nil, err
	}
	selected := []toolcatalog.IntegrationCaseV1{}
	for _, c := range all {
		if c.Fixture.Target.Platform == platform {
			selected = append(selected, c)
		}
	}
	if _, err := portableToolIntegrationRequestsV1(selected); err != nil {
		return nil, err
	}
	return selected, nil
}

func TestPortableToolBashAMD64MatrixDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to exercise the complete Bash amd64 matrix")
	}
	if runtime.GOARCH != "amd64" {
		t.Fatal("Bash amd64 proof requires an actual amd64 runner")
	}
	cases, err := toolcatalog.EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := portableToolIntegrationCasesForPlatformV1(cases, "bash", "linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	runPortableToolIntegrationCasesV1(t, selected)
}

// This fixture expectation identifies the subject's release in original
// output; it adds no command or tool branch to the production executor.
func requirePortableToolIntegrationBashOutputV1(caseV1 toolcatalog.IntegrationCaseV1, observation PortableToolCaseObservationV1) error {
	var bundle portableToolCaseEvidenceBundleV1
	if err := json.Unmarshal(observation.payload, &bundle); err != nil {
		return err
	}
	if len(bundle.Observations) == 0 {
		return fmt.Errorf("Bash case lacks original executor output")
	}
	for _, observed := range bundle.Observations {
		if len(observed.Results) == 0 {
			return fmt.Errorf("Bash profile lacks original results")
		}
		for _, result := range observed.Results {
			output, err := base64.StdEncoding.DecodeString(result.Stdout.Content)
			if err != nil || result.Stdout.Truncated || !strings.HasPrefix(string(output), "GNU bash, version "+caseV1.Manifest.Version+"(") {
				return fmt.Errorf("original Bash output does not identify selected release %s", caseV1.Manifest.Version)
			}
		}
	}
	return nil
}

// Workflow artifact packaging only: every value uses its existing encoding.
// The original case record and lock retain their existing digest domains.
type portableToolIntegrationNativeEvidenceV1 struct {
	Image      deploy.ImageDescriptor         `json:"image"`
	Lock       deploy.BuildLockV1             `json:"lock"`
	LockDigest canonical.Digest               `json:"lock_digest"`
	Exports    []providers.ExecutableEvidence `json:"exports"`
	Consumers  []providers.ExecutableEvidence `json:"consumers"`
}

func requirePortableToolIntegrationNativeEvidenceV1(caseV1 toolcatalog.IntegrationCaseV1, scope string, observation PortableToolCaseObservationV1, handoff *portableToolIntegrationNativeEvidenceV1) error {
	if handoff == nil {
		return fmt.Errorf("runtime native case lacks its observed executable handoff")
	}
	var bundle portableToolCaseEvidenceBundleV1
	if err := json.Unmarshal(observation.payload, &bundle); err != nil {
		return err
	}
	if err := requireCurrentPortableToolCaseBundleV1(bundle, caseV1, scope); err != nil {
		return err
	}
	if !reflect.DeepEqual(bundle.Image, handoff.Image) {
		return fmt.Errorf("native handoff and original output describe different images")
	}
	digest, err := deploy.BuildLockDigestV1(handoff.Lock, registry.ValidateRequirementProfileV1)
	if err != nil || digest != handoff.LockDigest {
		return fmt.Errorf("native handoff differs from its exact lock: %v", err)
	}
	closure, err := toolcatalog.EmbeddedSelectedClosureForIntegrationCaseV1(caseV1, scope)
	if err != nil {
		return err
	}
	if _, err := PortableToolApplicationCaseValidationInputFromBuildLockV1(
		InspectedImageCandidate{Descriptor: handoff.Image, Image: handoff.Lock.FinalImage},
		handoff.Lock, []toolcatalog.SelectedClosureV1{closure}, caseV1, scope); err != nil {
		return err
	}
	if len(handoff.Exports) != len(caseV1.Target.Exports) || len(handoff.Consumers) != len(handoff.Exports) {
		return fmt.Errorf("native handoff lacks the complete selected export and consumer set")
	}
	for i, export := range handoff.Exports {
		consumer := handoff.Consumers[i]
		if err := providers.ValidateFinalExecutableEvidence(export); err != nil {
			return err
		}
		if err := providers.ValidateFinalExecutableEvidence(consumer); err != nil {
			return err
		}
		if export.Output != consumer.Output || export.InvocationPath != caseV1.Target.Exports[i].Path ||
			consumer.InvocationPath != portableToolNativeConsumerPathV1(caseV1.Manifest.Tool, export.InvocationPath) ||
			export.Terminal.Owner == nil || export.Terminal.Kind != "regular" || consumer.Terminal.Kind != "regular" ||
			export.Terminal.SHA256 != consumer.Terminal.SHA256 ||
			export.Terminal.Size != consumer.Terminal.Size {
			return fmt.Errorf("native export and consumer do not identify the same selected regular executable")
		}
		matched := false
		for _, output := range handoff.Lock.Catalog {
			if output.Evidence.Output == export.Output && output.SupplierNode == "apt" &&
				output.Name == caseV1.Target.Exports[i].Name && output.Evidence.InvocationPath == export.InvocationPath &&
				reflect.DeepEqual(output.Evidence.Terminal, export.Terminal) && reflect.DeepEqual(output.Evidence.Facts, export.Facts) {
				if matched {
					return fmt.Errorf("native handoff has ambiguous locked package ownership")
				}
				matched = true
			}
		}
		if !matched {
			return fmt.Errorf("native handoff executable differs from its locked package-owned output")
		}
	}
	return nil
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
	nativeHandoffs := map[canonical.Digest]portableToolIntegrationNativeEvidenceV1{}
	for _, request := range requests {
		caseV1, scope := request.Case, request.Scope
		var observation PortableToolCaseObservationV1
		var handoff *portableToolIntegrationNativeEvidenceV1
		passed := t.Run(caseV1.Manifest.Tool+"/"+caseV1.Fixture.ID, func(t *testing.T) {
			observation, handoff = executePortableToolIntegrationCaseV1(t, caseV1, scope)
			if caseV1.Manifest.Tool == "bash" {
				if err := requirePortableToolIntegrationBashOutputV1(caseV1, observation); err != nil {
					t.Fatal(err)
				}
				if caseV1.Support.Context == "runtime" && handoff == nil {
					t.Fatal("runtime Bash case lost its observed native provenance")
				}
			}
			if handoff != nil {
				if err := requirePortableToolIntegrationNativeEvidenceV1(caseV1, scope, observation, handoff); err != nil {
					t.Fatal(err)
				}
			}
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
		if caseV1.Manifest.Tool == "bash" {
			var changed portableToolCaseEvidenceBundleV1
			if err := json.Unmarshal(observation.payload, &changed); err != nil {
				t.Fatal(err)
			}
			output := newBoundedPortableToolProbeOutput(portableToolProbeOutputLimit, nil)
			_, _ = output.Write([]byte("GNU bash, version 0.0.0(1)-release\n"))
			changed.Observations[0].Results[0].Stdout = output.Evidence()
			encoded, err := canonical.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if err := requirePortableToolIntegrationBashOutputV1(caseV1, PortableToolCaseObservationV1{payload: encoded}); err == nil {
				t.Fatal("wrong original Bash version passed")
			}
		}
		if handoff != nil {
			nativeHandoffs[caseV1.ID] = *handoff
			// Tamper the actual successful evidence, never invent a passing record.
			for _, fault := range []string{"missing", "image", "lock", "package", "executable", "consumer"} {
				encoded, err := json.Marshal(handoff)
				if err != nil {
					t.Fatal(err)
				}
				var changed portableToolIntegrationNativeEvidenceV1
				if err := json.Unmarshal(encoded, &changed); err != nil {
					t.Fatal(err)
				}
				candidate := &changed
				switch fault {
				case "missing":
					candidate = nil
				case "image":
					changed.Image.ConfigDigest = rendererDigest("f")
				case "lock":
					changed.LockDigest = rendererDigest("f")
				case "package":
					changed.Exports[0].Terminal.Owner = nil
				case "executable":
					changed.Exports[0].Terminal.SHA256 = rendererDigest("f")
				case "consumer":
					changed.Consumers[0].InvocationPath = "/wrong/bash"
				}
				if err := requirePortableToolIntegrationNativeEvidenceV1(caseV1, scope, observation, candidate); err == nil {
					t.Fatalf("%s native handoff substitution passed", fault)
				}
			}
		}
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
	if len(nativeHandoffs) != 0 {
		encoded, err := canonical.Marshal(nativeHandoffs)
		if err != nil {
			t.Fatal(err)
		}
		// Only complete, cleaned-up cases reach this external workflow artifact.
		path := filepath.Join(root, strings.ReplaceAll(t.Name(), "/", "-")+"-native.json")
		if err := os.WriteFile(path, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := os.ReadFile(path)
		if err != nil || string(loaded) != string(encoded) {
			t.Fatalf("retained native artifact differs from observed bytes: %v", err)
		}
		var retained map[canonical.Digest]portableToolIntegrationNativeEvidenceV1
		if err := json.Unmarshal(loaded, &retained); err != nil {
			t.Fatal(err)
		}
		for _, request := range requests {
			if handoff, ok := retained[request.Case.ID]; ok {
				payload, err := evidenceStore.LoadValidationRecord(refs[request.Case.ID])
				if err != nil {
					t.Fatal(err)
				}
				if err := requirePortableToolIntegrationNativeEvidenceV1(request.Case, request.Scope, PortableToolCaseObservationV1{payload: payload}, &handoff); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func executePortableToolIntegrationCaseV1(t *testing.T, caseV1 toolcatalog.IntegrationCaseV1, scope string) (PortableToolCaseObservationV1, *portableToolIntegrationNativeEvidenceV1) {
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
		return observation, nil
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
	var handoff *portableToolIntegrationNativeEvidenceV1
	observation, err := observeApplicationBuildCaseV1(ctx, LockedProviderBuildExecutionInputV1{
		Preparation: preparation, SourceWheels: []providerstore.ArtifactDescriptor{}, LocalOverrides: []PythonLocalOverrideV1{},
		RunOptions: options, Progress: os.Stdout,
	}, caseV1, scope, func(ctx context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
		observe := input.observeFinalImage
		input.observeFinalImage = func(ctx context.Context, image InspectedImageCandidate, lock deploy.BuildLockV1) error {
			if err := observe(ctx, image, lock); err != nil {
				return err
			}
			paths := map[string]string{}
			for _, entry := range lock.PortableTools.Plan.PortableToolPlan.Tools {
				if entry.Scope == scope && !portableToolEntryRequiresRuntimeLayerV1(entry) {
					for _, export := range entry.Exports {
						paths[export.Name] = portableToolNativeConsumerPathV1(caseV1.Manifest.Tool, export.Path)
					}
				}
			}
			if len(paths) != 0 {
				exports, consumers, err := CollectApplicationPortableNativeExportEvidenceV1(ctx, store, image, lock, scope, paths)
				if err != nil {
					return err
				}
				if len(exports) != len(paths) || len(consumers) != len(paths) {
					return fmt.Errorf("finalized native handoff lacks complete executable evidence")
				}
				for _, export := range exports {
					if export.Terminal.Owner == nil {
						return fmt.Errorf("finalized native export lacks package ownership")
					}
				}
				digest, err := deploy.BuildLockDigestV1(lock, registry.ValidateRequirementProfileV1)
				if err != nil {
					return err
				}
				handoff = &portableToolIntegrationNativeEvidenceV1{Image: image.Descriptor, Lock: lock, LockDigest: digest, Exports: exports, Consumers: consumers}
			}
			return nil
		}
		return ExecuteLockedProviderBuildV1(ctx, input)
	})
	if err != nil {
		t.Fatal(err)
	}
	return observation, handoff
}
