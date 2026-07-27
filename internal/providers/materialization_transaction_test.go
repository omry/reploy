package providers

import (
	"reflect"
	"testing"
)

func TestMaterializationTransactionSchemaConstants(t *testing.T) {
	got := []string{
		MaterializationTransactionSchemaV1, ChildEnvironmentSchemaV1, GeneratedExecutableEvidenceSchemaV1,
		ExecutableRoleCarrier, ExecutableRoleEnvironmentLauncher,
		ExecutableRoleProviderPrerequisite, ExecutableRoleSelectedOutput,
		TypedArgumentLiteral, TypedArgumentValidatedExecutable,
		TypedArgumentGeneratedExecutable, TypedArgumentMountedArtifact,
		string(NetworkPolicyNone), BuildMountSourceArtifact, BuildMountSourceScript,
		BuildMountSourcePrivateOutput, ImageHealthcheckNone,
	}
	want := []string{
		"materialization-transaction-v1", "child-environment-v1", "generated-executable-evidence-v1",
		"carrier", "environment-launcher", "provider-prerequisite", "selected-output",
		"literal", "validated-executable", "generated-executable", "mounted-artifact",
		"none", "artifact", "script", "private-output", "none",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("constants = %#v, want %#v", got, want)
	}
}

func TestMaterializationTransactionCanonicalFieldNames(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  []string
	}{
		{name: "transaction", value: MaterializationTransaction{}, want: []string{
			"schema", "node_id", "recipe_version", "upstream", "carrier",
			"environment_launcher", "prerequisites", "script", "argv",
			"child_environment", "working_directory", "build_identity", "network",
			"mounts", "generated_executables", "final_image_config",
		}},
		{name: "executable input", value: ValidatedExecutableInput{}, want: []string{"id", "role", "policy", "evidence"}},
		{name: "typed argument", value: TypedArgument{}, want: []string{"kind", "literal", "executable_id", "generated_id", "mount_id", "relative_path"}},
		{name: "child environment", value: ChildEnvironmentProfile{}, want: []string{"schema", "name", "inherit_none", "umask", "variables"}},
		{name: "environment variable", value: EnvironmentVariable{}, want: []string{"name", "value"}},
		{name: "container identity", value: ContainerIdentity{}, want: []string{"uid", "gid", "supplementary_gids"}},
		{name: "build mount", value: BuildMount{}, want: []string{"id", "source_kind", "source_digest", "destination", "read_only", "expected_kind"}},
		{name: "generated executable", value: GeneratedExecutableDeclaration{}, want: []string{"id", "path", "exclusive_root", "validation_policy"}},
		{name: "realized generated executable", value: RealizedGeneratedExecutable{}, want: []string{"declaration", "evidence"}},
		{name: "generated executable evidence", value: GeneratedExecutableEvidence{}, want: []string{"schema", "invocation_path", "link_chain", "terminal", "access", "facts"}},
		{name: "generated file evidence", value: GeneratedFileEvidence{}, want: []string{"path", "kind", "mode", "size", "sha256", "owner,omitempty"}},
		{name: "image config", value: ImageConfigPolicy{}, want: []string{"user", "working_dir", "environment", "entrypoint", "command", "healthcheck", "stop_signal", "labels"}},
		{name: "image label", value: ImageLabel{}, want: []string{"name", "value"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			typeOf := reflect.TypeOf(test.value)
			if typeOf.NumField() != len(test.want) {
				t.Fatalf("field count = %d, want %d", typeOf.NumField(), len(test.want))
			}
			for index, name := range test.want {
				if got := typeOf.Field(index).Tag.Get("json"); got != name {
					t.Fatalf("field %d JSON name = %q, want %q", index, got, name)
				}
			}
		})
	}
}
