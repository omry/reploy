package providers

import (
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providerstore"
)

const (
	MaterializationTransactionSchemaV1 = "materialization-transaction-v1"
	ChildEnvironmentSchemaV1           = "child-environment-v1"

	ExecutableRoleCarrier              = "carrier"
	ExecutableRoleEnvironmentLauncher  = "environment-launcher"
	ExecutableRoleProviderPrerequisite = "provider-prerequisite"
	ExecutableRoleSelectedOutput       = "selected-output"

	TypedArgumentLiteral             = "literal"
	TypedArgumentValidatedExecutable = "validated-executable"
	TypedArgumentGeneratedExecutable = "generated-executable"
	TypedArgumentMountedArtifact     = "mounted-artifact"

	NetworkPolicyNone NetworkPolicy = "none"

	BuildMountSourceArtifact      = "artifact"
	BuildMountSourceScript        = "script"
	BuildMountSourcePrivateOutput = "private-output"

	ImageHealthcheckNone = "none"
)

type MaterializationTransaction struct {
	Schema               string                           `json:"schema"`
	NodeID               NodeID                           `json:"node_id"`
	RecipeVersion        string                           `json:"recipe_version"`
	Upstream             RealizedImageV1                  `json:"upstream"`
	Carrier              ValidatedExecutableInput         `json:"carrier"`
	EnvironmentLauncher  ValidatedExecutableInput         `json:"environment_launcher"`
	Prerequisites        []ValidatedExecutableInput       `json:"prerequisites"`
	Script               providerstore.ArtifactDescriptor `json:"script"`
	Argv                 []TypedArgument                  `json:"argv"`
	ChildEnvironment     ChildEnvironmentProfile          `json:"child_environment"`
	WorkingDirectory     string                           `json:"working_directory"`
	BuildIdentity        ContainerIdentity                `json:"build_identity"`
	Network              NetworkPolicy                    `json:"network"`
	Mounts               []BuildMount                     `json:"mounts"`
	GeneratedExecutables []GeneratedExecutableDeclaration `json:"generated_executables"`
	FinalImageConfig     ImageConfigPolicy                `json:"final_image_config"`
}

type ValidatedExecutableInput struct {
	ID       string             `json:"id"`
	Role     string             `json:"role"`
	Policy   string             `json:"policy"`
	Evidence ExecutableEvidence `json:"evidence"`
}

type TypedArgument struct {
	Kind         string `json:"kind"`
	Literal      string `json:"literal"`
	ExecutableID string `json:"executable_id"`
	GeneratedID  string `json:"generated_id"`
	MountID      string `json:"mount_id"`
	RelativePath string `json:"relative_path"`
}

type ChildEnvironmentProfile struct {
	Schema      string                `json:"schema"`
	Name        string                `json:"name"`
	InheritNone bool                  `json:"inherit_none"`
	Umask       string                `json:"umask"`
	Variables   []EnvironmentVariable `json:"variables"`
}

type EnvironmentVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type ContainerIdentity struct {
	UID               string   `json:"uid"`
	GID               string   `json:"gid"`
	SupplementaryGIDs []string `json:"supplementary_gids"`
}

type NetworkPolicy string

type BuildMount struct {
	ID           string           `json:"id"`
	SourceKind   string           `json:"source_kind"`
	SourceDigest canonical.Digest `json:"source_digest"`
	Destination  string           `json:"destination"`
	ReadOnly     bool             `json:"read_only"`
	ExpectedKind string           `json:"expected_kind"`
}

type GeneratedExecutableDeclaration struct {
	ID               string `json:"id"`
	Path             string `json:"path"`
	ExclusiveRoot    string `json:"exclusive_root"`
	ValidationPolicy string `json:"validation_policy"`
}

type ImageConfigPolicy struct {
	User        string                `json:"user"`
	WorkingDir  string                `json:"working_dir"`
	Environment []EnvironmentVariable `json:"environment"`
	Entrypoint  []string              `json:"entrypoint"`
	Command     []string              `json:"command"`
	Healthcheck string                `json:"healthcheck"`
	StopSignal  string                `json:"stop_signal"`
	Labels      []ImageLabel          `json:"labels"`
}

type ImageLabel struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
