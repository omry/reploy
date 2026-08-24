package toolcatalog

import (
	"fmt"
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
		{name: "revision pin that matches no release", request: ToolRequestV1{Name: "demo", Context: "build",
			Version: "1.2.3", Revision: "77"}, wantSub: "no release matching"},
		{name: "revision pin without an exact version", request: ToolRequestV1{Name: "demo", Context: "build", Revision: "1"},
			wantSub: "requires an exact upstream version"},
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

// A request may carry a version constraint, not only an exact coordinate.
// Restricting enumeration to exact equality would make a range request select
// nothing at all.
// requestedVersionForTestV1 classifies a constraint against a tool advertising
// exactly the release under test, which is what the enumerator does before it
// asks whether that release satisfies the request.
func requestedVersionForTestV1(upstream string, aliases []string, constraint string) requestedVersionV1 {
	return classifyRequestedVersionV1([]releaseEntryV1{
		{manifest: &ReleaseManifestV1{Version: upstream, Aliases: aliases}}}, constraint)
}

func TestReleaseSatisfiesConstraintV1(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		scheme     string
		upstream   string
		aliases    []string
		constraint string
		want       bool
	}{
		{name: "empty constraint accepts", scheme: "semver", upstream: "1.2.3", constraint: "", want: true},
		{name: "exact coordinate", scheme: "semver", upstream: "1.2.3", constraint: "1.2.3", want: true},
		{name: "alias", scheme: "semver", upstream: "1.2.3", aliases: []string{"1.2"}, constraint: "1.2", want: true},
		{name: "range includes", scheme: "semver", upstream: "1.55.2", constraint: ">=1.55.0,<1.56.0", want: true},
		{name: "range excludes", scheme: "semver", upstream: "1.56.0", constraint: ">=1.55.0,<1.56.0", want: false},
		{name: "lower bound excludes", scheme: "semver", upstream: "1.54.9", constraint: ">=1.55.0", want: false},
		{name: "pep440 range", scheme: "pep440", upstream: "1.61.0", constraint: ">=1.60,<2.0", want: true},
		{name: "unrelated exact", scheme: "semver", upstream: "1.2.3", constraint: "9.9.9", want: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := releaseSatisfiesConstraintV1(testCase.scheme, testCase.upstream, testCase.aliases,
				requestedVersionForTestV1(testCase.upstream, testCase.aliases, testCase.constraint))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != testCase.want {
				t.Errorf("releaseSatisfiesConstraintV1(%q, %q) = %v, want %v",
					testCase.constraint, testCase.upstream, got, testCase.want)
			}
		})
	}
	// An opaque scheme has no ordering, so a comparison constraint cannot be
	// evaluated against it and must fail rather than silently matching nothing.
	if _, err := releaseSatisfiesConstraintV1("opaque", "vetted", nil, requestedVersionForTestV1("vetted", nil, ">=1.0.0")); err == nil {
		t.Error("a comparison constraint was evaluated against an opaque scheme")
	}
	if got, err := releaseSatisfiesConstraintV1("opaque", "vetted", nil, requestedVersionForTestV1("vetted", nil, "vetted")); err != nil || !got {
		t.Errorf("an exact opaque coordinate did not match: %v, %v", got, err)
	}
	if _, err := releaseSatisfiesConstraintV1("semver", "1.2.3", nil, requestedVersionForTestV1("1.2.3", nil, ">=not-a-version")); err == nil {
		t.Error("a malformed constraint was accepted")
	}
}

func TestSelectReleaseCandidatesAcceptsAVersionConstraintV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	candidates, err := catalog.SelectReleaseCandidatesV1(
		ToolRequestV1{Name: "demo", Context: "build", Version: ">=1.0.0,<2.0.0"},
		candidateTestObservedV1(), candidateTestClientV1())
	if err != nil {
		t.Fatalf("a range request selected nothing: %v", err)
	}
	if len(candidates) == 0 {
		t.Error("no candidate matched the range")
	}
	if _, err := catalog.SelectReleaseCandidatesV1(
		ToolRequestV1{Name: "demo", Context: "build", Version: ">=9.0.0"},
		candidateTestObservedV1(), candidateTestClientV1()); err == nil {
		t.Error("a range matching no release produced candidates")
	}
}

