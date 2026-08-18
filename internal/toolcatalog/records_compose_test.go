package toolcatalog

import (
	"fmt"
	"strings"
	"testing"
)

// composeTestRecordsV1 indexes the shared valid record values by ID so
// composition validators can resolve references exactly as the loader will.
//
// The shared set is not reference-closed on its own: the sample target names an
// unconditional payload that validRecordValuesV1 does not contain. Nothing
// before this slice resolved references, so nothing noticed. The missing
// payload is supplied here rather than by changing the shared fixture, which
// approved slices depend on.
func composeTestRecordsV1(extra ...any) map[string]loadedRecordV1 {
	records := make(map[string]loadedRecordV1)
	records["tool:demo/releases/1.2.3/payloads/demo-linux-amd64"] = loadedRecordV1{
		ID:     "tool:demo/releases/1.2.3/payloads/demo-linux-amd64",
		Schema: PayloadRecordSchemaV1, Digest: recordTestDigest,
		Value: &PayloadRecordV1{Schema: PayloadRecordSchemaV1,
			ID:   "tool:demo/releases/1.2.3/payloads/demo-linux-amd64",
			Name: "demo", Platform: "linux/amd64",
			LogicalPath: "tools/demo/demo.tar.gz", InstallDirectory: "demo"},
	}
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

func composeTestContractV1() *ReleaseContractV1 {
	return validRecordValuesV1()[2].(*ReleaseContractV1)
}

func composeTestTargetV1() *TargetRecordV1 {
	return validRecordValuesV1()[3].(*TargetRecordV1)
}

// A binding advertises a set of interpreters. Checking each artifact alone only
// proves that artifact serves some advertised interpreter, so a contract can
// advertise interpreters that no selected wheel can install.
func TestBindingInterpreterCoverageRequiresEveryAdvertisedVersionV1(t *testing.T) {
	contract := &BindingContractV1{Name: "python", SupportedPython: []string{"3.11", "3.12"}}
	cp311 := &BindingArtifactRecordV1{ID: "artifact-cp311", RequiresPython: ">=3.11,<3.12"}
	cp312 := &BindingArtifactRecordV1{ID: "artifact-cp312", RequiresPython: ">=3.12,<3.13"}
	universal := &BindingArtifactRecordV1{ID: "artifact-py3", RequiresPython: ">=3.11"}

	if err := validateBindingInterpreterCoverageV1(contract, []*BindingArtifactRecordV1{cp311, cp312}); err != nil {
		t.Errorf("both interpreters covered by two wheels: %v", err)
	}
	if err := validateBindingInterpreterCoverageV1(contract, []*BindingArtifactRecordV1{universal}); err != nil {
		t.Errorf("both interpreters covered by one universal wheel: %v", err)
	}
	// The carried finding from retired PR 83: advertising 3.11 and 3.12 while
	// shipping only a cp311 wheel must fail, even though that wheel satisfies
	// the per-artifact contract check.
	err := validateBindingInterpreterCoverageV1(contract, []*BindingArtifactRecordV1{cp311})
	if err == nil || !strings.Contains(err.Error(), "3.12") {
		t.Errorf("cp311-only artifact set error = %v, want an uncovered 3.12 rejection", err)
	}
	if err := validateBindingInterpreterCoverageV1(contract, nil); err == nil {
		t.Error("empty artifact set covered every interpreter")
	}
}

func TestBindingInterpreterCoverageRejectsMalformedVersionsV1(t *testing.T) {
	if err := validateBindingInterpreterCoverageV1(
		&BindingContractV1{Name: "python", SupportedPython: []string{"banana"}},
		[]*BindingArtifactRecordV1{{ID: "a", RequiresPython: ">=3.11"}}); err == nil {
		t.Error("malformed contract interpreter accepted")
	}
	if err := validateBindingInterpreterCoverageV1(
		&BindingContractV1{Name: "python", SupportedPython: []string{"3.11"}},
		[]*BindingArtifactRecordV1{{ID: "a", RequiresPython: "not-a-specifier"}}); err == nil {
		t.Error("malformed requires_python accepted")
	}
}

// Payloads reachable in one tuple install together, so they may not claim the
// same logical artifact or own overlapping directory trees.
func TestTuplePayloadsRejectCollisionsV1(t *testing.T) {
	payload := func(id string, logical string, install string) *PayloadRecordV1 {
		return &PayloadRecordV1{Schema: PayloadRecordSchemaV1, ID: id,
			Platform: "linux/amd64", LogicalPath: logical, InstallDirectory: install}
	}
	build := func(payloads ...*PayloadRecordV1) (map[string]loadedRecordV1, []selectedPayloadReferenceV1) {
		records := make(map[string]loadedRecordV1)
		references := make([]selectedPayloadReferenceV1, 0, len(payloads))
		for _, value := range payloads {
			records[value.ID] = loadedRecordV1{ID: value.ID, Schema: value.Schema, Digest: recordTestDigest, Value: value}
			references = append(references, selectedPayloadReferenceV1{
				Reference: recordTestReference(value.ID), Selection: value.Selection})
		}
		return records, references
	}

	records, references := build(
		payload("chromium", "tools/demo/chromium.zip", "chromium"),
		payload("headless", "tools/demo/headless.zip", "headless-shell"),
		payload("ffmpeg", "tools/demo/ffmpeg.zip", "ffmpeg"))
	if err := validateTuplePayloadsV1(records, references, "linux/amd64"); err != nil {
		t.Errorf("distinct coupled payloads rejected: %v", err)
	}

	// The carried finding from retired PR 83, first half: a shared logical path.
	records, references = build(
		payload("chromium", "tools/demo/browser.zip", "chromium"),
		payload("headless", "tools/demo/browser.zip", "headless-shell"))
	err := validateTuplePayloadsV1(records, references, "linux/amd64")
	if err == nil || !strings.Contains(err.Error(), "share logical path") {
		t.Errorf("shared logical path error = %v", err)
	}

	// Second half: overlapping install destinations, package requirements agreeing.
	for _, testCase := range []struct{ name, left, right string }{
		{name: "identical", left: "chromium", right: "chromium"},
		{name: "nested", left: "chromium", right: "chromium/headless"},
		{name: "reverse nested", left: "chromium/headless", right: "chromium"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			records, references := build(
				payload("left", "tools/demo/left.zip", testCase.left),
				payload("right", "tools/demo/right.zip", testCase.right))
			err := validateTuplePayloadsV1(records, references, "linux/amd64")
			if err == nil || !strings.Contains(err.Error(), "overlap install destinations") {
				t.Errorf("error = %v, want an overlap rejection", err)
			}
		})
	}

	// A shared unowned parent is allowed: neither owns the other's tree.
	records, references = build(
		payload("left", "tools/demo/left.zip", "browsers/chromium"),
		payload("right", "tools/demo/right.zip", "browsers/firefox"))
	if err := validateTuplePayloadsV1(records, references, "linux/amd64"); err != nil {
		t.Errorf("siblings under an unowned parent rejected: %v", err)
	}

	// A prefix that is not a segment boundary is not containment.
	records, references = build(
		payload("left", "tools/demo/left.zip", "chromium"),
		payload("right", "tools/demo/right.zip", "chromium-headless"))
	if err := validateTuplePayloadsV1(records, references, "linux/amd64"); err != nil {
		t.Errorf("non-boundary prefix treated as overlap: %v", err)
	}
}

