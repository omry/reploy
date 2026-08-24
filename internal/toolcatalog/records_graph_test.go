package toolcatalog

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

// graphTestRecordsV1 builds a resolvable release graph: the shared valid record
// values, the unconditional payload the sample target references, and one
// source mapping for its reachable content digest.
func graphTestRecordsV1(extra ...any) (map[string]loadedRecordV1, *ReleaseManifestV1) {
	const release = "tool:demo/releases/1.2.3"
	records := composeTestRecordsV1(extra...)
	manifest := *(validRecordValuesV1()[1].(*ReleaseManifestV1))

	payload := *(validRecordValuesV1()[6].(*PayloadRecordV1))
	payload.ID = release + "/payloads/demo-linux-amd64"
	payload.Name = "demo"
	payload.LogicalPath = "tools/demo/demo.tar.gz"
	payload.InstallDirectory = "demo"
	payload.ArchiveRoot = "."
	payload.Executables = []string{"demo/bin/demo"}
	payload.SHA256 = recordTestDigest
	payload.Size = "42"
	records[payload.ID] = loadedRecordV1{ID: payload.ID, Schema: payload.Schema, Digest: recordTestDigest, Value: &payload}

	source := *(validRecordValuesV1()[7].(*ArtifactSourceRecordV1))
	source.SHA256 = recordTestDigest
	records[source.ID] = loadedRecordV1{ID: source.ID, Schema: source.Schema, Digest: recordTestDigest, Value: &source}

	manifest.ArtifactSources = []ArtifactSourceMappingV1{{
		ArtifactSHA256: recordTestDigest,
		Artifact:       recordTestReference(payload.ID),
		Source:         recordTestReference(source.ID),
	}}
	records[manifest.ID] = loadedRecordV1{ID: manifest.ID, Schema: manifest.Schema, Digest: recordTestDigest, Value: &manifest}
	return records, &manifest
}

func TestManifestResolvedGraphAcceptsAClosedGraphV1(t *testing.T) {
	records, manifest := graphTestRecordsV1()
	if err := validateManifestResolvedGraphV1(manifest, records); err != nil {
		t.Fatalf("a closed release graph was rejected: %v", err)
	}
}

// The carried finding from retired PR 83: a content digest fixes the bytes, so
// two reachable records claiming one digest must agree on size. The source
// mapping names only one record in the content group.
func TestReachableArtifactsSharingADigestMustAgreeOnSizeV1(t *testing.T) {
	const release = "tool:demo/releases/1.2.3"
	records, manifest := graphTestRecordsV1()

	twin := *(records[release+"/payloads/demo-linux-amd64"].Value.(*PayloadRecordV1))
	twin.ID = release + "/payloads/twin-linux-amd64"
	twin.Name = "twin"
	twin.LogicalPath = "tools/demo/twin.tar.gz"
	twin.InstallDirectory = "twin"
	twin.Executables = []string{"twin/bin/twin"}
	twin.Size = "9999"
	records[twin.ID] = loadedRecordV1{ID: twin.ID, Schema: twin.Schema, Digest: recordTestDigest, Value: &twin}

	target := *(records[release+"/targets/debian/12/amd64"].Value.(*TargetRecordV1))
	target.Payloads = append(target.Payloads, recordTestReference(twin.ID))
	records[target.ID] = loadedRecordV1{ID: target.ID, Schema: target.Schema, Digest: recordTestDigest, Value: &target}

	err := validateManifestResolvedGraphV1(manifest, records)
	if err == nil || !strings.Contains(err.Error(), "disagree on size") {
		t.Errorf("error = %v, want a size disagreement across one content digest", err)
	}

	twin.Size = "42"
	if err := validateManifestResolvedGraphV1(manifest, records); err != nil {
		t.Errorf("records agreeing on size were rejected: %v", err)
	}
}