// A contract may require a binding, declare no default, and leave the choice to
// the target. Where the target advertises exactly one declared option there is
// nothing to choose, so an omitted binding infers it.
func TestResolveRequestedBindingInfersTheSoleAdvertisedOptionV1(t *testing.T) {
	request := BindingRequestV1{Options: []string{"node", "python"}, Required: true}
	single := &TargetRecordV1{Bindings: []TargetBindingV1{{Name: "python"}}}
	got, err := resolveRequestedBindingV1(request, single, "")
	if err != nil || got != "python" {
		t.Errorf("sole advertised binding not inferred: %q, %v", got, err)
	}
	both := &TargetRecordV1{Bindings: []TargetBindingV1{{Name: "node"}, {Name: "python"}}}
	if _, err := resolveRequestedBindingV1(request, both, ""); err == nil {
		t.Error("an ambiguous omitted binding was inferred rather than refused")
	}
	none := &TargetRecordV1{}
	if _, err := resolveRequestedBindingV1(request, none, ""); err == nil {
		t.Error("a required binding was resolved with nothing advertised")
	}
}

// An absent client version proves nothing about compatibility, so it is invalid
// rather than a reason to skip the check.
func TestClientWithoutAReployVersionIsRejectedV1(t *testing.T) {
	contract := &ReleaseContractV1{SupportedReploy: ">=0.0.0", ResolverPrimitives: []string{"https-sha256"}}
	err := verifyClientSatisfiesContractV1(contract, ClientCapabilitiesV1{ResolverPrimitives: []string{"https-sha256"}})
	if err == nil || !strings.Contains(err.Error(), "declares no Reploy version") {
		t.Errorf("a client with no version satisfied a versioned contract: %v", err)
	}
	if err := verifyClientSatisfiesContractV1(contract, candidateTestClientV1()); err != nil {
		t.Errorf("a complete client was rejected: %v", err)
	}
}

// An equality constraint names a token in the tool's exact-version and alias
// lookup map, so it is normalized to that token rather than compared whole.
func TestEqualityConstraintsResolveThroughTheLookupMapV1(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		scheme     string
		upstream   string
		aliases    []string
		constraint string
		want       bool
	}{
		{name: "integer equality", scheme: "integer", upstream: "21", constraint: "==21", want: true},
		{name: "integer equality mismatch", scheme: "integer", upstream: "17", constraint: "==21", want: false},
		{name: "opaque equality", scheme: "opaque", upstream: "vetted", constraint: "==vetted", want: true},
		{name: "opaque equality mismatch", scheme: "opaque", upstream: "other", constraint: "==vetted", want: false},
		{name: "equality naming an alias", scheme: "semver", upstream: "1.2.3",
			aliases: []string{"1.2"}, constraint: "==1.2", want: true},
		{name: "semver equality", scheme: "semver", upstream: "1.2.3", constraint: "==1.2.3", want: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := releaseSatisfiesConstraintV1(testCase.scheme, testCase.upstream, testCase.aliases,
				requestedVersionForTestV1(testCase.upstream, testCase.aliases, testCase.constraint))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != testCase.want {
				t.Errorf("releaseSatisfiesConstraintV1(%q, %q) = %v, want %v",
					testCase.constraint, testCase.upstream, got, testCase.want)
			}
		})
	}
	// An equality constraint never reaches the ordering path, so an opaque
	// scheme answers it rather than refusing it.
	if _, err := releaseSatisfiesConstraintV1("opaque", "vetted", nil, requestedVersionForTestV1("vetted", nil, "==other")); err != nil {
		t.Errorf("an opaque equality constraint was refused as an ordering constraint: %v", err)
	}
	// An equality operator naming nothing matches nothing. Reading it as an
	// absent constraint would widen the request to every release the tool has.
	empty, tool := enumerationTestCatalogV1(t, "semver", []enumerationReleaseV1{
		{Version: "1.0.0", Revision: "1"}, {Version: "2.0.0", Revision: "1"}})
	ordered, err := empty.enumerateReleaseCandidatesV1(tool, "==", "")
	if err != nil {
		t.Fatalf("a bare equality operator failed enumeration: %v", err)
	}
	if len(ordered) != 0 {
		t.Errorf("a bare equality operator selected %d releases, want none", len(ordered))
	}
	// A genuine ordering constraint against opaque is still refused.
	if _, err := releaseSatisfiesConstraintV1("opaque", "vetted", nil, requestedVersionForTestV1("vetted", nil, ">=1.0.0")); err == nil {
		t.Error("an ordering constraint against opaque was accepted")
	}
}

