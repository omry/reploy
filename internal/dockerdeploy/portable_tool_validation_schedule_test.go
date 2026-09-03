package dockerdeploy

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/omry/reploy/internal/blueprint"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

const portableToolScheduleTestDigestV1 = canonical.Digest(
	"sha256:1111111111111111111111111111111111111111111111111111111111111111",
)

// portableToolLockForValidationScheduleV1 builds the smallest lock that
// selects one validation profile with a contract runtime projection.
func portableToolLockForValidationScheduleV1(
	t *testing.T,
	providerPlan providers.ProviderPlanV1,
	owner providers.NodeID,
) providers.PortableToolLockV1 {
	t.Helper()
	profile := portableToolValidationProfile(
		toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--version"}},
	)
	digest, err := toolcatalog.ValidationProfileDigestV1(profile)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := canonical.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	value := canonical.Object{}
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	plan := providers.PortableToolPlanV1{
		Schema: providers.PortableToolPlanSchemaV1,
		Tools: []providers.PortableToolPlanEntryV1{{
			Scope:                 "application",
			SelectedClosureDigest: portableToolScheduleTestDigestV1,
			Provenance: providers.PortableToolReleaseProvenanceV1{
				Tool: "demo", Version: "1.2.3", Revision: "1", ManifestDigest: portableToolScheduleTestDigestV1,
			},
			Runtime: portableToolContractRuntimeV1(),
			Responsibilities: providers.PortableToolResponsibilitiesV1{
				BindingContracts:  []providers.PortableToolSelectedRecordV1{},
				BindingArtifacts:  []providers.PortableToolSelectedRecordV1{},
				Payloads:          []providers.PortableToolSelectedRecordV1{},
				NativePackageSets: []providers.PortableToolSelectedRecordV1{},
			},
			Exports: []providers.PortableToolExportV1{},
			ValidationProfiles: []providers.PortableToolValidationProfileV1{{
				Reference: providers.PortableToolRecordReferenceV1{ID: profile.ID, Digest: digest},
				Record:    providers.CanonicalProviderData{Schema: profile.Schema, Value: value},
			}},
		}},
	}
	manifestValue := canonical.Object{
		"schema": providers.PortableToolReleaseManifestRecordSchemaV1,
		"id":     "tool:demo/releases/1.2.3/revisions/1/manifest",
		"tool":   "demo", "version": "1.2.3", "revision": "1",
		"aliases": []any{}, "provenance": []any{}, "artifact_sources": []any{},
		"targets": []any{canonical.Object{
			"id": "tool:demo/releases/1.2.3/targets/debian/12/amd64", "digest": string(portableToolScheduleTestDigestV1),
		}},
		"validation_profiles": []any{canonical.Object{"id": profile.ID, "digest": string(digest)}},
		"contract":            canonical.Object{"id": "tool:demo/releases/1.2.3/contract", "digest": string(portableToolScheduleTestDigestV1)},
	}
	manifestDigest, err := canonical.Sum("portable-tool-record", "portable-tool-record-v1", manifestValue)
	if err != nil {
		t.Fatal(err)
	}
	manifest := providers.PortableToolSelectedRecordV1{
		Reference: providers.PortableToolRecordReferenceV1{ID: manifestValue["id"].(string), Digest: manifestDigest},
		Record:    providers.CanonicalProviderData{Schema: providers.PortableToolReleaseManifestRecordSchemaV1, Value: manifestValue},
	}
	plan.Tools[0].Provenance.ManifestDigest = manifest.Reference.Digest
	domain := providers.PortableToolDomainAuthorityV1{ID: "application", Owner: owner}
	dag, err := providers.BuildPortableToolProviderDAGV1(providerPlan, plan, []providers.PortableToolProviderDomainSetV1{{
		Scope: "application", PackageManager: domain, Binding: domain, Filesystem: domain,
		Environment: domain, Exports: domain, Capabilities: domain,
	}})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := providers.BuildPortableToolLockV1(dag, []providers.PortableToolReleaseManifestInputV1{{
		Scope: "application", Tool: "demo", Manifest: manifest,
	}}, []providers.PortableToolArtifactAcquisitionInputV1{})
	if err != nil {
		t.Fatal(err)
	}
	return lock
}

// portableToolStubEvidenceV1 mirrors what the fixed executor returns: evidence
// attributed to the exact profile it was asked to run.
func portableToolStubEvidenceV1(
	t *testing.T,
	invoked toolcatalog.ValidationProfileRecordV1,
	outcome string,
	results int,
) PortableToolProbeEvidenceV1 {
	t.Helper()
	digest, err := toolcatalog.ValidationProfileDigestV1(invoked)
	if err != nil {
		t.Fatal(err)
	}
	observed := make([]PortableToolProbeResultV1, 0, results)
	for index := 0; index < results && index < len(invoked.Probes); index++ {
		exit := "0"
		result := PortableToolProbeResultV1{Probe: invoked.Probes[index], Outcome: outcome}
		if outcome == PortableToolProbeOutcomePassV1 {
			result.ExitCode = &exit
		}
		observed = append(observed, result)
	}
	return PortableToolProbeEvidenceV1{
		Profile:           providers.PortableToolRecordReferenceV1{ID: invoked.ID, Digest: digest},
		ProfileDefinition: invoked,
		Results:           observed,
	}
}

