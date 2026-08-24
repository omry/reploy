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

func candidateTestGroupV1() CanonicalRequirementGroupV1 {
	return CanonicalRequirementGroupV1{
		Scope: "application", Tool: "demo", Context: "build", Binding: CanonicalBindingDemandV1{Infer: true},
		Selections: map[string][]string{},
	}
}

func candidateTestCatalogV1(t *testing.T) *CatalogV1 {
	t.Helper()
	catalog, err := loadCatalogV1(catalogTestFilesV1(t), "catalog")
	if err != nil {
		t.Fatalf("loading the candidate fixture catalog: %v", err)
	}
	return catalog
}

func TestSelectReleaseCandidatesAcceptsACanonicalGroupV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	candidates, err := catalog.SelectReleaseCandidatesV1(
		candidateTestGroupV1(), candidateTestObservedV1(), candidateTestClientV1(), nil)
	if err != nil {
		t.Fatalf("a satisfiable group produced no candidate: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(candidates))
	}
	candidate := candidates[0]
	if candidate.Scope != "application" || candidate.Manifest.Version != "1.2.3" {
		t.Errorf("candidate identity = %q/%q, want application/1.2.3", candidate.Scope, candidate.Manifest.Version)
	}
	if len(candidate.Contributions) == 0 || len(candidate.Profiles) == 0 {
		t.Error("candidate omitted its selected contribution or validation records")
	}
	if len(candidate.Bindings) != 0 || len(candidate.Selections) != 0 {
		t.Errorf("candidate tuple = bindings %v selections %v, want both empty", candidate.Bindings, candidate.Selections)
	}

	// Every record and collection in a candidate is detached from catalog state.
	candidates[0].Target.ID = "mutated"
	candidates[0].Profiles[0].Probes[0].Args[0] = "mutated"
	candidates[0].Selections["browser"] = []string{"mutated"}
	again, err := catalog.SelectReleaseCandidatesV1(
		candidateTestGroupV1(), candidateTestObservedV1(), candidateTestClientV1(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Target.ID == "mutated" || again[0].Profiles[0].Probes[0].Args[0] == "mutated" {
		t.Error("a returned candidate aliases loaded catalog state")
	}
	if _, found := again[0].Selections["browser"]; found {
		t.Error("a returned selection map aliases an earlier candidate")
	}
}

func TestSelectReleaseCandidatesRejectsUnusableGroupsV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	for _, testCase := range []struct {
		name    string
		mutate  func(*CanonicalRequirementGroupV1)
		wantSub string
	}{
		{name: "invalid scope", mutate: func(group *CanonicalRequirementGroupV1) { group.Scope = "" }, wantSub: "scope"},
		{name: "unknown tool", mutate: func(group *CanonicalRequirementGroupV1) { group.Tool = "absent" }, wantSub: "not defined"},
		{name: "invalid tool", mutate: func(group *CanonicalRequirementGroupV1) { group.Tool = "Demo" }, wantSub: "invalid"},
		{name: "unsorted constraints", mutate: func(group *CanonicalRequirementGroupV1) {
			group.VersionConstraints = []string{">=1.0.0", "<2.0.0"}
		}, wantSub: "unique, and sorted"},
		{name: "duplicate constraints", mutate: func(group *CanonicalRequirementGroupV1) {
			group.VersionConstraints = []string{">=1.0.0", ">=1.0.0"}
		}, wantSub: "unique, and sorted"},
		{name: "invalid revision", mutate: func(group *CanonicalRequirementGroupV1) { group.DefinitionRevision = "0" }, wantSub: "revision"},
		{name: "empty binding demand", mutate: func(group *CanonicalRequirementGroupV1) {
			group.Binding = CanonicalBindingDemandV1{}
		}, wantSub: "must retain"},
		{name: "all with inference", mutate: func(group *CanonicalRequirementGroupV1) {
			group.Binding = CanonicalBindingDemandV1{All: true, Infer: true}
		}, wantSub: "cannot carry"},
		{name: "unsorted explicit bindings", mutate: func(group *CanonicalRequirementGroupV1) {
			group.Binding = CanonicalBindingDemandV1{Explicit: []string{"python", "node"}}
		}, wantSub: "unique and sorted"},
		{name: "noncanonical selections", mutate: func(group *CanonicalRequirementGroupV1) {
			group.Selections = map[string][]string{"browser": {"firefox", "chromium"}}
		}, wantSub: "nonempty sorted set"},
		{name: "unsupported context", mutate: func(group *CanonicalRequirementGroupV1) { group.Context = "runtime" }, wantSub: "context"},
		{name: "version matches no release", mutate: func(group *CanonicalRequirementGroupV1) {
			group.VersionConstraints = []string{"9.9.9"}
		}, wantSub: "empty intersection"},
		{name: "revision without exact version", mutate: func(group *CanonicalRequirementGroupV1) {
			group.DefinitionRevision = "1"
		}, wantSub: "requires an exact upstream version"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			group := candidateTestGroupV1()
			testCase.mutate(&group)
			_, err := catalog.SelectReleaseCandidatesV1(
				group, candidateTestObservedV1(), candidateTestClientV1(), nil)
			if err == nil || !strings.Contains(err.Error(), testCase.wantSub) {
				t.Errorf("error = %v, want substring %q", err, testCase.wantSub)
			}
		})
	}
}

