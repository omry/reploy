package apt

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/canonical"
	providerapi "github.com/omry/reploy/internal/providers"
)

const (
	aptArtifactMountID = "artifacts"
	aptScriptMountID   = "script"
)

var materializationToolArgumentOrderV1 = []string{
	"apt_get", "dpkg", "dpkg_query", "sha256sum", "awk", "cmp", "mkdir", "rm", "sort", "wc",
}

// Materialize closes a validated APT bundle into one typed, root,
// network-disabled installation transaction. It describes the transaction;
// the image backend remains solely responsible for executing and accepting it.
func (ComponentProvider) Materialize(input providerapi.MaterializeInput) (providerapi.MaterializationTransaction, error) {
	if err := providerapi.ValidateMaterializeInput(input, ValidateRequirementProfileV1, ValidateResolvedBundlePayloadV1); err != nil {
		return providerapi.MaterializationTransaction{}, fmt.Errorf("materialize APT input: %w", err)
	}
	base, lockedExecutables, err := DecodeProfileFactsV1(input.Profile.Facts)
	if err != nil {
		return providerapi.MaterializationTransaction{}, fmt.Errorf("materialize APT profile: %w", err)
	}
	bundle, err := DecodeCanonicalBundleDataV1(input.Bundle.Payload.ProviderPayload)
	if err != nil {
		return providerapi.MaterializationTransaction{}, fmt.Errorf("materialize APT bundle: %w", err)
	}
	if bundle.NativeArchitecture != base.NativeArchitecture {
		return providerapi.MaterializationTransaction{}, fmt.Errorf("APT bundle architecture does not match its base profile")
	}

	byID := make(map[string]providerapi.ValidatedExecutableInput, len(lockedExecutables))
	for _, executable := range lockedExecutables {
		byID[executable.ID] = executable
	}
	if err := requireCurrentAPTExecutableV1("carrier", input.Carrier, byID["sh"]); err != nil {
		return providerapi.MaterializationTransaction{}, err
	}
	if err := requireCurrentAPTExecutableV1("environment launcher", input.EnvironmentLauncher, byID["env"]); err != nil {
		return providerapi.MaterializationTransaction{}, err
	}

	arguments := []providerapi.TypedArgument{
		{Kind: providerapi.TypedArgumentValidatedExecutable, ExecutableID: input.Carrier.ID},
		{Kind: providerapi.TypedArgumentLiteral, Literal: "-eu"},
		{Kind: providerapi.TypedArgumentMountedArtifact, MountID: aptScriptMountID, RelativePath: bundle.Script.LogicalPath},
	}
	usedPrerequisites := map[string]bool{}
	for _, id := range materializationToolArgumentOrderV1 {
		executable, found := byID[id]
		if !found || executable.Role != providerapi.ExecutableRoleProviderPrerequisite {
			return providerapi.MaterializationTransaction{}, fmt.Errorf("APT materialization profile is missing prerequisite %q", id)
		}
		usedPrerequisites[id] = true
		arguments = append(arguments, providerapi.TypedArgument{Kind: providerapi.TypedArgumentValidatedExecutable, ExecutableID: id})
	}
	arguments = append(arguments, providerapi.TypedArgument{
		Kind: providerapi.TypedArgumentMountedArtifact, MountID: aptArtifactMountID, RelativePath: bundle.StateManifest.LogicalPath,
	})
	for _, pkg := range bundle.BundlePackages {
		arguments = append(arguments, providerapi.TypedArgument{
			Kind: providerapi.TypedArgumentMountedArtifact, MountID: aptArtifactMountID, RelativePath: pkg.Artifact.LogicalPath,
		})
	}

	prerequisites := make([]providerapi.ValidatedExecutableInput, 0, len(usedPrerequisites))
	for id := range usedPrerequisites {
		prerequisites = append(prerequisites, byID[id])
	}
	sort.Slice(prerequisites, func(left int, right int) bool { return prerequisites[left].ID < prerequisites[right].ID })

	transaction := providerapi.MaterializationTransaction{
		Schema: providerapi.MaterializationTransactionSchemaV1, NodeID: input.Bundle.Payload.NodeID,
		RecipeVersion: MaterializationRecipeVersion, Upstream: input.AssemblyParent,
		Carrier: input.Carrier, EnvironmentLauncher: input.EnvironmentLauncher, Prerequisites: prerequisites,
		Script: bundle.Script, Argv: arguments, ChildEnvironment: MaterializationChildEnvironmentV1(),
		WorkingDirectory: "/", BuildIdentity: providerapi.ContainerIdentity{UID: "0", GID: "0", SupplementaryGIDs: []string{}},
		Network: providerapi.NetworkPolicyNone,
		Mounts: []providerapi.BuildMount{
			{ID: aptArtifactMountID, SourceKind: providerapi.BuildMountSourceArtifact, SourceDigest: input.Bundle.Identity, Destination: "/.reploy-build/apt", ReadOnly: true, ExpectedKind: "directory"},
			{ID: aptScriptMountID, SourceKind: providerapi.BuildMountSourceScript, SourceDigest: bundle.Script.SHA256, Destination: "/.reploy-build/script", ReadOnly: true, ExpectedKind: "directory"},
		},
		GeneratedExecutables: []providerapi.GeneratedExecutableDeclaration{},
		FinalImageConfig:     cloneAPTImageConfigV1(input.FinalImageConfig),
	}
	if err := providerapi.ValidateMaterializationTransaction(transaction); err != nil {
		return providerapi.MaterializationTransaction{}, fmt.Errorf("build APT materialization transaction: %w", err)
	}
	return transaction, nil
}

func requireCurrentAPTExecutableV1(role string, current providerapi.ValidatedExecutableInput, locked providerapi.ValidatedExecutableInput) error {
	currentBytes, err := canonical.Marshal(current)
	if err != nil {
		return fmt.Errorf("encode current APT %s: %w", role, err)
	}
	lockedBytes, err := canonical.Marshal(locked)
	if err != nil {
		return fmt.Errorf("encode locked APT %s: %w", role, err)
	}
	if !bytes.Equal(currentBytes, lockedBytes) {
		return fmt.Errorf("current APT %s does not match the locked prefix evidence", role)
	}
	return nil
}

func cloneAPTImageConfigV1(policy providerapi.ImageConfigPolicy) providerapi.ImageConfigPolicy {
	policy.Environment = append([]providerapi.EnvironmentVariable{}, policy.Environment...)
	policy.Entrypoint = append([]string{}, policy.Entrypoint...)
	policy.Command = append([]string{}, policy.Command...)
	policy.Labels = append([]providerapi.ImageLabel{}, policy.Labels...)
	return policy
}
