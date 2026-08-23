package toolcatalog

import (
	"fmt"
	"strings"
	"testing"
)

func composeTestRecordsV1(extra ...any) map[string]loadedRecordV1 {
	records := make(map[string]loadedRecordV1)
	add := func(value any) {
		id := recordIDV1(value)
		records[id] = loadedRecordV1{ID: id, Schema: recordSchemaV1(value), Digest: recordTestDigest, Value: value}
	}
	for _, value := range validRecordValuesV1() {
		add(value)
	}
	for _, value := range extra {
		add(value)
	}
	return records
}

func composeContractV1() *ReleaseContractV1 {
	const release = "tool:demo/releases/1.2.3"
	return &ReleaseContractV1{
		Schema: ReleaseContractSchemaV1, ID: release + "/contract", Contexts: []string{"build", "runtime"},
		Binding: BindingSetSchemaV1{Options: []string{"node", "python"}},
		Selections: SelectionSchemaV1{
			Dimensions: []SelectionDimensionV1{
				{Name: "browser", Options: []string{"chromium", "firefox"}},
				{Name: "mode", Options: []string{"full", "headless"}},
			},
			Combinations: []SelectionCombinationV1{
				{Values: [][]string{{"chromium"}, {"full", "headless"}}},
				{Values: [][]string{{"firefox"}, {"full"}}},
			},
		},
		Exports: []ToolExportV1{{Name: "demo", Path: "/opt/demo/bin/demo"}},
	}
}

func composeTargetV1() *TargetRecordV1 {
	const release = "tool:demo/releases/1.2.3"
	return &TargetRecordV1{
		Schema: TargetRecordSchemaV1, ID: release + "/targets/debian/12/amd64",
		Target: TargetIdentityV1{Platform: "linux/amd64", OSReleaseID: "debian", VersionID: "12", OCIArchitecture: "amd64", NativeArchitecture: "amd64", PackageManager: "apt"},
		Bindings: []TargetBindingV1{
			{Name: "node", Contract: recordTestReference(release + "/bindings/node/contract"), Artifacts: []RecordReferenceV1{}, Payloads: []RecordReferenceV1{}, PackageSets: []RecordReferenceV1{}, Exports: []ToolExportV1{}, ValidationProfiles: []RecordReferenceV1{}},
			{Name: "python", Contract: recordTestReference(release + "/bindings/python/contract"), Artifacts: []RecordReferenceV1{recordTestReference(release + "/bindings/python/artifacts/linux-amd64")}, Payloads: []RecordReferenceV1{}, PackageSets: []RecordReferenceV1{}, Exports: []ToolExportV1{}, ValidationProfiles: []RecordReferenceV1{}},
		},
		Selections: []TargetSelectionV1{
			{Dimension: "browser", Value: "chromium", Payloads: []RecordReferenceV1{}, PackageSets: []RecordReferenceV1{}, Exports: []ToolExportV1{}, ValidationProfiles: []RecordReferenceV1{}},
			{Dimension: "browser", Value: "firefox", Payloads: []RecordReferenceV1{}, PackageSets: []RecordReferenceV1{}, Exports: []ToolExportV1{}, ValidationProfiles: []RecordReferenceV1{}},
			{Dimension: "mode", Value: "full", Payloads: []RecordReferenceV1{}, PackageSets: []RecordReferenceV1{}, Exports: []ToolExportV1{}, ValidationProfiles: []RecordReferenceV1{}},
			{Dimension: "mode", Value: "headless", Payloads: []RecordReferenceV1{}, PackageSets: []RecordReferenceV1{}, Exports: []ToolExportV1{}, ValidationProfiles: []RecordReferenceV1{}},
		},
		PackageSets: []RecordReferenceV1{}, Payloads: []RecordReferenceV1{}, Exports: []ToolExportV1{},
		ValidationProfiles: []RecordReferenceV1{}, IntegrationFixtures: []RecordReferenceV1{},
	}
}

func composeFixtureV1(name, context string, bindings []string, selections map[string][]string) *IntegrationFixtureRecordV1 {
	target := composeTargetV1()
	return &IntegrationFixtureRecordV1{
		Schema: IntegrationFixtureSchemaV1, ID: "tool:demo/releases/1.2.3/validation/fixtures/" + name, Name: name,
		Target: target.Target, Context: context, Bindings: append([]string{}, bindings...), Selections: cloneSelectionMapV1(selections),
		ValidationProfiles: []RecordReferenceV1{}, BaseImage: "docker.io/library/debian:12-slim", BaseImageDigest: recordTestDigest,
	}
}