func TestSelectReleaseCandidatesRequiresCanonicalActiveBindingsV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	_, err := catalog.SelectReleaseCandidatesV1(candidateTestGroupV1(), candidateTestObservedV1(),
		candidateTestClientV1(), []string{"python", "node"})
	if err == nil || !strings.Contains(err.Error(), "active bindings") {
		t.Errorf("noncanonical active bindings = %v, want a canonical-set rejection", err)
	}
}

func TestSelectReleaseCandidatesRequiresAnExactTargetV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	other := candidateTestObservedV1()
	other.OSReleaseID = "ubuntu"
	_, err := catalog.SelectReleaseCandidatesV1(candidateTestGroupV1(), other, candidateTestClientV1(), nil)
	if err == nil || !strings.Contains(err.Error(), "no target leaf matches") {
		t.Errorf("error = %v, want a no-matching-target rejection", err)
	}
}

func addCandidateReleaseV1(t *testing.T, catalog *CatalogV1, version string,
	mutateContract func(*ReleaseContractV1), mutateTarget func(*TargetRecordV1)) {
	t.Helper()
	toolKey := catalog.tools["demo"]
	tool := catalog.records[toolKey].Value.(*ToolRecordV1)
	baseKey := recordKeyV1{ID: tool.Releases[0].ID, Digest: tool.Releases[0].Digest}
	baseManifest := catalog.records[baseKey].Value.(*ReleaseManifestV1)
	baseContractKey := recordKeyV1{ID: baseManifest.Contract.ID, Digest: baseManifest.Contract.Digest}
	contract := cloneReleaseContractV1(catalog.records[baseContractKey].Value.(*ReleaseContractV1))
	if mutateContract != nil {
		mutateContract(&contract)
	}
	contractDigest, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, &contract)
	if err != nil {
		t.Fatal(err)
	}
	catalog.records[recordKeyV1{ID: contract.ID, Digest: contractDigest}] = loadedRecordV1{
		ID: contract.ID, Schema: contract.Schema, Digest: contractDigest, Value: &contract,
	}

	baseTargetKey := recordKeyV1{ID: baseManifest.Targets[0].ID, Digest: baseManifest.Targets[0].Digest}
	target := cloneTargetRecordV1(catalog.records[baseTargetKey].Value.(*TargetRecordV1))
	if mutateTarget != nil {
		mutateTarget(&target)
	}
	targetDigest, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, &target)
	if err != nil {
		t.Fatal(err)
	}
	catalog.records[recordKeyV1{ID: target.ID, Digest: targetDigest}] = loadedRecordV1{
		ID: target.ID, Schema: target.Schema, Digest: targetDigest, Value: &target,
	}

	manifest := cloneReleaseManifestV1(baseManifest)
	manifest.ID = fmt.Sprintf("tool:demo/releases/%s/revisions/1/manifest", version)
	manifest.Version = version
	manifest.Aliases = nil
	manifest.Contract = RecordReferenceV1{ID: contract.ID, Digest: contractDigest}
	manifest.Targets = []RecordReferenceV1{{ID: target.ID, Digest: targetDigest}}
	manifestDigest, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, &manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestKey := recordKeyV1{ID: manifest.ID, Digest: manifestDigest}
	catalog.records[manifestKey] = loadedRecordV1{
		ID: manifest.ID, Schema: manifest.Schema, Digest: manifestDigest, Value: &manifest,
	}
	tool.Releases = append(tool.Releases, RecordReferenceV1{ID: manifest.ID, Digest: manifestDigest})
}

