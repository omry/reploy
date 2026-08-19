package toolcatalog

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func candidateTestClientV1() ClientCapabilitiesV1 {
	return ClientCapabilitiesV1{ReployVersion: "1.0.0", ResolverPrimitives: []string{"https-sha256"}}
}

func candidateTestObservedV1() TargetIdentityV1 {
	return TargetIdentityV1{Platform: "linux/amd64", OSReleaseID: "debian", VersionID: "12",
		OCIArchitecture: "amd64", NativeArchitecture: "amd64", PackageManager: "apt"}
}

func candidateTestCatalogV1(t *testing.T) *CatalogV1 {
	t.Helper()
	catalog, err := loadCatalogV1(catalogTestFilesV1(t), "catalog")
	if err != nil {
		t.Fatalf("loading the candidate fixture catalog: %v", err)
	}
	return catalog
}

func TestSelectReleaseCandidatesAcceptsASatisfiableRequestV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	candidates, err := catalog.SelectReleaseCandidatesV1(
		ToolRequestV1{Name: "demo", Context: "build"}, candidateTestObservedV1(), candidateTestClientV1())
	if err != nil {
		t.Fatalf("a satisfiable request produced no candidate: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("no candidates returned")
	}
	if len(candidates[0].Contributions) == 0 {
		t.Error("candidate carries no contribution union")
	}
	// Every record in a candidate is cloned, so mutating one cannot reach the
	// catalog the candidate came from.
	candidates[0].Target.ID = "mutated"
	again, err := catalog.SelectReleaseCandidatesV1(
		ToolRequestV1{Name: "demo", Context: "build"}, candidateTestObservedV1(), candidateTestClientV1())
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Target.ID == "mutated" {
		t.Error("a returned candidate aliases loaded catalog state")
	}
}

func TestSelectReleaseCandidatesRejectsUnusableRequestsV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	observed := candidateTestObservedV1()
	client := candidateTestClientV1()
	for _, testCase := range []struct {
		name    string
		request ToolRequestV1
		wantSub string
	}{
		{name: "unknown tool", request: ToolRequestV1{Name: "absent", Context: "build"},
			wantSub: "is not defined"},
		{name: "invalid tool name", request: ToolRequestV1{Name: "Demo", Context: "build"},
			wantSub: "is invalid"},
		{name: "invalid revision", request: ToolRequestV1{Name: "demo", Context: "build", Revision: "0"},
			wantSub: "revision"},
		{name: "unsupported context", request: ToolRequestV1{Name: "demo", Context: "runtime"},
			wantSub: "context"},
		{name: "version that matches no release", request: ToolRequestV1{Name: "demo", Context: "build", Version: "9.9.9"},
			wantSub: "no release matching"},
		{name: "revision pin that matches no release", request: ToolRequestV1{Name: "demo", Context: "build", Revision: "77"},
			wantSub: "no release matching"},
		{name: "undeclared parameter", request: ToolRequestV1{Name: "demo", Context: "build",
			Parameters: []ParameterValueV1{{Name: "absent", Value: "x"}}}, wantSub: "not declared"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := catalog.SelectReleaseCandidatesV1(testCase.request, observed, client); err == nil ||
				!strings.Contains(err.Error(), testCase.wantSub) {
				t.Errorf("error = %v, want substring %q", err, testCase.wantSub)
			}
		})
	}
}

func TestSelectReleaseCandidatesRequiresAMatchingTargetV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	other := candidateTestObservedV1()
	other.OSReleaseID = "ubuntu"
	_, err := catalog.SelectReleaseCandidatesV1(
		ToolRequestV1{Name: "demo", Context: "build"}, other, candidateTestClientV1())
	if err == nil || !strings.Contains(err.Error(), "no target leaf matches") {
		t.Errorf("error = %v, want a no-matching-target rejection", err)
	}
}

// A client that cannot satisfy the newest release must fall back to an older
// one rather than failing the request outright.
func TestClientIncompatibleNewestCandidateFallsBackV1(t *testing.T) {
	contract := &ReleaseContractV1{SupportedReploy: ">=2.0.0", ResolverPrimitives: []string{"https-sha256"}}
	if err := verifyClientSatisfiesContractV1(contract, candidateTestClientV1()); err == nil {
		t.Error("a client below the supported range satisfied the contract")
	}
	contract.SupportedReploy = ">=0.0.0"
	if err := verifyClientSatisfiesContractV1(contract, candidateTestClientV1()); err != nil {
		t.Errorf("a satisfied contract was rejected: %v", err)
	}
	contract.ResolverPrimitives = []string{"https-sha256", "ipfs"}
	err := verifyClientSatisfiesContractV1(contract, candidateTestClientV1())
	if err == nil || !strings.Contains(err.Error(), "resolver primitive") {
		t.Errorf("missing resolver primitive error = %v", err)
	}
}

