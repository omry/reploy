package toolcatalog

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

// graphTestRecordsV1 builds a resolvable release graph: the shared valid record
// values, the payload the sample target references, and an artifact source
// mapping for every reachable artifact so the graph closes.
func graphTestRecordsV1(extra ...any) (map[string]loadedRecordV1, *ReleaseManifestV1) {
	const release = "tool:demo/releases/1.2.3"
	records := composeTestRecordsV1(extra...)
	manifest := *(validRecordValuesV1()[1].(*ReleaseManifestV1))

	payload := records[release+"/payloads/demo-linux-amd64"].Value.(*PayloadRecordV1)
	payload.SHA256 = recordTestDigest
	payload.Size = "42"

	source := *(validRecordValuesV1()[7].(*ArtifactSourceRecordV1))
	source.SHA256 = recordTestDigest
	source.Size = "42"
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
// mapping only size-checks the record it names, leaving the other free.
func TestReachableArtifactsSharingADigestMustAgreeOnSizeV1(t *testing.T) {
	const release = "tool:demo/releases/1.2.3"
	records, manifest := graphTestRecordsV1()

	// A second payload, reachable through a selection, claiming the same digest
	// at a different size. Its own record-local validation is satisfied.
	twin := &PayloadRecordV1{Schema: PayloadRecordSchemaV1,
		ID: release + "/payloads/chromium/twin-linux-amd64", Selection: "chromium", Name: "twin",
		Revision: "1", UpstreamVersion: "1", Platform: "linux/amd64",
		LogicalPath: "tools/demo/twin.tar.gz", Kind: "archive",
		Size: "9999", SHA256: recordTestDigest, Resolver: "https-sha256",
		Entries: "1", UnpackedSize: "9999", InstallDirectory: "twin", ArchiveRoot: ".", Executable: "twin/bin/twin"}
	records[twin.ID] = loadedRecordV1{ID: twin.ID, Schema: twin.Schema, Digest: recordTestDigest, Value: twin}

	target := *(records[release+"/targets/debian/12/amd64"].Value.(*TargetRecordV1))
	target.Selections = []TargetSelectionV1{{Name: "chromium",
		Payloads:    []RecordReferenceV1{recordTestReference(twin.ID)},
		PackageSets: []RecordReferenceV1{}, Exports: []ToolExportV1{}, Probes: []RecordProbeV1{}}}
	records[target.ID] = loadedRecordV1{ID: target.ID, Schema: target.Schema, Digest: recordTestDigest, Value: &target}

	contract := *(records[release+"/contract"].Value.(*ReleaseContractV1))
	contract.Selections = SelectionRequestV1{Options: []string{"chromium"}, Minimum: "1", Maximum: "1",
		Defaults: []string{"chromium"}, CompatibilityGroups: [][]string{{"chromium"}}}
	records[contract.ID] = loadedRecordV1{ID: contract.ID, Schema: contract.Schema, Digest: recordTestDigest, Value: &contract}

	err := validateManifestResolvedGraphV1(manifest, records)
	if err == nil || !strings.Contains(err.Error(), "disagree on size") {
		t.Errorf("error = %v, want a size disagreement across one content digest", err)
	}

	// Agreeing on size removes that objection.
	twin.Size, twin.UnpackedSize = "42", "42"
	if err := validateManifestResolvedGraphV1(manifest, records); err != nil &&
		strings.Contains(err.Error(), "disagree on size") {
		t.Errorf("records agreeing on size still reported a size disagreement: %v", err)
	}
}

// The routed discovery from PR 94 round 1: the walker must reach every
// composition validator, not only the per-artifact contract check.
func TestManifestGraphReachesSetLevelInterpreterCoverageV1(t *testing.T) {
	const release = "tool:demo/releases/1.2.3"
	records, manifest := graphTestRecordsV1()

	// The binding contract advertises an interpreter its only wheel cannot
	// serve. Each artifact alone still satisfies the per-artifact check.
	contract := *(records[release+"/bindings/python/contract"].Value.(*BindingContractV1))
	contract.SupportedPython = append([]string{"3.10"}, contract.SupportedPython...)
	records[contract.ID] = loadedRecordV1{ID: contract.ID, Schema: contract.Schema, Digest: recordTestDigest, Value: &contract}

	target := *(records[release+"/targets/debian/12/amd64"].Value.(*TargetRecordV1))
	target.Bindings = targetBindingWithArtifactV1(release + "/bindings/python/artifacts/linux-amd64")
	records[target.ID] = loadedRecordV1{ID: target.ID, Schema: target.Schema, Digest: recordTestDigest, Value: &target}

	releaseContract := *(records[release+"/contract"].Value.(*ReleaseContractV1))
	releaseContract.Binding = BindingRequestV1{Options: []string{"python"}, Required: true, Default: "python"}
	records[releaseContract.ID] = loadedRecordV1{ID: releaseContract.ID, Schema: releaseContract.Schema,
		Digest: recordTestDigest, Value: &releaseContract}

	err := validateManifestResolvedGraphV1(manifest, records)
	if err == nil || !strings.Contains(err.Error(), "no selected artifact supports it") {
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
		{name: "missing contract", wantSub: "release contract",
			mutate: func(r map[string]loadedRecordV1, m *ReleaseManifestV1) { delete(r, release+"/contract") }},
		{name: "missing target", wantSub: "release target",
			mutate: func(r map[string]loadedRecordV1, m *ReleaseManifestV1) {
				delete(r, release+"/targets/debian/12/amd64")
			}},
		{name: "duplicate target identity", wantSub: "describe the same target",
			mutate: func(r map[string]loadedRecordV1, m *ReleaseManifestV1) {
				twin := *(r[release+"/targets/debian/12/amd64"].Value.(*TargetRecordV1))
				twin.ID = release + "/targets/ubuntu/24.04/amd64"
				r[twin.ID] = loadedRecordV1{ID: twin.ID, Schema: twin.Schema, Digest: recordTestDigest, Value: &twin}
				m.Targets = append(m.Targets, recordTestReference(twin.ID))
			}},
		{name: "reachable artifact without a source mapping", wantSub: "has no source mapping",
			mutate: func(r map[string]loadedRecordV1, m *ReleaseManifestV1) {
				m.ArtifactSources = []ArtifactSourceMappingV1{}
			}},
		{name: "source mapping digest disagreement", wantSub: "content digests disagree",
			mutate: func(r map[string]loadedRecordV1, m *ReleaseManifestV1) {
				payload := r[release+"/payloads/demo-linux-amd64"].Value.(*PayloadRecordV1)
				payload.SHA256 = canonical.Digest("sha256:" + strings.Repeat("b", 64))
			}},
		{name: "target using another validation profile", wantSub: "release validation profile",
			mutate: func(r map[string]loadedRecordV1, m *ReleaseManifestV1) {
				m.ValidationProfile = recordTestReference(release + "/validation/profiles/other")
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
	valid := *validValidationEvidenceV1()
	if err := validateValidationEvidenceV1(valid); err != nil {
		t.Fatalf("valid evidence rejected: %v", err)
	}
	for _, testCase := range []struct {
		name   string
		mutate func(*ValidationEvidenceV1)
	}{
		{name: "fixture outside the validation namespace",
			mutate: func(e *ValidationEvidenceV1) { e.Fixture = "tool:demo/releases/1.2.3/targets/debian/12/amd64" }},
		{name: "unsorted selections",
			mutate: func(e *ValidationEvidenceV1) { e.Selections = []string{"zeta", "alpha"} }},
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

	if err := validateToolReleaseManifestsV1(tool(),
		[]*ReleaseManifestV1{manifest("1.2.3", "1.2"), manifest("2.0.0", "2.0")}); err != nil {
		t.Errorf("distinct versions and aliases rejected: %v", err)
	}

	// An alias claimed by two different exact versions is a collision. The alias
	// itself is valid under the tool's scheme; the collision is the defect.
	err := validateToolReleaseManifestsV1(tool(),
		[]*ReleaseManifestV1{manifest("1.2.3", "1.2"), manifest("1.2.9", "1.2")})
	if err == nil || !strings.Contains(err.Error(), "maps to both") {
		t.Errorf("alias collision error = %v", err)
	}
	// An alias that shadows another release's exact version is the same defect.
	err = validateToolReleaseManifestsV1(tool(),
		[]*ReleaseManifestV1{manifest("1.2.3"), manifest("2.0.0", "1.2.3")})
	if err == nil || !strings.Contains(err.Error(), "maps to both") {
		t.Errorf("alias shadowing an exact version error = %v", err)
	}
	// Repeating an alias within one manifest's own version is not a collision.
	if err := validateToolReleaseManifestsV1(tool(),
		[]*ReleaseManifestV1{manifest("1.2.3", "1.2"), manifest("1.2.3", "1.2")}); err != nil {
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

	// An opaque tool's default must name an advertised exact release.
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
		{name: "malformed manifest digest",
			mutate: func(e *ValidationEvidenceV1) { e.ManifestDigest = canonical.Digest("sha256:zz") }},
		{name: "malformed selected closure digest",
			mutate: func(e *ValidationEvidenceV1) { e.SelectedClosureDigest = canonical.Digest("nope") }},
		{name: "inconsistent target identity",
			mutate: func(e *ValidationEvidenceV1) { e.Target.Platform = "linux/arm64" }},
		{name: "noncanonical binding", mutate: func(e *ValidationEvidenceV1) { e.Binding = "Python" }},
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