func TestCandidateReductionFallsBackToAnOlderCompatibleReleaseV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	addCandidateReleaseV1(t, catalog, "2.0.0", func(contract *ReleaseContractV1) {
		contract.SupportedReploy = ">=2.0.0"
	}, nil)
	candidates, err := catalog.SelectReleaseCandidatesV1(
		candidateTestGroupV1(), candidateTestObservedV1(), candidateTestClientV1(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Manifest.Version != "1.2.3" {
		t.Errorf("compatible candidates = %+v, want only the older 1.2.3 release", candidates)
	}
}

func TestCandidateReductionFallsBackFromAnUnsupportedTupleV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	addCandidateReleaseV1(t, catalog, "2.0.0", nil, func(target *TargetRecordV1) {
		target.SupportCases[0].Context = "runtime"
	})
	candidates, err := catalog.SelectReleaseCandidatesV1(
		candidateTestGroupV1(), candidateTestObservedV1(), candidateTestClientV1(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Manifest.Version != "1.2.3" {
		t.Errorf("tuple-compatible candidates = %+v, want only the older 1.2.3 release", candidates)
	}
}

func TestClientCompatibilityIsCheckedPerCandidateV1(t *testing.T) {
	contract := &ReleaseContractV1{SupportedReploy: ">=2.0.0", ResolverPrimitives: []string{"https-sha256"}}
	if err := verifyClientSatisfiesContractV1(contract, candidateTestClientV1()); err == nil {
		t.Error("a client below the supported range satisfied the contract")
	}
	contract.SupportedReploy = ">=0.0.0"
	if err := verifyClientSatisfiesContractV1(contract, candidateTestClientV1()); err != nil {
		t.Errorf("a satisfied contract was rejected: %v", err)
	}
	contract.ResolverPrimitives = []string{"https-sha256", "ipfs"}
	if err := verifyClientSatisfiesContractV1(contract, candidateTestClientV1()); err == nil ||
		!strings.Contains(err.Error(), "resolver primitive") {
		t.Errorf("missing resolver primitive error = %v", err)
	}
	contract = &ReleaseContractV1{SupportedReploy: ">=0.0.0"}
	if err := verifyClientSatisfiesContractV1(contract, ClientCapabilitiesV1{}); err == nil ||
		!strings.Contains(err.Error(), "declares no Reploy version") {
		t.Errorf("versionless client error = %v", err)
	}
}

func TestOpaqueGroupsUseTheDefaultCoordinateV1(t *testing.T) {
	opaque := &ToolRecordV1{Name: "demo", VersionScheme: "opaque", DefaultVersion: "vetted"}
	got, revision, err := normalizedVersionDemandV1(opaque, nil, "")
	if err != nil || strings.Join(got, ",") != "vetted" {
		t.Errorf("default opaque constraints = %v, %v, want [vetted]", got, err)
	}
	if revision != "" {
		t.Errorf("default opaque demand gained revision %q", revision)
	}
	got, _, err = normalizedVersionDemandV1(opaque, []string{"other"}, "")
	if err != nil || strings.Join(got, ",") != "other" {
		t.Errorf("explicit opaque constraint was rewritten: %v, %v", got, err)
	}
	if _, _, err := normalizedVersionDemandV1(&ToolRecordV1{Name: "demo", VersionScheme: "opaque"}, nil, ""); err == nil {
		t.Error("an opaque tool without a default coordinate normalized")
	}
	ordered := &ToolRecordV1{Name: "demo", VersionScheme: "semver"}
	if got, _, err := normalizedVersionDemandV1(ordered, nil, ""); err != nil || len(got) != 0 {
		t.Errorf("an ordered scheme received a default constraint: %v, %v", got, err)
	}
}

func TestCompactRevisionSuffixUsesTheLoadedVersionSchemeV1(t *testing.T) {
	for _, scheme := range []string{"semver", "pep440", "integer"} {
		catalog, tool := enumerationTestCatalogV1(t, scheme, []enumerationReleaseV1{
			{Version: "1", Revision: "1"}, {Version: "1", Revision: "2"},
		})
		ordered, err := catalog.enumerateReleaseCandidatesV1(tool, []string{"1~2"}, "")
		if err != nil || len(ordered) != 1 || !strings.Contains(ordered[0].ID, "/revisions/2/") {
			t.Errorf("%s compact suffix = %+v, %v", scheme, ordered, err)
		}
	}
	constraints, revision, err := normalizedVersionDemandV1(
		&ToolRecordV1{Name: "demo", VersionScheme: "opaque"}, []string{"vetted~2024"}, "")
	if err != nil || strings.Join(constraints, ",") != "vetted~2024" || revision != "" {
		t.Errorf("opaque exact coordinate was split: %v revision %q, %v", constraints, revision, err)
	}
	catalog, tool := enumerationTestCatalogV1(t, "semver", []enumerationReleaseV1{
		{Version: "1.0.0", Revision: "2"}, {Version: "1.0.0", Revision: "3"},
	})
	if _, err := catalog.enumerateReleaseCandidatesV1(tool, []string{"1.0.0~2"}, "3"); err == nil ||
		!strings.Contains(err.Error(), "conflicts") {
		t.Errorf("conflicting compact and structured revisions = %v", err)
	}

	catalog = candidateTestCatalogV1(t)
	group := candidateTestGroupV1()
	group.VersionConstraints = []string{"1.2.3~1"}
	candidates, err := catalog.SelectReleaseCandidatesV1(
		group, candidateTestObservedV1(), candidateTestClientV1(), nil)
	if err != nil || len(candidates) != 1 || candidates[0].Manifest.Revision != "1" {
		t.Errorf("end-to-end compact revision = %+v, %v", candidates, err)
	}
	catalog, tool = enumerationTestCatalogV1(t, "semver", []enumerationReleaseV1{
		{Version: "1.5.0", Revision: "1"}, {Version: "2.1.0", Revision: "1"},
	})
	for _, testCase := range []struct {
		constraint string
		want       string
	}{
		{constraint: ">=1.0.0,~2", want: "2.1.0"},
		{constraint: "1.5.0||~2", want: "2.1.0,1.5.0"},
	} {
		ordered, err := catalog.enumerateReleaseCandidatesV1(tool, []string{testCase.constraint}, "")
		if err != nil || strings.Join(enumeratedVersionsV1(t, catalog, ordered), ",") != testCase.want {
			t.Errorf("compound SemVer tilde constraint %q = %v, %v",
				testCase.constraint, enumeratedVersionsV1(t, catalog, ordered), err)
		}
	}
	if _, err := catalog.enumerateReleaseCandidatesV1(tool, []string{"1.5.0 ~2"}, ""); err == nil ||
		!strings.Contains(err.Error(), "empty intersection") {
		t.Errorf("space-separated SemVer conjunction was not preserved: %v", err)
	}
}

func TestCompareToolVersionsAndExactLookupV1(t *testing.T) {
	if compareToolVersionsV1("semver", "2.0.0", "1.9.9") <= 0 {
		t.Error("semver ordering is not descending-capable")
	}
	if compareToolVersionsV1("integer", "21", "8") <= 0 {
		t.Error("integer ordering compares as strings")
	}
	if compareToolVersionsV1("opaque", "b", "a") != 0 {
		t.Error("an opaque scheme claimed an ordering")
	}
	if !matchesRequestedVersionV1("1.2.3", []string{"1.2"}, "1.2.3") ||
		!matchesRequestedVersionV1("1.2.3", []string{"1.2"}, "1.2") {
		t.Error("an exact version or alias did not match")
	}
	if matchesRequestedVersionV1("1.2.3", []string{"1.2"}, "1.3") {
		t.Error("an unrelated coordinate matched")
	}
}

func TestResolveRequestedBindingsUsesPluralModesV1(t *testing.T) {
	schema := BindingSetSchemaV1{Options: []string{"node", "python"}}
	target := &TargetRecordV1{Bindings: []TargetBindingV1{{Name: "node"}, {Name: "python"}}}

	got, err := resolveRequestedBindingsV1(schema, target,
		CanonicalBindingDemandV1{Explicit: []string{"node", "python"}}, nil)
	if err != nil || strings.Join(got, ",") != "node,python" {
		t.Errorf("explicit bindings = %v, %v", got, err)
	}
	got, err = resolveRequestedBindingsV1(schema, target, CanonicalBindingDemandV1{All: true}, nil)
	if err != nil || strings.Join(got, ",") != "node,python" {
		t.Errorf("all bindings = %v, %v", got, err)
	}
	got, err = resolveRequestedBindingsV1(schema, target, CanonicalBindingDemandV1{Infer: true}, []string{"python"})
	if err != nil || strings.Join(got, ",") != "python" {
		t.Errorf("active-provider inference = %v, %v", got, err)
	}
	if _, err := resolveRequestedBindingsV1(schema, target, CanonicalBindingDemandV1{Infer: true}, nil); err == nil ||
		!strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("ambiguous omission = %v, want ambiguity", err)
	}
	single := &TargetRecordV1{Bindings: []TargetBindingV1{{Name: "python"}}}
	got, err = resolveRequestedBindingsV1(schema, single, CanonicalBindingDemandV1{Infer: true}, nil)
	if err != nil || strings.Join(got, ",") != "python" {
		t.Errorf("sole binding inference = %v, %v", got, err)
	}
	got, err = resolveRequestedBindingsV1(schema, target,
		CanonicalBindingDemandV1{Infer: true, Explicit: []string{"node"}}, []string{"python"})
	if err != nil || strings.Join(got, ",") != "node,python" {
		t.Errorf("cumulative inference plus explicit demand = %v, %v", got, err)
	}
	if _, err := resolveRequestedBindingsV1(schema, target,
		CanonicalBindingDemandV1{Explicit: []string{"ruby"}}, nil); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Errorf("undeclared binding = %v", err)
	}
}