func TestTuplePayloadsRejectUnresolvableAndMistypedReferencesV1(t *testing.T) {
	records := composeTestRecordsV1()
	absent := []selectedPayloadReferenceV1{{Reference: recordTestReference("tool:demo/releases/1.2.3/payloads/absent")}}
	if err := validateTuplePayloadsV1(records, absent, "linux/amd64"); err == nil {
		t.Error("unresolvable payload reference accepted")
	}
	contract := composeTestContractV1()
	mistyped := []selectedPayloadReferenceV1{{Reference: recordTestReference(contract.ID)}}
	if err := validateTuplePayloadsV1(records, mistyped, "linux/amd64"); err == nil {
		t.Error("non-payload record accepted as a payload")
	}
}

func TestRecordPathOverlapsV1(t *testing.T) {
	for _, testCase := range []struct {
		left, right string
		want        bool
	}{
		{left: "a", right: "a", want: true},
		{left: "a", right: "a/b", want: true},
		{left: "a/b", right: "a", want: true},
		{left: "a", right: "ab", want: false},
		{left: "a/b", right: "a/c", want: false},
		{left: "", right: "a", want: false},
	} {
		if got := recordPathOverlapsV1(testCase.left, testCase.right); got != testCase.want {
			t.Errorf("recordPathOverlapsV1(%q, %q) = %v, want %v", testCase.left, testCase.right, got, testCase.want)
		}
	}
}