func portableToolContractRuntimeV1() *providers.PortableToolRuntimeProjectionV1 {
	return &providers.PortableToolRuntimeProjectionV1{
		InstallRoot: "/opt/demo",
		Environment: []providers.PortableToolEnvironmentVariableV1{
			{Name: "JAVA_HOME", Value: "/opt/demo/jdk"},
			{Name: "PLAYWRIGHT_BROWSERS_PATH", Value: "/opt/demo/browsers"},
		},
	}
}

// The contract runtime projection must reach the probe, so the executor emits
// exactly one --environment-entry per selected contract variable.
func TestRunPortableToolValidationProfileCarriesContractEnvironmentToTheProbe(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(
		toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--version"}},
	)
	commands := stubPortableToolProbeCommands(t, []portableToolProbeStubResponse{{stdout: []byte("demo 1.2.3\n")}})

	evidence, err := RunPortableToolValidationProfile(
		context.Background(), descriptor, workspace, profile, portableToolContractRuntimeV1(),
	)
	if err != nil {
		t.Fatal(err)
	}
	wantContract := []toolcatalog.RecordEnvironmentVariableV1{
		{Name: "JAVA_HOME", Value: "/opt/demo/jdk"},
		{Name: "PLAYWRIGHT_BROWSERS_PATH", Value: "/opt/demo/browsers"},
	}
	if !reflect.DeepEqual(evidence.Policy.ContractEnvironment, wantContract) {
		t.Fatalf("contract environment = %#v, want %#v", evidence.Policy.ContractEnvironment, wantContract)
	}
	if evidence.Policy.InstallRoot != "/opt/demo" {
		t.Fatalf("install root = %q", evidence.Policy.InstallRoot)
	}
	// The fixed profile is unchanged; the contract is additional to it.
	wantFixed := []toolcatalog.RecordEnvironmentVariableV1{
		{Name: "HOME", Value: "/tmp"}, {Name: "LANG", Value: "C"}, {Name: "LC_ALL", Value: "C"},
		{Name: "PATH", Value: "/usr/bin:/bin"}, {Name: "TMPDIR", Value: "/tmp"},
	}
	if !reflect.DeepEqual(evidence.Policy.Environment, wantFixed) {
		t.Fatalf("fixed environment = %#v, want %#v", evidence.Policy.Environment, wantFixed)
	}
	found := false
	for _, spec := range *commands {
		joined := strings.Join(spec.Args, "\x00")
		if !strings.Contains(joined, "restricted-exec") || !strings.Contains(joined, "/opt/demo/bin/demo") {
			continue
		}
		found = true
		wantSequence := []string{
			"--environment-profile", "portable-tool-v1",
			"--environment-entry", "JAVA_HOME=/opt/demo/jdk",
			"--environment-entry", "PLAYWRIGHT_BROWSERS_PATH=/opt/demo/browsers",
			"--record-exit-status", "--", "/opt/demo/bin/demo", "--version",
		}
		if !strings.Contains(joined, strings.Join(wantSequence, "\x00")) {
			t.Fatalf("probe argv = %#v, want the contract entries between the profile and the command", spec.Args)
		}
	}
	if !found {
		t.Fatalf("no restricted-exec probe command was issued: %#v", *commands)
	}
}

// A definition may add environment values but may never replace one the fixed
// probe policy owns.
func TestRunPortableToolValidationProfileRejectsContractOverridesOfFixedPolicy(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{}})
	for _, name := range []string{"PATH", "TMPDIR", "HOME", "LANG", "LC_ALL"} {
		t.Run(name, func(t *testing.T) {
			stubPortableToolProbeCommands(t, []portableToolProbeStubResponse{{stdout: []byte("demo\n")}})
			runtime := &providers.PortableToolRuntimeProjectionV1{
				InstallRoot: "/opt/demo",
				Environment: []providers.PortableToolEnvironmentVariableV1{{Name: name, Value: "/attacker"}},
			}
			_, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile, runtime)
			if err == nil || !strings.Contains(err.Error(), "owned by the fixed probe policy") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestRunPortableToolValidationProfileRejectsAnInvalidRuntimeProjection(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{}})
	stubPortableToolProbeCommands(t, []portableToolProbeStubResponse{{stdout: []byte("demo\n")}})
	runtime := &providers.PortableToolRuntimeProjectionV1{InstallRoot: "relative/root"}
	_, err := RunPortableToolValidationProfile(context.Background(), descriptor, workspace, profile, runtime)
	if err == nil || !strings.Contains(err.Error(), "runtime projection") {
		t.Fatalf("error = %v", err)
	}
}

