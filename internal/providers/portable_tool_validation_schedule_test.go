package providers

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func portableToolValidationScheduleDAGFixtureV1(t *testing.T) PortableToolProviderDAGV1 {
	t.Helper()
	dag, err := BuildPortableToolProviderDAGV1(
		portableToolProviderPlanFixtureV1(),
		representativePortableToolPlanV1(),
		[]PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application")},
	)
	if err != nil {
		t.Fatal(err)
	}
	return dag
}

// portableToolValidationScheduleFixtureV1 projects the plan the DAG carries,
// mirroring what the lock-backed public entry point projects.
func portableToolValidationScheduleFixtureV1(t *testing.T) (PortableToolProviderDAGV1, PortableToolValidationScheduleV1) {
	t.Helper()
	dag := portableToolValidationScheduleDAGFixtureV1(t)
	schedule, err := portableToolValidationScheduleFromPlanV1(dag.PortableToolPlan)
	if err != nil {
		t.Fatal(err)
	}
	return dag, schedule
}

func TestPortableToolValidationScheduleCarriesProfilesAndRuntimeProjection(t *testing.T) {
	dag, schedule := portableToolValidationScheduleFixtureV1(t)
	if schedule.Schema != PortableToolValidationScheduleSchemaV1 {
		t.Fatalf("schema = %q", schedule.Schema)
	}
	if len(schedule.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(schedule.Entries))
	}
	entry := schedule.Entries[0]
	source := dag.PortableToolPlan.Tools[0]
	if entry.Scope != source.Scope || entry.Tool != source.Provenance.Tool {
		t.Fatalf("entry identity = %q/%q", entry.Scope, entry.Tool)
	}
	if !reflect.DeepEqual(entry.Profile, source.ValidationProfiles[0]) {
		t.Fatalf("profile = %#v, want %#v", entry.Profile, source.ValidationProfiles[0])
	}
	if entry.Runtime == nil || !reflect.DeepEqual(*entry.Runtime, *source.Runtime) {
		t.Fatalf("runtime = %#v, want %#v", entry.Runtime, source.Runtime)
	}
}

// The schedule must be a projection, never an identity input: mutating it may
// not reach back into the plan it was derived from.
func TestPortableToolValidationScheduleDoesNotAliasThePlan(t *testing.T) {
	dag, schedule := portableToolValidationScheduleFixtureV1(t)
	schedule.Entries[0].Runtime.Environment[0].Value = "/mutated"
	schedule.Entries[0].Profile.Record.Value["schema"] = "mutated"
	if dag.PortableToolPlan.Tools[0].Runtime.Environment[0].Value == "/mutated" {
		t.Fatal("schedule aliases the plan runtime projection")
	}
	if dag.PortableToolPlan.Tools[0].ValidationProfiles[0].Record.Value["schema"] == "mutated" {
		t.Fatal("schedule aliases the plan profile record")
	}
}

// Validation references stay outside selected-closure identity: scheduling a
// closure's profiles must not change the digest that identifies it.
func TestPortableToolValidationScheduleV1LeavesSelectedClosureIdentityUnchanged(t *testing.T) {
	dag, _ := portableToolValidationScheduleFixtureV1(t)
	before := dag.PortableToolPlan.Tools[0].SelectedClosureDigest
	if _, err := portableToolValidationScheduleFromPlanV1(dag.PortableToolPlan); err != nil {
		t.Fatal(err)
	}
	if dag.PortableToolPlan.Tools[0].SelectedClosureDigest != before {
		t.Fatalf("selected closure digest = %s, want %s", dag.PortableToolPlan.Tools[0].SelectedClosureDigest, before)
	}
}

func TestPortableToolValidationScheduleFromLockV1UsesThePersistedPlan(t *testing.T) {
	fixtureDAG, releases, acquisitions := portableToolLockFixtureV1(t)
	lock, err := BuildPortableToolLockV1(fixtureDAG, releases, acquisitions)
	if err != nil {
		t.Fatal(err)
	}
	fromLock, err := PortableToolValidationScheduleFromLockV1(lock)
	if err != nil {
		t.Fatal(err)
	}
	fromPlan, err := portableToolValidationScheduleFromPlanV1(fixtureDAG.PortableToolPlan)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromLock, fromPlan) {
		t.Fatalf("locked schedule = %#v, want %#v", fromLock, fromPlan)
	}
}