// Probe identity is a semantic key: identical probes deduplicate, but the same
// executable invoked differently is a conflict rather than two probes.
func TestTupleContributionsRejectConflictingProbesV1(t *testing.T) {
	contract := composeTestContractV1()
	target := composeTestTargetV1()
	target.Probes = []RecordProbeV1{
		{Path: "/opt/demo/bin/demo", Args: []string{"--version"}, Network: "none"},
	}
	records := composeTestRecordsV1()
	tuple := supportTupleV1{Context: "build", Selections: []string{}, Parameters: []ParameterValueV1{}}

	if err := validateTupleContributionsV1(records, contract, target, tuple); err != nil {
		t.Fatalf("single probe rejected: %v", err)
	}
	target.Probes = append(target.Probes, RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--version"}, Network: "none"})
	if err := validateTupleContributionsV1(records, contract, target, tuple); err != nil {
		t.Errorf("identical duplicate probe rejected instead of deduplicated: %v", err)
	}
	// Probe identity is the complete canonical value, so the same executable
	// invoked with different arguments is two probes rather than a conflict.
	target.Probes[1].Args = []string{"--help"}
	if err := validateTupleContributionsV1(records, contract, target, tuple); err != nil {
		t.Errorf("distinct probes on one executable rejected as a conflict: %v", err)
	}
}

// A target may advertise a subset of the contract's symbols, and the tuples it
// advertises must follow that subset rather than the full contract list.
func TestSupportTuplesFollowTargetAdvertisedSubsetV1(t *testing.T) {
	contract := composeRichContractV1()
	contract.Binding.Options = []string{"node", "python"}
	target := composeRichTargetV1()

	full, err := targetSupportTuplesV1(contract, target)
	if err != nil {
		t.Fatal(err)
	}
	for _, tuple := range full {
		if tuple.Binding == "node" {
			t.Fatalf("enumerated a binding the target does not advertise: %+v", tuple)
		}
	}
	if len(full) != 2 {
		t.Fatalf("python with two selections should advertise two tuples, got %d", len(full))
	}

	// Dropping a selection from the target drops its tuples, and the contract
	// still validates because advertising a subset is legal.
	target.Selections = target.Selections[:1]
	if err := validateTargetAgainstContractV1(contract, target); err != nil {
		t.Fatalf("advertising a subset of selections was rejected: %v", err)
	}
	narrowed, err := targetSupportTuplesV1(contract, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(narrowed) != 1 {
		t.Fatalf("one advertised selection should advertise one tuple, got %d", len(narrowed))
	}
	if len(narrowed[0].Selections) != 1 || narrowed[0].Selections[0] != "chromium" {
		t.Errorf("narrowed tuple = %+v", narrowed[0])
	}

	// Fixture coverage follows the narrowed set: one fixture now suffices.
	records := composeTestRecordsV1()
	chromium := composeFixtureV1("debian-12-amd64-chromium", "python", "chromium")
	if err := validateTargetFixtureCoverageV1(records, contract, target,
		[]*IntegrationFixtureRecordV1{chromium}); err != nil {
		t.Errorf("coverage of the narrowed tuple set rejected: %v", err)
	}
	firefox := composeFixtureV1("debian-12-amd64-firefox", "python", "firefox")
	if err := validateTargetFixtureCoverageV1(records, contract, target,
		[]*IntegrationFixtureRecordV1{chromium, firefox}); err == nil {
		t.Error("a fixture for an unadvertised selection was accepted")
	}
}

// Unselected contributions never enter a tuple, so a collision that only exists
// between two different selections is not a collision in either tuple.
func TestUnselectedContributionsNeverLeakIntoATupleV1(t *testing.T) {
	const release = "tool:demo/releases/1.2.3"
	colliding := func(id string, selection string) *PayloadRecordV1 {
		return &PayloadRecordV1{Schema: PayloadRecordSchemaV1, ID: id, Selection: selection,
			Platform: "linux/amd64", LogicalPath: "tools/demo/browser.zip", InstallDirectory: "browser"}
	}
	chromium := colliding(release+"/payloads/chromium/browser-linux-amd64", "chromium")
	firefox := colliding(release+"/payloads/firefox/browser-linux-amd64", "firefox")
	records := composeTestRecordsV1(chromium, firefox)

	contract := composeTestContractV1()
	target := composeTestTargetV1()
	target.Payloads = []RecordReferenceV1{}
	target.Selections = []TargetSelectionV1{
		{Name: "chromium", Payloads: []RecordReferenceV1{recordTestReference(chromium.ID)}},
		{Name: "firefox", Payloads: []RecordReferenceV1{recordTestReference(firefox.ID)}},
	}

	for _, selected := range []string{"chromium", "firefox"} {
		tuple := supportTupleV1{Context: "build", Selections: []string{selected}, Parameters: []ParameterValueV1{}}
		if err := validateTupleContributionsV1(records, contract, target, tuple); err != nil {
			t.Errorf("selecting only %q surfaced a collision with the unselected payload: %v", selected, err)
		}
	}
	// Selecting both together is the tuple where the collision is real.
	both := supportTupleV1{Context: "build", Selections: []string{"chromium", "firefox"}, Parameters: []ParameterValueV1{}}
	if err := validateTupleContributionsV1(records, contract, target, both); err == nil {
		t.Error("selecting both colliding payloads together was accepted")
	}
}

func TestSupportTupleKeyIsOrderIndependentIdentityV1(t *testing.T) {
	left := supportTupleV1{Context: "build", Binding: "python", Selections: []string{"chromium"},
		Parameters: []ParameterValueV1{{Name: "channel", Value: "stable"}}}
	right := supportTupleV1{Context: "build", Binding: "python", Selections: []string{"chromium"},
		Parameters: []ParameterValueV1{{Name: "channel", Value: "stable"}}}
	leftKey, err := supportTupleKeyV1(left)
	if err != nil {
		t.Fatal(err)
	}
	rightKey, err := supportTupleKeyV1(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftKey != rightKey {
		t.Errorf("equal tuples produced different keys:\n%s\n%s", leftKey, rightKey)
	}
	right.Selections = []string{"firefox"}
	otherKey, err := supportTupleKeyV1(right)
	if err != nil {
		t.Fatal(err)
	}
	if otherKey == leftKey {
		t.Error("different selections produced the same key")
	}
}

func TestTargetSupportTuplesEnumerateAndStayBoundedV1(t *testing.T) {
	contract := composeTestContractV1()
	target := composeTestTargetV1()
	tuples, err := targetSupportTuplesV1(contract, target)
	if err != nil {
		t.Fatalf("enumerating the sample target: %v", err)
	}
	if len(tuples) == 0 {
		t.Fatal("sample target advertises no support tuple")
	}
	keys := make(map[string]struct{}, len(tuples))
	for _, tuple := range tuples {
		key, err := supportTupleKeyV1(tuple)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := keys[key]; exists {
			t.Errorf("duplicate tuple enumerated: %s", key)
		}
		keys[key] = struct{}{}
	}

	// An integer parameter whose domain exceeds the validation-case limit must
	// fail closed rather than enumerate.
	wide := *contract
	wide.Parameters = []ParameterSchemaV1{{Name: "port", Type: "integer",
		Minimum: "0", Maximum: fmt.Sprint(maxDefinitionValidationCases + 1), Values: []string{}}}
	if _, err := targetSupportTuplesV1(&wide, target); err == nil {
		t.Error("an unbounded parameter domain enumerated instead of failing closed")
	}
}

func TestTargetAgainstContractValidatesAdvertisedSubsetV1(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		mutate  func(*ReleaseContractV1, *TargetRecordV1)
		wantSub string
	}{
		{name: "binding mapping not declared by the contract", wantSub: "not declared by the release contract",
			mutate: func(c *ReleaseContractV1, target *TargetRecordV1) {
				c.Binding.Options = []string{"python"}
				target.Bindings = []TargetBindingV1{{Name: "node"}}
			}},
		{name: "required binding advertised by no mapping", wantSub: "advertises none",
			mutate: func(c *ReleaseContractV1, target *TargetRecordV1) {
				c.Binding = BindingRequestV1{Options: []string{"python"}, Required: true, Default: "python"}
				target.Bindings = []TargetBindingV1{}
			}},
		{name: "contract default binding not advertised", wantSub: "default binding",
			mutate: func(c *ReleaseContractV1, target *TargetRecordV1) {
				c.Binding = BindingRequestV1{Options: []string{"node", "python"}, Required: true, Default: "python"}
				target.Bindings = []TargetBindingV1{{Name: "node"}}
			}},
		{name: "selection mapping not declared by the contract", wantSub: "not declared by the release contract",
			mutate: func(c *ReleaseContractV1, target *TargetRecordV1) {
				c.Selections.Options = []string{"chromium"}
				target.Selections = []TargetSelectionV1{{Name: "webkit"}}
			}},
		{name: "fewer selections advertised than required", wantSub: "requires 1 selections",
			mutate: func(c *ReleaseContractV1, target *TargetRecordV1) {
				c.Selections = SelectionRequestV1{Options: []string{"chromium"}, Minimum: "1", Maximum: "1",
					Defaults: []string{}, CompatibilityGroups: [][]string{{"chromium"}}}
				target.Selections = []TargetSelectionV1{}
			}},
		{name: "undeclared parameter constraint", wantSub: "not declared by the release contract",
			mutate: func(c *ReleaseContractV1, target *TargetRecordV1) {
				target.Parameters = []TargetParameterConstraintV1{{Name: "absent", Values: []string{"x"}}}
			}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			contract := composeTestContractV1()
			target := composeTestTargetV1()
			testCase.mutate(contract, target)
			err := validateTargetAgainstContractV1(contract, target)
			if err == nil || !strings.Contains(err.Error(), testCase.wantSub) {
				t.Errorf("error = %v, want substring %q", err, testCase.wantSub)
			}
		})
	}
}

func TestTargetAgainstContractAcceptsTheSampleTargetV1(t *testing.T) {
	if err := validateTargetAgainstContractV1(composeTestContractV1(), composeTestTargetV1()); err != nil {
		t.Errorf("the shared valid target and contract disagree: %v", err)
	}
}

// composeRichContractV1 advertises a binding, two mutually exclusive
// selections, and one enum parameter, so a target built from it advertises
// more than one support tuple and fixture coverage becomes meaningful.
func composeRichContractV1() *ReleaseContractV1 {
	contract := composeTestContractV1()
	contract.Binding = BindingRequestV1{Options: []string{"python"}, Required: true, Default: "python"}
	contract.Selections = SelectionRequestV1{
		Options: []string{"chromium", "firefox"}, Minimum: "1", Maximum: "1",
		Defaults: []string{}, CompatibilityGroups: [][]string{{"chromium", "firefox"}},
	}
	contract.Parameters = []ParameterSchemaV1{}
	return contract
}

func composeRichTargetV1() *TargetRecordV1 {
	target := composeTestTargetV1()
	target.Payloads = []RecordReferenceV1{}
	target.Bindings = targetBindingWithArtifactV1("tool:demo/releases/1.2.3/bindings/python/artifacts/linux-amd64")
	target.Selections = []TargetSelectionV1{
		{Name: "chromium", Payloads: []RecordReferenceV1{}, PackageSets: []RecordReferenceV1{}, Exports: []ToolExportV1{}, Probes: []RecordProbeV1{}},
		{Name: "firefox", Payloads: []RecordReferenceV1{}, PackageSets: []RecordReferenceV1{}, Exports: []ToolExportV1{}, Probes: []RecordProbeV1{}},
	}
	return target
}

func composeFixtureV1(name string, binding string, selections ...string) *IntegrationFixtureRecordV1 {
	return &IntegrationFixtureRecordV1{
		Schema: IntegrationFixtureSchemaV1, ID: "tool:demo/releases/1.2.3/validation/fixtures/" + name,
		Name: name, Context: "build", Binding: binding,
		Selections: append([]string{}, selections...), Parameters: []ParameterValueV1{},
	}
}

// The slice's headline acceptance criterion: every tuple a target advertises
// must have a fixture, and no fixture may cover a tuple the target does not
// advertise or duplicate one another fixture already covers.
func TestTargetFixtureCoverageMatchesAdvertisedTuplesV1(t *testing.T) {
	contract := composeRichContractV1()
	target := composeRichTargetV1()
	records := composeTestRecordsV1()

	tuples, err := targetSupportTuplesV1(contract, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(tuples) != 2 {
		t.Fatalf("expected two advertised tuples, got %d", len(tuples))
	}

	chromium := composeFixtureV1("debian-12-amd64-chromium", "python", "chromium")
	firefox := composeFixtureV1("debian-12-amd64-firefox", "python", "firefox")

	if err := validateTargetFixtureCoverageV1(records, contract, target,
		[]*IntegrationFixtureRecordV1{chromium, firefox}); err != nil {
		t.Errorf("complete coverage rejected: %v", err)
	}

	err = validateTargetFixtureCoverageV1(records, contract, target, []*IntegrationFixtureRecordV1{chromium})
	if err == nil || !strings.Contains(err.Error(), "do not cover support tuple") {
		t.Errorf("missing firefox fixture error = %v", err)
	}

	err = validateTargetFixtureCoverageV1(records, contract, target, nil)
	if err == nil || !strings.Contains(err.Error(), "do not cover support tuple") {
		t.Errorf("no fixtures at all error = %v", err)
	}

	duplicate := composeFixtureV1("debian-12-amd64-chromium-again", "python", "chromium")
	err = validateTargetFixtureCoverageV1(records, contract, target,
		[]*IntegrationFixtureRecordV1{chromium, firefox, duplicate})
	if err == nil || !strings.Contains(err.Error(), "cover the same support tuple") {
		t.Errorf("duplicate fixture error = %v", err)
	}

	unsupported := composeFixtureV1("debian-12-amd64-webkit", "python", "webkit")
	err = validateTargetFixtureCoverageV1(records, contract, target,
		[]*IntegrationFixtureRecordV1{chromium, firefox, unsupported})
	if err == nil || !strings.Contains(err.Error(), "unsupported tuple") {
		t.Errorf("fixture for an unadvertised tuple error = %v", err)
	}
}

// A fixture that omits its binding or selections inherits the contract
// defaults, so it must normalize to the same tuple an explicit fixture does.
func TestNormalizedFixtureTupleAppliesContractDefaultsV1(t *testing.T) {
	contract := composeRichContractV1()
	contract.Selections.Minimum = "0"
	contract.Selections.Defaults = []string{"chromium"}

	explicit := normalizedFixtureTupleV1(contract, composeFixtureV1("explicit", "python", "chromium"))
	implicit := normalizedFixtureTupleV1(contract, composeFixtureV1("implicit", ""))
	explicitKey, err := supportTupleKeyV1(explicit)
	if err != nil {
		t.Fatal(err)
	}
	implicitKey, err := supportTupleKeyV1(implicit)
	if err != nil {
		t.Fatal(err)
	}
	if explicitKey != implicitKey {
		t.Errorf("defaults did not normalize:\n explicit %s\n implicit %s", explicitKey, implicitKey)
	}

	// A declared parameter default enters the tuple even when the fixture omits it.
	defaultValue := "stable"
	contract.Parameters = []ParameterSchemaV1{{Name: "channel", Type: "enum",
		Values: []string{"beta", "stable"}, Default: &defaultValue}}
	tuple := normalizedFixtureTupleV1(contract, composeFixtureV1("defaulted", "python", "chromium"))
	if len(tuple.Parameters) != 1 || tuple.Parameters[0].Name != "channel" || tuple.Parameters[0].Value != "stable" {
		t.Errorf("parameter default missing from normalized tuple: %+v", tuple.Parameters)
	}
}

func TestFixtureAgainstTargetRejectsUndeclaredAndUnavailableV1(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		mutate  func(*ReleaseContractV1, *TargetRecordV1, *IntegrationFixtureRecordV1)
		wantSub string
	}{
		{name: "context not declared", wantSub: "context",
			mutate: func(c *ReleaseContractV1, target *TargetRecordV1, f *IntegrationFixtureRecordV1) {
				f.Context = "runtime"
			}},
		{name: "binding not declared", wantSub: "binding",
			mutate: func(c *ReleaseContractV1, target *TargetRecordV1, f *IntegrationFixtureRecordV1) {
				f.Binding = "node"
			}},
		{name: "binding unavailable on target", wantSub: "unavailable on the target",
			mutate: func(c *ReleaseContractV1, target *TargetRecordV1, f *IntegrationFixtureRecordV1) {
				target.Bindings = []TargetBindingV1{}
			}},
		{name: "selection not declared", wantSub: "selection",
			mutate: func(c *ReleaseContractV1, target *TargetRecordV1, f *IntegrationFixtureRecordV1) {
				f.Selections = []string{"webkit"}
			}},
		{name: "selection unavailable on target", wantSub: "unavailable on the target",
			mutate: func(c *ReleaseContractV1, target *TargetRecordV1, f *IntegrationFixtureRecordV1) {
				target.Selections = []TargetSelectionV1{}
			}},
		{name: "too many selections", wantSub: "do not satisfy",
			mutate: func(c *ReleaseContractV1, target *TargetRecordV1, f *IntegrationFixtureRecordV1) {
				f.Selections = []string{"chromium", "firefox"}
			}},
		{name: "parameter outside the contract domain", wantSub: "outside the contract or target domain",
			mutate: func(c *ReleaseContractV1, target *TargetRecordV1, f *IntegrationFixtureRecordV1) {
				c.Parameters = []ParameterSchemaV1{{Name: "channel", Type: "enum", Values: []string{"stable"}}}
				f.Parameters = []ParameterValueV1{{Name: "channel", Value: "nightly"}}
			}},
		{name: "parameter outside the target narrowing", wantSub: "outside the contract or target domain",
			mutate: func(c *ReleaseContractV1, target *TargetRecordV1, f *IntegrationFixtureRecordV1) {
				c.Parameters = []ParameterSchemaV1{{Name: "channel", Type: "enum", Values: []string{"beta", "stable"}}}
				target.Parameters = []TargetParameterConstraintV1{{Name: "channel", Values: []string{"stable"}}}
				f.Parameters = []ParameterValueV1{{Name: "channel", Value: "beta"}}
			}},
		{name: "required parameter missing", wantSub: "required parameter",
			mutate: func(c *ReleaseContractV1, target *TargetRecordV1, f *IntegrationFixtureRecordV1) {
				c.Parameters = []ParameterSchemaV1{{Name: "channel", Type: "enum",
					Values: []string{"stable"}, Required: true}}
			}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			contract := composeRichContractV1()
			target := composeRichTargetV1()
			fixture := composeFixtureV1("debian-12-amd64-chromium", "python", "chromium")
			testCase.mutate(contract, target, fixture)
			err := validateFixtureAgainstTargetV1(contract, target, fixture)
			if err == nil || !strings.Contains(err.Error(), testCase.wantSub) {
				t.Errorf("error = %v, want substring %q", err, testCase.wantSub)
			}
		})
	}
}