// Payload records are reusable values. The dimension/value contribution edge,
// not a payload-owned selection field, determines when a payload participates.
func TestManifestGraphAllowsPayloadReuseAcrossSelectionsV1(t *testing.T) {
	const release = "tool:demo/releases/1.2.3"
	records, manifest := graphTestRecordsV1()
	payload := recordTestReference(release + "/payloads/demo-linux-amd64")
	profile := recordTestReference(release + "/validation/profiles/default")

	contract := *(records[release+"/contract"].Value.(*ReleaseContractV1))
	contract.Selections = SelectionSchemaV1{
		Dimensions: []SelectionDimensionV1{{Name: "browser", Options: []string{"chromium", "firefox"}}},
		Combinations: []SelectionCombinationV1{
			{"browser": {"chromium"}},
			{"browser": {"firefox"}},
		},
	}
	records[contract.ID] = loadedRecordV1{ID: contract.ID, Schema: contract.Schema, Digest: recordTestDigest, Value: &contract}

	target := *(records[release+"/targets/debian/12/amd64"].Value.(*TargetRecordV1))
	target.Payloads = []RecordReferenceV1{}
	target.Selections = []TargetSelectionV1{
		{Dimension: "browser", Value: "chromium", Payloads: []RecordReferenceV1{payload}, PackageSets: []RecordReferenceV1{}, Exports: []ToolExportV1{}, ValidationProfiles: []RecordReferenceV1{}},
		{Dimension: "browser", Value: "firefox", Payloads: []RecordReferenceV1{payload}, PackageSets: []RecordReferenceV1{}, Exports: []ToolExportV1{}, ValidationProfiles: []RecordReferenceV1{}},
	}
	target.SupportCases = []TargetSupportCaseV1{
		{Context: "build", Bindings: []string{}, Selections: map[string][]string{"browser": {"chromium"}}},
		{Context: "build", Bindings: []string{}, Selections: map[string][]string{"browser": {"firefox"}}},
	}
	first := *(records[release+"/validation/fixtures/debian-12-amd64"].Value.(*IntegrationFixtureRecordV1))
	first.Selections = map[string][]string{"browser": {"chromium"}}
	second := first
	second.ID = release + "/validation/fixtures/debian-12-amd64-firefox"
	second.Name = "debian-12-amd64-firefox"
	second.Selections = map[string][]string{"browser": {"firefox"}}
	records[first.ID] = loadedRecordV1{ID: first.ID, Schema: first.Schema, Digest: recordTestDigest, Value: &first}
	records[second.ID] = loadedRecordV1{ID: second.ID, Schema: second.Schema, Digest: recordTestDigest, Value: &second}
	target.IntegrationFixtures = []RecordReferenceV1{recordTestReference(first.ID), recordTestReference(second.ID)}
	target.ValidationProfiles = []RecordReferenceV1{profile}
	records[target.ID] = loadedRecordV1{ID: target.ID, Schema: target.Schema, Digest: recordTestDigest, Value: &target}

	if err := validateManifestResolvedGraphV1(manifest, records); err != nil {
		t.Fatalf("a payload reused through two selection contributions was rejected: %v", err)
	}
}

// The routed discovery from PR 94 round 1: the walker must reach every
// composition validator, not only per-artifact checks.
func TestManifestGraphReachesSetLevelInterpreterCoverageV1(t *testing.T) {
	const release = "tool:demo/releases/1.2.3"
	records, manifest := graphTestRecordsV1()

	bindingContract := *(records[release+"/bindings/python/contract"].Value.(*BindingContractV1))
	bindingContract.SupportedPython = append([]string{"3.10"}, bindingContract.SupportedPython...)
	records[bindingContract.ID] = loadedRecordV1{ID: bindingContract.ID, Schema: bindingContract.Schema, Digest: recordTestDigest, Value: &bindingContract}

	target := *(records[release+"/targets/debian/12/amd64"].Value.(*TargetRecordV1))
	target.Bindings = targetBindingWithArtifactV1(release + "/bindings/python/artifacts/linux-amd64")
	target.SupportCases[0].Bindings = []string{"python"}
	records[target.ID] = loadedRecordV1{ID: target.ID, Schema: target.Schema, Digest: recordTestDigest, Value: &target}

	contract := *(records[release+"/contract"].Value.(*ReleaseContractV1))
	contract.Binding = BindingSetSchemaV1{Options: []string{"python"}}
	records[contract.ID] = loadedRecordV1{ID: contract.ID, Schema: contract.Schema, Digest: recordTestDigest, Value: &contract}

	err := validateManifestResolvedGraphV1(manifest, records)
	if err == nil || !strings.Contains(err.Error(), "no artifact covering interpreter") {
		t.Errorf("error = %v, want the set-level interpreter coverage rejection", err)
	}
}

