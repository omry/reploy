package python

import (
	"fmt"
	"path"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	providerapi "github.com/omry/reploy/internal/providers"
)

const (
	pythonMaterializationEnvironment = "python-v1"
	pythonMaterializationUmask       = "0022"
	pythonScriptMountID              = "script"
	pythonWheelMountID               = "wheels"
	pythonVenvExecutableID           = "venv_python"
)

// Materialize closes a verified Python bundle into the typed, offline recipe
// executed by the image backend. It describes the transaction only; it does
// not inspect the host store or run a container.
func (ComponentProvider) Materialize(input providerapi.MaterializeInput) (providerapi.MaterializationTransaction, error) {
	if err := providerapi.ValidateMaterializeInput(input, ValidateRequirementProfileV1, ValidateResolvedBundlePayloadV1); err != nil {
		return providerapi.MaterializationTransaction{}, fmt.Errorf("materialize Python input: %w", err)
	}
	request, err := decodeCanonicalProviderRequestV1(input.Bundle.Payload.Request)
	if err != nil {
		return providerapi.MaterializationTransaction{}, fmt.Errorf("materialize Python request: %w", err)
	}
	bundle, err := DecodeCanonicalBundleDataV1(request.Component, input.Bundle.Payload.ProviderPayload)
	if err != nil {
		return providerapi.MaterializationTransaction{}, fmt.Errorf("materialize Python bundle: %w", err)
	}
	if len(input.Profile.SelectedExecutables) != 1 || len(input.Profile.Declaration.Executables) != 1 {
		return providerapi.MaterializationTransaction{}, fmt.Errorf("materialize Python profile must contain exactly one interpreter")
	}
	if equal, err := canonicalEqual(bundle.Interpreter, input.Profile.SelectedExecutables[0]); err != nil {
		return providerapi.MaterializationTransaction{}, fmt.Errorf("compare Python interpreter evidence: %w", err)
	} else if !equal {
		return providerapi.MaterializationTransaction{}, fmt.Errorf("Python bundle interpreter does not match its requirement profile")
	}

	venvRoot := path.Join(
		InstallRoot,
		blueprint.ContributionRuntimeOwner(request.Component, blueprint.ContributionProviderPython),
	)
	venvPython := path.Join(venvRoot, "bin", "python")
	arguments := []providerapi.TypedArgument{
		{Kind: providerapi.TypedArgumentValidatedExecutable, ExecutableID: input.Carrier.ID},
		{Kind: providerapi.TypedArgumentLiteral, Literal: "-eu"},
		{Kind: providerapi.TypedArgumentMountedArtifact, MountID: pythonScriptMountID, RelativePath: bundle.Script.LogicalPath},
		{Kind: providerapi.TypedArgumentValidatedExecutable, ExecutableID: "interpreter"},
		{Kind: providerapi.TypedArgumentGeneratedExecutable, GeneratedID: pythonVenvExecutableID},
		{Kind: providerapi.TypedArgumentLiteral, Literal: venvRoot},
	}
	for _, wheel := range bundle.Wheels {
		arguments = append(arguments, providerapi.TypedArgument{
			Kind: providerapi.TypedArgumentMountedArtifact, MountID: pythonWheelMountID, RelativePath: wheel.Artifact.LogicalPath,
		})
	}

	generated := make([]providerapi.GeneratedExecutableDeclaration, 0, len(bundle.Outputs)+1)
	for _, output := range bundle.Outputs {
		generated = append(generated, providerapi.GeneratedExecutableDeclaration{
			ID: "output_" + output.Name, Path: output.Path, ExclusiveRoot: venvRoot,
			ValidationPolicy: providerapi.ValidationPolicyCompatible,
		})
	}
	generated = append(generated, providerapi.GeneratedExecutableDeclaration{
		ID: pythonVenvExecutableID, Path: venvPython, ExclusiveRoot: venvRoot,
		ValidationPolicy: providerapi.ValidationPolicyCompatible,
	})
	sort.Slice(generated, func(left int, right int) bool { return generated[left].ID < generated[right].ID })

	transaction := providerapi.MaterializationTransaction{
		Schema: providerapi.MaterializationTransactionSchemaV1, NodeID: input.Bundle.Payload.NodeID,
		RecipeVersion: MaterializationRecipeVersion, Upstream: input.AssemblyParent,
		Carrier: input.Carrier, EnvironmentLauncher: input.EnvironmentLauncher,
		Prerequisites: []providerapi.ValidatedExecutableInput{{
			ID: "interpreter", Role: providerapi.ExecutableRoleSelectedOutput,
			Policy: input.Profile.Declaration.Executables[0].ValidationPolicy, Evidence: bundle.Interpreter,
		}},
		Script: bundle.Script, Argv: arguments,
		ChildEnvironment: providerapi.ChildEnvironmentProfile{
			Schema: providerapi.ChildEnvironmentSchemaV1, Name: pythonMaterializationEnvironment,
			InheritNone: true, Umask: pythonMaterializationUmask, Variables: []providerapi.EnvironmentVariable{},
		},
		WorkingDirectory: "/",
		BuildIdentity:    providerapi.ContainerIdentity{UID: "0", GID: "0", SupplementaryGIDs: []string{}},
		Network:          providerapi.NetworkPolicyNone,
		Mounts: []providerapi.BuildMount{
			{ID: pythonScriptMountID, SourceKind: providerapi.BuildMountSourceScript, SourceDigest: bundle.Script.SHA256, Destination: "/.reploy-build/script", ReadOnly: true, ExpectedKind: "directory"},
			{ID: pythonWheelMountID, SourceKind: providerapi.BuildMountSourceArtifact, SourceDigest: input.Bundle.Identity, Destination: "/.reploy-build/wheels", ReadOnly: true, ExpectedKind: "directory"},
		},
		GeneratedExecutables: generated,
		FinalImageConfig:     clonePythonImageConfig(input.FinalImageConfig),
	}
	if err := providerapi.ValidateMaterializationTransaction(transaction); err != nil {
		return providerapi.MaterializationTransaction{}, fmt.Errorf("build Python materialization transaction: %w", err)
	}
	return transaction, nil
}

func clonePythonImageConfig(policy providerapi.ImageConfigPolicy) providerapi.ImageConfigPolicy {
	policy.Environment = append([]providerapi.EnvironmentVariable{}, policy.Environment...)
	policy.Entrypoint = append([]string{}, policy.Entrypoint...)
	policy.Command = append([]string{}, policy.Command...)
	policy.Labels = append([]providerapi.ImageLabel{}, policy.Labels...)
	return policy
}