// enumerationReleaseV1 is one release an enumeration test wants the tool to
// advertise. Enumeration reads only the version, aliases and revision, so a
// hand-built catalog holding manifests with nothing else is the whole input.
type enumerationReleaseV1 struct {
	Version  string
	Aliases  []string
	Revision string
}

func enumerationTestCatalogV1(t *testing.T, scheme string,
	releases []enumerationReleaseV1) (*CatalogV1, *ToolRecordV1) {
	t.Helper()
	catalog := &CatalogV1{records: map[recordKeyV1]loadedRecordV1{}, tools: map[string]recordKeyV1{}}
	tool := &ToolRecordV1{Schema: ToolRecordSchemaV1, ID: "tool:demo", Name: "demo", VersionScheme: scheme}
	for index, release := range releases {
		id := fmt.Sprintf("tool:demo/releases/%s/revisions/%s/manifest", release.Version, release.Revision)
		manifest := &ReleaseManifestV1{Schema: ReleaseManifestSchemaV1, ID: id, Tool: "demo",
			Version: release.Version, Aliases: release.Aliases, Revision: release.Revision}
		digest := canonical.Digest(fmt.Sprintf("sha256:%064d", index+1))
		key := recordKeyV1{ID: id, Digest: digest}
		catalog.records[key] = loadedRecordV1{ID: id, Schema: manifest.Schema, Digest: digest, Value: manifest}
		tool.Releases = append(tool.Releases, RecordReferenceV1{ID: id, Digest: digest})
	}
	return catalog, tool
}

func enumeratedVersionsV1(t *testing.T, catalog *CatalogV1, ordered []recordKeyV1) []string {
	t.Helper()
	versions := make([]string, 0, len(ordered))
	for _, key := range ordered {
		versions = append(versions, catalog.records[key].Value.(*ReleaseManifestV1).Version)
	}
	return versions
}