func TestSupportTuplesUseBindingSetsAndNamedSelectionCombinationsV1(t *testing.T) {
	contract, target := composeContractV1(), composeTargetV1()
	tuples, err := targetSupportTuplesV1(contract, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(tuples) != 12 { // 2 contexts * 3 nonempty binding subsets * 2 combinations.
		t.Fatalf("tuple count = %d, want 12", len(tuples))
	}
	for _, tuple := range tuples {
		if len(tuple.Bindings) == 0 || len(tuple.Selections) != 2 {
			t.Errorf("noncanonical tuple: %+v", tuple)
		}
	}
	left := supportTupleV1{Context: "build", Bindings: []string{"node", "python"}, Selections: map[string][]string{"mode": {"full"}, "browser": {"chromium"}}}
	right := supportTupleV1{Context: "build", Bindings: []string{"node", "python"}, Selections: map[string][]string{"browser": {"chromium"}, "mode": {"full"}}}
	leftKey, _ := supportTupleKeyV1(left)
	rightKey, _ := supportTupleKeyV1(right)
	if leftKey != rightKey {
		t.Fatal("selection map insertion order changed tuple identity")
	}
}

func TestSupportTupleEnumerationIsBoundedV1(t *testing.T) {
	contract, target := composeContractV1(), composeTargetV1()
	target.Bindings = nil
	for index := 0; index < 11; index++ {
		target.Bindings = append(target.Bindings, TargetBindingV1{Name: fmt.Sprintf("binding-%02d", index)})
	}
	if _, err := targetSupportTuplesV1(contract, target); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("unbounded binding power set error = %v", err)
	}
}

func TestTargetAgainstContractRequiresCompleteAdvertisedCombinationsV1(t *testing.T) {
	contract, target := composeContractV1(), composeTargetV1()
	if err := validateTargetAgainstContractV1(contract, target); err != nil {
		t.Fatalf("valid target rejected: %v", err)
	}
	target.Selections = []TargetSelectionV1{{Dimension: "browser", Value: "chromium"}}
	if err := validateTargetAgainstContractV1(contract, target); err == nil || !strings.Contains(err.Error(), "supported combination") {
		t.Fatalf("partial combination error = %v", err)
	}
	target = composeTargetV1()
	target.Selections = append(target.Selections, TargetSelectionV1{Dimension: "browser", Value: "webkit"})
	if err := validateTargetAgainstContractV1(contract, target); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("undeclared selection error = %v", err)
	}
	target = composeTargetV1()
	target.Bindings[0].Name = "ruby"
	if err := validateTargetAgainstContractV1(contract, target); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("undeclared binding error = %v", err)
	}
}

func TestFixtureMustMatchTargetTupleAndProfilesV1(t *testing.T) {
	contract, target := composeContractV1(), composeTargetV1()
	fixture := composeFixtureV1("ok", "build", []string{"python"}, map[string][]string{"browser": {"chromium"}, "mode": {"full", "headless"}})
	if err := validateFixtureAgainstTargetV1(contract, target, fixture); err != nil {
		t.Fatalf("valid fixture rejected: %v", err)
	}
	bad := *fixture
	bad.Bindings = []string{}
	if err := validateFixtureAgainstTargetV1(contract, target, &bad); err == nil || !strings.Contains(err.Error(), "binding set") {
		t.Fatalf("empty binding set error = %v", err)
	}
	bad = *fixture
	bad.Selections = map[string][]string{"browser": {"chromium"}, "mode": {"full"}}
	if err := validateFixtureAgainstTargetV1(contract, target, &bad); err == nil || !strings.Contains(err.Error(), "combination") {
		t.Fatalf("invented selection combination error = %v", err)
	}
	target.ValidationProfiles = []RecordReferenceV1{recordTestReference("tool:demo/releases/1.2.3/validation/profiles/default")}
	if err := validateFixtureAgainstTargetV1(contract, target, fixture); err == nil || !strings.Contains(err.Error(), "profiles") {
		t.Fatalf("missing selected profile error = %v", err)
	}
}

func TestFixtureCoverageRequiresEveryTupleExactlyOnceV1(t *testing.T) {
	contract, target := composeContractV1(), composeTargetV1()
	contract.Contexts = []string{"build"}
	target.Bindings = target.Bindings[1:]
	fixtures := []*IntegrationFixtureRecordV1{
		composeFixtureV1("chromium", "build", []string{"python"}, map[string][]string{"browser": {"chromium"}, "mode": {"full", "headless"}}),
		composeFixtureV1("firefox", "build", []string{"python"}, map[string][]string{"browser": {"firefox"}, "mode": {"full"}}),
	}
	if err := validateTargetFixtureCoverageV1(composeTestRecordsV1(), contract, target, fixtures); err != nil {
		t.Fatalf("complete coverage rejected: %v", err)
	}
	if err := validateTargetFixtureCoverageV1(composeTestRecordsV1(), contract, target, fixtures[:1]); err == nil || !strings.Contains(err.Error(), "do not cover") {
		t.Fatalf("missing fixture error = %v", err)
	}
	duplicate := append(fixtures, composeFixtureV1("duplicate", "build", []string{"python"}, map[string][]string{"browser": {"firefox"}, "mode": {"full"}}))
	if err := validateTargetFixtureCoverageV1(composeTestRecordsV1(), contract, target, duplicate); err == nil || !strings.Contains(err.Error(), "same support tuple") {
		t.Fatalf("duplicate fixture error = %v", err)
	}
}