// A versionless opaque request resolves to the tool record's default_version
// before enumeration, so enumeration never sees an absent coordinate.
func TestOpaqueRequestNormalizesToTheDefaultVersionV1(t *testing.T) {
	opaque := &ToolRecordV1{Name: "demo", VersionScheme: "opaque", DefaultVersion: "vetted"}
	got, err := normalizeRequestedVersionV1(opaque, "")
	if err != nil || got != "vetted" {
		t.Errorf("normalizeRequestedVersionV1 = %q, %v, want vetted", got, err)
	}
	if got, err := normalizeRequestedVersionV1(opaque, "other"); err != nil || got != "other" {
		t.Errorf("an explicit opaque version was rewritten: %q, %v", got, err)
	}
	if _, err := normalizeRequestedVersionV1(&ToolRecordV1{Name: "demo", VersionScheme: "opaque"}, ""); err == nil {
		t.Error("an opaque tool without a default version normalized")
	}
	ordered := &ToolRecordV1{Name: "demo", VersionScheme: "semver"}
	if got, err := normalizeRequestedVersionV1(ordered, ""); err != nil || got != "" {
		t.Errorf("an ordered scheme was given a default: %q, %v", got, err)
	}
}

func TestCompareToolVersionsV1(t *testing.T) {
	if compareToolVersionsV1("semver", "2.0.0", "1.9.9") <= 0 {
		t.Error("semver ordering is not descending-capable")
	}
	if compareToolVersionsV1("integer", "21", "8") <= 0 {
		t.Error("integer ordering compares as strings")
	}
	if compareToolVersionsV1("opaque", "b", "a") != 0 {
		t.Error("an opaque scheme claimed an ordering")
	}
}

func TestMatchesRequestedVersionV1(t *testing.T) {
	if !matchesRequestedVersionV1("1.2.3", []string{"1.2"}, "1.2.3") {
		t.Error("exact version did not match")
	}
	if !matchesRequestedVersionV1("1.2.3", []string{"1.2"}, "1.2") {
		t.Error("alias did not match")
	}
	if matchesRequestedVersionV1("1.2.3", []string{"1.2"}, "1.3") {
		t.Error("unrelated coordinate matched")
	}
}

func TestResolveRequestedSelectionsEnforcesTheContractV1(t *testing.T) {
	request := SelectionRequestV1{Options: []string{"chromium", "firefox"}, Minimum: "1", Maximum: "1",
		Defaults: []string{"chromium"}, CompatibilityGroups: [][]string{{"chromium", "firefox"}}}
	target := &TargetRecordV1{Selections: []TargetSelectionV1{{Name: "chromium"}, {Name: "firefox"}}}

	got, err := resolveRequestedSelectionsV1(request, target, nil)
	if err != nil || strings.Join(got, ",") != "chromium" {
		t.Errorf("omitted selections did not fall back to the default: %v, %v", got, err)
	}
	if _, err := resolveRequestedSelectionsV1(request, target, []string{"chromium", "firefox"}); err == nil {
		t.Error("more selections than the maximum were accepted")
	}
	if _, err := resolveRequestedSelectionsV1(request, target, []string{}); err == nil {
		t.Error("fewer selections than the minimum were accepted")
	}
	if _, err := resolveRequestedSelectionsV1(request, target, []string{"webkit"}); err == nil {
		t.Error("an undeclared selection was accepted")
	}
	if _, err := resolveRequestedSelectionsV1(request, target, []string{"chromium", "chromium"}); err == nil {
		t.Error("a duplicated selection was accepted")
	}
	unadvertised := &TargetRecordV1{Selections: []TargetSelectionV1{{Name: "firefox"}}}
	if _, err := resolveRequestedSelectionsV1(request, unadvertised, []string{"chromium"}); err == nil {
		t.Error("a selection the target does not advertise was accepted")
	}
}

func TestResolveRequestedBindingV1(t *testing.T) {
	request := BindingRequestV1{Options: []string{"python"}, Required: true, Default: "python"}
	target := &TargetRecordV1{Bindings: []TargetBindingV1{{Name: "python"}}}
	if got, err := resolveRequestedBindingV1(request, target, ""); err != nil || got != "python" {
		t.Errorf("binding inference = %q, %v", got, err)
	}
	if _, err := resolveRequestedBindingV1(request, target, "node"); err == nil {
		t.Error("an undeclared binding was accepted")
	}
	if _, err := resolveRequestedBindingV1(request, &TargetRecordV1{}, "python"); err == nil {
		t.Error("a binding the target does not advertise was accepted")
	}
	optional := BindingRequestV1{Options: []string{}, Required: false}
	if got, err := resolveRequestedBindingV1(optional, &TargetRecordV1{}, ""); err != nil || got != "" {
		t.Errorf("an optional absent binding = %q, %v", got, err)
	}
	if _, err := resolveRequestedBindingV1(BindingRequestV1{Required: true}, &TargetRecordV1{}, ""); err == nil {
		t.Error("a required binding with no default and no request was accepted")
	}
}

