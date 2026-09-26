package toolcatalog

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func TestDeriveIntegrationCasesV1UsesExactAdvertisedTuples(t *testing.T) {
	cases, err := EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 7 { // Four Java build leaves and three Playwright runtime leaves.
		t.Fatalf("derived %d cases, want 7", len(cases))
	}
	seen := make(map[string]bool)
	for _, item := range cases {
		if err := item.ID.Validate(); err != nil {
			t.Errorf("case has invalid identity: %v", err)
		}
		identityInput := struct {
			Manifest RecordReferenceV1   `json:"manifest"`
			Target   RecordReferenceV1   `json:"target"`
			Support  TargetSupportCaseV1 `json:"support"`
		}{item.ManifestReference, item.TargetReference, item.Support}
		wantID, err := canonical.Sum(integrationCaseIdentityKindV1, integrationCaseIdentitySchemaV1, identityInput)
		if err != nil {
			t.Fatalf("case %s identity recomputation failed: %v", item.ID, err)
		}
		if item.ID != wantID {
			t.Errorf("case %s identity does not match its canonical projection; want %s", item.ID, wantID)
		}
		if seen[string(item.ID)] {
			t.Errorf("case %s was repeated", item.ID)
		}
		seen[string(item.ID)] = true
		if item.Support.Context != item.Fixture.Context ||
			!reflect.DeepEqual(item.Support.Bindings, item.Fixture.Bindings) ||
			!reflect.DeepEqual(item.Support.Selections, item.Fixture.Selections) ||
			item.Target.Target != item.Fixture.Target || len(item.Profiles) != 1 {
			t.Errorf("case %s does not match its exact fixture and profile", item.ID)
		}
		if item.Manifest.Tool == "java" && item.Support.Context != "build" {
			t.Errorf("Java case %s invented a non-build context", item.ID)
		}
		if item.Manifest.Tool == "playwright" && item.Support.Context != "runtime" {
			t.Errorf("Playwright case %s invented a non-runtime context", item.ID)
		}
	}
	again, err := EmbeddedIntegrationCasesV1()
	if err != nil || !reflect.DeepEqual(cases, again) {
		t.Fatalf("case derivation is not deterministic: %v", err)
	}
	// Returned records must not provide a mutable backdoor into the catalog.
	cases[0].Support.Bindings = []string{"mutated"}
	cases[0].Fixture.ValidationProfiles = nil
	afterMutation, err := EmbeddedIntegrationCasesV1()
	if err != nil || !reflect.DeepEqual(again, afterMutation) {
		t.Fatalf("case derivation shares catalog storage: %v", err)
	}
}

func TestDeriveIntegrationCasesV1FailsCoverageBeforeExecution(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*TargetRecordV1)
		want   string
	}{
		{"missing fixture", func(target *TargetRecordV1) { target.IntegrationFixtures = nil }, "integration fixtures"},
		{"unsupported context", func(target *TargetRecordV1) { target.SupportCases[0].Context = "runtime" }, "context"},
		{"duplicate support tuple", func(target *TargetRecordV1) {
			target.SupportCases = append(target.SupportCases, target.SupportCases[0])
		}, "duplicated"},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog, err := loadCatalogV1(definitionFilesV1, "definitions")
			if err != nil {
				t.Fatal(err)
			}
			for key, record := range catalog.records {
				target, ok := record.Value.(*TargetRecordV1)
				if !ok || !strings.HasPrefix(target.ID, "tool:java/") {
					continue
				}
				changed := cloneTargetRecordV1(target)
				test.mutate(&changed)
				record.Value = &changed
				catalog.records[key] = record
				break
			}
			if _, err := catalog.DeriveIntegrationCasesV1(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("preflight error = %v, want %q", err, test.want)
			}
		})
	}
}