func TestBindingInterpreterCoverageRequiresEveryAdvertisedVersionV1(t *testing.T) {
	contract := &BindingContractV1{Name: "python", SupportedPython: []string{"3.11", "3.12"}}
	cp311 := &BindingArtifactRecordV1{ID: "cp311", RequiresPython: ">=3.11,<3.12"}
	cp312 := &BindingArtifactRecordV1{ID: "cp312", RequiresPython: ">=3.12,<3.13"}
	if err := validateBindingInterpreterCoverageV1(contract, []*BindingArtifactRecordV1{cp311, cp312}); err != nil {
		t.Fatal(err)
	}
	if err := validateBindingInterpreterCoverageV1(contract, []*BindingArtifactRecordV1{cp311}); err == nil || !strings.Contains(err.Error(), "3.12") {
		t.Fatalf("uncovered interpreter error = %v", err)
	}
}

func TestBindingArtifactsAgreeWithContractAndExactReferencesV1(t *testing.T) {
	values := validRecordValuesV1()
	contract := values[4].(*BindingContractV1)
	artifact := values[5].(*BindingArtifactRecordV1)
	target := composeTargetV1()
	target.Bindings = target.Bindings[1:]
	records := composeTestRecordsV1(contract, artifact)
	if err := validateTargetBindingsAgainstContractsV1(records, target); err != nil {
		t.Fatalf("valid binding rejected: %v", err)
	}
	broken := *artifact
	broken.Name = "other"
	records[broken.ID] = loadedRecordV1{ID: broken.ID, Schema: broken.Schema, Digest: recordTestDigest, Value: &broken}
	if err := validateTargetBindingsAgainstContractsV1(records, target); err == nil || !strings.Contains(err.Error(), "distribution") {
		t.Fatalf("wrong distribution error = %v", err)
	}
	broken = *artifact
	broken.Platform = "linux/arm64"
	records = composeTestRecordsV1(contract, &broken)
	if err := validateTargetBindingsAgainstContractsV1(records, target); err == nil || !strings.Contains(err.Error(), "target platform") {
		t.Fatalf("wrong artifact platform error = %v", err)
	}
	broken = *artifact
	broken.Tags = []string{"py3-none-any"}
	records = composeTestRecordsV1(contract, &broken)
	if err := validateTargetBindingsAgainstContractsV1(records, target); err == nil || !strings.Contains(err.Error(), "tags") {
		t.Fatalf("incompatible artifact tags error = %v", err)
	}
	target.Bindings[0].Contract.Digest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	if err := validateTargetBindingsAgainstContractsV1(composeTestRecordsV1(contract, artifact), target); err == nil || !strings.Contains(err.Error(), "exact") {
		t.Fatalf("wrong contract digest error = %v", err)
	}
}

func TestTuplePayloadsRejectCollisionsAndWrongPlatformsV1(t *testing.T) {
	payload := func(id, logical, install, platform string) *PayloadRecordV1 {
		return &PayloadRecordV1{Schema: PayloadRecordSchemaV1, ID: id, LogicalPath: logical, InstallDirectory: install, Platform: platform}
	}
	left := payload("left", "tools/left.zip", "browser", "linux/amd64")
	right := payload("right", "tools/right.zip", "browser/headless", "linux/amd64")
	records := composeTestRecordsV1(left, right)
	refs := []RecordReferenceV1{recordTestReference(left.ID), recordTestReference(right.ID)}
	if err := validateTuplePayloadsV1(records, refs, "linux/amd64"); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("overlap error = %v", err)
	}
	right.InstallDirectory = "headless"
	right.LogicalPath = left.LogicalPath
	if err := validateTuplePayloadsV1(records, refs, "linux/amd64"); err == nil || !strings.Contains(err.Error(), "logical path") {
		t.Fatalf("logical collision error = %v", err)
	}
	right.LogicalPath = "tools/right.zip"
	right.Platform = "linux/arm64"
	if err := validateTuplePayloadsV1(records, refs, "linux/amd64"); err == nil || !strings.Contains(err.Error(), "platform") {
		t.Fatalf("wrong platform error = %v", err)
	}
}

