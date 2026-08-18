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
		return &PayloadRecordV1{Schema: PayloadRecordSchemaV1, ID: id, LogicalPath: logical, InstallDirectory: install}
	}
	build := func(payloads ...*PayloadRecordV1) (map[string]loadedRecordV1, []RecordReferenceV1) {
		records := make(map[string]loadedRecordV1)
		references := make([]RecordReferenceV1, 0, len(payloads))
		for _, value := range payloads {
			records[value.ID] = loadedRecordV1{ID: value.ID, Schema: value.Schema, Digest: recordTestDigest, Value: value}
			references = append(references, recordTestReference(value.ID))
		}
		return records, references
	}

	records, references := build(
		payload("chromium", "tools/demo/chromium.zip", "chromium"),
		payload("headless", "tools/demo/headless.zip", "headless-shell"),
		payload("ffmpeg", "tools/demo/ffmpeg.zip", "ffmpeg"))
	if err := validateTuplePayloadsV1(records, references); err != nil {
		t.Errorf("distinct coupled payloads rejected: %v", err)
	}

	// The carried finding from retired PR 83, first half: a shared logical path.
	records, references = build(
		payload("chromium", "tools/demo/browser.zip", "chromium"),
		payload("headless", "tools/demo/browser.zip", "headless-shell"))
	err := validateTuplePayloadsV1(records, references)
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
			err := validateTuplePayloadsV1(records, references)
			if err == nil || !strings.Contains(err.Error(), "overlap install destinations") {
				t.Errorf("error = %v, want an overlap rejection", err)
			}
		})
	}

	// A shared unowned parent is allowed: neither owns the other's tree.
	records, references = build(
		payload("left", "tools/demo/left.zip", "browsers/chromium"),
		payload("right", "tools/demo/right.zip", "browsers/firefox"))
	if err := validateTuplePayloadsV1(records, references); err != nil {
		t.Errorf("siblings under an unowned parent rejected: %v", err)
	}

	// A prefix that is not a segment boundary is not containment.
	records, references = build(
		payload("left", "tools/demo/left.zip", "chromium"),
		payload("right", "tools/demo/right.zip", "chromium-headless"))
	if err := validateTuplePayloadsV1(records, references); err != nil {
		t.Errorf("non-boundary prefix treated as overlap: %v", err)
	}
}

func TestTuplePayloadsRejectUnresolvableAndMistypedReferencesV1(t *testing.T) {
	records := composeTestRecordsV1()
	if err := validateTuplePayloadsV1(records, []RecordReferenceV1{recordTestReference("tool:demo/releases/1.2.3/payloads/absent")}); err == nil {
		t.Error("unresolvable payload reference accepted")
	}
	contract := composeTestContractV1()
	if err := validateTuplePayloadsV1(records, []RecordReferenceV1{recordTestReference(contract.ID)}); err == nil {
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
	target.Probes[1].Args = []string{"--help"}
	err := validateTupleContributionsV1(records, contract, target, tuple)
	if err == nil || !strings.Contains(err.Error(), "conflict on probe") {
		t.Errorf("conflicting probe args error = %v", err)
	}
}

// Unselected contributions never enter a tuple, so a collision that only exists
// between two different selections is not a collision in either tuple.
func TestUnselectedContributionsNeverLeakIntoATupleV1(t *testing.T) {
	const release = "tool:demo/releases/1.2.3"
	colliding := func(id string, selection string) *PayloadRecordV1 {
		return &PayloadRecordV1{Schema: PayloadRecordSchemaV1, ID: id, Selection: selection,
			LogicalPath: "tools/demo/browser.zip", InstallDirectory: "browser"}
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

func TestTargetAgainstContractRequiresExactContributionMappingsV1(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		mutate  func(*ReleaseContractV1, *TargetRecordV1)
		wantSub string
	}{
		{name: "missing binding mapping", wantSub: "every contract binding option",
			mutate: func(c *ReleaseContractV1, target *TargetRecordV1) {
				c.Binding.Options = []string{"python"}
				target.Bindings = []TargetBindingV1{}
			}},
		{name: "binding mapping name mismatch", wantSub: "exactly match contract binding options",
			mutate: func(c *ReleaseContractV1, target *TargetRecordV1) {
				c.Binding.Options = []string{"python"}
				target.Bindings = []TargetBindingV1{{Name: "node"}}
			}},
		{name: "missing selection mapping", wantSub: "every contract selection option",
			mutate: func(c *ReleaseContractV1, target *TargetRecordV1) {
				c.Selections.Options = []string{"chromium"}
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