func portableToolTestScheduleV1(t *testing.T, profile toolcatalog.ValidationProfileRecordV1) providers.PortableToolValidationScheduleV1 {
	t.Helper()
	digest, err := toolcatalog.ValidationProfileDigestV1(profile)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := canonical.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	value := canonical.Object{}
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	return providers.PortableToolValidationScheduleV1{
		Schema: providers.PortableToolValidationScheduleSchemaV1,
		Entries: []providers.PortableToolScheduledValidationV1{{
			Scope: "application", Tool: "demo",
			Profile: providers.PortableToolValidationProfileV1{
				Reference: providers.PortableToolRecordReferenceV1{ID: profile.ID, Digest: digest},
				Record:    providers.CanonicalProviderData{Schema: profile.Schema, Value: value},
			},
			Runtime: portableToolContractRuntimeV1(),
		}},
	}
}

// Production scheduling invokes the fixed executor once per selected profile,
// with that closure's contract runtime projection.
func TestRunPortableToolValidationScheduleV1InvokesTheFixedExecutorPerProfile(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--version"}})
	schedule := portableToolTestScheduleV1(t, profile)

	type invocation struct {
		profile toolcatalog.ValidationProfileRecordV1
		runtime *providers.PortableToolRuntimeProjectionV1
	}
	invocations := []invocation{}
	previous := runScheduledPortableToolValidationProfile
	t.Cleanup(func() { runScheduledPortableToolValidationProfile = previous })
	runScheduledPortableToolValidationProfile = func(
		_ context.Context,
		_ deploy.ImageDescriptor,
		_ PreparedProbeWorkspace,
		invoked toolcatalog.ValidationProfileRecordV1,
		runtime *providers.PortableToolRuntimeProjectionV1,
	) (PortableToolProbeEvidenceV1, error) {
		invocations = append(invocations, invocation{profile: invoked, runtime: runtime})
		return portableToolStubEvidenceV1(t, invoked, PortableToolProbeOutcomePassV1, 1), nil
	}

	scheduled, err := RunPortableToolValidationScheduleV1(context.Background(), descriptor, workspace, schedule)
	if err != nil {
		t.Fatal(err)
	}
	if len(invocations) != 1 {
		t.Fatalf("executor invocations = %d, want 1", len(invocations))
	}
	if !reflect.DeepEqual(invocations[0].profile, profile) {
		t.Fatalf("invoked profile = %#v, want %#v", invocations[0].profile, profile)
	}
	if invocations[0].runtime == nil || !reflect.DeepEqual(*invocations[0].runtime, *portableToolContractRuntimeV1()) {
		t.Fatalf("invoked runtime = %#v", invocations[0].runtime)
	}
	if len(scheduled) != 1 || scheduled[0].Scope != "application" || scheduled[0].Tool != "demo" ||
		scheduled[0].Profile != schedule.Entries[0].Profile.Reference {
		t.Fatalf("scheduled evidence = %#v", scheduled)
	}
}