func TestManifestResolvedGraphRejectsBrokenGraphsV1(t *testing.T) {
	const release = "tool:demo/releases/1.2.3"
	for _, testCase := range []struct {
		name    string
		mutate  func(map[string]loadedRecordV1, *ReleaseManifestV1)
		wantSub string
	}{
		{name: "missing contract", wantSub: "release contract", mutate: func(r map[string]loadedRecordV1, _ *ReleaseManifestV1) {
			delete(r, release+"/contract")
		}},
		{name: "missing target", wantSub: "release target", mutate: func(r map[string]loadedRecordV1, _ *ReleaseManifestV1) {
			delete(r, release+"/targets/debian/12/amd64")
		}},
		{name: "duplicate target identity", wantSub: "describe the same target", mutate: func(r map[string]loadedRecordV1, m *ReleaseManifestV1) {
			twin := *(r[release+"/targets/debian/12/amd64"].Value.(*TargetRecordV1))
			twin.ID = release + "/targets/ubuntu/24.04/amd64"
			r[twin.ID] = loadedRecordV1{ID: twin.ID, Schema: twin.Schema, Digest: recordTestDigest, Value: &twin}
			m.Targets = append(m.Targets, recordTestReference(twin.ID))
		}},
		{name: "reachable artifact without a source mapping", wantSub: "has no source mapping", mutate: func(_ map[string]loadedRecordV1, m *ReleaseManifestV1) {
			m.ArtifactSources = []ArtifactSourceMappingV1{}
		}},
		{name: "source mapping digest disagreement", wantSub: "content digests disagree", mutate: func(r map[string]loadedRecordV1, _ *ReleaseManifestV1) {
			payload := r[release+"/payloads/demo-linux-amd64"].Value.(*PayloadRecordV1)
			payload.SHA256 = canonical.Digest("sha256:" + strings.Repeat("b", 64))
		}},
		{name: "missing release validation profile", wantSub: "release validation profile", mutate: func(r map[string]loadedRecordV1, _ *ReleaseManifestV1) {
			delete(r, release+"/validation/profiles/default")
		}},
		{name: "incompatible release validation profile", wantSub: "incompatible with the release", mutate: func(r map[string]loadedRecordV1, _ *ReleaseManifestV1) {
			profile := r[release+"/validation/profiles/default"].Value.(*ValidationProfileRecordV1)
			profile.Tool = "other"
		}},
		{name: "target profile not declared by manifest", wantSub: "not declared exactly", mutate: func(r map[string]loadedRecordV1, _ *ReleaseManifestV1) {
			profile := *(r[release+"/validation/profiles/default"].Value.(*ValidationProfileRecordV1))
			profile.ID = release + "/validation/profiles/other"
			r[profile.ID] = loadedRecordV1{ID: profile.ID, Schema: profile.Schema, Digest: recordTestDigest, Value: &profile}
			target := r[release+"/targets/debian/12/amd64"].Value.(*TargetRecordV1)
			target.ValidationProfiles = []RecordReferenceV1{recordTestReference(profile.ID)}
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			records, manifest := graphTestRecordsV1()
			testCase.mutate(records, manifest)
			err := validateManifestResolvedGraphV1(manifest, records)
			if err == nil || !strings.Contains(err.Error(), testCase.wantSub) {
				t.Errorf("error = %v, want substring %q", err, testCase.wantSub)
			}
		})
	}
}

func TestToolReleaseIndexResolvesEveryManifestV1(t *testing.T) {
	records, manifest := graphTestRecordsV1()
	tool := *(validRecordValuesV1()[0].(*ToolRecordV1))
	tool.Releases = []RecordReferenceV1{recordTestReference(manifest.ID)}

	if err := validateToolReleaseIndexV1(&tool, records); err != nil {
		t.Errorf("a resolvable release index was rejected: %v", err)
	}
	if err := validateToolReleaseIndexV1(nil, records); err == nil {
		t.Error("a nil tool record produced a valid index")
	}
	empty := tool
	empty.Releases = []RecordReferenceV1{}
	if err := validateToolReleaseIndexV1(&empty, records); err == nil {
		t.Error("an empty release list produced a valid index")
	}
	missing := tool
	missing.Releases = []RecordReferenceV1{recordTestReference("tool:demo/releases/9.9.9/revisions/1/manifest")}
	if err := validateToolReleaseIndexV1(&missing, records); err == nil {
		t.Error("an unresolvable manifest reference produced a valid index")
	}
	mistyped := tool
	mistyped.Releases = []RecordReferenceV1{recordTestReference("tool:demo/releases/1.2.3/contract")}
	err := validateToolReleaseIndexV1(&mistyped, records)
	if err == nil || !strings.Contains(err.Error(), "non-manifest record") {
		t.Errorf("release contract accepted as a manifest: %v", err)
	}
}