// An unconstrained ordered-scheme request selects a stable version. A
// prerelease sorts ahead of every stable release under its own scheme, so
// including one in unconstrained enumeration would hand an ordinary request a
// prerelease as its newest candidate.
func TestUnconstrainedEnumerationExcludesPrereleasesV1(t *testing.T) {
	catalog, tool := enumerationTestCatalogV1(t, "semver", []enumerationReleaseV1{
		{Version: "1.0.0", Revision: "1"}, {Version: "2.0.0-rc.1", Revision: "1"}})
	ordered, err := catalog.enumerateReleaseCandidatesV1(tool, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := enumeratedVersionsV1(t, catalog, ordered); len(got) != 1 || got[0] != "1.0.0" {
		t.Errorf("unconstrained SemVer enumeration = %v, want only the stable release", got)
	}
	// Naming the prerelease exactly is requesting it, which is permitted.
	ordered, err = catalog.enumerateReleaseCandidatesV1(tool, "2.0.0-rc.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := enumeratedVersionsV1(t, catalog, ordered); len(got) != 1 || got[0] != "2.0.0-rc.1" {
		t.Errorf("an explicitly requested prerelease = %v, want it selected", got)
	}
	catalog, tool = enumerationTestCatalogV1(t, "pep440", []enumerationReleaseV1{
		{Version: "1.0", Revision: "1"}, {Version: "2.0rc1", Revision: "1"}})
	ordered, err = catalog.enumerateReleaseCandidatesV1(tool, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := enumeratedVersionsV1(t, catalog, ordered); len(got) != 1 || got[0] != "1.0" {
		t.Errorf("unconstrained PEP 440 enumeration = %v, want only the stable release", got)
	}
}

// Whether a request token is an exact coordinate or a comparison expression is
// a property of the tool, not of the release the token is tested against. A
// per-release operator heuristic reads a coordinate carrying scheme-native
// punctuation as an expression, and then fails against every release that is
// not the one being named.
func TestExactCoordinatesResolveThroughTheToolWideLookupV1(t *testing.T) {
	catalog, tool := enumerationTestCatalogV1(t, "opaque", []enumerationReleaseV1{
		{Version: "vetted~2024", Revision: "1"}, {Version: "vetted~2023", Revision: "1"}})
	ordered, err := catalog.enumerateReleaseCandidatesV1(tool, "vetted~2024", "")
	if err != nil {
		t.Fatalf("an exact opaque coordinate was read as an ordering constraint: %v", err)
	}
	if got := enumeratedVersionsV1(t, catalog, ordered); len(got) != 1 || got[0] != "vetted~2024" {
		t.Errorf("exact opaque enumeration = %v, want the named coordinate", got)
	}
	catalog, tool = enumerationTestCatalogV1(t, "pep440", []enumerationReleaseV1{
		{Version: "1!2.0", Revision: "1"}, {Version: "1.5", Revision: "1"}})
	ordered, err = catalog.enumerateReleaseCandidatesV1(tool, "1!2.0", "")
	if err != nil {
		t.Fatalf("an exact PEP 440 epoch coordinate was read as an ordering constraint: %v", err)
	}
	if got := enumeratedVersionsV1(t, catalog, ordered); len(got) != 1 || got[0] != "1!2.0" {
		t.Errorf("exact PEP 440 epoch enumeration = %v, want the named coordinate", got)
	}
	// A token the tool advertises nowhere is still an ordering expression, and
	// an opaque scheme has no ordering to evaluate it against.
	if _, err := catalog.enumerateReleaseCandidatesV1(tool, ">=1.0", ""); err != nil {
		t.Fatalf("a PEP 440 range was refused: %v", err)
	}
	opaque, opaqueTool := enumerationTestCatalogV1(t, "opaque", []enumerationReleaseV1{
		{Version: "vetted", Revision: "1"}})
	if _, err := opaque.enumerateReleaseCandidatesV1(opaqueTool, ">=1.0", ""); err == nil {
		t.Error("an ordering constraint was evaluated against an opaque scheme")
	}
}

// A revision corrects one exact upstream version. Applying a revision pin
// across a range would select that revision from whichever version happened to
// sort first, which is not what the pin names.
func TestRevisionPinsRequireAnExactVersionV1(t *testing.T) {
	catalog, tool := enumerationTestCatalogV1(t, "semver", []enumerationReleaseV1{
		{Version: "1.0.0", Revision: "1"}, {Version: "1.0.0", Revision: "2"}, {Version: "2.0.0", Revision: "2"}})
	if _, err := catalog.enumerateReleaseCandidatesV1(tool, ">=1.0.0,<3.0.0", "2"); err == nil {
		t.Error("a revision pin was accepted alongside a ranged version")
	}
	if _, err := catalog.enumerateReleaseCandidatesV1(tool, "", "2"); err == nil {
		t.Error("a revision pin was accepted with no upstream version")
	}
	ordered, err := catalog.enumerateReleaseCandidatesV1(tool, "1.0.0", "2")
	if err != nil {
		t.Fatalf("an exact version with a revision pin was refused: %v", err)
	}
	if len(ordered) != 1 || !strings.Contains(ordered[0].ID, "/1.0.0/revisions/2/") {
		t.Errorf("exact-version revision pin = %+v, want one pinned release", ordered)
	}
}

// The integer scheme is ordered but is not SemVer: a bare decimal such as "21"
// is not a semantic version, so evaluating an integer range through a SemVer
// constraint evaluator rejects every release the range should have matched.
func TestIntegerSchemeEvaluatesOrderingConstraintsV1(t *testing.T) {
	catalog, tool := enumerationTestCatalogV1(t, "integer", []enumerationReleaseV1{
		{Version: "8", Revision: "1"}, {Version: "17", Revision: "1"}, {Version: "21", Revision: "1"}})
	ordered, err := catalog.enumerateReleaseCandidatesV1(tool, ">=17", "")
	if err != nil {
		t.Fatalf("an integer range was refused: %v", err)
	}
	if got := enumeratedVersionsV1(t, catalog, ordered); len(got) != 2 || got[0] != "21" || got[1] != "17" {
		t.Errorf("integer range >=17 = %v, want 21 then 17", got)
	}
	ordered, err = catalog.enumerateReleaseCandidatesV1(tool, ">=9,<21", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := enumeratedVersionsV1(t, catalog, ordered); len(got) != 1 || got[0] != "17" {
		t.Errorf("integer range >=9,<21 = %v, want only 17", got)
	}
	if _, err := catalog.enumerateReleaseCandidatesV1(tool, ">=lts", ""); err == nil {
		t.Error("a non-numeric integer constraint was accepted")
	}
}

// Enumeration reads the tool's whole release index before it filters, so
// malformed release data is reported rather than skipped, and a coordinate that
// is not valid under its own scheme matches no ordering constraint rather than
// failing the request.
func TestEnumerationRejectsMalformedReleaseDataV1(t *testing.T) {
	catalog, tool := enumerationTestCatalogV1(t, "semver", []enumerationReleaseV1{{Version: "1.0.0", Revision: "1"}})
	tool.Releases = append(tool.Releases, RecordReferenceV1{ID: "tool:demo/releases/2.0.0/revisions/1/manifest",
		Digest: canonical.Digest("sha256:" + strings.Repeat("f", 64))})
	if _, err := catalog.enumerateReleaseCandidatesV1(tool, "", ""); err == nil ||
		!strings.Contains(err.Error(), "is not in the catalog") {
		t.Errorf("a release the catalog does not hold = %v, want a missing-record error", err)
	}

	catalog, tool = enumerationTestCatalogV1(t, "semver", []enumerationReleaseV1{{Version: "1.0.0", Revision: "1"}})
	key := recordKeyV1{ID: tool.Releases[0].ID, Digest: tool.Releases[0].Digest}
	loaded := catalog.records[key]
	loaded.Value = &ToolRecordV1{Schema: ToolRecordSchemaV1, ID: "tool:demo", Name: "demo"}
	catalog.records[key] = loaded
	if _, err := catalog.enumerateReleaseCandidatesV1(tool, "", ""); err == nil ||
		!strings.Contains(err.Error(), "is not a manifest") {
		t.Errorf("a release reference resolving to a tool record = %v, want a schema error", err)
	}

	catalog, tool = enumerationTestCatalogV1(t, "semver", []enumerationReleaseV1{{Version: "1.0.0", Revision: "one"}})
	if _, err := catalog.enumerateReleaseCandidatesV1(tool, "", ""); err == nil ||
		!strings.Contains(err.Error(), "non-numeric revision") {
		t.Errorf("a non-numeric revision = %v, want a revision error", err)
	}

	catalog, tool = enumerationTestCatalogV1(t, "semver", []enumerationReleaseV1{{Version: "not-semver", Revision: "1"}})
	if _, err := catalog.enumerateReleaseCandidatesV1(tool, "", ""); err == nil ||
		!strings.Contains(err.Error(), "not valid SemVer") {
		t.Errorf("an unconstrained request against a non-SemVer coordinate = %v, want a version error", err)
	}
	catalog, tool = enumerationTestCatalogV1(t, "pep440", []enumerationReleaseV1{{Version: "not a version", Revision: "1"}})
	if _, err := catalog.enumerateReleaseCandidatesV1(tool, "", ""); err == nil ||
		!strings.Contains(err.Error(), "not valid PEP 440") {
		t.Errorf("an unconstrained request against a non-PEP-440 coordinate = %v, want a version error", err)
	}

	catalog, tool = enumerationTestCatalogV1(t, "integer", []enumerationReleaseV1{
		{Version: "lts", Revision: "1"}, {Version: "21", Revision: "1"}})
	ordered, err := catalog.enumerateReleaseCandidatesV1(tool, ">=17", "")
	if err != nil {
		t.Fatalf("a non-numeric integer coordinate failed the request: %v", err)
	}
	if got := enumeratedVersionsV1(t, catalog, ordered); len(got) != 1 || got[0] != "21" {
		t.Errorf("integer range against a non-numeric coordinate = %v, want only 21", got)
	}
}

// The integer scheme evaluates each comparison operator itself, and an integer
// coordinate is never a prerelease, so an unconstrained integer request selects
// every release rather than none.
func TestIntegerOrderingCoversEveryOperatorV1(t *testing.T) {
	catalog, tool := enumerationTestCatalogV1(t, "integer", []enumerationReleaseV1{
		{Version: "8", Revision: "1"}, {Version: "17", Revision: "1"}, {Version: "21", Revision: "1"}})
	for _, testCase := range []struct {
		constraint string
		want       []string
	}{
		{constraint: "", want: []string{"21", "17", "8"}},
		{constraint: "<=17", want: []string{"17", "8"}},
		{constraint: ">17", want: []string{"21"}},
		{constraint: "!=17", want: []string{"21", "8"}},
		{constraint: ">8,<=17,!=21", want: []string{"17"}},
	} {
		t.Run("constraint "+testCase.constraint, func(t *testing.T) {
			ordered, err := catalog.enumerateReleaseCandidatesV1(tool, testCase.constraint, "")
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(enumeratedVersionsV1(t, catalog, ordered), ","); got != strings.Join(testCase.want, ",") {
				t.Errorf("integer constraint %q = %v, want %v", testCase.constraint, got, testCase.want)
			}
		})
	}
}

// A coordinate that its own scheme cannot parse satisfies no ordering
// constraint. It is excluded rather than failing the request, so one malformed
// release cannot deny a request that another release answers.
func TestUnparseableCoordinatesSatisfyNoOrderingConstraintV1(t *testing.T) {
	catalog, tool := enumerationTestCatalogV1(t, "pep440", []enumerationReleaseV1{
		{Version: "not a version", Revision: "1"}, {Version: "2.0", Revision: "1"}})
	ordered, err := catalog.enumerateReleaseCandidatesV1(tool, ">=1.0", "")
	if err != nil {
		t.Fatalf("a malformed PEP 440 coordinate failed the request: %v", err)
	}
	if got := enumeratedVersionsV1(t, catalog, ordered); len(got) != 1 || got[0] != "2.0" {
		t.Errorf("PEP 440 range against a malformed coordinate = %v, want only 2.0", got)
	}
	if _, err := catalog.enumerateReleaseCandidatesV1(tool, ">=not a version", ""); err == nil ||
		!strings.Contains(err.Error(), "invalid under PEP 440") {
		t.Errorf("a malformed PEP 440 constraint = %v, want a constraint error", err)
	}

	catalog, tool = enumerationTestCatalogV1(t, "semver", []enumerationReleaseV1{
		{Version: "not-semver", Revision: "1"}, {Version: "2.0.0", Revision: "1"}})
	ordered, err = catalog.enumerateReleaseCandidatesV1(tool, ">=1.0.0", "")
	if err != nil {
		t.Fatalf("a malformed SemVer coordinate failed the request: %v", err)
	}
	if got := enumeratedVersionsV1(t, catalog, ordered); len(got) != 1 || got[0] != "2.0.0" {
		t.Errorf("SemVer range against a malformed coordinate = %v, want only 2.0.0", got)
	}
}

// Selection's duplicate-leaf check is a defence: the release graph walker
// rejects a manifest whose leaves collide before any catalog loads, so this
// state has to be built to be reached. Where it is reached, the request fails as
// invalid definition data rather than the candidate being removed and the
// request answered from whichever release is well formed.
func TestDuplicateTargetLeavesFailAsInvalidDefinitionDataV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	var manifest *ReleaseManifestV1
	for _, record := range catalog.records {
		if value, ok := record.Value.(*ReleaseManifestV1); ok {
			manifest = value
		}
	}
	if manifest == nil || len(manifest.Targets) != 1 {
		t.Fatalf("fixture manifest = %+v, want exactly one target leaf", manifest)
	}
	manifest.Targets = append(manifest.Targets, manifest.Targets[0])
	_, err := catalog.SelectReleaseCandidatesV1(ToolRequestV1{Name: "demo", Context: "build"},
		candidateTestObservedV1(), candidateTestClientV1())
	if err == nil || !strings.Contains(err.Error(), "invalid definition data") {
		t.Errorf("colliding target leaves = %v, want an invalid definition failure", err)
	}
	if err != nil && strings.Contains(err.Error(), "no candidate satisfying the request") {
		t.Error("colliding target leaves were reported as an unsatisfiable request")
	}
}

// An opaque request has one exact coordinate, so its definition revisions are
// what enumeration orders. Every eligible revision is retained rather than
// reduced to the newest, so joint solving can still reach an older one when the
// newest conflicts.
func TestOpaqueRequestOrdersAndRetainsEveryRevisionV1(t *testing.T) {
	catalog, tool := enumerationTestCatalogV1(t, "opaque", []enumerationReleaseV1{
		{Version: "vetted", Revision: "1"}, {Version: "vetted", Revision: "3"},
		{Version: "vetted", Revision: "2"}})
	tool.DefaultVersion = "vetted"
	normalized, err := normalizeRequestedVersionV1(tool, "")
	if err != nil {
		t.Fatal(err)
	}
	ordered, err := catalog.enumerateReleaseCandidatesV1(tool, normalized, "")
	if err != nil {
		t.Fatal(err)
	}
	revisions := make([]string, 0, len(ordered))
	for _, key := range ordered {
		revisions = append(revisions, catalog.records[key].Value.(*ReleaseManifestV1).Revision)
	}
	if got := strings.Join(revisions, ","); got != "3,2,1" {
		t.Errorf("opaque revision enumeration = %q, want every revision newest first", got)
	}
}