func TestPortableToolValidationScheduleFromPlanV1WithoutProfilesIsEmptyNotAbsent(t *testing.T) {
	plan := representativePortableToolPlanV1()
	plan.Tools[0].ValidationProfiles = []PortableToolValidationProfileV1{}
	schedule, err := portableToolValidationScheduleFromPlanV1(plan)
	if err != nil {
		t.Fatal(err)
	}
	if schedule.Schema != PortableToolValidationScheduleSchemaV1 || schedule.Entries == nil || len(schedule.Entries) != 0 {
		t.Fatalf("schedule = %#v", schedule)
	}
}

func TestValidatePortableToolValidationScheduleV1RejectsMalformedSchedules(t *testing.T) {
	_, valid := portableToolValidationScheduleFixtureV1(t)
	for _, test := range []struct {
		name   string
		mutate func(*PortableToolValidationScheduleV1)
		want   string
	}{
		{name: "schema", mutate: func(s *PortableToolValidationScheduleV1) { s.Schema = "other" }, want: "schema must be"},
		{name: "nil entries", mutate: func(s *PortableToolValidationScheduleV1) { s.Entries = nil }, want: "explicit array"},
		{name: "missing scope", mutate: func(s *PortableToolValidationScheduleV1) { s.Entries[0].Scope = "" }, want: "requires a scope"},
		{name: "missing tool", mutate: func(s *PortableToolValidationScheduleV1) { s.Entries[0].Tool = "" }, want: "requires a tool"},
		{
			name:   "profile digest",
			mutate: func(s *PortableToolValidationScheduleV1) { s.Entries[0].Profile.Reference.Digest = "" },
			want:   "digest",
		},
		{
			name:   "profile record schema",
			mutate: func(s *PortableToolValidationScheduleV1) { s.Entries[0].Profile.Record.Schema = "other" },
			want:   "profile record schema must be",
		},
		{
			name: "runtime projection",
			mutate: func(s *PortableToolValidationScheduleV1) {
				s.Entries[0].Runtime.InstallRoot = "relative"
			},
			want: "runtime",
		},
		{
			name: "unsorted entries",
			mutate: func(s *PortableToolValidationScheduleV1) {
				s.Entries = append(s.Entries, s.Entries[0])
			},
			want: "unique and sorted",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, schedule := portableToolValidationScheduleFixtureV1(t)
			test.mutate(&schedule)
			err := ValidatePortableToolValidationScheduleV1(schedule)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
	if err := ValidatePortableToolValidationScheduleV1(valid); err != nil {
		t.Fatalf("valid schedule rejected: %v", err)
	}
}

// Scheduling order is deterministic across scopes, tools, and profiles, so two
// equivalent plans always schedule validation in the same sequence.
func TestPortableToolValidationScheduleFromPlanV1OrdersByScopeThenToolThenProfile(t *testing.T) {
	plan := representativePortableToolPlanV1()
	first := plan.Tools[0]
	first.ValidationProfiles = []PortableToolValidationProfileV1{
		portableToolTestValidationProfile("tool:demo/releases/1.2.3/validation/profiles/b", canonical.Object{"name": "b"}),
		portableToolTestValidationProfile("tool:demo/releases/1.2.3/validation/profiles/a", canonical.Object{"name": "a"}),
	}
	second := first
	second.Provenance.Tool = "alpha"
	second.ValidationProfiles = []PortableToolValidationProfileV1{
		portableToolTestValidationProfile("tool:alpha/releases/1.0.0/validation/profiles/only", canonical.Object{"name": "only"}),
	}
	third := first
	third.Scope = "system"
	third.ValidationProfiles = []PortableToolValidationProfileV1{
		portableToolTestValidationProfile("tool:demo/releases/1.2.3/validation/profiles/sys", canonical.Object{"name": "sys"}),
	}
	plan.Tools = []PortableToolPlanEntryV1{third, first, second}

	schedule, err := portableToolValidationScheduleFromPlanV1(plan)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		scope string
		tool  string
	}{
		{scope: "application", tool: "alpha"},
		{scope: "application", tool: "demo"},
		{scope: "application", tool: "demo"},
		{scope: "system", tool: "demo"},
	}
	if len(schedule.Entries) != len(want) {
		t.Fatalf("entries = %d, want %d", len(schedule.Entries), len(want))
	}
	for index, entry := range schedule.Entries {
		if entry.Scope != want[index].scope || entry.Tool != want[index].tool {
			t.Fatalf("entry %d = %s/%s, want %s/%s", index, entry.Scope, entry.Tool, want[index].scope, want[index].tool)
		}
	}
	// The two application/demo profiles are ordered by their record reference.
	if schedule.Entries[1].Profile.Reference.ID >= schedule.Entries[2].Profile.Reference.ID {
		t.Fatalf("same-tool profiles are unordered: %q then %q",
			schedule.Entries[1].Profile.Reference.ID, schedule.Entries[2].Profile.Reference.ID)
	}
}

func TestPortableToolValidationScheduleForScopeV1SelectsOnlyTheExactScope(t *testing.T) {
	plan := representativePortableToolPlanV1()
	application := plan.Tools[0]
	build := application
	build.Scope = "java-build"
	build.Runtime = nil
	plan.Tools = []PortableToolPlanEntryV1{application, build}
	schedule, err := portableToolValidationScheduleFromPlanV1(plan)
	if err != nil {
		t.Fatal(err)
	}

	selected, err := PortableToolValidationScheduleForScopeV1(schedule, "java-build")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Schema != PortableToolValidationScheduleSchemaV1 || len(selected.Entries) != 1 {
		t.Fatalf("selected schedule = %#v", selected)
	}
	if selected.Entries[0].Scope != "java-build" || selected.Entries[0].Runtime != nil {
		t.Fatalf("selected entry = %#v", selected.Entries[0])
	}
	selected.Entries[0].Profile.Record.Value["schema"] = "mutated"
	if schedule.Entries[1].Profile.Record.Value["schema"] == "mutated" {
		t.Fatal("scope selection aliases the complete schedule")
	}
}

func TestPortableToolValidationScheduleForScopeV1RejectsInvalidInput(t *testing.T) {
	_, schedule := portableToolValidationScheduleFixtureV1(t)
	if _, err := PortableToolValidationScheduleForScopeV1(schedule, ""); err == nil ||
		!strings.Contains(err.Error(), "scope must not be empty") {
		t.Fatalf("empty scope error = %v", err)
	}
	schedule.Schema = "other"
	if _, err := PortableToolValidationScheduleForScopeV1(schedule, "application"); err == nil {
		t.Fatal("invalid schedule was accepted")
	}
}

// A selected closure may declare no contract runtime projection; scheduling
// carries that absence through rather than inventing one.
func TestPortableToolValidationScheduleFromPlanV1CarriesAnAbsentRuntimeProjection(t *testing.T) {
	plan := representativePortableToolPlanV1()
	plan.Tools[0].Runtime = nil
	schedule, err := portableToolValidationScheduleFromPlanV1(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(schedule.Entries) != 1 || schedule.Entries[0].Runtime != nil {
		t.Fatalf("entries = %#v", schedule.Entries)
	}
}

// Scheduling never trusts an unvalidated plan: a malformed DAG or lock is
// rejected before any profile reaches the executor.
func TestPortableToolValidationScheduleRejectsUnvalidatedSources(t *testing.T) {
	fixtureDAG, releases, acquisitions := portableToolLockFixtureV1(t)
	lock, err := BuildPortableToolLockV1(fixtureDAG, releases, acquisitions)
	if err != nil {
		t.Fatal(err)
	}
	lock.Schema = "portable-tool-lock-v0"
	if _, err := PortableToolValidationScheduleFromLockV1(lock); err == nil ||
		!strings.Contains(err.Error(), "portable tool validation schedule") {
		t.Fatalf("lock error = %v", err)
	}
}
