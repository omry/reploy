//go:build windows

package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortablePythonAliasWindowsPublicationUsesPrivateRecord(t *testing.T) {
	fixture := newPortablePythonAliasFixtureV1(t)
	published, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if err != nil || !published {
		t.Fatalf("publication = %v, %v", published, err)
	}
	info, err := os.Lstat(fixture.Destination)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("published Windows staging entry mode = %v, want a regular private record", info.Mode())
	}
	target, err := readPortablePythonAliasStagedEntryV1(fixture.Destination)
	if err != nil || target != fixture.Target {
		t.Fatalf("published Windows staging target = %q, error=%v; want %q", target, err, fixture.Target)
	}
}

func TestPortablePythonAliasWindowsArchiveRejectsInPlaceRecordRewrite(t *testing.T) {
	stagingRoot := t.TempDir()
	alias := portablePythonAliasSpecV1{
		Name: "demo", Destination: "/usr/local/bin/demo",
		Target: "/opt/reploy/providers/python/application/bin/demo",
	}
	stagedPath := portablePythonAliasStagingPathV1(stagingRoot, alias)
	if err := os.MkdirAll(filepath.Dir(stagedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	claims := make(portablePythonAliasClaimsV1)
	if _, err := publishPortablePythonAliasV1(stagingRoot, stagedPath, alias.Target, "binding/demo", claims); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(stagedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stagedPath, []byte(portablePythonAliasEntryPrefixV1+"/opt/reploy/providers/python/application/bin/other"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(stagedPath)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("in-place rewrite changed staging identity: before=%v after=%v error=%v", before, after, err)
	}
	if err := writePortablePythonAliasArchiveV1(t.TempDir(), stagingRoot, []portablePythonAliasSpecV1{alias}, nil); err == nil || !strings.Contains(err.Error(), "resolves") {
		t.Fatalf("in-place rewritten staging record must fail archive validation: %v", err)
	}
}