func TestResolveRequestedSelectionsRequiresAnExactCombinationV1(t *testing.T) {
	schema := SelectionSchemaV1{
		Dimensions: []SelectionDimensionV1{
			{Name: "browser", Options: []string{"chromium", "firefox"}},
			{Name: "mode", Options: []string{"full", "headless"}},
		},
		Combinations: []SelectionCombinationV1{
			{"browser": {"chromium"}, "mode": {"full", "headless"}},
			{"browser": {"firefox"}, "mode": {"full"}},
		},
	}
	target := &TargetRecordV1{Selections: []TargetSelectionV1{
		{Dimension: "browser", Value: "chromium"}, {Dimension: "browser", Value: "firefox"},
		{Dimension: "mode", Value: "full"}, {Dimension: "mode", Value: "headless"},
	}}
	wanted := map[string][]string{"browser": {"chromium"}, "mode": {"full", "headless"}}
	got, err := resolveRequestedSelectionsV1(schema, target, wanted)
	if err != nil || strings.Join(got["mode"], ",") != "full,headless" {
		t.Errorf("exact combination = %v, %v", got, err)
	}
	got["browser"][0] = "mutated"
	if wanted["browser"][0] != "chromium" {
		t.Error("resolved selections alias the canonical group")
	}
	if _, err := resolveRequestedSelectionsV1(schema, target,
		map[string][]string{"browser": {"chromium"}}); err == nil || !strings.Contains(err.Error(), "does not equal") {
		t.Errorf("partial combination = %v", err)
	}
	missing := *target
	missing.Selections = missing.Selections[1:]
	if _, err := resolveRequestedSelectionsV1(schema, &missing, wanted); err == nil ||
		!strings.Contains(err.Error(), "not advertised") {
		t.Errorf("target-missing selection = %v", err)
	}
	if got, err := resolveRequestedSelectionsV1(SelectionSchemaV1{}, &TargetRecordV1{}, nil); err != nil || len(got) != 0 || got == nil {
		t.Errorf("empty selection schema = %v, %v", got, err)
	}
}

