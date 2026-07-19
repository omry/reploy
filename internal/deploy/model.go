package deploy

import (
	reploy "github.com/omry/reploy"
	"github.com/omry/reploy/internal/providers"
)

var ToolVersion = reploy.Version

type Phase string

const (
	PhaseStaged    Phase = "staged"
	PhaseInstalled Phase = "installed"
)

type DeploymentManifest struct {
	SchemaVersion int                      `json:"schema_version"`
	Generator     string                   `json:"generator"`
	Files         map[string]GeneratedFile `json:"files"`
}

type GeneratedFile struct {
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
}

type DeploymentState struct {
	SchemaVersion         int                   `json:"schema_version"`
	ToolVersion           string                `json:"tool_version"`
	Target                string                `json:"target"`
	Phase                 Phase                 `json:"phase"`
	EnvironmentModel      bool                  `json:"environment_model,omitempty"`
	AppID                 string                `json:"app_id,omitempty"`
	Blueprint             PackRef               `json:"blueprint"`
	RequestedBlueprintRef string                `json:"requested_blueprint_ref,omitempty"`
	ResolvedArtifact      *ResolvedPackArtifact `json:"resolved_artifact,omitempty"`
	Runtime               *RuntimeState         `json:"runtime,omitempty"`
	Bundle                BundleState           `json:"bundle,omitempty"`
	Images                *GeneratedImagesState `json:"images,omitempty"`
	Materialization       *MaterializationState `json:"materialization,omitempty"`
	Install               *InstallState         `json:"install,omitempty"`
}

type RuntimeState struct {
	Path        string `json:"path"`
	ToolVersion string `json:"tool_version"`
	SHA256      string `json:"sha256"`
}

type ResolvedPackArtifact struct {
	Scheme        string `json:"scheme"`
	Package       string `json:"package,omitempty"`
	Version       string `json:"version,omitempty"`
	Filename      string `json:"filename,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
	Subdir        string `json:"subdir,omitempty"`
	CachePath     string `json:"cache_path,omitempty"`
	BlueprintPath string `json:"blueprint_path,omitempty"`
}

type BundleState struct {
	Roots               []ArtifactRoot `json:"roots,omitempty"`
	SelectedComponents  []string       `json:"selected_components,omitempty"`
	PreparedFingerprint string         `json:"prepared_fingerprint,omitempty"`
}

type ArtifactRoot struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Source   string `json:"source"`
}

type GeneratedImagesState struct {
	Staging  *GeneratedImageState `json:"staging,omitempty"`
	Deployed *GeneratedImageState `json:"deployed,omitempty"`
	Previous *GeneratedImageState `json:"previous,omitempty"`
}

type GeneratedImageState struct {
	Reference   string `json:"reference"`
	ImageID     string `json:"image_id,omitempty"`
	Fingerprint string `json:"fingerprint"`
	BaseDigest  string `json:"base_digest"`
}

type MaterializationState struct {
	BundleFingerprint string                                `json:"bundle_fingerprint"`
	Bundles           []providers.Bundle                    `json:"bundles"`
	Executables       map[string]providers.ExecutableOutput `json:"executables,omitempty"`
}

type InstallState struct {
	TargetDir      string                        `json:"target_dir"`
	Scope          string                        `json:"scope"`
	Service        string                        `json:"service"`
	UnitPath       string                        `json:"unit_path"`
	InstanceID     string                        `json:"instance_id"`
	ComposeProject string                        `json:"compose_project"`
	ContainerName  string                        `json:"container_name"`
	NetworkName    string                        `json:"network_name"`
	Ports          map[string]InstallPortBinding `json:"ports,omitempty"`
}

type InstallPortBinding struct {
	HostBind      string `json:"host_bind"`
	HostPort      string `json:"host_port"`
	ContainerPort string `json:"container_port"`
}