func TestTupleContributionsDoNotLeakAndRejectConflictsV1(t *testing.T) {
	const release = "tool:demo/releases/1.2.3"
	packageSet := func(id string, requirement string) *NativePackageSetV1 {
		return &NativePackageSetV1{Schema: NativePackageSetSchemaV1, ID: id, Manager: "apt", Requirements: []string{requirement}}
	}
	base := packageSet(release+"/package-sets/base", "libdemo=1")
	agreeing := packageSet(release+"/package-sets/agreeing", "libdemo=1")
	conflicting := packageSet(release+"/package-sets/conflicting", "libdemo=2")
	contract, target := composeContractV1(), composeTargetV1()
	target.Bindings = target.Bindings[1:]
	target.Selections = []TargetSelectionV1{
		{Dimension: "browser", Value: "chromium", PackageSets: []RecordReferenceV1{recordTestReference(agreeing.ID)}, Payloads: []RecordReferenceV1{}, Exports: []ToolExportV1{}, ValidationProfiles: []RecordReferenceV1{}},
		{Dimension: "browser", Value: "firefox", PackageSets: []RecordReferenceV1{recordTestReference(conflicting.ID)}, Payloads: []RecordReferenceV1{}, Exports: []ToolExportV1{}, ValidationProfiles: []RecordReferenceV1{}},
		{Dimension: "mode", Value: "full", PackageSets: []RecordReferenceV1{}, Payloads: []RecordReferenceV1{}, Exports: []ToolExportV1{}, ValidationProfiles: []RecordReferenceV1{}},
		{Dimension: "mode", Value: "headless", PackageSets: []RecordReferenceV1{}, Payloads: []RecordReferenceV1{}, Exports: []ToolExportV1{}, ValidationProfiles: []RecordReferenceV1{}},
	}
	target.PackageSets = []RecordReferenceV1{recordTestReference(base.ID)}
	records := composeTestRecordsV1(base, agreeing, conflicting)
	chromium := supportTupleV1{Context: "build", Bindings: []string{"python"}, Selections: map[string][]string{"browser": {"chromium"}, "mode": {"full"}}}
	if err := validateTupleContributionsV1(records, contract, target, chromium); err != nil {
		t.Fatalf("unselected conflict leaked into chromium: %v", err)
	}
	firefox := supportTupleV1{Context: "build", Bindings: []string{"python"}, Selections: map[string][]string{"browser": {"firefox"}, "mode": {"full"}}}
	if err := validateTupleContributionsV1(records, contract, target, firefox); err == nil || !strings.Contains(err.Error(), "conflict on package") {
		t.Fatalf("selected package conflict error = %v", err)
	}
	target.Exports = []ToolExportV1{{Name: "demo", Path: "/other/demo"}}
	if err := validateTupleContributionsV1(records, contract, target, chromium); err == nil || !strings.Contains(err.Error(), "conflict on export") {
		t.Fatalf("export conflict error = %v", err)
	}
	target.Exports = []ToolExportV1{}
	contract.Exports = []ToolExportV1{}
	target.Bindings[0].Exports = []ToolExportV1{{Name: "demo", Path: "/other/demo"}}
	if err := validateTupleContributionsV1(records, contract, target, chromium); err == nil || !strings.Contains(err.Error(), "conflict on export") {
		t.Fatalf("binding CLI conflict error = %v", err)
	}
}

func TestPackageSetReferencesRequireTargetManagerV1(t *testing.T) {
	target := composeTargetV1()
	set := &NativePackageSetV1{Schema: NativePackageSetSchemaV1, ID: "set", Manager: "apt"}
	records := composeTestRecordsV1(set)
	if err := validatePackageSetReferencesV1(records, []RecordReferenceV1{recordTestReference(set.ID)}, target); err != nil {
		t.Fatal(err)
	}
	set.Manager = "apk"
	if err := validatePackageSetReferencesV1(records, []RecordReferenceV1{recordTestReference(set.ID)}, target); err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("manager mismatch error = %v", err)
	}
}

func TestRecordPathOverlapsV1(t *testing.T) {
	for _, testCase := range []struct {
		left, right string
		want        bool
	}{
		{left: "browser", right: "browser", want: true},
		{left: "browser", right: "browser/headless", want: true},
		{left: "browser/headless", right: "browser", want: true},
		{left: "browser", right: "browser-old", want: false},
		{left: "chromium", right: "headless", want: false},
	} {
		if got := recordPathOverlapsV1(testCase.left, testCase.right); got != testCase.want {
			t.Errorf("overlap(%q, %q) = %v, want %v", testCase.left, testCase.right, got, testCase.want)
		}
	}
}