func TestResolveCandidateSupportTupleRequiresAnExactCaseV1(t *testing.T) {
	contract := &ReleaseContractV1{
		Binding: BindingSetSchemaV1{Options: []string{"python"}},
		Selections: SelectionSchemaV1{
			Dimensions:   []SelectionDimensionV1{{Name: "browser", Options: []string{"chromium"}}},
			Combinations: []SelectionCombinationV1{{"browser": {"chromium"}}},
		},
	}
	target := &TargetRecordV1{
		Bindings:   []TargetBindingV1{{Name: "python"}},
		Selections: []TargetSelectionV1{{Dimension: "browser", Value: "chromium"}},
		SupportCases: []TargetSupportCaseV1{{Context: "build", Bindings: []string{"python"},
			Selections: map[string][]string{"browser": {"chromium"}}}},
	}
	group := CanonicalRequirementGroupV1{Scope: "application", Tool: "demo", Context: "build",
		Binding:    CanonicalBindingDemandV1{Explicit: []string{"python"}},
		Selections: map[string][]string{"browser": {"chromium"}}}
	tuple, err := resolveCandidateSupportTupleV1(contract, target, group, nil)
	if err != nil || tuple.Context != "build" || strings.Join(tuple.Bindings, ",") != "python" {
		t.Errorf("support tuple = %+v, %v", tuple, err)
	}
	target.SupportCases[0].Context = "runtime"
	if _, err := resolveCandidateSupportTupleV1(contract, target, group, nil); err == nil ||
		!strings.Contains(err.Error(), "do not equal one target support case") {
		t.Errorf("nonexistent support tuple = %v", err)
	}
}

func TestCanonicalReferenceUnionIsOrderIndependentV1(t *testing.T) {
	a := recordTestReference("tool:demo/a")
	b := recordTestReference("tool:demo/b")
	left := canonicalReferenceUnionV1([]RecordReferenceV1{b, a, b})
	right := canonicalReferenceUnionV1([]RecordReferenceV1{a, b})
	if fmt.Sprint(left) != fmt.Sprint(right) || len(left) != 2 {
		t.Errorf("canonical unions differ: %v != %v", left, right)
	}
}

