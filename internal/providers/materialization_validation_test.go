package providers

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/providerstore"
)

func validMaterializationTransaction() MaterializationTransaction {
	input := func(id string, role string, path string) ValidatedExecutableInput {
		return ValidatedExecutableInput{
			ID: id, Role: role, Policy: ValidationPolicyCompatible,
			Evidence: selectionEvidence(
				ExecutableRequirement{ID: id},
				RealizedOutput{SupplierComponent: "base", Name: id, Candidate: ExecutableCandidate{InvocationPath: path}},
			),
		}
	}
	scriptDigest := testDigest("4")
	return MaterializationTransaction{
		Schema: MaterializationTransactionSchemaV1, NodeID: "python/app", RecipeVersion: "python-materialize-v1",
		Upstream:            RealizedImageV1{Digest: testDigest("1"), ConfigDigest: testDigest("2"), RootFSSubject: testDigest("3")},
		Carrier:             input("carrier", ExecutableRoleCarrier, "/bin/sh"),
		EnvironmentLauncher: input("cleanenv", ExecutableRoleEnvironmentLauncher, "/usr/bin/env"),
		Prerequisites:       []ValidatedExecutableInput{input("interpreter", ExecutableRoleSelectedOutput, "/usr/bin/python3")},
		Script:              providerstore.ArtifactDescriptor{LogicalPath: "scripts/python-app.sh", Kind: "script", Size: "100", SHA256: scriptDigest},
		Argv: []TypedArgument{
			{Kind: TypedArgumentValidatedExecutable, ExecutableID: "carrier"},
			{Kind: TypedArgumentMountedArtifact, MountID: "script", RelativePath: "python-app.sh"},
			{Kind: TypedArgumentValidatedExecutable, ExecutableID: "interpreter"},
		},
		ChildEnvironment: ChildEnvironmentProfile{Schema: ChildEnvironmentSchemaV1, Name: "python-v1", InheritNone: true, Umask: "0022", Variables: []EnvironmentVariable{}},
		WorkingDirectory: "/", BuildIdentity: ContainerIdentity{UID: "0", GID: "0", SupplementaryGIDs: []string{}}, Network: NetworkPolicyNone,
		Mounts:               []BuildMount{{ID: "script", SourceKind: BuildMountSourceScript, SourceDigest: scriptDigest, Destination: "/.reploy-build/script", ReadOnly: true, ExpectedKind: "regular"}},
		GeneratedExecutables: []GeneratedExecutableDeclaration{},
		FinalImageConfig:     ImageConfigPolicy{User: "1000:1000", WorkingDir: "/work", Environment: []EnvironmentVariable{}, Entrypoint: []string{}, Command: []string{}, Healthcheck: ImageHealthcheckNone, StopSignal: "SIGTERM", Labels: []ImageLabel{}},
	}
}

func TestMaterializationTransactionDigestValidatesClosedTransaction(t *testing.T) {
	valid := validMaterializationTransaction()
	first, err := MaterializationTransactionDigest(valid)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MaterializationTransactionDigest(valid)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("transaction digests differ: %q != %q", first, second)
	}
	valid.Argv = append(valid.Argv, TypedArgument{Kind: TypedArgumentLiteral, Literal: "--offline"})
	changed, err := MaterializationTransactionDigest(valid)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("argument change did not change transaction digest")
	}
}

