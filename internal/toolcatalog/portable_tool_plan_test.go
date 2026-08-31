package toolcatalog

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

func TestCompilePortableToolPlanV1ProjectsFullClosure(t *testing.T) {
	closure := portableToolCompilerClosureV1("scope-a", "demo", "1.2.3")
	plan, err := CompilePortableToolPlanV1([]SelectedClosureV1{closure})
	if err != nil {
		t.Fatal(err)
	}
	if err := providers.ValidatePortableToolPlanV1(plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(plan.Tools))
	}
	entry := plan.Tools[0]
	if entry.Scope != closure.Scope || entry.SelectedClosureDigest != closure.Identity {
		t.Fatalf("identity projection = %#v", entry)
	}
	if !reflect.DeepEqual(entry.Provenance, providers.PortableToolReleaseProvenanceV1{
		Tool: closure.Provenance.Tool, Version: closure.Provenance.Version,
		Revision: closure.Provenance.Revision, ManifestDigest: closure.Provenance.ManifestDigest,
	}) {
		t.Fatalf("provenance = %#v", entry.Provenance)
	}
	if entry.Runtime == nil || entry.Runtime.InstallRoot != "/opt/demo" ||
		!reflect.DeepEqual(entry.Runtime.Environment, []providers.PortableToolEnvironmentVariableV1{
			{Name: "ALPHA", Value: "a"}, {Name: "ZETA", Value: "z"},
		}) {
		t.Fatalf("runtime = %#v", entry.Runtime)
	}
	if got := len(entry.Responsibilities.BindingContracts); got != 1 {
		t.Fatalf("binding contracts = %d, want 1", got)
	}
	if got := len(entry.Responsibilities.BindingArtifacts); got != 1 {
		t.Fatalf("binding artifacts = %d, want 1", got)
	}
	if got := len(entry.Responsibilities.Payloads); got != 1 {
		t.Fatalf("payloads = %d, want 1", got)
	}
	if got := len(entry.Responsibilities.NativePackageSets); got != 1 {
		t.Fatalf("package sets = %d, want 1", got)
	}
	if !reflect.DeepEqual(entry.Exports, []providers.PortableToolExportV1{
		{Name: "demo", Path: "/opt/demo/bin/demo"},
		{Name: "python", Path: "/opt/demo/bin/python"},
	}) {
		t.Fatalf("exports = %#v", entry.Exports)
	}
	if len(entry.ValidationProfiles) != 2 ||
		entry.ValidationProfiles[0].Reference.ID >= entry.ValidationProfiles[1].Reference.ID {
		t.Fatalf("profiles are not ordered: %#v", entry.ValidationProfiles)
	}
	for _, selected := range entry.Responsibilities.BindingContracts {
		if selected.Record.Schema != BindingContractSchemaV1 || selected.Record.Value["id"] != selected.Reference.ID {
			t.Fatalf("binding contract envelope = %#v", selected)
		}
	}
}