func TestCandidateContributionsUseTheExactTupleV1(t *testing.T) {
	payloadRef := recordTestReference("tool:demo/payload/base")
	chromiumRef := recordTestReference("tool:demo/payload/chromium")
	firefoxRef := recordTestReference("tool:demo/payload/firefox")
	contractRef := recordTestReference("tool:demo/bindings/python/contract")
	view := map[string]loadedRecordV1{
		payloadRef.ID:  {ID: payloadRef.ID, Digest: payloadRef.Digest, Value: &PayloadRecordV1{}},
		chromiumRef.ID: {ID: chromiumRef.ID, Digest: chromiumRef.Digest, Value: &PayloadRecordV1{}},
		firefoxRef.ID:  {ID: firefoxRef.ID, Digest: firefoxRef.Digest, Value: &PayloadRecordV1{}},
		contractRef.ID: {ID: contractRef.ID, Digest: contractRef.Digest,
			Value: &BindingContractV1{Name: "python", CLI: ToolExportV1{Name: "python-demo", Path: "/bin/python-demo"}}},
	}
	target := &TargetRecordV1{Payloads: []RecordReferenceV1{payloadRef},
		Bindings: []TargetBindingV1{{Name: "python", Contract: contractRef}},
		Selections: []TargetSelectionV1{
			{Dimension: "browser", Value: "chromium", Payloads: []RecordReferenceV1{chromiumRef}},
			{Dimension: "browser", Value: "firefox", Payloads: []RecordReferenceV1{firefoxRef}},
		}}
	contract := &ReleaseContractV1{Exports: []ToolExportV1{{Name: "demo", Path: "/bin/demo"}}}
	references, exports, err := candidateContributionsV1(view, contract, target,
		supportTupleV1{Bindings: []string{"python"}, Selections: map[string][]string{"browser": {"chromium"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 3 || len(exports) != 2 {
		t.Errorf("candidate contributions = %v / %v, want three references and two exports", references, exports)
	}
	for _, reference := range references {
		if reference == firefoxRef {
			t.Error("candidate included an unselected contribution")
		}
	}
	conflicting := *contract
	conflicting.Exports = []ToolExportV1{{Name: "python-demo", Path: "/other"}}
	if _, _, err := candidateContributionsV1(view, &conflicting, target,
		supportTupleV1{Bindings: []string{"python"}, Selections: map[string][]string{"browser": {"chromium"}}}); err == nil ||
		!strings.Contains(err.Error(), "conflict") {
		t.Errorf("conflicting exports = %v", err)
	}
	missing := map[string]loadedRecordV1{contractRef.ID: view[contractRef.ID]}
	if _, _, err := candidateContributionsV1(missing, contract, target,
		supportTupleV1{Bindings: []string{"python"}, Selections: map[string][]string{"browser": {"chromium"}}}); err == nil {
		t.Error("a missing selected record was accepted")
	}
}

func requestedVersionForTestV1(upstream string, aliases []string, constraint string) requestedVersionV1 {
	return classifyRequestedVersionV1([]releaseEntryV1{{manifest: &ReleaseManifestV1{
		Version: upstream, Aliases: aliases}}}, constraint)
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
		{name: "semver range", scheme: "semver", upstream: "1.2.3", constraint: ">=1.0.0,<2.0.0", want: true},
		{name: "semver miss", scheme: "semver", upstream: "2.0.0", constraint: "<2.0.0", want: false},
		{name: "pep440 range", scheme: "pep440", upstream: "2.4", constraint: ">=2,<3", want: true},
		{name: "integer range", scheme: "integer", upstream: "21", constraint: ">=17", want: true},
		{name: "exact alias", scheme: "semver", upstream: "1.2.3", aliases: []string{"1.2"}, constraint: "1.2", want: true},
		{name: "integer equality", scheme: "integer", upstream: "21", constraint: "==21", want: true},
		{name: "opaque equality", scheme: "opaque", upstream: "vetted", constraint: "==vetted", want: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := releaseSatisfiesConstraintV1(testCase.scheme, testCase.upstream, testCase.aliases,
				requestedVersionForTestV1(testCase.upstream, testCase.aliases, testCase.constraint))
			if err != nil || got != testCase.want {
				t.Errorf("constraint %q = %v, %v, want %v", testCase.constraint, got, err, testCase.want)
			}
		})
	}
	if _, err := releaseSatisfiesConstraintV1("opaque", "vetted", nil,
		requestedVersionForTestV1("vetted", nil, ">=1.0.0")); err == nil {
		t.Error("an ordering constraint against opaque was accepted")
	}
	got, err := releaseSatisfiesConstraintV1("semver", "1.0.0", nil,
		requestedVersionForTestV1("1.0.0", nil, "==missing"))
	if err != nil || got {
		t.Errorf("missing equality coordinate = %v, %v, want no match", got, err)
	}
}

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

func TestConstraintGroupsIntersectUnderEveryVersionSchemeV1(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		scheme      string
		releases    []enumerationReleaseV1
		constraints []string
		want        string
		empty       []string
	}{
		{name: "semver", scheme: "semver", releases: []enumerationReleaseV1{
			{Version: "1.0.0", Revision: "1"}, {Version: "2.0.0", Revision: "1"}, {Version: "3.0.0", Revision: "1"}},
			constraints: []string{"<3.0.0", ">=1.0.0"}, want: "2.0.0,1.0.0", empty: []string{"<2.0.0", ">=2.0.0"}},
		{name: "pep440", scheme: "pep440", releases: []enumerationReleaseV1{
			{Version: "1.0", Revision: "1"}, {Version: "2.0", Revision: "1"}, {Version: "3.0", Revision: "1"}},
			constraints: []string{"<3", ">=1"}, want: "2.0,1.0", empty: []string{"<2", ">=2"}},
		{name: "integer", scheme: "integer", releases: []enumerationReleaseV1{
			{Version: "8", Revision: "1"}, {Version: "17", Revision: "1"}, {Version: "21", Revision: "1"}},
			constraints: []string{"<21", ">=9"}, want: "17", empty: []string{"<17", ">=17"}},
		{name: "opaque", scheme: "opaque", releases: []enumerationReleaseV1{
			{Version: "vetted", Revision: "1"}, {Version: "other", Revision: "1"}},
			constraints: []string{"==vetted", "vetted"}, want: "vetted", empty: []string{"other", "vetted"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			catalog, tool := enumerationTestCatalogV1(t, testCase.scheme, testCase.releases)
			ordered, err := catalog.enumerateReleaseCandidatesV1(tool, testCase.constraints, "")
			if err != nil {
				t.Fatalf("satisfiable conjunction failed: %v", err)
			}
			if got := strings.Join(enumeratedVersionsV1(t, catalog, ordered), ","); got != testCase.want {
				t.Errorf("intersection = %q, want %q", got, testCase.want)
			}
			if _, err := catalog.enumerateReleaseCandidatesV1(tool, testCase.empty, ""); err == nil ||
				!strings.Contains(err.Error(), "empty intersection") {
				t.Errorf("empty intersection error = %v", err)
			}
		})
	}
}

func TestEveryRetainedConstraintIsParsedBeforeEnumerationV1(t *testing.T) {
	for _, testCase := range []struct {
		scheme      string
		version     string
		constraints []string
		wantSub     string
	}{
		{scheme: "semver", version: "1.0.0", constraints: []string{"<1.0.0", ">=not-semver"}, wantSub: "invalid under semver"},
		{scheme: "pep440", version: "1.0", constraints: []string{"<1.0", ">=not a version"}, wantSub: "invalid under PEP 440"},
		{scheme: "integer", version: "17", constraints: []string{"<17", ">=not-an-integer"}, wantSub: "invalid under the integer scheme"},
		{scheme: "integer", version: "17", constraints: []string{">17,>=not-an-integer"}, wantSub: "invalid under the integer scheme"},
		{scheme: "opaque", version: "vetted", constraints: []string{"absent", ">=1"}, wantSub: "has no ordering"},
	} {
		t.Run(testCase.scheme, func(t *testing.T) {
			catalog, tool := enumerationTestCatalogV1(t, testCase.scheme,
				[]enumerationReleaseV1{{Version: testCase.version, Revision: "1"}})
			_, err := catalog.enumerateReleaseCandidatesV1(tool, testCase.constraints, "")
			if err == nil || !strings.Contains(err.Error(), testCase.wantSub) {
				t.Errorf("constraint parse error = %v, want substring %q", err, testCase.wantSub)
			}
		})
	}
}

func TestUnconstrainedEnumerationExcludesPrereleasesV1(t *testing.T) {
	catalog, tool := enumerationTestCatalogV1(t, "semver", []enumerationReleaseV1{
		{Version: "1.0.0", Revision: "1"}, {Version: "2.0.0-rc.1", Revision: "1"}})
	ordered, err := catalog.enumerateReleaseCandidatesV1(tool, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(enumeratedVersionsV1(t, catalog, ordered), ","); got != "1.0.0" {
		t.Errorf("unconstrained semver enumeration = %q, want 1.0.0", got)
	}
	ordered, err = catalog.enumerateReleaseCandidatesV1(tool, []string{"2.0.0-rc.1"}, "")
	if err != nil || strings.Join(enumeratedVersionsV1(t, catalog, ordered), ",") != "2.0.0-rc.1" {
		t.Errorf("exact prerelease enumeration = %v, %v", enumeratedVersionsV1(t, catalog, ordered), err)
	}
	catalog, tool = enumerationTestCatalogV1(t, "pep440", []enumerationReleaseV1{
		{Version: "1.0", Revision: "1"}, {Version: "2.0rc1", Revision: "1"}})
	ordered, err = catalog.enumerateReleaseCandidatesV1(tool, nil, "")
	if err != nil || strings.Join(enumeratedVersionsV1(t, catalog, ordered), ",") != "1.0" {
		t.Errorf("unconstrained PEP 440 enumeration = %v, %v", enumeratedVersionsV1(t, catalog, ordered), err)
	}
}

func TestExactCoordinatesResolveThroughTheToolWideLookupV1(t *testing.T) {
	catalog, tool := enumerationTestCatalogV1(t, "opaque", []enumerationReleaseV1{
		{Version: "vetted~2024", Revision: "1"}, {Version: "vetted~2023", Revision: "1"}})
	ordered, err := catalog.enumerateReleaseCandidatesV1(tool, []string{"vetted~2024"}, "")
	if err != nil || strings.Join(enumeratedVersionsV1(t, catalog, ordered), ",") != "vetted~2024" {
		t.Errorf("exact opaque coordinate = %v, %v", enumeratedVersionsV1(t, catalog, ordered), err)
	}
	catalog, tool = enumerationTestCatalogV1(t, "pep440", []enumerationReleaseV1{
		{Version: "1!2.0", Revision: "1"}, {Version: "1.5", Revision: "1"}})
	ordered, err = catalog.enumerateReleaseCandidatesV1(tool, []string{"1!2.0"}, "")
	if err != nil || strings.Join(enumeratedVersionsV1(t, catalog, ordered), ",") != "1!2.0" {
		t.Errorf("exact PEP 440 epoch = %v, %v", enumeratedVersionsV1(t, catalog, ordered), err)
	}
}

func TestRevisionPinsRequireAnExactCoordinateV1(t *testing.T) {
	catalog, tool := enumerationTestCatalogV1(t, "semver", []enumerationReleaseV1{
		{Version: "1.0.0", Revision: "1"}, {Version: "1.0.0", Revision: "2"}, {Version: "2.0.0", Revision: "2"}})
	if _, err := catalog.enumerateReleaseCandidatesV1(tool, []string{">=1.0.0"}, "2"); err == nil {
		t.Error("a revision pin was accepted without an exact coordinate")
	}
	ordered, err := catalog.enumerateReleaseCandidatesV1(tool, []string{"1.0.0"}, "2")
	if err != nil || len(ordered) != 1 || !strings.Contains(ordered[0].ID, "/1.0.0/revisions/2/") {
		t.Errorf("exact-coordinate revision pin = %+v, %v", ordered, err)
	}
}

func TestIntegerOrderingCoversEveryOperatorV1(t *testing.T) {
	catalog, tool := enumerationTestCatalogV1(t, "integer", []enumerationReleaseV1{
		{Version: "8", Revision: "1"}, {Version: "17", Revision: "1"}, {Version: "21", Revision: "1"}})
	for _, testCase := range []struct {
		constraint string
		want       string
	}{
		{constraint: "", want: "21,17,8"},
		{constraint: "<=17", want: "17,8"},
		{constraint: ">17", want: "21"},
		{constraint: "!=17", want: "21,8"},
		{constraint: ">8,<=17,!=21", want: "17"},
	} {
		constraints := []string(nil)
		if testCase.constraint != "" {
			constraints = []string{testCase.constraint}
		}
		ordered, err := catalog.enumerateReleaseCandidatesV1(tool, constraints, "")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(enumeratedVersionsV1(t, catalog, ordered), ","); got != testCase.want {
			t.Errorf("integer constraint %q = %q, want %q", testCase.constraint, got, testCase.want)
		}
	}
	if _, err := catalog.enumerateReleaseCandidatesV1(tool, []string{">=lts"}, ""); err == nil {
		t.Error("a nonnumeric integer constraint was accepted")
	}
}

func TestEnumerationRejectsMalformedReleaseDataV1(t *testing.T) {
	catalog, tool := enumerationTestCatalogV1(t, "semver", []enumerationReleaseV1{{Version: "1.0.0", Revision: "1"}})
	tool.Releases = append(tool.Releases, RecordReferenceV1{ID: "tool:demo/releases/2.0.0/revisions/1/manifest",
		Digest: canonical.Digest("sha256:" + strings.Repeat("f", 64))})
	if _, err := catalog.enumerateReleaseCandidatesV1(tool, nil, ""); err == nil ||
		!strings.Contains(err.Error(), "is not in the catalog") {
		t.Errorf("missing release error = %v", err)
	}
	catalog, tool = enumerationTestCatalogV1(t, "semver", []enumerationReleaseV1{{Version: "1.0.0", Revision: "1"}})
	key := recordKeyV1{ID: tool.Releases[0].ID, Digest: tool.Releases[0].Digest}
	loaded := catalog.records[key]
	loaded.Value = &ToolRecordV1{Schema: ToolRecordSchemaV1, ID: "tool:demo", Name: "demo"}
	catalog.records[key] = loaded
	if _, err := catalog.enumerateReleaseCandidatesV1(tool, nil, ""); err == nil ||
		!strings.Contains(err.Error(), "is not a manifest") {
		t.Errorf("wrong release-record type error = %v", err)
	}

	catalog, tool = enumerationTestCatalogV1(t, "semver", []enumerationReleaseV1{{Version: "1.0.0", Revision: "one"}})
	if _, err := catalog.enumerateReleaseCandidatesV1(tool, nil, ""); err == nil ||
		!strings.Contains(err.Error(), "non-numeric revision") {
		t.Errorf("nonnumeric revision error = %v", err)
	}
	catalog, tool = enumerationTestCatalogV1(t, "semver", []enumerationReleaseV1{{Version: "not-semver", Revision: "1"}})
	if _, err := catalog.enumerateReleaseCandidatesV1(tool, nil, ""); err == nil ||
		!strings.Contains(err.Error(), "not valid SemVer") {
		t.Errorf("invalid semver error = %v", err)
	}
}

func TestUnparseableCoordinatesSatisfyNoOrderingConstraintV1(t *testing.T) {
	catalog, tool := enumerationTestCatalogV1(t, "pep440", []enumerationReleaseV1{
		{Version: "not a version", Revision: "1"}, {Version: "2.0", Revision: "1"}})
	ordered, err := catalog.enumerateReleaseCandidatesV1(tool, []string{">=1.0"}, "")
	if err != nil || strings.Join(enumeratedVersionsV1(t, catalog, ordered), ",") != "2.0" {
		t.Errorf("PEP 440 malformed-coordinate filtering = %v, %v", enumeratedVersionsV1(t, catalog, ordered), err)
	}
	if _, err := catalog.enumerateReleaseCandidatesV1(tool, []string{">=not a version"}, ""); err == nil ||
		!strings.Contains(err.Error(), "invalid under PEP 440") {
		t.Errorf("malformed PEP 440 constraint = %v", err)
	}

	catalog, tool = enumerationTestCatalogV1(t, "semver", []enumerationReleaseV1{
		{Version: "not-semver", Revision: "1"}, {Version: "2.0.0", Revision: "1"}})
	ordered, err = catalog.enumerateReleaseCandidatesV1(tool, []string{">=1.0.0"}, "")
	if err != nil || strings.Join(enumeratedVersionsV1(t, catalog, ordered), ",") != "2.0.0" {
		t.Errorf("SemVer malformed-coordinate filtering = %v, %v", enumeratedVersionsV1(t, catalog, ordered), err)
	}
	catalog, tool = enumerationTestCatalogV1(t, "integer", []enumerationReleaseV1{
		{Version: "lts", Revision: "1"}, {Version: "21", Revision: "1"}})
	ordered, err = catalog.enumerateReleaseCandidatesV1(tool, []string{">=17"}, "")
	if err != nil || strings.Join(enumeratedVersionsV1(t, catalog, ordered), ",") != "21" {
		t.Errorf("integer malformed-coordinate filtering = %v, %v", enumeratedVersionsV1(t, catalog, ordered), err)
	}
}

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
	_, err := catalog.SelectReleaseCandidatesV1(candidateTestGroupV1(), candidateTestObservedV1(),
		candidateTestClientV1(), nil)
	if err == nil || !strings.Contains(err.Error(), "invalid definition data") {
		t.Errorf("colliding target leaves = %v, want an invalid definition failure", err)
	}
}

func TestOpaqueRequestOrdersAndRetainsEveryRevisionV1(t *testing.T) {
	catalog, tool := enumerationTestCatalogV1(t, "opaque", []enumerationReleaseV1{
		{Version: "vetted", Revision: "1"}, {Version: "vetted", Revision: "3"},
		{Version: "vetted", Revision: "2"}})
	tool.DefaultVersion = "vetted"
	normalized, _, err := normalizedVersionDemandV1(tool, nil, "")
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
