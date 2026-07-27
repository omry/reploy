package deploy

import reploy "github.com/omry/reploy"

var ToolVersion = reploy.Version

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

type UpdateStatus string

const (
	UpdateStatusUpdated  UpdateStatus = "updated"
	UpdateStatusUpToDate UpdateStatus = "up_to_date"
	UpdateStatusSkipped  UpdateStatus = "skipped"
	UpdateStatusRemoved  UpdateStatus = "removed"
)