func TestFixtureAgainstTargetAcceptsAnAdvertisedFixtureV1(t *testing.T) {
	contract := composeRichContractV1()
	target := composeRichTargetV1()
	fixture := composeFixtureV1("debian-12-amd64-chromium", "python", "chromium")
	if err := validateFixtureAgainstTargetV1(contract, target, fixture); err != nil {
		t.Errorf("an advertised fixture was rejected: %v", err)
	}
	// Omitting the binding falls back to the contract default rather than failing.
	fixture.Binding = ""
	if err := validateFixtureAgainstTargetV1(contract, target, fixture); err != nil {
		t.Errorf("fixture relying on the default binding rejected: %v", err)
	}
	// A required binding with no default and no fixture value must fail.
	contract.Binding.Default = ""
	if err := validateFixtureAgainstTargetV1(contract, target, fixture); err == nil {
		t.Error("missing required binding accepted")
	}
}

func TestTargetBindingsAgainstContractsResolveAndCoverV1(t *testing.T) {
	target := composeRichTargetV1()
	records := composeTestRecordsV1()
	if err := validateTargetBindingsAgainstContractsV1(records, target); err != nil {
		t.Errorf("the sample binding and its artifact disagree: %v", err)
	}

	// The contract advertises 3.10 while the only wheel requires >=3.11, so an
	// advertised interpreter has nothing to install.
	contract := *(validRecordValuesV1()[4].(*BindingContractV1))
	contract.SupportedPython = append([]string{"3.10"}, contract.SupportedPython...)
	narrowed := composeTestRecordsV1()
	narrowed[contract.ID] = loadedRecordV1{ID: contract.ID, Schema: contract.Schema, Digest: recordTestDigest, Value: &contract}
	err := validateTargetBindingsAgainstContractsV1(narrowed, target)
	if err == nil || !strings.Contains(err.Error(), "no selected artifact supports it") {
		t.Errorf("uncovered interpreter error = %v", err)
	}

	// An unresolvable artifact reference fails rather than being skipped.
	missing := composeRichTargetV1()
	missing.Bindings = targetBindingWithArtifactV1("tool:demo/releases/1.2.3/bindings/python/artifacts/linux-arm64")
	if err := validateTargetBindingsAgainstContractsV1(records, missing); err == nil {
		t.Error("unresolvable binding artifact accepted")
	}

	// A binding contract reference that resolves to another record type fails.
	mistyped := composeRichTargetV1()
	mistyped.Bindings[0].Contract = recordTestReference("tool:demo/releases/1.2.3/contract")
	if err := validateTargetBindingsAgainstContractsV1(records, mistyped); err == nil {
		t.Error("release contract accepted as a binding contract")
	}
}

