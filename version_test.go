package reploy

import "testing"

func TestDisplayVersionReleaseIgnoresBuildMetadata(t *testing.T) {
	restoreVersionState(t)
	Version = "0.4.0"
	BuildCommit = "a6479ce23d9999999999"
	BuildDirty = "true"
	BuildTimestamp = "2026-06-30 12:34:56 UTC"

	if got := DisplayVersion(); got != "0.4.0" {
		t.Fatalf("DisplayVersion() = %q", got)
	}
}

func TestDisplayVersionDevIncludesBuildMetadata(t *testing.T) {
	restoreVersionState(t)
	Version = "0.4.0.dev1"
	BuildCommit = "a6479ce23d9999999999"
	BuildTimestamp = "2026-06-30_12:34:56_UTC"

	want := "0.4.0.dev1 [a6479ce23d, built 2026-06-30 12:34:56 UTC]"
	if got := DisplayVersion(); got != want {
		t.Fatalf("DisplayVersion() = %q, want %q", got, want)
	}
}

func TestDisplayVersionDevIncludesDirtyMarker(t *testing.T) {
	restoreVersionState(t)
	Version = "0.4.0.dev1"
	BuildCommit = "a6479ce23d"
	BuildDirty = "1"
	BuildTimestamp = "2026-06-30 12:34:56 UTC"

	want := "0.4.0.dev1 [a6479ce23d (dirty), built 2026-06-30 12:34:56 UTC]"
	if got := DisplayVersion(); got != want {
		t.Fatalf("DisplayVersion() = %q, want %q", got, want)
	}
}

func TestDisplayVersionDevWithoutBuildMetadataFallsBackToVersion(t *testing.T) {
	restoreVersionState(t)
	Version = "0.4.0.dev1"

	if got := DisplayVersion(); got != "0.4.0.dev1" {
		t.Fatalf("DisplayVersion() = %q", got)
	}
}

func restoreVersionState(t *testing.T) {
	t.Helper()
	oldVersion := Version
	oldCommit := BuildCommit
	oldDirty := BuildDirty
	oldTimestamp := BuildTimestamp
	t.Cleanup(func() {
		Version = oldVersion
		BuildCommit = oldCommit
		BuildDirty = oldDirty
		BuildTimestamp = oldTimestamp
	})
}