func TestValidationEvidenceCannotCreateUnsupportedClaimsV1(t *testing.T) {
	if err := validateValidationEvidenceV1(*validValidationEvidenceV1()); err != nil {
		t.Fatalf("valid evidence rejected: %v", err)
	}
	for _, testCase := range []struct {
		name   string
		mutate func(*ValidationEvidenceV1)
	}{
		{name: "fixture outside the validation namespace", mutate: func(e *ValidationEvidenceV1) {
			e.Fixture = "tool:demo/releases/1.2.3/targets/debian/12/amd64"
		}},
		{name: "unsorted selection values", mutate: func(e *ValidationEvidenceV1) {
			e.Selections = map[string][]string{"browser": {"webkit", "chromium"}}
		}},
		{name: "unsupported context", mutate: func(e *ValidationEvidenceV1) { e.Context = "publish" }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			evidence := *validValidationEvidenceV1()
			testCase.mutate(&evidence)
			if err := validateValidationEvidenceV1(evidence); err == nil {
				t.Error("evidence accepted an unsupported claim")
			}
		})
	}
}

// Exact-version and alias collisions across a tool's manifests must fail: one
// coordinate cannot resolve to two different releases.
func TestToolReleaseManifestsRejectCoordinateCollisionsV1(t *testing.T) {
	tool := func() *ToolRecordV1 { value := *(validRecordValuesV1()[0].(*ToolRecordV1)); return &value }
	manifest := func(version string, aliases ...string) *ReleaseManifestV1 {
		value := *(validRecordValuesV1()[1].(*ReleaseManifestV1))
		value.Version, value.Aliases = version, append([]string{}, aliases...)
		return &value
	}

	if err := validateToolReleaseManifestsV1(tool(), []*ReleaseManifestV1{manifest("1.2.3", "1.2"), manifest("2.0.0", "2.0")}); err != nil {
		t.Errorf("distinct versions and aliases rejected: %v", err)
	}
	err := validateToolReleaseManifestsV1(tool(), []*ReleaseManifestV1{manifest("1.2.3", "1.2"), manifest("1.2.9", "1.2")})
	if err == nil || !strings.Contains(err.Error(), "maps to both") {
		t.Errorf("alias collision error = %v", err)
	}
	err = validateToolReleaseManifestsV1(tool(), []*ReleaseManifestV1{manifest("1.2.3"), manifest("2.0.0", "1.2.3")})
	if err == nil || !strings.Contains(err.Error(), "maps to both") {
		t.Errorf("alias shadowing an exact version error = %v", err)
	}
	if err := validateToolReleaseManifestsV1(tool(), []*ReleaseManifestV1{manifest("1.2.3", "1.2"), manifest("1.2.3", "1.2")}); err != nil {
		t.Errorf("one version repeating its own alias was treated as a collision: %v", err)
	}

	foreign := manifest("1.2.3")
	foreign.Tool = "other"
	if err := validateToolReleaseManifestsV1(tool(), []*ReleaseManifestV1{foreign}); err == nil {
		t.Error("a manifest belonging to another tool was accepted")
	}
	if err := validateToolReleaseManifestsV1(tool(), []*ReleaseManifestV1{nil}); err == nil {
		t.Error("a nil manifest was accepted")
	}
	if err := validateToolReleaseManifestsV1(tool(), []*ReleaseManifestV1{manifest("banana")}); err == nil {
		t.Error("a version violating the tool version scheme was accepted")
	}
	if err := validateToolReleaseManifestsV1(tool(), []*ReleaseManifestV1{manifest("1.2.3", "1.2.3.4")}); err == nil {
		t.Error("an alias violating the tool version scheme was accepted")
	}
	if err := validateToolReleaseManifestsV1(nil, []*ReleaseManifestV1{manifest("1.2.3")}); err == nil {
		t.Error("a nil tool record was accepted")
	}
	pep440Tool := tool()
	pep440Tool.VersionScheme = "pep440"
	if err := validateToolReleaseManifestsV1(pep440Tool, []*ReleaseManifestV1{manifest("1.0", "1.0RC1")}); err == nil {
		t.Error("a non-canonical PEP 440 alias was accepted")
	}

	opaque := tool()
	opaque.VersionScheme, opaque.DefaultVersion = "opaque", "vetted"
	if err := validateToolReleaseManifestsV1(opaque, []*ReleaseManifestV1{manifest("vetted")}); err != nil {
		t.Errorf("an advertised opaque default was rejected: %v", err)
	}
	err = validateToolReleaseManifestsV1(opaque, []*ReleaseManifestV1{manifest("other")})
	if err == nil || !strings.Contains(err.Error(), "not an advertised exact release") {
		t.Errorf("unadvertised opaque default error = %v", err)
	}
}

