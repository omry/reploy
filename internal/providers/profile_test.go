package providers

import (
	"errors"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func validRequirementProfile() RequirementProfile {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		panic(err)
	}
	return RequirementProfile{
		Schema: RequirementProfileSchemaV1,
		Declaration: RequirementDeclaration{
			Executables: []ExecutableRequirement{{
				ID: "interpreter", Command: "python", Supplier: "base", ValidationPolicy: ValidationPolicyCompatible,
			}},
			Files: []FileRequirement{{
				ID: "package_status", Path: "/var/lib/dpkg/status", Kind: "regular", ExpectedSHA256: testDigest("6"), ValidationPolicy: ValidationPolicyUnchanged,
			}},
			ProviderData: providerData("python-requirements-v1"),
		},
		SelectedExecutables: []ExecutableEvidence{{
			Schema:         ExecutableEvidenceSchemaV1,
			RequirementID:  "interpreter",
			Output:         QualifiedOutput{Component: "base", Name: "python"},
			InvocationPath: "/usr/bin/python",
			LinkChain: []LinkEvidence{{
				Path: "/usr/bin/python", Target: "python3", ResolvedPath: "/usr/bin/python3", Kind: "ordinary",
			}},
			Terminal: FileEvidence{
				Schema: FileEvidenceSchemaV1, RequirementID: "interpreter", Path: "/usr/bin/python3", Kind: "regular", Mode: "0755", Size: "6831736", SHA256: testDigest("7"),
			},
			Access: PortableAccessEvidence{
				Schema: PortableAccessSchemaV1, Profile: PortableOutputAccessV1,
				Paths: []AccessPathEvidence{
					{Path: "/usr", Kind: "directory", Mode: "0755", Required: "other-search"},
					{Path: "/usr/bin", Kind: "directory", Mode: "0755", Required: "other-search"},
					{Path: "/usr/bin/python3", Kind: "regular", Mode: "0755", Required: "other-read-execute"},
				},
			},
			Facts: providerData("python-interpreter-facts-v1"),
		}},
		SelectedFiles: []FileEvidence{{
			Schema: FileEvidenceSchemaV1, RequirementID: "package_status", Path: "/var/lib/dpkg/status", Kind: "regular", Mode: "0644", Size: "2000", SHA256: testDigest("6"),
		}},
		Platform: platform,
		Facts:    providerData("python-profile-facts-v1"),
	}
}

func validateTestProfileOwner(profile RequirementProfile) error {
	if profile.Facts.Schema != "python-profile-facts-v1" || profile.SelectedExecutables[0].Facts.Schema != "python-interpreter-facts-v1" {
		return errors.New("unknown provider evidence schema")
	}
	return nil
}

func TestRequirementProfileDigestValidatesCompleteEvidence(t *testing.T) {
	profile := validRequirementProfile()
	first, err := RequirementProfileDigest(profile, validateTestProfileOwner)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RequirementProfileDigest(profile, validateTestProfileOwner)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("profile digests differ: %q != %q", first, second)
	}
	profile.SelectedExecutables[0].Terminal.SHA256 = testDigest("8")
	changed, err := RequirementProfileDigest(profile, validateTestProfileOwner)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("observed executable change did not change profile digest")
	}
}

func TestRequirementProfileRejectsIncompleteOrMismatchedEvidence(t *testing.T) {
	valid := validRequirementProfile()
	tests := []struct {
		name   string
		mutate func(*RequirementProfile)
		want   string
	}{
		{name: "schema", mutate: func(value *RequirementProfile) { value.Schema = "requirement-profile-v2" }, want: "schema"},
		{name: "missing executable", mutate: func(value *RequirementProfile) { value.SelectedExecutables = []ExecutableEvidence{} }, want: "missing selected executable"},
		{name: "extra executable", mutate: func(value *RequirementProfile) { value.SelectedExecutables[0].RequirementID = "other" }, want: "no declaration"},
		{name: "wrong command", mutate: func(value *RequirementProfile) { value.SelectedExecutables[0].Output.Name = "python3" }, want: "want \"python\""},
		{name: "wrong supplier", mutate: func(value *RequirementProfile) { value.SelectedExecutables[0].Output.Component = "system" }, want: "want \"base\""},
		{name: "broken chain", mutate: func(value *RequirementProfile) { value.SelectedExecutables[0].LinkChain[0].ResolvedPath = "/other" }, want: "resolves to"},
		{name: "bad terminal", mutate: func(value *RequirementProfile) { value.SelectedExecutables[0].Terminal.Kind = "directory" }, want: "terminal kind"},
		{name: "weak access", mutate: func(value *RequirementProfile) { value.SelectedExecutables[0].Access.Paths[2].Mode = "0750" }, want: "does not prove"},
		{name: "missing terminal access", mutate: func(value *RequirementProfile) {
			value.SelectedExecutables[0].Access.Paths = value.SelectedExecutables[0].Access.Paths[:2]
		}, want: "omits terminal"},
		{name: "file digest", mutate: func(value *RequirementProfile) { value.SelectedFiles[0].SHA256 = testDigest("9") }, want: "declared digest"},
		{name: "owner schema", mutate: func(value *RequirementProfile) { value.Facts.Schema = "unknown" }, want: "provider facts"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneRequirementProfileForTest(valid)
			test.mutate(&candidate)
			if err := ValidateRequirementProfile(candidate, validateTestProfileOwner); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidationEvidenceBindsSubjectAndProfile(t *testing.T) {
	evidence, err := NewValidationEvidence(testDigest("a"), testDigest("b"))
	if err != nil {
		t.Fatal(err)
	}
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	evidence.SubjectRootFS = "bad"
	if err := evidence.Validate(); err == nil || !strings.Contains(err.Error(), "rootfs") {
		t.Fatalf("error = %v", err)
	}
}

func cloneRequirementProfileForTest(profile RequirementProfile) RequirementProfile {
	result := profile
	result.Declaration.Executables = append([]ExecutableRequirement{}, profile.Declaration.Executables...)
	result.Declaration.Files = append([]FileRequirement{}, profile.Declaration.Files...)
	result.SelectedExecutables = append([]ExecutableEvidence{}, profile.SelectedExecutables...)
	for index := range result.SelectedExecutables {
		result.SelectedExecutables[index].LinkChain = append([]LinkEvidence{}, profile.SelectedExecutables[index].LinkChain...)
		result.SelectedExecutables[index].Access.Paths = append([]AccessPathEvidence{}, profile.SelectedExecutables[index].Access.Paths...)
	}
	result.SelectedFiles = append([]FileEvidence{}, profile.SelectedFiles...)
	return result
}
