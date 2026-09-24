package dockerdeploy

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

// portableToolStubEvidenceV1 mirrors what the fixed executor returns: evidence
// attributed to the exact profile it was asked to run.
func portableToolStubEvidenceV1(
	t *testing.T,
	descriptor deploy.ImageDescriptor,
	invoked toolcatalog.ValidationProfileRecordV1,
	runtime *providers.PortableToolRuntimeProjectionV1,
	outcome string,
	results int,
) PortableToolProbeEvidenceV1 {
	t.Helper()
	digest, err := toolcatalog.ValidationProfileDigestV1(invoked)
	if err != nil {
		t.Fatal(err)
	}
	observed := make([]PortableToolProbeResultV1, 0, results)
	emptyOutput := newBoundedPortableToolProbeOutput(portableToolProbeOutputLimit, nil).Evidence()
	for index := 0; index < results && index < len(invoked.Probes); index++ {
		exit := "0"
		result := PortableToolProbeResultV1{
			Probe: invoked.Probes[index], Outcome: outcome,
			Stdout: emptyOutput, Stderr: emptyOutput,
		}
		if outcome == PortableToolProbeOutcomePassV1 {
			result.ExitCode = &exit
		} else if outcome == PortableToolProbeOutcomeExitV1 {
			exit = "1"
			result.ExitCode = &exit
		} else if outcome == PortableToolProbeOutcomeOutputLimitV1 {
			bounded := newBoundedPortableToolProbeOutput(portableToolProbeOutputLimit, nil)
			_, _ = bounded.Write(make([]byte, portableToolProbeOutputLimit+1))
			result.Stdout = bounded.Evidence()
		}
		observed = append(observed, result)
	}
	subject, err := deploy.RootFSSubject(descriptor.RootFSDiffIDs)
	if err != nil {
		t.Fatal(err)
	}
	policy, _, err := portableToolProbePolicyV1(runtime)
	if err != nil {
		t.Fatal(err)
	}
	return PortableToolProbeEvidenceV1{
		Schema: PortableToolProbeEvidenceSchemaV1, ExecutorVersion: PortableToolProbeExecutorV1,
		Profile:           providers.PortableToolRecordReferenceV1{ID: invoked.ID, Digest: digest},
		ProfileDefinition: invoked,
		SubjectRootFS:     subject, Platform: descriptor.Platform, Policy: policy,
		Results: observed,
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
			Scope: "application:demo", Tool: "demo",
			Profile: providers.PortableToolValidationProfileV1{
				Reference: providers.PortableToolRecordReferenceV1{ID: profile.ID, Digest: digest},
				Record:    providers.CanonicalProviderData{Schema: profile.Schema, Value: value},
			},
			Runtime: portableToolContractRuntimeV1(),
		}},
	}
}