func TestPackageSetReferencesRequireAMatchingManagerV1(t *testing.T) {
	records := composeTestRecordsV1()
	target := composeTestTargetV1()
	packageSet := validRecordValuesV1()[8].(*NativePackageSetV1)

	if err := validatePackageSetReferencesV1(records, []RecordReferenceV1{recordTestReference(packageSet.ID)}, target); err != nil {
		t.Errorf("matching apt package set rejected: %v", err)
	}
	if err := validatePackageSetReferencesV1(records, []RecordReferenceV1{recordTestReference("tool:demo/releases/1.2.3/package-sets/absent")}, target); err == nil {
		t.Error("unresolvable package set accepted")
	}
	other := *packageSet
	other.Manager = "apk"
	mismatched := composeTestRecordsV1()
	mismatched[other.ID] = loadedRecordV1{ID: other.ID, Schema: other.Schema, Digest: recordTestDigest, Value: &other}
	err := validatePackageSetReferencesV1(mismatched, []RecordReferenceV1{recordTestReference(other.ID)}, target)
	if err == nil || !strings.Contains(err.Error(), "incompatible package manager") {
		t.Errorf("mismatched package manager error = %v", err)
	}
}

func TestTargetParameterAllowsRespectsNarrowingV1(t *testing.T) {
	enum := []TargetParameterConstraintV1{{Name: "channel", Values: []string{"stable"}}}
	if !targetParameterAllowsV1(enum, "channel", "stable") {
		t.Error("narrowed enum rejected its own value")
	}
	if targetParameterAllowsV1(enum, "channel", "beta") {
		t.Error("narrowed enum accepted an excluded value")
	}
	if !targetParameterAllowsV1(enum, "absent", "anything") {
		t.Error("an unconstrained parameter was narrowed")
	}
	ranged := []TargetParameterConstraintV1{{Name: "port", Values: []string{}, Minimum: "10", Maximum: "20"}}
	if !targetParameterAllowsV1(ranged, "port", "15") {
		t.Error("in-range value rejected")
	}
	if targetParameterAllowsV1(ranged, "port", "21") {
		t.Error("out-of-range value accepted")
	}
}