func TestResolveRequestedParametersFillsDeclaredDefaultsV1(t *testing.T) {
	value := "stable"
	contract := &ReleaseContractV1{Parameters: []ParameterSchemaV1{
		{Name: "channel", Type: "enum", Values: []string{"beta", "stable"}, Default: &value}}}
	target := &TargetRecordV1{}
	got, err := resolveRequestedParametersV1(contract, target, nil)
	if err != nil || len(got) != 1 || got[0].Value != "stable" {
		t.Errorf("declared default not filled: %+v, %v", got, err)
	}
	if _, err := resolveRequestedParametersV1(contract, target,
		[]ParameterValueV1{{Name: "channel", Value: "nightly"}}); err == nil {
		t.Error("a value outside the contract domain was accepted")
	}
	narrowed := &TargetRecordV1{Parameters: []TargetParameterConstraintV1{{Name: "channel", Values: []string{"stable"}}}}
	if _, err := resolveRequestedParametersV1(contract, narrowed,
		[]ParameterValueV1{{Name: "channel", Value: "beta"}}); err == nil {
		t.Error("a value outside the target narrowing was accepted")
	}
	required := &ReleaseContractV1{Parameters: []ParameterSchemaV1{
		{Name: "channel", Type: "enum", Values: []string{"stable"}, Required: true}}}
	if _, err := resolveRequestedParametersV1(required, target, nil); err == nil {
		t.Error("a missing required parameter was accepted")
	}
	if _, err := resolveRequestedParametersV1(contract, target,
		[]ParameterValueV1{{Name: "channel", Value: "stable"}, {Name: "channel", Value: "beta"}}); err == nil {
		t.Error("a parameter supplied twice was accepted")
	}
}

func TestCanonicalReferenceUnionIsOrderIndependentV1(t *testing.T) {
	a := RecordReferenceV1{ID: "tool:demo/a", Digest: recordTestDigest}
	b := RecordReferenceV1{ID: "tool:demo/b", Digest: recordTestDigest}
	left := canonicalReferenceUnionV1([]RecordReferenceV1{a, b, a})
	right := canonicalReferenceUnionV1([]RecordReferenceV1{b, a, b})
	if len(left) != 2 || len(right) != 2 {
		t.Fatalf("union did not deduplicate: %d, %d", len(left), len(right))
	}
	for index := range left {
		if left[index] != right[index] {
			t.Errorf("union depends on gathering order: %+v vs %+v", left, right)
		}
	}
}

// Enumeration is newest-first, and a revision pin restricts it rather than
// being overridden by that ordering.
func TestEnumerateReleaseCandidatesOrdersAndPinsV1(t *testing.T) {
	const release = "tool:demo/releases/1.2.3"
	catalog := &CatalogV1{records: map[recordKeyV1]loadedRecordV1{}, tools: map[string]recordKeyV1{}}
	tool := &ToolRecordV1{Schema: ToolRecordSchemaV1, ID: "tool:demo", Name: "demo", VersionScheme: "semver"}
	for _, revision := range []string{"1", "2", "3"} {
		manifest := &ReleaseManifestV1{Schema: ReleaseManifestSchemaV1,
			ID: release + "/revisions/" + revision + "/manifest", Tool: "demo", Version: "1.2.3", Revision: revision}
		digest := canonical.Digest("sha256:" + strings.Repeat(revision, 64))
		key := recordKeyV1{ID: manifest.ID, Digest: digest}
		catalog.records[key] = loadedRecordV1{ID: manifest.ID, Schema: manifest.Schema, Digest: digest, Value: manifest}
		tool.Releases = append(tool.Releases, RecordReferenceV1{ID: manifest.ID, Digest: digest})
	}
	ordered, err := catalog.enumerateReleaseCandidatesV1(tool, "1.2.3", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ordered) != 3 || !strings.Contains(ordered[0].ID, "revisions/3/") {
		t.Errorf("enumeration is not newest-revision-first: %+v", ordered)
	}
	pinned, err := catalog.enumerateReleaseCandidatesV1(tool, "1.2.3", "2")
	if err != nil {
		t.Fatal(err)
	}
	if len(pinned) != 1 || !strings.Contains(pinned[0].ID, "revisions/2/") {
		t.Errorf("a revision pin did not restrict enumeration: %+v", pinned)
	}
}