func portableToolMaterializationTestInputV1(
	t *testing.T,
	image InspectedImageCandidate,
	schedule providers.PortableToolValidationScheduleV1,
) PortableToolMaterializationValidationInputV1 {
	t.Helper()
	selected, err := constructScheduledPortableToolProfiles(schedule)
	if err != nil {
		t.Fatal(err)
	}
	return PortableToolMaterializationValidationInputV1{Image: image, selected: selected}
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
		gotDescriptor deploy.ImageDescriptor,
		_ PreparedProbeWorkspace,
		invoked toolcatalog.ValidationProfileRecordV1,
		runtime *providers.PortableToolRuntimeProjectionV1,
	) (PortableToolProbeEvidenceV1, error) {
		invocations = append(invocations, invocation{profile: invoked, runtime: runtime})
		return portableToolStubEvidenceV1(t, gotDescriptor, invoked, runtime, PortableToolProbeOutcomePassV1, 1), nil
	}

	evidence, err := RunPortableToolValidationScheduleV1(context.Background(), descriptor, workspace, schedule)
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
	if len(evidence) != 1 || evidence[0].PortableToolProfileID != schedule.Entries[0].Profile.Reference.ID ||
		evidence[0].ProfileDigest != schedule.Entries[0].Profile.Reference.Digest ||
		evidence[0].PortableToolRuntimeDigest == "" {
		t.Fatalf("scheduled evidence = %#v", evidence)
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
		{name: "missing observation", outcome: PortableToolProbeOutcomePassV1, results: 0, want: "invalid evidence"},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := runScheduledPortableToolValidationProfile
			t.Cleanup(func() { runScheduledPortableToolValidationProfile = previous })
			runScheduledPortableToolValidationProfile = func(
				_ context.Context, gotDescriptor deploy.ImageDescriptor, _ PreparedProbeWorkspace,
				invoked toolcatalog.ValidationProfileRecordV1, runtime *providers.PortableToolRuntimeProjectionV1,
			) (PortableToolProbeEvidenceV1, error) {
				return portableToolStubEvidenceV1(t, gotDescriptor, invoked, runtime, test.outcome, test.results), nil
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

// Durable evidence identifies the exact locked profile and runtime inputs.
func TestPortableToolValidationEvidenceBindsTheLockedProfileAndRuntime(t *testing.T) {
	subject := canonical.Digest("sha256:" + strings.Repeat("ab", 32))
	reference := providers.PortableToolRecordReferenceV1{
		ID:     "tool:demo/releases/1.2.3/validation/profiles/default",
		Digest: canonical.Digest("sha256:" + strings.Repeat("cd", 32)),
	}
	evidence, err := providers.NewPortableToolValidationEvidence(subject, reference, portableToolContractRuntimeV1())
	if err != nil {
		t.Fatal(err)
	}
	if evidence.SubjectRootFS != subject || evidence.ProfileDigest != reference.Digest ||
		evidence.PortableToolProfileID != reference.ID || evidence.PortableToolRuntimeDigest == "" {
		t.Fatalf("evidence = %#v", evidence)
	}
}

// An explicit empty selection is accepted without preparing a probe workspace.
func TestValidatePortableToolMaterializationV1AcceptsAnEmptyScheduleWithoutRunning(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	previous := preparePortableToolValidationWorkspace
	t.Cleanup(func() { preparePortableToolValidationWorkspace = previous })
	prepared := false
	preparePortableToolValidationWorkspace = func(
		context.Context, providerstore.Store, blueprint.Platform,
	) (PreparedProbeWorkspace, func() error, error) {
		prepared = true
		return PreparedProbeWorkspace{}, func() error { return nil }, nil
	}
	empty := providers.PortableToolValidationScheduleV1{
		Schema:  providers.PortableToolValidationScheduleSchemaV1,
		Entries: []providers.PortableToolScheduledValidationV1{},
	}
	evidence, err := ValidatePortableToolMaterializationV1(
		context.Background(), providerstore.Store{}, portableToolMaterializationTestInputV1(
			t, inspectedValidationCandidate(t, descriptor), empty,
		),
	)
	if err != nil || evidence == nil || len(evidence) != 0 || prepared {
		t.Fatalf("empty schedule evidence = %#v, prepared = %v, err = %v", evidence, prepared, err)
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
			name:   "relative install root",
			mutate: func(p *PortableToolProbePolicyV1) { p.InstallRoot = "relative/root" },
			want:   "runtime install root",
		},
		{
			name:   "non-canonical install root",
			mutate: func(p *PortableToolProbePolicyV1) { p.InstallRoot = "/opt/demo/../demo" },
			want:   "runtime install root",
		},
		{
			name: "invalid contract environment name",
			mutate: func(p *PortableToolProbePolicyV1) {
				p.ContractEnvironment[0].Name = "lowercase"
			},
			want: "environment name",
		},
		{
			name: "contract environment control character",
			mutate: func(p *PortableToolProbePolicyV1) {
				p.ContractEnvironment[0].Value = "/opt/demo/\x01"
			},
			want: "control character",
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

// The materialization boundary invokes the selected schedule against exactly
// the image supplied by its usage owner and returns rootfs-bound evidence.
func TestValidatePortableToolMaterializationV1RunsASelectedSchedule(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	candidate := inspectedValidationCandidate(t, descriptor)
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(
		toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--version"}},
	)
	schedule := portableToolTestScheduleV1(t, profile)
	previous := runScheduledPortableToolValidationProfile
	t.Cleanup(func() { runScheduledPortableToolValidationProfile = previous })
	ran := false
	var observedDescriptor deploy.ImageDescriptor
	runScheduledPortableToolValidationProfile = func(
		_ context.Context, gotDescriptor deploy.ImageDescriptor, _ PreparedProbeWorkspace,
		invoked toolcatalog.ValidationProfileRecordV1, runtime *providers.PortableToolRuntimeProjectionV1,
	) (PortableToolProbeEvidenceV1, error) {
		ran = true
		observedDescriptor = gotDescriptor
		return portableToolStubEvidenceV1(t, gotDescriptor, invoked, runtime, PortableToolProbeOutcomePassV1, 1), nil
	}
	previousWorkspace := preparePortableToolValidationWorkspace
	t.Cleanup(func() { preparePortableToolValidationWorkspace = previousWorkspace })
	cleaned := false
	preparePortableToolValidationWorkspace = func(
		context.Context, providerstore.Store, blueprint.Platform,
	) (PreparedProbeWorkspace, func() error, error) {
		return workspace, func() error { cleaned = true; return nil }, nil
	}
	evidence, err := ValidatePortableToolMaterializationV1(
		context.Background(), providerstore.Store{}, portableToolMaterializationTestInputV1(t, candidate, schedule),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !ran || len(evidence) != 1 {
		t.Fatalf("selected schedule did not run: ran=%v evidence=%#v", ran, evidence)
	}
	if !reflect.DeepEqual(observedDescriptor, candidate.Descriptor) {
		t.Fatalf("validated descriptor = %#v, want %#v", observedDescriptor, candidate.Descriptor)
	}
	if evidence[0].SubjectRootFS != candidate.Image.RootFSSubject ||
		evidence[0].ProfileDigest != schedule.Entries[0].Profile.Reference.Digest {
		t.Fatalf("materialization evidence = %#v", evidence)
	}
	if !cleaned {
		t.Fatal("portable-tool validation workspace was not cleaned up")
	}
}

// The probe container name is derived from the workspace directory, so
// portable-tool scheduling must never reuse the validation workspace whose
// container the caller still holds open.
func TestValidatePortableToolMaterializationV1UsesItsOwnProbeWorkspace(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	candidate := inspectedValidationCandidate(t, descriptor)
	held := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	dedicated := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	if imageProbeContainerName(held.HostDir) == imageProbeContainerName(dedicated.HostDir) {
		t.Fatal("fixture workspaces do not produce distinct container names")
	}
	profile := portableToolValidationProfile(
		toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--version"}},
	)
	schedule := portableToolTestScheduleV1(t, profile)

	previousWorkspace := preparePortableToolValidationWorkspace
	t.Cleanup(func() { preparePortableToolValidationWorkspace = previousWorkspace })
	preparePortableToolValidationWorkspace = func(
		context.Context, providerstore.Store, blueprint.Platform,
	) (PreparedProbeWorkspace, func() error, error) {
		return dedicated, func() error { return nil }, nil
	}
	previous := runScheduledPortableToolValidationProfile
	t.Cleanup(func() { runScheduledPortableToolValidationProfile = previous })
	var observed PreparedProbeWorkspace
	runScheduledPortableToolValidationProfile = func(
		_ context.Context, gotDescriptor deploy.ImageDescriptor, workspace PreparedProbeWorkspace,
		invoked toolcatalog.ValidationProfileRecordV1, runtime *providers.PortableToolRuntimeProjectionV1,
	) (PortableToolProbeEvidenceV1, error) {
		observed = workspace
		return portableToolStubEvidenceV1(t, gotDescriptor, invoked, runtime, PortableToolProbeOutcomePassV1, 1), nil
	}
	if _, err := ValidatePortableToolMaterializationV1(
		context.Background(), providerstore.Store{}, portableToolMaterializationTestInputV1(t, candidate, schedule),
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

// Every selected case executes even when it shares a profile. Only identical
// profile, image, and runtime inputs may coalesce in durable evidence.
func TestRunPortableToolValidationScheduleV1CoalescesOnlyIdenticalInputs(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{}})
	schedule := portableToolTestScheduleV1(t, profile)
	same := schedule.Entries[0]
	same.Scope = "source-builder:alpha"
	different := schedule.Entries[0]
	different.Scope = "source-builder:beta"
	different.Runtime = &providers.PortableToolRuntimeProjectionV1{
		InstallRoot: "/opt/other", Environment: []providers.PortableToolEnvironmentVariableV1{},
	}
	schedule.Entries = append(schedule.Entries, same, different)
	runs := 0
	previous := runScheduledPortableToolValidationProfile
	t.Cleanup(func() { runScheduledPortableToolValidationProfile = previous })
	runScheduledPortableToolValidationProfile = func(
		_ context.Context, gotDescriptor deploy.ImageDescriptor, _ PreparedProbeWorkspace,
		invoked toolcatalog.ValidationProfileRecordV1, runtime *providers.PortableToolRuntimeProjectionV1,
	) (PortableToolProbeEvidenceV1, error) {
		runs++
		return portableToolStubEvidenceV1(t, gotDescriptor, invoked, runtime, PortableToolProbeOutcomePassV1, 1), nil
	}
	evidence, err := RunPortableToolValidationScheduleV1(context.Background(), descriptor, workspace, schedule)
	if err != nil {
		t.Fatal(err)
	}
	if runs != 3 || len(evidence) != 2 || evidence[0].PortableToolRuntimeDigest == evidence[1].PortableToolRuntimeDigest {
		t.Fatalf("runs = %d, evidence = %#v", runs, evidence)
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

func TestRunPortableToolValidationScheduleV1RejectsObservationDrift(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{}})
	schedule := portableToolTestScheduleV1(t, profile)
	for _, test := range []struct {
		name   string
		mutate func(*PortableToolProbeEvidenceV1)
		want   string
	}{
		{name: "image subject", mutate: func(e *PortableToolProbeEvidenceV1) {
			e.SubjectRootFS = canonical.Digest("sha256:" + strings.Repeat("99", 32))
		}, want: "different image subject"},
		{name: "runtime projection", mutate: func(e *PortableToolProbeEvidenceV1) {
			e.Policy.InstallRoot = "/opt/other"
		}, want: "different runtime projection"},
		{name: "truncated pass output", mutate: func(e *PortableToolProbeEvidenceV1) {
			bounded := newBoundedPortableToolProbeOutput(portableToolProbeOutputLimit, nil)
			_, _ = bounded.Write(make([]byte, portableToolProbeOutputLimit+1))
			e.Results[0].Stdout = bounded.Evidence()
		}, want: "invalid evidence"},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := runScheduledPortableToolValidationProfile
			t.Cleanup(func() { runScheduledPortableToolValidationProfile = previous })
			runScheduledPortableToolValidationProfile = func(
				_ context.Context, gotDescriptor deploy.ImageDescriptor, _ PreparedProbeWorkspace,
				invoked toolcatalog.ValidationProfileRecordV1, runtime *providers.PortableToolRuntimeProjectionV1,
			) (PortableToolProbeEvidenceV1, error) {
				observed := portableToolStubEvidenceV1(t, gotDescriptor, invoked, runtime, PortableToolProbeOutcomePassV1, 1)
				test.mutate(&observed)
				return observed, nil
			}
			if evidence, err := RunPortableToolValidationScheduleV1(context.Background(), descriptor, workspace, schedule); err == nil ||
				!strings.Contains(err.Error(), test.want) || evidence != nil {
				t.Fatalf("evidence = %#v, error = %v", evidence, err)
			}
		})
	}
}

func TestValidatePortableToolMaterializationV1RejectsWorkspaceCleanupFailure(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{}})
	input := portableToolMaterializationTestInputV1(
		t, inspectedValidationCandidate(t, descriptor), portableToolTestScheduleV1(t, profile),
	)
	cleanupErr := errors.New("probe workspace cleanup failed")
	previousWorkspace := preparePortableToolValidationWorkspace
	t.Cleanup(func() { preparePortableToolValidationWorkspace = previousWorkspace })
	preparePortableToolValidationWorkspace = func(_ context.Context, _ providerstore.Store, _ blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		return testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir()), func() error { return cleanupErr }, nil
	}
	previousRun := runScheduledPortableToolValidationProfile
	t.Cleanup(func() { runScheduledPortableToolValidationProfile = previousRun })
	runScheduledPortableToolValidationProfile = func(
		_ context.Context, gotDescriptor deploy.ImageDescriptor, _ PreparedProbeWorkspace,
		invoked toolcatalog.ValidationProfileRecordV1, runtime *providers.PortableToolRuntimeProjectionV1,
	) (PortableToolProbeEvidenceV1, error) {
		return portableToolStubEvidenceV1(t, gotDescriptor, invoked, runtime, PortableToolProbeOutcomePassV1, 1), nil
	}
	evidence, err := ValidatePortableToolMaterializationV1(context.Background(), providerstore.Store{}, input)
	if !errors.Is(err, cleanupErr) || evidence != nil {
		t.Fatalf("evidence = %#v, error = %v", evidence, err)
	}
}