// A target may narrow a contract parameter but never widen it, change its type,
// or exclude the contract default.
func TestTargetIntegerNarrowingV1(t *testing.T) {
	base := func() (*ReleaseContractV1, *TargetRecordV1) {
		contract := composeTestContractV1()
		contract.Parameters = []ParameterSchemaV1{{Name: "workers", Type: "integer",
			Minimum: "1", Maximum: "8", Values: []string{}}}
		target := composeTestTargetV1()
		target.Parameters = []TargetParameterConstraintV1{{Name: "workers", Values: []string{}, Minimum: "2", Maximum: "4"}}
		return contract, target
	}

	contract, target := base()
	if err := validateTargetAgainstContractV1(contract, target); err != nil {
		t.Errorf("a strictly narrower range was rejected: %v", err)
	}

	contract, target = base()
	target.Parameters[0].Maximum = "9"
	err := validateTargetAgainstContractV1(contract, target)
	if err == nil || !strings.Contains(err.Error(), "widens the contract domain") {
		t.Errorf("widening the maximum error = %v", err)
	}

	contract, target = base()
	target.Parameters[0].Minimum = "0"
	err = validateTargetAgainstContractV1(contract, target)
	if err == nil || !strings.Contains(err.Error(), "widens the contract domain") {
		t.Errorf("widening the minimum error = %v", err)
	}

	// Narrowing must not exclude the contract default.
	contract, target = base()
	defaultValue := "8"
	contract.Parameters[0].Default = &defaultValue
	err = validateTargetAgainstContractV1(contract, target)
	if err == nil || !strings.Contains(err.Error(), "excludes the contract default") {
		t.Errorf("range excluding the default error = %v", err)
	}
	inRange := "3"
	contract.Parameters[0].Default = &inRange
	if err := validateTargetAgainstContractV1(contract, target); err != nil {
		t.Errorf("range containing the default rejected: %v", err)
	}

	// A range constraint cannot be applied to a non-integer parameter.
	contract, target = base()
	contract.Parameters[0] = ParameterSchemaV1{Name: "workers", Type: "enum", Values: []string{"few", "many"}}
	err = validateTargetAgainstContractV1(contract, target)
	if err == nil || !strings.Contains(err.Error(), "incompatible with contract type") {
		t.Errorf("range on an enum parameter error = %v", err)
	}

	// An enum narrowing that drops the contract default is rejected too.
	contract, target = base()
	contract.Parameters[0] = ParameterSchemaV1{Name: "workers", Type: "enum", Values: []string{"few", "many"}, Default: &[]string{"many"}[0]}
	target.Parameters[0] = TargetParameterConstraintV1{Name: "workers", Values: []string{"few"}}
	err = validateTargetAgainstContractV1(contract, target)
	if err == nil || !strings.Contains(err.Error(), "excludes the contract default") {
		t.Errorf("enum narrowing excluding the default error = %v", err)
	}
}