func TestCompilePortableToolPlanV1IsOrderingInvariantAndDoesNotMutateInput(t *testing.T) {
	first := portableToolCompilerClosureV1("scope-a", "demo", "1.2.3")
	second := portableToolCompilerClosureV1("scope-b", "demo", "1.2.3")
	portableToolCompilerAddSecondRecordSetV1(&first)
	portableToolCompilerAddSecondRecordSetV1(&second)
	ordered, err := CompilePortableToolPlanV1([]SelectedClosureV1{first, second})
	if err != nil {
		t.Fatal(err)
	}
	// Reorder every input collection that the compiler projects, including
	// duplicate semantic exports and environment entries.
	portableToolCompilerReverseClosureV1(&first)
	portableToolCompilerReverseClosureV1(&second)
	reversed, err := CompilePortableToolPlanV1([]SelectedClosureV1{second, first})
	if err != nil {
		t.Fatal(err)
	}
	orderedBytes, err := providers.CanonicalPortableToolPlanBytesV1(ordered)
	if err != nil {
		t.Fatal(err)
	}
	reversedBytes, err := providers.CanonicalPortableToolPlanBytesV1(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(orderedBytes, reversedBytes) {
		t.Fatalf("ordering changed canonical plan:\n%s\n%s", orderedBytes, reversedBytes)
	}
	// The compiler must not sort or otherwise rewrite caller-owned closures.
	beforeCompile := first
	beforeCompileBytes, err := canonical.Marshal(beforeCompile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CompilePortableToolPlanV1([]SelectedClosureV1{beforeCompile}); err != nil {
		t.Fatal(err)
	}
	afterCompileBytes, err := canonical.Marshal(beforeCompile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeCompileBytes, afterCompileBytes) {
		t.Fatal("compiler mutated closure input")
	}
}

func TestCompilePortableToolPlanV1RejectsCrossToolConflicts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SelectedClosureV1)
		want   string
	}{
		{name: "environment", mutate: func(closure *SelectedClosureV1) {
			for index := range closure.Contract.Runtime.Environment {
				if closure.Contract.Runtime.Environment[index].Name == "ALPHA" {
					closure.Contract.Runtime.Environment[index].Value = "different"
				}
			}
		}, want: "conflicting environment"},
		{name: "export", mutate: func(closure *SelectedClosureV1) {
			closure.Target.Exports[0].Path = "/opt/other/bin/demo"
			closure.Target.Selections[0].Exports[0].Path = "/opt/other/bin/demo"
		}, want: "conflicting export"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			first := portableToolCompilerClosureV1("scope-a", "demo", "1.2.3")
			second := portableToolCompilerClosureV1("scope-a", "other", "2.0.0")
			test.mutate(&second)
			if _, err := CompilePortableToolPlanV1([]SelectedClosureV1{first, second}); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCompilePortableToolPlanV1SelectedChangesAlterOutput(t *testing.T) {
	base := portableToolCompilerClosureV1("scope-a", "demo", "1.2.3")
	baseBytes, err := portableToolPlanBytesV1(base)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*SelectedClosureV1)
	}{
		{name: "selected behavior", mutate: func(closure *SelectedClosureV1) {
			closure.Identity = portableToolCompilerDigestV1("changed-selected-behavior")
			closure.Contract.Selections["browser"] = []string{"firefox"}
		}},
		{name: "provenance", mutate: func(closure *SelectedClosureV1) {
			closure.Provenance.Revision = "2"
		}},
		{name: "closure contribution", mutate: func(closure *SelectedClosureV1) {
			closure.Records.Payloads[0].Record.Name = "changed"
			closure.Records.Payloads[0].Reference.Digest = portableToolCompilerReferenceV1(closure.Records.Payloads[0].Record).Digest
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := portableToolCompilerClosureV1("scope-a", "demo", "1.2.3")
			test.mutate(&candidate)
			changed, err := portableToolPlanBytesV1(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(baseBytes, changed) {
				t.Fatal("selected change did not alter compiled plan")
			}
		})
	}
}

func TestCompilePortableToolPlanV1RejectsConflictingRuntimeAndExports(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SelectedClosureV1)
		want   string
	}{
		{name: "environment", mutate: func(closure *SelectedClosureV1) {
			closure.Contract.Runtime.Environment = append(closure.Contract.Runtime.Environment,
				RecordEnvironmentVariableV1{Name: "ALPHA", Value: "different"})
		}, want: "conflicting values"},
		{name: "export", mutate: func(closure *SelectedClosureV1) {
			closure.Target.Exports = append(closure.Target.Exports, ToolExportV1{Name: "demo", Path: "/other/demo"})
		}, want: "conflicting paths"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			closure := portableToolCompilerClosureV1("scope-a", "demo", "1.2.3")
			test.mutate(&closure)
			if _, err := CompilePortableToolPlanV1([]SelectedClosureV1{closure}); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCompilePortableToolPlanV1FinalValidationRejectsReferenceMismatch(t *testing.T) {
	closure := portableToolCompilerClosureV1("scope-a", "demo", "1.2.3")
	closure.Records.Payloads[0].Reference.Digest = portableToolCompilerDigestV1("wrong-reference")
	if _, err := CompilePortableToolPlanV1([]SelectedClosureV1{closure}); err == nil ||
		!strings.Contains(err.Error(), "does not match carried record digest") {
		t.Fatalf("error = %v, want carried digest mismatch", err)
	}
}

func TestCompilePortableToolPlanV1EmptyInputAndProfileReferences(t *testing.T) {
	if _, err := CompilePortableToolPlanV1(nil); err == nil || !strings.Contains(err.Error(), "at least one entry") {
		t.Fatalf("empty input error = %v", err)
	}
	closure := portableToolCompilerClosureV1("scope-a", "demo", "1.2.3")
	plan, err := CompilePortableToolPlanV1([]SelectedClosureV1{closure})
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range plan.Tools[0].ValidationProfiles {
		if profile.Reference.ID == "" || profile.Reference.Digest == "" || profile.Record.Value["id"] != profile.Reference.ID {
			t.Fatalf("profile reference = %#v", profile)
		}
		digest, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, profile.Record.Value)
		if err != nil {
			t.Fatal(err)
		}
		if digest != profile.Reference.Digest {
			t.Fatalf("profile digest = %q, recomputed %q", profile.Reference.Digest, digest)
		}
	}
}

func portableToolPlanBytesV1(closure SelectedClosureV1) ([]byte, error) {
	plan, err := CompilePortableToolPlanV1([]SelectedClosureV1{closure})
	if err != nil {
		return nil, err
	}
	return providers.CanonicalPortableToolPlanBytesV1(plan)
}

func portableToolCompilerClosureV1(scope, tool, version string) SelectedClosureV1 {
	namespace := "tool:" + tool + "/releases/" + version
	contract := BindingContractV1{
		Schema: BindingContractSchemaV1, ID: namespace + "/bindings/python/contract", Name: "python",
		Package: "demo", Requirements: []string{"demo"}, SupportedPython: []string{"3.12"},
		SupportedTags: []string{"py3"}, BundledComponents: []BundledComponentV1{},
		CLI: ToolExportV1{Name: "python", Path: "/opt/demo/bin/python"},
	}
	artifact := BindingArtifactRecordV1{
		Schema: BindingArtifactSchemaV1, ID: namespace + "/bindings/python/artifacts/wheel", Binding: "python",
		Contract: RecordReferenceV1{ID: contract.ID}, Name: "demo", EcosystemVersion: "1",
		Platform: "linux", Filename: "demo.whl", Size: "1", SHA256: portableToolCompilerDigestV1("artifact-sha"),
		Resolver: "pypi", Tags: []string{"py3"}, RequiresPython: ">=3.12", BundledComponents: []BundledComponentV1{},
	}
	payload := PayloadRecordV1{
		Schema: PayloadRecordSchemaV1, ID: namespace + "/payloads/demo", Name: "demo", Revision: "1",
		UpstreamVersion: version, Platform: "linux-amd64", LogicalPath: "demo.tar.gz", Kind: "archive",
		Size: "1", SHA256: portableToolCompilerDigestV1("payload-sha"), Resolver: "https", Entries: "1",
		UnpackedSize: "1", InstallDirectory: "/opt/demo", ArchiveRoot: "demo", Executables: []string{"demo"},
	}
	packageSet := NativePackageSetV1{
		Schema: NativePackageSetSchemaV1, ID: namespace + "/package-sets/debian-12", Manager: "apt",
		Requirements: []string{"libx"}, Repositories: []string{"main"}, ValidationMetadata: []string{"stable"},
	}
	profileIDs := []string{namespace + "/validation/profiles/zeta", namespace + "/validation/profiles/alpha"}
	profiles := make([]ValidationProfileRecordV1, 0, len(profileIDs))
	for _, id := range profileIDs {
		profiles = append(profiles, ValidationProfileRecordV1{
			Schema: ValidationProfileSchemaV1, ID: id, Tool: tool, Version: version,
			Probes: []RecordProbeV1{{Path: "/opt/demo/bin/demo", Args: []string{"--version"}}},
		})
	}
	return SelectedClosureV1{
		Scope:      scope,
		Provenance: ReleaseProvenanceV1{Tool: tool, Version: version, Revision: "1", ManifestDigest: portableToolCompilerDigestV1("manifest")},
		Contract: SelectedContractProjectionV1{
			Context: "runtime", Bindings: []string{"python"}, Selections: map[string][]string{"browser": {"chromium"}},
			Runtime: &RecordRuntimeV1{InstallRoot: "/opt/demo", Environment: []RecordEnvironmentVariableV1{
				{Name: "ZETA", Value: "z"}, {Name: "ALPHA", Value: "a"}, {Name: "ALPHA", Value: "a"},
			}},
			Exports: []ToolExportV1{{Name: "python", Path: "/opt/demo/bin/python"}},
		},
		Target: SelectedTargetProjectionV1{
			PackageSets: []RecordReferenceV1{{ID: packageSet.ID}},
			Bindings:    []SelectedTargetBindingV1{{Name: "python", Contract: RecordReferenceV1{ID: contract.ID}, Exports: []ToolExportV1{{Name: "python", Path: "/opt/demo/bin/python"}}}},
			Payloads:    []RecordReferenceV1{{ID: payload.ID}},
			Selections:  []SelectedTargetSelectionV1{{Dimension: "browser", Value: "chromium", Exports: []ToolExportV1{{Name: "demo", Path: "/opt/demo/bin/demo"}}}},
			Exports:     []ToolExportV1{{Name: "demo", Path: "/opt/demo/bin/demo"}},
		},
		Records: SelectedClosureRecordsV1{
			BindingContracts: []SelectedBindingContractRecordV1{{Reference: portableToolCompilerReferenceV1(contract), Record: contract}},
			BindingArtifacts: []SelectedBindingArtifactRecordV1{{Reference: portableToolCompilerReferenceV1(artifact), Record: artifact}},
			Payloads:         []SelectedPayloadRecordV1{{Reference: portableToolCompilerReferenceV1(payload), Record: payload}},
			PackageSets:      []SelectedPackageSetRecordV1{{Reference: portableToolCompilerReferenceV1(packageSet), Record: packageSet}},
		},
		Profiles: profiles,
		Identity: portableToolCompilerDigestV1(scope + ":" + tool + ":" + version),
	}
}

func portableToolCompilerReferenceV1(record any) RecordReferenceV1 {
	encoded, err := canonical.Marshal(record)
	if err != nil {
		panic(err)
	}
	var value canonical.Object
	if err := jsonUnmarshalPortableToolCompiler(encoded, &value); err != nil {
		panic(err)
	}
	digest, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, record)
	if err != nil {
		panic(err)
	}
	id, _ := value["id"].(string)
	return RecordReferenceV1{ID: id, Digest: digest}
}

