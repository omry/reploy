package providers

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

const portableToolTestDigest = canonical.Digest("sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

func TestValidatePortableToolPlanV1AcceptsRepresentativePlan(t *testing.T) {
	plan := representativePortableToolPlanV1()
	if err := ValidatePortableToolPlanV1(plan); err != nil {
		t.Fatal(err)
	}
	encoded, err := CanonicalPortableToolPlanBytesV1(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"schema":"portable-tool-plan-v1"`)) {
		t.Fatalf("canonical plan = %s", encoded)
	}
}

func TestCanonicalPortableToolPlanBytesV1IsDeterministic(t *testing.T) {
	plan := representativePortableToolPlanV1()
	first, err := CanonicalPortableToolPlanBytesV1(plan)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalPortableToolPlanBytesV1(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("canonical bytes differ:\n%s\n%s", first, second)
	}
}

func TestValidatePortableToolPlanV1RejectsNilCollectionsAndRequiredFields(t *testing.T) {
	valid := representativePortableToolPlanV1()
	tests := []struct {
		name   string
		mutate func(*PortableToolPlanV1)
		want   string
	}{
		{name: "schema", mutate: func(plan *PortableToolPlanV1) { plan.Schema = "portable-tool-plan-v2" }, want: "schema"},
		{name: "nil tools", mutate: func(plan *PortableToolPlanV1) { plan.Tools = nil }, want: "explicit array"},
		{name: "missing closure digest", mutate: func(plan *PortableToolPlanV1) { plan.Tools[0].SelectedClosureDigest = "" }, want: "digest"},
		{name: "missing tool", mutate: func(plan *PortableToolPlanV1) { plan.Tools[0].Provenance.Tool = "" }, want: "tool"},
		{name: "noncanonical tool", mutate: func(plan *PortableToolPlanV1) { plan.Tools[0].Provenance.Tool = "demo_tool" }, want: "match"},
		{name: "missing version", mutate: func(plan *PortableToolPlanV1) { plan.Tools[0].Provenance.Version = "" }, want: "version"},
		{name: "missing revision", mutate: func(plan *PortableToolPlanV1) { plan.Tools[0].Provenance.Revision = "" }, want: "revision"},
		{name: "missing manifest digest", mutate: func(plan *PortableToolPlanV1) { plan.Tools[0].Provenance.ManifestDigest = "" }, want: "digest"},
		{name: "nil exports", mutate: func(plan *PortableToolPlanV1) { plan.Tools[0].Exports = nil }, want: "explicit array"},
		{name: "nil profiles", mutate: func(plan *PortableToolPlanV1) { plan.Tools[0].ValidationProfiles = nil }, want: "explicit array"},
		{name: "nil contracts", mutate: func(plan *PortableToolPlanV1) { plan.Tools[0].Responsibilities.BindingContracts = nil }, want: "explicit array"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := clonePortableToolPlanForTest(valid)
			test.mutate(&candidate)
			if err := ValidatePortableToolPlanV1(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidatePortableToolPlanV1RejectsToolOrderingAndDuplicates(t *testing.T) {
	valid := representativePortableToolPlanV1()
	second := clonePortableToolPlanForTest(valid).Tools[0]
	second.Scope = "source-builder"
	retargetPortableToolTestEntry(&second, "demo", "other")
	valid.Tools = append(valid.Tools, second)
	if err := ValidatePortableToolPlanV1(valid); err != nil {
		t.Fatal(err)
	}
	valid.Tools[0], valid.Tools[1] = valid.Tools[1], valid.Tools[0]
	if err := ValidatePortableToolPlanV1(valid); err == nil || !strings.Contains(err.Error(), "sorted") {
		t.Fatalf("ordering error = %v", err)
	}
	valid = representativePortableToolPlanV1()
	duplicate := clonePortableToolPlanForTest(valid).Tools[0]
	valid.Tools = append(valid.Tools, duplicate)
	if err := ValidatePortableToolPlanV1(valid); err == nil || !strings.Contains(err.Error(), "sorted") {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestValidatePortableToolPlanV1RejectsRecordCategoriesAndIDs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PortableToolPlanV1)
		want   string
	}{
		{name: "record category order", mutate: func(plan *PortableToolPlanV1) {
			item := plan.Tools[0].Responsibilities.BindingContracts[0]
			item.Reference.ID = "tool:demo/releases/1.2.3/a"
			plan.Tools[0].Responsibilities.BindingContracts = append(plan.Tools[0].Responsibilities.BindingContracts, item)
		}, want: "sorted"},
		{name: "record duplicate ID", mutate: func(plan *PortableToolPlanV1) {
			item := plan.Tools[0].Responsibilities.Payloads[0]
			plan.Tools[0].Responsibilities.Payloads = append(plan.Tools[0].Responsibilities.Payloads, item)
		}, want: "sorted"},
		{name: "record cross-category ID", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Responsibilities.BindingArtifacts[0].Reference.ID = "tool:demo/releases/1.2.3/bindings/demo/contract"
		}, want: "both"},
		{name: "record ID whitespace", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Responsibilities.BindingContracts[0].Reference.ID = " tool:demo/releases/1.2.3/contract"
		}, want: "canonical"},
		{name: "record ID dot segment", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Responsibilities.BindingContracts[0].Reference.ID = "tool:demo/releases/./contract"
		}, want: "invalid path segment"},
		{name: "record ID repeated separator", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Responsibilities.BindingContracts[0].Reference.ID = "tool:demo//contract"
		}, want: "invalid path segment"},
		{name: "record ID lowercase percent escape", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Responsibilities.BindingContracts[0].Reference.ID = "tool:demo/releases/1%2f2/contract"
		}, want: "uppercase hexadecimal"},
		{name: "record ID redundant percent escape", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Responsibilities.BindingContracts[0].Reference.ID = "tool:demo/releases/%31.2.3/contract"
		}, want: "not canonical"},
		{name: "record ID escape outside version", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Responsibilities.BindingContracts[0].Reference.ID = "tool:demo/releases/1.2.3/contr%21act"
		}, want: "outside its version"},
		{name: "record ID digest", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Responsibilities.BindingContracts[0].Reference.Digest = "sha256:BAD"
		}, want: "digest"},
		{name: "record envelope schema", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Responsibilities.BindingContracts[0].Record.Schema = ""
		}, want: "schema"},
		{name: "record envelope value", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Responsibilities.BindingContracts[0].Record.Value = nil
		}, want: "non-nil"},
		{name: "record envelope canonical value", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Responsibilities.BindingContracts[0].Record.Value["number"] = 1
		}, want: "canonical"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := clonePortableToolPlanForTest(representativePortableToolPlanV1())
			test.mutate(&candidate)
			if err := ValidatePortableToolPlanV1(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidatePortableToolPlanV1BindsRecordsToCatalogIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PortableToolPlanV1)
		want   string
	}{
		{name: "binding contract category schema", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Responsibilities.BindingContracts[0].Record.Schema = portableToolPayloadSchemaV1
		}, want: portableToolBindingContractSchemaV1},
		{name: "binding artifact category schema", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Responsibilities.BindingArtifacts[0].Record.Schema = portableToolPayloadSchemaV1
		}, want: portableToolBindingArtifactSchemaV1},
		{name: "payload category schema", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Responsibilities.Payloads[0].Record.Schema = portableToolBindingContractSchemaV1
		}, want: portableToolPayloadSchemaV1},
		{name: "package set category schema", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Responsibilities.NativePackageSets[0].Record.Schema = portableToolPayloadSchemaV1
		}, want: portableToolPackageSetSchemaV1},
		{name: "validation profile category schema", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].ValidationProfiles[0].Record.Schema = portableToolPayloadSchemaV1
		}, want: portableToolValidationProfileSchemaV1},
		{name: "carried schema", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Responsibilities.Payloads[0].Record.Value["schema"] = portableToolBindingContractSchemaV1
		}, want: "value schema"},
		{name: "carried ID", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Responsibilities.Payloads[0].Record.Value["id"] = "tool:demo/releases/1.2.3/payloads/other"
		}, want: "value ID"},
		{name: "carried digest", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Responsibilities.Payloads[0].Record.Value["name"] = "changed"
		}, want: "does not match"},
		{name: "validation profile digest", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].ValidationProfiles[0].Record.Value["name"] = "changed"
		}, want: "does not match"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := clonePortableToolPlanForTest(representativePortableToolPlanV1())
			test.mutate(&candidate)
			if err := ValidatePortableToolPlanV1(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidatePortableToolPlanV1BindsRecordsToReleaseProvenance(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PortableToolPlanV1)
		want   string
	}{
		{name: "other tool", mutate: func(plan *PortableToolPlanV1) {
			selected := &plan.Tools[0].Responsibilities.Payloads[0]
			setPortableToolTestRecordID(&selected.Reference, &selected.Record, "tool:other/releases/1.2.3/payloads/demo")
		}, want: "release namespace"},
		{name: "other version", mutate: func(plan *PortableToolPlanV1) {
			selected := &plan.Tools[0].Responsibilities.Payloads[0]
			setPortableToolTestRecordID(&selected.Reference, &selected.Record, "tool:demo/releases/2.0.0/payloads/demo")
		}, want: "release namespace"},
		{name: "tool root", mutate: func(plan *PortableToolPlanV1) {
			selected := &plan.Tools[0].Responsibilities.Payloads[0]
			setPortableToolTestRecordID(&selected.Reference, &selected.Record, "tool:demo")
		}, want: "release namespace"},
		{name: "wrong responsibility namespace", mutate: func(plan *PortableToolPlanV1) {
			selected := &plan.Tools[0].Responsibilities.Payloads[0]
			setPortableToolTestRecordID(&selected.Reference, &selected.Record, "tool:demo/releases/1.2.3/bindings/other/contract")
		}, want: portableToolPayloadSchemaV1},
		{name: "wrong validation namespace", mutate: func(plan *PortableToolPlanV1) {
			profile := &plan.Tools[0].ValidationProfiles[0]
			setPortableToolTestRecordID(&profile.Reference, &profile.Record, "tool:demo/releases/1.2.3/validation/default")
		}, want: portableToolValidationProfileSchemaV1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := clonePortableToolPlanForTest(representativePortableToolPlanV1())
			test.mutate(&candidate)
			if err := ValidatePortableToolPlanV1(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidatePortableToolPlanV1RejectsRuntimeAndExportConflicts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PortableToolPlanV1)
		want   string
	}{
		{name: "runtime relative path", mutate: func(plan *PortableToolPlanV1) { plan.Tools[0].Runtime.InstallRoot = "opt/demo" }, want: "absolute"},
		{name: "runtime aliased path", mutate: func(plan *PortableToolPlanV1) { plan.Tools[0].Runtime.InstallRoot = "/opt/./demo" }, want: "normalized"},
		{name: "runtime env order", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Runtime.Environment[0], plan.Tools[0].Runtime.Environment[1] = plan.Tools[0].Runtime.Environment[1], plan.Tools[0].Runtime.Environment[0]
		}, want: "sorted"},
		{name: "runtime env conflict", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Runtime.Environment[1].Name = plan.Tools[0].Runtime.Environment[0].Name
		}, want: "sorted"},
		{name: "runtime env identifier", mutate: func(plan *PortableToolPlanV1) { plan.Tools[0].Runtime.Environment[0].Name = "BAD-NAME" }, want: "invalid"},
		{name: "runtime env lowercase", mutate: func(plan *PortableToolPlanV1) { plan.Tools[0].Runtime.Environment[0].Name = "demo_home" }, want: "invalid"},
		{name: "export order", mutate: func(plan *PortableToolPlanV1) {
			plan.Tools[0].Exports[0], plan.Tools[0].Exports[1] = plan.Tools[0].Exports[1], plan.Tools[0].Exports[0]
		}, want: "sorted"},
		{name: "export conflict", mutate: func(plan *PortableToolPlanV1) { plan.Tools[0].Exports[1].Name = plan.Tools[0].Exports[0].Name }, want: "sorted"},
		{name: "export path", mutate: func(plan *PortableToolPlanV1) { plan.Tools[0].Exports[0].Path = "bin/demo" }, want: "absolute"},
		{name: "export control path", mutate: func(plan *PortableToolPlanV1) { plan.Tools[0].Exports[0].Path = "/opt/demo\n/bin" }, want: "normalized"},
		{name: "export noncanonical name", mutate: func(plan *PortableToolPlanV1) { plan.Tools[0].Exports[0].Name = "demo_tool" }, want: "match"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := clonePortableToolPlanForTest(representativePortableToolPlanV1())
			test.mutate(&candidate)
			if err := ValidatePortableToolPlanV1(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidatePortableToolPlanV1RejectsCrossToolContributionConflicts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PortableToolPlanEntryV1)
		want   string
	}{
		{name: "environment", mutate: func(entry *PortableToolPlanEntryV1) {
			entry.Runtime.Environment[0].Value = "/opt/other"
		}, want: "conflicting environment"},
		{name: "export", mutate: func(entry *PortableToolPlanEntryV1) {
			entry.Exports[0].Path = "/opt/other/bin/demo"
		}, want: "conflicting export"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := representativePortableToolPlanV1()
			second := clonePortableToolPlanForTest(plan).Tools[0]
			retargetPortableToolTestEntry(&second, "demo", "other")
			test.mutate(&second)
			plan.Tools = append(plan.Tools, second)
			if err := ValidatePortableToolPlanV1(plan); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidatePortableToolPlanV1AllowsIdenticalCrossToolContributions(t *testing.T) {
	plan := representativePortableToolPlanV1()
	second := clonePortableToolPlanForTest(plan).Tools[0]
	retargetPortableToolTestEntry(&second, "demo", "other")
	plan.Tools = append(plan.Tools, second)
	if err := ValidatePortableToolPlanV1(plan); err != nil {
		t.Fatal(err)
	}
}

func TestPortableToolValidationProfilesAreOutsideClosureIdentity(t *testing.T) {
	first := representativePortableToolPlanV1()
	second := clonePortableToolPlanForTest(first)
	second.Tools[0].ValidationProfiles[0].Record.Value["revision"] = "changed"
	refreshPortableToolTestRecordDigest(
		&second.Tools[0].ValidationProfiles[0].Reference,
		second.Tools[0].ValidationProfiles[0].Record,
	)
	if first.Tools[0].SelectedClosureDigest != second.Tools[0].SelectedClosureDigest {
		t.Fatal("validation profile changed selected closure identity")
	}
	firstBytes, err := CanonicalPortableToolPlanBytesV1(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := CanonicalPortableToolPlanBytesV1(second)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("validation profile change did not change canonical plan bytes")
	}
}

func TestValidatePortableToolPlanV1AcceptsCanonicalEscapedVersionRecordID(t *testing.T) {
	plan := representativePortableToolPlanV1()
	reversionPortableToolTestEntry(&plan.Tools[0], "1.2.3", "1!2.3")
	if err := ValidatePortableToolPlanV1(plan); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePortableToolPlanV1AllowsAggregateCollectionsAboveLegacyLimit(t *testing.T) {
	const count = 257
	plan := representativePortableToolPlanV1()
	plan.Tools = make([]PortableToolPlanEntryV1, 0, count)
	for index := 0; index < count; index++ {
		entry := clonePortableToolPlanForTest(representativePortableToolPlanV1()).Tools[0]
		retargetPortableToolTestEntry(&entry, "demo", fmt.Sprintf("demo-%03d", index))
		plan.Tools = append(plan.Tools, entry)
	}
	if err := ValidatePortableToolPlanV1(plan); err != nil {
		t.Fatalf("large tool collection: %v", err)
	}

	plan = representativePortableToolPlanV1()
	plan.Tools[0].Responsibilities.Payloads = make([]PortableToolSelectedRecordV1, 0, count)
	for index := 0; index < count; index++ {
		name := fmt.Sprintf("payload-%03d", index)
		plan.Tools[0].Responsibilities.Payloads = append(
			plan.Tools[0].Responsibilities.Payloads,
			portableToolTestSelectedRecord(
				portableToolPayloadSchemaV1,
				"tool:demo/releases/1.2.3/payloads/"+name,
				canonical.Object{"name": name},
			),
		)
	}
	if err := ValidatePortableToolPlanV1(plan); err != nil {
		t.Fatalf("large record collection: %v", err)
	}
}

func representativePortableToolPlanV1() PortableToolPlanV1 {
	return PortableToolPlanV1{
		Schema: PortableToolPlanSchemaV1,
		Tools: []PortableToolPlanEntryV1{{
			Scope:                 "application",
			SelectedClosureDigest: portableToolTestDigest,
			Provenance: PortableToolReleaseProvenanceV1{
				Tool: "demo", Version: "1.2.3", Revision: "1", ManifestDigest: portableToolTestDigest,
			},
			Runtime: &PortableToolRuntimeProjectionV1{
				InstallRoot: "/opt/demo",
				Environment: []PortableToolEnvironmentVariableV1{{Name: "DEMO_HOME", Value: "/opt/demo"}, {Name: "PATH", Value: "/opt/demo/bin"}},
			},
			Responsibilities: PortableToolResponsibilitiesV1{
				BindingContracts: []PortableToolSelectedRecordV1{portableToolTestSelectedRecord(
					portableToolBindingContractSchemaV1,
					"tool:demo/releases/1.2.3/bindings/demo/contract",
					canonical.Object{"name": "demo"},
				)},
				BindingArtifacts: []PortableToolSelectedRecordV1{portableToolTestSelectedRecord(
					portableToolBindingArtifactSchemaV1,
					"tool:demo/releases/1.2.3/bindings/demo/artifacts/linux-amd64",
					canonical.Object{"name": "demo"},
				)},
				Payloads: []PortableToolSelectedRecordV1{portableToolTestSelectedRecord(
					portableToolPayloadSchemaV1,
					"tool:demo/releases/1.2.3/payloads/demo",
					canonical.Object{"name": "demo"},
				)},
				NativePackageSets: []PortableToolSelectedRecordV1{portableToolTestSelectedRecord(
					portableToolPackageSetSchemaV1,
					"tool:demo/releases/1.2.3/package-sets/default",
					canonical.Object{"manager": "apt"},
				)},
			},
			Exports: []PortableToolExportV1{{Name: "demo", Path: "/opt/demo/bin/demo"}, {Name: "helper", Path: "/opt/demo/bin/helper"}},
			ValidationProfiles: []PortableToolValidationProfileV1{portableToolTestValidationProfile(
				"tool:demo/releases/1.2.3/validation/profiles/default",
				canonical.Object{"name": "default"},
			)},
		}},
	}
}

func portableToolTestSelectedRecord(schema string, id string, fields canonical.Object) PortableToolSelectedRecordV1 {
	record := portableToolTestRecord(schema, id, fields)
	reference := PortableToolRecordReferenceV1{ID: id}
	refreshPortableToolTestRecordDigest(&reference, record)
	return PortableToolSelectedRecordV1{Reference: reference, Record: record}
}

func portableToolTestValidationProfile(id string, fields canonical.Object) PortableToolValidationProfileV1 {
	record := portableToolTestRecord(portableToolValidationProfileSchemaV1, id, fields)
	reference := PortableToolRecordReferenceV1{ID: id}
	refreshPortableToolTestRecordDigest(&reference, record)
	return PortableToolValidationProfileV1{Reference: reference, Record: record}
}

func portableToolTestRecord(schema string, id string, fields canonical.Object) CanonicalProviderData {
	value := canonical.Object{"schema": schema, "id": id}
	for name, field := range fields {
		value[name] = field
	}
	return CanonicalProviderData{Schema: schema, Value: value}
}

func refreshPortableToolTestRecordDigest(reference *PortableToolRecordReferenceV1, record CanonicalProviderData) {
	digest, err := canonical.Sum(portableToolRecordIdentityKindV1, portableToolRecordIdentitySchemaV1, record.Value)
	if err != nil {
		panic(err)
	}
	reference.Digest = digest
}

func setPortableToolTestRecordID(
	reference *PortableToolRecordReferenceV1,
	record *CanonicalProviderData,
	id string,
) {
	reference.ID = id
	record.Value["id"] = id
	refreshPortableToolTestRecordDigest(reference, *record)
}

func retargetPortableToolTestEntry(entry *PortableToolPlanEntryV1, oldTool string, newTool string) {
	entry.Provenance.Tool = newTool
	for _, records := range [][]PortableToolSelectedRecordV1{
		entry.Responsibilities.BindingContracts,
		entry.Responsibilities.BindingArtifacts,
		entry.Responsibilities.Payloads,
		entry.Responsibilities.NativePackageSets,
	} {
		for index := range records {
			retargetPortableToolTestRecord(
				&records[index].Reference,
				&records[index].Record,
				oldTool,
				newTool,
			)
		}
	}
	for index := range entry.ValidationProfiles {
		retargetPortableToolTestRecord(
			&entry.ValidationProfiles[index].Reference,
			&entry.ValidationProfiles[index].Record,
			oldTool,
			newTool,
		)
	}
}

func retargetPortableToolTestRecord(
	reference *PortableToolRecordReferenceV1,
	record *CanonicalProviderData,
	oldTool string,
	newTool string,
) {
	setPortableToolTestRecordID(
		reference,
		record,
		strings.Replace(reference.ID, "tool:"+oldTool+"/", "tool:"+newTool+"/", 1),
	)
}

func reversionPortableToolTestEntry(entry *PortableToolPlanEntryV1, oldVersion string, newVersion string) {
	oldEncoded, err := encodePortableToolVersionSegment(oldVersion)
	if err != nil {
		panic(err)
	}
	newEncoded, err := encodePortableToolVersionSegment(newVersion)
	if err != nil {
		panic(err)
	}
	entry.Provenance.Version = newVersion
	for _, records := range [][]PortableToolSelectedRecordV1{
		entry.Responsibilities.BindingContracts,
		entry.Responsibilities.BindingArtifacts,
		entry.Responsibilities.Payloads,
		entry.Responsibilities.NativePackageSets,
	} {
		for index := range records {
			setPortableToolTestRecordID(
				&records[index].Reference,
				&records[index].Record,
				strings.Replace(records[index].Reference.ID, "/releases/"+oldEncoded+"/", "/releases/"+newEncoded+"/", 1),
			)
		}
	}
	for index := range entry.ValidationProfiles {
		setPortableToolTestRecordID(
			&entry.ValidationProfiles[index].Reference,
			&entry.ValidationProfiles[index].Record,
			strings.Replace(entry.ValidationProfiles[index].Reference.ID, "/releases/"+oldEncoded+"/", "/releases/"+newEncoded+"/", 1),
		)
	}
}

func clonePortableToolPlanForTest(plan PortableToolPlanV1) PortableToolPlanV1 {
	result := plan
	result.Tools = append([]PortableToolPlanEntryV1(nil), plan.Tools...)
	for index := range result.Tools {
		entry := &result.Tools[index]
		entry.Exports = append([]PortableToolExportV1(nil), plan.Tools[index].Exports...)
		entry.ValidationProfiles = append([]PortableToolValidationProfileV1(nil), plan.Tools[index].ValidationProfiles...)
		if plan.Tools[index].Runtime != nil {
			runtime := *plan.Tools[index].Runtime
			runtime.Environment = append([]PortableToolEnvironmentVariableV1(nil), plan.Tools[index].Runtime.Environment...)
			entry.Runtime = &runtime
		}
		entry.Responsibilities.BindingContracts = clonePortableToolRecordsForTest(plan.Tools[index].Responsibilities.BindingContracts)
		entry.Responsibilities.BindingArtifacts = clonePortableToolRecordsForTest(plan.Tools[index].Responsibilities.BindingArtifacts)
		entry.Responsibilities.Payloads = clonePortableToolRecordsForTest(plan.Tools[index].Responsibilities.Payloads)
		entry.Responsibilities.NativePackageSets = clonePortableToolRecordsForTest(plan.Tools[index].Responsibilities.NativePackageSets)
		for profileIndex := range entry.ValidationProfiles {
			value := canonical.Object{}
			for key, item := range entry.ValidationProfiles[profileIndex].Record.Value {
				value[key] = item
			}
			entry.ValidationProfiles[profileIndex].Record.Value = value
		}
	}
	return result
}

func clonePortableToolRecordsForTest(records []PortableToolSelectedRecordV1) []PortableToolSelectedRecordV1 {
	result := append([]PortableToolSelectedRecordV1(nil), records...)
	for index := range result {
		value := canonical.Object{}
		for key, item := range result[index].Record.Value {
			value[key] = item
		}
		result[index].Record.Value = value
	}
	return result
}