// A probe observation is not support: a non-passing outcome fails scheduling
// instead of becoming validation evidence.
func TestRunPortableToolValidationScheduleV1RejectsNonPassingObservations(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{}})
	schedule := portableToolTestScheduleV1(t, profile)
	for _, test := range []struct {
		name    string
		outcome string
		results int
		want    string
	}{
		{name: "exit failure", outcome: PortableToolProbeOutcomeExitV1, results: 1, want: "reported exit-failure"},
		{name: "timeout", outcome: PortableToolProbeOutcomeTimeoutV1, results: 1, want: "reported timeout"},
		{name: "output limit", outcome: PortableToolProbeOutcomeOutputLimitV1, results: 1, want: "reported output-limit"},
		{name: "missing observation", outcome: PortableToolProbeOutcomePassV1, results: 0, want: "observed 0 of 1 declared probes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := runScheduledPortableToolValidationProfile
			t.Cleanup(func() { runScheduledPortableToolValidationProfile = previous })
			runScheduledPortableToolValidationProfile = func(
				_ context.Context, _ deploy.ImageDescriptor, _ PreparedProbeWorkspace,
				invoked toolcatalog.ValidationProfileRecordV1, _ *providers.PortableToolRuntimeProjectionV1,
			) (PortableToolProbeEvidenceV1, error) {
				return portableToolStubEvidenceV1(t, invoked, test.outcome, test.results), nil
			}
			_, err := RunPortableToolValidationScheduleV1(context.Background(), descriptor, workspace, schedule)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

// The schedule decodes the locked profile record and refuses a substituted one
// before any probe container is created.
func TestRunPortableToolValidationScheduleV1RejectsASubstitutedProfileBeforeRunning(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{}})
	schedule := portableToolTestScheduleV1(t, profile)
	schedule.Entries[0].Profile.Record.Value["version"] = "9.9.9"

	previous := runScheduledPortableToolValidationProfile
	t.Cleanup(func() { runScheduledPortableToolValidationProfile = previous })
	runScheduledPortableToolValidationProfile = func(
		context.Context, deploy.ImageDescriptor, PreparedProbeWorkspace,
		toolcatalog.ValidationProfileRecordV1, *providers.PortableToolRuntimeProjectionV1,
	) (PortableToolProbeEvidenceV1, error) {
		t.Fatal("executor ran for a substituted profile record")
		return PortableToolProbeEvidenceV1{}, nil
	}
	_, err := RunPortableToolValidationScheduleV1(context.Background(), descriptor, workspace, schedule)
	if err == nil || !strings.Contains(err.Error(), "schedule portable-tool validation") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunPortableToolValidationScheduleV1RejectsAnInvalidSchedule(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	_, err := RunPortableToolValidationScheduleV1(
		context.Background(), descriptor, workspace, providers.PortableToolValidationScheduleV1{},
	)
	if err == nil || !strings.Contains(err.Error(), "schema must be") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunPortableToolValidationScheduleV1PropagatesExecutorFailure(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{}})
	schedule := portableToolTestScheduleV1(t, profile)
	sentinel := errors.New("probe infrastructure failure")
	previous := runScheduledPortableToolValidationProfile
	t.Cleanup(func() { runScheduledPortableToolValidationProfile = previous })
	runScheduledPortableToolValidationProfile = func(
		context.Context, deploy.ImageDescriptor, PreparedProbeWorkspace,
		toolcatalog.ValidationProfileRecordV1, *providers.PortableToolRuntimeProjectionV1,
	) (PortableToolProbeEvidenceV1, error) {
		return PortableToolProbeEvidenceV1{}, sentinel
	}
	_, err := RunPortableToolValidationScheduleV1(context.Background(), descriptor, workspace, schedule)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want %v", err, sentinel)
	}
}