func jsonUnmarshalPortableToolCompiler(encoded []byte, value *canonical.Object) error {
	// Keep test construction dependent only on the standard JSON shape, just as
	// the compiler is. This wrapper gives the helper a descriptive call site.
	return json.Unmarshal(encoded, value)
}

func portableToolCompilerDigestV1(seed string) canonical.Digest {
	digest, err := canonical.Sum("portable-tool-compiler-test", "portable-tool-compiler-test-v1", seed)
	if err != nil {
		panic(err)
	}
	return digest
}

func portableToolCompilerReverseClosureV1(closure *SelectedClosureV1) {
	reverse := func(length int, swap func(int, int)) {
		for left, right := 0, length-1; left < right; left, right = left+1, right-1 {
			swap(left, right)
		}
	}
	reverse(len(closure.Profiles), func(left, right int) {
		closure.Profiles[left], closure.Profiles[right] = closure.Profiles[right], closure.Profiles[left]
	})
	reverse(len(closure.Records.BindingContracts), func(left, right int) {
		closure.Records.BindingContracts[left], closure.Records.BindingContracts[right] = closure.Records.BindingContracts[right], closure.Records.BindingContracts[left]
	})
	reverse(len(closure.Records.BindingArtifacts), func(left, right int) {
		closure.Records.BindingArtifacts[left], closure.Records.BindingArtifacts[right] = closure.Records.BindingArtifacts[right], closure.Records.BindingArtifacts[left]
	})
	reverse(len(closure.Records.Payloads), func(left, right int) {
		closure.Records.Payloads[left], closure.Records.Payloads[right] = closure.Records.Payloads[right], closure.Records.Payloads[left]
	})
	reverse(len(closure.Records.PackageSets), func(left, right int) {
		closure.Records.PackageSets[left], closure.Records.PackageSets[right] = closure.Records.PackageSets[right], closure.Records.PackageSets[left]
	})
	if closure.Contract.Runtime != nil {
		reverse(len(closure.Contract.Runtime.Environment), func(left, right int) {
			closure.Contract.Runtime.Environment[left], closure.Contract.Runtime.Environment[right] = closure.Contract.Runtime.Environment[right], closure.Contract.Runtime.Environment[left]
		})
	}
	reverse(len(closure.Contract.Exports), func(left, right int) {
		closure.Contract.Exports[left], closure.Contract.Exports[right] = closure.Contract.Exports[right], closure.Contract.Exports[left]
	})
	reverse(len(closure.Target.Bindings), func(left, right int) {
		closure.Target.Bindings[left], closure.Target.Bindings[right] = closure.Target.Bindings[right], closure.Target.Bindings[left]
	})
	reverse(len(closure.Target.Selections), func(left, right int) {
		closure.Target.Selections[left], closure.Target.Selections[right] = closure.Target.Selections[right], closure.Target.Selections[left]
	})
	reverse(len(closure.Target.Exports), func(left, right int) {
		closure.Target.Exports[left], closure.Target.Exports[right] = closure.Target.Exports[right], closure.Target.Exports[left]
	})
}