func TestValidationEvidenceIdentityAndBoundsV1(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*ValidationEvidenceV1)
	}{
		{name: "wrong schema", mutate: func(e *ValidationEvidenceV1) { e.Schema = "other" }},
		{name: "noncanonical tool", mutate: func(e *ValidationEvidenceV1) { e.Tool = "Demo" }},
		{name: "revision not a positive decimal", mutate: func(e *ValidationEvidenceV1) { e.Revision = "0" }},
		{name: "malformed manifest digest", mutate: func(e *ValidationEvidenceV1) { e.ManifestDigest = canonical.Digest("sha256:zz") }},
		{name: "malformed selected closure digest", mutate: func(e *ValidationEvidenceV1) { e.SelectedClosureDigest = canonical.Digest("nope") }},
		{name: "inconsistent target identity", mutate: func(e *ValidationEvidenceV1) { e.Target.Platform = "linux/arm64" }},
		{name: "noncanonical binding", mutate: func(e *ValidationEvidenceV1) { e.Bindings = []string{"Python"} }},
		{name: "malformed validator output digest", mutate: func(e *ValidationEvidenceV1) { e.ValidatorOutputDigest = canonical.Digest("nope") }},
		{name: "nil selections", mutate: func(e *ValidationEvidenceV1) { e.Selections = nil }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			evidence := *validValidationEvidenceV1()
			testCase.mutate(&evidence)
			if err := validateValidationEvidenceV1(evidence); err == nil {
				t.Error("invalid validation evidence was accepted")
			}
		})
	}
}

func TestManifestResolvedGraphRejectsNilManifestV1(t *testing.T) {
	if err := validateManifestResolvedGraphV1(nil, nil); err == nil {
		t.Error("a nil release manifest was accepted")
	}
}

// References are exact: ID and digest together. An artifact naming its binding
// contract ID at a different digest is a dangling reference.
func TestBindingArtifactContractReferenceMustBeExactV1(t *testing.T) {
	const release = "tool:demo/releases/1.2.3"
	records, manifest := graphTestRecordsV1()

	artifact := *(records[release+"/bindings/python/artifacts/linux-amd64"].Value.(*BindingArtifactRecordV1))
	artifact.SHA256 = recordTestDigest
	artifact.Size = "42"
	records[artifact.ID] = loadedRecordV1{ID: artifact.ID, Schema: artifact.Schema, Digest: recordTestDigest, Value: &artifact}

	target := *(records[release+"/targets/debian/12/amd64"].Value.(*TargetRecordV1))
	target.Bindings = targetBindingWithArtifactV1(artifact.ID)
	target.SupportCases[0].Bindings = []string{"python"}
	records[target.ID] = loadedRecordV1{ID: target.ID, Schema: target.Schema, Digest: recordTestDigest, Value: &target}
	fixture := records[release+"/validation/fixtures/debian-12-amd64"].Value.(*IntegrationFixtureRecordV1)
	fixture.Bindings = []string{"python"}

	contract := *(records[release+"/contract"].Value.(*ReleaseContractV1))
	contract.Binding = BindingSetSchemaV1{Options: []string{"python"}}
	records[contract.ID] = loadedRecordV1{ID: contract.ID, Schema: contract.Schema, Digest: recordTestDigest, Value: &contract}

	if err := validateManifestResolvedGraphV1(manifest, records); err != nil {
		t.Fatalf("an exact contract reference was rejected: %v", err)
	}
	artifact.Contract = RecordReferenceV1{ID: artifact.Contract.ID, Digest: canonical.Digest("sha256:" + strings.Repeat("c", 64))}
	err := validateManifestResolvedGraphV1(manifest, records)
	if err == nil || !strings.Contains(err.Error(), "incompatible record") {
		t.Errorf("error = %v, want a contract digest disagreement", err)
	}
}

func TestValidationEvidenceSelectionsMustBeIdentifiersV1(t *testing.T) {
	for _, value := range []string{"Chromium", "not/a-selection", "chromium.1", "1chromium"} {
		evidence := *validValidationEvidenceV1()
		evidence.Selections = map[string][]string{"browser": {value}}
		if err := validateValidationEvidenceV1(evidence); err == nil {
			t.Errorf("evidence selection %q was accepted", value)
		}
	}
	for _, dimension := range []string{"Browser", "not/a-dimension", "browser.1", "1browser"} {
		evidence := *validValidationEvidenceV1()
		evidence.Selections = map[string][]string{dimension: {"chromium"}}
		if err := validateValidationEvidenceV1(evidence); err == nil {
			t.Errorf("evidence selection dimension %q was accepted", dimension)
		}
	}
	evidence := *validValidationEvidenceV1()
	evidence.Selections = map[string][]string{"browser": {"chromium", "firefox"}}
	if err := validateValidationEvidenceV1(evidence); err != nil {
		t.Errorf("canonical evidence selections rejected: %v", err)
	}
}
