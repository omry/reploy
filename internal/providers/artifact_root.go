package providers

// ArtifactRoot is the retained bundle-command input record. It remains only
// while bundle commands are being migrated to canonical provider requests.
type ArtifactRoot struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Source   string `json:"source"`
}