func portableToolCompilerAddSecondRecordSetV1(closure *SelectedClosureV1) {
	contract := closure.Records.BindingContracts[0].Record
	contract.ID = strings.Replace(contract.ID, "/bindings/python/", "/bindings/node/", 1)
	contract.Name = "node"
	artifact := closure.Records.BindingArtifacts[0].Record
	artifact.ID = strings.Replace(artifact.ID, "/bindings/python/", "/bindings/node/", 1)
	artifact.Binding = "node"
	artifact.Contract = RecordReferenceV1{ID: contract.ID}
	payload := closure.Records.Payloads[0].Record
	payload.ID += "-extra"
	packageSet := closure.Records.PackageSets[0].Record
	packageSet.ID += "-extra"
	closure.Records.BindingContracts = append(closure.Records.BindingContracts,
		SelectedBindingContractRecordV1{Reference: portableToolCompilerReferenceV1(contract), Record: contract})
	closure.Records.BindingArtifacts = append(closure.Records.BindingArtifacts,
		SelectedBindingArtifactRecordV1{Reference: portableToolCompilerReferenceV1(artifact), Record: artifact})
	closure.Records.Payloads = append(closure.Records.Payloads,
		SelectedPayloadRecordV1{Reference: portableToolCompilerReferenceV1(payload), Record: payload})
	closure.Records.PackageSets = append(closure.Records.PackageSets,
		SelectedPackageSetRecordV1{Reference: portableToolCompilerReferenceV1(packageSet), Record: packageSet})
}