// Domains drive tuple enumeration, so an unconstrained parameter enumerates its
// whole contract domain and a narrowed one enumerates only what survives.
func TestTargetParameterDomainV1(t *testing.T) {
	values := func(domain []*string) []string {
		out := make([]string, 0, len(domain))
		for _, value := range domain {
			if value == nil {
				out = append(out, "<absent>")
				continue
			}
			out = append(out, *value)
		}
		return out
	}

	boolean := ParameterSchemaV1{Name: "headless", Type: "boolean", Required: true, Values: []string{}}
	domain, err := targetParameterDomainV1(boolean, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(values(domain), ","); got != "false,true" {
		t.Errorf("boolean domain = %q", got)
	}

	integer := ParameterSchemaV1{Name: "workers", Type: "integer", Required: true, Minimum: "1", Maximum: "3", Values: []string{}}
	domain, err = targetParameterDomainV1(integer, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(values(domain), ","); got != "1,2,3" {
		t.Errorf("integer domain = %q", got)
	}
	domain, err = targetParameterDomainV1(integer,
		[]TargetParameterConstraintV1{{Name: "workers", Values: []string{}, Minimum: "2", Maximum: "3"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(values(domain), ","); got != "2,3" {
		t.Errorf("narrowed integer domain = %q", got)
	}

	// An optional parameter with no default also enumerates its absence.
	optional := ParameterSchemaV1{Name: "channel", Type: "enum", Values: []string{"beta", "stable"}}
	domain, err = targetParameterDomainV1(optional, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(values(domain), ","); got != "<absent>,beta,stable" {
		t.Errorf("optional enum domain = %q", got)
	}

	if _, err := targetParameterDomainV1(ParameterSchemaV1{Name: "x", Type: "duration", Values: []string{}}, nil); err == nil {
		t.Error("unsupported parameter type produced a domain")
	}
}

// A target leaf owns data specific to one architecture, so a payload it
// installs must be built for that architecture. Record-local validation only
// proves the payload agrees with its own ID.
func TestTuplePayloadsRequireTheTargetPlatformV1(t *testing.T) {
	arm := &PayloadRecordV1{Schema: PayloadRecordSchemaV1, ID: "tool:demo/releases/1.2.3/payloads/demo-linux-arm64",
		Platform: "linux/arm64", LogicalPath: "tools/demo/demo.tar.gz", InstallDirectory: "demo"}
	records := map[string]loadedRecordV1{arm.ID: {ID: arm.ID, Schema: arm.Schema, Digest: recordTestDigest, Value: arm}}
	references := []selectedPayloadReferenceV1{{Reference: recordTestReference(arm.ID)}}

	if err := validateTuplePayloadsV1(records, references, "linux/arm64"); err != nil {
		t.Errorf("matching platform rejected: %v", err)
	}
	err := validateTuplePayloadsV1(records, references, "linux/amd64")
	if err == nil || !strings.Contains(err.Error(), "built for platform") {
		t.Errorf("arm64 payload on an amd64 target error = %v", err)
	}
}

// Selection ownership is declared on the payload, not inferred from the mapping
// that references it, so a chromium entry may not install a firefox payload.
func TestTuplePayloadsRequireDeclaredSelectionOwnershipV1(t *testing.T) {
	const release = "tool:demo/releases/1.2.3"
	firefox := &PayloadRecordV1{Schema: PayloadRecordSchemaV1, ID: release + "/payloads/firefox/browser-linux-amd64",
		Selection: "firefox", Platform: "linux/amd64", LogicalPath: "tools/demo/firefox.zip", InstallDirectory: "firefox"}
	unconditional := &PayloadRecordV1{Schema: PayloadRecordSchemaV1, ID: release + "/payloads/demo-linux-amd64",
		Platform: "linux/amd64", LogicalPath: "tools/demo/demo.tar.gz", InstallDirectory: "demo"}
	records := map[string]loadedRecordV1{
		firefox.ID:       {ID: firefox.ID, Schema: firefox.Schema, Digest: recordTestDigest, Value: firefox},
		unconditional.ID: {ID: unconditional.ID, Schema: unconditional.Schema, Digest: recordTestDigest, Value: unconditional},
	}

	owned := []selectedPayloadReferenceV1{{Reference: recordTestReference(firefox.ID), Selection: "firefox"}}
	if err := validateTuplePayloadsV1(records, owned, "linux/amd64"); err != nil {
		t.Errorf("a payload referenced by its own selection was rejected: %v", err)
	}

	stolen := []selectedPayloadReferenceV1{{Reference: recordTestReference(firefox.ID), Selection: "chromium"}}
	err := validateTuplePayloadsV1(records, stolen, "linux/amd64")
	if err == nil || !strings.Contains(err.Error(), "which belongs to selection") {
		t.Errorf("chromium installing a firefox payload error = %v", err)
	}

	// An unconditional reference must name a payload that belongs to no selection.
	leaked := []selectedPayloadReferenceV1{{Reference: recordTestReference(firefox.ID)}}
	err = validateTuplePayloadsV1(records, leaked, "linux/amd64")
	if err == nil || !strings.Contains(err.Error(), "unconditional target payload") {
		t.Errorf("selection payload referenced unconditionally error = %v", err)
	}

	unconditionalRef := []selectedPayloadReferenceV1{{Reference: recordTestReference(unconditional.ID)}}
	if err := validateTuplePayloadsV1(records, unconditionalRef, "linux/amd64"); err != nil {
		t.Errorf("an unconditional payload was rejected: %v", err)
	}
	claimed := []selectedPayloadReferenceV1{{Reference: recordTestReference(unconditional.ID), Selection: "chromium"}}
	if err := validateTuplePayloadsV1(records, claimed, "linux/amd64"); err == nil {
		t.Error("a selection claimed an unconditional payload")
	}
}
