package dockerdeploy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func rendererDigest(char string) canonical.Digest {
	return canonical.Digest("sha256:" + strings.Repeat(char, 64))
}

func rendererExecutable(id string, role string, invocation string) providers.ValidatedExecutableInput {
	evidence := providers.ExecutableEvidence{
		Schema: providers.ExecutableEvidenceSchemaV1, RequirementID: id,
		Output: providers.QualifiedOutput{Component: "base", Name: id}, InvocationPath: invocation,
		LinkChain: []providers.LinkEvidence{},
		Terminal:  providers.FileEvidence{Schema: providers.FileEvidenceSchemaV1, RequirementID: id, Path: invocation, Kind: "regular", Mode: "0755", Size: "1", SHA256: rendererDigest("a")},
		Access: providers.PortableAccessEvidence{
			Schema: providers.PortableAccessSchemaV1, Profile: providers.PortableOutputAccessV1,
			Paths: []providers.AccessPathEvidence{{Path: invocation, Kind: "regular", Mode: "0755", Required: "other-read-execute"}},
		},
		Facts: providers.CanonicalProviderData{Schema: "renderer-executable-v1", Value: canonical.Object{}},
	}
	return providers.ValidatedExecutableInput{ID: id, Role: role, Policy: providers.ValidationPolicyCompatible, Evidence: evidence}
}

func rendererTransaction() providers.MaterializationTransaction {
	scriptDigest := rendererDigest("4")
	return providers.MaterializationTransaction{
		Schema: providers.MaterializationTransactionSchemaV1, NodeID: "python/web", RecipeVersion: "python-materialize-v1",
		Upstream:            providers.RealizedImageV1{Digest: rendererDigest("1"), ConfigDigest: rendererDigest("2"), RootFSSubject: rendererDigest("3")},
		Carrier:             rendererExecutable("carrier", providers.ExecutableRoleCarrier, "/bin/sh"),
		EnvironmentLauncher: rendererExecutable("cleanenv", providers.ExecutableRoleEnvironmentLauncher, "/usr/bin/env"),
		Prerequisites:       []providers.ValidatedExecutableInput{rendererExecutable("interpreter", providers.ExecutableRoleSelectedOutput, "/usr/bin/python3")},
		Script:              providerstore.ArtifactDescriptor{LogicalPath: "scripts/python-web.sh", Kind: "script", Size: "100", SHA256: scriptDigest},
		Argv: []providers.TypedArgument{
			{Kind: providers.TypedArgumentValidatedExecutable, ExecutableID: "carrier"},
			{Kind: providers.TypedArgumentLiteral, Literal: "-eu"},
			{Kind: providers.TypedArgumentMountedArtifact, MountID: "script", RelativePath: "python-web.sh"},
			{Kind: providers.TypedArgumentValidatedExecutable, ExecutableID: "interpreter"},
			{Kind: providers.TypedArgumentGeneratedExecutable, GeneratedID: "venv_python"},
			{Kind: providers.TypedArgumentLiteral, Literal: "$(touch /tmp/not-shell)"},
			{Kind: providers.TypedArgumentMountedArtifact, MountID: "wheels", RelativePath: "hydra.whl"},
		},
		ChildEnvironment: providers.ChildEnvironmentProfile{Schema: providers.ChildEnvironmentSchemaV1, Name: "python-v1", InheritNone: true, Umask: "0022", Variables: []providers.EnvironmentVariable{}},
		WorkingDirectory: "/", BuildIdentity: providers.ContainerIdentity{UID: "0", GID: "0", SupplementaryGIDs: []string{}}, Network: providers.NetworkPolicyNone,
		Mounts: []providers.BuildMount{
			{ID: "script", SourceKind: providers.BuildMountSourceScript, SourceDigest: scriptDigest, Destination: "/.reploy-build/script", ReadOnly: true, ExpectedKind: "directory"},
			{ID: "wheels", SourceKind: providers.BuildMountSourceArtifact, SourceDigest: rendererDigest("5"), Destination: "/.reploy-build/wheels", ReadOnly: true, ExpectedKind: "directory"},
		},
		GeneratedExecutables: []providers.GeneratedExecutableDeclaration{{ID: "venv_python", Path: "/opt/reploy/providers/python/web/bin/python", ExclusiveRoot: "/opt/reploy/providers/python/web", ValidationPolicy: providers.ValidationPolicyCompatible}},
		FinalImageConfig:     providers.ImageConfigPolicy{User: "1000:1000", WorkingDir: "/work", Environment: []providers.EnvironmentVariable{}, Entrypoint: []string{}, Command: []string{}, Healthcheck: providers.ImageHealthcheckNone, StopSignal: "SIGTERM", Labels: []providers.ImageLabel{}},
	}
}

func TestRenderMaterializationArgvResolvesTypedOperandsWithoutInterpretation(t *testing.T) {
	transaction := rendererTransaction()
	transaction.ChildEnvironment.Variables = []providers.EnvironmentVariable{
		{Name: "HOME", Value: "/nonexistent"},
		{Name: "LITERAL", Value: "$(touch /tmp/not-environment-shell)"},
	}
	argv, err := RenderMaterializationArgv(transaction)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/usr/bin/env", "-i", "HOME=/nonexistent", "LITERAL=$(touch /tmp/not-environment-shell)",
		"/bin/sh", "-c", materializationChildEnvironmentProgram, "python-v1", "0022",
		"/bin/sh", "-eu", "/.reploy-build/script/python-web.sh", "/usr/bin/python3",
		"/opt/reploy/providers/python/web/bin/python", "$(touch /tmp/not-shell)",
		"/.reploy-build/wheels/hydra.whl",
	}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv = %#v, want %#v", argv, want)
	}
}

func TestRenderMaterializationArgvRejectsInvalidTransaction(t *testing.T) {
	transaction := rendererTransaction()
	transaction.Argv[0] = providers.TypedArgument{Kind: providers.TypedArgumentLiteral, Literal: "/bin/sh"}
	if _, err := RenderMaterializationArgv(transaction); err == nil || !strings.Contains(err.Error(), "command position") {
		t.Fatalf("error = %v", err)
	}
}