// Evidence is bound to the exact locked profile reference, which keeps it
// outside selected-closure identity.
func TestPortableToolValidationEvidenceV1BindsTheLockedProfileReference(t *testing.T) {
	subject := canonical.Digest("sha256:" + strings.Repeat("ab", 32))
	reference := providers.PortableToolRecordReferenceV1{
		ID:     "tool:demo/releases/1.2.3/validation/profiles/default",
		Digest: canonical.Digest("sha256:" + strings.Repeat("cd", 32)),
	}
	evidence, err := PortableToolValidationEvidenceV1(subject, []PortableToolScheduledEvidenceV1{{
		Scope: "application", Tool: "demo", Profile: reference,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 1 || evidence[0].SubjectRootFS != subject || evidence[0].ProfileDigest != reference.Digest {
		t.Fatalf("evidence = %#v", evidence)
	}
}

// Production validation preparation carries the locked schedule onto the
// final image input, which is what gives the fixed executor a production
// caller at all.
func TestPrepareProviderGraphValidationCarriesTheLockedPortableToolSchedule(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	lock := portableToolLockForValidationScheduleV1(t, fixture.request.Plan, fixture.request.NodeID)
	want, err := providers.PortableToolValidationScheduleFromLockV1(lock)
	if err != nil {
		t.Fatal(err)
	}
	if len(want.Entries) == 0 {
		t.Fatal("fixture schedules no portable-tool validation")
	}

	baseImage, err := realizedImageFromDescriptor(fixture.lock.Base)
	if err != nil {
		t.Fatal(err)
	}
	graph := providers.GraphExecutionResult{
		Plan: fixture.request.Plan, SelectedEdges: []providers.ProviderEdgeV1{},
		Bundles: []providers.ResolvedBundle{}, Profiles: []providers.RequirementProfile{},
		ValidationEvidence: []providers.ValidationEvidence{},
		PrefixImages:       []providers.RealizedImageV1{baseImage},
		Materializations:   []providers.GraphNodeMaterializeResult{},
		Catalog:            []providers.RealizedOutput{},
	}
	result, err := prepareProviderGraphValidation(
		context.Background(), fixture.lock.Base, []providers.RealizedOutput{}, graph, &lock, fixture.lock.RuntimePolicy,
		func(_ context.Context, _ BuiltImageCandidate, _ blueprint.Platform) (InspectedImageCandidate, error) {
			return inspectedValidationCandidate(t, fixture.lock.Base), nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Final.PortableTools, want) {
		t.Fatalf("final schedule = %#v, want %#v", result.Final.PortableTools, want)
	}
}

// A build that selects no portable tool schedules none, and starts no probe.
func TestProviderFullImageValidationSkipsSchedulingWithoutSelectedPortableTools(t *testing.T) {
	previous := prepareFullValidationProbeWorkspace
	t.Cleanup(func() { prepareFullValidationProbeWorkspace = previous })
	prepared := false
	prepareFullValidationProbeWorkspace = func(
		context.Context, providerstore.Store, blueprint.Platform,
	) (PreparedProbeWorkspace, func() error, error) {
		prepared = true
		return PreparedProbeWorkspace{}, func() error { return nil }, nil
	}
	scheduled, err := runPortableToolScheduleIfSelectedV1(
		context.Background(), providerstore.Store{}, deploy.ImageDescriptor{}, providers.PortableToolValidationScheduleV1{},
	)
	if err != nil || scheduled != nil || prepared {
		t.Fatalf("absent schedule ran portable-tool validation: scheduled=%#v prepared=%v err=%v", scheduled, prepared, err)
	}
	empty := providers.PortableToolValidationScheduleV1{
		Schema:  providers.PortableToolValidationScheduleSchemaV1,
		Entries: []providers.PortableToolScheduledValidationV1{},
	}
	scheduled, err = runPortableToolScheduleIfSelectedV1(context.Background(), providerstore.Store{}, deploy.ImageDescriptor{}, empty)
	if err != nil || scheduled != nil || prepared {
		t.Fatalf("empty schedule = %#v, prepared = %v, err = %v", scheduled, prepared, err)
	}
}

// Layer prefixes must not carry the schedule: the final input is the only one
// that does, so the probes run exactly once against the shipped image.
func TestPrepareProviderGraphValidationSchedulesPortableToolsOnlyOnTheFinalInput(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	lock := portableToolLockForValidationScheduleV1(t, fixture.request.Plan, fixture.request.NodeID)
	bundle, err := providers.LoadResolvedBundleManifest(
		fixture.store, fixture.lock.Nodes[0].BundleManifest, pythonprovider.ValidateResolvedBundlePayloadV1,
	)
	if err != nil {
		t.Fatal(err)
	}
	layerDescriptor := providerBaseDescriptor(t, false)
	layerDescriptor.ConfigDigest = rendererDigest("d")
	layerDescriptor.AuthorReference = string(layerDescriptor.ConfigDigest)
	layerDescriptor.ImmutableReference = string(layerDescriptor.ConfigDigest)
	layerDescriptor.RootFSDiffIDs = []canonical.Digest{rendererDigest("e")}
	layerImage, err := realizedImageFromDescriptor(layerDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	baseImage, err := realizedImageFromDescriptor(fixture.lock.Base)
	if err != nil {
		t.Fatal(err)
	}
	materialized := fixture.lock.Nodes[0]
	graph := providers.GraphExecutionResult{
		Plan: fixture.request.Plan, SelectedEdges: append([]providers.ProviderEdgeV1{}, fixture.request.Plan.Edges...),
		Bundles: []providers.ResolvedBundle{bundle}, Profiles: []providers.RequirementProfile{materialized.RequirementProfile},
		ValidationEvidence: []providers.ValidationEvidence{materialized.ValidationEvidence},
		PrefixImages:       []providers.RealizedImageV1{baseImage, layerImage},
		Materializations: []providers.GraphNodeMaterializeResult{{
			Image: layerImage, TransactionDigest: materialized.TransactionDigest,
			GeneratedExecutables: []providers.RealizedGeneratedExecutable{}, Outputs: []providers.RealizedOutput{},
		}},
		Catalog: append([]providers.RealizedOutput{}, fixture.request.EarlierCatalog...),
	}
	result, err := prepareProviderGraphValidation(
		context.Background(), fixture.lock.Base, fixture.request.EarlierCatalog, graph, &lock, fixture.lock.RuntimePolicy,
		func(_ context.Context, _ BuiltImageCandidate, _ blueprint.Platform) (InspectedImageCandidate, error) {
			return inspectedValidationCandidate(t, layerDescriptor), nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Layers) == 0 {
		t.Fatal("fixture produced no layer inputs")
	}
	for index, layer := range result.Layers {
		if !portableToolScheduleAbsentV1(layer.PortableTools) {
			t.Fatalf("layer %d carries a portable-tool schedule: %#v", index, layer.PortableTools)
		}
	}
	if len(result.Final.PortableTools.Entries) == 0 {
		t.Fatal("final input carries no portable-tool schedule")
	}
}

// Recorded evidence must prove the fixed policy survived and that contract
// additions stayed well formed, independent of how the probe was invoked.
func TestValidatePortableToolProbePolicyV1RejectsTamperedContractEvidence(t *testing.T) {
	base := func() PortableToolProbePolicyV1 {
		policy, _, err := portableToolProbePolicyV1(portableToolContractRuntimeV1())
		if err != nil {
			t.Fatal(err)
		}
		return policy
	}
	if err := validatePortableToolProbePolicyV1(base()); err != nil {
		t.Fatalf("valid contract policy rejected: %v", err)
	}
	fixedOnly, _, err := portableToolProbePolicyV1(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePortableToolProbePolicyV1(fixedOnly); err != nil {
		t.Fatalf("fixed-only policy rejected: %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*PortableToolProbePolicyV1)
		want   string
	}{
		{
			name:   "weakened fixed policy",
			mutate: func(p *PortableToolProbePolicyV1) { p.NetworkDisabled = false },
			want:   "not the fixed executor policy",
		},
		{
			name:   "weakened fixed environment",
			mutate: func(p *PortableToolProbePolicyV1) { p.Environment[3].Value = "/attacker/bin" },
			want:   "not the fixed executor policy",
		},
		{
			name:   "nil contract array",
			mutate: func(p *PortableToolProbePolicyV1) { p.ContractEnvironment = nil },
			want:   "explicit array",
		},
		{
			name:   "contract without install root",
			mutate: func(p *PortableToolProbePolicyV1) { p.InstallRoot = "" },
			want:   "requires a contract install root",
		},
		{
			name: "contract shadows a fixed name",
			mutate: func(p *PortableToolProbePolicyV1) {
				p.ContractEnvironment[0] = toolcatalog.RecordEnvironmentVariableV1{Name: "PATH", Value: "/attacker"}
			},
			want: "owned by the fixed probe policy",
		},
		{
			name: "unsorted contract",
			mutate: func(p *PortableToolProbePolicyV1) {
				p.ContractEnvironment[0], p.ContractEnvironment[1] = p.ContractEnvironment[1], p.ContractEnvironment[0]
			},
			want: "unique and sorted",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := base()
			test.mutate(&policy)
			err := validatePortableToolProbePolicyV1(policy)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

// With portable tools selected, the runner's scheduling hook actually runs the
// schedule instead of skipping it.
func TestRunPortableToolScheduleIfSelectedV1RunsASelectedSchedule(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(
		toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--version"}},
	)
	schedule := portableToolTestScheduleV1(t, profile)
	previous := runScheduledPortableToolValidationProfile
	t.Cleanup(func() { runScheduledPortableToolValidationProfile = previous })
	ran := false
	runScheduledPortableToolValidationProfile = func(
		_ context.Context, _ deploy.ImageDescriptor, _ PreparedProbeWorkspace,
		invoked toolcatalog.ValidationProfileRecordV1, _ *providers.PortableToolRuntimeProjectionV1,
	) (PortableToolProbeEvidenceV1, error) {
		ran = true
		return portableToolStubEvidenceV1(t, invoked, PortableToolProbeOutcomePassV1, 1), nil
	}
	previousWorkspace := prepareFullValidationProbeWorkspace
	t.Cleanup(func() { prepareFullValidationProbeWorkspace = previousWorkspace })
	cleaned := false
	prepareFullValidationProbeWorkspace = func(
		context.Context, providerstore.Store, blueprint.Platform,
	) (PreparedProbeWorkspace, func() error, error) {
		return workspace, func() error { cleaned = true; return nil }, nil
	}
	scheduled, err := runPortableToolScheduleIfSelectedV1(context.Background(), providerstore.Store{}, descriptor, schedule)
	if err != nil {
		t.Fatal(err)
	}
	if !ran || len(scheduled) != 1 {
		t.Fatalf("selected schedule did not run: ran=%v scheduled=%#v", ran, scheduled)
	}
	if !cleaned {
		t.Fatal("portable-tool validation workspace was not cleaned up")
	}
}

// The probe container name is derived from the workspace directory, so
// portable-tool scheduling must never reuse the validation workspace whose
// container the caller still holds open.
func TestRunPortableToolScheduleIfSelectedV1UsesItsOwnProbeWorkspace(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	held := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	dedicated := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	if imageProbeContainerName(held.HostDir) == imageProbeContainerName(dedicated.HostDir) {
		t.Fatal("fixture workspaces do not produce distinct container names")
	}
	profile := portableToolValidationProfile(
		toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--version"}},
	)
	schedule := portableToolTestScheduleV1(t, profile)

	previousWorkspace := prepareFullValidationProbeWorkspace
	t.Cleanup(func() { prepareFullValidationProbeWorkspace = previousWorkspace })
	prepareFullValidationProbeWorkspace = func(
		context.Context, providerstore.Store, blueprint.Platform,
	) (PreparedProbeWorkspace, func() error, error) {
		return dedicated, func() error { return nil }, nil
	}
	previous := runScheduledPortableToolValidationProfile
	t.Cleanup(func() { runScheduledPortableToolValidationProfile = previous })
	var observed PreparedProbeWorkspace
	runScheduledPortableToolValidationProfile = func(
		_ context.Context, _ deploy.ImageDescriptor, workspace PreparedProbeWorkspace,
		invoked toolcatalog.ValidationProfileRecordV1, _ *providers.PortableToolRuntimeProjectionV1,
	) (PortableToolProbeEvidenceV1, error) {
		observed = workspace
		return portableToolStubEvidenceV1(t, invoked, PortableToolProbeOutcomePassV1, 1), nil
	}
	if _, err := runPortableToolScheduleIfSelectedV1(
		context.Background(), providerstore.Store{}, descriptor, schedule,
	); err != nil {
		t.Fatal(err)
	}
	if observed.HostDir != dedicated.HostDir {
		t.Fatalf("probe workspace = %q, want the dedicated workspace %q", observed.HostDir, dedicated.HostDir)
	}
	if observed.HostDir == held.HostDir {
		t.Fatal("portable-tool scheduling reused the held validation workspace")
	}
}

// R1-1: two closures selecting the same profile contribute one evidence
// record, so validateFullImageEvidence's unique-digest expectation holds.
func TestPortableToolValidationEvidenceV1CollapsesRepeatedProfileDigests(t *testing.T) {
	subject := canonical.Digest("sha256:" + strings.Repeat("ab", 32))
	shared := providers.PortableToolRecordReferenceV1{
		ID:     "tool:demo/releases/1.2.3/validation/profiles/default",
		Digest: canonical.Digest("sha256:" + strings.Repeat("cd", 32)),
	}
	other := providers.PortableToolRecordReferenceV1{
		ID:     "tool:alpha/releases/1.0.0/validation/profiles/default",
		Digest: canonical.Digest("sha256:" + strings.Repeat("ef", 32)),
	}
	evidence, err := PortableToolValidationEvidenceV1(subject, []PortableToolScheduledEvidenceV1{
		{Scope: "application", Tool: "demo", Profile: shared},
		{Scope: "system", Tool: "demo", Profile: shared},
		{Scope: "application", Tool: "alpha", Profile: other},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 2 {
		t.Fatalf("evidence = %d records, want 2 distinct profile digests: %#v", len(evidence), evidence)
	}
	if evidence[0].ProfileDigest != shared.Digest || evidence[1].ProfileDigest != other.Digest {
		t.Fatalf("evidence digests = %#v", evidence)
	}
}

// R1-1 end to end: a duplicated profile must not fail final image validation.
func TestValidateFullImageEvidenceAcceptsARepeatedPortableToolProfile(t *testing.T) {
	profile := portableToolValidationProfile(
		toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--version"}},
	)
	schedule := portableToolTestScheduleV1(t, profile)
	repeated := schedule.Entries[0]
	repeated.Scope = "system"
	schedule.Entries = append(schedule.Entries, repeated)

	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	candidate := inspectedValidationCandidate(t, descriptor)
	input := FullImageValidationInput{
		Image:         candidate,
		Profiles:      []providers.RequirementProfile{},
		Outputs:       []providers.RealizedOutput{},
		PortableTools: schedule,
	}
	evidence, err := PortableToolValidationEvidenceV1(
		candidate.Image.RootFSSubject,
		[]PortableToolScheduledEvidenceV1{
			{Scope: "application", Tool: "demo", Profile: schedule.Entries[0].Profile.Reference},
			{Scope: "system", Tool: "demo", Profile: repeated.Profile.Reference},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateFullImageEvidence(
		input, evidence, []providers.ExecutableEvidence{}, registry.ValidateRequirementProfileV1,
	); err != nil {
		t.Fatalf("repeated portable-tool profile rejected final image evidence: %v", err)
	}
}

// R1-2: the scheduling boundary proves attribution itself.
func TestRunPortableToolValidationScheduleV1RejectsMisattributedEvidence(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(
		toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--version"}},
	)
	schedule := portableToolTestScheduleV1(t, profile)
	for _, test := range []struct {
		name    string
		profile providers.PortableToolRecordReferenceV1
	}{
		{
			name: "different id",
			profile: providers.PortableToolRecordReferenceV1{
				ID:     "tool:alpha/releases/1.0.0/validation/profiles/default",
				Digest: schedule.Entries[0].Profile.Reference.Digest,
			},
		},
		{
			name: "different digest",
			profile: providers.PortableToolRecordReferenceV1{
				ID:     schedule.Entries[0].Profile.Reference.ID,
				Digest: canonical.Digest("sha256:" + strings.Repeat("99", 32)),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := runScheduledPortableToolValidationProfile
			t.Cleanup(func() { runScheduledPortableToolValidationProfile = previous })
			runScheduledPortableToolValidationProfile = func(
				_ context.Context, _ deploy.ImageDescriptor, _ PreparedProbeWorkspace,
				invoked toolcatalog.ValidationProfileRecordV1, _ *providers.PortableToolRuntimeProjectionV1,
			) (PortableToolProbeEvidenceV1, error) {
				pass := "0"
				return PortableToolProbeEvidenceV1{
					Profile:           test.profile,
					ProfileDefinition: invoked,
					Results: []PortableToolProbeResultV1{{
						Probe: invoked.Probes[0], Outcome: PortableToolProbeOutcomePassV1, ExitCode: &pass,
					}},
				}, nil
			}
			_, err := RunPortableToolValidationScheduleV1(context.Background(), descriptor, workspace, schedule)
			if err == nil || !strings.Contains(err.Error(), "attributed to profile") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

// Codex review-comment:3921143878 — the completion input check must accept the
// deliberate final-only schedule instead of rejecting every portable-tool build.
func TestValidateProviderBuildCompletionInputAcceptsTheFinalOnlyPortableToolSchedule(t *testing.T) {
	input, _, _ := providerBuildCompletionFixture(t)
	policy, err := providerBuildRuntimePolicyV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(input.Validation.Layers) == 0 {
		t.Fatal("fixture has no graph layers to compare against")
	}
	if err := validateProviderBuildCompletionInput(input, policy); err != nil {
		t.Fatalf("fixture rejected before the portable-tool schedule was added: %v", err)
	}

	lock := portableToolLockForValidationScheduleV1(t, input.Graph.Plan, input.Graph.Plan.Nodes[0].ID)
	schedule, err := PortableToolValidationScheduleFromBuildLockV1(&lock)
	if err != nil {
		t.Fatal(err)
	}
	if len(schedule.Entries) == 0 {
		t.Fatal("fixture schedules no portable-tool validation")
	}

	// Only the final input carries the schedule, exactly as
	// prepareProviderGraphValidation produces it.
	input.Validation.Final.PortableTools = schedule
	if err := validateProviderBuildCompletionInput(input, policy); err != nil {
		t.Fatalf("final-only portable-tool schedule rejected: %v", err)
	}

	// A layer that also carries the schedule is rejected, so the probes cannot
	// silently run twice.
	input.Validation.Layers[len(input.Validation.Layers)-1].PortableTools = schedule
	err = validateProviderBuildCompletionInput(input, policy)
	if err == nil || !strings.Contains(err.Error(), "must not schedule portable tools") {
		t.Fatalf("layer-carried schedule error = %v", err)
	}
}

// Codex review-comment:3921143886 — lock-backed entry points must reconstruct
// the locked schedule rather than dropping portable evidence.
func TestPortableToolValidationScheduleFromBuildLockV1ProjectsLockedProfiles(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	lock := portableToolLockForValidationScheduleV1(t, fixture.request.Plan, fixture.request.NodeID)
	schedule, err := PortableToolValidationScheduleFromBuildLockV1(&lock)
	if err != nil {
		t.Fatal(err)
	}
	want, err := providers.PortableToolValidationScheduleFromLockV1(lock)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(schedule, want) {
		t.Fatalf("schedule = %#v, want %#v", schedule, want)
	}
	if len(schedule.Entries) == 0 {
		t.Fatal("locked schedule is empty")
	}
	// A build that locked no portable tools schedules nothing.
	absent, err := PortableToolValidationScheduleFromBuildLockV1(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !portableToolScheduleAbsentV1(absent) {
		t.Fatalf("absent lock produced %#v", absent)
	}
}

// Integration seam: the plan prepareProviderGraphValidation actually produces
// must be accepted by validateProviderBuildCompletionInput. Unit-testing the
// two halves separately is what let a defect that rejected every portable-tool
// build reach review.
func TestPreparedGraphValidationPlanPassesCompletionValidation(t *testing.T) {
	input, _, _ := providerBuildCompletionFixture(t)
	policy, err := providerBuildRuntimePolicyV1(input)
	if err != nil {
		t.Fatal(err)
	}
	lock := portableToolLockForValidationScheduleV1(t, input.Graph.Plan, input.Graph.Plan.Nodes[0].ID)

	last := input.Validation.Layers[len(input.Validation.Layers)-1]
	prepared, err := prepareProviderGraphValidation(
		context.Background(), input.Base, input.BaseCatalog, input.Graph, &lock, policy,
		func(_ context.Context, _ BuiltImageCandidate, _ blueprint.Platform) (InspectedImageCandidate, error) {
			return last.Image, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Final.PortableTools.Entries) == 0 {
		t.Fatal("prepared plan carries no portable-tool schedule")
	}

	// Feed the real prepared plan straight into the completion check.
	input.Validation = prepared
	input.PortableTools = &lock
	if err := validateProviderBuildCompletionInput(input, policy); err != nil {
		t.Fatalf("prepared portable-tool validation plan rejected at completion: %v", err)
	}
}
