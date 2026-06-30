package reploy

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var versionText string

var Version string

var BuildCommit string
var BuildDirty string
var BuildTimestamp string

func init() {
	if strings.TrimSpace(Version) == "" {
		Version = strings.TrimSpace(versionText)
		return
	}
	Version = strings.TrimSpace(Version)
}

func DisplayVersion() string {
	version := strings.TrimSpace(Version)
	if !isDevelopmentVersion(version) {
		return version
	}
	commit := shortCommit(strings.TrimSpace(BuildCommit))
	dirty := strings.EqualFold(strings.TrimSpace(BuildDirty), "true") || strings.TrimSpace(BuildDirty) == "1"
	timestamp := strings.TrimSpace(BuildTimestamp)
	parts := []string{}
	if commit != "" {
		if dirty {
			commit += " (dirty)"
		}
		parts = append(parts, commit)
	}
	if timestamp != "" {
		parts = append(parts, "built "+strings.ReplaceAll(timestamp, "_", " "))
	}
	if len(parts) == 0 {
		return version
	}
	return version + " [" + strings.Join(parts, ", ") + "]"
}

func isDevelopmentVersion(version string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(version)), "dev")
}

func shortCommit(commit string) string {
	if len(commit) <= 10 {
		return commit
	}
	return commit[:10]
}