func TestValidateMaterializationTransactionRejectsOpenOrNoncanonicalRecords(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*MaterializationTransaction)
		want   string
	}{
		{name: "schema", mutate: func(value *MaterializationTransaction) { value.Schema = "v2" }, want: "schema"},
		{name: "node", mutate: func(value *MaterializationTransaction) { value.NodeID = "base" }, want: "node ID"},
		{name: "carrier role", mutate: func(value *MaterializationTransaction) { value.Carrier.Role = ExecutableRoleProviderPrerequisite }, want: "carrier role"},
		{name: "duplicate input", mutate: func(value *MaterializationTransaction) {
			value.EnvironmentLauncher.ID = value.Carrier.ID
			value.EnvironmentLauncher.Evidence.RequirementID = value.Carrier.ID
			value.EnvironmentLauncher.Evidence.Terminal.RequirementID = value.Carrier.ID
		}, want: "duplicated"},
		{name: "prerequisite order", mutate: func(value *MaterializationTransaction) {
			value.Prerequisites = append(value.Prerequisites, value.Prerequisites[0])
			value.Prerequisites[1].ID = "apt_get"
			value.Prerequisites[1].Evidence.RequirementID = "apt_get"
			value.Prerequisites[1].Evidence.Terminal.RequirementID = "apt_get"
		}, want: "sorted"},
		{name: "script kind", mutate: func(value *MaterializationTransaction) { value.Script.Kind = "archive" }, want: "script artifact kind"},
		{name: "literal command", mutate: func(value *MaterializationTransaction) {
			value.Argv[0] = TypedArgument{Kind: TypedArgumentLiteral, Literal: "/bin/sh"}
		}, want: "command position"},
		{name: "unknown input", mutate: func(value *MaterializationTransaction) { value.Argv[2].ExecutableID = "missing" }, want: "unknown executable"},
		{name: "wrong command input", mutate: func(value *MaterializationTransaction) { value.Argv[0].ExecutableID = "interpreter" }, want: "carrier executable"},
		{name: "unknown mount", mutate: func(value *MaterializationTransaction) { value.Argv[1].MountID = "missing" }, want: "unknown mount"},
		{name: "unreferenced script", mutate: func(value *MaterializationTransaction) {
			value.Argv = append([]TypedArgument{}, value.Argv[:1]...)
			value.Argv = append(value.Argv, value.Argv[0])
		}, want: "trusted script"},
		{name: "script digest", mutate: func(value *MaterializationTransaction) { value.Mounts[0].SourceDigest = testDigest("5") }, want: "does not match"},
		{name: "missing script mount", mutate: func(value *MaterializationTransaction) { value.Mounts = []BuildMount{} }, want: "exactly one"},
		{name: "generated order", mutate: func(value *MaterializationTransaction) {
			value.GeneratedExecutables = []GeneratedExecutableDeclaration{
				{ID: "z", Path: "/opt/z/bin/z", ExclusiveRoot: "/opt/z", ValidationPolicy: ValidationPolicyCompatible},
				{ID: "a", Path: "/opt/a/bin/a", ExclusiveRoot: "/opt/a", ValidationPolicy: ValidationPolicyCompatible},
			}
		}, want: "sorted"},
		{name: "unknown generated", mutate: func(value *MaterializationTransaction) {
			value.Argv = append(value.Argv, TypedArgument{Kind: TypedArgumentGeneratedExecutable, GeneratedID: "missing"})
		}, want: "unknown generated"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := validMaterializationTransaction()
			test.mutate(&candidate)
			if err := ValidateMaterializationTransaction(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateTypedArgumentStrictUnion(t *testing.T) {
	valid := []TypedArgument{
		{Kind: TypedArgumentLiteral, Literal: ""},
		{Kind: TypedArgumentValidatedExecutable, ExecutableID: "python"},
		{Kind: TypedArgumentGeneratedExecutable, GeneratedID: "installer"},
		{Kind: TypedArgumentMountedArtifact, MountID: "bundle", RelativePath: "wheels/demo.whl"},
	}
	for _, argument := range valid {
		if err := ValidateTypedArgument(argument); err != nil {
			t.Fatalf("valid argument %#v: %v", argument, err)
		}
	}
	invalid := []TypedArgument{
		{},
		{Kind: TypedArgumentLiteral, Literal: "x", ExecutableID: "python"},
		{Kind: TypedArgumentValidatedExecutable},
		{Kind: TypedArgumentGeneratedExecutable, GeneratedID: "Bad"},
		{Kind: TypedArgumentMountedArtifact, MountID: "bundle", RelativePath: "../escape"},
	}
	for _, argument := range invalid {
		if err := ValidateTypedArgument(argument); err == nil {
			t.Fatalf("invalid argument accepted: %#v", argument)
		}
	}
}

func TestValidateValidatedExecutableInput(t *testing.T) {
	valid := ValidatedExecutableInput{
		ID: "interpreter", Role: ExecutableRoleSelectedOutput, Policy: ValidationPolicyCompatible,
		Evidence: selectionEvidence(
			ExecutableRequirement{ID: "interpreter"},
			RealizedOutput{SupplierComponent: "base", Name: "python", Candidate: ExecutableCandidate{InvocationPath: "/usr/bin/python3"}},
		),
	}
	if err := ValidateValidatedExecutableInput(valid); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*ValidatedExecutableInput)
		want   string
	}{
		{name: "id", mutate: func(value *ValidatedExecutableInput) { value.ID = "Bad" }, want: "ID"},
		{name: "role", mutate: func(value *ValidatedExecutableInput) { value.Role = "runner" }, want: "role"},
		{name: "policy", mutate: func(value *ValidatedExecutableInput) { value.Policy = "latest" }, want: "policy"},
		{name: "evidence id", mutate: func(value *ValidatedExecutableInput) { value.Evidence.RequirementID = "other" }, want: "does not match"},
		{name: "evidence", mutate: func(value *ValidatedExecutableInput) { value.Evidence.InvocationPath = "python" }, want: "absolute"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := ValidateValidatedExecutableInput(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateChildEnvironmentProfile(t *testing.T) {
	valid := ChildEnvironmentProfile{
		Schema: ChildEnvironmentSchemaV1, Name: "python-materialize-v1", InheritNone: true, Umask: "0022",
		Variables: []EnvironmentVariable{{Name: "HOME", Value: "/tmp"}, {Name: "PATH", Value: "/usr/bin"}},
	}
	if err := ValidateChildEnvironmentProfile(valid); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*ChildEnvironmentProfile)
		want   string
	}{
		{name: "schema", mutate: func(value *ChildEnvironmentProfile) { value.Schema = "v2" }, want: "schema"},
		{name: "inherit", mutate: func(value *ChildEnvironmentProfile) { value.InheritNone = false }, want: "inherit no"},
		{name: "umask", mutate: func(value *ChildEnvironmentProfile) { value.Umask = "0999" }, want: "octal"},
		{name: "nil variables", mutate: func(value *ChildEnvironmentProfile) { value.Variables = nil }, want: "array"},
		{name: "name", mutate: func(value *ChildEnvironmentProfile) { value.Variables[0].Name = "9BAD" }, want: "variable name"},
		{name: "order", mutate: func(value *ChildEnvironmentProfile) {
			value.Variables[0], value.Variables[1] = value.Variables[1], value.Variables[0]
		}, want: "sorted"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			candidate.Variables = append([]EnvironmentVariable{}, valid.Variables...)
			test.mutate(&candidate)
			if err := ValidateChildEnvironmentProfile(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateContainerIdentityNumericOrdering(t *testing.T) {
	valid := ContainerIdentity{UID: "1000", GID: "1000", SupplementaryGIDs: []string{"2", "10", "100"}}
	if err := ValidateContainerIdentity(valid); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []ContainerIdentity{
		{UID: "01", GID: "0", SupplementaryGIDs: []string{}},
		{UID: "0", GID: "-1", SupplementaryGIDs: []string{}},
		{UID: "0", GID: "0", SupplementaryGIDs: nil},
		{UID: "0", GID: "0", SupplementaryGIDs: []string{"10", "2"}},
		{UID: "0", GID: "0", SupplementaryGIDs: []string{"2", "2"}},
	} {
		if err := ValidateContainerIdentity(invalid); err == nil {
			t.Fatalf("invalid identity accepted: %#v", invalid)
		}
	}
}

func TestValidateNetworkPolicyAllowsOnlyNone(t *testing.T) {
	if err := ValidateNetworkPolicy(NetworkPolicyNone); err != nil {
		t.Fatal(err)
	}
	if err := ValidateNetworkPolicy("default"); err == nil {
		t.Fatal("default network was accepted")
	}
}

func TestValidateBuildMounts(t *testing.T) {
	valid := []BuildMount{
		{ID: "bundle", SourceKind: BuildMountSourceArtifact, SourceDigest: testDigest("a"), Destination: "/.reploy-build/bundle", ReadOnly: true, ExpectedKind: "directory"},
		{ID: "output", SourceKind: BuildMountSourcePrivateOutput, Destination: "/.reploy-build/output", ExpectedKind: "directory"},
		{ID: "script", SourceKind: BuildMountSourceScript, SourceDigest: testDigest("b"), Destination: "/.reploy-build/script", ReadOnly: true, ExpectedKind: "regular"},
	}
	if err := ValidateBuildMounts(valid); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func([]BuildMount) []BuildMount
	}{
		{name: "nil", mutate: func([]BuildMount) []BuildMount { return nil }},
		{name: "order", mutate: func(value []BuildMount) []BuildMount { value[0], value[1] = value[1], value[0]; return value }},
		{name: "root", mutate: func(value []BuildMount) []BuildMount { value[0].Destination = "/.reploy-build"; return value }},
		{name: "outside", mutate: func(value []BuildMount) []BuildMount { value[0].Destination = "/tmp/bundle"; return value }},
		{name: "writable artifact", mutate: func(value []BuildMount) []BuildMount { value[0].ReadOnly = false; return value }},
		{name: "private digest", mutate: func(value []BuildMount) []BuildMount { value[1].SourceDigest = testDigest("c"); return value }},
		{name: "overlap", mutate: func(value []BuildMount) []BuildMount {
			value[1].Destination = "/.reploy-build/bundle/output"
			return value
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := append([]BuildMount{}, valid...)
			if err := ValidateBuildMounts(test.mutate(candidate)); err == nil {
				t.Fatal("invalid build mounts were accepted")
			}
		})
	}
}

func TestValidateGeneratedExecutableDeclaration(t *testing.T) {
	valid := GeneratedExecutableDeclaration{
		ID: "python", Path: "/opt/reploy/app/bin/python", ExclusiveRoot: "/opt/reploy/app", ValidationPolicy: ValidationPolicyCompatible,
	}
	if err := ValidateGeneratedExecutableDeclaration(valid); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*GeneratedExecutableDeclaration)
		want   string
	}{
		{name: "id", mutate: func(value *GeneratedExecutableDeclaration) { value.ID = "Python" }, want: "ID"},
		{name: "path", mutate: func(value *GeneratedExecutableDeclaration) { value.Path = "bin/python" }, want: "absolute"},
		{name: "root", mutate: func(value *GeneratedExecutableDeclaration) { value.ExclusiveRoot = "/opt/other" }, want: "descendant"},
		{name: "root itself", mutate: func(value *GeneratedExecutableDeclaration) { value.Path = value.ExclusiveRoot }, want: "descendant"},
		{name: "policy", mutate: func(value *GeneratedExecutableDeclaration) { value.ValidationPolicy = "latest" }, want: "policy"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := ValidateGeneratedExecutableDeclaration(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func validRealizedGeneratedExecutable() RealizedGeneratedExecutable {
	path := "/opt/reploy/providers/python/app/bin/python"
	return RealizedGeneratedExecutable{
		Declaration: GeneratedExecutableDeclaration{
			ID: "venv_python", Path: path, ExclusiveRoot: "/opt/reploy/providers/python/app",
			ValidationPolicy: ValidationPolicyCompatible,
		},
		Evidence: GeneratedExecutableEvidence{
			Schema: GeneratedExecutableEvidenceSchemaV1, InvocationPath: path, LinkChain: []LinkEvidence{},
			Terminal: GeneratedFileEvidence{Path: path, Kind: "regular", Mode: "0755", Size: "100", SHA256: testDigest("a")},
			Access: PortableAccessEvidence{
				Schema: PortableAccessSchemaV1, Profile: PortableOutputAccessV1,
				Paths: []AccessPathEvidence{{Path: path, Kind: "regular", Mode: "0755", Required: "other-read-execute"}},
			},
			Facts: providerData("python-generated-v1"),
		},
	}
}

func TestValidateRealizedGeneratedExecutable(t *testing.T) {
	if err := ValidateRealizedGeneratedExecutable(validRealizedGeneratedExecutable()); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*RealizedGeneratedExecutable)
		want   string
	}{
		{name: "schema", mutate: func(value *RealizedGeneratedExecutable) { value.Evidence.Schema = "generated-v2" }, want: "schema"},
		{name: "declared path", mutate: func(value *RealizedGeneratedExecutable) { value.Evidence.InvocationPath = "/opt/other/bin/python" }, want: "does not match"},
		{name: "nil chain", mutate: func(value *RealizedGeneratedExecutable) { value.Evidence.LinkChain = nil }, want: "array"},
		{name: "terminal kind", mutate: func(value *RealizedGeneratedExecutable) { value.Evidence.Terminal.Kind = "directory" }, want: "terminal kind"},
		{name: "terminal access", mutate: func(value *RealizedGeneratedExecutable) { value.Evidence.Access.Paths = []AccessPathEvidence{} }, want: "omits terminal"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := validRealizedGeneratedExecutable()
			test.mutate(&candidate)
			if err := ValidateRealizedGeneratedExecutable(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateMaterializationGeneratedExecutablesRequiresExactDeclarations(t *testing.T) {
	transaction := validMaterializationTransaction()
	realized := validRealizedGeneratedExecutable()
	transaction.GeneratedExecutables = []GeneratedExecutableDeclaration{realized.Declaration}
	if err := ValidateMaterializationGeneratedExecutables(transaction, []RealizedGeneratedExecutable{realized}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMaterializationGeneratedExecutables(transaction, []RealizedGeneratedExecutable{}); err == nil || !strings.Contains(err.Error(), "count") {
		t.Fatalf("missing evidence error = %v", err)
	}
	changed := realized
	changed.Declaration.ValidationPolicy = ValidationPolicyUnchanged
	if err := ValidateMaterializationGeneratedExecutables(transaction, []RealizedGeneratedExecutable{changed}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("changed declaration error = %v", err)
	}
	if err := ValidateRealizedGeneratedExecutableCollection(nil); err == nil || !strings.Contains(err.Error(), "array") {
		t.Fatalf("nil collection error = %v", err)
	}
}

func TestValidateImageConfigPolicy(t *testing.T) {
	valid := ImageConfigPolicy{
		User: "1000:1000", WorkingDir: "/work",
		Environment: []EnvironmentVariable{{Name: "HOME", Value: "/home/app"}, {Name: "PATH", Value: "/usr/bin"}},
		Entrypoint:  []string{}, Command: []string{}, Healthcheck: ImageHealthcheckNone, StopSignal: "SIGTERM",
		Labels: []ImageLabel{{Name: "io.reploy.component", Value: "app"}, {Name: "org.example.release", Value: "1"}},
	}
	if err := ValidateImageConfigPolicy(valid); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*ImageConfigPolicy)
		want   string
	}{
		{name: "user", mutate: func(value *ImageConfigPolicy) { value.User = "" }, want: "user"},
		{name: "workdir", mutate: func(value *ImageConfigPolicy) { value.WorkingDir = "work" }, want: "absolute"},
		{name: "nil environment", mutate: func(value *ImageConfigPolicy) { value.Environment = nil }, want: "environment"},
		{name: "environment order", mutate: func(value *ImageConfigPolicy) {
			value.Environment[0], value.Environment[1] = value.Environment[1], value.Environment[0]
		}, want: "sorted"},
		{name: "nil entrypoint", mutate: func(value *ImageConfigPolicy) { value.Entrypoint = nil }, want: "entrypoint"},
		{name: "nil command", mutate: func(value *ImageConfigPolicy) { value.Command = nil }, want: "command"},
		{name: "healthcheck", mutate: func(value *ImageConfigPolicy) { value.Healthcheck = "inherit" }, want: "healthcheck"},
		{name: "stop signal", mutate: func(value *ImageConfigPolicy) { value.StopSignal = "" }, want: "stop signal"},
		{name: "reserved label", mutate: func(value *ImageConfigPolicy) { value.Labels[0].Name = "io.reploy.validation.subject" }, want: "reserved"},
		{name: "label order", mutate: func(value *ImageConfigPolicy) { value.Labels[0], value.Labels[1] = value.Labels[1], value.Labels[0] }, want: "sorted"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			candidate.Environment = append([]EnvironmentVariable{}, valid.Environment...)
			candidate.Entrypoint = append([]string{}, valid.Entrypoint...)
			candidate.Command = append([]string{}, valid.Command...)
			candidate.Labels = append([]ImageLabel{}, valid.Labels...)
			test.mutate(&candidate)
			if err := ValidateImageConfigPolicy(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}