func TestSelectExactTargetRejectsAmbiguityV1(t *testing.T) {
	observed := candidateTestObservedV1()
	first := &TargetRecordV1{Schema: TargetRecordSchemaV1, ID: "tool:demo/releases/1.2.3/targets/debian/12/amd64", Target: observed}
	second := &TargetRecordV1{Schema: TargetRecordSchemaV1, ID: "tool:demo/releases/1.2.3/targets/ubuntu/24.04/amd64", Target: observed}
	view := map[string]loadedRecordV1{
		first.ID:  {ID: first.ID, Schema: first.Schema, Digest: recordTestDigest, Value: first},
		second.ID: {ID: second.ID, Schema: second.Schema, Digest: recordTestDigest, Value: second},
	}
	manifest := &ReleaseManifestV1{Targets: []RecordReferenceV1{
		recordTestReference(first.ID), recordTestReference(second.ID)}}
	_, err := selectExactTargetV1(view, manifest, observed)
	if err == nil || !strings.Contains(err.Error(), "both match") {
		t.Errorf("two matching target leaves error = %v, want invalid definition data", err)
	}
	single := &ReleaseManifestV1{Targets: []RecordReferenceV1{recordTestReference(first.ID)}}
	if _, err := selectExactTargetV1(view, single, observed); err != nil {
		t.Errorf("a single matching target was rejected: %v", err)
	}
}

// A candidate's contribution union takes unconditional target contributions plus
// only the entries for the resolved binding and normalized selections, and
// nothing from selections that were not chosen.
func TestCandidateContributionsIncludeOnlySelectedEntriesV1(t *testing.T) {
	const release = "tool:demo/releases/1.2.3"
	ref := func(id string) RecordReferenceV1 { return recordTestReference(id) }
	target := &TargetRecordV1{Schema: TargetRecordSchemaV1, ID: release + "/targets/debian/12/amd64",
		PackageSets: []RecordReferenceV1{ref(release + "/package-sets/base")},
		Payloads:    []RecordReferenceV1{ref(release + "/payloads/demo-linux-amd64")},
		Bindings: []TargetBindingV1{{Name: "python",
			Contract:    ref(release + "/bindings/python/contract"),
			Artifacts:   []RecordReferenceV1{ref(release + "/bindings/python/artifacts/linux-amd64")},
			PackageSets: []RecordReferenceV1{ref(release + "/package-sets/python")}}},
		Selections: []TargetSelectionV1{
			{Name: "chromium", Payloads: []RecordReferenceV1{ref(release + "/payloads/chromium/chromium-linux-amd64")},
				PackageSets: []RecordReferenceV1{ref(release + "/package-sets/chromium")}},
			{Name: "firefox", Payloads: []RecordReferenceV1{ref(release + "/payloads/firefox/firefox-linux-amd64")}},
		}}
	view := map[string]loadedRecordV1{}
	for _, id := range []string{
		release + "/package-sets/base", release + "/payloads/demo-linux-amd64",
		release + "/bindings/python/contract", release + "/bindings/python/artifacts/linux-amd64",
		release + "/package-sets/python", release + "/payloads/chromium/chromium-linux-amd64",
		release + "/package-sets/chromium", release + "/payloads/firefox/firefox-linux-amd64",
	} {
		view[id] = loadedRecordV1{ID: id, Digest: recordTestDigest, Value: &NativePackageSetV1{ID: id}}
	}

	union, err := candidateContributionsV1(view, &ReleaseContractV1{}, target, "python", []string{"chromium"})
	if err != nil {
		t.Fatalf("building the union: %v", err)
	}
	present := map[string]bool{}
	for _, reference := range union {
		present[reference.ID] = true
	}
	for _, required := range []string{
		release + "/package-sets/base", release + "/payloads/demo-linux-amd64",
		release + "/bindings/python/contract", release + "/bindings/python/artifacts/linux-amd64",
		release + "/package-sets/python", release + "/payloads/chromium/chromium-linux-amd64",
		release + "/package-sets/chromium",
	} {
		if !present[required] {
			t.Errorf("union omitted %q", required)
		}
	}
	if present[release+"/payloads/firefox/firefox-linux-amd64"] {
		t.Error("union included an unselected selection's payload")
	}

	if _, err := candidateContributionsV1(view, &ReleaseContractV1{}, target, "node", nil); err == nil {
		t.Error("a binding the target does not provide built a union")
	}
	if _, err := candidateContributionsV1(view, &ReleaseContractV1{}, target, "", []string{"webkit"}); err == nil {
		t.Error("a selection the target does not provide built a union")
	}
	missing := map[string]loadedRecordV1{}
	if _, err := candidateContributionsV1(missing, &ReleaseContractV1{}, target, "", nil); err == nil {
		t.Error("a union resolved with no records present")
	}
}
