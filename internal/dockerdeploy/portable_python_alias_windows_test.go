//go:build windows

package dockerdeploy

import (
	"os"
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
