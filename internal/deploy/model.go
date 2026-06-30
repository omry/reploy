package deploy

import reploy "github.com/omry/reploy"

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
	AppID                 string                `json:"app_id,omitempty"`
	Blueprint             PackRef               `json:"blueprint"`
	RequestedBlueprintRef string                `json:"requested_blueprint_ref,omitempty"`
	ResolvedArtifact      *ResolvedPackArtifact `json:"resolved_artifact,omitempty"`
	Bundle                BundleState           `json:"bundle,omitempty"`
	Install               *InstallState         `json:"install,omitempty"`
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
	PreparedFingerprint string         `json:"prepared_fingerprint,omitempty"`
}

type ArtifactRoot struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Source   string `json:"source"`
}

type InstallState struct {
	TargetDir      string                        `json:"target_dir"`
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
